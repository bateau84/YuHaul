package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/state"
)

// spaContinuedAction mirrors web/src/types.ts ContinuedAction (extends
// ActionOutcome). Decoding with DisallowUnknownFields makes a wire field the
// SPA type does not declare, or a type mismatch, fail the test.
type spaContinuedAction struct {
	ID                       int64   `json:"id"`
	ActionType               string  `json:"action_type"`
	TargetPlaylistID         string  `json:"target_playlist_id"`
	Outcome                  string  `json:"outcome"`
	OutcomeReason            *string `json:"outcome_reason"`
	OutcomeReasonUnavailable *bool   `json:"outcome_reason_unavailable"`
	ErrorMessage             *string `json:"error_message"`
	ErrorMessageUnavailable  *bool   `json:"error_message_unavailable"`
	ExecutedAt               *string `json:"executed_at"`
	DecisionRunID            int64   `json:"decision_run_id"`
	RouteDecisionID          int64   `json:"route_decision_id"`
	RouteID                  string  `json:"route_id"`
	RouteName                *string `json:"route_name"`
}

// spaRunVideoDetail is the part of RunVideoDetail this contract adds.
type spaRunVideoDetail struct {
	Video struct {
		ContinuedActions *[]spaContinuedAction `json:"continued_actions"`
	} `json:"video"`
}

func getVideoDetailBody(t *testing.T, srv *Server, runID int64, externalID string) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/"+externalID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}

// TestRunVideoDetail_ContinuedActions drives the real handler over a migrated
// SQLite database: run 2 sends under run 1's decision. The sends appear under
// run 2's continued_actions with the original run, decision, public route id,
// the route name from run 1's snapshot (not the later rename), the outcome and
// the vetted reason; run 1 lists none. The body decodes strictly into the SPA
// shape.
func TestRunVideoDetail_ContinuedActions(t *testing.T) {
	ctx := context.Background()
	db, stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	versionID := captureConfigVersion(t, ctx, versioner)
	run1, videoKey := createRunWithDecision(t, ctx, stateStore, route.ID, &versionID)
	recordRunVideoOutcomeForTest(t, db, run1, videoKey, state.FinalOutcomeAccepted)

	var decisionID int64
	require.NoError(t, db.QueryRow(`SELECT id FROM route_decision WHERE run_id = ?`, run1).Scan(&decisionID))
	_, err := stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: run1, VideoID: videoKey,
		ActionType: "playlist_insert", TargetPlaylistID: "PL1",
		Outcome: state.ActionOutcomeSucceeded, ExecutedAt: timePtr(time.Now().UTC()),
	})
	require.NoError(t, err)

	run2, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeManual,
		Status: state.RunStatusCompleted, ConfigVersionID: &versionID,
	})
	require.NoError(t, err)
	// Run 2 is a member of the video but made no decision of its own.
	recordRunVideoOutcomeForTest(t, db, run2, videoKey, state.FinalOutcomeAccepted)
	reason := "quota_exhausted"
	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: run2, VideoID: videoKey,
		ActionType: "playlist_insert", TargetPlaylistID: "PL2",
		Outcome: state.ActionOutcomeSucceeded, ExecutedAt: timePtr(time.Now().UTC()),
	})
	require.NoError(t, err)
	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: run2, VideoID: videoKey,
		ActionType: "playlist_insert", TargetPlaylistID: "PL3",
		Outcome: state.ActionOutcomeDeferred, OutcomeReason: &reason,
	})
	require.NoError(t, err)

	// Rename after the decision: the label must stay the decision run's.
	route.Name = "Rock"
	require.NoError(t, routerStore.UpdateRoute(ctx, route))

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))

	body := getVideoDetailBody(t, srv, run2, "v1")
	// Top-level video fields beyond continued_actions are outside this shape,
	// so decode only the continued_actions subtree strictly.
	var raw struct {
		Video map[string]json.RawMessage `json:"video"`
	}
	require.NoError(t, json.Unmarshal(body, &raw))
	require.Contains(t, raw.Video, "continued_actions")
	items := json.NewDecoder(bytes.NewReader(raw.Video["continued_actions"]))
	items.DisallowUnknownFields()
	var actions []spaContinuedAction
	require.NoError(t, items.Decode(&actions), "continued_actions must decode into the SPA ContinuedAction shape")

	require.Len(t, actions, 2)
	assert.Equal(t, "PL2", actions[0].TargetPlaylistID)
	assert.Equal(t, "succeeded", actions[0].Outcome)
	assert.Equal(t, run1, actions[0].DecisionRunID)
	assert.Equal(t, decisionID, actions[0].RouteDecisionID)
	assert.Equal(t, route.PublicID, actions[0].RouteID)
	require.NotNil(t, actions[0].RouteName)
	assert.Equal(t, "Music", *actions[0].RouteName)
	assert.NotZero(t, actions[0].ID)
	assert.NotNil(t, actions[0].ExecutedAt)
	assert.Equal(t, "PL3", actions[1].TargetPlaylistID)
	assert.Equal(t, "deferred", actions[1].Outcome)
	require.NotNil(t, actions[1].OutcomeReason)
	assert.Equal(t, "quota_exhausted", *actions[1].OutcomeReason)
	assert.Nil(t, actions[1].ExecutedAt)

	// The original run lists none: an empty array, not null or absent.
	body1 := getVideoDetailBody(t, srv, run1, "v1")
	var first spaRunVideoDetail
	require.NoError(t, json.Unmarshal(body1, &first))
	require.NotNil(t, first.Video.ContinuedActions)
	assert.Empty(t, *first.Video.ContinuedActions)
	assert.Contains(t, string(body1), `"continued_actions":[]`)
}

// TestRunVideoDetail_ContinuedActionRouteNameNullWithoutSnapshot: the route
// name comes only from the decision run's snapshot and is null when that run
// has none.
func TestRunVideoDetail_ContinuedActionRouteNameNullWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	db, stateStore, routerStore, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routerStore)

	run1, videoKey := createRunWithDecision(t, ctx, stateStore, route.ID, nil)
	var decisionID int64
	require.NoError(t, db.QueryRow(`SELECT id FROM route_decision WHERE run_id = ?`, run1).Scan(&decisionID))
	run2, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted,
	})
	require.NoError(t, err)
	recordRunVideoOutcomeForTest(t, db, run2, videoKey, state.FinalOutcomeAccepted)
	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: run2, VideoID: videoKey,
		ActionType: "playlist_insert", TargetPlaylistID: "PL2",
		Outcome: state.ActionOutcomeSucceeded, ExecutedAt: timePtr(time.Now().UTC()),
	})
	require.NoError(t, err)

	srv := newRunVideoServer(stateStore, routerStore, config.NewStore(routerStore.DB()))
	var got spaRunVideoDetail
	require.NoError(t, json.Unmarshal(getVideoDetailBody(t, srv, run2, "v1"), &got))
	require.NotNil(t, got.Video.ContinuedActions)
	require.Len(t, *got.Video.ContinuedActions, 1)
	assert.Nil(t, (*got.Video.ContinuedActions)[0].RouteName)
	assert.Equal(t, route.PublicID, (*got.Video.ContinuedActions)[0].RouteID)
}
