package metadata_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/titlekey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

var idxNow = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func openIndexDB(t *testing.T) (*metadata.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idx.db")
	db, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return metadata.NewStore(db), path
}

func setBatch(t *testing.T, n int) {
	t.Helper()
	old := metadata.IndexWriteBatch
	metadata.IndexWriteBatch = n
	t.Cleanup(func() { metadata.IndexWriteBatch = old })
}

// put stores a held-by-default row. It writes through Store.Upsert, which
// indexes at the current time. The row is dated around idxNow (March 2026),
// which is already past its expiry, so Upsert adds no entry and the row starts
// without one.
func put(t *testing.T, ms *metadata.Store, id, title string, mod func(*metadata.TransientMetadata)) {
	t.Helper()
	m := metadata.TransientMetadata{VideoID: id, FetchedAt: idxNow.Add(-time.Hour), ExpiresAt: idxNow.Add(10 * time.Hour), IsActive: true}
	if title != "" {
		m.Title = &title
	}
	if mod != nil {
		mod(&m)
	}
	require.NoError(t, ms.Upsert(context.Background(), m))
}

func indexNow(t *testing.T, db *sql.DB, id string, now time.Time) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, metadata.IndexTitleTx(context.Background(), tx, titlekey.NewFolder(), id, now))
	require.NoError(t, tx.Commit())
}

func entries(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT d.video_id, s.k FROM title_search_doc d JOIN title_search s ON s.rowid = d.doc_id`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, k string
		require.NoError(t, rows.Scan(&id, &k))
		out[id] = k
	}
	require.NoError(t, rows.Err())
	return out
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
	return n
}

// checkInvariants asserts I1 and I3 at now: every document has an FTS row, a
// source row that is Indexable, and k = Key(title); every Indexable row has
// exactly one entry.
func checkInvariants(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	got := entries(t, db)
	require.Equal(t, count(t, db, "title_search_doc"), len(got), "document without FTS row")
	require.Equal(t, count(t, db, "title_search"), len(got), "FTS row without document")
	rows, err := db.Query(`SELECT video_id, title, is_active, expired_at, fetched_at, expires_at FROM transient_metadata`)
	require.NoError(t, err)
	defer rows.Close()
	want := map[string]string{}
	for rows.Next() {
		var row metadata.LocalRow
		var title sql.NullString
		require.NoError(t, rows.Scan(&row.VideoID, &title, &row.IsActive, &row.ExpiredAt, &row.FetchedAt, &row.ExpiresAt))
		if title.Valid {
			row.Title = &title.String
		}
		if metadata.Indexable(row, now) {
			want[row.VideoID] = titlekey.Key(*row.Title)
		}
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, want, got)
}

func TestIndexableHeldOrBecomesHeldAtFetch(t *testing.T) {
	title, blank := "Some title", "  "
	row := func(mod func(*metadata.LocalRow)) metadata.LocalRow {
		r := metadata.LocalRow{VideoID: "v", Title: &title, IsActive: true,
			FetchedAt: idxNow.Add(-time.Hour), ExpiresAt: idxNow.Add(time.Hour)}
		if mod != nil {
			mod(&r)
		}
		return r
	}
	for name, tc := range map[string]struct {
		row  metadata.LocalRow
		want bool
	}{
		"held": {row(nil), true},
		"future fetched": {row(func(r *metadata.LocalRow) {
			r.FetchedAt = idxNow.Add(time.Hour)
			r.ExpiresAt = idxNow.Add(2 * time.Hour)
		}), true},
		"future fetched no ttl": {row(func(r *metadata.LocalRow) { r.FetchedAt = idxNow.Add(time.Hour); r.ExpiresAt = idxNow.Add(time.Hour) }), false},
		"past expiry":           {row(func(r *metadata.LocalRow) { r.ExpiresAt = idxNow }), false},
		"inactive":              {row(func(r *metadata.LocalRow) { r.IsActive = false }), false},
		"expired marker":        {row(func(r *metadata.LocalRow) { r.ExpiredAt = idxNow.Add(-time.Minute) }), false},
		"blank title":           {row(func(r *metadata.LocalRow) { r.Title = &blank }), false},
		"nil title":             {row(func(r *metadata.LocalRow) { r.Title = nil }), false},
		"bad fetched_at":        {row(func(r *metadata.LocalRow) { r.FetchedAt = "nope" }), false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, metadata.Indexable(tc.row, idxNow))
		})
	}
}

func TestIndexTitleTxReadsStoredRow(t *testing.T) {
	ms, _ := openIndexDB(t)
	db := ms.DB()
	ctx := context.Background()

	put(t, ms, "held", "Straße der Lieder", nil)
	put(t, ms, "blank", "   ", nil)
	put(t, ms, "inactive", "Inactive", func(m *metadata.TransientMetadata) { m.IsActive = false })
	put(t, ms, "marker", "Marker", func(m *metadata.TransientMetadata) { m.ExpiredAt = &idxNow })
	put(t, ms, "short", "Short ttl", func(m *metadata.TransientMetadata) { m.ExpiresAt = m.FetchedAt })
	put(t, ms, "future", "Future fetch", func(m *metadata.TransientMetadata) {
		m.FetchedAt = idxNow.Add(30 * time.Minute)
		m.ExpiresAt = idxNow.Add(5 * time.Hour)
	})
	for _, id := range []string{"held", "blank", "inactive", "marker", "short", "future", "missing"} {
		indexNow(t, db, id, idxNow)
	}
	assert.Equal(t, map[string]string{"held": "strasse der lieder", "future": "future fetch"}, entries(t, db))

	// Indexing again is idempotent: still exactly one entry.
	indexNow(t, db, "held", idxNow)
	assert.Equal(t, 2, count(t, db, "title_search_doc"))
	assert.Equal(t, 2, count(t, db, "title_search"))

	// An upsert that keeps an earlier expired_at is not indexed (finding F7).
	_, err := db.Exec(`UPDATE transient_metadata SET expired_at = ? WHERE video_id = 'held'`, idxNow.Add(-time.Hour))
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active)
		VALUES ('held', 'New title', ?, ?, 1)
		ON CONFLICT(video_id) DO UPDATE SET title = excluded.title, fetched_at = excluded.fetched_at,
		  expires_at = excluded.expires_at, is_active = excluded.is_active`, idxNow.Add(-time.Minute), idxNow.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, metadata.IndexTitleTx(ctx, tx, titlekey.NewFolder(), "held", idxNow))
	require.NoError(t, tx.Commit())
	assert.NotContains(t, entries(t, db), "held")
}

