package metadata_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/youtube"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeBound struct {
	calls [][]string
	do    func(call int, ids []string) (metadata.FetchResult, error)
}

func (f *fakeBound) FetchMetadataBound(_ context.Context, _ auth.AuthorizationBinding, ids []string) (metadata.FetchResult, error) {
	f.calls = append(f.calls, append([]string(nil), ids...))
	return f.do(len(f.calls)-1, ids)
}

func pv[T any](v T) *T { return &v }

var evalNow = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func newFetchManager(t *testing.T, bound metadata.BoundFetcher, limit int) (*metadata.Manager, *metadata.Store, *quota.Ledger) {
	t.Helper()
	ms := openTestDB(t)
	ledger := quota.New(ms.DB(), quota.WithDefaultLimit(limit))
	return metadata.NewManager(ms, nil, ledger, metadata.WithBoundFetcher(bound), metadata.WithNow(func() time.Time { return evalNow })), ms, ledger
}

func allInputs(ids ...string) []metadata.InputRequirement {
	out := make([]metadata.InputRequirement, len(ids))
	for i, id := range ids {
		out[i] = metadata.InputRequirement{VideoID: id, Title: true, ChannelName: true, Description: true, Duration: true}
	}
	return out
}

func detail(id string) metadata.TransientMetadata {
	return metadata.TransientMetadata{VideoID: id, Title: pv("t-" + id), ChannelName: pv("c"), Description: pv(""), DurationSeconds: pv(0), PublishedAt: pv(evalNow)}
}

func countRows(t *testing.T, ms *metadata.Store) int {
	t.Helper()
	var n int
	require.NoError(t, ms.DB().QueryRow(`SELECT count(*) FROM transient_metadata`).Scan(&n))
	return n
}

func TestFetchForEvaluationCurrentRowsMakeNoCall(t *testing.T) {
	bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
		t.Fatal("unexpected call")
		return metadata.FetchResult{}, nil
	}}
	m, ms, _ := newFetchManager(t, bound, 1000)
	// A real empty description and zero duration are present values.
	require.NoError(t, ms.Upsert(context.Background(), metadata.TransientMetadata{VideoID: "ok", Title: pv("T"), ChannelName: pv("C"), Description: pv(""), DurationSeconds: pv(0), FetchedAt: evalNow.Add(-time.Hour), ExpiresAt: evalNow.Add(time.Hour), IsActive: true}))
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("ok", "ok"))
	require.NoError(t, err)
	assert.Equal(t, map[string]metadata.FetchStatus{"ok": metadata.FetchCurrent}, out.Statuses)
	assert.Zero(t, out.Attempts)
	assert.Nil(t, out.Read)
}

func TestFetchForEvaluationNotCurrentRowsAreFetched(t *testing.T) {
	bound := &fakeBound{do: func(_ int, ids []string) (metadata.FetchResult, error) {
		res := metadata.FetchResult{Complete: true, Returned: ids}
		for _, id := range ids {
			res.Details = append(res.Details, detail(id))
		}
		return res, nil
	}}
	m, ms, _ := newFetchManager(t, bound, 1000)
	good := func(id string) metadata.TransientMetadata {
		return metadata.TransientMetadata{VideoID: id, Title: pv("T"), ChannelName: pv("C"), Description: pv("D"), DurationSeconds: pv(5), FetchedAt: evalNow.Add(-time.Hour), ExpiresAt: evalNow.Add(time.Hour), IsActive: true}
	}
	expired := good("expired")
	expired.ExpiresAt = evalNow
	inactive := good("inactive")
	inactive.IsActive = false
	noDescription := good("nodesc")
	noDescription.Description = nil
	for _, row := range []metadata.TransientMetadata{expired, inactive, noDescription} {
		require.NoError(t, ms.Upsert(context.Background(), row))
	}
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("expired", "inactive", "nodesc", "missing"))
	require.NoError(t, err)
	require.Len(t, bound.calls, 1)
	assert.Equal(t, []string{"expired", "inactive", "nodesc", "missing"}, bound.calls[0])
	for _, id := range bound.calls[0] {
		assert.Equal(t, metadata.FetchFetched, out.Statuses[id], id)
	}
	assert.Equal(t, 4, out.Saved)
	// Evicted/inactive and never-held rows are current again afterwards.
	again, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("expired", "inactive", "nodesc", "missing"))
	require.NoError(t, err)
	assert.Len(t, bound.calls, 1)
	assert.Equal(t, metadata.FetchCurrent, again.Statuses["missing"])
}

