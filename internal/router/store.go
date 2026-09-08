package router

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store persists route configuration.
type Store struct {
	db *sql.DB
}

// NewStore returns a route store backed by db. Tables are assumed to be created
// by the state store migration, which owns the shared schema.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// CreateRoute persists a new route, its conditions, actions, and exclusions.
func (s *Store) CreateRoute(ctx context.Context, r *Route) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("router: begin create route: %w", err)
	}
	defer tx.Rollback()

	now := nowUTC()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO route (name, description, evaluation_order, is_final, is_enabled,
		                   condition_composition, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
		r.Name, r.Description, r.EvaluationOrder, r.IsFinal, r.IsEnabled,
		r.ConditionComposition, now, now)
	if err != nil {
		return 0, fmt.Errorf("router: insert route: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("router: read route id: %w", err)
	}
	r.ID = id

	if err := s.saveRouteChildren(ctx, tx, r); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("router: commit create route: %w", err)
	}
	return id, nil
}

// GetRoute loads a route with its conditions, actions, and exclusions.
func (s *Store) GetRoute(ctx context.Context, id int64) (*Route, error) {
	r, err := scanRoute(s.db.QueryRowContext(ctx,
		`SELECT id, name, description, evaluation_order, is_final, is_enabled,
		        condition_composition, created_at, updated_at
		 FROM route WHERE id = ?;`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("route %d: %w", id, ErrRouteNotFound)
		}
		return nil, fmt.Errorf("router: load route %d: %w", id, err)
	}
	if err := s.loadRouteChildren(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// ListRoutes returns all routes ordered by evaluation_order, with children loaded.
func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, evaluation_order, is_final, is_enabled,
		        condition_composition, created_at, updated_at
		 FROM route ORDER BY evaluation_order;`)
	if err != nil {
		return nil, fmt.Errorf("router: list routes: %w", err)
	}
	defer rows.Close()

	var routes []Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, fmt.Errorf("router: scan route: %w", err)
		}
		routes = append(routes, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: iterate routes: %w", err)
	}

	for i := range routes {
		if err := s.loadRouteChildren(ctx, &routes[i]); err != nil {
			return nil, err
		}
	}
	return routes, nil
}

// LoadActiveRoutes loads all routes with children, ordered by evaluation_order.
// Disabled routes are included so the engine can record skip decisions for them.
func (s *Store) LoadActiveRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, evaluation_order, is_final, is_enabled,
		        condition_composition, created_at, updated_at
		 FROM route ORDER BY evaluation_order;`)
	if err != nil {
		return nil, fmt.Errorf("router: load active routes: %w", err)
	}
	defer rows.Close()

	var routes []Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, fmt.Errorf("router: scan route: %w", err)
		}
		routes = append(routes, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: iterate routes: %w", err)
	}

	for i := range routes {
		if err := s.loadRouteChildren(ctx, &routes[i]); err != nil {
			return nil, err
		}
	}
	return routes, nil
}

