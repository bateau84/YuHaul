//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestGuardedRetryFinalQuotaRefusalPipeline(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "committed"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, nil)
			p := env.pipeline
			db := p.db
			_, err := db.Exec(`UPDATE route_condition SET field='channel_id', operator='equals', value='UC1' WHERE route_id=?`, env.routeID)
			require.NoError(t, err)
			qs, err := queue.NewStore(db)
			require.NoError(t, err)
			p.queueStore = qs
			require.NoError(t, p.ledger.SetDailyLimit(ctx, 50))
			key := []byte("01234567890123456789012345678901")
			tokens, err := auth.NewSQLiteTokenStore(db, key)
			require.NoError(t, err)
			var competing *quota.Hold
			transport := roundTripTestFunc(func(r *http.Request) (*http.Response, error) {
				// Renewal happens after the executor preflight. Reserve one unit on
				// the same ledger before final admission, without issuing a fake call.
				var err error
				competing, err = p.ledger.Acquire(ctx, quota.OpVideosList)
				require.NoError(t, err)
				require.NotNil(t, competing)
				return responseTest(http.StatusOK, `{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`), nil
			})
			svc, err := auth.NewService(&auth.ServiceConfig{ClientID: "test", ClientSecret: "test", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: &http.Client{Transport: transport}}, tokens, auth.NewInMemoryStateStore())
			require.NoError(t, err)
			require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: auth.DefaultScopes()}))
			binding, err := svc.Bind(ctx)
			require.NoError(t, err)
			p.auth = svc
			p.executionBinding = &binding
			var requests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
			defer provider.Close()
			p.executor = NewExecutor(db, p.ledger, svc, WithYouTubeBaseURL(provider.URL))
			createRun := func() *state.Run {
				run, err := p.prepareProductionRun(ctx, state.RunTypeManual)
				require.NoError(t, err)
				tx, err := db.BeginTx(ctx, nil)
				require.NoError(t, err)
				run.ID, err = p.stateStore.CreateGuardedMutationRunTx(ctx, tx, run, true)
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				return run
			}
			origin := createRun()
			video, err := p.machine.DiscoverVideo(ctx, origin.ID, "V1", "UC1")
			require.NoError(t, err)
			id, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: video.ID, RunID: origin.ID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			receiving := createRun()
			var receipt queue.RetryGroupReceipt
			require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var claimed bool
				receipt, _, claimed, err = qs.ClaimDeferredGroupReceiptTx(ctx, tx, id, receiving.ID, time.Now())
				require.True(t, claimed)
				return err
			}))
			if rollback {
				_, err = db.Exec(`CREATE TRIGGER reject_deferred_video BEFORE UPDATE OF final_outcome ON video WHEN NEW.final_outcome='deferred' BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
				require.NoError(t, err)
			}
			err = p.processRetryVideo(context.WithValue(ctx, guardedRetryGroupKey{}, receipt), receiving.ID, video.ID)
			if rollback {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, competing, "final admission must follow successful preflight and renewal")
			require.Zero(t, requests.Load())
			require.NoError(t, competing.Cancel())
			available, err := p.ledger.Acquire(ctx, quota.OpPlaylistItemsInsert)
			require.NoError(t, err)
			require.NotNil(t, available, "refused mutation must not retain a hold")
			require.NoError(t, available.Cancel())
			decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, receiving.ID)
			require.NoError(t, err)
			require.Len(t, decisions, 1)
			require.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
			var guards, rows int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, receiving.ID, video.ID).Scan(&guards))
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id=?`, receiving.ID).Scan(&rows))
			require.Zero(t, rows)
			actions, err := p.stateStore.GetExecutedActionsForVideoInRun(ctx, video.ID, receiving.ID)
			require.NoError(t, err)
			item, err := qs.GetDeferredItem(ctx, id)
			require.NoError(t, err)
			require.Equal(t, receiving.ID, *item.RetryRunID)
			if rollback {
				require.Equal(t, 1, guards)
				require.Empty(t, actions)
				require.Equal(t, queue.DeferredStatusRetrying, item.Status)
			} else {
				require.Zero(t, guards)
				require.Equal(t, queue.DeferredStatusPending, item.Status)
				require.Len(t, actions, 1)
				require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
				require.Equal(t, "quota_exhausted", *actions[0].OutcomeReason)
				require.Nil(t, actions[0].ExecutedAt)
				v, err := p.stateStore.GetVideo(ctx, video.ID)
				require.NoError(t, err)
				require.Equal(t, state.FinalOutcomeDeferred, v.FinalOutcome)
			}
		})
	}
}

