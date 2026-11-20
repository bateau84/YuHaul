package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
)

type blockingRetry struct {
	started chan struct{}
	ctx     chan context.Context
}

func (b *blockingRetry) RetryQueueEntry(ctx context.Context, _ int64, _ bool) error {
	b.ctx <- ctx
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

// ADR-007 §3.4: a queue retry runs on the application context, is tracked by
// the run drain, and a client disconnect alone does not stop it.
func TestRetryCoordinatorJoinsShutdownLifecycle(t *testing.T) {
	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()
	s := NewServer(nil, nil, WithRunContext(appCtx))
	inner := &blockingRetry{started: make(chan struct{}), ctx: make(chan context.Context, 1)}
	coord := s.RetryCoordinator(inner)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- coord.RetryQueueEntry(reqCtx, 1, false) }()
	<-inner.started
	retryCtx := <-inner.ctx

	cancelReq() // client disconnect
	select {
	case <-retryCtx.Done():
		t.Fatal("a client disconnect must not cancel the retry")
	case <-time.After(100 * time.Millisecond):
	}

	drain, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDrain()
	require.ErrorIs(t, s.WaitRuns(drain), context.DeadlineExceeded, "the drain waits for the active retry")

	// During the drain a new retry is refused without reaching the coordinator.
	err := coord.RetryQueueEntry(context.Background(), 2, true)
	require.ErrorIs(t, err, queue.ErrShuttingDown)

	cancelApp() // SIGTERM
	require.ErrorIs(t, <-result, queue.ErrShuttingDown, "a retry cancelled by shutdown reports the shutdown refusal")
	require.NoError(t, s.WaitRuns(context.Background()))
}

func TestRetryCoordinatorRefusesOnceApplicationContextIsCancelled(t *testing.T) {
	appCtx, cancelApp := context.WithCancel(context.Background())
	s := NewServer(nil, nil, WithRunContext(appCtx))
	cancelApp()
	called := false
	coord := s.RetryCoordinator(retryFunc(func(context.Context, int64, bool) error { called = true; return nil }))
	err := coord.RetryQueueEntry(context.Background(), 1, true)
	require.True(t, errors.Is(err, queue.ErrShuttingDown))
	require.False(t, called)
}

type retryFunc func(context.Context, int64, bool) error

func (f retryFunc) RetryQueueEntry(ctx context.Context, id int64, e bool) error { return f(ctx, id, e) }

// A retry cancelled by shutdown, including one that never created its run,
// answers the shutdown 503 on both endpoints, not a 500. A failure while the
// server is running keeps its ordinary mapping.
func TestInterruptedRetryAnswers503OnBothEndpoints(t *testing.T) {
	db, _, _, cleanup := setupServerTest(t)
	defer cleanup()
	qs, err := queue.NewStore(db)
	require.NoError(t, err)

	newService := func(inner retryFunc) (*queue.Service, context.CancelFunc) {
		appCtx, cancelApp := context.WithCancel(context.Background())
		s := NewServer(nil, nil, WithRunContext(appCtx))
		return queue.NewService(qs, nil).WithRetryCoordinator(s.RetryCoordinator(inner)), cancelApp
	}
	post := func(h http.Handler) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/1/retry", nil))
		return rec
	}

	ordinary, cancel := newService(func(context.Context, int64, bool) error { return errors.New("ordinary failure") })
	defer cancel()
	require.Equal(t, http.StatusInternalServerError, post(ordinary.DeferredHandler()).Code, "a failure while running keeps its mapping")

	for _, endpoint := range []string{"error", "deferred"} {
		var cancelApp context.CancelFunc
		svc, cancelApp := newService(func(ctx context.Context, _ int64, _ bool) error {
			cancelApp() // SIGTERM arrives before the retry has created its run
			<-ctx.Done()
			return fmt.Errorf("prepare run: %w", ctx.Err())
		})
		h := svc.ErrorHandler()
		if endpoint == "deferred" {
			h = svc.DeferredHandler()
		}
		rec := post(h)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, endpoint)
		require.JSONEq(t, `{"error":"server is shutting down"}`, rec.Body.String(), endpoint)
	}
}
