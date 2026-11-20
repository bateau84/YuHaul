package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/state"
)

// ErrRetryOwnership signals conflicting or invalid durable owner evidence.
var ErrRetryOwnership = errors.New("queue: inconsistent retry ownership")

// RetryRowRef identifies an exact queue row and its immutable origin run.
type RetryRowRef struct {
	ID          int64
	OriginRunID int64
}

// RetryGroupReceipt is the immutable identity of the claimed video group.
type RetryGroupReceipt struct {
	ReceivingRunID int64
	VideoID        int64
	Deferred       []RetryRowRef
	Errors         []RetryRowRef
}

// RetryReceiptValidation reports each exact receipt member independently.
// Removed contains missing or terminal members; NotOwned contains members
// that still exist but no longer satisfy the receiving run's live claim.
type RetryReceiptValidation struct {
	Deferred []RetryRowRef
	Errors   []RetryRowRef
	Removed  []RetryRowRef
	NotOwned []RetryRowRef
}

// ValidateRetryReceiptTx rechecks the exact rows in receipt while the caller
// holds the SQLite writer transaction. It never chooses replacement rows or
// creates a retained-work sink. A partial result is returned with ErrWorkNotOwned
// when any extant member has changed ownership/state; removed siblings do not
// veto still-owned members.
func (s *Store) ValidateRetryReceiptTx(ctx context.Context, tx *sql.Tx, receipt RetryGroupReceipt) (RetryReceiptValidation, error) {
	var result RetryReceiptValidation
	if tx == nil || receipt.ReceivingRunID <= 0 || receipt.VideoID <= 0 || len(receipt.Deferred)+len(receipt.Errors) == 0 {
		return result, fmt.Errorf("invalid retry receipt: %w", ErrWorkNotOwned)
	}
	var runType, runStatus string
	if err := tx.QueryRowContext(ctx, `SELECT run_type,status FROM run WHERE id=?`, receipt.ReceivingRunID).Scan(&runType, &runStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrWorkRemoved
		}
		return result, fmt.Errorf("validate retry receipt run: %w", err)
	}
	if (runType != "manual" && runType != "scheduled") || (runStatus != "running" && runStatus != "interrupted") {
		return result, fmt.Errorf("retry receipt receiving run %d is not unfinished production: %w", receipt.ReceivingRunID, ErrWorkNotOwned)
	}
	// The caller's copy must be exactly the claim identity stored at claim time.
	stored, err := requireStoredRetryReceipt(ctx, tx, receipt.ReceivingRunID, receipt.VideoID)
	if err != nil {
		return result, err
	}
	if !sameRetryReceipt(stored, receipt) {
		return result, fmt.Errorf("retry receipt of run %d video %d differs from its stored claim provenance: %w", receipt.ReceivingRunID, receipt.VideoID, ErrRetryProvenance)
	}
	seen := make(map[string]struct{}, len(receipt.Deferred)+len(receipt.Errors))
	for _, group := range []struct {
		table string
		refs  []RetryRowRef
		owned *[]RetryRowRef
	}{
		{"deferred_item", receipt.Deferred, &result.Deferred}, {"error_entry", receipt.Errors, &result.Errors},
	} {
		for _, ref := range group.refs {
			key := fmt.Sprintf("%s:%d", group.table, ref.ID)
			if ref.ID <= 0 || ref.OriginRunID <= 0 {
				return result, fmt.Errorf("invalid retry row reference: %w", ErrWorkNotOwned)
			}
			if _, ok := seen[key]; ok {
				return result, fmt.Errorf("duplicate retry row reference: %w", ErrWorkNotOwned)
			}
			seen[key] = struct{}{}
			var video, origin int64
			var status string
			var owner sql.NullInt64
			err := tx.QueryRowContext(ctx, `SELECT video_id,run_id,status,retry_run_id FROM `+group.table+` WHERE id=?`, ref.ID).Scan(&video, &origin, &status, &owner)
			if errors.Is(err, sql.ErrNoRows) {
				result.Removed = append(result.Removed, ref)
				continue
			}
			if err != nil {
				return result, fmt.Errorf("validate retry row %s %d: %w", group.table, ref.ID, err)
			}
			if video != receipt.VideoID || origin != ref.OriginRunID {
				result.NotOwned = append(result.NotOwned, ref)
				continue
			}
			terminal := (group.table == "deferred_item" && (status == "discarded" || status == "completed")) || (group.table == "error_entry" && (status == "dismissed" || status == "resolved"))
			if terminal {
				result.Removed = append(result.Removed, ref)
				continue
			}
			if status != "retrying" || !owner.Valid || owner.Int64 != receipt.ReceivingRunID {
				result.NotOwned = append(result.NotOwned, ref)
				continue
			}
			*group.owned = append(*group.owned, ref)
		}
	}
	owner, err := retryOwner(ctx, tx, receipt.VideoID)
	if err != nil {
		return result, fmt.Errorf("validate retry receipt cross-queue ownership: %w", err)
	}
	if owner != 0 && owner != receipt.ReceivingRunID {
		return result, fmt.Errorf("retry receipt video %d owned by run %d: %w", receipt.VideoID, owner, ErrWorkNotOwned)
	}
	for _, table := range []string{"deferred_item", "error_entry"} {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM `+table+` WHERE video_id=? AND status='retrying'`, receipt.VideoID)
		if err != nil {
			return result, fmt.Errorf("validate retry siblings in %s: %w", table, err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return result, fmt.Errorf("scan retry sibling in %s: %w", table, err)
			}
			if _, ok := seen[fmt.Sprintf("%s:%d", table, id)]; !ok {
				rows.Close()
				return result, fmt.Errorf("unexpected live retry sibling in %s: %w", table, ErrWorkNotOwned)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return result, fmt.Errorf("read retry siblings in %s: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return result, fmt.Errorf("close retry siblings in %s: %w", table, err)
		}
	}
	if len(result.NotOwned) != 0 {
		return result, ErrWorkNotOwned
	}
	if len(result.Deferred)+len(result.Errors) == 0 {
		return result, ErrWorkRemoved
	}
	return result, nil
}

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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrCommit, err)
	}
	return nil
}

// ErrCommit marks an error returned by WithRetryTx when fn succeeded but the
// transaction's Commit failed. The commit's outcome is then unconfirmed.
var ErrCommit = errors.New("queue: commit retry transaction")

// ClaimDeferredGroupTx claims the selected item's outstanding video siblings.
// false means stale/ineligible work or another live owner; it changes nothing.
// Use WithRetryTx, and commit before evaluation. Same-owner recovery does not
// reset state or increment attempts again. runID must be unfinished production
// with a persisted configuration; the caller still validates snapshot contents.
func (s *Store) ClaimDeferredGroupTx(ctx context.Context, tx *sql.Tx, itemID, runID int64, now time.Time) (videoID int64, claimed bool, err error) {
	_, videoID, claimed, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, itemID, runID, now)
	return
}

func (s *Store) ClaimDeferredGroupReceiptTx(ctx context.Context, tx *sql.Tx, itemID, runID int64, now time.Time) (RetryGroupReceipt, int64, bool, error) {
	receipt, videoID, result, err := s.claimRetryGroupTx(ctx, tx, itemID, runID, now, false)
	return receipt, videoID, result == ClaimTaken, err
}

// ClaimDeferredGroupResultTx is ClaimDeferredGroupReceiptTx with the full
// claim result, so a caller can tell a closed final video from a lost claim.
func (s *Store) ClaimDeferredGroupResultTx(ctx context.Context, tx *sql.Tx, itemID, runID int64, now time.Time) (RetryGroupReceipt, int64, ClaimResult, error) {
	return s.claimRetryGroupTx(ctx, tx, itemID, runID, now, false)
}

// ClaimErrorGroupTx is explicit manual retry admission. It also claims all
// active/reclaimable error siblings and outstanding deferred siblings. Create
// the fresh manual receiving run in this same transaction; return an error from
// WithRetryTx on a losing claim so no orphan run can commit.
func (s *Store) ClaimErrorGroupTx(ctx context.Context, tx *sql.Tx, entryID, runID int64, now time.Time) (videoID int64, claimed bool, err error) {
	_, videoID, claimed, err = s.ClaimErrorGroupReceiptTx(ctx, tx, entryID, runID, now)
	return
}

func (s *Store) ClaimErrorGroupReceiptTx(ctx context.Context, tx *sql.Tx, entryID, runID int64, now time.Time) (RetryGroupReceipt, int64, bool, error) {
	receipt, videoID, result, err := s.claimRetryGroupTx(ctx, tx, entryID, runID, now, true)
	return receipt, videoID, result == ClaimTaken, err
}

// ClaimResult is the outcome of a claim attempt (BR-039, ARS-006 §3.1).
type ClaimResult int

const (
	// ClaimLost: stale or ineligible work, or another live owner. Nothing changed.
	ClaimLost ClaimResult = iota
	// ClaimTaken: the receiving run owns the video's retry group.
	ClaimTaken
	// ClaimClosedFinal: the video is final; its queue family was closed and the
	// run received no membership.
	ClaimClosedFinal
)

// closeFinalFamilyTx closes the queue items of a final video: deferred items
// become completed and, when includeErrors, Errors entries become resolved,
// both with closed_reason='final_outcome'. Rows held by a live run are left
// alone. History fields (retry_run_id, retry_count, created_at) are kept.
// It returns the number of rows closed.
func closeFinalFamilyTx(ctx context.Context, tx *sql.Tx, videoID int64, includeErrors bool, now time.Time) (int, error) {
	res, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status='completed', closed_reason=?
		WHERE video_id=? AND (status='pending'
		   OR (status='retrying' AND (retry_run_id IS NULL OR retry_run_id IN (SELECT id FROM run WHERE status IN ('failed','stopped')))))`,
		ClosedReasonFinalOutcome, videoID)
	if err != nil {
		return 0, fmt.Errorf("close final video %d deferred items: %w", videoID, err)
	}
	total, err := rowsAffected(res)
	if err != nil {
		return 0, err
	}
	if includeErrors {
		res, err := tx.ExecContext(ctx, `UPDATE error_entry SET status='resolved', resolved_at=?, closed_reason=?
			WHERE video_id=? AND (status='active'
			   OR (status='retrying' AND (retry_run_id IS NULL OR retry_run_id IN (SELECT id FROM run WHERE status IN ('failed','stopped')))))`,
			now.UTC().Format(time.RFC3339), ClosedReasonFinalOutcome, videoID)
		if err != nil {
			return 0, fmt.Errorf("close final video %d error entries: %w", videoID, err)
		}
		n, err := rowsAffected(res)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

func rowsAffected(res sql.Result) (int, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count closed rows: %w", err)
	}
	return int(n), nil
}

