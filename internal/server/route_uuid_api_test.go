package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
)

// canonicalUUIDPattern matches the ARS-004 public route identity: a
// canonical lowercase hyphenated UUIDv4.
const canonicalUUIDPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`

// routeAPICondition/action/exclusion are the decoded public child shapes.
// Child row `id` stays the unchanged non-route identifier; `route_id` is the
// route's public UUID (ARS-004).
type routeAPICondition struct {
	ID      int64  `json:"id"`
	RouteID string `json:"route_id"`
}

type routeAPIAction struct {
	ID               int64  `json:"id"`
	RouteID          string `json:"route_id"`
	ActionType       string `json:"action_type"`
	TargetPlaylistID string `json:"target_playlist_id"`
}

type routeAPIExclusion struct {
	ID             int64  `json:"id"`
	RouteID        string `json:"route_id"`
	ExclusionType  string `json:"exclusion_type"`
	ExclusionValue string `json:"exclusion_value"`
}

// routeAPI is the decoded public route shape served by the route-management
// endpoints. `id` is the stable public UUID.
type routeAPI struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	MatchCount *int64              `json:"match_count"`
	Conditions []routeAPICondition `json:"conditions"`
	Actions    []routeAPIAction    `json:"actions"`
	Exclusions []routeAPIExclusion `json:"exclusions"`
}

func decodeRouteAPI(t *testing.T, rec *httptest.ResponseRecorder) routeAPI {
	t.Helper()
	var route routeAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &route), "response: %s", rec.Body.String())
	return route
}

// createRouteFixture persists a route through the real store (internal
// integer fixtures) and returns it with its assigned public UUID.
func createRouteFixture(t *testing.T, ctx context.Context, store *router.Store, name string, order int) *router.Route {
	t.Helper()
	route := &router.Route{
		Name: name, EvaluationOrder: order, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "music"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
		Exclusions:           []router.RouteExclusion{},
	}
	_, err := store.CreateRoute(ctx, route)
	require.NoError(t, err)
	require.NotEmpty(t, route.PublicID)
	return route
}

// ARS-004: the route list keys every route by its stable public UUID and the
// child rows of a listed route carry that UUID as route_id — no internal
// integer route key is emitted anywhere in the response.
func TestRouteUUID_ListIsUUIDOnly(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	route := createRouteFixture(t, ctx, routerStore, "Music", 1)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var listed struct {
		Routes []routeAPI `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.Routes, 1)

	entry := listed.Routes[0]
	assert.Regexp(t, canonicalUUIDPattern, entry.ID)
	assert.Equal(t, route.PublicID, entry.ID)
	require.Len(t, entry.Actions, 1)
	assert.Equal(t, route.PublicID, entry.Actions[0].RouteID)
	require.Len(t, entry.Conditions, 1)
	assert.Equal(t, route.PublicID, entry.Conditions[0].RouteID)

	// No route identity field may be a JSON number.
	assert.NotRegexp(t, `"route_id":-?[0-9]`, rec.Body.String(),
		"a numeric route_id would leak the internal integer key")
}

