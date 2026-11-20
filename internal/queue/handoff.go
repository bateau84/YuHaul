package queue

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// HandoffResult counts what a handoff changed, for logs and tests.
type HandoffResult struct{ Requeued, Created, Resolved, Removed int }

// HandOffUnfinishedTx hands every unfinished video of ended run runID to the
// deferred queue and releases all of its claims. It runs in the caller's
// transaction, makes no network call and writes no guard or action row
// (run-initiation ARS-003 §2).
//
// reason must be DeferredReasonStoppedByUser or DeferredReasonDiscardedByUser.
// retry_run_id is deliberately kept as the most-recent-owner marker.
func (s *Store) HandOffUnfinishedTx(ctx context.Context, tx *sql.Tx, runID int64, reason DeferredReason, unfinished []int64) (HandoffResult, error) {
	var res HandoffResult
	if reason != DeferredReasonStoppedByUser && reason != DeferredReasonDiscardedByUser {
		return res, fmt.Errorf("queue: handoff reason %q is not a user handoff reason", reason)
	}

	// 1. Claimed deferred items go back to pending, due immediately.
	r, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status='pending', retry_after=NULL WHERE status='retrying' AND retry_run_id=?`, runID)
	if err != nil {
		return res, fmt.Errorf("release deferred claims of run %d: %w", runID, err)
	}
	n, _ := r.RowsAffected()
	res.Requeued = int(n)

	// 2. Claimed Errors-queue entries are resolved; their history stays.
	r, err = tx.ExecContext(ctx, `UPDATE error_entry SET status='resolved', resolved_at=? WHERE status='retrying' AND retry_run_id=?`,
		time.Now().UTC().Format(time.RFC3339), runID)
	if err != nil {
		return res, fmt.Errorf("release error claims of run %d: %w", runID, err)
	}
	n, _ = r.RowsAffected()
	res.Resolved = int(n)

	// 3. Every unfinished video gets exactly one due pending deferred item,
	// unless the user removed its claimed work while the run held it.
	for _, v := range unfinished {
		if _, err := tx.ExecContext(ctx, `UPDATE deferred_item SET retry_after=NULL WHERE video_id=? AND status='pending'`, v); err != nil {
			return res, fmt.Errorf("make deferred work of video %d due: %w", v, err)
		}
		var removed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE video_id=? AND retry_run_id=? AND status='discarded')
			OR EXISTS(SELECT 1 FROM error_entry WHERE video_id=? AND retry_run_id=? AND status='dismissed')`, v, runID, v, runID).Scan(&removed); err != nil {
			return res, fmt.Errorf("check user removal of video %d: %w", v, err)
		}
		if removed {
			res.Removed++
			continue
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE video_id=? AND status='pending')`, v).Scan(&pending); err != nil {
			return res, fmt.Errorf("check pending work of video %d: %w", v, err)
		}
		if pending {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deferred_item(video_id, run_id, deferral_reason, retry_after, retry_count, status)
			VALUES(?, ?, ?, NULL, 0, 'pending')`, v, runID, string(reason)); err != nil {
			return res, fmt.Errorf("hand off video %d: %w", v, err)
		}
		res.Created++
	}

	// 4. Postcondition: the ended run owns nothing.
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM deferred_item WHERE status='retrying' AND retry_run_id=?) +
		(SELECT COUNT(*) FROM error_entry WHERE status='retrying' AND retry_run_id=?)`, runID, runID).Scan(&left); err != nil {
		return res, fmt.Errorf("verify release of run %d: %w", runID, err)
	}
	if left != 0 {
		return res, fmt.Errorf("run %d still owns %d claimed rows: %w", runID, left, ErrRetryOwnership)
	}
	return res, nil
}
