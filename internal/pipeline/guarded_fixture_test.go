package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store/storetest"
	"github.com/bateau84/YuHaul/internal/youtube"
	"github.com/stretchr/testify/require"
)

func TestGuardedRejectionPreservesAuxiliaryFailures(t *testing.T) {
	for _, accountingFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("accounting_failure_%t", accountingFails), func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixtureWithRenewalStatus(t, []string{"PLguarded-fixture"}, http.StatusServiceUnavailable)
			defer f.db.Close()
			tokens, err := auth.NewSQLiteTokenStore(f.db, []byte("01234567890123456789012345678901"))
			require.NoError(t, err)
			require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "TOKEN_SENTINEL", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
			binding, err := f.auth.Bind(ctx)
			require.NoError(t, err)
			if accountingFails {
				_, err = f.db.Exec(`CREATE TRIGGER reject_accounting BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(ABORT,'SECRET_SENTINEL'); END`)
				require.NoError(t, err)
			}
			data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.posts++
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"authError"}]}}`)
			}))
			defer data.Close()
			e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(data.URL))
			matches := []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}
			_, err = e.ExecuteBound(ctx, f.runID, f.videoID, matches, binding)
			require.Error(t, err)
			var failure *mutationFailure
			require.ErrorAs(t, err, &failure)
			require.Equal(t, "temporary_provider", auth.RenewalFailureCategory(failure.renewal))
			require.Equal(t, accountingFails, failure.accounting != nil)
			require.Contains(t, diag.Reason(err, "playlist action failed"), "status=401")
			primaryErr := err
			_, err = e.ExecuteBound(ctx, f.runID, f.videoID, matches, binding)
			require.Error(t, err)
			require.Equal(t, 1, f.posts, "guard must prevent replay before run finalization")
			var logs bytes.Buffer
			p := NewPipeline(f.db, state.NewMachine(f.state), f.state, nil, nil, nil, e, f.ledger, nil, f.auth, nil, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
			p.failRun(ctx, f.runID, "action execution phase failed", primaryErr)
			run, getErr := f.state.GetRun(ctx, f.runID)
			require.NoError(t, getErr)
			require.Equal(t, state.RunStatusFailed, run.Status)
			require.Contains(t, logs.String(), "renewal_category=temporary_provider")
			if accountingFails {
				require.Contains(t, logs.String(), "accounting_diagnostic=")
				require.Contains(t, logs.String(), "code=sqlite_error")
			}
			require.NotContains(t, logs.String(), "TOKEN_SENTINEL")
			require.NotContains(t, logs.String(), "SECRET_SENTINEL")
			var guards, rows int
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, f.runID).Scan(&guards))
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id=?`, f.runID).Scan(&rows))
			if accountingFails {
				require.Zero(t, rows)
			} else {
				require.Equal(t, 1, rows)
			}
			require.Equal(t, 1, guards)
			_, err = e.ExecuteBound(ctx, f.runID, f.videoID, matches, binding)
			require.Error(t, err)
			require.Equal(t, 1, f.posts)
		})
	}
}

// guardedProductionFixture creates a production run through the explicit guarded
// capability API and composes real auth, encrypted token storage, executor,
// YouTube adapter, and auth.Dispatch. Only OAuth and Data API transports are local.
type guardedProductionFixture struct {
	db         *sql.DB
	state      *state.Store
	ledger     *quota.Ledger
	auth       *auth.Service
	binding    auth.AuthorizationBinding
	runID      int64
	videoID    int64
	decisionID int64
	routeID    int64
	target     string
	posts      int
}

func TestGuardedProviderFailureRetainsTypedCauseAndGuard(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		f.posts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}]}}`)
	}))
	defer data.Close()
	e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(data.URL))
	matches := []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}
	_, err := e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.Error(t, err)
	var apiErr *youtube.APIError
	require.ErrorAs(t, err, &apiErr, "typed provider facts must survive guarded execution")
	require.Equal(t, http.StatusInternalServerError, apiErr.Status)
	require.Contains(t, apiErr.Reasons, "backendError")
	require.NotContains(t, err.Error(), "secret")
	require.Equal(t, 1, f.posts)

	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeFailed, actions[0].Outcome)
	require.NotNil(t, actions[0].ExecutedAt)
	require.NotNil(t, actions[0].OutcomeReason)
	require.Equal(t, "mutation_application_uncertain", *actions[0].OutcomeReason)

	var guards int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.Equal(t, 1, guards, "unknown provider outcome must retain the send veto")
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	var errorID int64
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT id FROM error_entry WHERE run_id=? AND video_id=? ORDER BY id DESC LIMIT 1`, f.runID, f.videoID).Scan(&errorID))
	entry, err := qs.GetErrorEntry(ctx, errorID)
	require.NoError(t, err)
	require.Equal(t, queue.ErrorCategoryAPIError, entry.Category)
	require.Contains(t, entry.Detail, "provider_error")
	require.Contains(t, entry.Detail, "500")
	require.Contains(t, entry.Detail, "backendError")

	_, err = e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.Error(t, err)
	require.Equal(t, 1, f.posts, "re-entry must not issue a second playlist POST")
}

