package scheduler

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "scheduler.db")
	db, err := store.Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestParseSchedule_Interval(t *testing.T) {
	p, err := ParseSchedule("@every 30m")
	require.NoError(t, err)

	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	next, err := p.Next(now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(30*time.Minute), next)
}

func TestParseSchedule_CronExact(t *testing.T) {
	p, err := ParseSchedule("0 9 * * *")
	require.NoError(t, err)

	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	next, err := p.Next(now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC), next)
}

func TestParseSchedule_CronStep(t *testing.T) {
	p, err := ParseSchedule("0 */6 * * *")
	require.NoError(t, err)

	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	next, err := p.Next(now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), next)
}

func TestParseSchedule_Invalid(t *testing.T) {
	_, err := ParseSchedule("")
	require.Error(t, err)
	_, err = ParseSchedule("0 9 * *")
	require.Error(t, err)
	_, err = ParseSchedule("@every")
	require.Error(t, err)
}

func TestStore_ScheduleRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	store, err := NewStore(db)
	require.NoError(t, err)

	sch := &Schedule{Expression: "0 */6 * * *", Enabled: true}
	require.NoError(t, store.SetSchedule(ctx, sch))

	got, err := store.GetSchedule(ctx)
	require.NoError(t, err)
	assert.Equal(t, sch.Expression, got.Expression)
	assert.Equal(t, sch.Enabled, got.Enabled)
}

type fakeRun struct {
	id        int64
	startedAt time.Time
	runType   state.RunType
	status    state.RunStatus
}

type fakeStateStore struct {
	mu   sync.Mutex
	runs map[int64]*fakeRun
}

func newFakeStateStore() *fakeStateStore {
	return &fakeStateStore{runs: make(map[int64]*fakeRun)}
}

func (s *fakeStateStore) addRun(r *fakeRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.id] = r
}

func (s *fakeStateStore) GetRun(ctx context.Context, id int64) (*state.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, state.ErrNotFound
	}
	return &state.Run{
		ID:        r.id,
		RunType:   r.runType,
		Status:    r.status,
		StartedAt: r.startedAt,
	}, nil
}

func (s *fakeStateStore) ListInterruptedRuns(ctx context.Context) ([]state.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []state.Run
	for _, r := range s.runs {
		if r.status == state.RunStatusInterrupted {
			out = append(out, state.Run{
				ID:        r.id,
				RunType:   r.runType,
				Status:    r.status,
				StartedAt: r.startedAt,
			})
		}
	}
	return out, nil
}

type fakePipeline struct {
	mu       sync.Mutex
	started  []*state.Run
	executed []int64
}

func (p *fakePipeline) StartRun(ctx context.Context, runType state.RunType) (*state.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	run := &state.Run{ID: int64(len(p.started) + 1), RunType: runType, Status: state.RunStatusRunning}
	p.started = append(p.started, run)
	return run, nil
}

func (p *fakePipeline) Execute(ctx context.Context, runID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.executed = append(p.executed, runID)
}

func (p *fakePipeline) StartedRuns() []*state.Run {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*state.Run, len(p.started))
	copy(out, p.started)
	return out
}

func (p *fakePipeline) ExecutedRuns() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int64, len(p.executed))
	copy(out, p.executed)
	return out
}

func newTestScheduler(t *testing.T, store *Store, pipeline Pipeline, stateStore StateStore) *Scheduler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewScheduler(store, pipeline, stateStore, WithLogger(logger))
}

func TestScheduler_ResumesInterruptedRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := openTestDB(t)
	store, err := NewStore(db)
	require.NoError(t, err)

	stateStore := newFakeStateStore()
	stateStore.addRun(&fakeRun{id: 7, status: state.RunStatusInterrupted, runType: state.RunTypeScheduled, startedAt: time.Now().UTC()})
	stateStore.addRun(&fakeRun{id: 8, status: state.RunStatusCompleted, runType: state.RunTypeScheduled, startedAt: time.Now().UTC()})

	pipeline := &fakePipeline{}
	sch := newTestScheduler(t, store, pipeline, stateStore)

	require.NoError(t, sch.Start(ctx))
	defer sch.Stop()

	require.Eventually(t, func() bool {
		return len(pipeline.ExecutedRuns()) >= 1
	}, 2*time.Second, 50*time.Millisecond)

	executed := pipeline.ExecutedRuns()
	require.Len(t, executed, 1)
	assert.Equal(t, int64(7), executed[0])
}

func TestScheduler_TriggersScheduledRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := openTestDB(t)
	store, err := NewStore(db)
	require.NoError(t, err)

	// Set a schedule that fires every minute. The scheduler checks every minute,
	// so starting at 00:00:59 and ticking to 00:01:00 should trigger.
	require.NoError(t, store.SetSchedule(ctx, &Schedule{Expression: "@every 1s", Enabled: true}))

	stateStore := newFakeStateStore()
	pipeline := &fakePipeline{}
	sch := NewScheduler(store, pipeline, stateStore,
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithTickInterval(100*time.Millisecond))

	require.NoError(t, sch.Start(ctx))
	defer sch.Stop()

	require.Eventually(t, func() bool {
		return len(pipeline.StartedRuns()) >= 1
	}, 3*time.Second, 100*time.Millisecond)

	started := pipeline.StartedRuns()
	require.Len(t, started, 1)
	assert.Equal(t, state.RunTypeScheduled, started[0].RunType)
}

func TestScheduler_DisabledScheduleDoesNotTrigger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := openTestDB(t)
	store, err := NewStore(db)
	require.NoError(t, err)
	require.NoError(t, store.SetSchedule(ctx, &Schedule{Expression: "* * * * *", Enabled: false}))

	stateStore := newFakeStateStore()
	pipeline := &fakePipeline{}
	sch := NewScheduler(store, pipeline, stateStore,
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithTickInterval(100*time.Millisecond))

	require.NoError(t, sch.Start(ctx))
	defer sch.Stop()

	time.Sleep(300 * time.Millisecond)
	assert.Empty(t, pipeline.StartedRuns())
}

var _ = io.Discard
var _ = os.Remove
