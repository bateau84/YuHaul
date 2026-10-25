package migration_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bateau84/YuHaul/internal/fingerprint"
	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func openDestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := openDestDBFile(t)
	return db
}

// openDestDBFile is openDestDB plus the on-disk path, so a test can close the
// destination and reopen the same file to prove committed rows are durable.
func openDestDBFile(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dest.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Apply only the schema required by the importer. This keeps the
	// migration package tests independent of the full store migration set,
	// which is owned by the data-layer task. The credit-evidence tables are
	// added so tests can assert that import creates no discovery-credit or
	// route evidence.
	_, err = db.ExecContext(context.Background(), destSchemaDDL+creditEvidenceSchemaDDL)
	require.NoError(t, err)
	return db, path
}

const destSchemaDDL = `
CREATE TABLE title_fingerprint (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id TEXT NOT NULL UNIQUE,
    fingerprint TEXT NOT NULL,
    fingerprint_tokens TEXT,
    channel_id TEXT,
    source TEXT NOT NULL CHECK(source IN ('api_discovery', 'user_import')),
    first_seen_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    is_duplicate_reference BOOLEAN NOT NULL DEFAULT 0,
    import_batch_id TEXT
);

CREATE TABLE transient_metadata (
    video_id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    channel_name TEXT,
    description TEXT,
    published_at TIMESTAMP,
    duration_seconds INTEGER,
    thumbnail_url TEXT,
    fetched_at TIMESTAMP NOT NULL,
    refreshed_at TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT 1
);

-- Mirrors internal/store/migrations/013_corpus_import_metadata.up.sql: the
-- importer's success marker requires the singleton row to exist, exactly as
-- it does on the real migrated destination.
CREATE TABLE corpus_import_metadata (
    singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    last_successful_corpus_import_at TEXT
);

INSERT INTO corpus_import_metadata (singleton_id, last_successful_corpus_import_at)
VALUES (1, NULL);
`

// creditEvidenceSchemaDDL holds the destination tables that carry discovery
// credit (channel counters, the video discovery record) or route/duplicate
// provenance. They exist only so tests can prove that importing writes none of
// them; they mirror the corresponding tables in
// internal/store/migrations/001_initial_schema.up.sql, minus the `run` and
// `route` parent tables and their foreign-key clauses, which no import path
// touches.
const creditEvidenceSchemaDDL = `
CREATE TABLE channel (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id TEXT NOT NULL UNIQUE,
    channel_name TEXT,
    first_discovered_at TIMESTAMP,
    last_discovered_at TIMESTAMP,
    total_discovered INTEGER NOT NULL DEFAULT 0,
    total_routed INTEGER NOT NULL DEFAULT 0,
    total_retained INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE video (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id TEXT NOT NULL UNIQUE,
    channel_id TEXT NOT NULL,
    discovered_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    discovered_in_run_id INTEGER,
    processing_state TEXT NOT NULL,
    final_outcome TEXT
);

CREATE TABLE duplicate_match (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL,
    matched_fingerprint_id INTEGER REFERENCES title_fingerprint(id) ON DELETE SET NULL,
    similarity_score REAL NOT NULL,
    match_type TEXT NOT NULL,
    source_route_id INTEGER,
    threshold_used REAL NOT NULL
);

CREATE TABLE route_decision (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL,
    run_id INTEGER NOT NULL,
    route_id INTEGER NOT NULL,
    evaluation_order INTEGER NOT NULL,
    decision TEXT NOT NULL,
    UNIQUE(video_id, run_id, route_id)
);

CREATE TABLE executed_action (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER NOT NULL,
    video_id INTEGER NOT NULL,
    action_type TEXT NOT NULL,
    target_playlist_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    executed_at TIMESTAMP,
    UNIQUE(run_id, video_id, target_playlist_id, action_type)
);
`

func openSourceDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "source.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDiscoverSchemaFindsVideoTable(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (
			id INTEGER PRIMARY KEY,
			video_id TEXT NOT NULL,
			title TEXT NOT NULL,
			channel_id TEXT
		);`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)
	require.Equal(t, "videos", schema.TableName)
	require.Equal(t, "video_id", schema.VideoIDColumn)
	require.Equal(t, "title", schema.TitleColumn)
	require.Equal(t, "channel_id", schema.ChannelIDColumn)
}

func TestDiscoverSchemaCaseInsensitiveColumns(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE HISTORY (
			Video_ID TEXT,
			TITLE TEXT,
			ChannelID TEXT
		);`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)
	require.Equal(t, "HISTORY", schema.TableName)
	require.Equal(t, "Video_ID", schema.VideoIDColumn)
	require.Equal(t, "TITLE", schema.TitleColumn)
	require.Equal(t, "ChannelID", schema.ChannelIDColumn)
}

