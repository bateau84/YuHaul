package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/diag"
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

// Action outcome reasons recorded alongside the existing action vocabulary.
// They explain a no-attempt or deferral disposition without adding an action
// outcome category or a new failure taxonomy (ARD-001).
const (
	// outcomeReasonAlreadyCompleted records an exact-target production
	// suppression: an earlier production run already succeeded for this
	// target, so this run records a no-attempt, never a fabricated success.
	outcomeReasonAlreadyCompleted = "already_completed"
	// outcomeReasonQuotaExhausted records a quota deferral of the action.
	outcomeReasonQuotaExhausted = "quota_exhausted"
	// outcomeReasonPriorActionFailed records an action that was skipped
	// because an earlier action for the same video failed.
	outcomeReasonPriorActionFailed = "prior_action_failed"
)

func reasonPtr(reason string) *string { return &reason }

// YouTubeClientFactory creates a YouTube API client for a run.
type YouTubeClientFactory func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) PlaylistInserter

// TokenSource provides the active OAuth2 token for action execution.
type TokenSource interface {
	ActiveToken(ctx context.Context) (*oauth2.Token, error)
}

// Executor executes playlist actions with quota awareness and idempotency.
type Executor struct {
	db            *sql.DB
	ledger        *quota.Ledger
	auth          TokenSource
	factory       YouTubeClientFactory
	logger        *slog.Logger
	insertCost    int
	baseURL       string
	customFactory bool
}

type guardedDeferralKey struct{}
type guardedDeferredActionsKey struct{}
type guardedRetryGroupKey struct{}

// pipelineOwnsFailureSinkKey tells the executor that its caller commits the
// failed disposition and the retained error entry in one transaction
// (ADR-006), so the executor must not write the sink itself.
type pipelineOwnsFailureSinkKey struct{}

// videoActionFailure marks an action-level failure confined to one video: the
// video's own disposition can be recorded and independent videos may continue
// (ADR-006 §1). Any error without this marker, and every auxiliary accounting
// failure, stops the run.
type videoActionFailure struct{ cause error }

func (e *videoActionFailure) Error() string { return e.cause.Error() }
func (e *videoActionFailure) Unwrap() error { return e.cause }

// successAccountingError reports quota accounting failure after a confirmed
// playlist success. The success itself stays committed; the run stops.
type successAccountingError struct{ cause error }

func (e *successAccountingError) Error() string {
	return "playlist succeeded; quota accounting failed: " + e.cause.Error()
}
func (e *successAccountingError) Unwrap() error { return e.cause }

// runStoppingError overrides any handled marker inside cause: the run cannot
// safely continue (for example because evidence for the video is incomplete).
type runStoppingError struct{ cause error }

func (e *runStoppingError) Error() string { return e.cause.Error() }
func (e *runStoppingError) Unwrap() error { return e.cause }

// isHandledVideoFailure reports whether err is a per-video failure that the
// run can continue past. Unknown errors fail closed as run-stopping.
func isHandledVideoFailure(err error) bool {
	var accountingErr *postCommitAccountingError
	var successAccounting *successAccountingError
	var stopping *runStoppingError
	if errors.As(err, &accountingErr) || errors.As(err, &successAccounting) || errors.As(err, &stopping) {
		return false
	}
	var mutation *mutationFailure
	if errors.As(err, &mutation) {
		return mutation.accounting == nil
	}
	var failure *videoActionFailure
	return errors.As(err, &failure)
}

