//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Mark-a-legacy-run-as-ended tests (run-initiation ARS-004 §7). Real SQLite,
// the real pipeline and a counting local YouTube endpoint.

const endReason = "diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user"

// endSnapshot is the byte-for-byte snapshot ARS-004 §7 compares.
var endSnapshot = []string{"run", "video", "run_video_outcome", "route_decision", "executed_action",
	"playlist_send_guard", "deferred_item", "error_entry", "quota_ledger"}

// legacySpec is a version-0 production row: created before guarded sending.
func (e *runNowEnv) legacySpec(t *testing.T, runType, status string) seedSpec {
	t.Helper()
	s := e.eligible(t, time.Now().UTC().Add(-48*time.Hour))
	s.runType, s.status, s.initiatedBy, s.cause = runType, status, nil, nil
	s.safety, s.authority = 0, 0
	s.phase = sp("discovery")
	return s
}

// endPair seeds L14 (run-14-shaped, manual, running) and L2 (scheduled,
// interrupted) over a database that already holds a real completed run's
// videos, decisions, guards and actions, plus queue rows L14 holds.
func (e *runNowEnv) endPair(t *testing.T) (l14, l2 int64) {
	t.Helper()
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	adm.Execute(e.ctx)
	require.Equal(t, state.RunStatusCompleted, e.run(t, adm.RunID()).Status)

	l14 = e.seedRun(t, e.legacySpec(t, "manual", "running"))
	s2 := e.legacySpec(t, "scheduled", "interrupted")
	s2.startedAt = s2.startedAt.Add(time.Hour)
	l2 = e.seedRun(t, s2)
	_, err = e.db().Exec(`UPDATE run SET videos_discovered=19615, videos_not_yet_evaluated=19615, videos_accepted=7 WHERE id=?`, l14)
	require.NoError(t, err)

	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	for _, ext := range []string{"V1", "V2"} {
		v, err := e.stateStore.GetVideoByExternalID(e.ctx, ext)
		require.NoError(t, err)
		item, err := qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: v.ID, RunID: l14, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
		if ext == "V2" {
			require.NoError(t, qs.WithRetryTx(e.ctx, func(tx *sql.Tx) error {
				_, claimed, err := qs.ClaimDeferredGroupTx(e.ctx, tx, item, l14, time.Now())
				require.True(t, claimed)
				return err
			}))
		}
	}
	require.NotZero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying'`), "L14 holds a retrying claim")
	require.NotZero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='pending'`))
	return l14, l2
}

// endHandlerOrder is the handler's order: the lock-free precheck, then the
// locked end (ARS-004 §3).
func (e *runNowEnv) endHandlerOrder(id int64) (*EndResult, error) {
	if err := e.pipeline.PrecheckEndLegacyRun(e.ctx, id); err != nil {
		return nil, err
	}
	return e.pipeline.EndLegacyRun(e.ctx, id)
}

func (e *runNowEnv) endRefusal(t *testing.T, id int64, err error) *EndRefusedError {
	t.Helper()
	var refused *EndRefusedError
	require.ErrorAs(t, err, &refused, "run %d", id)
	return refused
}

// P1 (AC1, AC4–7): ending L14 and L2 changes only their status and reason; the
// claims, guards, actions and counters are untouched and the provider is silent.
func TestEndLegacyRunChangesOnlyStatusAndReason(t *testing.T) {
	e := newRunNowEnv(t)
	l14, l2 := e.endPair(t)
	rest := endSnapshot[1:]
	restBefore := e.tableDump(t, rest...)
	b14, b2 := e.run(t, l14), e.run(t, l2)
	requests := e.server.Requests()

	endable, err := e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Equal(t, map[int64]bool{l14: true, l2: true}, endable)

	for _, id := range []int64{l14, l2} {
		res, err := e.endHandlerOrder(id)
		require.NoError(t, err)
		require.Equal(t, id, res.RunID)
	}
	require.Equal(t, requests, e.server.Requests(), "zero provider requests")

	for _, c := range []struct {
		id     int64
		before *state.Run
	}{{l14, b14}, {l2, b2}} {
		after := e.run(t, c.id)
		require.Equal(t, state.RunStatusFailed, after.Status)
		require.Equal(t, endReason, str(after.InterruptReason))
		require.Nil(t, after.InterruptCause)
		want := *c.before
		want.Status, want.InterruptReason = after.Status, after.InterruptReason
		require.Equal(t, want, *after, "only status and reason change; completed_at stays as stored")
	}
	require.Nil(t, e.run(t, l14).CompletedAt, "completed_at stays NULL")

	// Every other table is byte-for-byte identical.
	require.Equal(t, restBefore, e.tableDump(t, rest...))

	endable, err = e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Empty(t, endable)
}

// P2 (AC9): every refusal, through both the precheck and the locked end.
func TestEndLegacyRunRefusalMatrix(t *testing.T) {
	e := newRunNowEnv(t)
	done := time.Now().UTC().Format(state.RunTimestampLayout)
	v1 := func(status string) seedSpec {
		s := e.eligible(t, time.Now().UTC().Add(-time.Hour))
		s.status = status
		if status != "interrupted" {
			s.cause = nil
		}
		return s
	}
	sim := func(runType string) seedSpec {
		s := e.legacySpec(t, runType, "interrupted")
		s.initiatedBy = nil
		return s
	}
	terminal := func(status string) seedSpec {
		s := e.legacySpec(t, "manual", status)
		s.completedAt = &done
		return s
	}
	type refusal struct {
		spec                          seedSpec
		code, status, reason, runType string
	}
	cases := map[string]refusal{
		"v1 running":     {spec: v1("running"), code: EndRunNotEndable, reason: EndReasonNotLegacy},
		"v1 interrupted": {spec: v1("interrupted"), code: EndRunNotEndable, reason: EndReasonNotLegacy},
		"v1 scheduled": {spec: func() seedSpec {
			s := v1("interrupted")
			s.runType = "scheduled"
			s.initiatedBy = sp("schedule")
			return s
		}(), code: EndRunNotEndable, reason: EndReasonNotLegacy},
		"dry run":        {spec: sim("dry_run"), code: EndRunNotEndable, reason: EndReasonNotProduction, runType: "dry_run"},
		"replay":         {spec: sim("replay"), code: EndRunNotEndable, reason: EndReasonNotProduction, runType: "replay"},
		"backtest":       {spec: sim("backtest"), code: EndRunNotEndable, reason: EndReasonNotProduction, runType: "backtest"},
		"completed":      {spec: terminal("completed"), code: EndRunEnded, status: "completed"},
		"failed":         {spec: terminal("failed"), code: EndRunEnded, status: "failed"},
		"stopped":        {spec: terminal("stopped"), code: EndRunEnded, status: "stopped"},
		"v1 failed":      {spec: func() seedSpec { s := v1("failed"); s.completedAt = &done; return s }(), code: EndRunEnded, status: "failed"},
		"v0 running sim": {spec: func() seedSpec { s := sim("dry_run"); s.status = "running"; return s }(), code: EndRunNotEndable, reason: EndReasonNotProduction, runType: "dry_run"},
	}
	ids := map[string]int64{}
	for name, c := range cases {
		if name == "stopped" {
			// A stopped row exists only after a marker: seed it the way Stop does.
			c.spec.status, c.spec.completedAt = "running", nil
			ids[name] = e.seedRun(t, c.spec)
			_, err := e.db().Exec(`UPDATE run SET stop_requested_at=? WHERE id=?`, done, ids[name])
			require.NoError(t, err)
			_, err = e.db().Exec(`UPDATE run SET status='stopped', completed_at=? WHERE id=?`, done, ids[name])
			require.NoError(t, err)
			continue
		}
		ids[name] = e.seedRun(t, c.spec)
	}
	snap := e.tableDump(t, endSnapshot...)
	requests := e.server.Requests()

	check := func(name string, id int64, err error, c refusal) {
		r := e.endRefusal(t, id, err)
		require.Equal(t, c.code, r.Code, name)
		require.Equal(t, c.status, r.Status, name)
		require.Equal(t, c.reason, r.Reason, name)
		require.Equal(t, c.runType, r.RunType, name)
		require.Equal(t, id, r.RunID, name)
	}
	for name, c := range cases {
		id := ids[name]
		check(name+" precheck", id, e.pipeline.PrecheckEndLegacyRun(e.ctx, id), c)
		_, err := e.pipeline.EndLegacyRun(e.ctx, id)
		check(name+" locked", id, err, c)
	}
	const missing = int64(9999)
	missingErr := e.pipeline.PrecheckEndLegacyRun(e.ctx, missing)
	require.Equal(t, EndRunNotFound, e.endRefusal(t, missing, missingErr).Code)
	_, err := e.pipeline.EndLegacyRun(e.ctx, missing)
	require.Equal(t, EndRunNotFound, e.endRefusal(t, missing, err).Code)

	require.Equal(t, snap, e.tableDump(t, endSnapshot...), "nothing changed")
	require.Equal(t, requests, e.server.Requests())

	endable, err := e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Empty(t, endable, "no refused seed reads endable")
}

// P3: a run with a control is executing and is not endable.
func TestEndLegacyRunRefusesExecuting(t *testing.T) {
	e := newRunNowEnv(t)
	id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
	e.pipeline.registerRun(id)
	snap := e.tableDump(t, endSnapshot...)

	endable, err := e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Empty(t, endable)

	_, err = e.pipeline.EndLegacyRun(e.ctx, id)
	r := e.endRefusal(t, id, err)
	require.Equal(t, EndRunNotEndable, r.Code)
	require.Equal(t, EndReasonExecuting, r.Reason)
	require.Equal(t, snap, e.tableDump(t, endSnapshot...))
	require.True(t, e.pipeline.ExecutingRuns()[id], "executing comes from the in-process control")

	e.pipeline.unregisterRun(id)
	require.False(t, e.pipeline.ExecutingRuns()[id])
	_, err = e.pipeline.EndLegacyRun(e.ctx, id)
	require.NoError(t, err)
}

// P4: a production run holding or registered refuses; a simulation does not.
func TestEndLegacyRunProductionActive(t *testing.T) {
	t.Run("a version-1 Execute holds the production lock", func(t *testing.T) {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		e.stallOn("V2")
		adm, err := e.pipeline.AdmitRunNow(e.ctx)
		require.NoError(t, err)
		runCtx, cancel := context.WithCancel(e.ctx)
		done := startExecution(func() { adm.Execute(runCtx) })
		e.waitBlocked(t)

		endable, err := e.pipeline.EndableRuns(e.ctx)
		require.NoError(t, err)
		require.Empty(t, endable, "a registered production run makes every run read not endable")

		before := e.run(t, id)
		start := time.Now()
		_, err = e.pipeline.EndLegacyRun(e.ctx, id)
		require.ErrorIs(t, err, ErrProductionRunActive)
		require.Less(t, time.Since(start), 5*time.Second, "refuses without waiting")
		require.Equal(t, before, e.run(t, id))
		cancel()
		waitDone(t, done)
	})
	t.Run("a registered version-1 control with the lock free", func(t *testing.T) {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		other := e.seedRun(t, e.eligible(t, time.Now().UTC()))
		e.pipeline.registerRun(other)
		snap := e.tableDump(t, endSnapshot...)
		_, err := e.pipeline.EndLegacyRun(e.ctx, id)
		require.ErrorIs(t, err, ErrProductionRunActive)
		require.Equal(t, snap, e.tableDump(t, endSnapshot...))
	})
	t.Run("a registered simulation does not refuse", func(t *testing.T) {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		sim := e.legacySpec(t, "dry_run", "running")
		sim.initiatedBy = nil
		simID := e.seedRun(t, sim)
		e.pipeline.registerRun(simID)
		endable, err := e.pipeline.EndableRuns(e.ctx)
		require.NoError(t, err)
		require.Equal(t, map[int64]bool{id: true}, endable)
		_, err = e.pipeline.EndLegacyRun(e.ctx, id)
		require.NoError(t, err)
		require.Equal(t, state.RunStatusFailed, e.run(t, id).Status)
		require.Equal(t, state.RunStatusRunning, e.run(t, simID).Status)
	})
}

// P5: while the startup gate is closed the end is refused before any lock.
func TestEndLegacyRunRefusedWhileRecovering(t *testing.T) {
	e := newRunNowEnv(t)
	id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
	e.pipeline.producerGate.Store(producersGated)
	e.pipeline.productionMu.Lock() // a lock attempt would report ErrProductionRunActive instead
	snap := e.tableDump(t, endSnapshot...)
	_, err := e.pipeline.EndLegacyRun(e.ctx, id)
	require.ErrorIs(t, err, ErrStartupRecoveryActive)
	e.pipeline.productionMu.Unlock()
	require.True(t, e.pipeline.initiationMu.TryLock(), "initiationMu stays free")
	e.pipeline.initiationMu.Unlock()
	require.Equal(t, snap, e.tableDump(t, endSnapshot...))
	endable, err := e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Empty(t, endable, "nothing is endable while the gate is closed")
}

// P6 (AC4): a failing write leaves the run unchanged and still endable.
func TestEndLegacyRunIsAtomic(t *testing.T) {
	e := newRunNowEnv(t)
	id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
	_, err := e.db().Exec(`CREATE TRIGGER inject BEFORE UPDATE ON run BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	snap := e.tableDump(t, endSnapshot...)

	_, err = e.pipeline.EndLegacyRun(e.ctx, id)
	require.ErrorIs(t, err, ErrEndNotApplied)
	var notApplied *EndNotAppliedError
	require.ErrorAs(t, err, &notApplied)
	require.Equal(t, "running", notApplied.Status)
	require.Equal(t, snap, e.tableDump(t, endSnapshot...))

	_, err = e.db().Exec(`DROP TRIGGER inject`)
	require.NoError(t, err)
	endable, err := e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.True(t, endable[id], "still endable")
}

