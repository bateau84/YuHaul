// Package router implements YuHaul's ordered route evaluation engine.
//
// The engine evaluates discovered videos against user-defined routes in
// evaluation_order. Each route has route-scoped exclusions, a set of conditions
// composed with AND/OR, and actions to execute on match. A route marked
// is_final terminates evaluation for that video after a successful match.
// Every route evaluation produces a RouteDecision and a ConditionAudit trail.
package router

import "errors"

// Composition defines how a route's conditions are combined.
type Composition string

const (
	// CompositionAll requires every condition to match (AND).
	CompositionAll Composition = "AND"
	// CompositionAny requires at least one condition to match (OR).
	CompositionAny Composition = "OR"
)

// ConditionField names the video metadata fields that can be used in route
// conditions.
type ConditionField string

const (
	ConditionFieldTitle           ConditionField = "title"
	ConditionFieldChannelID       ConditionField = "channel_id"
	ConditionFieldChannelName     ConditionField = "channel_name"
	ConditionFieldDescription     ConditionField = "description"
	ConditionFieldDurationSeconds ConditionField = "duration_seconds"
)

// ConditionOperator names the comparison operators supported in conditions.
type ConditionOperator string

const (
	ConditionOperatorContains    ConditionOperator = "contains"
	ConditionOperatorNotContains ConditionOperator = "not_contains"
	ConditionOperatorEquals      ConditionOperator = "equals"
	ConditionOperatorNotEquals   ConditionOperator = "not_equals"
	ConditionOperatorGT          ConditionOperator = "gt"
	ConditionOperatorGTE         ConditionOperator = "gte"
	ConditionOperatorLT          ConditionOperator = "lt"
	ConditionOperatorLTE         ConditionOperator = "lte"
	ConditionOperatorInList      ConditionOperator = "in_list"
)

// ExclusionType names the kinds of route-scoped exclusions.
type ExclusionType string

const (
	ExclusionTypeChannel     ExclusionType = "channel"
	ExclusionTypeVideo       ExclusionType = "video"
	ExclusionTypeTitle       ExclusionType = "title"
	ExclusionTypeWord        ExclusionType = "word"
	ExclusionTypeDurationMax ExclusionType = "duration_max"
	ExclusionTypeDurationMin ExclusionType = "duration_min"
)

// Route is a single ordered routing rule.
type Route struct {
	ID                   int64
	Name                 string
	Description          string
	EvaluationOrder      int
	IsFinal              bool
	IsEnabled            bool
	ConditionComposition string // "AND" or "OR"
	Conditions           []RouteCondition
	Actions              []RouteAction
	Exclusions           []RouteExclusion
	CreatedAt            string
	UpdatedAt            string
}

// RouteCondition is one predicate within a route.
type RouteCondition struct {
	ID             int64
	RouteID        int64
	ConditionIndex int
	Field          ConditionField
	Operator       ConditionOperator
	Value          string
}

// RouteAction describes what to do when a route matches.
type RouteAction struct {
	ID               int64
	RouteID          int64
	ActionType       string
	TargetPlaylistID string
	ActionIndex      int
}

// RouteExclusion is a route-scoped exclusion rule.
type RouteExclusion struct {
	ID             int64
	RouteID        int64
	ExclusionType  ExclusionType
	ExclusionValue string
}

// VideoContext is the data available when evaluating a video.
type VideoContext struct {
	VideoID         string
	ChannelID       string
	Title           string
	ChannelName     string
	Description     string
	DurationSeconds *int
}

// EvaluationResult is the outcome of evaluating one video against all routes.
type EvaluationResult struct {
	MatchedRoutes []int64 // route IDs that matched
	FinalMatched  bool    // true if a final route matched
}

// Common errors.
var (
	ErrRouteNotFound = errors.New("router: route not found")
	ErrInvalidRoute  = errors.New("router: invalid route")
)
