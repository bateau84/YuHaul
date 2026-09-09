//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/youtube"
)

// idempotencyTestEnv holds the real pipeline and its dependencies for one
// integration test. Every dependency is a real implementation; only the
// YouTube API is replaced by a deterministic mock server.
type idempotencyTestEnv struct {
	stateStore    *state.Store
	machine       *state.Machine
	pipeline      *Pipeline
	server        *mockYouTubeServer
	ledger        *quota.Ledger
	playlistID    string
	routeID       int64
	targetVideoID string
}

// fakeMetadataFetcher satisfies metadata.Manager's Fetcher interface. The
// idempotency tests use freshly fetched metadata, so no refresh is required.
type fakeMetadataFetcher struct{}

func (f *fakeMetadataFetcher) FetchMetadata(ctx context.Context, videoIDs []string) ([]metadata.TransientMetadata, error) {
	return nil, nil
}

// mockYouTubeServer serves deterministic YouTube Data API v3 responses and
// records playlist insertions so tests can assert on real pipeline output.
type mockYouTubeServer struct {
	server        *httptest.Server
	mu            sync.Mutex
	insertions    []playlistInsertion
	subscriptions []string
	channels      []mockChannel
	videos        []mockVideo
}

type playlistInsertion struct {
	PlaylistID string
	VideoID    string
}

type mockChannel struct {
	ID                string
	Title             string
	UploadsPlaylistID string
}

type mockVideo struct {
	ID              string
	Title           string
	ChannelID       string
	ChannelTitle    string
	Description     string
	DurationSeconds int
	PublishedAt     time.Time
	ThumbnailURL    string
}

func newMockYouTubeServer(channels []mockChannel, videos []mockVideo) *mockYouTubeServer {
	m := &mockYouTubeServer{
		subscriptions: []string{"UC1"},
		channels:      channels,
		videos:        videos,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", m.handleSubscriptions)
	mux.HandleFunc("/channels", m.handleChannels)
	mux.HandleFunc("/playlistItems", m.handlePlaylistItems)
	mux.HandleFunc("/videos", m.handleVideos)
	m.server = httptest.NewServer(mux)
	return m
}

func (m *mockYouTubeServer) URL() string {
	return m.server.URL
}

func (m *mockYouTubeServer) Close() {
	m.server.Close()
}

func (m *mockYouTubeServer) Insertions() []playlistInsertion {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]playlistInsertion, len(m.insertions))
	copy(out, m.insertions)
	return out
}

func (m *mockYouTubeServer) ResetInsertions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertions = nil
}

func (m *mockYouTubeServer) recordInsertion(playlistID, videoID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertions = append(m.insertions, playlistInsertion{PlaylistID: playlistID, VideoID: videoID})
}

