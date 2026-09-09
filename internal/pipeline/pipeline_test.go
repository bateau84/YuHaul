package pipeline

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func setupPipelineTest(t *testing.T) (*sql.DB, *state.Store, *state.Machine, *Broadcaster, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	machine := state.NewMachine(stateStore)
	broadcast := NewBroadcaster()

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, stateStore, machine, broadcast, cleanup
}

func TestPipeline_StartRun(t *testing.T) {
	ctx := context.Background()
	_, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	p := &Pipeline{
		stateStore: stateStore,
		machine:    machine,
		broadcast:  broadcast,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// In a fresh database the first run is ID 1, so subscribe before creating it.
	sub := broadcast.Subscribe(1)
	defer broadcast.Unsubscribe(1, sub)

	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	assert.Equal(t, int64(1), run.ID)
	assert.Equal(t, state.RunStatusRunning, run.Status)

	select {
	case ev := <-sub:
		assert.Equal(t, EventRunStarted, ev.Type)
		assert.Equal(t, run.ID, ev.RunID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for start event")
	}
}

func TestPipeline_StartRun_UnsupportedType(t *testing.T) {
	ctx := context.Background()
	_, stateStore, machine, _, cleanup := setupPipelineTest(t)
	defer cleanup()

	p := &Pipeline{
		stateStore: stateStore,
		machine:    machine,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	_, err := p.StartRun(ctx, state.RunTypeDryRun)
	require.Error(t, err)
}

func TestPipeline_StartRun_LinksConfigVersion(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	versioner := config.NewService(routerStore, cfgStore)

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Linked",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
	})
	require.NoError(t, err)

	p := &Pipeline{
		stateStore: stateStore,
		machine:    machine,
		broadcast:  broadcast,
		versioner:  versioner,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.NotNil(t, run.ConfigVersionID)

	loaded, err := stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.ConfigVersionID)
	assert.Equal(t, *run.ConfigVersionID, *loaded.ConfigVersionID)

	cv, err := cfgStore.Get(ctx, *loaded.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cv.Snapshot.Routes, 1)
	assert.Equal(t, "Linked", cv.Snapshot.Routes[0].Name)
}

func TestPipeline_ResumeFromActionExecutionSkipsEarlierPhases(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	// Set up a route with an action.
	routerStore := router.NewStore(db)
	routeID, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "ResumeRoute",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
		 VALUES (?, 'playlist_insert', 'PLresume', 0);`, routeID)
	require.NoError(t, err)

	// Create an interrupted run with a video and a matched route decision.
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeScheduled, Status: state.RunStatusInterrupted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`UPDATE run SET current_phase = ? WHERE id = ?;`, string(PhaseActionExecution), runID)
	require.NoError(t, err)

	video, err := machine.DiscoverVideo(ctx, runID, "resume_vid", "resume_ch")
	require.NoError(t, err)
	_, err = machine.RecordRouteDecision(ctx, &state.RouteDecision{
		VideoID:         video.ID,
		RunID:           runID,
		RouteID:         routeID,
		EvaluationOrder: 1,
		Decision:        state.RouteDecisionMatched,
	})
	require.NoError(t, err)
	// Simulate that routing completed in the interrupted run.
	_, err = machine.TransitionVideoState(ctx, video.ID,
		state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	_, err = machine.TransitionVideoState(ctx, video.ID,
		state.VideoStateEvaluating, state.VideoStateEvaluated)
	require.NoError(t, err)

	inserter := &recordingInserter{}
	ledger := quota.New(db)
	exec := newTestExecutor(t, db, ledger, inserter)

	queueStore, err := queue.NewStore(db)
	require.NoError(t, err)

	p := NewPipeline(db, machine, stateStore, nil, nil, nil, exec, ledger, nil, nil, broadcast,
		WithQueueStore(queueStore),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	p.Execute(ctx, runID)

	loaded, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)

	actions, err := stateStore.GetExecutedActionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeSucceeded, actions[0].Outcome)

	assert.Len(t, inserter.calls, 1)
	assert.Equal(t, "PLresume", inserter.calls[0].PlaylistID)
}

func TestPipeline_DeferredItemsProcessedBeforeDiscovery(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	// Set up a route with an action.
	routerStore := router.NewStore(db)
	routeID, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "DeferRoute",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
		 VALUES (?, 'playlist_insert', 'PLdefer', 0);`, routeID)
	require.NoError(t, err)

	// Create run1 with a matched video and a deferred item.
	run1 := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeScheduled, Status: state.RunStatusCompleted}
	run1ID, err := stateStore.CreateRun(ctx, run1)
	require.NoError(t, err)

	video, err := machine.DiscoverVideo(ctx, run1ID, "defer_vid", "defer_ch")
	require.NoError(t, err)
	_, err = machine.RecordRouteDecision(ctx, &state.RouteDecision{
		VideoID:         video.ID,
		RunID:           run1ID,
		RouteID:         routeID,
		EvaluationOrder: 1,
		Decision:        state.RouteDecisionMatched,
	})
	require.NoError(t, err)
	// Simulate that routing completed and the video is awaiting action execution.
	_, err = machine.TransitionVideoState(ctx, video.ID,
		state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	_, err = machine.TransitionVideoState(ctx, video.ID,
		state.VideoStateEvaluating, state.VideoStateEvaluated)
	require.NoError(t, err)

	queueStore, err := queue.NewStore(db)
	require.NoError(t, err)
	_, err = queueStore.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID:   video.ID,
		RunID:     run1ID,
		Reason:    queue.DeferredReasonQuotaExhausted,
		Status:    queue.DeferredStatusPending,
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	// Create run2 (scheduled) that will process the deferred item.
	run2 := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeScheduled, Status: state.RunStatusRunning}
	run2ID, err := stateStore.CreateRun(ctx, run2)
	require.NoError(t, err)

	inserter := &recordingInserter{}
	ledger := quota.New(db)
	exec := newTestExecutor(t, db, ledger, inserter)

	p := NewPipeline(db, machine, stateStore, nil, nil, nil, exec, ledger, nil, nil, broadcast,
		WithQueueStore(queueStore),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	p.Execute(ctx, run2ID)

	loaded, err := stateStore.GetRun(ctx, run2ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)

	// The deferred item should be completed and the action executed.
	items, err := queueStore.ListDeferredItems(ctx, "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, queue.DeferredStatusCompleted, items[0].Status)

	actions, err := stateStore.GetExecutedActionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeSucceeded, actions[0].Outcome)

	assert.Len(t, inserter.calls, 1)
	assert.Equal(t, "PLdefer", inserter.calls[0].PlaylistID)
}

var _ = io.Discard
