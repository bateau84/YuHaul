package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

func setupRunVideoTest(t *testing.T) (*state.Store, *router.Store, *config.Service, func()) {
	t.Helper()
	db, stateStore, _, cleanup := setupServerTest(t)

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	versioner := config.NewService(routerStore, cfgStore)
	routerStore.WithOnChange(versioner.Snapshot)

	return stateStore, routerStore, versioner, cleanup
}

func createRouteForRunVideoTests(t *testing.T, ctx context.Context, routerStore *router.Store) *router.Route {
	t.Helper()
	id, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "Music", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
		Exclusions:           []router.RouteExclusion{},
	})
	require.NoError(t, err)
	route, err := routerStore.GetRoute(ctx, id)
	require.NoError(t, err)
	identities, err := routerStore.ListRouteIdentities(ctx)
	require.NoError(t, err)
	route.PublicID = identities[id]
	require.NotEmpty(t, route.PublicID)
	return route
}

// captureConfigVersion persists a snapshot of the current route configuration
// and returns the new ConfigVersion ID.
func captureConfigVersion(t *testing.T, ctx context.Context, versioner *config.Service) int64 {
	t.Helper()
	require.NoError(t, versioner.Snapshot(ctx, "test snapshot"))
	id, err := versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, id)
	return *id
}

// createRunWithDecision creates a completed manual run (linked to
// configVersionID when non-nil), one completed video in it, and one matched
// route decision for routeID. It returns the run and video IDs.
func createRunWithDecision(t *testing.T, ctx context.Context, stateStore *state.Store,
	routeID int64, configVersionID *int64) (int64, int64) {
	t.Helper()
	runID, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeManual,
		Status: state.RunStatusCompleted, ConfigVersionID: configVersionID,
	})
	require.NoError(t, err)

	videoID, err := stateStore.CreateVideo(ctx, &state.Video{
		VideoID: "v1", ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
		DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted,
		FinalOutcome: state.FinalOutcomeAccepted,
	})
	require.NoError(t, err)

	_, err = stateStore.CreateRouteDecision(ctx, &state.RouteDecision{
		VideoID: videoID, RunID: runID, RouteID: routeID,
		EvaluationOrder: 1, Decision: state.RouteDecisionMatched,
	})
	require.NoError(t, err)
	return runID, videoID
}

func newRunVideoServer(stateStore *state.Store, routeStore RouteStore, cfgStore ConfigVersionStore) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routeStore),
		WithConfigVersionStore(cfgStore))
}