// ARS-004: POST assigns the UUID server-side, and a caller-supplied id —
// numeric or UUID — is rejected as invalid input.
func TestRouteUUID_CreateAssignsServerSideID(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	create := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	const validBody = `"evaluation_order":1,"is_enabled":true,"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],"exclusions":[]}`

	rec := create(`{"name":"Music",` + validBody + `}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := decodeRouteAPI(t, rec)
	assert.Regexp(t, canonicalUUIDPattern, created.ID)

	suppliedIDs := []string{
		`"id":7`,
		`"id":"7"`,
		fmt.Sprintf(`"id":%q`, created.ID),
	}
	for _, supplied := range suppliedIDs {
		body := `{"name":"Music",` + supplied + `,` + validBody + `}`
		rec := create(body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "supplied id %s must be rejected", supplied)
		assert.Contains(t, rec.Body.String(), "invalid route id")
	}

	// The rejected requests must not have created routes.
	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	assert.Len(t, routes, 1)
}

// BR-003 AC10/AC12/AC13 / ARS-004 verification 4: duplicate display names are
// accepted and the same-named routes stay independently addressable.
func TestRouteUUID_DuplicateNamesRemainIndependent(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	a := createRouteFixture(t, ctx, routerStore, "Music", 1)
	b := createRouteFixture(t, ctx, routerStore, "Music", 2)
	require.NotEqual(t, a.PublicID, b.PublicID)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// The list keeps both same-named routes as separate UUID-keyed entries.
	rec := do(http.MethodGet, "/api/routes", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var listed struct {
		Routes []routeAPI `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.Routes, 2)
	assert.Equal(t, a.PublicID, listed.Routes[0].ID)
	assert.Equal(t, b.PublicID, listed.Routes[1].ID)

	// Each is readable by its own UUID.
	for _, route := range []*router.Route{a, b} {
		rec := do(http.MethodGet, "/api/routes/"+route.PublicID, "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, route.PublicID, decodeRouteAPI(t, rec).ID)
	}

	// Renaming one route to the same name again keeps identities distinct and
	// never rotates a UUID.
	putBody := `{"name":"Music","evaluation_order":2,"is_enabled":true,"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],"exclusions":[]}`
	rec = do(http.MethodPut, "/api/routes/"+b.PublicID, putBody)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	updated := decodeRouteAPI(t, rec)
	assert.Equal(t, b.PublicID, updated.ID, "PUT must preserve the route UUID")

	// Reordering the same-named pair by UUID works and keeps identities.
	rec = do(http.MethodPut, "/api/routes/reorder", fmt.Sprintf(`{"route_ids":[%q,%q]}`, b.PublicID, a.PublicID))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	assert.Equal(t, b.PublicID, routes[0].PublicID)
	assert.Equal(t, a.PublicID, routes[1].PublicID)

	// Deleting one leaves the other addressable under its unchanged UUID.
	rec = do(http.MethodDelete, "/api/routes/"+b.PublicID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = do(http.MethodGet, "/api/routes/"+b.PublicID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = do(http.MethodGet, "/api/routes/"+a.PublicID, "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// ARS-004: route CRUD paths address routes by UUID only. Decimal integers and
// other non-canonical shapes are malformed (400) — there is no numeric alias —
// while a syntactically valid but unknown UUID preserves the 404 contract.
func TestRouteUUID_RejectsNonUUIDPathIDs(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	route := createRouteFixture(t, ctx, routerStore, "Music", 1)
	putBody := `{"name":"Updated","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],"exclusions":[]}`

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	malformed := []string{
		"1",                                      // former integer alias
		"0",                                      //
		"abc",                                    //
		"3a554e9647ba4f3da7bba61e3f50c1ce",       // bare hex, no hyphens
		"{3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce}", // braced
		"urn:uuid:3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce",
		"3a554e96-47ba-4f3d-a7bb-a61e3f50c1c", // short
		route.PublicID + "x",                  // long
	}
	for _, raw := range malformed {
		for _, tc := range []struct{ method, body string }{
			{http.MethodGet, ""},
			{http.MethodPut, putBody},
			{http.MethodDelete, ""},
		} {
			req := httptest.NewRequest(tc.method, "/api/routes/"+raw, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "%s /api/routes/%s", tc.method, raw)
			assert.Contains(t, rec.Body.String(), "invalid route id")
		}
	}

	unknown := "3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce"
	req := httptest.NewRequest(http.MethodGet, "/api/routes/"+unknown, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ARS-004 invariant 3: a route's UUID never changes across rename, reorder,
// or soft delete; the registry mapping survives soft delete while the route
// stops being served.
func TestRouteUUID_StableAcrossRenameReorderSoftDelete(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	a := createRouteFixture(t, ctx, routerStore, "Alpha", 1)
	b := createRouteFixture(t, ctx, routerStore, "Beta", 2)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	putBody := func(name string, order int) string {
		return fmt.Sprintf(`{"name":%q,"evaluation_order":%d,"is_enabled":true,"condition_composition":"OR",`+
			`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],`+
			`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL9"}],`+
			`"exclusions":[{"exclusion_type":"channel","exclusion_value":"UC1"}]}`, name, order)
	}

	// Rename plus full reconfiguration keeps the UUID.
	rec := do(http.MethodPut, "/api/routes/"+a.PublicID, putBody("Renamed", 1))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	renamed := decodeRouteAPI(t, rec)
	assert.Equal(t, a.PublicID, renamed.ID)
	assert.Equal(t, "Renamed", renamed.Name)

	// Reorder keeps both UUIDs.
	rec = do(http.MethodPut, "/api/routes/reorder", fmt.Sprintf(`{"route_ids":[%q,%q]}`, b.PublicID, a.PublicID))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	internalA, err := routerStore.ResolveRouteID(ctx, a.PublicID)
	require.NoError(t, err)

	// Soft delete keeps the registry mapping: the UUID still resolves to the
	// same internal key even though the route is no longer served.
	rec = do(http.MethodDelete, "/api/routes/"+a.PublicID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	resolvedA, err := routerStore.ResolveRouteID(ctx, a.PublicID)
	require.NoError(t, err, "a soft-deleted route keeps its identity mapping")
	assert.Equal(t, internalA, resolvedA)

	rec = do(http.MethodGet, "/api/routes/"+a.PublicID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = do(http.MethodPut, "/api/routes/"+a.PublicID, putBody("Zombie", 1))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = do(http.MethodDelete, "/api/routes/"+a.PublicID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ARS-004: reorder accepts UUID route_ids and persists order without changing
// identities. Malformed, duplicate, omitted, and unknown/deleted entries are
// rejected and leave the stored order untouched.
func TestRouteUUID_ReorderValidation(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	a := createRouteFixture(t, ctx, routerStore, "Alpha", 1)
	b := createRouteFixture(t, ctx, routerStore, "Beta", 2)
	deleted := createRouteFixture(t, ctx, routerStore, "Gone", 3)
	require.NoError(t, routerStore.DeleteRoute(ctx, deleted.ID))

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	reorder := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/routes/reorder", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	assertOrderUnchanged := func(t *testing.T) {
		t.Helper()
		routes, err := routerStore.ListRoutes(ctx)
		require.NoError(t, err)
		require.Len(t, routes, 2)
		assert.Equal(t, a.PublicID, routes[0].PublicID)
		assert.Equal(t, b.PublicID, routes[1].PublicID)
	}

	rec := reorder(`{"route_ids":["1","2"]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "numeric aliases are not route ids")
	assert.Contains(t, rec.Body.String(), "invalid route id")

	rec = reorder(fmt.Sprintf(`{"route_ids":[%q,"not-a-uuid"]}`, a.PublicID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid route id")

	rec = reorder(fmt.Sprintf(`{"route_ids":[%q,%q,%q]}`, a.PublicID, a.PublicID, b.PublicID))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "duplicates are rejected")
	assert.Contains(t, rec.Body.String(), "duplicate route id")

	rec = reorder(fmt.Sprintf(`{"route_ids":[%q]}`, a.PublicID))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "omitted active routes are rejected")
	assert.Contains(t, rec.Body.String(), "every active route")

	rec = reorder(fmt.Sprintf(`{"route_ids":[%q,%q,%q]}`, a.PublicID, b.PublicID, "3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce"))
	assert.Equal(t, http.StatusNotFound, rec.Code, "an unknown UUID is route-not-found")

	rec = reorder(fmt.Sprintf(`{"route_ids":[%q,%q,%q]}`, a.PublicID, b.PublicID, deleted.PublicID))
	assert.Equal(t, http.StatusNotFound, rec.Code, "a soft-deleted UUID is route-not-found")

	assertOrderUnchanged(t)

	// The valid permutation persists order without rotating identities.
	rec = reorder(fmt.Sprintf(`{"route_ids":[%q,%q]}`, b.PublicID, a.PublicID))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	assert.Equal(t, b.PublicID, routes[0].PublicID)
	assert.Equal(t, a.PublicID, routes[1].PublicID)
}

// ARS-001: route detail is a configuration response — it omits the Route List
// match_count projection — while the list carries it.
func TestRouteUUID_DetailOmitsMatchCount(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	route := createRouteFixture(t, ctx, routerStore, "Music", 1)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	listReq := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	var listed struct {
		Routes []routeAPI `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &listed))
	require.Len(t, listed.Routes, 1)
	require.NotNil(t, listed.Routes[0].MatchCount, "the list carries an explicit match_count")
	assert.Equal(t, int64(0), *listed.Routes[0].MatchCount)

	detailReq := httptest.NewRequest(http.MethodGet, "/api/routes/"+route.PublicID, nil)
	detailRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(detailRec, detailReq)
	require.Equal(t, http.StatusOK, detailRec.Code)
	assert.NotContains(t, detailRec.Body.String(), "match_count")

	var detail routeAPI
	require.NoError(t, json.Unmarshal(detailRec.Body.Bytes(), &detail))
	assert.Nil(t, detail.MatchCount)
}
