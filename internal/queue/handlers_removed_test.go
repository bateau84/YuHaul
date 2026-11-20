package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// Manual retry when the person's own removal won (authorization ARS-001; admin
// UI design 3a): 409 retry_work_removed with the designed copy and the current
// entry, on both retry endpoints. Other faults keep their responses.

type removedCoordinator struct {
	err     error
	removed func() // runs "during" the retry, as the person's Dismiss would
}

func (c removedCoordinator) RetryQueueEntry(context.Context, int64, bool) error {
	if c.removed != nil {
		c.removed()
	}
	return c.err
}

func TestRetryRoutesAnswerRetryWorkRemoved409(t *testing.T) {
	s := newTestStore(t)
	origin := retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "http-removed")
	errID := exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','retrying')`, video, origin)
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	exec(t, s.db, `UPDATE deferred_item SET status='retrying' WHERE id=?`, item)

	wrapped := fmt.Errorf("process retry video %d: %w", video, &removedWrap{ErrWorkRemoved})
	svc := NewService(s, nil).WithRetryCoordinator(removedCoordinator{err: wrapped, removed: func() {
		exec(t, s.db, `UPDATE error_entry SET status='dismissed' WHERE id=?`, errID)
		exec(t, s.db, `UPDATE deferred_item SET status='discarded' WHERE id=?`, item)
	}})
	for _, tc := range []struct {
		name    string
		handler http.Handler
		id      int64
		status  string
	}{{"error", svc.ErrorHandler(), errID, "dismissed"}, {"deferred", svc.DeferredHandler(), item, "discarded"}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+itoa(tc.id)+"/retry", nil))
			require.Equal(t, http.StatusConflict, rec.Code)
			var body struct {
				Code  string         `json:"code"`
				Error string         `json:"error"`
				Entry map[string]any `json:"entry"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, "retry_work_removed", body.Code)
			require.Equal(t, "This item was removed while its retry was running. The retry may already have sent playlist additions to YouTube. It will not be retried again.", body.Error)
			require.NotContains(t, body.Error, "nothing")
			require.Equal(t, float64(tc.id), body.Entry["id"])
			require.Equal(t, tc.status, body.Entry["status"], "the current entry, not the one the retry started with")
			_, has := body.Entry["final_outcome"]
			require.False(t, has, "not the video_already_final body")
		})
	}
}

type removedWrap struct{ cause error }

func (e *removedWrap) Error() string { return "retry refused: " + e.cause.Error() }
func (e *removedWrap) Unwrap() error { return e.cause }

func TestRetryRoutesKeepOtherOutcomes(t *testing.T) {
	s := newTestStore(t)
	origin := retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "http-others")
	errID := exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, video, origin)
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"claim lost or Stop", nil, http.StatusOK},
		{"not owned", ErrWorkNotOwned, http.StatusInternalServerError},
		{"ownership conflict", ErrRetryOwnership, http.StatusInternalServerError},
		{"unusable claim identity", ErrRetryProvenance, http.StatusInternalServerError},
		{"storage failure", errors.New("disk I/O"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(s, nil).WithRetryCoordinator(removedCoordinator{err: tc.err})
			for _, h := range []struct {
				handler http.Handler
				id      int64
			}{{svc.ErrorHandler(), errID}, {svc.DeferredHandler(), item}} {
				rec := httptest.NewRecorder()
				h.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+itoa(h.id)+"/retry", nil))
				require.Equal(t, tc.want, rec.Code, rec.Body.String())
				require.NotContains(t, rec.Body.String(), "retry_work_removed")
			}
		})
	}
}

// The entry cannot be loaded: the handler reports a 500, not a bogus 409.
func TestRetryWorkRemovedWithMissingEntryIs500(t *testing.T) {
	s := newTestStore(t)
	svc := NewService(s, nil).WithRetryCoordinator(removedCoordinator{err: ErrWorkRemoved})
	rec := httptest.NewRecorder()
	svc.ErrorHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/9999/retry", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
