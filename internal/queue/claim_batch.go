package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrRunNotClaiming means the receiving run may not take another batch: it is
// no longer running or a stop was requested. Nothing was claimed.
var ErrRunNotClaiming = errors.New("queue: receiving run no longer claims")

// BatchClaim is the outcome of one batch claim transaction.
type BatchClaim struct {
	// Receipts are the groups the run now owns, in candidate order.
	Receipts []RetryGroupReceipt
	// ClosedFinal counts final videos whose queue family was closed. They are
	// not members of the run (BR-039; ARS-005 section 15).
	ClosedFinal int
	// Lost counts claims another live owner or a concurrent claimer won.
	Lost int
}

// ClaimDeferredBatchTx claims items for runID, in list order, in the caller's
// WithRetryTx transaction (ARS-005 section 5.2 step 4). It first rechecks the
// run row under the writer: the run must still be running, with no stop
// requested, and without a stored accounting or integrity dispatch stop. When
// it is not, nothing is claimed: ErrRunNotClaiming, or ErrRunDispatchStopped
// for the stored stop. A stored authorization stop still claims, because the
// claim and its settlement are local (variant K). The quota level is not read
// here; the claim loop evaluates it from the ledger just before.
//
// A lost claim is skipped. A final video's family is closed without
// membership (ClaimClosedFinal), the same as ClaimLost for the run: it is not
// a member and counts against nothing. Classification stays in
// claimRetryGroupTx.
func (s *Store) ClaimDeferredBatchTx(ctx context.Context, tx *sql.Tx, runID int64, items []DeferredItem, now time.Time) (BatchClaim, error) {
	var status string
	var stopRequested, dispatchStop sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status, stop_requested_at, dispatch_stop FROM run WHERE id = ?`, runID).Scan(&status, &stopRequested, &dispatchStop); err != nil {
		return BatchClaim{}, fmt.Errorf("batch claim run %d: %w", runID, err)
	}
	if dispatchStop.Valid && dispatchStop.String == "accounting_or_integrity" {
		return BatchClaim{}, fmt.Errorf("batch claim run %d: %w", runID, ErrRunDispatchStopped)
	}
	if status != "running" || stopRequested.Valid {
		return BatchClaim{}, fmt.Errorf("batch claim run %d (status %s, stop requested %t): %w", runID, status, stopRequested.Valid, ErrRunNotClaiming)
	}
	var out BatchClaim
	for _, item := range items {
		receipt, _, result, err := s.claimRetryGroupTx(ctx, tx, item.ID, runID, now, false)
		if err != nil {
			return BatchClaim{}, err
		}
		switch result {
		case ClaimTaken:
			receipt.Deferred = append([]RetryRowRef(nil), receipt.Deferred...)
			receipt.Errors = append([]RetryRowRef(nil), receipt.Errors...)
			out.Receipts = append(out.Receipts, receipt)
		case ClaimClosedFinal:
			out.ClosedFinal++
		default:
			out.Lost++
		}
	}
	return out, nil
}
