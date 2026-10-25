package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/router"
)

// canonicalUUIDv4 matches the canonical lowercase hyphenated UUIDv4 form the
// public API must emit for route identities (ARS-004).
var canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func createLegacyRoute(t *testing.T, ctx context.Context, routerStore *router.Store, name string, order int) (int64, string) {
	t.Helper()
	route := &router.Route{
		Name: name, EvaluationOrder: order, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: router.InternalActionAddToPlaylist, TargetPlaylistID: "PL1"}},
	}
	id, err := routerStore.CreateRoute(ctx, route)
	require.NoError(t, err)
	require.NotEmpty(t, route.PublicID, "create must assign the stable public UUID")
	return id, route.PublicID
}

// ADR-007 / ARS-001: configuration-history responses emit add_to_playlist for
// route actions while the stored snapshot keeps the legacy internal value
// byte-for-byte.
func TestConfigHistory_EmitsPublicActionType(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, cfgStore, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	routeID, publicID := createLegacyRoute(t, ctx, routerStore, "Music", 1)

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	versionID := versions[len(versions)-1].ID

	before := rawSnapshot(t, cfgStore.db, versionID)

	handler := NewHandler(versioner, nil)

	getReq := httptest.NewRequest(http.MethodGet, "/api/config/versions/"+strconv.FormatInt(versionID, 10), nil)
	getRec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)
	assert.Contains(t, getRec.Body.String(), `"action_type":"add_to_playlist"`)
	assert.NotContains(t, getRec.Body.String(), "playlist_insert")

	listReq := httptest.NewRequest(http.MethodGet, "/api/config/versions", nil)
	listRec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	assert.Contains(t, listRec.Body.String(), `"action_type":"add_to_playlist"`)
	assert.NotContains(t, listRec.Body.String(), "playlist_insert")

	stored, err := cfgStore.Get(ctx, versionID)
	require.NoError(t, err)
	require.Len(t, stored.Snapshot.Routes, 1)
	assert.Equal(t, int64(routeID), stored.Snapshot.Routes[0].ID)
	require.Len(t, stored.Snapshot.Routes[0].Actions, 1)
	assert.Equal(t, router.InternalActionAddToPlaylist, stored.Snapshot.Routes[0].Actions[0].ActionType,
		"the stored historical snapshot must not be rewritten")
	assert.Equal(t, before, rawSnapshot(t, cfgStore.db, versionID),
		"the stored snapshot bytes must survive public reads unchanged")
	assert.Equal(t, publicID, decodeVersion(t, getRec).Snapshot.Routes[0].ID)
	assert.Equal(t, publicID, decodeVersions(t, listRec)[0].Snapshot.Routes[0].ID)
}

// ADR-007: diff responses normalize both compared snapshots consistently, so
// normalization never fabricates a route change.
func TestConfigHistory_DiffEmitsPublicActionType(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	routeID, publicID := createLegacyRoute(t, ctx, routerStore, "Music", 1)
	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	fromID := versions[len(versions)-1].ID

	route, err := routerStore.GetRoute(ctx, routeID)
	require.NoError(t, err)
	route.Name = "Music Updated"
	require.NoError(t, routerStore.UpdateRoute(ctx, route))

	toID, err := versioner.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, toID)
	newest := toID[len(toID)-1].ID
	require.Greater(t, newest, fromID)

	handler := NewHandler(versioner, nil)

	diffReq := httptest.NewRequest(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(newest, 10)+"/diff?compare_to="+strconv.FormatInt(fromID, 10), nil)
	diffRec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(diffRec, diffReq)
	require.Equal(t, http.StatusOK, diffRec.Code)
	assert.Contains(t, diffRec.Body.String(), `"action_type":"add_to_playlist"`)
	assert.NotContains(t, diffRec.Body.String(), "playlist_insert")

	changed := decodeDiff(t, diffRec)
	require.Len(t, changed.RoutesChanged, 1)
	assert.Equal(t, publicID, changed.RoutesChanged[0].ID,
		"the changed route is identified by its stable public UUID")
	assert.Equal(t, publicID, changed.RoutesChanged[0].Old.ID)
	assert.Equal(t, publicID, changed.RoutesChanged[0].New.ID)

	selfReq := httptest.NewRequest(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(newest, 10)+"/diff?compare_to="+strconv.FormatInt(newest, 10), nil)
	selfRec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(selfRec, selfReq)
	require.Equal(t, http.StatusOK, selfRec.Code)
	assert.Contains(t, selfRec.Body.String(), `"routes_changed":[]`)
	assert.Contains(t, selfRec.Body.String(), `"routes_added":[]`)
	assert.Contains(t, selfRec.Body.String(), `"routes_removed":[]`)
}

