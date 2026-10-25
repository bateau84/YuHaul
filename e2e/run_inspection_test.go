//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

type inspectedVideo struct {
	VideoID        string `json:"video_id"`
	FinalOutcome   string `json:"final_outcome"`
	RouteDecisions []struct {
		Decision        string `json:"decision"`
		ExecutedActions []struct {
			Target  string `json:"target_playlist_id"`
			Outcome string `json:"outcome"`
		} `json:"executed_actions"`
	} `json:"route_decisions"`
}

func inspectionServer(env *testEnv) *httptest.Server {
	api := server.NewServer(env.pipeline, env.broadcast,
		server.WithRouteStore(env.routerStore), server.WithRunStore(env.stateStore),
		server.WithRunVideoStore(env.stateStore), server.WithConfigVersionStore(env.configStore),
		server.WithConfigHandler(config.NewHandler(env.configSvc, nil)))
	return httptest.NewServer(api.Handler())
}

func inspectList(t *testing.T, api *httptest.Server, runID int64, query string, total int, ids ...string) {
	t.Helper()
	status, body := getJSON(t, api.Client(), fmt.Sprintf("%s/api/runs/%d/videos%s", api.URL, runID, query))
	require.Equal(t, http.StatusOK, status, string(body))
	var response struct {
		Videos     []inspectedVideo `json:"videos"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.Equal(t, total, response.Pagination.Total)
	require.NotNil(t, response.Videos)
	var actual []string
	for _, v := range response.Videos {
		actual = append(actual, v.VideoID)
		require.Len(t, v.RouteDecisions, 1, "another run's decisions must not leak")
	}
	require.Equal(t, ids, actual)
}

func inspectDetail(t *testing.T, api *httptest.Server, runID int64, videoID, outcome, decision, target, actionOutcome string) {
	t.Helper()
	status, body := getJSON(t, api.Client(), fmt.Sprintf("%s/api/runs/%d/videos/%s", api.URL, runID, videoID))
	require.Equal(t, http.StatusOK, status, string(body))
	var response struct {
		Video inspectedVideo `json:"video"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.Equal(t, outcome, response.Video.FinalOutcome)
	require.Len(t, response.Video.RouteDecisions, 1)
	d := response.Video.RouteDecisions[0]
	require.Equal(t, decision, d.Decision)
	if target == "" {
		require.Empty(t, d.ExecutedActions)
	} else {
		require.Len(t, d.ExecutedActions, 1)
		require.Equal(t, target, d.ExecutedActions[0].Target)
		require.Equal(t, actionOutcome, d.ExecutedActions[0].Outcome)
	}
}

func TestRunInspection_HistoricalHTTP(t *testing.T) {
	for _, mode := range []string{"replay", "backtest"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			env := setupEnv(t, []mockChannel{{ID: "UC1", Title: "Channel", UploadsPlaylistID: "UU1"}}, []mockVideo{
				makeVideo("jazz1", "jazz one", now.Add(-3*time.Hour)),
				makeVideo("jazz2", "jazz two", now.Add(-2*time.Hour)),
				makeVideo("classical", "classical music", now.Add(-time.Hour)),
			})
			routeID := env.createRoute(t, "Music", "classical", "PLoriginal")
			original := env.runProductionRun(t)
			require.Equal(t, 1, original.VideosAccepted)
			require.Equal(t, 2, original.VideosRejected)
			before, err := env.stateStore.ListRetainedVideos(ctx)
			require.NoError(t, err)
			route, err := env.routerStore.GetRoute(ctx, routeID)
			require.NoError(t, err)
			route.Conditions[0].Value = "jazz"
			route.Actions[0].TargetPlaylistID = "PLsimulation"
			require.NoError(t, env.routerStore.UpdateRoute(ctx, route))
			api := inspectionServer(env)
			defer api.Close()
			path, payload := "/api/runs/replay", map[string]any{}
			if mode == "backtest" {
				path = "/api/backtest"
				payload["proposed_configuration"] = map[string]any{"duplicate_threshold": .9, "routes": activeRoutes(t, api.Client(), api.URL)}
			}
			status, body := postJSON(t, api.Client(), api.URL+path, payload)
			require.Equal(t, http.StatusAccepted, status, string(body))
			var started struct {
				RunID int64 `json:"run_id"`
			}
			require.NoError(t, json.Unmarshal(body, &started))
			waitForRunCompleted(t, api.Client(), api.URL, started.RunID)
			run, err := env.stateStore.GetRun(ctx, started.RunID)
			require.NoError(t, err)
			require.Equal(t, 2, run.VideosAccepted)
			require.Equal(t, 1, run.VideosRejected)
			// All discoveries share a timestamp: the ID tie-breaker makes paging stable.
			inspectList(t, api, run.ID, "?page_size=1", 3, "classical")
			inspectList(t, api, run.ID, "?outcome=accepted&page_size=1", 2, "jazz2")
			inspectList(t, api, run.ID, "?outcome=accepted&page_size=1&page=2", 2, "jazz1")
			inspectList(t, api, run.ID, "?outcome=rejected", 1, "classical")
			inspectDetail(t, api, run.ID, "jazz1", "accepted", "matched", "PLsimulation", "not_attempted")
			inspectDetail(t, api, run.ID, "classical", "rejected", "not_matched", "", "")
			inspectDetail(t, api, original.ID, "classical", "accepted", "matched", "PLoriginal", "succeeded")
			unrelated, err := env.stateStore.CreateRun(ctx, &state.Run{StartedAt: now, RunType: state.RunTypeReplay, Status: state.RunStatusCompleted})
			require.NoError(t, err)
			status, body = getJSON(t, api.Client(), fmt.Sprintf("%s/api/runs/%d/videos/jazz1", api.URL, unrelated))
			require.Equal(t, http.StatusNotFound, status, string(body))
			inspectList(t, api, unrelated, "", 0)
			after, err := env.stateStore.ListRetainedVideos(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after, "inspection/simulation must not move discovery or mutate production results")
		})
	}
}
