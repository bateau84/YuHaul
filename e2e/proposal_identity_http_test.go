//go:build integration

package e2e

// PA E3 assembly evidence for ADR-012 proposal-only route identity anchors.
//
// The B7 pipeline tests prove the proposal-ingest transaction against a real
// SQLite database, and the server tests prove the HTTP contract against a fake
// pipeline. This test closes the gap between them: a proposal UUID crosses the
// real, assembled HTTP server — the same server.NewServer composition
// cmd/server/main.go builds — into the real pipeline, through the real
// proposal-ingest transaction in real SQLite, and back out through a public
// decision surface as the same public UUID, while the active route
// configuration is unchanged.
//
// The only external substitution is the mock YouTube Data API v3 server that
// populates the retained corpus (the approved base-URL seam); the HTTP mux and
// handlers, pipeline, stores, and database are the real product layers.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/stats"
)

// e3ProposalUUID is a canonical UUIDv4 that is absent from the registry at the
// start of the test, so the backtest proposal exercises the ADR-012 §3
// allocate-new-internal-key path.
const e3ProposalUUID = "7c9e6d54-2b8f-4a13-9d6e-5f3a2b1c8e07"

func postJSON(t *testing.T, client *http.Client, url string, payload any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	resp, err := client.Post(url, "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

func getJSON(t *testing.T, client *http.Client, url string) (int, []byte) {
	t.Helper()
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// waitForRunCompleted polls the assembled GET /api/runs/{id} until the async
// pipeline goroutine the run-start handler spawned finishes the run.
func waitForRunCompleted(t *testing.T, client *http.Client, baseURL string, runID int64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, body := getJSON(t, client, fmt.Sprintf("%s/api/runs/%d", baseURL, runID))
		require.Equal(t, http.StatusOK, status, "GET /api/runs/%d: %s", runID, body)
		var run struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.Unmarshal(body, &run))
		if run.Status == string(state.RunStatusCompleted) {
			return
		}
		require.NotEqual(t, string(state.RunStatusFailed), run.Status,
			"run %d failed: %s", runID, body)
		require.NotEqual(t, string(state.RunStatusInterrupted), run.Status,
			"run %d interrupted: %s", runID, body)
		if time.Now().After(deadline) {
			t.Fatalf("run %d never completed (last status %q): %s", runID, run.Status, body)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// activeRoutes decodes GET /api/routes into its public UUID-keyed entries.
func activeRoutes(t *testing.T, client *http.Client, baseURL string) []map[string]any {
	t.Helper()
	status, body := getJSON(t, client, baseURL+"/api/routes")
	require.Equal(t, http.StatusOK, status, "GET /api/routes: %s", body)
	var resp struct {
		Routes []map[string]any `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Routes
}

// TestE2E_ProposalIdentityThroughAssembledHTTPServer sends a backtest proposal
// whose route UUID is unknown to the registry through the real assembled HTTP
// server, real pipeline, and real SQLite database, and proves the UUID comes
// back unchanged on the public decision surface while the active route
// configuration stays exactly as it was.
func TestE2E_ProposalIdentityThroughAssembledHTTPServer(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{
			makeVideo("V1", "Jazz Live Session", publishedAt),
			makeVideo("V2", "Rock Concert", publishedAt),
		},
	)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	// The real assembled API server: the same server.NewServer composition
	// cmd/server/main.go builds, over the same pipeline and stores, served on
	// a real httptest listener.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	statsSvc := stats.NewService(stats.NewStore(env.db), env.configStore, env.ledger)
	api := server.NewServer(env.pipeline, env.broadcast,
		server.WithServerLogger(logger),
		server.WithRouteStore(env.routerStore),
		server.WithRunStore(env.stateStore),
		server.WithRunVideoStore(env.stateStore),
		server.WithConfigVersionStore(env.configStore),
		server.WithAnalyticsHandler(stats.NewHandler(statsSvc, logger)),
	)
	httpSrv := httptest.NewServer(api.Handler())
	t.Cleanup(httpSrv.Close)
	client := httpSrv.Client()
	baseURL := httpSrv.URL

	// A production run through the assembled HTTP path populates the retained
	// video corpus (discovery and execution use only the mock YouTube seam).
	status, body := postJSON(t, client, baseURL+"/api/runs", nil)
	require.Equal(t, http.StatusAccepted, status, "POST /api/runs: %s", body)
	var prodRun struct {
		RunID int64 `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal(body, &prodRun))
	waitForRunCompleted(t, client, baseURL, prodRun.RunID)

	// Baseline: the active route configuration before the proposal.
	routesBefore := activeRoutes(t, client, baseURL)
	require.Len(t, routesBefore, 1, "only the Jazz route is active")
	activeUUID, _ := routesBefore[0]["id"].(string)
	require.NotEmpty(t, activeUUID)

	// A backtest proposal carrying a valid UUIDv4 that is not in the
	// registry (ADR-012 §3).
	status, body = postJSON(t, client, baseURL+"/api/backtest", map[string]any{
		"proposed_configuration": map[string]any{
			"duplicate_threshold": 0.5,
			"duplicate_behavior":  "skip",
			"routes": []map[string]any{{
				"id":                    e3ProposalUUID,
				"name":                  "ProposedRock",
				"evaluation_order":      1,
				"is_enabled":            true,
				"condition_composition": "AND",
				"conditions": []map[string]any{{
					"condition_index": 0,
					"field":           "title",
					"operator":        "contains",
					"value":           "rock",
				}},
				"actions": []map[string]any{{
					"action_index":       0,
					"action_type":        "add_to_playlist",
					"target_playlist_id": "PLproposed",
				}},
			}},
		},
	})
	require.Equal(t, http.StatusAccepted, status, "POST /api/backtest: %s", body)
	var backtestRun struct {
		RunID   int64  `json:"run_id"`
		RunType string `json:"run_type"`
		Status  string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(body, &backtestRun))
	assert.Equal(t, "backtest", backtestRun.RunType)
	require.Positive(t, backtestRun.RunID)
	waitForRunCompleted(t, client, baseURL, backtestRun.RunID)

	// The public decision surface for the backtest run projects the proposal
	// route back out as its original public UUID, resolved through the real
	// route_identity registry.
	status, body = getJSON(t, client,
		fmt.Sprintf("%s/api/runs/%d/route-stats", baseURL, backtestRun.RunID))
	require.Equal(t, http.StatusOK, status, "GET route-stats: %s", body)
	var statsResp struct {
		RouteStats []struct {
			RouteID       string `json:"route_id"`
			RouteName     string `json:"route_name"`
			VideosMatched int    `json:"videos_matched"`
		} `json:"route_stats"`
	}
	require.NoError(t, json.Unmarshal(body, &statsResp), "route-stats body: %s", body)

	var proposalEntry *struct {
		RouteID       string `json:"route_id"`
		RouteName     string `json:"route_name"`
		VideosMatched int    `json:"videos_matched"`
	}
	for i := range statsResp.RouteStats {
		entry := &statsResp.RouteStats[i]
		if entry.RouteID == e3ProposalUUID {
			proposalEntry = entry
		}
		// No surface may emit an internal integer route key (ARS-004).
		canonical, ok := router.ParsePublicRouteID(entry.RouteID)
		require.True(t, ok, "route-stats emitted a non-UUID route identity %q", entry.RouteID)
		assert.Equal(t, entry.RouteID, canonical, "route identity must be emitted in canonical lowercase form")
	}
	require.NotNil(t, proposalEntry,
		"the proposal route must appear in the backtest decision surface under its original UUID %s: %s",
		e3ProposalUUID, body)
	assert.Equal(t, "ProposedRock", proposalEntry.RouteName,
		"the decision label comes from the run's proposal snapshot")
	assert.GreaterOrEqual(t, proposalEntry.VideosMatched, 1,
		"the retained corpus must produce at least one FK-backed decision on the proposal anchor")

	// The active route configuration is unchanged by the proposal evaluation:
	// same route list, same UUIDs, same row metadata, and the identity anchor
	// is not readable through route CRUD.
	routesAfter := activeRoutes(t, client, baseURL)
	assert.Equal(t, routesBefore, routesAfter,
		"proposal evaluation must not change the active route configuration")
	status, body = getJSON(t, client, baseURL+"/api/routes/"+e3ProposalUUID)
	assert.Equal(t, http.StatusNotFound, status,
		"the identity anchor must not be readable through route CRUD: %s", body)

	// Real SQLite state behind the HTTP responses: one registry mapping, one
	// soft-deleted inactive anchor at the mapped key, FK-backed decisions.
	var mappingCount int
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM route_identity WHERE public_id = ?;`, e3ProposalUUID).Scan(&mappingCount))
	assert.Equal(t, 1, mappingCount, "exactly one durable mapping for the proposal UUID")

	var anchorKey int64
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT route_id FROM route_identity WHERE public_id = ?;`, e3ProposalUUID).Scan(&anchorKey))

	var deletedAt any
	var isEnabled bool
	var evaluationOrder int
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT deleted_at, is_enabled, evaluation_order FROM route WHERE id = ?;`, anchorKey).Scan(
		&deletedAt, &isEnabled, &evaluationOrder))
	assert.NotNil(t, deletedAt, "the identity anchor must be committed soft-deleted")
	assert.False(t, isEnabled, "the identity anchor must never be enabled")
	assert.Equal(t, -anchorKey, int64(evaluationOrder), "ADR-003 inactive sentinel evaluation_order = -id")

	var decisions int
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM route_decision WHERE run_id = ? AND route_id = ?;`,
		backtestRun.RunID, anchorKey).Scan(&decisions))
	assert.GreaterOrEqual(t, decisions, 1,
		"backtest decisions must reference the anchor's internal integer key")

	var fkViolations int
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_foreign_key_check;`).Scan(&fkViolations))
	assert.Zero(t, fkViolations, "route_decision.route_id -> route.id must hold after proposal evaluation")
}
