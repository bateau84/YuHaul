package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Closed codes and reasons of EndRefusedError (run-initiation ARS-004 §2.2).
const (
	EndRunNotFound         = "run_not_found"
	EndRunEnded            = "run_ended"
	EndRunNotEndable       = "run_not_endable"
	EndReasonNotProduction = "not_production"
	EndReasonNotLegacy     = "not_legacy"
	EndReasonExecuting     = "executing"
)

// EndResult is a committed end of a legacy run.
type EndResult struct{ RunID int64 }

// EndRefusedError reports an end that changed nothing. Every field is a closed
// identifier.
type EndRefusedError struct {
	RunID   int64
	Code    string // run_not_found | run_ended | run_not_endable
	Status  string // run_ended only: completed | failed | stopped
	Reason  string // run_not_endable only: not_production | not_legacy | executing
	RunType string // run_not_endable/not_production only: dry_run | replay | backtest
}

func (e *EndRefusedError) Error() string {
	return fmt.Sprintf("run %d cannot be marked as ended: %s", e.RunID, e.Code)
}

// EndNotAppliedError: the transaction did not commit; the run is unchanged.
// Status is the unchanged saved status when known: running | interrupted | "".
type EndNotAppliedError struct {
	RunID  int64
	Status string
}

func (e *EndNotAppliedError) Error() string {
	return fmt.Sprintf("end of run %d did not take effect", e.RunID)
}

func (e *EndNotAppliedError) Is(target error) bool { return target == ErrEndNotApplied }

// ErrEndNotApplied matches every EndNotAppliedError.
var ErrEndNotApplied = errors.New("end did not take effect")

// ErrEndOutcomeUnknown is returned when Commit failed and the follow-up read
// could not confirm the result.
var ErrEndOutcomeUnknown = errors.New("end outcome could not be confirmed")

// classifyEndRow applies the permanent row refusals in ARS-004 order. nil means
// the row is a running or interrupted legacy production run.
func classifyEndRow(runID int64, runType, status string, safety int) *EndRefusedError {
	switch status {
	case string(state.RunStatusCompleted), string(state.RunStatusFailed), string(state.RunStatusStopped):
		return &EndRefusedError{RunID: runID, Code: EndRunEnded, Status: status}
	}
	if runType != string(state.RunTypeManual) && runType != string(state.RunTypeScheduled) {
		return &EndRefusedError{RunID: runID, Code: EndRunNotEndable, Reason: EndReasonNotProduction, RunType: runType}
	}
	if safety != 0 {
		return &EndRefusedError{RunID: runID, Code: EndRunNotEndable, Reason: EndReasonNotLegacy}
	}
	return nil
}

const selectEndRow = `SELECT run_type, status, mutation_safety_version FROM run WHERE id = ?`

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func readEndRow(ctx context.Context, q queryRower, runID int64) (status string, refused *EndRefusedError, err error) {
	var runType string
	var safety int
	switch err := q.QueryRowContext(ctx, selectEndRow, runID).Scan(&runType, &status, &safety); {
	case errors.Is(err, sql.ErrNoRows):
		return "", &EndRefusedError{RunID: runID, Code: EndRunNotFound}, nil
	case err != nil:
		return "", nil, fmt.Errorf("read run %d: %w", runID, err)
	}
	return status, classifyEndRow(runID, runType, status, safety), nil
}

