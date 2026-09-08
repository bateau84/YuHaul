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
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "dest.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Apply only the schema required by the importer. This keeps the
	// migration package tests independent of the full store migration set,
	// which is owned by the data-layer task.
	_, err = db.ExecContext(context.Background(), destSchemaDDL)
	require.NoError(t, err)
	return db
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
		`SELECT video_id, fingerprint, source, import_batch_id
		 FROM title_fingerprint
		 ORDER BY video_id;`)
	require.NoError(t, err)
	defer rows.Close()

	var found []string
	for rows.Next() {
		var videoID, fp, source, batch string
		require.NoError(t, rows.Scan(&videoID, &fp, &source, &batch))
		require.Equal(t, "user_import", source)
		require.Equal(t, "batch-001", batch)
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
