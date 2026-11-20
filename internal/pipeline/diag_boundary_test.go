package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/youtube"

	"golang.org/x/oauth2"
)

// diagSentinelBody is a fake provider response carrying secret-looking text.
// None of it may survive into persisted run/action/queue diagnostics, API
// JSON, SSE events, or logs (ARD-001 failure diagnostic boundary).
const diagSentinelBody = `{"error":{"errors":[{"reason":"backendError","message":"SENTINEL_SECRET_body_text"}],"code":500,"message":"Internal Server Error"}}`

const diagSentinel = "SENTINEL_SECRET_body_text"

// sentinelProviderError produces a real provider failure by calling the real
// youtube client against a fake provider that returns diagSentinelBody. The
// returned error is exactly what production code would carry.
func sentinelProviderError(t *testing.T) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, diagSentinelBody)
	}))
	t.Cleanup(srv.Close)

	client := youtube.NewClient(context.Background(), &oauth2.Token{AccessToken: "test"},
		youtube.WithBaseURL(srv.URL), youtube.WithHTTPClient(srv.Client()))
	err := client.InsertPlaylistItem(context.Background(), "PL1", "vid1")
	require.Error(t, err)
	require.NotContains(t, err.Error(), diagSentinel,
		"precondition: the client must not carry the raw body into the chain")
	return err
}

func TestRunFailureReason_ExcludesSentinelKeepsStatusAndReason(t *testing.T) {
	ctx := context.Background()
	providerErr := sentinelProviderError(t)

	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	p := NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, broadcast,
		WithLogger(logger))

	sub := broadcast.Subscribe(runID)
	defer broadcast.Unsubscribe(runID, sub)

	p.failRun(ctx, runID, "action execution phase failed", providerErr)

	// Persisted run reason: safe status + vetted reason, no sentinel.
	loaded, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError", *loaded.InterruptReason)
	assert.NotContains(t, *loaded.InterruptReason, diagSentinel)

	// SSE event payload: same vetted summary, no sentinel.
	var errorEvent *Event
	for {
		select {
		case ev := <-sub:
			if ev.Type == EventRunError {
				errorEvent = &ev
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for run.error event")
		}
		if errorEvent != nil {
			break
		}
	}
	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(errorEvent.Payload, &payload))
	assert.Equal(t, *loaded.InterruptReason, payload.Message)
	assert.NotContains(t, payload.Message, diagSentinel)

	// Logs: the sentinel must never reach log output either.
	assert.NotContains(t, logBuf.String(), diagSentinel)
}

// An expired or cancelled execution context is a resumable stop, not a run
// failure (ADR-007 §3.1). The terminal bookkeeping still runs on its own
// bounded context, with only the registered timeout diagnostic stored.
func TestFailRunInterruptsExpiredExecutionContext(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	require.NoError(t, stateStore.SetRunSelectedCount(ctx, runID, 1))
	require.NoError(t, stateStore.UpdateVideoState(ctx, videoID,
		state.VideoStateEvaluated, state.FinalOutcomeFailed, nil, nil))

	var logBuf bytes.Buffer
	p := NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, broadcast,
		WithLogger(slog.New(slog.NewTextHandler(&logBuf, nil))))
	expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	phaseErr := fmt.Errorf("discovery transaction: %w", context.DeadlineExceeded)
	p.failRun(expired, runID, "discovery phase failed", phaseErr)

	loaded, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusInterrupted, loaded.Status)
	require.NotNil(t, loaded.InterruptReason)
	assert.Equal(t, "diag:v1 phase=discovery operation=discover code=timeout", *loaded.InterruptReason)
	assert.Nil(t, loaded.CompletedAt, "an interrupted run is not terminal")
	require.NotNil(t, loaded.VideosSelected)
	assert.Equal(t, 1, *loaded.VideosSelected)
}

func TestFailRunUnknownMarkerFreeErrorDoesNotReachStorageOrLogs(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	const secret = "TOKEN_SENTINEL_plain_ascii_7341"
	var logs bytes.Buffer
	p := NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, broadcast,
		WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	sub := broadcast.Subscribe(runID)
	defer broadcast.Unsubscribe(runID, sub)
	p.failRun(ctx, runID, "routing phase failed", fmt.Errorf("evaluate video xyz-secret: %s", secret))
	stored, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, stored.InterruptReason)
	assert.Equal(t, "diag:v1 phase=routing operation=evaluate_video code=unknown_error", *stored.InterruptReason)
	assert.NotContains(t, logs.String(), secret)
	select {
	case event := <-sub:
		assert.NotContains(t, string(event.Payload), secret)
		assert.Contains(t, string(event.Payload), "diag:v1 phase=routing operation=evaluate_video code=unknown_error")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure event")
	}
}

func TestCompleteRunFinalizesExpiredExecutionContext(t *testing.T) {
	ctx := context.Background()
	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	require.NoError(t, stateStore.SetRunSelectedCount(ctx, runID, 1))
	require.NoError(t, stateStore.UpdateVideoState(ctx, videoID,
		state.VideoStateEvaluated, state.FinalOutcomeAccepted, nil, nil))

	var logBuf bytes.Buffer
	p := NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, broadcast,
		WithLogger(slog.New(slog.NewTextHandler(&logBuf, nil))))
	expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	p.completeRun(expired, runID)

	loaded, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusCompleted, loaded.Status)
	require.NotNil(t, loaded.CompletedAt)
	require.NotNil(t, loaded.VideosSelected)
	assert.Equal(t, 1, *loaded.VideosSelected)
	assert.NotEqual(t, state.RunStatusInterrupted, loaded.Status)
	assert.Contains(t, logBuf.String(), "complete run failed", "the canceled first attempt remains diagnosable")
}

