package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
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
	baseURL    string
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

// WithYouTubeBaseURL overrides the default production YouTube Data API base URL
// for this executor. Empty value keeps the production default.
func WithYouTubeBaseURL(u string) ExecutorOption {
	return func(e *Executor) {
		e.baseURL = u
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
		logger:     slog.Default(),
		insertCost: quota.OpPlaylistItemsInsert.UnitCost(),
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.factory == nil {
		e.factory = e.defaultYouTubeFactory
	}
	return e
}

func (e *Executor) defaultYouTubeFactory(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) PlaylistInserter {
	opts := []youtube.Option{youtube.WithRecorder(recorder)}
	if e.baseURL != "" {
		opts = append(opts, youtube.WithBaseURL(e.baseURL))
	}
	return youtube.NewClient(ctx, token, opts...)
}

// Execute runs all actions for the matched routes. It returns the final outcome
// for the video: accepted, deferred, or failed. The caller is responsible for
// updating durable channel counters atomically with the terminal state transition.
func (e *Executor) Execute(ctx context.Context, runID, videoID int64, matches []RouteMatch) (state.VideoFinalOutcome, error) {
	run, routes, err := productionAuthority(ctx, e.db, config.NewStore(e.db), runID)
	if err != nil {
		return state.FinalOutcomeFailed, err
	}
	if err := admitLegacy(ctx, e.db, run, videoID, routes, true); err != nil {
		qs, storeErr := queue.NewStore(e.db)
		if storeErr != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("%w; retention store: %v", err, storeErr)
		}
		retainErr := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
			_, rerr := qs.RetainRuntimeErrorTx(ctx, tx, videoID, runID, "snapshot action authority failure", err.Error())
			return rerr
		})
		if retainErr != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("%w; retention: %v", err, retainErr)
		}
		return state.FinalOutcomeFailed, err
	}
	actions, err := e.loadActions(ctx, runID, videoID, matches, routes)
	if err != nil {
		return state.FinalOutcomeFailed, err
	}
	if len(matches) == 0 {
		return state.FinalOutcomeRejected, nil
	}

	// Load the external video ID and all actions for matched routes.
	var videoIDText string
	err = e.db.QueryRowContext(ctx,
		`SELECT video_id FROM video WHERE id = ?;`, videoID).Scan(&videoIDText)
	if err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("load video id: %w", err)
	}

	if len(actions) == 0 {
		return state.FinalOutcomeAccepted, nil
	}
	// Completed actions are vetoes, not new work, and require no insertion quota.
	pending := actions[:0]
	for _, a := range actions {
		completed, err := actionCompleted(ctx, e.db, videoID, a)
		if err != nil {
			return state.FinalOutcomeFailed, err
		}
		if !completed {
			pending = append(pending, a)
		} else {
			e.logger.DebugContext(ctx, "action already executed; skipping", "run_id", runID, "video_id", videoID, "playlist_id", a.TargetPlaylistID)
		}
	}
	actions = pending
	if len(actions) == 0 {
		return state.FinalOutcomeAccepted, nil
	}
	ok, remaining, err := e.ledger.CanAfford(ctx, e.insertCost*len(actions))
	if err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("quota check: %w", err)
	}
	if !ok {
		e.logger.WarnContext(ctx, "quota exhausted: deferring video actions", "run_id", runID, "video_id", videoID, "remaining", remaining)
		return e.deferVideo()
	}

	// Execute actions one at a time, re-checking budget each iteration.
	allSucceeded := true
	for _, action := range actions {
		completed, err := actionCompleted(ctx, e.db, videoID, action)
		if err != nil {
			return state.FinalOutcomeFailed, err
		}
		if completed {
			continue
		}
		ok, _, err := e.ledger.CanAfford(ctx, e.insertCost)
		if err != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("quota check: %w", err)
		}
		if !ok {
			return e.deferVideo()
		}

		outcome, err := e.executeAction(ctx, runID, videoID, videoIDText, action)
		if err != nil {
			if youtube.IsQuotaExceeded(err) {
				e.logger.WarnContext(ctx, "youtube insertion quota exhausted; deferring video", "run_id", runID, "video_id", videoID)
				return e.deferVideo()
			}
			if retainErr := e.retainActionFailure(ctx, runID, videoID, err); retainErr != nil {
				return state.FinalOutcomeFailed, fmt.Errorf("%w; retain action failure: %v", err, retainErr)
			}
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
		return state.FinalOutcomeAccepted, nil
	}
	return state.FinalOutcomeFailed, nil
}