// P7: a failing Commit is resolved by reading the row back.
func TestEndLegacyRunCommitErrors(t *testing.T) {
	commitFailed := fmtCommitErr()
	t.Run("commit failed after it took effect", func(t *testing.T) {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		qs, err := queue.NewStore(e.db())
		require.NoError(t, err)
		e.pipeline.endTx = func(ctx context.Context, fn func(*sql.Tx) error) error {
			if err := qs.WithRetryTx(ctx, fn); err != nil {
				return err
			}
			return commitFailed
		}
		res, err := e.pipeline.EndLegacyRun(e.ctx, id)
		require.NoError(t, err)
		require.Equal(t, id, res.RunID)
		require.Equal(t, state.RunStatusFailed, e.run(t, id).Status)
	})
	t.Run("commit failed without taking effect", func(t *testing.T) {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "scheduled", "interrupted"))
		e.pipeline.endTx = func(ctx context.Context, fn func(*sql.Tx) error) error { return commitFailed }
		_, err := e.pipeline.EndLegacyRun(e.ctx, id)
		require.ErrorIs(t, err, ErrEndNotApplied)
		var notApplied *EndNotAppliedError
		require.ErrorAs(t, err, &notApplied)
		require.Equal(t, "interrupted", notApplied.Status)
		require.Equal(t, state.RunStatusInterrupted, e.run(t, id).Status)
	})
	t.Run("follow-up read also fails", func(t *testing.T) {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		_, err := e.db().Exec(`ALTER TABLE run RENAME COLUMN interrupt_reason TO interrupt_reason_x`)
		require.NoError(t, err)
		e.pipeline.endTx = func(ctx context.Context, fn func(*sql.Tx) error) error { return commitFailed }
		_, err = e.pipeline.EndLegacyRun(e.ctx, id)
		require.ErrorIs(t, err, ErrEndOutcomeUnknown)
	})
}