func TestHandleListRunVideos(t *testing.T) {
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

	_, err = stateStore.CreateConditionAudit(ctx, &state.ConditionAudit{
		RouteDecisionID: decisionID, ConditionIndex: 0,
		Field: "title", Operator: "contains", ExpectedValue: "jazz",
		ActualValue: strPtr("live jazz"), Matched: true,
	})
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"videos":`)
	assert.Contains(t, body, `"video_id":"v1"`)
	assert.Contains(t, body, `"processing_state":"completed"`)
	assert.Contains(t, body, `"final_outcome":"accepted"`)
	assert.Contains(t, body, `"route_decisions":`)
	// ARS-004: the public route reference is the stable UUID; the internal
	// integer key never appears on the wire.
	assert.Contains(t, body, `"route_id":"`+route.PublicID+`"`)
	assert.NotContains(t, body, `"route_id":`+strconv.FormatInt(route.ID, 10))
	assert.Contains(t, body, `"route_name":"Music"`)
	assert.Contains(t, body, `"decision":"matched"`)
	assert.Contains(t, body, `"pagination":`)
}

func TestHandleListRunVideos_NotFound(t *testing.T) {
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/999/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleListRunVideos_Pagination(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		video := &state.Video{
			VideoID: strconv.Itoa(i), ChannelID: "c1",
			DiscoveredAt:      time.Now().UTC().Add(time.Duration(i) * time.Second),
			DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted,
			FinalOutcome: state.FinalOutcomeAccepted,
		}
		_, err := stateStore.CreateVideo(ctx, video)
		require.NoError(t, err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos?page_size=2&page=1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp videoListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Videos, 2)
	assert.Equal(t, int64(3), resp.Pagination.Total)
	assert.Equal(t, 2, resp.Pagination.PageSize)
	assert.Equal(t, 2, resp.Pagination.TotalPages)
}

func TestHandleListRunVideos_OutcomeFilter(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	// One video per BR-032 outcome category. Excluded shares processing_state
	// "evaluated" with rejected, so the filter must key on final_outcome.
	categories := []struct {
		videoID    string
		outcome    state.VideoFinalOutcome
		processing state.VideoProcessingState
	}{
		{"v-accepted", state.FinalOutcomeAccepted, state.VideoStateCompleted},
		{"v-rejected", state.FinalOutcomeRejected, state.VideoStateEvaluated},
		{"v-excluded", state.FinalOutcomeExcluded, state.VideoStateEvaluated},
		{"v-deferred", state.FinalOutcomeDeferred, state.VideoStateDeferred},
		{"v-failed", state.FinalOutcomeFailed, state.VideoStateFailed},
		{"v-not-yet-evaluated", state.FinalOutcomeNotYetEvaluated, state.VideoStateNotYetEvaluated},
	}
	for i, c := range categories {
		_, err := stateStore.CreateVideo(ctx, &state.Video{
			VideoID: c.videoID, ChannelID: "c1",
			DiscoveredAt:      time.Now().UTC().Add(time.Duration(i) * time.Second),
			DiscoveredInRunID: runID, ProcessingState: c.processing, FinalOutcome: c.outcome,
		})
		require.NoError(t, err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	listVideos := func(query string) (videoListResponse, int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos"+query, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		var resp videoListResponse
		if rec.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		}
		return resp, rec.Code
	}

	t.Run("no filter returns every outcome category", func(t *testing.T) {
		resp, code := listVideos("")
		require.Equal(t, http.StatusOK, code)
		assert.Len(t, resp.Videos, len(categories))
		assert.Equal(t, int64(len(categories)), resp.Pagination.Total)
	})

	for _, c := range categories {
		t.Run("outcome="+string(c.outcome)+" returns only its videos", func(t *testing.T) {
			resp, code := listVideos("?outcome=" + string(c.outcome))
			require.Equal(t, http.StatusOK, code)
			require.Len(t, resp.Videos, 1)
			assert.Equal(t, c.videoID, resp.Videos[0].VideoID)
			assert.Equal(t, string(c.outcome), resp.Videos[0].FinalOutcome)
			// Pagination totals describe the filtered set, so a filtered
			// empty page and its total stay consistent for the client.
			assert.Equal(t, int64(1), resp.Pagination.Total)
			assert.Equal(t, 1, resp.Pagination.TotalPages)
		})
	}

	t.Run("outcome with no matches returns an empty page with a zero total", func(t *testing.T) {
		emptyRunID, err := stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted})
		require.NoError(t, err)
		_, err = stateStore.CreateVideo(ctx, &state.Video{
			VideoID: "v2-accepted", ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
			DiscoveredInRunID: emptyRunID, ProcessingState: state.VideoStateCompleted,
			FinalOutcome: state.FinalOutcomeAccepted,
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(emptyRunID, 10)+"/videos?outcome=failed", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp videoListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Empty(t, resp.Videos)
		assert.Equal(t, int64(0), resp.Pagination.Total)
		assert.Equal(t, 0, resp.Pagination.TotalPages)
	})

	t.Run("unknown outcome is rejected, not silently ignored", func(t *testing.T) {
		_, code := listVideos("?outcome=bogus")
		assert.Equal(t, http.StatusBadRequest, code)
	})
}

func TestHandleGetRunVideo(t *testing.T) {
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

	_, err = stateStore.CreateConditionAudit(ctx, &state.ConditionAudit{
		RouteDecisionID: decisionID, ConditionIndex: 0,
		Field: "title", Operator: "contains", ExpectedValue: "jazz",
		ActualValue: strPtr("live jazz performance"), Matched: true,
	})
	require.NoError(t, err)

	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: runID, VideoID: videoID,
		ActionType: "playlist_insert", TargetPlaylistID: "PL1",
		Outcome: state.ActionOutcomeSucceeded, ExecutedAt: timePtr(time.Now().UTC()),
	})
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/v1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"video":`)
	assert.Contains(t, body, `"video_id":"v1"`)
	assert.Contains(t, body, `"processing_state":"completed"`)
	assert.Contains(t, body, `"final_outcome":"accepted"`)
	assert.Contains(t, body, `"route_decisions":`)
	assert.Contains(t, body, `"condition_audits":`)
	assert.Contains(t, body, `"executed_actions":`)
	assert.Contains(t, body, `"route_id":"`+route.PublicID+`"`)
	assert.NotContains(t, body, `"route_id":`+strconv.FormatInt(route.ID, 10))
	assert.Contains(t, body, `"route_name":"Music"`)
	assert.Contains(t, body, `"matched":true`)
	assert.Contains(t, body, `"outcome":"succeeded"`)
}

