package pipeline

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func setupPipelineTest(t *testing.T) (*sql.DB, *state.Store, *state.Machine, *Broadcaster, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	machine := state.NewMachine(stateStore)
	broadcast := NewBroadcaster()

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, stateStore, machine, broadcast, cleanup
}

func TestPipeline_StartRun(t *testing.T) {
	ctx := context.Background()
	_, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	p := &Pipeline{
		stateStore: stateStore,
		machine:    machine,
		broadcast:  broadcast,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// In a fresh database the first run is ID 1, so subscribe before creating it.
	sub := broadcast.Subscribe(1)
	defer broadcast.Unsubscribe(1, sub)

	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	assert.Equal(t, int64(1), run.ID)
	assert.Equal(t, state.RunStatusRunning, run.Status)

	select {
	case ev := <-sub:
		assert.Equal(t, EventRunStarted, ev.Type)
		assert.Equal(t, run.ID, ev.RunID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for start event")
	}
}

func TestPipeline_StartRun_UnsupportedType(t *testing.T) {
	ctx := context.Background()
	_, stateStore, machine, _, cleanup := setupPipelineTest(t)
	defer cleanup()

	p := &Pipeline{
		stateStore: stateStore,
		machine:    machine,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	_, err := p.StartRun(ctx, state.RunTypeDryRun)
	require.Error(t, err)
}

var _ = io.Discard
