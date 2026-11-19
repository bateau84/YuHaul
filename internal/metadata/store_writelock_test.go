package metadata_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
)

const lockHold = time.Second

// writeLock is a BEGIN IMMEDIATE held on a separate pool over the same file, as
// a concurrent reconcile would hold it.
type writeLock struct {
	releasedAt atomic.Int64 // UnixNano taken just before ROLLBACK
	done       chan struct{}
}

// holdWriteLock returns once the lock is held and releases it after d.
func holdWriteLock(t *testing.T, path string, d time.Duration) *writeLock {
	t.Helper()
	other, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	conn, err := other.Conn(context.Background())
	require.NoError(t, err)
	_, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE")
	require.NoError(t, err)
	l := &writeLock{done: make(chan struct{})}
	time.AfterFunc(d, func() {
		l.releasedAt.Store(time.Now().UnixNano())
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		_ = other.Close()
		close(l.done)
	})
	t.Cleanup(func() { <-l.done })
	return l
}

// finishedAfterRelease proves the writer waited: it returned only after the
// holder started releasing the lock.
func (l *writeLock) finishedAfterRelease(t *testing.T) {
	t.Helper()
	rel := l.releasedAt.Load()
	require.NotZero(t, rel, "writer returned while the lock was still held")
	require.GreaterOrEqual(t, time.Now().UnixNano(), rel)
}

// freshStore opens a new pool over path. Its connections have not yet compiled
// the transient_metadata triggers or loaded the FTS virtual table, which is the
// state in which a deferred write transaction first reads (and then cannot
// upgrade) under a concurrent writer.
func freshStore(t *testing.T, path string) *metadata.Store {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return metadata.NewStore(db)
}

// seedIndexed stores an active held row and gives it a search entry.
func seedIndexed(t *testing.T, ms *metadata.Store, id string) {
	t.Helper()
	put(t, ms, id, "Title "+id, nil)
	indexNow(t, ms.DB(), id, idxNow)
	require.Contains(t, entries(t, ms.DB()), id)
}

// R9 (ARS-001 §10.5): every production writer of transient_metadata that can
// fire the title-search triggers must wait for a concurrent write lock, not
// fail at 0 ms. discovery.persist is covered in internal/discovery.
func TestR9ColdConnectionWriterContention(t *testing.T) {
	// Control: the former deferred form must fail at once, otherwise the cases
	// below prove nothing.
	t.Run("control deferred form fails immediately", func(t *testing.T) {
		ms, path := openIndexDB(t)
		seedIndexed(t, ms, "v1")
		fresh := freshStore(t, path)
		holdWriteLock(t, path, lockHold)

		start := time.Now()
		tx, err := fresh.DB().BeginTx(context.Background(), nil)
		require.NoError(t, err)
		defer tx.Rollback()
		// Same shape as the former Expire/Refresh: prepare, then execute.
		stmt, err := tx.Prepare(`UPDATE transient_metadata SET is_active = 0 WHERE video_id = ?`)
		require.NoError(t, err)
		defer stmt.Close()
		_, err = stmt.Exec("v1")
		require.Error(t, err, "inconclusive: the deferred form no longer fails under contention")
		require.Contains(t, err.Error(), "SQLITE_BUSY")
		require.Less(t, time.Since(start), 500*time.Millisecond, "deferred form must not have waited")
	})

	title := "Fresh"
	writers := map[string]func(*metadata.Store) error{
		"Expire": func(ms *metadata.Store) error {
			return ms.Expire(context.Background(), []string{"v1", "v2"}, idxNow)
		},
		"Refresh": func(ms *metadata.Store) error {
			return ms.Refresh(context.Background(), []metadata.TransientMetadata{
				{VideoID: "v1", Title: &title}, {VideoID: "v2", Title: &title},
			}, time.Hour, idxNow)
		},
		"Upsert": func(ms *metadata.Store) error {
			for _, id := range []string{"v1", "v2"} {
				if err := ms.Upsert(context.Background(), metadata.TransientMetadata{
					VideoID: id, Title: &title, FetchedAt: idxNow, ExpiresAt: idxNow.Add(time.Hour), IsActive: true,
				}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	for name, write := range writers {
		t.Run(name+" waits then succeeds", func(t *testing.T) {
			ms, path := openIndexDB(t)
			seedIndexed(t, ms, "v1")
			seedIndexed(t, ms, "v2")
			fresh := freshStore(t, path)
			lock := holdWriteLock(t, path, lockHold)

			require.NoError(t, write(fresh))
			lock.finishedAfterRelease(t)

			// Purge side only: the triggers delete the old entries and leave
			// no document or FTS row orphaned. ARS-001 §10.3 requires Refresh
			// and Upsert to re-index in their own transaction, but they do not
			// yet (node fts-writers), so re-indexing is not asserted here.
			// require.Empty(entries) below must change when §10.3 lands.
			require.Empty(t, entries(t, ms.DB()))
			require.Zero(t, count(t, ms.DB(), "title_search_doc"))
			require.Zero(t, count(t, ms.DB(), "title_search"))
			for _, id := range []string{"v1", "v2"} {
				got, err := ms.Get(context.Background(), id)
				require.NoError(t, err)
				if name == "Expire" {
					require.False(t, got.IsActive)
					require.Nil(t, got.Title)
					require.NotNil(t, got.ExpiredAt)
				} else {
					require.True(t, got.IsActive)
					require.Equal(t, "Fresh", *got.Title)
				}
			}
		})
	}
}

// A statement that fails inside the transaction must roll back every earlier
// write and leave no open transaction on the pooled connection. A trigger aborts
// the second item's UPDATE after the first was applied.
func TestRefreshStatementFailureRollsBack(t *testing.T) {
	ms, _ := openIndexDB(t)
	ctx := context.Background()
	seedIndexed(t, ms, "v1")
	seedIndexed(t, ms, "v2")
	_, err := ms.DB().ExecContext(ctx, `CREATE TRIGGER fail_v2 BEFORE UPDATE ON transient_metadata
		WHEN NEW.video_id = 'v2' BEGIN SELECT RAISE(ABORT, 'forced failure'); END`)
	require.NoError(t, err)

	title := "Changed"
	err = ms.Refresh(ctx, []metadata.TransientMetadata{{VideoID: "v1", Title: &title}, {VideoID: "v2", Title: &title}}, time.Hour, idxNow)
	require.ErrorContains(t, err, "forced failure")

	got, err := ms.Get(ctx, "v1")
	require.NoError(t, err)
	require.Equal(t, "Title v1", *got.Title, "first item must be rolled back")
	require.Contains(t, entries(t, ms.DB()), "v1", "index entry must survive the rollback")

	// Pool is clean: another writer can take the lock at once and no
	// transaction is left open on any pooled connection.
	require.NoError(t, ms.Expire(ctx, []string{"v1"}, idxNow))
	_, err = ms.DB().ExecContext(ctx, "BEGIN IMMEDIATE")
	require.NoError(t, err)
	_, err = ms.DB().ExecContext(ctx, "ROLLBACK")
	require.NoError(t, err)
}