func (m *mockYouTubeServer) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, len(m.subscriptions))
	for i, ch := range m.subscriptions {
		items[i] = map[string]any{
			"snippet": map[string]any{
				"resourceId": map[string]any{"channelId": ch},
			},
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

func (m *mockYouTubeServer) handleChannels(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, len(m.channels))
	for i, ch := range m.channels {
		items[i] = map[string]any{
			"id": ch.ID,
			"snippet": map[string]any{
				"title": ch.Title,
			},
			"contentDetails": map[string]any{
				"relatedPlaylists": map[string]any{
					"uploads": ch.UploadsPlaylistID,
				},
			},
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

func (m *mockYouTubeServer) handlePlaylistItems(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Snippet struct {
				PlaylistID string `json:"playlistId"`
				ResourceID struct {
					VideoID string `json:"videoId"`
				} `json:"resourceId"`
			} `json:"snippet"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.recordInsertion(body.Snippet.PlaylistID, body.Snippet.ResourceID.VideoID)
		writeJSON(w, map[string]any{"id": "inserted-item"})
		return
	}

	items := make([]map[string]any, len(m.videos))
	for i, v := range m.videos {
		items[i] = map[string]any{
			"snippet": map[string]any{
				"title":       v.Title,
				"channelId":   v.ChannelID,
				"publishedAt": v.PublishedAt.Format(time.RFC3339Nano),
				"resourceId":  map[string]any{"videoId": v.ID, "kind": "youtube#video"},
				"thumbnails": map[string]any{
					"default": map[string]any{"url": v.ThumbnailURL},
				},
			},
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

func (m *mockYouTubeServer) handleVideos(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, len(m.videos))
	for i, v := range m.videos {
		dur := "PT0S"
		if v.DurationSeconds > 0 {
			dur = formatISODuration(v.DurationSeconds)
		}
		items[i] = map[string]any{
			"id": v.ID,
			"snippet": map[string]any{
				"title":        v.Title,
				"channelId":    v.ChannelID,
				"channelTitle": v.ChannelTitle,
				"description":  v.Description,
				"publishedAt":  v.PublishedAt.Format(time.RFC3339Nano),
				"thumbnails": map[string]any{
					"default": map[string]any{"url": v.ThumbnailURL},
				},
			},
			"contentDetails": map[string]any{
				"duration": dur,
			},
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func formatISODuration(seconds int) string {
	m := seconds / 60
	s := seconds % 60
	return "PT" + itoa(m) + "M" + itoa(s) + "S"
}

func itoa(n int) string {
	// Avoid importing strconv just for this helper.
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func setupIdempotencyTest(t *testing.T, videos []mockVideo) *idempotencyTestEnv {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	path := filepath.Join(dir, "idempotency.db")

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(path)
	})

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	machine := state.NewMachine(stateStore)

	ledger := quota.New(db, quota.WithDefaultLimit(1_000_000))

	mock := newMockYouTubeServer([]mockChannel{
		{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"},
	}, videos)
	t.Cleanup(mock.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	discoveryFactory := func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) *youtube.Client {
		return youtube.NewClient(ctx, token,
			youtube.WithBaseURL(mock.URL()),
			youtube.WithHTTPClient(mock.server.Client()),
			youtube.WithRecorder(recorder),
			youtube.WithLogger(logger),
		)
	}
	disc := discovery.NewEngine(&fakeTokenSource{}, machine, ledger, db,
		discovery.WithClientFactory(discoveryFactory),
		discovery.WithLogger(logger),
	)

	metaStore := metadata.NewStore(db)
	meta := metadata.NewManager(metaStore, &fakeMetadataFetcher{}, ledger,
		metadata.WithLogger(logger),
	)

	detector := duplicate.NewDetector(db, duplicate.WithLogger(logger))

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	versioner := config.NewService(routerStore, cfgStore)

	routeID, err := routerStore.CreateRoute(ctx, &router.Route{
		Name:                 "Jazz",
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "OR",
		Conditions: []router.RouteCondition{
			{
				ConditionIndex: 0,
				Field:          router.ConditionFieldTitle,
				Operator:       router.ConditionOperatorContains,
				Value:          "jazz",
			},
		},
		Actions: []router.RouteAction{
			{
				ActionIndex:      0,
				ActionType:       "playlist_insert",
				TargetPlaylistID: "PLjazz",
			},
		},
	})
	require.NoError(t, err)

	routerEngine := router.NewEngine(routerStore)

	execFactory := func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) PlaylistInserter {
		return youtube.NewClient(ctx, token,
			youtube.WithBaseURL(mock.URL()),
			youtube.WithHTTPClient(mock.server.Client()),
			youtube.WithRecorder(recorder),
			youtube.WithLogger(logger),
		)
	}
	executor := NewExecutor(db, ledger, &fakeTokenSource{},
		WithExecutorLogger(logger),
		WithYouTubeFactory(execFactory),
	)

	broadcast := NewBroadcaster()
	p := NewPipeline(db, machine, stateStore, disc, detector, routerEngine, executor, ledger, meta, nil, broadcast,
		WithVersioner(versioner),
		WithLogger(logger),
	)

	targetVideoID := ""
	if len(videos) > 0 {
		targetVideoID = videos[0].ID
	}

	return &idempotencyTestEnv{
		stateStore:    stateStore,
		machine:       machine,
		pipeline:      p,
		server:        mock,
		ledger:        ledger,
		playlistID:    "PLjazz",
		routeID:       routeID,
		targetVideoID: targetVideoID,
	}
}

func (env *idempotencyTestEnv) runProductionRun(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	run, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	env.pipeline.Execute(ctx, run.ID)

	loaded, err := env.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status, "run %d did not complete: %v", run.ID, loaded.InterruptReason)
	return run.ID
}

func makeJazzVideo(id string, publishedAt time.Time) mockVideo {
	return mockVideo{
		ID:              id,
		Title:           "Jazz Live Session",
		ChannelID:       "UC1",
		ChannelTitle:    "Test Channel",
		Description:     "A great jazz session",
		DurationSeconds: 270,
		PublishedAt:     publishedAt,
		ThumbnailURL:    "http://example.com/thumb.jpg",
	}
}

// TestIdempotency_SameRunReexecution verifies INV-3: executing the same
// completed run a second time produces zero new playlist insertions.
func TestIdempotency_SameRunReexecution(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", publishedAt)})

	runID := env.runProductionRun(t)

	before := env.server.Insertions()
	require.Len(t, before, 1, "first run should insert exactly one video")
	assert.Equal(t, "V1", before[0].VideoID)

	// Re-execute the same run. The pipeline must read the durable state it
	// already produced and skip all work rather than call YouTube again.
	env.pipeline.Execute(ctx, runID)

	after := env.server.Insertions()
	assert.Len(t, after, 1, "re-executing a completed run produced new insertions")

	loaded, err := env.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.VideosAccepted)
	assert.Equal(t, 1, loaded.VideosDiscovered)
}

// TestIdempotency_CrashResume verifies INV-3: resuming an interrupted run
// after some actions already succeeded does not double-insert those actions.
func TestIdempotency_CrashResume(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupIdempotencyTest(t, []mockVideo{
		makeJazzVideo("V1", publishedAt),
		makeJazzVideo("V2", publishedAt.Add(1*time.Minute)),
	})

	// Create an interrupted run that has already completed routing and one
	// successful action, simulating a crash during action execution.
	run := &state.Run{
		StartedAt: time.Now().UTC().Add(-1 * time.Hour),
		RunType:   state.RunTypeManual,
		Status:    state.RunStatusInterrupted,
	}
	runID, err := env.stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	require.NoError(t, env.stateStore.SetRunPhase(ctx, runID, string(PhaseActionExecution)))

	v1, err := env.machine.DiscoverVideo(ctx, runID, "V1", "UC1")
	require.NoError(t, err)
	v2, err := env.machine.DiscoverVideo(ctx, runID, "V2", "UC1")
	require.NoError(t, err)

	// Move both videos through routing.
	for _, v := range []*state.Video{v1, v2} {
		_, err := env.machine.TransitionVideoState(ctx, v.ID, state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
		require.NoError(t, err)
		_, err = env.machine.TransitionVideoState(ctx, v.ID, state.VideoStateEvaluating, state.VideoStateEvaluated)
		require.NoError(t, err)
	}

	// Record matched route decisions for both videos.
	dec1, err := env.machine.RecordRouteDecision(ctx, &state.RouteDecision{
		VideoID:         v1.ID,
		RunID:           runID,
		RouteID:         env.routeID,
		EvaluationOrder: 1,
		Decision:        state.RouteDecisionMatched,
	})
	require.NoError(t, err)
	_, err = env.machine.RecordRouteDecision(ctx, &state.RouteDecision{
		VideoID:         v2.ID,
		RunID:           runID,
		RouteID:         env.routeID,
		EvaluationOrder: 1,
		Decision:        state.RouteDecisionMatched,
	})
	require.NoError(t, err)

	// Simulate that V1 was already inserted before the crash.
	now := time.Now().UTC()
	_, err = env.machine.RecordExecutedAction(ctx, &state.ExecutedAction{
		RouteDecisionID:  dec1.ID,
		RunID:            runID,
		VideoID:          v1.ID,
		ActionType:       "playlist_insert",
		TargetPlaylistID: env.playlistID,
		Outcome:          state.ActionOutcomeSucceeded,
		ExecutedAt:       &now,
	})
	require.NoError(t, err)

	// Resume the interrupted run.
	env.pipeline.Execute(ctx, runID)

	loaded, err := env.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status)

	insertions := env.server.Insertions()
	require.Len(t, insertions, 1, "resume should insert only the pending video")
	assert.Equal(t, "V2", insertions[0].VideoID)
}

// TestIdempotency_RetryCompletedAction verifies that retrying an already
// completed action through the executor is a no-op, not a duplicate.
func TestIdempotency_RetryCompletedAction(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", publishedAt)})

	runID := env.runProductionRun(t)
	require.Len(t, env.server.Insertions(), 1)

	v, err := env.stateStore.GetVideoByExternalID(ctx, "V1")
	require.NoError(t, err)

	decisions, err := env.stateStore.GetRouteDecisionsForVideo(ctx, v.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)

	// Retry the completed action by invoking the executor directly with the
	// same route match. The executor must see the existing succeeded
	// ExecutedAction and return without calling YouTube.
	outcome, err := env.pipeline.executor.Execute(ctx, runID, v.ID, []RouteMatch{
		{RouteDecisionID: decisions[0].ID, RouteID: env.routeID},
	})
	require.NoError(t, err)
	assert.Equal(t, state.FinalOutcomeAccepted, outcome)

	assert.Len(t, env.server.Insertions(), 1, "retrying a completed action produced a new insertion")
}

// TestIdempotency_RediscoverySkipsRoutedVideo verifies that a video already
// routed by a prior run is not inserted again when the pipeline encounters it
// in a subsequent run's discovery feed.
func TestIdempotency_RediscoverySkipsRoutedVideo(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", publishedAt)})

	run1ID := env.runProductionRun(t)
	require.Len(t, env.server.Insertions(), 1)

	run1, err := env.stateStore.GetRun(ctx, run1ID)
	require.NoError(t, err)

	// Run 2 returns the same video with a publishedAt after run 1 started,
	// bypassing the channel watermark guard. The video_id uniqueness guard
	// in discovery prevents re-insertion into the video table, so no new
	// action is executed.
	env.server.videos = []mockVideo{makeJazzVideo("V1", run1.StartedAt.Add(1*time.Minute))}

	run2ID := env.runProductionRun(t)

	run2, err := env.stateStore.GetRun(ctx, run2ID)
	require.NoError(t, err)
	assert.Equal(t, 0, run2.VideosDiscovered, "re-discovered video should not create a new video record")
	assert.Equal(t, 0, run2.VideosAccepted)
	assert.Len(t, env.server.Insertions(), 1, "re-discovering a routed video produced a new insertion")
}
