package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
)

// Assessment d8d611d7 blockers 1 and 2 (ARS-005 section 6.2, "Authorization-stop
// receipt settlement"): a receiving run that holds no authorization binding
// under an authorization stop settles the exact claim locally as
// deferred/authorization_pending. It makes no provider request, adds no Errors
// entry, action or sink, and keeps the saved decision and its audit.
//
// The cause of the stop (reconnect_required or temporary) is not stored per
// run by this revision (section 12); these tests assert the outcome only.

// videoText is the YouTube ID of the video row, which the Store A row is keyed by.
func (rf *receivingFixture) videoText(receipt queue.RetryGroupReceipt) string {
	rf.t.Helper()
	return rf.scalar(`SELECT video_id FROM video WHERE id=?`, receipt.VideoID)
}

// decisionDump is the receiving run's decision and condition audit of a video.
func (rf *receivingFixture) decisionDump(videoID int64) string {
	rf.t.Helper()
	return rf.scalar(`SELECT COALESCE(group_concat(rd.id||':'||rd.route_id||':'||rd.decision||':'||
		(SELECT COUNT(*) FROM condition_audit ca WHERE ca.route_decision_id=rd.id), ','), '')
		FROM route_decision rd WHERE rd.run_id=? AND rd.video_id=?`, rf.recvRun, videoID)
}

// restartWithoutBinding is a receiving run found interrupted, with the account
// disconnected so the execution cannot take a binding, and optionally with the
// authorization stop already stored.
func (rf *receivingFixture) runWithoutBinding(storedStop bool) {
	rf.t.Helper()
	ctx := context.Background()
	if storedStop {
		_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
		require.NoError(rf.t, err)
	}
	_, err := rf.db.Exec(`UPDATE run SET status='interrupted', current_phase=? WHERE id=?`, string(PhaseActionExecution), rf.recvRun)
	require.NoError(rf.t, err)
	require.NoError(rf.t, rf.auth.Disconnect(ctx))
	_, bindErr := rf.auth.Bind(ctx)
	require.ErrorIs(rf.t, bindErr, auth.ErrNoConnectedAccount)
	rf.p.Execute(ctx, rf.recvRun)
}

