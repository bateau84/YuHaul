package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Discovery ARS-002 section 7 test 5: the read-only source failure endpoints
// through the real mux and a real migrated SQLite database.

func sfChan(c byte) string { return "UC" + strings.Repeat(string(c), 22) }

type sfEnv struct {
	db     *sql.DB
	store  *state.Store
	srv    *Server
	logs   *strings.Builder
	runA   int64
	runB   int64
	ids    []int64
	handle http.Handler
}

func newSFEnv(t *testing.T, wrap func(RunVideoStore) RunVideoStore) *sfEnv {
	t.Helper()
	db, stateStore, _, cleanup := setupServerTest(t)
	t.Cleanup(cleanup)
	e := &sfEnv{db: db, store: stateStore, logs: &strings.Builder{}}
	ctx := context.Background()
	var err error
	e.runA, err = stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted})
	require.NoError(t, err)
	e.runB, err = stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeScheduled, Status: state.RunStatusCompleted})
	require.NoError(t, err)

	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	var rv RunVideoStore = stateStore
	if wrap != nil {
		rv = wrap(rv)
	}
	e.srv = NewServer(&fakePipeline{}, pipeline.NewBroadcaster(),
		WithServerLogger(slog.New(slog.NewTextHandler(e.logs, nil))),
		WithRunStore(stateStore),
		WithRunVideoStore(rv),
		WithQueueHandler(queue.NewService(qs, nil)))
	e.handle = e.srv.Handler()
	return e
}

func (e *sfEnv) record(t *testing.T, runID int64, c byte) {
	t.Helper()
	tx, err := e.db.Begin()
	require.NoError(t, err)
	require.NoError(t, state.RecordUploadsSourceFailureTx(context.Background(), tx, runID, sfChan(c), time.Date(2026, 11, 20, 12, 0, int(c), 0, time.UTC)))
	require.NoError(t, tx.Commit())
}

