package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// productionAuthority always reads persisted provenance, never caller assertions.
func productionAuthority(ctx context.Context, db *sql.DB, cs ConfigStore, runID int64) (*state.Run, []router.Route, error) {
	s, err := state.NewStore(db)
	if err != nil {
		return nil, nil, err
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	if (run.RunType != state.RunTypeManual && run.RunType != state.RunTypeScheduled) ||
		(run.Status != state.RunStatusRunning && run.Status != state.RunStatusInterrupted) ||
		(run.ActionAuthorityVersion != 0 && run.ActionAuthorityVersion != 1) {
		return nil, nil, fmt.Errorf("run %d has no production action authority", runID)
	}
	if cs == nil || run.ConfigVersionID == nil {
		return nil, nil, fmt.Errorf("run %d missing captured configuration", runID)
	}
	cv, err := cs.Get(ctx, *run.ConfigVersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("run %d snapshot: %w", runID, err)
	}
	routes := routesFromSnapshot(cv.Snapshot)
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].EvaluationOrder < routes[j].EvaluationOrder })
	seen := map[int64]bool{}
	for i := range routes {
		r := &routes[i]
		if r.ID <= 0 || seen[r.ID] {
			return nil, nil, fmt.Errorf("run %d invalid snapshot route %d", runID, r.ID)
		}
		seen[r.ID] = true
		sort.SliceStable(r.Conditions, func(i, j int) bool { return r.Conditions[i].ConditionIndex < r.Conditions[j].ConditionIndex })
		sort.SliceStable(r.Actions, func(i, j int) bool { return r.Actions[i].ActionIndex < r.Actions[j].ActionIndex })
	}
	return run, routes, nil
}

func (p *Pipeline) productionRoutes(ctx context.Context, runID int64) ([]router.Route, error) {
	_, routes, err := productionAuthority(ctx, p.db, p.configStore, runID)
	return routes, err
}

type authorityError struct{ error }

// admitLegacy is read-only, including metadata access. Missing input is not a
// negative predicate. Native evidence deliberately does not reread metadata.
func admitLegacy(ctx context.Context, db *sql.DB, run *state.Run, videoID int64, routes []router.Route, complete bool) error {
	_, err := checkedLegacyEvaluation(ctx, db, run, videoID, routes, complete)
	return err
}

