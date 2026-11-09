package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/youtube"
)

func setupExecutorTest(t *testing.T) (*sql.DB, *state.Store, *quota.Ledger, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)

	ledger := quota.New(db, quota.WithDefaultLimit(10_000))

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, stateStore, ledger, cleanup
}

type fakeTokenSource struct{}

func (f *fakeTokenSource) ActiveToken(ctx context.Context) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "test"}, nil
}

type recordingInserter struct {
	calls []struct{ PlaylistID, VideoID string }
	err   error
	// errFor returns a per-target error, letting a test fail one action of a
	// multi-action video while recording the observed mutation attempts.
	errFor func(playlistID string) error
}

func (r *recordingInserter) InsertPlaylistItem(ctx context.Context, playlistID, videoID string) error {
	r.calls = append(r.calls, struct{ PlaylistID, VideoID string }{playlistID, videoID})
	if r.errFor != nil {
		if err := r.errFor(playlistID); err != nil {
			return err
		}
	}
	return r.err
}

func newTestExecutor(t *testing.T, db *sql.DB, ledger *quota.Ledger, inserter *recordingInserter) *Executor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewExecutor(db, ledger, &fakeTokenSource{},
		WithExecutorLogger(logger),
		WithYouTubeFactory(func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) PlaylistInserter {
			return inserter
		}),
	)
}

func createRunAndVideo(t *testing.T, ctx context.Context, db *sql.DB, stateStore *state.Store) (int64, int64, string) {
	t.Helper()
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	cfgID, err := config.NewService(router.NewStore(db), config.NewStore(db)).ActiveVersionID(ctx)
	require.NoError(t, err)
	run.ConfigVersionID = cfgID
	run.ActionAuthorityVersion = 1
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	machine := state.NewMachine(stateStore)
	video, err := machine.DiscoverVideo(ctx, runID, "vid1", "ch1")
	require.NoError(t, err)
	return runID, video.ID, video.VideoID
}

func TestExecutor_NoMatchesRejected(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	_, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	exec := newTestExecutor(t, db, ledger, nil)

	outcome, err := exec.Execute(ctx, 1, videoID, nil)
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeRejected, outcome)
}

func TestExecutor_ExecutesAndRecordsAction(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	// Create a route with an action.
	routeID := createRoute(t, ctx, db, "Jazz", 1)
	createRouteAction(t, ctx, db, routeID, "PLjazz")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)

	// Record a matched decision.
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)

	inserter := &recordingInserter{}
	exec := newTestExecutor(t, db, ledger, inserter)

	outcome, err := exec.Execute(ctx, runID, videoID, []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}})
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeAccepted, outcome)
	require.Len(t, inserter.calls, 1)
	assert.Equal(t, "PLjazz", inserter.calls[0].PlaylistID)

	actions, err := stateStore.GetExecutedActionsForVideo(ctx, videoID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeSucceeded, actions[0].Outcome)
	assert.NotNil(t, actions[0].ExecutedAt,
		"a real attempted-and-confirmed insertion records its attempt time")
	assert.Nil(t, actions[0].OutcomeReason,
		"a plain success needs no explanatory reason")
}

func TestExecutor_LegacyRunFailsClosedBeforePlaylistPOST(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	routeID := createRoute(t, ctx, db, "Legacy", 1)
	createRouteAction(t, ctx, db, routeID, "PLlegacy")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore) // remains mutation_safety_version=0
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)
	exec := NewExecutor(db, ledger, &fakeTokenSource{}, WithYouTubeBaseURL(server.URL))

	_, err := exec.Execute(ctx, runID, videoID, []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}})
	require.ErrorContains(t, err, "lacks guarded dispatch capability")
	assert.Zero(t, posts, "a legacy run must not reach the error-only mutation endpoint")
}

