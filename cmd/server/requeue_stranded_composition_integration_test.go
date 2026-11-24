//go:build integration

package main

// Re-queue stranded videos over the real server (run-initiation ARS-005 §8,
// §11.9 J1 up to the confirm): the real server process, a migrated SQLite file
// shaped like production's run 14, and a counting fake YouTube endpoint. The
// browser's own client is exercised against this same wire shape by the web
// composition check, not here.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// doBody sends a request with a body and returns the status and trimmed body.
func (p *serverProcess) doBody(method, path, body string) (int, string) {
	p.t.Helper()
	req, err := http.NewRequest(method, "http://"+p.addr+path, strings.NewReader(body))
	require.NoError(p.t, err)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	require.NoError(p.t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(p.t, err)
	return resp.StatusCode, strings.TrimSpace(string(out))
}

func jsonMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m), body)
	return m
}

func TestRequeueStrandedJourneyOverHTTP(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seedBase(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	db := openReadDB(t, dbPath)

	// Run 14 is the stuck legacy run: saved as running, version 0, with 60
	// unfinished videos. One carries legacy send evidence (a matched decision),
	// one already has an active Errors entry (not claimed by any run). Run 15 is a failed version-1 run
	// with five unfinished videos: those are stranded already. One finished
	// video is a control.
	_, err := db.Exec(`INSERT INTO run (id, started_at, run_type, status, mutation_safety_version, action_authority_version, current_phase, videos_discovered, videos_not_yet_evaluated)
		VALUES (14, '2026-10-20T06:00:00Z', 'manual', 'running', 0, 0, 'discovery', 60, 60)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run (id, started_at, completed_at, run_type, status) VALUES (15, '2026-11-06T06:00:00Z', '2026-11-06T07:00:00Z', 'scheduled', 'failed')`)
	require.NoError(t, err)
	insertVideo := func(id string, run int, state, outcome string) int64 {
		res, err := db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', ?, ?, ?)`, id, run, state, outcome)
		require.NoError(t, err)
		vid, err := res.LastInsertId()
		require.NoError(t, err)
		return vid
	}
	var legacy, queued int64
	for i := 0; i < 60; i++ {
		vid := insertVideo(fmt.Sprintf("e14-%03d", i), 14, "not_yet_evaluated", "not_yet_evaluated")
		switch i {
		case 0:
			legacy = vid
		case 1:
			queued = vid
		}
	}
	for i := 0; i < 5; i++ {
		insertVideo(fmt.Sprintf("e15-%03d", i), 15, "not_yet_evaluated", "not_yet_evaluated")
	}
	insertVideo("finished-001", 15, "completed", "accepted")
	routeID := scalar[int64](t, db, `SELECT id FROM route WHERE name='Jazz'`)
	_, err = db.Exec(`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision) VALUES (?, 14, ?, 1, 'matched')`, legacy, routeID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO error_entry (video_id, run_id, error_category, error_message, status) VALUES (?, 14, 'runtime', 'legacy failure', 'active')`, queued)
	require.NoError(t, err)

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	eventually(t, 10*time.Second, "the scheduler to start", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })

	runs := scalar[int](t, db, `SELECT count(*) FROM run`)
	ledger := scalar[int](t, db, `SELECT count(*) FROM quota_ledger`)
	items := func() int { return scalar[int](t, db, `SELECT count(*) FROM deferred_item`) }
	errorsRow := func() string {
		return scalar[string](t, db, `SELECT id||'|'||status||'|'||error_message FROM error_entry WHERE video_id=?`, queued)
	}
	errorsBefore := errorsRow()
	require.Zero(t, items())
	requests := yt.requestCount()

	// Preview while run 14 has not ended: its 58 free videos belong to it.
	status, body := srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	require.JSONEq(t, fmt.Sprintf(`{
		"available": true, "unavailable_reason": null,
		"requeue": 5,
		"already_queued": {"videos": 1, "in_errors_queue": 1},
		"removed_by_user": 0,
		"blocked": {"videos": 1, "by_reason": {"ambiguous_legacy_evidence": 1},
			"items": [{"video_id": %d, "youtube_video_id": "e14-000", "reasons": [{"reason": "ambiguous_legacy_evidence", "run_id": 14}]}]},
		"not_ended": {"videos": 58, "runs": [{"run_id": 14, "videos": 58}]},
		"no_origin_run": 0,
		"retained_without_queue_item": {"videos": 0, "removed_by_user": 0}}`, legacy), body)

	// BR-036 first: end run 14, then the same preview counts its videos.
	status, body = srv.do(http.MethodPost, "/api/runs/14/end")
	require.Equal(t, http.StatusOK, status, body)

	// A production run that is active refuses the action, and nothing changes.
	hold := make(chan struct{})
	yt.setSubscriptionHold(hold)
	status, body = srv.do(http.MethodPost, "/api/runs")
	require.Equal(t, http.StatusAccepted, status, body)
	select {
	case <-yt.subReached:
	case <-time.After(20 * time.Second):
		t.Fatalf("the run never reached discovery\n%s", srv.out.String())
	}
	status, body = srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	preview := jsonMap(t, body)
	require.Equal(t, false, preview["available"])
	require.Equal(t, "production_run_active", preview["unavailable_reason"])
	require.EqualValues(t, 63, preview["requeue"], "the counts are current, run 14's videos included")
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":63}`)
	require.Equal(t, http.StatusConflict, status, body)
	require.JSONEq(t, `{"error":"a production run is already active","code":"production_run_active"}`, body)
	require.Zero(t, items(), "a refused confirm writes nothing")
	close(hold)
	eventually(t, runDoneBudget, "the Run Now run to complete", func() bool {
		return scalar[int](t, db, `SELECT count(*) FROM run WHERE status IN ('running','interrupted') AND id > 15`) == 0
	})
	runs = scalar[int](t, db, `SELECT count(*) FROM run`)
	ledger = scalar[int](t, db, `SELECT count(*) FROM quota_ledger`)
	requests = yt.requestCount()

	// The user confirms with a count from before run 14 ended: the action
	// applies to the set as it is now and says the count differs.
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":5}`)
	require.Equal(t, http.StatusOK, status, body)
	res := jsonMap(t, body)
	require.Equal(t, "requeued", res["outcome"])
	require.EqualValues(t, 63, res["requeued"])
	require.EqualValues(t, 0, res["skipped_concurrently"])
	require.EqualValues(t, 5, res["expected_requeue"])
	require.Equal(t, true, res["differs_from_preview"])
	require.EqualValues(t, 63, res["requeue"])
	require.Equal(t, map[string]any{"videos": float64(0), "runs": []any{}}, res["not_ended"], "run 14 has ended")
	require.EqualValues(t, 1, res["blocked"].(map[string]any)["videos"], "a blocked video stays blocked")

	require.Equal(t, 63, items())
	require.Equal(t, 63, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='pending' AND deferral_reason='requeued_by_user' AND retry_after IS NULL AND retry_count=0`))
	require.Equal(t, 58, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE deferral_reason='requeued_by_user' AND run_id=14`))
	require.Equal(t, 5, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE deferral_reason='requeued_by_user' AND run_id=15`))
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(DISTINCT created_at) FROM deferred_item WHERE deferral_reason='requeued_by_user'`), "one shared created_at")
	require.Equal(t, 0, scalar[int](t, db, `SELECT count(*) FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id IN ('e14-000','finished-001') AND d.deferral_reason='requeued_by_user'`), "blocked and finished videos get no item")
	require.Equal(t, errorsBefore, errorsRow(), "the Errors entry is untouched")
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE video_id=?`, queued), "an Errors-queue video gets no deferred item")
	require.Equal(t, runs, scalar[int](t, db, `SELECT count(*) FROM run`), "the action starts no run")
	require.Equal(t, ledger, scalar[int](t, db, `SELECT count(*) FROM quota_ledger`))
	require.Equal(t, requests, yt.requestCount(), "the action makes no YouTube request")

	// Repeating changes nothing, and the preview now counts none to re-queue.
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", ``)
	require.Equal(t, http.StatusOK, status, body)
	res = jsonMap(t, body)
	require.Equal(t, "nothing_to_requeue", res["outcome"])
	require.EqualValues(t, 0, res["requeued"])
	require.Nil(t, res["expected_requeue"])
	require.Equal(t, false, res["differs_from_preview"])
	require.Equal(t, 63, items())
	status, body = srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	preview = jsonMap(t, body)
	require.Equal(t, true, preview["available"])
	require.EqualValues(t, 0, preview["requeue"])
	require.Equal(t, map[string]any{"videos": float64(64), "in_errors_queue": float64(1)}, preview["already_queued"], "the 63 re-queued videos and the Errors entry")
	require.Equal(t, requests, yt.requestCount())

	// A malformed body is a 400 and writes nothing.
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":-1}`)
	require.Equal(t, http.StatusBadRequest, status, body)
	require.JSONEq(t, `{"error":"invalid request body","code":"invalid_body"}`, body)
	require.Equal(t, 63, items())

	// The paged Deferred read, in the same server, shows them a page at a time.
	status, body = srv.do(http.MethodGet, "/api/deferred?limit=50")
	require.Equal(t, http.StatusOK, status, body)
	page := jsonMap(t, body)
	require.EqualValues(t, 63, page["total"])
	require.Len(t, page["deferred"], 50)
	require.Equal(t, map[string]any{"requeued_by_user": float64(63)}, page["by_reason"])

	srv.sigterm()
}

