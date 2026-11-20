// Package scheduler implements YuHaul's internal cron scheduler.
//
// It reads a user-configurable schedule from the database, resumes any
// interrupted runs on startup, triggers scheduled production runs, and ensures
// deferred work is processed before new discoveries.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/state"
)

// Pipeline is the subset of the pipeline package used by the scheduler.
type Pipeline interface {
	StartRun(ctx context.Context, runType state.RunType) (*state.Run, error)
	Execute(ctx context.Context, runID int64)
}

// StateStore provides the scheduler with run and deferred-item queries.
type StateStore interface {
	GetRun(ctx context.Context, id int64) (*state.Run, error)
	ListInterruptedRuns(ctx context.Context) ([]state.Run, error)
}

// Scheduler triggers production runs according to a user-configurable schedule
// and resumes interrupted runs.
type Scheduler struct {
	store        *Store
	pipeline     Pipeline
	stateStore   StateStore
	logger       *slog.Logger
	tickInterval time.Duration

	mu         sync.Mutex
	running    bool
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	lastFiring time.Time
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *Scheduler) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithTickInterval sets how often the scheduler checks whether a scheduled run
// is due. It is used by tests to avoid waiting a full minute.
func WithTickInterval(d time.Duration) Option {
	return func(s *Scheduler) {
		if d > 0 {
			s.tickInterval = d
		}
	}
}

// NewScheduler constructs a scheduler. It does not start any goroutines until
// Start is called.
func NewScheduler(store *Store, pipeline Pipeline, stateStore StateStore, opts ...Option) *Scheduler {
	sch := &Scheduler{
		store:        store,
		pipeline:     pipeline,
		stateStore:   stateStore,
		logger:       slog.Default(),
		tickInterval: time.Minute,
	}
	for _, opt := range opts {
		opt(sch)
	}
	return sch
}

// Start begins the background schedule checker. Startup recovery must already be complete.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("scheduler: already running")
	}

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(ctx)
	}()

	s.logger.InfoContext(ctx, "scheduler started")
	return nil
}

// Stop cancels the scheduler context and waits for goroutines to finish.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.InfoContext(ctx, "scheduler stopped")
			return
		case now := <-ticker.C:
			s.tick(ctx, now)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	sch, err := s.store.GetSchedule(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "scheduler: failed to read schedule", "diagnostic", diag.Encode(err, "unknown", "load_run"))
		return
	}
	if !sch.Enabled || sch.Expression == "" {
		return
	}

	parser, err := ParseSchedule(sch.Expression)
	if err != nil {
		s.logger.ErrorContext(ctx, "scheduler: invalid schedule expression", "diagnostic", diag.Encode(err, "unknown", "select_inputs"))
		return
	}

	next, err := parser.Next(s.lastFiring)
	if err != nil {
		s.logger.ErrorContext(ctx, "scheduler: failed to compute next firing", "diagnostic", diag.Encode(err, "unknown", "select_inputs"))
		return
	}

	// Trigger if the computed next firing falls at or before now. Guard against
	// firing twice within the same minute by updating lastFiring immediately.
	if next.After(now) {
		return
	}
	if now.Sub(s.lastFiring) < time.Minute {
		return
	}
	s.lastFiring = now

	s.logger.InfoContext(ctx, "scheduler: triggering scheduled run", "scheduled_for", next)
	run, err := s.pipeline.StartRun(ctx, state.RunTypeScheduled)
	if err != nil {
		s.logger.ErrorContext(ctx, "scheduler: failed to start scheduled run", "diagnostic", diag.Encode(err, "unknown", "start_run"))
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.pipeline.Execute(ctx, run.ID)
	}()
}

// compile-time interface check.
var _ StateStore = (*state.Store)(nil)
