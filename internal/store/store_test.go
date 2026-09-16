package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
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
	require.Equal(t, 10, version)
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
