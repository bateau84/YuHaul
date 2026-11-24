//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
)

// P1 scale test (ARS-005 §11.9 test 38): the re-queue of stranded videos on a
// database shaped like production, through the real PreviewStranded and
// RequeueStranded over a real migrated SQLite file. The data is seeded in bulk
// SQL because only pre-024 history produces the version-0 evidence; nothing the
// confirm writes is seeded.
//
// Shape (the counts the report must show, asserted below):
//   - 100,000 stranded videos, discovered over 35 ended production runs;
//   - 30 of those runs are version 0 (the legacy veto joins every one of them),
//     with veto evidence on 1,000 further videos (blocked);
//   - 10,000 other queue rows on 10,000 more videos (8,000 deferred pending,
//     2,000 Errors entries), which stay already queued.
const (
	p1Stranded        = 100_000
	p1LegacyRuns      = 30
	p1GuardedRuns     = 5
	p1EvidenceVideos  = 1_000
	p1DeferredQuota   = 6_000
	p1DeferredOther   = 2_000
	p1ErrorsEntries   = 2_000
	p1OtherQueueRows  = p1DeferredQuota + p1DeferredOther + p1ErrorsEntries
	p1PhaseBTarget    = 2 * time.Second
	p1PreviewTarget   = 30 * time.Second
	p1PageTarget      = 200 * time.Millisecond
	p1BusyTimeout     = 5 * time.Second // store.sqliteDSN: _busy_timeout=5000
	p1PageRepetitions = 5
)

// seedP1 builds the shape above and returns the id of one pending deferred
// item on an unrelated video, which the concurrent writer discards.
func seedP1(t *testing.T, e *runNowEnv) (runIDs []int64, writerItem int64) {
	t.Helper()
	for i := 0; i < p1LegacyRuns; i++ {
		kind, status := "manual", "failed"
		if i%2 == 1 {
			kind, status = "scheduled", "completed"
		}
		// Phase "discovery": run 14's shape. A version-0 run that stopped in
		// action_execution would block every video it discovered (branch 3).
		runIDs = append(runIDs, e.sRun(t, kind, status, 0, "discovery"))
	}
	for i := 0; i < p1GuardedRuns; i++ {
		runIDs = append(runIDs, e.sRun(t, "manual", "failed", 1, ""))
	}
	list, err := json.Marshal(runIDs)
	require.NoError(t, err)

	tx, err := e.db().BeginTx(e.ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(e.ctx, query, args...)
		require.NoError(t, err, query)
	}
	series := func(n int) string {
		return fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < %d) `, n)
	}
	const runOf = `CAST(json_extract(?1, '$[' || (n.i % ?2) || ']') AS INTEGER)`

	// Stranded: every processing state that has no final outcome, spread over
	// all 35 runs. The 'evaluated' ones carry a not_matched decision.
	exec(series(p1Stranded)+`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state)
SELECT printf('p1-s-%06d', n.i), 'UC1', `+runOf+`,
       CASE n.i % 5 WHEN 0 THEN 'discovered' WHEN 1 THEN 'not_yet_evaluated' WHEN 2 THEN 'evaluating'
                    WHEN 3 THEN 'evaluated' ELSE 'acted' END
FROM n`, string(list), len(runIDs))
	exec(`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision)
SELECT v.id, v.discovered_in_run_id, ?1, 1, 'not_matched' FROM video v
WHERE v.video_id LIKE 'p1-s-%' AND v.processing_state = 'evaluated'`, e.routeID)

	// Legacy evidence on 1,000 videos of the 30 version-0 runs: a matched
	// decision under a run with no config snapshot (branch 1), and a failed
	// playlist insert never resolved elsewhere (branch 2).
	exec(series(p1EvidenceVideos)+`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state)
SELECT printf('p1-e-%06d', n.i), 'UC1', CAST(json_extract(?1, '$[' || (n.i % ?2) || ']') AS INTEGER), 'not_yet_evaluated'
FROM n`, string(list), p1LegacyRuns)
	exec(`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision)
