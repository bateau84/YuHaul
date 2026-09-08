package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store persists and queries the three-tier outcome model.
type Store struct {
	db *sql.DB
}

// NewStore opens the state store on db, creating required tables if they do
// not already exist. The caller retains ownership of db.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("state: db is required")
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("state: migrating store: %w", err)
	}
	return s, nil
}

// migrate creates the tables required by the state machine. It is idempotent.
func (s *Store) migrate(ctx context.Context) error {
	// PRAGMA foreign_keys must be enabled per connection for SQLite. We enable
	// it here; for connection pools it should be set via _fk=true on the DSN.
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS config_version (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP NOT NULL,
			snapshot TEXT NOT NULL,
			change_description TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`INSERT OR IGNORE INTO settings (key, value) VALUES ('duplicate_threshold', '0.5');`,
		`INSERT OR IGNORE INTO settings (key, value) VALUES ('duplicate_behavior', 'skip');`,
		`CREATE TABLE IF NOT EXISTS route (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			evaluation_order INTEGER NOT NULL UNIQUE,
			is_final BOOLEAN DEFAULT false,
			is_enabled BOOLEAN DEFAULT true,
			condition_composition TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS run (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP,
			run_type TEXT NOT NULL,
			status TEXT NOT NULL,
			config_version_id INTEGER REFERENCES config_version(id),
			videos_discovered INTEGER DEFAULT 0,
			videos_accepted INTEGER DEFAULT 0,
			videos_rejected INTEGER DEFAULT 0,
			videos_deferred INTEGER DEFAULT 0,
			videos_failed INTEGER DEFAULT 0,
			videos_not_yet_evaluated INTEGER DEFAULT 0,
			quota_cost INTEGER DEFAULT 0,
			duration_seconds REAL,
			interrupt_reason TEXT,
			current_phase TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS video (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id TEXT NOT NULL UNIQUE,
			channel_id TEXT NOT NULL,
			discovered_at TIMESTAMP NOT NULL,
			discovered_in_run_id INTEGER NOT NULL REFERENCES run(id),
			processing_state TEXT NOT NULL,
			final_outcome TEXT NOT NULL,
			deferred_reason TEXT,
			error_message TEXT,
			completed_at TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_video_run ON video(discovered_in_run_id);`,
		`CREATE INDEX IF NOT EXISTS idx_video_state ON video(processing_state);`,
		`CREATE TABLE IF NOT EXISTS route_decision (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id INTEGER NOT NULL REFERENCES video(id),
			run_id INTEGER NOT NULL REFERENCES run(id),
			route_id INTEGER NOT NULL REFERENCES route(id),
			evaluation_order INTEGER NOT NULL,
			decision TEXT NOT NULL,
			skip_reason TEXT,
			rejection_reason TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_route_decision_video ON route_decision(video_id);`,
		`CREATE INDEX IF NOT EXISTS idx_route_decision_run ON route_decision(run_id);`,
		`CREATE TABLE IF NOT EXISTS executed_action (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_decision_id INTEGER NOT NULL REFERENCES route_decision(id),
			run_id INTEGER NOT NULL REFERENCES run(id),
			video_id INTEGER NOT NULL REFERENCES video(id),
			action_type TEXT NOT NULL,
			target_playlist_id TEXT NOT NULL,
			outcome TEXT NOT NULL,
			error_message TEXT,
			executed_at TIMESTAMP,
			UNIQUE(run_id, video_id, target_playlist_id, action_type)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_executed_action_video ON executed_action(video_id);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing schema: %w", err)
		}
	}
	return nil
}

// CreateRun persists a new run and returns its generated ID.
func (s *Store) CreateRun(ctx context.Context, run *Run) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, config_version_id)
		 VALUES (?, ?, ?, ?);`,
		run.StartedAt, run.RunType, run.Status, run.ConfigVersionID)
	if err != nil {
		return 0, fmt.Errorf("creating run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading run id: %w", err)
	}
	return id, nil
}

