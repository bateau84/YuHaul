//go:build integration

package main

// Mark-a-legacy-run-as-ended composition test (run-initiation ARS-004 §7.3 U1
// and §7.5 E1): the real server process, a migrated SQLite file and a counting
// fake YouTube endpoint.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const endedReason = "diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user"

func TestEndLegacyRunOverHTTPSurvivesRestartsAndNeverBlocks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	recoverableID, _ := seedInterruptedRun(t, dbPath) // a version-1 interrupted row startup resumes
	yt := newFakeYouTube(t, "V1")
	db := openReadDB(t, dbPath)
	// L14: run-14-shaped. L2: a second stuck row, scheduled and interrupted.
	for _, row := range []struct {
		id        int64
		runType   string
		status    string
		initiator any
	}{{14, "manual", "running", nil}, {2, "scheduled", "interrupted", nil}} {
		_, err := db.Exec(`INSERT INTO run (id, started_at, run_type, status, mutation_safety_version, action_authority_version, current_phase, videos_discovered, videos_not_yet_evaluated, initiated_by, interrupt_reason)
			VALUES (?, '2026-10-20T06:00:00Z', ?, ?, 0, 0, 'discovery', 19615, 19615, ?, 'diag:v1 phase=discovery operation=discover code=timeout')`,
			row.id, row.runType, row.status, row.initiator)
		require.NoError(t, err)
	}

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	eventually(t, runDoneBudget, "the recovered version-1 run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, recoverableID) == "completed"
	})
	eventually(t, 10*time.Second, "the scheduler to start", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })

	read := func(s *serverProcess, id string) map[string]any {
		status, body := s.do(http.MethodGet, "/api/runs/"+id)
		require.Equal(t, http.StatusOK, status, body)
		var run map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &run))
		return run
	}
	l14 := read(srv, "14")
	require.Equal(t, "running", l14["status"])
	require.Equal(t, true, l14["endable"])
	require.Equal(t, false, l14["executing"], "saved as running, but nothing executes it")
	require.Equal(t, false, read(srv, strconv.FormatInt(recoverableID, 10))["endable"], "a version-1 run is never endable")

	// E1: the end answers 200 and makes no provider request.
	requests := yt.requestCount()
	status, body := srv.do(http.MethodPost, "/api/runs/14/end")
	require.Equal(t, http.StatusOK, status, body)
	require.JSONEq(t, `{"run_id":14,"status":"failed","outcome":"ended"}`, body)
	status, body = srv.do(http.MethodPost, "/api/runs/2/end")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, requests, yt.requestCount(), "ending makes no YouTube request")
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM quota_ledger WHERE run_id IN (14, 2)`), "ending writes no ledger row")

	status, body = srv.do(http.MethodPost, "/api/runs/14/end")
	require.Equal(t, http.StatusConflict, status, body)
	require.Contains(t, body, `"code":"run_ended"`)
	require.Contains(t, body, `"status":"failed"`)

	l14 = read(srv, "14")
	require.Equal(t, "failed", l14["status"])
	require.Equal(t, endedReason, l14["interrupt_reason"])
	require.Equal(t, false, l14["endable"])
	require.Equal(t, false, l14["executing"])
	require.Nil(t, l14["completed_at"])
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM run WHERE id=14 AND completed_at IS NULL`))
	srv.sigterm()

	assertStillEnded := func(label string, p *serverProcess) {
		t.Helper()
		for _, id := range []int64{14, 2} {
			require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, id), label)
			require.Equal(t, endedReason, scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, id), label)
		}
		require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM run WHERE id=14 AND completed_at IS NULL`), label+": completed_at stays NULL")
		out := p.out.String()
		require.NotContains(t, out, "startup recovery blocked", label)
		require.NotContains(t, out, "left unusable run", label)
		require.Contains(t, out, "scheduler started", label)
	}

	// Restart after SIGTERM, then after SIGKILL: the process starts, serves and
	// the gate opens; the ended runs are untouched and never named.
	second := startServer(t, dbPath, yt.srv.URL)
	second.waitOverview()
	eventually(t, 10*time.Second, "the scheduler to start", func() bool { return strings.Contains(second.out.String(), "scheduler started") })
	assertStillEnded("after SIGTERM", second)
	require.NoError(t, second.cmd.Process.Signal(syscall.SIGKILL))
	<-second.exited

	third := startServer(t, dbPath, yt.srv.URL)
	third.waitOverview()
	eventually(t, 10*time.Second, "the scheduler to start", func() bool { return strings.Contains(third.out.String(), "scheduler started") })
	assertStillEnded("after SIGKILL", third)

	// Run Now starts a new run and never selects an ended one.
	status, body = third.do(http.MethodPost, "/api/runs")
	require.Equal(t, http.StatusAccepted, status, body)
	var accepted struct {
		RunID     int64  `json:"run_id"`
		Admission string `json:"admission"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &accepted))
	require.Equal(t, "started", accepted.Admission)
	require.NotContains(t, []int64{14, 2}, accepted.RunID)
	eventually(t, runDoneBudget, "the new run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, accepted.RunID) == "completed"
	})
	assertStillEnded("after Run Now", third)
	third.sigterm()
}