func TestIndexTitleTxFailureAbortsTitleWrite(t *testing.T) {
	ms, _ := openIndexDB(t)
	db := ms.DB()
	for _, trg := range []string{"insert", "update", "delete"} {
		_, err := db.Exec(`DROP TRIGGER transient_metadata_search_purge_` + trg)
		require.NoError(t, err)
	}
	_, err := db.Exec(`DROP TABLE title_search`)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active) VALUES ('x', 'Title', ?, ?, 1)`,
		idxNow.Add(-time.Hour), idxNow.Add(time.Hour))
	require.NoError(t, err)
	err = metadata.IndexTitleTx(context.Background(), tx, titlekey.NewFolder(), "x", idxNow)
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
	assert.Zero(t, count(t, db, "transient_metadata"))
}

func TestSearchIndexReady(t *testing.T) {
	ms, _ := openIndexDB(t)
	db := ms.DB()
	ready := func() bool {
		tx, err := db.Begin()
		require.NoError(t, err)
		defer tx.Rollback()
		ok, err := metadata.SearchIndexReady(context.Background(), tx)
		require.NoError(t, err)
		return ok
	}
	assert.False(t, ready(), "fresh 029 database is not built")
	_, err := metadata.ReconcileSearchIndex(context.Background(), db, idxNow)
	require.NoError(t, err)
	assert.True(t, ready())
	_, err = db.Exec(`UPDATE title_search_state SET key_version = '0/x'`)
	require.NoError(t, err)
	assert.False(t, ready(), "foreign key version")
	_, err = db.Exec(`UPDATE title_search_state SET key_version = ?, built_at = NULL`, titlekey.Version())
	require.NoError(t, err)
	assert.False(t, ready(), "built_at unset")
}

// R4 (without the store.Open part).
func TestReconcileRepairsSeededDriftInBatches(t *testing.T) {
	setBatch(t, 2)
	ms, _ := openIndexDB(t)
	db := ms.DB()
	ctx := context.Background()
	later := idxNow.Add(2 * time.Hour)

	put(t, ms, "add1", "Alpha one", nil)
	put(t, ms, "add2", "Beta two", nil)
	put(t, ms, "add3", "Gamma three", nil)
	put(t, ms, "held", "Delta four", nil)
	indexNow(t, db, "held", idxNow)
	put(t, ms, "stale", "Epsilon five", func(m *metadata.TransientMetadata) { m.ExpiresAt = idxNow.Add(time.Hour) })
	indexNow(t, db, "stale", idxNow) // passed expires_at at `later` without a sweep
	put(t, ms, "gone", "Zeta six", nil)
	indexNow(t, db, "gone", idxNow)
	put(t, ms, "nofts", "Eta seven", nil)

	// Delete a row while the triggers are absent (test-only).
	rows, err := db.Query(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'transient_metadata_search_purge_%'`)
	require.NoError(t, err)
	var ddl []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		ddl = append(ddl, s)
	}
	require.NoError(t, rows.Close())
	require.Len(t, ddl, 3)
	for _, n := range []string{"insert", "update", "delete"} {
		_, err = db.Exec(`DROP TRIGGER transient_metadata_search_purge_` + n)
		require.NoError(t, err)
	}
	_, err = db.Exec(`DELETE FROM transient_metadata WHERE video_id = 'gone'`)
	require.NoError(t, err)
	for _, s := range ddl {
		_, err = db.Exec(s)
		require.NoError(t, err)
	}
	// An FTS row with no document, and a document with no FTS row.
	_, err = db.Exec(`INSERT INTO title_search(rowid, k) VALUES (9999, 'orphan')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO title_search_doc(doc_id, video_id) VALUES (8888, 'nofts')`)
	require.NoError(t, err)

	stats, err := metadata.ReconcileSearchIndex(ctx, db, later)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.OrphansRemoved)
	assert.Equal(t, 2, stats.Purged, "stale and gone")
	assert.Equal(t, 4, stats.Added, "add1-3 and nofts")
	assert.Greater(t, stats.Batches, 1)
	assert.Equal(t, map[string]string{
		"add1": "alpha one", "add2": "beta two", "add3": "gamma three", "held": "delta four", "nofts": "eta seven",
	}, entries(t, db))
	checkInvariants(t, db, later)

	var version, built sql.NullString
	require.NoError(t, db.QueryRow(`SELECT key_version, built_at FROM title_search_state`).Scan(&version, &built))
	assert.Equal(t, titlekey.Version(), version.String)
	_, perr := time.Parse(time.RFC3339Nano, built.String)
	assert.NoError(t, perr)
	assert.True(t, strings.HasSuffix(built.String, "Z"))

	again, err := metadata.ReconcileSearchIndex(ctx, db, later)
	require.NoError(t, err)
	assert.Equal(t, 0, again.OrphansRemoved)
	assert.Equal(t, 0, again.Purged)
	assert.Equal(t, 0, again.Added)
}

