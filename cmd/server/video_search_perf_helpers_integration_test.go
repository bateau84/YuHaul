//go:build integration

package main

// Helpers and the G-O gate for the ARS-001 §9.2 / §10.11 timing tests: the
// first-start backfill, the aging cohort, server-log parsing for the per-stage
// P2 split and the sweep numbers, and TestVideoSearchPerfOptimizeCost (G-O).
//
//	TMPDIR=<disk dir> YUHAUL_PERF=1 go test -tags integration -run TestVideoSearchPerfOptimizeCost -v -timeout 4h ./cmd/server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	modernsqlite "modernc.org/sqlite"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/store"
)

// perfBackfill is the first-start backfill: store.Open and then
// metadata.PrepareSearchIndex on a database whose index is still empty, the
// same two calls cmd/server makes. now is the fixture's controlled clock.
func perfBackfill(t *testing.T, path string, now time.Time) (metadata.PrepareStats, time.Duration) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	began := time.Now()
	prep, err := metadata.PrepareSearchIndex(ctx, db, now)
	took := time.Since(began)
	require.NoError(t, err)
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for _, ext := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + ext)
	}
	return prep, took
}

// ---------------------------------------------------------------------------
// Cohort

// agingCohort is a set of held, indexed titles whose expiry the test controls.
type agingCohort struct {
	ids        []string
	lastExpiry time.Time
}

func cohortPlaceholders(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

// countIndexed returns how many of ids have a document mapping and how many
// of those mappings have an FTS row, both read in chunks outside any write
// transaction.
func countIndexed(t *testing.T, db *sql.DB, ids []string) (docs, fts int) {
	t.Helper()
	for lo := 0; lo < len(ids); lo += 500 {
		chunk := ids[lo:min(lo+500, len(ids))]
		ph, args := cohortPlaceholders(chunk)
		var n, m int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM title_search_doc WHERE video_id IN `+ph, args...).Scan(&n))
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM title_search WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id IN `+ph+`)`, args...).Scan(&m))
		docs += n
		fts += m
	}
	return docs, fts
}

func (c *agingCohort) assertIndexed(t *testing.T, db *sql.DB, when string) {
	t.Helper()
	docs, fts := countIndexed(t, db, c.ids)
	t.Logf("cohort %s: %d of %d have a document mapping, %d an FTS row", when, docs, len(c.ids), fts)
	require.Equal(t, len(c.ids), docs, "every cohort title must have a document mapping %s", when)
	require.Equal(t, len(c.ids), fts, "every cohort title must have an FTS row %s", when)
}

