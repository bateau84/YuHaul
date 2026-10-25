package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// RouteMatch pairs a route decision with its route for action execution.
type RouteMatch struct {
	RouteDecisionID int64
	RouteID         int64
}

// ActionExecutor executes playlist actions and records outcomes.
type ActionExecutor interface {
	Execute(ctx context.Context, runID, videoID int64, matches []RouteMatch) (state.VideoFinalOutcome, error)
}

// Versioner returns the active configuration version ID for a run.
type Versioner interface {
	ActiveVersionID(ctx context.Context) (*int64, error)
}

// AdvisoryGenerator scans channel statistics after a production run and creates
// evidence-backed advisories. The system never acts autonomously.
type AdvisoryGenerator interface {
	GenerateAdvisories(ctx context.Context) error
}

// ConfigStore creates and retrieves configuration snapshots. It is a subset of
// config.Store used by the pipeline to persist draft configurations for dry-run
// and backtest modes. CreateTx lets proposal ingest commit the translated
// snapshot inside its single SQLite transaction (ADR-012).
type ConfigStore interface {
	Create(ctx context.Context, snapshot config.Snapshot, description string) (int64, error)
	CreateTx(ctx context.Context, tx *sql.Tx, snapshot config.Snapshot, description string) (int64, error)
	Get(ctx context.Context, id int64) (*config.ConfigVersion, error)
}

// Pipeline orchestrates the five-phase run lifecycle.
type Pipeline struct {
	productionMu      sync.Mutex
	db                *sql.DB
	machine           *state.Machine
	stateStore        *state.Store
	discovery         *discovery.Engine
	detector          *duplicate.Detector
	router            *router.Engine
	executor          ActionExecutor
	ledger            *quota.Ledger
	meta              *metadata.Manager
	auth              *auth.Service
	broadcast         *Broadcaster
	versioner         Versioner
	queueStore        *queue.Store
	configStore       ConfigStore
	advisoryGenerator AdvisoryGenerator
	logger            *slog.Logger
}

// Option configures a Pipeline.
type Option func(*Pipeline)

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(p *Pipeline) {
		if l != nil {
			p.logger = l
		}
	}
}

// WithVersioner sets the config versioner used to link runs to the active
// configuration snapshot.
func WithVersioner(v Versioner) Option {
	return func(p *Pipeline) {
		if v != nil {
			p.versioner = v
		}
	}
}

// WithQueueStore sets the queue store used to process deferred work and create
// error entries on retry failure.
func WithQueueStore(qs *queue.Store) Option {
	return func(p *Pipeline) {
		if qs != nil {
			p.queueStore = qs
		}
	}
}

// WithConfigStore sets the config store used to persist draft configurations
// for dry-run and backtest modes.
func WithConfigStore(cs ConfigStore) Option {
	return func(p *Pipeline) {
		if cs != nil {
			p.configStore = cs
		}
	}
}

// WithAdvisoryGenerator sets the generator that creates evidence-backed
// advisories after production runs complete.
func WithAdvisoryGenerator(g AdvisoryGenerator) Option {
	return func(p *Pipeline) {
		if g != nil {
			p.advisoryGenerator = g
		}
	}
}

