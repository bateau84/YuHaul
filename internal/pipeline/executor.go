package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/youtube"

	"golang.org/x/oauth2"
)

// PlaylistInserter performs the actual YouTube playlist insertion.
type PlaylistInserter interface {
	InsertPlaylistItem(ctx context.Context, playlistID, videoID string) error
}

// YouTubeClientFactory creates a YouTube API client for a run.
type YouTubeClientFactory func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) PlaylistInserter

// TokenSource provides the active OAuth2 token for action execution.
type TokenSource interface {
	ActiveToken(ctx context.Context) (*oauth2.Token, error)
}

// Executor executes playlist actions with quota awareness and idempotency.
type Executor struct {
	db         *sql.DB
	ledger     *quota.Ledger
	auth       TokenSource
	factory    YouTubeClientFactory
	logger     *slog.Logger
	insertCost int
}

// ExecutorOption configures an Executor.
type ExecutorOption func(*Executor)

// WithExecutorLogger sets the structured logger.
func WithExecutorLogger(l *slog.Logger) ExecutorOption {
	return func(e *Executor) {
		if l != nil {
			e.logger = l
		}
	}
}

// WithYouTubeFactory overrides the default YouTube client factory. Useful in
// tests to stub the API client.
func WithYouTubeFactory(f YouTubeClientFactory) ExecutorOption {
	return func(e *Executor) {
		if f != nil {
			e.factory = f
		}
	}
}

// WithTokenSource overrides the default auth service token source. Useful in
// tests to avoid constructing a real auth.Service.
func WithTokenSource(src TokenSource) ExecutorOption {
	return func(e *Executor) {
		if src != nil {
			e.auth = src
		}
	}
}

// NewExecutor returns an action executor.
func NewExecutor(db *sql.DB, ledger *quota.Ledger, auth TokenSource, opts ...ExecutorOption) *Executor {
	e := &Executor{
		db:         db,
		ledger:     ledger,
		auth:       auth,
		factory:    defaultYouTubeFactory,
		logger:     slog.Default(),
		insertCost: quota.OpPlaylistItemsInsert.UnitCost(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func defaultYouTubeFactory(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) PlaylistInserter {
	return youtube.NewClient(ctx, token, youtube.WithRecorder(recorder))
}

// Execute runs all actions for the matched routes. It returns the final outcome
// for the video: accepted, deferred, or failed. On successful execution it
// increments the channel's total_routed counter.
func (e *Executor) Execute(ctx context.Context, runID, videoID int64, matches []RouteMatch) (state.VideoFinalOutcome, error) {
	if len(matches) == 0 {
		return state.FinalOutcomeRejected, nil
	}

	// Load the external video ID and all actions for matched routes.
	var videoIDText string
	err := e.db.QueryRowContext(ctx,
		`SELECT video_id FROM video WHERE id = ?;`, videoID).Scan(&videoIDText)
	if err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("load video id: %w", err)
	}

	actions, err := e.loadActions(ctx, matches)
	if err != nil {
		return state.FinalOutcomeFailed, err
	}
	if len(actions) == 0 {
		return state.FinalOutcomeAccepted, nil
	}

	// Check overall budget for all actions before executing.
	totalCost := e.insertCost * len(actions)
	ok, remaining, err := e.ledger.CanAfford(ctx, totalCost)
	if err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("quota check: %w", err)
	}
	if !ok {
		e.logger.WarnContext(ctx, "quota exhausted: deferring video actions",
			"run_id", runID, "video_id", videoID, "remaining", remaining)
		return e.deferVideo(ctx, runID, videoID, "quota_exhausted")
	}

	// Execute actions one at a time, re-checking budget each iteration.
	allSucceeded := true
	for _, action := range actions {
		ok, _, err := e.ledger.CanAfford(ctx, e.insertCost)
		if err != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("quota check: %w", err)
		}
		if !ok {
			return e.deferVideo(ctx, runID, videoID, "quota_exhausted")
		}

		outcome, err := e.executeAction(ctx, runID, videoID, videoIDText, action)
		if err != nil {
			return state.FinalOutcomeFailed, err
		}
		if outcome == state.ActionOutcomeDeferred {
			return state.FinalOutcomeDeferred, nil
		}
		if outcome != state.ActionOutcomeSucceeded {
			allSucceeded = false
		}
	}

	if allSucceeded {
		if err := e.incrementChannelRouted(ctx, videoID); err != nil {
			e.logger.WarnContext(ctx, "failed to increment channel routed counter", "video_id", videoID, "error", err)
		}
		return state.FinalOutcomeAccepted, nil
	}
	return state.FinalOutcomeFailed, nil
}

