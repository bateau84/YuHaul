package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// The batch claim loop (ARS-005 section 5.2, variant K) on the real receiving
// path of receivingFixture: real store, ledger, metadata manager and auth.

// seedPending inserts n pending quota-deferred items, one video each, created
// one second apart in ordinal order starting two days ago. It returns the item
// ids in that order.
func (rf *receivingFixture) seedPending(n int) []int64 {
	rf.t.Helper()
	tx, err := rf.db.Begin()
	require.NoError(rf.t, err)
	defer func() { _ = tx.Rollback() }()
	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		rf.seq++
		res, err := tx.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'fixture-channel', ?, 'not_yet_evaluated', 'not_yet_evaluated')`,
			fmt.Sprintf("batch-%d", rf.seq), rf.runID)
		require.NoError(rf.t, err)
		video, err := res.LastInsertId()
		require.NoError(rf.t, err)
		res, err = tx.Exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at) VALUES (?, ?, 'quota_exhausted', 0, 'pending', ?)`,
			video, rf.runID, base.Add(time.Duration(i)*time.Second))
		require.NoError(rf.t, err)
		id, err := res.LastInsertId()
		require.NoError(rf.t, err)
		ids = append(ids, id)
	}
	require.NoError(rf.t, tx.Commit())
	return ids
}

// initiatedBy makes the receiving run a fresh run started by initiator. The
// initiator is immutable, so the fixture's run (no initiator) is replaced.
func (rf *receivingFixture) initiatedBy(initiator string) {
	rf.t.Helper()
	rf.recvRun = rf.newReceivingRun(initiator)
}

// newReceivingRun creates a guarded production run, such as a later day's
// scheduled run.
func (rf *receivingFixture) newReceivingRun(initiator string) int64 {
	rf.t.Helper()
	ctx := context.Background()
	routerStore, configStore := router.NewStore(rf.db), config.NewStore(rf.db)
	cfgID, err := config.NewService(routerStore, configStore).ActiveVersionID(ctx)
	require.NoError(rf.t, err)
	runType := state.RunTypeScheduled
	if initiator != state.InitiatedBySchedule {
		runType = state.RunTypeManual
	}
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: runType, Status: state.RunStatusRunning, ConfigVersionID: cfgID, ActionAuthorityVersion: 1, InitiatedBy: &initiator}
	tx, err := rf.db.BeginTx(ctx, nil)
	require.NoError(rf.t, err)
	id, err := rf.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
	require.NoError(rf.t, err)
	require.NoError(rf.t, tx.Commit())
	return id
}

// claimFirstBatch is createClaimedRunLocked's claim for a run that exists.
func (rf *receivingFixture) claimFirstBatch(run int64) []queue.RetryGroupReceipt {
	rf.t.Helper()
	ctx := context.Background()
	items, err := rf.qs.ListDeferredRetryCandidatesForRun(ctx, time.Now(), 0, retryClaimBatch)
	require.NoError(rf.t, err)
	var claim queue.BatchClaim
	require.NoError(rf.t, rf.qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		claim, err = rf.qs.ClaimDeferredBatchTx(ctx, tx, run, items, time.Now())
		return err
	}))
	return claim.Receipts
}

