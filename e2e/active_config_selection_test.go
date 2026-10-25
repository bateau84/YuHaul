//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/stretchr/testify/require"
)

func TestProposalHistoryDoesNotSelectProductionConfiguration(t *testing.T) {
	for _, mode := range []string{"backtest", "dry-run", "backtest-same-threshold", "historical-replay"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			seed := makeVideo("seed", "classical unrelated example", now.Add(-2*time.Hour))
			env := setupEnv(t, []mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}}, []mockVideo{seed})
			savedRouteID := env.createRoute(t, "ActiveJazz", "jazz", "PLactive")
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			api := server.NewServer(env.pipeline, env.broadcast,
				server.WithServerLogger(logger), server.WithRouteStore(env.routerStore),
				server.WithRunStore(env.stateStore), server.WithRunVideoStore(env.stateStore),
				server.WithConfigVersionStore(env.configStore),
				server.WithConfigHandler(config.NewHandler(env.configSvc, logger)))
			srv := httptest.NewServer(api.Handler())
			defer srv.Close()
			client := srv.Client()
			req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", bytes.NewBufferString(`{"duplicate_threshold":0.99}`))
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
			startRun := func(path string, input any) int64 {
				t.Helper()
				code, body := postJSON(t, client, srv.URL+path, input)
				require.Equal(t, http.StatusAccepted, code, string(body))
				var started struct {
					RunID int64 `json:"run_id"`
				}
				require.NoError(t, json.Unmarshal(body, &started))
				waitForRunCompleted(t, client, srv.URL, started.RunID)
				return started.RunID
			}
			baseline, err := env.stateStore.GetRun(ctx, startRun("/api/runs", nil))
			require.NoError(t, err)
			path, key := "/api/backtest", "proposed_configuration"
			if mode == "dry-run" {
				path, key = "/api/runs/dry-run", "draft_configuration"
			}
			proposalThreshold := .5
			if mode == "backtest-same-threshold" {
				proposalThreshold = .99
			}
			proposalRunID := startRun(path, map[string]any{key: map[string]any{
				"duplicate_threshold": proposalThreshold, "duplicate_behavior": "skip",
				"routes": []map[string]any{{"id": e3ProposalUUID, "name": "ProposalRock", "evaluation_order": 1, "is_enabled": true, "condition_composition": "AND",
					"conditions": []map[string]any{{"field": "title", "operator": "contains", "value": "rock"}},
					"actions":    []map[string]any{{"action_type": "add_to_playlist", "target_playlist_id": "PLproposal"}}}},
			}})
			proposalRun, err := env.stateStore.GetRun(ctx, proposalRunID)
			require.NoError(t, err)
			proposal, err := env.configStore.Get(ctx, *proposalRun.ConfigVersionID)
			require.NoError(t, err)
			require.Equal(t, proposalThreshold, proposal.Snapshot.DuplicateThreshold)
			require.Equal(t, "ProposalRock", proposal.Snapshot.Routes[0].Name)
			if mode == "historical-replay" {
				historicalID := startRun("/api/runs/replay", map[string]any{"config_version_id": proposal.ID})
				historical, err := env.stateStore.GetRun(ctx, historicalID)
				require.NoError(t, err)
				require.Equal(t, proposalRun.ConfigVersionID, historical.ConfigVersionID)
			}
			_, err = duplicate.SaveFingerprint(ctx, env.db, "reference", "UC-ref", "user_import", "jazz live session recording", true, nil)
			require.NoError(t, err)
			env.server.mu.Lock()
			env.server.videos = []mockVideo{seed, makeVideo("candidate", "jazz live session today", time.Now().UTC())}
			env.server.mu.Unlock()
			prodID := startRun("/api/runs", nil)
			prod, err := env.stateStore.GetRun(ctx, prodID)
			require.NoError(t, err)
			require.Equal(t, baseline.ConfigVersionID, prod.ConfigVersionID)
			selected, err := env.configStore.Get(ctx, *prod.ConfigVersionID)
			require.NoError(t, err)
			require.Equal(t, .99, selected.Snapshot.DuplicateThreshold)
			require.Equal(t, "ActiveJazz", selected.Snapshot.Routes[0].Name)
			require.Equal(t, savedRouteID, selected.Snapshot.Routes[0].ID)
			var matches int
			require.NoError(t, env.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM duplicate_match dm JOIN video v ON v.id=dm.video_id WHERE v.video_id='candidate'`).Scan(&matches))
			require.Zero(t, matches)
			insertions := env.server.Insertions()
			require.Len(t, insertions, 1)
			require.Equal(t, "PLactive", insertions[0].PlaylistID)
			require.Equal(t, "candidate", insertions[0].VideoID)
			unchanged, err := env.configStore.Get(ctx, proposal.ID)
			require.NoError(t, err)
			require.Equal(t, proposal, unchanged)
			code, settings := getJSON(t, client, srv.URL+"/api/settings")
			require.Equal(t, http.StatusOK, code)
			var saved struct {
				Threshold float64 `json:"duplicate_threshold"`
			}
			require.NoError(t, json.Unmarshal(settings, &saved))
			require.Equal(t, .99, saved.Threshold)
		})
	}
}
