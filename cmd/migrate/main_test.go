package main_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func TestCLIImportsSourceIntoDestination(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	dest := filepath.Join(dir, "dest.db")

	srcDB, err := sql.Open("sqlite", src)
	require.NoError(t, err)
	_, err = srcDB.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);
		 INSERT INTO videos (video_id, title, channel_id) VALUES
			('v1', 'Jazz Live Session', 'UC1'),
			('v2', 'Rock Anthem (Official Video)', 'UC2');`)
	require.NoError(t, err)
	require.NoError(t, srcDB.Close())

	start := time.Now().UTC()
	cmd := exec.Command(testBinary(t), "--source-db", src, "--dest-db", dest, "--batch-id", "cli-test")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "CLI output: %s", out)
	require.Contains(t, string(out), "Inserted: 2")

	destDB, err := store.Open(ctx, dest)
	require.NoError(t, err)
	defer destDB.Close()

	var count int
	err = destDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM title_fingerprint WHERE source = 'user_import' AND import_batch_id = 'cli-test';").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	// The real destination schema defaults is_duplicate_reference to 0, so the
	// importer must set it explicitly for the rows to be usable by the
	// exact/fuzzy detector (OC-004 guarantee 1, BR-009 AC4).
	var eligible int
	err = destDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM title_fingerprint WHERE source = 'user_import' AND import_batch_id = 'cli-test' AND is_duplicate_reference = 1;").Scan(&eligible)
	require.NoError(t, err)
	require.Equal(t, 2, eligible, "every imported row must be an exact/fuzzy matching candidate")

	err = destDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM transient_metadata;").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "CLI must not create transient metadata rows")

	// The CLI shares the importer's successful-completion boundary: the
	// durable last-successful-import marker is recorded as canonical UTC
	// RFC 3339 Nano inside the run window (ADR-015, BR-009 AC9).
	var marker sql.NullString
	err = destDB.QueryRowContext(ctx,
		"SELECT last_successful_corpus_import_at FROM corpus_import_metadata WHERE singleton_id = 1;").Scan(&marker)
	require.NoError(t, err)
	require.True(t, marker.Valid, "a completed CLI import must record the completion timestamp")
	parsed, err := time.Parse(time.RFC3339Nano, marker.String)
	require.NoError(t, err, "marker must be RFC 3339 Nano: %q", marker.String)
	require.Equal(t, parsed.UTC().Format(time.RFC3339Nano), marker.String,
		"marker must be the canonical UTC representation")
	require.False(t, parsed.Before(start), "marker %v predates the CLI run", parsed)
	require.False(t, parsed.After(time.Now().UTC().Add(time.Minute)), "marker %v postdates the CLI run", parsed)

	// Import grants matching eligibility only: it must create no discovery
	// credit, provenance, or route evidence (OC-004 guarantee 3, BR-010 AC6).
	for _, table := range []string{"video", "channel", "duplicate_match", "route_decision", "executed_action"} {
		err = destDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+";").Scan(&count)
		require.NoError(t, err)
		require.Zero(t, count, "import must not create %s rows", table)
	}
}

func TestCLIReportsBlockedPrecondition(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	dest := filepath.Join(dir, "dest.db")

	srcDB, err := sql.Open("sqlite", src)
	require.NoError(t, err)
	_, err = srcDB.ExecContext(ctx,
		`CREATE TABLE unrelated (id INTEGER PRIMARY KEY, name TEXT);`)
	require.NoError(t, err)
	require.NoError(t, srcDB.Close())

	cmd := exec.Command(testBinary(t), "--source-db", src, "--dest-db", dest)
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(out), "blocked precondition")
	require.NotZero(t, cmd.ProcessState.ExitCode())
}

func TestCLIRequiresFlags(t *testing.T) {
	cmd := exec.Command(testBinary(t))
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(out), "Usage")
}

var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "migrate-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	binaryPath = filepath.Join(dir, "migrate")

	out, err := exec.Command("go", "build", "-o", binaryPath, ".").CombinedOutput()
	if err != nil {
		panic(string(out))
	}

	os.Exit(m.Run())
}

func testBinary(t *testing.T) string {
	t.Helper()
	if binaryPath == "" {
		t.Fatal("binary not built")
	}
	return binaryPath
}
