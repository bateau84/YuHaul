package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// The receiving precondition of run-initiation ARS-005 section 6.2: before an
// open claimed video is evaluated its route inputs are made current (or the
// receipt is settled without evaluating), under the run's effective dispatch
// stop (section 6.1.3).

// errInputsUnavailable is returned by the evaluation readers when a route
// input the evaluation needs is not locally available. It is never a
// substitute-an-empty-value result.
var errInputsUnavailable = errors.New("pipeline: evaluation inputs unavailable")

// requireInputsKey marks a context whose evaluation may not run on degraded
// metadata. Its value is the metadata.InputRequirement of the video.
type requireInputsKey struct{}

// evaluationFetcher is the metadata manager's readiness seam. The receiving
// precondition and its backstop are active only when Pipeline.meta provides it.
type evaluationFetcher interface {
	FetchForEvaluation(context.Context, auth.AuthorizationBinding, []metadata.InputRequirement) (metadata.FetchOutcome, error)
}

// inputRequirement is what the enabled routes of the run read from the
// metadata of one video. Title is always required: duplicate detection reads it.
func inputRequirement(routes []router.Route, videoID string) metadata.InputRequirement {
	req := metadata.InputRequirement{VideoID: videoID, Title: true}
	for _, r := range routes {
		if !r.IsEnabled {
			continue
		}
		for _, c := range r.Conditions {
			switch c.Field {
			case router.ConditionFieldChannelName:
				req.ChannelName = true
			case router.ConditionFieldDescription:
				req.Description = true
			case router.ConditionFieldDurationSeconds:
				req.Duration = true
			}
		}
		for _, ex := range r.Exclusions {
			if ex.ExclusionType == router.ExclusionTypeDurationMax || ex.ExclusionType == router.ExclusionTypeDurationMin {
				req.Duration = true
			}
		}
	}
	return req
}

// requiredInputs returns the readiness requirement carried by ctx.
func requiredInputs(ctx context.Context) (metadata.InputRequirement, bool) {
	req, ok := ctx.Value(requireInputsKey{}).(metadata.InputRequirement)
	return req, ok
}

// inputsSupplied reports whether m supplies every input req needs.
func inputsSupplied(m *metadata.TransientMetadata, req metadata.InputRequirement) bool {
	return m != nil &&
		(!req.Title || m.Title != nil) &&
		(!req.ChannelName || m.ChannelName != nil) &&
		(!req.Description || m.Description != nil) &&
		(!req.Duration || m.DurationSeconds != nil)
}

// evaluationMetadata loads the display metadata of v for an evaluation reader.
// Without a readiness requirement in ctx it keeps the existing degraded
// behavior and returns (nil, nil): the caller substitutes its own default.
// With one (the readiness backstop), unavailable or incomplete inputs return
// errInputsUnavailable instead of an empty substitute.
func (p *Pipeline) evaluationMetadata(ctx context.Context, v *state.Video) (*metadata.TransientMetadata, error) {
	req, required := requiredInputs(ctx)
	if !required {
		return nil, nil
	}
	m, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
	if err != nil || !inputsSupplied(m, req) {
		if err != nil && ctx.Err() != nil {
			return nil, err
		}
		return nil, errInputsUnavailable
	}
	return m, nil
}

// runStop returns the run's effective dispatch stop. A read error is writer
// W3: it is latched and returned as a run-stopping accounting fault.
func (p *Pipeline) runStop(ctx context.Context, qs *queue.Store, runID int64) (quota.DispatchStop, error) {
	if p.ledger == nil {
		return quota.StopNone, nil
	}
	stop, err := p.ledger.RunDispatchStop(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stop, ctxErr
		}
		return quota.StopAccountingOrIntegrity, p.failClosed(ctx, qs, runID, err)
	}
	return stop, nil
}