func TestExecuteDoesNotRewriteTerminalRunAfterDisconnect(t *testing.T) {
	for _, tt := range []struct {
		name   string
		finish func(context.Context, *state.Machine, int64) (*state.Run, error)
	}{
		{name: "completed", finish: func(ctx context.Context, machine *state.Machine, runID int64) (*state.Run, error) {
			return machine.CompleteRun(ctx, runID)
		}},
		{name: "failed", finish: func(ctx context.Context, machine *state.Machine, runID int64) (*state.Run, error) {
			return machine.FailRun(ctx, runID, "original failure")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			machine := state.NewMachine(f.state)
			before, err := tt.finish(ctx, machine, f.runID)
			require.NoError(t, err)
			require.NoError(t, f.auth.Disconnect(ctx))

			p := NewPipeline(f.db, machine, f.state, nil, nil, nil, nil, nil, nil, f.auth, nil)
			p.Execute(ctx, f.runID)

			after, err := f.state.GetRun(ctx, f.runID)
			require.NoError(t, err)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, before.InterruptReason, after.InterruptReason)
			require.Equal(t, before.CompletedAt, after.CompletedAt)
		})
	}
}

func newGuardedProductionFixture(t *testing.T) *guardedProductionFixture {
	return newGuardedProductionFixtureWithTargets(t, []string{"PLguarded-fixture"})
}

func newGuardedProductionFixtureWithTargets(t *testing.T, targets []string) *guardedProductionFixture {
	return newGuardedProductionFixtureWithRenewalStatus(t, targets, http.StatusOK)
}

func newGuardedProductionFixtureWithRenewalStatus(t *testing.T, targets []string, renewalStatus int) *guardedProductionFixture {
	t.Helper()
	db := storetest.Open(t)
	return newGuardedProductionFixtureOnDB(t, db, targets, renewalStatus)
}

func newGuardedProductionFixtureOnDB(t *testing.T, db *sql.DB, targets []string, renewalStatus int) *guardedProductionFixture {
	t.Helper()
	return newGuardedProductionFixtureWithOAuth(t, db, targets, renewalStatus, nil)
}

// newGuardedProductionFixtureWithOAuth builds the guarded fixture; a non-nil
// oauthTransport replaces the canned token endpoint (e.g. a real local server).
func newGuardedProductionFixtureWithOAuth(t *testing.T, db *sql.DB, targets []string, renewalStatus int, oauthTransport http.RoundTripper) *guardedProductionFixture {
	t.Helper()
	ctx := context.Background()
	key := []byte("01234567890123456789012345678901")
	tokens, err := auth.NewSQLiteTokenStore(db, key)
	require.NoError(t, err)
	tokenTransport := roundTripTestFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "oauth2.googleapis.com" {
			t.Fatalf("unexpected OAuth endpoint %q", req.URL.Host)
		}
		if renewalStatus != http.StatusOK {
			return responseTest(renewalStatus, `{"error":"temporarily_unavailable"}`), nil
		}
		return responseTest(http.StatusOK, `{"access_token":"fresh-access","token_type":"Bearer","expires_in":3600}`), nil
	})
	var rt http.RoundTripper = tokenTransport
	if oauthTransport != nil {
		rt = oauthTransport
	}
	svc, err := auth.NewService(&auth.ServiceConfig{ClientID: "test-client", ClientSecret: "test-secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: &http.Client{Transport: rt}}, tokens, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	// Force real renewal through the token endpoint before the protected POST.
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "expired-access", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: auth.DefaultScopes()}))
	binding, err := svc.Bind(ctx)
	require.NoError(t, err)

	ss, err := state.NewStore(db)
	require.NoError(t, err)
	target := targets[0]
	routeID, err := router.NewStore(db).CreateRoute(ctx, &router.Route{Name: "guarded fixture", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND", Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldChannelID, Operator: router.ConditionOperatorEquals, Value: "fixture-channel"}}})
	require.NoError(t, err)
	for i, target := range targets {
		_, err = db.ExecContext(ctx, `INSERT INTO route_action(route_id,action_type,target_playlist_id,action_index) VALUES(?,'playlist_insert',?,?)`, routeID, target, i)
		require.NoError(t, err)
	}
	cfgID, err := config.NewService(router.NewStore(db), config.NewStore(db)).ActiveVersionID(ctx)
	require.NoError(t, err)
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: cfgID, ActionAuthorityVersion: 1}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	runID, err := ss.CreateGuardedMutationRunTx(ctx, tx, run, true)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	video, err := state.NewMachine(ss).DiscoverVideo(ctx, runID, "guarded-external-video", "fixture-channel")
	require.NoError(t, err)
	decision := createRouteDecision(t, ctx, ss, video.ID, runID, routeID, 1, state.RouteDecisionMatched)
	return &guardedProductionFixture{db: db, state: ss, ledger: quota.New(db, quota.WithDefaultLimit(10_000)), auth: svc, binding: binding, runID: runID, videoID: video.ID, decisionID: decision, routeID: routeID, target: target}
}

