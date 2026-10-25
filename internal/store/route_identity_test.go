package store_test

// Tests for migration 012 (route_identity registry) and the startup backfill
// gate (ARS-004 "Database schema and migration" and "Verification" items
// 1-3, plus the startup idempotency item 2).

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// canonicalUUIDv4 matches the ARS-004 public route identity format:
// lowercase, hyphenated, version 4, RFC 4122 variant.
var canonicalUUIDv4 = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ConfigVersion snapshots in the v11 fixture. Snapshot 1 is a valid legacy
// document whose route id 42 has no route row (snapshot-only historical ID,
// e.g. a pre-ADR-003 hard delete). Snapshot 2 is malformed JSON and must be
// skipped without repair. Snapshot 3 is structurally valid but carries
// invalid route ids ("9" string, 7.5 float) that must not be mapped, plus a
// valid integer id 10 that must be. Snapshot 4 matches the "{}" shape written
// by existing tests.
const (
	fixtureSnapshot1 = `{"routes":[` +
		`{"id":1,"name":"Music","evaluation_order":1,"is_final":false,"is_enabled":true,"condition_composition":"AND","conditions":[],"actions":[],"exclusions":[]},` +
		`{"id":3,"name":"Podcasts","evaluation_order":3,"is_final":false,"is_enabled":false,"condition_composition":"AND","conditions":[],"actions":[],"exclusions":[]},` +
		`{"id":42,"name":"Legacy removed route","evaluation_order":4,"is_final":false,"is_enabled":true,"condition_composition":"AND","conditions":[],"actions":[],"exclusions":[]}` +
		`],"duplicate_threshold":0.9,"duplicate_behavior":"skip"}`
	fixtureSnapshot2 = `{"routes":[{"id":1,`
	fixtureSnapshot3 = `{"routes":[{"id":"9"},{"id":7.5},{"id":10}],"duplicate_threshold":0.9,"duplicate_behavior":"skip"}`
	fixtureSnapshot4 = `{}`
)

// preMigrationState captures the durable route-identity evidence that
// migration/backfill must leave byte-for-byte unchanged.
type preMigrationState struct {
	routeIDs         []int64
	decisionRouteIDs []int64
	sourceRouteIDs   []int64
	auditCount       int
	executedCount    int
	snapshotsHex     map[int64]string
}

