package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/corpusimport"
	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/store"
)

// TestNewCorpusImportWiring proves cmd/server's constructor produces a
// working manager + handler pair over the real destination database and the
// private staging directory, and that the configured upload cap is honored.
func TestNewCorpusImportWiring(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "yuhaul.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	t.Setenv(migration.MaxUploadBytesEnvVar, "4096")

	manager, handler, err := newCorpusImport(db, testLogger(t))
	if err != nil {
		t.Fatalf("newCorpusImport: %v", err)
	}
	if manager.MaxUploadBytes() != 4096 {
		t.Fatalf("max upload bytes = %d, want the configured 4096", manager.MaxUploadBytes())
	}
	wantStaging := filepath.Join(os.TempDir(), "yuhaul-corpus-import-staging")
	if manager.StagingDir() != wantStaging {
		t.Fatalf("staging dir = %q, want %q", manager.StagingDir(), wantStaging)
	}

	// The handler serves the contract endpoints under /api/corpus/*.
	mux := http.NewServeMux()
	mux.Handle("/api/corpus/", handler.Handler())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/corpus/import", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/corpus/import with no state: status = %d, want 404; body = %s",
			rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("no import state available")) {
		t.Fatalf("body = %q, want the no-state error", rec.Body.String())
	}
}

// TestNewCorpusImportRejectsInvalidUploadCap proves an invalid
// YUHAUL_CORPUS_UPLOAD_MAX_BYTES fails server configuration instead of
// silently weakening the cap (ARS-002).
func TestNewCorpusImportRejectsInvalidUploadCap(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "yuhaul.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, value := range []string{"0", "-1", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(migration.MaxUploadBytesEnvVar, value)
			if _, _, err := newCorpusImport(db, testLogger(t)); err == nil {
				t.Fatalf("newCorpusImport accepted invalid %s=%q; want an error",
					migration.MaxUploadBytesEnvVar, value)
			}
		})
	}
}

// TestCorpusImportThroughAssembledServer is the composed path: the real
// server.NewServer with the corpus import handler mounted, a real SQLite
// destination, and a real multipart upload — preview → import → status →
// recovery through the assembled API mux (ARS-002 verification item 8).
func TestCorpusImportThroughAssembledServer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "yuhaul.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Default cap; the manager's real staging dir is the process temp dir,
	// which is cleaned at construction and by the shutdown below.
	t.Setenv(migration.MaxUploadBytesEnvVar, "")
	manager, handler, err := newCorpusImport(db, testLogger(t))
	if err != nil {
		t.Fatalf("newCorpusImport: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdownCtx)
	})

	srv := server.NewServer(
		&fakePipeline{},
		pipeline.NewBroadcaster(),
		server.WithServerLogger(testLogger(t)),
		server.WithCorpusImportHandler(handler),
	)

	// Source with two rows.
	srcPath := filepath.Join(dir, "source.sqlite")
	src, err := sql.Open("sqlite", srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	if _, err := src.Exec(`CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		INSERT INTO videos VALUES ('v1', 'Assembled One', 'c1'), ('v2', 'Assembled Two', 'c2');`); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}
	content, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}

	// Preview through the assembled mux: no destination writes.
	rec, body := doMultipart(t, srv, http.MethodPost, "/api/corpus/import/preview", "source.sqlite", content)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200; body = %s", rec.Code, body)
	}
	var preview struct {
		SourceRowCount int64 `json:"source_row_count"`
	}
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.SourceRowCount != 2 {
		t.Fatalf("source_row_count = %d, want 2", preview.SourceRowCount)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM title_fingerprint;`).Scan(&n); err != nil {
		t.Fatalf("count fingerprints: %v", err)
	}
	if n != 0 {
		t.Fatalf("destination rows after preview = %d, want 0", n)
	}

	// Import through the assembled mux.
	rec, body = doMultipart(t, srv, http.MethodPost, "/api/corpus/import", "source.sqlite", content)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import status = %d, want 202; body = %s", rec.Code, body)
	}
	var accepted struct {
		ImportID string `json:"import_id"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decode accept: %v", err)
	}
	if accepted.ImportID == "" || accepted.Status != corpusimport.StatusQueued {
		t.Fatalf("accept body = %s, want an import_id with status queued", body)
	}

	// Status through the assembled mux until terminal.
	st := pollImportStatus(t, srv, accepted.ImportID)
	if st["status"] != corpusimport.StatusCompleted {
		t.Fatalf("status = %v, want completed", st["status"])
	}
	if st["inserted"] != float64(2) || st["processed"] != float64(2) || st["total"] != float64(2) {
		t.Fatalf("counts = %+v, want 2 total / 2 processed / 2 inserted", st)
	}
	corpusSize, ok := st["total_corpus_size"].(float64)
	if !ok || corpusSize != 2 {
		t.Fatalf("total_corpus_size = %v, want the actual destination count 2", st["total_corpus_size"])
	}

	// The real destination holds the fingerprints with the import columns.
	if err := db.QueryRow(`SELECT COUNT(*) FROM title_fingerprint WHERE source = 'user_import'
		AND is_duplicate_reference = 1 AND import_batch_id = ?;`, accepted.ImportID).Scan(&n); err != nil {
		t.Fatalf("count imported rows: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported rows = %d, want 2 marked user_import + eligible + batch id", n)
	}

	// Latest-state recovery through the assembled mux (browser navigation).
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/corpus/import", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("latest status = %d, want 200; body = %s", rec2.Code, rec2.Body.String())
	}
	if !bytes.Contains(rec2.Body.Bytes(), []byte(accepted.ImportID)) {
		t.Fatalf("latest body = %s, want import id %s", rec2.Body.String(), accepted.ImportID)
	}
}

// doMultipart issues a multipart upload through the assembled server handler.
func doMultipart(t *testing.T, srv *server.Server, method, path, filename string, content []byte) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	srv.Handler().ServeHTTP(rec, req)
	return rec, rec.Body.Bytes()
}

// pollImportStatus polls GET /api/corpus/import/{id} through the assembled
// server until the job reaches a terminal state.
func pollImportStatus(t *testing.T, srv *server.Server, importID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec,
			httptest.NewRequest(http.MethodGet, "/api/corpus/import/"+importID, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status endpoint = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var st map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("decode status: %v (body %s)", err, rec.Body.String())
		}
		switch st["status"] {
		case corpusimport.StatusCompleted, corpusimport.StatusCancelled, corpusimport.StatusFailed:
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("import %s never reached a terminal state", importID)
	return nil
}
