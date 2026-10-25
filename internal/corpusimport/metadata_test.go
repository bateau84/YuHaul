package corpusimport

// HTTP-level tests for GET /api/corpus/import/metadata (ADR-015 / ARS-002):
// the durable last-successful-import completion timestamp, its null state,
// restart independence from process-local job status, exact 500 error
// contract, and the terminal-outcome rule that decides whether it advances.

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/migration"
)

// wireMetadata is the exact metadata response shape, kept separate from
// ImportMetadata so the tests verify the wire contract.
type wireMetadata struct {
	LastSuccessfulCorpusImportAt *string `json:"last_successful_corpus_import_at"`
}

// getMetadata fetches GET /api/corpus/import/metadata and returns the status
// plus the decoded body.
func (e *env) getMetadata(t *testing.T) (int, map[string]any) {
	t.Helper()
	resp, body := e.do(t, http.MethodGet, "/api/corpus/import/metadata", nil, "")
	return resp.StatusCode, body
}

// requireMetadataNull asserts a 200 response whose only field is an explicit
// JSON null (key present, value nil).
func (e *env) requireMetadataNull(t *testing.T) {
	t.Helper()
	status, body := e.getMetadata(t)
	require.Equal(t, http.StatusOK, status, "body: %v", body)
	require.Equal(t, []string{"last_successful_corpus_import_at"}, sortedKeys(body),
		"the metadata response carries exactly the one contract field")
	value, present := body["last_successful_corpus_import_at"]
	require.True(t, present, "the field must be present, not omitted: %v", body)
	require.Nil(t, value, "no recorded completion time must be explicit null: %v", body)
}

// requireMetadataTimestamp fetches the metadata and returns the parsed
// canonical RFC 3339 Nano UTC timestamp.
func (e *env) requireMetadataTimestamp(t *testing.T) time.Time {
	t.Helper()
	status, body := e.getMetadata(t)
	require.Equal(t, http.StatusOK, status, "body: %v", body)
	raw, ok := body["last_successful_corpus_import_at"].(string)
	require.True(t, ok, "expected a timestamp string: %v", body)
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	require.NoError(t, err, "stored marker must be RFC 3339 Nano: %q", raw)
	require.Equal(t, parsed.UTC().Format(time.RFC3339Nano), raw,
		"stored marker must be the canonical UTC representation")
	return parsed
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// importAndWait accepts an upload and waits for a terminal state.
func (e *env) importAndWait(t *testing.T, filename string, content []byte) wireStatus {
	t.Helper()
	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", filename, content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	id, _ := body["import_id"].(string)
	require.NotEmpty(t, id)
	return e.waitTerminal(t, id)
}

func TestMetadataIsNullBeforeAnyImport(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)
	e.requireMetadataNull(t)
}

func TestMetadataAfterCompletedImportAndRestart(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)
	content := writeSource(t, []srcRow{{id: "v1", title: "One"}, {id: "v2", title: "Two"}})

	st := e.importAndWait(t, "prior.sqlite", content)
	require.Equal(t, StatusCompleted, st.Status)

	before := time.Now().UTC().Add(-time.Minute)
	got := e.requireMetadataTimestamp(t)
	require.False(t, got.Before(before), "the completion timestamp predates the import")
	require.False(t, got.After(time.Now().UTC().Add(time.Minute)))

	// "Restart": a fresh manager and handler over the same destination
	// database, as a new process would construct.
	restarted, err := NewManager(e.dest, filepath.Join(e.dir, "staging"), migration.DefaultMaxUploadBytes)
	require.NoError(t, err)
	logger := slog.New(slog.NewJSONHandler(e.logBuf, nil))
	restartedSrv := httptest.NewServer(NewHandler(restarted, logger).Handler())
	t.Cleanup(restartedSrv.Close)
	restartedEnv := &env{dest: e.dest, dir: e.dir, mgr: restarted, srv: restartedSrv, logBuf: e.logBuf}

	statusResp, statusBody := restartedEnv.do(t, http.MethodGet, "/api/corpus/import", nil, "")
	require.Equal(t, http.StatusNotFound, statusResp.StatusCode,
		"job status stays process-local: %v", statusBody)
	require.Equal(t, got, restartedEnv.requireMetadataTimestamp(t),
		"the metadata must survive restart unchanged")

	// A re-import whose rows are all skipped is a normal completion and
	// advances the marker (BR-009 AC9).
	after := time.Now().UTC()
	retry := restartedEnv.importAndWait(t, "prior.sqlite", content)
	require.Equal(t, StatusCompleted, retry.Status)
	require.Zero(t, retry.Inserted)
	require.Equal(t, 2, retry.Skipped)
	require.False(t, restartedEnv.requireMetadataTimestamp(t).Before(after),
		"an all-skipped successful import must advance the timestamp")
}

