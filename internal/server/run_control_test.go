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

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/state"
)

// Stop and Discard HTTP contracts (run-initiation ARS-003 §4.1, ARS-002 §3) and
// the run-read projection (ARS-003 §4.2).

func controlServer(t *testing.T, f *fakePipeline, opts ...Option) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(f, pipeline.NewBroadcaster(), append([]Option{WithServerLogger(logger)}, opts...)...)
}

func postRun(t *testing.T, srv *Server, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return rec.Code, body
}

// X1 for Stop: every row of the §4.1 table, with the shutdown row driven by
// WaitRuns, and runWG balanced.
func TestStopRunHTTPContract(t *testing.T) {
	f := &fakePipeline{}
	srv := controlServer(t, f)

	f.stopRes = &pipeline.StopResult{RunID: 41, Outcome: pipeline.StopAccepted}
	code, body := postRun(t, srv, "/api/runs/41/stop")
	require.Equal(t, http.StatusAccepted, code)
	require.Equal(t, map[string]any{"run_id": float64(41), "status": "running", "stop": "accepted"}, body)

	f.stopRes = &pipeline.StopResult{RunID: 41, Outcome: pipeline.StopAlreadyStopping}
	code, body = postRun(t, srv, "/api/runs/41/stop")
	require.Equal(t, http.StatusAccepted, code)
	require.Equal(t, "already_stopping", body["stop"])

	for _, bad := range []string{"abc", "0", "-3"} {
		code, body = postRun(t, srv, "/api/runs/"+bad+"/stop")
		require.Equal(t, http.StatusBadRequest, code, bad)
		require.Equal(t, "invalid_run_id", body["code"])
	}

	cases := []struct {
		err    error
		status int
		code   string
		extra  map[string]any
	}{
		{&pipeline.StopRefusedError{RunID: 41, Code: pipeline.StopRunNotFound}, 404, "run_not_found", nil},
		{&pipeline.StopRefusedError{RunID: 41, Code: pipeline.StopRunEnded, Status: "stopped"}, 409, "run_ended", map[string]any{"status": "stopped"}},
		{&pipeline.StopRefusedError{RunID: 41, Code: pipeline.StopRunEnded, Status: "completed"}, 409, "run_ended", map[string]any{"status": "completed"}},
		{&pipeline.StopRefusedError{RunID: 41, Code: pipeline.StopRunEnded, Status: "failed"}, 409, "run_ended", map[string]any{"status": "failed"}},
		{&pipeline.StopRefusedError{RunID: 41, Code: pipeline.StopRunNotActive, Reason: "not_executing"}, 409, "run_not_active", map[string]any{"reason": "not_executing"}},
		{&pipeline.StopRefusedError{RunID: 41, Code: pipeline.StopRunNotActive, Reason: "ending"}, 409, "run_not_active", map[string]any{"reason": "ending"}},
		{pipeline.ErrStopNotRecorded, 500, "stop_failed", nil},
		{errors.New("raw database detail"), 500, "stop_failed", nil},
	}
	for _, tc := range cases {
		f.stopRes, f.stopErr = nil, tc.err
		code, body = postRun(t, srv, "/api/runs/41/stop")
		require.Equal(t, tc.status, code, tc.code)
		require.Equal(t, tc.code, body["code"])
		require.EqualValues(t, 41, body["run_id"])
		for k, v := range tc.extra {
			require.Equal(t, v, body[k])
		}
		require.NotContains(t, body["error"], "raw database detail")
	}

	// runWG is balanced: shutdown completes at once, then new requests are refused.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, srv.WaitRuns(ctx))
	code, body = postRun(t, srv, "/api/runs/41/stop")
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, "shutting_down", body["code"])
	require.EqualValues(t, 41, body["run_id"])
}

