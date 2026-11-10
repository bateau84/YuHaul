package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
)

// Every refusal of POST /api/runs maps to its contract status and body and
// balances the run WaitGroup (ARS-001 §3.1). The accepted 202 paths need the
// real pipeline and are covered in e2e (run_now_resume_test.go).
func TestRunNowRefusalResponses(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   map[string]any
	}{
		{
			name:       "startup recovery",
			err:        pipeline.ErrStartupRecoveryActive,
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   map[string]any{"error": pipeline.ErrStartupRecoveryActive.Error()},
		},
		{
			name:       "production active",
			err:        pipeline.ErrProductionRunActive,
			wantStatus: http.StatusConflict,
			wantBody:   map[string]any{"error": "a production run is already active", "code": "production_run_active"},
		},
		{
			name:       "wrapped production active",
			err:        errors.Join(errors.New("ctx"), pipeline.ErrProductionRunActive),
			wantStatus: http.StatusConflict,
			wantBody:   map[string]any{"error": "a production run is already active", "code": "production_run_active"},
		},
		{
			name:       "resume unavailable",
			err:        &pipeline.ResumeUnavailableError{RunID: 30, Reason: "snapshot_unavailable"},
			wantStatus: http.StatusConflict,
			wantBody: map[string]any{
				"error":  "run 30 stopped at its time limit but cannot be resumed safely; no new run was started",
				"code":   "resume_unavailable",
				"run_id": float64(30),
				"reason": "snapshot_unavailable",
			},
		},
		{
			name:       "other failure hides the error text",
			err:        errors.New("SECRET database path /var/lib/x"),
			wantStatus: http.StatusInternalServerError,
			wantBody:   map[string]any{"error": "failed to start run"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(&fakePipeline{admitErr: tc.err}, pipeline.NewBroadcaster())
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs", nil))

			require.Equal(t, tc.wantStatus, rec.Code)
			var got map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, tc.wantBody, got)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, srv.WaitRuns(ctx), "a refused press must release its run slot")
		})
	}
}

func TestRunNowRefusedWhileShuttingDown(t *testing.T) {
	appCtx, cancelApp := context.WithCancel(context.Background())
	srv := NewServer(&fakePipeline{admitErr: errors.New("must not be reached")}, pipeline.NewBroadcaster(), WithRunContext(appCtx))
	cancelApp()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"server is shutting down"}`, rec.Body.String())
}
