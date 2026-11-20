package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Closed causes of a discard (ARS-002 §2.1).
const (
	DiscardCauseSnapshotUnavailable    = "snapshot_unavailable"
	DiscardCauseSnapshotUnreadable     = "snapshot_unreadable"
	DiscardCauseCheckpointUnrecognized = "checkpoint_unrecognized"
)

// Closed codes and reasons of DiscardRefusedError.
const (
	DiscardRunNotFound       = "run_not_found"
	DiscardRunEnded          = "run_ended"
	DiscardRunNotDiscardable = "run_not_discardable"

	DiscardReasonResumable   = "resumable"
	DiscardReasonNotOldest   = "not_oldest"
	DiscardReasonNotEligible = "not_eligible"
)

// DiscardResult is a committed discard.
type DiscardResult struct {
	RunID int64
	Cause string
}

// DiscardRefusedError reports a discard that changed nothing. Code, Status and
// Reason are closed identifiers.
type DiscardRefusedError struct {
	RunID  int64
	Code   string // run_not_found | run_ended | run_not_discardable
	Status string // run_ended only: failed | completed | stopped
	Reason string // run_not_discardable only: resumable | not_oldest | not_eligible
}

func (e *DiscardRefusedError) Error() string {
	return fmt.Sprintf("run %d cannot be discarded: %s", e.RunID, e.Code)
}

// ErrDiscardNotApplied is returned when the discard transaction did not commit;
// the run is unchanged.
var ErrDiscardNotApplied = errors.New("discard did not take effect")

// ErrDiscardOutcomeUnknown is returned when Commit failed and the follow-up
// read could not confirm the result.
var ErrDiscardOutcomeUnknown = errors.New("discard outcome could not be confirmed")

// discardCauseFor maps a non-empty validateResumableRow reason to the cause the
// user sees. Any other value yields "" (a programming error for the caller).
func discardCauseFor(reason string) string {
	switch reason {
	case resumeSnapshotUnavailable:
		return DiscardCauseSnapshotUnavailable
	case resumeSnapshotUnreadable, resumeSnapshotInadmissible:
		return DiscardCauseSnapshotUnreadable
	case resumeCheckpointUnrecognized, resumeTerminalContradiction:
		return DiscardCauseCheckpointUnrecognized
	}
	return ""
}

