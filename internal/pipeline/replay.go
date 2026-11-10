package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// ErrInvalidDateRange is returned (wrapped) by Replay and Backtest when the
// published-date range is invalid. It is returned before any run, snapshot or
// configuration lookup exists (run-initiation ADR-002).
var ErrInvalidDateRange = errors.New("invalid date range")

// checkPublishedRange re-validates a range handed to Replay/Backtest.
func checkPublishedRange(rng state.PublishedRange) error {
	if _, rerr := state.ParsePublishedRange(rng.From, rng.To); rerr != nil {
		return fmt.Errorf("%w: %s", ErrInvalidDateRange, rerr.Error())
	}
	return nil
}

// Replay creates a replay run and starts it. The caller must invoke
// ExecuteReplay in a goroutine. rng is persisted immutably on the run.
func (p *Pipeline) Replay(ctx context.Context, configVersionID *int64, rng state.PublishedRange) (*state.Run, error) {
	if err := checkPublishedRange(rng); err != nil {
		return nil, err
	}
	run := &state.Run{
		StartedAt:     time.Now().UTC(),
		RunType:       state.RunTypeReplay,
		Status:        state.RunStatusRunning,
		PublishedFrom: rng.From,
		PublishedTo:   rng.To,
	}

	cfgID, err := p.resolveReplayConfig(ctx, configVersionID)
	if err != nil {
		return nil, err
	}
	run.ConfigVersionID = cfgID

	id, err := p.stateStore.CreateRun(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("pipeline: create replay: %w", err)
	}
	run.ID = id

	p.registerRun(run.ID)
	p.broadcastEvent(run.ID, EventRunStarted, nil)
	return run, nil
}

func (p *Pipeline) resolveReplayConfig(ctx context.Context, configVersionID *int64) (*int64, error) {
	if configVersionID != nil {
		if p.configStore == nil {
			return nil, fmt.Errorf("pipeline: replay config version requires a config store")
		}
		if _, err := p.configStore.Get(ctx, *configVersionID); err != nil {
			return nil, fmt.Errorf("pipeline: replay config version %d: %w", *configVersionID, err)
		}
		return configVersionID, nil
	}
	if p.versioner != nil {
		return p.versioner.ActiveVersionID(ctx)
	}
	return nil, nil
}

// ExecuteReplay re-evaluates retained historical observations against the
// configuration selected for runID without mutating external playlists,
// production statistics, the duplicate-reference corpus, or video processing
// state.
func (p *Pipeline) ExecuteReplay(ctx context.Context, runID int64) {
	p.executeHistorical(ctx, runID, state.RunTypeReplay, "replay")
}

// Backtest creates a backtest run and starts it. The caller must invoke
// ExecuteBacktest in a goroutine.
//
// The proposed configuration is a public proposal (ADR-012): its route UUID
// identities are translated to internal integer keys — creating inactive
// identity anchors and registry mappings where required — in the same SQLite
// transaction that stores the immutable ConfigVersion snapshot and the run
// row.
func (p *Pipeline) Backtest(ctx context.Context, proposed *config.ProposalSnapshot, rng state.PublishedRange) (*state.Run, error) {
	if err := checkPublishedRange(rng); err != nil {
		return nil, err
	}
	if proposed == nil {
		return nil, fmt.Errorf("pipeline: backtest requires a proposed configuration")
	}

	run := &state.Run{
		StartedAt:     time.Now().UTC(),
		RunType:       state.RunTypeBacktest,
		Status:        state.RunStatusRunning,
		PublishedFrom: rng.From,
		PublishedTo:   rng.To,
	}

	if err := p.ingestProposalRun(ctx, "backtest proposed configuration", proposed, run); err != nil {
		return nil, err
	}

	p.registerRun(run.ID)
	p.broadcastEvent(run.ID, EventRunStarted, nil)
	return run, nil
}

// ExecuteBacktest re-evaluates retained historical observations against the
// proposed configuration selected for runID without mutating external or
// production state.
func (p *Pipeline) ExecuteBacktest(ctx context.Context, runID int64) {
	p.executeHistorical(ctx, runID, state.RunTypeBacktest, "backtest")
}