func TestMetadataNotAdvancedByPreview(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)
	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import/preview", "prior.sqlite", content, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %v", body)
	require.Equal(t, float64(1), body["source_row_count"])

	e.requireMetadataNull(t)
}

func TestMetadataNotAdvancedByCancelledImport(t *testing.T) {
	const chunkSize = 10
	const sourceRows = 25

	// Hold the worker after the first committed chunk so the cancel request
	// deterministically lands mid-source, like the existing cancel test.
	firstChunk := make(chan struct{})
	release := make(chan struct{})
	var holdOnce sync.Once
	e := newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(chunkSize),
		withChunkHook(func(p migration.Progress) {
			if p.Processed >= chunkSize {
				holdOnce.Do(func() { close(firstChunk) })
				<-release
			}
		}))

	var rows []srcRow
	for i := 1; i <= sourceRows; i++ {
		rows = append(rows, srcRow{id: fmt.Sprintf("vid%03d", i), title: fmt.Sprintf("Title %d", i)})
	}
	content := writeSource(t, rows)

	resp, body := e.doFile(t, http.MethodPost, "/api/corpus/import", "prior.sqlite", content, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	importID, _ := body["import_id"].(string)

	select {
	case <-firstChunk:
	case <-time.After(30 * time.Second):
		t.Fatal("the first chunk never committed")
	}
	resp, body = e.do(t, http.MethodPost, "/api/corpus/import/"+importID+"/cancel", nil, "")
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body: %v", body)
	close(release)

	st := e.waitTerminal(t, importID)
	require.Equal(t, StatusCancelled, st.Status)
	require.Equal(t, chunkSize, st.Processed)

	// A cancelled import never advances the timestamp, even though a chunk
	// committed.
	e.requireMetadataNull(t)

	// The committed chunk survives; a successful append retry advances it.
	retry := e.importAndWait(t, "prior.sqlite", content)
	require.Equal(t, StatusCompleted, retry.Status)
	e.requireMetadataTimestamp(t)
}

func TestMetadataKeepsPriorValueAfterFailedImport(t *testing.T) {
	const chunkSize = 10
	const sourceRows = 25

	// Break the destination count after the final committed chunk: the job
	// fails at finalization even though every chunk is committed.
	var breakOnce sync.Once
	var e *env
	e = newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(chunkSize),
		withChunkHook(func(p migration.Progress) {
			// Only the second (25-row) import is broken; the small first
			// import must complete normally and record the prior marker.
			if p.Total == sourceRows && p.Processed == p.Total {
				breakOnce.Do(func() {
					_, err := e.dest.Exec(
						"ALTER TABLE title_fingerprint RENAME TO title_fingerprint_hidden;")
					require.NoError(t, err)
				})
			}
		}))

	first := writeSource(t, []srcRow{{id: "v1", title: "One"}})
	require.Equal(t, StatusCompleted, e.importAndWait(t, "a.sqlite", first).Status)
	prior := e.requireMetadataTimestamp(t)

	var rows []srcRow
	for i := 1; i <= sourceRows; i++ {
		rows = append(rows, srcRow{id: fmt.Sprintf("vid%03d", i), title: fmt.Sprintf("Title %d", i)})
	}
	content := writeSource(t, rows)
	st := e.importAndWait(t, "b.sqlite", content)
	require.Equal(t, StatusFailed, st.Status)

	// The prior timestamp stands; the failed import did not advance it.
	require.Equal(t, prior, e.requireMetadataTimestamp(t))
}

func TestMetadataRouteIsReservedLiteral(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)
	content := writeSource(t, []srcRow{{id: "v1", title: "One"}})
	st := e.importAndWait(t, "prior.sqlite", content)
	require.Equal(t, StatusCompleted, st.Status)
	expected := e.requireMetadataTimestamp(t)

	// The literal route answers metadata even while an import ID is known —
	// it is never interpreted as an import ID.
	status, body := e.getMetadata(t)
	require.Equal(t, http.StatusOK, status, "body: %v", body)
	require.NotEqual(t, st.ImportID, body["import_id"])

	jobResp, jobBody := e.do(t, http.MethodGet, "/api/corpus/import/"+st.ImportID, nil, "")
	require.Equal(t, http.StatusOK, jobResp.StatusCode, "body: %v", jobBody)
	require.Equal(t, st.ImportID, jobBody["import_id"])

	// An unknown ID still follows the import-ID contract (404), which the
	// literal route does not shadow.
	unknownResp, unknownBody := e.do(t, http.MethodGet, "/api/corpus/import/not-a-real-id", nil, "")
	require.Equal(t, http.StatusNotFound, unknownResp.StatusCode)
	require.Equal(t, "import not found", unknownBody["error"])

	require.Equal(t, expected, e.requireMetadataTimestamp(t))
}