// UpdateRoute updates a route and replaces its children.
func (s *Store) UpdateRoute(ctx context.Context, r *Route) error {
	if r.ID == 0 {
		return fmt.Errorf("%w: route id is required", ErrInvalidRoute)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("router: begin update route: %w", err)
	}
	defer tx.Rollback()

	now := nowUTC()
	res, err := tx.ExecContext(ctx,
		`UPDATE route SET name = ?, description = ?, evaluation_order = ?, is_final = ?,
		                  is_enabled = ?, condition_composition = ?, updated_at = ?
		 WHERE id = ?;`,
		r.Name, r.Description, r.EvaluationOrder, r.IsFinal, r.IsEnabled,
		r.ConditionComposition, now, r.ID)
	if err != nil {
		return fmt.Errorf("router: update route %d: %w", r.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("route %d: %w", r.ID, ErrRouteNotFound)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM route_condition WHERE route_id = ?;`, r.ID); err != nil {
		return fmt.Errorf("router: delete route conditions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM route_action WHERE route_id = ?;`, r.ID); err != nil {
		return fmt.Errorf("router: delete route actions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM route_exclusion WHERE route_id = ?;`, r.ID); err != nil {
		return fmt.Errorf("router: delete route exclusions: %w", err)
	}

	if err := s.saveRouteChildren(ctx, tx, r); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("router: commit update route: %w", err)
	}
	return nil
}

// DeleteRoute removes a route and its children.
func (s *Store) DeleteRoute(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM route WHERE id = ?;`, id)
	if err != nil {
		return fmt.Errorf("router: delete route %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("route %d: %w", id, ErrRouteNotFound)
	}
	return nil
}

// ReorderRoutes updates evaluation_order for the given route IDs. The slice
// order becomes the new evaluation order.
func (s *Store) ReorderRoutes(ctx context.Context, routeIDs []int64) error {
	if len(routeIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("router: begin reorder: %w", err)
	}
	defer tx.Rollback()

	// Avoid UNIQUE constraint collisions by shifting existing orders negative.
	if _, err := tx.ExecContext(ctx,
		`UPDATE route SET evaluation_order = -evaluation_order - 1;`); err != nil {
		return fmt.Errorf("router: shift orders: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE route SET evaluation_order = ? WHERE id = ?;`)
	if err != nil {
		return fmt.Errorf("router: prepare reorder: %w", err)
	}
	defer stmt.Close()

	for i, id := range routeIDs {
		if _, err := stmt.ExecContext(ctx, i+1, id); err != nil {
			return fmt.Errorf("router: reorder route %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("router: commit reorder: %w", err)
	}
	return nil
}

func (s *Store) saveRouteChildren(ctx context.Context, tx *sql.Tx, r *Route) error {
	for _, c := range r.Conditions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO route_condition (route_id, condition_index, field, operator, value)
			 VALUES (?, ?, ?, ?, ?);`,
			r.ID, c.ConditionIndex, c.Field, c.Operator, c.Value); err != nil {
			return fmt.Errorf("router: insert condition: %w", err)
		}
	}
	for _, a := range r.Actions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
			 VALUES (?, ?, ?, ?);`,
			r.ID, a.ActionType, a.TargetPlaylistID, a.ActionIndex); err != nil {
			return fmt.Errorf("router: insert action: %w", err)
		}
	}
	for _, e := range r.Exclusions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO route_exclusion (route_id, exclusion_type, exclusion_value)
			 VALUES (?, ?, ?);`,
			r.ID, e.ExclusionType, e.ExclusionValue); err != nil {
			return fmt.Errorf("router: insert exclusion: %w", err)
		}
	}
	return nil
}

func (s *Store) loadRouteChildren(ctx context.Context, r *Route) error {
	conds, err := s.listConditions(ctx, r.ID)
	if err != nil {
		return err
	}
	r.Conditions = conds

	acts, err := s.listActions(ctx, r.ID)
	if err != nil {
		return err
	}
	r.Actions = acts

	excls, err := s.listExclusions(ctx, r.ID)
	if err != nil {
		return err
	}
	r.Exclusions = excls
	return nil
}

func (s *Store) listConditions(ctx context.Context, routeID int64) ([]RouteCondition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, route_id, condition_index, field, operator, value
		 FROM route_condition WHERE route_id = ? ORDER BY condition_index;`, routeID)
	if err != nil {
		return nil, fmt.Errorf("router: list conditions %d: %w", routeID, err)
	}
	defer rows.Close()

	var out []RouteCondition
	for rows.Next() {
		c, err := scanCondition(rows)
		if err != nil {
			return nil, fmt.Errorf("router: scan condition: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: iterate conditions: %w", err)
	}
	return out, nil
}

func (s *Store) listActions(ctx context.Context, routeID int64) ([]RouteAction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, route_id, action_type, target_playlist_id, action_index
		 FROM route_action WHERE route_id = ? ORDER BY action_index;`, routeID)
	if err != nil {
		return nil, fmt.Errorf("router: list actions %d: %w", routeID, err)
	}
	defer rows.Close()

	var out []RouteAction
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, fmt.Errorf("router: scan action: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: iterate actions: %w", err)
	}
	return out, nil
}

func (s *Store) listExclusions(ctx context.Context, routeID int64) ([]RouteExclusion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, route_id, exclusion_type, exclusion_value
		 FROM route_exclusion WHERE route_id = ?;`, routeID)
	if err != nil {
		return nil, fmt.Errorf("router: list exclusions %d: %w", routeID, err)
	}
	defer rows.Close()

	var out []RouteExclusion
	for rows.Next() {
		e, err := scanExclusion(rows)
		if err != nil {
			return nil, fmt.Errorf("router: scan exclusion: %w", err)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("router: iterate exclusions: %w", err)
	}
	return out, nil
}

func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func scanRoute(sc scanner) (*Route, error) {
	r := &Route{}
	var desc sql.NullString
	err := sc.Scan(&r.ID, &r.Name, &desc, &r.EvaluationOrder, &r.IsFinal,
		&r.IsEnabled, &r.ConditionComposition, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		r.Description = desc.String
	}
	return r, nil
}

func scanCondition(sc scanner) (*RouteCondition, error) {
	c := &RouteCondition{}
	var field, operator string
	err := sc.Scan(&c.ID, &c.RouteID, &c.ConditionIndex, &field, &operator, &c.Value)
	if err != nil {
		return nil, err
	}
	c.Field = ConditionField(field)
	c.Operator = ConditionOperator(operator)
	return c, nil
}

func scanAction(sc scanner) (*RouteAction, error) {
	a := &RouteAction{}
	err := sc.Scan(&a.ID, &a.RouteID, &a.ActionType, &a.TargetPlaylistID, &a.ActionIndex)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func scanExclusion(sc scanner) (*RouteExclusion, error) {
	e := &RouteExclusion{}
	var exType string
	err := sc.Scan(&e.ID, &e.RouteID, &exType, &e.ExclusionValue)
	if err != nil {
		return nil, err
	}
	e.ExclusionType = ExclusionType(exType)
	return e, nil
}

type scanner interface {
	Scan(dest ...any) error
}

// compile-time interface check.
var _ scanner = (*sql.Row)(nil)
