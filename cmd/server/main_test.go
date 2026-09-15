package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func TestServerRunVideoStoreIsWired(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	stateStore, err := state.NewStore(db)
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}

	routerStore := router.NewStore(db)
	pl := &fakePipeline{}
	srv := server.NewServer(
		pl,
		pipeline.NewBroadcaster(),
		server.WithServerLogger(testLogger(t)),
		server.WithRouteStore(routerStore),
		server.WithRunStore(stateStore),
		server.WithRunVideoStore(stateStore),
	)

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted}
	runID, err := stateStore.CreateRun(ctx, run)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	video := &state.Video{
		VideoID: "v1", ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
		DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted,
		FinalOutcome: state.FinalOutcomeAccepted,
	}
	if _, err := stateStore.CreateVideo(ctx, video); err != nil {
		t.Fatalf("create video: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/videos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list run videos status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"videos":`) {
		t.Fatalf("expected videos list in response, got %q", body)
	}
}

type fakePipeline struct{}

func (f *fakePipeline) StartRun(ctx context.Context, runType state.RunType) (*state.Run, error) {
	return nil, nil
}
func (f *fakePipeline) Execute(ctx context.Context, runID int64) {}
func (f *fakePipeline) DryRun(ctx context.Context, draft *config.Snapshot) (*state.Run, error) {
	return nil, nil
}
func (f *fakePipeline) ExecuteDryRun(ctx context.Context, runID int64) {}
func (f *fakePipeline) Replay(ctx context.Context, configVersionID *int64) (*state.Run, error) {
	return nil, nil
}
func (f *fakePipeline) ExecuteReplay(ctx context.Context, runID int64) {}
func (f *fakePipeline) Backtest(ctx context.Context, proposed config.Snapshot) (*state.Run, error) {
	return nil, nil
}
func (f *fakePipeline) ExecuteBacktest(ctx context.Context, runID int64) {}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSpaFallsBackToIndexForNavigation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}

	handler := spaHandler(dir)

	// Navigation route falls back to index.html.
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "<html></html>" {
		t.Fatalf("settings body = %q, want index.html", got)
	}

	// Missing asset returns 404, not HTML.
	req = httptest.NewRequest(http.MethodGet, "/assets/index-OLDHASH.js", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", rec.Code)
	}

	// Existing asset is served directly.
	req = httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("existing asset status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("existing asset content-type = %q, want text/javascript", got)
	}
}
