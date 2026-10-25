package router

// Store-level tests for ADR-011/ARS-004 route identity on the create and
// lookup paths: new routes get a UUIDv4 mapping in the same transaction as
// the route row and its children, duplicate display names stay independent,
// and a public ID is stable across rename, reorder, enable/disable, and soft
// delete. The startup backfill for pre-existing routes is covered by
// internal/store's route_identity tests; these cover the writer seam that
// runs after startup.

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canonicalUUIDv4 matches the ARS-004 public route identity format:
// lowercase, hyphenated, version 4, RFC 4122 variant.
var canonicalUUIDv4 = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func countRows(t *testing.T, s *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, s.DB().QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

// registeredPublicID reads the single registry mapping for an internal key,
// asserting exactly one row exists (ARS-004 invariant 2).
func registeredPublicID(t *testing.T, s *Store, routeID int64) string {
	t.Helper()
	var publicID string
	require.NoError(t, s.DB().QueryRowContext(context.Background(),
		`SELECT public_id FROM route_identity WHERE route_id = ?;`, routeID).Scan(&publicID))
	return publicID
}

func TestCreateRoute_AssignsPublicIDInSameTransaction(t *testing.T) {
	ctx := context.Background()
	_, store, cleanup := setupTestDB(t)
	defer cleanup()

	r := &Route{
		Name: "Music", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND",
		Conditions: []RouteCondition{{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "live"}},
		Actions:    []RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
		Exclusions: []RouteExclusion{{ExclusionType: ExclusionTypeWord, ExclusionValue: "shorts"}},
	}
	id, err := store.CreateRoute(ctx, r)
	require.NoError(t, err)

	// The assigned public ID is a canonical UUIDv4 persisted as the route's
	// one and only registry mapping.
	assert.Regexp(t, canonicalUUIDv4, r.PublicID)
	var mappings int64
	require.NoError(t, store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM route_identity WHERE route_id = ? AND public_id = ?;`,
		id, r.PublicID).Scan(&mappings))
	assert.EqualValues(t, 1, mappings)
	assert.Equal(t, r.PublicID, registeredPublicID(t, store, id))

	// Store-level UUID lookup returns the internal row with its children.
	got, err := store.GetRouteByPublicID(ctx, r.PublicID)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, r.PublicID, got.PublicID)
	assert.Equal(t, "Music", got.Name)
	require.Len(t, got.Conditions, 1)
	require.Len(t, got.Actions, 1)
	require.Len(t, got.Exclusions, 1)

	resolved, err := store.ResolveRouteID(ctx, r.PublicID)
	require.NoError(t, err)
	assert.Equal(t, id, resolved)
}

func TestCreateRoute_DuplicateNamesHaveIndependentIdentities(t *testing.T) {
	ctx := context.Background()
	_, store, cleanup := setupTestDB(t)
	defer cleanup()

	a := &Route{Name: "Music", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"}
	b := &Route{Name: "Music", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND"}
	idA, err := store.CreateRoute(ctx, a)
	require.NoError(t, err)
	idB, err := store.CreateRoute(ctx, b)
	require.NoError(t, err)

	// Same display name, distinct stable identities.
	require.NotEqual(t, a.PublicID, b.PublicID)
	assert.NotEqual(t, idA, idB)

	gotA, err := store.GetRouteByPublicID(ctx, a.PublicID)
	require.NoError(t, err)
	gotB, err := store.GetRouteByPublicID(ctx, b.PublicID)
	require.NoError(t, err)
	assert.Equal(t, idA, gotA.ID)
	assert.Equal(t, idB, gotB.ID)

	// Renaming one same-named route changes neither identity: each UUID keeps
	// resolving to its own row, and the other route is untouched.
	a.Name = "Music (Live)"
	require.NoError(t, store.UpdateRoute(ctx, a))

	gotA, err = store.GetRouteByPublicID(ctx, a.PublicID)
	require.NoError(t, err)
	assert.Equal(t, idA, gotA.ID)
	assert.Equal(t, "Music (Live)", gotA.Name)

	gotB, err = store.GetRouteByPublicID(ctx, b.PublicID)
	require.NoError(t, err)
	assert.Equal(t, idB, gotB.ID)
	assert.Equal(t, "Music", gotB.Name)
}

func TestRoutePublicID_StableAcrossLifecycle(t *testing.T) {
	ctx := context.Background()
	_, store, cleanup := setupTestDB(t)
	defer cleanup()

	a := &Route{Name: "Alpha", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"}
	b := &Route{Name: "Beta", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND"}
	idA, err := store.CreateRoute(ctx, a)
	require.NoError(t, err)
	idB, err := store.CreateRoute(ctx, b)
	require.NoError(t, err)

	// After every operation below, each UUID resolves to the same internal
	// key and the registry row is byte-for-byte the assigned mapping
	// (ADR-011 invariant 3; nothing regenerates or reassigns it).
	assertStable := func(t *testing.T, wantName string, aActive bool) {
		t.Helper()
		resolvedA, err := store.ResolveRouteID(ctx, a.PublicID)
		require.NoError(t, err)
		assert.Equal(t, idA, resolvedA)
		resolvedB, err := store.ResolveRouteID(ctx, b.PublicID)
		require.NoError(t, err)
		assert.Equal(t, idB, resolvedB)
		assert.Equal(t, a.PublicID, registeredPublicID(t, store, idA))
		assert.Equal(t, b.PublicID, registeredPublicID(t, store, idB))

		gotA, err := store.GetRouteByPublicID(ctx, a.PublicID)
		if aActive {
			require.NoError(t, err)
			assert.Equal(t, idA, gotA.ID)
			assert.Equal(t, wantName, gotA.Name)
		} else {
			// A soft-deleted route is not loadable by UUID, preserving the
			// route-CRUD 404 behavior, but its identity mapping remains.
			assert.ErrorIs(t, err, ErrRouteNotFound)
		}
	}

	// Rename.
	a.Name = "Alpha Renamed"
	require.NoError(t, store.UpdateRoute(ctx, a))
	assertStable(t, "Alpha Renamed", true)

	// Enable/disable.
	a.IsEnabled = false
	require.NoError(t, store.UpdateRoute(ctx, a))
	assertStable(t, "Alpha Renamed", true)

	// Reorder.
	require.NoError(t, store.ReorderRoutes(ctx, []int64{idB, idA}))
	assertStable(t, "Alpha Renamed", true)

	// Soft delete: the route row stops resolving by UUID, the mapping stays.
	require.NoError(t, store.DeleteRoute(ctx, idA))
	assertStable(t, "", false)

	// The unaffected sibling route keeps its identity and availability.
	gotB, err := store.GetRouteByPublicID(ctx, b.PublicID)
	require.NoError(t, err)
	assert.Equal(t, idB, gotB.ID)
	assert.Equal(t, "Beta", gotB.Name)
}

func TestRouteLookupByPublicID_FailsClosed(t *testing.T) {
	ctx := context.Background()
	_, store, cleanup := setupTestDB(t)
	defer cleanup()

	for _, tc := range []struct {
		name     string
		publicID string
	}{
		{name: "unknown canonical uuid", publicID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"},
		{name: "malformed uuid", publicID: "not-a-uuid"},
		{name: "empty", publicID: ""},
		// Lookup input must be the canonical stored form; the API boundary
		// owns accepting/normalizing uppercase request input.
		{name: "uppercase uuid not normalized", publicID: "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.GetRouteByPublicID(ctx, tc.publicID)
			assert.ErrorIs(t, err, ErrRouteNotFound)
			_, err = store.ResolveRouteID(ctx, tc.publicID)
			assert.ErrorIs(t, err, ErrRouteNotFound)
		})
	}
}

func TestCreateRoute_RollbackLeavesNeitherRouteNorIdentity(t *testing.T) {
	ctx := context.Background()
	_, store, cleanup := setupTestDB(t)
	defer cleanup()

	routesBefore := countRows(t, store, `SELECT COUNT(*) FROM route;`)
	identitiesBefore := countRows(t, store, `SELECT COUNT(*) FROM route_identity;`)

	// Two conditions with the same index violate UNIQUE(route_id,
	// condition_index), so the children insert fails after the route row and
	// its identity mapping were already inserted in the transaction.
	r := &Route{
		Name: "Broken", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND",
		Conditions: []RouteCondition{
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "a"},
			{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "b"},
		},
	}
	_, err := store.CreateRoute(ctx, r)
	require.Error(t, err)

	assert.Equal(t, routesBefore, countRows(t, store, `SELECT COUNT(*) FROM route;`))
	assert.Equal(t, identitiesBefore, countRows(t, store, `SELECT COUNT(*) FROM route_identity;`))
	assert.EqualValues(t, 0, countRows(t, store,
		`SELECT COUNT(*) FROM route WHERE name = 'Broken';`))
}
