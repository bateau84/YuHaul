package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestClaimedDeferredRetryQuotaWithConcurrentDiscard(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()

	routerStore, configStore := router.NewStore(f.db), config.NewStore(f.db)
	versioner := config.NewService(routerStore, configStore)
	queueStore, err := queue.NewStore(f.db)
	require.NoError(t, err)
	itemID, err := queueStore.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted,
		Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	siblingID, err := queueStore.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted,
		Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	// Use a real guarded receiving run and claim the exact deferred group in the
	// same transaction, matching the production retry authorization boundary.
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	run.ActionAuthorityVersion = 1
	var receipt queue.RetryGroupReceipt
	require.NoError(t, queueStore.WithRetryTx(ctx, func(tx *sql.Tx) error {
		run.ID, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
		if err != nil {
			return err
		}
		var claimed bool
		receipt, _, claimed, err = queueStore.ClaimDeferredGroupReceiptTx(ctx, tx, itemID, run.ID, time.Now())
		if err != nil {
			return err
		}
		if !claimed {
			return errRetryClaimLost
		}
		_, err = tx.ExecContext(ctx, `UPDATE run SET current_phase=? WHERE id=?`, string(PhaseActionExecution), run.ID)
		return err
	}))
	require.Len(t, receipt.Deferred, 2)

	entered, release := make(chan struct{}), make(chan struct{})
	var posts int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		close(entered)
		<-release
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`)
	}))
	defer provider.Close()
	executor := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))
	p := &Pipeline{
		db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: queueStore, auth: f.auth,
		versioner: versioner, configStore: configStore, router: router.NewEngine(routerStore),
		detector: duplicate.NewDetector(f.db),
		meta:     metadata.NewManager(metadata.NewStore(f.db), nil, nil),
		logger:   discardLogger(), executor: executor,
	}
	p.executionBinding = &f.binding
	p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
	retryCtx := context.WithValue(ctx, guardedRetryGroupKey{}, receipt)
	finished := make(chan error, 1)
	go func() { finished <- p.processRetryVideo(retryCtx, run.ID, f.videoID) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("retry did not reach the provider")
	}

	// Discard while the provider request is outstanding. The subsequent quota
	// handoff may retain the still-owned sibling, but must not resurrect this row.
	require.NoError(t, queueStore.DiscardDeferredItem(ctx, itemID))
	close(release)
	result := <-finished
	require.NoError(t, result)
	require.Equal(t, 1, posts)
	runVideos, err := p.listRunVideos(ctx, run.ID)
	require.NoError(t, err)
	filtered, err := p.withoutRetryVideos(ctx, run.ID, runVideos)
	require.NoError(t, err)
	require.Empty(t, filtered, "retained retry ownership excludes the video from later phases of this run")

	old, err := queueStore.GetDeferredItem(ctx, itemID)
	require.NoError(t, err)
	require.Equal(t, queue.DeferredStatusDiscarded, old.Status)
	sibling, err := queueStore.GetDeferredItem(ctx, siblingID)
	require.NoError(t, err)
	require.Equal(t, queue.DeferredStatusPending, sibling.Status, "retained quota work is eligible after the run")
	require.NotNil(t, sibling.RetryRunID)
	require.Equal(t, run.ID, *sibling.RetryRunID)
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, run.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
	require.NotNil(t, actions[0].OutcomeReason)
	require.Equal(t, "quota_exhausted", *actions[0].OutcomeReason)
	require.Nil(t, actions[0].ExecutedAt)
	var pending int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending'`, f.videoID).Scan(&pending))
	require.Equal(t, 1, pending)
	runVideos, err = p.listRunVideos(ctx, run.ID)
	require.NoError(t, err)
	filtered, err = p.withoutRetryVideos(ctx, run.ID, runVideos)
	require.NoError(t, err)
	require.Empty(t, filtered, "pending retained work remains excluded from this run by its retry owner")
	var guards int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&guards))
	require.Zero(t, guards, "guard is released only with the committed sibling handoff")

	p.completeRun(ctx, run.ID)
	candidates, err := queueStore.ListDeferredRetryCandidates(ctx, time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	require.Len(t, candidates, 1, "quota-refused work is claimable after the receiving run completes")
	require.Equal(t, f.videoID, candidates[0].VideoID)
	laterRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	laterRun.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NoError(t, queueStore.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimErr error
		laterRun.ID, claimErr = f.state.CreateGuardedMutationRunTx(ctx, tx, laterRun, true)
		if claimErr != nil {
			return claimErr
		}
		_, _, claimed, claimErr := queueStore.ClaimDeferredGroupReceiptTx(ctx, tx, candidates[0].ID, laterRun.ID, time.Now().Add(24*time.Hour))
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			return errRetryClaimLost
		}
		return nil
	}))
}

