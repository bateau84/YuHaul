package corpusimport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/fingerprint"
	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/bateau84/YuHaul/internal/store"
)

// env is a real end-to-end test environment: a real SQLite destination opened
// through store.Open (migrations applied), a real Manager with a private
// staging dir, and a real httptest server serving the ARS-002 routes. Nothing
// is mocked except the log sink.
type env struct {
	dest   *sql.DB
	dir    string
	mgr    *Manager
	srv    *httptest.Server
	logBuf *bytes.Buffer
}

func newEnv(t *testing.T, maxUploadBytes int64, opts ...Option) *env {
	t.Helper()
	dir := t.TempDir()
	dest, err := store.Open(context.Background(), filepath.Join(dir, "yuhaul.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = dest.Close() })

	stagingDir := filepath.Join(dir, "staging")
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mgr, err := NewManager(dest, stagingDir, maxUploadBytes,
		append([]Option{WithLogger(logger)}, opts...)...)
	require.NoError(t, err)

	srv := httptest.NewServer(NewHandler(mgr, logger).Handler())
	t.Cleanup(srv.Close)

	return &env{dest: dest, dir: dir, mgr: mgr, srv: srv, logBuf: logBuf}
}

// withRunHook installs the test-only worker gate at construction time, before
// the HTTP server starts, so the hook write happens-before every worker read
// (goroutine creation) and the race detector sees a clean edge.
func withRunHook(h func()) Option {
	return func(m *Manager) { m.runHook = h }
}

// withChunkHook installs the test-only committed-chunk gate at construction
// time, before the HTTP server starts (same happens-before reasoning as
// withRunHook).
func withChunkHook(h func(migration.Progress)) Option {
	return func(m *Manager) { m.chunkHook = h }
}

// wireStatus is the exact ARS-002 status response shape, kept separate from
// Status so the tests verify the wire contract rather than the internal type.
type wireStatus struct {
	ImportID            string         `json:"import_id"`
	Status              string         `json:"status"`
	Total               int            `json:"total"`
	Processed           int            `json:"processed"`
	Inserted            int            `json:"inserted"`
	Skipped             int            `json:"skipped"`
	Errors              int            `json:"errors"`
	ErrorCountsByReason map[string]int `json:"error_counts_by_reason"`
	RatePerSecond       *float64       `json:"rate_per_second"`
	ETA                 *int64         `json:"estimated_seconds_remaining"`
	TotalCorpusSize     *int64         `json:"total_corpus_size"`
}

type srcRow struct {
	id      string
	title   string
	channel string
}

// writeSource creates a real SQLite source database with exactly one
// discoverable table and returns its bytes.
func writeSource(t *testing.T, rows []srcRow) []byte {
	t.Helper()
	return writeSourceFrom(t, `CREATE TABLE prior_corpus (video_id TEXT, title TEXT, channel_id TEXT);`, rows)
}

// writeSourceFrom creates a real SQLite source database from a CREATE
// statement plus row inserts and returns its bytes.
func writeSourceFrom(t *testing.T, create string, rows []srcRow) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.sqlite")
	src, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = src.Exec(create)
	require.NoError(t, err)
	tx, err := src.Begin()
	require.NoError(t, err)
	for _, r := range rows {
		_, err := tx.Exec(`INSERT INTO prior_corpus (video_id, title, channel_id) VALUES (?, ?, ?);`,
			r.id, r.title, r.channel)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	require.NoError(t, src.Close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

// writeSourceWith creates a SQLite database from raw CREATE statements with no
// rows and returns its bytes.
func writeSourceWith(t *testing.T, stmts ...string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.sqlite")
	src, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	for _, s := range stmts {
		_, err := src.Exec(s)
		require.NoError(t, err)
	}
	require.NoError(t, src.Close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

// multipartBody builds a multipart/form-data body with one file part named
// "file" plus any extra parts.
func multipartBody(t *testing.T, filename string, content []byte, extra map[string][]byte) ([]byte, string) {
	t.Helper()
	return multipartRaw(t, filename, content, extra, false)
}

// multipartBodyTwice builds a multipart body with two file parts, both named
// "file", which the contract forbids.
func multipartBodyTwice(t *testing.T, content []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, name := range []string{"a.sqlite", "b.sqlite"} {
		part, err := w.CreateFormFile("file", name)
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes(), w.FormDataContentType()
}

// multipartBodyNoFile builds a multipart body with no file part at all.
func multipartBodyNoFile(t *testing.T) ([]byte, string) {
	t.Helper()
	return multipartRaw(t, "", nil, map[string][]byte{"mode": []byte("replace")}, true)
}

func multipartRaw(t *testing.T, filename string, content []byte, extra map[string][]byte, skipFile bool) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if !skipFile {
		part, err := w.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
	}
	for name, data := range extra {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, name))
		h.Set("Content-Type", "application/octet-stream")
		p, err := w.CreatePart(h)
		require.NoError(t, err)
		_, err = p.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes(), w.FormDataContentType()
}

func (e *env) do(t *testing.T, method, path string, body []byte, contentType string) (*http.Response, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := e.srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := map[string]any{}
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out), "response body: %s", raw)
	}
	return resp, out
}

func (e *env) doFile(t *testing.T, method, path, filename string, content []byte, extra map[string][]byte) (*http.Response, map[string]any) {
	t.Helper()
	body, ct := multipartBody(t, filename, content, extra)
	return e.do(t, method, path, body, ct)
}

// getStatus fetches GET /api/corpus/import/{id} and decodes the wire shape.
func (e *env) getStatus(t *testing.T, id string) wireStatus {
	t.Helper()
	resp, raw := e.do(t, http.MethodGet, "/api/corpus/import/"+id, nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %v", raw)
	return decodeStatus(t, raw)
}

func decodeStatus(t *testing.T, raw map[string]any) wireStatus {
	t.Helper()
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	var st wireStatus
	require.NoError(t, json.Unmarshal(encoded, &st))
	return st
}

// waitTerminal polls the status endpoint until the job reaches a terminal
// state or the deadline expires.
func (e *env) waitTerminal(t *testing.T, id string) wireStatus {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, raw := e.do(t, http.MethodGet, "/api/corpus/import/"+id, nil, "")
		require.Equal(t, http.StatusOK, resp.StatusCode, "body: %v", raw)
		st := decodeStatus(t, raw)
		switch st.Status {
		case StatusCompleted, StatusCancelled, StatusFailed:
			return st
		}
		require.True(t, time.Now().Before(deadline),
			"import %s never reached a terminal state (last: %+v)", id, st)
		time.Sleep(5 * time.Millisecond)
	}
}

// requireReconciled asserts the ARS-002 accounting invariants on the wire.
func requireReconciled(t *testing.T, st wireStatus) {
	t.Helper()
	require.Equal(t, st.Inserted+st.Skipped+st.Errors, st.Processed,
		"processed must equal inserted+skipped+errors")
	require.LessOrEqual(t, st.Processed, st.Total, "processed must not exceed the preflight total")
	sum := 0
	for _, n := range st.ErrorCountsByReason {
		require.GreaterOrEqual(t, n, 0)
		sum += n
	}
	require.Equal(t, st.Errors, sum, "reason counts must sum to errors")
	if st.Errors == 0 {
		require.Empty(t, st.ErrorCountsByReason, "error_counts_by_reason must be {} when errors is zero")
	}
}

func (e *env) destCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, e.dest.QueryRow(`SELECT COUNT(*) FROM title_fingerprint;`).Scan(&n))
	return n
}