// addAgingCohort adds the separate aging cohort of ARS-001 §9.2 to a P3 copy:
// perfCohort held titles with no planted token and no video row, expiring in
// the real near future (150 s from now, spread over the following minute),
// then reconciles so that each is indexed while held. The returned cohort's
// lastExpiry is when the last one is past its expiry on the real clock.
func addAgingCohort(t *testing.T, dbPath string, tr *perfTruth) *agingCohort {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	rng := rand.New(rand.NewPCG(perfSeedValue, 4))
	now := time.Now().UTC().Truncate(time.Second)
	base := now.Add(150 * time.Second)
	c := &agingCohort{lastExpiry: base.Add(59 * time.Second)}

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO transient_metadata (video_id, title, channel_name, fetched_at, expires_at, expired_at, is_active) VALUES (?,?,?,?,?,NULL,1)`)
	require.NoError(t, err)
	for i := 0; i < perfCohort; i++ {
		id := fmt.Sprintf("co%09d", i)
		nw := 3 + rng.IntN(4)
		words := make([]string, nw)
		for j := range words {
			words[j] = tr.vocab[rng.IntN(len(tr.vocab))]
		}
		fetched := now.Add(-time.Duration(1+rng.IntN(29*24*60)) * time.Minute)
		_, err := stmt.Exec(id, strings.Join(words, " "), "Cohort "+strconv.Itoa(i%50), fetched, base.Add(time.Duration(i%60)*time.Second))
		require.NoError(t, err)
		c.ids = append(c.ids, id)
	}
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Commit())

	rec, err := metadata.ReconcileSearchIndex(ctx, db, now)
	require.NoError(t, err)
	require.Equal(t, perfCohort, rec.Added, "the reconcile must add exactly the cohort")
	require.Zero(t, rec.Purged+rec.OrphansRemoved)
	c.assertIndexed(t, db, "after the reconcile, before the server starts")
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for _, ext := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + ext)
	}
	return c
}

// observeSweep checks the run's sweep against ARS-001 §9.2 and returns its
// numbers for the report. It reads the database and the server log after the
// run: the sweep itself runs inside the server process, so the boundary
// between ExpireStale and the reconcile is read from the log (the reconcile
// reports 0 purged and 0 orphans, so the cohort's entries were removed by the
// expiry's own triggers, not by the reconcile).
func (c *agingCohort) observeSweep(t *testing.T, db *sql.DB, out, name string) string {
	t.Helper()
	docs, fts := countIndexed(t, db, c.ids)
	expiredRows := 0
	for lo := 0; lo < len(c.ids); lo += 500 {
		ph, args := cohortPlaceholders(c.ids[lo:min(lo+500, len(c.ids))])
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM transient_metadata WHERE is_active = 0 AND expired_at IS NOT NULL AND title IS NULL AND video_id IN `+ph, args...).Scan(&n))
		expiredRows += n
	}
	exp := perfLogLine(t, out, "metadata lifecycle expired stale records")
	rec := perfLogLine(t, out, "metadata lifecycle reconciled search index")
	opt := perfLogLine(t, out, "metadata lifecycle optimized search index")
	longest, total := time.Duration(asFloat(opt["longest"])), time.Duration(asFloat(opt["total"]))
	steps, complete := asFloat(opt["steps"]), opt["complete"] == true

	line := fmt.Sprintf("%s sweep: ExpireStale expired %v; source rows expired %d of %d; cohort docs left %d, FTS rows left %d; "+
		"reconcile orphans_removed %v purged %v added %v; optimize complete=%v steps=%v longest step %s total %s",
		name, exp["count"], expiredRows, len(c.ids), docs, fts, rec["orphans_removed"], rec["purged"], rec["added"],
		complete, steps, longest.Round(time.Millisecond), total.Round(time.Millisecond))
	t.Log(line)

	assertEq := func(want, got any, what string) {
		if want != got {
			t.Errorf("P3 %s: %s: got %v, want %v", name, what, got, want)
		}
	}
	assertEq(float64(perfCohort), asFloat(exp["count"]), "ExpireStale count")
	assertEq(perfCohort, expiredRows, "cohort source rows expired")
	assertEq(0, docs, "cohort document mappings left")
	assertEq(0, fts, "cohort FTS rows left")
	assertEq(float64(0), asFloat(rec["purged"])+asFloat(rec["orphans_removed"]), "entries the reconcile had to remove")
	if !complete {
		t.Errorf("P3(f) %s: optimize did not report Complete", name)
	}
	if longest > time.Second {
		t.Errorf("P3(e)/(f) %s: longest optimize step %s exceeds 1 s", name, longest)
	}
	if total > 60*time.Second {
		t.Errorf("P3(f) %s: optimize total %s exceeds 60 s", name, total)
	}
	return line
}

func asFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

// ---------------------------------------------------------------------------
// Server log

func perfLogLines(out string) []map[string]any {
	var lines []map[string]any
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "{") {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			lines = append(lines, m)
		}
	}
	return lines
}

// perfLogLine returns the first JSON log record with the given message.
func perfLogLine(t *testing.T, out, msg string) map[string]any {
	t.Helper()
	for _, m := range perfLogLines(out) {
		if m["msg"] == msg {
			return m
		}
	}
	t.Fatalf("no %q record in the server log:\n%.4000s", msg, out)
	return nil
}

