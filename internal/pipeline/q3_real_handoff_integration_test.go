//go:build integration

package pipeline

import (
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Q3 with handoffs made by the real Stop and Discard paths (run-initiation
// ARS-003 §7.3 Q3, ARS-005 §11.6 B3/B8). TestQ3_ResumeDoesNotClaimHandedOverItems
// seeds the unrelated handoff H as a final row; this test produces H through
// StopRun and DiscardRun, so the reason, retry_after, owner and origin run come
// from the product's own transactions.
//
// Run R is a real Run Now run interrupted at its time limit. It holds one item
// X by exact receipt (claimed through the real batch claim, as in the seeded
// test: a run interrupted inside batch 2 cannot be staged here). H exists only
// after X is held, so R can never have been given H. Run Now then resumes R;
// R must settle X and leave H untouched, and a fresh run must claim H.
//
// Stop: R is the only candidate. A scheduled run P discovers its own videos
// while R waits, and a real Stop of P hands over P's unfinished videos.
//
// Discard: ARS-003 Discard ends only the *oldest* Run Now candidate that Run
// Now cannot resume, and the product lifecycle makes at most one such candidate
// (a press resumes it instead of creating another). So the older P and the
// younger R cannot both come from presses alone. The fixture creates P and R
// through real presses, hiding P from candidate selection (interrupt_cause
// shutdown) only while R is created, then restores P and gives it an
// unrecognized checkpoint. The Discard transaction itself is the real one.

const (
	q3ViaStop    = "stop"
	q3ViaDiscard = "discard"
)

// useVideos replaces the endpoint's playlist with Jazz videos of the given IDs.
// Discovery keeps a per-channel watermark of the earlier run, so the videos are
// published after it.
func (e *runNowEnv) useVideos(ids ...string) {
	var videos []mockVideo
	for i, id := range ids {
		v := makeJazzVideo(id, time.Now().UTC().Add(time.Duration(i+1)*time.Second))
		v.Title = "Jazz Session " + id
		videos = append(videos, v)
	}
	e.server.mu.Lock()
	e.server.videos = videos
	e.server.mu.Unlock()
}

// holdByReceipt claims one fresh pending item for the interrupted run through
// the real batch claim and returns its video and item IDs. The run row is
// running only for the claim, as an interrupted run cannot claim.
func (e *runNowEnv) holdByReceipt(t *testing.T, runID int64, externalID string) (video, item int64) {
	t.Helper()
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	video = e.seedVideo(t, externalID, runID)
	item = e.seedPendingItem(t, video, runID, queue.DeferredReasonQuotaExhausted)
	var claim queue.BatchClaim
	e.setRunStatus(t, runID, state.RunStatusRunning)
	require.NoError(t, qs.WithRetryTx(e.ctx, func(tx *sql.Tx) error {
		items, err := qs.ListDeferredRetryCandidatesForRun(e.ctx, time.Now(), runID, retryClaimBatch)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.ID == item {
				claim, err = qs.ClaimDeferredBatchTx(e.ctx, tx, runID, []queue.DeferredItem{it}, time.Now())
				return err
			}
		}
		return fmt.Errorf("item %d is not a candidate", item)
	}))
	e.setRunStatus(t, runID, state.RunStatusInterrupted)
	require.Len(t, claim.Receipts, 1, "the run holds %s", externalID)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE id=? AND status='retrying' AND retry_run_id=?`, item, runID))
	return video, item
}

// eligibleVideos lists the external IDs a new run could claim right now.
func (e *runNowEnv) eligibleVideos(t *testing.T) []string {
	t.Helper()
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	items, err := qs.ListDeferredRetryCandidates(e.ctx, time.Now())
	require.NoError(t, err)
	var out []string
	for _, it := range items {
		out = append(out, it.ExternalVideoID)
	}
	sort.Strings(out)
	return out
}

// stopHandoff leaves R interrupted at its limit holding X (V videos), then
// creates H by a real Stop of the scheduled run P (U videos).
func (e *runNowEnv) stopHandoff(t *testing.T) (r int64, h []string, reason queue.DeferredReason) {
	t.Helper()
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit)
	r = first.RunID()
	e.holdByReceipt(t, r, "X-held")

	e.useVideos("U1", "U2", "U3")
	e.stallOn("U2")
	p, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	done := startExecution(func() { e.pipeline.Execute(e.ctx, p.ID) })
	require.Equal(t, "U2", e.waitBlocked(t))
	e.stop(t, p.ID, StopAccepted)
	waitDone(t, done)
	e.assertStopped(t, p.ID)
	require.Equal(t, map[string]int{"U2": 1, "U3": 1}, e.pendingHandoffs(t, "stopped_by_user"), "the real Stop handed over P's unfinished videos")
	require.Equal(t, 1, e.postsFor("U1"), "U1 was sent before the Stop")
	return r, []string{"U2", "U3"}, queue.DeferredReasonStoppedByUser
}

// discardHandoff creates the older P and the younger R through real presses,
// holds X in R, then creates H by a real Discard of P.
func (e *runNowEnv) discardHandoff(t *testing.T) (r int64, h []string, reason queue.DeferredReason) {
	t.Helper()
	e.stallOn("V2")
	p := e.interruptedAtLimit(t, testLimit)

	// Hide P from candidate selection only while R is created.
	_, err := e.db().ExecContext(e.ctx, `UPDATE run SET interrupt_cause='shutdown' WHERE id=?`, p.RunID())
	require.NoError(t, err)
	e.useVideos("U1", "U2", "U3")
	e.stallOn("U2")
	second := e.interruptedAtLimit(t, testLimit)
	require.False(t, second.Resumed())
	require.NotEqual(t, p.RunID(), second.RunID())
	r = second.RunID()
	_, err = e.db().ExecContext(e.ctx, `UPDATE run SET interrupt_cause='time_limit', current_phase='bogus' WHERE id=?`, p.RunID())
	require.NoError(t, err)
	e.holdByReceipt(t, r, "X-held")

	// P is the oldest candidate and unusable: Run Now refuses it, changing
	// nothing, and Discard is the only way on.
	oldest, cause, ok, err := e.pipeline.DiscardableRun(e.ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, p.RunID(), oldest)
	require.Equal(t, DiscardCauseCheckpointUnrecognized, cause)
	_, err = e.pipeline.AdmitRunNow(e.ctx)
	var unavailable *ResumeUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, p.RunID(), unavailable.RunID)

	res, err := e.pipeline.DiscardRun(e.ctx, p.RunID())
	require.NoError(t, err)
	require.Equal(t, DiscardCauseCheckpointUnrecognized, res.Cause)
	require.Equal(t, state.RunStatusFailed, e.run(t, p.RunID()).Status)
	require.Equal(t, map[string]int{"V2": 1, "V3": 1}, e.pendingHandoffs(t, "discarded_by_user"), "the real Discard handed over P's unfinished videos")
	return r, []string{"V2", "V3"}, queue.DeferredReasonDiscardedByUser
}

// q3HandoffByRealPath is the shared proof; via selects the producer of H.
func q3HandoffByRealPath(t *testing.T, via, next string) {
	e := newRunNowEnv(t)
	var (
		r      int64
		h      []string
		reason queue.DeferredReason
	)
	switch via {
	case q3ViaStop:
		r, h, reason = e.stopHandoff(t)
	case q3ViaDiscard:
		r, h, reason = e.discardHandoff(t)
	default:
		t.Fatalf("unknown handoff producer %q", via)
	}

	// X is R's own held work and is not claimable; exactly H is.
	xVideo, err := e.stateStore.GetVideoByExternalID(e.ctx, "X-held")
	require.NoError(t, err)
	require.Equal(t, h, e.eligibleVideos(t), "H is genuinely eligible and X is not")
	hRows := e.rowDump(t, `SELECT * FROM deferred_item WHERE deferral_reason=? ORDER BY id`, string(reason))
	require.Equal(t, 2, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason=? AND status='pending' AND retry_after IS NULL AND retry_run_id IS NULL`, string(reason)))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='retrying' AND retry_run_id=?`, xVideo.ID, r))
	hPosts := map[string]int{}
	for _, id := range h {
		hPosts[id] = e.postsFor(id)
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE o.run_id=? AND v.video_id=?`, r, id), "R has no tie to %s", id)
	}

	// Run Now resumes R with a scripted readiness seam: the run-now
	// environment has no metadata fetcher, so X cannot settle without it.
	e.stallOn()
	orig := e.pipeline.meta
	probe := &batchProbe{status: metadata.FetchUnusable}
	e.pipeline.meta = probe
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, adm.Resumed())
	require.Equal(t, r, adm.RunID(), "same run id")
	adm.Execute(e.ctx)
	e.pipeline.meta = orig
	got := e.run(t, r)
	require.Equal(t, state.RunStatusCompleted, got.Status, "the resumed run finished: %v", str(got.InterruptReason))
	require.Equal(t, []int{1}, probe.passes, "the resumed run's one metadata pass covered only X")

	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=?`, r, xVideo.ID), "X settled under R's ownership")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='retrying'`, xVideo.ID), "X is no longer held")
	require.Equal(t, hRows, e.rowDump(t, `SELECT * FROM deferred_item WHERE deferral_reason=? ORDER BY id`, string(reason)), "H is byte-identical")
	// X's readiness was unusable, so R released it unchanged: it is claimable
	// again, next to the untouched H.
	require.Equal(t, append(append([]string(nil), h...), "X-held"), e.eligibleVideos(t), "H is still eligible after the resume")
	for _, id := range h {
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE o.run_id=? AND v.video_id=?`, r, id), "R took no membership of %s", id)
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE d.retry_run_id=? AND v.video_id=?`, r, id), "R holds no receipt for %s", id)
		require.Equal(t, hPosts[id], e.postsFor(id), "the resume sent nothing for %s", id)
	}

	// X was only R's fixture: with no metadata fetcher in this environment a
	// fresh run cannot evaluate it. Remove it so the control isolates H.
	_, err = e.db().ExecContext(e.ctx, `DELETE FROM deferred_item WHERE video_id=?`, xVideo.ID)
	require.NoError(t, err)
	require.Equal(t, h, e.eligibleVideos(t), "only H remains for the fresh run")

	// A fresh run claims H once. Discovery returns none of its videos, so the
	// claim is the only way H can reach the run.
	e.useVideos()
	var nextID int64
	switch next {
	case "scheduled":
		run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
		require.NoError(t, err)
		nextID = run.ID
		e.pipeline.Execute(e.ctx, nextID)
	case "run_now":
		a, err := e.pipeline.AdmitRunNow(e.ctx)
		require.NoError(t, err)
		require.False(t, a.Resumed(), "no resume candidate remains")
		nextID = a.RunID()
		a.Execute(e.ctx)
	default:
		t.Fatalf("unknown next run %q", next)
	}
	require.NotEqual(t, r, nextID)
	fresh := e.run(t, nextID)
	require.Equal(t, state.RunStatusCompleted, fresh.Status, "reason: %v", str(fresh.InterruptReason))
	for _, id := range h {
		require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE o.run_id=? AND v.video_id=?`, nextID, id), "the fresh run claims %s once", id)
	}
	require.Equal(t, hPosts[h[0]], e.postsFor(h[0]), "zero POSTs to the uncertain target")
	require.Equal(t, hPosts[h[1]]+1, e.postsFor(h[1]), "the formerly unsent target is sent once")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying'`), "no claim is left held")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason=? AND status='pending'`, string(reason)), "no H item is left pending")
}

func TestQ3_ResumeLeavesRealStopAndDiscardHandoffsToFreshRun(t *testing.T) {
	for _, via := range []string{q3ViaStop, q3ViaDiscard} {
		for _, next := range []string{"scheduled", "run_now"} {
			t.Run(via+"/next_"+next, func(t *testing.T) { q3HandoffByRealPath(t, via, next) })
		}
	}
}
