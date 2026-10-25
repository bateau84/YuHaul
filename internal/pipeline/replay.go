package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// Replay creates a replay run and starts it. The caller must invoke
// ExecuteReplay in a goroutine.
func (p *Pipeline) Replay(ctx context.Context, configVersionID *int64) (*state.Run, error) {
	run := &state.Run{
		StartedAt: time.Now().UTC(),
		RunType:   state.RunTypeReplay,
		Status:    state.RunStatusRunning,
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
func (p *Pipeline) Backtest(ctx context.Context, proposed *config.ProposalSnapshot) (*state.Run, error) {
	if proposed == nil {
		return nil, fmt.Errorf("pipeline: backtest requires a proposed configuration")
	}

	run := &state.Run{
		StartedAt: time.Now().UTC(),
		RunType:   state.RunTypeBacktest,
		Status:    state.RunStatusRunning,
	}

	if err := p.ingestProposalRun(ctx, "backtest proposed configuration", proposed, run); err != nil {
		return nil, err
	}

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

	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("%s panic", label), "recover", r)
			_ = p.stateStore.InterruptRun(ctx, runID, fmt.Sprintf("panic: %v", r))
			p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: fmt.Sprintf("panic: %v", r)}))
			p.completeHistoricalRun(ctx, runID)
		}
	}()

	run, err := p.stateStore.GetRun(ctx, runID)
	if err != nil {
		logger.ErrorContext(ctx, fmt.Sprintf("load %s failed", label), "error", err)
		return
	}

	routes, err := p.dryRunRoutes(ctx, run)
	if err != nil {
		p.failHistoricalRun(ctx, runID, fmt.Sprintf("load %s routes failed", label), err)
		return
	}

	videos, err := p.stateStore.ListRetainedVideos(ctx)
	if err != nil {
		p.failHistoricalRun(ctx, runID, fmt.Sprintf("list %s videos failed", label), err)
		return
	}

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
				"video_id", v.VideoID, "error", err)
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
		return fmt.Errorf("evaluate video %s: %w", v.VideoID, err)
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
	run, err := p.machine.CompleteHistoricalRun(ctx, runID)
	if err != nil {
		p.logger.ErrorContext(ctx, "complete historical run failed", "run_id", runID, "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
		run, _ = p.stateStore.GetRun(ctx, runID)
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

func (p *Pipeline) failHistoricalRun(ctx context.Context, runID int64, message string, err error) {
	p.logger.ErrorContext(ctx, message, "run_id", runID, "error", err)
	_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
	p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: err.Error()}))
	p.completeHistoricalRun(ctx, runID)
}
