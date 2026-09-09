package pipeline

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

func setupReplayPipeline(t *testing.T) (*replayDeps, func()) {
	t.Helper()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	versioner := config.NewService(routerStore, cfgStore)
	metaStore := metadata.NewStore(db)
	metaMgr := metadata.NewManager(metaStore, nil, nil)

	p := &Pipeline{
		db:          db,
		machine:     machine,
		stateStore:  stateStore,
		detector:    nil,
		router:      router.NewEngine(routerStore),
		broadcast:   broadcast,
		versioner:   versioner,
		configStore: cfgStore,
		meta:        metaMgr,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	return &replayDeps{
		db:          db,
		stateStore:  stateStore,
		machine:     machine,
		pipeline:    p,
		routerStore: routerStore,
		metaStore:   metaStore,
	}, cleanup
}

type replayDeps struct {
	db          *sql.DB
	stateStore  *state.Store
	machine     *state.Machine
	pipeline    *Pipeline
	routerStore *router.Store
	metaStore   *metadata.Store
}

func createReplayRouteWithAction(t *testing.T, ctx context.Context, deps *replayDeps, name, value, playlistID string) int64 {
	t.Helper()
	routeID, err := deps.routerStore.CreateRoute(ctx, &router.Route{
		Name:                 name,
		EvaluationOrder:      1,
		IsEnabled:            true,
		IsFinal:              true,
		ConditionComposition: "AND",
		Conditions: []router.RouteCondition{
			{
				ConditionIndex: 0,
				Field:          router.ConditionFieldTitle,
				Operator:       router.ConditionOperatorContains,
				Value:          value,
			},
		},
		Actions: []router.RouteAction{
			{
				ActionIndex:      0,
				ActionType:       "playlist_insert",
				TargetPlaylistID: playlistID,
			},
		},
	})
	require.NoError(t, err)
	return routeID
}

func createRetainedVideo(t *testing.T, ctx context.Context, deps *replayDeps, runID int64, videoID, channelID, title string) *state.Video {
	t.Helper()
	video, err := deps.machine.DiscoverVideo(ctx, runID, videoID, channelID)
	require.NoError(t, err)
	if title != "" {
		now := time.Now().UTC()
		err := deps.metaStore.Upsert(ctx, metadata.TransientMetadata{
			VideoID:   videoID,
			Title:     &title,
			FetchedAt: now,
			ExpiresAt: now.Add(30 * 24 * time.Hour),
			IsActive:  true,
		})
		require.NoError(t, err)
	}
	return video
}

func TestReplay_EvaluatesRetainedVideosWithoutMutations(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	routeID := createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, state.RunTypeReplay, run.RunType)

	deps.pipeline.ExecuteReplay(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	assert.Equal(t, 1, loaded.VideosDiscovered)
	assert.Equal(t, 1, loaded.VideosAccepted)
	assert.Equal(t, 0, loaded.VideosRejected)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	assert.Equal(t, routeID, decisions[0].RouteID)

	actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
	assert.Equal(t, "PLjazz", actions[0].TargetPlaylistID)
	assert.Equal(t, run.ID, actions[0].RunID)

	videoAfter, err := deps.stateStore.GetVideo(ctx, video.ID)
	require.NoError(t, err)
	assert.Equal(t, state.VideoStateNotYetEvaluated, videoAfter.ProcessingState)
	assert.Equal(t, state.FinalOutcomeNotYetEvaluated, videoAfter.FinalOutcome)
}

func TestReplay_UsesAllRetainedVideosNotJustPending(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	pending := createRetainedVideo(t, ctx, deps, prodRunID, "vid_pending", "ch1", "cool jazz live")
	completed := createRetainedVideo(t, ctx, deps, prodRunID, "vid_completed", "ch1", "more jazz live")
	_, err = deps.machine.TransitionVideoState(ctx, completed.ID,
		state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	_, err = deps.machine.TransitionVideoState(ctx, completed.ID,
		state.VideoStateEvaluating, state.VideoStateEvaluated)
	require.NoError(t, err)
	_, err = deps.machine.TransitionVideoState(ctx, completed.ID,
		state.VideoStateEvaluated, state.VideoStateActed)
	require.NoError(t, err)
	_, err = deps.machine.TransitionVideoState(ctx, completed.ID,
		state.VideoStateActed, state.VideoStateCompleted,
		state.WithFinalOutcome(state.FinalOutcomeAccepted))
	require.NoError(t, err)

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	pendingDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, pending.ID, run.ID)
	require.NoError(t, err)
	assert.Len(t, pendingDecisions, 1)

	completedDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, completed.ID, run.ID)
	require.NoError(t, err)
	assert.Len(t, completedDecisions, 1)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, loaded.VideosDiscovered)
	assert.Equal(t, 2, loaded.VideosAccepted)
}