func TestHandleGetRunVideo_RunNotFound(t *testing.T) {
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/999/videos/v1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetRunVideo_VideoNotFound(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/unknown", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetRunVideo_VideoNotInRun(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	run1 := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	run1ID, err := stateStore.CreateRun(ctx, run1)
	require.NoError(t, err)

	run2 := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	run2ID, err := stateStore.CreateRun(ctx, run2)
	require.NoError(t, err)

	video := &state.Video{
		VideoID: "v1", ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
		DiscoveredInRunID: run2ID, ProcessingState: state.VideoStateCompleted,
		FinalOutcome: state.FinalOutcomeAccepted,
	}
	_, err = stateStore.CreateVideo(ctx, video)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(logger),
		WithRunStore(stateStore),
		WithRunVideoStore(stateStore),
		WithRouteStore(routerStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(run1ID, 10)+"/videos/v1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// stubRouteIdentityStore overrides the registry lookup on top of the real
// router store so tests can simulate a missing or failing route_identity
// mapping while every other store behavior stays real.
type stubRouteIdentityStore struct {
	*router.Store
	identities map[int64]string
	err        error
}

func (s stubRouteIdentityStore) ListRouteIdentities(context.Context) (map[int64]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.identities, nil
}

// TestHandleRunVideos_SnapshotLabelAndIdentity proves the ARS-001/ARS-004 run
// evidence contract: `route_id` is the stable UUID and `route_name` comes from
// the run's immutable ConfigVersion snapshot, not from the current route row.
func TestHandleRunVideos_SnapshotLabelAndIdentity(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	versionID := captureConfigVersion(t, ctx, versioner)
	runID, _ := createRunWithDecision(t, ctx, stateStore, route.ID, &versionID)

	// Rename after the run: the evaluated label must not change.
	route.Name = "Rock"
	require.NoError(t, routerStore.UpdateRoute(ctx, route))

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))

	listReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code, listRec.Body.String())

	var list videoListResponse
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &list))
	require.Len(t, list.Videos, 1)
	require.Len(t, list.Videos[0].RouteDecisions, 1)
	assert.Equal(t, route.PublicID, list.Videos[0].RouteDecisions[0].RouteID)
	assert.Equal(t, "Music", list.Videos[0].RouteDecisions[0].RouteName)

	detailReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/v1", nil)
	detailRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(detailRec, detailReq)
	require.Equal(t, http.StatusOK, detailRec.Code, detailRec.Body.String())

	var detail videoDetailResponse
	require.NoError(t, json.Unmarshal(detailRec.Body.Bytes(), &detail))
	require.Len(t, detail.Video.RouteDecisions, 1)
	assert.Equal(t, route.PublicID, detail.Video.RouteDecisions[0].RouteID)
	assert.Equal(t, "Music", detail.Video.RouteDecisions[0].RouteName)
}