// ListInterruptedRuns returns all runs with status interrupted, oldest first.
func (s *Store) ListInterruptedRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, completed_at, run_type, status, config_version_id,
		        videos_discovered, videos_accepted, videos_rejected, videos_deferred,
		        videos_failed, videos_not_yet_evaluated, quota_cost, duration_seconds,
		        interrupt_reason, current_phase
		 FROM run WHERE status = ? ORDER BY started_at ASC;`, RunStatusInterrupted)
	if err != nil {
		return nil, fmt.Errorf("listing interrupted runs: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning interrupted run: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating interrupted runs: %w", err)
	}
	return out, nil
}

// GetRun loads a run by ID.
func (s *Store) GetRun(ctx context.Context, id int64) (*Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, started_at, completed_at, run_type, status, config_version_id,
		        videos_discovered, videos_accepted, videos_rejected, videos_deferred,
		        videos_failed, videos_not_yet_evaluated, quota_cost, duration_seconds,
		        interrupt_reason, current_phase
		 FROM run WHERE id = ?;`, id)
	r, err := scanRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("run %d: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("loading run %d: %w", id, err)
	}
	return r, nil
}

// CompleteRun marks a run as completed and records final counters.
func (s *Store) CompleteRun(ctx context.Context, id int64, durationSeconds float64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE run
		 SET status = ?, completed_at = ?, duration_seconds = ?
		 WHERE id = ?;`,
		RunStatusCompleted, time.Now().UTC(), durationSeconds, id)
	if err != nil {
		return fmt.Errorf("completing run %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("run %d: %w", id, ErrNotFound)
	}
	return nil
}

// InterruptRun marks a run as interrupted with a reason.
func (s *Store) InterruptRun(ctx context.Context, id int64, reason string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE run SET status = ?, interrupt_reason = ? WHERE id = ?;`,
		RunStatusInterrupted, reason, id)
	if err != nil {
		return fmt.Errorf("interrupting run %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("run %d: %w", id, ErrNotFound)
	}
	return nil
}

// SetRunPhase records the phase a run has reached. It is used both for resume
// and for coarse-grained progress observation.
func (s *Store) SetRunPhase(ctx context.Context, id int64, phase string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE run SET current_phase = ? WHERE id = ?;`,
		phase, id)
	if err != nil {
		return fmt.Errorf("setting run %d phase: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("run %d: %w", id, ErrNotFound)
	}
	return nil
}

// CreateVideo persists a video record.
func (s *Store) CreateVideo(ctx context.Context, v *Video) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id,
		                   processing_state, final_outcome, deferred_reason, error_message, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		v.VideoID, v.ChannelID, v.DiscoveredAt, v.DiscoveredInRunID,
		v.ProcessingState, v.FinalOutcome, v.DeferredReason, v.ErrorMessage, v.CompletedAt)
	if err != nil {
		return 0, fmt.Errorf("creating video: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading video id: %w", err)
	}
	return id, nil
}

// GetVideo loads a video by internal ID.
func (s *Store) GetVideo(ctx context.Context, id int64) (*Video, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM video WHERE id = ?;`, id)
	v, err := scanVideo(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("video %d: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("loading video %d: %w", id, err)
	}
	return v, nil
}

// GetVideoByExternalID loads a video by its YouTube video ID.
func (s *Store) GetVideoByExternalID(ctx context.Context, videoID string) (*Video, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM video WHERE video_id = ?;`, videoID)
	v, err := scanVideo(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("video %s: %w", videoID, ErrNotFound)
		}
		return nil, fmt.Errorf("loading video %s: %w", videoID, err)
	}
	return v, nil
}

// UpdateVideoState atomically updates a video's processing state and final
// outcome. Callers should validate the transition via the state machine.
func (s *Store) UpdateVideoState(ctx context.Context, id int64, state VideoProcessingState,
	outcome VideoFinalOutcome, deferredReason, errorMessage *string) error {

	var completedAt *time.Time
	if outcome == FinalOutcomeAccepted || outcome == FinalOutcomeRejected ||
		outcome == FinalOutcomeFailed {
		now := time.Now().UTC()
		completedAt = &now
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE video
		 SET processing_state = ?, final_outcome = ?, deferred_reason = ?,
		     error_message = ?, completed_at = ?
		 WHERE id = ?;`,
		state, outcome, deferredReason, errorMessage, completedAt, id)
	if err != nil {
		return fmt.Errorf("updating video %d state: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("video %d: %w", id, ErrNotFound)
	}
	return nil
}

