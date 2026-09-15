package router

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bateau84/YuHaul/internal/state"
)

// Engine evaluates videos against ordered routes and records decisions.
type Engine struct {
	store *Store
}

// NewEngine returns a router engine backed by store.
func NewEngine(store *Store) *Engine {
	return &Engine{store: store}
}

// Routes returns the active route set loaded from the route store. It is
// exposed so callers such as the dry-run executor can simulate actions against
// the same definitions used for evaluation.
func (e *Engine) Routes(ctx context.Context) ([]Route, error) {
	return e.store.LoadActiveRoutes(ctx)
}

// Evaluate loads active routes and evaluates video against them. It records
// route decisions and condition audits via machine.
func (e *Engine) Evaluate(ctx context.Context, runID int64, videoID int64,
	vc VideoContext, machine *state.Machine) (*EvaluationResult, error) {

	routes, err := e.store.LoadActiveRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("router: load routes: %w", err)
	}
	return e.EvaluateWithRoutes(ctx, runID, videoID, vc, machine, routes)
}

// EvaluateWithRoutes evaluates video against an explicit set of routes. It is
// used by dry-run and backtest modes that need to evaluate against a
// configuration snapshot without mutating the active route table.
func (e *Engine) EvaluateWithRoutes(ctx context.Context, runID int64, videoID int64,
	vc VideoContext, machine *state.Machine, routes []Route) (*EvaluationResult, error) {

	result := &EvaluationResult{}
	finalMatched := false
	for _, route := range routes {
		var decision state.RouteDecision
		var audits []state.ConditionAudit
		var matched bool

		if finalMatched {
			decision = state.RouteDecision{
				EvaluationOrder: route.EvaluationOrder,
				Decision:        state.RouteDecisionSkipped,
				SkipReason:      skipPtr(state.SkipReasonFinalRouteMatched),
			}
		} else {
			decision, audits, matched = e.evaluateRoute(route, vc)
		}

		decision.VideoID = videoID
		decision.RunID = runID
		decision.RouteID = route.ID

		recorded, err := machine.RecordRouteDecision(ctx, &decision)
		if err != nil {
			return nil, fmt.Errorf("router: record decision for route %d: %w", route.ID, err)
		}

		for i := range audits {
			audits[i].RouteDecisionID = recorded.ID
			if _, err := machine.RecordConditionAudit(ctx, &audits[i]); err != nil {
				return nil, fmt.Errorf("router: record audit: %w", err)
			}
		}

		if matched {
			result.MatchedRoutes = append(result.MatchedRoutes, route.ID)
			if route.IsFinal {
				result.FinalMatched = true
				finalMatched = true
			}
		}
	}
	return result, nil
}

func (e *Engine) evaluateRoute(route Route, vc VideoContext) (state.RouteDecision, []state.ConditionAudit, bool) {
	order := route.EvaluationOrder

	if !route.IsEnabled {
		return state.RouteDecision{
			EvaluationOrder: order,
			Decision:        state.RouteDecisionSkipped,
			SkipReason:      skipPtr(state.SkipReasonDisabled),
		}, nil, false
	}

	if reason := Excluded(route, vc); reason != nil {
		return state.RouteDecision{
			EvaluationOrder: order,
			Decision:        state.RouteDecisionSkipped,
			SkipReason:      reason,
		}, nil, false
	}

	audits := make([]state.ConditionAudit, 0, len(route.Conditions))
	conditionResults := make([]bool, 0, len(route.Conditions))
	for _, cond := range route.Conditions {
		matched, actual := evaluateCondition(cond, vc)
		audits = append(audits, state.ConditionAudit{
			ConditionIndex: cond.ConditionIndex,
			Field:          string(cond.Field),
			Operator:       string(cond.Operator),
			ExpectedValue:  cond.Value,
			ActualValue:    &actual,
			Matched:        matched,
		})
		conditionResults = append(conditionResults, matched)
	}

	matched := composeResults(route.ConditionComposition, conditionResults)
	if matched {
		return state.RouteDecision{
			EvaluationOrder: order,
			Decision:        state.RouteDecisionMatched,
		}, audits, true
	}

	rejection := "no route condition matched"
	return state.RouteDecision{
		EvaluationOrder: order,
		Decision:        state.RouteDecisionNotMatched,
		RejectionReason: &rejection,
	}, audits, false
}

