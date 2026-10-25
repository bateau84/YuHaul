package migration_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/stretchr/testify/require"
)

// stageFile copies content into a real staged file through the Stager, the
// same way the web layer stages an upload before preflight. The caller owns
// the returned path.
func stageFile(t *testing.T, content []byte) string {
	t.Helper()
	stager, _ := newTestStager(t, migration.DefaultMaxUploadBytes)
	path, err := stager.Stage(context.Background(), bytes.NewReader(content))
	require.NoError(t, err)
	return path
}

// TestPreflightValidatesAndCounts proves Preflight returns the discovered
// schema and the complete source-row count used as the import total, and that
// it leaves the staged file for its caller (the accepted job owns it).
func TestPreflightValidatesAndCounts(t *testing.T) {
	ctx := context.Background()
	src := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		INSERT INTO videos (video_id, title, channel_id) VALUES
			('v1', 'One', 'UC1'), ('v2', 'Two', 'UC2'), ('v3', 'Three', 'UC3');`)

	staged := stageFile(t, src)
	defer func() { _ = os.Remove(staged) }()

	schema, total, err := migration.Preflight(ctx, staged)
	require.NoError(t, err)
	require.Equal(t, "videos", schema.TableName)
	require.Equal(t, "video_id", schema.VideoIDColumn)
	require.Equal(t, "title", schema.TitleColumn)
	require.Equal(t, "channel_id", schema.ChannelIDColumn)
	require.EqualValues(t, 3, total, "total is the complete source-table row count")

	_, err = os.Stat(staged)
	require.NoError(t, err, "preflight must not remove the staged file it does not own")
}

// TestPreflightRejectsBadSources proves the pre-acceptance validation classes
// match the web layer's 400/413 contract: unreadable content is
// ErrInvalidSource and a missing or ambiguous schema is
// ErrSchemaNotDiscoverable.
func TestPreflightRejectsBadSources(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		content  []byte
		sentinel error
	}{
		{"not sqlite", []byte("this is not a sqlite database at all"), migration.ErrInvalidSource},
		{"no matching table",
			buildSourceBytes(t, `CREATE TABLE junk (foo TEXT, bar TEXT);`),
			migration.ErrSchemaNotDiscoverable},
		{"ambiguous tables",
			buildSourceBytes(t, `CREATE TABLE a (video_id TEXT, title TEXT);
				CREATE TABLE b (video_id TEXT, title TEXT);`),
			migration.ErrSchemaNotDiscoverable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			staged := stageFile(t, tc.content)
			defer func() { _ = os.Remove(staged) }()
			_, _, err := migration.Preflight(ctx, staged)
			require.ErrorIs(t, err, tc.sentinel, "preflight must classify the source rejection")
		})
	}
}

// TestPreflightRejectsMissingTableColumns proves a readable database without
// the required video_id/title columns is rejected before any operation is
// created (ARS-002: unsupported source → 400).
func TestPreflightRejectsMissingTableColumns(t *testing.T) {
	staged := stageFile(t, buildSourceBytes(t, `CREATE TABLE videos (name TEXT);`))
	defer func() { _ = os.Remove(staged) }()

	_, _, err := migration.Preflight(context.Background(), staged)
	require.ErrorIs(t, err, migration.ErrSchemaNotDiscoverable)
}

// TestPreflightDoesNotWriteDestination proves preflight leaves the
// destination untouched (ARS-002: validation happens before any write).
func TestPreflightDoesNotWriteDestination(t *testing.T) {
	ctx := context.Background()
	dest := openDestDB(t)
	before := destSnapshot(t, dest)

	src := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT);
		INSERT INTO videos VALUES ('v1', 'One');`)
	staged := stageFile(t, src)
	defer func() { _ = os.Remove(staged) }()

	_, total, err := migration.Preflight(ctx, staged)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)

	require.Equal(t, before, destSnapshot(t, dest), "preflight must not write to the destination")
}

// TestOpenSourceReadOnlyRefusesWrites proves the connection preflight and the
// import worker use cannot mutate the staged source (ARS-002: read-only
// source).
func TestOpenSourceReadOnlyRefusesWrites(t *testing.T) {
	ctx := context.Background()
	src := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT);`)
	staged := stageFile(t, src)
	defer func() { _ = os.Remove(staged) }()

	db, err := migration.OpenSourceReadOnly(ctx, staged)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec(`INSERT INTO videos VALUES ('hack', 'hack');`)
	require.Error(t, err, "a read-only source connection must refuse writes")
}
