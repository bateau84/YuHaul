package server

import (
	"context"
	"encoding/json"
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
	return route
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

func strPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}
