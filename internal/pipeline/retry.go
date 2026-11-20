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
	"github.com/bateau84/YuHaul/internal/router"
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
	var claimedReceipt queue.RetryGroupReceipt
	var alreadyFinal *queue.AlreadyFinalError
	err = p.queueStore.WithRetryTx(ctx, func(tx *sql.Tx) error {
		// BR-039 / ARS-006 §4: refuse a final video's retry before any run is
		// created or bound. The family is closed and committed; no run exists.
		final, lost, err := p.queueStore.RefuseFinalRetryTx(ctx, tx, entryID, errorQueue, time.Now())
		if err != nil {
			return err
		}
		if lost {
			return errRetryClaimLost
		}
		if final != nil {
			alreadyFinal = final
			return nil
		}
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
	if alreadyFinal != nil {
		return alreadyFinal
	}
	ctl := p.registerRun(run.ID)
	defer p.finishRun(ctx, run.ID)
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
	ctx, stopped := ctl.attach(ctx)
	if stopped {
		return nil // settled by finishRun
	}
	// Authorization is bound only now: a refused retry above made no
	// authorization or provider call.
	binding, err := p.auth.Bind(ctx)
	if err != nil {
		bindErr := fmt.Errorf("pipeline: bind authorization for manual retry: %w", err)
		p.failRun(ctx, run.ID, "authorization binding failed", bindErr)
		return bindErr
	}
	p.executionBinding = &binding
	defer func() { p.executionBinding = nil }()
	if err := p.processDeferredItems(ctx, run.ID, []queue.RetryGroupReceipt{claimedReceipt}, false); err != nil {
		p.failRun(ctx, run.ID, "manual retry failed", err)
		if stopOwnsEnding(ctx) || errors.Is(err, state.ErrPlaylistSendStopped) {
			return nil // the user stopped this retry; finishRun settles it
		}
		return err
	}
	p.completeRun(ctx, run.ID)
	return nil
}

// retryAdmissionRefusal preserves the original claim for run-ending settlement.
// It must not be interpreted as permission to mutate any of its rows.
type retryAdmissionRefusal struct {
	receipt queue.RetryGroupReceipt
	cause   error
}

func (e *retryAdmissionRefusal) Error() string { return e.cause.Error() }
func (e *retryAdmissionRefusal) Unwrap() error { return e.cause }

// endRefusedRetry transfers the original receipt to the run-ending boundary.
// A stop marker may be observed by the guard before the execution context is
// cancelled; it must still take precedence over ordinary failure.
func (p *Pipeline) endRefusedRetry(ctx context.Context, runID int64, refusal *retryAdmissionRefusal, reason string) {
	ctl := p.control(runID)
	if ctl != nil {
		ctl.mu.Lock()
		ctl.refusedRetry = refusal
		if errors.Is(refusal, state.ErrPlaylistSendStopped) {
			ctl.stopRequested = true
		}
		stopped := ctl.stopRequested
		ctl.mu.Unlock()
		if stopped {
			return
		}
	}
	if err := p.settleRefusedRetry(ctx, runID, refusal, false, reason); err != nil {
		p.logger.ErrorContext(ctx, "retry refusal settlement unavailable", "run_id", runID, "diagnostic", diag.Encode(withIntegrityCode(err), "unknown", "finalize_run"))
		p.interruptAuthorityRun(ctx, runID, reason)
	}
}

// settleRefusedRetry ends a run whose retry claim was refused at admission:
// removed work (ErrWorkRemoved), lost or changed ownership (ErrWorkNotOwned,
// ErrRetryOwnership), a Stop seen at the guard (ErrPlaylistSendStopped) or an
// integrity failure of the stored claim identity (ErrRetryProvenance).
//
// Settlement runs under the reserved writer, keyed by durable claim
// provenance. The refused video is protected when its work was removed or lost,
// so no sink or synthetic handoff recreates it and no other owner's row is
// touched; every other unfinished video keeps its ordinary retention or stop
// handoff. A marked Stop wins: it settles through settleStop, with the same
// protection. Without a Stop, retention comes before the failed state, and any
// retention failure leaves the run interrupted. An unusable claim identity is
// never settled around: with no Stop the run is only interrupted, and a Stop
// settlement fails inside the handoff and leaves the marker for recovery.
func (p *Pipeline) settleRefusedRetry(ctx context.Context, runID int64, refusal *retryAdmissionRefusal, markedStop bool, reason string) error {
	protect := refusal.protectedVideoID()
	if markedStop {
		return p.settleStopProtecting(ctx, runID, protect)
	}
	if errors.Is(refusal.cause, queue.ErrRetryProvenance) {
		return refusal.cause
	}
	p.retainThenFail(ctx, runID, reason, "", func(retainCtx context.Context, qs *queue.Store, tx *sql.Tx) error {
		return sweepStrandedWorkExcept(retainCtx, qs, tx, runID, reason, protect)
	})
	return nil
}

// protectedVideoID is the video whose rows a refusal's settlement must not
// write: the one whose claimed work was removed, lost or conflicts. A Stop or
// an unusable claim identity protects nothing here.
func (e *retryAdmissionRefusal) protectedVideoID() int64 {
	switch {
	case errors.Is(e.cause, state.ErrPlaylistSendStopped), errors.Is(e.cause, queue.ErrRetryProvenance):
		return 0
	}
	return e.receipt.VideoID
}

// withIntegrityCode gives the failure that ended a retry run its closed
// diagnostic code, so the stored reason and the logs do not read unknown_error:
// retry_claim_unverifiable for a missing or unusable stored claim identity, and
// retry_work_removed when the person removed the work the run had claimed.
// Other errors pass through unchanged. The error identity is kept: errors.Is
// still sees the typed cause, so the 409 handler and the settlement still work.
func withIntegrityCode(err error) error {
	switch {
	case errors.Is(err, queue.ErrRetryProvenance):
		return diag.WithCode(err, "retry_claim_unverifiable")
	case errors.Is(err, queue.ErrWorkRemoved):
		return diag.WithCode(err, "retry_work_removed")
	}
	return err
}

// StartupFailureDiagnostic is the closed diagnostic for a failed startup
// recovery: stop_settlement_blocked when a marked Stop could not be settled,
// otherwise the error's own classification.
func StartupFailureDiagnostic(err error) string {
	var ce *startupCheckError
	if errors.As(err, &ce) && ce.Check == "stop_settlement" {
		return diag.Facts{Phase: "unknown", Operation: "finalize_run", Code: "stop_settlement_blocked"}.String()
	}
	return diag.Encode(withIntegrityCode(err), "unknown", "load_run")
}

// stopSettlementAttrs are the log attributes of a marked Stop that could not
// be settled: the closed stop_settlement_blocked code and, when the cause is an
// unusable claim identity, its own code.
func stopSettlementAttrs(err error) []any {
	attrs := []any{"diagnostic", diag.Facts{Phase: "unknown", Operation: "finalize_run", Code: "stop_settlement_blocked"}.String()}
	if cause := withIntegrityCode(err); cause != err {
		attrs = append(attrs, "cause_diagnostic", diag.Encode(cause, "unknown", "finalize_run"))
	}
	return attrs
}

func isRetryAdmissionRefusal(err error) bool {
	return errors.Is(err, queue.ErrWorkRemoved) ||
		errors.Is(err, queue.ErrWorkNotOwned) ||
		errors.Is(err, queue.ErrRetryOwnership) ||
		errors.Is(err, queue.ErrRetryProvenance) ||
		errors.Is(err, state.ErrPlaylistSendStopped)
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
		return fmt.Errorf("retry video %d missing exact receipt: %w", videoID, queue.ErrWorkNotOwned)
	}
	// BR-039 / ARS-006 §5: a final video is never processed. Settle the claim
	// and return before any evaluation or send.
	class, outcome, err := state.ClassifyVideoTx(ctx, p.db, videoID)
	if err != nil {
		return err
	}
	if class == state.VideoClassFinal {
		return p.settleFinalRetryVideo(ctx, qs, receipt, outcome)
	}
	v, err := p.stateStore.GetVideo(ctx, videoID)
	if err != nil {
		return err
	}
	var workErr error
	switch class {
	case state.VideoClassPartlyDone:
		// BR-039 / ARS-006 §7: never re-evaluated. Only the missing additions
		// are sent, under the original run's decision.
		workErr = p.continueOriginalDecision(ctx, runID, v)
	default:
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
	}
	// The retained message carries the vetted bounded summary, not the raw
	// chain (ARD-001); only bounded auxiliary facts reach the log below.
	message := diag.Reason(workErr, "retry failed")
	var accountingErr *postCommitAccountingError
	if isRetryAdmissionRefusal(workErr) {
		var refusal *retryAdmissionRefusal
		if errors.As(workErr, &refusal) {
			return workErr
		}
		return &retryAdmissionRefusal{receipt: receipt, cause: workErr}
	}
	if errors.As(workErr, &accountingErr) {
		return workErr
	}
	var writerFault *admissionWriterFault
	if errors.As(workErr, &writerFault) {
		// A local storage fault at final admission stops the run. The original
		// receipt stays live until the run-level retention sweep; this video
		// takes neither FinishRetryFailureTx nor an Errors entry.
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
		if outcome != state.FinalOutcomeAccepted && outcome != state.FinalOutcomeRejected && outcome != state.FinalOutcomeExcluded && outcome != state.FinalOutcomeUnavailableOnYouTube {
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

// continueOriginalDecision finishes a partly done video under the decision of
// the run that made its first production success (ARS-006 §7.2). It sends only
// the targets still missing. If the original decision cannot be identified or
// read, nothing is sent and the returned error carries the registered
// original_decision_unavailable code, so processRetryVideo's failure path
// retains the video in Errors with it.
func (p *Pipeline) continueOriginalDecision(ctx context.Context, runID int64, v *state.Video) error {
	matched, decisionRunID, err := p.originalDecision(ctx, v.ID)
	if errors.Is(err, state.ErrOriginalDecisionUnavailable) {
		p.logger.WarnContext(ctx, "original decision unavailable; nothing sent", "run_id", runID, "video_id", v.ID, "error", err)
		return diag.At(diag.WithCode(err, "original_decision_unavailable"), "action_execution", "load_run")
	}
	if err != nil {
		return err
	}
	_, err = p.executeDecision(ctx, runID, v, decisionRunID, matched)
	return err
}

// originalDecision resolves the single run holding all of a partly done
// video's production successes, checks that its snapshot is readable and that
// every matched route still has a playlist_insert action with a target, and
// returns its matched decisions.
func (p *Pipeline) originalDecision(ctx context.Context, videoID int64) ([]RouteMatch, int64, error) {
	od, err := state.OriginalDecisionTx(ctx, p.db, videoID)
	if err != nil {
		return nil, 0, err
	}
	routes, err := decisionAuthority(ctx, p.db, p.configStore, od.RunID)
	if err != nil {
		return nil, 0, err
	}
	matched := make([]RouteMatch, 0, len(od.Matched))
	for _, m := range od.Matched {
		r := findRoute(routes, m.RouteID)
		if r == nil || !hasInsertTarget(r) {
			return nil, 0, fmt.Errorf("%w: route %d of run %d has no playlist action", state.ErrOriginalDecisionUnavailable, m.RouteID, od.RunID)
		}
		matched = append(matched, RouteMatch{RouteDecisionID: m.RouteDecisionID, RouteID: m.RouteID})
	}
	return matched, od.RunID, nil
}

func hasInsertTarget(r *router.Route) bool {
	for _, a := range r.Actions {
		if a.ActionType == "playlist_insert" && a.TargetPlaylistID != "" {
			return true
		}
	}
	return false
}

// settleFinalRetryVideo settles a claimed retry whose video is final. When this
// run itself made the video final and was interrupted before acknowledging
// (M2), the claim is acknowledged as completed. Otherwise another run made it
// final, and the owned rows are closed with closed_reason='final_outcome'.
// Neither path evaluates, writes a decision or outcome, or sends. Rows an
// operator removed meanwhile leave nothing to settle.
func (p *Pipeline) settleFinalRetryVideo(ctx context.Context, qs *queue.Store, receipt queue.RetryGroupReceipt, outcome state.VideoFinalOutcome) error {
	return qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var own sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`,
			receipt.ReceivingRunID, receipt.VideoID).Scan(&own)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read run %d outcome of video %d: %w", receipt.ReceivingRunID, receipt.VideoID, err)
		}
		if own.Valid && own.String == string(outcome) {
			owned, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt)
			if errors.Is(err, queue.ErrWorkRemoved) {
				return nil
			}
			if err != nil {
				return err
			}
			return acknowledgeRetryReceiptTx(ctx, tx, receipt, owned)
		}
		if _, err := qs.CloseRetryReceiptFinalTx(ctx, tx, receipt); err != nil && !errors.Is(err, queue.ErrWorkRemoved) {
			return err
		}
		return nil
	})
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

// Retry membership is durable even when release clears retry_run_id. Foreign
// members are handled only through a live receipt, never the ordinary phases.
func (p *Pipeline) withoutRetryVideos(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	out := make([]*state.Video, 0, len(videos))
	for _, v := range videos {
		var retry bool
		if err := p.db.QueryRowContext(ctx, `SELECT
		 EXISTS(SELECT 1 FROM deferred_item WHERE video_id=?1 AND retry_run_id=?2
		        UNION ALL SELECT 1 FROM error_entry WHERE video_id=?1 AND retry_run_id=?2)
		 OR EXISTS(SELECT 1 FROM run_video_outcome o JOIN video v ON v.id=o.video_id
		           WHERE o.video_id=?1 AND o.run_id=?2
		             AND (o.retry_claim_provenance IS NOT NULL OR v.discovered_in_run_id IS NOT ?2))`, v.ID, runID).Scan(&retry); err != nil {
			return nil, err
		}
		if !retry {
			out = append(out, v)
		}
	}
	return out, nil
}