// withGuardedDeferral lets the pipeline provide its deferred-work ownership
// writes to the executor's guard-resolution transaction.
func withGuardedDeferral(ctx context.Context, handoff func(*sql.Tx, queue.DeferredReason) error) context.Context {
	return context.WithValue(ctx, guardedDeferralKey{}, handoff)
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
			e.customFactory = true
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

func (e *Executor) authorizedYouTubeFactory(ctx context.Context, binding auth.AuthorizationBinding, recorder youtube.CostRecorder) PlaylistInserter {
	opts := []youtube.Option{youtube.WithRecorder(recorder)}
	if e.baseURL != "" {
		opts = append(opts, youtube.WithBaseURL(e.baseURL))
	}
	return youtube.NewAuthorizedClient(ctx, func(ctx context.Context) (*oauth2.Token, error) {
		resolver, ok := e.auth.(interface {
			Resolve(context.Context, auth.AuthorizationBinding) (*oauth2.Token, error)
		})
		if !ok {
			return nil, errors.New("authorization resolver unavailable")
		}
		return resolver.Resolve(ctx, binding)
	}, opts...)
}

// Execute runs all actions for the matched routes. It returns the final outcome
// for the video: accepted, deferred, or failed. The caller is responsible for
// updating durable channel counters atomically with the terminal state transition.
//
// Every requested-run action disposition is persisted with the existing action
// vocabulary (ARD-001): a known success, a provider failure, a quota deferral,
// a prior exact-target completion (`not_attempted`/`already_completed`), and an
// action skipped after an earlier failure (`not_attempted`/`prior_action_failed`).
// `executed_at` is written only for a YouTube action that was actually
// attempted; deferral and no-attempt rows carry no attempt timestamp.
func (e *Executor) Execute(ctx context.Context, runID, videoID int64, matches []RouteMatch) (state.VideoFinalOutcome, error) {
	return e.execute(ctx, runID, videoID, matches, nil)
}

// ExecuteBound executes production actions using the pipeline invocation's
// authorization identity rather than taking a fresh token snapshot.
func (e *Executor) ExecuteBound(ctx context.Context, runID, videoID int64, matches []RouteMatch, binding auth.AuthorizationBinding) (state.VideoFinalOutcome, error) {
	return e.execute(ctx, runID, videoID, matches, &binding)
}

func (e *Executor) execute(ctx context.Context, runID, videoID int64, matches []RouteMatch, binding *auth.AuthorizationBinding) (state.VideoFinalOutcome, error) {
	run, routes, err := productionAuthority(ctx, e.db, config.NewStore(e.db), runID)
	if err != nil {
		return state.FinalOutcomeFailed, err
	}
	// A lower-level production entry still owns an invocation: bind once here
	// when the caller did not provide the pipeline's captured identity. Never
	// let version-1 mutation execution reach its guard without that binding.
	var mutationSafetyVersion int
	if err := e.db.QueryRowContext(ctx, `SELECT mutation_safety_version FROM run WHERE id=?`, runID).Scan(&mutationSafetyVersion); err != nil {
		return state.FinalOutcomeFailed, fmt.Errorf("read mutation safety version: %w", err)
	}
	if mutationSafetyVersion == 1 && binding == nil {
		binder, ok := e.auth.(interface {
			Bind(context.Context) (auth.AuthorizationBinding, error)
		})
		if !ok {
			return state.FinalOutcomeFailed, errors.New("authorization binding unavailable")
		}
		bound, err := binder.Bind(ctx)
		if err != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("bind authorization for executor invocation: %w", err)
		}
		binding = &bound
	}
	if err := admitLegacy(ctx, e.db, run, videoID, routes, true); err != nil {
		qs, storeErr := queue.NewStore(e.db)
		if storeErr != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("%w; retention store: %v", err, storeErr)
		}
		retainErr := qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
			_, rerr := qs.RetainRuntimeErrorTx(ctx, tx, videoID, runID, "snapshot action authority failure", diag.Reason(err, "snapshot action authority failure"))
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
	// Completed actions are vetoes, not new work, and require no insertion
	// quota. Each suppression is recorded as explicit current-run no-attempt
	// evidence, never as a fabricated current-run success or timestamp
	// (ARD-001/BR-012).
	pending := actions[:0]
	for _, a := range actions {
		completed, err := actionCompleted(ctx, e.db, videoID, a)
		if err != nil {
			return state.FinalOutcomeFailed, err
		}
		if !completed {
			pending = append(pending, a)
			continue
		}
		e.logger.DebugContext(ctx, "action already executed; skipping", "run_id", runID, "video_id", videoID)
		if err := e.recordActionEvidence(ctx, runID, videoID, a,
			state.ActionOutcomeNotAttempted, reasonPtr(outcomeReasonAlreadyCompleted), nil); err != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("record already-completed evidence: %w", err)
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
		return e.deferActions(ctx, runID, videoID, actions)
	}

	// Execute actions one at a time, re-checking budget each iteration.
	allSucceeded := true
	for i, action := range actions {
		completed, err := actionCompleted(ctx, e.db, videoID, action)
		if err != nil {
			return state.FinalOutcomeFailed, err
		}
		if completed {
			// The exact target completed between the pre-filter and this
			// attempt (for example via a concurrent run): record the
			// current-run no-attempt, not a second success.
			if err := e.recordActionEvidence(ctx, runID, videoID, action,
				state.ActionOutcomeNotAttempted, reasonPtr(outcomeReasonAlreadyCompleted), nil); err != nil {
				return state.FinalOutcomeFailed, fmt.Errorf("record already-completed evidence: %w", err)
			}
			continue
		}
		ok, _, err := e.ledger.CanAfford(ctx, e.insertCost)
		if err != nil {
			return state.FinalOutcomeFailed, fmt.Errorf("quota check: %w", err)
		}
		if !ok {
			return e.deferActions(ctx, runID, videoID, actions[i:])
		}

		actionCtx := context.WithValue(ctx, guardedDeferredActionsKey{}, actions[i+1:])
		outcome, err := e.executeAction(actionCtx, runID, videoID, videoIDText, action, binding)
		if err != nil {
			var accountingErr *postCommitAccountingError
			if errors.As(err, &accountingErr) {
				return state.FinalOutcomeDeferred, err
			}
			if ctx.Err() != nil {
				// The run is stopping (ADR-007 §3): write no new failure, skip
				// evidence or sink. Evidence the action already committed stays and
				// the video is reprocessed when the interrupted run resumes.
				return state.FinalOutcomeFailed, err
			}
			if youtube.IsQuotaExceeded(err) || outcome == state.ActionOutcomeDeferred {
				e.logger.WarnContext(ctx, "youtube insertion quota exhausted; deferring video", "run_id", runID, "video_id", videoID)
				return e.deferActions(ctx, runID, videoID, actions[i:])
			}
			// The attempted action's failed outcome is recorded inside
			// executeAction; the actions this failure prevented must not
			// disappear — record them as explicit no-attempt evidence.
			if skipErr := e.recordActionEvidenceBatch(ctx, runID, videoID, actions[i+1:],
				state.ActionOutcomeNotAttempted, reasonPtr(outcomeReasonPriorActionFailed)); skipErr != nil {
				// The video's evidence is incomplete; do not continue past it.
				err = &runStoppingError{cause: fmt.Errorf("%w; record prior-action-failed evidence: %v", err, skipErr)}
			}
			if outcome != state.ActionOutcomeNotAttempted {
				if retainErr := e.retainActionFailure(ctx, runID, videoID, err); retainErr != nil {
					return state.FinalOutcomeFailed, fmt.Errorf("%w; retain action failure: %v", err, retainErr)
				}
			}
			return state.FinalOutcomeFailed, err
		}
		if outcome == state.ActionOutcomeDeferred {
			// Guarded deferrals persist the current and remaining action rows,
			// queue handoff, video outcome, and guard resolution in one transaction.
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

// recordActionEvidence persists one requested-run action outcome with its
// bounded reason. The write is an upsert on the per-run action identity
// UNIQUE(run_id, video_id, target_playlist_id, action_type), so a resumed or
// repeated execution of the same run updates the row to the latest truthful
// disposition instead of violating the constraint or duplicating evidence. A
// durable `succeeded` row is the idempotency authority and is never
// downgraded by a later deferral or no-attempt write. executedAt must be
// non-nil only when a YouTube action was actually attempted (ARD-001); nil
// keeps executed_at NULL.
func (e *Executor) recordActionEvidence(ctx context.Context, runID, videoID int64, a action, outcome state.ActionOutcome, reason *string, executedAt *time.Time) error {
	return recordActionEvidenceTx(ctx, e.db, runID, videoID, a, outcome, reason, executedAt)
}

func recordActionEvidenceTx(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, runID, videoID int64, a action, outcome state.ActionOutcome, reason *string, executedAt *time.Time) error {
	if reason != nil {
		safe, ok := diag.GuardDisposition(*reason, "action_reason")
		if !ok {
			reason = nil
		} else {
			reason = &safe
		}
	}
	_, err := exec.ExecContext(ctx,
		`INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type,
		                              target_playlist_id, outcome, outcome_reason, executed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, video_id, target_playlist_id, action_type)
		 DO UPDATE SET route_decision_id = excluded.route_decision_id,
		               outcome = excluded.outcome,
		               outcome_reason = excluded.outcome_reason,
		               executed_at = excluded.executed_at
		 WHERE executed_action.outcome != 'succeeded'
		   AND (excluded.executed_at IS NOT NULL OR executed_action.executed_at IS NULL);`,
		a.RouteDecisionID, runID, videoID, a.ActionType, a.TargetPlaylistID,
		outcome, reason, executedAt)
	if err != nil {
		return fmt.Errorf("write action evidence: %w", err)
	}
	return nil
}

func (e *Executor) recordActionEvidenceBatch(ctx context.Context, runID, videoID int64, actions []action, outcome state.ActionOutcome, reason *string) error {
	for _, a := range actions {
		if err := e.recordActionEvidence(ctx, runID, videoID, a, outcome, reason, nil); err != nil {
			return err
		}
	}
	return nil
}

// deferActions records the quota-deferred disposition for every action this
// video did not complete. A quota deferral is not an external attempt: the
// rows carry outcome_reason=quota_exhausted and no executed_at (ARD-001). The
// queue disposition itself stays owned by the pipeline.
func (e *Executor) deferActions(ctx context.Context, runID, videoID int64, actions []action) (state.VideoFinalOutcome, error) {
	handoff, _ := ctx.Value(guardedDeferralKey{}).(func(*sql.Tx, queue.DeferredReason) error)
	qs, err := queue.NewStore(e.db)
	if err != nil {
		return state.FinalOutcomeFailed, err
	}
	blocked := false
	err = qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		blocked = false
		guarded := make([]bool, len(actions))
		firstGuard := len(actions)
		// An unresolved send is not a quota deferral. Check under the same
		// transaction as the no-attempt writes, before replacing any evidence.
		for i, a := range actions {
			var exists int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM playlist_send_guard WHERE video_id=? AND target_playlist_id=? AND action_type=?`, videoID, a.TargetPlaylistID, a.ActionType).Scan(&exists)
			if err == nil {
				blocked = true
				guarded[i] = true
				if i < firstGuard {
					firstGuard = i
				}
				if err := recordBlockedGuardEvidenceTx(ctx, tx, runID, videoID, a); err != nil {
					return err
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if blocked {
			for i, a := range actions {
				if !guarded[i] {
					var reason *string
					if i > firstGuard {
						reason = reasonPtr(outcomeReasonPriorActionFailed)
					}
					if err := recordActionEvidenceTx(ctx, tx, runID, videoID, a, state.ActionOutcomeNotAttempted, reason, nil); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if handoff != nil {
			if err := retainGuardedDeferralTx(ctx, tx, qs, runID, videoID, queue.DeferredReasonQuotaExhausted, handoff); err != nil {
				return err
			}
		}
		for _, a := range actions {
			if err := recordActionEvidenceTx(ctx, tx, runID, videoID, a, state.ActionOutcomeDeferred, reasonPtr(outcomeReasonQuotaExhausted), nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if _, retry := ctx.Value(guardedRetryGroupKey{}).(queue.RetryGroupReceipt); retry {
			return state.FinalOutcomeFailed, guardedRetryHandoffError{cause: err}
		}
		return state.FinalOutcomeFailed, err
	}
	if blocked {
		err := errGuardBlocked()
		if _, retry := ctx.Value(guardedRetryGroupKey{}).(queue.RetryGroupReceipt); retry {
			return state.FinalOutcomeFailed, guardedRetryHandoffError{cause: err}
		}
		return state.FinalOutcomeFailed, &videoActionFailure{cause: err}
	}
	return state.FinalOutcomeDeferred, nil
}

func retainGuardedDeferralTx(ctx context.Context, tx *sql.Tx, qs *queue.Store, runID, videoID int64, reason queue.DeferredReason, handoff func(*sql.Tx, queue.DeferredReason) error) error {
	receipt, retry := ctx.Value(guardedRetryGroupKey{}).(queue.RetryGroupReceipt)
	if retry {
		owned, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt)
		if err != nil {
			return err
		}
		if len(owned.Deferred) == 0 && len(owned.Errors) == 0 {
			return queue.ErrWorkRemoved
		}
		for _, ref := range owned.Deferred {
			if _, err := qs.RetainDeferredForRetryTx(ctx, tx, ref.ID, videoID, ref.OriginRunID, runID); err != nil {
				return err
			}
		}
	}
	if err := handoff(tx, reason); err != nil {
		return err
	}
	if retry {
		return qs.AcknowledgeRetryDeferralReceiptTx(ctx, tx, receipt)
	}
	return nil
}

func (e *Executor) retainActionFailure(ctx context.Context, runID, videoID int64, cause error) error {
	if ctx.Value(pipelineOwnsFailureSinkKey{}) != nil {
		// The pipeline commits this video's failed disposition and its sink in
		// one transaction (ADR-006); a sink written here could outlive a
		// rolled-back disposition.
		return nil
	}
	qs, err := queue.NewStore(e.db)
	if err != nil {
		return err
	}
	category := queue.ErrorCategoryAPIError
	var apiErr *youtube.APIError
	if !errors.As(cause, &apiErr) {
		category = queue.ErrorCategoryRuntime
	}
	entry := &queue.ErrorEntry{
		VideoID: videoID, RunID: runID, Category: category,
		// Queue detail carries the vetted bounded summary only: no raw
		// provider body, no error chain (ARD-001). The Error Queue lifecycle
		// itself is unchanged.
		Message: "playlist insertion failed", Detail: diag.Reason(cause, "playlist insertion failed"),
		Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC(),
	}
	if receipt, isRetry := ctx.Value(guardedRetryGroupKey{}).(queue.RetryGroupReceipt); isRetry {
		// Retry failure disposition belongs to processRetryVideo, which owns
		// the receipt and can atomically persist the receiving-run failure,
		// runtime sink, and resolution of surviving source rows. Mutating rows
		// here would release the receipt before that owner can validate it.
		_ = receipt
		return nil
	}
	_, err = qs.CreateErrorEntry(ctx, entry)
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

func (e *Executor) executeAction(ctx context.Context, runID, videoID int64, videoIDText string, a action, binding *auth.AuthorizationBinding) (state.ActionOutcome, error) {
	completed, err := actionCompleted(ctx, e.db, videoID, a)
	if err != nil {
		return state.ActionOutcomeFailed, err
	}
	if completed {
		// The exact target already succeeded in an earlier production run:
		// record the current-run no-attempt suppression, never a fabricated
		// current-run success or attempt timestamp (ARD-001/BR-012).
		if err := e.recordActionEvidence(ctx, runID, videoID, a,
			state.ActionOutcomeNotAttempted, reasonPtr(outcomeReasonAlreadyCompleted), nil); err != nil {
			return state.ActionOutcomeFailed, err
		}
		return state.ActionOutcomeSucceeded, nil
	}
	// Version-1 runs are mutation-safe only through the receipt-backed adapter.
	// Keep legacy/test inserters out of this path rather than silently falling
	// back to an error-only POST seam.
	var mutationSafetyVersion int
	if err := e.db.QueryRowContext(ctx, `SELECT mutation_safety_version FROM run WHERE id=?`, runID).Scan(&mutationSafetyVersion); err != nil {
		return state.ActionOutcomeFailed, fmt.Errorf("read mutation safety version: %w", err)
	}
	if mutationSafetyVersion == 1 {
		return e.executeGuardedAction(ctx, runID, videoID, videoIDText, a, binding)
	}
	// Production must never use the legacy error-only POST path: an error
	// cannot prove whether YouTube applied the mutation. Keep injected inserters
	// available to isolated tests, but fail closed for the shipped client until
	// the run has durable guarded-dispatch capability.
	if !e.customFactory {
		return state.ActionOutcomeFailed, errors.New("playlist mutation blocked: run lacks guarded dispatch capability")
	}
	client, err := e.youtubeClient(ctx, runID, binding)
	if err != nil {
		// No request was attempted: no action row is written and the outcome
		// stays unconfirmed rather than being invented.
		return state.ActionOutcomeFailed, err
	}
	executedAt := time.Now().UTC()
	if err := client.InsertPlaylistItem(ctx, a.TargetPlaylistID, videoIDText); err != nil {
		if youtube.IsQuotaExceeded(err) {
			// The caller records the deferral disposition; a quota rejection is
			// not an external action attempt (ARD-001).
			return state.ActionOutcomeDeferred, fmt.Errorf("insert playlist item: %w", err)
		}
		// The request was attempted and the provider returned an error. Record
		// the failed outcome with the attempt time; the row carries no raw
		// provider body (the safe diagnostic vocabulary is Task 1.4's).
		if recErr := e.recordActionEvidence(ctx, runID, videoID, a,
			state.ActionOutcomeFailed, nil, &executedAt); recErr != nil {
			return state.ActionOutcomeFailed, fmt.Errorf("%w; record failed action evidence: %v", err, recErr)
		}
		return state.ActionOutcomeFailed, &videoActionFailure{cause: fmt.Errorf("insert playlist item: %w", err)}
	}
	if err := e.recordActionEvidence(ctx, runID, videoID, a,
		state.ActionOutcomeSucceeded, nil, &executedAt); err != nil {
		return state.ActionOutcomeFailed, fmt.Errorf("record executed action: %w", err)
	}
	return state.ActionOutcomeSucceeded, nil
}

func (e *Executor) executeGuardedAction(ctx context.Context, runID, videoID int64, videoIDText string, a action, binding *auth.AuthorizationBinding) (state.ActionOutcome, error) {
	if binding == nil || e.customFactory {
		return state.ActionOutcomeFailed, errors.New("guarded playlist dispatch unavailable")
	}
	identity, err := auth.BindingIdentity(*binding)
	if err != nil {
		return state.ActionOutcomeFailed, err
	}
	guardStore, err := state.NewStore(e.db)
	if err != nil {
		return state.ActionOutcomeFailed, err
	}
	claim := state.PlaylistSendClaim{RunID: runID, VideoID: videoID, RouteDecisionID: a.RouteDecisionID, TargetPlaylistID: a.TargetPlaylistID, ActionType: a.ActionType, AuthorizationID: identity}
	claimed, receipt, err := guardStore.ClaimPlaylistSend(ctx, claim, time.Now().UTC())
	if err != nil {
		return state.ActionOutcomeFailed, err
	}
	switch claimed {
	case state.PlaylistAlreadyCompleted:
		if err := e.recordActionEvidence(ctx, runID, videoID, a, state.ActionOutcomeNotAttempted, reasonPtr(outcomeReasonAlreadyCompleted), nil); err != nil {
			return state.ActionOutcomeFailed, err
		}
		return state.ActionOutcomeSucceeded, nil
	case state.PlaylistUncertainBlocked:
		// Same-run attempted evidence is immutable across a blocked re-entry.
		// The guard itself is not evidence that this invocation attempted.
		if err := e.recordBlockedGuardEvidence(ctx, runID, videoID, a); err != nil {
			return state.ActionOutcomeFailed, err
		}
		return state.ActionOutcomeFailed, &videoActionFailure{cause: errGuardBlocked()}
	case state.PlaylistClaimed:
	default:
		return state.ActionOutcomeFailed, errors.New("invalid playlist claim result")
	}
	client := e.authorizedYouTubeFactory(ctx, *binding, nil)
	guardedClient, ok := client.(*youtube.Client)
	if !ok {
		return state.ActionOutcomeFailed, errors.New("guarded playlist adapter unavailable")
	}
	dispatcher, ok := e.auth.(interface {
		DispatchWithQuota(context.Context, auth.AuthorizationBinding, *http.Request, http.RoundTripper, *quota.Ledger, quota.Operation) (*http.Response, auth.AdmissionReceipt, *quota.Hold, error)
		ObserveAccessRejection(context.Context, auth.AuthorizationBinding, auth.AdmissionReceipt) error
	})
	if !ok {
		return state.ActionOutcomeFailed, errors.New("guarded authorization dispatch unavailable")
	}
	desc := youtube.PlaylistClaim{RunID: fmt.Sprint(runID), VideoID: fmt.Sprint(videoID), RouteDecisionID: fmt.Sprint(a.RouteDecisionID), ActionType: a.ActionType, TargetPlaylistID: a.TargetPlaylistID, AuthorizationID: identity}
	var hold *quota.Hold
	result := guardedClient.InsertGuardedPlaylistItem(ctx, desc, videoIDText, func(ctx context.Context, req *http.Request, transport http.RoundTripper) (*http.Response, youtube.DispatchAdmission, bool, error) {
		resp, admission, admittedHold, dispatchErr := dispatcher.DispatchWithQuota(ctx, *binding, req, transport, e.ledger, quota.OpPlaylistItemsInsert)
		hold = admittedHold
		mapped := youtube.DispatchAdmission{AuthorizationID: admission.AuthorizationID, CredentialRevision: admission.CredentialRevision, AdmittedAt: admission.AdmittedAt}
		if ctxErr := ctx.Err(); ctxErr != nil && dispatchErr != nil && admission.AuthorizationID == "" && errors.Is(dispatchErr, ctxErr) {
			// The caller's own context ended before any request was admitted.
			dispatchErr = youtube.DispatchRefusalError{Kind: youtube.DispatchRefusalCanceled}
		} else if auth.IsPreAdmissionAuthorizationRefusal(dispatchErr) && admission.AuthorizationID == "" {
			dispatchErr = youtube.DispatchRefusalError{Kind: youtube.DispatchRefusalAuthorization}
		} else if errors.Is(dispatchErr, auth.ErrQuotaRefused) && admission.AuthorizationID == "" {
			dispatchErr = youtube.DispatchRefusalError{Kind: youtube.DispatchRefusalQuota}
		}
		return resp, mapped, dispatchErr != nil && admission.AuthorizationID == "", dispatchErr
	}, nil)
	if hold != nil {
		// An admitted attempt is accounted exactly once even when shutdown
		// cancelled the request: use the bounded detached context (ADR-007 §3.2).
		accountCtx, cancelAccount := runFinalizationContext(ctx)
		result.Accounting = hold.Finalize(accountCtx, sql.NullInt64{Int64: runID, Valid: true}, quota.OutcomeCategory(result.AccountingCategory), sql.NullInt64{Int64: int64(result.HTTPStatus), Valid: result.HTTPStatus != 0})
		cancelAccount()
	}
	if result.AccessRejected && result.Admission != nil {
		admission := result.Admission
		result.Renewal = dispatcher.ObserveAccessRejection(ctx, *binding, auth.AdmissionReceipt{AuthorizationID: admission.AuthorizationID, CredentialRevision: admission.CredentialRevision, AdmittedAt: admission.AdmittedAt})
	}
	if result.Application == youtube.MutationNotIssued && result.Refusal == youtube.DispatchRefusalCanceled && result.Admission == nil {
		return e.releaseCanceledClaim(ctx, guardStore, *receipt, runID, videoID, a)
	}
	if result.Application == youtube.MutationNotIssued || result.Application == youtube.MutationNotApplied {
		outcome := state.ActionOutcomeDeferred
		reason := ""
		var attemptedAt *time.Time
		if result.Application == youtube.MutationNotApplied {
			reason = outcomeReasonQuotaExhausted
			if result.Admission == nil || result.Admission.AuthorizationID != identity || result.Admission.CredentialRevision <= 0 || result.Admission.AdmittedAt.IsZero() || !youtube.IsQuotaExceeded(result.Cause) {
				return state.ActionOutcomeFailed, errors.New("invalid conclusive playlist refusal")
			}
		} else {
			if result.Admission != nil {
				return state.ActionOutcomeFailed, errors.New("not-issued playlist result has admission")
			}
			switch result.Refusal {
			case youtube.DispatchRefusalAuthorization:
				reason = "authorization_pending"
			case youtube.DispatchRefusalQuota:
				reason = outcomeReasonQuotaExhausted
			default:
				return state.ActionOutcomeFailed, errors.New("playlist dispatch refusal is not confirmed")
			}
		}
		qs, err := queue.NewStore(e.db)
		if err != nil {
			return state.ActionOutcomeFailed, err
		}
		var handoff func(*sql.Tx, queue.DeferredReason) error
		if outcome == state.ActionOutcomeDeferred {
			handoff, _ = ctx.Value(guardedDeferralKey{}).(func(*sql.Tx, queue.DeferredReason) error)
			if handoff == nil {
				return state.ActionOutcomeFailed, errors.New("guarded quota deferral requires pipeline transaction owner")
			}
		}
		err = qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
			if outcome == state.ActionOutcomeNotAttempted {
				// A queue removal racing the authorization request is authoritative.
				var id, origin int64
				var status queue.ErrorStatus
				var owner sql.NullInt64
				err := tx.QueryRowContext(ctx, `SELECT id, run_id, status, retry_run_id FROM error_entry WHERE video_id=? AND error_category=? ORDER BY id DESC LIMIT 1`, videoID, queue.ErrorCategoryAuthorization).Scan(&id, &origin, &status, &owner)
				if err == nil {
					switch status {
					case queue.ErrorStatusDismissed, queue.ErrorStatusResolved:
						return queue.ErrWorkRemoved
					case queue.ErrorStatusRetrying:
						if !owner.Valid || owner.Int64 != runID {
							return queue.ErrWorkNotOwned
						}
						_, err = qs.RetainAuthorizationPendingForRetryTx(ctx, tx, videoID, origin, runID, "playlist insertion is awaiting authorization", reason)
					default:
						_, err = qs.RetainAuthorizationPendingTx(ctx, tx, videoID, origin, "playlist insertion is awaiting authorization", reason)
					}
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				} else {
					_, err = qs.RetainAuthorizationPendingTx(ctx, tx, videoID, runID, "playlist insertion is awaiting authorization", reason)
				}
				if err != nil {
					return err
				}
			} else if outcome == state.ActionOutcomeDeferred {
				// Retry disposition is constrained to the exact rows captured at
				// claim time. Fresh work alone may create a new deferred sink.
				tail, _ := ctx.Value(guardedDeferredActionsKey{}).([]action)
				for _, remaining := range tail {
					if err := recordActionEvidenceTx(ctx, tx, runID, videoID, remaining, state.ActionOutcomeDeferred, &reason, nil); err != nil {
						return err
					}
				}
				if err := retainGuardedDeferralTx(ctx, tx, qs, runID, videoID, queue.DeferredReason(reason), handoff); err != nil {
					return err
				}
			}
			if err := recordActionEvidenceTx(ctx, tx, runID, videoID, a, outcome, &reason, attemptedAt); err != nil {
				return err
			}
			return guardStore.ResolvePlaylistSendTx(ctx, tx, *receipt, outcome, &reason, attemptedAt)
		})
		if err != nil {
			wrapped := fmt.Errorf("retain guarded refusal: %w", err)
			if _, isRetry := ctx.Value(guardedRetryGroupKey{}).(queue.RetryGroupReceipt); isRetry {
				return state.ActionOutcomeFailed, guardedRetryHandoffError{cause: wrapped}
			}
			return state.ActionOutcomeFailed, wrapped
		}
		if outcome == state.ActionOutcomeDeferred {
			if result.Accounting != nil {
				return outcome, &postCommitAccountingError{cause: result.Accounting}
			}
			return outcome, nil
		}
		return outcome, nil
	}
	if result.Application != youtube.MutationSucceeded || result.Admission == nil {
		// Unknown outcomes retain the durable veto. Only a validated matching
		// admission receipt proves this invocation attempted the mutation.
		if result.Admission != nil {
			if err := e.recordUncertainGuardedAttempt(ctx, runID, videoID, a, identity, *result.Admission); err != nil {
				return state.ActionOutcomeFailed, fmt.Errorf("record uncertain guarded attempt: %w", err)
			}
		}
		// Preserve the adapter's vetted provider cause for classification and
		// safe diagnostics. The unresolved application state is represented
		// separately by the durable action reason and retained send guard.
		cause := result.Cause
		if cause == nil {
			cause = errors.New("playlist mutation outcome unresolved")
		}
		return state.ActionOutcomeFailed, &mutationFailure{primary: cause, accounting: result.Accounting, renewal: result.Renewal}
	}
	attemptedAt := result.Admission.AdmittedAt
	// A known success is recorded even if shutdown cancelled ctx meanwhile.
	resolveCtx, cancelResolve := runFinalizationContext(ctx)
	defer cancelResolve()
	tx, err := e.db.BeginTx(resolveCtx, nil)
	if err != nil {
		return state.ActionOutcomeFailed, fmt.Errorf("begin guarded success resolution: %w", err)
	}
	defer tx.Rollback()
	if err := guardStore.ResolvePlaylistSendTx(resolveCtx, tx, *receipt, state.ActionOutcomeSucceeded, nil, &attemptedAt); err != nil {
		return state.ActionOutcomeFailed, err
	}
	if err := tx.Commit(); err != nil {
		return state.ActionOutcomeFailed, fmt.Errorf("commit guarded success resolution: %w", err)
	}
	if result.Accounting != nil {
		return state.ActionOutcomeFailed, &successAccountingError{cause: result.Accounting}
	}
	return state.ActionOutcomeSucceeded, nil
}

// releaseCanceledClaim handles a caller context that ended after the guard
// claim committed and before Dispatch admitted a request (ADR-007 §3.2,
// ARS-001 `not_issued`). Zero requests were sent, so this invocation removes
// its own guard atomically with a `not_attempted` action row that has no
// attempt time. The video keeps no disposition and is reprocessed when the
// interrupted run resumes. The write uses a bounded context detached from the
// cancelled one; if it cannot commit, the guard stays and the target blocks as
// uncertain, which is the safe side. It always returns the context error so
// the run stops as interrupted.
func (e *Executor) releaseCanceledClaim(ctx context.Context, guardStore *state.Store, receipt state.PlaylistSendGuard, runID, videoID int64, a action) (state.ActionOutcome, error) {
	cause := ctx.Err()
	finalizeCtx, cancel := runFinalizationContext(ctx)
	defer cancel()
	tx, err := e.db.BeginTx(finalizeCtx, nil)
	if err == nil {
		defer tx.Rollback()
		if err = guardStore.ResolvePlaylistSendTx(finalizeCtx, tx, receipt, state.ActionOutcomeNotAttempted, nil, nil); err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		e.logger.ErrorContext(ctx, "release canceled playlist claim failed; guard kept", "run_id", runID, "video_id", videoID, "diagnostic", diag.Encode(err, "action_execution", "execute_action"))
		return state.ActionOutcomeFailed, fmt.Errorf("%w; guard kept: %v", cause, err)
	}
	return state.ActionOutcomeNotAttempted, cause
}

// errGuardBlocked is the cause of a video blocked by an unresolved send guard.
// It carries the registered uncertain-mutation code so the retained error entry
// is explainable instead of unknown_error.
func errGuardBlocked() error {
	return diag.At(diag.WithCode(errors.New("playlist mutation blocked by unresolved guard"), "mutation_application_uncertain"), "action_execution", "execute_action")
}

// mutationFailure keeps secondary failures out of primary diagnostic traversal.
type mutationFailure struct {
	primary, accounting, renewal error
}

func (e *mutationFailure) Error() string {
	if e.accounting != nil {
		return "playlist mutation application uncertain; quota accounting failed"
	}
	return "playlist mutation application uncertain"
}
func (e *mutationFailure) Unwrap() error { return e.primary }

func (e *Executor) recordUncertainGuardedAttempt(ctx context.Context, runID, videoID int64, a action, identity string, admission youtube.DispatchAdmission) error {
	if admission.AuthorizationID != identity || admission.CredentialRevision <= 0 || admission.AdmittedAt.IsZero() {
		return nil
	}
	// Admission proves an attempt; record it even if shutdown cancelled ctx.
	evidenceCtx, cancel := runFinalizationContext(ctx)
	defer cancel()
	return e.recordActionEvidence(evidenceCtx, runID, videoID, a, state.ActionOutcomeFailed, reasonPtr("mutation_application_uncertain"), &admission.AdmittedAt)
}

func (e *Executor) recordBlockedGuardEvidence(ctx context.Context, runID, videoID int64, a action) error {
	return recordBlockedGuardEvidenceTx(ctx, e.db, runID, videoID, a)
}

func recordBlockedGuardEvidenceTx(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, runID, videoID int64, a action) error {
	var outcome state.ActionOutcome
	err := db.QueryRowContext(ctx, `SELECT outcome FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=? AND action_type=?`, runID, videoID, a.TargetPlaylistID, a.ActionType).Scan(&outcome)
	if err == nil && outcome == state.ActionOutcomeFailed {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read blocked action evidence: %w", err)
	}
	return recordActionEvidenceTx(ctx, db, runID, videoID, a, state.ActionOutcomeNotAttempted, reasonPtr("mutation_application_uncertain"), nil)
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

func (e *Executor) youtubeClient(ctx context.Context, runID int64, binding *auth.AuthorizationBinding) (PlaylistInserter, error) {
	if binding != nil && !e.customFactory {
		recorder := func(ctx context.Context, op youtube.Operation, calls int) error {
			return e.ledger.RecordCalls(ctx, sql.NullInt64{Int64: runID, Valid: true}, quota.Operation(op), calls)
		}
		return e.authorizedYouTubeFactory(ctx, *binding, recorder), nil
	}
	token, err := e.auth.ActiveToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("no active token: %w", err)
	}
	recorder := func(ctx context.Context, op youtube.Operation, calls int) error {
		return e.ledger.RecordCalls(ctx, sql.NullInt64{Int64: runID, Valid: true}, quota.Operation(op), calls)
	}
	return e.factory(ctx, token, recorder), nil
}
