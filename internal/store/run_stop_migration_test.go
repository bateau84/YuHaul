package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
)

func migrateTo(t *testing.T, path string, version uint) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	src, err := iofs.New(os.DirFS("migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(version))
	return db
}

func schemaVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	require.NoError(t, db.QueryRow(`PRAGMA schema_version`).Scan(&v))
	return v
}

// M0 (ARS-003 §7.1 test 1): the writable_schema edit works on modernc.
func TestRunStatusCheckWidenedByWritableSchemaStep(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m0.db")
	db := migrateTo(t, path, 24)
	_, err := db.Exec(`INSERT INTO run (run_type,status) VALUES ('manual','interrupted')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var check string
	require.NoError(t, db.QueryRow(`PRAGMA integrity_check`).Scan(&check))
	require.Equal(t, "ok", check)
	var def string
	require.NoError(t, db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='run'`).Scan(&def))
	require.Contains(t, def, "'stopped'")

	// 'stopped' passes the CHECK only via the allowed transition.
	_, err = db.Exec(`INSERT INTO run (run_type,status) VALUES ('manual','stopped')`)
	require.ErrorContains(t, err, "invalid run stop request")
	_, err = db.Exec(`INSERT INTO run (run_type,status) VALUES ('manual','bogus')`)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "invalid run stop request")
	_, err = db.Exec(`UPDATE run SET status='bogus' WHERE id=1`)
	require.ErrorContains(t, err, "CHECK constraint failed")
	_, err = db.Exec(`UPDATE run SET status='running' WHERE id=1`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE run SET stop_requested_at='2026-11-10T12:00:00Z' WHERE id=1`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE run SET status='stopped' WHERE id=1`)
	require.NoError(t, err, "stopped via marker succeeds")

	require.NoError(t, db.Close())
	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='run'`).Scan(&def))
	require.Contains(t, def, "'stopped'", "reopen keeps the CHECK")
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger' AND name LIKE 'run_%'`).Scan(&n))
	require.Equal(t, 9, n, "six 024 triggers and three 025 triggers")
}

// M4: the step is idempotent and bumps schema_version once.
func TestRunStatusCheckStepIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m4.db")
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	v1 := schemaVersion(t, db)
	require.NoError(t, store.ApplyMigrations(db))
	require.Equal(t, v1, schemaVersion(t, db), "second step is a no-op")
	require.NoError(t, db.Close())

	// Migrate through 027 only (no widening steps). ApplyMigrations then runs
	// widenRunStatusCheck and widenFinalOutcomeChecks, one bump each.
	pre := migrateTo(t, filepath.Join(t.TempDir(), "pre.db"), 27)
	before := schemaVersion(t, pre)
	require.NoError(t, store.ApplyMigrations(pre))
	require.Equal(t, before+2, schemaVersion(t, pre), "each widening step bumps schema_version by exactly one")
	require.NoError(t, store.ApplyMigrations(pre))
	require.Equal(t, before+2, schemaVersion(t, pre), "a second step is a no-op")
}

// M1: a 024 database with queue rows in every status upgrades unchanged.
func TestMigration025PreservesDeferredItems(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m1.db")
	db := migrateTo(t, path, 24)
	_, err := db.Exec(`INSERT INTO run (run_type,status) VALUES ('scheduled','completed')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO video (id, video_id, channel_id, processing_state) VALUES (1,'a','c','deferred')`)
	require.NoError(t, err)
	for i, st := range []string{"pending", "retrying", "completed", "discarded"} {
		_, err = db.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status,retry_count,retry_run_id) VALUES (1,1,'quota_exhausted',?,?,CASE WHEN ?='retrying' THEN 1 END)`, st, i, st)
		require.NoError(t, err)
	}
	dump := func(db *sql.DB) []string {
		r, err := db.Query(`SELECT id||'|'||video_id||'|'||run_id||'|'||deferral_reason||'|'||ifnull(retry_after,'')||'|'||retry_count||'|'||status||'|'||created_at||'|'||ifnull(retry_run_id,'') FROM deferred_item ORDER BY id`)
		require.NoError(t, err)
		defer r.Close()
		var out []string
		for r.Next() {
			var s string
			require.NoError(t, r.Scan(&s))
			out = append(out, s)
		}
		return out
	}
	before := dump(db)
	require.Len(t, before, 4)
	require.NoError(t, db.Close())
	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.Equal(t, before, dump(db))
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(stop_requested_at) FROM run`).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name IN ('idx_deferred_item_status','deferred_item_retry_owner')`).Scan(&n))
	require.Equal(t, 2, n)
	// M3: both new reasons insert, an unknown one aborts.
	for _, reason := range []string{"stopped_by_user", "discarded_by_user"} {
		_, err = db.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status) VALUES (1,1,?,'pending')`, reason)
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status) VALUES (1,1,'nope','pending')`)
	require.Error(t, err)
}

// M2: the stop triggers.
func TestRunStopTriggers(t *testing.T) {
	db := openStopDB(t)
	exec := func(q string, args ...any) error { _, err := db.Exec(q, args...); return err }
	mk := func(status string) int64 {
		res, err := db.Exec(`INSERT INTO run (run_type,status) VALUES ('manual',?)`, status)
		require.NoError(t, err)
		id, _ := res.LastInsertId()
		return id
	}
	for _, st := range []string{"completed", "failed", "interrupted"} {
		id := mk(st)
		require.ErrorContains(t, exec(`UPDATE run SET stop_requested_at='x' WHERE id=?`, id), "invalid run stop request", st)
	}
	id := mk("running")
	require.NoError(t, exec(`UPDATE run SET stop_requested_at='t1' WHERE id=?`, id))
	require.ErrorContains(t, exec(`UPDATE run SET stop_requested_at='t2' WHERE id=?`, id), "invalid run stop request")
	require.ErrorContains(t, exec(`UPDATE run SET stop_requested_at=NULL WHERE id=?`, id), "invalid run stop request")
	for _, st := range []string{"completed", "failed", "interrupted"} {
		require.ErrorContains(t, exec(`UPDATE run SET status=? WHERE id=?`, st, id), "invalid stopped transition", st)
	}
	require.NoError(t, exec(`UPDATE run SET status='stopped' WHERE id=?`, id))
	require.ErrorContains(t, exec(`UPDATE run SET status='running' WHERE id=?`, id), "invalid stopped transition")
	unmarked := mk("running")
	require.ErrorContains(t, exec(`UPDATE run SET status='stopped' WHERE id=?`, unmarked), "invalid stopped transition")
	require.ErrorContains(t, exec(`INSERT INTO run (run_type,status) VALUES ('manual','stopped')`), "invalid run stop request")
	require.ErrorContains(t, exec(`INSERT INTO run (run_type,status,stop_requested_at) VALUES ('manual','running','t')`), "invalid run stop request")
}

func openStopDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
