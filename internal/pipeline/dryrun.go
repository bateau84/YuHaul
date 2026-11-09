package pipeline

import (
	"context"
	"database/sql"
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

// DryRun creates a dry-run record and starts it. The caller must invoke
// ExecuteDryRun in a goroutine to perform the evaluation.
//
// A non-nil draft is a public proposal configuration (ADR-012): its route
// UUID identities are translated to internal integer keys — creating inactive
// identity anchors and registry mappings where required — in the same SQLite
// transaction that stores the immutable ConfigVersion snapshot and the run
// row. A nil draft evaluates the currently active configuration.
func (p *Pipeline) DryRun(ctx context.Context, draft *config.ProposalSnapshot) (*state.Run, error) {
	run := &state.Run{
		StartedAt: time.Now().UTC(),
		RunType:   state.RunTypeDryRun,
		Status:    state.RunStatusRunning,
	}

	if draft != nil {
		if err := p.ingestProposalRun(ctx, "dry-run draft configuration", draft, run); err != nil {
			return nil, err
		}
	} else {
		if p.versioner != nil {
			cfgID, err := p.versioner.ActiveVersionID(ctx)
			if err != nil {
				return nil, err
			}
			run.ConfigVersionID = cfgID
		}
		id, err := p.stateStore.CreateRun(ctx, run)
		if err != nil {
			return nil, fmt.Errorf("pipeline: create dry-run: %w", err)
		}
		run.ID = id
	}

	p.broadcastEvent(run.ID, EventRunStarted, nil)
	return run, nil
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
			logger.ErrorContext(ctx, "dry-run panic", "diagnostic", diag.Encode(diag.PanicCause(), "unknown", "unknown"))
			p.failHistoricalRun(ctx, runID, "dry-run panic", diag.PanicCause())
		}
	}()

	run, err := p.stateStore.GetRun(ctx, runID)
	if err != nil {
		logger.ErrorContext(ctx, "load dry-run failed", "diagnostic", diag.Encode(err, "unknown", "load_run"))
		p.failHistoricalRun(ctx, runID, "load dry-run failed", err)
		return
	}

	routes, err := p.dryRunRoutes(ctx, run)
	if err != nil {
		p.failHistoricalRun(ctx, runID, "load dry-run routes failed", err)
		return
	}

	videos, err := p.stateStore.ListPendingVideos(ctx)
	if err != nil {
		p.failHistoricalRun(ctx, runID, "list pending videos failed", err)
		return
	}
	// The pending selector establishes the dry-run's input set here. Capture
	// it before evaluation so a later failure keeps the known selection and
	// an empty selection is stored as zero, not unknown (ARD-001).
	p.captureSelectedCount(ctx, runID, len(videos))

	p.setPhase(ctx, runID, PhaseRouting)
	p.broadcastPhase(runID, PhaseRouting)

	detector, err := p.detectorForRun(ctx, runID)
	if err != nil {
		p.failHistoricalRun(ctx, runID, "load duplicate configuration failed", err)
		return
	}
	for _, v := range videos {
		if err := p.dryRunVideo(ctx, runID, &v, routes, detector); err != nil {
			p.failHistoricalRun(ctx, runID, "dry-run video failed", err)
			return
		}
	}

	// Pending videos were discovered in production runs. Derive hypothetical
	// outcomes from this run's decisions, not their unchanged production state.
	p.completeHistoricalRun(ctx, runID)
}

func (p *Pipeline) dryRunRoutes(ctx context.Context, run *state.Run) ([]router.Route, error) {
	if run.ConfigVersionID == nil || p.configStore == nil {
		return nil, fmt.Errorf("run %d missing captured configuration", run.ID)
	}
	cv, err := p.configStore.Get(ctx, *run.ConfigVersionID)
	if err != nil {
		return nil, fmt.Errorf("load config version %d: %w", *run.ConfigVersionID, err)
	}
	if err := validateSnapshotRoutes(cv.Snapshot); err != nil {
		return nil, err
	}
	return routesFromSnapshot(cv.Snapshot), nil
}

func (p *Pipeline) dryRunVideo(ctx context.Context, runID int64, v *state.Video, routes []router.Route, detector *duplicate.Detector) error {
	meta := &metadata.TransientMetadata{VideoID: v.VideoID}
	if p.meta != nil {
		fetched, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, "dry-run using degraded metadata", "diagnostic", diag.Encode(err, "routing", "load_metadata"))
		} else {
			meta = fetched
		}
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

	// Read-only detection uses this run's threshold, not current settings.
	// Matches always continue to eligible routes/actions without production
	// duplicate-match writes.
	if detector != nil {
		if _, err := detector.Detect(ctx, v.VideoID, vc.Title); err != nil {
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

// validateSnapshotRoutes prevents malformed fields from acquiring matching
// semantics when converted to router's non-nullable representation.
func validateSnapshotRoutes(snap config.Snapshot) error {
	seen := make(map[int64]bool)
	for _, r := range snap.Routes {
		if !r.HasRequiredFields() {
			return fmt.Errorf("snapshot route %d has missing route field", r.ID)
		}
		if r.ID <= 0 || seen[r.ID] {
			return fmt.Errorf("invalid snapshot route %d", r.ID)
		}
		seen[r.ID] = true
		for _, c := range r.Conditions {
			if !c.HasRequiredFields() {
				return fmt.Errorf("snapshot route %d has missing condition field", r.ID)
			}
		}
		for _, a := range r.Actions {
			if !a.HasRequiredFields() {
				return fmt.Errorf("snapshot route %d has missing action field", r.ID)
			}
		}
		for _, ex := range r.Exclusions {
			if !ex.HasType() || !ex.HasValue() {
				return fmt.Errorf("snapshot route %d has missing exclusion field", r.ID)
			}
		}
	}
	return nil
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