// openV11Fixture creates a populated SQLite database pinned at schema version
// 11 and returns its path with the pre-migration evidence snapshot. The
// database is closed so callers can exercise the real startup path
// (store.Open: migrate to 012 + backfill) against it.
func openV11Fixture(t *testing.T) (string, preMigrationState) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	src, err := iofs.New(os.DirFS("migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(11))

	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "fixture insert: %s", query)
	}

	// Two active routes with the same display name plus one soft-deleted
	// route (ADR-003), so the fixture covers duplicate-name and deleted-row
	// identity keys.
	exec(`INSERT INTO route (id, name, description, evaluation_order, is_final, is_enabled, condition_composition)
	      VALUES (1, 'Music', 'first music route', 1, 0, 1, 'AND');`)
	exec(`INSERT INTO route (id, name, evaluation_order, is_final, is_enabled, condition_composition)
	      VALUES (2, 'Music', 2, 0, 1, 'OR');`)
	exec(`INSERT INTO route (id, name, evaluation_order, is_final, is_enabled, condition_composition, deleted_at)
	      VALUES (3, 'Podcasts', 3, 0, 0, 'AND', '2026-01-01T00:00:00Z');`)

	exec(`INSERT INTO route_condition (route_id, condition_index, field, operator, value)
	      VALUES (1, 0, 'title', 'contains', 'live');`)
	exec(`INSERT INTO route_condition (route_id, condition_index, field, operator, value)
	      VALUES (3, 0, 'channel_name', 'equals', 'old podcast');`)
	exec(`INSERT INTO route_action (route_id, action_index, action_type, target_playlist_id)
	      VALUES (1, 0, 'playlist_insert', 'PLmusic');`)
	exec(`INSERT INTO route_exclusion (route_id, exclusion_type, exclusion_value)
	      VALUES (2, 'word', 'shorts');`)

	exec(`INSERT INTO config_version (id, snapshot, change_description) VALUES (1, ?, 'v11 fixture');`, fixtureSnapshot1)
	exec(`INSERT INTO config_version (id, snapshot, change_description) VALUES (2, ?, 'v11 fixture');`, fixtureSnapshot2)
	exec(`INSERT INTO config_version (id, snapshot, change_description) VALUES (3, ?, 'v11 fixture');`, fixtureSnapshot3)
	exec(`INSERT INTO config_version (id, snapshot, change_description) VALUES (4, ?, 'v11 fixture');`, fixtureSnapshot4)

	exec(`INSERT INTO run (id, started_at, run_type, status, config_version_id)
	      VALUES (1, '2026-01-02T00:00:00Z', 'manual', 'completed', 1);`)
	exec(`INSERT INTO video (id, video_id, channel_id, processing_state, final_outcome)
	      VALUES (1, 'vid1', 'UC1', 'evaluated', 'accepted');`)

	// Decisions against an active route and the soft-deleted route.
	exec(`INSERT INTO route_decision (id, video_id, run_id, route_id, evaluation_order, decision)
	      VALUES (1, 1, 1, 1, 1, 'matched');`)
	exec(`INSERT INTO route_decision (id, video_id, run_id, route_id, evaluation_order, decision)
	      VALUES (2, 1, 1, 3, 3, 'not_matched');`)
	exec(`INSERT INTO condition_audit (route_decision_id, condition_index, field, operator, expected_value, actual_value, matched)
	      VALUES (1, 0, 'title', 'contains', 'live', 'live session', 1);`)
	exec(`INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type, target_playlist_id, outcome, executed_at)
	      VALUES (1, 1, 1, 'playlist_insert', 'PLmusic', 'succeeded', '2026-01-02T00:00:01Z');`)
	exec(`INSERT INTO duplicate_match (video_id, similarity_score, match_type, source_route_id, threshold_used)
	      VALUES (1, 1.0, 'exact_video_id', 1, 0.9);`)

	pre := capturePreMigrationState(t, db)
	require.NoError(t, db.Close())
	return path, pre
}

// capturePreMigrationState reads the durable evidence that must survive the
// migration and backfill unchanged.
func capturePreMigrationState(t *testing.T, db *sql.DB) preMigrationState {
	t.Helper()
	ctx := context.Background()

	listInts := func(query string) []int64 {
		t.Helper()
		rows, err := db.QueryContext(ctx, query)
		require.NoError(t, err)
		defer rows.Close()
		out := []int64{}
		for rows.Next() {
			var v int64
			require.NoError(t, rows.Scan(&v))
			out = append(out, v)
		}
		require.NoError(t, rows.Err())
		return out
	}
	count := func(query string) int {
		t.Helper()
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query).Scan(&n))
		return n
	}

	snapshots := make(map[int64]string)
	rows, err := db.QueryContext(ctx, `SELECT id, hex(snapshot) FROM config_version ORDER BY id;`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id int64
		var hex string
		require.NoError(t, rows.Scan(&id, &hex))
		snapshots[id] = hex
	}
	require.NoError(t, rows.Err())

	return preMigrationState{
		routeIDs:         listInts(`SELECT id FROM route ORDER BY id;`),
		decisionRouteIDs: listInts(`SELECT route_id FROM route_decision ORDER BY id;`),
		sourceRouteIDs:   listInts(`SELECT source_route_id FROM duplicate_match WHERE source_route_id IS NOT NULL ORDER BY id;`),
		auditCount:       count(`SELECT COUNT(*) FROM condition_audit;`),
		executedCount:    count(`SELECT COUNT(*) FROM executed_action;`),
		snapshotsHex:     snapshots,
	}
}

// expectedFixtureKeys is the union the ARS-004 backfill must map for the
// fixture: route rows 1/2/3 plus snapshot-only id 42 and snapshot-valid id
// 10. String id "9" and float id 7.5 are invalid and must not be mapped.
var expectedFixtureKeys = []int64{1, 2, 3, 10, 42}

