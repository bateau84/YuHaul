package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// ErrRunStoppedByUser is the context cause of an execution the user stopped
// (run-initiation ADR-004). It is only ever a context cause, never stored as
// text: the stored stop reason is the closed stop_run diagnostic.
var ErrRunStoppedByUser = errors.New("run stopped by user")

// ErrStopNotRecorded is returned when the stop marker could not be written. The
// run continues unchanged.
var ErrStopNotRecorded = errors.New("stop could not be recorded")

// Closed codes and reasons of StopRefusedError.
const (
	StopRunNotFound  = "run_not_found"
	StopRunEnded     = "run_ended"
	StopRunNotActive = "run_not_active"

	StopReasonNotExecuting = "not_executing"
	StopReasonEnding       = "ending"

	StopAccepted        = "accepted"
	StopAlreadyStopping = "already_stopping"
)

// StopResult is the outcome of an accepted stop request.
type StopResult struct {
	RunID   int64
	Outcome string // StopAccepted | StopAlreadyStopping
}

// StopRefusedError reports a stop request that changed nothing. Code, Status
// and Reason are closed identifiers; no raw text is carried.
type StopRefusedError struct {
	RunID  int64
	Code   string // StopRunNotFound | StopRunEnded | StopRunNotActive
	Status string // StopRunEnded only: completed | failed | stopped
	Reason string // StopRunNotActive only: not_executing | ending
}

func (e *StopRefusedError) Error() string {
	return fmt.Sprintf("run %d cannot be stopped: %s", e.RunID, e.Code)
}

// runControl arbitrates between a run's own ending and a user stop: exactly one
// of them writes the terminal state (ARS-003 §3.1–3.2).
type runControl struct {
	mu            sync.Mutex
	cancel        context.CancelCauseFunc // nil until the executor attaches
	stopRequested bool
	finalizing    bool
}

// registerRun returns the run's control, creating it on first use.
func (p *Pipeline) registerRun(id int64) *runControl {
	p.runsMu.Lock()
	defer p.runsMu.Unlock()
	if p.runs == nil {
		p.runs = map[int64]*runControl{}
	}
	ctl, ok := p.runs[id]
	if !ok {
		ctl = &runControl{}
		p.runs[id] = ctl
	}
	return ctl
}

func (p *Pipeline) unregisterRun(id int64) {
	p.runsMu.Lock()
	defer p.runsMu.Unlock()
	delete(p.runs, id)
}

func (p *Pipeline) control(id int64) *runControl {
	p.runsMu.Lock()
	defer p.runsMu.Unlock()
	return p.runs[id]
}

// attach derives the cancellable execution context. If a stop was already
// requested the returned context is already cancelled and stopped is true.
func (c *runControl) attach(ctx context.Context) (_ context.Context, stopped bool) {
	ctx, cancel := context.WithCancelCause(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = cancel
	if c.stopRequested {
		cancel(ErrRunStoppedByUser)
		return ctx, true
	}
	return ctx, false
}

// beginFinalize marks the run as ending by itself. It returns false when a stop
// owns the ending, in which case the caller must not write a terminal state.
func (c *runControl) beginFinalize() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopRequested {
		return false
	}
	c.finalizing = true
	return true
}

// mayFinalize is the terminal gate every terminal writer starts with. A run
// without a control (not executing in this process) is unrestricted.
func (p *Pipeline) mayFinalize(runID int64) bool {
	ctl := p.control(runID)
	return ctl == nil || ctl.beginFinalize()
}

// stopOwnsEnding reports whether ctx was cancelled by a user stop.
func stopOwnsEnding(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrRunStoppedByUser)
}

