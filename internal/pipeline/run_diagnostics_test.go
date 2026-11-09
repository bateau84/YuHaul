package pipeline

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

type deadlineMetadata struct{ entered chan struct{} }

func (m deadlineMetadata) GetDisplayMetadata(ctx context.Context, videoID string) (*metadata.TransientMetadata, error) {
	close(m.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (deadlineMetadata) RunLifecycle(context.Context) error { return nil }

// abortRouteDecisions installs a SQLite trigger that makes every route-decision
// insert fail, standing in for an unrecoverable per-video evaluation error
// after the selector has already established its input set.
func abortRouteDecisions(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`CREATE TRIGGER injected_decision_failure BEFORE INSERT ON route_decision
		 BEGIN SELECT RAISE(ABORT, 'injected decision failure'); END;`)
	require.NoError(t, err)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- dry-run selector and terminal status ---

func TestDryRun_EmptyPendingSelectionStoresZero(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	createRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	require.NotNil(t, loaded.VideosSelected,
		"a dry-run whose pending selector completed empty must store zero, not unknown")
	assert.Equal(t, 0, *loaded.VideosSelected)
}

func TestDryRun_RouteLoadFailureBeforeSelectionStaysUnknown(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)

	// Remove every route source so route loading fails before the pending
	// selector runs: the count must stay NULL (unknown), not zero.
	deps.pipeline.router = nil
	deps.pipeline.configStore = nil
	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status,
		"an unrecoverable dry-run phase failure must end failed, not completed")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=unknown_error", *loaded.InterruptReason)
	assert.Nil(t, loaded.VideosSelected,
		"a failure before the selector ran must keep the count unknown")
}

func TestDryRun_VideoFailureAfterSelectionKeepsKnownCount(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	createRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	createPendingVideo(t, ctx, deps, mustRunID(t, ctx, deps), "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)

	abortRouteDecisions(t, ctx, deps.db)
	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status,
		"a dry-run evaluation failure must end failed, not completed")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_CONSTRAINT", *loaded.InterruptReason,
		"the dry-run must retain the source-known operation and typed SQLite failure")
	require.NotNil(t, loaded.VideosSelected,
		"a selection known before a mid-run failure must survive it")
	assert.Equal(t, 1, *loaded.VideosSelected)
	assert.Equal(t, 0, loaded.VideosEvaluated,
		"evaluated counts persisted decision evidence, not the selected input")
}

func TestDryRun_PanicAfterSelectionEndsFailedNotCompleted(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	createRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	createPendingVideo(t, ctx, deps, mustRunID(t, ctx, deps), "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)

	// A metadata manager without a store panics on the first per-video
	// lookup, after the pending selector has captured its input set.
	deps.pipeline.meta = metadata.NewManager(nil, nil, nil)
	deps.pipeline.ExecuteDryRun(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status,
		"a dry-run panic must end terminal failed, never completed")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=panic", *loaded.InterruptReason)
	require.NotNil(t, loaded.VideosSelected, "a panic after selection keeps the known count")
	assert.Equal(t, 1, *loaded.VideosSelected)
}

// mustRunID creates the production run that owns the pending fixture video.
func mustRunID(t *testing.T, ctx context.Context, deps *dryRunDeps) int64 {
	t.Helper()
	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	id, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)
	return id
}

// --- replay/backtest selector and terminal status ---

func TestReplay_EmptyCorpusSelectionStoresZero(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	require.NotNil(t, loaded.VideosSelected,
		"a replay whose retained selector completed empty must store zero, not unknown")
	assert.Equal(t, 0, *loaded.VideosSelected)
}

func TestReplay_RouteLoadFailureBeforeSelectionStaysUnknown(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)

	deps.pipeline.router = nil
	deps.pipeline.configStore = nil
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status,
		"an unrecoverable replay phase failure must end failed, not completed")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=unknown_error", *loaded.InterruptReason)
	assert.Nil(t, loaded.VideosSelected)
}