// failClosed is writer W3: no further run-owned provider request in this
// execution (latch first), the stored level on a best-effort basis (the store
// may be what failed), and a run-stopping error that carries
// auth.ErrQuotaAccounting.
func (p *Pipeline) failClosed(ctx context.Context, qs *queue.Store, runID int64, cause error) error {
	quota.LatchRunStop(ctx, quota.StopAccountingOrIntegrity)
	if p.isProductionRunID(ctx, runID) {
		if err := p.raiseStoredStop(context.WithoutCancel(ctx), qs, runID, quota.StopAccountingOrIntegrity); err != nil {
			p.logger.ErrorContext(ctx, "run dispatch stop not stored; latched for this execution", "run_id", runID,
				"diagnostic", diag.Encode(err, "unknown", "account_quota"))
		}
	}
	if !errors.Is(cause, auth.ErrQuotaAccounting) {
		cause = errors.Join(auth.ErrQuotaAccounting, cause)
	}
	return &runStoppingError{cause: cause}
}

// failClosedAtLifecycle is writer W3 for a metadata lifecycle fault. The
// diagnostic is classified from the accounting cause, so a registered SQLite
// code of a failed ledger write is kept and a provider result of the same read
// is not reported for an accounting stop. The cause carries its own phase and
// operation, so the stored reason stays inside the closed diagnostic
// vocabulary. When the retry store itself cannot be built the latch still
// holds the stop for this execution.
func (p *Pipeline) failClosedAtLifecycle(ctx context.Context, runID int64, cause error) error {
	cause = diag.At(metadata.AccountingCause(cause), "unknown", "account_quota")
	qs, err := p.retryStore()
	if err != nil {
		quota.LatchRunStop(ctx, quota.StopAccountingOrIntegrity)
		return &runStoppingError{cause: errors.Join(auth.ErrQuotaAccounting, cause, err)}
	}
	return p.failClosed(ctx, qs, runID, cause)
}

func (p *Pipeline) isProductionRunID(ctx context.Context, runID int64) bool {
	run, err := p.stateStore.GetRun(ctx, runID)
	return err == nil && p.isProductionRun(run.RunType)
}

// raiseStoredStop latches level for this execution and then stores it in its
// own writer transaction (ARS-005 section 6.1.3, "Order of every write").
func (p *Pipeline) raiseStoredStop(ctx context.Context, qs *queue.Store, runID int64, level quota.DispatchStop) error {
	quota.LatchRunStop(ctx, level)
	return qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		return p.stateStore.RaiseRunDispatchStopTx(ctx, tx, runID, level)
	})
}