func (e *Executor) incrementChannelRouted(ctx context.Context, videoID int64) error {
	var channelID string
	if err := e.db.QueryRowContext(ctx, `SELECT channel_id FROM video WHERE id = ?;`, videoID).Scan(&channelID); err != nil {
		return fmt.Errorf("load channel id for video %d: %w", videoID, err)
	}
	_, err := e.db.ExecContext(ctx,
		`UPDATE channel SET total_routed = total_routed + 1 WHERE channel_id = ?;`, channelID)
	if err != nil {
		return fmt.Errorf("increment routed for channel %s: %w", channelID, err)
	}
	return nil
}

type action struct {
	RouteDecisionID  int64
	ActionType       string
	TargetPlaylistID string
}

func (e *Executor) loadActions(ctx context.Context, matches []RouteMatch) ([]action, error) {
	if len(matches) == 0 {
		return nil, nil
	}

	// Build a map from route_id to route_decision_id for the matched decisions.
	decisionByRoute := make(map[int64]int64, len(matches))
	routeIDs := make([]int64, 0, len(matches))
	for _, m := range matches {
		decisionByRoute[m.RouteID] = m.RouteDecisionID
		routeIDs = append(routeIDs, m.RouteID)
	}

	query := `SELECT route_id, action_type, target_playlist_id, action_index
		      FROM route_action
		      WHERE route_id IN (` + placeholders(len(routeIDs)) + `)
		      ORDER BY route_id, action_index;`
	rows, err := e.db.QueryContext(ctx, query, intsToAny(routeIDs)...)
	if err != nil {
		return nil, fmt.Errorf("load actions: %w", err)
	}
	defer rows.Close()

	var actions []action
	for rows.Next() {
		var routeID int64
		var a action
		if err := rows.Scan(&routeID, &a.ActionType, &a.TargetPlaylistID, new(int)); err != nil {
			return nil, fmt.Errorf("scan action: %w", err)
		}
		a.RouteDecisionID = decisionByRoute[routeID]
		actions = append(actions, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate actions: %w", err)
	}
	return actions, nil
}

func intsToAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := "?"
	for i := 1; i < n; i++ {
		s += ",?"
	}
	return s
}

func (e *Executor) executeAction(ctx context.Context, runID, videoID int64, videoIDText string, a action) (state.ActionOutcome, error) {
	// Idempotency: check for an existing successful action in this run.
	var exists int
	err := e.db.QueryRowContext(ctx,
		`SELECT 1 FROM executed_action
		 WHERE run_id = ? AND video_id = ? AND target_playlist_id = ? AND action_type = ? AND outcome = ?
		 LIMIT 1;`,
		runID, videoID, a.TargetPlaylistID, a.ActionType, state.ActionOutcomeSucceeded).Scan(&exists)
	if err == nil {
		e.logger.DebugContext(ctx, "action already executed; skipping",
			"run_id", runID, "video_id", videoID, "playlist_id", a.TargetPlaylistID)
		return state.ActionOutcomeSucceeded, nil
	}
	if err != sql.ErrNoRows {
		return state.ActionOutcomeFailed, fmt.Errorf("idempotency check: %w", err)
	}

	client, err := e.youtubeClient(ctx, runID)
	if err != nil {
		return state.ActionOutcomeFailed, err
	}

	executedAt := time.Now().UTC()
	if err := client.InsertPlaylistItem(ctx, a.TargetPlaylistID, videoIDText); err != nil {
		// TODO: categorize errors (authorization, API, runtime) and create ErrorEntry.
		return state.ActionOutcomeFailed, fmt.Errorf("insert playlist item: %w", err)
	}

	if _, err := e.db.ExecContext(ctx,
		`INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type,
		                              target_playlist_id, outcome, executed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?);`,
		a.RouteDecisionID, runID, videoID, a.ActionType, a.TargetPlaylistID,
		state.ActionOutcomeSucceeded, executedAt); err != nil {
		return state.ActionOutcomeFailed, fmt.Errorf("record executed action: %w", err)
	}
	return state.ActionOutcomeSucceeded, nil
}

func (e *Executor) youtubeClient(ctx context.Context, runID int64) (PlaylistInserter, error) {
	token, err := e.auth.ActiveToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("no active token: %w", err)
	}
	recorder := func(ctx context.Context, op youtube.Operation, calls int) error {
		return e.ledger.RecordCalls(ctx, sql.NullInt64{Int64: runID, Valid: true}, quota.Operation(op), calls)
	}
	return e.factory(ctx, token, recorder), nil
}

func (e *Executor) deferVideo(ctx context.Context, runID, videoID int64, reason string) (state.VideoFinalOutcome, error) {
	if _, err := e.db.ExecContext(ctx,
		`INSERT INTO deferred_item (video_id, run_id, deferral_reason, status, created_at)
		 VALUES (?, ?, ?, ?, ?);`,
		videoID, runID, reason, "pending", time.Now().UTC()); err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("record deferred item: %w", err)
	}
	return state.FinalOutcomeDeferred, nil
}
