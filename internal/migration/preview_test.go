package migration_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/stretchr/testify/require"
)

// buildSourceBytes materializes a SQLite database from the given DDL/DML and
// returns its bytes, so a test can feed it to a Stager as an untrusted upload
// stream. An empty ddl produces a database with no user tables.
func buildSourceBytes(t *testing.T, ddl string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	if ddl != "" {
		_, err = db.ExecContext(context.Background(), ddl)
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func newTestStager(t *testing.T, maxBytes int64) (*migration.Stager, string) {
	t.Helper()
	dir := t.TempDir()
	stager, err := migration.NewStager(dir, maxBytes)
	require.NoError(t, err)
	return stager, dir
}

func requireStagingDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "staging directory must contain no leftover files")
}

// destSnapshot captures every title_fingerprint row across all columns plus
// the row counts of the tables an import/preview must never touch. Comparing
// two snapshots proves the destination was not mutated.
func destSnapshot(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	snap := map[string]string{}

	rows, err := db.Query(`SELECT COALESCE(video_id,''), COALESCE(fingerprint,''),
	        COALESCE(fingerprint_tokens,''), COALESCE(channel_id,''), COALESCE(source,''),
	        COALESCE(first_seen_at,''), COALESCE(is_duplicate_reference,''), COALESCE(import_batch_id,'')
	 FROM title_fingerprint ORDER BY video_id;`)
	require.NoError(t, err)
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var a, b, c, d, e, f, g, h string
		require.NoError(t, rows.Scan(&a, &b, &c, &d, &e, &f, &g, &h))
		fmt.Fprintf(&sb, "%s|%s|%s|%s|%s|%s|%s|%s\n", a, b, c, d, e, f, g, h)
	}
	require.NoError(t, rows.Err())
	snap["title_fingerprint"] = sb.String()

	for _, tbl := range []string{"transient_metadata", "video", "channel", "duplicate_match", "route_decision", "executed_action"} {
		var n int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+tbl+";").Scan(&n))
		snap[tbl] = strconv.Itoa(n)
	}
	return snap
}

func TestPreviewValidSource(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	src := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		INSERT INTO videos (video_id, title, channel_id) VALUES
			('v1', 'Jazz Live Session', 'UC1'),
			('v2', 'Rock Anthem', 'UC2'),
			('v3', 'Blue Note', 'UC3');`)
	// v2 already exists in the destination corpus.
	_, err := dest.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference)
		 VALUES ('v2', 'rock anthem', 'rock anthem', 'UC2', 'api_discovery', 0);`)
	require.NoError(t, err)

	before := destSnapshot(t, dest)
	res, err := stager.Preview(ctx, bytes.NewReader(src), dest)
	require.NoError(t, err)

	require.Equal(t, int64(3), res.SourceRowCount)
	require.Equal(t, int64(1), res.AlreadyPresentCount, "only v2 already exists in the destination")
	require.ElementsMatch(t, []string{"Jazz Live Session", "Rock Anthem", "Blue Note"}, res.SampleTitles)

	require.Equal(t, before, destSnapshot(t, dest), "preview must not mutate the destination")
	requireStagingDirEmpty(t, dir)
}

func TestPreviewSampleTitleLimit(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	var sb strings.Builder
	sb.WriteString("CREATE TABLE videos (video_id TEXT, title TEXT);\n")
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&sb, "INSERT INTO videos (video_id, title) VALUES ('v%d', 'Title %d');\n", i, i)
	}
	src := buildSourceBytes(t, sb.String())

	res, err := stager.Preview(ctx, bytes.NewReader(src), dest)
	require.NoError(t, err)
	require.Equal(t, int64(15), res.SourceRowCount)
	require.Len(t, res.SampleTitles, migration.PreviewSampleTitleLimit,
		"preview must cap sample titles at ten")
	requireStagingDirEmpty(t, dir)
}