func TestFetchForEvaluationBatchesFiftyUniqueIDs(t *testing.T) {
	bound := &fakeBound{do: func(_ int, ids []string) (metadata.FetchResult, error) {
		res := metadata.FetchResult{Complete: true, Returned: ids}
		for _, id := range ids {
			res.Details = append(res.Details, detail(id))
		}
		return res, nil
	}}
	m, ms, _ := newFetchManager(t, bound, 1000)
	var reqs []metadata.InputRequirement
	for i := 0; i < 120; i++ {
		reqs = append(reqs, allInputs(fmt.Sprintf("v%03d", i))...)
	}
	reqs = append(reqs, allInputs("v000", "v119")...) // duplicates merge
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, reqs)
	require.NoError(t, err)
	require.Len(t, bound.calls, 3)
	assert.Len(t, bound.calls[0], 50)
	assert.Len(t, bound.calls[1], 50)
	assert.Len(t, bound.calls[2], 20)
	assert.Equal(t, 3, out.Attempts)
	assert.Len(t, out.Statuses, 120)
	assert.Equal(t, 120, countRows(t, ms))
}

func TestFetchForEvaluationDistinguishesAbsentUnusableFetched(t *testing.T) {
	for _, tc := range []struct {
		name     string
		complete bool
		want     map[string]metadata.FetchStatus
	}{
		{"complete", true, map[string]metadata.FetchStatus{"good": metadata.FetchFetched, "partial": metadata.FetchUnusable, "nodetail": metadata.FetchUnusable, "gone": metadata.FetchAbsent}},
		{"incomplete", false, map[string]metadata.FetchStatus{"good": metadata.FetchFetched, "partial": metadata.FetchUnusable, "nodetail": metadata.FetchUnusable, "gone": metadata.FetchUnusable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			partial := detail("partial")
			partial.DurationSeconds = nil // an invalid duration is NULL, never a default
			partial.Description = nil
			bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
				return metadata.FetchResult{Complete: tc.complete, Returned: []string{"good", "partial", "nodetail"}, Details: []metadata.TransientMetadata{detail("good"), partial}}, nil
			}}
			m, ms, _ := newFetchManager(t, bound, 1000)
			out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("good", "partial", "nodetail", "gone"))
			require.NoError(t, err)
			assert.Equal(t, tc.want, out.Statuses)
			assert.Nil(t, out.Read)
			// True presence: the empty description is '' and the invalid inputs are NULL.
			var desc, dur any
			require.NoError(t, ms.DB().QueryRow(`SELECT description, duration_seconds FROM transient_metadata WHERE video_id='good'`).Scan(&desc, &dur))
			assert.Equal(t, "", desc)
			assert.EqualValues(t, 0, dur)
			require.NoError(t, ms.DB().QueryRow(`SELECT description, duration_seconds FROM transient_metadata WHERE video_id='partial'`).Scan(&desc, &dur))
			assert.Nil(t, desc)
			assert.Nil(t, dur)
			assert.Equal(t, 2, countRows(t, ms), "only returned details are written")
		})
	}
}

func TestFetchForEvaluationOnlyRequiredInputsGateReadiness(t *testing.T) {
	partial := detail("v")
	partial.Description, partial.DurationSeconds = nil, nil
	bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
		return metadata.FetchResult{Complete: true, Returned: []string{"v"}, Details: []metadata.TransientMetadata{partial}}, nil
	}}
	m, _, _ := newFetchManager(t, bound, 1000)
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, []metadata.InputRequirement{{VideoID: "v", Title: true}})
	require.NoError(t, err)
	assert.Equal(t, metadata.FetchFetched, out.Statuses["v"])
}

