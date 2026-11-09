package router

import (
	"context"
	"os"
	"path/filepath"
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

func TestExclusionMatchReturnsFirstArrayOccurrenceAcrossTypes(t *testing.T) {
	route := Route{Exclusions: []RouteExclusion{
		{ExclusionType: ExclusionTypeWord, ExclusionValue: "jazz"},
		{ExclusionType: ExclusionTypeChannel, ExclusionValue: "channel-1"},
		{ExclusionType: ExclusionTypeWord, ExclusionValue: "jazz"},
	}}
	match, reason := ExclusionMatch(route, VideoContext{Title: "Jazz session", ChannelID: "channel-1"})
	require.Equal(t, state.SkipReasonExclusion, *reason)
	require.NotNil(t, match)
	require.Equal(t, 0, match.Index, "the first matching array entry wins, not type priority or a later duplicate")
	require.Equal(t, 1, match.SemanticsVersion)
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

func TestEngine_InvalidRegexDoesNotPanic(t *testing.T) {
	engine := NewEngine(nil)
	matched, actual := engine.evaluateCondition(1, RouteCondition{Field: ConditionFieldTitle, Operator: ConditionOperatorMatchesRegex, Value: "(unclosed"}, VideoContext{Title: "any title"})
	assert.False(t, matched)
	assert.Equal(t, "any title", actual)
}

func TestEngine_UnknownOperatorLogsWarningAndReturnsFalse(t *testing.T) {
	engine := NewEngine(nil)
	matched, actual := engine.evaluateCondition(1, RouteCondition{Field: ConditionFieldTitle, Operator: "gt", Value: "100"}, VideoContext{Title: "any title"})
	assert.False(t, matched)
	assert.Equal(t, "any title", actual)
}
