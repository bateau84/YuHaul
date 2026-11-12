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

// Discard tests (run-initiation ARS-002 §6.2 P1–P12, ARS-003 test D1/V1). Real
// SQLite, the real pipeline and a counting local YouTube endpoint.

// everything is the "snapshot" ARS-002 §6 compares byte for byte.
var everything = []string{"run", "deferred_item", "error_entry", "playlist_send_guard", "executed_action", "run_video_outcome"}

// unusableRun returns a real time-limit-interrupted Run Now run (V1 sent, V2's
// POST in flight with a retained guard, V3 untouched) whose snapshot was then
// deleted, so Run Now refuses it as snapshot_unavailable.
func (e *runNowEnv) unusableRun(t *testing.T) int64 {
	t.Helper()
	e.stallOn("V2")
	adm := e.interruptedAtLimit(t, testLimit)
	e.breakSnapshot(t, adm.RunID())
	e.stallOn()
	return adm.RunID()
}

func (e *runNowEnv) breakSnapshot(t *testing.T, runID int64) {
	t.Helper()
	var cfg int64
	require.NoError(t, e.db().QueryRow(`SELECT config_version_id FROM run WHERE id=?`, runID).Scan(&cfg))
	_, err := e.db().Exec(`DELETE FROM config_version WHERE id=?`, cfg)
	require.NoError(t, err)
}

func (e *runNowEnv) discardErr(t *testing.T, runID int64) *DiscardRefusedError {
	t.Helper()
	_, err := e.pipeline.DiscardRun(e.ctx, runID)
	var refused *DiscardRefusedError
	require.ErrorAs(t, err, &refused)
	return refused
}

