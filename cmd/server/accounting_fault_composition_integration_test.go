//go:build integration

package main

// Accounting-fault composition test (run-initiation ARS-005 §6.1.3 writer W3
// and evidence S6; OC-001 invariants). The real server composition runs as a
// child process against a disposable SQLite file and a loopback fake YouTube
// endpoint. A SQLite trigger makes the quota ledger refuse the next
// videos_list attempt row, so the run's first accounting fault happens in the
// real metadata lifecycle refresh of a video it added earlier.

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The first quota-accounting fault of a run stops every later run-owned
// provider request, including the discovery requests a run makes after its
// metadata lifecycle. The run ends failed, the stop is stored, and nothing
// reads the injected fault text.
func TestS6_LifecycleAccountingFaultSendsNoFurtherRequest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seedBase(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	db := openReadDB(t, dbPath)
	writer, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })

	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	waitLog(t, srv, "scheduler started") // the producer gate is open

	// Run 1 genuinely adds V1, so its metadata is a routed refresh candidate.
	code, body := srv.postJSON("/api/runs")
	require.Equal(t, 202, code, "%v", body)
	first := int64(body["run_id"].(float64))
	eventually(t, runDoneBudget, "the first run to complete", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, first) == "completed"
	})
	require.Equal(t, 1, yt.postCount("V1"))
	require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM executed_action WHERE outcome='succeeded'`))

	// V1's metadata is near expiry, so run 2's lifecycle refreshes it. The
	// ledger refuses the attempt row of that refresh. V2 gives the run work
	// that would otherwise send discovery and metadata requests.
	res, err := writer.Exec(`UPDATE transient_metadata SET expires_at=? WHERE video_id='V1'`, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "V1's metadata is stored and routed")
	_, err = writer.Exec(`CREATE TRIGGER refuse_videos_list_attempt BEFORE INSERT ON quota_ledger WHEN NEW.operation_type='videos_list'
		BEGIN SELECT RAISE(ABORT, 'injected ledger fault'); END`)
	require.NoError(t, err)
	yt.setVideos("V1", "V2")
	before := yt.requestCount()

	code, body = srv.postJSON("/api/runs")
	require.Equal(t, 202, code, "%v", body)
	second := int64(body["run_id"].(float64))
	eventually(t, runDoneBudget, "the second run to end", func() bool {
		s := scalar[string](t, db, `SELECT status FROM run WHERE id=?`, second)
		return s == "failed" || s == "completed"
	})

	require.Equal(t, []string{http.MethodGet + " /videos"}, yt.requestsSince(before),
		"the lifecycle refresh is the run's only provider request; none follows its accounting fault")
	require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, second))
	require.Equal(t, "accounting_or_integrity", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, second))
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM quota_ledger WHERE run_id=?`, second), "the refused attempt row was never written")
	require.Zero(t, yt.postCount("V2"))
	require.Contains(t, scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, second), "operation=account_quota")
	require.NotContains(t, srv.out.String(), "injected ledger fault", "no store text reaches the log")
	require.NotContains(t, scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, second), "injected")
	srv.sigterm()
}