func TestFetchForEvaluationKnownRefusalMakesNoCallAndWritesNothing(t *testing.T) {
	bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
		t.Fatal("unexpected call")
		return metadata.FetchResult{}, nil
	}}
	m, ms, ledger := newFetchManager(t, bound, 1)
	hold, err := ledger.Acquire(context.Background(), quota.OpVideosList) // the only unit is held
	require.NoError(t, err)
	require.NotNil(t, hold)
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("a", "b"))
	require.NoError(t, err)
	assert.Equal(t, map[string]metadata.FetchStatus{"a": metadata.FetchQuotaRefused, "b": metadata.FetchQuotaRefused}, out.Statuses)
	require.NotNil(t, out.Read)
	assert.True(t, out.Read.QuotaRefused)
	assert.ErrorIs(t, out.Read.Err, auth.ErrQuotaRefused)
	assert.Zero(t, out.Attempts)
	assert.Zero(t, countRows(t, ms))
}

func TestFetchForEvaluationReadFailureKeepsTypedFactsAndStopsFetching(t *testing.T) {
	renewal := errors.New("renewal unavailable")
	acct := errors.New("record failed")
	for _, tc := range []struct {
		name  string
		err   error
		want  metadata.FetchStatus
		check func(t *testing.T, f *metadata.ReadFacts)
	}{
		{"quota", &youtube.ReadError{Primary: &youtube.APIError{Status: 403, Reason: "quotaExceeded"}, Status: 403}, metadata.FetchQuotaRefused, func(t *testing.T, f *metadata.ReadFacts) { assert.True(t, f.QuotaRefused) }},
		{"daily", fmt.Errorf("w: %w", &youtube.APIError{Status: 403, Reason: "dailyLimitExceeded"}), metadata.FetchQuotaRefused, nil},
		{"rate", &youtube.APIError{Status: 429}, metadata.FetchRateLimited, nil},
		{"rate reason", &youtube.APIError{Status: 403, Reason: "userRateLimitExceeded"}, metadata.FetchRateLimited, nil},
		{"unauthorized", &youtube.APIError{Status: 401}, metadata.FetchAuthorizationPending, nil},
		{"renewal", &youtube.ReadError{Primary: &youtube.APIError{Status: 401}, Renewal: renewal, Status: 401}, metadata.FetchAuthorizationPending, func(t *testing.T, f *metadata.ReadFacts) { assert.Same(t, renewal, f.Renewal) }},
		{"plain forbidden is not authorization", &youtube.APIError{Status: 403}, metadata.FetchFailed, nil},
		{"server", &youtube.APIError{Status: 503}, metadata.FetchFailed, nil},
		{"accounting", &youtube.ReadError{Primary: &youtube.APIError{Status: 401}, Accounting: acct, Status: 401}, metadata.FetchFailed, func(t *testing.T, f *metadata.ReadFacts) {
			assert.Same(t, acct, f.Accounting)
			assert.Equal(t, 401, f.HTTPStatus)
		}},
		{"quota with accounting", &youtube.ReadError{Primary: &youtube.APIError{Status: 403, Reason: "quotaExceeded"}, Accounting: acct}, metadata.FetchFailed, func(t *testing.T, f *metadata.ReadFacts) {
			assert.True(t, f.QuotaRefused)
			assert.Same(t, acct, f.Accounting)
		}},
		{"interruption", &youtube.ReadError{Primary: context.Canceled, Interruption: context.Canceled}, metadata.FetchFailed, func(t *testing.T, f *metadata.ReadFacts) { assert.ErrorIs(t, f.Interruption, context.Canceled) }},
		{"local quota refusal", auth.ErrQuotaRefused, metadata.FetchQuotaRefused, nil},
		{"changed", auth.ErrAuthorizationChanged, metadata.FetchAuthorizationPending, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) { return metadata.FetchResult{}, tc.err }}
			m, ms, _ := newFetchManager(t, bound, 1000)
			var ids []string
			for i := 0; i < 60; i++ {
				ids = append(ids, fmt.Sprintf("v%02d", i))
			}
			out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs(ids...))
			require.NotNil(t, out.Read)
			if out.Read.Accounting != nil {
				// An accounting fault is also an error the caller cannot miss.
				var fault *metadata.ReadFaultError
				require.ErrorAs(t, err, &fault)
				assert.Same(t, out.Read.Accounting, fault.Facts.Accounting)
				assert.ErrorIs(t, err, tc.err)
				assert.NotContains(t, err.Error(), "record failed")
			} else {
				require.NoError(t, err, "a failed read is a typed fact, not a local error")
			}
			assert.ErrorIs(t, out.Read.Err, tc.err)
			assert.Len(t, bound.calls, 1, "no call after the failure")
			wantAttempts := 1
			if errors.Is(tc.err, auth.ErrQuotaRefused) || errors.Is(tc.err, auth.ErrAuthorizationChanged) {
				wantAttempts = 0 // refused before admission: not an attempt
			}
			assert.Equal(t, wantAttempts, out.Attempts)
			for _, id := range ids {
				assert.Equal(t, tc.want, out.Statuses[id], id)
			}
			assert.Zero(t, countRows(t, ms))
			if tc.check != nil {
				tc.check(t, out.Read)
			}
		})
	}
}

