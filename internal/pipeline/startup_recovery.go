package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("startup recovery transaction: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT r.id, r.status, r.completed_at, r.current_phase,
 r.mutation_safety_version, r.action_authority_version, c.snapshot
 FROM run r LEFT JOIN config_version c ON c.id=r.config_version_id
 WHERE r.run_type IN ('manual','scheduled') AND (r.status IN ('running','interrupted') OR (r.status='completed' AND r.completed_at IS NULL))
 AND r.mutation_safety_version <> 0
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
		var completed, phase, snapshot sql.NullString
		var safety, authority int
		if err := rows.Scan(&id, &status, &completed, &phase, &safety, &authority, &snapshot); err != nil {
			rows.Close()
			return nil, fmt.Errorf("startup recovery scan: %w", err)
		}
		if status == "completed" || completed.Valid {
			rows.Close()
			return nil, &startupCheckError{id, "terminal_timestamp_contradiction"}
		}
		if safety != 1 || authority != 1 {
			rows.Close()
			return nil, &startupCheckError{id, "authority"}
		}
		if reason := validateResumableRow(completed, phase, snapshot); reason != "" {
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
