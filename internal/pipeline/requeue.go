package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/queue"
)

// The preview and the confirmation of "re-queue stranded videos" (ARS-005 §4,
// BR-037). Both are built on scanStrandedTx. The preview takes no lock and
// writes nothing. The confirmation takes the Discard locks, scans again, and
// writes one pending deferred item per stranded video in one transaction. Neither
// calls the provider or starts, resumes or continues a run.

// Closed reasons that make the action unavailable (BR-037 AC2). The pipeline
// reports the first two. It does not know about shutdown: the HTTP handler
// sets RequeueUnavailableShuttingDown, which overrides any pipeline value.
const (
	RequeueUnavailableStartupRecovery = "startup_recovery_active"
	RequeueUnavailableProductionRun   = "production_run_active"
	RequeueUnavailableShuttingDown    = "shutting_down"
)

// StrandedPreview is the counts the user sees before confirming (BR-037 AC4).
// When Available is false, the counts are still current, but a confirmation
// would be refused for UnavailableReason.
type StrandedPreview struct {
	StrandedReport
	Available         bool
	UnavailableReason string // "" | RequeueUnavailableStartupRecovery | RequeueUnavailableProductionRun
}

// RequeueResult is what one confirmation did (BR-037 AC5).
type RequeueResult struct {
	// Report is the scan the confirmation acted on, taken after the locks were
	// held. Report.Requeue is the number planned.
	Report StrandedReport
	// Requeued is the number of items written.
	Requeued int
	// SkippedConcurrently is planned minus written: planned videos that a
	// concurrent queue change took out between the scan and the write.
	SkippedConcurrently int
	// Expected is the count from the confirmed preview, or nil.
	Expected *int
}

// ErrRequeueNotApplied is returned when the confirmation could not read or
// write its state and the transaction did not commit: nothing changed.
var ErrRequeueNotApplied = errors.New("re-queue did not take effect")

// ErrRequeueOutcomeUnknown is returned when the commit failed and the result
// cannot be told. Repeating the confirmation is safe (BR-037 AC10).
var ErrRequeueOutcomeUnknown = errors.New("re-queue outcome could not be confirmed")

// PreviewStranded counts the stranded videos and every video that is not
// re-queued, and says whether a confirmation would be accepted now. It takes
// no lock, changes nothing and makes no provider call (BR-037 AC4).
func (p *Pipeline) PreviewStranded(ctx context.Context) (*StrandedPreview, error) {
	preview := &StrandedPreview{}
	if p.refuseWhileRecovering() != nil {
		preview.UnavailableReason = RequeueUnavailableStartupRecovery
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, p.previewFailed(ctx, err)
	}
	defer tx.Rollback()
	if preview.UnavailableReason == "" {
		active, err := productionRegistered(ctx, tx, p.registeredRunIDs())
		if err != nil {
			return nil, p.previewFailed(ctx, err)
		}
		if active {
			preview.UnavailableReason = RequeueUnavailableProductionRun
		}
	}
	report, _, err := scanStrandedTx(ctx, tx)
	if err != nil {
		return nil, p.previewFailed(ctx, err)
	}
	preview.StrandedReport = report
	preview.Available = preview.UnavailableReason == ""
	return preview, nil
}

func (p *Pipeline) previewFailed(ctx context.Context, err error) error {
	p.logger.ErrorContext(ctx, "stranded videos preview failed", "diagnostic", diag.Encode(err, "unknown", "requeue_stranded"))
	return fmt.Errorf("stranded videos preview: %w", err)
}

// RequeueStranded re-queues the stranded videos as they are now (BR-037 AC3,
// AC5): it takes the locks, scans again and writes every planned item in one
// transaction, all or none (AC9). expected is the count of the confirmed
// preview and is only reported back. ctx must not be cancelled with the
// caller's request; the HTTP handler supplies a bounded, detached one.
//
// It is refused, with nothing changed, while startup recovery runs
// (ErrStartupRecoveryActive) or a production run is active
// (ErrProductionRunActive). Lock order: initiationMu, then productionMu, then
// runsMu, as for Run Now, Discard and Mark as ended.
func (p *Pipeline) RequeueStranded(ctx context.Context, expected *int) (*RequeueResult, error) {
	if err := p.refuseWhileRecovering(); err != nil {
		return nil, err
	}
	p.initiationMu.Lock()
	defer p.initiationMu.Unlock()
	if !p.productionMu.TryLock() {
		return nil, ErrProductionRunActive
	}
	defer p.productionMu.Unlock()

	qs, err := p.retryStore()
	if err != nil {
		return nil, p.requeueNotApplied(ctx, err)
	}

	// Phase A, outside the writer lock: the registered runs, then the scan.
	var report StrandedReport
	var plan []StrandedVideo
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, p.requeueNotApplied(ctx, err)
	}
	active, err := productionRegistered(ctx, tx, p.registeredRunIDs())
	if err == nil && !active {
		report, plan, err = scanStrandedTx(ctx, tx)
	}
	tx.Rollback()
	switch {
	case err != nil:
		return nil, p.requeueNotApplied(ctx, err)
	case active:
		return nil, ErrProductionRunActive
	}
	result := &RequeueResult{Report: report, Expected: expected}
	if len(plan) == 0 {
		p.logRequeued(ctx, result, 0)
		return result, nil
	}

	// Phase B, one write transaction. InsertRequeuedTx rechecks each planned
	// video under the writer lock, so a queue change after the scan only lowers
	// the count.
	items := make([]queue.RequeueItem, len(plan))
	for i, s := range plan {
		items[i] = queue.RequeueItem(s)
	}
	runTx := p.requeueTx
	if runTx == nil {
		runTx = qs.WithRetryTx
	}
	var inserted int
	err = runTx(ctx, func(tx *sql.Tx) error {
		n, err := qs.InsertRequeuedTx(ctx, tx, items, time.Now())
		inserted = n
		return err
	})
	if err != nil {
		if errors.Is(err, queue.ErrCommit) {
			p.logger.ErrorContext(ctx, "stranded videos requeue commit failed", "planned", len(plan), "diagnostic", diag.Encode(err, "unknown", "requeue_stranded"))
			return nil, ErrRequeueOutcomeUnknown
		}
		return nil, p.requeueNotApplied(ctx, err)
	}
	result.Requeued = inserted
	result.SkippedConcurrently = len(plan) - inserted
	p.logRequeued(ctx, result, len(plan))
	return result, nil
}

func (p *Pipeline) requeueNotApplied(ctx context.Context, err error) error {
	p.logger.ErrorContext(ctx, "stranded videos requeue not applied", "diagnostic", diag.Encode(err, "unknown", "requeue_stranded"))
	return ErrRequeueNotApplied
}

// logRequeued records the result with counts only, never video identifiers.
func (p *Pipeline) logRequeued(ctx context.Context, r *RequeueResult, planned int) {
	p.logger.InfoContext(ctx, "stranded videos requeued",
		"requeued", r.Requeued, "planned", planned, "skipped_concurrently", r.SkippedConcurrently,
		"already_queued", r.Report.AlreadyQueued.Videos, "removed_by_user", r.Report.RemovedByUser,
		"blocked", r.Report.Blocked.Videos, "not_ended", r.Report.NotEnded.Videos,
		"no_origin_run", r.Report.NoOriginRun, "retained_without_queue_item", r.Report.RetainedWithoutQueueItem.Videos)
}