// Excluded returns the exclusion skip reason if the video matches any of the
// route's exclusion rules, or nil if it does not.
func Excluded(route Route, vc VideoContext) *state.SkipReason {
	for _, ex := range route.Exclusions {
		switch ex.ExclusionType {
		case ExclusionTypeChannel:
			if vc.ChannelID == ex.ExclusionValue {
				return skipPtr(state.SkipReasonExclusion)
			}
		case ExclusionTypeVideo:
			if vc.VideoID == ex.ExclusionValue {
				return skipPtr(state.SkipReasonExclusion)
			}
		case ExclusionTypeTitle:
			if strings.EqualFold(strings.TrimSpace(vc.Title), strings.TrimSpace(ex.ExclusionValue)) {
				return skipPtr(state.SkipReasonExclusion)
			}
		case ExclusionTypeWord:
			if strings.Contains(strings.ToLower(vc.Title), strings.ToLower(ex.ExclusionValue)) {
				return skipPtr(state.SkipReasonExclusion)
			}
		case ExclusionTypeDurationMax:
			if vc.DurationSeconds != nil {
				max, err := strconv.Atoi(ex.ExclusionValue)
				if err == nil && *vc.DurationSeconds > max {
					return skipPtr(state.SkipReasonExclusion)
				}
			}
		case ExclusionTypeDurationMin:
			if vc.DurationSeconds != nil {
				min, err := strconv.Atoi(ex.ExclusionValue)
				if err == nil && *vc.DurationSeconds < min {
					return skipPtr(state.SkipReasonExclusion)
				}
			}
		}
	}
	return nil
}

func evaluateCondition(c RouteCondition, vc VideoContext) (bool, string) {
	actual := conditionValue(c.Field, vc)
	switch c.Operator {
	case ConditionOperatorContains:
		return strings.Contains(strings.ToLower(actual), strings.ToLower(c.Value)), actual
	case ConditionOperatorNotContains:
		return !strings.Contains(strings.ToLower(actual), strings.ToLower(c.Value)), actual
	case ConditionOperatorEquals:
		return strings.EqualFold(actual, c.Value), actual
	case ConditionOperatorNotEquals:
		return !strings.EqualFold(actual, c.Value), actual
	case ConditionOperatorInList:
		parts := strings.Split(c.Value, ",")
		for _, p := range parts {
			if strings.EqualFold(strings.TrimSpace(p), actual) {
				return true, actual
			}
		}
		return false, actual
	case ConditionOperatorGT, ConditionOperatorGTE, ConditionOperatorLT, ConditionOperatorLTE:
		return compareNumeric(c.Operator, actual, c.Value), actual
	}
	return false, actual
}

func conditionValue(field ConditionField, vc VideoContext) string {
	switch field {
	case ConditionFieldTitle:
		return vc.Title
	case ConditionFieldChannelID:
		return vc.ChannelID
	case ConditionFieldChannelName:
		return vc.ChannelName
	case ConditionFieldDescription:
		return vc.Description
	case ConditionFieldDurationSeconds:
		if vc.DurationSeconds != nil {
			return strconv.Itoa(*vc.DurationSeconds)
		}
	}
	return ""
}

func compareNumeric(op ConditionOperator, actual, expected string) bool {
	a, err1 := strconv.ParseFloat(actual, 64)
	b, err2 := strconv.ParseFloat(expected, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	switch op {
	case ConditionOperatorGT:
		return a > b
	case ConditionOperatorGTE:
		return a >= b
	case ConditionOperatorLT:
		return a < b
	case ConditionOperatorLTE:
		return a <= b
	}
	return false
}

func composeResults(composition string, results []bool) bool {
	if len(results) == 0 {
		return false
	}
	if composition == "OR" {
		for _, r := range results {
			if r {
				return true
			}
		}
		return false
	}
	for _, r := range results {
		if !r {
			return false
		}
	}
	return true
}

func skipPtr(r state.SkipReason) *state.SkipReason {
	return &r
}