// DiscardRun ends the Run Now candidate that Run Now cannot resume, handing its
// unfinished work to the deferred queue, in one transaction (ARS-002 §2.4).
// It makes no provider call and writes no guard or action row.
func (p *Pipeline) DiscardRun(ctx context.Context, runID int64) (*DiscardResult, error) {
	if err := p.refuseWhileRecovering(); err != nil {
		return nil, err
	}
	p.initiationMu.Lock()
	defer p.initiationMu.Unlock()
	if !p.productionMu.TryLock() {
		return nil, ErrProductionRunActive
	}
	defer p.productionMu.Unlock()

	qs, err := p.retryStore()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDiscardNotApplied, err)
	}
	// The ledger read stays outside the transaction: WithRetryTx forbids a
	// second connection, and nothing can add ledger rows while the lock is held.
	quotaCost, err := p.machine.RunQuotaCost(ctx, runID)
	if err != nil {
		p.logger.ErrorContext(ctx, "discard quota read failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
		return nil, ErrDiscardNotApplied
	}

	var cause, stored, reason string
	runTx := p.discardTx
	if runTx == nil {
		runTx = qs.WithRetryTx
	}
	err = runTx(ctx, func(tx *sql.Tx) error {
		var status string
		switch err := tx.QueryRowContext(ctx, `SELECT status FROM run WHERE id = ?`, runID).Scan(&status); {
		case errors.Is(err, sql.ErrNoRows):
			return &DiscardRefusedError{RunID: runID, Code: DiscardRunNotFound}
		case err != nil:
			return fmt.Errorf("read run %d: %w", runID, err)
		}
		switch status {
		case string(state.RunStatusFailed), string(state.RunStatusCompleted), string(state.RunStatusStopped):
			return &DiscardRefusedError{RunID: runID, Code: DiscardRunEnded, Status: status}
		}
		c, found, err := selectRunNowCandidateTx(ctx, tx)
		if err != nil {
			return err
		}
		if !found || c.ID != runID {
			var eligible bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run WHERE id = ?
 AND run_type = 'manual' AND initiated_by = 'run_now' AND status = 'interrupted'
 AND interrupt_cause = 'time_limit' AND mutation_safety_version = 1 AND action_authority_version = 1)`, runID).Scan(&eligible); err != nil {
				return fmt.Errorf("check discard eligibility of run %d: %w", runID, err)
			}
			if eligible {
				return &DiscardRefusedError{RunID: runID, Code: DiscardRunNotDiscardable, Reason: DiscardReasonNotOldest}
			}
			return &DiscardRefusedError{RunID: runID, Code: DiscardRunNotDiscardable, Reason: DiscardReasonNotEligible}
		}
		reason = validateResumableRow(c.CompletedAt, c.Phase, c.Snapshot)
		if reason == "" {
			return &DiscardRefusedError{RunID: runID, Code: DiscardRunNotDiscardable, Reason: DiscardReasonResumable}
		}
		cause = discardCauseFor(reason)
		if cause == "" {
			return fmt.Errorf("unmapped resume reason %q", reason)
		}
		stored = diag.Facts{Phase: "unknown", Operation: "discard_run", Code: cause}.String()
		ids, err := listUnfinishedRunVideosTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		if _, err := qs.HandOffUnfinishedTx(ctx, tx, runID, queue.DeferredReasonDiscardedByUser, ids); err != nil {
			return err
		}
		if err := p.stateStore.DiscardRunTx(ctx, tx, runID, stored, quotaCost); err != nil {
			return err
		}
		return p.stateStore.RecomputeRunCountersTx(ctx, tx, runID)
	})
	if err != nil {
		var refused *DiscardRefusedError
		if errors.As(err, &refused) {
			return nil, refused
		}
		if errors.Is(err, queue.ErrCommit) {
			return p.confirmDiscard(ctx, runID, cause, stored, reason, err)
		}
		p.logger.ErrorContext(ctx, "run discard not applied", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
		return nil, fmt.Errorf("%w", ErrDiscardNotApplied)
	}
	p.logger.InfoContext(ctx, "run discarded", "run_id", runID, "cause", cause, "reason", reason)
	return &DiscardResult{RunID: runID, Cause: cause}, nil
}

// confirmDiscard resolves a failed Commit by reading the row back while the
// locks are still held.
func (p *Pipeline) confirmDiscard(ctx context.Context, runID int64, cause, stored, reason string, commitErr error) (*DiscardResult, error) {
	p.logger.ErrorContext(ctx, "run discard commit failed", "run_id", runID, "diagnostic", diag.Encode(commitErr, "unknown", "finalize_run"))
	var status string
	var storedReason sql.NullString
	if err := p.db.QueryRowContext(ctx, `SELECT status, interrupt_reason FROM run WHERE id = ?`, runID).Scan(&status, &storedReason); err != nil {
		return nil, ErrDiscardOutcomeUnknown
	}
	switch {
	case status == string(state.RunStatusFailed) && storedReason.Valid && storedReason.String == stored && stored != "":
		p.logger.InfoContext(ctx, "run discarded", "run_id", runID, "cause", cause, "reason", reason)
		return &DiscardResult{RunID: runID, Cause: cause}, nil
	case status == string(state.RunStatusInterrupted):
		return nil, ErrDiscardNotApplied
	}
	return nil, ErrDiscardOutcomeUnknown
}

// DiscardableRun reports the one run Discard would accept right now, with its
// cause, without taking any lock (ARS-003 §3.6). It uses the same selector and
// validator as DiscardRun.
func (p *Pipeline) DiscardableRun(ctx context.Context) (runID int64, cause string, ok bool, err error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, "", false, fmt.Errorf("discardable read: %w", err)
	}
	defer tx.Rollback()
	c, found, err := selectRunNowCandidateTx(ctx, tx)
	if err != nil || !found {
		return 0, "", false, err
	}
	reason := validateResumableRow(c.CompletedAt, c.Phase, c.Snapshot)
	if reason == "" {
		return 0, "", false, nil
	}
	cause = discardCauseFor(reason)
	if cause == "" {
		return 0, "", false, fmt.Errorf("discardable read: unmapped resume reason")
	}
	return c.ID, cause, true, nil
}