// NewPipeline constructs a pipeline with the required dependencies.
func NewPipeline(
	db *sql.DB,
	machine *state.Machine,
	stateStore *state.Store,
	discovery *discovery.Engine,
	detector *duplicate.Detector,
	router *router.Engine,
	executor ActionExecutor,
	ledger *quota.Ledger,
	meta *metadata.Manager,
	auth *auth.Service,
	broadcast *Broadcaster,
	opts ...Option,
) *Pipeline {
	p := &Pipeline{
		db:         db,
		machine:    machine,
		stateStore: stateStore,
		discovery:  discovery,
		detector:   detector,
		router:     router,
		executor:   executor,
		ledger:     ledger,
		meta:       meta,
		auth:       auth,
		broadcast:  broadcast,
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// StartResult is returned when a run is started.
type StartResult struct {
	RunID int64 `json:"run_id"`
}

// StartRun creates and starts a new run of the given type. It returns the run
// immediately; the caller should run Execute in a goroutine.
func (p *Pipeline) StartRun(ctx context.Context, runType state.RunType) (*state.Run, error) {
	run, err := p.prepareProductionRun(ctx, runType)
	if err != nil {
		return nil, err
	}
	p.productionMu.Lock()
	defer p.productionMu.Unlock()
	qs := p.queueStore
	if qs == nil {
		qs, err = queue.NewStore(p.db)
		if err != nil {
			return nil, err
		}
	}
	items, err := qs.ListDeferredRetryCandidates(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	err = qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		id, err := p.stateStore.CreateRunTx(ctx, tx, run)
		if err != nil {
			return err
		}
		run.ID = id
		for _, item := range items {
			if _, _, err := qs.ClaimDeferredGroupTx(ctx, tx, item.ID, id, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline: create and claim run: %w", err)
	}
	p.broadcastEvent(run.ID, EventRunStarted, nil)
	return run, nil
}

// Capture is deliberately outside the execution gate: waiting cannot change
// the configuration selected at initiation. Both production producers use it.
func (p *Pipeline) prepareProductionRun(ctx context.Context, runType state.RunType) (*state.Run, error) {
	if runType != state.RunTypeManual && runType != state.RunTypeScheduled {
		return nil, fmt.Errorf("pipeline: unsupported run type %q", runType)
	}

	run := &state.Run{
		StartedAt: time.Now().UTC(),
		RunType:   runType,
		Status:    state.RunStatusRunning,
	}
	if p.versioner != nil {
		cfgID, err := p.versioner.ActiveVersionID(ctx)
		if err != nil {
			return nil, fmt.Errorf("pipeline: active config version: %w", err)
		}
		run.ConfigVersionID = cfgID
	}
	if run.ConfigVersionID == nil || p.configStore == nil {
		return nil, fmt.Errorf("pipeline: captured configuration required")
	}
	if _, err := p.configStore.Get(ctx, *run.ConfigVersionID); err != nil {
		return nil, fmt.Errorf("pipeline: captured configuration: %w", err)
	}
	run.ActionAuthorityVersion = 1
	return run, nil
}

// Execute runs all five phases for runID. It is safe to call in a goroutine.
// If runID refers to an interrupted run, execution resumes from the last
// recorded phase. For new production runs, pending deferred work is processed
// before new discoveries.
func (p *Pipeline) Execute(ctx context.Context, runID int64) {
	p.productionMu.Lock()
	defer p.productionMu.Unlock()
	logger := p.logger.With("run_id", runID)
	logger.InfoContext(ctx, "pipeline execute started")

	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(ctx, "pipeline panic", "recover", r)
			reason := fmt.Sprintf("panic: %v", r)
			if _, ferr := p.machine.FailRun(ctx, runID, reason); ferr != nil {
				logger.ErrorContext(ctx, "fail run failed after panic", "run_id", runID, "error", ferr)
				_ = p.stateStore.InterruptRun(ctx, runID, reason)
			}
			p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: reason}))
		}
	}()

	run, err := p.stateStore.GetRun(ctx, runID)
	if err != nil {
		logger.ErrorContext(ctx, "load run failed", "error", err)
		return
	}
	if _, _, err := productionAuthority(ctx, p.db, p.configStore, runID); err != nil {
		logger.ErrorContext(ctx, "production authority rejected", "error", err)
		if p.isProductionRun(run.RunType) && (run.Status == state.RunStatusRunning || run.Status == state.RunStatusInterrupted) {
			if candidates, loadErr := p.listRunVideos(ctx, runID); loadErr == nil {
				p.retainAuthorityFailure(ctx, runID, candidates, err)
			}
		}
		return
	}
	// Validate every legacy candidate before any recovery shortcut or write.
	candidates, err := p.listRunVideos(ctx, runID)
	if err != nil {
		p.failRun(ctx, runID, "load authority candidates", err)
		return
	}
	routes, err := p.productionRoutes(ctx, runID)
	if err != nil {
		p.failRun(ctx, runID, "load snapshot routes", err)
		return
	}
	for _, v := range candidates {
		if err := admitLegacy(ctx, p.db, run, v.ID, routes, false); err != nil {
			p.retainAuthorityFailure(ctx, runID, candidates, err)
			return
		}
	}

	startPhase := PhaseDiscovery
	isResume := run.Status == state.RunStatusInterrupted
	if isResume && run.CurrentPhase != nil {
		startPhase = Phase(*run.CurrentPhase)
	}

	if p.meta != nil {
		if err := p.meta.RunLifecycle(ctx); err != nil {
			logger.WarnContext(ctx, "metadata lifecycle failed", "error", err)
		}
	}

	// Owned retries precede dispatch even when a crash left an actions checkpoint
	// before the receiving run's first decision.
	if err := p.processDeferredItems(ctx, runID); err != nil {
		p.failRun(ctx, runID, "retry processing failed", err)
		return
	}

	var videos []*state.Video

	if startPhase == PhaseDiscovery {
		videos, err = p.phaseDiscovery(ctx, runID)
		if err != nil {
			p.failRun(ctx, runID, "discovery phase failed", err)
			return
		}
		startPhase = PhaseExclusion
	}

	if videos == nil {
		videos, err = p.listRunVideos(ctx, runID)
		if err != nil {
			p.failRun(ctx, runID, "list run videos failed", err)
			return
		}
	}

	videos, err = p.withoutRetryVideos(ctx, runID, videos)
	if err != nil {
		p.failRun(ctx, runID, "load retry membership", err)
		return
	}
	if startPhase == PhaseExclusion {
		videos, err = p.phaseExclusion(ctx, runID, videos)
		if err != nil {
			p.failRun(ctx, runID, "exclusion gate failed", err)
			return
		}
		startPhase = PhaseDuplicateDetect
	} else if startPhase == PhaseDuplicateDetect || startPhase == PhaseRouting {
		// Reloaded run videos include fully excluded inputs. Reconstruct the
		// survivor set without rolling the durable phase checkpoint backwards.
		videos, err = p.filterExcludedVideos(ctx, runID, videos)
		if err != nil {
			p.failRun(ctx, runID, "restore exclusion gate failed", err)
			return
		}
	}

	if startPhase == PhaseDuplicateDetect {
		if err := p.phaseDuplicateDetection(ctx, runID, videos); err != nil {
			p.failRun(ctx, runID, "duplicate detection phase failed", err)
			return
		}
		startPhase = PhaseRouting
	}

	var routed []*state.Video
	if startPhase == PhaseRouting {
		routed, err = p.phaseRouting(ctx, runID, videos)
		if err != nil {
			p.failRun(ctx, runID, "routing phase failed", err)
			return
		}
		startPhase = PhaseActionExecution
	}

	if startPhase == PhaseActionExecution {
		if routed == nil {
			if run.ActionAuthorityVersion == 0 {
				for _, v := range videos {
					if _, err := p.completeLegacyEvaluation(ctx, run, v, routes); err != nil {
						p.failRun(ctx, runID, "legacy recovery failed", err)
						return
					}
				}
			}
			routed, err = p.videosWithMatchedDecisions(ctx, runID, videos)
			if err != nil {
				p.failRun(ctx, runID, "load matched videos failed", err)
				return
			}
		}
		if err := p.phaseActionExecution(ctx, runID, routed); err != nil {
			p.failRun(ctx, runID, "action execution phase failed", err)
			return
		}
	}

	p.completeRun(ctx, runID)
}

