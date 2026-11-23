package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store/storetest"
	"github.com/bateau84/YuHaul/internal/youtube"
	"github.com/stretchr/testify/require"
)

// receivingFixture composes the real receiving path of ARS-005 section 6:
// a real SQLite store, auth service with encrypted tokens, quota ledger,
// metadata manager, executor and pipeline. Only the OAuth token endpoint and
// the Data API are local servers, and both count what reaches them.
type receivingFixture struct {
	*guardedProductionFixture
	t       *testing.T
	p       *Pipeline
	qs      *queue.Store
	server  *httptest.Server
	recvRun int64
	seq     int

	oauthStatus atomic.Int32 // status of the token endpoint
	renewals    atomic.Int32 // token endpoint hits
	listCalls   atomic.Int32 // videos.list hits
	posts       atomic.Int32 // playlistItems hits
	listStatus  atomic.Int32 // status of videos.list
}

// receivingFetcher is the bound by-ID read of cmd/server, reduced to the part
// under test: the real auth.DispatchWithQuota (and so reader R0), a local Data
// API, hold finalization, and the typed read facts the manager classifies.
type receivingFetcher struct{ f *receivingFixture }

func (b receivingFetcher) FetchMetadataBound(ctx context.Context, binding auth.AuthorizationBinding, ids []string) (metadata.FetchResult, error) {
	f := b.f
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/videos?id="+strings.Join(ids, ","), nil)
	if err != nil {
		return metadata.FetchResult{}, err
	}
	runID, _ := quota.RunFromContext(ctx)
	resp, _, hold, err := f.auth.DispatchWithQuota(ctx, binding, req, http.DefaultTransport, f.ledger, quota.OpVideosList)
	if err != nil {
		read := &youtube.ReadError{Primary: err}
		switch {
		case errors.Is(err, auth.ErrQuotaAccounting):
			read.Accounting = auth.ErrQuotaAccounting
		case auth.IsPreAdmissionAuthorizationRefusal(err):
			read.Renewal = err
		}
		return metadata.FetchResult{}, read
	}
	defer resp.Body.Close()
	category := quota.OutcomeSuccess
	var primary error
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		category = quota.OutcomeQuota
		primary = &youtube.APIError{Status: resp.StatusCode, Reason: "quotaExceeded", Reasons: []string{"quotaExceeded"}, Complete: true}
	default:
		category = quota.OutcomeTransient
		primary = &youtube.APIError{Status: resp.StatusCode, Complete: true}
	}
	finalizeErr := hold.Finalize(ctx, sql.NullInt64{Int64: runID, Valid: runID > 0}, category,
		sql.NullInt64{Int64: int64(resp.StatusCode), Valid: true})
	if finalizeErr != nil {
		if primary == nil {
			primary = errors.New("provider response not accounted")
		}
		return metadata.FetchResult{}, &youtube.ReadError{Primary: primary, Accounting: finalizeErr, Status: resp.StatusCode}
	}
	if primary != nil {
		return metadata.FetchResult{}, &youtube.ReadError{Primary: primary, Status: resp.StatusCode}
	}
	out := metadata.FetchResult{Complete: true, Returned: ids}
	for _, id := range ids {
		title, channel, description, duration := "Fetched "+id, "Fetched channel", "Fetched description", 90
		out.Details = append(out.Details, metadata.TransientMetadata{VideoID: id, Title: &title, ChannelName: &channel, Description: &description, DurationSeconds: &duration})
	}
	return out, nil
}

