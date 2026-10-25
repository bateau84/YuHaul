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

// execer is satisfied by *sql.DB and *sql.Tx so we can share write helpers.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// queryer is satisfied by *sql.DB and *sql.Tx so we can share read helpers.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
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

// migrate is retained as a lightweight validation hook. All schema creation is
// owned by the central migration system in internal/store/migrations.
func (s *Store) migrate(ctx context.Context) error {
	// PRAGMA foreign_keys must be enabled per connection for SQLite. The DSN
	// used to open the database sets _fk=1; this explicit pragma defends against
	// callers that pass a connection opened without foreign-key enforcement.
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}
	return nil
}

// RunTimestampLayout is the canonical text layout for run timestamps in
// SQLite: RFC3339 in UTC with fixed-width nanoseconds. Fixed width keeps
// lexical SQL comparison identical to chronological order (RFC3339Nano's
// trimmed trailing zeros would make "…00.5Z" sort before "…00Z").
const RunTimestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// formatRunTime renders a run timestamp as fixed-width RFC3339 text in UTC.
//
// The SQLite driver stores a bound time.Time in Go's String() format
// ("2006-01-02 15:04:05.999999999 -0700 MST"), which does not compare
// chronologically against RFC3339 strings in SQL range predicates (see
// stats.Store.GetTemporalStats). Formatting explicitly keeps lexical order
// equal to chronological order for the UTC timestamps the pipeline writes.
func formatRunTime(t time.Time) string {
	return t.UTC().Format(RunTimestampLayout)
}

// CreateRun persists a new run and returns its generated ID.
func (s *Store) CreateRun(ctx context.Context, run *Run) (int64, error) {
	return s.createRun(ctx, s.db, run)
}

// CreateRunTx persists a new run inside the caller's transaction and returns
// its generated ID. It exists so proposal ingest (ADR-012) can commit the run
// row in the same SQLite transaction as the proposal's identity anchors and
// ConfigVersion snapshot.
func (s *Store) CreateRunTx(ctx context.Context, tx *sql.Tx, run *Run) (int64, error) {
	return s.createRun(ctx, tx, run)
}

func (s *Store) createRun(ctx context.Context, db execer, run *Run) (int64, error) {
	if run.ActionAuthorityVersion == 1 && ((run.RunType != RunTypeManual && run.RunType != RunTypeScheduled) || run.ConfigVersionID == nil) {
		return 0, errors.New("action authority version 1 requires a production run linked to its snapshot")
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, config_version_id, action_authority_version)
		 VALUES (?, ?, ?, ?, ?);`,
		formatRunTime(run.StartedAt), run.RunType, run.Status, run.ConfigVersionID, run.ActionAuthorityVersion)
	if err != nil {
		return 0, fmt.Errorf("creating run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading run id: %w", err)
	}
	return id, nil
}

// ListRuns returns all runs ordered by started_at descending (newest first).
func (s *Store) ListRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, completed_at, run_type, status, config_version_id,
		        videos_discovered, videos_accepted, videos_rejected, videos_excluded,
		        videos_deferred, videos_failed, videos_not_yet_evaluated, quota_cost,
		        duration_seconds, interrupt_reason, current_phase, action_authority_version
		 FROM run ORDER BY started_at DESC;`)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	defer rows.Close()

	out := make([]Run, 0)
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning run: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating runs: %w", err)
	}
	return out, nil
}

