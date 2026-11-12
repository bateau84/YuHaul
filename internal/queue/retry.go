package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
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
	return s.claimRetryGroupTx(ctx, tx, entryID, runID, now, true)
}

func (s *Store) claimRetryGroupTx(ctx context.Context, tx *sql.Tx, entryID, runID int64, now time.Time, manual bool) (RetryGroupReceipt, int64, bool, error) {
	var receipt RetryGroupReceipt
	var runType, status string
	err := tx.QueryRowContext(ctx, `SELECT r.run_type, r.status FROM run r JOIN config_version c ON c.id = r.config_version_id WHERE r.id = ?`, runID).Scan(&runType, &status)
	if err != nil {
		return receipt, 0, false, fmt.Errorf("retry receiving run %d: %w", runID, err)
	}
	if (runType != "manual" && runType != "scheduled") || (status != "running" && status != "interrupted") || (manual && runType != "manual") {
		return receipt, 0, false, fmt.Errorf("retry receiving run %d: %w", runID, ErrInvalidState)
	}
	var videoID int64
	query := `SELECT video_id FROM deferred_item WHERE id = ? AND status IN ('pending','retrying')`
	if manual {
		query = `SELECT video_id FROM error_entry WHERE id = ? AND status IN ('active','retrying')`
	}
	if err := tx.QueryRowContext(ctx, query, entryID).Scan(&videoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return receipt, 0, false, nil
		}
		return receipt, 0, false, err
	}
	owner, err := retryOwner(ctx, tx, videoID)
	if err != nil {
		return receipt, videoID, false, err
	}
	if owner != 0 && owner != runID {
		return receipt, videoID, false, nil
	}
	if !manual && owner == 0 {
		var eligible bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		 SELECT 1 FROM deferred_item d LEFT JOIN run r ON r.id = d.retry_run_id
		 WHERE d.video_id = ? AND ((d.status = 'pending' AND (d.retry_after IS NULL OR julianday(d.retry_after) <= julianday(?)))
		 OR (d.status = 'retrying' AND (d.retry_run_id IS NULL OR r.status IN ('failed','stopped')))))`, videoID, now.UTC().Format(time.RFC3339Nano)).Scan(&eligible)
		if err != nil || !eligible {
			return receipt, videoID, false, err
		}
	}
	// Durable membership belongs to the receiving run, not the queue's
	// originating run. Insert only after admission succeeds; the surrounding
	// transaction also owns run creation and the claim, so rollback removes both.
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at)
		VALUES(?,?, NULL, ?) ON CONFLICT(run_id,video_id) DO NOTHING`,
		runID, videoID, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return receipt, videoID, false, fmt.Errorf("record retry run %d membership for video %d: %w", runID, videoID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status = 'retrying', retry_run_id = ?, retry_count = retry_count + CASE WHEN status = 'retrying' AND retry_run_id = ? THEN 0 ELSE 1 END WHERE video_id = ? AND status IN ('pending','retrying')`, runID, runID, videoID); err != nil {
		return receipt, videoID, false, err
	}
	if manual {
		if _, err := tx.ExecContext(ctx, `UPDATE error_entry SET status = 'retrying', retry_run_id = ?, retry_count = retry_count + CASE WHEN status = 'retrying' AND retry_run_id = ? THEN 0 ELSE 1 END WHERE video_id = ? AND status IN ('active','retrying')`, runID, runID, videoID); err != nil {
			return receipt, videoID, false, err
		}
	}
	if owner == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE video SET processing_state = 'not_yet_evaluated', final_outcome = 'not_yet_evaluated', deferred_reason = NULL, error_message = NULL, completed_at = NULL WHERE id = ?`, videoID); err != nil {
			return receipt, videoID, false, err
		}
	}
	receipt = RetryGroupReceipt{ReceivingRunID: runID, VideoID: videoID}
	for _, table := range []struct {
		name   string
		target *[]RetryRowRef
	}{{"deferred_item", &receipt.Deferred}, {"error_entry", &receipt.Errors}} {
		if table.name == "error_entry" && !manual {
			continue
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, run_id FROM `+table.name+` WHERE video_id=? AND status='retrying' AND retry_run_id=? ORDER BY id`, videoID, runID)
		if err != nil {
			return RetryGroupReceipt{}, videoID, false, err
		}
		for rows.Next() {
			var ref RetryRowRef
			if err := rows.Scan(&ref.ID, &ref.OriginRunID); err != nil {
				rows.Close()
				return RetryGroupReceipt{}, videoID, false, err
			}
			*table.target = append(*table.target, ref)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return RetryGroupReceipt{}, videoID, false, err
		}
		if err := rows.Close(); err != nil {
			return RetryGroupReceipt{}, videoID, false, err
		}
	}
	return receipt, videoID, true, nil
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

// RetryGroupReceiptForRunVideo snapshots the exact currently owned rows before
// evaluation begins. Claim paths should prefer the receipt returned by claim;
// this loader supports automatic-run dispatch/recovery after the claim commit.
func (s *Store) RetryGroupReceiptForRunVideo(ctx context.Context, runID, videoID int64) (RetryGroupReceipt, error) {
	r := RetryGroupReceipt{ReceivingRunID: runID, VideoID: videoID}
	for _, table := range []struct {
		name   string
		target *[]RetryRowRef
	}{{"deferred_item", &r.Deferred}, {"error_entry", &r.Errors}} {
		rows, err := s.db.QueryContext(ctx, `SELECT id, run_id FROM `+table.name+` WHERE video_id=? AND status='retrying' AND retry_run_id=? ORDER BY id`, videoID, runID)
		if err != nil {
			return RetryGroupReceipt{}, err
		}
		for rows.Next() {
			var ref RetryRowRef
			if err := rows.Scan(&ref.ID, &ref.OriginRunID); err != nil {
				rows.Close()
				return RetryGroupReceipt{}, err
			}
			*table.target = append(*table.target, ref)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return RetryGroupReceipt{}, err
		}
		if err := rows.Close(); err != nil {
			return RetryGroupReceipt{}, err
		}
	}
	if len(r.Deferred)+len(r.Errors) == 0 {
		return RetryGroupReceipt{}, fmt.Errorf("no retry rows owned by run %d for video %d", runID, videoID)
	}
	return r, nil
}

// ListDeferredRetryCandidates returns one oldest outstanding row per eligible
// video, in FIFO order. A claim rechecks eligibility and owners; this list is not
// authority. Track attempted videos for the execution even after requeueing.
func (s *Store) ListDeferredRetryCandidates(ctx context.Context, now time.Time) ([]DeferredItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after, d.retry_count, d.status, d.created_at, d.retry_run_id,
	 d.closed_reason, CASE WHEN d.closed_reason IS NOT NULL THEN v.final_outcome END
	 FROM deferred_item d JOIN video v ON v.id = d.video_id
	 WHERE d.id = (SELECT x.id FROM deferred_item x WHERE x.video_id = d.video_id AND x.status IN ('pending','retrying') ORDER BY x.created_at, x.id LIMIT 1)
	 AND EXISTS (SELECT 1 FROM deferred_item x LEFT JOIN run r ON r.id = x.retry_run_id WHERE x.video_id = d.video_id AND
	 ((x.status = 'pending' AND (x.retry_after IS NULL OR julianday(x.retry_after) <= julianday(?))) OR (x.status = 'retrying' AND (x.retry_run_id IS NULL OR r.status IN ('failed','stopped')))))
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