SELECT v.id, v.discovered_in_run_id, ?1, 1,
       CASE WHEN CAST(substr(v.video_id, 6) AS INTEGER) % 2 = 0 THEN 'matched' ELSE 'not_matched' END
FROM video v WHERE v.video_id LIKE 'p1-e-%'`, e.routeID)
	exec(`INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type, target_playlist_id, outcome)
SELECT d.id, d.run_id, d.video_id, 'playlist_insert', 'PLlegacy', 'failed' FROM route_decision d
JOIN video v ON v.id = d.video_id WHERE v.video_id LIKE 'p1-e-%' AND d.decision = 'not_matched'`)

	// Other queue rows, each on its own unfinished video of a guarded run.
	guarded := runIDs[p1LegacyRuns]
	old := "2026-01-01T00:00:00Z"
	exec(series(p1OtherQueueRows)+`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state)
SELECT printf('p1-q-%06d', n.i), 'UC1', ?1, 'not_yet_evaluated' FROM n`, guarded)
	exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at)
SELECT v.id, v.discovered_in_run_id,
       CASE WHEN CAST(substr(v.video_id, 6) AS INTEGER) < ?2 THEN 'quota_exhausted' ELSE 'api_rate_limited' END,
       0, 'pending', ?1
FROM video v WHERE v.video_id LIKE 'p1-q-%' AND CAST(substr(v.video_id, 6) AS INTEGER) < ?3`,
		old, p1DeferredQuota, p1DeferredQuota+p1DeferredOther)
	exec(`INSERT INTO error_entry (video_id, run_id, error_category, error_message, status, created_at)
SELECT v.id, v.discovered_in_run_id, 'runtime', 'seed', 'active', ?1
FROM video v WHERE v.video_id LIKE 'p1-q-%' AND CAST(substr(v.video_id, 6) AS INTEGER) >= ?2`,
		old, p1DeferredQuota+p1DeferredOther)
	require.NoError(t, tx.Commit())

	require.NoError(t, e.db().QueryRowContext(e.ctx,
		`SELECT MIN(id) FROM deferred_item WHERE deferral_reason = 'api_rate_limited' AND status = 'pending'`).Scan(&writerItem))
	return runIDs, writerItem
}

type p1Stats struct{ min, median, max time.Duration }

func summarize(d []time.Duration) p1Stats {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return p1Stats{min: s[0], median: s[len(s)/2], max: s[len(s)-1]}
}

// deferredPage reads one page through the registered Deferred handler (what
// GET /api/deferred runs) and decodes the fields the checks need.
type deferredReply struct {
	Deferred []json.RawMessage `json:"deferred"`
	Total    int               `json:"total"`
	Queue    int               `json:"queue_total"`
	Page     struct {
		Number        int  `json:"number"`
		TotalPages    int  `json:"total_pages"`
		FirstPosition *int `json:"first_position"`
		LastPosition  *int `json:"last_position"`
	} `json:"page"`
}

func getDeferredPage(t *testing.T, h http.Handler, query url.Values) (deferredReply, time.Duration) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var reply deferredReply
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reply))
	return reply, elapsed
}

