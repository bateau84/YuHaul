package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// ADR-007 / ARS-001: a route written through the public API with
// add_to_playlist persists the internal playlist_insert representation and
// every public route representation returns add_to_playlist.
func TestRouteAction_PublicRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	body := `{"name":"Music","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],"exclusions":[]}`

	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), `"action_type":"add_to_playlist"`)
	assert.NotContains(t, rec.Body.String(), "playlist_insert")

	created := decodeRouteAPI(t, rec)
	require.Len(t, created.Actions, 1)
	assert.Regexp(t, canonicalUUIDPattern, created.ID)

	// Internal store fixtures keep the integer key; only the public boundary
	// uses the UUID.
	routeID, err := routerStore.ResolveRouteID(ctx, created.ID)
	require.NoError(t, err)
	stored, err := routerStore.GetRoute(ctx, routeID)
	require.NoError(t, err)
	require.Len(t, stored.Actions, 1)
	assert.Equal(t, router.InternalActionAddToPlaylist, stored.Actions[0].ActionType,
		"the API boundary must map the public name to the internal persisted key")

	listReq := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	assert.Contains(t, listRec.Body.String(), `"action_type":"add_to_playlist"`)
	assert.Contains(t, listRec.Body.String(), `"match_count"`)
	assert.NotContains(t, listRec.Body.String(), "playlist_insert")

	detailReq := httptest.NewRequest(http.MethodGet, "/api/routes/"+created.ID, nil)
	detailRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(detailRec, detailReq)
	require.Equal(t, http.StatusOK, detailRec.Code)
	assert.Contains(t, detailRec.Body.String(), `"action_type":"add_to_playlist"`)
	assert.NotContains(t, detailRec.Body.String(), "playlist_insert")
	assert.NotContains(t, detailRec.Body.String(), "match_count",
		"route detail is a configuration response and omits the Route List statistics projection")
}

// ADR-007: the public API does not accept the internal spelling as an input
// alias, and unknown or missing action types are rejected with 400.
func TestRouteAction_RejectedActionTypes(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	route := createRouteFixture(t, ctx, routerStore, "Music", 1)

	createBody := func(actionJSON string) string {
		return `{"name":"Music","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",` +
			`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
			`"actions":[` + actionJSON + `],"exclusions":[]}`
	}

	tests := []struct {
		name   string
		action string
	}{
		{name: "internal spelling", action: `{"action_index":0,"action_type":"playlist_insert","target_playlist_id":"PL1"}`},
		{name: "unknown type", action: `{"action_index":0,"action_type":"remove_from_playlist","target_playlist_id":"PL1"}`},
		{name: "empty type", action: `{"action_index":0,"action_type":"","target_playlist_id":"PL1"}`},
		{name: "missing type", action: `{"action_index":0,"target_playlist_id":"PL1"}`},
		{name: "case variant", action: `{"action_index":0,"action_type":"Add_To_Playlist","target_playlist_id":"PL1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))

			req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(createBody(tt.action)))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), `"error"`)

			putReq := httptest.NewRequest(http.MethodPut, "/api/routes/"+route.PublicID, strings.NewReader(createBody(tt.action)))
			putRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(putRec, putReq)
			assert.Equal(t, http.StatusBadRequest, putRec.Code)
		})
	}
}

// BR-004 AC8 / ARS-001: a selected playlist is required; an empty or
// whitespace-only target_playlist_id is rejected with 400.
func TestRouteAction_RequiresTargetPlaylist(t *testing.T) {
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))

	for _, target := range []string{"", "   "} {
		body := fmt.Sprintf(`{"name":"Music","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",`+
			`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],`+
			`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":%q}],"exclusions":[]}`, target)
		req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "target %q must be rejected", target)
		assert.Contains(t, rec.Body.String(), "target_playlist_id")
	}
}

// ARS-001 / ADR-007: legacy routes stored with playlist_insert keep their
// stored value but are emitted with the public action type everywhere.
func TestRouteAction_LegacyStoredRouteEmitsPublicValue(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	route := createRouteFixture(t, ctx, routerStore, "Legacy", 1)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))

	for _, path := range []string{"/api/routes", "/api/routes/" + route.PublicID} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Body.String(), `"action_type":"add_to_playlist"`, path)
		assert.NotContains(t, rec.Body.String(), "playlist_insert", path)
	}
}