func TestClaimedDeferredRetryRequeueWriteFailurePreservesGuardAndActionTransaction(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()

	routerStore, configStore := router.NewStore(f.db), config.NewStore(f.db)
	versioner := config.NewService(routerStore, configStore)
	queueStore, err := queue.NewStore(f.db)
	require.NoError(t, err)
	itemID, err := queueStore.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted,
		Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	errorID, err := queueStore.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime, Message: "prior error", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)

	// Use a real guarded receiving run and claim the exact deferred group in the
	// same transaction, matching the production retry authorization boundary.
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	run.ActionAuthorityVersion = 1
	var receipt queue.RetryGroupReceipt
	require.NoError(t, queueStore.WithRetryTx(ctx, func(tx *sql.Tx) error {
		run.ID, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
		if err != nil {
			return err
		}
		var claimed bool
		receipt, _, claimed, err = queueStore.ClaimErrorGroupReceiptTx(ctx, tx, errorID, run.ID, time.Now())
		if err != nil {
			return err
		}
		if !claimed {
			return errRetryClaimLost
		}
		_ = errorID
		_, err = tx.ExecContext(ctx, `UPDATE run SET current_phase=? WHERE id=?`, string(PhaseActionExecution), run.ID)
		return err
	}))
	require.Len(t, receipt.Deferred, 1)

	var posts int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`)
	}))
	_, err = f.db.ExecContext(ctx, `CREATE TRIGGER reject_retry_ack BEFORE UPDATE OF status ON error_entry WHEN OLD.status='retrying' AND NEW.status='resolved' BEGIN SELECT RAISE(ABORT,'injected acknowledgement failure'); END`)
	require.NoError(t, err)
	defer provider.Close()
	executor := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))
	p := &Pipeline{
		db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: queueStore, auth: f.auth,
		versioner: versioner, configStore: configStore, router: router.NewEngine(routerStore),
		detector: duplicate.NewDetector(f.db),
		meta:     metadata.NewManager(metadata.NewStore(f.db), nil, nil),
		logger:   discardLogger(), executor: executor,
	}
	p.executionBinding = &f.binding
	p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
	retryCtx := context.WithValue(ctx, guardedRetryGroupKey{}, receipt)
	err = p.processRetryVideo(retryCtx, run.ID, f.videoID)
	require.Error(t, err, "failed guarded handoff must be reported")
	require.NotErrorIs(t, err, queue.ErrWorkRemoved)
	require.Equal(t, 1, posts)

	var guards, actions, retrying, pending, deferredCount int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&guards))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&actions))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE id=? AND status='retrying' AND retry_run_id=?`, itemID, run.ID).Scan(&retrying))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending'`, f.videoID).Scan(&pending))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, f.videoID).Scan(&deferredCount))
	require.Equal(t, 1, guards, "requeue failure rolls back guard release")
	require.Equal(t, 1, retrying, "failed handoff preserves exact retry ownership")
	require.Zero(t, pending, "failed handoff does not create or requeue work")
	require.Equal(t, 1, deferredCount, "failed handoff does not create a replacement sink")
	require.Zero(t, actions, "requeue failure rolls back requested-run action evidence")
}

