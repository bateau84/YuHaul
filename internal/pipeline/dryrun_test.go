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
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

func setupDryRunPipeline(t *testing.T) (*dryRunDeps, func()) {
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
		router:      router.NewEngine(routerStore),
		broadcast:   broadcast,
		versioner:   versioner,
		configStore: cfgStore,
		meta:        metaMgr,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	return &dryRunDeps{db: db, stateStore: stateStore, machine: machine, pipeline: p, routerStore: routerStore, metaStore: metaStore}, cleanup
}

type dryRunDeps struct {
	db          *sql.DB
	stateStore  *state.Store
	machine     *state.Machine
	pipeline    *Pipeline
	routerStore *router.Store
	metaStore   *metadata.Store
}

func createPendingVideo(t *testing.T, ctx context.Context, deps *dryRunDeps, runID int64, videoID, channelID, title string) *state.Video {
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

func createRouteWithAction(t *testing.T, ctx context.Context, deps *dryRunDeps, name, value, playlistID string) *router.Route {
	t.Helper()
	r := &router.Route{
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
	}
	_, err := deps.routerStore.CreateRoute(ctx, r)
	require.NoError(t, err)
	return r
}

func TestDryRun_EvaluatesPendingVideoWithoutMutations(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	route := createRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createPendingVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, state.RunTypeDryRun, run.RunType)

	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	assert.Equal(t, route.ID, decisions[0].RouteID)

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

func TestDryRun_DraftConfigOverridesActive(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	// Active route does not match the pending video.
	activeRoute := createRouteWithAction(t, ctx, deps, "Rock", "rock", "PLrock")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createPendingVideo(t, ctx, deps, prodRunID, "vid_jazz", "ch1", "cool jazz live")

	draft := config.ProposalSnapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes: []config.ProposalRoute{
			{
				ID:                   activeRoute.PublicID,
				Name:                 "JazzDraft",
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

	run, err := deps.pipeline.DryRun(ctx, &draft)
	require.NoError(t, err)
	require.NotNil(t, run.ConfigVersionID)

	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	assert.Equal(t, activeRoute.ID, decisions[0].RouteID)

	actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "PLjazz", actions[0].TargetPlaylistID)
	assert.Equal(t, router.InternalActionAddToPlaylist, actions[0].ActionType,
		"simulated dry-run actions keep the internal action key")
	assert.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
}

func TestDryRun_OnlyEvaluatesPendingVideos(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	createRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	pending := createPendingVideo(t, ctx, deps, prodRunID, "vid_pending", "ch1", "cool jazz live")
	completed, err := deps.machine.DiscoverVideo(ctx, prodRunID, "vid_completed", "ch1")
	require.NoError(t, err)
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

	rejected, err := deps.machine.DiscoverVideo(ctx, prodRunID, "vid_rejected", "ch1")
	require.NoError(t, err)
	_, err = deps.machine.TransitionVideoState(ctx, rejected.ID,
		state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	_, err = deps.machine.TransitionVideoState(ctx, rejected.ID,
		state.VideoStateEvaluating, state.VideoStateEvaluated,
		state.WithFinalOutcome(state.FinalOutcomeRejected))
	require.NoError(t, err)

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	pendingDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, pending.ID, run.ID)
	require.NoError(t, err)
	assert.Len(t, pendingDecisions, 1)

	completedDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, completed.ID, run.ID)
	require.NoError(t, err)
	assert.Empty(t, completedDecisions)

	rejectedDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, rejected.ID, run.ID)
	require.NoError(t, err)
	assert.Empty(t, rejectedDecisions)
}

func TestDryRun_DoesNotChangeDuplicateReferenceCorpus(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	createRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createPendingVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")
	_, err = deps.db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference)
		 VALUES (?, 'fp', 'fp', ?, 'api_discovery', 1);`, video.VideoID, video.ChannelID)
	require.NoError(t, err)

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	var isRef int
	err = deps.db.QueryRowContext(ctx,
		`SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = ?;`, video.VideoID).Scan(&isRef)
	require.NoError(t, err)
	assert.Equal(t, 1, isRef, "production duplicate-reference corpus must not change")
}

func TestDryRun_NoSucceededActions(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	createRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	video := createPendingVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteDryRun(ctx, run.ID)

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