func TestGuardedFreshFinalQuotaRefusalPipeline(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, nil)
	p := env.pipeline
	db := p.db
	_, err := db.Exec(`UPDATE route_condition SET field='channel_id', operator='equals', value='UC1' WHERE route_id=?`, env.routeID)
	require.NoError(t, err)
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	p.queueStore = qs
	require.NoError(t, p.ledger.SetDailyLimit(ctx, 50))
	key := []byte("01234567890123456789012345678901")
	tokens, err := auth.NewSQLiteTokenStore(db, key)
	require.NoError(t, err)
	var competing *quota.Hold
	transport := roundTripTestFunc(func(r *http.Request) (*http.Response, error) {
		var acquireErr error
		competing, acquireErr = p.ledger.Acquire(ctx, quota.OpVideosList)
		require.NoError(t, acquireErr)
		require.NotNil(t, competing)
		return responseTest(http.StatusOK, `{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`), nil
	})
	svc, err := auth.NewService(&auth.ServiceConfig{ClientID: "test", ClientSecret: "test", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: &http.Client{Transport: transport}}, tokens, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: auth.DefaultScopes()}))
	p.auth = svc
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer provider.Close()
	p.executor = NewExecutor(db, p.ledger, svc, WithYouTubeBaseURL(provider.URL))
	run, err := p.prepareProductionRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	run.ID, err = p.stateStore.CreateGuardedMutationRunTx(ctx, tx, run, true)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	video, err := p.machine.DiscoverVideo(ctx, run.ID, "V1", "UC1")
	require.NoError(t, err)
	require.NoError(t, p.stateStore.SetRunPhase(ctx, run.ID, string(PhaseExclusion)))
	require.NoError(t, p.stateStore.InterruptRun(ctx, run.ID, "fixture restart"))
	p.Execute(ctx, run.ID)
	require.NotNil(t, competing, "renewal after preflight must reserve the competing unit")
	require.Zero(t, requests.Load())
	require.NoError(t, competing.Cancel())
	available, err := p.ledger.Acquire(ctx, quota.OpPlaylistItemsInsert)
	require.NoError(t, err)
	require.NotNil(t, available, "refused mutation must not retain a hold")
	require.NoError(t, available.Cancel())
	decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	require.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	var guards, rows, pending int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, run.ID, video.ID).Scan(&guards))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id=?`, run.ID).Scan(&rows))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM deferred_item WHERE video_id=? AND run_id=? AND status='pending' AND deferral_reason='quota_exhausted'`, video.ID, run.ID).Scan(&pending))
	require.Zero(t, guards)
	require.Zero(t, rows)
	require.Equal(t, 1, pending)
	actions, err := p.stateStore.GetExecutedActionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
	require.Equal(t, "quota_exhausted", *actions[0].OutcomeReason)
	require.Nil(t, actions[0].ExecutedAt)
	v, err := p.stateStore.GetVideo(ctx, video.ID)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, v.FinalOutcome)
}

