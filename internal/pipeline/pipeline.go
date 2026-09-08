package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
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

// Pipeline orchestrates the four-phase run lifecycle.
type Pipeline struct {
	db         *sql.DB
	machine    *state.Machine
	stateStore *state.Store
	discovery  *discovery.Engine
	detector   *duplicate.Detector
	router     *router.Engine
	executor   ActionExecutor
	ledger     *quota.Ledger
	meta       *metadata.Manager
	auth       *auth.Service
	broadcast  *Broadcaster
	logger     *slog.Logger
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
	if runType != state.RunTypeManual && runType != state.RunTypeScheduled {
		return nil, fmt.Errorf("pipeline: unsupported run type %q", runType)
	}

	run := &state.Run{
		StartedAt: time.Now().UTC(),
		RunType:   runType,
		Status:    state.RunStatusRunning,
	}
	id, err := p.stateStore.CreateRun(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("pipeline: create run: %w", err)
	}
	run.ID = id

	p.broadcastEvent(run.ID, EventRunStarted, nil)
	return run, nil
}

// Execute runs all four phases for runID. It is safe to call in a goroutine.
func (p *Pipeline) Execute(ctx context.Context, runID int64) {
	logger := p.logger.With("run_id", runID)
	logger.InfoContext(ctx, "pipeline execute started")

	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(ctx, "pipeline panic", "recover", r)
			_ = p.stateStore.InterruptRun(ctx, runID, fmt.Sprintf("panic: %v", r))
			p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: fmt.Sprintf("panic: %v", r)}))
			p.completeRun(ctx, runID)
		}
	}()

	if err := p.meta.RunLifecycle(ctx); err != nil {
		logger.WarnContext(ctx, "metadata lifecycle failed", "error", err)
	}

	videos, err := p.phaseDiscovery(ctx, runID)
	if err != nil {
		logger.ErrorContext(ctx, "discovery phase failed", "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
		p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: err.Error()}))
		p.completeRun(ctx, runID)
		return
	}

	if err := p.phaseDuplicateDetection(ctx, runID, videos); err != nil {
		logger.ErrorContext(ctx, "duplicate detection phase failed", "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
		p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: err.Error()}))
		p.completeRun(ctx, runID)
		return
	}

	routed, err := p.phaseRouting(ctx, runID, videos)
	if err != nil {
		logger.ErrorContext(ctx, "routing phase failed", "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
		p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: err.Error()}))
		p.completeRun(ctx, runID)
		return
	}

	if err := p.phaseActionExecution(ctx, runID, routed); err != nil {
		logger.ErrorContext(ctx, "action execution phase failed", "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, err.Error())
		p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: err.Error()}))
		p.completeRun(ctx, runID)
		return
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
	p.broadcastEvent(runID, EventRunCompleted, payload(completedPayload{
		RunID:                 run.ID,
		Status:                string(run.Status),
		VideosDiscovered:      run.VideosDiscovered,
		VideosAccepted:        run.VideosAccepted,
		VideosRejected:        run.VideosRejected,
		VideosDeferred:        run.VideosDeferred,
		VideosFailed:          run.VideosFailed,
		VideosNotYetEvaluated: run.VideosNotYetEvaluated,
	}))
}

func (p *Pipeline) phaseDiscovery(ctx context.Context, runID int64) ([]*state.Video, error) {
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
	p.broadcastPhase(runID, PhaseDuplicateDetect)

	for _, v := range videos {
		meta, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, "duplicate detection using degraded metadata", "video_id", v.VideoID, "error", err)
			meta = &metadata.TransientMetadata{VideoID: v.VideoID}
		}
		title := ""
		if meta.Title != nil {
			title = *meta.Title
		}

		match, err := p.detector.Detect(ctx, v.VideoID, title)
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
	p.broadcastPhase(runID, PhaseRouting)

	var routed []*state.Video
	for _, v := range videos {
		meta, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, "routing using degraded metadata", "video_id", v.VideoID, "error", err)
			meta = &metadata.TransientMetadata{VideoID: v.VideoID}
		}

		vc := videoContextFromMetadata(v.VideoID, meta)
		result, err := p.router.Evaluate(ctx, runID, v.ID, vc, p.machine)
		if err != nil {
			return nil, fmt.Errorf("route video %s: %w", v.VideoID, err)
		}

		if len(result.MatchedRoutes) > 0 {
			routed = append(routed, v)
		} else {
			// No route matched: mark rejected.
			if _, err := p.machine.TransitionVideoState(ctx, v.ID,
				state.VideoStateNotYetEvaluated, state.VideoStateEvaluated,
				state.WithFinalOutcome(state.FinalOutcomeRejected)); err != nil {
				return nil, fmt.Errorf("mark video %s rejected: %w", v.VideoID, err)
			}
			p.broadcastVideo(runID, v, state.FinalOutcomeRejected, false)
		}
	}
	return routed, nil
}

func (p *Pipeline) phaseActionExecution(ctx context.Context, runID int64, videos []*state.Video) error {
	p.broadcastPhase(runID, PhaseActionExecution)

	for _, v := range videos {
		// Collect matched route IDs for the video.
		decisions, err := p.stateStore.GetRouteDecisionsForVideo(ctx, v.ID)
		if err != nil {
			return fmt.Errorf("load decisions for video %d: %w", v.ID, err)
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

		outcome, err := p.executor.Execute(ctx, runID, v.ID, matched)
		if err != nil {
			return fmt.Errorf("execute actions for video %d: %w", v.ID, err)
		}

		if outcome == state.FinalOutcomeAccepted {
			if err := duplicate.SetDuplicateReference(ctx, p.db, v.VideoID, true); err != nil {
				return fmt.Errorf("mark duplicate reference for %s: %w", v.VideoID, err)
			}
		}

		p.broadcastVideo(runID, v, outcome, len(matched) > 0)
	}
	return nil
}

func (p *Pipeline) listRunVideos(ctx context.Context, runID int64) ([]*state.Video, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM video WHERE discovered_in_run_id = ?;`, runID)
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
		videoID, fpID, match.Similarity, string(match.Type), duplicate.DefaultThreshold)
	return err
}

func videoContextFromMetadata(videoID string, m *metadata.TransientMetadata) router.VideoContext {
	vc := router.VideoContext{VideoID: videoID}
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