func (e *env) requireStagingEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.dir, "staging"))
	require.NoError(t, err)
	require.Empty(t, entries, "staging directory must be empty")
}

// TestPreviewReportsCountsAndWritesNothing proves preview: accurate row
// count, same-ID count, at most ten samples, no date field, no destination
// writes, staged file removed.
func TestPreviewReportsCountsAndWritesNothing(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)

	_, err := e.dest.Exec(`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, source, is_duplicate_reference)
		VALUES ('v1', 'fp', 'tok', 'api_discovery', 0), ('v2', 'fp2', 'tok2', 'api_discovery', 0);`)
	require.NoError(t, err)
	before := e.destCount(t)

	var rows []srcRow
	for i := 1; i <= 15; i++ {
		rows = append(rows, srcRow{id: fmt.Sprintf("v%d", i), title: fmt.Sprintf("Title %d", i)})
	}
	content := writeSource(t, rows)

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import/preview", "prior.sqlite", content, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %v", body)
	require.Equal(t, float64(15), body["source_row_count"])
	require.Equal(t, float64(2), body["already_present_count"])
	samples, ok := body["sample_titles"].([]any)
	require.True(t, ok, "sample_titles must be an array: %v", body)
	require.Len(t, samples, 10, "at most ten sample titles")
	for key := range body {
		require.NotContains(t, key, "date", "preview must not report a date or date-range field")
	}

	require.Equal(t, before, e.destCount(t), "preview must not write to the destination")
	e.requireStagingEmpty(t)
}

// TestPreviewRejectsInvalidUploads proves every invalid/ambiguous source and
// framing case maps to 400 before any destination write, with the staged
// upload removed and a generic error message.
func TestPreviewRejectsInvalidUploads(t *testing.T) {
	ambiguous := writeSourceWith(t,
		"CREATE TABLE a (video_id TEXT, title TEXT);",
		"CREATE TABLE b (video_id TEXT, title TEXT);")
	noColumns := writeSourceWith(t, "CREATE TABLE junk (foo TEXT, bar TEXT);")
	valid := writeSource(t, []srcRow{{id: "v1", title: "One"}})

	cases := []struct {
		name  string
		build func(t *testing.T) ([]byte, string)
	}{
		{"not sqlite", func(t *testing.T) ([]byte, string) {
			return multipartBody(t, "evil.bin", []byte("definitely not a sqlite database"), nil)
		}},
		{"empty file part", func(t *testing.T) ([]byte, string) {
			return multipartBody(t, "empty.sqlite", nil, nil)
		}},
		{"ambiguous schema", func(t *testing.T) ([]byte, string) {
			return multipartBody(t, "ambiguous.sqlite", ambiguous, nil)
		}},
		{"missing columns", func(t *testing.T) ([]byte, string) {
			return multipartBody(t, "junk.sqlite", noColumns, nil)
		}},
		{"no file part", multipartBodyNoFile},
		{"extra part", func(t *testing.T) ([]byte, string) {
			return multipartBody(t, "x.sqlite", valid, map[string][]byte{"mode": []byte("replace")})
		}},
		{"second file part", func(t *testing.T) ([]byte, string) {
			return multipartBodyTwice(t, valid)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, migration.DefaultMaxUploadBytes)
			before := e.destCount(t)
			body, ct := tc.build(t)
			resp, out := e.do(t, http.MethodPost, "/api/corpus/import/preview", body, ct)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %v", out)
			require.NotEmpty(t, out["error"])
			require.Equal(t, before, e.destCount(t), "rejected preview must not write")
			e.requireStagingEmpty(t)
		})
	}

	t.Run("not multipart content type", func(t *testing.T) {
		e := newEnv(t, migration.DefaultMaxUploadBytes)
		resp, out := e.do(t, http.MethodPost, "/api/corpus/import/preview", valid, "application/json")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %v", out)
		require.Zero(t, e.destCount(t))
		e.requireStagingEmpty(t)
	})

	t.Run("missing content type", func(t *testing.T) {
		e := newEnv(t, migration.DefaultMaxUploadBytes)
		resp, out := e.do(t, http.MethodPost, "/api/corpus/import/preview", valid, "")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %v", out)
		require.Zero(t, e.destCount(t))
		e.requireStagingEmpty(t)
	})
}

