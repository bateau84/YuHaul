package store_test

import (
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

// M1 (range columns) and M4: upgrading keeps pre-024 Backtest/Replay rows with
// NULL bounds, and the triggers guard every later write.
func TestMigration024PublishedRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "range.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	src, err := iofs.New(os.DirFS("migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(23))

	for _, rt := range []string{"manual", "scheduled", "backtest", "replay"} {
		_, err = db.Exec(`INSERT INTO run (started_at, run_type, status) VALUES ('2026-10-01T00:00:00Z', ?, 'completed')`, rt)
		require.NoError(t, err)
	}
	require.NoError(t, store.ApplyMigrations(db))
	require.NoError(t, db.Close())
	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)

	var withRange int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM run WHERE published_from IS NOT NULL OR published_to IS NOT NULL`).Scan(&withRange))
	require.Zero(t, withRange, "existing rows are not backfilled")
	var total int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM run`).Scan(&total))
	require.Equal(t, 4, total)

	insert := func(runType string, from, to any) error {
		_, err := db.Exec(`INSERT INTO run (started_at, run_type, status, published_from, published_to) VALUES ('2026-11-01T00:00:00Z', ?, 'running', ?, ?)`, runType, from, to)
		return err
	}
	require.NoError(t, insert("backtest", "2026-10-01", "2026-10-31"))
	require.NoError(t, insert("replay", nil, "2026-10-31"))
	require.NoError(t, insert("replay", "2026-10-01", nil))
	require.NoError(t, insert("backtest", "2026-10-15", "2026-10-15"))
	require.NoError(t, insert("backtest", nil, nil))

	for name, c := range map[string]struct {
		runType  string
		from, to any
	}{
		"manual run":       {"manual", "2026-10-01", nil},
		"scheduled run":    {"scheduled", nil, "2026-10-01"},
		"dry run":          {"dry_run", "2026-10-01", "2026-10-02"},
		"non canonical":    {"backtest", "2026-1-05", nil},
		"reversed":         {"backtest", "2026-11-02", "2026-11-01"},
		"not a date":       {"backtest", "abcd-ef-gh", nil},
		"integer bound":    {"backtest", 20261001, nil},
		"trailing garbage": {"backtest", "2026-10-011", nil},
	} {
		require.Error(t, insert(c.runType, c.from, c.to), name)
	}

	// Record what the secondary guard does for an impossible day (ARS-001 M4).
	// The Go parser is authoritative; the application never relies on this.
	err = insert("backtest", "2026-02-30", nil)
	t.Logf("SQLite trigger on 2026-02-30: error=%v", err)

	var id int64
	require.NoError(t, db.QueryRow(`SELECT id FROM run WHERE published_from = '2026-10-01' AND published_to = '2026-10-31'`).Scan(&id))
	for _, stmt := range []string{
		`UPDATE run SET published_from = '2026-09-01' WHERE id = ?`,
		`UPDATE run SET published_to = NULL WHERE id = ?`,
	} {
		_, err := db.Exec(stmt, id)
		require.Error(t, err, stmt)
	}
	var nullID int64
	require.NoError(t, db.QueryRow(`SELECT id FROM run WHERE run_type = 'backtest' AND published_from IS NULL AND published_to IS NULL ORDER BY id DESC LIMIT 1`).Scan(&nullID))
	_, err = db.Exec(`UPDATE run SET published_to = '2026-10-01' WHERE id = ?`, nullID)
	require.Error(t, err, "NULL -> value is rejected")
	_, err = db.Exec(`UPDATE run SET status = 'completed' WHERE id = ?`, id)
	require.NoError(t, err, "unrelated updates still work")
}
