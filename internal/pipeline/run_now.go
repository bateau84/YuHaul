package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// ErrRunTimeLimit is the context cause of a Run Now execution that hit its
// time limit. It is only ever a context cause, never stored as text: failRun
// maps it to the closed interrupt_cause value (run-initiation ADR-001).
var ErrRunTimeLimit = errors.New("run execution time limit reached")

// ErrProductionRunActive is returned by AdmitRunNow when another production run
// holds the execution lock. Run Now refuses instead of waiting.
var ErrProductionRunActive = errors.New("a production run is already active")

// Closed reasons of ResumeUnavailableError.
const (
	resumeSnapshotUnavailable    = "snapshot_unavailable"
	resumeSnapshotUnreadable     = "snapshot_unreadable"
	resumeSnapshotInadmissible   = "snapshot_inadmissible"
	resumeCheckpointUnrecognized = "checkpoint_unrecognized"
	resumeTerminalContradiction  = "terminal_timestamp_contradiction"
	resumeRecheckAtWrite         = "recheck_at_write"
)

// ResumeUnavailableError reports that the oldest time-limit-interrupted Run Now
// run cannot be resumed safely. The press is refused: nothing was written, no
// provider call was made and no new run was started. Reason is a closed
// identifier; no raw error text is carried.
type ResumeUnavailableError struct {
	RunID  int64
	Reason string
}

func (e *ResumeUnavailableError) Error() string {
	return fmt.Sprintf("run %d cannot be resumed: %s", e.RunID, e.Reason)
}

// validateResumableRow is the one per-row resumability check, shared by
// startup classification and Run Now admission so the two can never disagree
// about what is resumable. It returns "" when the row is resumable, or the
// closed reason it is not. The order is the order startup classification has
// always checked in. A NULL phase is a recognized "not yet started" checkpoint.
func validateResumableRow(completedAt, phase, snapshot sql.NullString) string {
	if completedAt.Valid {
		return resumeTerminalContradiction
	}
	if !snapshot.Valid {
		return resumeSnapshotUnavailable
	}
	if phase.Valid {
		switch Phase(phase.String) {
		case PhaseDiscovery, PhaseExclusion, PhaseDuplicateDetect, PhaseRouting, PhaseActionExecution:
		default:
			return resumeCheckpointUnrecognized
		}
	}
	if !bytes.HasPrefix(bytes.TrimSpace([]byte(snapshot.String)), []byte("{")) {
		return resumeSnapshotUnreadable
	}
	var snap config.Snapshot
	if err := json.Unmarshal([]byte(snapshot.String), &snap); err != nil {
		return resumeSnapshotUnreadable
	}
	if err := validateSnapshotRoutes(snap); err != nil {
		return resumeSnapshotInadmissible
	}
	return ""
}

// startupCheckFor maps a validateResumableRow reason to the check name startup
// classification has always reported, so its observable behavior is unchanged.
func startupCheckFor(reason string) string {
	switch reason {
	case resumeSnapshotUnavailable:
		return "authority"
	case resumeCheckpointUnrecognized:
		return "checkpoint"
	case resumeSnapshotUnreadable:
		return "snapshot_readable"
	case resumeSnapshotInadmissible:
		return "snapshot_admissible"
	default:
		return reason
	}
}

// interruptCauseFor classifies why an execution context ended. Only a proven
// cause is returned: the Run Now time limit, or cancellation (shutdown).
func interruptCauseFor(ctx context.Context) string {
	if stopOwnsEnding(ctx) {
		return ""
	}
	if errors.Is(context.Cause(ctx), ErrRunTimeLimit) {
		return state.InterruptCauseTimeLimit
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return state.InterruptCauseShutdown
	}
	return ""
}

// RunNowAdmission is an accepted Run Now press that still holds the production
// execution lock. Exactly one of Execute or Release must be called.
type RunNowAdmission struct {
	p       *Pipeline
	runID   int64
	resumed bool
	used    atomic.Bool
}

// RunID is the run this press owns: the resumed run, or the newly created one.
func (a *RunNowAdmission) RunID() int64 { return a.runID }

// Resumed reports whether the press resumed an interrupted run rather than
// starting a new one.
func (a *RunNowAdmission) Resumed() bool { return a.resumed }

// Execute runs the admitted run under the lock the admission already holds and
// releases the lock on return. It is a no-op after Execute or Release.
func (a *RunNowAdmission) Execute(ctx context.Context) {
	if !a.used.CompareAndSwap(false, true) {
		return
	}
	defer a.p.productionMu.Unlock()
	a.p.executeHeld(ctx, a.runID, a.resumed)
}