func (rf *receivingFixture) memberCount(run int64) int {
	rf.t.Helper()
	var n int
	require.NoError(rf.t, rf.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=?`, run).Scan(&n))
	return n
}

// untouched counts the given items still pending, unowned and never retried.
func (rf *receivingFixture) untouched(ids []int64) int {
	rf.t.Helper()
	n := 0
	for _, id := range ids {
		var status string
		var owner sql.NullInt64
		var retries int
		require.NoError(rf.t, rf.db.QueryRow(`SELECT status, retry_run_id, retry_count FROM deferred_item WHERE id=?`, id).Scan(&status, &owner, &retries))
		if status == "pending" && !owner.Valid {
			n++
		}
	}
	return n
}

// batchProbe is a readiness seam that records each pass's input count and
// answers every input with one scripted status.
type batchProbe struct {
	status metadata.FetchStatus
	passes []int
	onPass func(pass int)
}

func (b *batchProbe) GetDisplayMetadata(_ context.Context, id string) (*metadata.TransientMetadata, error) {
	return nil, &metadata.MetadataExpiredError{VideoID: id}
}
func (b *batchProbe) RunLifecycle(context.Context) error { return nil }
func (b *batchProbe) FetchForEvaluation(_ context.Context, _ auth.AuthorizationBinding, reqs []metadata.InputRequirement) (metadata.FetchOutcome, error) {
	b.passes = append(b.passes, len(reqs))
	out := metadata.FetchOutcome{Statuses: map[string]metadata.FetchStatus{}}
	for _, r := range reqs {
		out.Statuses[r.VideoID] = b.status
	}
	if b.onPass != nil {
		b.onPass(len(b.passes))
	}
	return out, nil
}

func (rf *receivingFixture) runCtx(run int64) context.Context {
	return quota.WithRun(context.Background(), run)
}

// B1: 1,200 eligible items are claimed 500, 500 and 200 per pass, each video
// once, and the loop ends when no candidate remains.
func TestBatchLoopClaimsFiveHundredAtATimeB1(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.initiatedBy(state.InitiatedBySchedule)
	ids := rf.seedPending(1200)
	probe := &batchProbe{status: metadata.FetchUnusable} // released unchanged
	rf.p.meta = probe

	first := rf.claimFirstBatch(rf.recvRun)
	require.Len(t, first, retryClaimBatch)
	require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, first, false))

	require.Equal(t, []int{500, 500, 200}, probe.passes)
	require.Equal(t, 1200, rf.memberCount(rf.recvRun), "each video is a member once")
	require.Equal(t, 1200, rf.untouched(ids), "unusable inputs are released unchanged")
	more, err := rf.qs.ListDeferredRetryCandidatesForRun(context.Background(), time.Now(), rf.recvRun, 10)
	require.NoError(t, err)
	require.Empty(t, more, "a run never claims a video twice")
}

// B2 and B7 share a setup: 1,100 items, so one batch of 500 leaves 600
// unclaimed.
func TestBatchLoopQuotaRefusalLeavesTheRestUntouchedThenNextDayContinuesB2(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.initiatedBy(state.InitiatedBySchedule)
	ids := rf.seedPending(1100)
	rf.listStatus.Store(http.StatusForbidden) // quotaExceeded on the first chunk

	first := rf.claimFirstBatch(rf.recvRun)
	require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, first, false))

	require.Equal(t, int32(1), rf.listCalls.Load(), "one admitted request, then no more")
	require.Empty(t, rf.storedStop(), "quota is derived from the ledger, never stored")
	require.Equal(t, 500, rf.memberCount(rf.recvRun), "the run stopped claiming after its first batch")
	require.Equal(t, 600, rf.untouched(ids[500:]), "unclaimed rows are untouched")
	for _, r := range first {
		var reason string
		require.NoError(t, rf.db.QueryRow(`SELECT deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, r.VideoID).Scan(&reason))
		require.Equal(t, "quota_exhausted", reason)
	}

	// Next quota day, next run: it claims the 500 oldest eligible. The items
	// the first run claimed are released unchanged and older, so they come
	// first; none is skipped over.
	_, err := rf.db.Exec(`UPDATE quota_ledger SET date = date(date, '-1 day') WHERE run_id=?`, rf.recvRun)
	require.NoError(t, err)
	rf.listStatus.Store(http.StatusOK)
	next := rf.newReceivingRun(state.InitiatedBySchedule)
	got := rf.claimFirstBatch(next)
	require.Len(t, got, retryClaimBatch)
	for i, r := range got {
		require.Equal(t, ids[i], r.Deferred[0].ID, "claims are the created-at order")
	}
}

// B7 (variant K): a provider 5xx on the first metadata chunk suspends
// fetching for the rest of the run, and the run keeps claiming until no candidate remains.
func TestBatchLoopKeepsClaimingAfterAProviderFailureB7(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.initiatedBy(state.InitiatedByRunNow)
	ids := rf.seedPending(1100)
	rf.listStatus.Store(http.StatusInternalServerError)

	first := rf.claimFirstBatch(rf.recvRun)
	require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, first, false))

	require.Equal(t, int32(1), rf.listCalls.Load(), "one failed request, none after it for the rest of the run")
	require.Equal(t, 1100, rf.memberCount(rf.recvRun), "every video claimed once")
	require.Equal(t, 1100, rf.untouched(ids), "each video without current metadata is released unchanged")
}

// B4/R2: a stop requested during a batch ends the loop. The run holds only the
// batch it already claimed.
func TestBatchLoopStopRequestedBetweenBatchesClaimsNothingMoreB4(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.initiatedBy(state.InitiatedByRunNow)
	ids := rf.seedPending(700)
	probe := &batchProbe{status: metadata.FetchUnusable}
	probe.onPass = func(int) {
		_, err := rf.db.Exec(`UPDATE run SET stop_requested_at=? WHERE id=?`, time.Now().UTC(), rf.recvRun)
		require.NoError(t, err)
	}
	rf.p.meta = probe

	first := rf.claimFirstBatch(rf.recvRun)
	require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, first, false), "a stop is not a failure of the loop")
	require.Equal(t, []int{500}, probe.passes)
	require.Equal(t, 500, rf.memberCount(rf.recvRun))
	require.Equal(t, 200, rf.untouched(ids[500:]))
}

