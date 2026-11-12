package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// diagAPISentinel stands in for legacy unsafe free text (raw provider bodies,
// secrets) already stored in run/video/action/queue rows. It must never
// appear in any API response; safe text alongside it must remain visible
// (ARD-001 failure diagnostic boundary).
const diagAPISentinel = "TOKEN_SENTINEL_plain_ascii_7341"
const diagAPIUnsafeReason = diagAPISentinel

// diagSeedLegacyRunReason writes historical data below the guarded Store API,
// because only old on-disk rows can contain unsafe diagnostic prose.
func diagSeedLegacyRunReason(t *testing.T, ctx context.Context, db *sql.DB, stateStore *state.Store, reason string) int64 {
	t.Helper()
	runID, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeManual,
		Status: state.RunStatusFailed,
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE run SET status=?, interrupt_reason=?, current_phase=?, completed_at=? WHERE id=?`,
		state.RunStatusFailed, reason, "VIDEO_SENTINEL_phase_secret", time.Now().UTC(), runID)
	require.NoError(t, err)
	return runID
}

// TestRunAPI_WithholdsUnsafeInterruptReason proves the public Run projection
// never serializes unsafe stored free text: the key stays null and an
// explicit unavailable flag replaces it, while safe reasons pass through.
func TestRunAPI_WithholdsUnsafeInterruptReason(t *testing.T) {
	ctx := context.Background()
	db, stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()

	unsafeRun := diagSeedLegacyRunReason(t, ctx, db, stateStore, diagAPIUnsafeReason)
	safeRun := diagSeedLegacyRunReason(t, ctx, db, stateStore, "youtube API returned 403: quotaExceeded")
	unknownRun, err := stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	require.NoError(t, stateStore.FailRun(ctx, unknownRun, "TOKEN_SENTINEL_plain_ascii_7341", 1, 1))
	plainRun, err := stateStore.CreateRun(ctx, &state.Run{
		StartedAt: time.Now().UTC(), RunType: state.RunTypeManual,
		Status: state.RunStatusCompleted,
	})
	require.NoError(t, err)

	srv := diagServer(stateStore, routerStore, versioner)

	for _, tc := range []struct {
		name      string
		runID     int64
		rawReason any // nil means JSON null; string means exposed reason
		unavail   bool
	}{
		{"unsafe legacy reason is unavailable, not raw", unsafeRun, nil, true},
		{"provider literal is normalized", safeRun, "diag:v1 phase=unknown operation=unknown code=provider_error status=403 reason=quotaExceeded", false},
		{"new generic canonical reason is retained but marked unavailable", unknownRun, "diag:v1 phase=unknown operation=unknown code=unknown_error", true},
		{"no recorded reason stays null without flag", plainRun, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := diagGetJSON(t, srv, "/api/runs/"+strconv.FormatInt(tc.runID, 10))
			require.Equal(t, http.StatusOK, code)

			reason, present := body["interrupt_reason"]
			require.True(t, present, "interrupt_reason key must stay present")
			if tc.rawReason == nil {
				assert.Nil(t, reason, "unsafe or absent reason must serialize as null")
			} else {
				assert.Equal(t, tc.rawReason, reason)
			}
			assert.Equal(t, tc.unavail, body["failure_reason_unavailable"] == true)
			assert.Nil(t, body["current_phase"], "unregistered stored phase must not be projected")

			raw, err := json.Marshal(body)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), diagAPISentinel)
		})
	}

	// The list endpoint carries the same gating.
	code, list := diagGetJSON(t, srv, "/api/runs")
	require.Equal(t, http.StatusOK, code)
	raw, err := json.Marshal(list)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), diagAPISentinel,
		"the run list must withhold unsafe legacy reasons too")
	runs := list["runs"].([]any)
	require.Len(t, runs, 4)
	for _, entry := range runs {
		run := entry.(map[string]any)
		if run["failure_reason_unavailable"] == true {
			reason := run["interrupt_reason"]
			assert.True(t, reason == nil || reason == "diag:v1 phase=unknown operation=unknown code=unknown_error",
				"unavailable detail may be withheld or represented by the safe generic summary")
		}
	}
}

// TestRunVideoAPI_WithholdsUnsafeErrorMessage proves legacy unsafe
// error_message text on videos and actions is withheld in list and detail
// responses with an explicit unavailable flag, while safe text passes.
func TestRunVideoAPI_WithholdsUnsafeErrorMessage(t *testing.T) {
	ctx := context.Background()
	db, stateStore, routerStore, versioner, cleanup := setupRunVideoTest(t)
	defer cleanup()

	route := createRouteForRunVideoTests(t, ctx, routerStore)

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	// Seed the run-scoped outcome row directly: the inspection projection
	// reads error_message from run_video_outcome for production runs.
	newVideo := func(externalID, message string) int64 {
		t.Helper()
		id, err := stateStore.CreateVideo(ctx, &state.Video{
			VideoID: externalID, ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
			DiscoveredInRunID: runID, ProcessingState: state.VideoStateFailed,
			FinalOutcome: state.FinalOutcomeFailed,
		})
		require.NoError(t, err)
		if message == "" {
			_, err = db.ExecContext(ctx,
				`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, updated_at) VALUES (?, ?, ?, ?)`,
				runID, id, state.FinalOutcomeNotYetEvaluated, time.Now().UTC())
		} else {
			_, err = db.ExecContext(ctx,
				`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, error_message, updated_at)
				 VALUES (?, ?, ?, ?, ?)`,
				runID, id, state.FinalOutcomeFailed, message, time.Now().UTC())
		}
		require.NoError(t, err)
		return id
	}
	unsafeVideo := newVideo("vu1", diagAPIUnsafeReason)
	safeVideo := newVideo("vs1", "youtube API returned 500: backendError")
	deferredVideo := newVideo("vd1", "")
	_, err = db.ExecContext(ctx, `UPDATE run_video_outcome SET final_outcome=?, deferred_reason=?, error_message=NULL
		WHERE run_id=? AND video_id=?`, state.FinalOutcomeDeferred, diagAPISentinel, runID, deferredVideo)
	require.NoError(t, err)

	// Action evidence with legacy unsafe text on the unsafe video's decision.
	unsafeDecision, err := stateStore.CreateRouteDecision(ctx, &state.RouteDecision{
		VideoID: unsafeVideo, RunID: runID, RouteID: route.ID,
		EvaluationOrder: 1, Decision: state.RouteDecisionMatched,
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome,error_message)
		VALUES(?,?,?,?,?,?,?)`, unsafeDecision, runID, unsafeVideo, "playlist_insert", "PL1", state.ActionOutcomeFailed, diagAPIUnsafeReason)
	require.NoError(t, err)
	safeDecision, err := stateStore.CreateRouteDecision(ctx, &state.RouteDecision{
		VideoID: safeVideo, RunID: runID, RouteID: route.ID,
		EvaluationOrder: 1, Decision: state.RouteDecisionMatched,
	})
	require.NoError(t, err)
	safeMsg := "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError"
	_, err = stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: safeDecision, RunID: runID, VideoID: safeVideo,
		ActionType: "playlist_insert", TargetPlaylistID: "PL1",
		Outcome: state.ActionOutcomeFailed, ErrorMessage: &safeMsg,
	})
	require.NoError(t, err)

	srv := diagServer(stateStore, routerStore, versioner)

	assertBodyExcludesSentinel := func(t *testing.T, code int, rec *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusOK, code)
		assert.NotContains(t, rec.Body.String(), diagAPISentinel)
	}

	// Run video list: unsafe message withheld with flag; safe message exposed.
	req := httptest.NewRequest(http.MethodGet,
		"/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assertBodyExcludesSentinel(t, rec.Code, rec)

	var list struct {
		Videos []struct {
			VideoID                   string  `json:"video_id"`
			FinalOutcome              *string `json:"final_outcome"`
			DeferredReason            *string `json:"deferred_reason"`
			DeferredReasonUnavailable bool    `json:"deferred_reason_unavailable"`
			ErrorMessage              *string `json:"error_message"`
			ErrorMessageUnavailable   bool    `json:"error_message_unavailable"`
		} `json:"videos"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Videos, 3)
	for _, v := range list.Videos {
		switch v.VideoID {
		case "vu1":
			assert.Nil(t, v.ErrorMessage)
			assert.True(t, v.ErrorMessageUnavailable,
				"withheld legacy text must be explicitly unavailable")
		case "vs1":
			require.NotNil(t, v.ErrorMessage)
			assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=provider_error status=500 reason=backendError", *v.ErrorMessage)
			assert.False(t, v.ErrorMessageUnavailable)
		case "vd1":
			require.NotNil(t, v.FinalOutcome)
			assert.Equal(t, string(state.FinalOutcomeDeferred), *v.FinalOutcome)
			assert.Nil(t, v.DeferredReason)
			assert.True(t, v.DeferredReasonUnavailable)
		}
	}

	// Decision detail: same gating on video and action messages.
	for _, videoID := range []string{"vu1", "vs1", "vd1"} {
		req := httptest.NewRequest(http.MethodGet,
			"/api/runs/"+strconv.FormatInt(runID, 10)+"/videos/"+videoID, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		assertBodyExcludesSentinel(t, rec.Code, rec)

		var detail videoDetailResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
		if videoID == "vu1" {
			assert.Nil(t, detail.Video.ErrorMessage)
			assert.True(t, detail.Video.ErrorMessageUnavailable)
			require.Len(t, detail.Video.RouteDecisions, 1)
			actions := detail.Video.RouteDecisions[0].ExecutedActions
			require.Len(t, actions, 1)
			assert.Nil(t, actions[0].ErrorMessage)
			assert.True(t, actions[0].ErrorMessageUnavailable,
				"legacy unsafe action text must be withheld")
		} else if videoID == "vs1" {
			require.NotNil(t, detail.Video.ErrorMessage)
			assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=provider_error status=500 reason=backendError", *detail.Video.ErrorMessage)
			assert.False(t, detail.Video.ErrorMessageUnavailable)
			require.Len(t, detail.Video.RouteDecisions, 1)
			actions := detail.Video.RouteDecisions[0].ExecutedActions
			require.Len(t, actions, 1)
			require.NotNil(t, actions[0].ErrorMessage)
			assert.Equal(t, "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError", *actions[0].ErrorMessage)
			assert.False(t, actions[0].ErrorMessageUnavailable)
		} else {
			require.NotNil(t, detail.Video.FinalOutcome)
			assert.Equal(t, string(state.FinalOutcomeDeferred), *detail.Video.FinalOutcome)
			assert.Nil(t, detail.Video.DeferredReason)
			assert.True(t, detail.Video.DeferredReasonUnavailable)
		}
	}
}

// TestQueueAPI_WithholdsUnsafeDetail proves the public error-queue projection
// never serializes stored raw Detail or unsafe Message text; safe entries are
// unaffected and the queue lifecycle fields all remain.
func TestQueueAPI_WithholdsUnsafeDetail(t *testing.T) {
	ctx := context.Background()
	db, stateStore, _, _, cleanup := setupRunVideoTest(t)
	defer cleanup()

	qs, err := queue.NewStore(db)
	require.NoError(t, err)

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	newVideo := func(externalID string) int64 {
		t.Helper()
		id, err := stateStore.CreateVideo(ctx, &state.Video{
			VideoID: externalID, ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
			DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted,
			FinalOutcome: state.FinalOutcomeAccepted,
		})
		require.NoError(t, err)
		return id
	}
	unsafeVideo := newVideo("qvu")
	safeVideo := newVideo("qvs")

	_, err = db.ExecContext(ctx, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,error_detail,status,created_at)
		VALUES(?,?,?,?,?,?,?)`, unsafeVideo, runID, queue.ErrorCategoryAPIError,
		"TOKEN_SENTINEL_plain_ascii_7341", diagAPIUnsafeReason, queue.ErrorStatusActive, time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)
	_, err = qs.CreateErrorEntry(ctx, &queue.ErrorEntry{
		VideoID: safeVideo, RunID: runID, Category: queue.ErrorCategoryAPIError,
		Message: "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError",
		Detail:  "youtube API returned 500: backendError",
		Status:  queue.ErrorStatusActive, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	svc := queue.NewService(qs, nil)
	api := httptest.NewServer(svc.ErrorHandler())
	t.Cleanup(api.Close)

	resp, err := http.Get(api.URL + "/")
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Errors []struct {
			ID                int64  `json:"id"`
			Message           string `json:"error_message"`
			Detail            string `json:"detail"`
			Status            string `json:"status"`
			ReasonUnavailable bool   `json:"error_message_unavailable"`
		} `json:"errors"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Errors, 2)

	// Lifecycle fields survive for both entries. Unsafe historical message and
	// detail values are withheld; new canonical/provider diagnostics stay useful.
	for _, e := range body.Errors {
		assert.Equal(t, "active", e.Status)
	}

	byID := map[int64]struct {
		Message           string
		Detail            string
		ReasonUnavailable bool
	}{}
	for _, e := range body.Errors {
		byID[e.ID] = struct {
			Message           string
			Detail            string
			ReasonUnavailable bool
		}{e.Message, e.Detail, e.ReasonUnavailable}
	}

	// Unsafe detail withheld with explicit flag; safe detail exposed.
	var unsafeEntryID, safeEntryID int64
	rows, err := qs.ListErrorEntries(ctx, queue.ErrorStatusActive)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, r := range rows {
		if r.VideoID == unsafeVideo {
			unsafeEntryID = r.ID
		} else {
			safeEntryID = r.ID
		}
	}
	assert.Empty(t, byID[unsafeEntryID].Detail, "raw legacy Detail must never serialize")
	assert.Empty(t, byID[unsafeEntryID].Message, "raw legacy Message must never serialize")
	assert.True(t, byID[unsafeEntryID].ReasonUnavailable)
	assert.Equal(t, "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError", byID[safeEntryID].Message)
	assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=provider_error status=500 reason=backendError", byID[safeEntryID].Detail)
	assert.False(t, byID[safeEntryID].ReasonUnavailable)
}
