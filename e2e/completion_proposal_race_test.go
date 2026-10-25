//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// Exercise the real asynchronous HTTP path with independent databases in
// parallel. Immediately submit a proposal at the first observed completed
// response; never retry a failed start or delay to let finalization settle.
func TestE2E_CompletedProductionImmediatelyAcceptsProposal(t *testing.T) {
	for _, mode := range []string{"backtest", "dry-run"} {
		for worker := range 2 {
			t.Run(fmt.Sprintf("%s/worker%d", mode, worker), func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				env := setupEnv(t, []mockChannel{{ID: "UC1", Title: "Channel", UploadsPlaylistID: "UU1"}}, nil)
				env.createRoute(t, "ActiveJazz", "jazz", "PLactive")
				require.NoError(t, env.configSvc.SetDuplicateThreshold(ctx, .99))
				api := server.NewServer(env.pipeline, env.broadcast,
					server.WithRouteStore(env.routerStore), server.WithRunStore(env.stateStore),
					server.WithConfigVersionStore(env.configStore),
					server.WithConfigHandler(config.NewHandler(env.configSvc, nil)))
				srv := httptest.NewServer(api.Handler())
				defer srv.Close()
				client := srv.Client()
				// Poll only while work is genuinely running, with no sleeps or
				// assertion retries. Completed responses are themselves checked.
				wait := func(id int64) state.Run {
					t.Helper()
					deadline := time.Now().Add(20 * time.Second)
					for time.Now().Before(deadline) {
						code, body := getJSON(t, client, fmt.Sprintf("%s/api/runs/%d", srv.URL, id))
						require.Equal(t, http.StatusOK, code, string(body))
						var run state.Run
						require.NoError(t, json.Unmarshal(body, &run))
						if run.Status == state.RunStatusCompleted {
							return run
						}
						require.Equal(t, state.RunStatusRunning, run.Status, string(body))
					}
					t.Fatal("run did not complete")
					return state.Run{}
				}
				start := func(path string, input any) int64 {
					t.Helper()
					code, body := postJSON(t, client, srv.URL+path, input)
					require.Equal(t, http.StatusAccepted, code, string(body))
					var started struct {
						RunID int64 `json:"run_id"`
					}
					require.NoError(t, json.Unmarshal(body, &started))
					return started.RunID
				}
				path, key := "/api/backtest", "proposed_configuration"
				if mode == "dry-run" {
					path, key = "/api/runs/dry-run", "draft_configuration"
				}
				var activeID *int64
				const iterations = 25
				for i := range iterations {
					env.server.mu.Lock()
					env.server.videos = append(env.server.videos, makeVideo(fmt.Sprintf("candidate-%d", i), fmt.Sprintf("jazz session number %d", i), time.Now().UTC()))
					env.server.mu.Unlock()
					production := wait(start("/api/runs", nil))
					// The very next HTTP request after completed is proposal ingest.
					proposalID := start(path, map[string]any{key: map[string]any{
						"duplicate_threshold": .5,
						"routes": []map[string]any{{"id": e3ProposalUUID, "name": "ProposalRock", "evaluation_order": 1, "is_enabled": true, "condition_composition": "AND",
							"conditions": []map[string]any{{"field": "title", "operator": "contains", "value": "rock"}},
							"actions":    []map[string]any{{"action_type": "add_to_playlist", "target_playlist_id": "PLproposal"}}}},
					}})
					require.Equal(t, 1, production.VideosDiscovered, "final counters must be present in the first completed response")
					require.Equal(t, 1, production.VideosAccepted)
					if activeID == nil {
						activeID = production.ConfigVersionID
					}
					require.Equal(t, activeID, production.ConfigVersionID, "proposal history must not activate configuration")
					proposal := wait(proposalID)
					cv, err := env.configStore.Get(ctx, *proposal.ConfigVersionID)
					require.NoError(t, err)
					require.Equal(t, .5, cv.Snapshot.DuplicateThreshold)
					require.Len(t, cv.Snapshot.Routes, 1)
					var mapped int64
					require.NoError(t, env.db.QueryRowContext(ctx, `SELECT ri.route_id FROM route_identity ri JOIN route r ON r.id = ri.route_id WHERE ri.public_id = ? AND r.deleted_at IS NOT NULL AND r.is_enabled = 0`, e3ProposalUUID).Scan(&mapped))
					require.Equal(t, mapped, cv.Snapshot.Routes[0].ID)
				}
				resp, err := client.Get(srv.URL + "/api/routes")
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.NotContains(t, string(body), e3ProposalUUID)
				t.Logf("%d immediate completed→proposal transitions, zero rejected starts or stale production counters", iterations)
			})
		}
	}
}