func (p *Pipeline) completeRun(ctx context.Context, runID int64) {
	run, err := p.machine.CompleteRun(ctx, runID)
	if err != nil {
		p.logger.ErrorContext(ctx, "complete run failed", "run_id", runID, "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
		run, _ = p.stateStore.GetRun(ctx, runID)
	}
	if run == nil {
		return
	}

	if p.isProductionRun(run.RunType) && p.advisoryGenerator != nil {
		if err := p.advisoryGenerator.GenerateAdvisories(ctx); err != nil {
			p.logger.WarnContext(ctx, "advisory generation failed", "run_id", runID, "error", err)
		}
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

func (p *Pipeline) failRun(ctx context.Context, runID int64, message string, err error) {
	p.logger.ErrorContext(ctx, message, "run_id", runID, "error", err)
	var authority *authorityError
	if errors.As(err, &authority) {
		videos, loadErr := p.listRunVideos(ctx, runID)
		if loadErr != nil {
			_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
			return
		}
		p.retainAuthorityFailure(ctx, runID, videos, err)
		return
	}
	if _, ferr := p.machine.FailRun(ctx, runID, err.Error()); ferr != nil {
		p.logger.ErrorContext(ctx, "fail run failed", "run_id", runID, "error", ferr)
		_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
	}
	p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: err.Error()}))
}

