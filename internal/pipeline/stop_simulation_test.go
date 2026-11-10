package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

// S1 for simulations (run-initiation ARS-003 §7.4 test 13): a dry run, a replay
// and a backtest each end `stopped` with the stop reason, write no queue, guard
// or action row, and take their counters from decisions. The stop is accepted
// while the run is registered but not yet executing (the pre-attach case, S4).
func TestStopSimulationRunsEndStopped(t *testing.T) {
	for _, mode := range []string{"dry_run", "replay", "backtest"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			deps, cleanup := setupReplayPipeline(t)
			defer cleanup()
			createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
			prodRunID, err := deps.stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
			require.NoError(t, err)
			video := createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")
			_, err = deps.db.Exec(`UPDATE video SET processing_state='not_yet_evaluated' WHERE id=?`, video.ID)
			require.NoError(t, err)

			var run *state.Run
			switch mode {
			case "dry_run":
				run, err = deps.pipeline.DryRun(ctx, nil)
			case "replay":
				run, err = deps.pipeline.Replay(ctx, nil, state.PublishedRange{})
			default:
				run, err = deps.pipeline.Backtest(ctx, proposalSnapshot(proposalRoute(proposalTestUUID, "first", "jazz")), state.PublishedRange{})
			}
			require.NoError(t, err)

			tables := func() int {
				n := 0
				for _, tbl := range []string{"deferred_item", "error_entry", "playlist_send_guard", "executed_action"} {
					var c int
					require.NoError(t, deps.db.QueryRow(`SELECT COUNT(*) FROM `+tbl).Scan(&c))
					n += c
				}
				return n
			}
			before := tables()

			res, err := deps.pipeline.StopRun(ctx, run.ID)
			require.NoError(t, err)
			require.Equal(t, StopAccepted, res.Outcome)
			again, err := deps.pipeline.StopRun(ctx, run.ID)
			require.NoError(t, err)
			require.Equal(t, StopAlreadyStopping, again.Outcome)

			switch mode {
			case "dry_run":
				deps.pipeline.ExecuteDryRun(ctx, run.ID)
			case "replay":
				deps.pipeline.ExecuteReplay(ctx, run.ID)
			default:
				deps.pipeline.ExecuteBacktest(ctx, run.ID)
			}

			got, err := deps.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			require.Equal(t, state.RunStatusStopped, got.Status)
			require.NotNil(t, got.InterruptReason)
			require.Equal(t, stopReason, *got.InterruptReason)
			require.NotNil(t, got.StopRequestedAt)
			require.NotNil(t, got.CompletedAt)
			require.Nil(t, got.InterruptCause)
			require.Zero(t, got.VideosAccepted+got.VideosRejected+got.VideosDiscovered, "counters come from decisions, and none were written")
			require.Equal(t, before, tables(), "a simulation stop writes no queue, guard or action row")

			// Re-entry on a stopped run is rejected; nothing changes.
			deps.pipeline.ExecuteReplay(ctx, run.ID)
			re, err := deps.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			require.Equal(t, got.Status, re.Status)

			// A stop after settling is refused.
			_, err = deps.pipeline.StopRun(ctx, run.ID)
			var refused *StopRefusedError
			require.True(t, errors.As(err, &refused))
			require.Equal(t, StopRunEnded, refused.Code)
			require.Equal(t, "stopped", refused.Status)
		})
	}
}

// StopRun's refusals for runs that are not executing.
func TestStopRunRefusals(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	_, err := deps.pipeline.StopRun(ctx, 9999)
	var refused *StopRefusedError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, StopRunNotFound, refused.Code)

	for _, status := range []state.RunStatus{state.RunStatusCompleted, state.RunStatusFailed} {
		id, err := deps.stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
		require.NoError(t, err)
		_, err = deps.db.Exec(`UPDATE run SET status=?, completed_at='2026-01-01T00:00:00Z' WHERE id=?`, string(status), id)
		require.NoError(t, err)
		_, err = deps.pipeline.StopRun(ctx, id)
		require.ErrorAs(t, err, &refused)
		require.Equal(t, StopRunEnded, refused.Code)
		require.Equal(t, string(status), refused.Status)
	}

	// Running in the database but not executing in this process.
	id, err := deps.stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	_, err = deps.pipeline.StopRun(ctx, id)
	require.ErrorAs(t, err, &refused)
	require.Equal(t, StopRunNotActive, refused.Code)
	require.Equal(t, StopReasonNotExecuting, refused.Reason)

	// An executing run that is already ending.
	ctl := deps.pipeline.registerRun(id)
	require.True(t, ctl.beginFinalize())
	_, err = deps.pipeline.StopRun(ctx, id)
	require.ErrorAs(t, err, &refused)
	require.Equal(t, StopReasonEnding, refused.Reason)
	deps.pipeline.unregisterRun(id)
	var marked int
	require.NoError(t, deps.db.QueryRow(`SELECT COUNT(stop_requested_at) FROM run WHERE id=?`, id).Scan(&marked))
	require.Zero(t, marked, "a refused stop writes no marker")
}