// P5, P8 and P9 on one real run: the handoff shapes, untouched guards, no
// provider request, idempotency, and the next run's handling of the target.
func TestDiscardHandsOverWorkAndKeepsGuards(t *testing.T) {
	e := newRunNowEnv(t)
	runID := e.unusableRun(t)
	before := e.run(t, runID)
	guards := e.tableDump(t, "playlist_send_guard", "executed_action")
	requests := e.server.Requests()

	res, err := e.pipeline.DiscardRun(e.ctx, runID)
	require.NoError(t, err)
	require.Equal(t, runID, res.RunID)
	require.Equal(t, DiscardCauseSnapshotUnavailable, res.Cause)
	require.Equal(t, requests, e.server.Requests(), "AC18: zero provider requests during discard")
	require.Equal(t, guards, e.tableDump(t, "playlist_send_guard", "executed_action"), "guards and actions unchanged")

	r := e.run(t, runID)
	require.Equal(t, state.RunStatusFailed, r.Status)
	require.NotNil(t, r.CompletedAt)
	require.Nil(t, r.InterruptCause)
	require.Equal(t, "diag:v1 phase=unknown operation=discard_run code=snapshot_unavailable", str(r.InterruptReason))
	require.Equal(t, before.DurationSeconds, r.DurationSeconds, "duration is not written")
	require.Equal(t, before.StartedAt, r.StartedAt)
	require.Equal(t, str(before.CurrentPhase), str(r.CurrentPhase))
	require.Equal(t, before.InitiatedBy, r.InitiatedBy)
	require.Equal(t, map[string]int{"V2": 1, "V3": 1}, e.pendingHandoffs(t, "discarded_by_user"))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry`), "a discard creates no Errors-queue entry")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying'`))
	require.NoError(t, e.stateStore.RecomputeRunCounters(e.ctx, runID))
	require.Equal(t, r.VideosAccepted, e.run(t, runID).VideosAccepted, "counters equal a fresh recompute")

	// P9: a second discard is refused and changes nothing.
	snap := e.tableDump(t, everything...)
	refused := e.discardErr(t, runID)
	require.Equal(t, DiscardRunEnded, refused.Code)
	require.Equal(t, "failed", refused.Status)
	require.Equal(t, snap, e.tableDump(t, everything...))

	// P8: the next run claims the handed-over items, sends nothing more to the
	// guarded V2, and sends V3.
	v2Posts := e.postsFor("V2")
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.False(t, adm.Resumed())
	adm.Execute(e.ctx)
	require.Equal(t, v2Posts, e.postsFor("V2"), "the guarded target is not re-sent")
	require.Equal(t, 1, e.postsFor("V3"))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM error_entry`), "the guard block is a handled failure in the next run")
	// P12: the failed run never makes a later start fail, and is never selected.
	require.Equal(t, state.RunStatusFailed, e.run(t, runID).Status)
	ids, err := e.pipeline.classifyStartupRuns(e.ctx)
	require.NoError(t, err)
	require.NotContains(t, ids, runID)
}

// P1: only the oldest unusable candidate is discarded; every other request is
// refused with the expected code and changes nothing.
func TestDiscardEligibilityMatrix(t *testing.T) {
	e := newRunNowEnv(t)
	at := time.Now().UTC().Add(-3 * time.Hour)
	done := time.Now().UTC().Format(state.RunTimestampLayout)

	oldest := e.eligible(t, at)
	oldest.phase = sp("bogus")
	oldestID := e.seedRun(t, oldest)
	youngUsable := e.seedRun(t, e.eligible(t, at.Add(time.Hour)))
	youngUnusable := e.eligible(t, at.Add(2*time.Hour))
	youngUnusable.phase = sp("bogus")
	youngUnusableID := e.seedRun(t, youngUnusable)

	type kind struct {
		spec       seedSpec
		code, info string
	}
	mod := func(f func(*seedSpec)) seedSpec { s := e.eligible(t, at.Add(3*time.Hour)); f(&s); return s }
	kinds := map[string]kind{
		"mutation safety 0":   {mod(func(s *seedSpec) { s.safety = 0 }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"authority version 0": {mod(func(s *seedSpec) { s.authority = 0 }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"NULL origin":         {mod(func(s *seedSpec) { s.initiatedBy = nil }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"queue retry":         {mod(func(s *seedSpec) { s.initiatedBy = sp("queue_retry") }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"scheduled":           {mod(func(s *seedSpec) { s.runType = "scheduled"; s.initiatedBy = sp("schedule") }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"shutdown cause":      {mod(func(s *seedSpec) { s.cause = sp("shutdown") }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"NULL cause":          {mod(func(s *seedSpec) { s.cause = nil }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"failed":              {mod(func(s *seedSpec) { s.status = "failed"; s.cause = nil; s.completedAt = &done }), DiscardRunEnded, "failed"},
		"completed":           {mod(func(s *seedSpec) { s.status = "completed"; s.cause = nil; s.completedAt = &done }), DiscardRunEnded, "completed"},
		"dry_run":             {mod(func(s *seedSpec) { s.runType = "dry_run"; s.initiatedBy = nil; s.safety = 0; s.authority = 0 }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"replay":              {mod(func(s *seedSpec) { s.runType = "replay"; s.initiatedBy = nil; s.safety = 0; s.authority = 0 }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"backtest":            {mod(func(s *seedSpec) { s.runType = "backtest"; s.initiatedBy = nil; s.safety = 0; s.authority = 0 }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
		"running":             {mod(func(s *seedSpec) { s.status = "running"; s.cause = nil }), DiscardRunNotDiscardable, DiscardReasonNotEligible},
	}
	ids := map[string]int64{}
	for name, k := range kinds {
		ids[name] = e.seedRun(t, k.spec)
	}

	check := func(name string, id int64, code, info string) {
		snap := e.tableDump(t, everything...)
		refused := e.discardErr(t, id)
		require.Equal(t, code, refused.Code, name)
		if code == DiscardRunEnded {
			require.Equal(t, info, refused.Status, name)
		} else if code == DiscardRunNotDiscardable {
			require.Equal(t, info, refused.Reason, name)
		}
		require.Equal(t, snap, e.tableDump(t, everything...), "%s: a refusal changes nothing", name)
	}
	for name, k := range kinds {
		check(name, ids[name], k.code, k.info)
	}
	check("younger usable", youngUsable, DiscardRunNotDiscardable, DiscardReasonNotOldest)
	check("younger unusable", youngUnusableID, DiscardRunNotDiscardable, DiscardReasonNotOldest)
	check("missing id", 987654, DiscardRunNotFound, "")

	res, err := e.pipeline.DiscardRun(e.ctx, oldestID)
	require.NoError(t, err)
	require.Equal(t, DiscardCauseCheckpointUnrecognized, res.Cause)
	require.Equal(t, state.RunStatusFailed, e.run(t, oldestID).Status)
	// Only the oldest changed; the next candidate is now the younger usable one.
	require.Equal(t, state.RunStatusInterrupted, e.run(t, youngUsable).Status)
	id, cause, ok, err := e.pipeline.DiscardableRun(e.ctx)
	require.NoError(t, err)
	require.False(t, ok, "the next candidate is usable: nothing is discardable (id=%d cause=%s)", id, cause)
}

// P2 and P3: a usable candidate is refused as resumable; a stale control is
// refused when the run was repaired or an older candidate appeared.
func TestDiscardRefusesResumableAndStale(t *testing.T) {
	e := newRunNowEnv(t)
	at := time.Now().UTC().Add(-2 * time.Hour)
	usable := e.seedRun(t, e.eligible(t, at))
	snap := e.tableDump(t, everything...)
	require.Equal(t, DiscardReasonResumable, e.discardErr(t, usable).Reason)
	require.Equal(t, snap, e.tableDump(t, everything...))
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, adm.Resumed(), "the next press resumes it")
	adm.Release()

	// P3: Run Now refused R as resume_unavailable; R is then repaired.
	e2 := newRunNowEnv(t)
	broken := e2.eligible(t, at)
	cfg := e2.newConfigVersion(t, `{"routes":[]}`)
	broken.configID = &cfg
	rID := e2.seedRun(t, broken)
	_, err = e2.db().Exec(`DELETE FROM config_version WHERE id=?`, cfg)
	require.NoError(t, err)
	_, err = e2.pipeline.AdmitRunNow(e2.ctx)
	var unavailable *ResumeUnavailableError
	require.ErrorAs(t, err, &unavailable)
	repaired := e2.newConfigVersion(t, `{"routes":[]}`)
	_, err = e2.db().Exec(`UPDATE run SET config_version_id=? WHERE id=?`, repaired, rID)
	require.NoError(t, err)
	require.Equal(t, DiscardReasonResumable, e2.discardErr(t, rID).Reason)
	// Separately: an older candidate appears.
	_, err = e2.db().Exec(`UPDATE run SET config_version_id=NULL WHERE id=?`, rID)
	require.NoError(t, err)
	older := e2.eligible(t, at.Add(-time.Hour))
	e2.seedRun(t, older)
	require.Equal(t, DiscardReasonNotOldest, e2.discardErr(t, rID).Reason)
}

// P4: one case per validator reason maps to its closed cause and the stored
// diagnostic is what Run Detail shows.
func TestDiscardCauses(t *testing.T) {
	cases := []struct {
		name, cause string
		prepare     func(t *testing.T, e *runNowEnv, s *seedSpec)
	}{
		{"snapshot_unavailable", DiscardCauseSnapshotUnavailable, func(t *testing.T, e *runNowEnv, s *seedSpec) { s.configID = nil }},
		{"snapshot_unreadable", DiscardCauseSnapshotUnreadable, func(t *testing.T, e *runNowEnv, s *seedSpec) {
			id := e.newConfigVersion(t, `not json`)
			s.configID = &id
		}},
		{"snapshot_inadmissible", DiscardCauseSnapshotUnreadable, func(t *testing.T, e *runNowEnv, s *seedSpec) {
			id := e.newConfigVersion(t, `{"routes":[{"id":0}]}`)
			s.configID = &id
		}},
		{"checkpoint_unrecognized", DiscardCauseCheckpointUnrecognized, func(t *testing.T, e *runNowEnv, s *seedSpec) { s.phase = sp("bogus") }},
		{"terminal_timestamp_contradiction", DiscardCauseCheckpointUnrecognized, func(t *testing.T, e *runNowEnv, s *seedSpec) {
			s.completedAt = sp(time.Now().UTC().Format(state.RunTimestampLayout))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRunNowEnv(t)
			spec := e.eligible(t, time.Now().UTC().Add(-time.Hour))
			tc.prepare(t, e, &spec)
			id := e.seedRun(t, spec)
			res, err := e.pipeline.DiscardRun(e.ctx, id)
			require.NoError(t, err)
			require.Equal(t, tc.cause, res.Cause)
			r := e.run(t, id)
			require.Equal(t, "diag:v1 phase=unknown operation=discard_run code="+tc.cause, str(r.InterruptReason))
			require.NotNil(t, r.CompletedAt, "completed_at is set, or kept when it already existed")
			if spec.completedAt != nil {
				require.Equal(t, *spec.completedAt, r.CompletedAt.UTC().Format(state.RunTimestampLayout), "COALESCE keeps an existing completed_at")
			}
		})
	}
}

// P6: a failure at any step changes nothing and leaves the run discardable.
func TestDiscardIsAtomic(t *testing.T) {
	steps := map[string]string{
		"handoff deferred insert": `CREATE TRIGGER inject BEFORE INSERT ON deferred_item BEGIN SELECT RAISE(ABORT,'injected'); END`,
		"claim release":           `CREATE TRIGGER inject BEFORE UPDATE ON deferred_item BEGIN SELECT RAISE(ABORT,'injected'); END`,
		"discard write":           `CREATE TRIGGER inject BEFORE UPDATE OF interrupt_reason ON run BEGIN SELECT RAISE(ABORT,'injected'); END`,
		"counter recompute":       `CREATE TRIGGER inject BEFORE UPDATE OF videos_accepted ON run BEGIN SELECT RAISE(ABORT,'injected'); END`,
	}
	for name, trigger := range steps {
		t.Run(name, func(t *testing.T) {
			e := newRunNowEnv(t)
			e.stallOn("V2")
			runID := e.interruptedAtLimit(t, testLimit).RunID()
			e.stallOn()
			// Give the claim-release step something to release (the claim needs
			// the snapshot, so it is made before the snapshot is broken).
			v3, err := e.stateStore.GetVideoByExternalID(e.ctx, "V3")
			require.NoError(t, err)
			qs, err := queue.NewStore(e.db())
			require.NoError(t, err)
			item, err := qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: v3.ID, RunID: runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			require.NoError(t, qs.WithRetryTx(e.ctx, func(tx *sql.Tx) error {
				_, _, err := qs.ClaimDeferredGroupTx(e.ctx, tx, item, runID, time.Now())
				return err
			}))
			e.breakSnapshot(t, runID)
			_, err = e.db().Exec(trigger)
			require.NoError(t, err)
			snap := e.tableDump(t, everything...)
			_, err = e.pipeline.DiscardRun(e.ctx, runID)
			require.ErrorIs(t, err, ErrDiscardNotApplied)
			require.Equal(t, snap, e.tableDump(t, everything...))
			_, err = e.db().Exec(`DROP TRIGGER inject`)
			require.NoError(t, err)
			id, _, ok, err := e.pipeline.DiscardableRun(e.ctx)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, runID, id, "the run is still the Run Now candidate")
		})
	}
}

// P7: a failing Commit is resolved by reading the row back.
func TestDiscardCommitErrors(t *testing.T) {
	commitFailed := fmtCommitErr()
	t.Run("commit failed after it took effect", func(t *testing.T) {
		e := newRunNowEnv(t)
		runID := e.unusableRun(t)
		qs, err := queue.NewStore(e.db())
		require.NoError(t, err)
		e.pipeline.discardTx = func(ctx context.Context, fn func(*sql.Tx) error) error {
			if err := qs.WithRetryTx(ctx, fn); err != nil {
				return err
			}
			return commitFailed
		}
		res, err := e.pipeline.DiscardRun(e.ctx, runID)
		require.NoError(t, err)
		require.Equal(t, DiscardCauseSnapshotUnavailable, res.Cause)
	})
	t.Run("commit failed without taking effect", func(t *testing.T) {
		e := newRunNowEnv(t)
		runID := e.unusableRun(t)
		e.pipeline.discardTx = func(ctx context.Context, fn func(*sql.Tx) error) error { return commitFailed }
		_, err := e.pipeline.DiscardRun(e.ctx, runID)
		require.ErrorIs(t, err, ErrDiscardNotApplied)
		require.Equal(t, state.RunStatusInterrupted, e.run(t, runID).Status)
	})
	t.Run("follow-up read also fails", func(t *testing.T) {
		e := newRunNowEnv(t)
		runID := e.unusableRun(t)
		_, err := e.db().Exec(`ALTER TABLE run RENAME COLUMN interrupt_reason TO interrupt_reason_x`)
		require.NoError(t, err)
		e.pipeline.discardTx = func(ctx context.Context, fn func(*sql.Tx) error) error { return commitFailed }
		_, err = e.pipeline.DiscardRun(e.ctx, runID)
		require.ErrorIs(t, err, ErrDiscardOutcomeUnknown)
	})
}

func fmtCommitErr() error { return errors.Join(queue.ErrCommit, errors.New("injected commit failure")) }

// P11: while the startup gate is closed, discard is refused before any lock.
func TestDiscardRefusedWhileRecovering(t *testing.T) {
	e := newRunNowEnv(t)
	runID := e.unusableRun(t)
	e.pipeline.producerGate.Store(producersGated)
	e.pipeline.productionMu.Lock() // a lock attempt would report ErrProductionRunActive instead
	snap := e.tableDump(t, everything...)
	_, err := e.pipeline.DiscardRun(e.ctx, runID)
	e.pipeline.productionMu.Unlock()
	require.ErrorIs(t, err, ErrStartupRecoveryActive)
	require.Equal(t, snap, e.tableDump(t, everything...))
}

// P10 (c) and (a): a busy production lock refuses without waiting; two
// concurrent discards give one success and one run_ended.
func TestDiscardBusyAndConcurrent(t *testing.T) {
	e := newRunNowEnv(t)
	runID := e.unusableRun(t)
	e.pipeline.productionMu.Lock()
	snap := e.tableDump(t, everything...)
	start := time.Now()
	_, err := e.pipeline.DiscardRun(e.ctx, runID)
	require.ErrorIs(t, err, ErrProductionRunActive)
	require.Less(t, time.Since(start), 2*time.Second)
	require.Equal(t, snap, e.tableDump(t, everything...))
	e.pipeline.productionMu.Unlock()

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_, err := e.pipeline.DiscardRun(e.ctx, runID)
				if errors.Is(err, ErrProductionRunActive) {
					continue
				}
				results <- err
				return
			}
		}()
	}
	wg.Wait()
	close(results)
	var ok, ended int
	for err := range results {
		var refused *DiscardRefusedError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &refused) && refused.Code == DiscardRunEnded:
			ended++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	require.Equal(t, 1, ok)
	require.Equal(t, 1, ended)
}

// P10 (b): discard R and Run Now at once with a younger usable candidate Y.
// Either {press refused resume_unavailable R, discard succeeds} or {discard
// succeeds, press resumes Y}. R is never resumed.
func TestDiscardVersusRunNowPress(t *testing.T) {
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		at := time.Now().UTC().Add(-2 * time.Hour)
		broken := e.eligible(t, at)
		broken.phase = sp("bogus")
		rID := e.seedRun(t, broken)
		yID := e.seedRun(t, e.eligible(t, at.Add(time.Hour)))

		var discardErr, pressErr error
		var adm *RunNowAdmission
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for {
				_, discardErr = e.pipeline.DiscardRun(e.ctx, rID)
				if !errors.Is(discardErr, ErrProductionRunActive) {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for {
				adm, pressErr = e.pipeline.AdmitRunNow(e.ctx)
				if !errors.Is(pressErr, ErrProductionRunActive) {
					return
				}
			}
		}()
		wg.Wait()
		require.NoError(t, discardErr, "iteration %d", i)
		require.Equal(t, state.RunStatusFailed, e.run(t, rID).Status)
		if pressErr != nil {
			var unavailable *ResumeUnavailableError
			require.ErrorAs(t, pressErr, &unavailable, "iteration %d", i)
			require.Equal(t, rID, unavailable.RunID)
			require.Equal(t, state.RunStatusInterrupted, e.run(t, yID).Status)
			continue
		}
		require.True(t, adm.Resumed())
		require.Equal(t, yID, adm.RunID(), "iteration %d: R is never resumed", i)
		adm.Release()
	}
}

// P10 (d): a discard holding the locks delays a scheduler start, which then
// runs and leaves R failed.
func TestDiscardHoldingLocksDelaysStart(t *testing.T) {
	e := newRunNowEnv(t)
	runID := e.unusableRun(t)
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	inside, proceed := make(chan struct{}), make(chan struct{})
	e.pipeline.discardTx = func(ctx context.Context, fn func(*sql.Tx) error) error {
		return qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
			close(inside)
			<-proceed
			return fn(tx)
		})
	}
	discardDone := make(chan error, 1)
	go func() { _, err := e.pipeline.DiscardRun(e.ctx, runID); discardDone <- err }()
	<-inside
	started := make(chan *state.Run, 1)
	go func() {
		run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
		if err != nil {
			t.Errorf("start: %v", err)
		}
		started <- run
	}()
	select {
	case <-started:
		t.Fatal("a start proceeded while the discard held the locks")
	case <-time.After(200 * time.Millisecond):
	}
	close(proceed)
	require.NoError(t, <-discardDone)
	run := <-started
	require.NotNil(t, run)
	e.pipeline.Execute(e.ctx, run.ID)
	require.Equal(t, state.RunStatusFailed, e.run(t, runID).Status)
	require.Equal(t, state.RunStatusCompleted, e.run(t, run.ID).Status)
}

// V1 (AC22): only the oldest unusable row reads discardable, and the read
// changes when the situation does.
func TestDiscardableRunRead(t *testing.T) {
	e := newRunNowEnv(t)
	id, _, ok, err := e.pipeline.DiscardableRun(e.ctx)
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, id)

	at := time.Now().UTC().Add(-3 * time.Hour)
	oldest := e.eligible(t, at)
	oldest.phase = sp("bogus")
	oldestID := e.seedRun(t, oldest)
	younger := e.eligible(t, at.Add(time.Hour))
	younger.configID = nil
	e.seedRun(t, younger)
	id, cause, ok, err := e.pipeline.DiscardableRun(e.ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, oldestID, id)
	require.Equal(t, DiscardCauseCheckpointUnrecognized, cause)

	// An older time-limit row appears: the answer changes.
	older := e.eligible(t, at.Add(-time.Hour))
	older.configID = nil
	olderID := e.seedRun(t, older)
	id, cause, ok, err = e.pipeline.DiscardableRun(e.ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, olderID, id)
	require.Equal(t, DiscardCauseSnapshotUnavailable, cause)

	_, err = e.pipeline.DiscardRun(e.ctx, olderID)
	require.NoError(t, err)
	id, _, _, err = e.pipeline.DiscardableRun(e.ctx)
	require.NoError(t, err)
	require.Equal(t, oldestID, id, "after the discard the next candidate is read")
}
