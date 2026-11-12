//go:build integration

package main

// Stop and unusable-row composition tests (run-initiation ARS-003 §7.6 U1/U3
// and §7.8 E1). Like the ADR-007 tests, each case runs the real server
// composition as a child process against a disposable SQLite file and a
// loopback fake YouTube endpoint; nothing can reach a live provider.

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/store"
)

// seedBase builds a disposable database with a synthetic encrypted token and a
// Jazz route, and no run at all.
func seedBase(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	defer db.Close()
	tokens, err := auth.NewSQLiteTokenStore(db, []byte(childKey))
	require.NoError(t, err)
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "local-test", RefreshToken: "local-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	routerStore := router.NewStore(db)
	_, err = routerStore.CreateRoute(ctx, &router.Route{
		Name: "Jazz", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "OR",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
		Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"}},
	})
	require.NoError(t, err)
}

func (p *serverProcess) postJSON(path string) (int, map[string]any) {
	p.t.Helper()
	code, body := p.do("POST", path)
	var out map[string]any
	require.NoError(p.t, json.Unmarshal([]byte(body), &out), body)
	return code, out
}

func (p *serverProcess) getJSON(path string) map[string]any {
	p.t.Helper()
	code, body := p.do("GET", path)
	require.Equal(p.t, 200, code, body)
	var out map[string]any
	require.NoError(p.t, json.Unmarshal([]byte(body), &out), body)
	return out
}

func waitLog(t *testing.T, srv *serverProcess, needle string) {
	t.Helper()
	eventually(t, 30*time.Second, "log line "+needle, func() bool { return strings.Contains(srv.out.String(), needle) })
}

// U1 (AC9): the process dies after the stop was acknowledged and before it
// settled. The restart settles the run as stopped, never resumes it, keeps the
// guard of the admitted POST (its action stays uncertain) and hands the
// unfinished videos to the deferred queue. The "before it settled" window is
// held open by a trigger that fails the settle, then the process is killed
// with SIGKILL.
func TestU1_KillAfterStopAckIsSettledAtRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	runID, _ := seedInterruptedRun(t, dbPath)
	yt := newFakeYouTube(t, "V1", "V2")
	yt.setPostHold(make(chan struct{})) // V1's response never arrives
	db := openReadDB(t, dbPath)
	writer, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	_, err = writer.Exec(`CREATE TRIGGER block_settle BEFORE UPDATE OF status ON run WHEN NEW.status='stopped' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	select {
	case v := <-yt.postReached:
		require.Equal(t, "V1", v)
	case <-time.After(30 * time.Second):
		t.Fatalf("no playlist insertion was admitted\n%s", srv.out.String())
	}
	code, body := srv.postJSON("/api/runs/" + itoa(runID) + "/stop")
	require.Equal(t, 202, code, "%v", body)
	require.Equal(t, "accepted", body["stop"])
	waitLog(t, srv, "run stop settle failed")
	require.NoError(t, srv.cmd.Process.Kill())
	<-srv.exited

	require.Equal(t, "running", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID))
	require.NotEmpty(t, scalar[sql.NullString](t, db, `SELECT stop_requested_at FROM run WHERE id=?`, runID).String)

	_, err = writer.Exec(`DROP TRIGGER block_settle`)
	require.NoError(t, err)
	yt.setPostHold(nil)
	restarted := startServer(t, dbPath, yt.srv.URL)
	restarted.waitOverview()
	eventually(t, 30*time.Second, "the restart to settle the run", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) == "stopped"
	})
	require.Equal(t, 1, yt.postCount("V1"), "the admitted POST is not repeated")
	require.Zero(t, yt.postCount("V2"), "a stopped run is never resumed")
	require.Equal(t, "diag:v1 phase=unknown operation=stop_run code=stopped_by_user", scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, runID))
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM playlist_send_guard`), "the admitted POST's guard is kept")
	require.Equal(t, "mutation_application_uncertain", scalar[string](t, db, `SELECT outcome_reason FROM executed_action WHERE run_id=?`, runID))
	require.Equal(t, 2, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='pending' AND deferral_reason='stopped_by_user' AND retry_after IS NULL`))
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM error_entry`))
	restarted.sigterm()
}

