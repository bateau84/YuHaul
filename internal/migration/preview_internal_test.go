package migration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOpenSourceReadOnlyRejectsWrites proves the DSN used to open a staged
// upload (file:...?mode=ro) yields a genuinely read-only connection: reads
// succeed, writes are rejected, and the on-disk file is left unchanged. This is
// the read-only guarantee behind ARS-002's "opened read-only" requirement.
func TestOpenSourceReadOnlyRejectsWrites(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.db")

	// Build a small valid source database.
	rw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = rw.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT);
		 INSERT INTO videos (video_id, title) VALUES ('v1', 'T');`)
	require.NoError(t, err)
	require.NoError(t, rw.Close())

	ro, err := OpenSourceReadOnly(ctx, path)
	require.NoError(t, err)

	// Reads work.
	var n int
	require.NoError(t, ro.QueryRowContext(ctx, "SELECT COUNT(*) FROM videos;").Scan(&n))
	require.Equal(t, 1, n)

	// Writes are rejected by the read-only connection.
	_, err = ro.ExecContext(ctx, "INSERT INTO videos (video_id, title) VALUES ('v2', 'X');")
	require.Error(t, err, "read-only connection must reject writes")
	require.NoError(t, ro.Close())

	// The on-disk file is unchanged, and no sidecar (-wal/-shm) was created.
	reopen, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	require.NoError(t, reopen.QueryRowContext(ctx, "SELECT COUNT(*) FROM videos;").Scan(&n))
	require.Equal(t, 1, n, "read-only preview must not modify the source file")
	require.NoError(t, reopen.Close())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "read-only open must not create sidecar files")
	require.Equal(t, "source.db", entries[0].Name())
}
