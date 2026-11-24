//go:build integration

package main

// ARS-006 §10.4 test 28 (K18): the BR-039 example over the real server.
//
// The test binary re-executes itself into the real main() (TestMain in
// startup_composition_integration_test.go), so the migrated SQLite store, the
// registered HTTP routes, the production pipeline, the scheduler, the send
// guard and the guarded YouTube adapter are the shipped ones. Only YouTube is
// a loopback fake. The product produces the original decision itself: Run Now
// matches V to "Lofi" and "Study", sends "Lofi", and the provider refuses
// "Study" for quota, so V is partly done and its Study addition is pending.
// The same run rejects W. The prerequisites written by the test are the ones
// the BR-039 example names or production already holds: the user's route edit
// (over HTTP), the stale deferred item that points at the already final W, a
// schedule, and one unfinished video in a legacy run that the preview must
// count while it must not count V or W.
//
// Placement: ARS-006 §10.4 names `-tags=integration ./e2e`. The real-process
// harness (serverProcess, the loopback fake, SIGTERM and restart) lives in
// package main, which ./e2e cannot import, so the journey sits beside J1 and
// G2:
//
//	go test -tags=integration -count=1 -run '^TestK18' ./cmd/server
//
// It takes about a minute: the real scheduler checks once a minute and fires
// on the first check after the process starts.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/scheduler"
	"github.com/bateau84/YuHaul/internal/store"
)

const (
	k18Lofi  = "PLjazz"   // the route seedBase creates
	k18Study = "PLsecond" // the route this journey adds
	k18V     = "k18-v"    // matches both routes: partly done after run 40
	k18W     = "k18-w"    // matches neither: rejected in run 40
	k18S     = "k18-s"    // unfinished in a legacy run: the one stranded video
)

// insertCall is one playlistItems.insert the provider received.
type insertCall struct {
	Playlist, Video string
	Status          int
}

// insertGate is the base URL the server talks to. It forwards everything to
// the fake YouTube, and answers a quotaExceeded refusal to inserts into
// k18Study while refuseStudy is set. It records every insert in arrival order.
type insertGate struct {
	srv         *httptest.Server
	refuseStudy atomic.Bool

	mu    sync.Mutex
	calls []insertCall
}

