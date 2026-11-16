package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
)

// K2: a retry refused as already final (BR-039) answers 409 through the
// tracked coordinator, and the run drain is balanced afterwards.
func TestAlreadyFinalRetryAnswers409AndBalancesRunWG(t *testing.T) {
	db, _, _, cleanup := setupServerTest(t)
	defer cleanup()
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	s := NewServer(nil, nil, WithRunContext(context.Background()))
	svc := queue.NewService(qs, nil).WithRetryCoordinator(s.RetryCoordinator(
		retryFunc(func(context.Context, int64, bool) error {
			return &queue.AlreadyFinalError{VideoID: 1, EntryID: 1, Outcome: "accepted"}
		})))

	// The entry must exist for the 409 body's closed-entry view.
	_, err = db.Exec(`INSERT INTO run (id, started_at, run_type, status) VALUES (900, CURRENT_TIMESTAMP, 'manual', 'completed')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO video (id, video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (1, 'v', 'c', 900, 'completed', 'accepted')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO deferred_item (id, video_id, run_id, deferral_reason, status, closed_reason) VALUES (1, 1, 900, 'quota_exhausted', 'completed', 'final_outcome')`)
	require.NoError(t, err)

	for range 2 {
		rec := httptest.NewRecorder()
		svc.DeferredHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/1/retry", nil))
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), `"code":"video_already_final"`)
	}
	drain, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, s.WaitRuns(drain), "the refused retries must leave runWG balanced")
}