// logFamilyClosed records that a final video's queue rows were closed. runID is
// the run doing the closing; 0 means none exists yet (a refused user retry).
func (s *Store) logFamilyClosed(ctx context.Context, runID, videoID, entryID int64, outcome string, rows int) {
	attrs := []any{"video_id", videoID, "entry_id", entryID, "final_outcome", outcome, "rows", rows}
	if runID != 0 {
		attrs = append([]any{"run_id", runID}, attrs...)
	}
	s.logger.InfoContext(ctx, "queue item closed: video final", attrs...)
}

// RefuseFinalRetryTx is the first step of a user retry (ARS-006 §4), run in the
// same transaction as the run creation and claim, before either. It looks up
// the entry whatever its status (a missing entry returns ErrNotFound). lost
// reports a live owner other than a run this retry would create: the retry is
// then a no-op, as today. When the entry's video is final, the family is closed
// and a non-nil *AlreadyFinalError is returned with a nil error; the caller
// commits, creates no run and answers the error after the commit.
func (s *Store) RefuseFinalRetryTx(ctx context.Context, tx *sql.Tx, entryID int64, errorQueue bool, now time.Time) (final *AlreadyFinalError, lost bool, err error) {
	var videoID int64
	table := "deferred_item"
	if errorQueue {
		table = "error_entry"
	}
	if err := tx.QueryRowContext(ctx, `SELECT video_id FROM `+table+` WHERE id = ?`, entryID).Scan(&videoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("%s %d: %w", table, entryID, ErrNotFound)
		}
		return nil, false, err
	}
	owner, err := retryOwner(ctx, tx, videoID)
	if err != nil {
		return nil, false, err
	}
	if owner != 0 {
		return nil, true, nil
	}
	class, outcome, err := state.ClassifyVideoTx(ctx, tx, videoID)
	if err != nil {
		return nil, false, err
	}
	if class != state.VideoClassFinal {
		return nil, false, nil
	}
	rows, err := closeFinalFamilyTx(ctx, tx, videoID, errorQueue, now)
	if err != nil {
		return nil, false, err
	}
	s.logFamilyClosed(ctx, 0, videoID, entryID, string(outcome), rows)
	return &AlreadyFinalError{VideoID: videoID, EntryID: entryID, ErrorQueue: errorQueue, Outcome: string(outcome)}, false, nil
}