// StopRun records the user's stop of one executing run and cancels it. The
// marker commits before the cancel, so a crash after the acknowledgement is
// settled at startup. It never takes initiationMu or productionMu and is not
// gated by startup recovery (ARS-003 §3.4).
func (p *Pipeline) StopRun(ctx context.Context, runID int64) (*StopResult, error) {
	status, marker, err := p.readStopState(ctx, runID)
	if err != nil {
		return nil, err
	}
	if refused := stopRefusalFor(runID, status); refused != nil {
		return nil, refused
	}
	ctl := p.control(runID)
	if ctl == nil {
		if marker.Valid {
			return &StopResult{RunID: runID, Outcome: StopAlreadyStopping}, nil
		}
		return nil, &StopRefusedError{RunID: runID, Code: StopRunNotActive, Reason: StopReasonNotExecuting}
	}
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if ctl.finalizing {
		return nil, &StopRefusedError{RunID: runID, Code: StopRunNotActive, Reason: StopReasonEnding}
	}
	if ctl.stopRequested {
		return &StopResult{RunID: runID, Outcome: StopAlreadyStopping}, nil
	}
	res, err := p.db.ExecContext(ctx,
		`UPDATE run SET stop_requested_at = ? WHERE id = ? AND status = 'running' AND stop_requested_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339), runID)
	if err != nil {
		p.logger.ErrorContext(ctx, "run stop not recorded", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
		return nil, ErrStopNotRecorded
	}
	if n, rerr := res.RowsAffected(); rerr != nil || n == 0 {
		status, marker, err := p.readStopState(ctx, runID)
		if err != nil {
			return nil, err
		}
		if refused := stopRefusalFor(runID, status); refused != nil {
			return nil, refused
		}
		if marker.Valid {
			return &StopResult{RunID: runID, Outcome: StopAlreadyStopping}, nil
		}
		return nil, &StopRefusedError{RunID: runID, Code: StopRunNotActive, Reason: StopReasonNotExecuting}
	}
	ctl.stopRequested = true
	if ctl.cancel != nil {
		ctl.cancel(ErrRunStoppedByUser)
	}
	p.logger.InfoContext(ctx, "run stop accepted", "run_id", runID)
	return &StopResult{RunID: runID, Outcome: StopAccepted}, nil
}

func (p *Pipeline) readStopState(ctx context.Context, runID int64) (string, sql.NullString, error) {
	var status string
	var marker sql.NullString
	err := p.db.QueryRowContext(ctx, `SELECT status, stop_requested_at FROM run WHERE id = ?`, runID).Scan(&status, &marker)
	if errors.Is(err, sql.ErrNoRows) {
		return "", marker, &StopRefusedError{RunID: runID, Code: StopRunNotFound}
	}
	if err != nil {
		return "", marker, fmt.Errorf("read run %d for stop: %w", runID, err)
	}
	return status, marker, nil
}

func stopRefusalFor(runID int64, status string) *StopRefusedError {
	switch status {
	case string(state.RunStatusCompleted), string(state.RunStatusFailed), string(state.RunStatusStopped):
		return &StopRefusedError{RunID: runID, Code: StopRunEnded, Status: status}
	}
	return nil
}

// finishRun is deferred by every execution entry point. When a stop was
// accepted it settles the run (before the entry point returns, so production
// runs still hold productionMu), then it releases the run's control.
func (p *Pipeline) finishRun(ctx context.Context, runID int64) {
	ctl := p.control(runID)
	if ctl == nil {
		return
	}
	defer p.unregisterRun(runID)
	ctl.mu.Lock()
	stopped := ctl.stopRequested
	cancel := ctl.cancel
	if !stopped {
		// The entry point is returning: a stop can no longer take effect.
		ctl.finalizing = true
	}
	ctl.mu.Unlock()
	if stopped {
		if err := p.settleStop(ctx, runID); err != nil {
			// The run stays running with its marker; a restart settles it.
			p.logger.ErrorContext(ctx, "run stop settle failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
		}
	}
	if cancel != nil {
		cancel(nil)
	}
}

var stopReason = diag.Facts{Phase: "unknown", Operation: "stop_run", Code: "stopped_by_user"}.String()

// settleStop commits the stop: the deferred-queue handoff (production runs),
// the terminal stopped state and the counters, in one transaction. One retry.
func (p *Pipeline) settleStop(ctx context.Context, runID int64) error {
	qs, err := p.retryStore()
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		finalizeCtx, cancel := runFinalizationContext(ctx)
		var result queue.HandoffResult
		quotaCost, qerr := p.machine.RunQuotaCost(finalizeCtx, runID)
		if qerr == nil {
			qerr = qs.WithRetryTx(finalizeCtx, func(tx *sql.Tx) error {
				var err error
				result, err = settleStopTx(finalizeCtx, qs, p.stateStore, tx, runID, quotaCost, time.Now().UTC())
				return err
			})
		}
		cancel()
		if qerr == nil {
			p.logger.InfoContext(ctx, "run stop settled", "run_id", runID,
				"requeued", result.Requeued, "created", result.Created, "resolved", result.Resolved, "removed", result.Removed)
			p.broadcastEvent(runID, EventRunStopped, payload(completedPayload{RunID: runID, Status: string(state.RunStatusStopped), QuotaCost: quotaCost}))
			return nil
		}
		lastErr = qerr
		if attempt == 0 {
			p.logger.WarnContext(ctx, "run stop settle failed, retrying", "run_id", runID, "diagnostic", diag.Encode(qerr, "unknown", "finalize_run"))
		}
	}
	return lastErr
}

// settleStopTx performs the settle inside tx (ARS-003 §3.3). It is idempotent
// for a run that is already stopped. It returns the handoff counts for logs.
func settleStopTx(ctx context.Context, qs *queue.Store, ss *state.Store, tx *sql.Tx, runID int64, quotaCost int, now time.Time) (queue.HandoffResult, error) {
	var result queue.HandoffResult
	var runType, status string
	var marker sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT run_type, status, stop_requested_at FROM run WHERE id = ?`, runID).Scan(&runType, &status, &marker); err != nil {
		return result, fmt.Errorf("settle stop of run %d: read run: %w", runID, err)
	}
	if status == string(state.RunStatusStopped) {
		return result, nil
	}
	if !marker.Valid || (status != string(state.RunStatusRunning) && status != string(state.RunStatusInterrupted)) {
		return result, fmt.Errorf("settle stop of run %d: status %s without a valid marker: %w", runID, status, state.ErrRunStopNotApplied)
	}
	production := runType == string(state.RunTypeManual) || runType == string(state.RunTypeScheduled)
	if production {
		ids, err := listUnfinishedRunVideosTx(ctx, tx, runID)
		if err != nil {
			return result, err
		}
		if result, err = qs.HandOffUnfinishedTx(ctx, tx, runID, queue.DeferredReasonStoppedByUser, ids); err != nil {
			return result, err
		}
	}
	if err := ss.StopRunTx(ctx, tx, runID, stopReason, quotaCost, now); err != nil {
		return result, err
	}
	if production {
		return result, ss.RecomputeRunCountersTx(ctx, tx, runID)
	}
	return result, ss.RecomputeRunCountersFromDecisionsTx(ctx, tx, runID)
}