// TestHandleRunVideos_LegacyRunWithoutSnapshot uses the current persisted
// label when a run has no usable configuration snapshot (OC-003 incomplete
// context), while identity stays the stable UUID.
func TestHandleRunVideos_LegacyRunWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	runID, _ := createRunWithDecision(t, ctx, stateStore, route.ID, nil)

	route.Name = "Rock"
	require.NoError(t, routerStore.UpdateRoute(ctx, route))

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var list videoListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Videos, 1)
	require.Len(t, list.Videos[0].RouteDecisions, 1)
	assert.Equal(t, route.PublicID, list.Videos[0].RouteDecisions[0].RouteID)
	assert.Equal(t, "Rock", list.Videos[0].RouteDecisions[0].RouteName)
}

// TestHandleRunVideos_CorruptSnapshotFallsBack proves a stored snapshot that
// cannot be decoded is incomplete configuration context (OC-003), not an
// endpoint failure: evidence keeps serving with the current persisted label
// and the stable UUID.
func TestHandleRunVideos_CorruptSnapshotFallsBack(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	res, err := routerStore.DB().ExecContext(ctx,
		`INSERT INTO config_version (created_at, snapshot, change_description) VALUES (?, ?, ?);`,
		time.Now().UTC(), "{not json", "corrupt snapshot")
	require.NoError(t, err)
	badVersionID, err := res.LastInsertId()
	require.NoError(t, err)

	runID, _ := createRunWithDecision(t, ctx, stateStore, route.ID, &badVersionID)

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var list videoListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Videos, 1)
	require.Len(t, list.Videos[0].RouteDecisions, 1)
	assert.Equal(t, route.PublicID, list.Videos[0].RouteDecisions[0].RouteID)
	assert.Equal(t, "Music", list.Videos[0].RouteDecisions[0].RouteName)
}

// TestHandleRunVideos_SoftDeletedRoute keeps UUID identity and the evaluated
// label after the route is soft-deleted (ADR-003/ARS-004 historical
// preservation).
func TestHandleRunVideos_SoftDeletedRoute(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	versionID := captureConfigVersion(t, ctx, versioner)
	runID, _ := createRunWithDecision(t, ctx, stateStore, route.ID, &versionID)
	require.NoError(t, routerStore.DeleteRoute(ctx, route.ID))

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/v1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var detail videoDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	require.Len(t, detail.Video.RouteDecisions, 1)
	assert.Equal(t, route.PublicID, detail.Video.RouteDecisions[0].RouteID)
	assert.Equal(t, "Music", detail.Video.RouteDecisions[0].RouteName)
}

// TestHandleRunVideos_SnapshotOnlyRoute simulates a pre-ADR-003 hard delete:
// the route row is gone but the decision and ConfigVersion snapshot survive.
// The registry still resolves the UUID and the snapshot still supplies the
// evaluated label; nothing falls back to an integer or a current-name lookup.
func TestHandleRunVideos_SnapshotOnlyRoute(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	versionID := captureConfigVersion(t, ctx, versioner)
	runID, videoID := createRunWithDecision(t, ctx, stateStore, route.ID, &versionID)

	// Reproduce the legacy hard delete: route_decision has ON DELETE
	// CASCADE, so the orphaned decision only exists when the row was removed
	// with foreign keys unenforced, as old deployments did.
	conn, err := routerStore.DB().Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF;`)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `DELETE FROM route WHERE id = ?;`, route.ID)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`)
	require.NoError(t, err)

	decisions, err := stateStore.GetRouteDecisionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)

	// The registry mapping survives the route-row deletion.
	identities, err := routerStore.ListRouteIdentities(ctx)
	require.NoError(t, err)
	assert.Equal(t, route.PublicID, identities[route.ID])

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var list videoListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Videos, 1)
	require.Len(t, list.Videos[0].RouteDecisions, 1)
	assert.Equal(t, route.PublicID, list.Videos[0].RouteDecisions[0].RouteID)
	assert.Equal(t, "Music", list.Videos[0].RouteDecisions[0].RouteName)
}

