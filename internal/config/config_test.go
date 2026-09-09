package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func setupConfigTest(t *testing.T) (*sql.DB, *state.Store, *router.Store, *Store, *Service, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)

	routerStore := router.NewStore(db)
	configStore := NewStore(db)
	versioner := NewService(routerStore, configStore)
	routerStore.WithOnChange(versioner.Snapshot)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, stateStore, routerStore, configStore, versioner, cleanup
}

func TestStore_CreateAndGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, _, _, cfgStore, _, cleanup := setupConfigTest(t)
	defer cleanup()

	snap := Snapshot{
		Routes: []SnapshotRoute{
			{ID: 1, Name: "Music", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"},
		},
		DuplicateThreshold: 0.6,
		DuplicateBehavior:  "flag",
	}
	id, err := cfgStore.Create(ctx, snap, "test snapshot")
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	cv, err := cfgStore.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, cv.ID)
	assert.Equal(t, "test snapshot", cv.ChangeDescription)
	assert.Equal(t, 0.6, cv.Snapshot.DuplicateThreshold)
	assert.Equal(t, "flag", cv.Snapshot.DuplicateBehavior)
	require.Len(t, cv.Snapshot.Routes, 1)
	assert.Equal(t, "Music", cv.Snapshot.Routes[0].Name)
}

func TestStore_Get_NotFound(t *testing.T) {
	ctx := context.Background()
	_, _, _, cfgStore, _, cleanup := setupConfigTest(t)
	defer cleanup()

	_, err := cfgStore.Get(ctx, 999)
	assert.ErrorIs(t, err, state.ErrNotFound)
}

func TestStore_List_OrderedByID(t *testing.T) {
	ctx := context.Background()
	_, _, _, cfgStore, _, cleanup := setupConfigTest(t)
	defer cleanup()

	id1, err := cfgStore.Create(ctx, Snapshot{DuplicateThreshold: 0.1}, "first")
	require.NoError(t, err)
	id2, err := cfgStore.Create(ctx, Snapshot{DuplicateThreshold: 0.2}, "second")
	require.NoError(t, err)

	versions, err := cfgStore.List(ctx)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, id1, versions[0].ID)
	assert.Equal(t, id2, versions[1].ID)
}

func TestStore_SettingRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, _, _, cfgStore, _, cleanup := setupConfigTest(t)
	defer cleanup()

	v, err := cfgStore.GetSetting(ctx, "duplicate_threshold")
	require.NoError(t, err)
	assert.Equal(t, "0.5", v)

	require.NoError(t, cfgStore.SetSetting(ctx, "duplicate_threshold", "0.75"))
	v, err = cfgStore.GetSetting(ctx, "duplicate_threshold")
	require.NoError(t, err)
	assert.Equal(t, "0.75", v)
}

func TestService_Snapshot_CapturesRoutesAndSettings(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Jazz",
		EvaluationOrder:      1,
		IsEnabled:            true,
		IsFinal:              true,
		ConditionComposition: "AND",
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"},
		},
		Actions: []router.RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"},
		},
		Exclusions: []router.RouteExclusion{
			{ExclusionType: router.ExclusionTypeChannel, ExclusionValue: "UCexclude"},
		},
	})
	require.NoError(t, err)

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	cv := versions[0]
	assert.Contains(t, cv.ChangeDescription, "Jazz")
	require.Len(t, cv.Snapshot.Routes, 1)
	r := cv.Snapshot.Routes[0]
	assert.Equal(t, "Jazz", r.Name)
	assert.True(t, r.IsFinal)
	require.Len(t, r.Conditions, 1)
	assert.Equal(t, "title", r.Conditions[0].Field)
	require.Len(t, r.Actions, 1)
	assert.Equal(t, "PLjazz", r.Actions[0].TargetPlaylistID)
	require.Len(t, r.Exclusions, 1)
	assert.Equal(t, "channel", r.Exclusions[0].ExclusionType)
	assert.Equal(t, 0.5, cv.Snapshot.DuplicateThreshold)
}

func TestService_ActiveVersionID_CreatesInitialSnapshot(t *testing.T) {
	ctx := context.Background()
	_, _, _, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	id, err := versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, id)

	cv, err := versioner.Get(ctx, *id)
	require.NoError(t, err)
	assert.Equal(t, "initial configuration", cv.ChangeDescription)
	assert.Empty(t, cv.Snapshot.Routes)
}