// CreateRouteDecision persists a route decision.
func (s *Store) CreateRouteDecision(ctx context.Context, d *RouteDecision) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order,
		                             decision, skip_reason, rejection_reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?);`,
		d.VideoID, d.RunID, d.RouteID, d.EvaluationOrder, d.Decision, d.SkipReason, d.RejectionReason)
	if err != nil {
		return 0, fmt.Errorf("creating route decision: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading route decision id: %w", err)
	}
	return id, nil
}

// GetRouteDecisionsForVideo returns all route decisions for a video, ordered by
// evaluation order.
func (s *Store) GetRouteDecisionsForVideo(ctx context.Context, videoID int64) ([]RouteDecision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, video_id, run_id, route_id, evaluation_order, decision,
		        skip_reason, rejection_reason
		 FROM route_decision WHERE video_id = ? ORDER BY evaluation_order;`, videoID)
	if err != nil {
		return nil, fmt.Errorf("querying route decisions: %w", err)
	}
	defer rows.Close()

	var out []RouteDecision
	for rows.Next() {
		d, err := scanRouteDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning route decision: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating route decisions: %w", err)
	}
	return out, nil
}

// GetRouteDecisionsForVideoInRun returns route decisions for a specific video
// within a specific run, ordered by evaluation order.
func (s *Store) GetRouteDecisionsForVideoInRun(ctx context.Context, videoID, runID int64) ([]RouteDecision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, video_id, run_id, route_id, evaluation_order, decision,
		        skip_reason, rejection_reason
		 FROM route_decision WHERE video_id = ? AND run_id = ? ORDER BY evaluation_order;`,
		videoID, runID)
	if err != nil {
		return nil, fmt.Errorf("querying route decisions: %w", err)
	}
	defer rows.Close()

	var out []RouteDecision
	for rows.Next() {
		d, err := scanRouteDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning route decision: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating route decisions: %w", err)
	}
	return out, nil
}

// ListRetainedVideos returns all videos in the corpus ordered by discovered_at.
// This is the observation set used by replay and backtest modes.
func (s *Store) ListRetainedVideos(ctx context.Context) ([]Video, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM video ORDER BY discovered_at;`)
	if err != nil {
		return nil, fmt.Errorf("listing retained videos: %w", err)
	}
	defer rows.Close()

	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning retained video: %w", err)
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating retained videos: %w", err)
	}
	return out, nil
}

// ListPendingVideos returns videos that are eligible for dry-run evaluation:
// those in the not_yet_evaluated state and not already processed or rejected.
func (s *Store) ListPendingVideos(ctx context.Context) ([]Video, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM video WHERE processing_state = ? ORDER BY discovered_at;`,
		VideoStateNotYetEvaluated)
	if err != nil {
		return nil, fmt.Errorf("listing pending videos: %w", err)
	}
	defer rows.Close()

	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning pending video: %w", err)
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pending videos: %w", err)
	}
	return out, nil
}

// CreateExecutedAction persists an executed action.
func (s *Store) CreateExecutedAction(ctx context.Context, a *ExecutedAction) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type,
		                              target_playlist_id, outcome, error_message, executed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
		a.RouteDecisionID, a.RunID, a.VideoID, a.ActionType,
		a.TargetPlaylistID, a.Outcome, a.ErrorMessage, a.ExecutedAt)
	if err != nil {
		return 0, fmt.Errorf("creating executed action: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading executed action id: %w", err)
	}
	return id, nil
}

// GetExecutedActionsForVideo returns all executed actions for a video.
func (s *Store) GetExecutedActionsForVideo(ctx context.Context, videoID int64) ([]ExecutedAction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, route_decision_id, run_id, video_id, action_type,
		        target_playlist_id, outcome, error_message, executed_at
		 FROM executed_action WHERE video_id = ?;`, videoID)
	if err != nil {
		return nil, fmt.Errorf("querying executed actions: %w", err)
	}
	defer rows.Close()

	var out []ExecutedAction
	for rows.Next() {
		a, err := scanExecutedAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning executed action: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating executed actions: %w", err)
	}
	return out, nil
}