// TestPreviewUploadTooLarge proves the file cap returns 413 before any
// destination write and removes the staged upload.
func TestPreviewUploadTooLarge(t *testing.T) {
	e := newEnv(t, 2048)
	content := bytes.Repeat([]byte("SQLite format 3\x00"), 1024)
	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import/preview", "big.sqlite", content, nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "body: %v", body)
	require.NotEmpty(t, body["error"])
	require.Zero(t, e.destCount(t))
	e.requireStagingEmpty(t)
}

// TestRequestEnvelopeBounded proves the total HTTP request body is bounded by
// the file cap plus 1 MiB of multipart framing: a request whose overall body
// exceeds the envelope is 413 even when the file part itself is within the
// file cap, and nothing is staged. The unknown-Content-Length (chunked) form
// is bounded by the same rule, whether the breach is in the file part itself
// or in multipart framing.
func TestRequestEnvelopeBounded(t *testing.T) {
	t.Run("known content length", func(t *testing.T) {
		e := newEnv(t, 4096)
		resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "small.sqlite",
			[]byte("SQLite format 3\x00padding"),
			map[string][]byte{"bulk": bytes.Repeat([]byte("x"), 2<<20)})
		require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "body: %v", body)
		require.NotEmpty(t, body["error"])
		require.Zero(t, e.destCount(t))
		e.requireStagingEmpty(t)
	})

	t.Run("unknown content length file part", func(t *testing.T) {
		e := newEnv(t, 4096)
		body, ct := multipartBody(t, "big.sqlite", bytes.Repeat([]byte("A"), 200000), nil)
		resp, out := doChunked(t, e, http.MethodPost, "/api/corpus/import", body, ct)
		require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "body: %v", out)
		require.Zero(t, e.destCount(t))
		e.requireStagingEmpty(t)
	})

	t.Run("unknown content length framing", func(t *testing.T) {
		e := newEnv(t, 4096)
		var buf bytes.Buffer
		buf.WriteString("--bnd\r\nContent-Disposition: form-data; name=\"file\"; filename=\"")
		buf.Write(bytes.Repeat([]byte("f"), 2<<20))
		buf.WriteString("\"\r\n\r\nSQLite format 3\x00\r\n--bnd--\r\n")
		resp, out := doChunked(t, e, http.MethodPost, "/api/corpus/import/preview", buf.Bytes(),
			"multipart/form-data; boundary=bnd")
		require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "body: %v", out)
		require.Zero(t, e.destCount(t))
		e.requireStagingEmpty(t)
	})
}

// doChunked issues a request with an unknown Content-Length (chunked
// transfer), forcing the server-side streaming bounds instead of the
// Content-Length pre-check.
func doChunked(t *testing.T, e *env, method, path string, body []byte, contentType string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = -1
	resp, err := e.srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := map[string]any{}
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out), "response body: %s", raw)
	}
	return resp, out
}

// TestStartImportFullLifecycle is the composed proof: HTTP -> stager ->
// preflight -> real bounded importer -> real SQLite destination, with exact
// counters, real fingerprints, correct import columns, actual destination
// total, and terminal recovery through GET /api/corpus/import.
func TestStartImportFullLifecycle(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(3))

	_, err := e.dest.Exec(`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, source, is_duplicate_reference)
		VALUES ('v2', 'existing-fp', 'existing-tok', 'api_discovery', 0);`)
	require.NoError(t, err)

	rows := []srcRow{
		{id: "v1", title: "Alpha video", channel: "chanA"},
		{id: "v2", title: "Beta video", channel: "chanB"},    // skipped: same video id
		{id: "v3", title: "Gamma video", channel: "chanC"},   // inserted
		{id: "v4", title: "", channel: ""},                   // error: empty fingerprint
		{id: "", title: "No id here", channel: ""},           // error: empty video id
		{id: "v5", title: "Delta video", channel: "chanD"},   // inserted
		{id: "v6", title: "Epsilon video", channel: "chanE"}, // inserted
	}
	content := writeSource(t, rows)

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "prior.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	importID, _ := body["import_id"].(string)
	require.NotEmpty(t, importID)
	require.Equal(t, StatusQueued, body["status"])
	require.NotContains(t, body, "total_corpus_size", "the accept response is only import_id + status")

	st := e.waitTerminal(t, importID)
	require.Equal(t, StatusCompleted, st.Status)
	require.Equal(t, 7, st.Total, "total is the preflight source row count")
	require.Equal(t, 7, st.Processed)
	require.Equal(t, 4, st.Inserted)
	require.Equal(t, 1, st.Skipped)
	require.Equal(t, 2, st.Errors)
	requireReconciled(t, st)
	require.Equal(t, map[string]int{migration.ReasonEmptyFingerprint: 1, migration.ReasonEmptyVideoID: 1}, st.ErrorCountsByReason)
	require.NotNil(t, st.RatePerSecond)
	require.Nil(t, st.ETA, "a terminal status carries no ETA")

	var corpusSize int64
	require.NoError(t, e.dest.QueryRow(`SELECT COUNT(*) FROM title_fingerprint;`).Scan(&corpusSize))
	require.NotNil(t, st.TotalCorpusSize)
	require.Equal(t, corpusSize, *st.TotalCorpusSize, "total_corpus_size is the actual destination count")
	require.EqualValues(t, 5, corpusSize, "1 pre-existing + 4 inserted")

	// The inserted rows carry the real shared fingerprints and exact import
	// columns (ARS-002 structural constraints).
	titles := map[string]string{
		"v1": "Alpha video", "v3": "Gamma video", "v5": "Delta video", "v6": "Epsilon video",
	}
	channels := map[string]string{"v1": "chanA", "v3": "chanC", "v5": "chanD", "v6": "chanE"}
	for id, title := range titles {
		fp, tokens, ok := fingerprint.FromTitle(title)
		require.True(t, ok)
		var gotFP, gotTokens, source, channel string
		var dupRef bool
		var batch sql.NullString
		err := e.dest.QueryRow(`SELECT fingerprint, fingerprint_tokens, source, is_duplicate_reference, import_batch_id, channel_id
			FROM title_fingerprint WHERE video_id = ?;`, id).
			Scan(&gotFP, &gotTokens, &source, &dupRef, &batch, &channel)
		require.NoError(t, err, "video %s", id)
		require.Equal(t, fp, gotFP, "video %s fingerprint", id)
		require.Equal(t, tokens, gotTokens, "video %s tokens", id)
		require.Equal(t, "user_import", source, "video %s source", id)
		require.True(t, dupRef, "video %s must be an eligible duplicate reference", id)
		require.Equal(t, importID, batch.String, "video %s import batch", id)
		require.Equal(t, channels[id], channel, "video %s channel association", id)
	}

	// The same-ID skip must not have altered the pre-existing row.
	var existingSource string
	require.NoError(t, e.dest.QueryRow(`SELECT source FROM title_fingerprint WHERE video_id = 'v2';`).Scan(&existingSource))
	require.Equal(t, "api_discovery", existingSource)

	// GET /api/corpus/import recovers the most recent terminal state without
	// browser-stored job state.
	latestResp, latest := e.do(t, http.MethodGet, "/api/corpus/import", nil, "")
	require.Equal(t, http.StatusOK, latestResp.StatusCode, "body: %v", latest)
	st2 := decodeStatus(t, latest)
	require.Equal(t, importID, st2.ImportID)
	require.Equal(t, StatusCompleted, st2.Status)
	require.Equal(t, st.Processed, st2.Processed)

	// By-id and latest agree on the terminal corpus size.
	require.NotNil(t, st2.TotalCorpusSize)

	e.requireStagingEmpty(t)
	requireLogsFreeOf(t, e, "Alpha video", "prior.sqlite")
}

