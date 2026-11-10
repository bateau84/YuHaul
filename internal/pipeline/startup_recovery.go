package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
)

// RecoverStartup classifies and drains guarded runs before any producer starts.
// The caller must establish exclusive ownership of the database and provider dispatch.
// RecoverStartup is the synchronous composition used by tools and tests. It
// does not gate producers. The server uses BeginStartupRecovery and
// ResumeRecovered so the listener can start between the two (ADR-007).
func (p *Pipeline) RecoverStartup(ctx context.Context) error {
	ids, err := p.classifyAndLog(ctx)
	if err != nil {
		return err
	}
	return p.ResumeRecovered(ctx, ids)
}

// BeginStartupRecovery closes the one-way production-producer gate and then
// classifies recoverable runs in one short transaction (ADR-007 §1 step 3). It
// makes no provider call. The caller resumes the returned run IDs, oldest
// first, with ResumeRecovered and opens the gate with OpenProducers afterwards.
func (p *Pipeline) BeginStartupRecovery(ctx context.Context) ([]int64, error) {
	p.producerGate.CompareAndSwap(producersUngated, producersGated)
	return p.classifyAndLog(ctx)
}

// ResumeRecovered executes the classified runs one after another and stops
// early when ctx ends. A cancelled run is left interrupted (ADR-007 §3.1).
func (p *Pipeline) ResumeRecovered(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.Execute(ctx, id)
	}
	return ctx.Err()
}

func (p *Pipeline) classifyAndLog(ctx context.Context) ([]int64, error) {
	ids, err := p.classifyStartupRuns(ctx)
	if err != nil {
		attrs := []any{"check", "startup_recovery_io"}
		var ce *startupCheckError
		if errors.As(err, &ce) {
			attrs = []any{"run_id", ce.RunID, "check", ce.Check}
		}
		p.logger.ErrorContext(ctx, "startup recovery blocked", attrs...)
		return nil, err
	}
	p.logger.InfoContext(ctx, "recovered runs", "count", len(ids))
	return ids, nil
}

// startupCheckError names the run and the fixed check that blocked startup
// classification. Check is a closed identifier; no raw error text is carried.
type startupCheckError struct {
	RunID int64
	Check string
}

func (e *startupCheckError) Error() string {
	return fmt.Sprintf("startup recovery: run %d failed check %s", e.RunID, e.Check)
}

