//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
)

// Rebuild every retry-path dependency after closing the actual SQLite handle.
// Only the external provider factory survives the simulated process restart.
func reopenRetryPipeline(t *testing.T, p *Pipeline) *Pipeline {
	t.Helper()
	ctx := context.Background()
	var seq int
	var name, path string
	require.NoError(t, p.db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &path))
	baseURL := p.executor.(*Executor).baseURL
	require.NoError(t, p.db.Close())
	return openRetryPipeline(t, path, baseURL, p.logger)
}

// openRetryPipeline builds every dependency fresh over the SQLite file at path.
func openRetryPipeline(t *testing.T, path, baseURL string, logger *slog.Logger) *Pipeline {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ss, err := state.NewStore(db)
	require.NoError(t, err)
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	ledger := quota.New(db)
	machine := state.NewMachine(ss)
	rs := router.NewStore(db)
	cs := config.NewStore(db)
	key := []byte("01234567890123456789012345678901")
	tokens, err := auth.NewSQLiteTokenStore(db, key)
	require.NoError(t, err)
	service, err := auth.NewService(&auth.ServiceConfig{ClientID: "test", ClientSecret: "test", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)}, tokens, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	return NewPipeline(db, machine, ss, nil, duplicate.NewDetector(db), router.NewEngine(rs), NewExecutor(db, ledger, service, WithYouTubeBaseURL(baseURL)), ledger, metadata.NewManager(metadata.NewStore(db), &fakeMetadataFetcher{}, ledger), service, nil, WithQueueStore(qs), WithConfigStore(cs), WithVersioner(config.NewService(rs, cs)), WithLogger(logger))
}

