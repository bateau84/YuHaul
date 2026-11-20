package pipeline

import (
	"context"
	"fmt"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// phaseExclusion runs the global exclusion gate for a production run. Videos
// excluded by every enabled route are marked FinalOutcomeExcluded and removed
// from the slice returned to duplicate detection and routing.
func (p *Pipeline) phaseExclusion(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	p.setPhase(ctx, runID, PhaseExclusion)
	p.broadcastPhase(runID, PhaseExclusion)
	return p.filterExcludedVideos(ctx, runID, videos)
}

func (p *Pipeline) filterExcludedVideos(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	routes, err := p.productionRoutes(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load routes for exclusion gate: %w", err)
	}
	return p.runExclusionGate(ctx, runID, videos, routes, true)
}

// runExclusionGate checks each video against the exclusion rules of all enabled
// routes. If mutate is true, videos excluded by every enabled route are
// transitioned to evaluated with FinalOutcomeExcluded and have a skip decision
// recorded for each enabled route. The returned slice contains only videos that
// are not fully excluded and should proceed to route condition evaluation.
func (p *Pipeline) runExclusionGate(ctx context.Context, runID int64, videos []*state.Video, routes []router.Route, mutate bool) ([]*state.Video, error) {
	if err := p.ValidateRunRoutes(ctx, runID, routes); err != nil {
		return nil, fmt.Errorf("validate exclusion gate routes: %w", err)
	}
	var remaining []*state.Video

	for _, v := range videos {
		if mutate {
			run, _, err := productionAuthority(ctx, p.db, p.configStore, runID)
			if err != nil {
				return nil, err
			}
			if err := admitLegacy(ctx, p.db, run, v.ID, routes, false); err != nil {
				return nil, err
			}
		}
		existing, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
		if err != nil {
			return nil, fmt.Errorf("load decisions for video %d: %w", v.ID, err)
		}
		excludedDecisionRoutes := make(map[int64]bool)
		processed := false
		for _, d := range existing {
			if d.Decision == state.RouteDecisionSkipped && d.SkipReason != nil && *d.SkipReason == state.SkipReasonExclusion {
				excludedDecisionRoutes[d.RouteID] = true
			} else if d.Decision != state.RouteDecisionSkipped {
				processed = true
			}
		}
		if processed {
			// Preserve downstream recovery for videos already condition-evaluated.
			remaining = append(remaining, v)
			continue
		}

		vc, err := p.videoContextForExclusion(ctx, v)
		if err != nil {
			return nil, err
		}

		excludedRoutes := enabledRoutesThatExclude(routes, vc)
		excludedRouteIDs := make(map[int64]bool)
		for _, candidate := range excludedRoutes {
			excludedRouteIDs[candidate.route.ID] = true
		}
		// Durable exclusion evidence survives unavailable transient metadata.
		for _, r := range routes {
			if r.IsEnabled && excludedDecisionRoutes[r.ID] && !excludedRouteIDs[r.ID] {
				excludedRoutes = append(excludedRoutes, exclusionCandidate{route: r})
			}
		}
		if len(excludedRoutes) == 0 || len(excludedRoutes) < enabledRouteCount(routes) {
			remaining = append(remaining, v)
			continue
		}

		if mutate {
			if v.ProcessingState == state.VideoStateEvaluated && v.FinalOutcome == state.FinalOutcomeExcluded {
				continue
			}
			if err := p.markVideoExcluded(ctx, runID, v, excludedRoutes, excludedDecisionRoutes); err != nil {
				return nil, err
			}
		} else {
			for _, candidate := range excludedRoutes {
				r := candidate.route
				if excludedDecisionRoutes[r.ID] {
					continue
				}
				decision := &state.RouteDecision{
					VideoID:         v.ID,
					RunID:           runID,
					RouteID:         r.ID,
					EvaluationOrder: r.EvaluationOrder,
					Decision:        state.RouteDecisionSkipped,
					SkipReason:      skipReasonPtr(state.SkipReasonExclusion),
				}
				if candidate.match != nil {
					decision.ExclusionIndex = intPointer(candidate.match.Index)
					decision.ExclusionSemanticsVersion = intPointer(candidate.match.SemanticsVersion)
				}
				if _, err := p.machine.RecordRouteEvaluation(ctx, decision, nil); err != nil {
					return nil, fmt.Errorf("record exclusion decision: %w", err)
				}
			}
			p.broadcastVideo(runID, v, state.FinalOutcomeExcluded, false)
		}
	}

	return remaining, nil
}

func enabledRouteCount(routes []router.Route) int {
	n := 0
	for _, r := range routes {
		if r.IsEnabled {
			n++
		}
	}
	return n
}

type exclusionCandidate struct {
	route router.Route
	match *router.ExclusionMatchResult
}

func enabledRoutesThatExclude(routes []router.Route, vc router.VideoContext) []exclusionCandidate {
	var out []exclusionCandidate
	for _, r := range routes {
		if !r.IsEnabled {
			continue
		}
		if match, reason := router.ExclusionMatch(r, vc); reason != nil {
			out = append(out, exclusionCandidate{route: r, match: match})
		}
	}
	return out
}

func (p *Pipeline) markVideoExcluded(ctx context.Context, runID int64, v *state.Video, routes []exclusionCandidate, recorded map[int64]bool) error {
	if v.ProcessingState != state.VideoStateEvaluating {
		if _, err := p.machine.TransitionVideoState(ctx, v.ID,
			state.VideoStateNotYetEvaluated, state.VideoStateEvaluating); err != nil {
			return fmt.Errorf("transition video %d to evaluating: %w", v.ID, err)
		}
	}

	for _, candidate := range routes {
		r := candidate.route
		if recorded[r.ID] {
			continue
		}
		decision := &state.RouteDecision{
			VideoID:         v.ID,
			RunID:           runID,
			RouteID:         r.ID,
			EvaluationOrder: r.EvaluationOrder,
			Decision:        state.RouteDecisionSkipped,
			SkipReason:      skipReasonPtr(state.SkipReasonExclusion),
		}
		if candidate.match != nil {
			decision.ExclusionIndex = intPointer(candidate.match.Index)
			decision.ExclusionSemanticsVersion = intPointer(candidate.match.SemanticsVersion)
		}
		if _, err := p.machine.RecordRouteEvaluation(ctx, decision, nil); err != nil {
			return fmt.Errorf("record exclusion decision for route %d: %w", r.ID, err)
		}
	}

	if _, err := p.machine.TransitionRunVideoState(ctx, runID, v.ID,
		state.VideoStateEvaluating, state.VideoStateEvaluated, state.FinalOutcomeExcluded, nil, nil); err != nil {
		return fmt.Errorf("mark video %d excluded: %w", v.ID, err)
	}
	p.broadcastVideo(runID, v, state.FinalOutcomeExcluded, false)
	return nil
}

func intPointer(v int) *int { return &v }

func (p *Pipeline) videoContextForExclusion(ctx context.Context, v *state.Video) (router.VideoContext, error) {
	meta := &metadata.TransientMetadata{VideoID: v.VideoID}
	if p.meta != nil {
		fetched, err := p.meta.GetDisplayMetadata(ctx, v.VideoID)
		if err != nil {
			p.logger.DebugContext(ctx, "exclusion gate using degraded metadata", "diagnostic", diag.Encode(err, "exclusion", "load_metadata"))
		} else {
			meta = fetched
		}
	}
	return videoContextFromMetadata(v.VideoID, v.ChannelID, meta), nil
}

func skipReasonPtr(r state.SkipReason) *state.SkipReason {
	return &r
}
