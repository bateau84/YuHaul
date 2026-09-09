package main_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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

	err = destDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM transient_metadata;").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "CLI must not create transient metadata rows")
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
