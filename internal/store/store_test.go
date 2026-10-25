package store_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesMigrations(t *testing.T) {
	db := openTestDB(t)

	var version int
	err := db.QueryRowContext(context.Background(),
		"SELECT version FROM schema_migrations LIMIT 1;").Scan(&version)
	require.NoError(t, err)
	require.Equal(t, 14, version)
}

func TestMigration011NormalizesLegacyRunTimestamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Pin the schema at version 10 so run rows can be inserted in the legacy
	// driver format ("2006-01-02 15:04:05.999999999 -0700 MST") that
	// migration 011 rewrites. The migration files are read from the package
	// directory (the same files the store embeds).
	src, err := iofs.New(os.DirFS("migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(10))

	legacyTime := "2026-01-01 00:00:00.500000000 +0000 UTC"
	_, err = db.Exec(`INSERT INTO run (started_at, run_type, status, completed_at)
		VALUES ('` + legacyTime + `', 'manual', 'completed', '` + legacyTime + `');`)
	require.NoError(t, err)
	// An already-normalized row must be left untouched.
	_, err = db.Exec(`INSERT INTO run (started_at, run_type, status)
		VALUES ('2026-02-01T00:00:00.000000000Z', 'manual', 'completed');`)
	require.NoError(t, err)

	require.NoError(t, store.ApplyMigrations(db))

	rows, err := db.Query(`SELECT hex(started_at), hex(completed_at) FROM run ORDER BY id;`)
	require.NoError(t, err)
	defer rows.Close()
	var got [][2]string
	for rows.Next() {
		var startedAt, completedAt sql.NullString
		require.NoError(t, rows.Scan(&startedAt, &completedAt))
		got = append(got, [2]string{decodeHex(t, startedAt.String), decodeHex(t, completedAt.String)})
	}
	require.Len(t, got, 2)
	assert.Equal(t, "2026-01-01T00:00:00.500000000Z", got[0][0])
	assert.Equal(t, "2026-01-01T00:00:00.500000000Z", got[0][1])
	assert.Equal(t, "2026-02-01T00:00:00.000000000Z", got[1][0])
}

// decodeHex decodes a hex-encoded SQLite TEXT value so the raw stored bytes
// can be asserted; the driver re-formats time-like TEXT on scan, which would
// hide the storage format this test verifies.
func decodeHex(t *testing.T, h string) string {
	t.Helper()
	if h == "" {
		return ""
	}
	b, err := hex.DecodeString(h)
	require.NoError(t, err)
	return string(b)
}

// TestMigration013UpgradeSeedsNullTimestampAndPreservesCorpus proves the
// ADR-015 upgrade contract: a populated version-012 database gains exactly
// one singleton corpus_import_metadata row with a NULL timestamp, no
// historical time is backfilled from existing rows, and every corpus row is
// preserved unchanged (ARS-002 verification item 10).
func TestMigration013UpgradeSeedsNullTimestampAndPreservesCorpus(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v12.db")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)

	// Pin the database at the pre-migration schema version 012.
	src, err := iofs.New(os.DirFS("migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(12))

	// An existing corpus imported before the metadata table existed.
	_, err = db.Exec(`INSERT INTO title_fingerprint
		(video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference, import_batch_id)
		VALUES
		('v1', 'jazz live session', 'jazz live session', 'UC1', 'user_import', 1, 'batch-old'),
		('v2', 'rock anthem', 'rock anthem', 'UC2', 'api_discovery', 0, NULL);`)
	require.NoError(t, err)
	// A timestamp-like value in an existing row must never be mistaken for
	// import-completion evidence.
	_, err = db.Exec(`UPDATE title_fingerprint
		SET first_seen_at = '2020-01-02T03:04:05.000000000Z' WHERE video_id = 'v1';`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Real startup path: store.Open applies migration 013 before serving.
	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var version int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT version FROM schema_migrations LIMIT 1;`).Scan(&version))
	require.Equal(t, 14, version)

	// Exactly one singleton row, seeded NULL: no backfill, no sentinel date.
	var rowCount, nullCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*), SUM(CASE WHEN last_successful_corpus_import_at IS NULL THEN 1 ELSE 0 END)
		 FROM corpus_import_metadata WHERE singleton_id = 1;`).Scan(&rowCount, &nullCount))
	require.Equal(t, 1, rowCount, "the singleton row must exist exactly once")
	require.Equal(t, 1, nullCount, "the timestamp must be seeded NULL, never backfilled")

	// No additional rows are valid.
	var totalRows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM corpus_import_metadata;`).Scan(&totalRows))
	require.Equal(t, 1, totalRows)

	// The existing corpus is preserved byte-for-byte. first_seen_at is read
	// through hex() because the driver re-formats time-like TEXT on scan.
	type fpRow struct {
		videoID, fingerprint, tokens, source, batchID string
		channelID                                     sql.NullString
		firstSeenAtHex                                string
		isDupRef                                      int
	}
	var got []fpRow
	rows, err := db.QueryContext(ctx,
		`SELECT video_id, fingerprint, fingerprint_tokens, channel_id, source,
		        hex(first_seen_at), is_duplicate_reference, COALESCE(import_batch_id, '')
		 FROM title_fingerprint ORDER BY video_id;`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var r fpRow
		require.NoError(t, rows.Scan(&r.videoID, &r.fingerprint, &r.tokens, &r.channelID,
			&r.source, &r.firstSeenAtHex, &r.isDupRef, &r.batchID))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 2)
	require.Equal(t, "v1", got[0].videoID)
	require.Equal(t, "jazz live session", got[0].fingerprint)
	require.Equal(t, "jazz live session", got[0].tokens)
	require.Equal(t, sql.NullString{String: "UC1", Valid: true}, got[0].channelID)
	require.Equal(t, "user_import", got[0].source)
	require.Equal(t, 1, got[0].isDupRef)
	require.Equal(t, "batch-old", got[0].batchID)
	require.Equal(t, "2020-01-02T03:04:05.000000000Z",
		decodeHex(t, got[0].firstSeenAtHex), "migration 013 must not rewrite corpus rows")

	require.Equal(t, "v2", got[1].videoID)
	require.Equal(t, "rock anthem", got[1].fingerprint)
	require.Equal(t, sql.NullString{String: "UC2", Valid: true}, got[1].channelID)
	require.Equal(t, "api_discovery", got[1].source)
	require.Zero(t, got[1].isDupRef)
	require.Empty(t, got[1].batchID)
	require.NotEmpty(t, got[1].firstSeenAtHex)

	// The metadata row carries nothing but the timestamp: no counts, ids, or
	// history columns were added alongside it.
	cols, err := db.QueryContext(ctx,
		`SELECT name FROM pragma_table_info('corpus_import_metadata') ORDER BY cid;`)
	require.NoError(t, err)
	defer cols.Close()
	var names []string
	for cols.Next() {
		var name string
		require.NoError(t, cols.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, cols.Err())
	require.Equal(t, []string{"singleton_id", "last_successful_corpus_import_at"}, names)
}

func TestSchemaContainsAllEntities(t *testing.T) {
	db := openTestDB(t)

	rows, err := db.QueryContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%';")
	require.NoError(t, err)
	defer rows.Close()

	tables := make(map[string]struct{})
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables[name] = struct{}{}
	}
	require.NoError(t, rows.Err())

	expected := []string{
		"advisory",
		"auth_tokens",
		"channel",
		"condition_audit",
		"config_version",
		"corpus_import_metadata",
		"deferred_item",
		"duplicate_match",
		"error_entry",
		"executed_action",
		"quota_ledger",
		"quota_settings",
		"retention_playlist",
		"retention_signal",
		"route",
		"route_action",
		"route_condition",
		"route_decision",
		"route_exclusion",
		"route_identity",
		"run",
		"scheduler_settings",
		"settings",
		"title_fingerprint",
		"transient_metadata",
		"video",
	}
	for _, name := range expected {
		require.Contains(t, tables, name, "missing table %s", name)
	}
}

func TestStoreAHasTTLColumns(t *testing.T) {
	db := openTestDB(t)

	for _, col := range []string{"fetched_at", "expires_at", "refreshed_at"} {
		var found int
		err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM pragma_table_info('transient_metadata') WHERE name = ?;", col).Scan(&found)
		require.NoError(t, err)
		require.Equal(t, 1, found, "transient_metadata should have column %s", col)
	}
}

func TestStoreBHasNoTTLColumns(t *testing.T) {
	db := openTestDB(t)

	rows, err := db.QueryContext(context.Background(),
		"SELECT name FROM pragma_table_info('title_fingerprint');")
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		require.NotContains(t, []string{"fetched_at", "refreshed_at", "expires_at"}, name,
			"title_fingerprint must not carry TTL columns")
	}
	require.NoError(t, rows.Err())
}

func TestExecutedActionUniqueConstraint(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	res, err := db.ExecContext(ctx,
		"INSERT INTO config_version (snapshot) VALUES (?);", "{}")
	require.NoError(t, err)
	cfgID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx,
		"INSERT INTO run (run_type, status, config_version_id) VALUES (?, ?, ?);",
		"manual", "running", cfgID)
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx,
		"INSERT INTO video (video_id, channel_id, processing_state, final_outcome) VALUES (?, ?, ?, ?);",
		"abc123", "UCxyz", "not_yet_evaluated", "not_yet_evaluated")
	require.NoError(t, err)
	videoID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx,
		"INSERT INTO route (name, evaluation_order, condition_composition, created_at, updated_at) VALUES (?, ?, ?, ?, ?);",
		"music", 1, "AND", now, now)
	require.NoError(t, err)
	routeID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx,
		"INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision) VALUES (?, ?, ?, ?, ?);",
		videoID, runID, routeID, 1, "matched")
	require.NoError(t, err)
	decisionID, err := res.LastInsertId()
	require.NoError(t, err)

	insertAction := func(decisionID, runID, videoID int64) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type,
			                              target_playlist_id, outcome, executed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?);`,
			decisionID, runID, videoID, "playlist_insert", "PLtest", "succeeded", now)
		return err
	}

	require.NoError(t, insertAction(decisionID, runID, videoID))
	err = insertAction(decisionID, runID, videoID)
	require.Error(t, err)
	require.ErrorContains(t, err, "UNIQUE constraint failed")
}

func TestFTS5VirtualTableExistsAndIndexes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var ftsCount int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = 'title_fingerprint_fts';").Scan(&ftsCount)
	require.NoError(t, err)
	require.Equal(t, 1, ftsCount)

	// Insert a fingerprint and verify it becomes searchable through FTS5.
	_, err = db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source)
		 VALUES (?, ?, ?, ?, ?);`,
		"v1", "jazz live session", "jazz live session", "UC1", "api_discovery")
	require.NoError(t, err)

	var count int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM title_fingerprint_fts WHERE fingerprint_tokens MATCH ?;",
		"jazz").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
