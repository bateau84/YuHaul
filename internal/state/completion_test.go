package state

import (
	"context"
	"database/sql/driver"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// Pause the real counter write inside SQLite, while a separate WAL reader
// observes the externally visible run. No scheduler timing is needed.
func TestCompleteRunPublishesAfterCounters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	name := "pause_counters_" + uuid.New().String()[:8]
	require.NoError(t, sqlite.RegisterScalarFunction(name, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		close(entered)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "completion.db"))
	require.NoError(t, err)
	defer db.Close()
	defer unblock()
	s, err := NewStore(db)
	require.NoError(t, err)
	m := NewMachine(s)
	id := seedRun(t, m)
	_, err = m.DiscoverVideo(ctx, id, "candidate", "channel")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TRIGGER pause_counter_write BEFORE UPDATE OF videos_discovered ON run BEGIN SELECT `+name+`(); END`)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := m.CompleteRun(ctx, id); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	observed, err := s.GetRun(ctx, id)
	require.NoError(t, err)
	require.Equal(t, RunStatusRunning, observed.Status, "completion must not precede the final counter write")
	unblock()
	require.NoError(t, <-done)
	observed, err = s.GetRun(ctx, id)
	require.NoError(t, err)
	require.Equal(t, RunStatusCompleted, observed.Status)
	require.Equal(t, 1, observed.VideosDiscovered)
	require.Equal(t, 1, observed.VideosNotYetEvaluated)
}

func TestCompleteRunCounterFailureDoesNotPublishCompleted(t *testing.T) {
	m := newTestMachine(t)
	id := seedRun(t, m)
	_, err := m.store.db.Exec(`CREATE TRIGGER fail_counter_write BEFORE UPDATE OF videos_discovered ON run BEGIN SELECT RAISE(ABORT, 'counter write failed'); END`)
	require.NoError(t, err)
	_, err = m.CompleteRun(context.Background(), id)
	require.ErrorContains(t, err, "counter write failed")
	run, err := m.store.GetRun(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, RunStatusRunning, run.Status)
	require.Nil(t, run.CompletedAt)
}