func TestMetadataReadErrorsReturn500(t *testing.T) {
	cases := []struct {
		name    string
		breakIt func(t *testing.T, e *env)
	}{
		{
			name: "missing singleton row",
			breakIt: func(t *testing.T, e *env) {
				_, err := e.dest.Exec("DELETE FROM corpus_import_metadata WHERE singleton_id = 1;")
				require.NoError(t, err)
			},
		},
		{
			name: "malformed stored timestamp",
			breakIt: func(t *testing.T, e *env) {
				_, err := e.dest.Exec(
					"UPDATE corpus_import_metadata SET last_successful_corpus_import_at = 'not-a-timestamp' WHERE singleton_id = 1;")
				require.NoError(t, err)
			},
		},
		{
			name: "metadata table unreadable",
			breakIt: func(t *testing.T, e *env) {
				_, err := e.dest.Exec("DROP TABLE corpus_import_metadata;")
				require.NoError(t, err)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, migration.DefaultMaxUploadBytes)
			// A recorded value first, so the failure is about reading, not
			// about the null state.
			content := writeSource(t, []srcRow{{id: "v1", title: "One"}})
			require.Equal(t, StatusCompleted, e.importAndWait(t, "prior.sqlite", content).Status)
			e.requireMetadataTimestamp(t)

			tc.breakIt(t, e)

			status, body := e.getMetadata(t)
			require.Equal(t, http.StatusInternalServerError, status, "body: %v", body)
			require.Equal(t,
				map[string]any{"error": "failed to read corpus import metadata"}, body)
		})
	}
}

func TestMetadataWriteFailureFailsWebJob(t *testing.T) {
	const chunkSize = 10
	const sourceRows = 25

	// Break the success-marker write after every chunk committed: the job
	// must become failed, not completed, and no timestamp may be recorded.
	var breakOnce sync.Once
	var e *env
	e = newEnv(t, migration.DefaultMaxUploadBytes, WithChunkSize(chunkSize),
		withChunkHook(func(p migration.Progress) {
			if p.Processed == p.Total {
				breakOnce.Do(func() {
					_, err := e.dest.Exec(
						"ALTER TABLE corpus_import_metadata RENAME TO corpus_import_metadata_hidden;")
					require.NoError(t, err)
				})
			}
		}))

	var rows []srcRow
	for i := 1; i <= sourceRows; i++ {
		rows = append(rows, srcRow{id: fmt.Sprintf("vid%03d", i), title: fmt.Sprintf("Title %d", i)})
	}
	content := writeSource(t, rows)
	st := e.importAndWait(t, "prior.sqlite", content)
	require.Equal(t, StatusFailed, st.Status,
		"a marker-write failure is a job-level failure, not a completion")
	require.EqualValues(t, sourceRows, st.Processed,
		"every chunk stayed committed even though the marker failed")

	// Restore the table and prove the failed job recorded no timestamp.
	_, err := e.dest.Exec(
		"ALTER TABLE corpus_import_metadata_hidden RENAME TO corpus_import_metadata;")
	require.NoError(t, err)
	e.requireMetadataNull(t)
}

func TestMetadataResponseContainsNoSourceContent(t *testing.T) {
	e := newEnv(t, migration.DefaultMaxUploadBytes)
	rows := []srcRow{
		{id: "v1", title: "Zebra Uncommon Title Marker"},
		{id: "v2", title: "Second Raw Title Marker"},
	}
	content := writeSource(t, rows)
	require.Equal(t, StatusCompleted, e.importAndWait(t, "private-source-name.sqlite", content).Status)

	resp, err := http.Get(e.srv.URL + "/api/corpus/import/metadata")
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", raw)

	var decoded wireMetadata
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotNil(t, decoded.LastSuccessfulCorpusImportAt)

	for _, secret := range []string{
		"Zebra Uncommon Title Marker",
		"Second Raw Title Marker",
		"private-source-name.sqlite",
		"prior",
	} {
		require.NotContains(t, string(raw), secret,
			"the metadata response must carry no raw title or upload filename")
	}
}