// P8 (AC10): a repeat always says "already ended", even while a production run
// holds the lock or the startup gate is closed.
func TestEndLegacyRunIsIdempotent(t *testing.T) {
	e := newRunNowEnv(t)
	id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
	_, err := e.endHandlerOrder(id)
	require.NoError(t, err)
	after := e.tableDump(t, endSnapshot...)

	_, err = e.endHandlerOrder(id)
	r := e.endRefusal(t, id, err)
	require.Equal(t, EndRunEnded, r.Code)
	require.Equal(t, "failed", r.Status)
	require.Equal(t, after, e.tableDump(t, endSnapshot...))

	e.pipeline.productionMu.Lock()
	_, err = e.endHandlerOrder(id)
	e.pipeline.productionMu.Unlock()
	require.Equal(t, EndRunEnded, e.endRefusal(t, id, err).Code, "not production_run_active")

	e.pipeline.producerGate.Store(producersGated)
	_, err = e.endHandlerOrder(id)
	require.Equal(t, EndRunEnded, e.endRefusal(t, id, err).Code, "not startup_recovery_active")
	require.Equal(t, after, e.tableDump(t, endSnapshot...))
}

// P9 (a): two concurrent ends of one run give one success and one run_ended.
func TestEndLegacyRunConcurrentEnds(t *testing.T) {
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for n := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					_, errs[n] = e.endHandlerOrder(id)
					if !errors.Is(errs[n], ErrProductionRunActive) {
						return
					}
				}
			}()
		}
		wg.Wait()
		var ok, ended int
		for _, err := range errs {
			var refused *EndRefusedError
			switch {
			case err == nil:
				ok++
			case errors.As(err, &refused) && refused.Code == EndRunEnded:
				ended++
			default:
				t.Fatalf("iteration %d: unexpected %v", i, err)
			}
		}
		require.Equal(t, 1, ok, "iteration %d", i)
		require.Equal(t, 1, ended, "iteration %d", i)
		require.Equal(t, endReason, str(e.run(t, id).InterruptReason))
	}
}

