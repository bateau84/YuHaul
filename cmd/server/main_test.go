package main

import (
	"context"
	"database/sql"
	"encoding/json"
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

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/quota"
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
func (f *fakePipeline) DryRun(ctx context.Context, draft *config.ProposalSnapshot) (*state.Run, error) {
	return nil, nil
}
func (f *fakePipeline) ExecuteDryRun(ctx context.Context, runID int64) {}
func (f *fakePipeline) Replay(ctx context.Context, configVersionID *int64) (*state.Run, error) {
	return nil, nil
}
func (f *fakePipeline) ExecuteReplay(ctx context.Context, runID int64) {}
func (f *fakePipeline) Backtest(ctx context.Context, proposed *config.ProposalSnapshot) (*state.Run, error) {
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

// newTestPlaylistProvider builds the production playlistProvider against a
// real SQLite database (store migrations create auth and quota tables), a real
// auth.Service holding one valid token, and a real quota ledger, so pagination
// and ledger behavior are exercised end to end through the fake YouTube seam.
func newTestPlaylistProvider(t *testing.T, baseURL string) (*playlistProvider, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	encKey := strings.Repeat("k", 32)
	tokenStore, err := auth.NewSQLiteTokenStore(db, []byte(encKey))
	if err != nil {
		t.Fatalf("new token store: %v", err)
	}
	authSvc, err := auth.NewService(&auth.ServiceConfig{
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost/callback",
		EncryptionKey: encKey,
	}, tokenStore, auth.NewInMemoryStateStore())
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	now := time.Now().UTC()
	if err := tokenStore.Save(ctx, &auth.TokenRecord{
		Provider:    "youtube",
		AccessToken: "test-access-token",
		TokenType:   "Bearer",
		Expiry:      now.Add(time.Hour),
		ConnectedAt: now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("save token: %v", err)
	}

	return &playlistProvider{auth: authSvc, baseURL: baseURL, ledger: quota.New(db)}, db
}

// newFakeYouTubePlaylists serves playlists.list pages keyed by pageToken (the
// empty string is the first request). nextTokens maps a page token to the
// nextPageToken returned after that page. An unknown page token yields a 4xx,
// like the real API.
func newFakeYouTubePlaylists(pages map[string][]string, nextTokens map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/playlists" {
			http.NotFound(w, r)
			return
		}
		token := r.URL.Query().Get("pageToken")
		ids, ok := pages[token]
		if !ok {
			http.Error(w, `{"error":"invalid page token"}`, http.StatusBadRequest)
			return
		}
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]any{
				"id":      id,
				"snippet": map[string]any{"title": "Title " + id},
			})
		}
		resp := map[string]any{"items": items}
		if tok, ok := nextTokens[token]; ok {
			resp["nextPageToken"] = tok
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// playlistsListQuota sums the playlists_list ledger rows: total units, total
// call count, and number of rows.
func playlistsListQuota(t *testing.T, db *sql.DB) (units, calls, rows int) {
	t.Helper()
	err := db.QueryRowContext(context.Background(),
		`SELECT COALESCE(SUM(units_consumed),0), COALESCE(SUM(call_count),0), COUNT(*)
		 FROM quota_ledger WHERE operation_type = 'playlists_list'`,
	).Scan(&units, &calls, &rows)
	if err != nil {
		t.Fatalf("query quota ledger: %v", err)
	}
	return units, calls, rows
}

func TestHandleListPlaylists_AllPagesInOrderAndQuotaPerPage(t *testing.T) {
	var page1, page2 []string
	for i := 0; i < 50; i++ {
		page1 = append(page1, fmt.Sprintf("PL%03d", i))
	}
	for i := 50; i < 75; i++ {
		page2 = append(page2, fmt.Sprintf("PL%03d", i))
	}

	fake := newFakeYouTubePlaylists(
		map[string][]string{"": page1, "page-2": page2},
		map[string]string{"": "page-2"},
	)
	defer fake.Close()

	provider, db := newTestPlaylistProvider(t, fake.URL)
	srv := server.NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		server.WithServerLogger(testLogger(t)),
		server.WithPlaylistProvider(provider))

	req := httptest.NewRequest(http.MethodGet, "/api/playlists", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Playlists []struct {
			PlaylistID   string `json:"playlist_id"`
			PlaylistName string `json:"playlist_name"`
		} `json:"playlists"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Playlists) != 75 {
		t.Fatalf("got %d playlists, want all 75 across both pages", len(body.Playlists))
	}
	for i, pl := range body.Playlists {
		want := fmt.Sprintf("PL%03d", i)
		if pl.PlaylistID != want {
			t.Fatalf("playlist[%d].id = %q, want %q (upstream page/item order)", i, pl.PlaylistID, want)
		}
		if wantName := "Title " + want; pl.PlaylistName != wantName {
			t.Fatalf("playlist[%d].name = %q, want %q", i, pl.PlaylistName, wantName)
		}
	}

	if units, calls, rows := playlistsListQuota(t, db); units != 2 || calls != 2 || rows != 2 {
		t.Fatalf("quota ledger = %d units, %d calls, %d rows; want 2/2/2 (one unit per successful page)", units, calls, rows)
	}
}

func TestHandleListPlaylists_LaterPageFailureNoPartialList(t *testing.T) {
	var page1 []string
	for i := 0; i < 50; i++ {
		page1 = append(page1, fmt.Sprintf("PL%03d", i))
	}

	// Page 2 is requested (nextPageToken "page-2") but not served, so the fake
	// returns 400 for the second page — a later-page failure.
	fake := newFakeYouTubePlaylists(
		map[string][]string{"": page1},
		map[string]string{"": "page-2"},
	)
	defer fake.Close()

	provider, db := newTestPlaylistProvider(t, fake.URL)
	srv := server.NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		server.WithServerLogger(testLogger(t)),
		server.WithPlaylistProvider(provider))

	req := httptest.NewRequest(http.MethodGet, "/api/playlists", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"error":"failed to list playlists"`) {
		t.Fatalf("body = %q, want failed-to-list error object", body)
	}
	if strings.Contains(body, `"playlists"`) {
		t.Fatalf("partial playlist list leaked on later-page failure: %s", body)
	}

	// The successful first page's quota entry is retained.
	if units, calls, rows := playlistsListQuota(t, db); units != 1 || calls != 1 || rows != 1 {
		t.Fatalf("quota ledger = %d units, %d calls, %d rows; want 1/1/1 retained from the successful first page", units, calls, rows)
	}
}

func TestHandleListPlaylists_EmptyCompleteList(t *testing.T) {
	fake := newFakeYouTubePlaylists(map[string][]string{"": {}}, nil)
	defer fake.Close()

	provider, db := newTestPlaylistProvider(t, fake.URL)
	srv := server.NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		server.WithServerLogger(testLogger(t)),
		server.WithPlaylistProvider(provider))

	req := httptest.NewRequest(http.MethodGet, "/api/playlists", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"playlists":[]`) {
		t.Fatalf("body = %q, want explicit empty playlists array", rec.Body.String())
	}

	// A successful empty page still costs one unit.
	if units, calls, rows := playlistsListQuota(t, db); units != 1 || calls != 1 || rows != 1 {
		t.Fatalf("quota ledger = %d units, %d calls, %d rows; want 1/1/1 for the successful empty page", units, calls, rows)
	}
}
