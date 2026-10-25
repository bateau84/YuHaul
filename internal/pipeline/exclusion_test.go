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
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

type exclusionDeps struct {
	db          *sql.DB
	stateStore  *state.Store
	machine     *state.Machine
	pipeline    *Pipeline
	routerStore *router.Store
	metaStore   *metadata.Store
}

func setupExclusionPipeline(t *testing.T) (*exclusionDeps, func()) {
	t.Helper()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)

	routerStore := router.NewStore(db)
	metaStore := metadata.NewStore(db)
	metaMgr := metadata.NewManager(metaStore, nil, nil)
	ledger := quota.New(db, quota.WithDefaultLimit(10_000))
	inserter := &recordingInserter{}
	exec := newTestExecutor(t, db, ledger, inserter)

	p := NewPipeline(db, machine, stateStore, nil, duplicate.NewDetector(db), router.NewEngine(routerStore), exec, ledger, metaMgr, nil, broadcast,
		WithConfigStore(config.NewStore(db)),
		WithVersioner(config.NewService(routerStore, config.NewStore(db))),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	return &exclusionDeps{
		db:          db,
		stateStore:  stateStore,
		machine:     machine,
		pipeline:    p,
		routerStore: routerStore,
		metaStore:   metaStore,
	}, cleanup
}

func createInterruptedRunAtExclusion(t *testing.T, ctx context.Context, deps *exclusionDeps) int64 {
	t.Helper()
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusInterrupted}
	cfgID, err := config.NewService(deps.routerStore, config.NewStore(deps.db)).ActiveVersionID(ctx)
	require.NoError(t, err)
	run.ConfigVersionID = cfgID
	run.ActionAuthorityVersion = 1
	runID, err := deps.stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	require.NoError(t, deps.stateStore.SetRunPhase(ctx, runID, string(PhaseExclusion)))
	return runID
}

func discoverVideo(t *testing.T, ctx context.Context, deps *exclusionDeps, runID int64, videoID, channelID, title string) *state.Video {
	t.Helper()
	video, err := deps.machine.DiscoverVideo(ctx, runID, videoID, channelID)
	require.NoError(t, err)
	if title != "" {
		now := time.Now().UTC()
		require.NoError(t, deps.metaStore.Upsert(ctx, metadata.TransientMetadata{
			VideoID:   videoID,
			Title:     &title,
			FetchedAt: now,
			ExpiresAt: now.Add(30 * 24 * time.Hour),
			IsActive:  true,
		}))
	}
	return video
}

func TestExclusionGate_MarksFullyExcludedVideo(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupExclusionPipeline(t)
	defer cleanup()

	_, err := deps.routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "ExcludeCh1",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeChannel, ExclusionValue: "ch1"},
		},
	})
	require.NoError(t, err)

	runID := createInterruptedRunAtExclusion(t, ctx, deps)
	video := discoverVideo(t, ctx, deps, runID, "vid1", "ch1", "")

	deps.pipeline.Execute(ctx, runID)

	loaded, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	assert.Equal(t, 1, loaded.VideosDiscovered)
	assert.Equal(t, 1, loaded.VideosExcluded)
	assert.Equal(t, 0, loaded.VideosAccepted)
	assert.Equal(t, 0, loaded.VideosRejected)

	videoAfter, err := deps.stateStore.GetVideo(ctx, video.ID)
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeExcluded, videoAfter.FinalOutcome)
	assert.Equal(t, state.VideoStateEvaluated, videoAfter.ProcessingState)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, runID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionSkipped, decisions[0].Decision)
	require.NotNil(t, decisions[0].SkipReason)
	assert.Equal(t, state.SkipReasonExclusion, *decisions[0].SkipReason)

	audits, err := deps.stateStore.GetConditionAuditsForDecision(ctx, decisions[0].ID)
	require.NoError(t, err)
	assert.Empty(t, audits, "fully excluded videos should not have condition audits")
}

func TestExclusionGate_LetsNonExcludedVideosRoute(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupExclusionPipeline(t)
	defer cleanup()

	routeID, err := deps.routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Jazz",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"},
		},
	})
	require.NoError(t, err)
	_, err = deps.db.ExecContext(ctx,
		`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
		 VALUES (?, 'playlist_insert', 'PLjazz', 0);`, routeID)
	require.NoError(t, err)

	runID := createInterruptedRunAtExclusion(t, ctx, deps)
	discoverVideo(t, ctx, deps, runID, "vid1", "ch1", "rock song")

	deps.pipeline.Execute(ctx, runID)

	loaded, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	assert.Equal(t, 0, loaded.VideosExcluded)
	assert.Equal(t, 0, loaded.VideosAccepted)
	assert.Equal(t, 1, loaded.VideosRejected)
}

func TestExclusionGate_PartialExclusionStillRoutes(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupExclusionPipeline(t)
	defer cleanup()

	_, err := deps.routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "ExcludeCh1",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeChannel, ExclusionValue: "ch1"},
		},
	})
	require.NoError(t, err)

	routeID, err := deps.routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "MatchCh1",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldChannelID, Operator: router.ConditionOperatorEquals, Value: "ch1"},
		},
	})
	require.NoError(t, err)
	_, err = deps.db.ExecContext(ctx,
		`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
		 VALUES (?, 'playlist_insert', 'PLch1', 0);`, routeID)
	require.NoError(t, err)

	runID := createInterruptedRunAtExclusion(t, ctx, deps)
	video := discoverVideo(t, ctx, deps, runID, "vid1", "ch1", "")

	deps.pipeline.Execute(ctx, runID)

	loaded, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	assert.Equal(t, 0, loaded.VideosExcluded)
	assert.Equal(t, 1, loaded.VideosAccepted)

	videoAfter, err := deps.stateStore.GetVideo(ctx, video.ID)
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeAccepted, videoAfter.FinalOutcome)

	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, runID)
	require.NoError(t, err)
	require.Len(t, decisions, 2)
	assert.Equal(t, state.RouteDecisionSkipped, decisions[0].Decision)
	assert.Equal(t, state.RouteDecisionMatched, decisions[1].Decision)
}

func TestExclusionGate_DiscoveredCounterIncludesExcluded(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupExclusionPipeline(t)
	defer cleanup()

	routeID, err := deps.routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "JazzOrExcludeCh1",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"},
		},
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeChannel, ExclusionValue: "ch1"},
		},
	})
	require.NoError(t, err)
	_, err = deps.db.ExecContext(ctx,
		`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
		 VALUES (?, 'playlist_insert', 'PLjazz', 0);`, routeID)
	require.NoError(t, err)

	runID := createInterruptedRunAtExclusion(t, ctx, deps)
	discoverVideo(t, ctx, deps, runID, "vid1", "ch1", "jazz live") // excluded by channel
	discoverVideo(t, ctx, deps, runID, "vid2", "ch2", "jazz live") // accepted

	deps.pipeline.Execute(ctx, runID)

	loaded, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	assert.Equal(t, 2, loaded.VideosDiscovered)
	assert.Equal(t, 1, loaded.VideosExcluded)
	assert.Equal(t, 1, loaded.VideosAccepted)
}