func TestRetryQueueEntryGuardedProduction(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	qs, err := queue.NewStore(p.db)
	require.NoError(t, err)
	p.queueStore = qs
	require.NoError(t, p.ledger.SetDailyLimit(ctx, 4))
	origin, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	p.Execute(ctx, origin.ID)
	items, err := qs.ListDeferredItems(ctx, queue.DeferredStatusPending)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NoError(t, p.ledger.SetDailyLimit(ctx, 1000000))
	_, err = p.db.ExecContext(ctx, `UPDATE deferred_item SET retry_after=NULL WHERE id=?`, items[0].ID)
	require.NoError(t, err)
	require.NoError(t, p.RetryQueueEntry(ctx, items[0].ID, false))
	var runID int64
	var version int
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT max(id) FROM run`).Scan(&runID))
	require.NotEqual(t, origin.ID, runID)
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT mutation_safety_version FROM run WHERE id=?`, runID).Scan(&version))
	require.Equal(t, 1, version)
	run, err := p.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, run.Status)
	require.Len(t, env.server.Insertions(), 1)
	var calls int
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT count(*) FROM quota_ledger WHERE run_id=? AND operation_type=?`, runID, quota.OpPlaylistItemsInsert).Scan(&calls))
	require.Equal(t, 1, calls)
}

// The carrier is inspected before execution because the send guard does not
// yet consume retry receipts. This test does not prove dispatch fencing.
func TestFreshRetryReceiptTransfer(t *testing.T) {
	for _, mode := range []string{"scheduled", "run-now"} {
		for _, change := range []string{"intact", "released", "discarded", "all-released"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
				p := env.pipeline
				qs, err := queue.NewStore(p.db)
				require.NoError(t, err)
				p.queueStore = qs
				require.NoError(t, p.ledger.SetDailyLimit(ctx, 4))
				origin, err := p.StartRun(ctx, state.RunTypeManual)
				require.NoError(t, err)
				p.Execute(ctx, origin.ID)
				items, err := qs.ListDeferredItems(ctx, queue.DeferredStatusPending)
				require.NoError(t, err)
				require.Len(t, items, 1)
				_, err = p.db.ExecContext(ctx, `UPDATE deferred_item SET retry_after=NULL WHERE id=?`, items[0].ID)
				require.NoError(t, err)
				_, err = p.db.ExecContext(ctx, `INSERT INTO deferred_item(video_id,run_id,deferral_reason,status,created_at) VALUES(?,?,'quota_exhausted','pending',?)`, items[0].VideoID, origin.ID, time.Now().UTC())
				require.NoError(t, err)
				require.NoError(t, p.ledger.SetDailyLimit(ctx, 1000000))
				var runID int64
				var admission *RunNowAdmission
				if mode == "scheduled" {
					run, err := p.StartRun(ctx, state.RunTypeScheduled)
					require.NoError(t, err)
					runID = run.ID
				} else {
					admission, err = p.AdmitRunNow(ctx)
					require.NoError(t, err)
					require.False(t, admission.Resumed())
					runID = admission.RunID()
					defer admission.Release()
				}
				ctl := p.control(runID)
				require.NotNil(t, ctl)
				ctl.mu.Lock()
				require.Len(t, ctl.freshRetries, 1)
				receipt := ctl.freshRetries[0]
				ctl.mu.Unlock()
				require.Equal(t, runID, receipt.ReceivingRunID)
				require.Len(t, receipt.Deferred, 2)
				if change != "intact" {
					status := "pending"
					if change == "discarded" {
						status = "discarded"
					}
					require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
						_, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status=?,retry_run_id=NULL WHERE id=?`, status, receipt.Deferred[0].ID)
						if err == nil && change == "all-released" {
							_, err = tx.ExecContext(ctx, `UPDATE deferred_item SET status='pending',retry_run_id=NULL WHERE id=?`, receipt.Deferred[1].ID)
						}
						return err
					}))
				}
				err = qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
					_, err := qs.ValidateRetryReceiptTx(ctx, tx, receipt)
					return err
				})
				if change == "released" || change == "all-released" {
					require.ErrorIs(t, err, queue.ErrWorkNotOwned)
				} else {
					require.NoError(t, err)
				}
				// The released case must not execute until the guard implements
				// receipt validation; this test proves only the carrier identity.
				if change == "released" {
					return
				}
				if change == "all-released" {
					var before int
					require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM quota_ledger`).Scan(&before))
					err := p.processDeferredItems(ctx, runID, []queue.RetryGroupReceipt{receipt}, false)
					var refusal *retryAdmissionRefusal
					require.ErrorAs(t, err, &refusal)
					require.ErrorIs(t, err, queue.ErrWorkNotOwned)
					require.Equal(t, receipt, refusal.receipt)
					require.Empty(t, env.server.Insertions())
					var after, pending int
					require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM quota_ledger`).Scan(&after))
					require.Equal(t, before, after)
					require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending' AND retry_run_id IS NULL AND id IN (?,?)`, receipt.VideoID, receipt.Deferred[0].ID, receipt.Deferred[1].ID).Scan(&pending))
					require.Equal(t, 2, pending)
					require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, receipt.VideoID).Scan(&pending))
					require.Equal(t, 2, pending)
				}
				if admission != nil {
					admission.Execute(ctx)
				} else {
					p.Execute(ctx, runID)
				}
				run, err := p.stateStore.GetRun(ctx, runID)
				require.NoError(t, err)
				if change == "all-released" {
					require.NotEqual(t, state.RunStatusCompleted, run.Status)
					require.Empty(t, env.server.Insertions())
					var count int
					require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, receipt.VideoID).Scan(&count))
					require.Equal(t, 2, count)
					require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE id IN (?,?) AND status='pending' AND retry_run_id IS NULL`, receipt.Deferred[0].ID, receipt.Deferred[1].ID).Scan(&count))
					require.Equal(t, 2, count)
				} else {
					require.Equal(t, state.RunStatusCompleted, run.Status)
					require.Len(t, env.server.Insertions(), 1)
				}
			})
		}
	}
}

func TestRetryRecovery_ReopenBeforeDecisionAndBeforeAcknowledgement(t *testing.T) {
	for _, point := range []string{"claim", "terminal", "error claim"} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
			p := env.pipeline
			qs, err := queue.NewStore(p.db)
			require.NoError(t, err)
			p.queueStore = qs
			require.NoError(t, p.ledger.SetDailyLimit(ctx, 4))
			origin, err := p.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			p.Execute(ctx, origin.ID)
			items, err := qs.ListDeferredItems(ctx, queue.DeferredStatusPending)
			require.NoError(t, err)
			require.Len(t, items, 1)
			_, err = p.db.ExecContext(ctx, `INSERT INTO deferred_item(video_id,run_id,deferral_reason,status,created_at) VALUES(?,?,'quota_exhausted','pending',?)`, items[0].VideoID, origin.ID, time.Now().UTC())
			require.NoError(t, err)
			require.NoError(t, p.ledger.SetDailyLimit(ctx, 1000000))
			var receiving *state.Run
			if point == "error claim" {
				errorID, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: items[0].VideoID, RunID: origin.ID, Category: queue.ErrorCategoryRuntime, Message: "retained failure", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
				require.NoError(t, err)
				receiving, err = p.prepareProductionRun(ctx, state.RunTypeManual)
				require.NoError(t, err)
				require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
					id, err := p.stateStore.CreateGuardedMutationRunTx(ctx, tx, receiving, true)
					if err != nil {
						return err
					}
					receiving.ID = id
					_, claimed, err := qs.ClaimErrorGroupTx(ctx, tx, errorID, id, time.Now())
					require.True(t, claimed)
					return err
				}))
			} else {
				receiving, err = p.StartRun(ctx, state.RunTypeManual)
				require.NoError(t, err)
			}
			if point == "terminal" {
				v, err := p.stateStore.GetVideo(ctx, items[0].VideoID)
				require.NoError(t, err)
				_, err = p.phaseRouting(ctx, receiving.ID, []*state.Video{v})
				require.NoError(t, err)
				v, err = p.stateStore.GetVideo(ctx, v.ID)
				require.NoError(t, err)
				out, err := p.executeVideoActions(ctx, receiving.ID, v)
				require.NoError(t, err)
				require.Equal(t, state.FinalOutcomeAccepted, out)
			}
			// A late-phase checkpoint cannot hide the owned pre-decision video.
			require.NoError(t, p.stateStore.SetRunPhase(ctx, receiving.ID, string(PhaseActionExecution)))
			require.NoError(t, p.stateStore.InterruptRun(ctx, receiving.ID, "process stopped"))
			p = reopenRetryPipeline(t, p)
			_, err = p.db.ExecContext(ctx, `UPDATE route_action SET target_playlist_id='PLchanged'; UPDATE route_condition SET value='classical'`)
			require.NoError(t, err)
			p.Execute(ctx, receiving.ID)
			loaded, err := p.stateStore.GetRun(ctx, receiving.ID)
			require.NoError(t, err)
			require.Equal(t, state.RunStatusCompleted, loaded.Status)
			require.Len(t, env.server.Insertions(), 1)
			require.Equal(t, "PLjazz", env.server.Insertions()[0].PlaylistID)
			all, err := p.queueStore.ListDeferredItems(ctx, "")
			require.NoError(t, err)
			require.Len(t, all, 2)
			for _, d := range all {
				require.Equal(t, queue.DeferredStatusCompleted, d.Status)
				require.Equal(t, 1, d.RetryCount)
			}
			var routed int
			require.NoError(t, p.db.QueryRowContext(ctx, `SELECT total_routed FROM channel WHERE channel_id='UC1'`).Scan(&routed))
			require.Equal(t, 1, routed)
		})
	}
}

func TestRetryRecovery_CompletedTargetSurvivesFreshEvaluation(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	qs, err := queue.NewStore(p.db)
	require.NoError(t, err)
	p.queueStore = qs
	_, err = router.NewStore(p.db).CreateRoute(ctx, &router.Route{Name: "Second", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND", Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "jazz"}}, Actions: []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLsecond"}}})
	require.NoError(t, err)
	origin, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	vs, err := p.phaseDiscovery(ctx, origin.ID)
	require.NoError(t, err)
	_, err = p.phaseRouting(ctx, origin.ID, vs)
	require.NoError(t, err)
	ds, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, origin.ID)
	require.NoError(t, err)
	require.Len(t, ds, 2)
	// Persist one real provider success, as if interrupted between route actions.
	out, err := p.executor.Execute(ctx, origin.ID, vs[0].ID, []RouteMatch{{ds[0].ID, ds[0].RouteID}})
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeAccepted, out)
	require.NoError(t, p.ledger.SetDailyLimit(ctx, 54))
	v, err := p.stateStore.GetVideo(ctx, vs[0].ID)
	require.NoError(t, err)
	out, err = p.executeVideoActions(ctx, origin.ID, v)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, out)
	p.completeRun(ctx, origin.ID)
	require.NoError(t, p.ledger.SetDailyLimit(ctx, 1000000))
	retry, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	p.Execute(ctx, retry.ID)
	// BR-039 AC7: V is partly done, so the receiving run routes nothing. It sends
	// only the missing addition, under the original decision, and never repeats
	// the completed one.
	ins := env.server.Insertions()
	require.Len(t, ins, 2, "one send from the origin run and one continued send")
	require.Equal(t, "PLjazz", ins[0].PlaylistID, "the completed target was sent once, by the origin run")
	require.Equal(t, "PLsecond", ins[1].PlaylistID, "only the missing target is sent")
	newDecisions, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, retry.ID)
	require.NoError(t, err)
	require.Empty(t, newDecisions, "a partly done video is not routed again")
	done, err := p.stateStore.GetVideo(ctx, v.ID)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeAccepted, done.FinalOutcome, "finishing every missing addition makes the video accepted")
}