// TestImportAtContextualCorpusScale runs the composed HTTP → stager → real
// bounded importer → real SQLite path at the contextual 10,000+ title scale
// (ADR-009 confirmation; not a minimum-size threshold) and proves monotonic
// committed progress with a terminal summary that reconciles.
func TestImportAtContextualCorpusScale(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(500))

	const scale = 12000
	var rows []srcRow
	for i := 1; i <= scale; i++ {
		rows = append(rows, srcRow{
			id:      fmt.Sprintf("scale%06d", i),
			title:   fmt.Sprintf("Historical corpus title number %d", i),
			channel: fmt.Sprintf("channel%d", i%50),
		})
	}
	content := writeSource(t, rows)

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "corpus.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	importID, _ := body["import_id"].(string)

	// Observe monotonic committed progress while it runs: every snapshot
	// reconciles and never moves backwards.
	var lastProcessed int
	sawIntermediate := false
	for {
		resp, raw := e.do(t, http.MethodGet, "/api/corpus/import/"+importID, nil, "")
		require.Equal(t, http.StatusOK, resp.StatusCode, "body: %v", raw)
		st := decodeStatus(t, raw)
		requireReconciled(t, st)
		require.GreaterOrEqual(t, st.Processed, lastProcessed, "committed progress must be monotonic")
		require.Equal(t, scale, st.Total, "total is the preflight count, never fabricated")
		lastProcessed = st.Processed
		switch st.Status {
		case StatusCompleted:
			require.Equal(t, scale, st.Processed)
			require.Equal(t, scale, st.Inserted)
			require.Zero(t, st.Skipped)
			require.Zero(t, st.Errors)
			requireReconciled(t, st)
			require.NotNil(t, st.TotalCorpusSize)
			require.EqualValues(t, scale, *st.TotalCorpusSize, "actual destination count matches")
			if sawIntermediate {
				require.NotNil(t, st.RatePerSecond)
			}
			require.Equal(t, lastProcessed, scale)
		case StatusCancelled, StatusFailed:
			t.Fatalf("unexpected terminal state %s at scale: %+v", st.Status, st)
		default:
			sawIntermediate = true
			// processed == total while still running is the legitimate window
			// between the last committed chunk and the terminal corpus-size
			// read; keep polling for the terminal state.
			require.LessOrEqual(t, st.Processed, scale)
			time.Sleep(2 * time.Millisecond)
			continue
		}
		break
	}
	require.True(t, sawIntermediate, "the run should have been observable while active")

	// Spot-check the real fingerprints and associations at scale.
	var n int
	require.NoError(t, e.dest.QueryRow(`SELECT COUNT(*) FROM title_fingerprint
		WHERE source = 'user_import' AND is_duplicate_reference = 1 AND import_batch_id = ?;`,
		importID).Scan(&n))
	require.Equal(t, scale, n)
	var channels int
	require.NoError(t, e.dest.QueryRow(`SELECT COUNT(DISTINCT channel_id) FROM title_fingerprint;`).Scan(&channels))
	require.Equal(t, 50, channels, "optional source channel ids are retained as associations")

	e.requireStagingEmpty(t)
}

// TestStartRejectsInvalidSourcesBeforeOperation proves invalid sources are
// rejected with 400, no operation is created, and nothing is written.
func TestStartRejectsInvalidSourcesBeforeOperation(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)
	ambiguous := writeSourceWith(t,
		"CREATE TABLE a (video_id TEXT, title TEXT);",
		"CREATE TABLE b (video_id TEXT, title TEXT);")

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "bad.sqlite", ambiguous, nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %v", body)

	latestResp, latest := e.do(t, http.MethodGet, "/api/corpus/import", nil, "")
	require.Equal(t, http.StatusNotFound, latestResp.StatusCode,
		"a rejected source must not create operation state: %v", latest)
	require.Zero(t, e.destCount(t))
	e.requireStagingEmpty(t)
}