// ListInterruptedRuns returns all runs with status interrupted, oldest first.
func (s *Store) ListInterruptedRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, completed_at, run_type, status, config_version_id,
		        videos_discovered, videos_accepted, videos_rejected, videos_excluded,
		        videos_deferred, videos_failed, videos_not_yet_evaluated, quota_cost,
		        duration_seconds, interrupt_reason, current_phase, action_authority_version
		 FROM run WHERE status = ? ORDER BY started_at ASC;`, RunStatusInterrupted)
	if err != nil {
		return nil, fmt.Errorf("listing interrupted runs: %w", err)
	}
	defer rows.Close()

	out := make([]Run, 0)
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
		        videos_discovered, videos_accepted, videos_rejected, videos_excluded,
		        videos_deferred, videos_failed, videos_not_yet_evaluated, quota_cost,
		        duration_seconds, interrupt_reason, current_phase, action_authority_version
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

// CompleteRun marks a run as completed and records final counters and quota cost.
func (s *Store) CompleteRun(ctx context.Context, id int64, durationSeconds float64, quotaCost int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE run
		 SET status = ?, completed_at = ?, duration_seconds = ?, quota_cost = ?
		 WHERE id = ?;`,
		RunStatusCompleted, formatRunTime(time.Now()), durationSeconds, quotaCost, id)
	if err != nil {
		return fmt.Errorf("completing run %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("run %d: %w", id, ErrNotFound)
	}
	return nil
}

