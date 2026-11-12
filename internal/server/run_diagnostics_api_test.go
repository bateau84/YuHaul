package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

// diagGetJSON performs a GET against the test server and decodes the response
// body into a generic map.
func diagGetJSON(t *testing.T, srv *Server, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var body map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return rec.Code, body
}

func TestGetRunShowsFinalStatusAfterDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.db")
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	runID, err := stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning})
	require.NoError(t, err)
	require.NoError(t, stateStore.FailRun(ctx, runID, "run completion failed", 1, 0))
	require.NoError(t, db.Close())

	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _ = os.Remove(path) })
	stateStore, err = state.NewStore(db)
	require.NoError(t, err)
	code, body := diagGetJSON(t, diagServer(stateStore, nil, nil), "/api/runs/"+strconv.FormatInt(runID, 10))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, string(state.RunStatusFailed), body["status"])
	assert.NotNil(t, body["completed_at"])
	assert.NotNil(t, body["interrupt_reason"])
}

func TestSimulationProducerFailureSurvivesReopenAndRegisteredHTTPRead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "producer.db")
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	machine := state.NewMachine(stateStore)
	producer := pipeline.NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, nil)
	run, err := producer.DryRun(ctx, nil)
	require.NoError(t, err)
	producer.ExecuteDryRun(ctx, run.ID) // Missing routes causes a real producer failure.
	loaded, err := stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusFailed, loaded.Status)
	require.NotNil(t, loaded.CompletedAt)
	require.NotNil(t, loaded.InterruptReason)
	require.NoError(t, db.Close())

	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _ = os.Remove(path) })
	stateStore, err = state.NewStore(db)
	require.NoError(t, err)
	code, body := diagGetJSON(t, diagServer(stateStore, nil, nil), "/api/runs/"+strconv.FormatInt(run.ID, 10))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, string(state.RunStatusFailed), body["status"])
	assert.NotNil(t, body["completed_at"])
	assert.NotNil(t, body["interrupt_reason"])
}

// diagServer builds a server exposing the run and run-video reads.
func diagServer(stateStore *state.Store, routeStore RouteStore, cfgStore ConfigVersionStore) *Server {
	return newRunVideoServer(stateStore, routeStore, cfgStore)
}

// TestHandleRunAPIProjectsSelectedAndEvaluatedCounts proves the requested-run
// API distinguishes an unknown selected count (null) from a captured zero,
// keeps videos_discovered unchanged, and projects videos_evaluated as the
// distinct decision-bearing video count for that run only (ARD-001/BR-020).
func TestHandleRunAPIProjectsSelectedAndEvaluatedCounts(t *testing.T) {
	ctx := context.Background()
	_, stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()

	// Legacy run: no selection captured, no decision evidence.
	legacy, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest,
		Status: state.RunStatusCompleted,
	})
	require.NoError(t, err)

	// Empty selection: selection completed with zero inputs.
	emptyRun, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeDryRun,
		Status: state.RunStatusCompleted,
	})
	require.NoError(t, err)
	require.NoError(t, stateStore.SetRunSelectedCount(ctx, emptyRun, 0))

	// Partial run: five inputs selected, one video bearing two decisions.
	partialRun, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeManual,
		Status: state.RunStatusCompleted,
	})
	require.NoError(t, err)
	require.NoError(t, stateStore.SetRunSelectedCount(ctx, partialRun, 5))

	route := createRouteForRunVideoTests(t, ctx, routerStore)
	otherRoute, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "Other", EvaluationOrder: 2, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "rock"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL2"}},
	})
	require.NoError(t, err)

	newVideo := func(t *testing.T, runID int64, externalID string) int64 {
		t.Helper()
		id, err := stateStore.CreateVideo(ctx, &state.Video{
			VideoID: externalID, ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
			DiscoveredInRunID: runID, ProcessingState: state.VideoStateNotYetEvaluated,
			FinalOutcome: state.FinalOutcomeNotYetEvaluated,
		})
		require.NoError(t, err)
		return id
	}
	decide := func(t *testing.T, runID, videoID, routeID int64, order int) {
		t.Helper()
		_, err := stateStore.CreateRouteDecision(ctx, &state.RouteDecision{
			VideoID: videoID, RunID: runID, RouteID: routeID,
			EvaluationOrder: order, Decision: state.RouteDecisionMatched,
		})
		require.NoError(t, err)
	}

	v1 := newVideo(t, partialRun, "p1")
	newVideo(t, partialRun, "p2")
	decide(t, partialRun, v1, route.ID, 1)
	decide(t, partialRun, v1, otherRoute, 2)

	// Production discovery counting keeps its own meaning for this run.
	require.NoError(t, stateStore.RecomputeRunCounters(ctx, partialRun))

	srv := diagServer(stateStore, routerStore, versioner)

	for _, tc := range []struct {
		name      string
		runID     int64
		selected  any // nil means JSON null; a number means a captured count
		evaluated float64
	}{
		{"legacy run keeps unknown selection", legacy, nil, 0},
		{"captured empty selection is zero", emptyRun, float64(0), 0},
		{"partial selection counts distinct evaluated videos", partialRun, float64(5), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := diagGetJSON(t, srv, "/api/runs/"+strconv.FormatInt(tc.runID, 10))
			require.Equal(t, http.StatusOK, code)

			raw, ok := body["videos_selected"]
			require.True(t, ok, "videos_selected must be present in the run response")
			if tc.selected == nil {
				assert.Nil(t, raw, "an uncaptured selection must serialize as null, never zero")
			} else {
				assert.Equal(t, tc.selected, raw, "a captured selection must serialize as its count")
			}
			assert.Equal(t, tc.evaluated, body["videos_evaluated"],
				"videos_evaluated must count distinct decision-bearing videos for the run")
		})
	}

	// videos_discovered keeps its existing meaning and presence.
	code, body := diagGetJSON(t, srv, "/api/runs/"+strconv.FormatInt(partialRun, 10))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(2), body["videos_discovered"],
		"videos_discovered counts videos discovered in the run, independent of the evaluated projection")

	// The list endpoint carries the same projections.
	code, list := diagGetJSON(t, srv, "/api/runs")
	require.Equal(t, http.StatusOK, code)
	runs, ok := list["runs"].([]any)
	require.True(t, ok)
	require.Len(t, runs, 3)
	for _, raw := range runs {
		run := raw.(map[string]any)
		assert.Contains(t, run, "videos_selected")
		assert.Contains(t, run, "videos_evaluated")
	}
}