func (e *Executor) retainActionFailure(ctx context.Context, runID, videoID int64, cause error) error {
	qs, err := queue.NewStore(e.db)
	if err != nil {
		return err
	}
	category := queue.ErrorCategoryAPIError
	var apiErr *youtube.APIError
	if !errors.As(cause, &apiErr) {
		category = queue.ErrorCategoryRuntime
	}
	_, err = qs.CreateErrorEntry(ctx, &queue.ErrorEntry{
		VideoID: videoID, RunID: runID, Category: category,
		Message: "playlist insertion failed", Detail: cause.Error(),
		Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC(),
	})
	return err
}

type action struct {
	RouteDecisionID  int64
	ActionType       string
	TargetPlaylistID string
}

func (e *Executor) loadActions(ctx context.Context, runID, videoID int64, matches []RouteMatch, routes []router.Route) ([]action, error) {
	if len(matches) == 0 {
		return nil, nil
	}

	decisionByRoute := make(map[int64]int64, len(matches))
	for _, m := range matches {
		var persistedRun, persistedVideo, persistedRoute int64
		var decision string
		if err := e.db.QueryRowContext(ctx, `SELECT run_id, video_id, route_id, decision FROM route_decision WHERE id=?`, m.RouteDecisionID).Scan(&persistedRun, &persistedVideo, &persistedRoute, &decision); err != nil {
			return nil, fmt.Errorf("action decision %d: %w", m.RouteDecisionID, err)
		}
		r := findRoute(routes, m.RouteID)
		if persistedRun != runID || persistedVideo != videoID || persistedRoute != m.RouteID || decision != string(state.RouteDecisionMatched) || r == nil || !r.IsEnabled {
			return nil, fmt.Errorf("action decision %d has no authority for run %d video %d route %d", m.RouteDecisionID, runID, videoID, m.RouteID)
		}
		decisionByRoute[m.RouteID] = m.RouteDecisionID
	}
	var actions []action
	seenActions := map[[2]string]bool{}
	for _, r := range routes {
		d, ok := decisionByRoute[r.ID]
		if !ok {
			continue
		}
		if len(r.Actions) == 0 {
			return nil, fmt.Errorf("snapshot route %d has no action", r.ID)
		}
		seenIndexes := map[int]bool{}
		for _, a := range r.Actions {
			if seenIndexes[a.ActionIndex] {
				return nil, fmt.Errorf("snapshot route %d duplicate action index %d", r.ID, a.ActionIndex)
			}
			seenIndexes[a.ActionIndex] = true
			if a.ActionType != "playlist_insert" || a.TargetPlaylistID == "" {
				return nil, fmt.Errorf("snapshot route %d invalid action", r.ID)
			}
			key := [2]string{a.ActionType, a.TargetPlaylistID}
			if seenActions[key] {
				continue
			}
			seenActions[key] = true
			actions = append(actions, action{d, a.ActionType, a.TargetPlaylistID})
		}
	}
	return actions, nil
}

func (e *Executor) executeAction(ctx context.Context, runID, videoID int64, videoIDText string, a action) (state.ActionOutcome, error) {
	completed, err := actionCompleted(ctx, e.db, videoID, a)
	if err != nil {
		return state.ActionOutcomeFailed, err
	}
	if completed {
		return state.ActionOutcomeSucceeded, nil
	}
	client, err := e.youtubeClient(ctx, runID)
	if err != nil {
		return state.ActionOutcomeFailed, err
	}
	executedAt := time.Now().UTC()
	if err := client.InsertPlaylistItem(ctx, a.TargetPlaylistID, videoIDText); err != nil {
		return state.ActionOutcomeFailed, fmt.Errorf("insert playlist item: %w", err)
	}
	if _, err := e.db.ExecContext(ctx,
		`INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type, target_playlist_id, outcome, executed_at) VALUES (?, ?, ?, ?, ?, ?, ?);`,
		a.RouteDecisionID, runID, videoID, a.ActionType, a.TargetPlaylistID, state.ActionOutcomeSucceeded, executedAt); err != nil {
		return state.ActionOutcomeFailed, fmt.Errorf("record executed action: %w", err)
	}
	return state.ActionOutcomeSucceeded, nil
}

func actionCompleted(ctx context.Context, db *sql.DB, videoID int64, a action) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM executed_action a JOIN run r ON r.id=a.run_id
		 WHERE r.run_type IN ('manual','scheduled') AND a.video_id = ? AND a.target_playlist_id = ? AND a.action_type = ? AND a.outcome = ?
		 LIMIT 1;`,
		videoID, a.TargetPlaylistID, a.ActionType, state.ActionOutcomeSucceeded).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if err != sql.ErrNoRows {
		return false, fmt.Errorf("idempotency check: %w", err)
	}

	return false, nil
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

func (e *Executor) deferVideo() (state.VideoFinalOutcome, error) {
	// Queue persistence is coordinated with lifecycle/outcome persistence by the
	// pipeline, which owns the terminal run-local disposition transaction.
	return state.FinalOutcomeDeferred, nil
}