// Release gives the lock back without executing. It is a no-op after Execute
// or Release.
func (a *RunNowAdmission) Release() {
	if a.used.CompareAndSwap(false, true) {
		a.p.unregisterRun(a.runID)
		a.p.productionMu.Unlock()
	}
}

// AdmitRunNow handles one Run Now press atomically under the production
// execution lock. It either resumes the oldest Run Now run that stopped at its
// time limit, or starts a new run; it never does both. It refuses instead of
// waiting when another production run is active (ErrProductionRunActive), and
// with a *ResumeUnavailableError, changing nothing, when the oldest candidate
// cannot be resumed safely. On success the caller owns the held lock and must
// call Execute or Release on the result (run-initiation ADR-001, ARS-001 §2.2).
func (p *Pipeline) AdmitRunNow(ctx context.Context) (*RunNowAdmission, error) {
	if err := p.refuseWhileRecovering(); err != nil {
		return nil, err
	}
	p.initiationMu.Lock()
	defer p.initiationMu.Unlock()
	if !p.productionMu.TryLock() {
		return nil, ErrProductionRunActive
	}
	admitted := false
	defer func() {
		if !admitted {
			p.productionMu.Unlock()
		}
	}()
	adm, err := p.admitLocked(ctx)
	if err != nil {
		return nil, err
	}
	admitted = true
	return adm, nil
}

// admitLocked runs with productionMu held.
func (p *Pipeline) admitLocked(ctx context.Context) (*RunNowAdmission, error) {
	qs, err := p.retryStore()
	if err != nil {
		return nil, err
	}
	var resumedID int64
	if err := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		resumedID, err = resumeOldestRunNowTx(ctx, tx)
		return err
	}); err != nil {
		var unavailable *ResumeUnavailableError
		if errors.As(err, &unavailable) {
			return nil, err
		}
		return nil, fmt.Errorf("pipeline: admit run now: %w", err)
	}
	if resumedID != 0 {
		p.registerRun(resumedID)
		return &RunNowAdmission{p: p, runID: resumedID, resumed: true}, nil
	}
	run, err := p.prepareProductionRun(ctx, state.RunTypeManual)
	if err != nil {
		return nil, err
	}
	initiator := state.InitiatedByRunNow
	run.InitiatedBy = &initiator
	if err := p.createClaimedRunLocked(ctx, run); err != nil {
		return nil, err
	}
	return &RunNowAdmission{p: p, runID: run.ID}, nil
}

// runNowCandidate is the row Run Now would consider for resume.
type runNowCandidate struct {
	ID                           int64
	CompletedAt, Phase, Snapshot sql.NullString
}

// selectRunNowCandidateTx returns the oldest eligible time-limit Run Now row
// (ARS-001 §2.2 step 3). found is false when there is none. Resume and Discard
// both decide through it, so they can never disagree about the candidate.
func selectRunNowCandidateTx(ctx context.Context, tx *sql.Tx) (c runNowCandidate, found bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT r.id, r.completed_at, r.current_phase, c.snapshot
 FROM run r LEFT JOIN config_version c ON c.id = r.config_version_id
 WHERE r.run_type = 'manual' AND r.initiated_by = 'run_now'
   AND r.status = 'interrupted' AND r.interrupt_cause = 'time_limit'
   AND r.mutation_safety_version = 1 AND r.action_authority_version = 1
 ORDER BY r.started_at ASC, r.id ASC
 LIMIT 1`).Scan(&c.ID, &c.CompletedAt, &c.Phase, &c.Snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, fmt.Errorf("select run now candidate: %w", err)
	}
	return c, true, nil
}

// resumeOldestRunNowTx selects the oldest eligible run (only the oldest is
// ever considered), validates it and moves it from interrupted to running, all
// inside the caller's transaction. It returns 0 when there is no candidate.
func resumeOldestRunNowTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	c, found, err := selectRunNowCandidateTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	id := c.ID
	if reason := validateResumableRow(c.CompletedAt, c.Phase, c.Snapshot); reason != "" {
		return 0, &ResumeUnavailableError{RunID: id, Reason: reason}
	}
	res, err := tx.ExecContext(ctx, `UPDATE run SET status='running', interrupt_reason=NULL, interrupt_cause=NULL
 WHERE id=? AND status='interrupted' AND interrupt_cause='time_limit' AND initiated_by='run_now' AND completed_at IS NULL`, id)
	if err != nil {
		return 0, fmt.Errorf("resume run now candidate: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return 0, &ResumeUnavailableError{RunID: id, Reason: resumeRecheckAtWrite}
	}
	return id, nil
}

// retryStore returns the pipeline's queue store, opening one on demand.
func (p *Pipeline) retryStore() (*queue.Store, error) {
	if p.queueStore != nil {
		return p.queueStore, nil
	}
	return queue.NewStore(p.db)
}
