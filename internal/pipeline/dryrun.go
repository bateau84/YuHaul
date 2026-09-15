package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// DryRun creates a dry-run record and starts it. The caller must invoke
// ExecuteDryRun in a goroutine to perform the evaluation.
func (p *Pipeline) DryRun(ctx context.Context, draft *config.Snapshot) (*state.Run, error) {
	run := &state.Run{
		StartedAt: time.Now().UTC(),
		RunType:   state.RunTypeDryRun,
		Status:    state.RunStatusRunning,
	}

	cfgID, err := p.resolveDryRunConfig(ctx, draft)
	if err != nil {
		return nil, err
	}
	run.ConfigVersionID = cfgID

	id, err := p.stateStore.CreateRun(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("pipeline: create dry-run: %w", err)
	}
	run.ID = id

	p.broadcastEvent(run.ID, EventRunStarted, nil)
	return run, nil
}

func (p *Pipeline) resolveDryRunConfig(ctx context.Context, draft *config.Snapshot) (*int64, error) {
	if draft != nil {
		if p.configStore == nil {
			return nil, fmt.Errorf("pipeline: dry-run draft requires a config store")
		}
		id, err := p.configStore.Create(ctx, *draft, "dry-run draft configuration")
		if err != nil {
			return nil, fmt.Errorf("pipeline: persist dry-run draft: %w", err)
		}
		return &id, nil
	}
	if p.versioner != nil {
		return p.versioner.ActiveVersionID(ctx)
	}
	return nil, nil
}

// ExecuteDryRun evaluates the current pending workload against the
// configuration selected for runID without mutating external playlists,
// production statistics, the duplicate-reference corpus, or video processing
// state. It records route decisions, condition audits, and simulated
// (not_attempted) executed actions under the dry-run run.
func (p *Pipeline) ExecuteDryRun(ctx context.Context, runID int64) {
	logger := p.logger.With("run_id", runID, "run_type", state.RunTypeDryRun)
	logger.InfoContext(ctx, "dry-run execute started")

	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(ctx, "dry-run panic", "recover", r)
			_ = p.stateStore.InterruptRun(ctx, runID, fmt.Sprintf("panic: %v", r))
			p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: fmt.Sprintf("panic: %v", r)}))
			p.completeRun(ctx, runID)
		}
	}()

	run, err := p.stateStore.GetRun(ctx, runID)
	if err != nil {
		logger.ErrorContext(ctx, "load dry-run failed", "error", err)
		return
	}

	routes, err := p.dryRunRoutes(ctx, run)
	if err != nil {
		p.failRun(ctx, runID, "load dry-run routes failed", err)
		return
	}

	videos, err := p.stateStore.ListPendingVideos(ctx)
	if err != nil {
		p.failRun(ctx, runID, "list pending videos failed", err)
		return
	}

	p.setPhase(ctx, runID, PhaseRouting)
	p.broadcastPhase(runID, PhaseRouting)

	for _, v := range videos {
		if err := p.dryRunVideo(ctx, runID, &v, routes); err != nil {
			p.failRun(ctx, runID, "dry-run video failed", err)
			return
		}
	}

	p.completeRun(ctx, runID)
}

func (p *Pipeline) dryRunRoutes(ctx context.Context, run *state.Run) ([]router.Route, error) {
	if run.ConfigVersionID != nil && p.configStore != nil {
		cv, err := p.configStore.Get(ctx, *run.ConfigVersionID)
		if err != nil {
			return nil, fmt.Errorf("load config version %d: %w", *run.ConfigVersionID, err)
		}
		return routesFromSnapshot(cv.Snapshot), nil
	}
	if p.router == nil {
		return nil, fmt.Errorf("no router available for dry-run")
	}
	return p.router.Routes(ctx)
}

