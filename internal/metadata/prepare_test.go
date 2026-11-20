package metadata_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/titlekey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRaw stores n held rows with raw SQL, so none has an index entry.
func seedRaw(t *testing.T, db *sql.DB, n int, now time.Time) {
	t.Helper()
	for i := range n {
		_, err := db.Exec(`INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active)
			VALUES (?, ?, ?, ?, 1)`, fmt.Sprintf("prep%04d", i), fmt.Sprintf("Prepare title number %04d", i),
			now.Add(-time.Hour), now.Add(time.Hour))
		require.NoError(t, err)
	}
}

func indexReady(t *testing.T, db *sql.DB) bool {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	ready, err := metadata.SearchIndexReady(context.Background(), tx)
	require.NoError(t, err)
	return ready
}

// R5, first start: store.Open leaves the index unbuilt; PrepareSearchIndex is
// the backfill. It removes nothing, so it runs no optimize.
func TestPrepareSearchIndexBackfillsOnFirstStart(t *testing.T) {
	ctx := context.Background()
	ms, _ := openIndexDB(t)
	db := ms.DB()
	now := time.Now().UTC()
	seedRaw(t, db, 5, now)
	require.False(t, indexReady(t, db), "store.Open does not build the index")

	stats, err := metadata.PrepareSearchIndex(ctx, db, now)
	require.NoError(t, err)
	assert.False(t, stats.Recreated)
	assert.Equal(t, 5, stats.Reconcile.Added)
	assert.Nil(t, stats.Optimize, "nothing was removed")
	assert.True(t, indexReady(t, db))
	assert.Equal(t, 5, count(t, db, "title_search_doc"))
	checkInvariants(t, db, now)
}

// R5, restart: a second start finds a ready index and changes nothing.
func TestPrepareSearchIndexRestartIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	now := time.Now().UTC()

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	seedRaw(t, db, 5, now)
	_, err = metadata.PrepareSearchIndex(ctx, db, now)
	require.NoError(t, err)
	before := entries(t, db)
	require.NoError(t, db.Close())

	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.True(t, indexReady(t, db), "the built index survives a restart")
	stats, err := metadata.PrepareSearchIndex(ctx, db, now)
	require.NoError(t, err)
	assert.False(t, stats.Recreated)
	assert.Equal(t, metadata.ReconcileStats{Batches: stats.Reconcile.Batches, LongestHold: stats.Reconcile.LongestHold}, stats.Reconcile, "no change")
	assert.Nil(t, stats.Optimize)
	assert.Equal(t, before, entries(t, db))
}

// R5 and §10.9, key-version recovery: the operator reset (or a titlekey
// version bump) makes the next start recreate the index and backfill it.
func TestPrepareSearchIndexRecreatesOnKeyVersionMismatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recover.db")
	now := time.Now().UTC()

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	seedRaw(t, db, 5, now)
	_, err = metadata.PrepareSearchIndex(ctx, db, now)
	require.NoError(t, err)
	before := entries(t, db)
	require.Len(t, before, 5)
	// An entry built under another key version would be wrong; the reset leaves
	// the old entries in place until the recreate drops them.
	_, err = db.Exec(`UPDATE title_search_state SET key_version = 'reset' WHERE id = 1`)
	require.NoError(t, err)
	require.False(t, indexReady(t, db))
	require.NoError(t, db.Close())

	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	stats, err := metadata.PrepareSearchIndex(ctx, db, now)
	require.NoError(t, err)
	assert.True(t, stats.Recreated)
	assert.Equal(t, 5, stats.Reconcile.Added, "the reconcile is the backfill")
	assert.Zero(t, stats.Reconcile.OrphansRemoved+stats.Reconcile.Purged)
	assert.Nil(t, stats.Optimize, "a recreate frees the old segments; there is nothing to merge away")
	assert.True(t, indexReady(t, db))
	assert.Equal(t, before, entries(t, db))
	var version string
	require.NoError(t, db.QueryRow(`SELECT key_version FROM title_search_state WHERE id = 1`).Scan(&version))
	assert.Equal(t, titlekey.Version(), version)
}

// R5: an entry the reconcile removes triggers the optimize, which completes.
func TestPrepareSearchIndexOptimizesOnlyAfterRemoval(t *testing.T) {
	ctx := context.Background()
	ms, _ := openIndexDB(t)
	db := ms.DB()
	now := time.Now().UTC()
	seedRaw(t, db, 3, now)
	_, err := metadata.PrepareSearchIndex(ctx, db, now)
	require.NoError(t, err)

	// A document with no FTS row is an orphan the reconcile must remove.
	_, err = db.Exec(`INSERT INTO title_search_doc (doc_id, video_id) VALUES (900001, 'ghost')`)
	require.NoError(t, err)
	stats, err := metadata.PrepareSearchIndex(ctx, db, now)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Reconcile.OrphansRemoved)
	require.NotNil(t, stats.Optimize)
	assert.True(t, stats.Optimize.Complete)
	assert.Equal(t, 3, count(t, db, "title_search_doc"))

	// A row that stopped being indexable is purged by the reconcile too.
	_, err = db.Exec(`UPDATE title_search_state SET built_at = NULL`)
	require.NoError(t, err)
	stats, err = metadata.PrepareSearchIndex(ctx, db, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, stats.Reconcile.Purged)
	require.NotNil(t, stats.Optimize)
	assert.True(t, stats.Optimize.Complete)
	assert.Zero(t, count(t, db, "title_search_doc"))
}

// A failure is returned at its step and stops the later steps.
func TestPrepareSearchIndexReturnsFirstError(t *testing.T) {
	ms, _ := openIndexDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stats, err := metadata.PrepareSearchIndex(ctx, ms.DB(), time.Now().UTC())
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, stats.Optimize)
	assert.False(t, stats.Recreated)
}