// BR-007 AC9-11: exclusion entries with an unsupported category or an
// unusable value are rejected with 400 and are not saved.
func TestRouteExclusions_RejectInvalidEntries(t *testing.T) {
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	tests := []struct {
		name      string
		exclusion string
	}{
		{name: "unsupported category", exclusion: `{"exclusion_type":"category","exclusion_value":"x"}`},
		{name: "empty type", exclusion: `{"exclusion_type":"","exclusion_value":"x"}`},
		{name: "empty channel value", exclusion: `{"exclusion_type":"channel","exclusion_value":""}`},
		{name: "blank video value", exclusion: `{"exclusion_type":"video","exclusion_value":"   "}`},
		{name: "empty title value", exclusion: `{"exclusion_type":"title","exclusion_value":""}`},
		{name: "empty word value", exclusion: `{"exclusion_type":"word","exclusion_value":""}`},
		{name: "non-numeric duration", exclusion: `{"exclusion_type":"duration_min","exclusion_value":"abc"}`},
		{name: "negative duration", exclusion: `{"exclusion_type":"duration_max","exclusion_value":"-5"}`},
		{name: "fractional duration", exclusion: `{"exclusion_type":"duration_min","exclusion_value":"60.5"}`},
		{name: "padded duration", exclusion: `{"exclusion_type":"duration_min","exclusion_value":" 60"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
			body := `{"name":"Music","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",` +
				`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
				`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],` +
				`"exclusions":[` + tt.exclusion + `]}`
			req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}

	t.Run("minimum above maximum", func(t *testing.T) {
		srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
		body := `{"name":"Music","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",` +
			`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
			`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],` +
			`"exclusions":[{"exclusion_type":"duration_min","exclusion_value":"600"},{"exclusion_type":"duration_max","exclusion_value":"120"}]}`
		req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "must not exceed")
	})
}

// BR-007 AC9-10: valid entries are accepted and persisted; duration bounds of
// zero and min equal to max are allowed.
func TestRouteExclusions_AcceptValidEntries(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	body := `{"name":"Music","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],` +
		`"exclusions":[` +
		`{"exclusion_type":"channel","exclusion_value":"UC123"},` +
		`{"exclusion_type":"video","exclusion_value":"vid1"},` +
		`{"exclusion_type":"title","exclusion_value":"Prank Compilation"},` +
		`{"exclusion_type":"word","exclusion_value":"reaction"},` +
		`{"exclusion_type":"duration_min","exclusion_value":"0"},` +
		`{"exclusion_type":"duration_max","exclusion_value":"0"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	created := decodeRouteAPI(t, rec)
	require.Len(t, created.Exclusions, 6)
	assert.Regexp(t, canonicalUUIDPattern, created.ID)
	for _, e := range created.Exclusions {
		assert.Equal(t, created.ID, e.RouteID,
			"exclusion children carry the route's public UUID, not an internal integer")
	}

	routeID, err := routerStore.ResolveRouteID(ctx, created.ID)
	require.NoError(t, err)
	stored, err := routerStore.GetRoute(ctx, routeID)
	require.NoError(t, err)
	require.Len(t, stored.Exclusions, len(created.Exclusions))
	for i, e := range created.Exclusions {
		assert.Equal(t, string(stored.Exclusions[i].ExclusionType), e.ExclusionType)
		assert.Equal(t, stored.Exclusions[i].ExclusionValue, e.ExclusionValue)
	}
}

// BR-007: exclusions are route-scoped. Validating and saving one route's
// exclusions must not attach them to any other route.
func TestRouteExclusions_RemainRouteScoped(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	create := func(name string, order int, exclusions string) routeAPI {
		t.Helper()
		body := fmt.Sprintf(`{"name":%q,"evaluation_order":%d,"is_enabled":true,"condition_composition":"AND",`+
			`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],`+
			`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],`+
			`"exclusions":[%s]}`, name, order, exclusions)
		req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		return decodeRouteAPI(t, rec)
	}

	routeA := create("With Exclusion", 1, `{"exclusion_type":"channel","exclusion_value":"UCexcluded"}`)
	routeB := create("Without Exclusion", 2, "")

	assert.Len(t, routeA.Exclusions, 1)
	assert.Empty(t, routeB.Exclusions, "route B must not inherit route A's exclusion")

	routeBID, err := routerStore.ResolveRouteID(ctx, routeB.ID)
	require.NoError(t, err)
	storedB, err := routerStore.GetRoute(ctx, routeBID)
	require.NoError(t, err)
	assert.Empty(t, storedB.Exclusions)

	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	for _, r := range routes {
		if r.PublicID == routeB.ID {
			assert.Empty(t, r.Exclusions, "list load must keep route B exclusion-free")
		}
	}

	// A validation failure on one route must not write anything for any route.
	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(
		`{"name":"Invalid","evaluation_order":1,"is_enabled":true,"condition_composition":"AND",`+
			`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],`+
			`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],`+
			`"exclusions":[{"exclusion_type":"channel","exclusion_value":""}]}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	routes, err = routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	assert.Len(t, routes, 2, "a rejected save must not create a route")
}