// perfBusyHits returns up to three log lines that carry SQLite result code 5
// (SQLITE_BUSY, SQLITE_BUSY_SNAPSHOT) or 6 (SQLITE_LOCKED).
func perfBusyHits(out string) []string {
	var hits []string
	for _, l := range strings.Split(out, "\n") {
		low := strings.ToLower(l)
		if strings.Contains(low, "sqlite_busy") || strings.Contains(low, "sqlite_locked") ||
			strings.Contains(low, "database is locked") || strings.Contains(low, "database table is locked") ||
			strings.Contains(low, "(5)") && strings.Contains(low, "sqlite") || strings.Contains(low, "(6)") && strings.Contains(low, "sqlite") {
			hits = append(hits, l)
			if len(hits) == 3 {
				break
			}
		}
	}
	return hits
}

var perfStageNames = []string{"begin", "candidates", "recheck", "sort", "project", "encode"}

// perfStageReport zips the server's per-search stage records with the labels
// of the requests sent, and reports p50/p95/max of each stage for the
// first-page set and the page>=100 set (ARS-001 §9.2). It also counts the name
// searches that took the scan path, which must be 0.
func perfStageReport(t *testing.T, srv *serverProcess, labels []string) string {
	t.Helper()
	var recs []map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for {
		recs = recs[:0]
		for _, m := range perfLogLines(srv.out.String()) {
			if m["msg"] == "video search" {
				if _, ok := m["stages"]; ok && m["kind"] == "results" {
					recs = append(recs, m)
				}
			}
		}
		if len(recs) >= len(labels) || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.Equal(t, len(labels), len(recs), "one stage record per name search")

	stage := map[string]map[string][]time.Duration{}
	paths := map[string]int{}
	var total = map[string][]time.Duration{}
	for i, m := range recs {
		paths[fmt.Sprint(m["path"])]++
		label := labels[i]
		if label != "first page" && label != "page>=100" {
			continue
		}
		stages := m["stages"].(map[string]any)
		if stage[label] == nil {
			stage[label] = map[string][]time.Duration{}
		}
		var sum time.Duration
		for _, s := range perfStageNames {
			d := time.Duration(asFloat(stages[s]))
			stage[label][s] = append(stage[label][s], d)
			sum += d
		}
		total[label] = append(total[label], sum)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "P2 per-stage split from the server log (ns in the log; server-side, before the loopback hop). Paths taken: %v (scan must be 0)\n\n", paths)
	b.WriteString("| set | stage | n | p50 | p95 | max |\n|---|---|---|---|---|---|\n")
	for _, label := range []string{"first page", "page>=100"} {
		for _, s := range perfStageNames {
			n, p50, p95, mx := summarize(stage[label][s])
			fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s |\n", label, s, n, ms(p50), ms(p95), ms(mx))
		}
		n, p50, p95, mx := summarize(total[label])
		fmt.Fprintf(&b, "| %s | **sum of stages** | %d | %s | %s | %s |\n", label, n, ms(p50), ms(p95), ms(mx))
	}
	require.Zero(t, paths["scan"], "no planted-token name search may take the scan path")
	return b.String()
}

// ---------------------------------------------------------------------------
// Write-transaction observer for G-O

type txRec struct {
	dur   time.Duration
	merge bool
}

// txObserver wraps the real SQLite driver at BEGIN, COMMIT and the FTS5 'merge'
// command only; SQLite does all the work. It times every write transaction
// from the end of its BEGIN IMMEDIATE (the lock is held) to the end of its
// COMMIT, and samples the WAL size after each commit.
type txObserver struct {
	dsn  string
	path string

	mu      sync.Mutex
	txs     []txRec
	walPeak int64
}

func (o *txObserver) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.txs, o.walPeak = nil, 0
}

func (o *txObserver) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&modernsqlite.Driver{}).Open(o.dsn)
	if err != nil {
		return nil, err
	}
	return &txConn{Conn: conn, owner: o}, nil
}

func (*txObserver) Driver() driver.Driver { return &modernsqlite.Driver{} }

type txConn struct {
	driver.Conn
	owner *txObserver
	began time.Time
	merge bool
}