func TestBacktest_VideoFailureAfterSelectionKeepsKnownCount(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)
	createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.Backtest(ctx, &config.ProposalSnapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes:             []config.ProposalRoute{proposalRoute(proposalTestUUID, "jazz", "jazz")},
	})
	require.NoError(t, err)

	abortRouteDecisions(t, ctx, deps.db)
	deps.pipeline.ExecuteBacktest(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status,
		"a backtest evaluation failure must end failed, not completed")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_CONSTRAINT", *loaded.InterruptReason,
		"the backtest must preserve the source-known routing operation and typed SQLite code")
	require.NotNil(t, loaded.VideosSelected)
	assert.Equal(t, 1, *loaded.VideosSelected,
		"a backtest selection known before a mid-run failure must survive it")
	assert.Equal(t, 0, loaded.VideosEvaluated)
}

func TestReplay_PanicAfterSelectionEndsFailedNotCompleted(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)
	createRetainedVideo(t, ctx, deps, prodRunID, "vid1", "ch1", "cool jazz live")

	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)

	deps.pipeline.meta = metadata.NewManager(nil, nil, nil)
	deps.pipeline.ExecuteReplay(ctx, run.ID)

	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status,
		"a replay panic must end terminal failed, never completed")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=panic", *loaded.InterruptReason)
	require.NotNil(t, loaded.VideosSelected, "a panic after selection keeps the known count")
	assert.Equal(t, 1, *loaded.VideosSelected)
}

func TestSimulationCanceledAtInitialLoadFinalizesFailed(t *testing.T) {
	for _, mode := range []string{"dry-run", "replay", "backtest"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var p *Pipeline
			var run *state.Run
			var err error
			if mode == "dry-run" {
				deps, cleanup := setupDryRunPipeline(t)
				t.Cleanup(cleanup)
				p = deps.pipeline
				run, err = p.DryRun(ctx, nil)
			} else {
				deps, cleanup := setupReplayPipeline(t)
				t.Cleanup(cleanup)
				p = deps.pipeline
				if mode == "replay" {
					run, err = p.Replay(ctx, nil)
				} else {
					run, err = p.Backtest(ctx, &config.ProposalSnapshot{})
				}
			}
			require.NoError(t, err)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			switch mode {
			case "dry-run":
				p.ExecuteDryRun(canceled, run.ID)
			case "replay":
				p.ExecuteReplay(canceled, run.ID)
			case "backtest":
				p.ExecuteBacktest(canceled, run.ID)
			}
			loaded, err := p.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			assert.Equal(t, state.RunStatusFailed, loaded.Status)
			require.NotNil(t, loaded.CompletedAt, "terminal failed status must be durable")
			require.NotNil(t, loaded.InterruptReason)
			assert.Contains(t, *loaded.InterruptReason, "code=canceled")
			assert.Zero(t, loaded.VideosAccepted)
			assert.Zero(t, loaded.VideosRejected)
		})
	}
}