func TestService_Diff_DetectsThresholdChange(t *testing.T) {
	ctx := context.Background()
	_, _, _, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	id1, err := versioner.snapshot(ctx, "first")
	require.NoError(t, err)
	require.NoError(t, versioner.SetDuplicateThreshold(ctx, 0.9))
	id2, err := versioner.snapshot(ctx, "second")
	require.NoError(t, err)

	result, err := versioner.Diff(ctx, id1, id2)
	require.NoError(t, err)
	assert.Equal(t, id1, result.FromID)
	assert.Equal(t, id2, result.ToID)
	require.Len(t, result.SettingsChanged, 1)
	assert.Equal(t, "0.5", result.SettingsChanged["duplicate_threshold"].Old)
	assert.Equal(t, "0.9", result.SettingsChanged["duplicate_threshold"].New)
}

func TestService_Diff_DetectsRouteChanges(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	id1, err := versioner.snapshot(ctx, "empty")
	require.NoError(t, err)

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Blues",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "blues"},
		},
	})
	require.NoError(t, err)

	id2, err := versioner.snapshot(ctx, "with route")
	require.NoError(t, err)

	result, err := versioner.Diff(ctx, id1, id2)
	require.NoError(t, err)
	require.Len(t, result.RoutesAdded, 1)
	assert.Equal(t, "Blues", result.RoutesAdded[0].Name)
}

func TestService_Restore_CreatesNewVersionFromHistoricalSnapshot(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Old",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
	})
	require.NoError(t, err)

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	original := versions[0]

	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "New",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: "AND",
	})
	require.NoError(t, err)

	restored, err := versioner.Restore(ctx, original.ID, "restore old config")
	require.NoError(t, err)
	require.Greater(t, restored.ID, original.ID)
	assert.Equal(t, "restore old config", restored.ChangeDescription)
	require.Len(t, restored.Snapshot.Routes, 1)
	assert.Equal(t, "Old", restored.Snapshot.Routes[0].Name)
}

func TestService_SetDuplicateThreshold_ClampsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	_, _, _, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	require.NoError(t, versioner.SetDuplicateThreshold(ctx, 1.5))
	v, err := versioner.DuplicateThreshold(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1.0, v)

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.Len(t, versions, 1)
}

func TestService_DuplicateBehavior_DefaultsToSkip(t *testing.T) {
	ctx := context.Background()
	_, _, _, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	b, err := versioner.DuplicateBehavior(ctx)
	require.NoError(t, err)
	assert.Equal(t, "skip", b)
}

func TestRouterStore_OnChange_SnapshotAfterMutations(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, _, cleanup := setupConfigTest(t)
	defer cleanup()

	callCount := 0
	routerStore.WithOnChange(func(ctx context.Context, desc string) error {
		callCount++
		return nil
	})

	id, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "A",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	require.NoError(t, routerStore.UpdateRoute(ctx, &router.Route{
		ID:                   id,
		Name:                 "A2",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
	}))
	assert.Equal(t, 2, callCount)

	require.NoError(t, routerStore.DeleteRoute(ctx, id))
	assert.Equal(t, 3, callCount)
}

func TestRouterStore_Reorder_Snapshots(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, _, cleanup := setupConfigTest(t)
	defer cleanup()

	callCount := 0
	routerStore.WithOnChange(func(ctx context.Context, desc string) error {
		callCount++
		return nil
	})

	id1, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "A", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"})
	require.NoError(t, err)
	id2, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "B", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND"})
	require.NoError(t, err)

	// Two creates happened.
	require.NoError(t, routerStore.ReorderRoutes(ctx, []int64{id2, id1}))
	assert.Equal(t, 3, callCount)
}

func TestRouterStore_NoOnChange_DoesNotFail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	defer db.Close()

	rs := router.NewStore(db)
	_, err = rs.CreateRoute(ctx, &router.Route{
		Name: "A", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"})
	require.NoError(t, err)
}

func TestConfigVersionSnapshot_JSONShape(t *testing.T) {
	ctx := context.Background()
	_, _, _, cfgStore, _, cleanup := setupConfigTest(t)
	defer cleanup()

	snap := Snapshot{
		Routes: []SnapshotRoute{
			{
				ID:                   7,
				Name:                 "Rock",
				EvaluationOrder:      1,
				IsFinal:              true,
				IsEnabled:            true,
				ConditionComposition: "OR",
				Conditions: []SnapshotCondition{
					{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "rock"},
				},
				Actions: []SnapshotAction{
					{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLrock"},
				},
				Exclusions: []SnapshotExclusion{
					{ExclusionType: "word", ExclusionValue: "karaoke"},
				},
			},
		},
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
	}
	id, err := cfgStore.Create(ctx, snap, "shape check")
	require.NoError(t, err)

	cv, err := cfgStore.Get(ctx, id)
	require.NoError(t, err)
	data, err := json.Marshal(cv.Snapshot)
	require.NoError(t, err)

	var decoded Snapshot
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, snap, decoded)
}