func TestReplay_DoesNotChangeDuplicateReferenceCorpus(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")
	_, err = deps.db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference)
		 VALUES (?, 'fp', 'fp', ?, 'api_discovery', 1);`, video.VideoID, video.ChannelID)
	require.NoError(t, err)

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	var isRef int
	err = deps.db.QueryRowContext(ctx,
		`SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = ?;`, video.VideoID).Scan(&isRef)
	require.NoError(t, err)
	assert.Equal(t, 1, isRef, "production duplicate-reference corpus must not change")
}

func TestReplay_NoSucceededActions(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	rows, err := deps.db.QueryContext(ctx,
		`SELECT outcome FROM executed_action WHERE run_id = ? AND video_id = ?;`, run.ID, video.ID)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var outcome string
		require.NoError(t, rows.Scan(&outcome))
		assert.NotEqual(t, string(state.ActionOutcomeSucceeded), outcome)
	}
	require.NoError(t, rows.Err())
}

func TestBacktest_UsesProposedConfig(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	activeRouteID := createReplayRouteWithAction(t, ctx, deps, "Rock", "rock", "PLrock")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createRetainedVideo(t, ctx, deps, prodRunID, "vid_jazz", "ch1", "cool jazz live")

	proposed := config.Snapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes: []config.SnapshotRoute{
			{
				ID:                   activeRouteID,
				Name:                 "JazzProposed",
				EvaluationOrder:      1,
				IsFinal:              true,
				IsEnabled:            true,
				ConditionComposition: "AND",
				Conditions: []config.SnapshotCondition{
					{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "jazz"},
				},
				Actions: []config.SnapshotAction{
					{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"},
				},
			},
		},
	}

	run, err := deps.pipeline.Backtest(ctx, proposed)
	require.NoError(t, err)
	require.Equal(t, state.RunTypeBacktest, run.RunType)
	require.NotNil(t, run.ConfigVersionID)

	deps.pipeline.ExecuteBacktest(ctx, run.ID)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	assert.Equal(t, activeRouteID, decisions[0].RouteID)

	actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "PLjazz", actions[0].TargetPlaylistID)
	assert.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
	assert.Equal(t, run.ID, actions[0].RunID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.VideosDiscovered)
	assert.Equal(t, 1, loaded.VideosAccepted)
}

func TestReplay_DetectsDuplicatesAgainstRetainedCorpus(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	// Insert a duplicate-reference fingerprint with the same video_id.
	_, err = deps.db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference)
		 VALUES (?, 'fp', 'fp', ?, 'api_discovery', 1);`, video.VideoID, video.ChannelID)
	require.NoError(t, err)

	// Wire in the real detector now that the reference exists.
	deps.pipeline.detector = duplicate.NewDetector(deps.db)

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	assert.Empty(t, decisions)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, loaded.VideosDiscovered)
	assert.Equal(t, 0, loaded.VideosAccepted)
	assert.Equal(t, 0, loaded.VideosRejected)
}

func TestReplay_SkipsDuplicateDetectionWhenDetectorNil(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	deps.pipeline.detector = nil

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	assert.Len(t, decisions, 1)
}

func TestReplay_HistoricalConfigVersion(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	activeRouteID := createReplayRouteWithAction(t, ctx, deps, "Rock", "rock", "PLrock")

	historicalCfgID, err := deps.pipeline.configStore.Create(ctx, config.Snapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes: []config.SnapshotRoute{
			{
				ID:                   activeRouteID,
				Name:                 "JazzHistorical",
				EvaluationOrder:      1,
				IsFinal:              true,
				IsEnabled:            true,
				ConditionComposition: "AND",
				Conditions: []config.SnapshotCondition{
					{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "jazz"},
				},
				Actions: []config.SnapshotAction{
					{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"},
				},
			},
		},
	}, "historical config")
	require.NoError(t, err)

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createRetainedVideo(t, ctx, deps, prodRunID, "vid_jazz", "ch1", "cool jazz live")

	run, err := deps.pipeline.Replay(ctx, &historicalCfgID)
	require.NoError(t, err)
	require.Equal(t, state.RunTypeReplay, run.RunType)
	require.NotNil(t, run.ConfigVersionID)
	assert.Equal(t, historicalCfgID, *run.ConfigVersionID)

	deps.pipeline.ExecuteReplay(ctx, run.ID)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)

	actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "PLjazz", actions[0].TargetPlaylistID)
}

func TestReplay_UnknownConfigVersionReturnsError(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	unknownID := int64(9999)
	_, err := deps.pipeline.Replay(ctx, &unknownID)
	require.Error(t, err)
}

func TestBacktest_RequiresConfigStore(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	deps.pipeline.configStore = nil
	_, err := deps.pipeline.Backtest(ctx, config.Snapshot{})
	require.Error(t, err)
}

func TestBacktest_DoesNotChangeChannelCounters(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	_, err = deps.db.ExecContext(ctx,
		`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered, total_routed, total_retained)
		 VALUES (?, 'Channel One', ?, ?, 1, 0, 0);`,
		"ch1", time.Now().UTC(), time.Now().UTC())
	require.NoError(t, err)

	run, err := deps.pipeline.Backtest(ctx, config.Snapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes:             []config.SnapshotRoute{},
	})
	require.NoError(t, err)
	deps.pipeline.ExecuteBacktest(ctx, run.ID)

	var totalRouted int
	err = deps.db.QueryRowContext(ctx,
		`SELECT total_routed FROM channel WHERE channel_id = ?;`, "ch1").Scan(&totalRouted)
	require.NoError(t, err)
	assert.Equal(t, 0, totalRouted, "backtest must not increment production channel counters")
}
