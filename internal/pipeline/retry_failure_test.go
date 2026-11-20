package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
)

func TestProcessRetryVideoProviderFailureFinishesExactReceipt(t *testing.T) {
	for _, tt := range []struct {
		name          string
		dismissOne    bool
		injectFailure bool
	}{
		{name: "two live siblings"},
		{name: "dismissed sibling", dismissOne: true},
		{name: "transaction rollback", injectFailure: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			qs, err := queue.NewStore(f.db)
			require.NoError(t, err)
			first, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime, Message: "first", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			second, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryAPIError, Message: "second", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
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
				receipt, _, claimed, err = qs.ClaimErrorGroupReceiptTx(ctx, tx, first, run.ID, time.Now())
				if err != nil {
					return err
				}
				if !claimed {
					return errRetryClaimLost
				}
				_, err = tx.ExecContext(ctx, `UPDATE run SET current_phase=? WHERE id=?`, string(PhaseActionExecution), run.ID)
				return err
			}))
			require.Len(t, receipt.Errors, 2)
			if tt.dismissOne {
				require.NoError(t, qs.DismissErrorEntry(ctx, second))
			}
			if tt.injectFailure {
				_, err = f.db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER fail_retry_source_resolution BEFORE UPDATE OF status ON error_entry WHEN OLD.id=%d AND NEW.status='resolved' BEGIN SELECT RAISE(ABORT, 'injected source failure'); END`, first))
				require.NoError(t, err)
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				f.posts++
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}]}}`)
			}))
			defer provider.Close()
			p := &Pipeline{db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, auth: f.auth, versioner: versioner,
				configStore: config.NewStore(f.db), router: router.NewEngine(router.NewStore(f.db)), detector: duplicate.NewDetector(f.db),
				meta: metadata.NewManager(metadata.NewStore(f.db), nil, nil), logger: discardLogger(),
				executor: NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))}
			p.executionBinding = &f.binding
			p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
			retryCtx := context.WithValue(ctx, guardedRetryGroupKey{}, receipt)
			err = p.processRetryVideo(retryCtx, run.ID, f.videoID)
			require.Equal(t, 1, f.posts)
			if tt.injectFailure {
				require.Error(t, err)
				for _, id := range []int64{first, second} {
					entry, getErr := qs.GetErrorEntry(ctx, id)
					require.NoError(t, getErr)
					require.Equal(t, queue.ErrorStatusRetrying, entry.Status)
					require.Equal(t, run.ID, *entry.RetryRunID)
				}
				var sinks int
				require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE run_id=? AND error_category='runtime' AND status='active'`, run.ID).Scan(&sinks))
				require.Zero(t, sinks, "the sink insert must roll back with source-resolution failure")
				var outcomes int
				require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome='failed'`, run.ID, f.videoID).Scan(&outcomes))
				require.Zero(t, outcomes, "run outcome must roll back with the failed handoff")
			} else {
				require.NoError(t, err)
				wantResolved := 2
				if tt.dismissOne {
					wantResolved = 1
				}
				var resolved int
				require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND status='resolved' AND retry_run_id=?`, f.videoID, run.ID).Scan(&resolved))
				require.Equal(t, wantResolved, resolved)
				var sinks int
				require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE run_id=? AND error_category='runtime' AND status='active'`, run.ID).Scan(&sinks))
				require.Equal(t, 1, sinks)
				video, getErr := f.state.GetVideo(ctx, f.videoID)
				require.NoError(t, getErr)
				require.Equal(t, state.FinalOutcomeFailed, video.FinalOutcome)
				var outcome state.VideoFinalOutcome
				require.NoError(t, f.db.QueryRowContext(ctx, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&outcome))
				require.Equal(t, state.FinalOutcomeFailed, outcome)
			}
			actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, run.ID)
			require.NoError(t, err)
			require.Len(t, actions, 1)
			if !tt.injectFailure {
				// Reprocessing the same exact receipt after its failure handoff
				// committed must return that disposition, not treat it as removal.
				require.NoError(t, p.processRetryVideo(retryCtx, run.ID, f.videoID))
				require.Equal(t, 1, f.posts, "committed failure recovery must not replay the provider POST")
				var sinks int
				require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE run_id=? AND error_category='runtime' AND status='active'`, run.ID).Scan(&sinks))
				require.Equal(t, 1, sinks, "reconciliation must preserve the existing sink")
			}
			require.Equal(t, state.ActionOutcomeFailed, actions[0].Outcome)
			require.NotNil(t, actions[0].ExecutedAt, "provider 500 follows one real admitted POST")
			var guards int
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&guards))
			require.Equal(t, 1, guards, "ordinary failure handoff must retain the independent send guard")
			if tt.dismissOne {
				dismissed, getErr := qs.GetErrorEntry(ctx, second)
				require.NoError(t, getErr)
				require.Equal(t, queue.ErrorStatusDismissed, dismissed.Status)
			}
		})
	}
}

func TestProcessRetryVideoCommittedFailureSurvivesSQLiteReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "recovery.db")
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	f := newGuardedProductionFixtureOnDB(t, db, []string{"PLguarded-fixture"}, http.StatusOK)
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	first, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryAPIError, Message: "provider failed", Detail: "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	versioner := config.NewService(router.NewStore(db), config.NewStore(db))
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
		receipt, _, claimed, err = qs.ClaimErrorGroupReceiptTx(ctx, tx, first, run.ID, time.Now())
		if err != nil {
			return err
		}
		if !claimed {
			return errRetryClaimLost
		}
		_, err = tx.ExecContext(ctx, `UPDATE run SET current_phase=? WHERE id=?`, string(PhaseActionExecution), run.ID)
		return err
	}))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.posts++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}]}}`)
	}))
	defer provider.Close()
	p := &Pipeline{db: db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, auth: f.auth, versioner: versioner,
		configStore: config.NewStore(db), router: router.NewEngine(router.NewStore(db)), detector: duplicate.NewDetector(db),
		meta: metadata.NewManager(metadata.NewStore(db), nil, nil), logger: discardLogger(), executor: NewExecutor(db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))}
	p.executionBinding = &f.binding
	p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
	retryCtx := context.WithValue(ctx, guardedRetryGroupKey{}, receipt)
	require.NoError(t, p.processRetryVideo(retryCtx, run.ID, f.videoID))
	require.Equal(t, 1, f.posts)
	// Discard every in-memory receipt and close the only database handle.
	require.NoError(t, db.Close())
	db, err = store.Open(ctx, dbPath)
	require.NoError(t, err)
	defer db.Close()
	qs, err = queue.NewStore(db)
	require.NoError(t, err)
	var persisted queue.RetryGroupReceipt
	persisted.ReceivingRunID, persisted.VideoID = run.ID, f.videoID
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, `SELECT id,run_id FROM error_entry WHERE video_id=? AND retry_run_id=? AND status='resolved'`, f.videoID, run.ID)
	require.NoError(t, err)
	for rows.Next() {
		var ref queue.RetryRowRef
		require.NoError(t, rows.Scan(&ref.ID, &ref.OriginRunID))
		persisted.Errors = append(persisted.Errors, ref)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, receipt, persisted, "receipt must be reconstructible from durable owner evidence")
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	versioner = config.NewService(router.NewStore(db), config.NewStore(db))
	p = &Pipeline{db: db, stateStore: stateStore, machine: state.NewMachine(stateStore), queueStore: qs, auth: f.auth, versioner: versioner,
		configStore: config.NewStore(db), router: router.NewEngine(router.NewStore(db)), detector: duplicate.NewDetector(db),
		meta: metadata.NewManager(metadata.NewStore(db), nil, nil), logger: discardLogger(), executor: NewExecutor(db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))}
	p.executionBinding = &f.binding
	p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
	ctxWithReceipt := context.WithValue(ctx, guardedRetryGroupKey{}, persisted)
	require.NoError(t, p.processRetryVideo(ctxWithReceipt, run.ID, f.videoID))
	require.Equal(t, 1, f.posts, "recovery must not issue another provider POST")
	var sinks, guards int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE run_id=? AND status='active' AND error_category='runtime'`, run.ID).Scan(&sinks))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, run.ID, f.videoID).Scan(&guards))
	require.Equal(t, 1, sinks)
	require.Equal(t, 1, guards)
	actions, err := stateStore.GetExecutedActionsForVideoInRun(ctx, f.videoID, run.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, state.ActionOutcomeFailed, actions[0].Outcome)
	var status, detail string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,error_detail FROM error_entry WHERE id=?`, first).Scan(&status, &detail))
	require.Equal(t, string(queue.ErrorStatusResolved), status)
	require.Contains(t, detail, "status=500")
	require.Contains(t, detail, "reason=backendError")
}