// prepareReceiving is the §6.2 precondition for one pass. It returns, per open
// claimed video, the input status that decides its disposition. Final and
// partly-done videos are not touched here (BR-039). It returns nil when the
// metadata manager has no readiness seam.
//
// One FetchForEvaluation call covers the pass. The effective dispatch stop is
// resolved first (reader R4), before generic fetch suspension, a nil binding
// or the readiness backstop; the manager repeats the read before every chunk
// (reader R5), so a stop reached in the middle of the pass keeps the unsent
// suffix unsent.
func (p *Pipeline) prepareReceiving(ctx context.Context, qs *queue.Store, runID int64, receipts []queue.RetryGroupReceipt) (map[int64]metadata.FetchStatus, error) {
	fetcher, ok := p.meta.(evaluationFetcher)
	if !ok || len(receipts) == 0 {
		return nil, nil
	}
	var routes []router.Route
	var reqs []metadata.InputRequirement
	byText := map[string]int64{}
	for _, receipt := range receipts {
		// A receipt that is no longer valid (removed, lost, wrong owner) is
		// reported by the per-receipt loop, in order, as an admission refusal.
		// It takes no part in the fetch.
		if err := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
			_, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt)
			return err
		}); err != nil {
			continue
		}
		class, _, err := state.ClassifyVideoTx(ctx, p.db, receipt.VideoID)
		if err != nil {
			return nil, err
		}
		if class != state.VideoClassOpen {
			continue
		}
		if routes == nil {
			if routes, err = p.productionRoutes(ctx, runID); err != nil {
				return nil, fmt.Errorf("load routes for receiving precondition: %w", err)
			}
		}
		var text string
		if err := p.db.QueryRowContext(ctx, `SELECT video_id FROM video WHERE id = ?`, receipt.VideoID).Scan(&text); err != nil {
			return nil, fmt.Errorf("read video %d: %w", receipt.VideoID, err)
		}
		reqs = append(reqs, inputRequirement(routes, text))
		byText[text] = receipt.VideoID
	}
	if len(reqs) == 0 {
		return nil, nil
	}

	stop, err := p.runStop(ctx, qs, runID)
	if err != nil {
		return nil, err
	}
	if stop == quota.StopAccountingOrIntegrity {
		return nil, p.failClosed(ctx, qs, runID, auth.ErrQuotaAccounting)
	}
	var binding auth.AuthorizationBinding
	switch {
	case p.executionBinding != nil:
		binding = *p.executionBinding
	case stop == quota.StopNone:
		// A missing binding is a malformed per-call setup, not proof that
		// the user must reconnect.
		return nil, errors.New("pipeline: receiving precondition requires an authorization binding")
	}
	// With a stop in force the manager returns before it uses the binding.

	out, err := fetcher.FetchForEvaluation(ctx, binding, reqs)
	if err != nil {
		var fault *metadata.ReadFaultError
		if errors.Is(err, auth.ErrQuotaAccounting) || errors.As(err, &fault) {
			if fault != nil && fault.Facts.Authorization != "" {
				// An accounting fault controls the disposition, but the vetted
				// authorization cause it wraps is not lost.
				p.logger.WarnContext(ctx, "receiving read failed on authorization and accounting", "run_id", runID,
					"authorization_cause", string(fault.Facts.Authorization))
			}
			return nil, p.failClosed(ctx, qs, runID, err)
		}
		return nil, fmt.Errorf("fetch inputs for evaluation: %w", err)
	}
	ready := make(map[int64]metadata.FetchStatus, len(out.Statuses))
	pending, failed := 0, 0
	for text, status := range out.Statuses {
		id, ok := byText[text]
		if !ok {
			continue
		}
		ready[id] = status
		switch status {
		case metadata.FetchAuthorizationPending:
			pending++
			quota.LatchRunStop(ctx, quota.StopAuthorization)
		case metadata.FetchFailed:
			failed++
		}
	}
	if failed > 0 && out.Read != nil {
		// The read that suspended fetching failed on this call: its inputs are
		// released unchanged (applyReadiness). One bounded line per failure,
		// with the closed diagnostic and a count, never provider text or
		// per-video lines. A later pass that the manager keeps suspended
		// carries no Read, so the suspension is reported once, here.
		p.logger.WarnContext(ctx, "receiving metadata read failed; inputs released unchanged", "run_id", runID,
			"diagnostic", diag.Encode(out.Read.Err, "routing", "load_metadata"), "inputs", failed)
	}
	if pending > 0 && out.Read != nil && out.Read.Authorization != "" {
		// One bounded line per authorization failure of this pass: the vetted
		// cause and a count, never provider text or per-video lines. The stop
		// itself is stored by the first settlement (writer W1).
		p.logger.WarnContext(ctx, "run authorization stop", "run_id", runID,
			"authorization_cause", string(out.Read.Authorization), "inputs", pending)
	}
	return ready, nil
}