func newInsertGate(t *testing.T, fake *fakeYouTube) *insertGate {
	t.Helper()
	target, err := url.Parse(fake.srv.URL)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	g := &insertGate{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/playlistItems" {
			proxy.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body struct {
			Snippet struct {
				PlaylistID string `json:"playlistId"`
				ResourceID struct {
					VideoID string `json:"videoId"`
				} `json:"resourceId"`
			} `json:"snippet"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		call := insertCall{Playlist: body.Snippet.PlaylistID, Video: body.Snippet.ResourceID.VideoID, Status: http.StatusOK}
		if call.Playlist == k18Study && g.refuseStudy.Load() {
			call.Status = http.StatusForbidden
		}
		g.mu.Lock()
		g.calls = append(g.calls, call)
		g.mu.Unlock()
		if call.Status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(call.Status)
			_, _ = io.WriteString(w, `{"error":{"code":403,"message":"quota","errors":[{"reason":"quotaExceeded","domain":"youtube.quota"}]}}`)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *insertGate) all() []insertCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]insertCall(nil), g.calls...)
}

// sql group_concat of rows, in a fixed order, as one comparable string.
func k18Rows(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(query, args...)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return strings.Join(out, "\n")
}

// k18Record is everything the store holds about one video that BR-039 says a
// later run must not change: the video row, every decision, every executed
// action, every run outcome. The queue rows are read separately because the
// journey is meant to change them.
func k18Record(t *testing.T, db *sql.DB, external string) string {
	t.Helper()
	return strings.Join([]string{
		k18Rows(t, db, `SELECT 'video|'||v.id||'|'||v.processing_state||'|'||IFNULL(v.final_outcome,'-')||'|'||IFNULL(v.completed_at,'-') FROM video v WHERE v.video_id=?`, external),
		k18Rows(t, db, `SELECT 'decision|'||r.id||'|'||r.run_id||'|'||r.route_id||'|'||r.decision FROM route_decision r JOIN video v ON v.id=r.video_id WHERE v.video_id=? ORDER BY r.id`, external),
		k18Rows(t, db, `SELECT 'action|'||a.id||'|'||a.run_id||'|'||a.route_decision_id||'|'||a.target_playlist_id||'|'||a.outcome||'|'||IFNULL(a.outcome_reason,'-') FROM executed_action a JOIN video v ON v.id=a.video_id WHERE v.video_id=? ORDER BY a.id`, external),
		k18Rows(t, db, `SELECT 'outcome|'||o.run_id||'|'||o.final_outcome FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE v.video_id=? ORDER BY o.run_id`, external),
	}, "\n")
}

// k18Item is the stored columns of every queue row of one video.
func k18Item(t *testing.T, db *sql.DB, external string) string {
	t.Helper()
	return queueRow(t, db, external)
}

func k18ItemID(t *testing.T, db *sql.DB, external string) int64 {
	t.Helper()
	return scalar[int64](t, db, `SELECT d.id FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, external)
}

func k18RunVideo(t *testing.T, srv *serverProcess, run int64, external string) map[string]any {
	t.Helper()
	status, body := srv.do(http.MethodGet, "/api/runs/"+strconv.FormatInt(run, 10)+"/videos/"+external)
	require.Equal(t, http.StatusOK, status, body)
	video, ok := jsonMap(t, body)["video"].(map[string]any)
	require.True(t, ok, body)
	return video
}

// k18DisableRoutes is the user's edit in the example: every route is turned
// off over the public API, so a fresh evaluation of V would match nothing.
func k18DisableRoutes(t *testing.T, srv *serverProcess) {
	t.Helper()
	status, body := srv.do(http.MethodGet, "/api/routes")
	require.Equal(t, http.StatusOK, status, body)
	var routes []map[string]any
	var wrapped struct {
		Routes []map[string]any `json:"routes"`
	}
	if json.Unmarshal([]byte(body), &routes) != nil {
		require.NoError(t, json.Unmarshal([]byte(body), &wrapped), body)
		routes = wrapped.Routes
	}
	require.NotEmpty(t, routes, body)
	for _, route := range routes {
		route["is_enabled"] = false
		raw, err := json.Marshal(route)
		require.NoError(t, err)
		status, out := srv.doBody(http.MethodPut, "/api/routes/"+route["id"].(string), string(raw))
		require.Equal(t, http.StatusOK, status, out)
	}
}

func TestK18_PartlyDoneVideoFinishesAndFinalVideoStaysFinal(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seedBase(t, dbPath)
	func() {
		db, err := store.Open(ctx, dbPath)
		require.NoError(t, err)
		defer db.Close()
		_, err = router.NewStore(db).CreateRoute(ctx, &router.Route{
			Name: "Study", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "OR",
			Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
			Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: k18Study}},
		})
		require.NoError(t, err)
	}()
	db := openReadDB(t, dbPath)

	yt := newFakeYouTube(t, k18V, k18W)
	yt.titleOf = func(id string) string {
		if id == k18V {
			return "Jazz night " + id
		}
		return "Plain talk " + id
	}
	recordPosts(t, yt) // drains the fake's insert announcements
	prov := &d7Provider{}
	prov.setBody(metadataScript{
		absent: func(string) bool { return false },
		jazz:   func(id string) bool { return id == k18V },
	}.body)
	yt.setVideosHook(prov.handle)
	gate := newInsertGate(t, yt)
	gate.refuseStudy.Store(true)

	// Process 1. Run Now matches V to Lofi and Study and rejects W; the
	// provider refuses Study for quota.
	srv := startServer(t, dbPath, gate.srv.URL)
	srv.waitOverview()
	run40 := d7RunNow(t, srv, db)

	require.Equal(t, []insertCall{{k18Lofi, k18V, 200}, {k18Study, k18V, 403}}, gate.all(), "run 40: Lofi sent, Study refused for quota")
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM executed_action a JOIN video v ON v.id=a.video_id WHERE v.video_id=? AND a.target_playlist_id=? AND a.outcome='succeeded'`, k18V, k18Lofi))
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM executed_action a JOIN video v ON v.id=a.video_id WHERE v.video_id=? AND a.target_playlist_id=? AND a.outcome='deferred'`, k18V, k18Study))
	require.Equal(t, "pending", scalar[string](t, db, `SELECT d.status FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, k18V), "V's Study addition waits in the queue")
	require.NotContains(t, []string{"accepted", "rejected", "excluded", "unavailable_on_youtube"}, scalar[string](t, db, `SELECT final_outcome FROM video WHERE video_id=?`, k18V), "V has no final outcome: it is partly done")
	require.Equal(t, "rejected", scalar[string](t, db, `SELECT final_outcome FROM video WHERE video_id=?`, k18W))
	require.Equal(t, "rejected", runOutcome(t, db, run40, k18W))
	studyDecision := scalar[int64](t, db, `SELECT r.id FROM route_decision r JOIN video v ON v.id=r.video_id JOIN route ro ON ro.id=r.route_id WHERE v.video_id=? AND r.run_id=? AND ro.name='Study' AND r.decision='matched'`, k18V, run40)
	run40Detail := k18RunVideo(t, srv, run40, k18V)
	require.Len(t, run40Detail["route_decisions"], 2, "run 40 decided both routes for V")
	requestsAfterRun40 := prov.seen()

	// The user edits the routes so V would match nothing; the process stops.
	k18DisableRoutes(t, srv)
	srv.sigterm()

	// What production holds at the restart: W's stale deferred item (it was
	// final before the item was written), one unfinished video in a legacy
	// run, and a schedule.
	wRecord, vRecord := k18Record(t, db, k18W), k18Record(t, db, k18V)
	vItem := k18Item(t, db, k18V)
	legacy := scalar[int64](t, db, `SELECT max(id) FROM run`) + 1
	_, err := db.Exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at)
		SELECT id, ?, 'quota_exhausted', 0, 'pending', ? FROM video WHERE video_id=?`, run40, time.Now().UTC().Add(-24*time.Hour).Truncate(time.Second), k18W)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run (id, started_at, run_type, status, mutation_safety_version, action_authority_version, current_phase)
		VALUES (?, ?, 'manual', 'running', 0, 0, 'discovery')`, legacy, "2026-10-20T06:00:00Z")
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', ?, 'not_yet_evaluated', 'not_yet_evaluated')`, k18S, legacy)
	require.NoError(t, err)
	require.NoError(t, mustScheduler(t, db).SetSchedule(ctx, &scheduler.Schedule{Expression: "@every 24h", Enabled: true}))
	wItem := k18Item(t, db, k18W)
	require.Contains(t, wItem, "|pending|")

	// Process 2: the restart. Nothing the first process decided has changed.
	gate.refuseStudy.Store(false)
	insertsBefore := len(gate.all())
	srv = startServer(t, dbPath, gate.srv.URL)
	srv.waitOverview()
	waitLog(t, srv, "scheduler started")
	require.Equal(t, vRecord, k18Record(t, db, k18V), "the restart changed none of V's decisions, actions or outcomes")
	require.Equal(t, wRecord, k18Record(t, db, k18W))
	require.Equal(t, vItem, k18Item(t, db, k18V), "the restart changed V's queue row")
	require.Equal(t, wItem, k18Item(t, db, k18W), "the restart changed W's queue row")
	require.Equal(t, run40Detail["route_decisions"], k18RunVideo(t, srv, run40, k18V)["route_decisions"], "run 40 still shows its decisions for V")
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM run WHERE id > ?`, legacy), "no run started before the scheduler's first check")

	// The next scheduled run.
	run41 := waitScheduledRun(t, db, srv, legacy)

	// Exactly one insertion, and it is Study. Lofi is not sent again; W and
	// every other video are not fetched or sent.
	require.Equal(t, []insertCall{{k18Study, k18V, 200}}, gate.all()[insertsBefore:], "the scheduled run sends exactly one insertion: Study")
	require.Equal(t, requestsAfterRun40, prov.seen(), "run 41 fetches no metadata for V or W")
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM route_decision r JOIN video v ON v.id=r.video_id WHERE r.run_id=? AND v.video_id IN (?, ?)`, run41, k18V, k18W), "run 41 evaluates no routes for V or W")
	studyAction := scalar[int64](t, db, `SELECT a.route_decision_id FROM executed_action a JOIN video v ON v.id=a.video_id WHERE v.video_id=? AND a.run_id=? AND a.target_playlist_id=? AND a.outcome='succeeded'`, k18V, run41, k18Study)
	require.Equal(t, studyDecision, studyAction, "Study's send belongs to run 40's decision")
	require.Equal(t, "already_completed", scalar[string](t, db, `SELECT a.outcome_reason FROM executed_action a JOIN video v ON v.id=a.video_id WHERE v.video_id=? AND a.run_id=? AND a.target_playlist_id=? AND a.outcome='not_attempted'`, k18V, run41, k18Lofi))
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM executed_action a JOIN video v ON v.id=a.video_id WHERE v.video_id=? AND a.target_playlist_id=? AND a.outcome='succeeded'`, k18V, k18Lofi), "Lofi's success is recorded once and unchanged")
	require.Equal(t, "accepted", scalar[string](t, db, `SELECT final_outcome FROM video WHERE video_id=?`, k18V))
	require.Equal(t, "accepted", runOutcome(t, db, run41, k18V))
	require.Equal(t, "completed", scalar[string](t, db, `SELECT d.status FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, k18V))

	// W stays rejected. Run 41 commits and counts nothing for it, and its
	// item is closed with the reason and kept.
	require.Equal(t, wRecord, k18Record(t, db, k18W), "W's outcome, decisions and outcomes are unchanged")
	require.Equal(t, "(none)", runOutcome(t, db, run41, k18W))
	closed := k18Item(t, db, k18W)
	require.Contains(t, closed, "|completed|")
	require.Contains(t, closed, "|final_outcome", "W's item records that it was not processed because W already has a final outcome")
	require.Contains(t, closed, strconv.FormatInt(k18ItemID(t, db, k18W), 10)+"|", "the closed item is kept")
	status, body := srv.do(http.MethodGet, "/api/errors")
	require.Equal(t, http.StatusOK, status, body)
	require.Zero(t, jsonMap(t, body)["total"], "nothing run 41 did is an error")

	// Run detail shows the continued action and no decision of its own.
	run41V := k18RunVideo(t, srv, run41, k18V)
	require.Equal(t, "accepted", run41V["final_outcome"])
	require.Empty(t, run41V["route_decisions"], "run 41 made no decision for V")
	continued, ok := run41V["continued_actions"].([]any)
	require.True(t, ok, "%v", run41V["continued_actions"])
	byPlaylist := map[string]map[string]any{}
	for _, c := range continued {
		a := c.(map[string]any)
		require.EqualValues(t, run40, a["decision_run_id"], "every continued action is under run 40's decision")
		byPlaylist[a["target_playlist_id"].(string)] = a
	}
	require.Len(t, byPlaylist, 2)
	require.Equal(t, "succeeded", byPlaylist[k18Study]["outcome"])
	require.EqualValues(t, studyDecision, byPlaylist[k18Study]["route_decision_id"])
	require.Equal(t, "not_attempted", byPlaylist[k18Lofi]["outcome"])
	require.Empty(t, k18RunVideo(t, srv, run40, k18V)["continued_actions"], "run 40 lists no continued actions")
	status, body = srv.do(http.MethodGet, "/api/runs/"+strconv.FormatInt(run41, 10))
	require.Equal(t, http.StatusOK, status, body)
	require.EqualValues(t, 0, jsonMap(t, body)["videos_rejected"], "W is not counted in run 41: %s", body)

	// The user retries W: refused, and nothing else changes.
	runs, requests, inserts := scalar[int](t, db, `SELECT count(*) FROM run`), yt.requestCount(), len(gate.all())
	vAfter, wItemAfter := k18Record(t, db, k18V), k18Item(t, db, k18W)
	status, body = srv.do(http.MethodPost, "/api/deferred/"+strconv.FormatInt(k18ItemID(t, db, k18W), 10)+"/retry")
	require.Equal(t, http.StatusConflict, status, body)
	refusal := jsonMap(t, body)
	require.Equal(t, "video_already_final", refusal["code"], body)
	require.Equal(t, "rejected", refusal["final_outcome"], body)
	require.Equal(t, "final_outcome", refusal["entry"].(map[string]any)["closed_reason"], body)
	require.Equal(t, runs, scalar[int](t, db, `SELECT count(*) FROM run`), "the refused retry starts no run")
	require.Equal(t, requests, yt.requestCount(), "the refused retry makes no provider request")
	require.Equal(t, inserts, len(gate.all()))
	require.Equal(t, wRecord, k18Record(t, db, k18W))
	require.Equal(t, wItemAfter, k18Item(t, db, k18W), "the refused retry changed W's item")
	require.Equal(t, vAfter, k18Record(t, db, k18V))

	// The re-queue preview counts S, the one unfinished video, and neither V
	// nor W.
	status, body = srv.do(http.MethodPost, "/api/runs/"+strconv.FormatInt(legacy, 10)+"/end")
	require.Equal(t, http.StatusOK, status, body)
	status, body = srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	preview := jsonMap(t, body)
	require.Equal(t, true, preview["available"], body)
	require.EqualValues(t, 1, preview["requeue"], "only S is stranded: %s", body)
	require.EqualValues(t, 0, preview["already_queued"].(map[string]any)["videos"], body)
	require.EqualValues(t, 0, preview["blocked"].(map[string]any)["videos"], body)
	require.NotContains(t, body, k18W)
	require.NotContains(t, body, k18V)
	require.Equal(t, wRecord, k18Record(t, db, k18W), "W is still rejected after the preview")
	require.Equal(t, wItemAfter, k18Item(t, db, k18W))
	require.Equal(t, requests, yt.requestCount(), "the preview makes no provider request")
	require.Equal(t, 2, scalar[int](t, db, `SELECT count(*) FROM executed_action WHERE outcome='succeeded'`), "two successes in the whole journey: Lofi and Study")
	srv.sigterm()
}