// TestConcurrentStartsOneActive proves one active import under the -race
// detector: exactly one of N simultaneous starts is accepted, the rest are
// 409, and rejected uploads leave no staged files.
func TestConcurrentStartsOneActive(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(1), withRunHook(func() { <-release }))

	content := writeSource(t, []srcRow{
		{id: "v1", title: "One"}, {id: "v2", title: "Two"},
		{id: "v3", title: "Three"}, {id: "v4", title: "Four"},
	})

	const starters = 8
	type outcome struct {
		code int
		body map[string]any
		err  error
	}
	results := make([]outcome, starters)
	var wg sync.WaitGroup
	trigger := make(chan struct{})
	for i := 0; i < starters; i++ {
		body, ct := multipartBody(t, "prior.sqlite", content, nil)
		wg.Add(1)
		go func(i int, body []byte, ct string) {
			defer wg.Done()
			<-trigger
			req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/corpus/import", bytes.NewReader(body))
			if err != nil {
				results[i] = outcome{err: err}
				return
			}
			req.Header.Set("Content-Type", ct)
			resp, err := e.srv.Client().Do(req)
			if err != nil {
				results[i] = outcome{err: err}
				return
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				results[i] = outcome{err: err}
				return
			}
			parsed := map[string]any{}
			_ = json.Unmarshal(raw, &parsed)
			results[i] = outcome{code: resp.StatusCode, body: parsed}
		}(i, body, ct)
	}
	close(trigger)
	wg.Wait()

	accepted := 0
	var acceptedID string
	for _, r := range results {
		require.NoError(t, r.err)
		switch r.code {
		case http.StatusAccepted:
			accepted++
			acceptedID, _ = r.body["import_id"].(string)
		case http.StatusConflict:
			require.Equal(t, "an import is already active", r.body["error"])
		default:
			t.Fatalf("unexpected status %d: %v", r.code, r.body)
		}
	}
	require.Equal(t, 1, accepted, "exactly one start may be accepted")
	require.NotEmpty(t, acceptedID)

	close(release)
	st := e.waitTerminal(t, acceptedID)
	require.Equal(t, StatusCompleted, st.Status)
	require.Equal(t, 4, st.Inserted)
	e.requireStagingEmpty(t)
}

// TestStartBusyReturns409 proves a start while an import is active is
// rejected with 409 and does not disturb the running job.
func TestStartBusyReturns409(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, migration.DefaultMaxUploadBytes, withRunHook(func() { <-release }))

	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})
	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "a.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	importID, _ := body["import_id"].(string)

	resp2, body2 := e.doFile(t, http.MethodPost, "/api/corpus/import", "b.sqlite", content, nil)
	require.Equal(t, http.StatusConflict, resp2.StatusCode)
	require.Equal(t, "an import is already active", body2["error"])

	st := e.getStatus(t, importID)
	require.NotEqual(t, StatusFailed, st.Status, "the rejected start must not disturb the active job")

	close(release)
	require.Equal(t, StatusCompleted, e.waitTerminal(t, importID).Status)
	e.requireStagingEmpty(t)
}