// assertPreserved verifies the pre-migration durable evidence is unchanged.
func assertPreserved(t *testing.T, db *sql.DB, pre preMigrationState) {
	t.Helper()
	post := capturePreMigrationState(t, db)
	assert.Equal(t, pre.routeIDs, post.routeIDs, "route ids must be unchanged")
	assert.Equal(t, pre.decisionRouteIDs, post.decisionRouteIDs, "decision route FKs must be unchanged")
	assert.Equal(t, pre.sourceRouteIDs, post.sourceRouteIDs, "duplicate provenance FKs must be unchanged")
	assert.Equal(t, pre.auditCount, post.auditCount, "condition audit rows must be unchanged")
	assert.Equal(t, pre.executedCount, post.executedCount, "executed action rows must be unchanged")
	assert.Equal(t, pre.snapshotsHex, post.snapshotsHex, "stored ConfigVersion snapshot bytes must be unchanged")
}

// assertRegistry verifies the registry contains exactly wantKeys, each with
// one canonical lowercase UUIDv4, all distinct.
func assertRegistry(t *testing.T, db *sql.DB, wantKeys []int64) map[int64]string {
	t.Helper()
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `SELECT route_id, public_id FROM route_identity ORDER BY route_id;`)
	require.NoError(t, err)
	defer rows.Close()

	got := make(map[int64]string)
	for rows.Next() {
		var key int64
		var publicID string
		require.NoError(t, rows.Scan(&key, &publicID))
		got[key] = publicID
	}
	require.NoError(t, rows.Err())

	keys := make([]int64, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	assert.ElementsMatch(t, wantKeys, keys, "registry must map exactly the recoverable keys")

	seen := make(map[string]int64)
	for key, publicID := range got {
		assert.Regexp(t, canonicalUUIDv4, publicID, "route %d public id must be canonical lowercase UUIDv4", key)
		if previous, dup := seen[publicID]; dup {
			assert.Failf(t, "duplicate public id", "public id %s mapped to routes %d and %d", publicID, previous, key)
		}
		seen[publicID] = key
	}
	return got
}

func TestMigration012UpgradePreservesDataAndBackfillsIdentities(t *testing.T) {
	path, pre := openV11Fixture(t)
	ctx := context.Background()

	// Real startup path: migrate the populated v11 database to 012 and run
	// the backfill, exactly as cmd/server does via store.Open.
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var version int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT version FROM schema_migrations LIMIT 1;`).Scan(&version))
	require.Equal(t, 14, version)

	assertPreserved(t, db, pre)
	mapped := assertRegistry(t, db, expectedFixtureKeys)

	// Snapshot-only ID 42 and snapshot-valid ID 10 resolve; invalid snapshot
	// ids do not.
	assert.NotEmpty(t, mapped[42], "snapshot-only route id 42 must have a mapping")
	assert.NotEmpty(t, mapped[10], "valid snapshot route id 10 must have a mapping")
	assert.NotContains(t, mapped, int64(9), "string snapshot id must not be mapped")
	assert.NotContains(t, mapped, int64(7), "float snapshot id must not be mapped")
}

func TestBackfillRouteIdentitiesIsIdempotentAcrossRestarts(t *testing.T) {
	path, pre := openV11Fixture(t)
	ctx := context.Background()

	// Simulate a crash after the 012 SQL migration is recorded but before
	// the backfill commits: apply schema migrations only, confirm the
	// registry is still empty, then restart through store.Open.
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	require.NoError(t, store.ApplyMigrations(db))
	var mappings int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_identity;`).Scan(&mappings))
	require.Equal(t, 0, mappings, "schema migration alone must not create mappings")
	require.NoError(t, db.Close())

	first, err := store.Open(ctx, path)
	require.NoError(t, err)
	firstMappings := assertRegistry(t, first, expectedFixtureKeys)
	assertPreserved(t, first, pre)
	require.NoError(t, first.Close())

	// Second restart: no UUID may change and no row may be added.
	second, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	secondMappings := assertRegistry(t, second, expectedFixtureKeys)
	assert.Equal(t, firstMappings, secondMappings, "assigned UUIDs must be stable across restarts")
	assertPreserved(t, second, pre)

	// A route created after the first backfill (e.g. before the CRUD path
	// writes identity rows itself) is picked up missing-only on the next
	// startup; existing UUIDs are untouched.
	_, err = second.ExecContext(ctx,
		`INSERT INTO route (id, name, evaluation_order, condition_composition)
		 VALUES (7, 'Late route', 7, 'AND');`)
	require.NoError(t, err)
	require.NoError(t, second.Close())

	third, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = third.Close() })
	thirdMappings := assertRegistry(t, third, append(expectedFixtureKeys, 7))
	assert.Subset(t, thirdMappings, firstMappings, "existing UUIDs must never be replaced")
}