// requireSettledLocally checks everything the settlement must and must not do
// for one receipt: the run outcome and lifecycle, the exact queue row, and the
// absence of any action, Errors entry or provider work.
func (rf *receivingFixture) requireSettledLocally(receipt queue.RetryGroupReceipt) {
	rf.t.Helper()
	rf.requireDeferred(receipt, "authorization_pending")
	require.Equal(rf.t, "deferred/deferred", rf.scalar(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, receipt.VideoID))
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action WHERE video_id=?`, receipt.VideoID), "no action was attempted or recorded")
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry WHERE video_id=?`, receipt.VideoID), "no Errors entry")
	require.Equal(rf.t, "1", rf.scalar(`SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, receipt.VideoID), "no second queue row")
	require.Equal(rf.t, "authorization", rf.storedStop())
	rf.requireNoProviderWork()
}

// A receipt whose inputs are held locally and whose route matches is the case
// the executor used to fail: without a binding it turned the receipt into an
// ordinary failed video and an active Errors entry.
func TestReceivingNoBindingSettlesALocallyReadyMatchAsAuthorizationPending(t *testing.T) {
	for _, c := range []struct {
		name       string
		storedStop bool
	}{
		{name: "stored stop, account disconnected", storedStop: true},
		{name: "authorization cause from the refused binding", storedStop: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			logs := rf.captureLogs()
			ready := rf.claimNew(1)[0]
			seedCurrentMetadata(t, rf.db, rf.videoText(ready))
			notReady := rf.claimNew(1)[0] // no Store A row: settles through readiness

			rf.runWithoutBinding(c.storedStop)

			require.NotContains(t, logs.String(), "receiving settlement without a binding failed")
			require.Equal(t, "failed", rf.scalar(`SELECT status FROM run WHERE id=?`, rf.recvRun), "the run still ends: it has no binding")
			rf.requireSettledLocally(ready)
			rf.requireSettledLocally(notReady)
			require.Equal(t, "1", rf.scalar(`SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=? AND decision='matched'`, rf.recvRun, ready.VideoID),
				"the local evaluation ran and its decision is kept")
		})
	}
}

// A receipt whose receiving run already saved its decision keeps that decision
// and audit, and the blocked action is deferred durably in the same
// transaction as the handoff (assessment a4044327).
func TestReceivingNoBindingSettlesASavedDecisionAsAuthorizationPending(t *testing.T) {
	for _, c := range []struct {
		name       string
		localInput bool // the saved video's inputs are still held locally
	}{
		{name: "inputs not held locally", localInput: false},
		{name: "inputs held locally", localInput: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			logs := rf.captureLogs()
			receipt := rf.claimNew(1)[0]
			if c.localInput {
				seedCurrentMetadata(t, rf.db, rf.videoText(receipt))
			}
			rf.decide(receipt)
			decision := rf.decisionDump(receipt.VideoID)
			require.NotEmpty(t, decision)

			rf.runWithoutBinding(true)

			require.NotContains(t, logs.String(), "receiving settlement without a binding failed")
			rf.requireSettledLocally(receipt)
			require.Equal(t, decision, rf.decisionDump(receipt.VideoID), "the saved decision and its audit are preserved, not rerouted")
		})
	}
}

// The same saved receipt with a binding in hand (a connected execution under a
// stored authorization stop) records the same durable deferral.
func TestReceivingBoundSavedDecisionUnderAuthorizationStopRecordsTheDeferral(t *testing.T) {
	rf := newReceivingFixture(t)
	receipt := rf.claimNew(1)[0]
	rf.decide(receipt)
	decision := rf.decisionDump(receipt.VideoID)
	_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
	require.NoError(t, err)

	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false))

	rf.requireSettledLocally(receipt)
	require.Equal(t, decision, rf.decisionDump(receipt.VideoID))
}

// The quota twin of the same seam: a saved decision under a quota stop with no
// inputs records deferred/quota_exhausted, not a silent acknowledgement. This
// is the shared deferral transaction; it is outside the authorization scope
// of the assessment and is recorded in ARS-005 section 12 as such.
func TestReceivingSavedDecisionUnderQuotaStopRecordsTheDeferral(t *testing.T) {
	rf := newReceivingFixture(t)
	receipt := rf.claimNew(1)[0]
	rf.decide(receipt)
	// Quota is derived from the run's ledger, never stored: a refusal in the
	// current bucket puts this run under the stop.
	budget, err := rf.ledger.Budget(context.Background())
	require.NoError(t, err)
	_, err = rf.db.Exec(`INSERT INTO quota_ledger (date, operation_type, units_consumed, call_count, run_id, recorded_at, outcome_category, http_status) VALUES (?, 'videos_list', 1, 1, ?, ?, 'quota', 403)`,
		budget.Date, rf.recvRun, time.Now().UTC())
	require.NoError(t, err)
	stop, err := rf.ledger.RunDispatchStop(rf.ctx())
	require.NoError(t, err)
	require.Equal(t, quota.StopQuota, stop)

	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false))

	rf.requireDeferred(receipt, "quota_exhausted")
	require.Equal(t, "deferred/deferred", rf.scalar(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, receipt.VideoID))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action WHERE video_id=?`, receipt.VideoID))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry WHERE video_id=?`, receipt.VideoID))
	require.Zero(t, rf.posts.Load())
	require.Zero(t, rf.listCalls.Load())
}

// A send guard that is still unresolved means a mutation may already have been
// issued. Its disposition is never reinterpreted: the receipt is not deferred,
// the guard is untouched, and nothing is sent.
func TestReceivingNoBindingLeavesAnUnresolvedGuardAlone(t *testing.T) {
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	receipt := rf.claimNew(1)[0]
	rf.decide(receipt)
	_, err := rf.db.Exec(`INSERT INTO playlist_send_guard(video_id,target_playlist_id,action_type,run_id,route_decision_id,authorization_id,prepared_at)
		SELECT video_id, 'PLreceiving', 'playlist_insert', run_id, id, 'opaque', ? FROM route_decision WHERE run_id=? AND video_id=?`,
		"2026-01-01T00:00:00Z", rf.recvRun, receipt.VideoID)
	require.NoError(t, err)
	guards := rf.scalar(`SELECT group_concat(video_id||target_playlist_id||run_id||route_decision_id||authorization_id||prepared_at) FROM playlist_send_guard`)

	rf.runWithoutBinding(true)

	require.Contains(t, logs.String(), "receiving settlement without a binding failed")
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND deferred_reason='authorization_pending'`, rf.recvRun, receipt.VideoID))
	require.Equal(t, guards, rf.scalar(`SELECT group_concat(video_id||target_playlist_id||run_id||route_decision_id||authorization_id||prepared_at) FROM playlist_send_guard`))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action WHERE video_id=?`, receipt.VideoID))
	rf.requireNoProviderWork()
}

// Local-only work that needs no provider request still settles locally: a
// locally ready video no route matches ends as rejected without a binding and
// is not turned into an authorization deferral.
func TestReceivingNoBindingStillSettlesALocallyReadyNonMatchLocally(t *testing.T) {
	rf := newReceivingFixture(t)
	ctx := context.Background()
	v, err := state.NewMachine(rf.state).DiscoverVideo(ctx, rf.runID, "recv-other", "other-channel")
	require.NoError(t, err)
	seedCurrentMetadata(t, rf.db, "recv-other")
	receipt := rf.claimVideo(v.ID)

	rf.runWithoutBinding(true)

	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND deferred_reason='authorization_pending'`, rf.recvRun, receipt.VideoID),
		"a non-match needs no send, so it is not an authorization deferral")
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry WHERE video_id=?`, receipt.VideoID))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action WHERE video_id=?`, receipt.VideoID))
	rf.requireNoProviderWork()
}