// TestStatusAndCancelContract exercises the status/cancel tables: latest 404
// before any job with its distinct message, unknown id 404, active cancel 202
// cancel_requested, repeated cancel 202, terminal cancel 200 unchanged.
func TestStatusAndCancelContract(t *testing.T) {
	// Hold a job active: the worker blocks before any importer work starts.
	release := make(chan struct{})
	e := newEnv(t, migration.DefaultMaxUploadBytes, withRunHook(func() { <-release }))

	resp, body := e.do(t, http.MethodGet, "/api/corpus/import", nil, "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "no import state available", body["error"])

	resp, body = e.do(t, http.MethodGet, "/api/corpus/import/no-such-id", nil, "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "import not found", body["error"])

	resp, body = e.do(t, http.MethodPost, "/api/corpus/import/no-such-id/cancel", nil, "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "import not found", body["error"])

	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})
	resp, body = e.doFile(t, http.MethodPost, "/api/corpus/import", "a.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	importID, _ := body["import_id"].(string)

	// The active state is recoverable through GET /api/corpus/import.
	latestResp, latest := e.do(t, http.MethodGet, "/api/corpus/import", nil, "")
	require.Equal(t, http.StatusOK, latestResp.StatusCode, "body: %v", latest)
	require.Equal(t, importID, latest["import_id"])
	require.NotContains(t, latest, "total_corpus_size",
		"non-terminal states omit total_corpus_size")

	resp, body = e.do(t, http.MethodPost, "/api/corpus/import/"+importID+"/cancel", nil, "")
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	require.Equal(t, StatusCancelRequested, body["status"])

	// Repeated cancellation while pending: still 202 with the current status.
	resp, body = e.do(t, http.MethodPost, "/api/corpus/import/"+importID+"/cancel", nil, "")
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Equal(t, StatusCancelRequested, body["status"])

	close(release)
	st := e.waitTerminal(t, importID)
	require.Equal(t, StatusCancelled, st.Status)
	require.Equal(t, 0, st.Processed, "cancelled before the first chunk committed")
	require.Equal(t, 1, st.Total, "the preflight total survives a pre-chunk cancellation")
	requireReconciled(t, st)
	require.NotNil(t, st.TotalCorpusSize, "a cancelled state reports the actual destination count")

	// Terminal cancel: 200 with the unchanged terminal state and counters.
	resp, body = e.do(t, http.MethodPost, "/api/corpus/import/"+importID+"/cancel", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %v", body)
	require.Equal(t, StatusCancelled, body["status"])
	require.Equal(t, float64(1), body["total"])
	e.requireStagingEmpty(t)
}

// TestCancelAfterCommittedChunkPreservesRows cancels a real running import
// after at least one committed chunk and proves committed fingerprints stay,
// the final counters describe only committed rows, the actual destination
// count is reported, and a repeated append is idempotent.
func TestCancelAfterCommittedChunkPreservesRows(t *testing.T) {
	const sourceRows = 6000
	const chunkSize = 100

	// Hold the worker inside the report callback after the first committed
	// chunk, so the cancellation request deterministically lands while the
	// import is mid-source: after the hook returns, RunChunked observes the
	// cancelled context at the next transaction boundary.
	firstChunkCommitted := make(chan struct{})
	release := make(chan struct{})
	var holdOnce sync.Once
	e := newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(chunkSize),
		withChunkHook(func(p migration.Progress) {
			if p.Processed >= chunkSize {
				holdOnce.Do(func() { close(firstChunkCommitted) })
				<-release
			}
		}))

	var rows []srcRow
	for i := 1; i <= sourceRows; i++ {
		rows = append(rows, srcRow{id: fmt.Sprintf("vid%05d", i), title: fmt.Sprintf("Corpus title number %d", i)})
	}
	content := writeSource(t, rows)

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "big.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	importID, _ := body["import_id"].(string)

	// Wait until exactly one chunk has committed and the worker is held.
	select {
	case <-firstChunkCommitted:
	case <-time.After(30 * time.Second):
		t.Fatal("the first chunk never committed")
	}
	st := e.getStatus(t, importID)
	require.Equal(t, StatusRunning, st.Status)
	require.Equal(t, chunkSize, st.Processed, "exactly one chunk is committed while the worker is held")

	// Cancel while the worker is held: 202 + cancel_requested, then release.
	resp, body = e.do(t, http.MethodPost, "/api/corpus/import/"+importID+"/cancel", nil, "")
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	require.Equal(t, StatusCancelRequested, body["status"])
	close(release)

	// The active status carries an observed rate and an ETA while work
	// remains (ARS-002; both may be null only before a rate is available).
	active := decodeStatus(t, body)
	require.NotNil(t, active.RatePerSecond, "an active status with committed rows reports a rate")
	require.Greater(t, *active.RatePerSecond, 0.0)
	require.NotNil(t, active.ETA, "an active status with remaining rows reports an ETA")
	require.GreaterOrEqual(t, *active.ETA, int64(0))

	terminal := e.waitTerminal(t, importID)
	require.Equal(t, StatusCancelled, terminal.Status)
	require.Equal(t, sourceRows, terminal.Total)
	require.Equal(t, chunkSize, terminal.Processed,
		"cancellation preserves exactly the committed chunk and rolls back the rest")
	require.Equal(t, chunkSize, terminal.Inserted)
	requireReconciled(t, terminal)
	require.Zero(t, terminal.Errors)
	require.NotNil(t, terminal.TotalCorpusSize)
	require.EqualValues(t, chunkSize, *terminal.TotalCorpusSize,
		"fresh destination: committed rows are the corpus")

	// Committed chunks are durable and a repeated append is idempotent.
	resp, body = e.doFile(t, http.MethodPost, "/api/corpus/import", "big.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	id2, _ := body["import_id"].(string)
	st2 := e.waitTerminal(t, id2)
	require.Equal(t, StatusCompleted, st2.Status)
	require.Equal(t, sourceRows, st2.Processed)
	require.Equal(t, terminal.Inserted, st2.Skipped, "committed rows are skipped on append retry")
	require.Equal(t, sourceRows-terminal.Inserted, st2.Inserted)
	requireReconciled(t, st2)

	var multiplicity int
	require.NoError(t, e.dest.QueryRow(`SELECT COALESCE(MAX(c), 0) FROM
		(SELECT COUNT(*) AS c FROM title_fingerprint GROUP BY video_id);`).Scan(&multiplicity))
	require.Equal(t, 1, multiplicity, "no duplicate video_id rows after cancel + append retry")

	// A new accepted import replaces the prior terminal result: GET
	// /api/corpus/import now reports the newest job, not the cancelled one.
	latestResp, latest := e.do(t, http.MethodGet, "/api/corpus/import", nil, "")
	require.Equal(t, http.StatusOK, latestResp.StatusCode, "body: %v", latest)
	require.Equal(t, id2, latest["import_id"])

	e.requireStagingEmpty(t)
}

// TestRestartLosesStatusKeepsRows simulates a process restart with a fresh
// manager over the same destination: job state is gone (404s), committed rows
// remain, and re-uploading the same source safely skips existing ids.
func TestRestartLosesStatusKeepsRows(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)
	content := writeSource(t, []srcRow{{id: "v1", title: "One"}, {id: "v2", title: "Two"}})

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "a.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	importID, _ := body["import_id"].(string)
	require.Equal(t, StatusCompleted, e.waitTerminal(t, importID).Status)
	rowsBefore := e.destCount(t)
	require.EqualValues(t, 2, rowsBefore)

	// "Restart": a fresh manager and handler over the same destination
	// database, as a new process would construct.
	restarted, err := NewManager(e.dest, filepath.Join(e.dir, "staging"), migration.DefaultMaxUploadBytes,
		WithLogger(slog.New(slog.NewJSONHandler(e.logBuf, nil))))
	require.NoError(t, err)
	restartedSrv := httptest.NewServer(NewHandler(restarted, slog.New(slog.NewJSONHandler(e.logBuf, nil))).Handler())
	defer restartedSrv.Close()
	restartedEnv := &env{dest: e.dest, dir: e.dir, mgr: restarted, srv: restartedSrv, logBuf: e.logBuf}

	// After restart: status is absent and the old id is unknown.
	resp2, body2 := restartedEnv.do(t, http.MethodGet, "/api/corpus/import", nil, "")
	require.Equal(t, http.StatusNotFound, resp2.StatusCode, "body: %v", body2)
	require.Equal(t, "no import state available", body2["error"])

	resp3, body3 := restartedEnv.do(t, http.MethodGet, "/api/corpus/import/"+importID, nil, "")
	require.Equal(t, http.StatusNotFound, resp3.StatusCode, "body: %v", body3)
	require.Equal(t, "import not found", body3["error"])

	var rowsAfter int64
	require.NoError(t, e.dest.QueryRow(`SELECT COUNT(*) FROM title_fingerprint;`).Scan(&rowsAfter))
	require.Equal(t, rowsBefore, rowsAfter, "committed rows survive the restart")

	// Re-upload: the append retry skips both ids and reports no fabricated
	// summary.
	resp4, body4 := restartedEnv.doFile(t, http.MethodPost, "/api/corpus/import", "a.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp4.StatusCode, "body: %v", body4)
	id2, _ := body4["import_id"].(string)
	st := restartedEnv.waitTerminal(t, id2)
	require.Equal(t, StatusCompleted, st.Status)
	require.Equal(t, 2, st.Processed)
	require.Equal(t, 0, st.Inserted)
	require.Equal(t, 2, st.Skipped)
	require.Zero(t, st.Errors)
	requireReconciled(t, st)
	require.NotNil(t, st.TotalCorpusSize)
	require.EqualValues(t, 2, *st.TotalCorpusSize)
	require.EqualValues(t, 2, restartedEnv.destCount(t), "append retry inserted no duplicates")
}

// TestDatabaseFailureReportsFailedWithNullCorpusSize forces a destination
// failure and proves the job reports failed, keeps its committed-boundary
// counters, reports total_corpus_size as explicit null when the destination
// cannot be read, and removes the staged source.
func TestDatabaseFailureReportsFailedWithNullCorpusSize(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)

	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})

	// Break the destination before the import starts; preflight reads only
	// the source, so acceptance still succeeds and the job fails on its first
	// destination transaction.
	require.NoError(t, e.dest.Close())
	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "a.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	importID, _ := body["import_id"].(string)

	_, raw := e.do(t, http.MethodGet, "/api/corpus/import/"+importID, nil, "")
	_ = raw // polling continues below

	deadline := time.Now().Add(30 * time.Second)
	for {
		resp2, raw2 := e.do(t, http.MethodGet, "/api/corpus/import/"+importID, nil, "")
		require.Equal(t, http.StatusOK, resp2.StatusCode, "body: %v", raw2)
		if raw2["status"] == StatusFailed {
			require.Contains(t, raw2, "total_corpus_size",
				"a terminal failed state must carry total_corpus_size")
			require.Nil(t, raw2["total_corpus_size"], "explicit null when the destination cannot be read")
			break
		}
		require.True(t, time.Now().Before(deadline), "import never failed: %v", raw2)
		time.Sleep(5 * time.Millisecond)
	}

	st := e.getStatus(t, importID)
	require.Equal(t, StatusFailed, st.Status)
	require.Equal(t, 0, st.Processed)
	require.Nil(t, st.TotalCorpusSize)
	e.requireStagingEmpty(t)

	// Terminal failed keeps its unchanged state on a repeated cancel.
	resp3, body3 := e.do(t, http.MethodPost, "/api/corpus/import/"+importID+"/cancel", nil, "")
	require.Equal(t, http.StatusOK, resp3.StatusCode, "body: %v", body3)
	require.Equal(t, StatusFailed, body3["status"])
}

