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
//
// The release is exact: it moves the rows named by each stored claim identity
// (run_video_outcome.retry_claim_provenance), never "every row this run owns"
// (ARS-005 §5.1.1). A live row owned by the run with no stored identity, or a
// live row outside the stored set, fails the handoff with ErrRetryProvenance
// or ErrRetryOwnership; the caller's transaction then rolls back unsettled.
func (s *Store) HandOffUnfinishedTx(ctx context.Context, tx *sql.Tx, runID int64, reason DeferredReason, unfinished []int64) (HandoffResult, error) {
	return s.HandOffUnfinishedExceptTx(ctx, tx, runID, reason, unfinished, 0)
}

// HandOffUnfinishedExceptTx is HandOffUnfinishedTx for a run whose retry of
// protectVideoID was refused because its work was removed or its ownership
// was lost. Any still-owned members of that video are released like the rest,
// but the video is never given a synthetic pending item: removed or lost work
// is not recreated. protectVideoID 0 protects nothing.
func (s *Store) HandOffUnfinishedExceptTx(ctx context.Context, tx *sql.Tx, runID int64, reason DeferredReason, unfinished []int64, protectVideoID int64) (HandoffResult, error) {
	var res HandoffResult
	if reason != DeferredReasonStoppedByUser && reason != DeferredReasonDiscardedByUser {
		return res, fmt.Errorf("queue: handoff reason %q is not a user handoff reason", reason)
	}

	stored, err := storedRunReceipts(ctx, tx, runID)
	if err != nil {
		return res, err
	}
	if err := requireStoredForLiveRows(ctx, tx, runID, stored); err != nil {
		return res, err
	}

	// 1. Claimed deferred items go back to pending, due immediately, and
	// claimed Errors-queue entries are resolved; their history stays. Only the
	// stored members move.
	resolvedAt := time.Now().UTC().Format(time.RFC3339)
	for _, receipt := range stored {
		for _, ref := range receipt.Deferred {
			r, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status='pending', retry_after=NULL
				WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, ref.ID, receipt.VideoID, ref.OriginRunID, runID)
			if err != nil {
				return res, fmt.Errorf("release deferred claim %d of run %d: %w", ref.ID, runID, err)
			}
			n, _ := r.RowsAffected()
			res.Requeued += int(n)
		}
		for _, ref := range receipt.Errors {
			r, err := tx.ExecContext(ctx, `UPDATE error_entry SET status='resolved', resolved_at=?
				WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, resolvedAt, ref.ID, receipt.VideoID, ref.OriginRunID, runID)
			if err != nil {
				return res, fmt.Errorf("release error claim %d of run %d: %w", ref.ID, runID, err)
			}
			n, _ := r.RowsAffected()
			res.Resolved += int(n)
		}
	}

	// 2. Every unfinished video gets exactly one due pending deferred item,
	// unless the user removed its claimed work while the run held it.
	for _, v := range unfinished {
		if v == protectVideoID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE deferred_item SET retry_after=NULL WHERE video_id=? AND status='pending'`, v); err != nil {
			return res, fmt.Errorf("make deferred work of video %d due: %w", v, err)
		}
		removed, err := claimedWorkRemoved(ctx, tx, runID, v, stored[v])
		if err != nil {
			return res, err
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

	// 3. Postcondition: the ended run owns nothing.
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM deferred_item WHERE status='retrying' AND retry_run_id=?) +
		(SELECT COUNT(*) FROM error_entry WHERE status='retrying' AND retry_run_id=?)`, runID, runID).Scan(&left); err != nil {
		return res, fmt.Errorf("verify release of run %d: %w", runID, err)
	}
	if left != 0 {
		return res, fmt.Errorf("run %d still owns %d live rows outside its stored claims: %w", runID, left, ErrRetryOwnership)
	}
	return res, nil
}

// storedRunReceipts decodes every stored claim identity of runID, by video.
// A present but unusable value fails with ErrRetryProvenance.
func storedRunReceipts(ctx context.Context, tx *sql.Tx, runID int64) (map[int64]RetryGroupReceipt, error) {
	rows, err := tx.QueryContext(ctx, `SELECT video_id FROM run_video_outcome WHERE run_id=? AND retry_claim_provenance IS NOT NULL ORDER BY video_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list stored claims of run %d: %w", runID, err)
	}
	var videos []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan stored claim of run %d: %w", runID, err)
		}
		videos = append(videos, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	stored := make(map[int64]RetryGroupReceipt, len(videos))
	for _, id := range videos {
		receipt, present, err := storedRetryReceipt(ctx, tx, runID, id)
		if err != nil {
			return nil, err
		}
		if present {
			stored[id] = receipt
		}
	}
	return stored, nil
}

// requireStoredForLiveRows refuses a run that holds live retry rows of a
// video it has no stored claim identity for (work claimed before migration
// 031). Nothing may be handed off or released for it.
func requireStoredForLiveRows(ctx context.Context, tx *sql.Tx, runID int64, stored map[int64]RetryGroupReceipt) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT video_id FROM (
		SELECT video_id FROM deferred_item WHERE status='retrying' AND retry_run_id=?1
		UNION SELECT video_id FROM error_entry WHERE status='retrying' AND retry_run_id=?1)`, runID)
	if err != nil {
		return fmt.Errorf("list live claims of run %d: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan live claim of run %d: %w", runID, err)
		}
		if _, ok := stored[id]; !ok {
			return fmt.Errorf("run %d holds live retry rows of video %d with no stored claim provenance: %w", runID, id, ErrRetryProvenance)
		}
	}
	return rows.Err()
}

// claimedWorkRemoved reports that the user removed work the run claimed. With
// a stored claim identity only its members count; without one (no claim, or
// history from before migration 031) the most-recent-owner marker is the
// only evidence, and it only suppresses a handoff, never authorizes work.
func claimedWorkRemoved(ctx context.Context, tx *sql.Tx, runID, videoID int64, receipt RetryGroupReceipt) (bool, error) {
	var removed bool
	if len(receipt.Deferred)+len(receipt.Errors) == 0 {
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE video_id=? AND retry_run_id=? AND status='discarded')
			OR EXISTS(SELECT 1 FROM error_entry WHERE video_id=? AND retry_run_id=? AND status='dismissed')`, videoID, runID, videoID, runID).Scan(&removed)
		if err != nil {
			return false, fmt.Errorf("check user removal of video %d: %w", videoID, err)
		}
		return removed, nil
	}
	for _, ref := range receipt.Deferred {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE id=? AND status='discarded')`, ref.ID).Scan(&removed); err != nil {
			return false, fmt.Errorf("check user removal of video %d: %w", videoID, err)
		}
		if removed {
			return true, nil
		}
	}
	for _, ref := range receipt.Errors {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM error_entry WHERE id=? AND status='dismissed')`, ref.ID).Scan(&removed); err != nil {
			return false, fmt.Errorf("check user removal of video %d: %w", videoID, err)
		}
		if removed {
			return true, nil
		}
	}
	return false, nil
}