func TestDiscoverSchemaNoMatchingTable(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE unrelated (id INTEGER PRIMARY KEY, name TEXT);`)
	require.NoError(t, err)

	_, err = migration.DiscoverSchema(ctx, src)
	require.Error(t, err)
	require.True(t, errors.Is(err, migration.ErrSchemaNotDiscoverable),
		"expected ErrSchemaNotDiscoverable, got %v", err)
}

func TestDiscoverSchemaMultipleMatches(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE a (video_id TEXT, title TEXT);
		 CREATE TABLE b (video_id TEXT, title TEXT);`)
	require.NoError(t, err)

	_, err = migration.DiscoverSchema(ctx, src)
	require.Error(t, err)
	require.True(t, errors.Is(err, migration.ErrSchemaNotDiscoverable),
		"expected ErrSchemaNotDiscoverable for ambiguous schema, got %v", err)
}

func TestImporterCreatesUserImportFingerprints(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT);
		 INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('v2', 'Rock Anthem (Official Video)'),
			('v3', '(Official Video)');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	imp := migration.NewImporter(src, dest, "batch-001")
	result, err := imp.Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 3, result.Read)
	require.Equal(t, 2, result.Inserted)
	require.Equal(t, 0, result.Skipped)
	require.Equal(t, 1, result.Errors) // v3 normalizes to empty tokens

	rows, err := dest.QueryContext(ctx,
		`SELECT video_id, fingerprint, source, import_batch_id, is_duplicate_reference
		 FROM title_fingerprint
		 ORDER BY video_id;`)
	require.NoError(t, err)
	defer rows.Close()

	var found []string
	for rows.Next() {
		var videoID, fp, source, batch string
		var isRef int
		require.NoError(t, rows.Scan(&videoID, &fp, &source, &batch, &isRef))
		require.Equal(t, "user_import", source)
		require.Equal(t, "batch-001", batch)
		require.Equal(t, 1, isRef,
			"imported row must be an exact/fuzzy matching candidate (OC-004 G1)")
		found = append(found, videoID+":"+fp)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{
		"v1:jazz live session",
		"v2:anthem rock",
	}, found)
}

func TestImporterIsIdempotent(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT);
		 INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	imp := migration.NewImporter(src, dest, "batch-001")
	result1, err := imp.Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 1, result1.Inserted)

	result2, err := imp.Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 1, result2.Read)
	require.Equal(t, 0, result2.Inserted)
	require.Equal(t, 1, result2.Skipped)

	var count int
	err = dest.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM title_fingerprint WHERE video_id = 'v1';").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestImporterSkipsEmptyVideoID(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT);
		 INSERT INTO videos (video_id, title) VALUES ('', 'Some Title'), ('v1', 'Valid Title');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	imp := migration.NewImporter(src, dest, "batch-001")
	result, err := imp.Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 2, result.Read)
	require.Equal(t, 1, result.Inserted)
	require.Equal(t, 1, result.Errors)
}

func TestImporterDoesNotCreateTransientMetadata(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT);
		 INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	imp := migration.NewImporter(src, dest, "batch-001")
	_, err = imp.Run(ctx, schema)
	require.NoError(t, err)

	var count int
	err = dest.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM transient_metadata WHERE video_id = 'v1';").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "migration must not create transient metadata rows")
}

func TestImporterUsesSharedFingerprintNormalization(t *testing.T) {
	// Imported titles must produce the exact same canonical fingerprint as the
	// native discovery path (internal/fingerprint), satisfying INV-6.
	fp, _, ok := fingerprint.FromTitle("Get Lucky (Daft Punk) [Official Video]")
	require.True(t, ok)

	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT);
		 INSERT INTO videos (video_id, title) VALUES ('v1', 'Get Lucky (Daft Punk) [Official Video]');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	imp := migration.NewImporter(src, dest, "batch-001")
	_, err = imp.Run(ctx, schema)
	require.NoError(t, err)

	var got string
	err = dest.QueryRowContext(ctx,
		"SELECT fingerprint FROM title_fingerprint WHERE video_id = 'v1';").Scan(&got)
	require.NoError(t, err)
	require.Equal(t, fp, got)
}

func TestImporterPreservesChannelID(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		 INSERT INTO videos (video_id, title, channel_id) VALUES ('v1', 'Title', 'UCchannel');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	imp := migration.NewImporter(src, dest, "batch-001")
	_, err = imp.Run(ctx, schema)
	require.NoError(t, err)

	var channelID string
	err = dest.QueryRowContext(ctx,
		"SELECT channel_id FROM title_fingerprint WHERE video_id = 'v1';").Scan(&channelID)
	require.NoError(t, err)
	require.Equal(t, "UCchannel", channelID)
}

