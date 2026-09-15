// Package config implements YuHaul's configuration versioning.
//
// Every change to route configuration or effective-configuration settings
// (duplicate threshold/behavior) persists a full JSON snapshot in
// ConfigVersion. Each Run records the active config_version_id so historical
// runs can be interpreted against the configuration that was active at the time.
package config

import "time"

// ConfigVersion is a persisted full snapshot of the effective configuration.
type ConfigVersion struct {
	ID                int64     `json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	Snapshot          Snapshot  `json:"snapshot"`
	ChangeDescription string    `json:"change_description"`
}

// Snapshot is the JSON-serializable effective configuration.
type Snapshot struct {
	Routes             []SnapshotRoute `json:"routes"`
	DuplicateThreshold float64         `json:"duplicate_threshold"`
	DuplicateBehavior  string          `json:"duplicate_behavior"`
}

// SnapshotRoute is the serializable form of a route and its children.
type SnapshotRoute struct {
	ID                   int64               `json:"id"`
	Name                 string              `json:"name"`
	Description          string              `json:"description,omitempty"`
	EvaluationOrder      int                 `json:"evaluation_order"`
	IsFinal              bool                `json:"is_final"`
	IsEnabled            bool                `json:"is_enabled"`
	ConditionComposition string              `json:"condition_composition"`
	Conditions           []SnapshotCondition `json:"conditions"`
	Actions              []SnapshotAction    `json:"actions"`
	Exclusions           []SnapshotExclusion `json:"exclusions"`
}

// SnapshotCondition is the serializable form of a route condition.
type SnapshotCondition struct {
	ConditionIndex int    `json:"condition_index"`
	Field          string `json:"field"`
	Operator       string `json:"operator"`
	Value          string `json:"value"`
}

// SnapshotAction is the serializable form of a route action.
type SnapshotAction struct {
	ActionIndex      int    `json:"action_index"`
	ActionType       string `json:"action_type"`
	TargetPlaylistID string `json:"target_playlist_id"`
}

// SnapshotExclusion is the serializable form of a route exclusion.
type SnapshotExclusion struct {
	ExclusionType  string `json:"exclusion_type"`
	ExclusionValue string `json:"exclusion_value"`
}

// DiffResult describes the differences between two configuration snapshots.
type DiffResult struct {
	FromID          int64             `json:"from_id"`
	ToID            int64             `json:"to_id"`
	SettingsChanged map[string]Change `json:"settings_changed"`
	RoutesAdded     []SnapshotRoute   `json:"routes_added"`
	RoutesRemoved   []SnapshotRoute   `json:"routes_removed"`
	RoutesChanged   []RouteChange     `json:"routes_changed"`
}

// Change is a single-field change.
type Change struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// RouteChange describes a route that changed between two snapshots.
type RouteChange struct {
	ID      int64         `json:"id"`
	Old     SnapshotRoute `json:"old"`
	New     SnapshotRoute `json:"new"`
	Changes []FieldChange `json:"changes"`
}

// FieldChange describes a changed scalar field within a route.
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}