func (s *Store) claimRetryGroupTx(ctx context.Context, tx *sql.Tx, entryID, runID int64, now time.Time, manual bool) (RetryGroupReceipt, int64, ClaimResult, error) {
	var receipt RetryGroupReceipt
	var runType, status string
	err := tx.QueryRowContext(ctx, `SELECT r.run_type, r.status FROM run r JOIN config_version c ON c.id = r.config_version_id WHERE r.id = ?`, runID).Scan(&runType, &status)
	if err != nil {
		return receipt, 0, ClaimLost, fmt.Errorf("retry receiving run %d: %w", runID, err)
	}
	if (runType != "manual" && runType != "scheduled") || (status != "running" && status != "interrupted") || (manual && runType != "manual") {
		return receipt, 0, ClaimLost, fmt.Errorf("retry receiving run %d: %w", runID, ErrInvalidState)
	}
	var videoID int64
	query := `SELECT video_id FROM deferred_item WHERE id = ? AND status IN ('pending','retrying')`
	if manual {
		query = `SELECT video_id FROM error_entry WHERE id = ? AND status IN ('active','retrying')`
	}
	if err := tx.QueryRowContext(ctx, query, entryID).Scan(&videoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return receipt, 0, ClaimLost, nil
		}
		return receipt, 0, ClaimLost, err
	}
	owner, err := retryOwner(ctx, tx, videoID)
	if err != nil {
		return receipt, videoID, ClaimLost, err
	}
	if owner != 0 && owner != runID {
		return receipt, videoID, ClaimLost, nil
	}
	class, outcome, err := state.ClassifyVideoTx(ctx, tx, videoID)
	if err != nil {
		return receipt, videoID, ClaimLost, err
	}
	if class == state.VideoClassFinal {
		var own sql.NullString
		if owner == runID {
			err := tx.QueryRowContext(ctx, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, videoID).Scan(&own)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return receipt, videoID, ClaimLost, fmt.Errorf("read run %d outcome of video %d: %w", runID, videoID, err)
			}
		}
		if owner == runID && own.Valid && own.String == string(outcome) {
			// M2: this run itself made the video final and was interrupted before
			// acknowledging. Hand back the stored receipt; reset and write nothing.
			receipt, err := requireStoredRetryReceipt(ctx, tx, runID, videoID)
			if err != nil {
				return RetryGroupReceipt{}, videoID, ClaimLost, err
			}
			return receipt, videoID, ClaimTaken, nil
		}
		rows, err := closeFinalFamilyTx(ctx, tx, videoID, manual, now)
		if err != nil {
			return receipt, videoID, ClaimLost, err
		}
		s.logFamilyClosed(ctx, runID, videoID, entryID, string(outcome), rows)
		return receipt, videoID, ClaimClosedFinal, nil
	}
	if owner == runID {
		// Same-owner recovery reads the stored claim identity. Live owner rows
		// can reveal an integrity problem but never supply receipt members.
		receipt, err := requireStoredRetryReceipt(ctx, tx, runID, videoID)
		if err != nil {
			return RetryGroupReceipt{}, videoID, ClaimLost, err
		}
		refs := receipt.Deferred
		if manual {
			refs = receipt.Errors
		}
		for _, ref := range refs {
			if ref.ID == entryID {
				return receipt, videoID, ClaimTaken, nil
			}
		}
		return RetryGroupReceipt{}, videoID, ClaimLost, nil
	}
	var admitted bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM run_video_outcome WHERE run_id=? AND video_id=?)`, runID, videoID).Scan(&admitted); err != nil {
		return receipt, videoID, ClaimLost, fmt.Errorf("read retry run %d membership for video %d: %w", runID, videoID, err)
	}
	if admitted {
		return receipt, videoID, ClaimLost, nil
	}
	if !manual && owner == 0 {
		var eligible bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		 SELECT 1 FROM deferred_item d LEFT JOIN run r ON r.id = d.retry_run_id
		 WHERE d.video_id = ? AND ((d.status = 'pending' AND (d.retry_after IS NULL OR julianday(d.retry_after) <= julianday(?)))
		 OR (d.status = 'retrying' AND (d.retry_run_id IS NULL OR r.status IN ('failed','stopped')))))`, videoID, now.UTC().Format(time.RFC3339Nano)).Scan(&eligible)
		if err != nil || !eligible {
			return receipt, videoID, ClaimLost, err
		}
	}
	// Durable membership belongs to the receiving run, not the queue's
	// originating run. Fix the exact claimed row set first, write it once with
	// the membership row, then move exactly those rows. The surrounding
	// transaction also owns run creation, so rollback removes all of it.
	receipt = RetryGroupReceipt{ReceivingRunID: runID, VideoID: videoID}
	if receipt.Deferred, err = claimRowRefs(ctx, tx, "deferred_item", videoID, `'pending','retrying'`); err != nil {
		return RetryGroupReceipt{}, videoID, ClaimLost, err
	}
	if manual {
		if receipt.Errors, err = claimRowRefs(ctx, tx, "error_entry", videoID, `'active','retrying'`); err != nil {
			return RetryGroupReceipt{}, videoID, ClaimLost, err
		}
	}
	provenance, err := encodeRetryProvenance(receipt)
	if err != nil {
		return RetryGroupReceipt{}, videoID, ClaimLost, fmt.Errorf("retry claim of run %d video %d: %v: %w", runID, videoID, err, ErrRetryProvenance)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at,retry_claim_provenance)
		VALUES(?,?, NULL, ?, ?)`, runID, videoID, now.UTC().Format(time.RFC3339Nano), provenance); err != nil {
		return RetryGroupReceipt{}, videoID, ClaimLost, fmt.Errorf("record retry run %d membership for video %d: %w", runID, videoID, err)
	}
	for _, group := range []struct {
		table string
		wasIn string
		refs  []RetryRowRef
	}{{"deferred_item", `'pending','retrying'`, receipt.Deferred}, {"error_entry", `'active','retrying'`, receipt.Errors}} {
		for _, ref := range group.refs {
			res, err := tx.ExecContext(ctx, `UPDATE `+group.table+` SET status = 'retrying', retry_run_id = ?, retry_count = retry_count + CASE WHEN status = 'retrying' AND retry_run_id = ? THEN 0 ELSE 1 END
				WHERE id = ? AND video_id = ? AND run_id = ? AND status IN (`+group.wasIn+`)`, runID, runID, ref.ID, videoID, ref.OriginRunID)
			if err != nil {
				return RetryGroupReceipt{}, videoID, ClaimLost, fmt.Errorf("claim %s %d: %w", group.table, ref.ID, err)
			}
			if n, err := rowsAffected(res); err != nil {
				return RetryGroupReceipt{}, videoID, ClaimLost, err
			} else if n != 1 {
				return RetryGroupReceipt{}, videoID, ClaimLost, fmt.Errorf("claim %s %d moved %d rows: %w", group.table, ref.ID, n, ErrRetryOwnership)
			}
		}
	}
	// owner == 0 here: a partly done video keeps its successes, so reset only
	// the evaluation state so its missing additions can finish (BR-039). Its
	// continuation under the original decision arrives with ARS-006 §7.
	resetSQL := `UPDATE video SET processing_state = 'not_yet_evaluated', final_outcome = 'not_yet_evaluated', deferred_reason = NULL, error_message = NULL, completed_at = NULL WHERE id = ?`
	if class == state.VideoClassPartlyDone {
		resetSQL = `UPDATE video SET processing_state = 'evaluated', final_outcome = 'not_yet_evaluated', deferred_reason = NULL, error_message = NULL WHERE id = ?`
	}
	if _, err := tx.ExecContext(ctx, resetSQL, videoID); err != nil {
		return RetryGroupReceipt{}, videoID, ClaimLost, err
	}
	return receipt, videoID, ClaimTaken, nil
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
		if status.String == "failed" || status.String == "stopped" {
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

// CloseRetryReceiptFinalTx closes the exact rows receipt still owns because
// the video became final after the claim (BR-039, ARS-006 §5): deferred rows
// become completed and Errors rows resolved, both with
// closed_reason='final_outcome'. It reports how many rows it closed. Rows an
// operator already removed leave nothing to close and return ErrWorkRemoved.
func (s *Store) CloseRetryReceiptFinalTx(ctx context.Context, tx *sql.Tx, receipt RetryGroupReceipt) (int, error) {
	owned, err := s.ValidateRetryReceiptTx(ctx, tx, receipt)
	if err != nil {
		return 0, err
	}
	var outcome string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(final_outcome,'') FROM video WHERE id=?`, receipt.VideoID).Scan(&outcome); err != nil {
		return 0, fmt.Errorf("read video %d outcome: %w", receipt.VideoID, err)
	}
	resolvedAt := time.Now().UTC().Format(time.RFC3339)
	closed := 0
	for _, group := range []struct {
		table, set string
		args       []any
		refs       []RetryRowRef
	}{
		{"deferred_item", `status='completed', closed_reason=?`, []any{ClosedReasonFinalOutcome}, owned.Deferred},
		{"error_entry", `status='resolved', resolved_at=?, closed_reason=?`, []any{resolvedAt, ClosedReasonFinalOutcome}, owned.Errors},
	} {
		for _, ref := range group.refs {
			args := append(append([]any(nil), group.args...), ref.ID, receipt.VideoID, ref.OriginRunID, receipt.ReceivingRunID)
			res, err := tx.ExecContext(ctx, `UPDATE `+group.table+` SET `+group.set+` WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, args...)
			if err != nil {
				return closed, fmt.Errorf("close final retry row %s %d: %w", group.table, ref.ID, err)
			}
			n, err := rowsAffected(res)
			if err != nil {
				return closed, err
			}
			if n != 1 {
				return closed, fmt.Errorf("close final retry row %s %d: %w", group.table, ref.ID, ErrWorkNotOwned)
			}
			closed += n
		}
	}
	first := int64(0)
	if len(owned.Deferred) > 0 {
		first = owned.Deferred[0].ID
	} else if len(owned.Errors) > 0 {
		first = owned.Errors[0].ID
	}
	s.logFamilyClosed(ctx, receipt.ReceivingRunID, receipt.VideoID, first, outcome, closed)
	return closed, nil
}

// RequeueRetryReceiptTx releases only the exact retry rows admitted by receipt.
// It is safe to use in a disposition transaction alongside action and send-guard
// writes: any SQL failure leaves all three unchanged when the caller rolls back.
func (s *Store) RequeueRetryReceiptTx(ctx context.Context, tx *sql.Tx, receipt RetryGroupReceipt) error {
	owned, err := s.ValidateRetryReceiptTx(ctx, tx, receipt)
	if err != nil {
		return err
	}
	for _, group := range []struct {
		table string
		refs  []RetryRowRef
	}{{"deferred_item", owned.Deferred}, {"error_entry", owned.Errors}} {
		status := "pending"
		if group.table == "error_entry" {
			status = "active"
		}
		for _, ref := range group.refs {
			res, err := tx.ExecContext(ctx, `UPDATE `+group.table+` SET status=?, retry_run_id=NULL WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, status, ref.ID, receipt.VideoID, ref.OriginRunID, receipt.ReceivingRunID)
			if err != nil {
				return fmt.Errorf("requeue exact retry row %s %d: %w", group.table, ref.ID, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return fmt.Errorf("requeue exact retry row %s %d: %w", group.table, ref.ID, ErrWorkNotOwned)
			}
		}
	}
	return nil
}