func TestHistoricalRunFailureReason_ExcludesSentinel(t *testing.T) {
	ctx := context.Background()
	providerErr := sentinelProviderError(t)

	db, stateStore, machine, broadcast, cleanup := setupPipelineTest(t)
	defer cleanup()

	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeDryRun, Status: state.RunStatusRunning}
	runID, err := stateStore.CreateRun(ctx, run)
	require.NoError(t, err)

	var logBuf bytes.Buffer
	p := NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, broadcast,
		WithLogger(slog.New(slog.NewTextHandler(&logBuf, nil))))

	sub := broadcast.Subscribe(runID)
	defer broadcast.Unsubscribe(runID, sub)

	p.failHistoricalRun(ctx, runID, "dry-run video failed", providerErr)

	loaded, err := stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, state.RunStatusFailed, loaded.Status)
	require.NotNil(t, loaded.InterruptReason)
	assert.Contains(t, *loaded.InterruptReason, "backendError")
	assert.NotContains(t, *loaded.InterruptReason, diagSentinel)
	assert.NotContains(t, logBuf.String(), diagSentinel)

	select {
	case ev := <-sub:
		require.Equal(t, EventRunError, ev.Type)
		assert.NotContains(t, string(ev.Payload), diagSentinel)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run.error event")
	}
}

// TestActionFailureQueueAndOutcome_ExcludeSentinel drives a real provider
// failure through the executor against the real youtube client seam and
// proves the retained queue row and per-video outcome carry only the vetted
// summary.
func TestActionFailureQueueAndOutcome_ExcludeSentinel(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, diagSentinelBody)
	}))
	t.Cleanup(srv.Close)

	var logBuf bytes.Buffer
	exec := NewExecutor(f.db, f.ledger, f.auth,
		WithExecutorLogger(slog.New(slog.NewTextHandler(&logBuf, nil))),
		WithYouTubeBaseURL(srv.URL))

	outcome, execErr := exec.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
	require.Error(t, execErr)
	assert.Equal(t, state.FinalOutcomeFailed, outcome)

	// Retained queue row: vetted status/reason, no sentinel, lifecycle intact.
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	active, err := qs.ListErrorEntries(ctx, queue.ErrorStatusActive)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, queue.ErrorCategoryAPIError, active[0].Category)
	assert.Equal(t, "diag:v1 phase=action_execution operation=execute_action code=unknown_error", active[0].Message)
	assert.Equal(t, "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError", active[0].Detail)
	assert.NotContains(t, active[0].Detail, diagSentinel)
	assert.NotContains(t, active[0].Message, diagSentinel)

	// Action evidence stays message-free (Task 1.3 invariant) and sentinel-free.
	actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Nil(t, actions[0].ErrorMessage)
	assert.NotContains(t, logBuf.String(), diagSentinel)
}

// TestVideoOutcomeMessage_ExcludesSentinel proves the per-video outcome
// message written by executeVideoActions carries only the vetted summary.
func TestVideoOutcomeMessage_ExcludesSentinel(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	machine := state.NewMachine(f.state)
	broadcast := NewBroadcaster()
	routerStore := router.NewStore(f.db)
	cfgStore := config.NewStore(f.db)
	versioner := config.NewService(routerStore, cfgStore)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, diagSentinelBody)
	}))
	t.Cleanup(srv.Close)

	exec := NewExecutor(f.db, f.ledger, f.auth,
		WithExecutorLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithYouTubeBaseURL(srv.URL))

	p := NewPipeline(f.db, machine, f.state, nil, nil, nil, exec, nil, nil, nil, broadcast,
		WithVersioner(versioner), WithConfigStore(cfgStore),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))

	v, err := f.state.GetVideo(ctx, f.videoID)
	require.NoError(t, err)
	require.NoError(t, f.state.UpdateVideoState(ctx, f.videoID,
		state.VideoStateEvaluated, state.FinalOutcomeNotYetEvaluated, nil, nil))
	v.ProcessingState = state.VideoStateEvaluated

	// A provider failure is a handled per-video outcome (ADR-006 §1): the
	// disposition and its sink commit and the run may continue.
	outcome, execErr := p.executeVideoActions(ctx, f.runID, v)
	require.NoError(t, execErr)
	assert.Equal(t, state.FinalOutcomeFailed, outcome)

	var sinks int
	require.NoError(t, f.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM error_entry WHERE run_id=? AND video_id=? AND status='active'`, f.runID, f.videoID).Scan(&sinks))
	assert.Equal(t, 1, sinks)

	var msg sql.NullString
	require.NoError(t, f.db.QueryRowContext(ctx,
		`SELECT error_message FROM run_video_outcome WHERE run_id = ? AND video_id = ?`, f.runID, f.videoID).
		Scan(&msg))
	require.True(t, msg.Valid, "a failed video must carry an outcome message")
	assert.Contains(t, msg.String, "backendError", "vetted provider reason must remain visible")
	assert.NotContains(t, msg.String, diagSentinel)
}