func (p *Pipeline) isProductionRun(runType state.RunType) bool {
	return runType == state.RunTypeScheduled || runType == state.RunTypeManual
}

func (p *Pipeline) setPhase(ctx context.Context, runID int64, phase Phase) {
	if err := p.stateStore.SetRunPhase(ctx, runID, string(phase)); err != nil {
		p.logger.WarnContext(ctx, "failed to record run phase", "run_id", runID, "phase", phase, "error", err)
	}
}

func (p *Pipeline) phaseDiscovery(ctx context.Context, runID int64) ([]*state.Video, error) {
	p.setPhase(ctx, runID, PhaseDiscovery)
	p.broadcastPhase(runID, PhaseDiscovery)

	result, err := p.discovery.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	if result.Deferred {
		p.broadcastEvent(runID, EventRunQuotaWarning, payload(quotaWarningPayload{
			Remaining: 0,
			Message:   result.DeferredReason,
		}))
	}

	videos, err := p.listRunVideos(ctx, runID)
	if err != nil {
		return nil, err
	}
	p.logger.InfoContext(ctx, "discovery complete", "run_id", runID,
		"discovered", len(videos), "deferred", result.Deferred)
	return videos, nil
}

func (p *Pipeline) phaseDuplicateDetection(ctx context.Context, runID int64, videos []*state.Video) error {
	p.setPhase(ctx, runID, PhaseDuplicateDetect)
	p.broadcastPhase(runID, PhaseDuplicateDetect)
	return p.detectVideos(ctx, runID, videos)
}

func (p *Pipeline) detectVideos(ctx context.Context, runID int64, videos []*state.Video) error {
	detector, err := p.detectorForRun(ctx, runID)
	if err != nil {
		return err
	}

	for _, v := range videos {
		// Idempotency: existing match evidence avoids repeating detection, not
		// routing. The unchanged survivor list still reaches eligible actions.
		alreadyMatched, err := p.hasDuplicateMatchInRun(ctx, v.ID)
		if err != nil {
			return fmt.Errorf("check duplicate match for %s: %w", v.VideoID, err)
		}
		if alreadyMatched {
			continue
		}

		meta, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, "duplicate detection using degraded metadata", "video_id", v.VideoID, "error", err)
			meta = &metadata.TransientMetadata{VideoID: v.VideoID}
		}
		title := ""
		if meta.Title != nil {
			title = *meta.Title
		}

		match, err := detector.Detect(ctx, v.VideoID, title)
		if err != nil {
			return fmt.Errorf("detect duplicate for %s: %w", v.VideoID, err)
		}
		if match != nil {
			if err := p.recordDuplicateMatch(ctx, v.ID, match); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Pipeline) phaseRouting(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	p.setPhase(ctx, runID, PhaseRouting)
	p.broadcastPhase(runID, PhaseRouting)
	return p.routeVideos(ctx, runID, videos)
}