// U3 and V1 at process level: an unusable time-limit Run Now row no longer
// stops the process from starting. Run Now refuses it by ID, the run reads
// discardable on the API, and Discard ends it; the recoverable row resumed.
func TestU3_ProcessStartsWithUnusableTimeLimitRow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	recoverableID, configID := seedInterruptedRun(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	db := openReadDB(t, dbPath)
	writer, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	res, err := writer.Exec(`INSERT INTO run (started_at, run_type, status, config_version_id, action_authority_version, mutation_safety_version,
		initiated_by, interrupt_cause, current_phase, interrupt_reason)
		VALUES ('2020-01-01T00:00:00.000000000Z','manual','interrupted',?,1,1,'run_now','time_limit','bogus','diag:v1 phase=action_execution operation=load_run code=timeout')`, configID)
	require.NoError(t, err)
	unusableID, err := res.LastInsertId()
	require.NoError(t, err)
	before := scalar[string](t, db, `SELECT group_concat(id||status||ifnull(current_phase,'')||ifnull(interrupt_reason,''),'|') FROM run WHERE id=?`, unusableID)

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	require.NotContains(t, srv.out.String(), "startup recovery blocked")
	require.Contains(t, srv.out.String(), "startup recovery left unusable run")
	require.Contains(t, srv.out.String(), `"run_id":`+itoa(unusableID)+`,"cause":"checkpoint_unrecognized"`)
	eventually(t, runDoneBudget, "the recoverable run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, recoverableID) == "completed"
	})
	require.Equal(t, before, scalar[string](t, db, `SELECT group_concat(id||status||ifnull(current_phase,'')||ifnull(interrupt_reason,''),'|') FROM run WHERE id=?`, unusableID))

	waitLog(t, srv, "scheduler started") // the producer gate is open
	code, body := srv.postJSON("/api/runs")
	require.Equal(t, 409, code)
	require.Equal(t, "resume_unavailable", body["code"])
	require.EqualValues(t, unusableID, body["run_id"])
	read := srv.getJSON("/api/runs/" + itoa(unusableID))
	require.Equal(t, true, read["discardable"])
	require.Equal(t, "checkpoint_unrecognized", read["discard_cause"])
	require.Equal(t, false, srv.getJSON("/api/runs/" + itoa(recoverableID))["discardable"])

	code, body = srv.postJSON("/api/runs/" + itoa(unusableID) + "/discard")
	require.Equal(t, 200, code, "%v", body)
	require.Equal(t, "discarded", body["outcome"])
	require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, unusableID))
	code, body = srv.postJSON("/api/runs/" + itoa(unusableID) + "/discard")
	require.Equal(t, 409, code)
	require.Equal(t, "run_ended", body["code"])
	srv.sigterm()
}

