package config

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/router"
)

// RouteStore is the subset of router.Store used by the config versioner.
type RouteStore interface {
	ListRoutes(ctx context.Context) ([]router.Route, error)
}

// ConfigStore is the subset of Store used by the config versioner. It
// includes the route_identity registry read because the public history
// serializers resolve stored snapshot route keys through it (ARS-004).
type ConfigStore interface {
	Create(ctx context.Context, snapshot Snapshot, description string) (int64, error)
	Get(ctx context.Context, id int64) (*ConfigVersion, error)
	List(ctx context.Context) ([]ConfigVersion, error)
	MatchingID(ctx context.Context, snapshot Snapshot) (*int64, error)
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	RouteIdentities(ctx context.Context) (map[int64]string, error)
}

// Service creates and queries configuration snapshots.
type Service struct {
	routeStore RouteStore
	store      ConfigStore
}

// NewService returns a config versioner backed by routeStore and store.
func NewService(routeStore RouteStore, store ConfigStore) *Service {
	return &Service{
		routeStore: routeStore,
		store:      store,
	}
}

// Snapshot builds the current effective configuration and persists it as a
// ConfigVersion. It is intended to be invoked after every route or threshold
// change.
func (s *Service) Snapshot(ctx context.Context, description string) error {
	_, err := s.snapshot(ctx, description)
	return err
}

func (s *Service) snapshot(ctx context.Context, description string) (int64, error) {
	snap, err := s.buildSnapshot(ctx)
	if err != nil {
		return 0, err
	}
	id, err := s.store.Create(ctx, snap, description)
	if err != nil {
		return 0, fmt.Errorf("config: snapshot: %w", err)
	}
	return id, nil
}

// ActiveVersionID captures the saved effective configuration, reusing an exact
// historical match when available. Explicit proposals and historical copies do
// not activate configuration merely by being appended to history.
func (s *Service) ActiveVersionID(ctx context.Context) (*int64, error) {
	snap, err := s.buildSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	id, err := s.store.MatchingID(ctx, snap)
	if err != nil {
		return nil, err
	}
	if id != nil {
		return id, nil
	}
	// Also repairs legacy history or an uncaptured saved edit without rewriting
	// any immutable version or changing an existing run's reference.
	newID, err := s.store.Create(ctx, snap, "effective configuration at run initiation")
	if err != nil {
		return nil, err
	}
	return &newID, nil
}

// Restore creates a new ConfigVersion from a historical snapshot. The new
// version persists the stored internal representation — integer route keys
// and legacy action values — unchanged; only its public HTTP representation
// projects route UUIDs (ADR-011/ARS-004).
func (s *Service) Restore(ctx context.Context, id int64, description string) (*ConfigVersion, error) {
	cv, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if description == "" {
		description = fmt.Sprintf("restored from version %d", id)
	}
	newID, err := s.store.Create(ctx, cv.Snapshot, description)
	if err != nil {
		return nil, fmt.Errorf("config: restore version %d: %w", id, err)
	}
	return s.store.Get(ctx, newID)
}

// Diff compares two ConfigVersions and returns a structural diff. The
// comparison itself runs on the stored snapshots — internal integer route
// keys and legacy action values — so public UUID projection (which happens
// at the HTTP boundary) can never fabricate or hide a route change
// (ADR-011/ARS-004).
func (s *Service) Diff(ctx context.Context, fromID, toID int64) (*DiffResult, error) {
	from, err := s.store.Get(ctx, fromID)
	if err != nil {
		return nil, fmt.Errorf("config: diff from version %d: %w", fromID, err)
	}
	to, err := s.store.Get(ctx, toID)
	if err != nil {
		return nil, fmt.Errorf("config: diff to version %d: %w", toID, err)
	}
	return diffSnapshots(fromID, toID, from.Snapshot, to.Snapshot), nil
}

// List returns all ConfigVersions.
func (s *Service) List(ctx context.Context) ([]ConfigVersion, error) {
	return s.store.List(ctx)
}

