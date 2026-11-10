package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

var errRetryClaimLost = errors.New("retry already owned or disposed")

// postCommitAccountingError is returned only after a deferred disposition and
// its guard handoff have committed, when final quota accounting then fails.
type postCommitAccountingError struct{ cause error }

func (e *postCommitAccountingError) Error() string {
	return "quota accounting failed after committed deferral"
}
func (e *postCommitAccountingError) Unwrap() error { return e.cause }

type guardedRetryHandoffError struct{ cause error }

func (e guardedRetryHandoffError) Error() string {
	return "guarded retry handoff failed: " + e.cause.Error()
}
func (e guardedRetryHandoffError) Unwrap() error { return e.cause }

// RetryQueueEntry owns the full manual retry lifecycle. The queue service must
// not acknowledge the row itself: nil also covers a durable quota requeue.
func (p *Pipeline) RetryQueueEntry(ctx context.Context, entryID int64, errorQueue bool) error {
	if err := p.refuseWhileRecovering(); err != nil {
		return err
	}
	if p.queueStore == nil || p.versioner == nil || p.configStore == nil {
		return fmt.Errorf("pipeline: retry requires queue and configuration stores")
	}
	run, err := p.prepareProductionRun(ctx, state.RunTypeManual)
	if err != nil {
		return err
	}
	initiator := state.InitiatedByQueueRetry
	run.InitiatedBy = &initiator
	p.productionMu.Lock()
	defer p.productionMu.Unlock()
	if p.auth == nil {
		return fmt.Errorf("pipeline: manual retry requires authorization binding")
	}
	binding, err := p.auth.Bind(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: bind authorization for manual retry: %w", err)
	}
	p.executionBinding = &binding
	defer func() { p.executionBinding = nil }()
	var claimedReceipt queue.RetryGroupReceipt
	err = p.queueStore.WithRetryTx(ctx, func(tx *sql.Tx) error {
		id, err := p.stateStore.CreateGuardedMutationRunTx(ctx, tx, run, true)
		if err != nil {
			return err
		}
		run.ID = id
		var claimed bool
		if errorQueue {
			claimedReceipt, _, claimed, err = p.queueStore.ClaimErrorGroupReceiptTx(ctx, tx, entryID, id, time.Now())
		} else {
			claimedReceipt, _, claimed, err = p.queueStore.ClaimDeferredGroupReceiptTx(ctx, tx, entryID, id, time.Now())
		}
		if err != nil {
			return err
		}
		if !claimed {
			return errRetryClaimLost
		}
		// Manual queue retry has no discovery workload. A restart dispatches
		// owned retry work first and then completes this empty actions phase.
		_, err = tx.ExecContext(ctx, `UPDATE run SET current_phase = ? WHERE id = ?`, string(PhaseActionExecution), id)
		return err
	})
	if err != nil {
		if errors.Is(err, errRetryClaimLost) {
			return nil // Return the existing queue result; no orphan run committed.
		}
		return err
	}
	p.broadcastEvent(run.ID, EventRunStarted, nil)
	// A manual retry run has no discovery workload: its selector is the claim
	// above, whose owned retry rows are now the run's complete input set.
	// Capture it before processing so a later failure keeps the known
	// selection (ARD-001).
	if ids, err := p.queueStore.ListRetryVideoIDs(ctx, run.ID); err != nil {
		p.logger.WarnContext(ctx, "list claimed retry inputs failed", "run_id", run.ID, "diagnostic", diag.Encode(err, "unknown", "retry"))
	} else {
		p.captureSelectedCount(ctx, run.ID, len(ids))
	}
	ctx = context.WithValue(ctx, guardedRetryGroupKey{}, claimedReceipt)
	if err := p.processDeferredItems(ctx, run.ID); err != nil {
		p.failRun(ctx, run.ID, "manual retry failed", err)
		return err
	}
	p.completeRun(ctx, run.ID)
	return nil
}

