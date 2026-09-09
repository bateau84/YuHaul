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
			{ConditionIndex: 1, Field: ConditionFieldDurationSeconds, Operator: ConditionOperatorGT, Value: "180"},
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

func TestEngine_NotEqualsAndInList(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &Route{
		Name:                 "Exclude channels",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: string(CompositionAll),
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldChannelID, Operator: ConditionOperatorInList, Value: "ch1, ch2, ch3"},
			{ConditionIndex: 1, Field: ConditionFieldTitle, Operator: ConditionOperatorNotEquals, Value: "blocked"},
		},
	})
	require.NoError(t, err)

	result := evaluateWithVideo(t, ctx, stateStore, routerStore, "ch2", "allowed", nil)
	require.Len(t, result.MatchedRoutes, 1)

	result = evaluateWithVideo(t, ctx, stateStore, routerStore, "ch2", "blocked", nil)
	assert.Empty(t, result.MatchedRoutes)
}
