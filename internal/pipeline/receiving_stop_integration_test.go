//go:build integration

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

// S4 (partly-done control, whole pipeline): a partly-done video claimed by a
// run that holds a stored authorization stop is neither hydrated nor sent. Only
// its outstanding work is retained, with the original queue reason, age and
// lineage; the first send is untouched and no guard is prepared.
func TestPartlyDoneUnderAuthorizationStopRetainsOnlyOutstandingWork(t *testing.T) {
	e := newContEnv(t)
	_, video, _ := e.partlyDone(t)
	var itemID int64
	var reason, created string
	require.NoError(t, e.pipeline.db.QueryRow(`SELECT id, deferral_reason, created_at FROM deferred_item WHERE video_id=? AND status='pending'`, video).Scan(&itemID, &reason, &created))
	insertions, requests, guards := len(e.server.Insertions()), len(e.server.log), e.one(t, `SELECT COUNT(*) FROM playlist_send_guard`)

	run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	require.NotZero(t, e.one(t, `SELECT COUNT(*) FROM deferred_item WHERE id=? AND status='retrying' AND retry_run_id=?`, itemID, run.ID), "the run claimed the item at creation")
	_, err = e.pipeline.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, run.ID)
	require.NoError(t, err)
	e.pipeline.Execute(e.ctx, run.ID)

	require.Len(t, e.server.Insertions(), insertions, "nothing is sent under the stop")
	for _, r := range e.server.log[requests:] {
		require.NotContains(t, r.Path, "/videos", "partly-done work is not hydrated")
		require.NotContains(t, r.Path, "/playlistItems", "and no send is attempted")
	}
	require.Equal(t, guards, e.one(t, `SELECT COUNT(*) FROM playlist_send_guard`), "no guard is prepared")
	require.Zero(t, e.one(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=?`, run.ID, video), "the video is not re-evaluated")

	require.Equal(t, "deferred", e.text(t, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, run.ID, video))
	require.Equal(t, "authorization_pending", e.text(t, `SELECT deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, run.ID, video))
	require.Equal(t, "pending", e.text(t, `SELECT status FROM deferred_item WHERE id=?`, itemID))
	require.Equal(t, reason, e.text(t, `SELECT deferral_reason FROM deferred_item WHERE id=?`, itemID), "the original queue reason is kept")
	require.Equal(t, created, e.text(t, `SELECT created_at FROM deferred_item WHERE id=?`, itemID), "and its age")
	class, _, err := state.ClassifyVideoTx(e.ctx, e.pipeline.db, video)
	require.NoError(t, err)
	require.Equal(t, state.VideoClassPartlyDone, class, "the video stays partly done: the first success is untouched")
	require.Equal(t, int64(1), e.one(t, `SELECT COUNT(*) FROM executed_action WHERE video_id=? AND outcome='succeeded'`, video))
}