func TestGuardedRealDispatchAuthorizationRefusalsDeferBeforePOST(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     int
		disconnect bool
	}{
		{name: "disconnected", status: http.StatusOK, disconnect: true},
		{name: "temporary renewal failure", status: http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixtureWithRenewalStatus(t, []string{"PLguarded-fixture"}, tt.status)
			defer f.db.Close()
			if tt.disconnect {
				require.NoError(t, f.auth.Disconnect(ctx))
			}
			data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				f.posts++
				w.WriteHeader(http.StatusOK)
			}))
			defer data.Close()
			e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(data.URL))
			qs, err := queue.NewStore(f.db)
			require.NoError(t, err)
			handoff := func(tx *sql.Tx, reason queue.DeferredReason) error {
				_, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: reason, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
				return err
			}
			outcome, err := e.ExecuteBound(withGuardedDeferral(ctx, handoff), f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
			require.NoError(t, err)
			require.Equal(t, state.FinalOutcomeDeferred, outcome)
			require.Zero(t, f.posts)
			actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
			require.NoError(t, err)
			require.Len(t, actions, 1)
			require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
			require.Equal(t, "authorization_pending", *actions[0].OutcomeReason)
			require.Nil(t, actions[0].ExecutedAt)
			var pending, guards int
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending' AND deferral_reason='authorization_pending'`, f.videoID).Scan(&pending))
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
			require.Equal(t, 1, pending)
			require.Zero(t, guards)
		})
	}
}

type roundTripTestFunc func(*http.Request) (*http.Response, error)

func (f roundTripTestFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func responseTest(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: &http.Request{}}
}

func TestGuardedProductionFixtureSmoke(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", req.Method)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer fresh-access" {
			t.Errorf("authorization=%q", got)
		}
		f.posts++
		_, _ = io.WriteString(w, `{"id":"item-1","snippet":{"playlistId":"`+f.target+`","resourceId":{"videoId":"guarded-external-video"}}}`)
	}))
	defer data.Close()
	e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(data.URL))
	_, err := e.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.NoError(t, err)
	require.Equal(t, 1, f.posts)
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeSucceeded, actions[0].Outcome)
	var calls, units int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(units_consumed),0) FROM quota_ledger WHERE run_id=?`, f.runID).Scan(&calls, &units))
	require.Equal(t, 1, calls)
	require.Equal(t, 50, units)
	var guards int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.Zero(t, guards)
}

func TestGuardedLostResponseAccountsOneAttempt(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	var posts atomic.Int32
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1) // the hijacked handler never answers, so nothing else orders this write
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer data.Close()
	e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(data.URL))
	matches := []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}
	_, err := e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.Error(t, err)
	var calls, units int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(units_consumed),0) FROM quota_ledger WHERE run_id=?`, f.runID).Scan(&calls, &units))
	require.Equal(t, 1, calls)
	require.Equal(t, 50, units)
	_, err = e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.Error(t, err)
	require.EqualValues(t, 1, posts.Load())
}

type observingService struct {
	*auth.Service
	observed int
}

func (s *observingService) ObserveAccessRejection(ctx context.Context, binding auth.AuthorizationBinding, receipt auth.AdmissionReceipt) error {
	s.observed++
	return s.Service.ObserveAccessRejection(ctx, binding, receipt)
}