func TestExecutor_VersionOneLowerLevelEntryRequiresInvocationBinding(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	routeID := createRoute(t, ctx, db, "Bound", 1)
	createRouteAction(t, ctx, db, routeID, "PLbound")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)
	_, err := db.ExecContext(ctx, `UPDATE run SET mutation_safety_version=1 WHERE id=?`, runID)
	require.NoError(t, err)

	exec := NewExecutor(db, ledger, &fakeTokenSource{}, WithYouTubeFactory(func(context.Context, *oauth2.Token, youtube.CostRecorder) PlaylistInserter {
		t.Fatal("unbound version-1 entry must not construct a playlist client")
		return nil
	}))
	_, err = exec.Execute(ctx, runID, videoID, []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}})
	require.ErrorContains(t, err, "authorization binding unavailable")
	var guards int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE video_id=?`, videoID).Scan(&guards))
	assert.Zero(t, guards, "missing invocation binding must fail before guard claim")
}

func TestExecutor_DeferredWhenQuotaExhausted(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()
	require.NoError(t, ledger.SetDailyLimit(ctx, 10))

	routeID := createRoute(t, ctx, db, "Jazz", 1)
	createRouteAction(t, ctx, db, routeID, "PLjazz")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)

	inserter := &recordingInserter{}
	exec := newTestExecutor(t, db, ledger, inserter)

	outcome, err := exec.Execute(ctx, runID, videoID, []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}})
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeDeferred, outcome)
	assert.Empty(t, inserter.calls)

	// A proactive quota deferral is not an external attempt: it records
	// deferred/quota_exhausted evidence with no executed_at (ARD-001).
	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
	require.NotNil(t, actions[0].OutcomeReason)
	assert.Equal(t, "quota_exhausted", *actions[0].OutcomeReason)
	assert.Nil(t, actions[0].ExecutedAt,
		"a quota deferral must not carry an attempt timestamp")
}

func TestExecutor_WithYouTubeBaseURLUsesOverrideURL(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()

	var requestPath string
	targetURL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "item-id",
			"snippet": map[string]any{
				"playlistId": f.target,
				"resourceId": map[string]any{"kind": "youtube#video", "videoId": "guarded-external-video"},
			},
		})
	}))
	defer targetURL.Close()

	exec := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(targetURL.URL))

	outcome, err := exec.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeAccepted, outcome)
	assert.Equal(t, "/playlistItems", requestPath)
}

func TestExecutor_HTTPQuotaExceededDefersAndRetainsWork(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`)
	}))
	defer server.Close()
	exec := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(server.URL))
	ctx = withGuardedDeferral(ctx, func(tx *sql.Tx, _ queue.DeferredReason) error {
		qs, err := queue.NewStore(f.db)
		if err != nil {
			return err
		}
		_, err = qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		return err
	})
	outcome, err := exec.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeDeferred, outcome)
	assert.Equal(t, 1, calls)
	var deferred, errors int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM deferred_item WHERE video_id=? AND status='pending'`, f.videoID).Scan(&deferred))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM error_entry WHERE video_id=? AND status='active'`, f.videoID).Scan(&errors))
	assert.Equal(t, 1, deferred, "queue ownership commits with guard resolution and action evidence")
	assert.Zero(t, errors)
	// A quota rejection is recorded as a deferral without an attempt claim.
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
	require.NotNil(t, actions[0].OutcomeReason)
	assert.Equal(t, "quota_exhausted", *actions[0].OutcomeReason)
	assert.Nil(t, actions[0].ExecutedAt,
		"a quota deferral is not counted as an external attempt")
}