// TestHandleRunVideos_DuplicateRouteNames proves two same-named routes stay
// independently identified by UUID in run evidence; the shared label never
// merges them.
func TestHandleRunVideos_DuplicateRouteNames(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()
	first := createRouteForRunVideoTests(t, ctx, routerStore)

	secondID, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "Music", EvaluationOrder: 2, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
		Exclusions:           []router.RouteExclusion{},
	})
	require.NoError(t, err)

	versionID := captureConfigVersion(t, ctx, versioner)
	runID, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeManual,
		Status: state.RunStatusCompleted, ConfigVersionID: &versionID,
	})
	require.NoError(t, err)
	videoID, err := stateStore.CreateVideo(ctx, &state.Video{
		VideoID: "v1", ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
		DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted,
		FinalOutcome: state.FinalOutcomeAccepted,
	})
	require.NoError(t, err)
	for _, d := range []struct {
		routeID  int64
		order    int
		decision state.RouteDecisionType
	}{
		{first.ID, 1, state.RouteDecisionMatched},
		{secondID, 2, state.RouteDecisionNotMatched},
	} {
		_, err = stateStore.CreateRouteDecision(ctx, &state.RouteDecision{
			VideoID: videoID, RunID: runID, RouteID: d.routeID,
			EvaluationOrder: d.order, Decision: d.decision,
		})
		require.NoError(t, err)
	}

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/v1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var detail videoDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	require.Len(t, detail.Video.RouteDecisions, 2)

	firstDecision, secondDecision := detail.Video.RouteDecisions[0], detail.Video.RouteDecisions[1]
	assert.Equal(t, first.PublicID, firstDecision.RouteID)
	assert.NotEqual(t, first.PublicID, secondDecision.RouteID)
	assert.Equal(t, 36, len(secondDecision.RouteID))
	assert.Equal(t, "Music", firstDecision.RouteName)
	assert.Equal(t, "Music", secondDecision.RouteName)
}

// TestHandleRunVideos_MissingIdentityMappingFailsClosed proves a decision
// whose route key has no route_identity mapping fails the endpoint (500)
// instead of emitting an integer or a name-only label (ARS-004 invariant 6).
func TestHandleRunVideos_MissingIdentityMappingFailsClosed(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	runID, _ := createRunWithDecision(t, ctx, stateStore, route.ID, nil)

	identities, err := routerStore.ListRouteIdentities(ctx)
	require.NoError(t, err)
	delete(identities, route.ID) // simulate the missing mapping under test

	srv := newRunVideoServer(stateStore, stubRouteIdentityStore{Store: routerStore, identities: identities},
		config.NewStore(routerStore.DB()))

	listReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, listReq)
	assert.Equal(t, http.StatusInternalServerError, listRec.Code)
	assert.NotContains(t, listRec.Body.String(), `"route_id"`)
	assert.NotContains(t, listRec.Body.String(), "route-")

	detailReq := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/v1", nil)
	detailRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(detailRec, detailReq)
	assert.Equal(t, http.StatusInternalServerError, detailRec.Code)
	assert.NotContains(t, detailRec.Body.String(), `"route_id"`)
}

// TestHandleRunVideos_IdentityLookupErrorFailsClosed proves a store failure
// while loading the registry fails the endpoint rather than degrading to an
// unlabelled or integer-identified response.
func TestHandleRunVideos_IdentityLookupErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	runID, _ := createRunWithDecision(t, ctx, stateStore, route.ID, nil)

	srv := newRunVideoServer(stateStore,
		stubRouteIdentityStore{Store: routerStore, err: errors.New("registry unavailable")},
		config.NewStore(routerStore.DB()))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"route_id"`)
}

func strPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}