func (p *Pipeline) routeVideos(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	run, routes, err := productionAuthority(ctx, p.db, p.configStore, runID)
	if err != nil {
		return nil, err
	}

	var routed []*state.Video
	for _, v := range videos {
		if err := admitLegacy(ctx, p.db, run, v.ID, routes, false); err != nil {
			return nil, err
		}
		decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
		if err != nil {
			return nil, fmt.Errorf("load decisions for video %d: %w", v.ID, err)
		}
		// Decisions alone are not a completion marker: an interruption can
		// leave only a prefix of the ordered routes persisted.
		if run.ActionAuthorityVersion == 1 && v.ProcessingState != state.VideoStateNotYetEvaluated && v.ProcessingState != state.VideoStateEvaluating {
			for _, d := range decisions {
				if d.Decision == state.RouteDecisionMatched {
					routed = append(routed, v)
					break
				}
			}
			continue
		}

		// Move into evaluating before route conditions are checked.
		if v.ProcessingState == state.VideoStateNotYetEvaluated {
			if _, err := p.machine.TransitionVideoState(ctx, v.ID,
				state.VideoStateNotYetEvaluated, state.VideoStateEvaluating); err != nil {
				return nil, fmt.Errorf("transition video %s to evaluating: %w", v.VideoID, err)
			}
		}

		meta, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, "routing using degraded metadata", "video_id", v.VideoID, "error", err)
			meta = &metadata.TransientMetadata{VideoID: v.VideoID}
		}

		vc := videoContextFromMetadata(v.VideoID, v.ChannelID, meta)
		var result *router.EvaluationResult
		if run.ActionAuthorityVersion == 0 {
			result, err = p.completeLegacyEvaluation(ctx, run, v, routes)
		} else {
			result, err = p.router.EvaluateWithRoutes(ctx, runID, v.ID, vc, p.machine, routes)
		}
		if err != nil {
			return nil, fmt.Errorf("route video %s: %w", v.VideoID, err)
		}

		decisions, err = p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
		if err != nil {
			return nil, fmt.Errorf("load decisions for video %s: %w", v.VideoID, err)
		}
		if run.ActionAuthorityVersion == 0 && v.ProcessingState != state.VideoStateNotYetEvaluated && v.ProcessingState != state.VideoStateEvaluating {
			if len(result.MatchedRoutes) > 0 {
				routed = append(routed, v)
			}
			continue
		}

		// A route-local exclusion does not exclude a video that reached
		// condition evaluation on another eligible route (OC-002).
		notMatched := false
		for _, d := range decisions {
			if d.Decision == state.RouteDecisionNotMatched {
				notMatched = true
				break
			}
		}
		if len(result.MatchedRoutes) > 0 {
			if v.ProcessingState == state.VideoStateNotYetEvaluated || v.ProcessingState == state.VideoStateEvaluating {
				if _, err := p.machine.TransitionVideoState(ctx, v.ID,
					state.VideoStateEvaluating, state.VideoStateEvaluated); err != nil {
					return nil, fmt.Errorf("mark video %s evaluated: %w", v.VideoID, err)
				}
			}
			routed = append(routed, v)
		} else if !notMatched && hasExclusionDecision(decisions) {
			if _, err := p.machine.TransitionRunVideoState(ctx, runID, v.ID,
				state.VideoStateEvaluating, state.VideoStateEvaluated, state.FinalOutcomeExcluded, nil, nil); err != nil {
				return nil, fmt.Errorf("mark video %s excluded: %w", v.VideoID, err)
			}
			p.broadcastVideo(runID, v, state.FinalOutcomeExcluded, false)
		} else {
			// No route matched and not excluded: mark rejected.
			if _, err := p.machine.TransitionRunVideoState(ctx, runID, v.ID,
				state.VideoStateEvaluating, state.VideoStateEvaluated, state.FinalOutcomeRejected, nil, nil); err != nil {
				return nil, fmt.Errorf("mark video %s rejected: %w", v.VideoID, err)
			}
			p.broadcastVideo(runID, v, state.FinalOutcomeRejected, false)
		}
	}
	return routed, nil
}