func (p *Pipeline) executeHistorical(ctx context.Context, runID int64, runType state.RunType, label string) {
	logger := p.logger.With("run_id", runID, "run_type", runType)
	logger.InfoContext(ctx, fmt.Sprintf("%s execute started", label))

	ctl := p.registerRun(runID)
	defer p.finishRun(ctx, runID)
	ctx, stopped := ctl.attach(ctx)
	if stopped {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("%s panic", label), "diagnostic", diag.Encode(diag.PanicCause(), "unknown", "unknown"))
			p.failHistoricalRun(ctx, runID, fmt.Sprintf("%s panic", label), diag.PanicCause())
		}
	}()

	run, err := p.stateStore.GetRun(ctx, runID)
	if err != nil {
		logger.ErrorContext(ctx, fmt.Sprintf("load %s failed", label), "diagnostic", diag.Encode(err, "unknown", "load_run"))
		p.failHistoricalRun(ctx, runID, fmt.Sprintf("load %s failed", label), err)
		return
	}

	routes, err := p.dryRunRoutes(ctx, run)
	if err != nil {
		p.failHistoricalRun(ctx, runID, fmt.Sprintf("load %s routes failed", label), err)
		return
	}

	// Without a range every retained video is selected. With one, only videos
	// whose locally held, still-eligible published date is in range are; the
	// selector reads local rows only and never calls YouTube (ADR-002).
	var videos []state.Video
	if rng := run.Range(); rng.IsSet() {
		videos, err = p.stateStore.ListRetainedVideosPublishedIn(ctx, rng, time.Now().UTC())
	} else {
		videos, err = p.stateStore.ListRetainedVideos(ctx)
	}
	if err != nil {
		p.failHistoricalRun(ctx, runID, fmt.Sprintf("list %s videos failed", label), err)
		return
	}
	// Capture the selector's established set before evaluation so a later
	// failure keeps the known selection and an empty selection is stored as
	// zero, not unknown (ARD-001).
	p.captureSelectedCount(ctx, runID, len(videos))

	p.setPhase(ctx, runID, PhaseRouting)
	p.broadcastPhase(runID, PhaseRouting)

	detector, err := p.detectorForRun(ctx, runID)
	if err != nil {
		p.failHistoricalRun(ctx, runID, "load duplicate configuration failed", err)
		return
	}
	for _, v := range videos {
		if err := p.evaluateHistoricalVideo(ctx, runID, &v, routes, label, detector); err != nil {
			p.failHistoricalRun(ctx, runID, fmt.Sprintf("%s video failed", label), err)
			return
		}
	}

	p.completeHistoricalRun(ctx, runID)
}

func (p *Pipeline) evaluateHistoricalVideo(ctx context.Context, runID int64, v *state.Video, routes []router.Route, label string, detector *duplicate.Detector) error {
	meta := &metadata.TransientMetadata{VideoID: v.VideoID}
	if p.meta != nil {
		fetched, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, fmt.Sprintf("%s using degraded metadata", label),
				"diagnostic", diag.Encode(err, "unknown", "load_metadata"))
		} else {
			meta = fetched
		}
	}

	title := ""
	if meta.Title != nil {
		title = *meta.Title
	}

	vc := videoContextFromMetadata(v.VideoID, v.ChannelID, meta)

	// Global exclusion gate: videos excluded by every enabled route are marked
	// excluded without evaluating route conditions.
	remaining, err := p.runExclusionGate(ctx, runID, []*state.Video{v}, routes, false)
	if err != nil {
		return fmt.Errorf("exclusion gate for video %s: %w", v.VideoID, err)
	}
	if len(remaining) == 0 {
		return nil
	}

	// Detection is read-only in simulations. A distinct match is supporting
	// information, never a reason to skip eligible routes or actions.
	if detector != nil {
		if _, err := detector.Detect(ctx, v.VideoID, title); err != nil {
			return fmt.Errorf("detect duplicate for %s: %w", v.VideoID, err)
		}
	}

	if _, err := p.router.EvaluateWithRoutes(ctx, runID, v.ID, vc, p.machine, routes); err != nil {
		return diag.At(fmt.Errorf("evaluate video %s: %w", v.VideoID, err), "routing", "evaluate_video")
	}

	decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
	if err != nil {
		return fmt.Errorf("load decisions for video %d: %w", v.ID, err)
	}

	matched := false
	notMatched := false
	for _, d := range decisions {
		if d.Decision == state.RouteDecisionNotMatched {
			notMatched = true
		}
		if d.Decision == state.RouteDecisionMatched {
			matched = true
			if err := p.simulateAction(ctx, runID, v.ID, &d, routes); err != nil {
				return err
			}
		}
	}

	outcome := state.FinalOutcomeRejected
	if matched {
		outcome = state.FinalOutcomeAccepted
	} else if !notMatched && hasExclusionDecision(decisions) {
		outcome = state.FinalOutcomeExcluded
	}
	p.broadcastVideo(runID, v, outcome, matched)
	return nil
}