func TestGuardedAccountingFailureStillObservesRejection(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	_, err := f.db.ExecContext(ctx, `CREATE TRIGGER reject_attempt BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(ABORT,'injected accounting failure'); END`)
	require.NoError(t, err)
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.posts++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"authError"}]}}`)
	}))
	defer data.Close()
	service := &observingService{Service: f.auth}
	e := NewExecutor(f.db, f.ledger, service, WithYouTubeBaseURL(data.URL))
	matches := []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}
	_, err = e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.ErrorContains(t, err, "quota accounting failed")
	require.Equal(t, 1, service.observed)
	require.Equal(t, 1, f.posts)
	var rows, guards int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM quota_ledger`).Scan(&rows))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=?`, f.runID).Scan(&guards))
	require.Zero(t, rows)
	require.Equal(t, 1, guards)
	_, err = e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.Error(t, err)
	require.Equal(t, 1, f.posts, "uncertain POST is never replayed")
}

type refusingDispatchService struct {
	*auth.Service
	err error
}

func (s refusingDispatchService) DispatchWithQuota(context.Context, auth.AuthorizationBinding, *http.Request, http.RoundTripper, *quota.Ledger, quota.Operation) (*http.Response, auth.AdmissionReceipt, *quota.Hold, error) {
	return nil, auth.AdmissionReceipt{}, nil, s.err
}

func TestGuardedAuthorizationRefusalCommitsHandoffAtomically(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	// The refusing dispatcher is the controlled authorization boundary; the
	// underlying real service, SQLite stores, adapter, executor and queue remain real.
	e := NewExecutor(f.db, f.ledger, refusingDispatchService{Service: f.auth, err: youtube.DispatchRefusalError{Kind: youtube.DispatchRefusalAuthorization}}, WithYouTubeBaseURL("http://127.0.0.1:1"))
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	handoff := func(tx *sql.Tx, reason queue.DeferredReason) error {
		_, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: reason, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		return err
	}
	outcome, err := e.ExecuteBound(withGuardedDeferral(ctx, handoff), f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, outcome)
	require.Zero(t, f.posts, "authorization refusal must happen before transport POST")
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
	require.Equal(t, "authorization_pending", *actions[0].OutcomeReason)
	require.Nil(t, actions[0].ExecutedAt)
	var pending, guards int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending' AND deferral_reason='authorization_pending'`, f.videoID).Scan(&pending))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.Equal(t, 1, pending)
	require.Zero(t, guards, "guard is released only in committed queue handoff")
}

func TestGuardedAuthorizationHandoffFailureRetainsGuardAndActionTransaction(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	_, err := f.db.ExecContext(ctx, `CREATE TRIGGER reject_auth_pending BEFORE INSERT ON deferred_item WHEN NEW.deferral_reason='authorization_pending' BEGIN SELECT RAISE(ABORT,'injected queue failure'); END`)
	require.NoError(t, err)
	e := NewExecutor(f.db, f.ledger, refusingDispatchService{Service: f.auth, err: youtube.DispatchRefusalError{Kind: youtube.DispatchRefusalAuthorization}}, WithYouTubeBaseURL("http://127.0.0.1:1"))
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	handoff := func(tx *sql.Tx, reason queue.DeferredReason) error {
		_, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: reason, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		return err
	}
	_, err = e.ExecuteBound(withGuardedDeferral(ctx, handoff), f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.Error(t, err)
	var guards, actions, pending int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&actions))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending' AND deferral_reason='authorization_pending'`, f.videoID).Scan(&pending))
	require.Equal(t, 1, guards)
	require.Zero(t, actions, "failed handoff rolls back no-attempt evidence too")
	require.Zero(t, pending)
}

func TestGuardedNonAuthorizationOrUnconfirmedRefusalFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "local validation", err: youtube.DispatchRefusalError{Kind: youtube.DispatchRefusalLocalValidation}},
		{name: "missing admission receipt", err: errors.New("dispatch failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			e := NewExecutor(f.db, f.ledger, refusingDispatchService{Service: f.auth, err: tt.err}, WithYouTubeBaseURL("http://127.0.0.1:1"))
			_, err := e.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
			require.Error(t, err)
			var guards, actions, pending int
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&actions))
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending'`, f.videoID).Scan(&pending))
			require.Equal(t, 1, guards)
			require.Zero(t, actions)
			require.Zero(t, pending)
		})
	}
}