// settleOwnedReceiptsWithoutBinding runs when the execution could not take an
// authorization binding. If the run has a stored authorization stop, or the
// binding was refused for an authorization cause (OC-006), there is no safe
// authorized continuation: the run's open and final receipts, including live
// receipts restored after a restart, settle locally under that stop. Open
// receipts become authorization_pending with the stop stored in the same
// transaction as the first of them; final ones reconcile. No provider request
// is made. Partly-done receipts need a guarded send path that a missing
// binding cannot give, so they stay live for the run's own retention sweep.
//
// Any other bind failure (a local fault) or a quota or accounting stop is left
// to the caller's ordinary failure path. Errors are logged, never swallowed:
// the caller fails the run right after, and its sweep retains whatever is left.
func (p *Pipeline) settleOwnedReceiptsWithoutBinding(ctx context.Context, run *state.Run, resumeAdmitted bool, fresh []queue.RetryGroupReceipt, bindErr error) {
	runID := run.ID
	if p.ledger == nil || !p.isProductionRun(run.RunType) {
		return
	}
	fail := func(what string, err error) {
		p.logger.ErrorContext(ctx, "receiving settlement without a binding failed", "run_id", runID, "step", what,
			"diagnostic", diag.Encode(err, "routing", "load_metadata"))
	}
	qs, err := p.retryStore()
	if err != nil {
		fail("store", err)
		return
	}
	stop, err := p.runStop(ctx, qs, runID)
	if err != nil {
		fail("stop", err)
		return
	}
	cause := auth.ClassifyAuthorizationCause(bindErr)
	if stop != quota.StopAuthorization && (stop != quota.StopNone || cause == "") {
		return
	}
	if _, _, err := productionAuthority(ctx, p.db, p.configStore, runID); err != nil {
		return // the ordinary failure path reports the run; nothing is settled around it
	}
	receipts := fresh
	if receipts == nil && (run.Status == state.RunStatusInterrupted || resumeAdmitted) {
		if receipts, err = recoverOwnedReceipts(ctx, qs, runID); err != nil {
			fail("recover", err)
			return
		}
	}
	var settle []queue.RetryGroupReceipt
	for _, receipt := range receipts {
		class, _, err := state.ClassifyVideoTx(ctx, p.db, receipt.VideoID)
		if err != nil {
			fail("classify", err)
			return
		}
		if class != state.VideoClassPartlyDone {
			settle = append(settle, receipt)
		}
	}
	if len(settle) == 0 {
		return
	}
	if run.Status == state.RunStatusInterrupted {
		if err := p.stateStore.ResumeRun(ctx, runID); err != nil {
			fail("resume", err)
			return
		}
		run.Status = state.RunStatusRunning
	}
	quota.LatchRunStop(ctx, quota.StopAuthorization)
	source := string(cause)
	if source == "" {
		source = "stored_stop"
	}
	p.logger.WarnContext(ctx, "run authorization stop", "run_id", runID, "authorization_cause", source, "inputs", len(settle))
	if err := p.processDeferredItems(ctx, runID, settle, false); err != nil {
		fail("settle", err)
	}
}

// applyReadiness disposes of one open receipt whose inputs are not ready.
// handled is false for current and fetched inputs: the video is evaluated.
func (p *Pipeline) applyReadiness(ctx context.Context, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt, status metadata.FetchStatus) (handled bool, err error) {
	switch status {
	case metadata.FetchCurrent, metadata.FetchFetched:
		return false, nil
	case metadata.FetchQuotaRefused:
		return true, p.deferForInputs(ctx, qs, runID, receipt, queue.DeferredReasonQuotaExhausted)
	case metadata.FetchRateLimited:
		return true, p.deferForInputs(ctx, qs, runID, receipt, queue.DeferredReasonAPIRateLimited)
	case metadata.FetchAuthorizationPending:
		return true, p.settleAuthorizationPending(ctx, qs, runID, receipt)
	case metadata.FetchAbsent:
		return true, p.settleAbsent(ctx, qs, runID, receipt)
	case metadata.FetchFailed, metadata.FetchUnusable:
		return true, p.releaseUnchanged(ctx, qs, receipt)
	default:
		return true, fmt.Errorf("pipeline: unknown input status %q", status)
	}
}

func (p *Pipeline) releaseUnchanged(ctx context.Context, qs *queue.Store, receipt queue.RetryGroupReceipt) error {
	return qs.WithRetryTx(ctx, func(tx *sql.Tx) error { return qs.RequeueRetryReceiptTx(ctx, tx, receipt) })
}

// settleAbsent disposes of an open video whose id YouTube omitted from a
// successful response (ARS-005 §6.2, `absent` row). A video whose claim receipt
// holds a requeued_by_user row ends as unavailable_on_youtube (§6.3); any other
// retried video takes the ordinary video-unavailable retry failure. Both run in
// one WithRetryTx, so the receipt is revalidated before either writes.
func (p *Pipeline) settleAbsent(ctx context.Context, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt) error {
	var unavailable bool
	err := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		unavailable = false
		requeued, err := receiptRequeuedTx(ctx, tx, receipt)
		if err != nil {
			return err
		}
		if requeued {
			unavailable = true
			return p.commitUnavailableTx(ctx, tx, qs, runID, receipt)
		}
		return p.failUnavailableTx(ctx, tx, qs, runID, receipt)
	})
	if err == nil && unavailable {
		p.broadcastReceiptOutcome(runID, receipt.VideoID, state.FinalOutcomeUnavailableOnYouTube)
	}
	return err
}

