package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
)

// M1 (run-initiation ARS-001 §8.1): a database at version 023 with manual,
// scheduled, retry-shaped, backtest and replay rows upgrades to 024. Both new
// columns are NULL, no existing value changes, and the database reopens.
func TestMigration024UpgradeLeavesExistingRunsUntouched(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre024.db")
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

	rows := []struct{ runType, status, interruptReason string }{
		{"manual", "completed", ""},
		{"scheduled", "completed", ""},
		{"manual", "interrupted", "diag:v1 phase=action_execution operation=load_run code=timeout"}, // retry-shaped / old time-limit stop
		{"backtest", "completed", ""},
		{"replay", "failed", "diag:v1 phase=unknown operation=unknown code=unknown_error"},
	}
	for i, r := range rows {
		var reason any
		if r.interruptReason != "" {
			reason = r.interruptReason
		}
		_, err := db.Exec(`INSERT INTO run (started_at, run_type, status, interrupt_reason, mutation_safety_version)
			VALUES (?,?,?,?,?)`, fmt.Sprintf("2026-01-0%dT00:00:00.000000000Z", i+1), r.runType, r.status, reason, i%2)
		require.NoError(t, err)
	}
	before, oldColumns := dumpColumns(t, db, nil)
	require.Len(t, before, len(rows))
	require.NoError(t, db.Close())

	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	var version int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT version FROM schema_migrations LIMIT 1`).Scan(&version))
	require.GreaterOrEqual(t, version, 24)

	after, _ := dumpColumns(t, db, oldColumns)
	require.Equal(t, before, after, "no existing column value may change")
	var withInitiator, withCause int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(initiated_by), COUNT(interrupt_cause) FROM run`).Scan(&withInitiator, &withCause))
	require.Zero(t, withInitiator, "no row is backfilled with an initiator")
	require.Zero(t, withCause, "no row is backfilled with a cause")
	require.NoError(t, db.Close())

	db, err = store.Open(ctx, path)
	require.NoError(t, err, "close and reopen succeeds")
	require.NoError(t, db.Close())
}

// dumpColumns renders the given columns of every run row (all columns when
// columns is nil) and returns the column list it used, so a later dump after an
// upgrade compares only what existed before it.
func dumpColumns(t *testing.T, db *sql.DB, columns []string) ([]string, []string) {
	t.Helper()
	if columns == nil {
		r, err := db.Query(`SELECT * FROM run LIMIT 0`)
		require.NoError(t, err)
		columns, err = r.Columns()
		require.NoError(t, err)
		require.NoError(t, r.Close())
	}
	r, err := db.Query(`SELECT ` + strings.Join(columns, ", ") + ` FROM run ORDER BY id`)
	require.NoError(t, err)
	defer r.Close()
	var out []string
	for r.Next() {
		vals := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, r.Scan(ptrs...))
		var parts []string
		for i, c := range columns {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			parts = append(parts, fmt.Sprintf("%s=%v", c, v))
		}
		out = append(out, strings.Join(parts, "|"))
	}
	require.NoError(t, r.Err())
	return out, columns
}