func TestExecutor_HTTPQuotaExceededDefersRemainingGuardedActions(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixtureWithTargets(t, []string{"PLguarded-fixture", "PLguarded-tail"})
	defer f.db.Close()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`)
	}))
	defer server.Close()
	exec := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(server.URL))
	ctx = withGuardedDeferral(ctx, func(tx *sql.Tx, _ queue.DeferredReason) error {
		qs, err := queue.NewStore(f.db)
		if err != nil {
			return err
		}
		_, err = qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		return err
	})
	outcome, err := exec.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, outcome)
	require.Equal(t, 1, calls, "quota refusal stops further Data API calls")
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 2)
	for _, action := range actions {
		require.Equal(t, state.ActionOutcomeDeferred, action.Outcome)
		require.NotNil(t, action.OutcomeReason)
		require.Equal(t, "quota_exhausted", *action.OutcomeReason)
		require.Nil(t, action.ExecutedAt)
	}
	var guards, activeErrors int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=?`, f.runID).Scan(&guards))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE run_id=? AND status='active'`, f.runID).Scan(&activeErrors))
	require.Zero(t, guards, "conclusive quota refusal releases its owned send guard")
	require.Zero(t, activeErrors, "quota deferral is not a runtime error")
}

func TestExecutor_GuardedQuotaHandoffFailureRollsBackOwnership(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`)
	}))
	defer server.Close()
	exec := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(server.URL))
	ctx = withGuardedDeferral(ctx, func(tx *sql.Tx, _ queue.DeferredReason) error {
		qs, err := queue.NewStore(f.db)
		if err != nil {
			return err
		}
		if _, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		return errors.New("injected handoff failure")
	})
	outcome, err := exec.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.ErrorContains(t, err, "injected handoff failure")
	assert.Equal(t, state.FinalOutcomeFailed, outcome)
	var guards, deferred, actions int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM deferred_item WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&deferred))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM executed_action WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&actions))
	assert.Equal(t, 1, guards, "failed ownership handoff must preserve the replay veto")
	assert.Zero(t, deferred)
	assert.Zero(t, actions, "rollback cannot fabricate an attempt or disposition")
}

func TestExecutor_GuardedQuotaTailEvidenceFailureRollsBackHandoff(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixtureWithTargets(t, []string{"PLguarded-fixture", "PLguarded-tail"})
	defer f.db.Close()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`)
	}))
	defer server.Close()
	_, err := f.db.ExecContext(ctx, `CREATE TRIGGER fail_tail_action BEFORE INSERT ON executed_action
		WHEN NEW.target_playlist_id='PLguarded-tail' BEGIN SELECT RAISE(ABORT, 'injected tail evidence failure'); END`)
	require.NoError(t, err)
	exec := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(server.URL))
	ctx = withGuardedDeferral(ctx, func(tx *sql.Tx, _ queue.DeferredReason) error {
		qs, err := queue.NewStore(f.db)
		if err != nil {
			return err
		}
		_, err = qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		return err
	})
	outcome, err := exec.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.ErrorContains(t, err, "injected tail evidence failure")
	assert.Equal(t, state.FinalOutcomeFailed, outcome)
	assert.Equal(t, 1, calls, "quota refusal must stop further provider attempts")
	var guards, deferred, actions int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT count(*) FROM deferred_item WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&deferred))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT count(*) FROM executed_action WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&actions))
	assert.Equal(t, 1, guards, "failed tail write preserves the unresolved replay veto")
	assert.Zero(t, deferred, "queue handoff must roll back with tail evidence")
	assert.Zero(t, actions, "current and remaining action dispositions commit together")
	consumed, err := f.ledger.ConsumedByRun(ctx, f.runID)
	require.NoError(t, err)
	assert.Equal(t, quota.OpPlaylistItemsInsert.UnitCost(), consumed,
		"the one provider request remains counted despite the rolled-back disposition transaction")
}

func TestExecutor_HTTPActionFailureCreatesRetryableError(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}],"code":500,"message":"Internal Server Error"}}`)
	}))
	defer server.Close()
	exec := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(server.URL))
	outcome, err := exec.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.Error(t, err)
	assert.Equal(t, state.FinalOutcomeFailed, outcome)
	entries, err := queue.NewStore(f.db)
	require.NoError(t, err)
	active, err := entries.ListErrorEntries(ctx, queue.ErrorStatusActive)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, f.videoID, active[0].VideoID)
	assert.Contains(t, active[0].Detail, "backendError")

	// The attempted action's failed outcome is persisted requested-run
	// evidence with an attempt time and no raw provider body in the row.
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeFailed, actions[0].Outcome)
	require.NotNil(t, actions[0].OutcomeReason)
	assert.Equal(t, "mutation_application_uncertain", *actions[0].OutcomeReason)
	assert.NotNil(t, actions[0].ExecutedAt,
		"a provider-rejected request was actually attempted")
	if actions[0].ErrorMessage != nil {
		t.Fatalf("action evidence must not carry a raw error message, got %q", *actions[0].ErrorMessage)
	}
}

