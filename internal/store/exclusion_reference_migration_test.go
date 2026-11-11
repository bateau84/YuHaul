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

func TestMigration022ExclusionReferenceShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	src, err := iofs.New(os.DirFS("migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(21))

	// Version 21 permitted the malformed row; upgrading must not erase it.
	_, err = db.Exec(`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason, exclusion_index, exclusion_semantics_version)
		VALUES (1, 1, 1, 0, 'skipped', NULL, 0, 1), (2, 1, 1, 0, 'skipped', 'exclusion', NULL, NULL), (3, 1, 1, 0, 'skipped', 'exclusion', 0, 1)`)
	require.NoError(t, err)
	require.NoError(t, store.ApplyMigrations(db))
	require.NoError(t, db.Close())
	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	var schemaVersion int
	require.NoError(t, db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&schemaVersion))
	require.Equal(t, 26, schemaVersion)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM route_decision`).Scan(&count))
	require.Equal(t, 3, count)
	var reason sql.NullString
	var index, version int
	require.NoError(t, db.QueryRow(`SELECT skip_reason, exclusion_index, exclusion_semantics_version FROM route_decision WHERE video_id = 1`).Scan(&reason, &index, &version))
	require.False(t, reason.Valid)
	require.Equal(t, 0, index)
	require.Equal(t, 1, version)

	cases := []struct {
		name                             string
		decision, reason, index, version any
		valid                            bool
	}{
		{"legacy", "skipped", "exclusion", nil, nil, true},
		{"ordinary", "matched", nil, nil, nil, true},
		{"captured", "skipped", "exclusion", 0, 1, true},
		{"null reason", "skipped", nil, 0, 1, false},
		{"wrong reason", "skipped", "disabled", 0, 1, false},
		{"wrong decision", "matched", "exclusion", 0, 1, false},
		{"fractional index", "skipped", "exclusion", 0.5, 1, false},
		{"fractional version", "skipped", "exclusion", 0, 1.5, false},
		{"missing index", "skipped", "exclusion", nil, 1, false},
		{"missing version", "skipped", "exclusion", 0, nil, false},
		{"negative index", "skipped", "exclusion", -1, 1, false},
		{"zero version", "skipped", "exclusion", 0, 0, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason, exclusion_index, exclusion_semantics_version) VALUES (?, 1, 1, 0, ?, ?, ?, ?)`, 100+i, tc.decision, tc.reason, tc.index, tc.version)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			_, err = db.Exec(`UPDATE route_decision SET decision = ?, skip_reason = ?, exclusion_index = ?, exclusion_semantics_version = ? WHERE video_id = 2`, tc.decision, tc.reason, tc.index, tc.version)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