func TestReconcileRefusesForeignKeyVersion(t *testing.T) {
	ms, _ := openIndexDB(t)
	db := ms.DB()
	put(t, ms, "a", "Alpha", nil)
	_, err := db.Exec(`UPDATE title_search_state SET key_version = '0/x'`)
	require.NoError(t, err)
	_, err = metadata.ReconcileSearchIndex(context.Background(), db, idxNow)
	require.ErrorIs(t, err, metadata.ErrSearchIndexVersion)
	assert.Zero(t, count(t, db, "title_search_doc"), "nothing written")
	var v string
	require.NoError(t, db.QueryRow(`SELECT key_version FROM title_search_state`).Scan(&v))
	assert.Equal(t, "0/x", v)
}

func TestReconcileConcurrentWritersNeverBusy(t *testing.T) {
	setBatch(t, 2)
	ms, _ := openIndexDB(t)
	db := ms.DB()
	ctx := context.Background()
	const seeded = 60
	// The pool is deliberately cold (default idle limit): fresh connections
	// are what expose a deferred writer that reaches the FTS5 triggers before it
	// holds the write lock (ARS-001 §10.5, R9).

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var werrs []error
	record := func(err error) {
		if err != nil {
			mu.Lock()
			werrs = append(werrs, err)
			mu.Unlock()
		}
	}
	wg.Add(2)
	go func() { // discovery-style upserts
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmt.Sprintf("disc%03d", i%25)
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				record(err)
				return
			}
			// Like Engine.persist, write the video row before transient_metadata.
			_, err = tx.Exec(`INSERT INTO video (video_id, channel_id, processing_state) VALUES (?, 'chan', 'discovered')
				ON CONFLICT(video_id) DO UPDATE SET channel_id = excluded.channel_id`, id)
			if err != nil {
				_ = tx.Rollback()
				record(fmt.Errorf("DISC-VIDEO %w", err))
				return
			}
			_, err = tx.Exec(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active)
				VALUES (?, ?, ?, ?, 1)
				ON CONFLICT(video_id) DO UPDATE SET title = excluded.title, fetched_at = excluded.fetched_at,
				  expires_at = excluded.expires_at, is_active = excluded.is_active`,
				id, fmt.Sprintf("Discovered %d", i), idxNow.Add(-time.Minute), idxNow.Add(5*time.Hour))
			if err != nil {
				_ = tx.Rollback()
				record(fmt.Errorf("DISC-INSERT %w", err))
				return
			}
			if err = metadata.IndexTitleTx(ctx, tx, titlekey.NewFolder(), id, idxNow); err != nil {
				_ = tx.Rollback()
				record(fmt.Errorf("DISC-INDEX %w", err))
				return
			}
			if err = tx.Commit(); err != nil {
				record(fmt.Errorf("DISC-COMMIT %w", err))
			}
		}
	}()
	go func() { // Expire batches
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			ids := []string{fmt.Sprintf("seed%03d", (i*2)%seeded), fmt.Sprintf("seed%03d", (i*2+1)%seeded), fmt.Sprintf("disc%03d", i%25)}
			record(ms.Expire(ctx, ids, idxNow))
		}
	}()

	var rerr error
	for i := 0; i < 3 && rerr == nil; i++ {
		_, rerr = metadata.ReconcileSearchIndex(ctx, db, idxNow)
	}
	close(stop)
	wg.Wait()
	require.NoError(t, rerr)
	for _, err := range werrs {
		assert.NoError(t, err)
	}
	checkInvariants(t, db, idxNow)
}

func TestReconcileCancelAfterWriteKeepsEarlierBatch(t *testing.T) {
	setBatch(t, 2)
	ms, path := openIndexDB(t)
	db := ms.DB()
	db.SetMaxOpenConns(1)
	for i := 0; i < 6; i++ {
		put(t, ms, fmt.Sprintf("v%d", i), fmt.Sprintf("Video number %d", i), nil)
	}
	other, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var competitor chan error
	calls := 0
	metadata.SetTestHooks(t, metadata.TestHooks{BeforeCommit: func(metadata.ReconcileStats) error {
		calls++
		if calls != 2 {
			return nil
		}
		// A competing writer on another connection must get the lock once the
		// canceled batch has been rolled back.
		competitor = make(chan error, 1)
		started := make(chan struct{})
		go func() {
			close(started)
			_, err := other.Exec(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active) VALUES ('rival', 'Rival', ?, ?, 1)`,
				idxNow.Add(-time.Minute), idxNow.Add(time.Hour))
			competitor <- err
		}()
		<-started
		cancel()
		return nil
	}})

	stats, err := metadata.ReconcileSearchIndex(ctx, db, idxNow)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, stats.Batches, "only the committed batch counts")
	require.NoError(t, <-competitor)
	assert.Equal(t, 2, count(t, db, "title_search_doc"), "first batch stays, second rolled back")
	ready := func() bool {
		tx, err := db.Begin()
		require.NoError(t, err)
		defer tx.Rollback()
		ok, err := metadata.SearchIndexReady(context.Background(), tx)
		require.NoError(t, err)
		return ok
	}
	assert.False(t, ready())

	// The single pooled connection is usable again and rejects no new write.
	wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wcancel()
	_, err = db.ExecContext(wctx, `UPDATE transient_metadata SET channel_name = 'c' WHERE video_id = 'v0'`)
	require.NoError(t, err)

	metadata.SetTestHooks(t, metadata.TestHooks{})
	_, err = metadata.ReconcileSearchIndex(wctx, db, idxNow)
	require.NoError(t, err)
	assert.True(t, ready())
	checkInvariants(t, db, idxNow)
}

func tempProbeCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_temp_master WHERE name = 'probe'`).Scan(&n))
	return n
}

func TestReconcileCleanupConfirmedKeepsConnectionUnconfirmedDiscardsIt(t *testing.T) {
	setBatch(t, 2)
	ms, _ := openIndexDB(t)
	db := ms.DB()
	db.SetMaxOpenConns(1)
	for i := 0; i < 3; i++ {
		put(t, ms, fmt.Sprintf("v%d", i), fmt.Sprintf("Video number %d", i), nil)
	}
	_, err := db.Exec(`CREATE TEMP TABLE probe(x)`)
	require.NoError(t, err)
	wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fail := errors.New("injected failure before commit")

	// Confirmed rollback: the physical connection returns to the pool.
	metadata.SetTestHooks(t, metadata.TestHooks{BeforeCommit: func(metadata.ReconcileStats) error { return fail }})
	_, err = metadata.ReconcileSearchIndex(wctx, db, idxNow)
	require.ErrorIs(t, err, fail)
	assert.Equal(t, 1, tempProbeCount(t, db), "same connection reused")

	// Unconfirmed rollback: the connection is discarded, the next checkout is new.
	rollbackErr := errors.New("injected rollback failure")
	metadata.SetTestHooks(t, metadata.TestHooks{
		BeforeCommit: func(metadata.ReconcileStats) error { return fail },
		Rollback:     func(context.Context, *sql.Conn) error { return rollbackErr },
	})
	_, err = metadata.ReconcileSearchIndex(wctx, db, idxNow)
	require.ErrorIs(t, err, fail)
	require.ErrorIs(t, err, rollbackErr)
	assert.Zero(t, tempProbeCount(t, db), "physical connection discarded")
	_, err = db.ExecContext(wctx, `UPDATE transient_metadata SET channel_name = 'c' WHERE video_id = 'v0'`)
	require.NoError(t, err, "the abandoned transaction left no write lock behind")
	assert.Zero(t, count(t, db, "title_search_doc"), "nothing from the failed batches persisted")
}

// conn0Rollback issues ROLLBACK outside any transaction on the pool's only
// connection and returns SQLite's error.
func conn0Rollback(t *testing.T, db *sql.DB) (sql.Result, error) {
	t.Helper()
	return db.Exec("ROLLBACK")
}

func TestReconcileAmbiguousCommitIsNotCounted(t *testing.T) {
	setBatch(t, 2)
	ms, _ := openIndexDB(t)
	db := ms.DB()
	db.SetMaxOpenConns(1)
	for i := 0; i < 3; i++ {
		put(t, ms, fmt.Sprintf("v%d", i), fmt.Sprintf("Video number %d", i), nil)
	}
	wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := db.Exec(`CREATE TEMP TABLE probe(x)`)
	require.NoError(t, err)
	ambiguous := errors.New("injected commit error after commit")
	metadata.SetTestHooks(t, metadata.TestHooks{Commit: func(ctx context.Context, c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		return ambiguous
	}})
	stats, err := metadata.ReconcileSearchIndex(wctx, db, idxNow)
	require.ErrorIs(t, err, ambiguous)
	assert.Equal(t, 0, stats.Batches)
	assert.Zero(t, stats.Added)
	// SQLite reports "no transaction is active" for the cleanup ROLLBACK. That
	// confirms autocommit, so the connection is kept and no rollback error is
	// joined.
	assert.NotContains(t, err.Error(), "roll back search index transaction")
	assert.Equal(t, 1, tempProbeCount(t, db), "connection kept, not discarded")
	var sqliteErr *sqlite.Error
	_, rbErr := conn0Rollback(t, db)
	require.ErrorAs(t, rbErr, &sqliteErr)
	assert.Equal(t, 1, sqliteErr.Code(), "SQLite 'no transaction is active' is a generic error")

	metadata.SetTestHooks(t, metadata.TestHooks{})
	_, err = metadata.ReconcileSearchIndex(wctx, db, idxNow)
	require.NoError(t, err)
	checkInvariants(t, db, idxNow)
}

// seedRows bulk-inserts n held rows without index entries.
func seedRows(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	stmt, err := tx.Prepare(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active) VALUES (?, ?, ?, ?, 1)`)
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		_, err := stmt.Exec(fmt.Sprintf("v%08d", i), fmt.Sprintf("Video %d about topic %d with words w%d", i, i%977, i*7919%10007),
			idxNow.Add(-time.Hour), idxNow.Add(10*time.Hour))
		require.NoError(t, err)
	}
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Commit())
}

