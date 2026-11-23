package pipeline

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// ARS-005 section 6.2, "Classify BR-039 final and partly_done before
// hydration": an open receipt whose receiving run already saved a complete
// decision is not hydrated or rerouted. The saved decision and its outstanding
// action are settled locally, whatever the metadata seam would now answer.
// Only unfinished evaluation depends on readiness.

// countingReadiness is a scripted readiness seam that counts how often the
// receiver asks it to fetch.
type countingReadiness struct {
	*stubReadiness
	fetches atomic.Int32
}

func (c *countingReadiness) FetchForEvaluation(ctx context.Context, b auth.AuthorizationBinding, reqs []metadata.InputRequirement) (metadata.FetchOutcome, error) {
	c.fetches.Add(1)
	return c.stubReadiness.FetchForEvaluation(ctx, b, reqs)
}

// script makes every video of the receipts answer status at the seam and
// local display metadata unavailable (no Store A row).
func (rf *receivingFixture) script(status metadata.FetchStatus, receipts ...queue.RetryGroupReceipt) *countingReadiness {
	rf.t.Helper()
	statuses := map[string]metadata.FetchStatus{}
	for _, r := range receipts {
		statuses[rf.scalar(`SELECT video_id FROM video WHERE id=?`, r.VideoID)] = status
	}
	c := &countingReadiness{stubReadiness: &stubReadiness{statuses: statuses}}
	rf.p.meta = c
	return c
}

// decide lets the receiving run really evaluate the claimed video (real
// router, real state machine) and then stops before any action: the state a
// receiver finds after a crash between evaluation and dispatch.
func (rf *receivingFixture) decide(receipt queue.RetryGroupReceipt) {
	rf.t.Helper()
	real := rf.p.meta
	defer func() { rf.p.meta = real }()
	rf.p.meta = &stubReadiness{} // local display metadata unavailable; the route reads channel_id
	v, err := rf.state.GetVideo(context.Background(), receipt.VideoID)
	require.NoError(rf.t, err)
	routed, err := rf.p.routeVideos(rf.ctx(), rf.recvRun, []*state.Video{v})
	require.NoError(rf.t, err)
	require.Len(rf.t, routed, 1)
	require.Equal(rf.t, "evaluated/not_yet_evaluated", rf.scalar(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, receipt.VideoID))
	require.Equal(rf.t, "1", rf.scalar(`SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=? AND decision='matched'`, rf.recvRun, receipt.VideoID))
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action WHERE video_id=?`, receipt.VideoID), "the action is still outstanding")
}

// A saved decision settles its outstanding action without a metadata read,
// for every answer the seam could give, including the ones that would
// otherwise settle the receipt as unavailable, failed, or released.
func TestSavedDecisionIsSettledWithoutMetadata(t *testing.T) {
	cases := []struct {
		name    string
		claim   func(*receivingFixture) queue.RetryGroupReceipt
		status  metadata.FetchStatus
		inputOK bool // the seam says the inputs are fetched, but display metadata is gone
	}{
		{"omitted from a requeued receipt", func(rf *receivingFixture) queue.RetryGroupReceipt { return rf.claimRequeued() }, metadata.FetchAbsent, false},
		{"omitted from an ordinary receipt", func(rf *receivingFixture) queue.RetryGroupReceipt { return rf.claimNew(1)[0] }, metadata.FetchAbsent, false},
		{"fetch failed", func(rf *receivingFixture) queue.RetryGroupReceipt { return rf.claimNew(1)[0] }, metadata.FetchFailed, false},
		{"unusable response", func(rf *receivingFixture) queue.RetryGroupReceipt { return rf.claimNew(1)[0] }, metadata.FetchUnusable, false},
		{"fetched then lost", func(rf *receivingFixture) queue.RetryGroupReceipt { return rf.claimNew(1)[0] }, metadata.FetchFetched, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			receipt := c.claim(rf)
			rf.decide(receipt)
			seam := rf.script(c.status, receipt)

			require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false))

			require.Zero(t, seam.fetches.Load(), "a saved decision is never hydrated")
			require.Zero(t, rf.listCalls.Load())
			require.Equal(t, "1", rf.scalar(`SELECT COUNT(*) FROM quota_ledger WHERE run_id=?`, rf.recvRun), "one debit")
			require.Equal(t, "playlist_items_insert", rf.scalar(`SELECT operation_type FROM quota_ledger WHERE run_id=?`, rf.recvRun), "and it is the send, not a metadata read")
			require.Equal(t, int32(1), rf.posts.Load(), "the outstanding action was sent once")
			require.Equal(t, "completed/accepted", rf.scalar(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, receipt.VideoID))
			require.Equal(t, "accepted", rf.scalar(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, receipt.VideoID))
			require.Equal(t, "completed", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, receipt.Deferred[0].ID), "the claimed item is acknowledged")
			require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry WHERE video_id=?`, receipt.VideoID), "no Errors entry")
			require.Equal(t, "1", rf.scalar(`SELECT COUNT(*) FROM route_decision WHERE video_id=? AND run_id=?`, receipt.VideoID, rf.recvRun), "the saved decision was neither repeated nor replaced")
		})
	}
}

