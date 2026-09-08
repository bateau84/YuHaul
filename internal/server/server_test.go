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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
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
	dryRun  *state.Run
	draft   *config.Snapshot
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

func (f *fakePipeline) DryRun(ctx context.Context, draft *config.Snapshot) (*state.Run, error) {
	f.draft = draft
	if f.dryRun == nil {
		f.dryRun = f.run
	}
	return f.dryRun, nil
}

func (f *fakePipeline) ExecuteDryRun(ctx context.Context, runID int64) {
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

func TestHandleDryRun(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeDryRun, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	req := httptest.NewRequest(http.MethodPost, "/api/runs/dry-run", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Nil(t, fp.draft)
	select {
	case <-fp.done:
		assert.Equal(t, runID, fp.execute)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pipeline ExecuteDryRun")
	}
	assert.Contains(t, rec.Body.String(), `"run_type":"dry_run"`)
}

func TestHandleDryRun_WithDraftConfig(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeDryRun, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	body := `{"draft_configuration":{"duplicate_threshold":0.75,"duplicate_behavior":"skip","routes":[]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/runs/dry-run", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	require.NotNil(t, fp.draft)
	assert.Equal(t, 0.75, fp.draft.DuplicateThreshold)
}

func setupConfigHandlerTest(t *testing.T) (*sql.DB, *config.Store, *config.Service, *config.Handler, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	_ = stateStore

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	versioner := config.NewService(routerStore, cfgStore)
	routerStore.WithOnChange(versioner.Snapshot)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := config.NewHandler(versioner, logger)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, cfgStore, versioner, handler, cleanup
}

func TestHandleListConfigVersions(t *testing.T) {
	ctx := context.Background()
	_, cfgStore, _, handler, cleanup := setupConfigHandlerTest(t)
	defer cleanup()

	_, err := cfgStore.Create(ctx, config.Snapshot{DuplicateThreshold: 0.5}, "first")
	require.NoError(t, err)
	_, err = cfgStore.Create(ctx, config.Snapshot{DuplicateThreshold: 0.5}, "second")
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithConfigHandler(handler))
	req := httptest.NewRequest(http.MethodGet, "/api/config/versions", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "first")
	assert.Contains(t, body, "second")
}

func TestHandleGetConfigVersion(t *testing.T) {
	ctx := context.Background()
	_, cfgStore, _, handler, cleanup := setupConfigHandlerTest(t)
	defer cleanup()

	id, err := cfgStore.Create(ctx, config.Snapshot{DuplicateThreshold: 0.5}, "detail")
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithConfigHandler(handler))
	req := httptest.NewRequest(http.MethodGet, "/api/config/versions/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "detail")
}

func TestHandleGetConfigVersion_NotFound(t *testing.T) {
	_, _, _, handler, cleanup := setupConfigHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithConfigHandler(handler))
	req := httptest.NewRequest(http.MethodGet, "/api/config/versions/999", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleDiffConfigVersion(t *testing.T) {
	ctx := context.Background()
	_, cfgStore, _, handler, cleanup := setupConfigHandlerTest(t)
	defer cleanup()

	_, err := cfgStore.Create(ctx, config.Snapshot{DuplicateThreshold: 0.5, DuplicateBehavior: "skip"}, "first")
	require.NoError(t, err)
	id2, err := cfgStore.Create(ctx, config.Snapshot{DuplicateThreshold: 0.9, DuplicateBehavior: "skip"}, "second")
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithConfigHandler(handler))
	req := httptest.NewRequest(http.MethodGet, "/api/config/versions/"+strconv.FormatInt(id2, 10)+"/diff", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "duplicate_threshold")
}

func TestHandleRestoreConfigVersion(t *testing.T) {
	ctx := context.Background()
	_, cfgStore, _, handler, cleanup := setupConfigHandlerTest(t)
	defer cleanup()

	id, err := cfgStore.Create(ctx, config.Snapshot{DuplicateThreshold: 0.5}, "original")
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithConfigHandler(handler))
	req := httptest.NewRequest(http.MethodPost, "/api/config/versions/"+strconv.FormatInt(id, 10)+"/restore", strings.NewReader(`{"description":"restored"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), "restored")
}

func TestHandleGetDuplicateThreshold(t *testing.T) {
	_, _, _, handler, cleanup := setupConfigHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithConfigHandler(handler))
	req := httptest.NewRequest(http.MethodGet, "/api/settings/duplicate-threshold", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "0.5")
}

func TestHandleSetDuplicateThreshold(t *testing.T) {
	ctx := context.Background()
	_, _, versioner, handler, cleanup := setupConfigHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithConfigHandler(handler))
	req := httptest.NewRequest(http.MethodPut, "/api/settings/duplicate-threshold", strings.NewReader(`{"threshold":0.75}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	v, err := versioner.DuplicateThreshold(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0.75, v)
}