// AcknowledgeRetryDeferralReceiptTx makes exact participating deferred rows
// eligible for a later run while retaining their most-recent owner for
// same-run exclusion. Participating errors are resolved after the caller has
// durably retained the deferred disposition. The caller includes this in the
// same transaction as action evidence and send-guard resolution.
func (s *Store) AcknowledgeRetryDeferralReceiptTx(ctx context.Context, tx *sql.Tx, receipt RetryGroupReceipt) error {
	owned, err := s.ValidateRetryReceiptTx(ctx, tx, receipt)
	if err != nil {
		return err
	}
	resolvedAt := time.Now().UTC().Format(time.RFC3339)
	for _, ref := range owned.Deferred {
		res, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status='pending' WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, ref.ID, receipt.VideoID, ref.OriginRunID, receipt.ReceivingRunID)
		if err != nil {
			return fmt.Errorf("requeue deferred retry row %d: %w", ref.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("requeue deferred retry row %d: %w", ref.ID, ErrWorkNotOwned)
		}
	}
	for _, ref := range owned.Errors {
		res, err := tx.ExecContext(ctx, `UPDATE error_entry SET status='resolved', resolved_at=? WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, resolvedAt, ref.ID, receipt.VideoID, ref.OriginRunID, receipt.ReceivingRunID)
		if err != nil {
			return fmt.Errorf("resolve deferred retry error %d: %w", ref.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("resolve deferred retry error %d: %w", ref.ID, ErrWorkNotOwned)
		}
	}
	return nil
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

// RetryGroupReceiptForRunVideo returns the claim identity stored when the
// receiving run claimed the video. It never reconstructs a receipt from owner
// pointers: a sibling that later gained the same owner is not a member, and
// members that are now terminal or missing stay in it. ErrWorkRemoved means the
// run owns no live retry row for the video (disposed-only history is not a work
// candidate). ErrRetryProvenance means unfinished work has no trustworthy
// stored identity (legacy NULL, malformed or unsupported value).
func (s *Store) RetryGroupReceiptForRunVideo(ctx context.Context, runID, videoID int64) (RetryGroupReceipt, error) {
	var live bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM deferred_item WHERE video_id=? AND retry_run_id=? AND status='retrying')
		OR EXISTS (SELECT 1 FROM error_entry WHERE video_id=? AND retry_run_id=? AND status='retrying')`, videoID, runID, videoID, runID).Scan(&live); err != nil {
		return RetryGroupReceipt{}, fmt.Errorf("load retry receipt owners: %w", err)
	}
	if !live {
		return RetryGroupReceipt{}, fmt.Errorf("no live retry rows owned by run %d for video %d: %w", runID, videoID, ErrWorkRemoved)
	}
	return requireStoredRetryReceipt(ctx, s.db, runID, videoID)
}

// ListDeferredRetryCandidates returns one oldest outstanding row per eligible
// video, in FIFO order. A claim rechecks eligibility and owners; this list is not
// authority. Track attempted videos for the execution even after requeueing.
func (s *Store) ListDeferredRetryCandidates(ctx context.Context, now time.Time) ([]DeferredItem, error) {
	return s.ListDeferredRetryCandidatesForRun(ctx, now, 0, -1)
}

// ListDeferredRetryCandidatesForRun excludes work already admitted by the
// receiving run. A zero run ID excludes nothing; a negative limit is unbounded.
func (s *Store) ListDeferredRetryCandidatesForRun(ctx context.Context, now time.Time, receivingRunID int64, limit int) ([]DeferredItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after, d.retry_count, d.status, d.created_at, d.retry_run_id,
	 d.closed_reason, CASE WHEN d.closed_reason IS NOT NULL THEN v.final_outcome END
	 FROM deferred_item d JOIN video v ON v.id = d.video_id
	 WHERE d.id = (SELECT x.id FROM deferred_item x WHERE x.video_id = d.video_id AND x.status IN ('pending','retrying') ORDER BY x.created_at, x.id LIMIT 1)
	 AND EXISTS (SELECT 1 FROM deferred_item x LEFT JOIN run r ON r.id = x.retry_run_id WHERE x.video_id = d.video_id AND
	 ((x.status = 'pending' AND (x.retry_after IS NULL OR julianday(x.retry_after) <= julianday(?))) OR (x.status = 'retrying' AND (x.retry_run_id IS NULL OR r.status IN ('failed','stopped')))))
	 AND NOT EXISTS (SELECT 1 FROM run_video_outcome o WHERE o.run_id = ? AND o.video_id = d.video_id)
	 ORDER BY d.created_at, d.id LIMIT ?`, now.UTC().Format(time.RFC3339Nano), receivingRunID, limit)
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
	return s.retainErrorTx(ctx, tx, videoID, originRunID, ErrorCategoryRuntime, message, detail)
}

// RetainRunSinkTx keeps exactly one active retained error entry for
// (video, originRun), whatever its category (ADR-006). An existing active entry
// is reused untouched; otherwise one is created. Explicitly dismissed or
// resolved entries are not reused, and no other run's entry is considered.
// Call inside WithRetryTx so the sink commits with the caller's disposition.
func (s *Store) RetainRunSinkTx(ctx context.Context, tx *sql.Tx, videoID, originRunID int64, category ErrorCategory, message, detail string) (int64, error) {
	if tx == nil {
		return 0, errors.New("retaining run sink: transaction is required")
	}
	if !category.Valid() {
		return 0, fmt.Errorf("retaining run sink: invalid category %q", category)
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM error_entry WHERE video_id=? AND run_id=? AND status='active' ORDER BY id LIMIT 1`, videoID, originRunID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("checking run sink: %w", err)
	}
	return s.retainErrorTx(ctx, tx, videoID, originRunID, category, message, detail)
}