func TestRetryActionFailureDoesNotRecreateDismissedClaim(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	errorID, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime, Message: "old", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	versioner := config.NewService(router.NewStore(f.db), config.NewStore(f.db))
	run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	run.ActionAuthorityVersion = 1
	var receipt queue.RetryGroupReceipt
	require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		run.ID, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
		if err != nil {
			return err
		}
		var claimed bool
		receipt, _, claimed, err = qs.ClaimErrorGroupReceiptTx(ctx, tx, errorID, run.ID, time.Now())
		if err == nil && !claimed {
			return errRetryClaimLost
		}
		return err
	}))
	entered, release := make(chan struct{}), make(chan struct{})
	requestDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	go func() {
		defer close(requestDone)
		resp, err := http.Get(server.URL)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("request did not start")
	}
	require.NoError(t, qs.DismissErrorEntry(ctx, errorID))
	close(release)
	<-requestDone
	err = NewExecutor(f.db, f.ledger, f.auth).retainActionFailure(context.WithValue(ctx, guardedRetryGroupKey{}, receipt), run.ID, f.videoID, errors.New("playlist insertion failed"))
	require.NoError(t, err, "the executor must leave source disposition to the receipt owner")
	var count int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=?`, f.videoID).Scan(&count))
	require.Equal(t, 1, count, "dismissed retry must not create a replacement sink")
	got, err := qs.GetErrorEntry(ctx, errorID)
	require.NoError(t, err)
	require.Equal(t, queue.ErrorStatusDismissed, got.Status)
}

func TestFreshActionFailureCreatesErrorSink(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	e := NewExecutor(f.db, f.ledger, f.auth)
	require.NoError(t, e.retainActionFailure(ctx, f.runID, f.videoID, errors.New("playlist insertion failed")))
	var count int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND error_category=? AND status='active'`, f.videoID, queue.ErrorCategoryRuntime).Scan(&count))
	require.Equal(t, 1, count)
}

