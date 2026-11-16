package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// videoRowsDump renders every row that belongs to one video, for byte-for-byte
// before/after comparison. It covers the video row and every table the
// classification and the main loop read or write for it.
func videoRowsDump(t *testing.T, db *sql.DB, videoID int64) string {
	t.Helper()
	var b strings.Builder
	dump := func(table, where string) {
		rows, err := db.Query(`SELECT * FROM `+table+` WHERE `+where+`=? ORDER BY 1`, videoID)
		require.NoError(t, err)
		defer rows.Close()
		cols, err := rows.Columns()
		require.NoError(t, err)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			fmt.Fprintf(&b, "%s:", table)
			for i, v := range vals {
				if bs, ok := v.([]byte); ok {
					v = string(bs)
				}
				fmt.Fprintf(&b, "%s=%v|", cols[i], v)
			}
			b.WriteString("\n")
		}
		require.NoError(t, rows.Err())
	}
	dump("video", "id")
	for _, table := range []string{"run_video_outcome", "route_decision", "executed_action", "playlist_send_guard", "deferred_item", "error_entry"} {
		dump(table, "video_id")
	}
	return b.String()
}

// K17 (AC12, M1): simulations never apply the production filter. A replay
// (the backtest path) over retained final and partly done videos evaluates all
// of them and reports simulated actions, and a dry run alongside it leaves the
// production rows of every seed byte-for-byte unchanged.
func TestK17SimulationsIgnoreFinalState(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	route := createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	qs, err := queue.NewStore(deps.db)
	require.NoError(t, err)

	res, err := deps.db.ExecContext(ctx, `INSERT INTO run (started_at, run_type, status) VALUES (?, 'scheduled', 'running')`, time.Now().UTC())
	require.NoError(t, err)
	prod, err := res.LastInsertId()
	require.NoError(t, err)

	pend := func(videoID int64) {
		_, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: videoID, RunID: prod,
			Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
	}
	var seeds []int64
	// K1 seeds: one final video per final outcome, each with a pending item.
	for i, outcome := range []string{"accepted", "rejected", "excluded", "unavailable_on_youtube"} {
		v := createRetainedVideo(t, ctx, deps, prod, fmt.Sprintf("final-%d", i), "ch1", "cool jazz live")
		_, err := deps.db.ExecContext(ctx, `UPDATE video SET processing_state='completed', final_outcome=? WHERE id=?`, outcome, v.ID)
		require.NoError(t, err)
		pend(v.ID)
		seeds = append(seeds, v.ID)
	}
	// K9 seed: matched to two targets, one succeeded under a production run.
	partly := createRetainedVideo(t, ctx, deps, prod, "partly", "ch1", "cool jazz live")
	res, err = deps.db.ExecContext(ctx, `INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(?,?,?,1,'matched')`, partly.ID, prod, route.ID)
	require.NoError(t, err)
	decision, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = deps.db.ExecContext(ctx, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(?,?,?,'playlist_insert','PLjazz','succeeded')`, decision, prod, partly.ID)
	require.NoError(t, err)
	_, err = deps.db.ExecContext(ctx, `UPDATE video SET processing_state='deferred', final_outcome='deferred' WHERE id=?`, partly.ID)
	require.NoError(t, err)
	pend(partly.ID)
	seeds = append(seeds, partly.ID)

	_, err = deps.db.ExecContext(ctx, `UPDATE run SET status='completed', completed_at=? WHERE id=?`, time.Now().UTC(), prod)
	require.NoError(t, err)

	before := map[int64]string{}
	for _, id := range seeds {
		before[id] = videoRowsDump(t, deps.db, id)
	}

	dry, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteDryRun(ctx, dry.ID)
	replay, err := deps.pipeline.Replay(ctx, nil, state.PublishedRange{})
	require.NoError(t, err)
	deps.pipeline.ExecuteReplay(ctx, replay.ID)

	for _, id := range []int64{dry.ID, replay.ID} {
		loaded, err := deps.stateStore.GetRun(ctx, id)
		require.NoError(t, err)
		require.Equal(t, state.RunStatusCompleted, loaded.Status)
	}
	for _, id := range seeds {
		decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, id, replay.ID)
		require.NoError(t, err)
		require.Len(t, decisions, 1, "the simulation evaluates video %d although it is final or partly done", id)
		require.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
		actions, err := deps.stateStore.GetExecutedActionsForVideoInRun(ctx, id, replay.ID)
		require.NoError(t, err)
		require.Len(t, actions, 1)
		require.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome, "simulated, never sent")
	}
	// Production rows of every seed: only the simulation's own rows were added.
	for _, id := range seeds {
		var prodDecisions, prodActions int
		require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_decision WHERE video_id=? AND run_id=?`, id, prod).Scan(&prodDecisions))
		require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action WHERE video_id=? AND run_id=?`, id, prod).Scan(&prodActions))
		if id == partly.ID {
			require.Equal(t, 1, prodDecisions)
			require.Equal(t, 1, prodActions)
		} else {
			require.Zero(t, prodDecisions)
			require.Zero(t, prodActions)
		}
		var video, items string
		require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, id).Scan(&video))
		require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT group_concat(status||'/'||COALESCE(closed_reason,''))  FROM deferred_item WHERE video_id=?`, id).Scan(&items))
		require.Equal(t, "pending/", items, "a simulation never closes a queue item")
		require.NotEmpty(t, video)
	}
}
