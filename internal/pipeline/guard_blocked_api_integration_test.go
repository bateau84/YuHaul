//go:build integration

package pipeline

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
)

// A guard-blocked video in a normal run keeps the registered code in the
// headline and the detail of its retained error entry, as served by
// GET /api/errors.
func TestGuardBlockedErrorEntryServedWithRegisteredCode(t *testing.T) {
	e := newIsolationEnv(t)
	e.seedGuard(t, e.routed[0])
	e.execute()

	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	queue.NewService(qs, nil).ErrorHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?include=active", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Errors []struct {
			VideoID     int64  `json:"video_id"`
			Message     string `json:"error_message"`
			Detail      string `json:"detail"`
			Unavailable bool   `json:"error_message_unavailable"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.Len(t, body.Errors, 1, rec.Body.String())
	want := "diag:v1 phase=action_execution operation=execute_action code=mutation_application_uncertain"
	require.Equal(t, want, body.Errors[0].Message)
	require.Equal(t, want, body.Errors[0].Detail)
	require.False(t, body.Errors[0].Unavailable)
}