func hasExclusionDecision(decisions []state.RouteDecision) bool {
	for _, d := range decisions {
		if d.Decision == state.RouteDecisionSkipped && d.SkipReason != nil &&
			*d.SkipReason == state.SkipReasonExclusion {
			return true
		}
	}
	return false
}

func (p *Pipeline) phaseActionExecution(ctx context.Context, runID int64, videos []*state.Video) error {
	p.setPhase(ctx, runID, PhaseActionExecution)
	p.broadcastPhase(runID, PhaseActionExecution)

	for _, v := range videos {
		if _, err := p.executeVideoActions(ctx, runID, v); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) executeVideoActions(ctx context.Context, runID int64, v *state.Video) (state.VideoFinalOutcome, error) {
	// Collect matched route IDs for the video.
	run, routes, err := productionAuthority(ctx, p.db, p.configStore, runID)
	if err != nil {
		return state.FinalOutcomeFailed, err
	}
	if err := admitLegacy(ctx, p.db, run, v.ID, routes, true); err != nil {
		return state.FinalOutcomeFailed, err
	}
	decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
	if err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("load decisions for video %d: %w", v.ID, err)
	}

	var matched []RouteMatch
	for _, d := range decisions {
		if d.Decision == state.RouteDecisionMatched {
			matched = append(matched, RouteMatch{
				RouteDecisionID: d.ID,
				RouteID:         d.RouteID,
			})
		}
	}

	if len(matched) == 0 {
		return state.FinalOutcomeRejected, nil
	}
	if v.ProcessingState == state.VideoStateCompleted {
		// Completion is a veto only after current-run admission. Reconcile a
		// finished video without repeating its terminal transition/counters.
		allComplete := true
		for _, m := range matched {
			r := findRoute(routes, m.RouteID)
			if r == nil || !r.IsEnabled || len(r.Actions) == 0 {
				allComplete = false
				break
			}
			for _, a := range r.Actions {
				if a.ActionType != "playlist_insert" || a.TargetPlaylistID == "" {
					allComplete = false
					break
				}
				done, err := actionCompleted(ctx, p.db, v.ID, action{m.RouteDecisionID, a.ActionType, a.TargetPlaylistID})
				if err != nil {
					return state.FinalOutcomeFailed, err
				}
				allComplete = allComplete && done
			}
		}
		if allComplete {
			return state.FinalOutcomeAccepted, nil
		}
	}

	// Transition into acted before executing. If resuming at action execution,
	// the video may already be in acted, evaluated, or deferred state; use the
	// current state as the from-state so retries work for deferred videos.
	fromState := state.VideoStateEvaluated
	switch v.ProcessingState {
	case state.VideoStateActed, state.VideoStateDeferred:
		fromState = v.ProcessingState
	}
	if _, err := p.machine.TransitionVideoState(ctx, v.ID, fromState, state.VideoStateActed); err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("transition video %d to acted: %w", v.ID, err)
	}

	outcome, err := p.executor.Execute(ctx, runID, v.ID, matched)
	if err != nil {
		message := err.Error()
		if _, terr := p.machine.TransitionRunVideoState(ctx, runID, v.ID,
			state.VideoStateActed, state.VideoStateFailed, state.FinalOutcomeFailed, nil, &message); terr != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("execute actions for video %d: %w", v.ID, terr)
		}
		return state.FinalOutcomeFailed, fmt.Errorf("execute actions for video %d: %w", v.ID, err)
	}

	switch outcome {
	case state.FinalOutcomeAccepted:
		// Atomically mark the video completed and update channel counters. For
		// v1, a routed (accepted) video is treated as retained because the
		// retention-signal monitoring path is not yet wired into the pipeline.
		if err := p.markVideoAccepted(ctx, runID, v); err != nil {
			return outcome, err
		}
	case state.FinalOutcomeDeferred:
		reason := "quota_exhausted"
		if err := p.deferRunVideo(ctx, runID, v.ID, reason); err != nil {
			return outcome, fmt.Errorf("transition video %d to deferred: %w", v.ID, err)
		}
	case state.FinalOutcomeFailed:
		message := "action execution failed"
		if _, err := p.machine.TransitionRunVideoState(ctx, runID, v.ID,
			state.VideoStateActed, state.VideoStateFailed, state.FinalOutcomeFailed, nil, &message); err != nil {
			return outcome, fmt.Errorf("transition video %d to failed: %w", v.ID, err)
		}
	}

	p.broadcastVideo(runID, v, outcome, len(matched) > 0)
	return outcome, nil
}