func (p *Pipeline) dryRunVideo(ctx context.Context, runID int64, v *state.Video, routes []router.Route) error {
	meta := &metadata.TransientMetadata{VideoID: v.VideoID}
	if p.meta != nil {
		fetched, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, "dry-run using degraded metadata", "video_id", v.VideoID, "error", err)
		} else {
			meta = fetched
		}
	}

	vc := videoContextFromMetadata(v.VideoID, meta)
	if _, err := p.router.EvaluateWithRoutes(ctx, runID, v.ID, vc, p.machine, routes); err != nil {
		return fmt.Errorf("evaluate video %s: %w", v.VideoID, err)
	}

	decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
	if err != nil {
		return fmt.Errorf("load decisions for video %d: %w", v.ID, err)
	}

	matched := false
	for _, d := range decisions {
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
	} else if hasExclusionDecision(decisions) {
		outcome = state.FinalOutcomeExcluded
	}
	p.broadcastVideo(runID, v, outcome, matched)
	return nil
}

func (p *Pipeline) simulateAction(ctx context.Context, runID, videoID int64,
	decision *state.RouteDecision, routes []router.Route) error {

	route := findRoute(routes, decision.RouteID)
	if route == nil {
		return fmt.Errorf("simulate action: route %d not found in dry-run config", decision.RouteID)
	}
	for _, a := range route.Actions {
		exists, err := p.dryRunActionExists(ctx, runID, videoID, a.ActionType, a.TargetPlaylistID)
		if err != nil {
			return fmt.Errorf("check simulated action for video %d: %w", videoID, err)
		}
		if exists {
			continue
		}
		_, err = p.machine.RecordExecutedAction(ctx, &state.ExecutedAction{
			RouteDecisionID:  decision.ID,
			RunID:            runID,
			VideoID:          videoID,
			ActionType:       a.ActionType,
			TargetPlaylistID: a.TargetPlaylistID,
			Outcome:          state.ActionOutcomeNotAttempted,
		})
		if err != nil {
			return fmt.Errorf("record simulated action for video %d: %w", videoID, err)
		}
	}
	return nil
}

func (p *Pipeline) dryRunActionExists(ctx context.Context, runID, videoID int64, actionType, target string) (bool, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT 1 FROM executed_action
		 WHERE run_id = ? AND video_id = ? AND action_type = ? AND target_playlist_id = ?
		 LIMIT 1;`,
		runID, videoID, actionType, target).Scan(&n)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func routesFromSnapshot(snap config.Snapshot) []router.Route {
	routes := make([]router.Route, len(snap.Routes))
	for i, sr := range snap.Routes {
		r := router.Route{
			ID:                   sr.ID,
			Name:                 sr.Name,
			Description:          sr.Description,
			EvaluationOrder:      sr.EvaluationOrder,
			IsFinal:              sr.IsFinal,
			IsEnabled:            sr.IsEnabled,
			ConditionComposition: sr.ConditionComposition,
		}
		for _, c := range sr.Conditions {
			r.Conditions = append(r.Conditions, router.RouteCondition{
				ConditionIndex: c.ConditionIndex,
				Field:          router.ConditionField(c.Field),
				Operator:       router.ConditionOperator(c.Operator),
				Value:          c.Value,
			})
		}
		for _, a := range sr.Actions {
			r.Actions = append(r.Actions, router.RouteAction{
				ActionIndex:      a.ActionIndex,
				ActionType:       a.ActionType,
				TargetPlaylistID: a.TargetPlaylistID,
			})
		}
		for _, e := range sr.Exclusions {
			r.Exclusions = append(r.Exclusions, router.RouteExclusion{
				ExclusionType:  router.ExclusionType(e.ExclusionType),
				ExclusionValue: e.ExclusionValue,
			})
		}
		routes[i] = r
	}
	return routes
}

func findRoute(routes []router.Route, id int64) *router.Route {
	for i := range routes {
		if routes[i].ID == id {
			return &routes[i]
		}
	}
	return nil
}