func TestExecutor_FailedActionRecordsOutcome(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	routeID := createRoute(t, ctx, db, "Jazz", 1)
	createRouteAction(t, ctx, db, routeID, "PLjazz")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)

	inserter := &recordingInserter{err: errors.New("api error")}
	exec := newTestExecutor(t, db, ledger, inserter)

	outcome, err := exec.Execute(ctx, runID, videoID, []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}})
	require.Error(t, err)
	assert.Equal(t, state.FinalOutcomeFailed, outcome)

	// The provider failure is a durable requested-run action outcome, not an
	// absent or fabricated result.
	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeFailed, actions[0].Outcome)
	assert.NotNil(t, actions[0].ExecutedAt)
}

// TestExecutor_PriorCompletionIsCurrentRunNoAttempt proves BR-012/ARD-001: an
// exact target completed by an earlier production run is suppressed with
// not_attempted/already_completed evidence in the requested run — no mutation
// attempt, no fabricated current-run success, and no attempt timestamp.
func TestExecutor_PriorCompletionIsCurrentRunNoAttempt(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	routeID := createRoute(t, ctx, db, "Jazz", 1)
	createRouteAction(t, ctx, db, routeID, "PLjazz")
	priorRunID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, priorRunID, routeID, 1, state.RouteDecisionMatched)

	// The earlier production run really succeeded for this exact target.
	priorAttempt := time.Now().UTC().Add(-time.Hour)
	_, err := stateStore.CreateExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID: decisionID, RunID: priorRunID, VideoID: videoID,
		ActionType: "playlist_insert", TargetPlaylistID: "PLjazz",
		Outcome: state.ActionOutcomeSucceeded, ExecutedAt: &priorAttempt,
	})
	require.NoError(t, err)

	// A fresh production run evaluates the same video against the same target.
	nextRunID := createRunOnly(t, ctx, db, stateStore)
	nextDecisionID := createRouteDecision(t, ctx, stateStore, videoID, nextRunID, routeID, 2, state.RouteDecisionMatched)

	inserter := &recordingInserter{}
	exec := newTestExecutor(t, db, ledger, inserter)
	outcome, err := exec.Execute(ctx, nextRunID, videoID, []RouteMatch{{RouteDecisionID: nextDecisionID, RouteID: routeID}})
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeAccepted, outcome)
	assert.Empty(t, inserter.calls, "the completed exact target must not be attempted again")

	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, nextRunID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
	require.NotNil(t, actions[0].OutcomeReason)
	assert.Equal(t, "already_completed", *actions[0].OutcomeReason)
	assert.Nil(t, actions[0].ExecutedAt,
		"a prior completion must not gain a current-run attempt timestamp")

	// The prior run's durable success remains the suppression evidence.
	prior, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, priorRunID)
	require.NoError(t, err)
	require.Len(t, prior, 1)
	assert.Equal(t, state.ActionOutcomeSucceeded, prior[0].Outcome)
	assert.NotNil(t, prior[0].ExecutedAt)
}