// A reconcile scans the index once, so its batch count and time are linear in
// the number of rows. 10,000 rows always run; YUHAUL_PERF=1 adds 40,000 and
// 150,000 and logs the timings.
func TestReconcileCostIsLinear(t *testing.T) {
	sizes := []int{10000}
	if os.Getenv("YUHAUL_PERF") == "1" {
		sizes = append(sizes, 40000, 150000)
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			ms, _ := openIndexDB(t)
			db := ms.DB()
			seedRows(t, db, n)
			ctx := context.Background()

			start := time.Now()
			backfill, err := metadata.ReconcileSearchIndex(ctx, db, idxNow)
			require.NoError(t, err)
			backfillTime := time.Since(start)
			assert.Equal(t, n, backfill.Added)

			start = time.Now()
			steady, err := metadata.ReconcileSearchIndex(ctx, db, idxNow)
			require.NoError(t, err)
			steadyTime := time.Since(start)
			assert.Zero(t, steady.OrphansRemoved+steady.Purged+steady.Added)
			// Nothing to fix: three scan phases of 2,000 rows per batch, plus the
			// batch that finishes.
			assert.LessOrEqual(t, steady.Batches, 3*n/2000+4, "steady-state batches grow linearly")
			// Backfill: n/200 change-limited batches plus the scan batches.
			assert.LessOrEqual(t, backfill.Batches, n/200+3*n/2000+4)
			t.Logf("rows=%d backfill=%s batches=%d steady=%s batches=%d", n, backfillTime, backfill.Batches, steadyTime, steady.Batches)
			checkInvariants(t, db, idxNow)
		})
	}
}

