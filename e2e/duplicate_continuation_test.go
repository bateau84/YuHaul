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
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestE2E_ProductionDuplicateContinuationReferenceKinds(t *testing.T) {
	for _, source := range []string{"imported", "native"} {
		for _, kind := range []string{"exact", "fuzzy"} {
			t.Run(source+"/"+kind, func(t *testing.T) {
				ctx := context.Background()
				referenceTitle, candidateTitle := "jazz live session recording", "jazz live session recording"
				if kind == "fuzzy" {
					candidateTitle = "jazz live session today"
				}
				var env *testEnv
				if source == "imported" {
					c := setupImportedCorpus(t, "reference", "candidate", referenceTitle, false)
					env = c.env
					env.server.videos[0].Title = candidateTitle
					env.createRoute(t, "Jazz", "jazz", "PLjazz")
				} else {
					env = setupEnv(t, []mockChannel{{ID: "UC1", Title: "native", UploadsPlaylistID: "UU1"}}, []mockVideo{
						makeVideo("reference", referenceTitle, time.Now().UTC().Add(-time.Hour)),
					})
					env.createRoute(t, "Jazz", "jazz", "PLjazz")
					first := env.runProductionRun(t)
					require.Equal(t, 1, first.VideosAccepted)
					env.server.mu.Lock()
					env.server.videos = append(env.server.videos, makeVideo("candidate", candidateTitle, time.Now().UTC()))
					env.server.mu.Unlock()
				}
				require.NoError(t, env.configSvc.SetDuplicateThreshold(ctx, .55))
				run := env.runProductionRun(t)
				video, err := env.stateStore.GetVideoByExternalID(ctx, "candidate")
				require.NoError(t, err)
				require.Equal(t, state.FinalOutcomeAccepted, video.FinalOutcome)
				require.Equal(t, 1, run.VideosAccepted)
				var matchType string
				var score float64
				require.NoError(t, env.db.QueryRowContext(ctx, `SELECT match_type, similarity_score FROM duplicate_match WHERE video_id = ?`, video.ID).Scan(&matchType, &score))
				if kind == "fuzzy" {
					require.Equal(t, string(duplicate.MatchTypeFuzzy), matchType)
					require.InDelta(t, .6, score, .00001)
				} else {
					require.Equal(t, string(duplicate.MatchTypeExact), matchType)
					require.Equal(t, 1.0, score)
				}
				actions, err := env.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
				require.NoError(t, err)
				require.Len(t, actions, 1)
				require.Equal(t, state.ActionOutcomeSucceeded, actions[0].Outcome)
				require.Equal(t, run.ID, actions[0].RunID)
				inserted := 0
				for _, insertion := range env.server.Insertions() {
					if insertion.VideoID == "candidate" {
						inserted++
					}
				}
				require.Equal(t, 1, inserted)
			})
		}
	}
}