// While startup recovery holds the producer gate, the real server refuses the
// action with its closed code, and the preview says why (BR-037 AC2).
func TestRequeueStrandedRefusedWhileStartupRecoveryRuns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	runID, _ := seedInterruptedRun(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	hold := make(chan struct{})
	yt.setSubscriptionHold(hold)
	db := openReadDB(t, dbPath)
	_, err := db.Exec(`INSERT INTO run (id, started_at, completed_at, run_type, status) VALUES (900, '2026-11-06T06:00:00Z', '2026-11-06T07:00:00Z', 'scheduled', 'failed')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES ('gate-001', 'UC1', 900, 'not_yet_evaluated', 'not_yet_evaluated')`)
	require.NoError(t, err)

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	select {
	case <-yt.subReached:
	case <-time.After(20 * time.Second):
		t.Fatalf("the recovered run never started discovery\n%s", srv.out.String())
	}
	requests := yt.requestCount()

	status, body := srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	preview := jsonMap(t, body)
	require.Equal(t, false, preview["available"])
	require.Equal(t, "startup_recovery_active", preview["unavailable_reason"])
	require.EqualValues(t, 1, preview["requeue"])

	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":1}`)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
	require.Equal(t, "startup_recovery_active", jsonMap(t, body)["code"])
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM deferred_item`))
	require.Equal(t, requests, yt.requestCount())

	close(hold)
	eventually(t, runDoneBudget, "the recovered run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) == "completed"
	})
	srv.sigterm()
}