// seedExpired bulk-inserts n rows as Expire leaves them: inactive, expired,
// no title. Production never deletes them.
func seedExpired(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	stmt, err := tx.Prepare(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, expired_at, is_active) VALUES (?, NULL, ?, ?, ?, 0)`)
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		_, err := stmt.Exec(fmt.Sprintf("x%08d", i), idxNow.Add(-48*time.Hour), idxNow.Add(-24*time.Hour), idxNow.Add(-23*time.Hour))
		require.NoError(t, err)
	}
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Commit())
}

// Expired rows that sort after every held row must cost scan budget: a batch
// walks at most one 2,000-row page, however many expired rows trail.
func TestReconcileBatchWalksAtMostOnePageOfExpiredRows(t *testing.T) {
	ms, _ := openIndexDB(t)
	db := ms.DB()
	seedRows(t, db, 10)
	seedExpired(t, db, 5000) // 2.5 pages of rows with nothing to add

	var calls int
	metadata.SetTestHooks(t, metadata.TestHooks{BeforeCommit: func(metadata.ReconcileStats) error { calls++; return nil }})
	stats, err := metadata.ReconcileSearchIndex(context.Background(), db, idxNow)
	require.NoError(t, err)
	assert.Equal(t, 10, stats.Added)
	// 5,010 walked rows over 2,000-row pages: 3 batches (the last also finishes).
	// A walk bounded only by the number of candidates would finish in 1.
	assert.Equal(t, 3, stats.Batches)
	assert.Equal(t, stats.Batches, calls)
	checkInvariants(t, db, idxNow)
}

// With YUHAUL_PERF=1: longest write-locked batch with 150,000 held rows and
// 150,000 expired rows.
func TestReconcileLongestBatchWithExpiredTail(t *testing.T) {
	if os.Getenv("YUHAUL_PERF") != "1" {
		t.Skip("set YUHAUL_PERF=1")
	}
	ms, _ := openIndexDB(t)
	db := ms.DB()
	seedRows(t, db, 150000)
	seedExpired(t, db, 150000)
	for _, phase := range []string{"backfill", "steady"} {
		last := time.Now()
		var longest time.Duration
		var batches int
		metadata.SetTestHooks(t, metadata.TestHooks{BeforeCommit: func(metadata.ReconcileStats) error {
			if d := time.Since(last); d > longest {
				longest = d
			}
			batches++
			last = time.Now()
			return nil
		}})
		start := time.Now()
		_, err := metadata.ReconcileSearchIndex(context.Background(), db, idxNow)
		require.NoError(t, err)
		t.Logf("%s: total=%s batches=%d longest batch (to pre-commit)=%s", phase, time.Since(start), batches, longest)
		last = time.Now()
	}
}

// writerWaits runs small writers on their own connection (short busy_timeout)
// until stop closes. It returns the longest wait and the first error.
func writerWaits(t *testing.T, path string, stop <-chan struct{}) (maxWait <-chan time.Duration, firstErr <-chan error) {
	t.Helper()
	other, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })
	_, err = other.Exec(`PRAGMA busy_timeout = 2000`)
	require.NoError(t, err)
	wait, errc := make(chan time.Duration, 1), make(chan error, 1)
	go func() {
		var longest time.Duration
		var first error
		for i := 0; ; i++ {
			select {
			case <-stop:
				wait <- longest
				errc <- first
				return
			default:
			}
			start := time.Now()
			_, err := other.Exec(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active) VALUES (?, ?, ?, ?, 1)
				ON CONFLICT(video_id) DO UPDATE SET fetched_at = excluded.fetched_at`,
				fmt.Sprintf("w%03d", i%20), fmt.Sprintf("Writer title %d", i%20), idxNow.Add(-time.Minute), idxNow.Add(time.Hour))
			if d := time.Since(start); d > longest {
				longest = d
			}
			if err != nil && first == nil {
				first = err
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return wait, errc
}

// Another writer is not starved by a clean multi-batch reconcile: it sees no
// SQLITE_BUSY and a bounded wait, and the reconcile reports its longest hold.
func TestReconcileDoesNotStarveWriters(t *testing.T) {
	setBatch(t, 5)
	ms, path := openIndexDB(t)
	// Enough rows that the reconcile needs many batches.
	for i := 0; i < 600; i++ {
		put(t, ms, fmt.Sprintf("v%04d", i), fmt.Sprintf("Video number %d with some words", i), nil)
	}

	stop := make(chan struct{})
	wait, errc := writerWaits(t, path, stop)
	stats, err := metadata.ReconcileSearchIndex(context.Background(), ms.DB(), idxNow)
	close(stop)
	require.NoError(t, err)
	require.Greater(t, stats.Batches, 20, "the test needs many back-to-back batches")
	assert.Positive(t, stats.LongestHold)
	assert.NoError(t, <-errc, "a concurrent writer must not see SQLITE_BUSY")
	assert.Less(t, <-wait, time.Second, "a concurrent writer must not wait long")
}