// receiptRequeuedTx is requeued(v) of §6.2: one of the receipt's deferred rows
// was created by the user's re-queue. The item keeps that reason for its whole
// life, however many runs have claimed it.
func receiptRequeuedTx(ctx context.Context, tx *sql.Tx, receipt queue.RetryGroupReceipt) (bool, error) {
	if len(receipt.Deferred) == 0 {
		return false, nil
	}
	args := make([]any, 0, len(receipt.Deferred)+1)
	args = append(args, string(queue.DeferredReasonRequeuedByUser))
	placeholders := make([]byte, 0, 2*len(receipt.Deferred))
	for i, ref := range receipt.Deferred {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, ref.ID)
	}
	var requeued bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM deferred_item WHERE deferral_reason = ? AND id IN (`+string(placeholders)+`))`,
		args...).Scan(&requeued)
	if err != nil {
		return false, fmt.Errorf("read requeued state of video %d: %w", receipt.VideoID, err)
	}
	return requeued, nil
}

// failUnavailableTx is the ordinary video-unavailable retry failure. The
// special unavailable_on_youtube outcome belongs to commitUnavailableTx.
func (p *Pipeline) failUnavailableTx(ctx context.Context, tx *sql.Tx, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt) error {
	message := diag.Facts{Phase: "routing", Operation: "load_metadata", Code: "video_unavailable"}.String()
	if _, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt); err != nil {
		return err
	}
	if _, err := qs.FinishRetryFailureTx(ctx, tx, receipt, queue.RetryFailureFacts{Message: message, Detail: message}); err != nil {
		return fmt.Errorf("finish retry failure: %w", err)
	}
	return p.recordRetryVideoFailureTx(ctx, tx, runID, receipt.VideoID, message)
}

// commitUnavailableTx is ARS-005 §6.3, in the caller's single writer
// transaction: the re-queued video YouTube no longer returns ends the run as
// unavailable_on_youtube and its claimed rows are acknowledged. It writes no
// route decision, condition audit, action, send guard, Errors entry or new
// deferred row, and makes no provider call.
func (p *Pipeline) commitUnavailableTx(ctx context.Context, tx *sql.Tx, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt) error {
	owned, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt)
	if err != nil {
		return err
	}
	video, err := p.stateStore.GetVideoTx(ctx, tx, receipt.VideoID)
	if err != nil {
		return err
	}
	// A same-owner resumed claim may already be past not_yet_evaluated.
	if video.ProcessingState == state.VideoStateNotYetEvaluated {
		if _, err := p.machine.TransitionRunVideoStateTx(ctx, tx, runID, receipt.VideoID,
			state.VideoStateNotYetEvaluated, state.VideoStateEvaluating, state.FinalOutcomeNotYetEvaluated, nil, nil); err != nil {
			return err
		}
		video.ProcessingState = state.VideoStateEvaluating
	}
	if _, err := p.machine.TransitionRunVideoStateTx(ctx, tx, runID, receipt.VideoID,
		video.ProcessingState, state.VideoStateEvaluated, state.FinalOutcomeUnavailableOnYouTube, nil, nil); err != nil {
		return err
	}
	return acknowledgeRetryReceiptTx(ctx, tx, receipt, owned)
}

// settleAuthorizationPending settles an open receipt under an authorization
// stop: the exact owned rows return to pending with the original reason and
// age, and the run outcome is deferred with authorization_pending. The
// authorization stop is stored in the same transaction (writer W1). Raising it
// is idempotent, so every settlement under the stop repeats it harmlessly.
func (p *Pipeline) settleAuthorizationPending(ctx context.Context, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt) error {
	quota.LatchRunStop(ctx, quota.StopAuthorization)
	err := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		if err := p.stateStore.RaiseRunDispatchStopTx(ctx, tx, runID, quota.StopAuthorization); err != nil {
			return err
		}
		return p.deferForInputsTx(ctx, tx, qs, runID, receipt, queue.DeferredReasonAuthorizationPending)
	})
	if errors.Is(err, queue.ErrWorkRemoved) {
		// The settlement rolled back, and with it the stored stop. The stop is
		// still a fact about the run: store it on its own.
		if raiseErr := p.raiseStoredStop(ctx, qs, runID, quota.StopAuthorization); raiseErr != nil {
			return errors.Join(err, raiseErr)
		}
	}
	if err == nil {
		p.broadcastReceiptDeferred(runID, receipt.VideoID)
	}
	return err
}

func (p *Pipeline) deferForInputs(ctx context.Context, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt, reason queue.DeferredReason) error {
	err := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		return p.deferForInputsTx(ctx, tx, qs, runID, receipt, reason)
	})
	if err == nil {
		p.broadcastReceiptDeferred(runID, receipt.VideoID)
	}
	return err
}

func (p *Pipeline) broadcastReceiptDeferred(runID, videoID int64) {
	p.broadcastReceiptOutcome(runID, videoID, state.FinalOutcomeDeferred)
}

func (p *Pipeline) broadcastReceiptOutcome(runID, videoID int64, outcome state.VideoFinalOutcome) {
	if v, err := p.stateStore.GetVideo(context.Background(), videoID); err == nil {
		p.broadcastVideo(runID, v, outcome, false)
	}
}

// deferForInputsTx is §6.2's deferRetryForInputsTx, in the caller's single
// writer transaction. The item keeps its original deferral_reason; the run
// outcome records reason. Nothing is evaluated, sent or attempted.
func (p *Pipeline) deferForInputsTx(ctx context.Context, tx *sql.Tx, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt, reason queue.DeferredReason) error {
	owned, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt)
	if err != nil {
		return err
	}
	video, err := p.stateStore.GetVideoTx(ctx, tx, receipt.VideoID)
	if err != nil {
		return err
	}
	reasonText := string(reason)
	// A same-owner resumed claim that is already past not_yet_evaluated uses
	// its current state on an allowed edge.
	if video.ProcessingState == state.VideoStateNotYetEvaluated {
		if _, err := p.machine.TransitionRunVideoStateTx(ctx, tx, runID, receipt.VideoID,
			state.VideoStateNotYetEvaluated, state.VideoStateEvaluating, state.FinalOutcomeNotYetEvaluated, nil, nil); err != nil {
			return err
		}
		video.ProcessingState = state.VideoStateEvaluating
	}
	if video.ProcessingState == state.VideoStateEvaluating {
		if _, err := p.machine.TransitionRunVideoStateTx(ctx, tx, runID, receipt.VideoID,
			state.VideoStateEvaluating, state.VideoStateDeferred, state.FinalOutcomeDeferred, &reasonText, nil); err != nil {
			return err
		}
	}
	if len(owned.Deferred) == 0 {
		// An Errors-only receipt: establish the pending sink before the Errors
		// rows are resolved. Its origin is the first owned Errors row's run.
		origin := runID
		if len(owned.Errors) > 0 {
			origin = owned.Errors[0].OriginRunID
		}
		if _, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{
			VideoID: receipt.VideoID, RunID: origin, Reason: reason,
			Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	return qs.AcknowledgeRetryDeferralReceiptTx(ctx, tx, receipt)
}

// settleInputsLost handles errInputsUnavailable from the readiness backstop:
// the effective stop first (an authorization stop wins over a generic input
// loss), then the unchanged release.
func (p *Pipeline) settleInputsLost(ctx context.Context, qs *queue.Store, runID int64, receipt queue.RetryGroupReceipt) error {
	stop, err := p.runStop(ctx, qs, runID)
	if err != nil {
		return err
	}
	switch stop {
	case quota.StopAuthorization:
		return p.settleAuthorizationPending(ctx, qs, runID, receipt)
	case quota.StopQuota:
		return p.deferForInputs(ctx, qs, runID, receipt, queue.DeferredReasonQuotaExhausted)
	case quota.StopAccountingOrIntegrity:
		return p.failClosed(ctx, qs, runID, auth.ErrQuotaAccounting)
	}
	return p.releaseUnchanged(ctx, qs, receipt)
}
