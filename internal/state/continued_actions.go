package state

import (
	"context"
	"fmt"
)

// ContinuedAction is an executed action a run made for a video under a route
// decision that belongs to another run (BR-039 partly done video; ARS-006
// §8.1). DecisionRunID is the run that made the decision and RouteID is its
// internal route key.
type ContinuedAction struct {
	ExecutedAction
	DecisionRunID int64
	RouteID       int64
}

type extraScan struct {
	sc    interface{ Scan(dest ...any) error }
	extra []any
}

func (e extraScan) Scan(dest ...any) error { return e.sc.Scan(append(dest, e.extra...)...) }

// ListContinuedActions returns the actions runID attempted for videoID under
// route decisions made by other runs, ordered by action id.
func (s *Store) ListContinuedActions(ctx context.Context, runID, videoID int64) ([]ContinuedAction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ea.id, ea.route_decision_id, ea.run_id, ea.video_id, ea.action_type,
		        ea.target_playlist_id, ea.outcome, ea.outcome_reason, ea.error_message,
		        ea.executed_at, rd.run_id, rd.route_id
		 FROM executed_action ea JOIN route_decision rd ON rd.id = ea.route_decision_id
		 WHERE ea.run_id = ? AND ea.video_id = ? AND rd.run_id <> ?
		 ORDER BY ea.id;`, runID, videoID, runID)
	if err != nil {
		return nil, fmt.Errorf("querying continued actions: %w", err)
	}
	defer rows.Close()

	out := make([]ContinuedAction, 0)
	for rows.Next() {
		var c ContinuedAction
		a, err := scanExecutedAction(extraScan{sc: rows, extra: []any{&c.DecisionRunID, &c.RouteID}})
		if err != nil {
			return nil, fmt.Errorf("scanning continued action: %w", err)
		}
		c.ExecutedAction = *a
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating continued actions: %w", err)
	}
	return out, nil
}
