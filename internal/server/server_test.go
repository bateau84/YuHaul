package server

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func setupServerTest(t *testing.T) (*sql.DB, *state.Store, *state.Machine, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	machine := state.NewMachine(stateStore)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, stateStore, machine, cleanup
}

type fakePipeline struct {
	started bool
	run     *state.Run
	execute int64
	done    chan struct{}
}

func newFakePipeline(run *state.Run) *fakePipeline {
	return &fakePipeline{run: run, done: make(chan struct{})}
}

func (f *fakePipeline) StartRun(ctx context.Context, runType state.RunType) (*state.Run, error) {
	f.started = true
	return f.run, nil
}

func (f *fakePipeline) Execute(ctx context.Context, runID int64) {
	f.execute = runID
	close(f.done)
}

func TestHandleStartRun(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	req := httptest.NewRequest(http.MethodPost, "/api/runs", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.True(t, fp.started)
	select {
	case <-fp.done:
		assert.Equal(t, runID, fp.execute)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pipeline Execute")
	}
	assert.Contains(t, rec.Body.String(), "\"run_id\":")
}

func TestHandleRunStream(t *testing.T) {
	_, _, _, cleanup := setupServerTest(t)
	defer cleanup()

	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, broadcast, WithServerLogger(logger))

	// Start a request that subscribes to run 42.
	req := httptest.NewRequest(http.MethodGet, "/api/runs/42/stream", nil)
	rec := httptest.NewRecorder()

	// Broadcast an event after giving the handler time to subscribe.
	go func() {
		time.Sleep(50 * time.Millisecond)
		broadcast.Broadcast(42, pipeline.Event{
			Type:      pipeline.EventRunStarted,
			RunID:     42,
			Timestamp: time.Now().UTC(),
			Payload:   []byte(`{}`),
		})
		broadcast.Broadcast(42, pipeline.Event{
			Type:      pipeline.EventRunCompleted,
			RunID:     42,
			Timestamp: time.Now().UTC(),
			Payload:   []byte(`{"run_id":42}`),
		})
	}()

	srv.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body, "event: run.started")
	assert.Contains(t, body, "event: run.completed")
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream"))
}

func TestHandleRunStreamInvalidID(t *testing.T) {
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, broadcast, WithServerLogger(logger))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/abc/stream", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