// P9 (b): end vs a Run Now press with no version-1 candidate. Either the end
// commits and the press starts a new run, or the press starts first and holds
// the lock and the end is refused with the run unchanged.
func TestEndLegacyRunVersusRunNowPress(t *testing.T) {
	var endedFirst, pressedFirst int
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		var endErr, pressErr error
		var adm *RunNowAdmission
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, endErr = e.pipeline.EndLegacyRun(e.ctx, id) }()
		go func() {
			defer wg.Done()
			<-start
			for {
				adm, pressErr = e.pipeline.AdmitRunNow(e.ctx)
				if !errors.Is(pressErr, ErrProductionRunActive) {
					return
				}
			}
		}()
		close(start)
		wg.Wait()
		require.NoError(t, pressErr, "iteration %d: the press never fails; it never selects L14", i)
		require.False(t, adm.Resumed())
		require.NotEqual(t, id, adm.RunID())
		switch {
		case endErr == nil:
			endedFirst++
			require.Equal(t, endReason, str(e.run(t, id).InterruptReason))
		case errors.Is(endErr, ErrProductionRunActive):
			pressedFirst++
			require.Equal(t, state.RunStatusRunning, e.run(t, id).Status, "iteration %d: refused end changes nothing", i)
			require.Nil(t, e.run(t, id).InterruptCause)
		default:
			t.Fatalf("iteration %d: unexpected %v", i, endErr)
		}
		adm.Release()
	}
	t.Logf("end first %d, press first %d", endedFirst, pressedFirst)
}