func newReceivingFixture(t *testing.T) *receivingFixture {
	t.Helper()
	ctx := context.Background()
	rf := &receivingFixture{t: t}
	rf.oauthStatus.Store(http.StatusOK)
	rf.listStatus.Store(http.StatusOK)
	rf.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/videos"):
			rf.listCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(int(rf.listStatus.Load()))
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			rf.posts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "inserted"})
		}
	}))
	t.Cleanup(rf.server.Close)

	oauth := roundTripTestFunc(func(req *http.Request) (*http.Response, error) {
		rf.renewals.Add(1)
		if status := int(rf.oauthStatus.Load()); status != http.StatusOK {
			return responseTest(status, `{"error":"temporarily_unavailable"}`), nil
		}
		return responseTest(http.StatusOK, `{"access_token":"fresh-access","token_type":"Bearer","expires_in":3600}`), nil
	})
	db := storetest.Open(t)
	rf.guardedProductionFixture = newGuardedProductionFixtureWithOAuth(t, db, []string{"PLreceiving"}, http.StatusOK, oauth)
	f := rf.guardedProductionFixture
	t.Cleanup(func() { _ = f.db.Close() })

	routerStore, configStore := router.NewStore(f.db), config.NewStore(f.db)
	versioner := config.NewService(routerStore, configStore)
	var err error
	rf.qs, err = queue.NewStore(f.db)
	require.NoError(t, err)
	machine := state.NewMachine(f.state, state.WithLedger(f.ledger))
	meta := metadata.NewManager(metadata.NewStore(f.db), nil, f.ledger, metadata.WithBoundFetcher(receivingFetcher{rf}), metadata.WithLogger(discardLogger()))
	rf.p = &Pipeline{
		db: f.db, stateStore: f.state, machine: machine, queueStore: rf.qs, auth: f.auth,
		versioner: versioner, configStore: configStore, router: router.NewEngine(routerStore),
		detector: duplicate.NewDetector(f.db), ledger: f.ledger, meta: meta, broadcast: NewBroadcaster(),
		logger:   discardLogger(),
		executor: NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(rf.server.URL), WithExecutorLogger(discardLogger())),
	}
	rf.p.executionBinding = &f.binding
	rf.p.router.WithRunRoutesValidator(rf.p.ValidateRunRoutes)

	// The origin run stays open (recording evidence on a terminal run is
	// refused); the receiving run is a fresh guarded run.
	cfgID, err := config.NewService(routerStore, configStore).ActiveVersionID(ctx)
	require.NoError(t, err)
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: cfgID, ActionAuthorityVersion: 1}
	tx, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	rf.recvRun, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return rf
}

// ctx is one execution of the receiving run: a fresh in-memory latch.
func (rf *receivingFixture) ctx() context.Context {
	return quota.WithRun(context.Background(), rf.recvRun)
}

// claimNew creates n videos with a pending quota-deferred item each, aged two
// days, and claims them for the receiving run. The videos have no Store A row.
func (rf *receivingFixture) claimNew(n int) []queue.RetryGroupReceipt {
	rf.t.Helper()
	ctx := context.Background()
	var receipts []queue.RetryGroupReceipt
	for i := 0; i < n; i++ {
		rf.seq++
		v, err := state.NewMachine(rf.state).DiscoverVideo(ctx, rf.runID, fmt.Sprintf("recv-%d", rf.seq), "fixture-channel")
		require.NoError(rf.t, err)
		receipts = append(receipts, rf.claimVideo(v.ID))
	}
	return receipts
}

func (rf *receivingFixture) claimVideo(videoID int64) queue.RetryGroupReceipt {
	rf.t.Helper()
	ctx := context.Background()
	item, err := rf.qs.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID: videoID, RunID: rf.runID, Reason: queue.DeferredReasonQuotaExhausted,
		Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second),
	})
	require.NoError(rf.t, err)
	var receipt queue.RetryGroupReceipt
	require.NoError(rf.t, rf.qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		receipt, _, claimed, err = rf.qs.ClaimDeferredGroupReceiptTx(ctx, tx, item, rf.recvRun, time.Now())
		if err == nil && !claimed {
			return errRetryClaimLost
		}
		return err
	}))
	return receipt
}

func (rf *receivingFixture) storedStop() string {
	rf.t.Helper()
	var s sql.NullString
	require.NoError(rf.t, rf.db.QueryRow(`SELECT dispatch_stop FROM run WHERE id=?`, rf.recvRun).Scan(&s))
	return s.String
}

func (rf *receivingFixture) ledgerRows() int {
	rf.t.Helper()
	var n int
	require.NoError(rf.t, rf.db.QueryRow(`SELECT COUNT(*) FROM quota_ledger WHERE run_id=?`, rf.recvRun).Scan(&n))
	return n
}

