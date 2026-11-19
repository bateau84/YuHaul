package discovery

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/titlekey"
	"github.com/stretchr/testify/require"
)

// R9 (ARS-001 §10.5), discovery.persist case. persist starts deferred, but
// DiscoverVideoTx writes `video` before any transient_metadata statement can fire the title-search triggers, so the write
// lock is acquired first and busy_timeout applies. This pins that ordering: if
// the order changes, persist fails at 0 ms under a concurrent writer.
func TestR9DiscoveryPersistColdConnectionWriterContention(t *testing.T) {
	for name, preseed := range map[string]bool{"new metadata row": false, "existing metadata row": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			eng, st, _, db := newTestEngine(t, "http://unused.invalid", 10_000)
			runStart := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
			runID, err := st.CreateRun(ctx, &state.Run{StartedAt: runStart, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
			require.NoError(t, err)

			if preseed {
				// Existing row => the upsert takes the UPDATE path and fires the trigger.
				_, err = db.ExecContext(ctx, `INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active) VALUES ('vid1', 'old', ?, ?, 1)`,
					runStart, runStart.Add(time.Hour))
				require.NoError(t, err)
			}

			var path string
			var seq int
			var dbName string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT seq, name, file FROM pragma_database_list WHERE name = 'main'`).Scan(&seq, &dbName, &path))
			require.NotEmpty(t, path)
			// persist must run on a pool that has never compiled the
			// transient_metadata triggers; the engine's own pool has (it seeded
			// above). Open it after seeding and before the lock is taken.
			freshDB, err := store.Open(ctx, filepath.Clean(path))
			require.NoError(t, err)
			t.Cleanup(func() { _ = freshDB.Close() })
			freshState, err := state.NewStore(freshDB)
			require.NoError(t, err)
			freshEng := NewEngine(eng.auth, state.NewMachine(freshState), quota.New(freshDB), freshDB)
			other, err := store.Open(ctx, filepath.Clean(path))
			require.NoError(t, err)
			conn, err := other.Conn(ctx)
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
			require.NoError(t, err)
			released := make(chan struct{})
			var releasedAt atomic.Int64
			time.AfterFunc(time.Second, func() {
				releasedAt.Store(time.Now().UnixNano())
				_, _ = conn.ExecContext(ctx, "ROLLBACK")
				_ = conn.Close()
				_ = other.Close()
				close(released)
			})
			t.Cleanup(func() { <-released })

			err = freshEng.persist(ctx, runID, runStart, []*discoveredVideo{{
				VideoID: "vid1", ChannelID: "UC1", ChannelName: "Chan", Title: "New title",
				PublishedAt: runStart.Add(-time.Hour), ThumbnailURL: "https://i.ytimg.com/x.jpg",
			}})
			require.NoError(t, err)
			require.NotZero(t, releasedAt.Load(), "persist returned while the lock was still held")

			var title string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT title FROM transient_metadata WHERE video_id = 'vid1'`).Scan(&title))
			require.Equal(t, "New title", title)

			// Index effect (I1, I3): exactly one entry, for the new title.
			rows, err := db.QueryContext(ctx, `SELECT d.video_id, s.k FROM title_search_doc d JOIN title_search s ON s.rowid = d.doc_id`)
			require.NoError(t, err)
			defer rows.Close()
			got := map[string]string{}
			for rows.Next() {
				var id, k string
				require.NoError(t, rows.Scan(&id, &k))
				got[id] = k
			}
			require.NoError(t, rows.Err())
			require.Equal(t, map[string]string{"vid1": titlekey.Key("New title")}, got)
		})
	}
}