func TestErrorOnlyRetryAuthorizationPendingHandoff(t *testing.T) {
	for _, tt := range []struct {
		name       string
		dismiss    bool
		wrongOwner bool
		wantErr    error
		wantSink   int
		wantSource queue.ErrorStatus
	}{
		{name: "dismissed after claim", dismiss: true, wantErr: queue.ErrWorkRemoved, wantSource: queue.ErrorStatusDismissed},
		{name: "still owned", wantSink: 1, wantSource: queue.ErrorStatusResolved},
		{name: "wrong-owner receipt", wrongOwner: true, wantErr: queue.ErrWorkNotOwned, wantSource: queue.ErrorStatusRetrying},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			qs, err := queue.NewStore(f.db)
			require.NoError(t, err)
			sourceID, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{
				VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime,
				Message: "retry source", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC(),
			})
			require.NoError(t, err)

			// Claim the exact error group and establish the receiving run's send
			// guard in the same committed SQLite transaction.
			versioner := config.NewService(router.NewStore(f.db), config.NewStore(f.db))
			run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
			run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
			require.NoError(t, err)
			var receipt queue.RetryGroupReceipt
			require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
				run.ID, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
				if err != nil {
					return err
				}
				var claimed bool
				receipt, _, claimed, err = qs.ClaimErrorGroupReceiptTx(ctx, tx, sourceID, run.ID, time.Now())
				if err != nil {
					return err
				}
				if !claimed {
					return errRetryClaimLost
				}
				_, err = tx.ExecContext(ctx, `UPDATE run SET current_phase=? WHERE id=?`, string(PhaseActionExecution), run.ID)
				return err
			}))
			require.Len(t, receipt.Errors, 1)
			if tt.wrongOwner {
				receipt.Errors[0].OriginRunID++
			}
			if tt.dismiss {
				require.NoError(t, qs.DismissErrorEntry(ctx, sourceID))
			}

			// Real auth.Service refuses after Disconnect, before the local Data API
			// transport can be reached. The provider remains a local observable.
			require.NoError(t, f.auth.Disconnect(ctx))
			providerPosts := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				providerPosts++
				w.WriteHeader(http.StatusOK)
			}))
			defer provider.Close()
			e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))
			p := &Pipeline{db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, auth: f.auth, versioner: versioner,
				configStore: config.NewStore(f.db), router: router.NewEngine(router.NewStore(f.db)), detector: duplicate.NewDetector(f.db),
				meta: metadata.NewManager(metadata.NewStore(f.db), nil, nil), logger: discardLogger(), executor: e}
			p.executionBinding = &f.binding
			retryCtx := context.WithValue(ctx, guardedRetryGroupKey{}, receipt)
			createRouteDecision(t, ctx, f.state, f.videoID, run.ID, f.routeID, 1, state.RouteDecisionMatched)
			video, err := f.state.GetVideo(ctx, f.videoID)
			require.NoError(t, err)
			_, err = p.machine.TransitionVideoState(ctx, video.ID, state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
			require.NoError(t, err)
			_, err = p.machine.TransitionVideoState(ctx, video.ID, state.VideoStateEvaluating, state.VideoStateEvaluated)
			require.NoError(t, err)
			video, err = f.state.GetVideo(ctx, f.videoID)
			require.NoError(t, err)
			outcome, err := p.executeVideoActions(retryCtx, run.ID, video)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, state.FinalOutcomeDeferred, outcome)
			}
			require.Zero(t, providerPosts)
			source, err := qs.GetErrorEntry(ctx, sourceID)
			require.NoError(t, err)
			require.Equal(t, tt.wantSource, source.Status)
			var sinks, guards int
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending' AND deferral_reason='authorization_pending'`, f.videoID).Scan(&sinks))
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&guards))
			require.Equal(t, tt.wantSink, sinks)
			wantGuards := 0
			if tt.dismiss || tt.wrongOwner {
				wantGuards = 1
			}
			require.Equal(t, wantGuards, guards, "guard removal must be atomic with a retained pending sink")
			if !tt.dismiss && !tt.wrongOwner {
				actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, run.ID)
				require.NoError(t, err)
				require.Len(t, actions, 1)
				require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
				require.Equal(t, "authorization_pending", *actions[0].OutcomeReason)
				require.Nil(t, actions[0].ExecutedAt)
				// The real disconnected-auth deferral is retained for a later run,
				// while its retry owner prevents this receiving run from selecting it.
				p.completeRun(ctx, run.ID)
				candidates, err := qs.ListDeferredRetryCandidates(ctx, time.Now().Add(24*time.Hour))
				require.NoError(t, err)
				require.Len(t, candidates, 1)
				laterRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
				laterRun.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
				require.NoError(t, err)
				require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
					var claimErr error
					laterRun.ID, claimErr = f.state.CreateGuardedMutationRunTx(ctx, tx, laterRun, true)
					if claimErr != nil {
						return claimErr
					}
					_, _, claimed, claimErr := qs.ClaimDeferredGroupReceiptTx(ctx, tx, candidates[0].ID, laterRun.ID, time.Now().Add(24*time.Hour))
					if claimErr != nil {
						return claimErr
					}
					if !claimed {
						return errRetryClaimLost
					}
					return nil
				}))
			}
		})
	}
}

// accountingFailureExecutor injects the future Finalize failure only after the
// real executor has committed its guarded deferral transaction.
type accountingFailureExecutor struct{ *Executor }

func (e accountingFailureExecutor) ExecuteBound(ctx context.Context, runID, videoID int64, matches []RouteMatch, binding auth.AuthorizationBinding) (state.VideoFinalOutcome, error) {
	outcome, err := e.Executor.ExecuteBound(ctx, runID, videoID, matches, binding)
	if err == nil && outcome == state.FinalOutcomeDeferred {
		return outcome, &postCommitAccountingError{cause: errors.New("private accounting failure")}
	}
	return outcome, err
}