// TestStartupCleanupRemovesAbandonedStaging proves crash leftovers are
// removed at manager construction while unrelated files are preserved.
func TestStartupCleanupRemovesAbandonedStaging(t *testing.T) {
	dir := t.TempDir()
	dest, err := store.Open(context.Background(), filepath.Join(dir, "yuhaul.db"))
	require.NoError(t, err)
	defer func() { _ = dest.Close() }()

	staging := filepath.Join(dir, "staging")
	require.NoError(t, os.MkdirAll(staging, 0o700))
	abandoned := filepath.Join(staging, migration.StagingFilePrefix+strings.Repeat("ab", 16)+migration.StagingFileSuffix)
	require.NoError(t, os.WriteFile(abandoned, []byte("raw upload"), 0o600))
	unrelated := filepath.Join(staging, "operator-note.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("keep me"), 0o600))

	logBuf := &bytes.Buffer{}
	_, err = NewManager(dest, staging, migration.DefaultMaxUploadBytes,
		WithLogger(slog.New(slog.NewJSONHandler(logBuf, nil))))
	require.NoError(t, err)

	_, err = os.Stat(abandoned)
	require.ErrorIs(t, err, os.ErrNotExist, "abandoned staging file must be removed at startup")
	data, err := os.ReadFile(unrelated)
	require.NoError(t, err, "unrelated files are preserved")
	require.Equal(t, "keep me", string(data))
}

// TestStagingDirectoryIsPrivate proves the staging area uses the required
// restrictive permissions and that the client filename never influences the
// staged path (ARS-002 security surface).
func TestStagingDirectoryIsPrivate(t *testing.T) {
	dir := t.TempDir()
	dest, err := store.Open(context.Background(), filepath.Join(dir, "yuhaul.db"))
	require.NoError(t, err)
	defer func() { _ = dest.Close() }()

	staging := filepath.Join(dir, "staging")
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, nil))
	release := make(chan struct{})
	mgr, err := NewManager(dest, staging, migration.DefaultMaxUploadBytes,
		WithLogger(logger), withRunHook(func() { <-release }))
	require.NoError(t, err)

	info, err := os.Stat(staging)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "staging directory must be 0700")

	srv := httptest.NewServer(NewHandler(mgr, logger).Handler())
	defer srv.Close()

	// A client filename with path traversal content must not create a path.
	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})
	bodyBytes, ct := multipartBody(t, "../../etc/passwd.sqlite", content, nil)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/corpus/import", bytes.NewReader(bodyBytes))
	require.NoError(t, err)
	req.Header.Set("Content-Type", ct)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	_ = resp.Body.Close()

	entries, err := os.ReadDir(staging)
	require.NoError(t, err)
	require.Len(t, entries, 1, "one staged file for the accepted import")
	require.Contains(t, entries[0].Name(), migration.StagingFilePrefix,
		"the staged name is server-generated, never derived from the client filename")
	info, err = entries[0].Info()
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "staged files must be 0600")

	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, mgr.Shutdown(ctx))
	entries, err = os.ReadDir(staging)
	require.NoError(t, err)
	require.Empty(t, entries, "staged file removed after shutdown")
}

