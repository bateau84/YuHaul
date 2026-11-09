package router_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func TestEngine_SingleRouteConditionMatches(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	routerStore := router.NewStore(db)
	configStore := config.NewStore(db)
	versioner := config.NewService(routerStore, configStore)
	engine := router.NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	pipeline.NewPipeline(db, machine, stateStore, nil, nil, engine, nil, nil, nil, nil, nil,
		pipeline.WithConfigStore(configStore), pipeline.WithVersioner(versioner))

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name: "Jazz", EvaluationOrder: 1, IsEnabled: true, IsFinal: true,
		ConditionComposition: string(router.CompositionAll),
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"}},
	})
	require.NoError(t, err)
	versionID, err := versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, versionID)
	runID, err := stateStore.CreateRun(ctx, &state.Run{RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: versionID})
	require.NoError(t, err)
	video, err := machine.DiscoverVideo(ctx, runID, "video1", "channel1")
	require.NoError(t, err)

	result, err := engine.Evaluate(ctx, runID, video.ID, router.VideoContext{
		VideoID: "video1", ChannelID: "channel1", Title: "Best jazz compilation",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 1)
	assert.Equal(t, int64(1), result.MatchedRoutes[0])
	assert.True(t, result.FinalMatched)
	decisions, err := stateStore.GetRouteDecisionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	audits, err := stateStore.GetConditionAuditsForDecision(ctx, decisions[0].ID)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	assert.True(t, audits[0].Matched)
	assert.Equal(t, "title", audits[0].Field)
	assert.Equal(t, "contains", audits[0].Operator)
	assert.Equal(t, "jazz", audits[0].ExpectedValue)
	assert.Equal(t, "Best jazz compilation", *audits[0].ActualValue)
}

func TestEngine_ANDCompositionRequiresAll(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	routerStore := router.NewStore(db)
	configStore := config.NewStore(db)
	versioner := config.NewService(routerStore, configStore)
	engine := router.NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	pipeline.NewPipeline(db, machine, stateStore, nil, nil, engine, nil, nil, nil, nil, nil,
		pipeline.WithConfigStore(configStore), pipeline.WithVersioner(versioner))

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name: "Jazz Long", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"},
			{ConditionIndex: 1, Field: router.ConditionFieldDurationSeconds, Operator: router.ConditionOperatorGreaterThan, Value: "180"},
		},
	})
	require.NoError(t, err)
	versionID, err := versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, versionID)
	runID, err := stateStore.CreateRun(ctx, &state.Run{RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: versionID})
	require.NoError(t, err)
	video, err := machine.DiscoverVideo(ctx, runID, "video1", "channel1")
	require.NoError(t, err)

	duration := 120
	result, err := engine.Evaluate(ctx, runID, video.ID, router.VideoContext{
		VideoID: "video1", ChannelID: "channel1", Title: "Best jazz compilation", DurationSeconds: &duration,
	}, machine)
	require.NoError(t, err)
	assert.Empty(t, result.MatchedRoutes)
	assert.False(t, result.FinalMatched)
}

func TestEngine_ORCompositionMatchesAny(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	routerStore := router.NewStore(db)
	configStore := config.NewStore(db)
	versioner := config.NewService(routerStore, configStore)
	engine := router.NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	pipeline.NewPipeline(db, machine, stateStore, nil, nil, engine, nil, nil, nil, nil, nil,
		pipeline.WithConfigStore(configStore), pipeline.WithVersioner(versioner))

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name: "Jazz or Blues", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: string(router.CompositionAny),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"},
			{ConditionIndex: 1, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "blues"},
		},
	})
	require.NoError(t, err)
	versionID, err := versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, versionID)
	runID, err := stateStore.CreateRun(ctx, &state.Run{RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: versionID})
	require.NoError(t, err)
	video, err := machine.DiscoverVideo(ctx, runID, "video1", "channel1")
	require.NoError(t, err)

	result, err := engine.Evaluate(ctx, runID, video.ID, router.VideoContext{
		VideoID: "video1", ChannelID: "channel1", Title: "Delta blues live",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 1)
	audits, err := stateStore.GetConditionAuditsForDecision(ctx, 1)
	require.NoError(t, err)
	require.Len(t, audits, 2)
	assert.False(t, audits[0].Matched)
	assert.True(t, audits[1].Matched)
}