// TestStrandedP1Scale is ARS-005 §11.9 test 38. Targets: phase B within 2 s,
// the preview within 30 s, a Deferred page within 200 ms (first page,
// page=last, page=last&reason=quota_exhausted) and a concurrent writer that
// succeeds within busy_timeout while phase B holds the writer lock.
func TestStrandedP1Scale(t *testing.T) {
	if testing.Short() {
		t.Skip("100,000-video scale test skipped in -short mode")
	}
	e := newRunNowEnv(t)
	seedStart := time.Now()
	runIDs, writerItem := seedP1(t, e)
	seedTook := time.Since(seedStart)
	runsBefore := e.count(t, `SELECT COUNT(*) FROM run`)
	ledgerBefore := e.count(t, `SELECT COUNT(*) FROM quota_ledger`)
	require.Equal(t, p1LegacyRuns+p1GuardedRuns, len(runIDs))

	// Phase A alone: the scan in one read-only transaction.
	scanStart := time.Now()
	report, plan := e.scanStranded(t)
	phaseA := time.Since(scanStart)
	require.Equal(t, p1Stranded, report.Requeue, "stranded")
	require.Len(t, plan, p1Stranded)
	require.Equal(t, p1EvidenceVideos, report.Blocked.Videos, "blocked by version-0 evidence")
	require.Equal(t, map[string]int{BlockAmbiguousLegacyEvidence: p1EvidenceVideos}, report.Blocked.ByReason)
	require.Equal(t, p1DeferredQuota+p1DeferredOther+p1ErrorsEntries, report.AlreadyQueued.Videos, "already queued")
	require.Equal(t, p1ErrorsEntries, report.AlreadyQueued.InErrorsQueue)
	require.Zero(t, report.NotEnded.Videos)
	require.Zero(t, report.RemovedByUser)
	require.Zero(t, report.NoOriginRun)

	// The preview, as the handler calls it.
	previewStart := time.Now()
	preview, err := e.pipeline.PreviewStranded(e.ctx)
	previewTook := time.Since(previewStart)
	require.NoError(t, err)
	require.True(t, preview.Available, preview.UnavailableReason)
	require.Equal(t, p1Stranded, preview.Requeue)

	// Phase B: the real confirm. The seam times the writer lock (held from the
	// body's entry, the no-op write having taken it, until the commit
	// returns) and starts a real product writer, the Deferred Discard of an
	// unrelated item, on another connection while the lock is held.
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	type writerResult struct {
		err     error
		started time.Time
		waited  time.Duration
	}
	writerDone := make(chan writerResult, 1)
	var lockHeld, lockAcquired time.Time
	var lockedFor time.Duration
	e.pipeline.requeueTx = func(ctx context.Context, body func(*sql.Tx) error) error {
		var once sync.Once
		err := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
			once.Do(func() {
				lockAcquired = time.Now()
				go func() {
					r := writerResult{started: time.Now()}
					r.err = qs.DiscardDeferredItem(context.Background(), writerItem)
					r.waited = time.Since(r.started)
					writerDone <- r
				}()
			})
			return body(tx)
		})
		lockHeld = time.Now()
		lockedFor = lockHeld.Sub(lockAcquired)
		return err
	}
	confirmStart := time.Now()
	result, err := e.pipeline.RequeueStranded(e.ctx, &preview.Requeue)
	confirmTook := time.Since(confirmStart)
	require.NoError(t, err)
	var writer writerResult
	select {
	case writer = <-writerDone:
	case <-time.After(2 * p1BusyTimeout):
		t.Fatal("the concurrent writer did not finish")
	}

	require.Equal(t, p1Stranded, result.Requeued)
	require.Zero(t, result.SkippedConcurrently)
	require.Equal(t, p1Stranded, result.Report.Requeue)
	require.True(t, writer.started.Before(lockHeld), "the writer started while phase B held the lock")
	// It had to wait for the lock: it cannot have finished well before phase B
	// did. busy_timeout caps the wait when phase B is longer than it.
	require.GreaterOrEqual(t, writer.waited, min(lockedFor, p1BusyTimeout)/2, "the writer overlapped phase B (waited %s of %s)", writer.waited, lockedFor)
	if raceEnabled {
		// Under the detector phase B outlasts busy_timeout, so SQLITE_BUSY is
		// the correct answer for the writer; only the target run asserts it.
		t.Logf("P1 RESULT concurrent writer under -race: err=%v", writer.err)
	} else {
		require.NoError(t, writer.err, "a concurrent writer must succeed within busy_timeout")
		require.Less(t, writer.waited, p1BusyTimeout)
	}

	// What the confirm wrote, and nothing else.
	require.Equal(t, p1Stranded, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='requeued_by_user' AND status='pending' AND retry_after IS NULL`))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(DISTINCT created_at) FROM deferred_item WHERE deferral_reason='requeued_by_user'`))
	require.Equal(t, p1Stranded, e.count(t, `SELECT COUNT(DISTINCT video_id) FROM deferred_item WHERE deferral_reason='requeued_by_user'`))
	require.Equal(t, runsBefore, e.count(t, `SELECT COUNT(*) FROM run`), "no run row")
	require.Equal(t, ledgerBefore, e.count(t, `SELECT COUNT(*) FROM quota_ledger`), "no ledger row")
	require.Zero(t, e.totalPosts(), "no provider request")
	discarded := 0 // the writer's Discard took effect only if it succeeded
	if writer.err == nil {
		discarded = 1
	}
	require.Equal(t, p1DeferredQuota+p1DeferredOther-discarded, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason<>'requeued_by_user' AND status='pending'`))

	// A repeat finds nothing to re-queue.
	again, err := e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.Zero(t, again.Requeue)

	// Deferred pages over the queue as the confirm left it.
	pending := p1DeferredQuota + p1DeferredOther - discarded + p1Stranded
	handler := queue.NewService(qs, nil).DeferredHandler()
	pages := []struct {
		name  string
		query url.Values
		check func(deferredReply)
	}{
		{"first page", url.Values{}, func(r deferredReply) {
			require.Equal(t, pending, r.Total)
			require.Equal(t, pending, r.Queue)
			require.Equal(t, 1, r.Page.Number)
			require.Len(t, r.Deferred, 50)
			require.Equal(t, 1, *r.Page.FirstPosition)
		}},
		{"page=last", url.Values{"page": {"last"}}, func(r deferredReply) {
			require.Equal(t, pending, r.Total)
			require.Equal(t, r.Page.TotalPages, r.Page.Number)
			require.Equal(t, pending, *r.Page.LastPosition)
			require.NotEmpty(t, r.Deferred)
		}},
		{"page=last&reason=quota_exhausted", url.Values{"page": {"last"}, "reason": {"quota_exhausted"}}, func(r deferredReply) {
			require.Equal(t, p1DeferredQuota, r.Total)
			require.Equal(t, pending, r.Queue)
			require.Equal(t, p1DeferredQuota, *r.Page.LastPosition)
			require.NotEmpty(t, r.Deferred)
		}},
	}
	pageStats := map[string]p1Stats{}
	for _, p := range pages {
		var took []time.Duration
		for i := 0; i < p1PageRepetitions; i++ {
			reply, d := getDeferredPage(t, handler, p.query)
			p.check(reply)
			took = append(took, d)
		}
		pageStats[p.name] = summarize(took)
	}

	var size int64
	var seq int
	var schema, file string
	require.NoError(t, e.db().QueryRowContext(e.ctx, `PRAGMA database_list`).Scan(&seq, &schema, &file))
	if info, err := os.Stat(file); err == nil {
		size = info.Size()
	}
	t.Logf("P1 RESULT race=%v stranded=%d legacy_runs=%d evidence_videos=%d other_queue_rows=%d db_bytes=%d seed=%s",
		raceEnabled, p1Stranded, p1LegacyRuns, p1EvidenceVideos, p1OtherQueueRows, size, seedTook.Round(time.Millisecond))
	t.Logf("P1 RESULT phase_A_scan=%s preview=%s confirm_total=%s phase_B_writer_lock=%s concurrent_writer_waited=%s",
		phaseA.Round(time.Millisecond), previewTook.Round(time.Millisecond), confirmTook.Round(time.Millisecond),
		lockedFor.Round(time.Millisecond), writer.waited.Round(time.Millisecond))
	for _, p := range pages {
		s := pageStats[p.name]
		t.Logf("P1 RESULT deferred %-36s min=%s median=%s max=%s (n=%d)", p.name,
			s.min.Round(time.Microsecond), s.median.Round(time.Microsecond), s.max.Round(time.Microsecond), p1PageRepetitions)
	}

	if raceEnabled {
		t.Log("P1 targets not asserted under -race: the detector's slowdown makes them meaningless; counts and behavior were asserted")
		return
	}
	require.LessOrEqual(t, lockedFor, p1PhaseBTarget, "phase B (writer lock held)")
	require.LessOrEqual(t, previewTook, p1PreviewTarget, "preview")
	for _, p := range pages {
		require.LessOrEqual(t, pageStats[p.name].max, p1PageTarget, "deferred %s", p.name)
	}
}