func TestPostCommitAccountingFailurePreservesRetryDeferral(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	rs, cs := router.NewStore(f.db), config.NewStore(f.db)
	versioner := config.NewService(rs, cs)
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	itemID, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: f.videoID, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
	run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	var receipt queue.RetryGroupReceipt
	require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		run.ID, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
		if err != nil {
			return err
		}
		var claimed bool
		receipt, _, claimed, err = qs.ClaimDeferredGroupReceiptTx(ctx, tx, itemID, run.ID, time.Now())
		if err != nil {
			return err
		}
		if !claimed {
			return errRetryClaimLost
		}
		return nil
	}))
	posts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403}}`)
	}))
	defer provider.Close()
	p := &Pipeline{db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, auth: f.auth, versioner: versioner, configStore: cs, router: router.NewEngine(rs), detector: duplicate.NewDetector(f.db), meta: metadata.NewManager(metadata.NewStore(f.db), nil, nil), logger: discardLogger(), broadcast: NewBroadcaster(), executor: accountingFailureExecutor{NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))}, executionBinding: &f.binding}
	p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
	err = p.processRetryVideo(context.WithValue(ctx, guardedRetryGroupKey{}, receipt), run.ID, f.videoID)
	require.Error(t, err)
	p.failRun(ctx, run.ID, "retry processing failed", err)
	loaded, err := f.state.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusFailed, loaded.Status)
	require.NotNil(t, loaded.InterruptReason)
	require.Contains(t, *loaded.InterruptReason, "operation=account_quota")
	require.NotContains(t, *loaded.InterruptReason, "private")
	v, err := f.state.GetVideo(ctx, f.videoID)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, v.FinalOutcome)
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, run.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
	var pending, guards, errorsCount int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending'`, f.videoID).Scan(&pending))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&guards))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=?`, f.videoID).Scan(&errorsCount))
	require.Equal(t, 1, pending)
	require.Zero(t, guards)
	require.Zero(t, errorsCount)
	require.Equal(t, 1, posts)
}

func TestPostCommitAccountingFailurePreservesProductionDeferral(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	machine := state.NewMachine(f.state)
	_, err := machine.TransitionVideoState(ctx, f.videoID, state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	_, err = machine.TransitionVideoState(ctx, f.videoID, state.VideoStateEvaluating, state.VideoStateEvaluated)
	require.NoError(t, err)
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	posts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403}}`)
	}))
	defer provider.Close()
	p := &Pipeline{db: f.db, stateStore: f.state, machine: machine, queueStore: qs, configStore: config.NewStore(f.db), logger: discardLogger(), broadcast: NewBroadcaster(), executor: accountingFailureExecutor{NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))}, executionBinding: &f.binding}
	v, err := f.state.GetVideo(ctx, f.videoID)
	require.NoError(t, err)
	outcome, err := p.executeVideoActions(ctx, f.runID, v)
	require.Error(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, outcome)
	p.failRun(ctx, f.runID, "action execution phase failed", err)
	run, err := f.state.GetRun(ctx, f.runID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusFailed, run.Status)
	require.NotNil(t, run.InterruptReason)
	require.Contains(t, *run.InterruptReason, "operation=account_quota")
	require.NotContains(t, *run.InterruptReason, "private")
	v, err = f.state.GetVideo(ctx, f.videoID)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, v.FinalOutcome)
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeDeferred, actions[0].Outcome)
	var pending, guards, errorsCount int
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending'`, f.videoID).Scan(&pending))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&guards))
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=?`, f.videoID).Scan(&errorsCount))
	require.Equal(t, 1, pending)
	require.Zero(t, guards)
	require.Zero(t, errorsCount)
	require.Equal(t, 1, posts)
}