// fingerprintRow is the stored state of every title_fingerprint column except
// the immutable autoincrement id. Comparing two snapshots is how a test proves
// an import retry mutated nothing.
type fingerprintRow struct {
	videoID        string
	fingerprint    string
	tokens         sql.NullString
	channelID      sql.NullString
	source         string
	firstSeenAt    string
	isDuplicateRef int
	importBatchID  sql.NullString
}

func readFingerprint(t *testing.T, ctx context.Context, db *sql.DB, videoID string) fingerprintRow {
	t.Helper()
	var r fingerprintRow
	err := db.QueryRowContext(ctx,
		`SELECT video_id, fingerprint, fingerprint_tokens, channel_id, source,
		        first_seen_at, is_duplicate_reference, import_batch_id
		 FROM title_fingerprint
		 WHERE video_id = ?;`,
		videoID,
	).Scan(&r.videoID, &r.fingerprint, &r.tokens, &r.channelID, &r.source,
		&r.firstSeenAt, &r.isDuplicateRef, &r.importBatchID)
	require.NoError(t, err)
	return r
}

func TestImporterSameVideoIDSkipLeavesExistingRowUnchanged(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	// An existing native observation that is not a duplicate reference: the
	// import retry must neither promote nor rewrite it (BR-009 AC8, OC-004
	// failure semantics).
	_, err := dest.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference, import_batch_id)
		 VALUES ('v1', 'native fingerprint', 'native tokens', 'UCnative', 'api_discovery', 0, NULL);`)
	require.NoError(t, err)

	_, err = src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		 INSERT INTO videos (video_id, title, channel_id) VALUES ('v1', 'Jazz Live Session', 'UCimport');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	before := readFingerprint(t, ctx, dest, "v1")

	imp := migration.NewImporter(src, dest, "batch-002")
	result, err := imp.Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 1, result.Read)
	require.Equal(t, 0, result.Inserted)
	require.Equal(t, 1, result.Skipped)
	require.Zero(t, result.Errors)

	require.Equal(t, before, readFingerprint(t, ctx, dest, "v1"),
		"same-video-ID import skip must leave the existing row untouched")
}

func TestImporterEligibilityIsNotDiscoveryCredit(t *testing.T) {
	// OC-004 guarantee 3 and BR-010 AC6: a valid imported row becomes an
	// exact/fuzzy candidate, but import creates no route, action, channel
	// counter, or earliest-discovery evidence. The optional source channel_id
	// stays a plain association.
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		 INSERT INTO videos (video_id, title, channel_id) VALUES ('v1', 'Jazz Live Session', 'UCchannel');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)

	imp := migration.NewImporter(src, dest, "batch-001")
	result, err := imp.Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 1, result.Inserted)

	got := readFingerprint(t, ctx, dest, "v1")
	require.Equal(t, 1, got.isDuplicateRef,
		"imported row must be eligible for exact/fuzzy matching")
	require.Equal(t, "user_import", got.source)
	require.Equal(t, "batch-001", got.importBatchID.String)
	require.Equal(t, "UCchannel", got.channelID.String,
		"channel_id is retained as an association only")

	for _, tc := range []struct {
		table string
		query string
	}{
		{"video", "SELECT COUNT(*) FROM video;"},
		{"channel", "SELECT COUNT(*) FROM channel;"},
		{"duplicate_match", "SELECT COUNT(*) FROM duplicate_match;"},
		{"route_decision", "SELECT COUNT(*) FROM route_decision;"},
		{"executed_action", "SELECT COUNT(*) FROM executed_action;"},
		{"transient_metadata", "SELECT COUNT(*) FROM transient_metadata;"},
	} {
		var count int
		require.NoError(t, dest.QueryRowContext(ctx, tc.query).Scan(&count))
		require.Zero(t, count, "import must not create %s rows", tc.table)
	}
}

func TestImporterRunRequiresSchema(t *testing.T) {
	ctx := context.Background()
	imp := migration.NewImporter(openSourceDB(t), openDestDB(t), "batch")
	_, err := imp.Run(ctx, nil)
	require.Error(t, err)
}

func TestImporterNoChannelIDColumn(t *testing.T) {
	ctx := context.Background()
	src := openSourceDB(t)
	dest := openDestDB(t)

	_, err := src.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT);
		 INSERT INTO videos (video_id, title) VALUES ('v1', 'Title');`)
	require.NoError(t, err)

	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)
	require.Empty(t, schema.ChannelIDColumn)

	imp := migration.NewImporter(src, dest, "batch-001")
	result, err := imp.Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 1, result.Inserted)
}