func TestFetchForEvaluationUnusableResponseDoesNotSuspend(t *testing.T) {
	bound := &fakeBound{do: func(call int, ids []string) (metadata.FetchResult, error) {
		if call == 0 {
			return metadata.FetchResult{}, fmt.Errorf("get videos: %w", &youtube.InvalidResponseError{})
		}
		res := metadata.FetchResult{Complete: true, Returned: ids}
		for _, id := range ids {
			res.Details = append(res.Details, detail(id))
		}
		return res, nil
	}}
	m, _, _ := newFetchManager(t, bound, 1000)
	var ids []string
	for i := 0; i < 60; i++ {
		ids = append(ids, fmt.Sprintf("v%02d", i))
	}
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs(ids...))
	require.NoError(t, err)
	assert.Nil(t, out.Read)
	assert.Len(t, bound.calls, 2)
	assert.Equal(t, metadata.FetchUnusable, out.Statuses["v00"])
	assert.Equal(t, metadata.FetchFetched, out.Statuses["v55"])
}

func TestFetchForEvaluationUnreliableIdentityIsNotAbsence(t *testing.T) {
	for name, returned := range map[string][]string{"unrequested": {"a", "other"}, "duplicate": {"a", "a"}} {
		t.Run(name, func(t *testing.T) {
			bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
				return metadata.FetchResult{Complete: true, Returned: returned, Details: []metadata.TransientMetadata{detail("a")}}, nil
			}}
			m, _, _ := newFetchManager(t, bound, 1000)
			out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("a", "b"))
			require.NoError(t, err)
			assert.Equal(t, metadata.FetchUnusable, out.Statuses["b"])
		})
	}
}

func TestFetchForEvaluationPersistenceFailureIsALocalError(t *testing.T) {
	bound := &fakeBound{do: func(_ int, ids []string) (metadata.FetchResult, error) {
		return metadata.FetchResult{Complete: true, Returned: ids, Details: []metadata.TransientMetadata{detail("a")}}, nil
	}}
	m, ms, _ := newFetchManager(t, bound, 1000)
	_, err := ms.DB().Exec(`CREATE TRIGGER fail_metadata BEFORE INSERT ON transient_metadata BEGIN SELECT RAISE(FAIL, 'save failed'); END`)
	require.NoError(t, err)
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("a"))
	require.Error(t, err)
	assert.Equal(t, metadata.FetchFailed, out.Statuses["a"])
	assert.Nil(t, out.Read)
	assert.Zero(t, countRows(t, ms))
}

func TestFetchForEvaluationWithoutBoundFetcherIsALocalError(t *testing.T) {
	m, _, _ := newFetchManager(t, nil, 1000)
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("a"))
	require.Error(t, err)
	assert.Equal(t, metadata.FetchFailed, out.Statuses["a"])
}

func TestFetchForEvaluationBareAccountingErrorIsAFault(t *testing.T) {
	bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
		return metadata.FetchResult{}, fmt.Errorf("wrapped: %w", auth.ErrQuotaAccounting)
	}}
	m, _, _ := newFetchManager(t, bound, 1000)
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("a"))
	var fault *metadata.ReadFaultError
	require.ErrorAs(t, err, &fault)
	assert.ErrorIs(t, err, auth.ErrQuotaAccounting)
	assert.Equal(t, metadata.FetchFailed, out.Statuses["a"])
	assert.Zero(t, out.Attempts, "failed before admission is not an attempt")
}