// ADR-007: restoring a historical version creates a new internal snapshot and
// returns the public representation.
func TestConfigHistory_RestoreKeepsInternalStorage(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, cfgStore, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	_, publicID := createLegacyRoute(t, ctx, routerStore, "Music", 1)
	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	versionID := versions[len(versions)-1].ID

	handler := NewHandler(versioner, nil)
	req := httptest.NewRequest(http.MethodPost,
		"/api/config/versions/"+strconv.FormatInt(versionID, 10)+"/restore",
		strings.NewReader(`{"description":"restored"}`))
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), `"action_type":"add_to_playlist"`)
	assert.NotContains(t, rec.Body.String(), "playlist_insert")
	assert.Equal(t, publicID, decodeVersion(t, rec).Snapshot.Routes[0].ID,
		"the restored version's public representation uses the route UUID")

	restored, err := cfgStore.Get(ctx, versionID+1)
	require.NoError(t, err)
	require.Len(t, restored.Snapshot.Routes, 1)
	require.Len(t, restored.Snapshot.Routes[0].Actions, 1)
	assert.Equal(t, router.InternalActionAddToPlaylist, restored.Snapshot.Routes[0].Actions[0].ActionType,
		"the restored snapshot persists the internal representation")
}

// rawSnapshot reads the stored snapshot JSON for a config version exactly as
// persisted, so tests can assert byte-for-byte immutability (OC-003).
func rawSnapshot(t *testing.T, db *sql.DB, versionID int64) string {
	t.Helper()
	var snapshot string
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT snapshot FROM config_version WHERE id = ?;`, versionID).Scan(&snapshot))
	return snapshot
}

// insertRawSnapshot inserts a snapshot JSON document verbatim, simulating a
// row written by an earlier application version (a legacy v11-era snapshot).
func insertRawSnapshot(t *testing.T, db *sql.DB, snapshotJSON string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO config_version (created_at, snapshot, change_description)
		 VALUES (?, ?, ?);`, "2026-01-02T03:04:05Z", snapshotJSON, "legacy v11 row")
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// decodeVersion decodes a single-version response. Decoding fails if a route
// identity was emitted as anything other than a string (e.g. a legacy
// integer), which is the ARS-004 identity-type assertion.
func decodeVersion(t *testing.T, rec *httptest.ResponseRecorder) PublicConfigVersion {
	t.Helper()
	var v PublicConfigVersion
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	return v
}