// The contrast: a claimed video whose evaluation is unfinished still follows
// readiness. The same seam answers settle the receipt exactly as the section
// 6.2 table says, and nothing is sent.
func TestUnfinishedEvaluationStillFollowsReadiness(t *testing.T) {
	t.Run("omitted from a requeued receipt is unavailable", func(t *testing.T) {
		rf := newReceivingFixture(t)
		receipt := rf.claimRequeued()
		seam := rf.script(metadata.FetchAbsent, receipt)

		require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false))

		require.Equal(t, int32(1), seam.fetches.Load())
		require.Equal(t, "unavailable_on_youtube", rf.scalar(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, rf.recvRun, receipt.VideoID))
		require.Zero(t, rf.posts.Load())
	})
	t.Run("fetch failure releases unchanged", func(t *testing.T) {
		rf := newReceivingFixture(t)
		receipt := rf.claimNew(1)[0]
		seam := rf.script(metadata.FetchFailed, receipt)

		require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false))

		require.Equal(t, int32(1), seam.fetches.Load())
		require.Equal(t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, receipt.Deferred[0].ID))
		require.Zero(t, rf.posts.Load())
		require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM route_decision WHERE video_id=?`, receipt.VideoID))
	})
	t.Run("a persisted prefix of decisions is unfinished", func(t *testing.T) {
		rf := newReceivingFixture(t)
		receipt := rf.claimNew(1)[0]
		// The receiver was interrupted while evaluating, after it persisted the
		// decision of its first route: the state is still evaluating.
		_, err := rf.machine().TransitionVideoState(context.Background(), receipt.VideoID, state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
		require.NoError(t, err)
		createRouteDecision(t, context.Background(), rf.state, receipt.VideoID, rf.recvRun, rf.routeID, 1, state.RouteDecisionMatched)
		seam := rf.script(metadata.FetchFailed, receipt)

		require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false))

		require.Equal(t, int32(1), seam.fetches.Load(), "an unfinished evaluation needs real inputs")
		require.Equal(t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, receipt.Deferred[0].ID))
		require.Zero(t, rf.posts.Load())
	})
}

// A saved decision does not bypass a stop in force: an authorization stop
// still settles it as authorization_pending, with no send.
func TestSavedDecisionStillHonoursAnAuthorizationStop(t *testing.T) {
	rf := newReceivingFixture(t)
	receipt := rf.claimNew(1)[0]
	rf.decide(receipt) // the real metadata manager stays in place
	_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
	require.NoError(t, err)

	require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, []queue.RetryGroupReceipt{receipt}, false))

	require.Zero(t, rf.posts.Load())
	require.Zero(t, rf.listCalls.Load())
	require.Zero(t, rf.renewals.Load())
	require.Equal(t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, receipt.Deferred[0].ID))
}

func (rf *receivingFixture) machine() *state.Machine { return state.NewMachine(rf.state) }