// requireDeferred checks one receipt's settled disposition: the run outcome is
// deferred with runReason, and the exact queue row is pending again with its
// original reason and age and no live owner other than the receiving run.
func (rf *receivingFixture) requireDeferred(receipt queue.RetryGroupReceipt, runReason string) {
	rf.t.Helper()
	var outcome, reason sql.NullString
	require.NoError(rf.t, rf.db.QueryRow(`SELECT final_outcome, deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`,
		rf.recvRun, receipt.VideoID).Scan(&outcome, &reason))
	require.Equal(rf.t, "deferred", outcome.String)
	require.Equal(rf.t, runReason, reason.String)
	require.Len(rf.t, receipt.Deferred, 1)
	var status, itemReason, created string
	require.NoError(rf.t, rf.db.QueryRow(`SELECT status, deferral_reason, created_at FROM deferred_item WHERE id=?`, receipt.Deferred[0].ID).Scan(&status, &itemReason, &created))
	require.Equal(rf.t, "pending", status)
	require.Equal(rf.t, "quota_exhausted", itemReason, "the queue row keeps its original reason")
	require.Contains(rf.t, created, time.Now().UTC().Add(-48*time.Hour).Format("2006-01-02"), "the queue row keeps its age")
}

// S4: an authorization stop (variant K). Renewal fails inside an admitted
// metadata batch; the unsent suffix, later local claims, a local-ready claim
// and a fresh execution all settle without another provider request.
func TestReceivingAuthorizationStopVariantK(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.oauthStatus.Store(http.StatusServiceUnavailable) // renewal fails: unavailable, not reconnect

	first := rf.claimNew(3)
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, first, false))

	require.Equal(t, "authorization", rf.storedStop(), "the stop commits with the first authorization_pending settlement")
	require.Equal(t, int32(1), rf.renewals.Load(), "one renewal attempt; no retry loop")
	require.Zero(t, rf.listCalls.Load(), "no videos.list request was admitted")
	require.Zero(t, rf.ledgerRows(), "no debit for an unsent request")
	for _, r := range first {
		rf.requireDeferred(r, "authorization_pending")
	}

	// A later batch is claimed and settled locally: no renewal, no request.
	later := rf.claimNew(1)
	ready := rf.claimVideo(rf.videoID) // the fixture video has a current Store A row
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, append(later, ready), false))
	require.Equal(t, int32(1), rf.renewals.Load())
	require.Zero(t, rf.listCalls.Load())
	require.Zero(t, rf.posts.Load(), "no playlist insert was sent")
	rf.requireDeferred(later[0], "authorization_pending")
	rf.requireDeferred(ready, "authorization_pending")
	var guards int
	require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=?`, rf.recvRun).Scan(&guards))
	require.Zero(t, guards, "no send guard was prepared under the stop")
	var evidence string
	require.NoError(t, rf.db.QueryRow(`SELECT outcome_reason FROM executed_action WHERE run_id=? AND video_id=?`, rf.recvRun, rf.videoID).Scan(&evidence))
	require.Equal(t, "authorization_pending", evidence)

	// A new execution (fresh latch) reads the stored stop back.
	restored := rf.claimNew(1)
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, restored, false))
	require.Equal(t, int32(1), rf.renewals.Load())
	require.Zero(t, rf.listCalls.Load())
	rf.requireDeferred(restored[0], "authorization_pending")
	got, err := rf.ledger.RunDispatchStop(rf.ctx())
	require.NoError(t, err)
	require.Equal(t, quota.StopAuthorization, got)
}

// S4: an authorization stop wins over a missing binding, and a missing binding
// without a stop is a local fault, not a reconnect.
func TestReceivingMissingBindingIsALocalFaultUnlessStopped(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.p.executionBinding = nil

	receipts := rf.claimNew(1)
	err := rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false)
	require.ErrorContains(t, err, "requires an authorization binding")
	require.Empty(t, rf.storedStop())
	require.Zero(t, rf.listCalls.Load())

	_, err = rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
	require.NoError(t, err)
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))
	rf.requireDeferred(receipts[0], "authorization_pending")
	require.Zero(t, rf.renewals.Load())
}

// S5: a provider quota refusal is read from the ledger and never stored.
func TestReceivingQuotaStopIsDerivedNotStored(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.listStatus.Store(http.StatusForbidden) // quotaExceeded

	// 51 inputs: chunk one (50) is refused by the provider, chunk two is unsent.
	first := rf.claimNew(51)
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, first, false))
	require.Equal(t, int32(1), rf.listCalls.Load(), "one admitted request, no second")
	require.Empty(t, rf.storedStop(), "the quota level is never written to the column")
	require.Equal(t, 1, rf.ledgerRows(), "the one admitted attempt")
	var quotaRows int
	require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM quota_ledger WHERE run_id=? AND outcome_category='quota'`, rf.recvRun).Scan(&quotaRows))
	require.Equal(t, 1, quotaRows)
	for _, r := range first {
		rf.requireDeferred(r, "quota_exhausted")
	}

	// R1: the claim loop's own check sees the stop and leaves.
	got, err := rf.ledger.RunDispatchStop(rf.ctx())
	require.NoError(t, err)
	require.Equal(t, quota.StopQuota, got)
	ok, _, err := rf.ledger.CanAfford(rf.ctx(), 1)
	require.NoError(t, err)
	require.False(t, ok)

	// Same run, same bucket, fresh execution: still no request.
	rf.listStatus.Store(http.StatusOK)
	again := rf.claimNew(1)
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, again, false))
	require.Equal(t, int32(1), rf.listCalls.Load())
	rf.requireDeferred(again[0], "quota_exhausted")

	// After the reset the run is admitted again.
	_, err = rf.db.Exec(`UPDATE quota_ledger SET date = date(date, '-1 day') WHERE run_id=?`, rf.recvRun)
	require.NoError(t, err)
	after := rf.claimNew(1)
	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, after, false))
	require.Equal(t, int32(2), rf.listCalls.Load(), "admitted again after the reset")
	require.Empty(t, rf.storedStop())
}