func setupEvaluationFixture(t *testing.T) (*state.Store, *router.Store, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	return stateStore, router.NewStore(db), func() { require.NoError(t, db.Close()) }
}

func evaluateWithVideo(t *testing.T, ctx context.Context, stateStore *state.Store, routerStore *router.Store, channelID, title string, dur *int) *router.EvaluationResult {
	t.Helper()
	db := routerStore.DB()
	configStore := config.NewStore(db)
	versioner := config.NewService(routerStore, configStore)
	engine := router.NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	pipeline.NewPipeline(db, machine, stateStore, nil, nil, engine, nil, nil, nil, nil, nil, pipeline.WithConfigStore(configStore), pipeline.WithVersioner(versioner))
	versionID, err := versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, versionID)
	runID, err := stateStore.CreateRun(ctx, &state.Run{RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: versionID})
	require.NoError(t, err)
	videoID := fmt.Sprintf("video%d", runID)
	video, err := machine.DiscoverVideo(ctx, runID, videoID, channelID)
	require.NoError(t, err)
	result, err := engine.Evaluate(ctx, runID, video.ID, router.VideoContext{VideoID: videoID, ChannelID: channelID, Title: title, DurationSeconds: dur}, machine)
	require.NoError(t, err)
	return result
}

func evaluationIntPtr(n int) *int { return &n }

func TestEngine_TitleExactExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "All",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "mix"},
		},
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeTitle, ExclusionValue: "Weekly Mix"},
		},
	})
	require.NoError(t, err)

	// Exact match (with whitespace and equivalent Unicode) should be excluded.
	result := evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "  Weekly Mix  ", nil)
	assert.Empty(t, result.MatchedRoutes)

	// Partial match should not be excluded.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "Weekly Mixtape", nil)
	require.Len(t, result.MatchedRoutes, 1)
}

func TestEngine_DurationExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Clips",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "clip"},
		},
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeDurationMax, ExclusionValue: "60"},
			{ExclusionType: router.ExclusionTypeDurationMin, ExclusionValue: "10"},
		},
	})
	require.NoError(t, err)

	// Too long.
	result := evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "funny clip", evaluationIntPtr(120))
	assert.Empty(t, result.MatchedRoutes)

	// Too short.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "funny clip", evaluationIntPtr(5))
	assert.Empty(t, result.MatchedRoutes)

	// Within range.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "funny clip", evaluationIntPtr(30))
	require.Len(t, result.MatchedRoutes, 1)
}

func TestEngine_CanonicalOperators(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	// starts_with
	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "starts_with",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorStartsWith, Value: "live"},
		},
	})
	require.NoError(t, err)

	// ends_with
	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "ends_with",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorEndsWith, Value: "mix"},
		},
	})
	require.NoError(t, err)

	// matches_regex (case-insensitive via (?i))
	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "matches_regex",
		EvaluationOrder:      3,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorMatchesRegex, Value: "(?i)jazz"},
		},
	})
	require.NoError(t, err)

	// between (inclusive)
	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "between",
		EvaluationOrder:      4,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "boundary"},
			{ConditionIndex: 1, Field: router.ConditionFieldDurationSeconds, Operator: router.ConditionOperatorBetween, Value: "120,300"},
		},
	})
	require.NoError(t, err)

	// less_than
	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "less_than",
		EvaluationOrder:      5,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "short"},
			{ConditionIndex: 1, Field: router.ConditionFieldDurationSeconds, Operator: router.ConditionOperatorLessThan, Value: "600"},
		},
	})
	require.NoError(t, err)

	// starts_with matches.
	result := evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "live performance", nil)
	require.Len(t, result.MatchedRoutes, 1)

	// ends_with matches.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "weekly mix", nil)
	require.Len(t, result.MatchedRoutes, 1)

	// matches_regex matches case-insensitively.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "JAZZ compilation", nil)
	require.Len(t, result.MatchedRoutes, 1)

	// between inclusive bounds match.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "boundary lower", evaluationIntPtr(120))
	require.Len(t, result.MatchedRoutes, 1)
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "boundary upper", evaluationIntPtr(300))
	require.Len(t, result.MatchedRoutes, 1)

	// between outside range does not match (title intentionally avoids route 5's "short" trigger).
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "boundary outside range", evaluationIntPtr(119))
	assert.Empty(t, result.MatchedRoutes)

	// less_than matches.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "short clip", evaluationIntPtr(599))
	require.Len(t, result.MatchedRoutes, 1)

	// less_than strict inequality does not match at boundary.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "short boundary", evaluationIntPtr(600))
	assert.Empty(t, result.MatchedRoutes)
}