// RetryFailureFacts are bounded diagnostic facts for an ordinary retry
// failure. Callers must not pass raw provider bodies or unbounded error chains.
type RetryFailureFacts struct {
	Message string
	Detail  string
}

// FinishRetryFailureTx validates the exact live receipt, creates or proves one
// active runtime sink owned by the receiving run, and resolves only surviving
// receipt rows. It deliberately uses the caller's transaction: any later
// failure must cause that caller to roll back the sink and source changes.
// ErrWorkRemoved is returned without creating a sink when every member was
// removed; wrong ownership and storage errors also fail closed.
func (s *Store) FinishRetryFailureTx(ctx context.Context, tx *sql.Tx, receipt RetryGroupReceipt, facts RetryFailureFacts) (int64, error) {
	if tx == nil {
		return 0, errors.New("finishing retry failure: transaction is required")
	}
	validated, err := s.ValidateRetryReceiptTx(ctx, tx, receipt)
	if err != nil {
		return 0, err
	}
	sinkID, err := s.RetainRuntimeErrorTx(ctx, tx, receipt.VideoID, receipt.ReceivingRunID, facts.Message, facts.Detail)
	if err != nil {
		return 0, fmt.Errorf("create retry runtime sink: %w", err)
	}
	resolvedAt := time.Now().UTC().Format(time.RFC3339)
	for _, group := range []struct {
		table string
		refs  []RetryRowRef
	}{{"deferred_item", validated.Deferred}, {"error_entry", validated.Errors}} {
		for _, ref := range group.refs {
			var result sql.Result
			if group.table == "deferred_item" {
				result, err = tx.ExecContext(ctx, `UPDATE deferred_item SET status='completed' WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, ref.ID, receipt.VideoID, ref.OriginRunID, receipt.ReceivingRunID)
			} else {
				result, err = tx.ExecContext(ctx, `UPDATE error_entry SET status='resolved', resolved_at=? WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`, resolvedAt, ref.ID, receipt.VideoID, ref.OriginRunID, receipt.ReceivingRunID)
			}
			if err != nil {
				return 0, fmt.Errorf("resolve retry source %s %d: %w", group.table, ref.ID, err)
			}
			n, err := result.RowsAffected()
			if err != nil {
				return 0, fmt.Errorf("verify retry source %s %d resolution: %w", group.table, ref.ID, err)
			}
			if n != 1 {
				return 0, fmt.Errorf("resolve retry source %s %d: %w", group.table, ref.ID, ErrWorkNotOwned)
			}
		}
	}
	return sinkID, nil
}

// RetainAuthorizationPendingTx retains work blocked before provider admission.
// A dismissed row is authoritative user removal and is never recreated.
func (s *Store) RetainAuthorizationPendingTx(ctx context.Context, tx *sql.Tx, videoID, originRunID int64, message, detail string) (int64, error) {
	return s.retainErrorTx(ctx, tx, videoID, originRunID, ErrorCategoryAuthorization, message, detail)
}

// RetainRuntimeErrorForRetryTx updates the active retry sink only while it is
// still owned by retryRunID. A missing sink is distinct from explicit removal.
func (s *Store) RetainRuntimeErrorForRetryTx(ctx context.Context, tx *sql.Tx, videoID, originRunID, retryRunID int64, message, detail string) (int64, error) {
	return s.retainOwnedErrorTx(ctx, tx, videoID, originRunID, retryRunID, ErrorCategoryRuntime, message, detail)
}

// RetainAuthorizationPendingForRetryTx updates the active retry sink only
// while it is still owned by retryRunID. Dismissed/resolved rows are not sinks.
func (s *Store) RetainAuthorizationPendingForRetryTx(ctx context.Context, tx *sql.Tx, videoID, originRunID, retryRunID int64, message, detail string) (int64, error) {
	return s.retainOwnedErrorTx(ctx, tx, videoID, originRunID, retryRunID, ErrorCategoryAuthorization, message, detail)
}

// RetainDeferredForRetryTx returns the exact claimed deferred sink only while
// its video, origin and retry owner still match. Discarded work is explicit
// removal, not a miss.
func (s *Store) RetainDeferredForRetryTx(ctx context.Context, tx *sql.Tx, itemID, videoID, originRunID, retryRunID int64) (int64, error) {
	if tx == nil {
		return 0, errors.New("retaining deferred retry work: transaction is required")
	}
	var id int64
	var status DeferredStatus
	var owner sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, status, retry_run_id FROM deferred_item WHERE id=? AND video_id=? AND run_id=?`, itemID, videoID, originRunID).Scan(&id, &status, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrWorkRemoved
	}
	if err != nil {
		return 0, fmt.Errorf("checking deferred retry ownership: %w", err)
	}
	if status == DeferredStatusDiscarded || status == DeferredStatusCompleted {
		return 0, ErrWorkRemoved
	}
	if status != DeferredStatusRetrying || !owner.Valid || owner.Int64 != retryRunID {
		return 0, ErrWorkNotOwned
	}
	return id, nil
}

var ErrWorkRemoved = errors.New("queue: work explicitly removed")
var ErrWorkNotOwned = errors.New("queue: retained work is not owned by retry run")

func (s *Store) retainOwnedErrorTx(ctx context.Context, tx *sql.Tx, videoID, originRunID, retryRunID int64, category ErrorCategory, message, detail string) (int64, error) {
	if tx == nil {
		return 0, errors.New("retaining retry error: transaction is required")
	}
	var id int64
	var status ErrorStatus
	var owner sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, status, retry_run_id FROM error_entry WHERE video_id=? AND run_id=? AND error_category=? ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, id DESC LIMIT 1`, videoID, originRunID, category).Scan(&id, &status, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrWorkRemoved
	}
	if err != nil {
		return 0, fmt.Errorf("checking retry error ownership: %w", err)
	}
	if status == ErrorStatusDismissed || status == ErrorStatusResolved {
		return 0, ErrWorkRemoved
	}
	if status != ErrorStatusRetrying || !owner.Valid || owner.Int64 != retryRunID {
		return 0, ErrWorkNotOwned
	}
	message = diag.GuardErrorText(message)
	detail = diag.GuardErrorText(detail)
	return id, nil
}

func (s *Store) retainErrorTx(ctx context.Context, tx *sql.Tx, videoID, originRunID int64, category ErrorCategory, message, detail string) (int64, error) {
	message = diag.GuardErrorText(message)
	detail = diag.GuardErrorText(detail)
	var id int64
	// Preserve an authorization queue decision, including a discard racing the
	// provider request. Never turn explicitly removed work active.
	if category == ErrorCategoryAuthorization {
		err := tx.QueryRowContext(ctx, `SELECT id FROM error_entry WHERE video_id=? AND run_id=? AND error_category=? ORDER BY id LIMIT 1`, videoID, originRunID, category).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	// Do not reuse an owned retrying row: acknowledging its old group would
	// resolve the newly retained failure too. An active row is the handoff sink.
	err := tx.QueryRowContext(ctx, `SELECT id FROM error_entry WHERE video_id = ? AND run_id = ? AND error_category = ? AND status = 'active' ORDER BY id LIMIT 1`, videoID, originRunID, category).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO error_entry (video_id, run_id, error_category, error_message, error_detail, status, created_at) VALUES (?, ?, ?, ?, ?, 'active', ?)`, videoID, originRunID, category, message, detail, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