func TestRouteIdentityRegistryIsImmutable(t *testing.T) {
	path, _ := openV11Fixture(t)
	ctx := context.Background()

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	before := assertRegistry(t, db, expectedFixtureKeys)

	_, err = db.ExecContext(ctx,
		`UPDATE route_identity SET public_id = 'ffffffff-ffff-4fff-8fff-ffffffffffff' WHERE route_id = 1;`)
	require.Error(t, err, "updating an assigned mapping must be rejected")
	assert.ErrorContains(t, err, "immutable")

	_, err = db.ExecContext(ctx, `DELETE FROM route_identity WHERE route_id = 1;`)
	require.Error(t, err, "deleting an assigned mapping must be rejected")
	assert.ErrorContains(t, err, "append-only")

	after := assertRegistry(t, db, expectedFixtureKeys)
	assert.Equal(t, before, after, "failed mutation attempts must leave mappings unchanged")
}

func TestBackfillRouteIdentitiesRejectsInvalidRegistry(t *testing.T) {
	// Each invalid registry row is stuck for the life of its database (the
	// delete trigger is the point), so every case uses a fresh fixture.
	cases := []struct {
		name      string
		publicID  string
		insertFor int64
	}{
		{name: "not a uuid", publicID: "not-a-uuid", insertFor: 1},
		{name: "wrong version", publicID: "00000000-0000-1000-8000-000000000000", insertFor: 1},
		{name: "non-RFC variant", publicID: "00000000-0000-4000-0000-000000000000", insertFor: 1},
		{name: "uppercase non-canonical", publicID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", insertFor: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := openV11Fixture(t)
			ctx := context.Background()

			// Reach the post-migration, pre-backfill state, then plant the
			// invalid row the way a corrupt write would.
			db, err := sql.Open("sqlite", path)
			require.NoError(t, err)
			require.NoError(t, store.ApplyMigrations(db))
			_, err = db.ExecContext(ctx,
				`INSERT INTO route_identity (route_id, public_id) VALUES (?, ?);`, tc.insertFor, tc.publicID)
			require.NoError(t, err, "inserting the corrupt row must be possible; validation is the gate")
			require.NoError(t, db.Close())

			_, err = store.Open(ctx, path)
			require.Error(t, err, "an invalid registry must prevent startup")
			assert.ErrorContains(t, err, "canonical UUIDv4")

			// Direct backfill call reports the same failure for callers
			// that hold an open handle.
			verify, err := sql.Open("sqlite", path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = verify.Close() })
			require.Error(t, store.BackfillRouteIdentities(ctx, verify))
		})
	}

	t.Run("duplicate public id rejected by unique constraint", func(t *testing.T) {
		path, _ := openV11Fixture(t)
		ctx := context.Background()

		db, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		require.NoError(t, store.ApplyMigrations(db))

		const publicID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		_, err = db.ExecContext(ctx,
			`INSERT INTO route_identity (route_id, public_id) VALUES (1, ?);`, publicID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`INSERT INTO route_identity (route_id, public_id) VALUES (2, ?);`, publicID)
		require.Error(t, err, "one UUID must never map to two internal keys")
		assert.ErrorContains(t, err, "UNIQUE constraint failed")
	})
}