func TestSimulationDeadlineAfterSelectionFinalizesFailed(t *testing.T) {
	for _, mode := range []string{"dry-run", "replay", "backtest"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var p *Pipeline
			var run *state.Run
			var db *sql.DB
			var err error
			if mode == "dry-run" {
				deps, cleanup := setupDryRunPipeline(t)
				t.Cleanup(cleanup)
				p, db = deps.pipeline, deps.db
				createRouteWithAction(t, ctx, deps, "Deadline", "match", "PLdeadline")
				createPendingVideo(t, ctx, deps, mustRunID(t, ctx, deps), "deadline-video", "channel", "match title")
				run, err = p.DryRun(ctx, nil)
			} else {
				deps, cleanup := setupReplayPipeline(t)
				t.Cleanup(cleanup)
				p, db = deps.pipeline, deps.db
				createReplayRouteWithAction(t, ctx, deps, "Deadline", "match", "PLdeadline")
				prod := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
				prodID, createErr := deps.stateStore.CreateRun(ctx, prod)
				require.NoError(t, createErr)
				createRetainedVideo(t, ctx, deps, prodID, "deadline-video", "channel", "match title")
				if mode == "replay" {
					run, err = p.Replay(ctx, nil)
				} else {
					run, err = p.Backtest(ctx, &config.ProposalSnapshot{Routes: []config.ProposalRoute{proposalRoute(proposalTestUUID, "match", "match")}})
				}
			}
			require.NoError(t, err)
			entered := make(chan struct{})
			p.meta = deadlineMetadata{entered: entered}
			execCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				switch mode {
				case "dry-run":
					p.ExecuteDryRun(execCtx, run.ID)
				case "replay":
					p.ExecuteReplay(execCtx, run.ID)
				case "backtest":
					p.ExecuteBacktest(execCtx, run.ID)
				}
			}()
			<-entered
			// At the metadata hook the selector has already captured its count.
			stored, err := p.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			require.NotNil(t, stored.VideosSelected)
			assert.Equal(t, 1, *stored.VideosSelected)
			<-done
			require.ErrorIs(t, execCtx.Err(), context.DeadlineExceeded, "the test must observe actual deadline expiry")
			loaded, err := p.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			assert.Equal(t, state.RunStatusFailed, loaded.Status)
			require.NotNil(t, loaded.CompletedAt)
			require.NotNil(t, loaded.InterruptReason)
			assert.Contains(t, *loaded.InterruptReason, "code=timeout")
			require.NotNil(t, loaded.VideosSelected)
			assert.Equal(t, 1, *loaded.VideosSelected)
			assert.Zero(t, loaded.VideosAccepted)
			assert.Zero(t, loaded.VideosRejected)
			var externalActions int
			err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action WHERE run_id = ? AND outcome != 'not_attempted'`, run.ID).Scan(&externalActions)
			require.NoError(t, err)
			assert.Zero(t, externalActions, "simulation must not record attempted provider actions")
		})
	}
}

func TestHistoricalFailedWriteFallsBackToInterrupted(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	prod := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodID, err := deps.stateStore.CreateRun(ctx, prod)
	require.NoError(t, err)
	createRetainedVideo(t, ctx, deps, prodID, "fallback-video", "channel", "fallback title")
	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, deps.stateStore.SetRunSelectedCount(ctx, run.ID, 1))
	_, err = deps.db.ExecContext(ctx, `CREATE TRIGGER reject_failed_status BEFORE UPDATE OF status ON run WHEN NEW.status = 'failed' BEGIN SELECT RAISE(ABORT, 'injected failed-status rejection'); END;`)
	require.NoError(t, err)
	deps.pipeline.failHistoricalRun(ctx, run.ID, "evaluation deadline expired", context.DeadlineExceeded)
	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusInterrupted, loaded.Status,
		"failed-write rejection must fall back to resumable interrupted, never completed")
	assert.Nil(t, loaded.CompletedAt, "interrupted remains resumable, not terminal completion")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=timeout", *loaded.InterruptReason)
	require.NotNil(t, loaded.VideosSelected)
	assert.Equal(t, 1, *loaded.VideosSelected)
	assert.Zero(t, loaded.VideosAccepted)
	assert.Zero(t, loaded.VideosRejected)
}

func TestHistoricalCompletionFailureRetriesThenFailsTerminally(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	run, err := deps.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	_, err = deps.db.ExecContext(ctx, `CREATE TRIGGER reject_completion BEFORE UPDATE OF status ON run WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END;`)
	require.NoError(t, err)
	deps.pipeline.completeHistoricalRun(ctx, run.ID)
	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status)
	require.NotNil(t, loaded.CompletedAt)
	require.NotNil(t, loaded.InterruptReason)
	assert.NotEqual(t, "", *loaded.InterruptReason)
}

// --- production selector and terminal status ---

func TestPipeline_RoutingFailureAfterSelectionKeepsKnownCount(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	routerStore := router.NewStore(db)
	_, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "FailRoute",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Conditions: []router.RouteCondition{{
			ConditionIndex: 0, Field: router.ConditionFieldTitle,
			Operator: router.ConditionOperatorContains, Value: "jazz",
		}},
	})
	require.NoError(t, err)

	// An interrupted run resuming at routing with one discovered input.
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusInterrupted}
	captureNativeTestRun(t, ctx, db, run)
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`UPDATE run SET current_phase = ? WHERE id = ?;`, string(PhaseRouting), runID)
	require.NoError(t, err)
	_, err = machine.DiscoverVideo(ctx, runID, "fail_vid", "fail_ch")
	require.NoError(t, err)

	abortRouteDecisions(t, ctx, db)

	queueStore, err := queue.NewStore(db)
	require.NoError(t, err)
	p := NewPipeline(db, machine, stateStore, nil, nil, router.NewEngine(routerStore), nil, nil,
		metadata.NewManager(metadata.NewStore(db), nil, nil), nil, broadcast,
		WithConfigStore(config.NewStore(db)),
		WithQueueStore(queueStore),
		WithLogger(discardLogger()),
	)

	p.Execute(ctx, runID)

	loaded, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status,
		"an unrecoverable production phase failure must end failed, not completed")
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_CONSTRAINT", *loaded.InterruptReason,
		"the run must fail on the injected routing error, not an incidental panic")
	require.NotNil(t, loaded.VideosSelected,
		"the run-owned input set established before the failing phase must survive it")
	assert.Equal(t, 1, *loaded.VideosSelected)
	assert.Equal(t, 0, loaded.VideosEvaluated)
}

func TestRetryQueueEntry_CapturesClaimedRetrySelection(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	db, stateStore := f.db, f.state
	machine, broadcast := state.NewMachine(stateStore), NewBroadcaster()

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	queueStore, err := queue.NewStore(db)
	require.NoError(t, err)

	p := &Pipeline{
		db:          db,
		machine:     machine,
		stateStore:  stateStore,
		queueStore:  queueStore,
		auth:        f.auth,
		versioner:   config.NewService(routerStore, cfgStore),
		configStore: cfgStore,
		router:      router.NewEngine(routerStore),
		detector:    duplicate.NewDetector(db),
		meta:        metadata.NewManager(metadata.NewStore(db), nil, nil),
		broadcast:   broadcast,
		logger:      discardLogger(),
	}

	// Origin run holds a video that already has a durable rejected
	// disposition plus pending deferred work waiting for manual retry. The
	// run is finalized only after its fixture rows exist: recording run
	// evidence on a terminal run is rejected.
	origin := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	originID, err := stateStore.CreateRun(ctx, origin)
	require.NoError(t, err)
	video, err := machine.DiscoverVideo(ctx, originID, "retry_vid", "retry_ch")
	require.NoError(t, err)
	_, err = machine.TransitionVideoState(ctx, video.ID,
		state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	_, err = machine.TransitionVideoState(ctx, video.ID,
		state.VideoStateEvaluating, state.VideoStateEvaluated,
		state.WithFinalOutcome(state.FinalOutcomeRejected))
	require.NoError(t, err)
	itemID, err := queueStore.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID: video.ID, RunID: originID, Reason: queue.DeferredReasonQuotaExhausted,
		Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE run SET status = ? WHERE id = ?`,
		string(state.RunStatusCompleted), originID)
	require.NoError(t, err)

	require.NoError(t, p.RetryQueueEntry(ctx, itemID, false))

	runs, err := stateStore.ListRuns(ctx)
	require.NoError(t, err)
	var retryRun *state.Run
	for i := range runs {
		if runs[i].ID != originID {
			retryRun = &runs[i]
			break
		}
	}
	require.NotNil(t, retryRun, "the manual retry must have created its own run")
	assert.Equal(t, state.RunStatusCompleted, retryRun.Status)
	require.NotNil(t, retryRun.VideosSelected,
		"a manual retry's claimed group is its selector's established input set")
	assert.Equal(t, 1, *retryRun.VideosSelected)
}