// TestExecutor_PriorActionFailureSkipsLaterActions proves that when one
// action fails, the actions after it are persisted as
// not_attempted/prior_action_failed — with no attempt and no timestamp —
// instead of silently disappearing, while the failed action keeps its own
// attempted evidence and the existing Error Queue entry.
func TestExecutor_PriorActionFailureSkipsLaterActions(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	routeID := createRoute(t, ctx, db, "Jazz", 1)
	createRouteActionAt(t, ctx, db, routeID, "PLfirst", 0)
	createRouteActionAt(t, ctx, db, routeID, "PLsecond", 1)
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)

	inserter := &recordingInserter{errFor: func(playlistID string) error {
		if playlistID == "PLfirst" {
			return errors.New("api error")
		}
		return nil
	}}
	exec := newTestExecutor(t, db, ledger, inserter)

	outcome, err := exec.Execute(ctx, runID, videoID, []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}})
	require.Error(t, err)
	assert.Equal(t, state.FinalOutcomeFailed, outcome)

	// Observed mutation attempts agree with persisted outcomes: one call to
	// the failing target, none to the skipped target.
	require.Len(t, inserter.calls, 1)
	assert.Equal(t, "PLfirst", inserter.calls[0].PlaylistID)

	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	byTarget := make(map[string]state.ExecutedAction, len(actions))
	for _, a := range actions {
		byTarget[a.TargetPlaylistID] = a
	}
	require.Len(t, actions, 2, "duplicate-free per-target evidence for every configured action")

	first, ok := byTarget["PLfirst"]
	require.True(t, ok)
	assert.Equal(t, state.ActionOutcomeFailed, first.Outcome)
	assert.NotNil(t, first.ExecutedAt, "the failed action was actually attempted")

	second, ok := byTarget["PLsecond"]
	require.True(t, ok)
	assert.Equal(t, state.ActionOutcomeNotAttempted, second.Outcome)
	require.NotNil(t, second.OutcomeReason)
	assert.Equal(t, "prior_action_failed", *second.OutcomeReason)
	assert.Nil(t, second.ExecutedAt, "a skipped action was never attempted")

	// The failure still lives in the existing Error Queue, unchanged.
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	active, err := qs.ListErrorEntries(ctx, queue.ErrorStatusActive)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, videoID, active[0].VideoID)
}

func TestGuardedUncertainAttemptSurvivesBlockedReentry(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, createRoute(t, ctx, db, "Jazz", 1), 1, state.RouteDecisionMatched)
	e := newTestExecutor(t, db, ledger, &recordingInserter{})
	a := action{RouteDecisionID: decisionID, ActionType: "playlist_insert", TargetPlaylistID: "PLtarget"}
	attempted := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	require.NoError(t, e.recordUncertainGuardedAttempt(ctx, runID, videoID, a, "auth", youtube.DispatchAdmission{AuthorizationID: "auth", CredentialRevision: 2, AdmittedAt: attempted}))
	require.NoError(t, e.recordBlockedGuardEvidence(ctx, runID, videoID, a))
	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeFailed, actions[0].Outcome)
	require.NotNil(t, actions[0].ExecutedAt)
	assert.Equal(t, attempted, *actions[0].ExecutedAt)
}

func TestGuardedUncertainAttemptRequiresAdmissionEvidence(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	routeID := createRoute(t, ctx, db, "Jazz", 1)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)
	e := newTestExecutor(t, db, ledger, &recordingInserter{})
	a := action{RouteDecisionID: decisionID, ActionType: "playlist_insert", TargetPlaylistID: "PLtarget"}
	// No admission is intentionally not passed to the recorder: a guard alone
	// must not manufacture an attempted action or timestamp.
	require.NoError(t, e.recordBlockedGuardEvidence(ctx, runID, videoID, a))
	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
	assert.Nil(t, actions[0].ExecutedAt)
}

// TestExecutor_DuplicateTargetsProduceSingleEvidenceRow proves that duplicate
// configured targets (the same exact target on several matched routes) yield
// one mutation attempt and one evidence row, not repeated evidence.
func TestExecutor_DuplicateTargetsProduceSingleEvidenceRow(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	firstRoute := createRoute(t, ctx, db, "Jazz", 1)
	secondRoute := createRoute(t, ctx, db, "Jazz Two", 2)
	createRouteAction(t, ctx, db, firstRoute, "PLshared")
	createRouteAction(t, ctx, db, secondRoute, "PLshared")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	firstDecision := createRouteDecision(t, ctx, stateStore, videoID, runID, firstRoute, 1, state.RouteDecisionMatched)
	secondDecision := createRouteDecision(t, ctx, stateStore, videoID, runID, secondRoute, 2, state.RouteDecisionMatched)

	inserter := &recordingInserter{}
	exec := newTestExecutor(t, db, ledger, inserter)

	outcome, err := exec.Execute(ctx, runID, videoID, []RouteMatch{
		{RouteDecisionID: firstDecision, RouteID: firstRoute},
		{RouteDecisionID: secondDecision, RouteID: secondRoute},
		{RouteDecisionID: firstDecision, RouteID: firstRoute}, // repeated match
	})
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeAccepted, outcome)
	require.Len(t, inserter.calls, 1, "the duplicate target is attempted once")

	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, actions, 1, "duplicate targets must not create repeated evidence")
	assert.Equal(t, state.ActionOutcomeSucceeded, actions[0].Outcome)
}

