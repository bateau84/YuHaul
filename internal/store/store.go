// Package store opens a YuHaul SQLite database and applies versioned migrations.
//
// Migrations live in the embedded migrations/ directory and are applied with
// golang-migrate so the schema is versioned and re-runnable.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// Register the pure-Go sqlite driver. FTS5 is enabled by default in this
	// driver, which is required for the title-fingerprint search index.
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens the SQLite database at path, applies any pending migrations,
// completes the route identity backfill, and returns a *sql.DB configured
// with foreign keys, WAL mode, and a busy timeout.
//
// The route identity backfill (migration 012 / ARS-004) runs here so it
// finishes before any caller can start workers or serve requests:
// cmd/server calls Open before scheduler.Start and ListenAndServe, and both
// the migration and backfill errors abort startup.
//
// The caller is responsible for closing the returned database.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := sqliteDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping database: %w", err)
	}

	if err := ApplyMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: apply migrations: %w", err)
	}

	if err := BackfillRouteIdentities(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: backfill route identities: %w", err)
	}

	return db, nil
}

func sqliteDSN(path string) string {
	b := &strings.Builder{}
	b.WriteString(path)
	b.WriteString("?_fk=1")
	b.WriteString("&_journal_mode=WAL")
	b.WriteString("&_busy_timeout=5000")
	b.WriteString("&_foreign_keys=1")
	return b.String()
}

// ApplyMigrations runs all embedded migrations against an existing *sql.DB.
// It is exported so tests can create an in-memory database and apply the same
// schema used in production.
func ApplyMigrations(db *sql.DB) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("load migration source: %w", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// MustRegisterDriver registers the golang-migrate sqlite driver with the
// sqlite database instance. This is a no-op at runtime because Open handles
// migrations itself; the import is kept so the driver is available.
var _ = sqlite.Config{}
