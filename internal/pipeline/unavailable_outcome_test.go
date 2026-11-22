package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// ARS-005 section 6.3 and 6.4 (tests D3, D4, O1 for the pipeline): a retried
// video YouTube no longer returns ends as unavailable_on_youtube only when its
// claim receipt holds a requeued_by_user row. Real migrated SQLite, the real
// receiving path and the real state machine; the metadata seam is scripted to
// answer `absent`, which the metadata package tests derive from raw ids.

// claimRequeued creates a video with a pending requeued_by_user item and claims
// it for the receiving run, as a re-queued stranded video is claimed.
func (rf *receivingFixture) claimRequeued() queue.RetryGroupReceipt {
	rf.t.Helper()
	ctx := context.Background()
	rf.seq++
	v, err := state.NewMachine(rf.state).DiscoverVideo(ctx, rf.runID, fmt.Sprintf("requeued-%d", rf.seq), "fixture-channel")
	require.NoError(rf.t, err)
	item, err := rf.qs.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID: v.ID, RunID: rf.runID, Reason: queue.DeferredReasonRequeuedByUser,
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

func (rf *receivingFixture) scalar(query string, args ...any) string {
	rf.t.Helper()
	var s sql.NullString
	require.NoError(rf.t, rf.db.QueryRow(query, args...).Scan(&s))
	return s.String
}

func (rf *receivingFixture) absent(receipts ...queue.RetryGroupReceipt) {
	rf.t.Helper()
	statuses := map[string]metadata.FetchStatus{}
	for _, r := range receipts {
		statuses[rf.scalar(`SELECT video_id FROM video WHERE id=?`, r.VideoID)] = metadata.FetchAbsent
	}
	rf.p.meta = &stubReadiness{statuses: statuses}
}

// D3: re-queued videos absent from a successful response end as
// unavailable_on_youtube, with nothing else written and nothing sent.
func TestAbsentRequeuedVideoEndsUnavailableOnYouTubeD3(t *testing.T) {
	rf := newReceivingFixture(t)
	receipts := []queue.RetryGroupReceipt{rf.claimRequeued(), rf.claimRequeued()}
	rf.absent(receipts...)
	events := rf.p.broadcast.Subscribe(rf.recvRun)
	defer rf.p.broadcast.Unsubscribe(rf.recvRun, events)
	deferredBefore := rf.scalar(`SELECT COUNT(*) FROM deferred_item`)

	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))

	for _, r := range receipts {
		require.Equal(t, "evaluated/unavailable_on_youtube", rf.scalar(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, r.VideoID))
		var outcome string
		var reason, message sql.NullString
		require.NoError(t, rf.db.QueryRow(`SELECT final_outcome, deferred_reason, error_message FROM run_video_outcome WHERE run_id=? AND video_id=?`,
			rf.recvRun, r.VideoID).Scan(&outcome, &reason, &message))
		require.Equal(t, "unavailable_on_youtube", outcome)
		require.False(t, reason.Valid, "no reason")
		require.False(t, message.Valid, "no message")

		require.Equal(t, "completed", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, r.Deferred[0].ID), "the claimed item is acknowledged")
		for _, table := range []string{"route_decision", "executed_action", "playlist_send_guard", "error_entry"} {
			require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM `+table+` WHERE video_id=?`, r.VideoID), table)
		}
		var active int
		require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status IN ('pending','retrying')`, r.VideoID).Scan(&active))
		require.Zero(t, active, "no active queue item afterwards")
	}
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM condition_audit`), "no condition audit (it hangs off route decisions)")
	require.Equal(t, deferredBefore, rf.scalar(`SELECT COUNT(*) FROM deferred_item`), "no new deferred row")
	require.Zero(t, rf.posts.Load(), "no playlistItems.insert")
	require.Zero(t, rf.listCalls.Load(), "the stub answers without a provider call")
	require.Zero(t, rf.ledgerRows(), "nothing is debited")

	for range receipts {
		ev := <-events
		require.Equal(t, EventRunVideo, ev.Type)
		var payload videoPayload
		require.NoError(t, json.Unmarshal(ev.Payload, &payload))
		require.Equal(t, "unavailable_on_youtube", payload.Outcome)
		require.False(t, payload.MatchedRoute)
	}

	// O1: the counter reads the new outcome as its own category.
	require.NoError(t, rf.state.RecomputeRunCounters(context.Background(), rf.recvRun))
	run, err := rf.state.GetRun(context.Background(), rf.recvRun)
	require.NoError(t, err)
	require.Equal(t, 2, run.VideosUnavailable)
	require.Zero(t, run.VideosFailed+run.VideosRejected+run.VideosExcluded)
}

// D4: the same response for a retried video that was not re-queued is the
// ordinary video-unavailable failure, never unavailable_on_youtube.
func TestAbsentOtherRetriedVideoFailsD4(t *testing.T) {
	rf := newReceivingFixture(t)
	receipts := rf.claimNew(1)
	rf.absent(receipts...)

	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))

	v := receipts[0].VideoID
	require.Equal(t, "failed/failed", rf.scalar(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, v))
	require.Equal(t, "failed", rf.scalar(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, v))
	require.Contains(t, rf.scalar(`SELECT error_message FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, v), "code=video_unavailable")
	require.Equal(t, "1", rf.scalar(`SELECT COUNT(*) FROM error_entry WHERE video_id=? AND status='active'`, v), "one Errors entry")
}

// One pass mixes both kinds: the receipt, not the response, decides.
func TestAbsentSplitsOnTheReceiptInOnePass(t *testing.T) {
	rf := newReceivingFixture(t)
	requeued, other := rf.claimRequeued(), rf.claimNew(1)[0]
	rf.absent(requeued, other)

	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{requeued, other}, false))

	require.Equal(t, "unavailable_on_youtube", rf.scalar(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, requeued.VideoID))
	require.Equal(t, "failed", rf.scalar(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, other.VideoID))
}

// A receipt that is no longer live writes nothing: the removal is an admission
// refusal, not an outcome (invariant 6).
func TestAbsentRequeuedVideoWithRemovedItemWritesNothing(t *testing.T) {
	rf := newReceivingFixture(t)
	receipt := rf.claimRequeued()
	rf.absent(receipt)
	_, err := rf.db.Exec(`UPDATE deferred_item SET status='discarded' WHERE id=?`, receipt.Deferred[0].ID)
	require.NoError(t, err)

	err = rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false)

	var refusal *retryAdmissionRefusal
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "not_yet_evaluated/not_yet_evaluated", rf.scalar(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, receipt.VideoID))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome='unavailable_on_youtube'`, rf.recvRun, receipt.VideoID))
}