// R2 and R1: a stored accounting stop, a quota row and a context that is done
// each end the loop before any claim.
func TestBatchLoopLeavesBeforeClaimingUnderAStop(t *testing.T) {
	cases := map[string]func(rf *receivingFixture) context.Context{
		"accounting stop": func(rf *receivingFixture) context.Context {
			_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='accounting_or_integrity' WHERE id=?`, rf.recvRun)
			require.NoError(t, err)
			return rf.ctx()
		},
		"quota refusal in the bucket": func(rf *receivingFixture) context.Context {
			budget, err := rf.ledger.Budget(context.Background())
			require.NoError(t, err)
			_, err = rf.db.Exec(`INSERT INTO quota_ledger (date, operation_type, units_consumed, call_count, run_id, recorded_at, outcome_category, http_status) VALUES (?, 'videos_list', 1, 1, ?, ?, 'quota', 403)`,
				budget.Date, rf.recvRun, time.Now().UTC())
			require.NoError(t, err)
			return rf.ctx()
		},
		"context done": func(rf *receivingFixture) context.Context {
			ctx, cancel := context.WithCancel(rf.ctx())
			cancel()
			return ctx
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			rf.initiatedBy(state.InitiatedBySchedule)
			ids := rf.seedPending(5)
			ctx := arrange(rf)
			require.NoError(t, rf.p.processRetryBatches(ctx, rf.recvRun, []queue.RetryGroupReceipt{}, false))
			require.Zero(t, rf.memberCount(rf.recvRun))
			require.Equal(t, 5, rf.untouched(ids))
			require.Zero(t, rf.listCalls.Load())
		})
	}
}

// An authorization stop does not leave the loop: claiming and settling are
// local, and no provider request follows.
func TestBatchLoopAuthorizationStopStillClaimsLocally(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.initiatedBy(state.InitiatedBySchedule)
	ids := rf.seedPending(5)
	_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
	require.NoError(t, err)

	require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{}, false))
	require.Equal(t, 5, rf.memberCount(rf.recvRun))
	require.Zero(t, rf.listCalls.Load())
	require.Zero(t, rf.renewals.Load())
	for _, id := range ids {
		var status, reason string
		require.NoError(t, rf.db.QueryRow(`SELECT status, deferral_reason FROM deferred_item WHERE id=?`, id).Scan(&status, &reason))
		require.Equal(t, "pending", status, "authorization_pending settlement restores the row")
		require.Equal(t, "quota_exhausted", reason, "with its original reason")
	}
	var deferred int
	require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND deferred_reason='authorization_pending'`, rf.recvRun).Scan(&deferred))
	require.Equal(t, 5, deferred)
}

// B5: a queue retry, and a run with no recorded initiator, never claim more.
func TestBatchLoopOnlyRunNowAndScheduledClaimMoreB5(t *testing.T) {
	for _, initiator := range []string{state.InitiatedByQueueRetry, ""} {
		t.Run("initiator="+initiator, func(t *testing.T) {
			rf := newReceivingFixture(t)
			if initiator != "" {
				rf.initiatedBy(initiator)
			}
			rf.seedPending(5)
			rf.p.meta = &batchProbe{status: metadata.FetchUnusable}
			require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{}, false))
			require.Zero(t, rf.memberCount(rf.recvRun))
		})
	}
}

// B3: a resumed Run Now run settles what it holds, then continues in order.
func TestBatchLoopResumedRunFinishesItsBatchThenContinuesB3(t *testing.T) {
	rf := newReceivingFixture(t)
	rf.initiatedBy(state.InitiatedByRunNow)
	rf.seedPending(7)
	rf.p.meta = &batchProbe{status: metadata.FetchUnusable}
	items, err := rf.qs.ListDeferredRetryCandidatesForRun(context.Background(), time.Now(), 0, 3) // the interrupted run held three
	require.NoError(t, err)
	var held queue.BatchClaim
	require.NoError(t, rf.qs.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		held, err = rf.qs.ClaimDeferredBatchTx(context.Background(), tx, rf.recvRun, items, time.Now())
		return err
	}))
	require.Len(t, held.Receipts, 3)

	require.NoError(t, rf.p.processRetryBatches(rf.ctx(), rf.recvRun, nil, true))
	require.Equal(t, 7, rf.memberCount(rf.recvRun), "the resumed run took the rest, in one pass each, once")
}