func TestGuardedRetryPreflightQuotaRefusalPipeline(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "committed"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, nil)
			p := env.pipeline
			db := p.db
			_, err := db.Exec(`UPDATE route_condition SET field='channel_id', operator='equals', value='UC1' WHERE route_id=?`, env.routeID)
			require.NoError(t, err)
			qs, err := queue.NewStore(db)
			require.NoError(t, err)
			p.queueStore = qs
			require.NoError(t, p.ledger.SetDailyLimit(ctx, 49))
			key := []byte("01234567890123456789012345678901")
			tokens, err := auth.NewSQLiteTokenStore(db, key)
			require.NoError(t, err)
			var competing *quota.Hold
			transport := roundTripTestFunc(func(r *http.Request) (*http.Response, error) {
				// Renewal happens after the executor preflight. Reserve one unit on
				// the same ledger before final admission, without issuing a fake call.
				var err error
				competing, err = p.ledger.Acquire(ctx, quota.OpVideosList)
				require.NoError(t, err)
				require.NotNil(t, competing)
				return responseTest(http.StatusOK, `{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`), nil
			})
			svc, err := auth.NewService(&auth.ServiceConfig{ClientID: "test", ClientSecret: "test", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: &http.Client{Transport: transport}}, tokens, auth.NewInMemoryStateStore())
			require.NoError(t, err)
			require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: auth.DefaultScopes()}))
			binding, err := svc.Bind(ctx)
			require.NoError(t, err)
			p.auth = svc
			p.executionBinding = &binding
			var requests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
			defer provider.Close()
			p.executor = NewExecutor(db, p.ledger, svc, WithYouTubeBaseURL(provider.URL))
			createRun := func() *state.Run {
				run, err := p.prepareProductionRun(ctx, state.RunTypeManual)
				require.NoError(t, err)
				tx, err := db.BeginTx(ctx, nil)
				require.NoError(t, err)
				run.ID, err = p.stateStore.CreateGuardedMutationRunTx(ctx, tx, run, true)
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				return run
			}
			origin := createRun()
			video, err := p.machine.DiscoverVideo(ctx, origin.ID, "V1", "UC1")
			require.NoError(t, err)
			id, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: video.ID, RunID: origin.ID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			receiving := createRun()
			var receipt queue.RetryGroupReceipt
			require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var claimed bool
				receipt, _, claimed, err = qs.ClaimDeferredGroupReceiptTx(ctx, tx, id, receiving.ID, time.Now())
				require.True(t, claimed)
				return err
			}))
			if rollback {
				_, err = db.Exec(`CREATE TRIGGER reject_deferred_video BEFORE UPDATE OF final_outcome ON video WHEN NEW.final_outcome='deferred' BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
				require.NoError(t, err)
			}
			err = p.processRetryVideo(context.WithValue(ctx, guardedRetryGroupKey{}, receipt), receiving.ID, video.ID)
			if rollback {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Nil(t, competing, "preflight must not renew or dispatch")
			require.Zero(t, requests.Load())
			decisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, receiving.ID)
			require.NoError(t, err)
			require.Len(t, decisions, 1)
			require.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
			var guards, rows int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, receiving.ID, video.ID).Scan(&guards))
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id=?`, receiving.ID).Scan(&rows))
			require.Zero(t, rows)
			actions, err := p.stateStore.GetExecutedActionsForVideoInRun(ctx, video.ID, receiving.ID)
			require.NoError(t, err)
			item, err := qs.GetDeferredItem(ctx, id)
			require.NoError(t, err)
			require.Equal(t, receiving.ID, *item.RetryRunID)
			if rollback {
				require.Zero(t, guards)
				require.Empty(t, actions)
				require.Equal(t, queue.DeferredStatusRetrying, item.Status)
			} else {
				require.Zero(t, guards)
				require.Equal(t, queue.DeferredStatusPending, item.Status)
				require.Len(t, actions, 1)
				require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
				require.Equal(t, "quota_exhausted", *actions[0].OutcomeReason)
				require.Nil(t, actions[0].ExecutedAt)
				v, err := p.stateStore.GetVideo(ctx, video.ID)
				require.NoError(t, err)
				require.Equal(t, state.FinalOutcomeDeferred, v.FinalOutcome)
			}
		})
	}
}
