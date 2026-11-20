package discovery

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/titlekey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	modernsqlite "modernc.org/sqlite"
)

// stepObserver wraps the real SQLite driver at two statements only. It records
// the argument of every FTS5 'merge' command, and after the COMMIT that ends a
// merge step it calls after, synchronously, before the optimize's next step
// begins. SQLite does all the work.
type stepObserver struct {
	dsn   string
	mu    sync.Mutex
	args  []int64
	after func(step int)
}

func (o *stepObserver) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&modernsqlite.Driver{}).Open(o.dsn)
	if err != nil {
		return nil, err
	}
	return &stepConn{Conn: conn, owner: o}, nil
}

func (*stepObserver) Driver() driver.Driver { return &modernsqlite.Driver{} }

type stepConn struct {
	driver.Conn
	owner  *stepObserver
	merged bool
}

func (c *stepConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	res, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	if err != nil {
		return res, err
	}
	switch {
	case strings.Contains(query, "'merge'"):
		c.merged = true
		c.owner.mu.Lock()
		c.owner.args = append(c.owner.args, args[0].Value.(int64))
		c.owner.mu.Unlock()
	case query == "COMMIT" && c.merged:
		c.merged = false
		c.owner.mu.Lock()
		step := len(c.owner.args)
		c.owner.mu.Unlock()
		if c.owner.after != nil {
			c.owner.after(step)
		}
	}
	return res, nil
}

// R8 concurrency (ARS-001 §10.10, §10.11): the optimize releases the write lock
// between steps. After each of its first three steps commits, discovery's
// persist upserts a title and an Expire batch commits, on the real engine and
// store, before the next step begins. Neither returns SQLITE_BUSY, both are
// indexed or purged correctly, and the optimize still completes. The merge
// command's argument is negative on the first step only.
func TestOptimizeStepsInterleaveWithDiscoveryAndExpire_R8(t *testing.T) {
	ctx := context.Background()
	eng, st, _, db := newTestEngine(t, "http://unused.invalid", 10_000)
	runStart := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	runID, err := st.CreateRun(ctx, &state.Run{StartedAt: runStart, RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)

	// Many small batches give the index many segments, so several steps are needed.
	oldBatch, oldPages := metadata.IndexWriteBatch, metadata.OptimizeMergePages
	metadata.IndexWriteBatch, metadata.OptimizeMergePages = 10, 2
	t.Cleanup(func() { metadata.IndexWriteBatch, metadata.OptimizeMergePages = oldBatch, oldPages })
	var ids []string
	for i := range 150 {
		id := fmt.Sprintf("base%04d", i)
		ids = append(ids, id)
		_, err := db.ExecContext(ctx, `INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active)
			VALUES (?, ?, ?, ?, 1)`, id, fmt.Sprintf("Interleave base title number %04d", i), runStart.Add(-time.Hour), runStart.Add(time.Hour))
		require.NoError(t, err)
	}
	_, err = metadata.PrepareSearchIndex(ctx, db, runStart)
	require.NoError(t, err)
	store := metadata.NewStore(db)
	require.NoError(t, store.Expire(ctx, ids[:20], runStart), "tombstones for the optimize to merge away")

	var path string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path))
	obs := &stepObserver{dsn: path + "?_fk=1&_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1"}
	var rounds []int
	obs.after = func(step int) {
		if len(rounds) >= 3 {
			return
		}
		n := len(rounds)
		rounds = append(rounds, step)
		video := &discoveredVideo{VideoID: fmt.Sprintf("disc%04d", n), ChannelID: "UC1", ChannelName: "Chan",
			Title: fmt.Sprintf("Discovered between steps %d", n), PublishedAt: runStart.Add(-time.Hour)}
		assert.NoError(t, eng.persist(ctx, runID, runStart, []*discoveredVideo{video}), "discovery upsert between steps")
		assert.NoError(t, store.Expire(ctx, ids[20+5*n:25+5*n], runStart), "Expire batch between steps")
	}
	optDB := sql.OpenDB(obs)
	t.Cleanup(func() { _ = optDB.Close() })

	stats, err := metadata.OptimizeSearchIndex(ctx, optDB)
	require.NoError(t, err)
	require.True(t, stats.Complete)
	require.Len(t, rounds, 3, "three writes landed between steps")
	require.Greater(t, stats.Steps, 3, "and the optimize still had steps left after them")
	assert.Equal(t, []int{1, 2, 3}, rounds, "after the first, second and third step")

	obs.mu.Lock()
	args := append([]int64(nil), obs.args...)
	obs.mu.Unlock()
	require.Len(t, args, stats.Steps)
	assert.Equal(t, int64(-2), args[0], "the first step starts a full merge")
	for i, a := range args[1:] {
		assert.Equal(t, int64(2), a, "step %d continues the merge", i+2)
	}

	// The interleaved writes are reflected in the index.
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
	for n := range 3 {
		title := fmt.Sprintf("Discovered between steps %d", n)
		assert.Equal(t, titlekey.Key(title), got[fmt.Sprintf("disc%04d", n)])
	}
	for _, id := range ids[:35] {
		assert.NotContains(t, got, id, "expired before or between steps")
	}
	assert.Len(t, got, 150-35+3)
}
