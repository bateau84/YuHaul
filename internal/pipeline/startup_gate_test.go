package pipeline

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store/storetest"
)

// ADR-007 required test 6: cancellation while classification is blocked inside
// its transaction rolls back, and the next start classifies the same rows once.
func TestStartupClassificationCancelledInsideTransactionIsRetriedOnce(t *testing.T) {
	db, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()
	_, err := db.Exec(`INSERT INTO config_version (id,snapshot) VALUES (1,'{"routes":[]}');
		INSERT INTO run (started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version,current_phase) VALUES ('2026-01-01','manual','running',1,1,1,'routing');
		INSERT INTO run (started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version) VALUES ('2026-01-02','manual','interrupted',1,1,1)`)
	require.NoError(t, err)
	p := &Pipeline{db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// A trigger makes the classification UPDATE run until it is interrupted,
	// which pauses the process inside its transaction.
	ctx := context.Background()
	_, err = db.Exec(`CREATE TRIGGER pause_startup BEFORE UPDATE ON run BEGIN
		SELECT count(*) FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT x FROM c LIMIT 4000000000);
	END`)
	require.NoError(t, err)

	cctx, cancel := context.WithCancel(ctx)
	type result struct {
		ids []int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		ids, err := p.BeginStartupRecovery(cctx)
		done <- result{ids, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("classification finished while the writer was blocked: %v", r.err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel() // SIGTERM
	var r result
	select {
	case r = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("classification did not stop on cancellation")
	}
	require.Error(t, r.err)
	require.Nil(t, r.ids)

	_, err = db.Exec(`DROP TRIGGER pause_startup`)
	require.NoError(t, err)

	var running int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM run WHERE status='running'`).Scan(&running))
	require.Equal(t, 1, running, "the cancelled classification must roll back")

	first, err := p.BeginStartupRecovery(ctx)
	require.NoError(t, err)
	require.Len(t, first, 2)
	again, err := p.BeginStartupRecovery(ctx)
	require.NoError(t, err)
	require.Equal(t, first, again, "classification is idempotent: same rows, no duplicates")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM run WHERE status='running'`).Scan(&running))
	require.Zero(t, running)
}

type cancelBeforeDispatch struct {
	*auth.Service
	cancel context.CancelFunc
}

// DispatchWithQuota ends the caller context after the guard claim committed
// and before the real dispatch can admit a request.
func (s cancelBeforeDispatch) DispatchWithQuota(ctx context.Context, b auth.AuthorizationBinding, req *http.Request, rt http.RoundTripper, l *quota.Ledger, op quota.Operation) (*http.Response, auth.AdmissionReceipt, *quota.Hold, error) {
	s.cancel()
	return s.Service.DispatchWithQuota(ctx, b, req, rt, l, op)
}

// ADR-007 required test 5: shutdown between guard claim and admission sends
// nothing, removes the claiming invocation's own guard together with a
// no-attempt row, and the run becomes interrupted rather than failed.
func TestCancelBetweenClaimAndAdmissionReleasesGuardAndInterruptsRun(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	var posts atomic.Int32
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer data.Close()

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e := NewExecutor(f.db, f.ledger, cancelBeforeDispatch{Service: f.auth, cancel: cancel}, WithYouTubeBaseURL(data.URL))
	matches := []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}
	_, err := e.ExecuteBound(cctx, f.runID, f.videoID, matches, f.binding)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, posts.Load(), "nothing is sent after cancellation")

	var guards int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.Zero(t, guards, "the claiming invocation removes its own guard")
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
	require.Nil(t, actions[0].ExecutedAt, "no attempt time without admission")
	var sinks int
	require.NoError(t, f.db.QueryRow(`SELECT (SELECT count(*) FROM error_entry)+(SELECT count(*) FROM deferred_item)`).Scan(&sinks))
	require.Zero(t, sinks, "cancellation creates no queue entry")

	// The pipeline turns the executor's stop into an interrupted run.
	p := NewPipeline(f.db, state.NewMachine(f.state), f.state, nil, nil, nil, e, f.ledger, nil, f.auth, nil,
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	p.failRun(cctx, f.runID, "action execution phase failed", err)
	run, err := f.state.GetRun(ctx, f.runID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusInterrupted, run.Status)
	require.Nil(t, run.CompletedAt)
	require.NotNil(t, run.InterruptReason)
	require.Contains(t, *run.InterruptReason, "code=canceled")
	require.NoError(t, f.db.QueryRow(`SELECT (SELECT count(*) FROM error_entry)+(SELECT count(*) FROM deferred_item)`).Scan(&sinks))
	require.Zero(t, sinks, "no ADR-006 sweep runs for a cancelled run")
}

// rewriteHostTransport sends every request to a local server over a real
// http.Transport, so context cancellation surfaces exactly as in production.
type rewriteHostTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = t.base.Scheme, t.base.Host
	return t.rt.RoundTrip(out)
}

