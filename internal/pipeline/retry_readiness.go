package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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

// receiptGuardedError is the disposition of one receipt whose video holds an
// unresolved send guard: a mutation may already have been issued, so the
// receipt keeps its established disposition and is neither deferred nor
// released here. The veto belongs to that receipt alone. processDeferredItems
// still settles the pass's other receipts, then returns this error so the run
// ends and its retention sweep keeps the guarded work live.
type receiptGuardedError struct{ videoID int64 }

func (e *receiptGuardedError) Error() string {
	return fmt.Sprintf("receipt of video %d keeps its disposition: unresolved send guard", e.videoID)
}

// Unwrap gives the closed mutation_application_uncertain diagnostic.
func (e *receiptGuardedError) Unwrap() error { return errGuardBlocked() }

// withStorageFault is a receipt-local disposition (a claim-integrity refusal or
// a guard veto) that happened beside a real storage fault, for example the
// authorization stop that could not be stored after the settlement rolled back.
// Its chain exposes only the fault, so a sentinel test on the chain never takes
// it for the pure disposition. The disposition stays reachable for protected
// retention (see refusalBesideFault).
//
// This is the one rule of the no-binding settlement path: a pure refusal, guard
// veto or cancellation keeps its ordinary ending; any accompanying storage
// fault governs the ending (writer W3, ARS-005 section 6.1.3).
type withStorageFault struct{ disposition, fault error }

func (e *withStorageFault) Error() string { return "storage fault beside a receipt disposition" }
func (e *withStorageFault) Unwrap() error { return e.fault }

// accompanyingFault returns the storage fault that governs err, or nil when err
// has none.
func accompanyingFault(err error) error {
	var refusal *retryAdmissionRefusal
	if errors.As(err, &refusal) && refusal.fault != nil {
		return refusal.fault
	}
	var beside *withStorageFault
	if errors.As(err, &beside) {
		return beside.fault
	}
	return nil
}

// refusalBesideFault turns a refusal that happened beside a storage fault into
// the refusal of receipt that carries the fault. It reports false for any other
// error, including a guard veto beside a fault (nothing to protect: the guarded
// work stays live) and a pure refusal.
func refusalBesideFault(receipt queue.RetryGroupReceipt, err error) (error, bool) {
	var beside *withStorageFault
	if errors.As(err, &beside) && isRetryAdmissionRefusal(beside.disposition) {
		return &retryAdmissionRefusal{receipt: receipt, cause: beside.disposition, fault: beside.fault}, true
	}
	return nil, false
}

// isPureGuardVeto reports whether err is a receipt-local guard veto and nothing
// else. It follows single wraps only: an error joined with another fault (a
// failed stop write, a rollback failure) is not pure, so the pass never
// continues past a storage fault because a veto happens to be inside it.
func isPureGuardVeto(err error) bool {
	for err != nil {
		if _, ok := err.(*receiptGuardedError); ok {
			return true
		}
		next, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = next.Unwrap()
	}
	return false
}

// governingSettlementFault is the fault of a no-binding settlement that must
// end the run instead of being logged and dropped: a failed read or write of
// local state, alone or beside a refusal or guard veto (withStorageFault).
// Not governing: a cancelled execution (the run is resumable; a fault that
// only the cancellation caused cannot be told apart from it), a pure
// claim-integrity refusal (removed, lost or unverifiable work keeps its own
// protected retention) and a pure guard veto (the guarded receipt keeps its
// disposition).
func governingSettlementFault(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if accompanyingFault(err) != nil {
		return err
	}
	if ctx.Err() != nil || isRetryAdmissionRefusal(err) || isPureGuardVeto(err) {
		return nil
	}
	return err
}

// governingStorageFault reports whether err, returned by a receiving pass that
// held a valid binding, is a real storage fault that must end the run through
// writer W3 (ARS-005 section 6.1.3: a fault that prevents reliable settlement).
// It is the bound-execution counterpart of governingSettlementFault: here an
// ordinary ending already reports the fault, so only a classified SQLite fault
// (alone or beside a refusal or veto) is routed, and only the stored integrity
// stop is added. Not routed: a run already ended by W3, a cancelled execution,
// a pure refusal, a pure guard veto, a user Stop, and any non-storage error
// (provider, quota and accounting outcomes keep their own handling).
func governingStorageFault(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var stopping *runStoppingError
	if errors.As(err, &stopping) {
		return false // W3 already ran
	}
	if accompanyingFault(err) != nil {
		return true
	}
	if isRetryAdmissionRefusal(err) || isPureGuardVeto(err) || errors.Is(err, state.ErrPlaylistSendStopped) {
		return false
	}
	return diag.Classify(err, "unknown", "unknown").Code == "sqlite_error"
}