// Importer -> real HTTP production start -> discovery/detector/router/action ->
// real HTTP replay/backtest -> durable results. Only external YouTube is faked.
func TestE2E_DuplicateContinuationHTTPComposition(t *testing.T) {
	ctx := context.Background()
	c := setupImportedCorpus(t, "reference", "candidate", "Jazz Live Session", false)
	env := c.env
	c.assertImportedReferenceOnly(t)
	require.NoError(t, env.configStore.SetSetting(ctx, "duplicate_behavior", "skip"))
	env.createRoute(t, "Jazz", "jazz", "PLjazz")
	api := server.NewServer(env.pipeline, env.broadcast,
		server.WithRouteStore(env.routerStore), server.WithRunStore(env.stateStore),
		server.WithRunVideoStore(env.stateStore), server.WithConfigVersionStore(env.configStore),
		server.WithConfigHandler(config.NewHandler(env.configSvc, nil)))
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	client, base := httpServer.Client(), httpServer.URL
	status, body := postJSON(t, client, base+"/api/runs", nil)
	require.Equal(t, http.StatusAccepted, status, string(body))
	var started struct {
		RunID int64 `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal(body, &started))
	waitForRunCompleted(t, client, base, started.RunID)
	c.assertDistinctMatchRecordsImportedReference(t, "candidate")
	video, err := env.stateStore.GetVideoByExternalID(ctx, "candidate")
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeAccepted, video.FinalOutcome)
	require.Len(t, env.server.Insertions(), 1, "a detected duplicate must reach a real playlist action")
	prodRun, err := env.stateStore.GetRun(ctx, started.RunID)
	require.NoError(t, err)
	selected, err := env.configStore.Get(ctx, *prodRun.ConfigVersionID)
	require.NoError(t, err)
	require.Equal(t, "continue", selected.Snapshot.DuplicateBehavior)
	// Historical selection must not alter the recorded skip field or old run.
	selected.Snapshot.DuplicateBehavior = "skip"
	legacyID, err := env.configStore.Create(ctx, selected.Snapshot, "legacy recorded snapshot")
	require.NoError(t, err)
	var legacyBefore string
	require.NoError(t, env.db.QueryRowContext(ctx, `SELECT snapshot FROM config_version WHERE id = ?`, legacyID).Scan(&legacyBefore))
	productionActions, err := env.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Len(t, productionActions, 1)
	require.Equal(t, state.ActionOutcomeSucceeded, productionActions[0].Outcome)
	match, err := duplicate.NewDetector(env.db).Detect(ctx, "candidate", "Jazz Live Session")
	require.NoError(t, err)
	require.NotNil(t, match)
	requestsBefore := env.server.Requests()
	insertionsBefore := env.server.Insertions()

	status, body = postJSON(t, client, base+"/api/runs/replay", map[string]any{"config_version_id": legacyID})
	require.Equal(t, http.StatusAccepted, status, string(body))
	require.NoError(t, json.Unmarshal(body, &started))
	waitForRunCompleted(t, client, base, started.RunID)
	assertSimulationAccepted := func(runID int64) {
		t.Helper()
		run, err := env.stateStore.GetRun(ctx, runID)
		require.NoError(t, err)
		require.Equal(t, []int{1, 1, 0, 0}, []int{run.VideosDiscovered, run.VideosAccepted, run.VideosRejected, run.VideosExcluded})
		decisions, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, runID)
		require.NoError(t, err)
		require.Len(t, decisions, 1)
		require.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
		var simulated int
		require.NoError(t, env.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action WHERE run_id = ? AND outcome = 'not_attempted'`, runID).Scan(&simulated))
		require.Equal(t, 1, simulated)
	}
	assertSimulationAccepted(started.RunID)
	replayed, err := env.stateStore.GetRun(ctx, started.RunID)
	require.NoError(t, err)
	require.Equal(t, legacyID, *replayed.ConfigVersionID)
	// Exercise the real request decoder and proposal capture boundary, not a
	// fake pipeline. Null/omitted are admitted, non-string values still fail.
	for _, legacy := range []any{"skip", "flag", "", "arbitrary", nil, "omitted"} {
		for _, mode := range []string{"backtest", "dry-run"} {
			proposal := map[string]any{
				"duplicate_threshold": 0.5,
				"routes":              activeRoutes(t, client, base),
			}
			if legacy != "omitted" {
				proposal["duplicate_behavior"] = legacy
			}
			path, key := "/api/backtest", "proposed_configuration"
			if mode == "dry-run" {
				path, key = "/api/runs/dry-run", "draft_configuration"
			}
			status, body = postJSON(t, client, base+path, map[string]any{key: proposal})
			require.Equal(t, http.StatusAccepted, status, string(body))
			require.NoError(t, json.Unmarshal(body, &started))
			waitForRunCompleted(t, client, base, started.RunID)
			run, err := env.stateStore.GetRun(ctx, started.RunID)
			require.NoError(t, err)
			cv, err := env.configStore.Get(ctx, *run.ConfigVersionID)
			require.NoError(t, err)
			require.Equal(t, "continue", cv.Snapshot.DuplicateBehavior)
			if mode == "backtest" {
				assertSimulationAccepted(started.RunID)
			}
		}
	}
	for _, invalid := range []any{1, false, map[string]any{}, []string{"skip"}} {
		for _, endpoint := range []struct{ path, key string }{{"/api/backtest", "proposed_configuration"}, {"/api/runs/dry-run", "draft_configuration"}} {
			status, body = postJSON(t, client, base+endpoint.path, map[string]any{endpoint.key: map[string]any{
				"duplicate_threshold": 0.5, "duplicate_behavior": invalid, "routes": activeRoutes(t, client, base),
			}})
			require.Equal(t, http.StatusBadRequest, status, string(body))
		}
	}
	require.Equal(t, requestsBefore, env.server.Requests(), "historical evaluation makes no external requests with retained metadata")
	require.Equal(t, insertionsBefore, env.server.Insertions())
	videoAfter, err := env.stateStore.GetVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Equal(t, video, videoAfter)
	prodAfter, err := env.stateStore.GetRun(ctx, prodRun.ID)
	require.NoError(t, err)
	require.Equal(t, prodRun, prodAfter)
	var legacyAfter string
	require.NoError(t, env.db.QueryRowContext(ctx, `SELECT snapshot FROM config_version WHERE id = ?`, legacyID).Scan(&legacyAfter))
	require.Equal(t, legacyBefore, legacyAfter)
	require.Equal(t, 1, c.countRows(t, `SELECT COUNT(*) FROM duplicate_match`))
	for _, action := range productionActions {
		var outcome string
		require.NoError(t, env.db.QueryRowContext(ctx, `SELECT outcome FROM executed_action WHERE id = ?`, action.ID).Scan(&outcome))
		require.Equal(t, string(action.Outcome), outcome)
	}
	status, body = getJSON(t, client, fmt.Sprintf("%s/api/config/versions/%d", base, legacyID))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(body), `"duplicate_behavior":"skip"`)
}
