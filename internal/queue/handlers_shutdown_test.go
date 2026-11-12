package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type refusingRetry struct{}

func (refusingRetry) RetryQueueEntry(context.Context, int64, bool) error { return ErrShuttingDown }

func TestRetryEndpointsAnswer503WhileShuttingDown(t *testing.T) {
	store := newTestStore(t)
	h := NewService(store, nil).WithRetryCoordinator(refusingRetry{})
	for name, tc := range map[string]struct {
		handler http.Handler
		path    string
	}{"error": {h.ErrorHandler(), "/1/retry"}, "deferred": {h.DeferredHandler(), "/1/retry"}} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, nil))
			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.Equal(t, `{"error":"server is shutting down"}`, strings.TrimSpace(rec.Body.String()))
		})
	}
}