// Cancellation during a real OAuth refresh (x/oauth2 flattens the transport
// error with %v) must be treated as cancellation: the executor releases its own
// guard with a no-attempt row, nothing is POSTed, no renewal failure is
// persisted, and the same process renews on the next run.
func TestCancelDuringRealRefreshReleasesGuardAndNextRunRenews(t *testing.T) {
	ctx := context.Background()
	held := make(chan struct{}, 1)
	var tokenHits, blocked atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client disconnect
		if blocked.Add(1) == 1 {
			held <- struct{}{}
			<-r.Context().Done() // hold the request until the client gives up
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"fresh-access","token_type":"Bearer","expires_in":3600}`)
	}))
	defer oauth.Close()
	base, err := url.Parse(oauth.URL)
	require.NoError(t, err)
	rt := rewriteHostTransport{base: base, rt: &http.Transport{DisableKeepAlives: true}}

	f := newGuardedProductionFixtureWithOAuth(t, storetest.Open(t), []string{"PLguarded-fixture"}, http.StatusOK, rt)
	defer f.db.Close()
	var posts atomic.Int32
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_, _ = io.WriteString(w, `{"id":"item-1","snippet":{"playlistId":"`+f.target+`","resourceId":{"videoId":"guarded-external-video"}}}`)
	}))
	defer data.Close()
	e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(data.URL))
	matches := []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := e.ExecuteBound(cctx, f.runID, f.videoID, matches, f.binding)
		done <- err
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh request never reached the OAuth server")
	}
	cancel()
	var execErr error
	select {
	case execErr = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not stop on cancellation")
	}
	require.ErrorIs(t, execErr, context.Canceled)
	require.Zero(t, posts.Load(), "no POST after cancelled refresh")

	var guards int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.Zero(t, guards, "the cancelled invocation releases its own guard")
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
	require.Nil(t, actions[0].ExecutedAt)

	// Same process: cancellation left no sticky or persisted renewal failure.
	status, err := f.auth.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "renewal_available", status.AuthorizationState, "status after cancelled renewal")
	require.Nil(t, status.AuthorizationReason)

	outcome, err := e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeAccepted, outcome)
	require.EqualValues(t, 1, posts.Load())
	require.EqualValues(t, 2, tokenHits.Load(), "the next run refreshed once more")
	status, err = f.auth.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "usable", status.AuthorizationState)
}

// ADR-007 required test 8: the gate exists only for server composition and
// covers only the production producers.
func TestProducerGateScope(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()
	routerStore := router.NewStore(db)
	newPipeline := func() *Pipeline {
		return &Pipeline{
			db: db, stateStore: stateStore, machine: machine, broadcast: broadcast,
			configStore: config.NewStore(db),
			versioner:   config.NewService(routerStore, config.NewStore(db)),
			logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
	}

	// A Pipeline that never began startup recovery is not gated.
	ungated := newPipeline()
	run, err := ungated.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.NoError(t, stateStore.InterruptRun(ctx, run.ID, "test"))

	// Gated: producers refuse and write nothing.
	gated := newPipeline()
	gated.queueStore, err = queue.NewStore(db)
	require.NoError(t, err)
	ids, err := gated.BeginStartupRecovery(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{run.ID}, ids)
	count := func(table string) (n int) {
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		return n
	}
	runs, versions := count("run"), count("config_version")
	_, err = gated.StartRun(ctx, state.RunTypeManual)
	require.ErrorIs(t, err, ErrStartupRecoveryActive)
	_, err = gated.StartRun(ctx, state.RunTypeScheduled)
	require.ErrorIs(t, err, ErrStartupRecoveryActive)
	require.ErrorIs(t, gated.RetryQueueEntry(ctx, 1, true), ErrStartupRecoveryActive)
	require.ErrorIs(t, gated.RetryQueueEntry(ctx, 1, false), ErrStartupRecoveryActive)
	require.Equal(t, runs, count("run"))
	require.Equal(t, versions, count("config_version"))

	// Not gated: simulations, reads, configuration edits and queue dismissal.
	dry, err := gated.DryRun(ctx, nil)
	require.NoError(t, err)
	got, err := stateStore.GetRun(ctx, dry.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunTypeDryRun, got.RunType)
	_, err = routerStore.CreateRoute(ctx, &router.Route{Name: "edit during recovery", EvaluationOrder: 1, IsEnabled: true, ConditionComposition: "AND",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "x"}}})
	require.NoError(t, err)
	res, err := db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES ('v-dismiss','c',?,'not_yet_evaluated','not_yet_evaluated')`, run.ID)
	require.NoError(t, err)
	videoID, err := res.LastInsertId()
	require.NoError(t, err)
	qs := gated.queueStore
	entryID, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: videoID, RunID: run.ID, Category: queue.ErrorCategoryRuntime, Message: "m", Detail: "d", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	svc := queue.NewService(qs, nil).WithRetryCoordinator(gated)
	dismissed, err := svc.DismissError(ctx, entryID)
	require.NoError(t, err)
	require.Equal(t, queue.ErrorStatusDismissed, dismissed.Status)

	// The queue service surfaces the typed refusal for the HTTP mapping.
	_, err = svc.RetryError(ctx, entryID)
	require.ErrorIs(t, err, queue.ErrStartupRecoveryActive)

	// One-way: opening is final, and BeginStartupRecovery cannot close it again.
	gated.OpenProducers()
	_, err = gated.BeginStartupRecovery(ctx)
	require.NoError(t, err)
	require.NoError(t, gated.refuseWhileRecovering())
	require.False(t, strings.Contains(ErrStartupRecoveryActive.Error(), "\n"))
	require.Equal(t, "startup recovery in progress", ErrStartupRecoveryActive.Error())
}