// TestHandleGetRunVideoProjectsOutcomeReason proves action detail exposes an
// optional outcome_reason without adding an outcome category, and omits the
// key entirely when no reason was captured.
func TestHandleGetRunVideoProjectsOutcomeReason(t *testing.T) {
	ctx := context.Background()
	db, stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
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

	reason := "already_completed"
	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: runID, VideoID: videoID,
		ActionType: "playlist_insert", TargetPlaylistID: "PL1",
		Outcome: state.ActionOutcomeNotAttempted, OutcomeReason: &reason,
	})
	require.NoError(t, err)
	invalidReasonActionID, err := stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: runID, VideoID: videoID,
		ActionType: "playlist_insert", TargetPlaylistID: "PL2",
		Outcome: state.ActionOutcomeSucceeded,
	})
	require.NoError(t, err)
	// Simulate a pre-guard historical row; production writes reject this value.
	_, err = db.ExecContext(ctx, `UPDATE executed_action SET outcome_reason=? WHERE id=?`,
		"TOKEN_SENTINEL_plain_ascii_7341", invalidReasonActionID)
	require.NoError(t, err)
	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: runID, VideoID: videoID,
		ActionType: "playlist_insert", TargetPlaylistID: "PL3",
		Outcome: state.ActionOutcomeSucceeded,
	})
	require.NoError(t, err)

	srv := diagServer(stateStore, routerStore, versioner)
	code, body := diagGetJSON(t, srv, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/v1")
	require.Equal(t, http.StatusOK, code)

	videoDetail, ok := body["video"].(map[string]any)
	require.True(t, ok)
	decisions, ok := videoDetail["route_decisions"].([]any)
	require.True(t, ok)
	require.Len(t, decisions, 1)
	actions, ok := decisions[0].(map[string]any)["executed_actions"].([]any)
	require.True(t, ok)
	require.Len(t, actions, 3)

	byTarget := make(map[string]map[string]any, len(actions))
	for _, raw := range actions {
		action := raw.(map[string]any)
		byTarget[action["target_playlist_id"].(string)] = action
	}

	withReason := byTarget["PL1"]
	assert.Equal(t, "not_attempted", withReason["outcome"], "the outcome vocabulary is unchanged")
	assert.Equal(t, "already_completed", withReason["outcome_reason"])

	withoutReason := byTarget["PL2"]
	assert.NotContains(t, withoutReason, "outcome_reason",
		"an invalid legacy reason must be withheld, never echoed")
	assert.Equal(t, true, withoutReason["outcome_reason_unavailable"])
	withoutCapture := byTarget["PL3"]
	assert.NotContains(t, withoutCapture, "outcome_reason")
	assert.NotContains(t, withoutCapture, "outcome_reason_unavailable")
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "TOKEN_SENTINEL_plain_ascii_7341")
}