func (p *Pipeline) classifyStartupRuns(ctx context.Context) ([]int64, error) {
	// Rows with a stop marker were accepted by a stop that did not finish
	// settling. Read their ledger cost before the transaction: WithRetryTx and
	// this transaction forbid a second connection (ARS-003 §3.7).
	marked, err := p.markedStopRuns(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("startup recovery transaction: %w", err)
	}
	defer tx.Rollback()
	// A marked row is settled first and never resumed. A settle failure blocks
	// startup: the row cannot be resumed and must not be skipped.
	if len(marked) > 0 {
		qs, err := p.retryStore()
		if err != nil {
			return nil, fmt.Errorf("startup recovery stop settlement: %w", err)
		}
		for _, m := range marked {
			if _, err := settleStopTx(ctx, qs, p.stateStore, tx, m.id, m.quotaCost, time.Now().UTC()); err != nil {
				p.logger.ErrorContext(ctx, "startup stop settlement failed", "run_id", m.id, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
				return nil, &startupCheckError{m.id, "stop_settlement"}
			}
			p.logger.InfoContext(ctx, "startup settled a stopped run", "run_id", m.id)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id, r.status, r.completed_at, r.current_phase,
 r.mutation_safety_version, r.action_authority_version, c.snapshot, r.run_type, r.initiated_by, r.interrupt_cause
 FROM run r LEFT JOIN config_version c ON c.id=r.config_version_id
 WHERE r.run_type IN ('manual','scheduled') AND (r.status IN ('running','interrupted') OR (r.status='completed' AND r.completed_at IS NULL))
 AND r.mutation_safety_version <> 0 AND r.stop_requested_at IS NULL
 ORDER BY r.started_at, r.id`)
	if err != nil {
		return nil, fmt.Errorf("startup recovery read: %w", err)
	}
	type candidate struct {
		id            int64
		status, phase string
	}
	var candidates []candidate
	for rows.Next() {
		var id int64
		var status string
		var completed, phase, snapshot, runType, initiatedBy, interruptCause sql.NullString
		var safety, authority int
		if err := rows.Scan(&id, &status, &completed, &phase, &safety, &authority, &snapshot, &runType, &initiatedBy, &interruptCause); err != nil {
			rows.Close()
			return nil, fmt.Errorf("startup recovery scan: %w", err)
		}
		// The one row startup leaves alone: a time-limit Run Now run that Run
		// Now cannot resume. The user discards it from Run Detail (BR-017 AC11).
		timeLimitRunNow := runType.String == "manual" && initiatedBy.String == "run_now" && status == "interrupted" &&
			interruptCause.String == "time_limit" && safety == 1 && authority == 1
		if status == "completed" || completed.Valid {
			if timeLimitRunNow {
				p.logUnusableTimeLimitRun(ctx, id, resumeTerminalContradiction)
				continue
			}
			rows.Close()
			return nil, &startupCheckError{id, "terminal_timestamp_contradiction"}
		}
		if safety != 1 || authority != 1 {
			rows.Close()
			return nil, &startupCheckError{id, "authority"}
		}
		if reason := validateResumableRow(completed, phase, snapshot); reason != "" {
			if timeLimitRunNow {
				p.logUnusableTimeLimitRun(ctx, id, reason)
				continue
			}
			rows.Close()
			return nil, &startupCheckError{id, startupCheckFor(reason)}
		}
		ph := "unknown"
		if phase.Valid {
			ph = phase.String
		}
		candidates = append(candidates, candidate{id, status, ph})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("startup recovery iteration: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("startup recovery close: %w", err)
	}
	ids := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		if c.status == "running" {
			reason := (diag.Facts{Phase: c.phase, Operation: "load_run", Code: "unknown_error"}).String()
			result, err := tx.ExecContext(ctx, `UPDATE run SET status='interrupted', interrupt_reason=?, interrupt_cause=NULL WHERE id=? AND status='running' AND completed_at IS NULL AND run_type IN ('manual','scheduled') AND mutation_safety_version=1 AND action_authority_version=1`, reason, c.id)
			if err != nil {
				return nil, fmt.Errorf("startup recovery update: %w", err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				return nil, &startupCheckError{c.id, "recheck_at_write"}
			}
		}
		ids = append(ids, c.id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("startup recovery commit: %w", err)
	}
	return ids, nil
}

func (p *Pipeline) logUnusableTimeLimitRun(ctx context.Context, runID int64, reason string) {
	p.logger.WarnContext(ctx, "startup recovery left unusable run",
		"run_id", runID, "cause", discardCauseFor(reason), "reason", reason)
}

type markedStopRun struct {
	id        int64
	quotaCost int
}

// markedStopRuns lists the runs that carry a stop marker and are not yet
// stopped, oldest first, with their ledger cost.
func (p *Pipeline) markedStopRuns(ctx context.Context) ([]markedStopRun, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id FROM run WHERE stop_requested_at IS NOT NULL AND status IN ('running','interrupted') ORDER BY started_at, id`)
	if err != nil {
		return nil, fmt.Errorf("startup recovery read stop markers: %w", err)
	}
	var out []markedStopRun
	for rows.Next() {
		var m markedStopRun
		if err := rows.Scan(&m.id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("startup recovery scan stop marker: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("startup recovery iterate stop markers: %w", err)
	}
	for i := range out {
		cost, err := p.machine.RunQuotaCost(ctx, out[i].id)
		if err != nil {
			return nil, &startupCheckError{out[i].id, "stop_settlement"}
		}
		out[i].quotaCost = cost
	}
	return out, nil
}