// Get returns a ConfigVersion by ID.
func (s *Service) Get(ctx context.Context, id int64) (*ConfigVersion, error) {
	return s.store.Get(ctx, id)
}

// RouteIdentities returns the complete route_identity registry as an
// internal-key → public-UUID map (ADR-011/ARS-004). It backs the public
// configuration-history serializers: a stored snapshot route key without a
// registry mapping must fail the response rather than be emitted as an
// integer or substituted by route name.
func (s *Service) RouteIdentities(ctx context.Context) (map[int64]string, error) {
	return s.store.RouteIdentities(ctx)
}

// DuplicateThreshold returns the current configured threshold, defaulting to
// the architecture-resolved value if unset.
func (s *Service) DuplicateThreshold(ctx context.Context) (float64, error) {
	v, err := s.store.GetSetting(ctx, "duplicate_threshold")
	if err != nil {
		return 0, err
	}
	if v == "" {
		return duplicate.DefaultThreshold, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return duplicate.DefaultThreshold, nil
	}
	return f, nil
}

// SetDuplicateThreshold persists a new threshold and snapshots the effective
// configuration.
func (s *Service) SetDuplicateThreshold(ctx context.Context, threshold float64) error {
	if threshold < 0.0 {
		threshold = 0.0
	}
	if threshold > 1.0 {
		threshold = 1.0
	}
	if err := s.store.SetSetting(ctx, "duplicate_threshold", strconv.FormatFloat(threshold, 'f', -1, 64)); err != nil {
		return err
	}
	return s.Snapshot(ctx, fmt.Sprintf("duplicate threshold changed to %.3f", threshold))
}

// DuplicateBehavior returns fixed continuation. Legacy settings are inert;
// historical snapshots retain their recorded strings independently.
func (s *Service) DuplicateBehavior(ctx context.Context) (string, error) {
	return "continue", nil
}

// SetDuplicateBehavior accepts legacy callers without changing settings or
// creating a configuration version. Continuation is not configurable.
func (s *Service) SetDuplicateBehavior(ctx context.Context, behavior string) error {
	return nil
}