// failClosedAtReceiving is the one W3 boundary of the receiving settlement
// path: latch, a best-effort stored accounting_or_integrity stop, and the
// classified fault (with any protected refusal it carries) as the run's
// reason. A fault that already went through W3 is returned unchanged.
func (p *Pipeline) failClosedAtReceiving(ctx context.Context, runID int64, fault error) error {
	var stopping *runStoppingError
	if errors.As(fault, &stopping) {
		return fault
	}
	return p.failClosedAtLifecycle(ctx, runID, fault)
}

// receivingEnding is the error a run ends with after a receiving pass failed
// with err: err itself, or, for a governing storage fault, err through W3.
func (p *Pipeline) receivingEnding(ctx context.Context, runID int64, err error) error {
	if governingStorageFault(ctx, err) {
		return p.failClosedAtReceiving(ctx, runID, err)
	}
	return err
}

// sendBlockedKey marks a context whose execution holds no authorization
// binding under an authorization stop (settleOwnedReceiptsWithoutBinding).
// Evaluation is local and still runs, but a video with a matched decision
// stops before its actions: executeDecision returns errSendBlocked and
// processRetryVideo settles the exact receipt as authorization_pending.
type sendBlockedKey struct{}

// errSendBlocked is returned by executeDecision before any lifecycle write
// when the context is send-blocked and the video has matched decisions.
var errSendBlocked = errors.New("pipeline: send blocked by authorization stop")

// requiredInputs returns the readiness requirement carried by ctx.
func requiredInputs(ctx context.Context) (metadata.InputRequirement, bool) {
	req, ok := ctx.Value(requireInputsKey{}).(metadata.InputRequirement)
	return req, ok
}

// evaluationFinished reports whether run's evaluation of v has completed. A
// version-1 run moves v out of not_yet_evaluated/evaluating only after every
// route of the ordered list is decided, so a persisted prefix of decisions is
// never mistaken for a completed one.
func evaluationFinished(run *state.Run, v *state.Video) bool {
	return run.ActionAuthorityVersion == 1 &&
		v.ProcessingState != state.VideoStateNotYetEvaluated &&
		v.ProcessingState != state.VideoStateEvaluating
}

