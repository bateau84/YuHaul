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
// The canonical vocabulary matches the accepted design spec: text fields use
// contains, equals, starts_with, ends_with, matches_regex; duration_seconds
// uses equals, greater_than, less_than, between.
type ConditionOperator string

const (
	ConditionOperatorContains     ConditionOperator = "contains"
	ConditionOperatorEquals       ConditionOperator = "equals"
	ConditionOperatorStartsWith   ConditionOperator = "starts_with"
	ConditionOperatorEndsWith     ConditionOperator = "ends_with"
	ConditionOperatorMatchesRegex ConditionOperator = "matches_regex"
	ConditionOperatorGreaterThan  ConditionOperator = "greater_than"
	ConditionOperatorLessThan     ConditionOperator = "less_than"
	ConditionOperatorBetween      ConditionOperator = "between"
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
	ID                   int64            `json:"id"`
	Name                 string           `json:"name"`
	Description          string           `json:"description"`
	EvaluationOrder      int              `json:"evaluation_order"`
	IsFinal              bool             `json:"is_final"`
	IsEnabled            bool             `json:"is_enabled"`
	ConditionComposition string           `json:"condition_composition"` // "AND" or "OR"
	Conditions           []RouteCondition `json:"conditions"`
	Actions              []RouteAction    `json:"actions"`
	Exclusions           []RouteExclusion `json:"exclusions"`
	MatchCount           int64            `json:"match_count"`
	CreatedAt            string           `json:"created_at"`
	UpdatedAt            string           `json:"updated_at"`
	DeletedAt            string           `json:"deleted_at,omitempty"`
}

// RouteCondition is one predicate within a route.
type RouteCondition struct {
	ID             int64             `json:"id"`
	RouteID        int64             `json:"route_id"`
	ConditionIndex int               `json:"condition_index"`
	Field          ConditionField    `json:"field"`
	Operator       ConditionOperator `json:"operator"`
	Value          string            `json:"value"`
}

// RouteAction describes what to do when a route matches.
type RouteAction struct {
	ID               int64  `json:"id"`
	RouteID          int64  `json:"route_id"`
	ActionType       string `json:"action_type"`
	TargetPlaylistID string `json:"target_playlist_id"`
	ActionIndex      int    `json:"action_index"`
}

// RouteExclusion is a route-scoped exclusion rule.
type RouteExclusion struct {
	ID             int64         `json:"id"`
	RouteID        int64         `json:"route_id"`
	ExclusionType  ExclusionType `json:"exclusion_type"`
	ExclusionValue string        `json:"exclusion_value"`
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
