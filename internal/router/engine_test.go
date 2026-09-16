package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func setupTestDB(t *testing.T) (*state.Store, *Store, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	routerStore := NewStore(db)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return stateStore, routerStore, cleanup
}

func createRunAndVideo(t *testing.T, ctx context.Context, stateStore *state.Store) (int64, int64, string) {
	t.Helper()
	n := atomic.AddInt64(&evalCounter, 1)
	videoIDText := fmt.Sprintf("video%d", n)

	run := &state.Run{
		RunType: state.RunTypeManual,
		Status:  state.RunStatusRunning,
	}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	machine := state.NewMachine(stateStore)
	video, err := machine.DiscoverVideo(ctx, runID, videoIDText, "channel1")
	require.NoError(t, err)
	return runID, video.ID, videoIDText
}

func intPtr(n int) *int {
	return &n
}

var evalCounter int64

func evaluateWithVideo(t *testing.T, ctx context.Context, stateStore *state.Store, routerStore *Store, channelID, title string, dur *int) *EvaluationResult {
	t.Helper()
	runID, videoID, videoIDText := createRunAndVideo(t, ctx, stateStore)
	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
		VideoID:         videoIDText,
		ChannelID:       channelID,
		Title:           title,
		DurationSeconds: dur,
	}, machine)
	require.NoError(t, err)
	return result
}

func TestEngine_SingleRouteConditionMatches(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Jazz",
		EvaluationOrder:      1,
		IsEnabled:            true,
		IsFinal:              true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "jazz"},
		},
		Actions: []RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Best jazz compilation",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 1)
	assert.Equal(t, int64(1), result.MatchedRoutes[0])
	assert.True(t, result.FinalMatched)

	decisions, err := stateStore.GetRouteDecisionsForVideo(ctx, videoID)
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
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Jazz Long",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "jazz"},
			{ConditionIndex: 1, Field: ConditionFieldDurationSeconds, Operator: ConditionOperatorGreaterThan, Value: "180"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)

	// Only title matches; duration is too short.
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
		VideoID:         "video1",
		ChannelID:       "channel1",
		Title:           "Best jazz compilation",
		DurationSeconds: intPtr(120),
	}, machine)
	require.NoError(t, err)
	assert.Empty(t, result.MatchedRoutes)
	assert.False(t, result.FinalMatched)
}

func TestEngine_ORCompositionMatchesAny(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Jazz or Blues",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAny),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "jazz"},
			{ConditionIndex: 1, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "blues"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Delta blues live",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 1)

	audits, err := stateStore.GetConditionAuditsForDecision(ctx, 1)
	require.NoError(t, err)
	require.Len(t, audits, 2)
	assert.False(t, audits[0].Matched)
	assert.True(t, audits[1].Matched)
}

func TestEngine_ChannelExclusionSkipsRouteButLeavesOthersEligible(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Route A",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "music"},
		},
		Exclusions: []RouteExclusion{
			{ExclusionType: ExclusionTypeChannel, ExclusionValue: "channel1"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "Route B",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
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

func TestEngine_TitleExactExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "All",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "mix"},
		},
		Exclusions: []RouteExclusion{
			{ExclusionType: ExclusionTypeTitle, ExclusionValue: "Weekly Mix"},
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

func TestEngine_WordExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Music",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "song"},
		},
		Exclusions: []RouteExclusion{
			{ExclusionType: ExclusionTypeWord, ExclusionValue: "karaoke"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Best karaoke song ever",
	}, machine)
	require.NoError(t, err)
	assert.Empty(t, result.MatchedRoutes)
}

func TestEngine_VideoExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "All",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "vlog"},
		},
		Exclusions: []RouteExclusion{
			{ExclusionType: ExclusionTypeVideo, ExclusionValue: "video1"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Daily vlog",
	}, machine)
	require.NoError(t, err)
	assert.Empty(t, result.MatchedRoutes)
}

func TestEngine_DurationExclusion(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Clips",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "clip"},
		},
		Exclusions: []RouteExclusion{
			{ExclusionType: ExclusionTypeDurationMax, ExclusionValue: "60"},
			{ExclusionType: ExclusionTypeDurationMin, ExclusionValue: "10"},
		},
	})
	require.NoError(t, err)

	// Too long.
	result := evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "funny clip", intPtr(120))
	assert.Empty(t, result.MatchedRoutes)

	// Too short.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "funny clip", intPtr(5))
	assert.Empty(t, result.MatchedRoutes)

	// Within range.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "channel1", "funny clip", intPtr(30))
	require.Len(t, result.MatchedRoutes, 1)
}

func TestEngine_FinalRouteTerminatesEvaluation(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "First",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "Final",
		EvaluationOrder:      2,
		IsEnabled:            true,
		IsFinal:              true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "mix"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "Skipped",
		EvaluationOrder:      3,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
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
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Disabled",
		EvaluationOrder:      1,
		IsEnabled:            false,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "music"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
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
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "A",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "live"},
		},
		Actions: []RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL-A"},
		},
	})
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "B",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldChannelID, Operator: ConditionOperatorEquals, Value: "channel1"},
		},
		Actions: []RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL-B"},
		},
	})
	require.NoError(t, err)

	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
		VideoID:   "video1",
		ChannelID: "channel1",
		Title:     "Live performance",
	}, machine)
	require.NoError(t, err)
	require.Len(t, result.MatchedRoutes, 2)
	assert.False(t, result.FinalMatched)
}