// receivingDecisionSaved reports whether the receiving run already holds a
// complete decision for the open video: the claim reset it to not_yet_evaluated,
// so a finished evaluation is this run's own. Such a receipt is not hydrated,
// rerouted or re-detected (ARS-005 section 6.2). Its saved decision and
// outstanding action are continued from local state, so no metadata answer,
// however it turns out, may reopen, release, fail or re-decide it. An
// unfinished evaluation is not saved and still needs real inputs.
func (p *Pipeline) receivingDecisionSaved(ctx context.Context, runID, videoID int64) (bool, error) {
	run, err := p.stateStore.GetRun(ctx, runID)
	if err != nil {
		return false, fmt.Errorf("read receiving run %d: %w", runID, err)
	}
	v, err := p.stateStore.GetVideo(ctx, videoID)
	if err != nil {
		return false, fmt.Errorf("read video %d: %w", videoID, err)
	}
	return evaluationFinished(run, v), nil
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
	saved := map[string]bool{} // open videos whose receiving run holds a complete decision
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
		isSaved, err := p.receivingDecisionSaved(ctx, runID, receipt.VideoID)
		if err != nil {
			return nil, err
		}
		saved[text] = isSaved
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
	if stop == quota.StopNone {
		// A saved decision needs no input read. With a stop in force it still
		// takes the stop's disposition below, which makes no provider request.
		reqs = slices.DeleteFunc(reqs, func(r metadata.InputRequirement) bool { return saved[r.VideoID] })
		if len(reqs) == 0 {
			return nil, nil
		}
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
// to the caller's ordinary failure path, and so are claim-integrity refusals and
// a pure guard veto (see governingSettlementFault). A failed read or write of
// local state while settling is not left there: it is returned, and the caller
// ends the run through writer W3 with that fault (ARS-005 section 6.1.3), not
// with the binding error. Every error is also logged.
func (p *Pipeline) settleOwnedReceiptsWithoutBinding(ctx context.Context, run *state.Run, resumeAdmitted bool, fresh []queue.RetryGroupReceipt, bindErr error) error {
	runID := run.ID
	if p.ledger == nil || !p.isProductionRun(run.RunType) {
		return nil
	}
	fail := func(what string, err error) error {
		p.logger.ErrorContext(ctx, "receiving settlement without a binding failed", "run_id", runID, "step", what,
			"diagnostic", diag.Encode(err, "routing", "load_metadata"))
		return governingSettlementFault(ctx, err)
	}
	qs, err := p.retryStore()
	if err != nil {
		return fail("store", err)
	}
	stop, err := p.runStop(ctx, qs, runID)
	if err != nil {
		return fail("stop", err)
	}
	cause := auth.ClassifyAuthorizationCause(bindErr)
	if stop != quota.StopAuthorization && (stop != quota.StopNone || cause == "") {
		return nil
	}
	if _, _, err := productionAuthority(ctx, p.db, p.configStore, runID); err != nil {
		return nil // the ordinary failure path reports the run; nothing is settled around it
	}
	receipts := fresh
	if receipts == nil && (run.Status == state.RunStatusInterrupted || resumeAdmitted) {
		if receipts, err = recoverOwnedReceipts(ctx, qs, runID); err != nil {
			return fail("recover", err)
		}
	}
	var settle []queue.RetryGroupReceipt
	for _, receipt := range receipts {
		class, _, err := state.ClassifyVideoTx(ctx, p.db, receipt.VideoID)
		if err != nil {
			return fail("classify", err)
		}
		if class != state.VideoClassPartlyDone {
			settle = append(settle, receipt)
		}
	}
	if len(settle) == 0 {
		return nil
	}
	if run.Status == state.RunStatusInterrupted {
		if err := p.stateStore.ResumeRun(ctx, runID); err != nil {
			return fail("resume", err)
		}
		run.Status = state.RunStatusRunning
	}
	quota.LatchRunStop(ctx, quota.StopAuthorization)
	source := string(cause)
	if source == "" {
		source = "stored_stop"
	}
	p.logger.WarnContext(ctx, "run authorization stop", "run_id", runID, "authorization_cause", source, "inputs", len(settle))
	// Without a binding no action can be sent, so a matched video must not
	// reach the executor, which would fail it as an ordinary action failure.
	ctx = context.WithValue(ctx, sendBlockedKey{}, struct{}{})
	if err := p.processDeferredItems(ctx, runID, settle, false); err != nil {
		return fail("settle", err)
	}
	return nil
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
	if errors.Is(err, queue.ErrWorkRemoved) || isPureGuardVeto(err) {
		// The settlement rolled back, and with it the stored stop. The stop is
		// still a fact about the run: store it on its own. If that fails too,
		// the disposition (removal or veto) is kept beside the real fault, which
		// governs the ending.
		if raiseErr := p.raiseStoredStop(ctx, qs, runID, quota.StopAuthorization); raiseErr != nil {
			return &withStorageFault{disposition: err, fault: fmt.Errorf("store authorization stop: %w", raiseErr)}
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
// outcome records reason. Nothing is evaluated, sent or attempted. A video that
// already holds a saved decision goes evaluated or acted to deferred on the
// same edge, so its outcome is recorded too.
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
	switch video.ProcessingState {
	case state.VideoStateEvaluated, state.VideoStateActed:
		// A saved decision whose action cannot be sent keeps that decision and
		// its audit: only the lifecycle and the run outcome change. An
		// unresolved send guard means a mutation may already have been issued;
		// that disposition is never reinterpreted here, so the receipt stays
		// live for the run's retention sweep.
		var guarded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM playlist_send_guard WHERE video_id = ?)`, receipt.VideoID).Scan(&guarded); err != nil {
			return fmt.Errorf("read send guards of video %d: %w", receipt.VideoID, err)
		}
		if guarded {
			return &receiptGuardedError{videoID: receipt.VideoID}
		}
		fallthrough
	case state.VideoStateEvaluating:
		if _, err := p.machine.TransitionRunVideoStateTx(ctx, tx, runID, receipt.VideoID,
			video.ProcessingState, state.VideoStateDeferred, state.FinalOutcomeDeferred, &reasonText, nil); err != nil {
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