// FailRun marks a run as failed with a reason and records final counters and
// quota cost. It reuses the interrupt_reason column to explain why the run did
// not reach a normal terminal state.
func (s *Store) FailRun(ctx context.Context, id int64, reason string, durationSeconds float64, quotaCost int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE run
		 SET status = ?, interrupt_reason = ?, completed_at = ?, duration_seconds = ?, quota_cost = ?
		 WHERE id = ?;`,
		RunStatusFailed, reason, formatRunTime(time.Now()), durationSeconds, quotaCost, id)
	if err != nil {
		return fmt.Errorf("failing run %d: %w", id, err)
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
	return s.createVideo(ctx, s.db, v)
}

// CreateVideoTx is the transaction-scoped variant of CreateVideo.
func (s *Store) CreateVideoTx(ctx context.Context, tx *sql.Tx, v *Video) (int64, error) {
	return s.createVideo(ctx, tx, v)
}

func (s *Store) createVideo(ctx context.Context, exec execer, v *Video) (int64, error) {
	res, err := exec.ExecContext(ctx,
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
	return s.getVideo(ctx, s.db, id)
}

// GetVideoTx is the transaction-scoped variant of GetVideo.
func (s *Store) GetVideoTx(ctx context.Context, tx *sql.Tx, id int64) (*Video, error) {
	return s.getVideo(ctx, tx, id)
}

func (s *Store) getVideo(ctx context.Context, q queryer, id int64) (*Video, error) {
	row := q.QueryRowContext(ctx,
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

	return s.updateVideoState(ctx, s.db, id, state, outcome, deferredReason, errorMessage)
}

// UpdateVideoStateTx is the transaction-scoped variant of UpdateVideoState.
func (s *Store) UpdateVideoStateTx(ctx context.Context, tx *sql.Tx, id int64, state VideoProcessingState,
	outcome VideoFinalOutcome, deferredReason, errorMessage *string) error {

	return s.updateVideoState(ctx, tx, id, state, outcome, deferredReason, errorMessage)
}

func (s *Store) updateVideoState(ctx context.Context, exec execer, id int64, state VideoProcessingState,
	outcome VideoFinalOutcome, deferredReason, errorMessage *string) error {

	var completedAt *time.Time
	if outcome == FinalOutcomeAccepted || outcome == FinalOutcomeRejected ||
		outcome == FinalOutcomeExcluded || outcome == FinalOutcomeFailed {
		now := time.Now().UTC()
		completedAt = &now
	}

	res, err := exec.ExecContext(ctx,
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
	return createRouteDecision(ctx, s.db, d)
}

func createRouteDecision(ctx context.Context, db execer, d *RouteDecision) (int64, error) {
	res, err := db.ExecContext(ctx,
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

	out := make([]RouteDecision, 0)
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

	out := make([]RouteDecision, 0)
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

	out := make([]Video, 0)
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

// runVideoInspection projects read-only run membership and simulation outcomes.
// Discovery ownership is retained; decisions also establish membership for
// historical evaluation and receiving runs. Aggregated evidence avoids multiplying
// videos by their route/action counts. Simulation outcomes follow the same bounded
// successful-evaluation classification as RecomputeRunCountersFromDecisions.
const runVideoInspection = `WITH requested_run AS (
 SELECT id, run_type FROM run WHERE id = ?
), evidence AS (
 SELECT video_id,
        MAX(decision = 'matched') AS matched,
        MAX(decision = 'not_matched') AS not_matched,
        MAX(decision = 'skipped' AND skip_reason = 'exclusion') AS excluded
 FROM route_decision WHERE run_id = (SELECT id FROM requested_run)
 GROUP BY video_id
), inspection AS (
 SELECT v.id, v.video_id, v.channel_id, v.discovered_at, v.discovered_in_run_id,
        v.processing_state,
        CASE WHEN r.run_type IN ('dry_run', 'replay', 'backtest') THEN
          CASE WHEN e.matched = 1 THEN 'accepted'
               WHEN e.not_matched = 1 THEN 'rejected'
               WHEN e.excluded = 1 THEN 'excluded'
               ELSE 'not_yet_evaluated' END
          ELSE v.final_outcome END AS final_outcome,
        CASE WHEN r.run_type IN ('dry_run', 'replay', 'backtest') THEN NULL ELSE v.deferred_reason END AS deferred_reason,
        CASE WHEN r.run_type IN ('dry_run', 'replay', 'backtest') THEN NULL ELSE v.error_message END AS error_message,
        CASE WHEN r.run_type IN ('dry_run', 'replay', 'backtest') THEN NULL ELSE v.completed_at END AS completed_at
 FROM video v CROSS JOIN requested_run r LEFT JOIN evidence e ON e.video_id = v.id
 WHERE v.discovered_in_run_id = r.id OR e.video_id IS NOT NULL
) `

// ListRunVideos returns paginated videos inspected in a specific run, ordered
// by discovered_at descending (newest first). When outcomeFilter is a non-empty
// final outcome, only videos classified with that outcome are returned and
// counted, so the returned total and pagination metadata describe the filtered
// set. An empty outcomeFilter returns every video in the run. It also returns
// the total count of matching videos in the run for pagination metadata.
func (s *Store) ListRunVideos(ctx context.Context, runID int64, limit, offset int, outcomeFilter VideoFinalOutcome) ([]Video, int64, error) {
	where := "1 = 1"
	args := []any{runID}
	if outcomeFilter != "" {
		where += " AND final_outcome = ?"
		args = append(args, outcomeFilter)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx,
		runVideoInspection+`SELECT COUNT(*) FROM inspection WHERE `+where+`;`, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting run %d videos: %w", runID, err)
	}

	rows, err := s.db.QueryContext(ctx,
		runVideoInspection+`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM inspection WHERE `+where+` ORDER BY discovered_at DESC, id DESC LIMIT ? OFFSET ?;`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing run %d videos: %w", runID, err)
	}
	defer rows.Close()

	out := make([]Video, 0)
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scanning run video: %w", err)
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating run videos: %w", err)
	}
	return out, total, nil
}

// GetVideoByExternalIDInRun loads a video by its YouTube video ID only if it
// was discovered or evaluated in the specified run. It shares membership and
// outcome projection with ListRunVideos, without changing persisted video state.
func (s *Store) GetVideoByExternalIDInRun(ctx context.Context, runID int64, videoID string) (*Video, error) {
	row := s.db.QueryRowContext(ctx,
		runVideoInspection+`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM inspection WHERE video_id = ?;`, runID, videoID)
	v, err := scanVideo(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("video %s in run %d: %w", videoID, runID, ErrNotFound)
		}
		return nil, fmt.Errorf("loading video %s in run %d: %w", videoID, runID, err)
	}
	return v, nil
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

	out := make([]Video, 0)
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

	out := make([]ExecutedAction, 0)
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

// GetExecutedActionsForVideoInRun returns executed actions for a specific video
// within a specific run. This scopes the lookup to the run surfaced in the
// decision detail endpoint.
func (s *Store) GetExecutedActionsForVideoInRun(ctx context.Context, videoID, runID int64) ([]ExecutedAction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, route_decision_id, run_id, video_id, action_type,
		        target_playlist_id, outcome, error_message, executed_at
		 FROM executed_action WHERE video_id = ? AND run_id = ?;`, videoID, runID)
	if err != nil {
		return nil, fmt.Errorf("querying executed actions: %w", err)
	}
	defer rows.Close()

	out := make([]ExecutedAction, 0)
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

	out := make([]ConditionAudit, 0)
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
	return createConditionAudit(ctx, s.db, a)
}