// GetConditionAuditsForDecision returns the condition audit trail for a route
// decision, ordered by condition_index.
func (s *Store) GetConditionAuditsForDecision(ctx context.Context, decisionID int64) ([]ConditionAudit, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, route_decision_id, condition_index, field, operator,
		        expected_value, actual_value, matched
		 FROM condition_audit WHERE route_decision_id = ? ORDER BY condition_index;`,
		decisionID)
	if err != nil {
		return nil, fmt.Errorf("querying condition audits: %w", err)
	}
	defer rows.Close()

	var out []ConditionAudit
	for rows.Next() {
		a, err := scanConditionAudit(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning condition audit: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating condition audits: %w", err)
	}
	return out, nil
}

// CreateConditionAudit persists a condition audit record.
func (s *Store) CreateConditionAudit(ctx context.Context, a *ConditionAudit) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO condition_audit (route_decision_id, condition_index, field,
		                              operator, expected_value, actual_value, matched)
		 VALUES (?, ?, ?, ?, ?, ?, ?);`,
		a.RouteDecisionID, a.ConditionIndex, a.Field, a.Operator,
		a.ExpectedValue, a.ActualValue, a.Matched)
	if err != nil {
		return 0, fmt.Errorf("creating condition audit: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading condition audit id: %w", err)
	}
	return id, nil
}

// RecomputeRunCounters recomputes outcome counters for a run from video rows.
func (s *Store) RecomputeRunCounters(ctx context.Context, runID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE run SET
		  videos_discovered = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ?),
		  videos_accepted = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?),
		  videos_rejected = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?),
		  videos_deferred = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?),
		  videos_failed = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?),
		  videos_not_yet_evaluated = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?)
		 WHERE id = ?;`,
		runID, runID, FinalOutcomeAccepted,
		runID, FinalOutcomeRejected,
		runID, FinalOutcomeDeferred,
		runID, FinalOutcomeFailed,
		runID, FinalOutcomeNotYetEvaluated,
		runID)
	if err != nil {
		return fmt.Errorf("recomputing run %d counters: %w", runID, err)
	}
	return nil
}

// RecomputeRunCountersFromDecisions recomputes outcome counters for a
// replay/backtest run from the route decisions recorded in the run. Videos are
// counted as accepted when at least one route matched, rejected when no route
// matched, and discovered when any route decision was recorded.
func (s *Store) RecomputeRunCountersFromDecisions(ctx context.Context, runID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE run SET
		  videos_discovered = (SELECT COUNT(DISTINCT video_id) FROM route_decision WHERE run_id = ?),
		  videos_accepted = (
		      SELECT COUNT(DISTINCT video_id)
		      FROM route_decision
		      WHERE run_id = ? AND decision = ?
		  ),
		  videos_rejected = (
		      SELECT COUNT(DISTINCT video_id)
		      FROM route_decision rd
		      WHERE rd.run_id = ? AND rd.decision = ?
		        AND rd.video_id NOT IN (
		            SELECT video_id FROM route_decision WHERE run_id = ? AND decision = ?
		        )
		  ),
		  videos_deferred = 0,
		  videos_failed = 0,
		  videos_not_yet_evaluated = 0
		 WHERE id = ?;`,
		runID, runID, RouteDecisionMatched,
		runID, RouteDecisionNotMatched, runID, RouteDecisionMatched,
		runID)
	if err != nil {
		return fmt.Errorf("recomputing run %d counters from decisions: %w", runID, err)
	}
	return nil
}