// E1 stop journey: stop an executing Run Now run mid-POST over HTTP, restart,
// press Run Now: the new run claims the handed-over videos and the provider's
// insert count equals only the unsent target.
func TestE1_StopJourney(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seedBase(t, dbPath)
	yt := newFakeYouTube(t, "V1", "V2")
	yt.setPostHold(make(chan struct{}))
	db := openReadDB(t, dbPath)

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	waitLog(t, srv, "scheduler started") // the producer gate is open
	code, body := srv.postJSON("/api/runs")
	require.Equal(t, 202, code, "%v", body)
	runID := int64(body["run_id"].(float64))
	select {
	case v := <-yt.postReached:
		require.Equal(t, "V1", v)
	case <-time.After(30 * time.Second):
		t.Fatalf("no playlist insertion was admitted\n%s", srv.out.String())
	}

	code, body = srv.postJSON("/api/runs/" + itoa(runID) + "/stop")
	require.Equal(t, 202, code, "%v", body)
	require.Equal(t, "running", body["status"])
	eventually(t, 30*time.Second, "the run to settle as stopped", func() bool {
		return srv.getJSON("/api/runs/" + itoa(runID))["status"] == "stopped"
	})
	read := srv.getJSON("/api/runs/" + itoa(runID))
	require.NotNil(t, read["stop_requested_at"])
	require.Equal(t, false, read["discardable"])
	code, body = srv.postJSON("/api/runs/" + itoa(runID) + "/stop")
	require.Equal(t, 409, code)
	require.Equal(t, "run_ended", body["code"])
	require.Equal(t, "stopped", body["status"])
	srv.sigterm()

	yt.setPostHold(nil)
	restarted := startServer(t, dbPath, yt.srv.URL)
	restarted.waitOverview()
	waitLog(t, restarted, "scheduler started")
	code, body = restarted.postJSON("/api/runs")
	require.Equal(t, 202, code, "%v", body)
	require.Equal(t, "started", body["admission"], "a stopped run is not a resume candidate")
	newID := int64(body["run_id"].(float64))
	require.NotEqual(t, runID, newID)
	eventually(t, runDoneBudget, "the new run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, newID) == "completed"
	})
	require.Equal(t, 1, yt.postCount("V1"), "the guarded target is never sent again")
	require.Equal(t, 1, yt.postCount("V2"), "the provider's insert count equals only the unsent target")
	require.Equal(t, 2, scalar[int](t, db, `SELECT count(*) FROM run_video_outcome WHERE run_id=?`, newID), "the new run claimed both handed-over videos")
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='retrying'`))
	restarted.sigterm()
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// timeLimitedUnusableRun drives a real Run Now through the HTTP handler until
// the shortened time limit (the production WithTimeoutCause path) interrupts it
// mid-actions: V1 sent, V2's POST in flight with its guard kept, V3 untouched.
// It then deletes the run's config_version row so the run can no longer resume.
func timeLimitedUnusableRun(t *testing.T, dbPath string, yt *fakeYouTube) (srv *serverProcess, runID int64) {
	t.Helper()
	db := openReadDB(t, dbPath)
	yt.setPostHoldFor("V2", make(chan struct{})) // V2's response never arrives
	srv = startServer(t, dbPath, yt.srv.URL, childRunTimeLimitEnv+"=6s")
	srv.waitOverview()
	waitLog(t, srv, "scheduler started") // the producer gate is open
	code, body := srv.postJSON("/api/runs")
	require.Equal(t, 202, code, "%v", body)
	require.Equal(t, "started", body["admission"])
	runID = int64(body["run_id"].(float64))
	eventually(t, runDoneBudget, "the run to hit its time limit", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) == "interrupted"
	})
	require.Equal(t, "time_limit", scalar[string](t, db, `SELECT interrupt_cause FROM run WHERE id=?`, runID))
	require.Equal(t, 1, yt.postCount("V1"))
	require.Equal(t, 1, yt.postCount("V2"), "V2's POST was in flight when the limit hit")
	require.Zero(t, yt.postCount("V3"))

	writer, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	_, err = writer.Exec(`DELETE FROM config_version WHERE id=(SELECT config_version_id FROM run WHERE id=?)`, runID)
	require.NoError(t, err)

	code, body = srv.postJSON("/api/runs")
	require.Equal(t, 409, code, "%v", body)
	require.Equal(t, "resume_unavailable", body["code"])
	require.EqualValues(t, runID, body["run_id"])
	read := srv.getJSON("/api/runs/" + itoa(runID))
	require.Equal(t, true, read["discardable"])
	return srv, runID
}

// discardAndRunAgain discards the unusable run through srv, checks the handoff,
// then restarts on the same database and presses Run Now: a started run claims
// the handed-over videos and sends only what the guard does not block.
func discardAndRunAgain(t *testing.T, srv *serverProcess, dbPath string, yt *fakeYouTube, runID int64, restartFirst bool) {
	t.Helper()
	db := openReadDB(t, dbPath)
	if restartFirst {
		// U3: the process starts with the unusable row, and discard still works.
		srv.sigterm()
		srv = startServer(t, dbPath, yt.srv.URL, childRunTimeLimitEnv+"=6s")
		srv.waitOverview()
		waitLog(t, srv, "scheduler started")
		require.Contains(t, srv.out.String(), "startup recovery left unusable run")
	}
	requests := yt.requestCount()
	code, body := srv.postJSON("/api/runs/" + itoa(runID) + "/discard")
	require.Equal(t, 200, code, "%v", body)
	require.Equal(t, "discarded", body["outcome"])
	require.Equal(t, requests, yt.requestCount(), "AC18: the discard makes zero provider requests")
	require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID))

	// The deferred queue lists the unfinished videos.
	queue := srv.getJSON("/api/queue/deferred")
	listed := map[string]string{}
	for _, it := range queue["deferred"].([]any) {
		m := it.(map[string]any)
		listed[m["external_video_id"].(string)] = m["deferral_reason"].(string)
	}
	require.Equal(t, map[string]string{"V2": "discarded_by_user", "V3": "discarded_by_user"}, listed)
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM playlist_send_guard`), "V2's guard is untouched")
	srv.sigterm()

	// Restart (a new process on the same database) and press Run Now.
	yt.setPostHoldFor("", nil)
	restarted := startServer(t, dbPath, yt.srv.URL, childRunTimeLimitEnv+"=6s")
	restarted.waitOverview()
	waitLog(t, restarted, "scheduler started")
	code, body = restarted.postJSON("/api/runs")
	require.Equal(t, 202, code, "%v", body)
	require.Equal(t, "started", body["admission"], "a discarded run is not a resume candidate")
	newID := int64(body["run_id"].(float64))
	require.NotEqual(t, runID, newID)
	eventually(t, runDoneBudget, "the new run to finish", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, newID) != "running"
	})
	require.Equal(t, "completed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, newID))
	require.Equal(t, 2, scalar[int](t, db, `SELECT count(*) FROM run_video_outcome WHERE run_id=? AND video_id IN (SELECT id FROM video WHERE video_id IN ('V2','V3'))`, newID),
		"the new run claimed both handed-over videos")
	require.Equal(t, 1, yt.postCount("V2"), "the guarded target is never sent again")
	require.Equal(t, 1, yt.postCount("V3"), "the formerly unsent target is sent once")
	require.Equal(t, 1, yt.postCount("V1"), "the committed target is not re-sent")
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='retrying'`))
	restarted.sigterm()
}

// E1 discard journey (ARS-002 §6.4): a real time limit leaves an unusable run;
// Run Now refuses it, discard hands its videos over without a provider request,
// and after a restart the next run claims them.
func TestE1_DiscardJourney(t *testing.T) {
	for _, restartFirst := range []bool{false, true} {
		name := "discard_then_restart"
		if restartFirst {
			name = "restart_then_discard"
		}
		t.Run(name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
			seedBase(t, dbPath)
			yt := newFakeYouTube(t, "V1", "V2", "V3")
			srv, runID := timeLimitedUnusableRun(t, dbPath, yt)
			discardAndRunAgain(t, srv, dbPath, yt, runID, restartFirst)
		})
	}
}