func TestPreviewAlreadyPresentCountsSourceRows(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	// v1 appears twice in the source and already exists in the destination;
	// each matching source row counts, per ARS-002 point-in-time semantics.
	src := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT);
		INSERT INTO videos (video_id, title) VALUES
			('v1', 'First'),
			('v1', 'Second'),
			('v2', 'Third');`)
	_, err := dest.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference)
		 VALUES ('v1', 'first', 'first', '', 'api_discovery', 0);`)
	require.NoError(t, err)

	res, err := stager.Preview(ctx, bytes.NewReader(src), dest)
	require.NoError(t, err)
	require.Equal(t, int64(3), res.SourceRowCount)
	require.Equal(t, int64(2), res.AlreadyPresentCount, "both v1 source rows match the destination")
	requireStagingDirEmpty(t, dir)
}

func TestPreviewMissingSchema(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	src := buildSourceBytes(t, `CREATE TABLE unrelated (id INTEGER PRIMARY KEY, name TEXT);`)
	before := destSnapshot(t, dest)

	_, err := stager.Preview(ctx, bytes.NewReader(src), dest)
	require.Error(t, err)
	require.True(t, errors.Is(err, migration.ErrSchemaNotDiscoverable),
		"expected ErrSchemaNotDiscoverable, got %v", err)
	require.Equal(t, before, destSnapshot(t, dest))
	requireStagingDirEmpty(t, dir)
}

func TestPreviewAmbiguousSchema(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	src := buildSourceBytes(t, `CREATE TABLE a (video_id TEXT, title TEXT);
		CREATE TABLE b (video_id TEXT, title TEXT);`)
	before := destSnapshot(t, dest)

	_, err := stager.Preview(ctx, bytes.NewReader(src), dest)
	require.Error(t, err)
	require.True(t, errors.Is(err, migration.ErrSchemaNotDiscoverable),
		"expected ErrSchemaNotDiscoverable for ambiguous schema, got %v", err)
	require.Equal(t, before, destSnapshot(t, dest))
	requireStagingDirEmpty(t, dir)
}

func TestPreviewEmptyFile(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	_, err := stager.Preview(ctx, bytes.NewReader(nil), dest)
	require.ErrorIs(t, err, migration.ErrUploadEmpty)
	requireStagingDirEmpty(t, dir)
}

func TestPreviewCorruptFile(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	// Garbage that is not a SQLite database (and longer than the 16-byte
	// header), so it fails the magic-header validation.
	garbage := []byte("this is definitely not a sqlite database, just padded garbage bytes")
	_, err := stager.Preview(ctx, bytes.NewReader(garbage), dest)
	require.ErrorIs(t, err, migration.ErrInvalidSource)
	requireStagingDirEmpty(t, dir)
}

func TestPreviewTruncatedFile(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	full := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT);
		INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
	require.Greater(t, len(full), 16, "valid source must be larger than the SQLite header")
	truncated := full[:16] // header signature only; the rest of the database is gone.

	before := destSnapshot(t, dest)
	_, err := stager.Preview(ctx, bytes.NewReader(truncated), dest)
	require.Error(t, err, "truncated source must be rejected")
	// Truncated content is rejected either as malformed content or as an
	// unreadable/absent schema; both are source rejections (web layer: 400).
	require.True(t, errors.Is(err, migration.ErrInvalidSource) || errors.Is(err, migration.ErrSchemaNotDiscoverable),
		"expected a source-rejection error, got %v", err)
	require.Equal(t, before, destSnapshot(t, dest))
	requireStagingDirEmpty(t, dir)
}

func TestPreviewNoDestinationWrites(t *testing.T) {
	// Dedicated end-to-end guard: a valid preview leaves every destination
	// table byte-for-byte unchanged, including the credit-evidence tables an
	// import must never populate.
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	dest := openDestDB(t)

	_, err := dest.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference)
		 VALUES ('existing', 'fp', 'tokens', 'UCx', 'api_discovery', 0);`)
	require.NoError(t, err)

	src := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		INSERT INTO videos (video_id, title, channel_id) VALUES
			('existing', 'Jazz', 'UC1'),
			('newid', 'Rock', 'UC2');`)

	before := destSnapshot(t, dest)
	res, err := stager.Preview(ctx, bytes.NewReader(src), dest)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.AlreadyPresentCount)
	require.Equal(t, before, destSnapshot(t, dest))
	requireStagingDirEmpty(t, dir)
}