// S6: an accounting fault. With a stop read error, and then with a failure of
// the stored write itself, the execution makes no further provider request and
// the error propagates.
func TestReceivingAccountingFaultFailsClosed(t *testing.T) {
	t.Run("stop read error", func(t *testing.T) {
		rf := newReceivingFixture(t)
		receipts := rf.claimNew(2)
		// The read of the stored column fails (and so does the best-effort write).
		_, err := rf.db.Exec(`ALTER TABLE run RENAME COLUMN dispatch_stop TO dispatch_stop_broken`)
		require.NoError(t, err)
		ctx := rf.ctx()
		err = rf.p.processDeferredItems(ctx, rf.recvRun, receipts, false)
		require.ErrorIs(t, err, auth.ErrQuotaAccounting)
		var stopping *runStoppingError
		require.ErrorAs(t, err, &stopping)
		require.Zero(t, rf.listCalls.Load())
		require.Zero(t, rf.renewals.Load())
		got, _ := rf.ledger.RunDispatchStop(ctx)
		require.Equal(t, quota.StopAccountingOrIntegrity, got, "the latch holds the stop for this execution")
	})

	// A ledger fault after an admitted request: accounting fails.
	accountingFault := func(t *testing.T, failStoredWrite bool) (*receivingFixture, context.Context, []queue.RetryGroupReceipt) {
		rf := newReceivingFixture(t)
		receipts := rf.claimNew(2)
		_, err := rf.db.Exec(`CREATE TRIGGER reject_accounting BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(ABORT,'ledger down'); END`)
		require.NoError(t, err)
		if failStoredWrite {
			_, err = rf.db.Exec(`CREATE TRIGGER reject_stop_write BEFORE UPDATE OF dispatch_stop ON run BEGIN SELECT RAISE(ABORT,'run row locked'); END`)
			require.NoError(t, err)
		}
		ctx := rf.ctx()
		err = rf.p.processDeferredItems(ctx, rf.recvRun, receipts, false)
		require.ErrorIs(t, err, auth.ErrQuotaAccounting)
		require.Equal(t, int32(1), rf.listCalls.Load(), "the one admitted request")
		return rf, ctx, receipts
	}

	t.Run("stored write fails", func(t *testing.T) {
		rf, ctx, receipts := accountingFault(t, true)
		require.Empty(t, rf.storedStop())
		// Same execution: the latch alone stops everything, with no more requests.
		err := rf.p.processDeferredItems(ctx, rf.recvRun, receipts, false)
		require.ErrorIs(t, err, auth.ErrQuotaAccounting)
		require.Equal(t, int32(1), rf.listCalls.Load())
		got, _ := rf.ledger.RunDispatchStop(ctx)
		require.Equal(t, quota.StopAccountingOrIntegrity, got)
	})

	t.Run("stored write works and a later resume reads it", func(t *testing.T) {
		rf, _, receipts := accountingFault(t, false)
		require.Equal(t, "accounting_or_integrity", rf.storedStop())
		_, err := rf.db.Exec(`DROP TRIGGER reject_accounting`)
		require.NoError(t, err)
		err = rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false)
		require.ErrorIs(t, err, auth.ErrQuotaAccounting, "a fresh execution reads the stored stop")
		require.Equal(t, int32(1), rf.listCalls.Load(), "and makes no request")
		// R2: the claim loop claims nothing under it.
		item, err := rf.qs.CreateDeferredItem(context.Background(), &queue.DeferredItem{VideoID: rf.videoID, RunID: rf.runID,
			Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
		err = rf.qs.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
			_, _, _, claimErr := rf.qs.ClaimDeferredGroupReceiptTx(context.Background(), tx, item, rf.recvRun, time.Now())
			return claimErr
		})
		require.ErrorIs(t, err, queue.ErrRunDispatchStopped)
	})
}