// P9 (c): end vs a scheduler StartRun, the same two orders.
func TestEndLegacyRunVersusScheduledStart(t *testing.T) {
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		var endErr, startErr error
		var run *state.Run
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, endErr = e.pipeline.EndLegacyRun(e.ctx, id)
		}()
		go func() { defer wg.Done(); <-start; run, startErr = e.pipeline.StartRun(e.ctx, state.RunTypeScheduled) }()
		close(start)
		wg.Wait()
		require.NoError(t, startErr, "iteration %d", i)
		switch {
		case endErr == nil:
			require.Equal(t, endReason, str(e.run(t, id).InterruptReason))
		case errors.Is(endErr, ErrProductionRunActive):
			require.Equal(t, state.RunStatusRunning, e.run(t, id).Status, "iteration %d: refused end changes nothing", i)
		default:
			t.Fatalf("iteration %d: unexpected %v", i, endErr)
		}
		e.pipeline.unregisterRun(run.ID)
	}
}

// P9 (d): end of L14 vs Discard of a discardable version-1 row: both succeed,
// in either order.
func TestEndLegacyRunVersusDiscard(t *testing.T) {
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		broken := e.eligible(t, time.Now().UTC().Add(-2*time.Hour))
		broken.phase = sp("bogus")
		rID := e.seedRun(t, broken)
		var endErr, discardErr error
		var wg sync.WaitGroup
		wg.Add(2)
		retry := func(fn func() error) error {
			for {
				if err := fn(); !errors.Is(err, ErrProductionRunActive) {
					return err
				}
			}
		}
		go func() {
			defer wg.Done()
			endErr = retry(func() error { _, err := e.pipeline.EndLegacyRun(e.ctx, id); return err })
		}()
		go func() {
			defer wg.Done()
			discardErr = retry(func() error { _, err := e.pipeline.DiscardRun(e.ctx, rID); return err })
		}()
		wg.Wait()
		require.NoError(t, endErr, "iteration %d", i)
		require.NoError(t, discardErr, "iteration %d", i)
		require.Equal(t, endReason, str(e.run(t, id).InterruptReason))
		require.Equal(t, state.RunStatusFailed, e.run(t, rID).Status)
		require.NotEqual(t, endReason, str(e.run(t, rID).InterruptReason))
	}
}