func TestPreviewRemovesStagedFileOnPanic(t *testing.T) {
	// A nil destination panics inside loadDestVideoIDs (database/sql
	// dereferences the nil *sql.DB). The defer in Preview must still remove the
	// staged file while the panic unwinds, proving panic-safe cleanup.
	ctx := context.Background()
	stager, dir := newTestStager(t, migration.DefaultMaxUploadBytes)
	src := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT);
		INSERT INTO videos (video_id, title) VALUES ('v1', 'T');`)

	defer func() {
		r := recover()
		require.NotNil(t, r, "nil destination should panic to exercise panic-safe cleanup")
		requireStagingDirEmpty(t, dir)
	}()
	_, _ = stager.Preview(ctx, bytes.NewReader(src), nil)
}

func TestStageEnforcesSizeLimitBoundary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stager, err := migration.NewStager(dir, 10)
	require.NoError(t, err)

	// Over the cap by one byte: rejected and removed.
	_, err = stager.Stage(ctx, bytes.NewReader(bytes.Repeat([]byte{'x'}, 11)))
	require.ErrorIs(t, err, migration.ErrUploadTooLarge)
	requireStagingDirEmpty(t, dir)

	// Exactly at the cap: accepted (the cap is inclusive).
	path, err := stager.Stage(ctx, bytes.NewReader(bytes.Repeat([]byte{'x'}, 10)))
	require.NoError(t, err)
	b, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Len(t, b, 10)
	require.NoError(t, os.Remove(path))
	requireStagingDirEmpty(t, dir)

	// Empty: rejected and removed.
	_, err = stager.Stage(ctx, bytes.NewReader(nil))
	require.ErrorIs(t, err, migration.ErrUploadEmpty)
	requireStagingDirEmpty(t, dir)
}

func TestPreviewRejectsOversizedUpload(t *testing.T) {
	ctx := context.Background()
	stager, dir := newTestStager(t, 10)
	dest := openDestDB(t)

	big := bytes.Repeat([]byte{'x'}, 11)
	_, err := stager.Preview(ctx, bytes.NewReader(big), dest)
	require.ErrorIs(t, err, migration.ErrUploadTooLarge)
	requireStagingDirEmpty(t, dir)
}

func TestStageSetsPrivatePermissions(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "staging")
	stager, err := migration.NewStager(dir, 1<<20)
	require.NoError(t, err)

	data := buildSourceBytes(t, `CREATE TABLE videos (video_id TEXT, title TEXT);`)
	path, err := stager.Stage(ctx, bytes.NewReader(data))
	require.NoError(t, err)

	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "staged file must be private (0600)")

	dirInfo, statErr := os.Stat(dir)
	require.NoError(t, statErr)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm(), "staging directory must be private (0700)")

	// The staged name is server-generated and independent of any client name.
	require.True(t, strings.HasPrefix(filepath.Base(path), migration.StagingFilePrefix),
		"staged file must use a generated name, got %q", filepath.Base(path))
	require.True(t, strings.HasSuffix(filepath.Base(path), migration.StagingFileSuffix),
		"staged file must use a generated name, got %q", filepath.Base(path))
	require.NoError(t, os.Remove(path))
}

func TestMaxUploadBytesFromEnv(t *testing.T) {
	t.Run("unset yields default", func(t *testing.T) {
		t.Setenv(migration.MaxUploadBytesEnvVar, "")
		n, err := migration.MaxUploadBytesFromEnv()
		require.NoError(t, err)
		require.Equal(t, migration.DefaultMaxUploadBytes, n)
	})

	t.Run("valid override", func(t *testing.T) {
		t.Setenv(migration.MaxUploadBytesEnvVar, "1048576")
		n, err := migration.MaxUploadBytesFromEnv()
		require.NoError(t, err)
		require.Equal(t, int64(1048576), n)
	})

	for _, bad := range []string{"abc", "-1", "0", "12.5", " "} {
		t.Run("invalid "+bad, func(t *testing.T) {
			t.Setenv(migration.MaxUploadBytesEnvVar, bad)
			_, err := migration.MaxUploadBytesFromEnv()
			require.Error(t, err, "invalid %q must fail configuration, not weaken the cap", bad)
		})
	}
}

func TestNewStagerRejectsNonPositiveMax(t *testing.T) {
	for _, max := range []int64{0, -1} {
		_, err := migration.NewStager(t.TempDir(), max)
		require.Error(t, err)
	}
}