func createSnapshotRunAndVideo(t *testing.T, ctx context.Context, stateStore *state.Store, routerStore *router.Store) (int64, int64, *router.Engine) {
	t.Helper()
	db := routerStore.DB()
	configStore := config.NewStore(db)
	versioner := config.NewService(routerStore, configStore)
	engine := router.NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	pipeline.NewPipeline(db, machine, stateStore, nil, nil, engine, nil, nil, nil, nil, nil, pipeline.WithConfigStore(configStore), pipeline.WithVersioner(versioner))
	versionID, err := versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, versionID)
	runID, err := stateStore.CreateRun(ctx, &state.Run{RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: versionID})
	require.NoError(t, err)
	video, err := machine.DiscoverVideo(ctx, runID, "video1", "channel1")
	require.NoError(t, err)
	return runID, video.ID, engine
}

func TestEngine_ChannelExclusionSkipsRouteButLeavesOthersEligible(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Route A",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"},
		},
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeChannel, ExclusionValue: "channel1"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Route B",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	runID, videoID, engine := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, router.VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Electronic music mix",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 1)
	assert.Equal(t, int64(2), result.MatchedRoutes[0])

	decisions, err := stateStore.GetRouteDecisionsForVideo(ctx, videoID)
	require.NoError(t, err)
	require.Len(t, decisions, 2)
	assert.Equal(t, state.RouteDecisionSkipped, decisions[0].Decision)
	assert.Equal(t, state.SkipReasonExclusion, *decisions[0].SkipReason)
	assert.Equal(t, state.RouteDecisionMatched, decisions[1].Decision)
}

func TestEngine_WordExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Music",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "song"},
		},
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeWord, ExclusionValue: "karaoke"},
		},
	})
	require.NoError(t, err)

	runID, videoID, engine := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, router.VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Best karaoke song ever",
	}, machine)
	require.NoError(t, err)
	assert.Empty(t, result.MatchedRoutes)
}

func TestEngine_VideoExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "All",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "vlog"},
		},
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeVideo, ExclusionValue: "video1"},
		},
	})
	require.NoError(t, err)

	runID, videoID, engine := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, router.VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Daily vlog",
	}, machine)
	require.NoError(t, err)
	assert.Empty(t, result.MatchedRoutes)
}

func TestEngine_FinalRouteTerminatesEvaluation(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "First",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Final",
		EvaluationOrder:      2,
		IsEnabled:            true,
		IsFinal:              true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "mix"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Skipped",
		EvaluationOrder:      3,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	runID, videoID, engine := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, router.VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Electronic music mix",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 2)
	assert.True(t, result.FinalMatched)

	decisions, err := stateStore.GetRouteDecisionsForVideo(ctx, videoID)
	require.NoError(t, err)
	require.Len(t, decisions, 3)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	assert.Equal(t, state.RouteDecisionMatched, decisions[1].Decision)
	assert.Equal(t, state.RouteDecisionSkipped, decisions[2].Decision)
	assert.Equal(t, state.SkipReasonFinalRouteMatched, *decisions[2].SkipReason)
}

func TestEngine_DisabledRouteSkipped(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Disabled",
		EvaluationOrder:      1,
		IsEnabled:            false,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	runID, videoID, engine := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, router.VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "music video",
	}, machine)
	require.NoError(t, err)
	assert.Empty(t, result.MatchedRoutes)

	decisions, err := stateStore.GetRouteDecisionsForVideo(ctx, videoID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionSkipped, decisions[0].Decision)
	assert.Equal(t, state.SkipReasonDisabled, *decisions[0].SkipReason)
}