// A retry's global state was reset by the claim transaction, not inferred from
// old decisions. Resume a persisted prefix normally; terminal state means only
// acknowledgement remains. Never replay the originating run's matches.
func (p *Pipeline) processRetryVideo(ctx context.Context, runID, videoID int64) error {
	qs, err := queue.NewStore(p.db)
	if err != nil {
		return err
	}
	receipt, ok := ctx.Value(guardedRetryGroupKey{}).(queue.RetryGroupReceipt)
	if !ok || receipt.ReceivingRunID != runID || receipt.VideoID != videoID {
		receipt, err = qs.RetryGroupReceiptForRunVideo(ctx, runID, videoID)
		if err != nil {
			return fmt.Errorf("capture retry group receipt: %w", err)
		}
		ctx = context.WithValue(ctx, guardedRetryGroupKey{}, receipt)
	}
	v, err := p.stateStore.GetVideo(ctx, videoID)
	if err != nil {
		return err
	}
	var workErr error
	if v.FinalOutcome == state.FinalOutcomeNotYetEvaluated {
		var survivors []*state.Video
		routes, err := p.productionRoutes(ctx, runID)
		if err != nil {
			return err
		}
		survivors, workErr = p.runExclusionGate(ctx, runID, []*state.Video{v}, routes, true)
		if workErr == nil && len(survivors) > 0 {
			workErr = p.detectVideos(ctx, runID, survivors)
			if workErr == nil {
				_, workErr = p.routeVideos(ctx, runID, survivors)
			}
		}
	}
	if workErr == nil {
		v, err = p.stateStore.GetVideo(ctx, videoID)
		if err != nil {
			return err
		}
		if v.FinalOutcome == state.FinalOutcomeNotYetEvaluated && (v.ProcessingState == state.VideoStateEvaluated || v.ProcessingState == state.VideoStateActed) {
			_, workErr = p.executeVideoActions(ctx, runID, v)
		}
	}
	// The retained message carries the vetted bounded summary, not the raw
	// chain (ARD-001); only bounded auxiliary facts reach the log below.
	message := diag.Reason(workErr, "retry failed")
	var accountingErr *postCommitAccountingError
	if errors.As(workErr, &accountingErr) {
		return workErr
	}
	var guardedHandoffErr guardedRetryHandoffError
	if errors.As(workErr, &guardedHandoffErr) {
		// A failed auth/quota composition is already an attempted transaction
		// whose rollback must remain visible; it is not an ordinary provider
		// failure and must never be converted into a generic runtime sink.
		return workErr
	}
	var retainedFailure string
	// The executor commits its own exact quota handoff before returning a
	// deferred outcome. Do not run the outer failure fallback over that result:
	// its receipt is already intentionally no longer retrying.
	if workErr == nil {
		var outcome state.VideoFinalOutcome
		if err := p.db.QueryRowContext(ctx, `SELECT final_outcome FROM video WHERE id=?`, videoID).Scan(&outcome); err != nil {
			return err
		}
		if outcome == state.FinalOutcomeDeferred {
			return nil
		}
	}
	err = qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		owned, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt)
		if err != nil {
			if errors.Is(err, queue.ErrWorkRemoved) {
				committed, reconcileErr := committedRetryFailureTx(ctx, tx, receipt)
				if reconcileErr != nil {
					return reconcileErr
				}
				if committed {
					// The prior disposition committed before interruption. Its
					// active sink and failed outcome are the winner; never recreate
					// the sink or replay the provider action.
					return nil
				}
			}
			return err
		}
		var outcome state.VideoFinalOutcome
		if err := tx.QueryRowContext(ctx, `SELECT final_outcome FROM video WHERE id = ?`, videoID).Scan(&outcome); err != nil {
			return err
		}
		if workErr != nil || outcome == state.FinalOutcomeFailed {
			if _, err := qs.FinishRetryFailureTx(ctx, tx, receipt, queue.RetryFailureFacts{Message: message, Detail: message}); err != nil {
				return fmt.Errorf("finish retry failure: %w", err)
			}
			if err := p.recordRetryVideoFailureTx(ctx, tx, runID, videoID, message); err != nil {
				return fmt.Errorf("record retry video failure: %w", err)
			}
			retainedFailure = message
			return nil
		}
		if outcome == state.FinalOutcomeDeferred {
			// The executor has retained pending deferred work. Resolve the error
			// group only with that handoff, not merely because dispatch returned.
			var retained bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE video_id=? AND status IN ('pending','retrying'))`, videoID).Scan(&retained); err != nil {
				return err
			}
			if !retained {
				return fmt.Errorf("retry video %d deferred without retained work", videoID)
			}
			for _, ref := range owned.Errors {
				res, err := tx.ExecContext(ctx, `UPDATE error_entry SET status='resolved', resolved_at=? WHERE id=? AND video_id=? AND run_id=? AND retry_run_id=? AND status='retrying'`, time.Now().UTC().Format(time.RFC3339), ref.ID, videoID, ref.OriginRunID, receipt.ReceivingRunID)
				if err != nil {
					return err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				if n != 1 {
					return queue.ErrWorkNotOwned
				}
			}
			return qs.RequeueRetryReceiptTx(ctx, tx, receipt)
		}
		if outcome != state.FinalOutcomeAccepted && outcome != state.FinalOutcomeRejected && outcome != state.FinalOutcomeExcluded {
			return fmt.Errorf("retry video %d has no durable disposition: %s", videoID, outcome)
		}
		return acknowledgeRetryReceiptTx(ctx, tx, receipt, owned)
	})
	if err == nil && retainedFailure != "" {
		attrs := []any{"run_id", runID, "diagnostic", diag.GuardErrorText(retainedFailure)}
		var mutation *mutationFailure
		if errors.As(workErr, &mutation) {
			if mutation.accounting != nil {
				attrs = append(attrs, "accounting_diagnostic", diag.Encode(mutation.accounting, "action_execution", "account_quota"))
			}
			if mutation.renewal != nil {
				attrs = append(attrs, "renewal_category", auth.RenewalFailureCategory(mutation.renewal))
			}
		}
		p.logger.WarnContext(ctx, "retry handed to retained runtime error", attrs...)
	}
	return err
}

// committedRetryFailureTx recognizes only the exact already-committed
// failure disposition for this receipt. Similar-looking removed or
// reassigned rows are deliberately not treated as success.
func committedRetryFailureTx(ctx context.Context, tx *sql.Tx, receipt queue.RetryGroupReceipt) (bool, error) {
	if len(receipt.Deferred)+len(receipt.Errors) == 0 {
		return false, nil
	}
	committedMember := false
	for _, group := range []struct {
		table string
		refs  []queue.RetryRowRef
	}{{"deferred_item", receipt.Deferred}, {"error_entry", receipt.Errors}} {
		for _, ref := range group.refs {
			var video, origin int64
			var status string
			if err := tx.QueryRowContext(ctx, `SELECT video_id,run_id,status FROM `+group.table+` WHERE id=?`, ref.ID).Scan(&video, &origin, &status); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return false, nil
				}
				return false, err
			}
			committed := (group.table == "deferred_item" && status == "completed") || (group.table == "error_entry" && status == "resolved")
			removed := (group.table == "deferred_item" && status == "discarded") || (group.table == "error_entry" && status == "dismissed")
			if video != receipt.VideoID || origin != ref.OriginRunID || (!committed && !removed) {
				return false, nil
			}
			committedMember = committedMember || committed
		}
	}
	if !committedMember {
		return false, nil
	}
	var sink, failed int
	if err := tx.QueryRowContext(ctx, `SELECT
	 EXISTS(SELECT 1 FROM error_entry WHERE video_id=? AND run_id=? AND error_category='runtime' AND status='active'),
	 EXISTS(SELECT 1 FROM video WHERE id=? AND final_outcome='failed' AND processing_state='failed')`, receipt.VideoID, receipt.ReceivingRunID, receipt.VideoID).Scan(&sink, &failed); err != nil {
		return false, err
	}
	return sink == 1 && failed == 1, nil
}

func (p *Pipeline) recordRetryVideoFailureTx(ctx context.Context, tx *sql.Tx, runID, videoID int64, message string) error {
	video, err := p.stateStore.GetVideoTx(ctx, tx, videoID)
	if err != nil {
		return err
	}
	if video.FinalOutcome == state.FinalOutcomeFailed && video.ProcessingState == state.VideoStateFailed {
		outcome := state.FinalOutcomeFailed
		return p.stateStore.RecordRunVideoOutcomeTx(ctx, tx, runID, videoID, &outcome, nil, &message, video.CompletedAt)
	}
	if video.ProcessingState == state.VideoStateNotYetEvaluated {
		if _, err := p.machine.TransitionRunVideoStateTx(ctx, tx, runID, videoID,
			state.VideoStateNotYetEvaluated, state.VideoStateEvaluating, state.FinalOutcomeNotYetEvaluated, nil, nil); err != nil {
			return err
		}
		video.ProcessingState = state.VideoStateEvaluating
	}
	_, err = p.machine.TransitionRunVideoStateTx(ctx, tx, runID, videoID,
		video.ProcessingState, state.VideoStateFailed, state.FinalOutcomeFailed, nil, &message)
	return err
}

func acknowledgeRetryReceiptTx(ctx context.Context, tx *sql.Tx, receipt queue.RetryGroupReceipt, owned queue.RetryReceiptValidation) error {
	resolvedAt := time.Now().UTC().Format(time.RFC3339)
	for _, group := range []struct {
		table, status string
		refs          []queue.RetryRowRef
	}{{"deferred_item", "completed", owned.Deferred}, {"error_entry", "resolved", owned.Errors}} {
		for _, ref := range group.refs {
			query := `UPDATE ` + group.table + ` SET status=?`
			args := []any{group.status}
			if group.table == "error_entry" {
				query = `UPDATE error_entry SET status=?, resolved_at=?`
				args = append(args, resolvedAt)
			}
			query += ` WHERE id=? AND video_id=? AND run_id=? AND status='retrying' AND retry_run_id=?`
			args = append(args, ref.ID, receipt.VideoID, ref.OriginRunID, receipt.ReceivingRunID)
			result, err := tx.ExecContext(ctx, query, args...)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return queue.ErrWorkNotOwned
			}
		}
	}
	return nil
}

// Includes requeued and acknowledged rows: a quota requeue must not become a
// second ordinary phase attempt in the same receiving run.
func (p *Pipeline) withoutRetryVideos(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	out := make([]*state.Video, 0, len(videos))
	for _, v := range videos {
		var retry bool
		if err := p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE video_id=? AND retry_run_id=? UNION ALL SELECT 1 FROM error_entry WHERE video_id=? AND retry_run_id=?)`, v.ID, runID, v.ID, runID).Scan(&retry); err != nil {
			return nil, err
		}
		if !retry {
			out = append(out, v)
		}
	}
	return out, nil
}