func (p *Pipeline) deferRunVideo(ctx context.Context, runID, videoID int64, reason string) error {
	qs := p.queueStore
	if qs == nil {
		var err error
		qs, err = queue.NewStore(p.db)
		if err != nil {
			return err
		}
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deferred disposition transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{
		VideoID: videoID, RunID: runID, Reason: queue.DeferredReason(reason),
		Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("record deferred item: %w", err)
	}
	if _, err := p.machine.TransitionRunVideoStateTx(ctx, tx, runID, videoID,
		state.VideoStateActed, state.VideoStateDeferred, state.FinalOutcomeDeferred, &reason, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deferred disposition: %w", err)
	}
	return nil
}

func (p *Pipeline) markVideoAccepted(ctx context.Context, runID int64, v *state.Video) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin accepted transaction for video %d: %w", v.ID, err)
	}

	if err := duplicate.SetDuplicateReference(ctx, tx, v.VideoID, true); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("mark duplicate reference for %s: %w", v.VideoID, err)
	}
	if _, err := p.machine.TransitionVideoStateTx(ctx, tx, v.ID,
		state.VideoStateActed, state.VideoStateCompleted,
		state.WithFinalOutcome(state.FinalOutcomeAccepted)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("transition video %d to completed: %w", v.ID, err)
	}
	accepted := state.FinalOutcomeAccepted
	completedAt := time.Now().UTC()
	if err := p.stateStore.RecordRunVideoOutcomeTx(ctx, tx, runID, v.ID, &accepted, nil, nil, &completedAt); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record accepted outcome for run %d video %d: %w", runID, v.ID, err)
	}
	if err := p.stateStore.IncrementChannelRoutedTx(ctx, tx, v.ChannelID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("increment routed for video %d: %w", v.ID, err)
	}
	if err := p.stateStore.IncrementChannelRetainedTx(ctx, tx, v.ChannelID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("increment retained for video %d: %w", v.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit accepted transaction for video %d: %w", v.ID, err)
	}
	return nil
}

func (p *Pipeline) listRunVideos(ctx context.Context, runID int64) ([]*state.Video, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM video WHERE discovered_in_run_id = ? OR id IN (SELECT video_id FROM route_decision WHERE run_id=?) OR id IN (SELECT video_id FROM deferred_item WHERE retry_run_id=? AND status='retrying') OR id IN (SELECT video_id FROM error_entry WHERE retry_run_id=? AND status='retrying');`, runID, runID, runID, runID)
	if err != nil {
		return nil, fmt.Errorf("list run videos: %w", err)
	}
	defer rows.Close()

	var out []*state.Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run video: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run videos: %w", err)
	}
	return out, nil
}

func (p *Pipeline) recordDuplicateMatch(ctx context.Context, videoID int64, match *duplicate.Match) error {
	var fpID sql.NullInt64
	if match.FingerprintID > 0 {
		fpID = sql.NullInt64{Int64: match.FingerprintID, Valid: true}
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO duplicate_match (video_id, matched_fingerprint_id, similarity_score,
		                              match_type, threshold_used)
		 VALUES (?, ?, ?, ?, ?);`,
		videoID, fpID, match.Similarity, string(match.Type), match.ThresholdUsed)
	return err
}