func (s *Service) buildSnapshot(ctx context.Context) (Snapshot, error) {
	routes, err := s.routeStore.ListRoutes(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("config: list routes for snapshot: %w", err)
	}

	threshold, err := s.DuplicateThreshold(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	behavior, err := s.DuplicateBehavior(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	snap := Snapshot{
		Routes:             make([]SnapshotRoute, 0, len(routes)),
		DuplicateThreshold: threshold,
		DuplicateBehavior:  behavior,
	}
	for _, r := range routes {
		sr := SnapshotRoute{
			ID:                   r.ID,
			Name:                 r.Name,
			Description:          r.Description,
			EvaluationOrder:      r.EvaluationOrder,
			IsFinal:              r.IsFinal,
			IsEnabled:            r.IsEnabled,
			ConditionComposition: r.ConditionComposition,
			Conditions:           make([]SnapshotCondition, 0, len(r.Conditions)),
			Actions:              make([]SnapshotAction, 0, len(r.Actions)),
			Exclusions:           make([]SnapshotExclusion, 0, len(r.Exclusions)),
		}
		for _, c := range r.Conditions {
			sr.Conditions = append(sr.Conditions, SnapshotCondition{
				ConditionIndex: c.ConditionIndex,
				Field:          string(c.Field),
				Operator:       string(c.Operator),
				Value:          c.Value,
			})
		}
		for _, a := range r.Actions {
			sr.Actions = append(sr.Actions, SnapshotAction{
				ActionIndex:      a.ActionIndex,
				ActionType:       a.ActionType,
				TargetPlaylistID: a.TargetPlaylistID,
			})
		}
		for _, e := range r.Exclusions {
			sr.Exclusions = append(sr.Exclusions, SnapshotExclusion{
				ExclusionType:  string(e.ExclusionType),
				ExclusionValue: e.ExclusionValue,
			})
		}
		snap.Routes = append(snap.Routes, sr)
	}
	return snap, nil
}

func diffSnapshots(fromID, toID int64, from, to Snapshot) *DiffResult {
	result := &DiffResult{
		FromID:          fromID,
		ToID:            toID,
		SettingsChanged: make(map[string]Change),
		RoutesAdded:     make([]SnapshotRoute, 0),
		RoutesRemoved:   make([]SnapshotRoute, 0),
		RoutesChanged:   make([]RouteChange, 0),
	}

	if from.DuplicateThreshold != to.DuplicateThreshold {
		result.SettingsChanged["duplicate_threshold"] = Change{
			Old: strconv.FormatFloat(from.DuplicateThreshold, 'f', -1, 64),
			New: strconv.FormatFloat(to.DuplicateThreshold, 'f', -1, 64),
		}
	}
	if from.DuplicateBehavior != to.DuplicateBehavior {
		result.SettingsChanged["duplicate_behavior"] = Change{
			Old: from.DuplicateBehavior,
			New: to.DuplicateBehavior,
		}
	}

	fromRoutes := indexRoutes(from.Routes)
	toRoutes := indexRoutes(to.Routes)

	for id, r := range toRoutes {
		if old, ok := fromRoutes[id]; !ok {
			result.RoutesAdded = append(result.RoutesAdded, r)
		} else if !routesEqual(old, r) {
			result.RoutesChanged = append(result.RoutesChanged, makeRouteChange(old, r))
		}
	}
	for id, r := range fromRoutes {
		if _, ok := toRoutes[id]; !ok {
			result.RoutesRemoved = append(result.RoutesRemoved, r)
		}
	}

	return result
}

func indexRoutes(routes []SnapshotRoute) map[int64]SnapshotRoute {
	m := make(map[int64]SnapshotRoute, len(routes))
	for _, r := range routes {
		m[r.ID] = r
	}
	return m
}

func routesEqual(a, b SnapshotRoute) bool {
	if a.Name != b.Name || a.Description != b.Description ||
		a.EvaluationOrder != b.EvaluationOrder || a.IsFinal != b.IsFinal ||
		a.IsEnabled != b.IsEnabled || a.ConditionComposition != b.ConditionComposition {
		return false
	}
	if !slicesEqual(a.Conditions, b.Conditions) {
		return false
	}
	if !slicesEqual(a.Actions, b.Actions) {
		return false
	}
	if !slicesEqual(a.Exclusions, b.Exclusions) {
		return false
	}
	return true
}

func makeRouteChange(old, new SnapshotRoute) RouteChange {
	change := RouteChange{ID: new.ID, Old: old, New: new, Changes: make([]FieldChange, 0)}

	check := func(field, oldV, newV string) {
		if oldV != newV {
			change.Changes = append(change.Changes, FieldChange{Field: field, Old: oldV, New: newV})
		}
	}
	check("name", old.Name, new.Name)
	check("description", old.Description, new.Description)
	check("evaluation_order", strconv.Itoa(old.EvaluationOrder), strconv.Itoa(new.EvaluationOrder))
	check("is_final", strconv.FormatBool(old.IsFinal), strconv.FormatBool(new.IsFinal))
	check("is_enabled", strconv.FormatBool(old.IsEnabled), strconv.FormatBool(new.IsEnabled))
	check("condition_composition", old.ConditionComposition, new.ConditionComposition)
	if !slicesEqual(old.Conditions, new.Conditions) {
		change.Changes = append(change.Changes, FieldChange{Field: "conditions", Old: "changed", New: "changed"})
	}
	if !slicesEqual(old.Actions, new.Actions) {
		change.Changes = append(change.Changes, FieldChange{Field: "actions", Old: "changed", New: "changed"})
	}
	if !slicesEqual(old.Exclusions, new.Exclusions) {
		change.Changes = append(change.Changes, FieldChange{Field: "exclusions", Old: "changed", New: "changed"})
	}
	return change
}

func slicesEqual[S comparable](a, b []S) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
