package server

import (
	"context"
	"database/sql"
	"fmt"
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
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/stats"
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
	started          bool
	run              *state.Run
	execute          int64
	dryRun           *state.Run
	draft            *config.Snapshot
	replay           *state.Run
	replayConfigID   *int64
	backtest         *state.Run
	backtestProposed config.Snapshot
	done             chan struct{}
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

func (f *fakePipeline) Replay(ctx context.Context, configVersionID *int64) (*state.Run, error) {
	f.replayConfigID = configVersionID
	if f.replay == nil {
		f.replay = f.run
	}
	return f.replay, nil
}

func (f *fakePipeline) ExecuteReplay(ctx context.Context, runID int64) {
	f.execute = runID
	close(f.done)
}

func (f *fakePipeline) Backtest(ctx context.Context, proposed config.Snapshot) (*state.Run, error) {
	f.backtestProposed = proposed
	if f.backtest == nil {
		f.backtest = f.run
	}
	return f.backtest, nil
}

func (f *fakePipeline) ExecuteBacktest(ctx context.Context, runID int64) {
	f.execute = runID
	close(f.done)
}

type fakeOverviewProvider struct {
	overview *Overview
	err      error
}

func (f *fakeOverviewProvider) Overview(context.Context) (*Overview, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.overview, nil
}

func TestHandleListRuns(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	_, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithServerLogger(logger), WithRunStore(stateStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"runs":`)
}

func TestHandleGetRun(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithServerLogger(logger), WithRunStore(stateStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"id":`)
}

func TestHandleGetRun_NotFound(t *testing.T) {
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithServerLogger(logger), WithRunStore(stateStore))

	req := httptest.NewRequest(http.MethodGet, "/api/runs/999", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleOverview(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	overview := &Overview{RunsToday: 3, ErrorCount: 1, DeferredCount: 2}
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithServerLogger(logger), WithOverviewProvider(&fakeOverviewProvider{overview: overview}))

	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"runs_today":3`)
	assert.Contains(t, body, `"error_count":1`)
	assert.Contains(t, body, `"deferred_count":2`)
}

func TestHandleQueueEndpointsMounted(t *testing.T) {
	ctx := context.Background()
	db, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	queueStore, err := queue.NewStore(db)
	require.NoError(t, err)
	queueSvc := queue.NewService(queueStore, nil)

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	video := &state.Video{VideoID: "v1", ChannelID: "c1", DiscoveredAt: time.Now().UTC(), DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted, FinalOutcome: state.FinalOutcomeAccepted}
	videoID, err := stateStore.CreateVideo(ctx, video)
	require.NoError(t, err)

	_, err = queueStore.CreateErrorEntry(ctx, &queue.ErrorEntry{
		VideoID:   videoID,
		RunID:     runID,
		Category:  queue.ErrorCategoryRuntime,
		Message:   "test error",
		Status:    queue.ErrorStatusActive,
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithServerLogger(logger), WithQueueHandler(queueSvc))

	req := httptest.NewRequest(http.MethodGet, "/api/errors/?include=active", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"errors":`)
	assert.Contains(t, rec.Body.String(), `"test error"`)
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

func TestHandleReplay(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeReplay, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	req := httptest.NewRequest(http.MethodPost, "/api/runs/replay", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Nil(t, fp.replayConfigID)
	select {
	case <-fp.done:
		assert.Equal(t, runID, fp.execute)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pipeline ExecuteReplay")
	}
	assert.Contains(t, rec.Body.String(), `"run_type":"replay"`)
}

func TestHandleReplay_WithConfigVersionID(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeReplay, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	body := `{"config_version_id":7}`
	req := httptest.NewRequest(http.MethodPost, "/api/runs/replay", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	require.NotNil(t, fp.replayConfigID)
	assert.Equal(t, int64(7), *fp.replayConfigID)
}

func TestHandleReplay_WithConfigVersionIDQueryParam(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeReplay, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	req := httptest.NewRequest(http.MethodPost, "/api/runs/replay?config_version_id=9", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	require.NotNil(t, fp.replayConfigID)
	assert.Equal(t, int64(9), *fp.replayConfigID)
}

func TestHandleReplay_InvalidConfigVersionIDQueryParam(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeReplay, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	req := httptest.NewRequest(http.MethodPost, "/api/runs/replay?config_version_id=abc", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleBacktest(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	body := `{"proposed_configuration":{"duplicate_threshold":0.8,"duplicate_behavior":"skip","routes":[]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/backtest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, 0.8, fp.backtestProposed.DuplicateThreshold)
	select {
	case <-fp.done:
		assert.Equal(t, runID, fp.execute)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pipeline ExecuteBacktest")
	}
	assert.Contains(t, rec.Body.String(), `"run_type":"backtest"`)
}

func TestHandleBacktest_InvalidThreshold(t *testing.T) {
	ctx := context.Background()
	_, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	run.ID = runID

	fp := newFakePipeline(run)
	broadcast := pipeline.NewBroadcaster()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(fp, broadcast, WithServerLogger(logger))

	body := `{"proposed_configuration":{"duplicate_threshold":1.5,"duplicate_behavior":"skip","routes":[]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/backtest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, 0.0, fp.backtestProposed.DuplicateThreshold)
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

func setupRouteHandlerTest(t *testing.T) (*sql.DB, *router.Store, *config.Service, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	versioner := config.NewService(routerStore, cfgStore)
	routerStore.WithOnChange(versioner.Snapshot)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, routerStore, versioner, cleanup
}

func TestHandleListRoutes(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "Music", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
		Exclusions:           []router.RouteExclusion{},
	})
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"Music"`)
}

func TestHandleGetRoute(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	id, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "Music", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
		Exclusions:           []router.RouteExclusion{},
	})
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodGet, "/api/routes/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"Music"`)
}

func TestHandleGetRoute_NotFound(t *testing.T) {
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodGet, "/api/routes/999", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleCreateRoute(t *testing.T) {
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	body := `{"name":"Music","evaluation_order":1,"is_enabled":true,"condition_composition":"AND","conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],"actions":[{"action_index":0,"action_type":"playlist_insert","target_playlist_id":"PL1"}],"exclusions":[]}`
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"Music"`)
}

func TestHandleCreateRoute_Validation(t *testing.T) {
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	body := `{"evaluation_order":1,"is_enabled":true,"condition_composition":"AND"}`
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUpdateRoute(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	id, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "Music", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:              []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
		Exclusions:           []router.RouteExclusion{},
	})
	require.NoError(t, err)

	body := `{"name":"Updated","evaluation_order":2,"is_enabled":false,"condition_composition":"OR","conditions":[],"actions":[],"exclusions":[]}`
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodPut, "/api/routes/"+strconv.FormatInt(id, 10), strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"Updated"`)
}

func TestHandleUpdateRoute_NotFound(t *testing.T) {
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	body := `{"name":"Updated","evaluation_order":2,"is_enabled":false,"condition_composition":"OR","conditions":[],"actions":[],"exclusions":[]}`
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodPut, "/api/routes/999", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleDeleteRoute(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	id, err := routerStore.CreateRoute(ctx, &router.Route{
		Name: "Music", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: "AND",
	})
	require.NoError(t, err)

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodDelete, "/api/routes/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestHandleDeleteRoute_NotFound(t *testing.T) {
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodDelete, "/api/routes/999", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleReorderRoutes(t *testing.T) {
	ctx := context.Background()
	_, routerStore, _, cleanup := setupRouteHandlerTest(t)
	defer cleanup()

	id1, err := routerStore.CreateRoute(ctx, &router.Route{Name: "A", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND"})
	require.NoError(t, err)
	id2, err := routerStore.CreateRoute(ctx, &router.Route{Name: "B", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND"})
	require.NoError(t, err)

	body := fmt.Sprintf(`{"route_ids":[%d,%d]}`, id2, id1)
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithRouteStore(routerStore))
	req := httptest.NewRequest(http.MethodPut, "/api/routes/reorder", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	routes, err := routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	assert.Equal(t, id2, routes[0].ID)
	assert.Equal(t, id1, routes[1].ID)
}

func TestAnalyticsRoutesReachable(t *testing.T) {
	ctx := context.Background()
	db, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()

	statsStore := stats.NewStore(db)
	cfgStore := config.NewStore(db)
	statsSvc := stats.NewService(statsStore, cfgStore, nil)
	analyticsHandler := stats.NewHandler(statsSvc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&fakePipeline{}, pipeline.NewBroadcaster(), WithServerLogger(logger), WithAnalyticsHandler(analyticsHandler))

	cases := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{"GET", "/api/settings/retention-playlists", "", http.StatusOK},
		{"POST", "/api/settings/retention-playlists", `{"playlist_id":"PLx","playlist_name":"x"}`, http.StatusCreated},
		{"GET", "/api/runs/" + strconv.FormatInt(runID, 10) + "/stats", "", http.StatusOK},
		{"GET", "/api/runs/" + strconv.FormatInt(runID, 10) + "/route-stats", "", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, tc.want, rec.Code, "body: %s", rec.Body.String())
		})
	}
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
