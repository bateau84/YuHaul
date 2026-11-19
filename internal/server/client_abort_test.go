package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
)

func overviewWith(t *testing.T, err error) (*Server, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithServerLogger(logger),
		WithOverviewProvider(&fakeOverviewProvider{err: err}))
	return srv, &buf
}

// A client that goes away mid-request is not a server fault: DEBUG, no
// response body, no ERROR line.
func TestHandleOverviewClientAbortLogsDebugAndWritesNothing(t *testing.T) {
	srv, buf := overviewWith(t, context.Canceled)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Empty(t, rec.Body.String(), "nothing is written to a gone client")
	assert.NotContains(t, buf.String(), "level=ERROR")
	assert.Contains(t, buf.String(), "level=DEBUG")
	assert.Contains(t, buf.String(), "client aborted request")
}

// Canceled that did not come from this request's context is a real error.
func TestHandleOverviewCanceledWithLiveRequestStaysError(t *testing.T) {
	srv, buf := overviewWith(t, context.Canceled)

	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, buf.String(), "level=ERROR")
}

func TestHandleOverviewRealErrorStaysError(t *testing.T) {
	srv, buf := overviewWith(t, errors.New("disk on fire"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, buf.String(), "level=ERROR")
}
