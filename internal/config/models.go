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

// Snapshot is the JSON-serializable recorded configuration. New effective
// saved/proposal captures use DuplicateBehavior "continue"; history reads,
// diffs, and restore copies preserve arbitrary recorded legacy strings.
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

// ProposalSnapshot is the public request shape of a dry-run draft
// (`draft_configuration`) or backtest proposed configuration
// (`proposed_configuration`) (ADR-012/ARS-004). Route IDs are the routes'
// stable public UUIDv4 identities, not internal integer keys: the
// proposal-ingest transaction translates each UUID through the
// route_identity registry before the immutable ConfigVersion snapshot is
// stored, so the stored snapshot keeps internal integer route keys exactly
// like every other snapshot (OC-003).
// DuplicateBehavior accepts legacy strings but is inert; ingest captures the
// fixed effective value "continue" rather than the submitted string.
type ProposalSnapshot struct {
	Routes             []ProposalRoute `json:"routes"`
	DuplicateThreshold float64         `json:"duplicate_threshold"`
	DuplicateBehavior  string          `json:"duplicate_behavior"`
}

// ProposalRoute is one route inside a proposal configuration. ID is the
// canonical lowercase public UUIDv4; the remaining fields are the full route
// definition this proposal evaluates (name, conditions, actions, exclusions,
// enabled state, and evaluation order), which the stored snapshot preserves
// verbatim after ID translation.
type ProposalRoute struct {
	ID                   string              `json:"id"`
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

// PublicConfigVersion is the public API representation of a configuration
// version (ADR-011/ARS-004). ConfigVersion identifiers such as `id` keep
// their existing integer type — they identify configuration versions, not
// routes — while route identities inside the snapshot are stable public
// UUIDs resolved through the route_identity registry.
type PublicConfigVersion struct {
	ID                int64          `json:"id"`
	CreatedAt         time.Time      `json:"created_at"`
	Snapshot          PublicSnapshot `json:"snapshot"`
	ChangeDescription string         `json:"change_description"`
}

// PublicSnapshot is the public representation of a recorded configuration,
// including its unchanged historical DuplicateBehavior string.
type PublicSnapshot struct {
	Routes             []PublicSnapshotRoute `json:"routes"`
	DuplicateThreshold float64               `json:"duplicate_threshold"`
	DuplicateBehavior  string                `json:"duplicate_behavior"`
}

// PublicSnapshotRoute is the public representation of one route in a
// configuration snapshot. `id` is the route's stable public UUIDv4; the
// stored snapshot keeps the internal integer route key (ADR-011/ARS-004).
// Name is a display label only: it is not unique and never identifies a
// route.
type PublicSnapshotRoute struct {
	ID                   string              `json:"id"`
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

// PublicDiffResult is the public representation of a snapshot diff.
// `from_id` and `to_id` remain ConfigVersion identifiers; route identities
// inside the compared snapshots are public UUIDs.
type PublicDiffResult struct {
	FromID          int64                 `json:"from_id"`
	ToID            int64                 `json:"to_id"`
	SettingsChanged map[string]Change     `json:"settings_changed"`
	RoutesAdded     []PublicSnapshotRoute `json:"routes_added"`
	RoutesRemoved   []PublicSnapshotRoute `json:"routes_removed"`
	RoutesChanged   []PublicRouteChange   `json:"routes_changed"`
}

// PublicRouteChange is the public representation of one route that changed
// between two snapshots; `id` and the nested route IDs are public UUIDs.
type PublicRouteChange struct {
	ID      string              `json:"id"`
	Old     PublicSnapshotRoute `json:"old"`
	New     PublicSnapshotRoute `json:"new"`
	Changes []FieldChange       `json:"changes"`
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
