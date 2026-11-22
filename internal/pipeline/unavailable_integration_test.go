//go:build integration

package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// D8 (ARS-005 section 15): a re-queued partly done video whose metadata has
// expired is not fetched, not committed unavailable_on_youtube, and continues
// only its missing addition under its original decision. The re-queue is
// represented by the item's reason; the provider's videos.list would answer
// with no items, so any fetch would make the video look absent.
func TestRequeuedPartlyDoneVideoIsNeverUnavailableD8(t *testing.T) {
	e := newContEnv(t)
	r0, video, studyDecision := e.partlyDone(t)
	_, err := e.pipeline.db.Exec(`UPDATE deferred_item SET deferral_reason='requeued_by_user' WHERE video_id=? AND status='pending'`, video)
	require.NoError(t, err)
	_, err = e.pipeline.db.Exec(`DELETE FROM transient_metadata`)
	require.NoError(t, err)
	requests := len(e.server.log)

	r1 := e.scheduled(t)

	for _, r := range e.server.log[requests:] {
		require.NotContains(t, r.Path, "/videos", "a partly done video is not fetched")
	}
	require.Zero(t, e.one(t, `SELECT COUNT(*) FROM run_video_outcome WHERE video_id=? AND final_outcome='unavailable_on_youtube'`, video))
	require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
	require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, r1, video))
	require.Equal(t, studyDecision, e.one(t, `SELECT route_decision_id FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=? AND outcome='succeeded'`, r1, video, contStudy),
		"only the missing addition is sent, under run %d's decision", r0)
	require.Equal(t, 1, e.attemptsAt(contLofi), "the first send is never repeated")
}

// D9 (ARS-005 section 15): a re-queued item whose video became final after the
// confirm is closed at the claim: no fetch, no run outcome, and the video's
// final outcome is untouched.
func TestRequeuedItemOfVideoThatBecameFinalIsClosedD9(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, nil)
	p := env.pipeline
	qs, err := queue.NewStore(p.db)
	require.NoError(t, err)
	p.queueStore = qs
	seeds := seedFinalVideos(t, env, qs, false)
	for _, s := range seeds {
		_, err := p.db.Exec(`UPDATE deferred_item SET deferral_reason='requeued_by_user' WHERE id=?`, s.item)
		require.NoError(t, err)
	}
	requests := len(env.server.log)

	run, err := p.StartRun(ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	p.Execute(ctx, run.ID)

	for _, r := range env.server.log[requests:] {
		require.NotContains(t, r.Path, "/videos", "a final video is not fetched")
	}
	for _, s := range seeds {
		got := snapshotFinal(t, env, s)
		require.Equal(t, "completed/"+s.outcome, got.video, "the final outcome is unchanged")
		require.Equal(t, "completed/final_outcome/0", got.item, "the item is closed at the claim")
		var members int
		require.NoError(t, p.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=?`, run.ID, s.video).Scan(&members))
		require.Zero(t, members, "no outcome for this run")
	}
}