// TestExecutor_QuotaDeferralThenRetryRecordsOneTruthfulRow proves resume
// safety on the per-run action identity: a deferral row in the same run is
// replaced by the later real attempt's outcome instead of violating the
// UNIQUE(run_id, video_id, target, action_type) constraint or duplicating
// evidence, and the durable row matches the observed mutation.
func TestExecutor_QuotaDeferralThenRetryRecordsOneTruthfulRow(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()
	require.NoError(t, ledger.SetDailyLimit(ctx, 10))

	routeID := createRoute(t, ctx, db, "Jazz", 1)
	createRouteAction(t, ctx, db, routeID, "PLjazz")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)

	inserter := &recordingInserter{}
	exec := newTestExecutor(t, db, ledger, inserter)
	matches := []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}}

	outcome, err := exec.Execute(ctx, runID, videoID, matches)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, outcome)
	require.Empty(t, inserter.calls)

	require.NoError(t, ledger.SetDailyLimit(ctx, 100_000))
	outcome, err = exec.Execute(ctx, runID, videoID, matches)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeAccepted, outcome)
	require.Len(t, inserter.calls, 1, "the retry performs the single real insertion")

	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, actions, 1, "one run holds one truthful row per action")
	assert.Equal(t, state.ActionOutcomeSucceeded, actions[0].Outcome)
	assert.Nil(t, actions[0].OutcomeReason, "a plain success carries no no-attempt reason")
	assert.NotNil(t, actions[0].ExecutedAt)
}

// createRunOnly creates an admissible production run without any video work,
// for scenarios where an existing video is re-evaluated under a new run.
func createRunOnly(t *testing.T, ctx context.Context, db *sql.DB, stateStore *state.Store) int64 {
	t.Helper()
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	cfgID, err := config.NewService(router.NewStore(db), config.NewStore(db)).ActiveVersionID(ctx)
	require.NoError(t, err)
	run.ConfigVersionID = cfgID
	run.ActionAuthorityVersion = 1
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	return runID
}

func createRoute(t *testing.T, ctx context.Context, db *sql.DB, name string, order int) int64 {
	t.Helper()
	id, err := router.NewStore(db).CreateRoute(ctx, &router.Route{Name: name, EvaluationOrder: order, IsEnabled: true, ConditionComposition: "AND"})
	require.NoError(t, err)
	return id
}

func createRouteAction(t *testing.T, ctx context.Context, db *sql.DB, routeID int64, playlistID string) {
	t.Helper()
	createRouteActionAt(t, ctx, db, routeID, playlistID, 0)
}

func createRouteActionAt(t *testing.T, ctx context.Context, db *sql.DB, routeID int64, playlistID string, index int) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
		 VALUES (?, 'playlist_insert', ?, ?);`,
		routeID, playlistID, index)
	require.NoError(t, err)
}

func createRouteDecision(t *testing.T, ctx context.Context, stateStore *state.Store,
	videoID, runID, routeID int64, order int, decision state.RouteDecisionType) int64 {
	t.Helper()
	d := &state.RouteDecision{
		VideoID:         videoID,
		RunID:           runID,
		RouteID:         routeID,
		EvaluationOrder: order,
		Decision:        decision,
	}
	if decision == state.RouteDecisionSkipped {
		sr := state.SkipReasonDisabled
		d.SkipReason = &sr
	}
	result, err := state.NewMachine(stateStore).RecordRouteDecision(ctx, d)
	require.NoError(t, err)
	return result.ID
}

var _ = io.Discard