func (p *Pipeline) hasDuplicateMatchInRun(ctx context.Context, videoID int64) (bool, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT 1 FROM duplicate_match WHERE video_id = ? LIMIT 1;`, videoID).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (p *Pipeline) videosWithMatchedDecisions(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	var routed []*state.Video
	for _, v := range videos {
		decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
		if err != nil {
			return nil, err
		}
		for _, d := range decisions {
			if d.Decision == state.RouteDecisionMatched {
				routed = append(routed, v)
				break
			}
		}
	}
	return routed, nil
}

// processDeferredItems retries pending deferred videos before new discoveries.
// Items are processed oldest-first (FIFO). Quota checks are delegated to the
// action executor; if quota is exhausted the remaining items stay pending.
func (p *Pipeline) processDeferredItems(ctx context.Context, runID int64) error {
	qs := p.queueStore
	if qs == nil {
		var err error
		qs, err = queue.NewStore(p.db)
		if err != nil {
			return err
		}
	}

	ids, err := qs.ListRetryVideoIDs(ctx, runID)
	if err != nil {
		return fmt.Errorf("list deferred items: %w", err)
	}
	for _, id := range ids {
		if err := p.processRetryVideo(ctx, runID, id); err != nil {
			return fmt.Errorf("process retry video %d: %w", id, err)
		}
	}
	return nil
}

func videoContextFromMetadata(videoID, channelID string, m *metadata.TransientMetadata) router.VideoContext {
	vc := router.VideoContext{VideoID: videoID, ChannelID: channelID}
	if m.ChannelName != nil {
		vc.ChannelName = *m.ChannelName
	}
	if m.Title != nil {
		vc.Title = *m.Title
	}
	if m.Description != nil {
		vc.Description = *m.Description
	}
	if m.DurationSeconds != nil {
		vc.DurationSeconds = m.DurationSeconds
	}
	return vc
}

func (p *Pipeline) broadcastPhase(runID int64, phase Phase) {
	p.broadcastEvent(runID, EventRunPhase, payload(phasePayload{Phase: phase}))
}

func (p *Pipeline) broadcastVideo(runID int64, v *state.Video, outcome state.VideoFinalOutcome, matched bool) {
	p.broadcastEvent(runID, EventRunVideo, payload(videoPayload{
		VideoID:      v.ID,
		VideoIDText:  v.VideoID,
		Outcome:      string(outcome),
		MatchedRoute: matched,
	}))
}

func (p *Pipeline) broadcastEvent(runID int64, typ EventType, raw json.RawMessage) {
	if p.broadcast == nil {
		return
	}
	p.broadcast.Broadcast(runID, Event{
		Type:      typ,
		RunID:     runID,
		Timestamp: time.Now().UTC(),
		Payload:   raw,
	})
}

func scanVideo(sc interface {
	Scan(dest ...any) error
}) (*state.Video, error) {
	v := &state.Video{}
	var deferredReason, errorMessage, completedAt sql.NullString
	err := sc.Scan(&v.ID, &v.VideoID, &v.ChannelID, &v.DiscoveredAt, &v.DiscoveredInRunID,
		&v.ProcessingState, &v.FinalOutcome, &deferredReason, &errorMessage, &completedAt)
	if err != nil {
		return nil, err
	}
	if deferredReason.Valid {
		v.DeferredReason = &deferredReason.String
	}
	if errorMessage.Valid {
		v.ErrorMessage = &errorMessage.String
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
		v.CompletedAt = &t
	}
	return v, nil
}