// X1 for Discard: every row of the ARS-002 §3 table.
func TestDiscardRunHTTPContract(t *testing.T) {
	f := &fakePipeline{}
	srv := controlServer(t, f)

	f.discardRes = &pipeline.DiscardResult{RunID: 30, Cause: "snapshot_unavailable"}
	code, body := postRun(t, srv, "/api/runs/30/discard")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, map[string]any{"run_id": float64(30), "status": "failed", "outcome": "discarded", "cause": "snapshot_unavailable"}, body)

	code, body = postRun(t, srv, "/api/runs/zero/discard")
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "invalid_run_id", body["code"])

	cases := []struct {
		err    error
		status int
		code   string
		extra  map[string]any
	}{
		{pipeline.ErrStartupRecoveryActive, 503, "startup_recovery_active", nil},
		{pipeline.ErrProductionRunActive, 409, "production_run_active", nil},
		{&pipeline.DiscardRefusedError{RunID: 30, Code: pipeline.DiscardRunNotFound}, 404, "run_not_found", nil},
		{&pipeline.DiscardRefusedError{RunID: 30, Code: pipeline.DiscardRunEnded, Status: "failed"}, 409, "run_ended", map[string]any{"status": "failed"}},
		{&pipeline.DiscardRefusedError{RunID: 30, Code: pipeline.DiscardRunEnded, Status: "completed"}, 409, "run_ended", map[string]any{"status": "completed"}},
		{&pipeline.DiscardRefusedError{RunID: 30, Code: pipeline.DiscardRunNotDiscardable, Reason: "resumable"}, 409, "run_not_discardable", map[string]any{"reason": "resumable"}},
		{&pipeline.DiscardRefusedError{RunID: 30, Code: pipeline.DiscardRunNotDiscardable, Reason: "not_oldest"}, 409, "run_not_discardable", map[string]any{"reason": "not_oldest"}},
		{&pipeline.DiscardRefusedError{RunID: 30, Code: pipeline.DiscardRunNotDiscardable, Reason: "not_eligible"}, 409, "run_not_discardable", map[string]any{"reason": "not_eligible"}},
		{pipeline.ErrDiscardNotApplied, 500, "discard_failed", nil},
		{pipeline.ErrDiscardOutcomeUnknown, 500, "discard_outcome_unknown", nil},
	}
	for _, tc := range cases {
		f.discardRes, f.discardErr = nil, tc.err
		code, body = postRun(t, srv, "/api/runs/30/discard")
		require.Equal(t, tc.status, code, tc.code)
		require.Equal(t, tc.code, body["code"])
		require.EqualValues(t, 30, body["run_id"])
		for k, v := range tc.extra {
			require.Equal(t, v, body[k])
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, srv.WaitRuns(ctx), "runWG is balanced in every branch")
	code, body = postRun(t, srv, "/api/runs/30/discard")
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, "shutting_down", body["code"])
}

// X2: stopped, stop_requested_at, discardable and discard_cause on every run
// read (list, single, overview last_run).
func TestRunReadProjection(t *testing.T) {
	ctx := context.Background()
	db, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()
	stoppedID, err := stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC().Add(-time.Hour), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	_, err = stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)

	f := &fakePipeline{discardableID: stoppedID, discardCause: "snapshot_unavailable", discardableOK: true}
	last := &state.Run{ID: stoppedID, Status: state.RunStatusStopped}
	srv := controlServer(t, f, WithRunStore(stateStore), WithOverviewProvider(&fakeOverviewProvider{overview: &Overview{LastRun: last}}))

	get := func(path string) map[string]any {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body
	}

	single := get("/api/runs/" + strconv.FormatInt(stoppedID, 10))
	require.Equal(t, true, single["discardable"])
	require.Equal(t, "snapshot_unavailable", single["discard_cause"])
	require.Contains(t, single, "stop_requested_at")
	require.Nil(t, single["stop_requested_at"])

	list := get("/api/runs")["runs"].([]any)
	require.Len(t, list, 2)
	var discardable, plain int
	for _, item := range list {
		run := item.(map[string]any)
		require.Contains(t, run, "stop_requested_at")
		if run["discardable"] == true {
			discardable++
			require.EqualValues(t, stoppedID, run["id"])
			require.Equal(t, "snapshot_unavailable", run["discard_cause"])
		} else {
			plain++
			require.Equal(t, false, run["discardable"])
			require.NotContains(t, run, "discard_cause")
		}
	}
	require.Equal(t, 1, discardable, "at most one run reads discardable")
	require.Equal(t, 1, plain)

	lr := get("/api/overview")["last_run"].(map[string]any)
	require.Equal(t, "stopped", lr["status"])
	require.Equal(t, true, lr["discardable"])
	require.Contains(t, lr, "stop_requested_at")

	// A stop marker round-trips.
	_, err = db.Exec(`UPDATE run SET stop_requested_at='2026-11-10T12:00:03Z' WHERE id=?`, stoppedID)
	require.NoError(t, err)
	require.Equal(t, "2026-11-10T12:00:03Z", get("/api/runs/" + strconv.FormatInt(stoppedID, 10))["stop_requested_at"])

	// A read failure yields null on every item.
	f.discardableEr = errors.New("read failed")
	require.Nil(t, get("/api/runs/" + strconv.FormatInt(stoppedID, 10))["discardable"])
	for _, item := range get("/api/runs")["runs"].([]any) {
		run := item.(map[string]any)
		require.Contains(t, run, "discardable")
		require.Nil(t, run["discardable"])
		require.NotContains(t, run, "discard_cause")
	}
}