func createConditionAudit(ctx context.Context, db execer, a *ConditionAudit) (int64, error) {
	res, err := db.ExecContext(ctx,
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
		  videos_excluded = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?),
		  videos_deferred = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?),
		  videos_failed = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?),
		  videos_not_yet_evaluated = (SELECT COUNT(*) FROM video WHERE discovered_in_run_id = ? AND final_outcome = ?)
		 WHERE id = ?;`,
		runID, runID, FinalOutcomeAccepted,
		runID, FinalOutcomeRejected,
		runID, FinalOutcomeExcluded,
		runID, FinalOutcomeDeferred,
		runID, FinalOutcomeFailed,
		runID, FinalOutcomeNotYetEvaluated,
		runID)
	if err != nil {
		return fmt.Errorf("recomputing run %d counters: %w", runID, err)
	}
	return nil
}

// IncrementChannelRoutedTx increments a channel's total_routed counter within
// an existing transaction.
func (s *Store) IncrementChannelRoutedTx(ctx context.Context, tx *sql.Tx, channelIDText string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE channel SET total_routed = total_routed + 1 WHERE channel_id = ?;`, channelIDText)
	if err != nil {
		return fmt.Errorf("increment channel routed %s: %w", channelIDText, err)
	}
	return nil
}

// IncrementChannelRetainedTx increments a channel's total_retained counter
// within an existing transaction.
func (s *Store) IncrementChannelRetainedTx(ctx context.Context, tx *sql.Tx, channelIDText string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE channel SET total_retained = total_retained + 1 WHERE channel_id = ?;`, channelIDText)
	if err != nil {
		return fmt.Errorf("increment channel retained %s: %w", channelIDText, err)
	}
	return nil
}

// RecomputeRunCountersFromDecisions recomputes outcome counters for a
// replay/backtest run from the route decisions recorded in the run. Videos are
// counted as accepted when at least one route matched, rejected when no route
// matched and an applicable route did not match, excluded when only exclusion
// evidence exists (without a matched or not-matched route), and
// discovered when any route decision was recorded.
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
		  videos_excluded = (
		      SELECT COUNT(DISTINCT video_id)
		      FROM route_decision rd
		      WHERE rd.run_id = ? AND rd.decision = ? AND rd.skip_reason = ?
		        AND rd.video_id NOT IN (
		            SELECT video_id FROM route_decision WHERE run_id = ? AND decision IN (?, ?)
		        )
		  ),
		  videos_deferred = 0,
		  videos_failed = 0,
		  videos_not_yet_evaluated = 0
		 WHERE id = ?;`,
		runID, runID, RouteDecisionMatched,
		runID, RouteDecisionNotMatched, runID, RouteDecisionMatched,
		runID, RouteDecisionSkipped, SkipReasonExclusion, runID, RouteDecisionMatched, RouteDecisionNotMatched,
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
		&r.VideosExcluded, &r.VideosDeferred, &r.VideosFailed, &r.VideosNotYetEvaluated,
		&r.QuotaCost, &durationSeconds, &interruptReason, &currentPhase, &r.ActionAuthorityVersion)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
		r.CompletedAt = &t
	}
	if r.ActionAuthorityVersion != 0 && r.ActionAuthorityVersion != 1 {
		return nil, fmt.Errorf("unsupported action authority version %d", r.ActionAuthorityVersion)
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