func (e *sfEnv) do(method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.handle.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

type sfPage struct {
	Items        []map[string]any `json:"items"`
	NextBeforeID *float64         `json:"next_before_id"`
}

func decodeSF(t *testing.T, rec *httptest.ResponseRecorder) sfPage {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var p sfPage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

func TestSourceFailureEndpointsShapeOrderingAndPaging(t *testing.T) {
	e := newSFEnv(t, nil)
	for _, c := range []byte{'A', 'B', 'C'} {
		e.record(t, e.runA, c)
	}
	e.record(t, e.runB, 'A')
	_, err := e.db.Exec(`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered) VALUES (?, 'Channel B', '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z', 1)`, sfChan('B'))
	require.NoError(t, err)

	all := decodeSF(t, e.do(http.MethodGet, "/api/errors/sources"))
	require.Len(t, all.Items, 4)
	require.Nil(t, all.NextBeforeID)
	for i := 1; i < len(all.Items); i++ {
		require.Greater(t, all.Items[i-1]["id"], all.Items[i]["id"], "newest first")
	}
	for _, it := range all.Items {
		require.Equal(t, []string{"category", "channel_id", "channel_name", "http_status", "id", "occurred_at", "operation", "reason", "run_id"}, sortedKeys(it))
		require.Equal(t, "permanent", it["category"])
		require.Equal(t, "playlist_items_list", it["operation"])
		require.EqualValues(t, 404, it["http_status"])
		require.Equal(t, "playlistNotFound", it["reason"])
		if it["channel_id"] == sfChan('B') {
			require.Equal(t, "Channel B", it["channel_name"])
		} else {
			require.Nil(t, it["channel_name"], "null when the channel never produced a video")
		}
	}
	require.Equal(t, "2026-11-20T12:01:05Z", all.Items[0]["occurred_at"], "UTC RFC 3339 of the newest row (run B, channel A, 65 s)")

	p1 := decodeSF(t, e.do(http.MethodGet, "/api/errors/sources?limit=3"))
	require.Len(t, p1.Items, 3)
	require.NotNil(t, p1.NextBeforeID)
	require.EqualValues(t, p1.Items[2]["id"], *p1.NextBeforeID)
	p2 := decodeSF(t, e.do(http.MethodGet, fmt.Sprintf("/api/errors/sources?limit=3&before_id=%d", int64(*p1.NextBeforeID))))
	require.Len(t, p2.Items, 1)
	require.Nil(t, p2.NextBeforeID)

	run := decodeSF(t, e.do(http.MethodGet, fmt.Sprintf("/api/runs/%d/discovery-source-errors", e.runA)))
	require.Len(t, run.Items, 3)
	for _, it := range run.Items {
		require.EqualValues(t, e.runA, it["run_id"])
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func TestSourceFailureEndpointsRejectBadInput(t *testing.T) {
	e := newSFEnv(t, nil)
	for _, target := range []string{
		"/api/errors/sources?limit=0", "/api/errors/sources?limit=201", "/api/errors/sources?limit=abc", "/api/errors/sources?limit=-1",
		"/api/errors/sources?before_id=0", "/api/errors/sources?before_id=-5", "/api/errors/sources?before_id=x",
		fmt.Sprintf("/api/runs/%d/discovery-source-errors?limit=1000", e.runA),
		"/api/runs/abc/discovery-source-errors", "/api/runs/0/discovery-source-errors",
	} {
		require.Equal(t, http.StatusBadRequest, e.do(http.MethodGet, target).Code, target)
	}
	require.Equal(t, http.StatusNotFound, e.do(http.MethodGet, "/api/runs/99999/discovery-source-errors").Code)
	okRun := e.do(http.MethodGet, fmt.Sprintf("/api/runs/%d/discovery-source-errors", e.runA))
	require.JSONEq(t, `{"items":[],"next_before_id":null}`, okRun.Body.String(), "a run with no rows is 200 with an empty list")
	require.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/errors/sources?limit=200").Code)
}

type failingSourceStore struct{ RunVideoStore }

func (failingSourceStore) ListSourceFailures(context.Context, state.SourceFailureQuery) (*state.SourceFailurePage, error) {
	return nil, errors.New("read failed at /secret/path?key=1")
}

func TestSourceFailureReadErrorIsASafe500NotAnEmptyList(t *testing.T) {
	e := newSFEnv(t, func(s RunVideoStore) RunVideoStore { return failingSourceStore{s} })
	for _, target := range []string{"/api/errors/sources", fmt.Sprintf("/api/runs/%d/discovery-source-errors", e.runA)} {
		rec := e.do(http.MethodGet, target)
		require.Equal(t, http.StatusInternalServerError, rec.Code, target)
		require.NotContains(t, rec.Body.String(), "items")
		require.NotContains(t, rec.Body.String(), "secret")
	}
	require.NotContains(t, e.logs.String(), "/secret/path", "error text stays out of the log")
}

func TestSourceFailureProjectionWithholdsAndNullsUnsafeValues(t *testing.T) {
	e := newSFEnv(t, nil)
	e.db.SetMaxOpenConns(1)
	e.record(t, e.runA, 'A')
	_, err := e.db.Exec(`PRAGMA ignore_check_constraints = ON`)
	require.NoError(t, err)
	ins := func(channel, category string, reason any) {
		_, err := e.db.Exec(`INSERT INTO discovery_source_failure (run_id, channel_id, occurred_at, category, operation, http_status, reason) VALUES (?, ?, '2026-11-20T00:00:00Z', ?, 'playlist_items_list', 404, ?)`, e.runA, channel, category, reason)
		require.NoError(t, err)
	}
	ins(sfChan('Z'), "mystery", "playlistNotFound")                       // unknown category: withheld
	ins("not-a-valid-channel-id!!", "transient", "https://x/?key=1 oops") // invalid ID, unvetted reason
	_, err = e.db.Exec(`PRAGMA ignore_check_constraints = OFF`)
	require.NoError(t, err)

	rec := e.do(http.MethodGet, fmt.Sprintf("/api/runs/%d/discovery-source-errors", e.runA))
	p := decodeSF(t, rec)
	require.Len(t, p.Items, 2, "the unknown category is withheld")
	odd := p.Items[0]
	require.Nil(t, odd["channel_id"])
	require.Nil(t, odd["reason"])
	require.NotContains(t, rec.Body.String(), "key=1")
	require.NotContains(t, rec.Body.String(), "mystery")
	require.Contains(t, e.logs.String(), "source failure rows withheld")
}

func TestSourceFailureResponsesNeverCarryPlaylistsTokensURLsOrText(t *testing.T) {
	e := newSFEnv(t, nil)
	e.record(t, e.runA, 'B')
	for _, target := range []string{"/api/errors/sources", fmt.Sprintf("/api/runs/%d/discovery-source-errors", e.runA)} {
		body := e.do(http.MethodGet, target).Body.String()
		for _, banned := range []string{"UU" + strings.Repeat("B", 22), "http://", "https://", "pageToken", "error\":", "playlist_id", "title"} {
			require.NotContains(t, body, banned, target)
		}
	}
}

func TestSourceEntriesHaveNoCommandsAndLeaveTheVideoErrorQueueUnchanged(t *testing.T) {
	e := newSFEnv(t, nil)
	list := func() string {
		rec := e.do(http.MethodGet, "/api/errors")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Body.String()
	}
	before := list()
	e.record(t, e.runA, 'B')
	require.Equal(t, before, list(), "a source row does not appear in the video error list or its totals")

	for _, target := range []string{"/api/errors/sources/retry", "/api/errors/sources/dismiss", "/api/errors/sources"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			rec := e.do(method, target)
			require.GreaterOrEqual(t, rec.Code, 400, "%s %s", method, target)
			require.Less(t, rec.Code, 500, "%s %s", method, target)
		}
	}
	for _, target := range []string{"/api/errors/sources/retry", "/api/errors/sources/dismiss"} {
		require.Equal(t, http.StatusBadRequest, e.do(http.MethodPost, target).Code, target, "refused by the integer ID parsing")
	}
	require.Equal(t, http.StatusMethodNotAllowed, e.do(http.MethodPost, fmt.Sprintf("/api/runs/%d/discovery-source-errors", e.runA)).Code)
	require.Equal(t, before, list())
	var n int
	require.NoError(t, e.db.QueryRow(`SELECT COUNT(*) FROM discovery_source_failure`).Scan(&n))
	require.Equal(t, 1, n, "no command touched the record")
}