func TestGuardedFinalQuotaRefusalAndProviderQuota(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int
		provider bool
		rollback bool
	}{
		{name: "final refusal", limit: 1},
		{name: "handoff rollback", limit: 1, rollback: true},
		{name: "admitted provider refusal", limit: 10000, provider: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			f.ledger = quota.New(f.db, quota.WithDefaultLimit(tc.limit))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.posts++
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403}}`)
			}))
			defer server.Close()
			qs, err := queue.NewStore(f.db)
			require.NoError(t, err)
			handoff := func(tx *sql.Tx, reason queue.DeferredReason) error {
				_, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: reason, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
				if err != nil {
					return err
				}
				if tc.rollback {
					return errors.New("handoff failed")
				}
				return nil
			}
			e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(server.URL))
			outcome, err := e.executeGuardedAction(withGuardedDeferral(ctx, handoff), f.runID, f.videoID, "guarded-external-video", action{RouteDecisionID: f.decisionID, ActionType: "playlist_insert", TargetPlaylistID: f.target}, &f.binding)
			if tc.rollback {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, state.ActionOutcomeDeferred, outcome)
			}
			if tc.provider {
				require.Equal(t, 1, f.posts)
			} else {
				require.Zero(t, f.posts)
			}
			actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
			require.NoError(t, err)
			var guards, pending, rows, units int
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&guards))
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='quota_exhausted'`).Scan(&pending))
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(units_consumed),0) FROM quota_ledger`).Scan(&rows, &units))
			if tc.rollback {
				require.Equal(t, 1, guards)
				require.Zero(t, pending)
				require.Empty(t, actions)
			} else {
				require.Zero(t, guards)
				require.Equal(t, 1, pending)
				require.Len(t, actions, 1)
				require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
				require.Equal(t, "quota_exhausted", *actions[0].OutcomeReason)
				require.Nil(t, actions[0].ExecutedAt)
			}
			if tc.provider {
				require.Equal(t, 1, rows)
				require.Equal(t, 50, units)
			} else {
				require.Zero(t, rows)
				require.Zero(t, units)
			}
		})
	}
}

func TestGuardedFinalQuotaRefusalRetryReceipt(t *testing.T) {
	for _, mode := range []string{"owned", "removed", "wrong owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			f.ledger = quota.New(f.db, quota.WithDefaultLimit(1))
			qs, err := queue.NewStore(f.db)
			require.NoError(t, err)
			id, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
			require.NoError(t, f.db.QueryRow(`SELECT config_version_id FROM run WHERE id=?`, f.runID).Scan(&run.ConfigVersionID))
			var receipt queue.RetryGroupReceipt
			require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var err error
				run.ID, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
				if err != nil {
					return err
				}
				var claimed bool
				receipt, _, claimed, err = qs.ClaimDeferredGroupReceiptTx(ctx, tx, id, run.ID, time.Now())
				if err != nil {
					return err
				}
				if !claimed {
					return errors.New("not claimed")
				}
				return nil
			}))
			decision := createRouteDecision(t, ctx, f.state, f.videoID, run.ID, f.routeID, 1, state.RouteDecisionMatched)
			if mode == "removed" {
				require.NoError(t, qs.DiscardDeferredItem(ctx, id))
			}
			if mode == "wrong owner" {
				_, err = f.db.Exec(`UPDATE deferred_item SET retry_run_id=? WHERE id=?`, f.runID, id)
				require.NoError(t, err)
			}
			handoff := func(tx *sql.Tx, reason queue.DeferredReason) error { return nil }
			retryCtx := context.WithValue(withGuardedDeferral(ctx, handoff), guardedRetryGroupKey{}, receipt)
			e := NewExecutor(f.db, f.ledger, f.auth)
			outcome, err := e.executeGuardedAction(retryCtx, run.ID, f.videoID, "guarded-external-video", action{RouteDecisionID: decision, ActionType: "playlist_insert", TargetPlaylistID: f.target}, &f.binding)
			var guards, rows int
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=?`, run.ID).Scan(&guards))
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM quota_ledger`).Scan(&rows))
			require.Zero(t, rows)
			actions, readErr := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, run.ID)
			require.NoError(t, readErr)
			item, readErr := qs.GetDeferredItem(ctx, id)
			require.NoError(t, readErr)
			if mode == "owned" {
				require.NoError(t, err)
				require.Equal(t, state.ActionOutcomeDeferred, outcome)
				require.Zero(t, guards)
				require.Len(t, actions, 1)
				require.Nil(t, actions[0].ExecutedAt)
				require.Equal(t, queue.DeferredStatusPending, item.Status)
				require.Equal(t, run.ID, *item.RetryRunID)
			} else {
				require.Error(t, err)
				require.Equal(t, 1, guards)
				require.Empty(t, actions)
				if mode == "removed" {
					require.Equal(t, queue.DeferredStatusDiscarded, item.Status)
				} else {
					require.Equal(t, f.runID, *item.RetryRunID)
				}
			}
		})
	}
}
