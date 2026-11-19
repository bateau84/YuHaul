package videosearch

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
)

// migrateEmbeddedTo applies the embedded migration files up to version. The
// files are the ones store embeds; reading the directory keeps the test on the
// same SQL.
func migrateEmbeddedTo(t *testing.T, path string, version uint) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_fk=1")
	require.NoError(t, err)
	src, err := iofs.New(os.DirFS("../store/migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(version))
	return db
}

// The real upgrade path (ARS-001 §10.4, §10.9): a database at migration 028
// with held titles is opened by store.Open, which applies 029, and then by
// metadata.PrepareSearchIndex, which is the backfill. Search then takes the
// indexed path and returns the same results as the scan path did before.
func TestUpgradeFrom028BuildsIndexAndChoosesIndexedPath(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")

	old := migrateEmbeddedTo(t, path, 28)
	var hasIndex int
	require.NoError(t, old.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('title_search', 'title_search_state')`).Scan(&hasIndex))
	require.Zero(t, hasIndex, "the database is at 028: no search objects yet")
	const n = 30
	for i := range n {
		id := fmt.Sprintf("up%09d", i)
		seedMeta(t, old, id, fmt.Sprintf("Lofi Beats %d", i), true, storeNow.Add(-2*time.Hour), storeNow.Add(time.Hour), nil)
		seedVideo(t, old, id, storeNow)
	}
	seedMeta(t, old, "up-other-01", "Unrelated Talk", true, storeNow.Add(-2*time.Hour), storeNow.Add(time.Hour), nil)
	seedVideo(t, old, "up-other-01", storeNow)
	require.NoError(t, old.Close())

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	svc := NewService(db, func() time.Time { return storeNow })

	// After Open and before the startup step the index is not ready: scan.
	scan, p := searchPath(t, svc, "lofi")
	require.Equal(t, PathScan, p)
	require.Equal(t, n, scan.Results.Total)

	stats, err := metadata.PrepareSearchIndex(ctx, db, storeNow)
	require.NoError(t, err)
	require.False(t, stats.Recreated)
	require.Equal(t, n+1, stats.Reconcile.Added, "the reconcile backfilled every held title")
	require.Nil(t, stats.Optimize)

	idx, p := searchPath(t, svc, "lofi")
	require.Equal(t, PathIndexed, p)
	require.Equal(t, scan.Results, idx.Results)
	require.Equal(t, n, idx.Stats.Matches)
}