// A quota preflight that cannot be read (here an unreadable daily limit, while
// the stop read still works) is writer W3 as well, not an ordinary failure:
// no request, the stop is latched and stored, and the error stops the run.
func TestReceivingPreflightReadErrorFailsClosed(t *testing.T) {
	rf := newReceivingFixture(t)
	receipts := rf.claimNew(2)
	_, err := rf.db.Exec(`INSERT OR REPLACE INTO quota_settings (key, value) VALUES ('daily_limit', 'not-a-number')`)
	require.NoError(t, err)
	ctx := rf.ctx()

	err = rf.p.processDeferredItems(ctx, rf.recvRun, receipts, false)

	require.ErrorIs(t, err, auth.ErrQuotaAccounting)
	var stopping *runStoppingError
	require.ErrorAs(t, err, &stopping)
	require.Zero(t, rf.listCalls.Load())
	require.Zero(t, rf.renewals.Load())
	require.Equal(t, "accounting_or_integrity", rf.storedStop())
	got, _ := rf.ledger.RunDispatchStop(ctx)
	require.Equal(t, quota.StopAccountingOrIntegrity, got)
}

// lifecycleStub keeps the real metadata manager and replaces only the sweep
// result, so the run owner's handling of that result is what is under test.
type lifecycleStub struct {
	*metadata.Manager
	err error
}

func (s lifecycleStub) RunLifecycle(context.Context) error { return s.err }

// The metadata lifecycle runs inside the run. When it reports an accounting
// fault, the run makes no further provider request, stores the stop, ends
// failed through the retention path with a closed diagnostic, and never
// reaches discovery. Any other lifecycle error stays a warning.
func TestRunLifecycleAccountingFaultFailsTheRunClosed(t *testing.T) {
	sweepFault := fmt.Errorf("metadata: fetch refresh batch: %w", &youtube.ReadError{Accounting: errors.New("quota: record attempt: SQL text must not leak")})
	run := func(t *testing.T, lifecycleErr error) (*receivingFixture, state.Run) {
		rf := newReceivingFixture(t)
		rf.p.meta = lifecycleStub{Manager: rf.p.meta.(*metadata.Manager), err: lifecycleErr}
		rf.p.Execute(context.Background(), rf.recvRun)
		got, err := rf.state.GetRun(context.Background(), rf.recvRun)
		require.NoError(t, err)
		return rf, *got
	}

	t.Run("accounting fault", func(t *testing.T) {
		rf, got := run(t, sweepFault)
		require.Equal(t, state.RunStatusFailed, got.Status)
		require.NotNil(t, got.InterruptReason)
		require.Equal(t, "diag:v1 phase=unknown operation=account_quota code=unknown_error", *got.InterruptReason)
		require.Equal(t, "accounting_or_integrity", rf.storedStop())
		require.Zero(t, rf.listCalls.Load())
		require.Zero(t, rf.posts.Load())
		require.Zero(t, rf.renewals.Load())
	})

	t.Run("other lifecycle error", func(t *testing.T) {
		rf, got := run(t, errors.New("metadata: optimize search index"))
		if got.InterruptReason != nil {
			require.NotContains(t, *got.InterruptReason, "account_quota")
		}
		require.Empty(t, rf.storedStop(), "a non-accounting sweep error raises no stop")
	})
}