func TestEngine_MultipleRouteMatching(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "A",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "live"},
		},
		Actions: []router.RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL-A"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "B",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldChannelID, Operator: router.ConditionOperatorEquals, Value: "channel1"},
		},
		Actions: []router.RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL-B"},
		},
	})
	require.NoError(t, err)

	runID, videoID, engine := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, router.VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Live performance",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 2)
	assert.False(t, result.FinalMatched)
}

func TestStore_SoftDeletePreservesDecisionEvidence(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()

	// Create a route with a condition and action.
	routeID, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Preserve me",
		EvaluationOrder:      1,
		IsEnabled:            true,
		IsFinal:              true,
		ConditionComposition: string(router.CompositionAll),
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"},
		},
		Actions: []router.RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLmusic"},
		},
	})
	require.NoError(t, err)

	// Run an evaluation to produce decision evidence.
	runID, videoID, engine := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, router.VideoContext{
		VideoID:   "v1",
		ChannelID: "ch1",
		Title:     "music video",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 1)

	// Capture pre-delete run counters.
	runBefore, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)

	// Capture evidence IDs for later existence checks.
	decisions, err := stateStore.GetRouteDecisionsForVideo(ctx, videoID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	decisionID := decisions[0].ID

	audits, err := stateStore.GetConditionAuditsForDecision(ctx, decisionID)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	auditID := audits[0].ID

	// Soft-delete the route.
	require.NoError(t, routerStore.DeleteRoute(ctx, routeID))

	// Deleted route is no longer visible to management or evaluation queries.
	_, err = routerStore.GetRoute(ctx, routeID)
	require.ErrorIs(t, err, router.ErrRouteNotFound)

	active, err := routerStore.LoadActiveRoutes(ctx)
	require.NoError(t, err)
	assert.Empty(t, active)

	// Historical evidence rows survive the soft delete.
	db := routerStore.DB()
	var exists bool
	err = db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM route_decision WHERE id = ?)`, decisionID).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists)

	err = db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM condition_audit WHERE id = ?)`, auditID).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists)

	// Run counters are unchanged after deletion.
	runAfter, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, runBefore.VideosAccepted, runAfter.VideosAccepted)
	assert.Equal(t, runBefore.VideosRejected, runAfter.VideosRejected)
	assert.Equal(t, runBefore.VideosExcluded, runAfter.VideosExcluded)
}

func TestRecordingPreflightRejectsUnboundRoutesBeforeWrites(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupEvaluationFixture(t)
	defer cleanup()
	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "first", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: string(router.CompositionAll),
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"}},
		Exclusions:           []router.RouteExclusion{{ExclusionType: router.ExclusionTypeWord, ExclusionValue: "karaoke"}},
	})
	require.NoError(t, err)
	runID, videoID, bound := createSnapshotRunAndVideo(t, ctx, stateStore, routerStore)
	machine := state.NewMachine(stateStore)
	routes, err := bound.Routes(ctx)
	require.NoError(t, err)
	vc := router.VideoContext{VideoID: "video1", ChannelID: "channel1", Title: "music"}
	unbound := router.NewEngine(routerStore)
	_, err = unbound.EvaluateWithRoutes(ctx, runID, videoID, vc, machine, routes)
	require.ErrorContains(t, err, "validator is required")
	_, err = unbound.Evaluate(ctx, runID, videoID, vc, machine)
	require.ErrorContains(t, err, "validator is required")
	routes[0].Exclusions[0].ExclusionValue = "changed"
	_, err = bound.EvaluateWithRoutes(ctx, runID, videoID, vc, machine, routes)
	require.Error(t, err)
	decisions, err := stateStore.GetRouteDecisionsForVideo(ctx, videoID)
	require.NoError(t, err)
	require.Empty(t, decisions)
	// A valid call still records the decision and its audit after the failures.
	_, err = bound.Evaluate(ctx, runID, videoID, vc, machine)
	require.NoError(t, err)
	decisions, err = stateStore.GetRouteDecisionsForVideo(ctx, videoID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	audits, err := stateStore.GetConditionAuditsForDecision(ctx, decisions[0].ID)
	require.NoError(t, err)
	require.Len(t, audits, 1)
}
