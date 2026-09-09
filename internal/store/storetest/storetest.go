// Package storetest provides test helpers for opening SQLite databases with the
// canonical YuHaul schema applied.
package storetest

import (
	"database/sql"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"

	// Register the pure-Go sqlite driver used throughout the project.
	_ "modernc.org/sqlite"
)

// Open returns a new in-memory SQLite database with all migrations applied.
// The database is closed automatically when the test finishes.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_fk=1")
	if err != nil {
		t.Fatalf("storetest: open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := store.ApplyMigrations(db); err != nil {
		t.Fatalf("storetest: apply migrations: %v", err)
	}
	return db
}