func decodeVersions(t *testing.T, rec *httptest.ResponseRecorder) []PublicConfigVersion {
	t.Helper()
	var body struct {
		Versions []PublicConfigVersion `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Versions
}

func decodeDiff(t *testing.T, rec *httptest.ResponseRecorder) PublicDiffResult {
	t.Helper()
	var d PublicDiffResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	return d
}

// ARS-004 identity representation: snapshot route IDs in configuration-history
// responses are stable public UUIDv4 strings, and routes with duplicate
// display names stay independently identifiable.
func TestConfigHistory_SnapshotRouteIDsArePublicUUIDs(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	_, uuidA := createLegacyRoute(t, ctx, routerStore, "Music", 1)
	_, uuidB := createLegacyRoute(t, ctx, routerStore, "Music", 2)
	require.NotEqual(t, uuidA, uuidB, "duplicate names must not share an identity")

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	versionID := versions[len(versions)-1].ID

	handler := NewHandler(versioner, nil)

	getRec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(getRec, httptest.NewRequest(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(versionID, 10), nil))
	require.Equal(t, http.StatusOK, getRec.Code)
	get := decodeVersion(t, getRec)
	require.Len(t, get.Snapshot.Routes, 2)
	assert.Equal(t, uuidA, get.Snapshot.Routes[0].ID)
	assert.Equal(t, uuidB, get.Snapshot.Routes[1].ID)
	assert.True(t, canonicalUUIDv4.MatchString(get.Snapshot.Routes[0].ID))
	assert.True(t, canonicalUUIDv4.MatchString(get.Snapshot.Routes[1].ID))

	listRec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/config/versions", nil))
	require.Equal(t, http.StatusOK, listRec.Code)
	listed := decodeVersions(t, listRec)
	require.NotEmpty(t, listed)
	require.Len(t, listed[len(listed)-1].Snapshot.Routes, 2)
	assert.Equal(t, uuidA, listed[len(listed)-1].Snapshot.Routes[0].ID)
	assert.Equal(t, uuidB, listed[len(listed)-1].Snapshot.Routes[1].ID)
}

// Migration-era v11 snapshot: stored bytes keep the internal integer route id
// and the legacy internal action type, while the public read projects the
// route UUID and the public action vocabulary. Reading never rewrites the row.
func TestConfigHistory_LegacyV11SnapshotProjectsUUIDAndPreservesBytes(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, cfgStore, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	routeID, publicID := createLegacyRoute(t, ctx, routerStore, "Legacy Music", 1)

	legacy := `{"routes":[{"id":` + strconv.FormatInt(routeID, 10) +
		`,"name":"Legacy Music","evaluation_order":1,"is_final":false,"is_enabled":true,` +
		`"condition_composition":"AND","conditions":[],` +
		`"actions":[{"action_index":0,"action_type":"playlist_insert","target_playlist_id":"PLlegacy"}],` +
		`"exclusions":[]}],"duplicate_threshold":0.4,"duplicate_behavior":"skip"}`
	versionID := insertRawSnapshot(t, cfgStore.db, legacy)

	before := rawSnapshot(t, cfgStore.db, versionID)
	require.Equal(t, legacy, before)

	handler := NewHandler(versioner, nil)
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(versionID, 10), nil))
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `"action_type":"add_to_playlist"`)
	assert.NotContains(t, body, "playlist_insert")

	got := decodeVersion(t, rec)
	require.Len(t, got.Snapshot.Routes, 1)
	assert.Equal(t, publicID, got.Snapshot.Routes[0].ID)
	assert.Equal(t, "Legacy Music", got.Snapshot.Routes[0].Name)

	assert.Equal(t, before, rawSnapshot(t, cfgStore.db, versionID),
		"the stored v11 snapshot bytes must remain byte-for-byte unchanged")
}

// ARS-004: diff compares the stored internal snapshots and only then projects
// public UUIDs, so duplicate route names cannot confuse route identity: the
// renamed route is identified by its UUID, not by its (shared) name.
func TestConfigHistory_DiffWithDuplicateNames(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	idA, uuidA := createLegacyRoute(t, ctx, routerStore, "Music", 1)
	_, uuidB := createLegacyRoute(t, ctx, routerStore, "Music", 2)

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	fromID := versions[1].ID // v2: both routes present, both named "Music"

	route, err := routerStore.GetRoute(ctx, idA)
	require.NoError(t, err)
	route.Name = "Jazz"
	require.NoError(t, routerStore.UpdateRoute(ctx, route))

	versions, err = versioner.List(ctx)
	require.NoError(t, err)
	require.Len(t, versions, 3) // v3: route A renamed to "Jazz"
	toID := versions[len(versions)-1].ID

	handler := NewHandler(versioner, nil)
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(toID, 10)+
			"/diff?compare_to="+strconv.FormatInt(fromID, 10), nil))
	require.Equal(t, http.StatusOK, rec.Code)

	diff := decodeDiff(t, rec)
	require.Len(t, diff.RoutesChanged, 1, "only the renamed route changed")
	require.Len(t, diff.RoutesAdded, 0)
	require.Len(t, diff.RoutesRemoved, 0)
	assert.Equal(t, uuidA, diff.RoutesChanged[0].ID)
	assert.Equal(t, uuidA, diff.RoutesChanged[0].Old.ID)
	assert.Equal(t, uuidA, diff.RoutesChanged[0].New.ID)
	assert.Equal(t, "Music", diff.RoutesChanged[0].Old.Name)
	assert.Equal(t, "Jazz", diff.RoutesChanged[0].New.Name)
	assert.NotEqual(t, uuidA, uuidB)
	assert.NotContains(t, rec.Body.String(), `"id":`+strconv.FormatInt(idA, 10),
		"the diff must never emit an internal integer route key")
}

// ARS-004: restoring a historical version persists the stored internal
// snapshot (integer route keys) and returns the UUID-projected representation;
// both same-named routes restore independently and the source row is untouched.
func TestConfigHistory_RestoreWithDuplicateNames(t *testing.T) {
	ctx := context.Background()
	_, _, routerStore, cfgStore, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	idA, uuidA := createLegacyRoute(t, ctx, routerStore, "Music", 1)
	idB, uuidB := createLegacyRoute(t, ctx, routerStore, "Music", 2)

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	sourceID := versions[len(versions)-1].ID
	sourceBytes := rawSnapshot(t, cfgStore.db, sourceID)

	route, err := routerStore.GetRoute(ctx, idA)
	require.NoError(t, err)
	route.Name = "Jazz"
	require.NoError(t, routerStore.UpdateRoute(ctx, route))

	handler := NewHandler(versioner, nil)
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/config/versions/"+strconv.FormatInt(sourceID, 10)+"/restore",
		strings.NewReader(`{"description":"back to the duplicate-name era"}`)))
	require.Equal(t, http.StatusCreated, rec.Code)

	restored := decodeVersion(t, rec)
	require.Len(t, restored.Snapshot.Routes, 2)
	byID := map[string]string{}
	for _, r := range restored.Snapshot.Routes {
		byID[r.ID] = r.Name
	}
	assert.Equal(t, "Music", byID[uuidA], "route A restored to its historical name")
	assert.Equal(t, "Music", byID[uuidB], "route B untouched, still identified by UUID")

	// Storage stays legacy: the new version holds the internal integer keys.
	stored, err := cfgStore.Get(ctx, restored.ID)
	require.NoError(t, err)
	require.Len(t, stored.Snapshot.Routes, 2)
	storedKeys := []int64{stored.Snapshot.Routes[0].ID, stored.Snapshot.Routes[1].ID}
	assert.ElementsMatch(t, []int64{idA, idB}, storedKeys,
		"the restored snapshot persists internal integer route keys")

	assert.Equal(t, sourceBytes, rawSnapshot(t, cfgStore.db, sourceID),
		"restoring must not modify the source version's stored bytes")
	assert.Equal(t, sourceBytes, rawSnapshot(t, cfgStore.db, restored.ID),
		"the restored version stores the same internal snapshot bytes")
}

// ARS-004 verification item 7: a snapshot-only route ID whose route row no
// longer exists (pre-ADR-003 hard delete) still projects its registry UUID.
func TestConfigHistory_SnapshotOnlyRouteIDProjectsUUID(t *testing.T) {
	ctx := context.Background()
	db, _, routerStore, _, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	routeID, publicID := createLegacyRoute(t, ctx, routerStore, "Hard Deleted", 1)

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	versionID := versions[len(versions)-1].ID

	// Simulate a pre-ADR-003 hard delete: the route row (and its children)
	// disappear while the ConfigVersion snapshot keeps the integer key. The
	// registry row survives because it deliberately has no FK to route.
	_, err = db.ExecContext(ctx, `DELETE FROM route WHERE id = ?;`, routeID)
	require.NoError(t, err)
	var remaining int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM route WHERE id = ?;`, routeID).Scan(&remaining))
	require.Equal(t, 0, remaining)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM route_identity WHERE route_id = ?;`, routeID).Scan(&remaining))
	require.Equal(t, 1, remaining, "the identity mapping outlives the route row")

	handler := NewHandler(versioner, nil)
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(versionID, 10), nil))
	require.Equal(t, http.StatusOK, rec.Code)

	got := decodeVersion(t, rec)
	require.Len(t, got.Snapshot.Routes, 1)
	assert.Equal(t, publicID, got.Snapshot.Routes[0].ID,
		"a snapshot-only identity projects its UUID despite the missing route row")
}

// ARS-004 failure behavior: a stored snapshot route key without a registry
// mapping fails the history endpoints with their existing internal-error
// contract; it is never emitted as an integer, and restore refuses to commit
// a version the public endpoints could not serve.
func TestConfigHistory_UnmappedRouteIDFailsClosed(t *testing.T) {
	ctx := context.Background()
	_, _, _, cfgStore, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	v1, err := cfgStore.Create(ctx, Snapshot{
		Routes:             []SnapshotRoute{{ID: 9999, Name: "Ghost", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"}},
		DuplicateThreshold: 0.5, DuplicateBehavior: "skip",
	}, "snapshot with unmapped route key")
	require.NoError(t, err)
	v2, err := cfgStore.Create(ctx, Snapshot{
		Routes:             []SnapshotRoute{{ID: 9999, Name: "Ghost Renamed", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"}},
		DuplicateThreshold: 0.5, DuplicateBehavior: "skip",
	}, "same unmapped key, changed")
	require.NoError(t, err)

	handler := NewHandler(versioner, nil)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		var reader *strings.Reader = strings.NewReader(body)
		handler.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, reader))
		return rec
	}

	getRec := do(http.MethodGet, "/api/config/versions/"+strconv.FormatInt(v1, 10), "")
	assert.Equal(t, http.StatusInternalServerError, getRec.Code)
	assert.Contains(t, getRec.Body.String(), "failed to load config version")
	assert.NotContains(t, getRec.Body.String(), "9999", "no integer route key may leak")

	listRec := do(http.MethodGet, "/api/config/versions", "")
	assert.Equal(t, http.StatusInternalServerError, listRec.Code)
	assert.NotContains(t, listRec.Body.String(), "9999")

	diffRec := do(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(v2, 10)+"/diff?compare_to="+strconv.FormatInt(v1, 10), "")
	assert.Equal(t, http.StatusInternalServerError, diffRec.Code)
	assert.Contains(t, diffRec.Body.String(), "failed to diff config versions")
	assert.NotContains(t, diffRec.Body.String(), "9999")

	restoreRec := do(http.MethodPost, "/api/config/versions/"+strconv.FormatInt(v1, 10)+"/restore",
		`{"description":"would poison history"}`)
	assert.Equal(t, http.StatusInternalServerError, restoreRec.Code)
	assert.NotContains(t, restoreRec.Body.String(), "9999")

	versions, err := versioner.List(ctx)
	require.NoError(t, err)
	assert.Len(t, versions, 2, "the failed restore must not commit a new config version")
}

// A snapshot with no routes projects trivially (the shape used by the
// internal/server config-version handler tests).
func TestConfigHistory_EmptySnapshotProjects(t *testing.T) {
	ctx := context.Background()
	_, _, _, cfgStore, versioner, cleanup := setupConfigTest(t)
	defer cleanup()

	id, err := cfgStore.Create(ctx, Snapshot{DuplicateThreshold: 0.5, DuplicateBehavior: "skip"}, "empty")
	require.NoError(t, err)

	handler := NewHandler(versioner, nil)
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/config/versions/"+strconv.FormatInt(id, 10), nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"routes":[]`)
}