func TestFetchForEvaluationCountsOnlyAdmittedSends(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		attempts int
	}{
		"success":            {nil, 1},
		"provider answered":  {&youtube.APIError{Status: 503}, 1},
		"admitted transport": {&youtube.ReadError{Primary: &youtube.ReadTransportError{}}, 1},
		"invalid response":   {&youtube.InvalidResponseError{}, 1},
		"refused":            {auth.ErrQuotaRefused, 0},
		"access expired":     {auth.ErrAccessExpiredBeforeAdmission, 0},
		"not configured":     {auth.ErrNotConfigured, 0},
	} {
		t.Run(name, func(t *testing.T) {
			bound := &fakeBound{do: func(_ int, ids []string) (metadata.FetchResult, error) {
				if tc.err != nil {
					return metadata.FetchResult{}, tc.err
				}
				return metadata.FetchResult{Complete: true, Returned: ids, Details: []metadata.TransientMetadata{detail("a")}}, nil
			}}
			m, _, _ := newFetchManager(t, bound, 1000)
			out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("a"))
			require.NoError(t, err)
			assert.Equal(t, tc.attempts, out.Attempts)
		})
	}
}

// R5 (run-initiation ARS-005 section 6.1.3): the run's effective dispatch stop
// is read before every chunk's preflight. Each level keeps the unsent inputs
// unsent with zero attempts, and the manager never writes the stop.
func TestFetchForEvaluationHonoursRunDispatchStop(t *testing.T) {
	ids := make([]string, 120) // three chunks
	for i := range ids {
		ids[i] = fmt.Sprintf("v%03d", i)
	}
	cases := []struct {
		name    string
		stored  string
		quota   bool
		want    metadata.FetchStatus
		wantErr bool
		stop    quota.DispatchStop
	}{
		{name: "authorization", stored: "authorization", want: metadata.FetchAuthorizationPending, stop: quota.StopAuthorization},
		{name: "quota", quota: true, want: metadata.FetchQuotaRefused, stop: quota.StopQuota},
		{name: "accounting", stored: "accounting_or_integrity", want: metadata.FetchFailed, wantErr: true, stop: quota.StopAccountingOrIntegrity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
				t.Fatal("a stopped run made a provider call")
				return metadata.FetchResult{}, nil
			}}
			m, ms, ledger := newFetchManager(t, bound, 1000)
			db := ms.DB()
			res, err := db.Exec(`INSERT INTO run (started_at, run_type, status) VALUES (?, 'manual', 'running')`, evalNow)
			require.NoError(t, err)
			runID, err := res.LastInsertId()
			require.NoError(t, err)
			ctx := quota.WithRun(context.Background(), runID)
			if c.stored != "" {
				_, err = db.Exec(`UPDATE run SET dispatch_stop=? WHERE id=?`, c.stored, runID)
				require.NoError(t, err)
			}
			if c.quota {
				h, err := ledger.Acquire(ctx, quota.OpVideosList)
				require.NoError(t, err)
				require.NotNil(t, h)
				require.NoError(t, h.Admit(ctx))
				require.NoError(t, h.Finalize(ctx, sqlNullInt(runID), quota.OutcomeQuota, sqlNullInt(403)))
			}
			rowsBefore := ledgerRowCount(t, ms)

			out, err := m.FetchForEvaluation(ctx, auth.AuthorizationBinding{}, allInputs(ids...))
			if c.wantErr {
				require.ErrorIs(t, err, auth.ErrQuotaAccounting)
			} else {
				require.NoError(t, err)
			}
			assert.Zero(t, out.Attempts)
			assert.Empty(t, bound.calls)
			assert.Equal(t, c.stop, out.Stop)
			for _, id := range ids[:50] {
				assert.Equal(t, c.want, out.Statuses[id], id)
			}
			if !c.wantErr {
				assert.Len(t, out.Statuses, len(ids), "every unsent input has a status")
				for _, id := range ids {
					assert.Equal(t, c.want, out.Statuses[id], id)
				}
			}
			assert.Equal(t, rowsBefore, ledgerRowCount(t, ms), "no ledger row, no debit")
			var stored *string
			require.NoError(t, db.QueryRow(`SELECT dispatch_stop FROM run WHERE id=?`, runID).Scan(&stored))
			if c.stored == "" {
				assert.Nil(t, stored, "the manager never writes the stop")
			}
		})
	}
}

func sqlNullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

func ledgerRowCount(t *testing.T, ms *metadata.Store) int {
	t.Helper()
	var n int
	require.NoError(t, ms.DB().QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&n))
	return n
}

// B7 at the manager: a generic provider failure suspends fetching for the rest
// of the run, across calls, and only for that run.
func TestFetchForEvaluationGenericFailureSuspendsTheRun(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want metadata.FetchStatus
	}{
		"server":     {&youtube.APIError{Status: 500}, metadata.FetchFailed},
		"rate limit": {&youtube.APIError{Status: 429}, metadata.FetchRateLimited},
	} {
		t.Run(name, func(t *testing.T) {
			bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) { return metadata.FetchResult{}, tc.err }}
			m, ms, _ := newFetchManager(t, bound, 1000)
			run7 := quota.WithRun(context.Background(), 7)

			out, err := m.FetchForEvaluation(run7, auth.AuthorizationBinding{}, allInputs("a1", "a2"))
			require.NoError(t, err)
			require.NotNil(t, out.Read)
			require.Len(t, bound.calls, 1)

			// A later call of the same run, with new IDs, sends nothing.
			out, err = m.FetchForEvaluation(run7, auth.AuthorizationBinding{}, allInputs("b1", "b2"))
			require.NoError(t, err)
			assert.Len(t, bound.calls, 1, "no request after the failure, for the rest of the run")
			assert.Zero(t, out.Attempts)
			assert.Equal(t, tc.want, out.Statuses["b1"])
			assert.Equal(t, tc.want, out.Statuses["b2"])
			assert.Zero(t, countRows(t, ms))

			// Another run, and a call with no run scope, still fetch.
			_, err = m.FetchForEvaluation(quota.WithRun(context.Background(), 8), auth.AuthorizationBinding{}, allInputs("c1"))
			require.NoError(t, err)
			assert.Len(t, bound.calls, 2)
			_, err = m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("d1"))
			require.NoError(t, err)
			assert.Len(t, bound.calls, 3)
		})
	}
}

// Only a generic failure outlives the call. The stop and the ledger carry the
// others, and an unusable response never suspends.
func TestFetchForEvaluationOtherFailuresDoNotSuspendTheRun(t *testing.T) {
	for name, err := range map[string]error{
		"unusable":     fmt.Errorf("get videos: %w", &youtube.InvalidResponseError{}),
		"quota":        &youtube.APIError{Status: 403, Reason: "quotaExceeded"},
		"unauthorized": &youtube.APIError{Status: 401},
		"interruption": &youtube.ReadError{Primary: context.Canceled, Interruption: context.Canceled},
	} {
		t.Run(name, func(t *testing.T) {
			bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) { return metadata.FetchResult{}, err }}
			m, _, _ := newFetchManager(t, bound, 1000)
			run := quota.WithRun(context.Background(), 7)
			_, _ = m.FetchForEvaluation(run, auth.AuthorizationBinding{}, allInputs("a1"))
			_, _ = m.FetchForEvaluation(run, auth.AuthorizationBinding{}, allInputs("b1"))
			assert.Len(t, bound.calls, 2, "the next call of the run is not suspended")
		})
	}
}

// A run dispatch stop wins over the suspension (ARS-005 section 6.2).
func TestFetchForEvaluationStopWinsOverRunSuspension(t *testing.T) {
	bound := &fakeBound{do: func(int, []string) (metadata.FetchResult, error) {
		return metadata.FetchResult{}, &youtube.APIError{Status: 500}
	}}
	m, _, _ := newFetchManager(t, bound, 1000)
	run := quota.WithRun(context.Background(), 7)
	_, err := m.FetchForEvaluation(run, auth.AuthorizationBinding{}, allInputs("a1"))
	require.NoError(t, err)

	quota.LatchRunStop(run, quota.StopAuthorization)
	out, err := m.FetchForEvaluation(run, auth.AuthorizationBinding{}, allInputs("b1"))
	require.NoError(t, err)
	assert.Equal(t, quota.StopAuthorization, out.Stop)
	assert.Equal(t, metadata.FetchAuthorizationPending, out.Statuses["b1"])
	assert.Len(t, bound.calls, 1)
}
