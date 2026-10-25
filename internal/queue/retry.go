package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrRetryOwnership signals conflicting or invalid durable owner evidence.
var ErrRetryOwnership = errors.New("queue: inconsistent retry ownership")

// WithRetryTx acquires the SQLite writer before invoking fn. Run creation,
// claiming and disposition handoffs can therefore share a single commit. fn
// must not perform network work or call store methods that use a second DB
// connection. An error (including a losing claim reported by fn) rolls back.
func (s *Store) WithRetryTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retry transaction: %w", err)
	}
	defer tx.Rollback()
	// A write, even with no matching rows, obtains SQLite's reserved writer lock
	// before any arbitration reads (a deferred read transaction is insufficient).
	if _, err := tx.ExecContext(ctx, `UPDATE run SET id = id WHERE id = -1`); err != nil {
		return fmt.Errorf("lock retry writer: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimDeferredGroupTx claims the selected item's outstanding video siblings.
// false means stale/ineligible work or another live owner; it changes nothing.
// Use WithRetryTx, and commit before evaluation. Same-owner recovery does not
// reset state or increment attempts again. runID must be unfinished production
// with a persisted configuration; the caller still validates snapshot contents.
func (s *Store) ClaimDeferredGroupTx(ctx context.Context, tx *sql.Tx, itemID, runID int64, now time.Time) (videoID int64, claimed bool, err error) {
	return s.claimRetryGroupTx(ctx, tx, itemID, runID, now, false)
}

// ClaimErrorGroupTx is explicit manual retry admission. It also claims all
// active/reclaimable error siblings and outstanding deferred siblings. Create
// the fresh manual receiving run in this same transaction; return an error from
// WithRetryTx on a losing claim so no orphan run can commit.
func (s *Store) ClaimErrorGroupTx(ctx context.Context, tx *sql.Tx, entryID, runID int64, now time.Time) (videoID int64, claimed bool, err error) {
	return s.claimRetryGroupTx(ctx, tx, entryID, runID, now, true)
}

func (s *Store) claimRetryGroupTx(ctx context.Context, tx *sql.Tx, entryID, runID int64, now time.Time, manual bool) (int64, bool, error) {
	var runType, status string
	err := tx.QueryRowContext(ctx, `SELECT r.run_type, r.status FROM run r JOIN config_version c ON c.id = r.config_version_id WHERE r.id = ?`, runID).Scan(&runType, &status)
	if err != nil {
		return 0, false, fmt.Errorf("retry receiving run %d: %w", runID, err)
	}
	if (runType != "manual" && runType != "scheduled") || (status != "running" && status != "interrupted") || (manual && runType != "manual") {
		return 0, false, fmt.Errorf("retry receiving run %d: %w", runID, ErrInvalidState)
	}
	var videoID int64
	query := `SELECT video_id FROM deferred_item WHERE id = ? AND status IN ('pending','retrying')`
	if manual {
		query = `SELECT video_id FROM error_entry WHERE id = ? AND status IN ('active','retrying')`
	}
	if err := tx.QueryRowContext(ctx, query, entryID).Scan(&videoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	owner, err := retryOwner(ctx, tx, videoID)
	if err != nil {
		return videoID, false, err
	}
	if owner != 0 && owner != runID {
		return videoID, false, nil
	}
	if !manual && owner == 0 {
		var eligible bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		 SELECT 1 FROM deferred_item d LEFT JOIN run r ON r.id = d.retry_run_id
		 WHERE d.video_id = ? AND ((d.status = 'pending' AND (d.retry_after IS NULL OR julianday(d.retry_after) <= julianday(?)))
		 OR (d.status = 'retrying' AND (d.retry_run_id IS NULL OR r.status = 'failed'))))`, videoID, now.UTC().Format(time.RFC3339Nano)).Scan(&eligible)
		if err != nil || !eligible {
			return videoID, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status = 'retrying', retry_run_id = ?, retry_count = retry_count + CASE WHEN status = 'retrying' AND retry_run_id = ? THEN 0 ELSE 1 END WHERE video_id = ? AND status IN ('pending','retrying')`, runID, runID, videoID); err != nil {
		return videoID, false, err
	}
	if manual {
		if _, err := tx.ExecContext(ctx, `UPDATE error_entry SET status = 'retrying', retry_run_id = ?, retry_count = retry_count + CASE WHEN status = 'retrying' AND retry_run_id = ? THEN 0 ELSE 1 END WHERE video_id = ? AND status IN ('active','retrying')`, runID, runID, videoID); err != nil {
			return videoID, false, err
		}
	}
	if owner == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE video SET processing_state = 'not_yet_evaluated', final_outcome = 'not_yet_evaluated', deferred_reason = NULL, error_message = NULL, completed_at = NULL WHERE id = ?`, videoID); err != nil {
			return videoID, false, err
		}
	}
	return videoID, true, nil
}

// retryOwner arbitrates both queues. Failed and legacy NULL owners are
// reclaimable; completed, missing, non-production or conflicting owners are
// integrity failures, not permission to erase unfinished work.
func retryOwner(ctx context.Context, tx *sql.Tx, videoID int64) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT q.retry_run_id, r.status, r.run_type FROM (
	 SELECT retry_run_id FROM deferred_item WHERE video_id = ? AND status = 'retrying'
	 UNION SELECT retry_run_id FROM error_entry WHERE video_id = ? AND status = 'retrying'
	) q LEFT JOIN run r ON r.id = q.retry_run_id WHERE q.retry_run_id IS NOT NULL`, videoID, videoID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var owner int64
	for rows.Next() {
		var id int64
		var status, mode sql.NullString
		if err := rows.Scan(&id, &status, &mode); err != nil {
			return 0, err
		}
		if !status.Valid || (mode.String != "manual" && mode.String != "scheduled") {
			return 0, fmt.Errorf("video %d invalid owner %d: %w", videoID, id, ErrRetryOwnership)
		}
		if status.String == "failed" {
			continue
		}
		if (status.String != "running" && status.String != "interrupted") || (owner != 0 && owner != id) {
			return 0, fmt.Errorf("video %d conflicting owner %d: %w", videoID, id, ErrRetryOwnership)
		}
		owner = id
	}
	return owner, rows.Err()
}

// RequeueRetryGroupTx releases unfinished work: all owned deferred siblings
// become pending and owned errors active. Most recent retry owner IDs remain.
// For a handoff to a newly retained queue disposition use Acknowledge instead,
// in the same transaction as the retention write.
func (s *Store) RequeueRetryGroupTx(ctx context.Context, tx *sql.Tx, videoID, runID int64) error {
	return s.finishRetryGroupTx(ctx, tx, videoID, runID, false)
}

// AcknowledgeRetryGroupTx completes all owned deferred siblings and resolves
// all owned error siblings atomically. The caller MUST persist a real terminal
// disposition or retained-work handoff in this transaction before calling it.
// It does not finalize counters or manufacture an outcome. Repetition is safe.
func (s *Store) AcknowledgeRetryGroupTx(ctx context.Context, tx *sql.Tx, videoID, runID int64) error {
	return s.finishRetryGroupTx(ctx, tx, videoID, runID, true)
}

func (s *Store) finishRetryGroupTx(ctx context.Context, tx *sql.Tx, videoID, runID int64, complete bool) error {
	owner, err := retryOwner(ctx, tx, videoID)
	if err != nil {
		return err
	}
	if owner != 0 && owner != runID {
		return fmt.Errorf("video %d owned by run %d: %w", videoID, owner, ErrRetryOwnership)
	}
	deferredStatus, errorStatus := "pending", "active"
	var resolvedAt any
	if complete {
		deferredStatus, errorStatus = "completed", "resolved"
		resolvedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status = ? WHERE video_id = ? AND retry_run_id = ? AND status = 'retrying'`, deferredStatus, videoID, runID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE error_entry SET status = ?, resolved_at = ? WHERE video_id = ? AND retry_run_id = ? AND status = 'retrying'`, errorStatus, resolvedAt, videoID, runID)
	return err
}

// ListRetryVideoIDs returns the distinct pre-decision recovery membership of
// both queues. Load this before phase dispatch; discovery ownership is not moved.
func (s *Store) ListRetryVideoIDs(ctx context.Context, runID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT video_id FROM (
	 SELECT video_id, created_at, id, ROW_NUMBER() OVER (PARTITION BY video_id ORDER BY created_at, id) AS position FROM (
	  SELECT video_id, created_at, id FROM deferred_item WHERE retry_run_id = ? AND status = 'retrying'
	  UNION ALL SELECT video_id, created_at, id FROM error_entry WHERE retry_run_id = ? AND status = 'retrying'
	 )
	) WHERE position = 1 ORDER BY created_at, id, video_id`, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListDeferredRetryCandidates returns one oldest outstanding row per eligible
// video, in FIFO order. A claim rechecks eligibility and owners; this list is not
// authority. Track attempted videos for the execution even after requeueing.
func (s *Store) ListDeferredRetryCandidates(ctx context.Context, now time.Time) ([]DeferredItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after, d.retry_count, d.status, d.created_at, d.retry_run_id
	 FROM deferred_item d JOIN video v ON v.id = d.video_id
	 WHERE d.id = (SELECT x.id FROM deferred_item x WHERE x.video_id = d.video_id AND x.status IN ('pending','retrying') ORDER BY x.created_at, x.id LIMIT 1)
	 AND EXISTS (SELECT 1 FROM deferred_item x LEFT JOIN run r ON r.id = x.retry_run_id WHERE x.video_id = d.video_id AND
	 ((x.status = 'pending' AND (x.retry_after IS NULL OR julianday(x.retry_after) <= julianday(?))) OR (x.status = 'retrying' AND (x.retry_run_id IS NULL OR r.status = 'failed'))))
	 ORDER BY d.created_at, d.id`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeferredItems(rows)
}

// RetainRuntimeErrorTx reconciles repeated integrity failure for the same
// origin run/video without rewriting an existing error's diagnostic history.
// Use alongside acknowledgement in WithRetryTx for an atomic failure handoff.
func (s *Store) RetainRuntimeErrorTx(ctx context.Context, tx *sql.Tx, videoID, originRunID int64, message, detail string) (int64, error) {
	var id int64
	// Do not reuse an owned retrying row: acknowledging its old group would
	// resolve the newly retained failure too. An active row is the handoff sink.
	err := tx.QueryRowContext(ctx, `SELECT id FROM error_entry WHERE video_id = ? AND run_id = ? AND error_category = 'runtime' AND status = 'active' ORDER BY id LIMIT 1`, videoID, originRunID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO error_entry (video_id, run_id, error_category, error_message, error_detail, status, created_at) VALUES (?, ?, 'runtime', ?, ?, 'active', ?)`, videoID, originRunID, message, detail, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
