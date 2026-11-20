package metadata_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logRecords parses the JSON records a lifecycle manager wrote, by message.
type logRecords struct{ buf bytes.Buffer }

func (l *logRecords) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&l.buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (l *logRecords) find(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(l.buf.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal(line, &rec))
		if rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

func lifecycleManager(t *testing.T, ms *metadata.Store, now time.Time, logs *logRecords) *metadata.Manager {
	t.Helper()
	return metadata.NewManager(ms, &fakeFetcher{}, nil,
		metadata.WithNow(func() time.Time { return now }), metadata.WithLogger(logs.logger()))
}

// seedHeld stores n held rows through Store.Upsert, which indexes at the real
// clock. They expire an hour from now.
func seedHeld(t *testing.T, ms *metadata.Store, n int, prefix string) (expireAfter time.Time) {
	t.Helper()
	now := time.Now().UTC()
	for i := range n {
		title := fmt.Sprintf("%s lifecycle title number %04d", prefix, i)
		require.NoError(t, ms.Upsert(context.Background(), metadata.TransientMetadata{
			VideoID: fmt.Sprintf("%s%04d", prefix, i), Title: &title, FetchedAt: now.Add(-time.Minute),
			ExpiresAt: now.Add(time.Hour + time.Duration(i)*time.Millisecond), IsActive: true,
		}))
	}
	return now.Add(2 * time.Hour)
}

func countCommits(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	metadata.SetTestHooks(t, metadata.TestHooks{Commit: func(ctx context.Context, c *sql.Conn) error {
		n.Add(1)
		_, err := c.ExecContext(ctx, "COMMIT")
		return err
	}})
	return &n
}

// R5: expiry runs in chunks of IndexWriteBatch, one write transaction each.
func TestExpireStaleRunsInChunksOfIndexWriteBatch(t *testing.T) {
	ms, _ := openIndexDB(t)
	later := seedHeld(t, ms, 450, "chunk")
	require.Equal(t, 450, count(t, ms.DB(), "title_search_doc"))
	var logs logRecords
	mgr := lifecycleManager(t, ms, later, &logs)

	commits := countCommits(t)
	expired, err := mgr.ExpireStale(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 450, expired)
	assert.EqualValues(t, 3, commits.Load(), "450 rows at a batch of 200 are three Expire transactions")
	assert.Zero(t, count(t, ms.DB(), "title_search_doc"), "the update trigger purged every entry")
	assert.Zero(t, count(t, ms.DB(), "title_search"))
}

// R5: a failing chunk returns the rows expired by earlier chunks with the error;
// the earlier chunks stay committed.
func TestExpireStaleChunkFailureKeepsEarlierChunks(t *testing.T) {
	ms, _ := openIndexDB(t)
	later := seedHeld(t, ms, 450, "part")
	// The last row to expire fails inside the final chunk.
	_, err := ms.DB().Exec(`CREATE TRIGGER inject_expire_failure BEFORE UPDATE OF is_active ON transient_metadata
		WHEN NEW.video_id = 'part0449' BEGIN SELECT RAISE(ABORT, 'injected expire failure'); END`)
	require.NoError(t, err)
	var logs logRecords
	mgr := lifecycleManager(t, ms, later, &logs)

	expired, err := mgr.ExpireStale(context.Background())
	require.ErrorContains(t, err, "injected expire failure")
	assert.Equal(t, 400, expired, "two chunks committed before the third failed")
	assert.Equal(t, 50, count(t, ms.DB(), "title_search_doc"), "the failed chunk rolled back whole")
}

// R5: the sweep runs expiry, reconcile and optimize, and refresh last.
func TestRunLifecycleExpiresReconcilesAndOptimizes(t *testing.T) {
	ms, _ := openIndexDB(t)
	later := seedHeld(t, ms, 450, "sweep")
	var logs logRecords
	mgr := lifecycleManager(t, ms, later, &logs)

	require.NoError(t, mgr.RunLifecycle(context.Background()))

	assert.Zero(t, count(t, ms.DB(), "title_search_doc"))
	opt := logs.find(t, "metadata lifecycle optimized search index")
	require.Len(t, opt, 1)
	assert.Equal(t, true, opt[0]["complete"])
	assert.Equal(t, false, opt[0]["failed"])
	require.Len(t, logs.find(t, "metadata lifecycle reconciled search index"), 1)
	ref := logs.find(t, "metadata lifecycle refreshed routed records")
	require.Len(t, ref, 1, "refresh runs after a successful expiry")
	// Order of the four steps.
	order := []string{
		"metadata lifecycle expired stale records", "metadata lifecycle reconciled search index",
		"metadata lifecycle optimized search index", "metadata lifecycle refreshed routed records",
	}
	var last int
	for _, msg := range order {
		idx := bytes.Index(logs.buf.Bytes(), []byte(msg))
		require.GreaterOrEqual(t, idx, last, msg)
		last = idx
	}
}

// R5: with nothing to expire, the reconcile still purges an entry whose row is
// past expires_at in a text layout the expiry SQL comparison misses.
func TestRunLifecycleReconcilesWhenNothingExpired(t *testing.T) {
	ms, _ := openIndexDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	title := "layout missed by expiry sql"
	require.NoError(t, ms.Upsert(context.Background(), metadata.TransientMetadata{
		VideoID: "layout", Title: &title, FetchedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(time.Hour), IsActive: true,
	}))
	// The T/Z layout sorts after the space layout of the bound cutoff, so
	// ListActivePastExpiry does not select the row. The row is already past
	// expiry at the sweep's clock and still carries an entry.
	_, err := ms.DB().Exec(`UPDATE transient_metadata SET expires_at = ? WHERE video_id = 'layout'`,
		now.Add(-time.Minute).Format("2006-01-02T15:04:05Z"))
	require.NoError(t, err)
	indexNow(t, ms.DB(), "layout", now.Add(-2*time.Hour))
	require.Equal(t, 1, count(t, ms.DB(), "title_search_doc"))

	var logs logRecords
	mgr := lifecycleManager(t, ms, now, &logs)
	expired, err := mgr.ExpireStale(context.Background())
	require.NoError(t, err)
	require.Zero(t, expired, "the layout hides the row from the expiry selector")

	require.NoError(t, mgr.RunLifecycle(context.Background()))
	assert.Zero(t, count(t, ms.DB(), "title_search_doc"), "reconcile purged the entry")
	rec := logs.find(t, "metadata lifecycle reconciled search index")
	require.Len(t, rec, 1)
	assert.EqualValues(t, 1, rec[0]["purged"])
}

// R5: a failed ExpireStale still reconciles and optimizes, skips the refresh,
// and returns its error joined with the other steps' errors.
func TestRunLifecycleFailedExpiryStillReconcilesAndOptimizes(t *testing.T) {
	ms, _ := openIndexDB(t)
	later := seedHeld(t, ms, 3, "fail")
	_, err := ms.DB().Exec(`CREATE TRIGGER inject_expire_failure BEFORE UPDATE OF is_active ON transient_metadata
		BEGIN SELECT RAISE(ABORT, 'injected expire failure'); END`)
	require.NoError(t, err)
	// Damage a different part of the index so the reconcile has work to prove it ran.
	_, err = ms.DB().Exec(`INSERT INTO title_search_doc (doc_id, video_id) VALUES (900001, 'ghost')`)
	require.NoError(t, err)
	var logs logRecords
	mgr := lifecycleManager(t, ms, later, &logs)

	err = mgr.RunLifecycle(context.Background())
	require.ErrorContains(t, err, "injected expire failure")

	assert.Equal(t, 0, count(t, ms.DB(), "title_search_doc WHERE video_id = 'ghost'"), "reconcile ran")
	opt := logs.find(t, "metadata lifecycle optimized search index")
	require.Len(t, opt, 1, "optimize ran")
	assert.Equal(t, true, opt[0]["complete"])
	assert.Empty(t, logs.find(t, "metadata lifecycle refreshed routed records"), "refresh needs a successful expiry")
}

// R5: optimize runs even when the reconcile fails, and both errors are joined.
func TestRunLifecycleFailedReconcileStillOptimizes(t *testing.T) {
	ms, _ := openIndexDB(t)
	_, err := ms.DB().Exec(`UPDATE title_search_state SET key_version = 'another-version' WHERE id = 1`)
	require.NoError(t, err)
	var logs logRecords
	mgr := lifecycleManager(t, ms, time.Now().UTC(), &logs)

	err = mgr.RunLifecycle(context.Background())
	require.ErrorIs(t, err, metadata.ErrSearchIndexVersion)
	require.Len(t, logs.find(t, "metadata lifecycle optimized search index"), 1)
	assert.Len(t, logs.find(t, "metadata lifecycle refreshed routed records"), 1, "expiry succeeded")
}

// optimizeFixture returns a database with enough segments for several steps.
func optimizeFixture(t *testing.T, rows int) *metadata.Store {
	t.Helper()
	ms, _ := openIndexDB(t)
	seedHeld(t, ms, rows, "opt")
	return ms
}

// R8: a small merge size takes several steps, reports Complete and keeps search
// results unchanged.
func TestOptimizeSearchIndexCompletesInSteps(t *testing.T) {
	ms := optimizeFixture(t, 300)
	old := metadata.OptimizeMergePages
	metadata.OptimizeMergePages = 2
	t.Cleanup(func() { metadata.OptimizeMergePages = old })
	before := entries(t, ms.DB())

	stats, err := metadata.OptimizeSearchIndex(context.Background(), ms.DB())
	require.NoError(t, err)
	assert.True(t, stats.Complete)
	assert.Greater(t, stats.Steps, 1)
	assert.Positive(t, stats.Longest)
	assert.GreaterOrEqual(t, stats.Total, stats.Longest)
	assert.Equal(t, before, entries(t, ms.DB()))

	again, err := metadata.OptimizeSearchIndex(context.Background(), ms.DB())
	require.NoError(t, err)
	assert.True(t, again.Complete, "a finished index needs no more work")
}

// R8: cancelling after the first step is incomplete; the next call completes.
func TestOptimizeSearchIndexInterruptedIsIncomplete(t *testing.T) {
	ms := optimizeFixture(t, 300)
	old := metadata.OptimizeMergePages
	metadata.OptimizeMergePages = 2
	t.Cleanup(func() { metadata.OptimizeMergePages = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metadata.SetTestHooks(t, metadata.TestHooks{Commit: func(c context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(c, "COMMIT")
		cancel()
		return err
	}})
	stats, err := metadata.OptimizeSearchIndex(ctx, ms.DB())
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, stats.Complete)
	assert.Equal(t, 1, stats.Steps)

	metadata.SetTestHooks(t, metadata.TestHooks{})
	next, err := metadata.OptimizeSearchIndex(context.Background(), ms.DB())
	require.NoError(t, err)
	assert.True(t, next.Complete)
}

// R8: a run stopped by the step cap is incomplete without an error.
func TestOptimizeSearchIndexStepCapIsIncomplete(t *testing.T) {
	ms := optimizeFixture(t, 300)
	oldPages, oldMax := metadata.OptimizeMergePages, metadata.OptimizeMaxSteps
	metadata.OptimizeMergePages, metadata.OptimizeMaxSteps = 2, 1
	t.Cleanup(func() { metadata.OptimizeMergePages, metadata.OptimizeMaxSteps = oldPages, oldMax })

	stats, err := metadata.OptimizeSearchIndex(context.Background(), ms.DB())
	require.NoError(t, err)
	assert.False(t, stats.Complete)
	assert.Equal(t, 1, stats.Steps)
}

// R8: a failed step rolls back, is not counted and leaves the run incomplete.
func TestOptimizeSearchIndexFailedStepIsIncomplete(t *testing.T) {
	ms := optimizeFixture(t, 50)
	metadata.SetTestHooks(t, metadata.TestHooks{Commit: func(ctx context.Context, c *sql.Conn) error {
		return fmt.Errorf("injected commit failure")
	}})
	stats, err := metadata.OptimizeSearchIndex(context.Background(), ms.DB())
	require.ErrorContains(t, err, "injected commit failure")
	assert.False(t, stats.Complete)
	assert.Zero(t, stats.Steps, "an unconfirmed commit is not a completed step")
}

// R8: other writers interleave with the optimize steps; nothing sees BUSY.
func TestOptimizeSearchIndexInterleavesWithWriters(t *testing.T) {
	ms := optimizeFixture(t, 300)
	old := metadata.OptimizeMergePages
	metadata.OptimizeMergePages = 2
	t.Cleanup(func() { metadata.OptimizeMergePages = old })

	var wg sync.WaitGroup
	stop := make(chan struct{})
	writeErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		// A bounded burst: optimize must finish once the writer pauses.
		for i := 0; i < 60; i++ {
			select {
			case <-stop:
				return
			default:
			}
			title := fmt.Sprintf("concurrent writer title %d", i)
			now := time.Now().UTC()
			if err := ms.Upsert(context.Background(), metadata.TransientMetadata{
				VideoID: fmt.Sprintf("w%04d", i%50), Title: &title, FetchedAt: now, ExpiresAt: now.Add(time.Hour), IsActive: true,
			}); err != nil {
				writeErr <- err
				return
			}
		}
	}()
	stats, err := metadata.OptimizeSearchIndex(context.Background(), ms.DB())
	close(stop)
	wg.Wait()
	require.NoError(t, err)
	assert.True(t, stats.Complete)
	select {
	case err := <-writeErr:
		require.NoError(t, err)
	default:
	}
}