func TestStore_ReorderRoutes(t *testing.T) {
	ctx := context.Background()
	_, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	id1, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "First",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)

	id2, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Second",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)

	err = routerStore.ReorderRoutes(ctx, []int64{id2, id1})
	require.NoError(t, err)

	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	assert.Equal(t, id2, routes[0].ID)
	assert.Equal(t, 1, routes[0].EvaluationOrder)
	assert.Equal(t, id1, routes[1].ID)
	assert.Equal(t, 2, routes[1].EvaluationOrder)
}

func TestStore_DeleteHighestThenCreate(t *testing.T) {
	ctx := context.Background()
	_, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	id1, err := routerStore.CreateRoute(ctx, &Route{
		Name: "First", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)
	id2, err := routerStore.CreateRoute(ctx, &Route{
		Name: "Second", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)

	// Deleting the highest-id route sets its evaluation_order sentinel to -id2.
	require.NoError(t, routerStore.DeleteRoute(ctx, id2))

	// Creating a new route must not collide with the deleted sentinel.
	id3, err := routerStore.CreateRoute(ctx, &Route{
		Name: "Third", EvaluationOrder: 3, IsEnabled: true, ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)

	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	assert.Equal(t, id1, routes[0].ID)
	assert.Equal(t, id3, routes[1].ID)
}

func TestStore_DeleteMiddleThenReorder(t *testing.T) {
	ctx := context.Background()
	_, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	id1, err := routerStore.CreateRoute(ctx, &Route{
		Name: "First", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)
	id2, err := routerStore.CreateRoute(ctx, &Route{
		Name: "Second", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)
	id3, err := routerStore.CreateRoute(ctx, &Route{
		Name: "Third", EvaluationOrder: 3, IsEnabled: true, ConditionComposition: string(CompositionAll),
	})
	require.NoError(t, err)

	// Soft-delete the middle route; its sentinel is -id2.
	require.NoError(t, routerStore.DeleteRoute(ctx, id2))

	// Reordering the remaining active routes must not collide with -id2.
	require.NoError(t, routerStore.ReorderRoutes(ctx, []int64{id3, id1}))

	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	assert.Equal(t, id3, routes[0].ID)
	assert.Equal(t, 1, routes[0].EvaluationOrder)
	assert.Equal(t, id1, routes[1].ID)
	assert.Equal(t, 2, routes[1].EvaluationOrder)
}

func TestEngine_CanonicalOperators(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	// starts_with
	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "starts_with",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorStartsWith, Value: "live"},
		},
	})
	require.NoError(t, err)

	// ends_with
	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "ends_with",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorEndsWith, Value: "mix"},
		},
	})
	require.NoError(t, err)

	// matches_regex (case-insensitive via (?i))
	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "matches_regex",
		EvaluationOrder:      3,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorMatchesRegex, Value: "(?i)jazz"},
		},
	})
	require.NoError(t, err)

	// between (inclusive)
	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "between",
		EvaluationOrder:      4,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "boundary"},
			{ConditionIndex: 1, Field: ConditionFieldDurationSeconds, Operator: ConditionOperatorBetween, Value: "120,300"},
		},
	})
	require.NoError(t, err)

	// less_than
	_, err = routerStore.CreateRoute(ctx, &Route{
		Name:                 "less_than",
		EvaluationOrder:      5,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "short"},
			{ConditionIndex: 1, Field: ConditionFieldDurationSeconds, Operator: ConditionOperatorLessThan, Value: "600"},
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
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "boundary lower", intPtr(120))
	require.Len(t, result.MatchedRoutes, 1)
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "boundary upper", intPtr(300))
	require.Len(t, result.MatchedRoutes, 1)

	// between outside range does not match (title intentionally avoids route 5's "short" trigger).
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "boundary outside range", intPtr(119))
	assert.Empty(t, result.MatchedRoutes)

	// less_than matches.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "short clip", intPtr(599))
	require.Len(t, result.MatchedRoutes, 1)

	// less_than strict inequality does not match at boundary.
	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "short boundary", intPtr(600))
	assert.Empty(t, result.MatchedRoutes)
}

func TestStore_SoftDeletePreservesDecisionEvidence(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a route with a condition and action.
	routeID, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Preserve me",
		EvaluationOrder:      1,
		IsEnabled:            true,
		IsFinal:              true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "music"},
		},
		Actions: []RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLmusic"},
		},
	})
	require.NoError(t, err)

	// Run an evaluation to produce decision evidence.
	runID, videoID, _ := createRunAndVideo(t, ctx, stateStore)
	engine := NewEngine(routerStore)
	machine := state.NewMachine(stateStore)
	result, err := engine.Evaluate(ctx, runID, videoID, VideoContext{
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
	require.ErrorIs(t, err, ErrRouteNotFound)

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

func TestEngine_InvalidRegexDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Invalid regex",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorMatchesRegex, Value: "(unclosed"},
		},
	})
	require.NoError(t, err)

	result := evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "any title", nil)
	assert.Empty(t, result.MatchedRoutes)
}

func TestEngine_UnknownOperatorLogsWarningAndReturnsFalse(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Drifted operator",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: "gt", Value: "100"},
		},
	})
	require.NoError(t, err)

	result := evaluateWithVideo(t, ctx, stateStore, routerStore, "ch1", "any title", nil)
	assert.Empty(t, result.MatchedRoutes)
}
