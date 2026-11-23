package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/youtube"
	"github.com/stretchr/testify/require"
)

// F8 (ARS-005 section 6.2, `failed` row): a transient metadata failure that
// releases claims unchanged reports one safe routing/load_metadata diagnostic
// per failure or suspension, with a count, and nothing else.

const receivingFailureMessage = "receiving metadata read failed; inputs released unchanged"

// capturedLogs collects the records a pipeline logger writes.
type capturedLogs struct{ buf bytes.Buffer }

func (c *capturedLogs) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&c.buf, nil))
}

func (c *capturedLogs) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		out = append(out, rec)
	}
	return out
}

// failures are the records of the F8 diagnostic.
func (c *capturedLogs) failures(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range c.records(t) {
		if rec["msg"] == receivingFailureMessage {
			out = append(out, rec)
		}
	}
	return out
}

// failingFetcher is a bound read that always fails with err.
type failingFetcher struct{ err error }

func (f failingFetcher) FetchMetadataBound(context.Context, auth.AuthorizationBinding, []string) (metadata.FetchResult, error) {
	return metadata.FetchResult{}, f.err
}

// A 503 on the first chunk suspends fetching for the run. Three batches follow
// (500, 500, 100) and every video is released unchanged. The diagnostic is
// emitted once, for the failing pass, and again by nothing: later batches
// meet the suspension without a new failure.
func TestReceivingTransientFailureReportsOnceAcrossBatches(t *testing.T) {
	var logs capturedLogs
	rf := newReceivingFixture(t)
	rf.compose(logs.logger())
	rf.initiatedBy(state.InitiatedByRunNow)
	ids := rf.seedPending(1100)
	rf.listStatus.Store(http.StatusServiceUnavailable)

	first := rf.claimFirstBatch(rf.recvRun)
	require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, first, false))

	require.Equal(t, int32(1), rf.listCalls.Load(), "one failed request; the suspension holds for the rest of the run")
	require.Equal(t, 1100, rf.memberCount(rf.recvRun), "every video was claimed and settled")

	got := logs.failures(t)
	require.Len(t, got, 1, "one diagnostic for the failure and its suspension, not one per batch or per video")
	require.Equal(t, "WARN", got[0]["level"])
	require.EqualValues(t, rf.recvRun, got[0]["run_id"])
	require.EqualValues(t, 500, got[0]["inputs"], "the count of the pass that met the failure")
	require.Equal(t, "diag:v1 phase=routing operation=load_metadata code=provider_error status=503", got[0]["diagnostic"])

	// Claimed-work behavior is the existing behavior: released unchanged, no
	// outcome, nothing in Errors.
	require.Equal(t, 1100, rf.untouched(ids))
	var errorsEntries, outcomes int
	require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM error_entry`).Scan(&errorsEntries))
	require.Zero(t, errorsEntries, "a transient release creates no Errors entry")
	require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome<>'not_yet_evaluated'`, rf.recvRun).Scan(&outcomes))
	require.Zero(t, outcomes, "a transient release records no outcome")
	require.Equal(t, 1, rf.ledgerRows(), "only the one failed request was debited")
	require.NotContains(t, logs.buf.String(), "batch-", "no per-video text reaches the log")
}

// The diagnostic carries the closed vocabulary only. Text the provider or the
// transport put into an error never reaches it.
func TestReceivingTransientFailureDiagnosticIsSafe(t *testing.T) {
	const secret = "token=SECRET-provider-text"
	cases := map[string]struct {
		err  error
		want string
	}{
		"unclassified text": {
			err:  errors.New(secret),
			want: "diag:v1 phase=routing operation=load_metadata code=unknown_error",
		},
		"provider reason": {
			err:  &youtube.ReadError{Primary: &youtube.APIError{Status: 500, Reason: "backendError"}, Status: 500},
			want: "diag:v1 phase=routing operation=load_metadata code=provider_error status=500 reason=backendError",
		},
		"wrapped text": {
			err:  fmt.Errorf("metadata fetcher: %s: %w", secret, &youtube.APIError{Status: 502}),
			want: "diag:v1 phase=routing operation=load_metadata code=provider_error status=502",
		},
		"transport": {
			err:  &youtube.ReadError{Primary: &youtube.ReadTransportError{}},
			want: "diag:v1 phase=routing operation=load_metadata code=unknown_error",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var logs capturedLogs
			rf := newReceivingFixture(t)
			rf.compose(logs.logger())
			rf.p.meta = metadata.NewManager(metadata.NewStore(rf.db), nil, rf.ledger,
				metadata.WithBoundFetcher(failingFetcher{c.err}), metadata.WithLogger(discardLogger()))
			receipts := rf.claimNew(3)

			require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))

			got := logs.failures(t)
			require.Len(t, got, 1)
			require.Equal(t, c.want, got[0]["diagnostic"])
			require.EqualValues(t, 3, got[0]["inputs"])
			require.NotContains(t, logs.buf.String(), "SECRET", "no raw provider or transport text in any record")
			for _, r := range receipts {
				var status string
				require.NoError(t, rf.db.QueryRow(`SELECT status FROM deferred_item WHERE id=?`, r.Deferred[0].ID).Scan(&status))
				require.Equal(t, "pending", status, "released unchanged")
			}
		})
	}
}

// The diagnostic belongs to a release for a failed read only. Every other
// disposition keeps its existing signals, and a pass that merely meets a
// suspension already reported (no new read failure) adds none.
func TestReceivingOtherDispositionsEmitNoReleaseDiagnostic(t *testing.T) {
	t.Run("quota refusal", func(t *testing.T) { requireNoReleaseDiagnostic(t, http.StatusForbidden) })
	t.Run("rate limit", func(t *testing.T) { requireNoReleaseDiagnostic(t, http.StatusTooManyRequests) })
	for _, status := range []metadata.FetchStatus{metadata.FetchUnusable, metadata.FetchFailed} {
		t.Run("scripted "+string(status)+" without a read failure", func(t *testing.T) {
			var logs capturedLogs
			rf := newReceivingFixture(t)
			rf.compose(logs.logger())
			rf.p.meta = &batchProbe{status: status}
			receipts := rf.claimNew(2)
			require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))
			require.Empty(t, logs.failures(t))
		})
	}
}

func requireNoReleaseDiagnostic(t *testing.T, listStatus int) {
	t.Helper()
	var logs capturedLogs
	rf := newReceivingFixture(t)
	rf.compose(logs.logger())
	rf.listStatus.Store(int32(listStatus))
	receipts := rf.claimNew(2)
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))
	require.Equal(t, int32(1), rf.listCalls.Load())
	require.Empty(t, logs.failures(t), "a deferral is not a release for a failed read")
	require.NotContains(t, logs.buf.String(), "load_metadata")
}
