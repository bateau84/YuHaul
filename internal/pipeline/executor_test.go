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
}

func (r *recordingInserter) InsertPlaylistItem(ctx context.Context, playlistID, videoID string) error {
	r.calls = append(r.calls, struct{ PlaylistID, VideoID string }{playlistID, videoID})
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
}

func TestExecutor_WithYouTubeBaseURLUsesOverrideURL(t *testing.T) {
	ctx := context.Background()
	db, stateStore, ledger, cleanup := setupExecutorTest(t)
	defer cleanup()

	routeID := createRoute(t, ctx, db, "Jazz", 1)
	createRouteAction(t, ctx, db, routeID, "PLjazz")
	runID, videoID, _ := createRunAndVideo(t, ctx, db, stateStore)
	decisionID := createRouteDecision(t, ctx, stateStore, videoID, runID, routeID, 1, state.RouteDecisionMatched)

	var requestPath string
	targetURL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "item-id",
			"snippet": map[string]any{
				"playlistId": "PLjazz",
				"resourceId": map[string]any{"kind": "youtube#video", "videoId": "vid1"},
			},
		})
	}))
	defer targetURL.Close()

	exec := NewExecutor(db, ledger, &fakeTokenSource{}, WithYouTubeBaseURL(targetURL.URL))

	outcome, err := exec.Execute(ctx, runID, videoID, []RouteMatch{{RouteDecisionID: decisionID, RouteID: routeID}})
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeAccepted, outcome)
	assert.Equal(t, "/playlistItems", requestPath)
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
}

func createRoute(t *testing.T, ctx context.Context, db *sql.DB, name string, order int) int64 {
	t.Helper()
	id, err := router.NewStore(db).CreateRoute(ctx, &router.Route{Name: name, EvaluationOrder: order, IsEnabled: true, ConditionComposition: "AND"})
	require.NoError(t, err)
	return id
}

func createRouteAction(t *testing.T, ctx context.Context, db *sql.DB, routeID int64, playlistID string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO route_action (route_id, action_type, target_playlist_id, action_index)
		 VALUES (?, 'playlist_insert', ?, 0);`,
		routeID, playlistID)
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