// ErrNotFound indicates that a requested entity does not exist.
var ErrNotFound = errors.New("state: not found")

func scanRun(sc interface {
	Scan(dest ...any) error
}) (*Run, error) {
	r := &Run{}
	var completedAt, interruptReason, currentPhase sql.NullString
	var durationSeconds sql.NullFloat64
	var configVersionID sql.NullInt64
	err := sc.Scan(&r.ID, &r.StartedAt, &completedAt, &r.RunType, &r.Status,
		&configVersionID, &r.VideosDiscovered, &r.VideosAccepted, &r.VideosRejected,
		&r.VideosDeferred, &r.VideosFailed, &r.VideosNotYetEvaluated, &r.QuotaCost,
		&durationSeconds, &interruptReason, &currentPhase)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
		r.CompletedAt = &t
	}
	if configVersionID.Valid {
		r.ConfigVersionID = &configVersionID.Int64
	}
	if durationSeconds.Valid {
		r.DurationSeconds = &durationSeconds.Float64
	}
	if interruptReason.Valid {
		r.InterruptReason = &interruptReason.String
	}
	if currentPhase.Valid {
		r.CurrentPhase = &currentPhase.String
	}
	return r, nil
}

func scanVideo(sc interface {
	Scan(dest ...any) error
}) (*Video, error) {
	v := &Video{}
	var deferredReason, errorMessage, completedAt sql.NullString
	err := sc.Scan(&v.ID, &v.VideoID, &v.ChannelID, &v.DiscoveredAt, &v.DiscoveredInRunID,
		&v.ProcessingState, &v.FinalOutcome, &deferredReason, &errorMessage, &completedAt)
	if err != nil {
		return nil, err
	}
	if deferredReason.Valid {
		v.DeferredReason = &deferredReason.String
	}
	if errorMessage.Valid {
		v.ErrorMessage = &errorMessage.String
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
		v.CompletedAt = &t
	}
	return v, nil
}

func scanRouteDecision(sc interface {
	Scan(dest ...any) error
}) (*RouteDecision, error) {
	d := &RouteDecision{}
	var skipReason, rejectionReason sql.NullString
	err := sc.Scan(&d.ID, &d.VideoID, &d.RunID, &d.RouteID, &d.EvaluationOrder,
		&d.Decision, &skipReason, &rejectionReason)
	if err != nil {
		return nil, err
	}
	if skipReason.Valid {
		sr := SkipReason(skipReason.String)
		d.SkipReason = &sr
	}
	if rejectionReason.Valid {
		s := rejectionReason.String
		d.RejectionReason = &s
	}
	return d, nil
}

func scanExecutedAction(sc interface {
	Scan(dest ...any) error
}) (*ExecutedAction, error) {
	a := &ExecutedAction{}
	var errorMessage, executedAt sql.NullString
	err := sc.Scan(&a.ID, &a.RouteDecisionID, &a.RunID, &a.VideoID, &a.ActionType,
		&a.TargetPlaylistID, &a.Outcome, &errorMessage, &executedAt)
	if err != nil {
		return nil, err
	}
	if errorMessage.Valid {
		a.ErrorMessage = &errorMessage.String
	}
	if executedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, executedAt.String)
		a.ExecutedAt = &t
	}
	return a, nil
}

func scanConditionAudit(sc interface {
	Scan(dest ...any) error
}) (*ConditionAudit, error) {
	a := &ConditionAudit{}
	var actualValue sql.NullString
	err := sc.Scan(&a.ID, &a.RouteDecisionID, &a.ConditionIndex, &a.Field,
		&a.Operator, &a.ExpectedValue, &actualValue, &a.Matched)
	if err != nil {
		return nil, err
	}
	if actualValue.Valid {
		a.ActualValue = &actualValue.String
	}
	return a, nil
}