// PrecheckEndLegacyRun answers the permanent refusals (not found, ended, not a
// production run, not a legacy run) from one lock-free read, so a repeat always
// says "already ended" (ADR-005 Decision 3). It only refuses; it never accepts.
func (p *Pipeline) PrecheckEndLegacyRun(ctx context.Context, runID int64) error {
	_, refused, err := readEndRow(ctx, p.db, runID)
	if err != nil {
		p.logger.ErrorContext(ctx, "end precheck read failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "end_legacy_run"))
		return nil
	}
	if refused != nil {
		return refused
	}
	return nil
}

// registeredRunIDs copies the IDs that have an in-process run control.
func (p *Pipeline) registeredRunIDs() []int64 {
	p.runsMu.Lock()
	defer p.runsMu.Unlock()
	ids := make([]int64, 0, len(p.runs))
	for id := range p.runs {
		ids = append(ids, id)
	}
	return ids
}

// ExecutingRuns returns the IDs of runs that have an in-process run control,
// meaning this process is executing them. A saved status of running does not
// imply it.
func (p *Pipeline) ExecutingRuns() map[int64]bool {
	ids := p.registeredRunIDs()
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// productionRegistered reports whether any registered run control belongs to a
// manual or scheduled row.
func productionRegistered(ctx context.Context, tx *sql.Tx, ids []int64) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var found bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM run WHERE id IN (`+marks+`) AND run_type IN ('manual','scheduled'))`, args...).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check registered production runs: %w", err)
	}
	return found, nil
}

// EndLegacyRun marks a stuck legacy production run as failed with the end
// reason, in one guarded UPDATE under the Discard locks (ARS-004 §2.4). It
// makes no provider, auth, queue or execution call.
func (p *Pipeline) EndLegacyRun(ctx context.Context, runID int64) (*EndResult, error) {
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
		return nil, &EndNotAppliedError{RunID: runID}
	}
	stored := diag.Facts{Phase: "unknown", Operation: "end_legacy_run", Code: "ended_by_user"}.String()
	var prior string
	runTx := p.endTx
	if runTx == nil {
		runTx = qs.WithRetryTx
	}
	err = runTx(ctx, func(tx *sql.Tx) error {
		status, refused, err := readEndRow(ctx, tx, runID)
		if err != nil {
			return err
		}
		if refused != nil {
			return refused
		}
		prior = status
		if p.control(runID) != nil {
			return &EndRefusedError{RunID: runID, Code: EndRunNotEndable, Reason: EndReasonExecuting}
		}
		active, err := productionRegistered(ctx, tx, p.registeredRunIDs())
		if err != nil {
			return err
		}
		if active {
			return ErrProductionRunActive
		}
		return p.stateStore.EndLegacyRunTx(ctx, tx, runID, stored)
	})
	if err != nil {
		var refused *EndRefusedError
		switch {
		case errors.As(err, &refused):
			return nil, refused
		case errors.Is(err, ErrProductionRunActive):
			return nil, ErrProductionRunActive
		case errors.Is(err, queue.ErrCommit):
			return p.confirmEnd(ctx, runID, stored, err)
		}
		p.logger.ErrorContext(ctx, "legacy run end not applied", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "end_legacy_run"))
		return nil, &EndNotAppliedError{RunID: runID, Status: prior}
	}
	p.logger.InfoContext(ctx, "legacy run marked as ended", "run_id", runID, "prior_status", prior)
	return &EndResult{RunID: runID}, nil
}

// confirmEnd resolves a failed Commit by reading the row back while the locks
// are still held.
func (p *Pipeline) confirmEnd(ctx context.Context, runID int64, stored string, commitErr error) (*EndResult, error) {
	p.logger.ErrorContext(ctx, "legacy run end commit failed", "run_id", runID, "diagnostic", diag.Encode(commitErr, "unknown", "end_legacy_run"))
	var status string
	var reason sql.NullString
	if err := p.db.QueryRowContext(ctx, `SELECT status, interrupt_reason FROM run WHERE id = ?`, runID).Scan(&status, &reason); err != nil {
		return nil, ErrEndOutcomeUnknown
	}
	switch {
	case status == string(state.RunStatusFailed) && reason.Valid && reason.String == stored:
		p.logger.InfoContext(ctx, "legacy run marked as ended", "run_id", runID)
		return &EndResult{RunID: runID}, nil
	case status == string(state.RunStatusRunning) || status == string(state.RunStatusInterrupted):
		return nil, &EndNotAppliedError{RunID: runID, Status: status}
	}
	return nil, ErrEndOutcomeUnknown
}

// EndableRuns returns the IDs of runs EndLegacyRun would currently accept,
// without taking any lock but runsMu (ARS-004 §2.5). The fact is advisory.
func (p *Pipeline) EndableRuns(ctx context.Context) (map[int64]bool, error) {
	out := map[int64]bool{}
	if p.producerGate.Load() == producersGated {
		return out, nil
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("endable runs read: %w", err)
	}
	defer tx.Rollback()
	ids := p.registeredRunIDs()
	active, err := productionRegistered(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	if active {
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM run WHERE `+state.LegacyEndableRow)
	if err != nil {
		return nil, fmt.Errorf("endable runs read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("endable runs read: %w", err)
		}
		if p.control(id) == nil {
			out[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("endable runs read: %w", err)
	}
	return out, nil
}