// execStub stands in for the executor at its own seam and returns a fixed
// result, so the retry path's handling of that result is what is under test.
type execStub struct{ err error }

func (e execStub) Execute(context.Context, int64, int64, []RouteMatch) (state.VideoFinalOutcome, error) {
	return state.FinalOutcomeFailed, e.err
}

// A run-stopping error from the executor (writer W3) ends the retry run
// through its retention sweep. It is not one video's failure: the claimed row
// is not finished as failed and no Errors entry is written for it.
func TestRetryVideoPropagatesARunStoppingError(t *testing.T) {
	rf := newReceivingFixture(t)
	receipts := rf.claimNew(1)
	var text string
	require.NoError(t, rf.db.QueryRow(`SELECT video_id FROM video WHERE id=?`, receipts[0].VideoID).Scan(&text))
	rf.p.meta = &stubReadiness{
		statuses: map[string]metadata.FetchStatus{text: metadata.FetchCurrent},
		rows:     map[string]*metadata.TransientMetadata{text: {VideoID: text, Title: new("t"), ChannelName: new("c"), Description: new("d"), DurationSeconds: new(60)}},
	}
	rf.p.executor = execStub{err: &runStoppingError{cause: auth.ErrQuotaAccounting}}

	err := rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false)

	var stopping *runStoppingError
	require.ErrorAs(t, err, &stopping)
	require.ErrorIs(t, err, auth.ErrQuotaAccounting)
	var sinks int
	require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM error_entry WHERE video_id=?`, receipts[0].VideoID).Scan(&sinks))
	require.Zero(t, sinks, "no Errors entry for a run-level stop")
	var status string
	require.NoError(t, rf.db.QueryRow(`SELECT status FROM deferred_item WHERE id=?`, receipts[0].Deferred[0].ID).Scan(&status))
	require.Equal(t, "retrying", status, "the claim stays live for the run-level retention sweep")
	require.Zero(t, rf.posts.Load())
}

// S7: with a stale in-process view, R2 refuses a claim under a stored
// accounting stop and R7 refuses ClaimPlaylistSend under any stored value, each
// inside its writer transaction with no guard or membership write.
func TestReceivingPerCallReadersRefuseInsideTheWriter(t *testing.T) {
	t.Run("R2 claim", func(t *testing.T) {
		rf := newReceivingFixture(t)
		rf.p.ledger = nil // no ledger view: only the stored column can refuse
		v, err := state.NewMachine(rf.state).DiscoverVideo(context.Background(), rf.runID, "r2-video", "fixture-channel")
		require.NoError(t, err)
		item, err := rf.qs.CreateDeferredItem(context.Background(), &queue.DeferredItem{VideoID: v.ID, RunID: rf.runID,
			Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)

		claim := func() error {
			return rf.qs.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
				_, _, _, err := rf.qs.ClaimDeferredGroupReceiptTx(context.Background(), tx, item, rf.recvRun, time.Now())
				return err
			})
		}
		// An authorization stop still claims (local-only, variant K).
		_, err = rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
		require.NoError(t, err)
		require.NoError(t, claim())
		_, err = rf.db.Exec(`UPDATE deferred_item SET status='pending', retry_run_id=NULL WHERE id=?`, item)
		require.NoError(t, err)

		_, err = rf.db.Exec(`UPDATE run SET dispatch_stop='accounting_or_integrity' WHERE id=?`, rf.recvRun)
		require.NoError(t, err)
		require.ErrorIs(t, claim(), queue.ErrRunDispatchStopped)
		var status string
		var owner sql.NullInt64
		require.NoError(t, rf.db.QueryRow(`SELECT status, retry_run_id FROM deferred_item WHERE id=?`, item).Scan(&status, &owner))
		require.Equal(t, "pending", status)
		require.False(t, owner.Valid, "the refused claim changed nothing")
	})

	for _, level := range []string{"authorization", "accounting_or_integrity"} {
		t.Run("R7 ClaimPlaylistSend "+level, func(t *testing.T) {
			rf := newReceivingFixture(t)
			_, err := rf.db.Exec(`UPDATE run SET dispatch_stop=? WHERE id=?`, level, rf.runID)
			require.NoError(t, err)
			identity, err := auth.BindingIdentity(rf.binding)
			require.NoError(t, err)
			_, _, err = rf.state.ClaimPlaylistSend(context.Background(), state.PlaylistSendClaim{
				RunID: rf.runID, VideoID: rf.videoID, RouteDecisionID: rf.decisionID,
				TargetPlaylistID: rf.target, ActionType: "playlist_insert", AuthorizationID: identity,
			}, time.Now().UTC())
			require.ErrorIs(t, err, state.ErrPlaylistSendDispatchStopped)
			var guards int
			require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&guards))
			require.Zero(t, guards, "no guard was written")
			var members int
			require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.runID, rf.videoID).Scan(&members))
			require.Zero(t, rf.posts.Load())
			_ = members
		})
	}
}

// S8: simulations never write the stop and read none; a dry run can still
// derive a quota stop from the ledger.
func TestSimulationsNeverWriteTheDispatchStop(t *testing.T) {
	rf := newReceivingFixture(t)
	ctx := context.Background()
	for _, typ := range []state.RunType{state.RunTypeDryRun, state.RunTypeReplay, state.RunTypeBacktest} {
		t.Run(string(typ), func(t *testing.T) {
			id, err := rf.state.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: typ, Status: state.RunStatusRunning})
			require.NoError(t, err)
			for _, level := range []quota.DispatchStop{quota.StopAuthorization, quota.StopAccountingOrIntegrity} {
				require.NoError(t, rf.qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
					return rf.state.RaiseRunDispatchStopTx(ctx, tx, id, level)
				}), "a refused write is not an error")
			}
			var stored sql.NullString
			require.NoError(t, rf.db.QueryRow(`SELECT dispatch_stop FROM run WHERE id=?`, id).Scan(&stored))
			require.False(t, stored.Valid, "a simulation never stores a stop")
			sctx := quota.WithRun(ctx, id)
			got, err := rf.ledger.RunDispatchStop(sctx)
			require.NoError(t, err)
			require.Equal(t, quota.StopNone, got)

			// The quota level is still derived for it.
			h, err := rf.ledger.Acquire(sctx, quota.OpVideosList)
			require.NoError(t, err)
			require.NoError(t, h.Admit(sctx))
			require.NoError(t, h.Finalize(sctx, sql.NullInt64{Int64: id, Valid: true}, quota.OutcomeQuota, sql.NullInt64{Int64: 403, Valid: true}))
			got, err = rf.ledger.RunDispatchStop(sctx)
			require.NoError(t, err)
			require.Equal(t, quota.StopQuota, got)
		})
	}
	// Only a production run stores; the writers' own guards (production runs
	// only) are what keep Replay and Backtest from reaching them.
	require.NoError(t, rf.qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		return rf.state.RaiseRunDispatchStopTx(ctx, tx, rf.recvRun, quota.StopAuthorization)
	}))
	require.Equal(t, "authorization", rf.storedStop())
	require.Error(t, rf.qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		return rf.state.RaiseRunDispatchStopTx(ctx, tx, rf.recvRun, quota.StopQuota)
	}), "quota is derived, never storable")
}

// stubReadiness is a metadata seam whose readiness answers are scripted, for
// the dispositions of the section 6.2 table.
type stubReadiness struct {
	statuses map[string]metadata.FetchStatus
	rows     map[string]*metadata.TransientMetadata
}

func (s *stubReadiness) GetDisplayMetadata(_ context.Context, id string) (*metadata.TransientMetadata, error) {
	if m, ok := s.rows[id]; ok {
		return m, nil
	}
	return nil, &metadata.MetadataExpiredError{VideoID: id}
}
func (s *stubReadiness) RunLifecycle(context.Context) error { return nil }
func (s *stubReadiness) FetchForEvaluation(_ context.Context, _ auth.AuthorizationBinding, reqs []metadata.InputRequirement) (metadata.FetchOutcome, error) {
	out := metadata.FetchOutcome{Statuses: map[string]metadata.FetchStatus{}}
	for _, r := range reqs {
		out.Statuses[r.VideoID] = s.statuses[r.VideoID]
	}
	return out, nil
}

// The section 6.2 table: each status settles the exact receipt as specified,
// without evaluating, sending or debiting.
func TestReceivingStatusDispositions(t *testing.T) {
	cases := []struct {
		status    metadata.FetchStatus
		outcome   string // run outcome, "" when none is written
		runReason string
		released  bool // queue row returned to pending with no owner, unchanged
		failed    bool
	}{
		{status: metadata.FetchQuotaRefused, outcome: "deferred", runReason: "quota_exhausted"},
		{status: metadata.FetchRateLimited, outcome: "deferred", runReason: "api_rate_limited"},
		{status: metadata.FetchAuthorizationPending, outcome: "deferred", runReason: "authorization_pending"},
		{status: metadata.FetchFailed, released: true},
		{status: metadata.FetchUnusable, released: true},
		{status: metadata.FetchAbsent, outcome: "failed", failed: true},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			rf := newReceivingFixture(t)
			receipts := rf.claimNew(1)
			var text string
			require.NoError(t, rf.db.QueryRow(`SELECT video_id FROM video WHERE id=?`, receipts[0].VideoID).Scan(&text))
			rf.p.meta = &stubReadiness{statuses: map[string]metadata.FetchStatus{text: c.status}}

			require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))

			var outcome, reason sql.NullString
			err := rf.db.QueryRow(`SELECT final_outcome, deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, receipts[0].VideoID).Scan(&outcome, &reason)
			if c.outcome == "" {
				require.True(t, errors.Is(err, sql.ErrNoRows) || outcome.String == "" || outcome.String == "not_yet_evaluated", "no outcome is written: %v %v", err, outcome)
			} else {
				require.NoError(t, err)
				require.Equal(t, c.outcome, outcome.String)
				require.Equal(t, c.runReason, reason.String)
			}
			var status string
			var owner sql.NullInt64
			require.NoError(t, rf.db.QueryRow(`SELECT status, retry_run_id FROM deferred_item WHERE id=?`, receipts[0].Deferred[0].ID).Scan(&status, &owner))
			switch {
			case c.released:
				require.Equal(t, "pending", status)
				require.False(t, owner.Valid, "released unchanged: no owner")
			case c.failed:
				require.Equal(t, "completed", status, "an ordinary retry failure resolves the claimed row")
				var sinks int
				require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM error_entry WHERE video_id=? AND status='active'`, receipts[0].VideoID).Scan(&sinks))
				require.Equal(t, 1, sinks, "the failure is retained in Errors")
			default:
				require.Equal(t, "pending", status)
			}
			require.Zero(t, rf.posts.Load())
			require.Zero(t, rf.ledgerRows())
			require.Equal(t, c.status == metadata.FetchAuthorizationPending, rf.storedStop() == "authorization")
		})
	}
}

// The readiness backstop: an input that was current at fetch time but is gone
// when evaluation reads it is never replaced by an empty value. The effective
// stop is classified first.
func TestReceivingBackstopReleasesUnlessStopped(t *testing.T) {
	for _, stored := range []string{"", "authorization"} {
		t.Run("stored="+stored, func(t *testing.T) {
			rf := newReceivingFixture(t)
			receipts := rf.claimNew(1)
			var text string
			require.NoError(t, rf.db.QueryRow(`SELECT video_id FROM video WHERE id=?`, receipts[0].VideoID).Scan(&text))
			// Current at fetch time, expired by evaluation time (no row).
			rf.p.meta = &stubReadiness{statuses: map[string]metadata.FetchStatus{text: metadata.FetchCurrent}}
			if stored != "" {
				_, err := rf.db.Exec(`UPDATE run SET dispatch_stop=? WHERE id=?`, stored, rf.recvRun)
				require.NoError(t, err)
			}
			require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))
			require.Zero(t, rf.posts.Load(), "nothing was evaluated into a send")
			var status string
			require.NoError(t, rf.db.QueryRow(`SELECT status FROM deferred_item WHERE id=?`, receipts[0].Deferred[0].ID).Scan(&status))
			require.Equal(t, "pending", status)
			var reason sql.NullString
			err := rf.db.QueryRow(`SELECT deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, receipts[0].VideoID).Scan(&reason)
			if stored == "authorization" {
				require.NoError(t, err)
				require.Equal(t, "authorization_pending", reason.String, "an authorization stop wins over a generic input loss")
			} else {
				require.Empty(t, reason.String, "a generic input loss releases unchanged")
			}
		})
	}
}