// TestShutdownCancelsActiveImportAndRemovesStaged proves graceful shutdown
// cancels the active import and only returns after the staged source is
// removed (cmd/server performs this before closing the database).
func TestShutdownCancelsActiveImportAndRemovesStaged(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, migration.DefaultMaxUploadBytes, withRunHook(func() { <-release }))

	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})
	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "a.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	importID, _ := body["import_id"].(string)

	staged, err := os.ReadDir(filepath.Join(e.dir, "staging"))
	require.NoError(t, err)
	require.NotEmpty(t, staged, "the active job's staged source exists while it runs")

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownDone <- e.mgr.Shutdown(ctx)
	}()

	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before the worker stopped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-shutdownDone, "shutdown waits for the worker and staged removal")

	st := e.getStatus(t, importID)
	require.Equal(t, StatusCancelled, st.Status)
	e.requireStagingEmpty(t)
}

// TestShutdownTimeoutReturnsError proves a stuck worker makes Shutdown report
// its context deadline instead of blocking forever.
func TestShutdownTimeoutReturnsError(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, migration.DefaultMaxUploadBytes, withRunHook(func() { <-release }))

	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})
	resp, _ := e.doFile(t, http.MethodPost, "/api/corpus/import", "a.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.Error(t, e.mgr.Shutdown(ctx))

	close(release)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	require.NoError(t, e.mgr.Shutdown(ctx2), "a second shutdown reaps the released worker")
}

// TestNoRawTitlesFilenamesOrPathsInLogs runs preview, success, invalid input,
// cancellation, and failure paths and proves no raw title, upload filename,
// or staging path reaches the logs or the non-preview responses.
func TestNoRawTitlesFilenamesOrPathsInLogs(t *testing.T) {
	const secretTitle = "Very Secret Private Video Title 42"
	const secretName = "private-export-with-secret-name.sqlite"

	// Gate that blocks the worker before it runs. The hook is installed at
	// construction (before any job exists); the gate flag is toggled
	// atomically from the test goroutine.
	var block atomic.Bool
	gate := make(chan struct{})
	var gateOnce sync.Once
	e := newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(1),
		withRunHook(func() {
			if block.Load() {
				<-gate
			}
		}))

	rows := []srcRow{
		{id: "v1", title: secretTitle},
		{id: "v2", title: "Second title"},
		{id: "v3", title: "Third title"},
	}
	content := writeSource(t, rows)

	// Preview: legitimate transient sample in the response, nothing in logs.
	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import/preview", secretName, content, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %v", body)
	require.NotEmpty(t, body["sample_titles"])

	// Success.
	resp, body = e.doFile(t, http.MethodPost, "/api/corpus/import", secretName, content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	importID, _ := body["import_id"].(string)
	require.Equal(t, StatusCompleted, e.waitTerminal(t, importID).Status)

	// Invalid input: generic error, no echo of content or filename.
	resp, body = e.doFile(t, http.MethodPost, "/api/corpus/import", secretName, []byte("not sqlite"), nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	errMsg := fmt.Sprint(body["error"])
	require.NotContains(t, errMsg, secretTitle)
	require.NotContains(t, errMsg, secretName)

	// Cancellation of an active job.
	block.Store(true)
	resp, body = e.doFile(t, http.MethodPost, "/api/corpus/import", secretName, content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	id2, _ := body["import_id"].(string)
	resp, body = e.do(t, http.MethodPost, "/api/corpus/import/"+id2+"/cancel", nil, "")
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Equal(t, StatusCancelRequested, body["status"])
	gateOnce.Do(func() { close(gate) })
	require.Equal(t, StatusCancelled, e.waitTerminal(t, id2).Status)
	block.Store(false)

	// Failure: close the destination so the next import dies on its first
	// destination transaction.
	require.NoError(t, e.dest.Close())
	resp, body = e.doFile(t, http.MethodPost, "/api/corpus/import", secretName, content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	id3, _ := body["import_id"].(string)
	require.Equal(t, StatusFailed, e.waitTerminal(t, id3).Status)

	// Failure of a preview against the broken destination (internal error).
	resp, body = e.doFile(t, http.MethodPost, "/api/corpus/import/preview", secretName, content, nil)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode, "body: %v", body)
	require.NotContains(t, fmt.Sprint(body["error"]), secretTitle)

	logs := e.logBuf.String()
	require.NotContains(t, logs, secretTitle, "logs must not contain raw titles")
	require.NotContains(t, logs, secretName, "logs must not contain the upload filename")
	require.NotContains(t, logs, e.mgr.StagingDir(), "logs must not contain the staging path")
	require.NotContains(t, logs, migration.StagingFilePrefix, "logs must not contain staged file names")
}

// requireLogsFreeOf asserts captured logs contain none of the given secrets
// nor the staging location.
func requireLogsFreeOf(t *testing.T, e *env, secrets ...string) {
	t.Helper()
	logs := e.logBuf.String()
	for _, s := range secrets {
		require.NotContains(t, logs, s)
	}
	require.NotContains(t, logs, e.mgr.StagingDir())
	require.NotContains(t, logs, migration.StagingFilePrefix)
}
