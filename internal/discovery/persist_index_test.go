package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/titlekey"
	"github.com/stretchr/testify/require"
)

// R3 (ARS-001 §10.3), discovery case: persist leaves exactly one title search
// entry for a held title, none for rows the writer must not index, and an
// injected IndexTitleTx failure rolls back the title write.
func TestPersistKeepsTitleSearchIndexCurrent(t *testing.T) {
	ctx := context.Background()
	runStart := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	entries := func(t *testing.T, eng *Engine) map[string]string {
		t.Helper()
		rows, err := eng.db.QueryContext(ctx,
			`SELECT d.video_id, s.k FROM title_search_doc d JOIN title_search s ON s.rowid = d.doc_id`)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var id, k string
			require.NoError(t, rows.Scan(&id, &k))
			out[id] = k
		}
		require.NoError(t, rows.Err())
		return out
	}
	start := func(t *testing.T) (*Engine, int64) {
		t.Helper()
		eng, st, _, _ := newTestEngine(t, "http://unused.invalid", 10_000)
		runID, err := st.CreateRun(ctx, &state.Run{StartedAt: runStart, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
		require.NoError(t, err)
		return eng, runID
	}
	video := func(id, title string) *discoveredVideo {
		return &discoveredVideo{VideoID: id, ChannelID: "UC1", ChannelName: "Chan", Title: title,
			PublishedAt: runStart.Add(-time.Hour)}
	}

	t.Run("new and replaced titles are indexed once", func(t *testing.T) {
		eng, runID := start(t)
		require.NoError(t, eng.persist(ctx, runID, runStart, []*discoveredVideo{video("v1", "Straße der Lieder")}))
		require.Equal(t, map[string]string{"v1": titlekey.Key("Straße der Lieder")}, entries(t, eng))
	})

	t.Run("a re-upsert with a different title replaces the entry", func(t *testing.T) {
		eng, runID := start(t)
		_, err := eng.db.ExecContext(ctx, `INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active)
			VALUES ('v1', 'Old title', ?, ?, 1)`, runStart.Add(-time.Hour), runStart.Add(time.Hour))
		require.NoError(t, err)
		tx, err := eng.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, metadata.IndexTitleTx(ctx, tx, titlekey.NewFolder(), "v1", runStart))
		require.NoError(t, tx.Commit())
		require.Equal(t, map[string]string{"v1": titlekey.Key("Old title")}, entries(t, eng))

		require.NoError(t, eng.persist(ctx, runID, runStart, []*discoveredVideo{video("v1", "Another Title")}))
		require.Equal(t, map[string]string{"v1": titlekey.Key("Another Title")}, entries(t, eng))
	})

	t.Run("no entry for a blank title", func(t *testing.T) {
		eng, runID := start(t)
		require.NoError(t, eng.persist(ctx, runID, runStart, []*discoveredVideo{video("v1", "   ")}))
		require.Empty(t, entries(t, eng))
	})

	t.Run("no entry when a kept expired_at marks the row", func(t *testing.T) {
		eng, runID := start(t)
		_, err := eng.db.ExecContext(ctx, `INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, expired_at, is_active)
			VALUES ('v1', NULL, ?, ?, ?, 0)`, runStart.Add(-48*time.Hour), runStart.Add(-24*time.Hour), runStart.Add(-24*time.Hour))
		require.NoError(t, err)
		require.NoError(t, eng.persist(ctx, runID, runStart, []*discoveredVideo{video("v1", "Fresh title")}))

		var title string
		require.NoError(t, eng.db.QueryRowContext(ctx, `SELECT title FROM transient_metadata WHERE video_id = 'v1'`).Scan(&title))
		require.Equal(t, "Fresh title", title, "the title write itself still happens")
		require.Empty(t, entries(t, eng))
	})

	t.Run("index failure rolls back the title write", func(t *testing.T) {
		eng, runID := start(t)
		for _, trg := range []string{"insert", "update", "delete"} {
			_, err := eng.db.ExecContext(ctx, `DROP TRIGGER transient_metadata_search_purge_`+trg)
			require.NoError(t, err)
		}
		_, err := eng.db.ExecContext(ctx, `DROP TABLE title_search`)
		require.NoError(t, err)

		err = eng.persist(ctx, runID, runStart, []*discoveredVideo{video("v1", "Title")})
		require.Error(t, err)
		var n int
		require.NoError(t, eng.db.QueryRowContext(ctx, `SELECT count(*) FROM transient_metadata`).Scan(&n))
		require.Zero(t, n, "title write must roll back with the failed index update")
		require.NoError(t, eng.db.QueryRowContext(ctx, `SELECT count(*) FROM video`).Scan(&n))
		require.Zero(t, n, "the whole video transaction rolls back")
	})
}