func (p *Pipeline) completeHistoricalRun(ctx context.Context, runID int64) {
	if !p.mayFinalize(runID) {
		return
	}
	run, err := p.machine.CompleteHistoricalRun(ctx, runID)
	if err != nil {
		p.logger.ErrorContext(ctx, "complete historical run failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
		finalizeCtx, cancel := historicalFinalizationContext(ctx)
		defer cancel()
		run, err = p.machine.CompleteHistoricalRun(finalizeCtx, runID)
		if err != nil {
			p.failHistoricalRun(finalizeCtx, runID, "run completion failed", err)
			return
		}
	}
	if run == nil {
		return
	}

	p.broadcastEvent(runID, EventRunCompleted, payload(completedPayload{
		RunID:                 run.ID,
		Status:                string(run.Status),
		VideosDiscovered:      run.VideosDiscovered,
		VideosAccepted:        run.VideosAccepted,
		VideosRejected:        run.VideosRejected,
		VideosExcluded:        run.VideosExcluded,
		VideosDeferred:        run.VideosDeferred,
		VideosFailed:          run.VideosFailed,
		VideosNotYetEvaluated: run.VideosNotYetEvaluated,
		QuotaCost:             run.QuotaCost,
	}))
}

// failHistoricalRun finalizes a dry-run/replay/backtest run whose evaluation
// did not reach a normal terminal state. Per the accepted run-status rule
// (System Architecture §Run Recovery / ADR-002) the run ends terminal `failed`
// — never interrupted-then-completed — and only falls back to the resumable
// `interrupted` state when failure finalization itself cannot be persisted.
func (p *Pipeline) failHistoricalRun(ctx context.Context, runID int64, message string, err error) {
	if !p.mayFinalize(runID) {
		return
	}
	p.logger.ErrorContext(ctx, "historical run failed", "run_id", runID, "diagnostic", diag.Reason(err, message))
	// Persisted reason, logs and SSE payload all carry only classified facts
	// (ARD-001); the raw chain stays in process memory for control flow only.
	reason := diag.Reason(err, message)
	finalizeCtx, cancel := historicalFinalizationContext(ctx)
	defer cancel()
	if _, ferr := p.machine.FailHistoricalRun(finalizeCtx, runID, reason); ferr != nil {
		p.logger.ErrorContext(ctx, "fail historical run failed", "run_id", runID, "diagnostic", diag.Encode(ferr, "unknown", "finalize_run"))
		// The failed-status attempt may have exhausted its deadline. Give the
		// resumable fallback its own bounded bookkeeping window.
		interruptCtx, interruptCancel := historicalFinalizationContext(ctx)
		defer interruptCancel()
		if interruptErr := p.stateStore.InterruptRun(interruptCtx, runID, reason); interruptErr != nil {
			p.logger.ErrorContext(ctx, "interrupt historical run fallback failed", "run_id", runID, "diagnostic", diag.Encode(interruptErr, "unknown", "finalize_run"))
		}
	}
	p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: reason}))
}

// Historical finalization must survive cancellation of the execution request.
// Keep bookkeeping bounded while retaining context values for diagnostics.
func historicalFinalizationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}