func (c *txConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

func (c *txConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}

func (c *txConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *txConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	res, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
	if err != nil {
		return res, err
	}
	switch {
	case strings.HasPrefix(q, "BEGIN"):
		c.began, c.merge = time.Now(), false
	case strings.Contains(q, "'merge'"):
		c.merge = true
	case q == "COMMIT" && !c.began.IsZero():
		rec := txRec{dur: time.Since(c.began), merge: c.merge}
		c.began = time.Time{}
		var wal int64
		if fi, err := os.Stat(c.owner.path + "-wal"); err == nil {
			wal = fi.Size()
		}
		c.owner.mu.Lock()
		c.owner.txs = append(c.owner.txs, rec)
		c.owner.walPeak = max(c.owner.walPeak, wal)
		c.owner.mu.Unlock()
	}
	return res, nil
}

func (o *txObserver) snapshot() (txs []txRec, walPeak int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]txRec(nil), o.txs...), o.walPeak
}

func durations(txs []txRec, merge bool) []time.Duration {
	var d []time.Duration
	for _, x := range txs {
		if x.merge == merge {
			d = append(d, x.dur)
		}
	}
	return d
}

// ---------------------------------------------------------------------------
// G-O

// TestVideoSearchPerfOptimizeCost is ARS-001 §10.11 G-O and the standalone
// measurement of P3(f): on disk, for each merge-page setting, a fresh
// 150,000-entry backfill, then the controlled-clock expiry of the 5,000-row
// cohort, then OptimizeSearchIndex. Pass: Complete, every step <= 1 s, total
// <= 60 s.
func TestVideoSearchPerfOptimizeCost(t *testing.T) {
	if os.Getenv("YUHAUL_PERF") != "1" {
		t.Skip("set YUHAUL_PERF=1 to run the ARS-001 timing tests")
	}
	ctx := context.Background()
	dir := perfScratch(t)
	pre := filepath.Join(dir, "seed.db")
	truth := seedPerfDB(t, pre)
	t.Log(machineInfo())
	require.GreaterOrEqual(t, len(truth.plain), perfCohort)
	cohortIDs := truth.plain[:perfCohort]
	t0 := truth.t0
	t1 := t0.Add(2 * time.Hour)
	defaultPages := metadata.OptimizeMergePages
	t.Cleanup(func() { metadata.OptimizeMergePages = defaultPages })

	var report []string
	for _, pages := range []int{500, 100} {
		t.Run(fmt.Sprintf("pages_%d", pages), func(t *testing.T) {
			db0 := filepath.Join(dir, fmt.Sprintf("go_%d.db", pages))
			copyFile(t, pre, db0)

			// The cohort is 5,000 of the 150,000 base rows: held at T0, past
			// expiry before T1 (fetched_at <= T0 < expires_at < T1). Set before
			// the index exists, so no entry is purged by this edit.
			{
				db, err := store.Open(ctx, db0)
				require.NoError(t, err)
				tx, err := db.BeginTx(ctx, nil)
				require.NoError(t, err)
				stmt, err := tx.PrepareContext(ctx, `UPDATE transient_metadata SET expires_at = ? WHERE video_id = ?`)
				require.NoError(t, err)
				for i, id := range cohortIDs {
					_, err := stmt.Exec(t0.Add(time.Duration(1+i%58)*time.Minute), id)
					require.NoError(t, err)
				}
				require.NoError(t, stmt.Close())
				require.NoError(t, tx.Commit())
				require.NoError(t, db.Close())
			}

			perfWaitQuiet(t, fmt.Sprintf("G-O pages=%d backfill", pages))
			prep, backfill := perfBackfill(t, db0, t0)
			require.Equal(t, perfKnownHeld, prep.Reconcile.Added)
			t.Logf("backfill: %d entries in %s", prep.Reconcile.Added, backfill.Round(time.Millisecond))

			obs := &txObserver{path: db0, dsn: db0 + "?_fk=1&_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1"}
			db := sql.OpenDB(obs)
			defer db.Close()
			docs0, fts0 := countIndexed(t, db, cohortIDs)
			require.Equal(t, perfCohort, docs0, "every cohort title is indexed while held")
			require.Equal(t, perfCohort, fts0)
			var totalDocs0 int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM title_search_doc`).Scan(&totalDocs0))

			// Quiet check, then the measured part. No reopen, update or
			// reconcile happens between the membership check above and ExpireStale.
			perfWaitQuiet(t, fmt.Sprintf("G-O pages=%d expire+optimize", pages))
			defer perfLoadNow(t, fmt.Sprintf("G-O pages=%d", pages))
			mgr := metadata.NewManager(metadata.NewStore(db), nil, nil, metadata.WithNow(func() time.Time { return t1 }))
			obs.reset()
			ex0 := time.Now()
			expired, err := mgr.ExpireStale(ctx)
			exDur := time.Since(ex0)
			require.NoError(t, err)
			require.Equal(t, perfCohort, expired, "ExpireStale must expire exactly the cohort")
			expTxs, _ := obs.snapshot()
			docs1, fts1 := countIndexed(t, db, cohortIDs)
			var gone int
			for lo := 0; lo < len(cohortIDs); lo += 500 {
				ph, args := cohortPlaceholders(cohortIDs[lo:min(lo+500, len(cohortIDs))])
				var n int
				require.NoError(t, db.QueryRow(`SELECT count(*) FROM transient_metadata WHERE is_active = 0 AND expired_at IS NOT NULL AND title IS NULL AND video_id IN `+ph, args...).Scan(&n))
				gone += n
			}
			var totalDocs1 int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM title_search_doc`).Scan(&totalDocs1))
			require.Equal(t, perfCohort, gone, "the cohort's source titles are cleared")
			require.Zero(t, docs1, "the cohort's document mappings are gone after ExpireStale")
			require.Zero(t, fts1, "the cohort's FTS rows are gone after ExpireStale")
			require.Equal(t, totalDocs0-perfCohort, totalDocs1, "ExpireStale purged only the cohort")
			expDurs := durations(expTxs, false)
			_, _, _, expMax := summarize(expDurs)

			metadata.OptimizeMergePages = pages
			obs.reset()
			stats, err := metadata.OptimizeSearchIndex(ctx, db)
			require.NoError(t, err)
			txs, walPeak := obs.snapshot()
			steps := durations(txs, true)
			n, p50, p95, mx := summarize(steps)
			fi, _ := os.Stat(db0)

			line := fmt.Sprintf("G-O OptimizeMergePages=%d: backfill %d entries in %s; ExpireStale %d rows in %s over %d write transactions (longest %s); "+
				"cohort mappings/FTS rows before %d/%d, after ExpireStale %d/%d (before reconcile and optimize); source expiry count %d; "+
				"optimize Complete=%v steps=%d (observed %d) step p50 %s p95 %s max %s (OptimizeStats.Longest %s) total %s; WAL peak %d MB; database %d MB",
				pages, prep.Reconcile.Added, backfill.Round(time.Millisecond), expired, exDur.Round(time.Millisecond), len(expDurs), expMax.Round(time.Millisecond),
				docs0, fts0, docs1, fts1, gone,
				stats.Complete, stats.Steps, n, p50.Round(time.Millisecond), p95.Round(time.Millisecond), mx.Round(time.Millisecond),
				stats.Longest.Round(time.Millisecond), stats.Total.Round(time.Millisecond), walPeak>>20, fi.Size()>>20)
			t.Log(line)
			report = append(report, line)

			if !stats.Complete {
				t.Errorf("G-O pages=%d: optimize did not report Complete", pages)
			}
			if mx > time.Second || stats.Longest > time.Second {
				t.Errorf("G-O pages=%d: a step took %s (limit 1 s)", pages, max(mx, stats.Longest))
			}
			if stats.Total > 60*time.Second {
				t.Errorf("G-O pages=%d: total %s exceeds 60 s", pages, stats.Total)
			}
			if expMax > time.Second {
				t.Errorf("G-O pages=%d: an expiry write transaction took %s (limit 1 s)", pages, expMax)
			}
			sort.Strings(report)
		})
	}
	if p := os.Getenv("YUHAUL_PERF_REPORT_GO"); p != "" {
		require.NoError(t, os.WriteFile(p, []byte(machineInfo()+"\n"+strings.Join(report, "\n")+"\n"+strings.Join(perfLoads, "\n")+"\n"), 0o644))
	}
}