func checkedLegacyEvaluation(ctx context.Context, db *sql.DB, run *state.Run, videoID int64, routes []router.Route, complete bool) (_ []router.ExpectedEvaluation, err error) {
	defer func() {
		if err != nil {
			err = &authorityError{err}
		}
	}()
	if run.ActionAuthorityVersion == 1 {
		return nil, nil
	}
	s, err := state.NewStore(db)
	if err != nil {
		return nil, err
	}
	v, err := s.GetVideo(ctx, videoID)
	if err != nil {
		return nil, err
	}
	m, metaErr := metadata.NewStore(db).Get(ctx, v.VideoID)
	available := metaErr == nil && m.ExpiredAt == nil && m.ExpiresAt.After(time.Now().UTC())
	if !available {
		m = &metadata.TransientMetadata{}
	}
	need := func(field string) error {
		ok := false
		switch field {
		case "channel_id":
			ok = v.ChannelID != ""
		case "title":
			ok = available && m.Title != nil
		case "channel_name":
			ok = available && m.ChannelName != nil
		case "description":
			ok = available && m.Description != nil
		case "duration_seconds":
			ok = available && m.DurationSeconds != nil
		}
		if !ok {
			return fmt.Errorf("legacy run %d video %d snapshot input unavailable: %s", run.ID, videoID, field)
		}
		return nil
	}
	for _, r := range routes {
		if !r.IsEnabled {
			continue
		}
		for _, ex := range r.Exclusions {
			var field string
			switch ex.ExclusionType {
			case router.ExclusionTypeChannel, router.ExclusionTypeVideo:
				continue
			case router.ExclusionTypeTitle, router.ExclusionTypeWord:
				field = "title"
			case router.ExclusionTypeDurationMin, router.ExclusionTypeDurationMax:
				field = "duration_seconds"
			default:
				return nil, fmt.Errorf("unsupported snapshot exclusion for route %d", r.ID)
			}
			if err := need(field); err != nil {
				return nil, err
			}
		}
	}
	expected := router.NewEngine(nil).ExpectedEvaluations(run.ID, videoID, videoContextFromMetadata(v.VideoID, v.ChannelID, m), routes)
	byRoute := map[int64]router.ExpectedEvaluation{}
	for _, e := range expected {
		r := findRoute(routes, e.Decision.RouteID)
		if len(e.Audits) > 0 && r.ConditionComposition != "AND" && r.ConditionComposition != "OR" {
			return nil, fmt.Errorf("legacy route %d unsupported snapshot composition", e.Decision.RouteID)
		}
		seenConditions := map[int]bool{}
		for _, a := range e.Audits {
			if seenConditions[a.ConditionIndex] {
				return nil, fmt.Errorf("legacy route %d duplicate snapshot condition index %d", e.Decision.RouteID, a.ConditionIndex)
			}
			seenConditions[a.ConditionIndex] = true
			switch router.ConditionOperator(a.Operator) {
			case router.ConditionOperatorContains, router.ConditionOperatorEquals, router.ConditionOperatorStartsWith, router.ConditionOperatorEndsWith, router.ConditionOperatorMatchesRegex, router.ConditionOperatorGreaterThan, router.ConditionOperatorLessThan, router.ConditionOperatorBetween:
			default:
				return nil, fmt.Errorf("legacy route %d unsupported snapshot operator", e.Decision.RouteID)
			}
			if err := need(a.Field); err != nil {
				return nil, err
			}
		}
		byRoute[e.Decision.RouteID] = e
	}
	actual, err := s.GetRouteDecisionsForVideoInRun(ctx, videoID, run.ID)
	if err != nil {
		return nil, err
	}
	if complete && len(actual) != len(expected) {
		return nil, fmt.Errorf("legacy run %d video %d incomplete snapshot evaluation", run.ID, videoID)
	}
	for _, d := range actual {
		e, ok := byRoute[d.RouteID]
		want := e.Decision
		want.ID = d.ID
		if !ok || !reflect.DeepEqual(d, want) {
			return nil, fmt.Errorf("legacy run %d video %d decision %d route %d snapshot mismatch", run.ID, videoID, d.ID, d.RouteID)
		}
		audits, err := s.GetConditionAuditsForDecision(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		if len(audits) != len(e.Audits) {
			return nil, fmt.Errorf("legacy decision %d route %d snapshot audit count mismatch", d.ID, d.RouteID)
		}
		for i, a := range audits {
			want := e.Audits[i]
			want.ID = a.ID
			want.RouteDecisionID = d.ID
			if !reflect.DeepEqual(a, want) {
				return nil, fmt.Errorf("legacy decision %d route %d snapshot audit mismatch", d.ID, d.RouteID)
			}
		}
	}
	return expected, nil
}

func (p *Pipeline) completeLegacyEvaluation(ctx context.Context, run *state.Run, v *state.Video, routes []router.Route) (*router.EvaluationResult, error) {
	expected, err := checkedLegacyEvaluation(ctx, p.db, run, v.ID, routes, false)
	if err != nil {
		return nil, err
	}
	result := &router.EvaluationResult{}
	for _, e := range expected {
		if _, err := p.machine.RecordRouteEvaluation(ctx, &e.Decision, e.Audits); err != nil {
			return nil, err
		}
		if e.Decision.Decision == state.RouteDecisionMatched {
			result.MatchedRoutes = append(result.MatchedRoutes, e.Decision.RouteID)
		}
	}
	if err := admitLegacy(ctx, p.db, run, v.ID, routes, true); err != nil {
		return nil, err
	}
	return result, nil
}

// Keep the checkpoint resumable until queue integration can atomically hand off
// owned groups. Retain all candidates, not only the first conflicting video.
func (p *Pipeline) retainAuthorityFailure(ctx context.Context, runID int64, videos []*state.Video, cause error) {
	qs, err := queue.NewStore(p.db)
	if err != nil {
		p.logger.ErrorContext(ctx, "open authority retention store", "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, cause.Error())
		return
	}
	err = qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		for _, v := range videos {
			if v.ProcessingState == state.VideoStateCompleted {
				continue
			}
			if _, err := qs.RetainRuntimeErrorTx(ctx, tx, v.ID, runID, "snapshot action authority failure", cause.Error()); err != nil {
				return err
			}
			if err := qs.AcknowledgeRetryGroupTx(ctx, tx, v.ID, runID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		p.logger.ErrorContext(ctx, "retain authority failure", "run_id", runID, "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, cause.Error())
		return
	}
	if _, err := p.machine.FailRun(ctx, runID, cause.Error()); err != nil {
		p.logger.ErrorContext(ctx, "fail retained authority run", "run_id", runID, "error", err)
		_ = p.stateStore.InterruptRun(ctx, runID, cause.Error())
	}
	p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: cause.Error()}))
}
