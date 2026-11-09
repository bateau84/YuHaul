package pipeline

import (
	"context"
	"fmt"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// ValidateRunRoutes checks the exact ordered effective routes against the
// persisted run's snapshot. Callers must not mutate routes during validation
// or subsequent evaluation.
func (p *Pipeline) ValidateRunRoutes(ctx context.Context, runID int64, routes []router.Route) error {
	s, err := state.NewStore(p.db)
	if err != nil {
		return err
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	var expected []router.Route
	switch run.RunType {
	case state.RunTypeManual, state.RunTypeScheduled:
		_, expected, err = productionAuthority(ctx, p.db, p.configStore, runID)
	case state.RunTypeDryRun, state.RunTypeReplay, state.RunTypeBacktest:
		expected, err = p.dryRunRoutes(ctx, run)
	default:
		return fmt.Errorf("run %d has unsupported type", runID)
	}
	if err != nil {
		return err
	}
	if len(routes) != len(expected) {
		return fmt.Errorf("run %d route definition mismatch", runID)
	}
	for i := range routes {
		if !sameEffectiveRoute(routes[i], expected[i]) {
			return fmt.Errorf("run %d route definition mismatch at position %d", runID, i)
		}
	}
	return nil
}

func sameEffectiveRoute(a, b router.Route) bool {
	if a.ID != b.ID || a.EvaluationOrder != b.EvaluationOrder || a.IsEnabled != b.IsEnabled ||
		a.IsFinal != b.IsFinal || a.ConditionComposition != b.ConditionComposition ||
		len(a.Conditions) != len(b.Conditions) || len(a.Actions) != len(b.Actions) || len(a.Exclusions) != len(b.Exclusions) {
		return false
	}
	for i, c := range a.Conditions {
		d := b.Conditions[i]
		if c.ConditionIndex != d.ConditionIndex || c.Field != d.Field || c.Operator != d.Operator || c.Value != d.Value {
			return false
		}
	}
	for i, action := range a.Actions {
		other := b.Actions[i]
		if action.ActionIndex != other.ActionIndex || action.ActionType != other.ActionType || action.TargetPlaylistID != other.TargetPlaylistID {
			return false
		}
	}
	for i, exclusion := range a.Exclusions {
		other := b.Exclusions[i]
		if exclusion.ExclusionType != other.ExclusionType || exclusion.ExclusionValue != other.ExclusionValue {
			return false
		}
	}
	return true
}