// U1 (pipeline part): startup classification ignores an ended row, whatever
// its completed_at.
func TestEndedRunsAreOutsideStartupClassification(t *testing.T) {
	e := newRunNowEnv(t)
	l14, l2 := e.endPair(t)
	recoverable := e.seedRun(t, e.eligible(t, time.Now().UTC().Add(-time.Hour)))
	s := e.eligible(t, time.Now().UTC().Add(-time.Minute))
	s.runType, s.initiatedBy = "scheduled", sp("schedule")
	scheduled := e.seedRun(t, s)
	before, err := e.pipeline.classifyStartupRuns(e.ctx)
	require.NoError(t, err)
	require.Contains(t, before, recoverable)
	require.Contains(t, before, scheduled)

	for _, id := range []int64{l14, l2} {
		_, err := e.endHandlerOrder(id)
		require.NoError(t, err)
	}
	_, err = e.db().Exec(`UPDATE run SET completed_at=? WHERE id=?`, time.Now().UTC().Format(state.RunTimestampLayout), l2)
	require.NoError(t, err)
	after, err := e.pipeline.classifyStartupRuns(e.ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after, "classification returns exactly the version-1 recoverable rows")
	require.NotContains(t, after, l14)
	require.NotContains(t, after, l2)
}

// U2 (AC8): after ending, a Run Now press and a scheduled start succeed and
// never select the ended runs. L14's retrying claim is reclaimable as a failed
// owner, so it blocks nothing.
func TestEndedRunNeverResumedAndNeverBlocks(t *testing.T) {
	e := newRunNowEnv(t)
	l14, l2 := e.endPair(t)
	for _, id := range []int64{l14, l2} {
		_, err := e.endHandlerOrder(id)
		require.NoError(t, err)
	}

	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.False(t, adm.Resumed())
	require.NotContains(t, []int64{l14, l2}, adm.RunID())
	adm.Execute(e.ctx)
	require.Equal(t, state.RunStatusCompleted, e.run(t, adm.RunID()).Status)

	run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	require.NotContains(t, []int64{l14, l2}, run.ID)
	e.pipeline.Execute(e.ctx, run.ID)
	require.Equal(t, state.RunStatusCompleted, e.run(t, run.ID).Status)

	for _, id := range []int64{l14, l2} {
		r := e.run(t, id)
		require.Equal(t, state.RunStatusFailed, r.Status)
		require.Equal(t, endReason, str(r.InterruptReason))
	}
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying' AND run_id IN (?,?)`, l14, l2),
		"the claim L14 held was reclaimed by a later run")
}

// R1/R2 (AC12): endable is true only for the two stuck legacy runs and is
// withdrawn while a production run is registered; a stale true is refused.
func TestEndableReadAndStaleTrue(t *testing.T) {
	e := newRunNowEnv(t)
	l14, l2 := e.endPair(t)
	e.seedRun(t, e.eligible(t, time.Now().UTC()))

	endable, err := e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Equal(t, map[int64]bool{l14: true, l2: true}, endable)

	// R2: read endable:true for L14, start a production run, then end.
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	_, err = e.pipeline.EndLegacyRun(e.ctx, l14)
	require.ErrorIs(t, err, ErrProductionRunActive)
	require.Equal(t, state.RunStatusRunning, e.run(t, l14).Status)
	endable, err = e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Empty(t, endable, "withdrawn while a production run is registered")
	adm.Release()

	endable, err = e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Equal(t, map[int64]bool{l14: true, l2: true}, endable)

	_, err = e.pipeline.EndLegacyRun(e.ctx, l14)
	require.NoError(t, err)
	endable, err = e.pipeline.EndableRuns(e.ctx)
	require.NoError(t, err)
	require.Equal(t, map[int64]bool{l2: true}, endable, "false for L14 once ended")
}

// H2: ending does not touch the schedule or emit a run event.
func TestEndLegacyRunEmitsNoRunEvent(t *testing.T) {
	e := newRunNowEnv(t)
	id := e.seedRun(t, e.legacySpec(t, "manual", "running"))
	events := e.pipeline.broadcast.Subscribe(id)
	defer e.pipeline.broadcast.Unsubscribe(id, events)
	_, err := e.pipeline.EndLegacyRun(e.ctx, id)
	require.NoError(t, err)
	select {
	case ev := <-events:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