// ADR-007: dry-run draft and backtest proposed configurations cross the same
// public boundary as route CRUD. The SPA builds them from GET /api/routes, so
// they carry add_to_playlist; the pipeline must receive the internal
// representation before the snapshot is persisted or evaluated.
func TestProposalAction_PublicSpellingNormalizedForPipeline(t *testing.T) {
	const proposalRoute = `{"id":"3f1d2c8e-9a67-4b52-8f0c-1d2e3f4a5b6c","name":"Music","evaluation_order":1,"is_final":false,"is_enabled":true,` +
		`"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],"exclusions":[]}`

	assertActionsInternal := func(t *testing.T, routes []config.ProposalRoute) {
		t.Helper()
		require.Len(t, routes, 1)
		require.Len(t, routes[0].Actions, 1)
		assert.Equal(t, router.InternalActionAddToPlaylist, routes[0].Actions[0].ActionType,
			"the pipeline must receive the internal representation (ADR-007)")
	}

	t.Run("backtest", func(t *testing.T) {
		run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning}
		fp := newFakePipeline(run)
		srv := NewServer(fp, pipeline.NewBroadcaster())

		body := `{"proposed_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` + proposalRoute + `]}}`
		req := httptest.NewRequest(http.MethodPost, "/api/backtest", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		select {
		case <-fp.done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for pipeline ExecuteBacktest")
		}
		assertActionsInternal(t, fp.backtestProposed.Routes)
	})

	t.Run("dry-run", func(t *testing.T) {
		run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeDryRun, Status: state.RunStatusRunning}
		fp := newFakePipeline(run)
		srv := NewServer(fp, pipeline.NewBroadcaster())

		body := `{"draft_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` + proposalRoute + `]}}`
		req := httptest.NewRequest(http.MethodPost, "/api/runs/dry-run", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		select {
		case <-fp.done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for pipeline ExecuteDryRun")
		}
		require.NotNil(t, fp.draft)
		assertActionsInternal(t, fp.draft.Routes)
	})
}

// ADR-007: proposal inputs use the public vocabulary only. The internal
// spelling is not an input alias and unsupported values are rejected with 400
// before any run starts.
func TestProposalAction_RejectedProposalActionTypes(t *testing.T) {
	proposal := func(action string) string {
		return `"routes":[{"id":"3f1d2c8e-9a67-4b52-8f0c-1d2e3f4a5b6c","name":"Music","evaluation_order":1,"is_final":false,"is_enabled":true,` +
			`"condition_composition":"AND",` +
			`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
			`"actions":[` + action + `],"exclusions":[]}]`
	}

	tests := []struct {
		name   string
		action string
	}{
		{name: "internal spelling", action: `{"action_index":0,"action_type":"playlist_insert","target_playlist_id":"PL1"}`},
		{name: "unknown type", action: `{"action_index":0,"action_type":"remove_from_playlist","target_playlist_id":"PL1"}`},
		{name: "empty type", action: `{"action_index":0,"action_type":"","target_playlist_id":"PL1"}`},
		{name: "missing type", action: `{"action_index":0,"target_playlist_id":"PL1"}`},
		{name: "case variant", action: `{"action_index":0,"action_type":"Add_To_Playlist","target_playlist_id":"PL1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("backtest", func(t *testing.T) {
				run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning}
				fp := newFakePipeline(run)
				srv := NewServer(fp, pipeline.NewBroadcaster())

				body := `{"proposed_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip",` + proposal(tt.action) + `}}`
				req := httptest.NewRequest(http.MethodPost, "/api/backtest", strings.NewReader(body))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Body.String(), `"error"`)
				assert.Zero(t, fp.execute, "a rejected proposal must not start a run")
				assert.Empty(t, fp.backtestProposed.Routes, "a rejected proposal must not reach the pipeline")
			})

			t.Run("dry-run", func(t *testing.T) {
				run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeDryRun, Status: state.RunStatusRunning}
				fp := newFakePipeline(run)
				srv := NewServer(fp, pipeline.NewBroadcaster())

				body := `{"draft_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip",` + proposal(tt.action) + `}}`
				req := httptest.NewRequest(http.MethodPost, "/api/runs/dry-run", strings.NewReader(body))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Body.String(), `"error"`)
				assert.Zero(t, fp.execute, "a rejected draft must not start a run")
				assert.Nil(t, fp.draft, "a rejected draft must not reach the pipeline")
			})
		})
	}
}

// ARS-001: public executed-action details emit add_to_playlist while the
// executed_action row keeps the stored legacy value.
func TestRunVideoDetail_EmitsPublicActionType(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	route := createRouteForRunVideoTests(t, ctx, routerStore)

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	video := &state.Video{
		VideoID: "v1", ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
		DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted,
		FinalOutcome: state.FinalOutcomeAccepted,
	}
	videoID, err := stateStore.CreateVideo(ctx, video)
	require.NoError(t, err)

	decisionID, err := stateStore.CreateRouteDecision(ctx, &state.RouteDecision{
		VideoID: videoID, RunID: runID, RouteID: route.ID,
		EvaluationOrder: 1, Decision: state.RouteDecisionMatched,
	})
	require.NoError(t, err)

	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: runID, VideoID: videoID,
		ActionType: router.InternalActionAddToPlaylist, TargetPlaylistID: "PL1",
		Outcome: state.ActionOutcomeSucceeded, ExecutedAt: timePtr(time.Now().UTC()),
	})
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/v1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"action_type":"add_to_playlist"`)
	assert.NotContains(t, body, "playlist_insert")
}
