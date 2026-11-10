//go:build integration

// Package e2e contains the final vertical-composition proof for YuHaul.
//
// Every test in this file runs the real processing pipeline against a real
// SQLite database with a mocked YouTube Data API v3 server. The goal is to
// falsify each of the nine architecture invariants (INV-1..INV-9) end-to-end.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/youtube"
)

// ---------- fixtures ----------

type localOAuthTransport struct {
	target string
	base   http.RoundTripper
}

func (t localOAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
	copy.Host = target.Host
	return t.base.RoundTrip(copy)
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

type mockChannel struct {
	ID                string
	Title             string
	UploadsPlaylistID string
}

type playlistInsertion struct {
	PlaylistID string
	VideoID    string
}

// mockYouTubeServer serves deterministic YouTube Data API v3 responses and
// records playlist insertions so tests can assert on real pipeline output.
type mockYouTubeServer struct {
	server               *httptest.Server
	mu                   sync.Mutex
	requests             int
	insertions           []playlistInsertion
	subscriptions        []string
	channels             []mockChannel
	videos               []mockVideo
	expectedBearer       string
	unauthorizedRequests int

	// Failure-injection knobs let composition tests exercise real
	// provider-error paths through the same base-URL seam (ARD-001
	// diagnostic boundary). Zero/empty means normal provider behavior.
	subscriptionsFailStatus int
	subscriptionsFailBody   string
	insertFailTarget        string
	insertFailStatus        int
	insertFailBody          string
	// insertHook sees every playlist POST before it is applied. A non-zero
	// status makes the endpoint answer with it and record nothing; the hook may
	// block first to hold the call in flight.
	insertHook func(playlistID, videoID string) (status int, body string)
}

func newMockYouTubeServer(channels []mockChannel, videos []mockVideo) *mockYouTubeServer {
	// The mock subscribes the account to exactly the channels the test passed,
	// so discovery fetches the channels under test instead of a hard-coded
	// subscription list that only happens to work because handleChannels
	// returns every channel regardless of the requested IDs.
	subscriptions := make([]string, 0, len(channels))
	for _, ch := range channels {
		subscriptions = append(subscriptions, ch.ID)
	}
	m := &mockYouTubeServer{
		subscriptions: subscriptions,
		channels:      channels,
		videos:        videos,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", m.handleSubscriptions)
	mux.HandleFunc("/channels", m.handleChannels)
	mux.HandleFunc("/playlistItems", m.handlePlaylistItems)
	mux.HandleFunc("/videos", m.handleVideos)
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.requests++
		if m.expectedBearer != "" && r.Header.Get("Authorization") != "Bearer "+m.expectedBearer {
			m.unauthorizedRequests++
		}
		m.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	return m
}

func (m *mockYouTubeServer) URL() string { return m.server.URL }
func (m *mockYouTubeServer) Close()      { m.server.Close() }

func (m *mockYouTubeServer) Requests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func (m *mockYouTubeServer) expectBearer(t *testing.T, token string) {
	t.Helper()
	m.mu.Lock()
	m.expectedBearer = token
	m.mu.Unlock()
}

func (m *mockYouTubeServer) assertBearerRequests(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Greater(t, m.requests, 0, "expected provider requests")
	require.Zero(t, m.unauthorizedRequests, "all provider requests must carry the synthetic bearer")
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

// FailSubscriptions makes every /subscriptions request fail with the given
// HTTP status and raw JSON body, standing in for a real provider outage before
// any selector runs.
func (m *mockYouTubeServer) FailSubscriptions(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscriptionsFailStatus = status
	m.subscriptionsFailBody = body
}

// FailPlaylistInsert makes playlist inserts targeting one playlist ID fail
// with the given HTTP status and raw body, standing in for a real provider
// failure (including quotaExceeded) at action-attempt time. Inserts for other
// targets keep succeeding.
func (m *mockYouTubeServer) FailPlaylistInsert(playlistID string, status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertFailTarget = playlistID
	m.insertFailStatus = status
	m.insertFailBody = body
}

// ClearFailures restores normal provider behavior on every endpoint.
func (m *mockYouTubeServer) ClearFailures() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscriptionsFailStatus = 0
	m.subscriptionsFailBody = ""
	m.insertFailTarget = ""
	m.insertFailStatus = 0
	m.insertFailBody = ""
}

// writeProviderFailure responds with a raw YouTube-style JSON error body so
// composition tests can prove payload text never crosses the diagnostic
// boundary (ARD-001).
func writeProviderFailure(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (m *mockYouTubeServer) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	status, body := m.subscriptionsFailStatus, m.subscriptionsFailBody
	m.mu.Unlock()
	if status != 0 {
		writeProviderFailure(w, status, body)
		return
	}
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
		m.mu.Lock()
		failTarget, failStatus, failBody := m.insertFailTarget, m.insertFailStatus, m.insertFailBody
		hook := m.insertHook
		m.mu.Unlock()
		if hook != nil {
			if status, payload := hook(body.Snippet.PlaylistID, body.Snippet.ResourceID.VideoID); status != 0 {
				writeProviderFailure(w, status, payload)
				return
			}
		}
		if failTarget != "" && failTarget == body.Snippet.PlaylistID {
			writeProviderFailure(w, failStatus, failBody)
			return
		}
		m.recordInsertion(body.Snippet.PlaylistID, body.Snippet.ResourceID.VideoID)
		writeJSON(w, map[string]any{"id": "inserted-item", "snippet": map[string]any{
			"playlistId": body.Snippet.PlaylistID,
			"resourceId": map[string]any{"videoId": body.Snippet.ResourceID.VideoID},
		}})
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

// fakeTokenSource returns a deterministic OAuth2 token for the mock server.
type fakeTokenSource struct{}

func (f *fakeTokenSource) ActiveToken(ctx context.Context) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "test"}, nil
}

// fakeMetadataFetcher satisfies metadata.Manager's Fetcher interface. The
// end-to-end tests use freshly fetched metadata, so no refresh is required.
type fakeMetadataFetcher struct{}

func (f *fakeMetadataFetcher) FetchMetadata(ctx context.Context, videoIDs []string) ([]metadata.TransientMetadata, error) {
	return nil, nil
}

// ---------- harness ----------

type testEnv struct {
	db          *sql.DB
	dbPath      string
	stateStore  *state.Store
	machine     *state.Machine
	pipeline    *pipeline.Pipeline
	broadcast   *pipeline.Broadcaster
	ledger      *quota.Ledger
	server      *mockYouTubeServer
	routerStore *router.Store
	configStore *config.Store
	configSvc   *config.Service
	metaStore   *metadata.Store
	meta        *metadata.Manager
}

func setupEnv(t *testing.T, channels []mockChannel, videos []mockVideo) *testEnv {
	return setupEnvWithCompositionAdapters(t, channels, videos, false)
}

// setupDiagnosticCompositionEnv additionally uses provider-backed metadata.
func setupDiagnosticCompositionEnv(t *testing.T, channels []mockChannel, videos []mockVideo) *testEnv {
	return setupEnvWithCompositionAdapters(t, channels, videos, true)
}

func setupEnvWithCompositionAdapters(t *testing.T, channels []mockChannel, videos []mockVideo, productionAdapters bool) *testEnv {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	path := filepath.Join(dir, "e2e.db")

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(path)
	})

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	ledger := quota.New(db, quota.WithDefaultLimit(1_000_000))
	machine := state.NewMachine(stateStore, state.WithLedger(ledger))

	mock := newMockYouTubeServer(channels, videos)
	t.Cleanup(mock.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	key := []byte("01234567890123456789012345678901")
	tokenStore, err := auth.NewSQLiteTokenStore(db, key)
	require.NoError(t, err)
	// Reject any unexpected OAuth request rather than reaching a live provider.
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected OAuth request", http.StatusBadRequest)
	}))
	t.Cleanup(oauthServer.Close)
	authSvc, err := auth.NewService(&auth.ServiceConfig{ClientID: "test-client", ClientSecret: "test-secret", RedirectURL: "http://localhost/callback", EncryptionKey: string(key), HTTPClient: &http.Client{Transport: localOAuthTransport{target: oauthServer.URL, base: guardTransport(t, oauthServer.Client().Transport)}}}, tokenStore, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, tokenStore.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "diagnostic-synthetic-token", TokenType: "Bearer", Expiry: now.Add(time.Hour), ConnectedAt: now, UpdatedAt: now}))
	mock.expectBearer(t, "diagnostic-synthetic-token")
	disc := discovery.NewEngine(authSvc, machine, ledger, db,
		discovery.WithBaseURL(mock.URL()),
		discovery.WithLogger(logger),
	)

	metaStore := metadata.NewStore(db)
	var fetcher metadata.Fetcher = &fakeMetadataFetcher{}
	if productionAdapters {
		fetcher = &compositionMetadataFetcher{auth: authSvc, baseURL: mock.URL(), transport: guardTransport(t, mock.server.Client().Transport)}
	}
	meta := metadata.NewManager(metaStore, fetcher, ledger,
		metadata.WithLogger(logger),
	)

	queueStore, err := queue.NewStore(db)
	require.NoError(t, err)

	detector := duplicate.NewDetector(db, duplicate.WithLogger(logger))

	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)
	configSvc := config.NewService(routerStore, cfgStore)
	routerStore.WithOnChange(configSvc.Snapshot)

	routerEngine := router.NewEngine(routerStore)

	executor := pipeline.NewExecutor(db, ledger, authSvc,
		pipeline.WithExecutorLogger(logger),
		pipeline.WithYouTubeBaseURL(mock.URL()),
	)

	broadcast := pipeline.NewBroadcaster()
	p := pipeline.NewPipeline(db, machine, stateStore, disc, detector, routerEngine, executor, ledger, meta, authSvc, broadcast,
		pipeline.WithLogger(logger),
		pipeline.WithVersioner(configSvc),
		pipeline.WithQueueStore(queueStore),
		pipeline.WithConfigStore(cfgStore),
	)

	return &testEnv{
		db:          db,
		dbPath:      path,
		stateStore:  stateStore,
		machine:     machine,
		pipeline:    p,
		broadcast:   broadcast,
		ledger:      ledger,
		server:      mock,
		routerStore: routerStore,
		configStore: cfgStore,
		configSvc:   configSvc,
		metaStore:   metaStore,
		meta:        meta,
	}
}

// compositionMetadataFetcher mirrors cmd/server's production adapter: obtains
// the active token from auth.Service, then fetches details through YouTube's
// real client. Only the HTTP destination/client is controlled by this harness.
type compositionMetadataFetcher struct {
	auth interface {
		ActiveToken(context.Context) (*oauth2.Token, error)
	}
	baseURL   string
	transport http.RoundTripper
}

func (f *compositionMetadataFetcher) FetchMetadata(ctx context.Context, ids []string) ([]metadata.TransientMetadata, error) {
	if len(ids) == 0 {
		return []metadata.TransientMetadata{}, nil
	}
	token, err := f.auth.ActiveToken(ctx)
	if err != nil {
		return nil, err
	}
	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	httpClient.Transport = &oauth2.Transport{Source: oauth2.StaticTokenSource(token), Base: f.transport}
	client := youtube.NewClient(ctx, token, youtube.WithBaseURL(f.baseURL), youtube.WithHTTPClient(httpClient))
	details, err := client.GetVideos(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]metadata.TransientMetadata, 0, len(details))
	for _, d := range details {
		title, channel, desc, duration, thumb, published := d.Title, d.ChannelTitle, d.Description, d.DurationSeconds, d.ThumbnailURL, d.PublishedAt
		out = append(out, metadata.TransientMetadata{VideoID: d.ID, Title: &title, ChannelName: &channel, Description: &desc, DurationSeconds: &duration, ThumbnailURL: &thumb, PublishedAt: &published})
	}
	return out, nil
}

func (env *testEnv) createRoute(t *testing.T, name, value, playlistID string) int64 {
	t.Helper()
	ctx := context.Background()
	route := &router.Route{
		Name:                 name,
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "OR",
		Conditions: []router.RouteCondition{
			{
				ConditionIndex: 0,
				Field:          router.ConditionFieldTitle,
				Operator:       router.ConditionOperatorContains,
				Value:          value,
			},
		},
		Actions: []router.RouteAction{
			{
				ActionIndex:      0,
				ActionType:       "playlist_insert",
				TargetPlaylistID: playlistID,
			},
		},
	}
	id, err := env.routerStore.CreateRoute(ctx, route)
	require.NoError(t, err)
	return id
}

func (env *testEnv) runProductionRun(t *testing.T) *state.Run {
	t.Helper()
	ctx := context.Background()
	run, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	env.pipeline.Execute(ctx, run.ID)

	loaded, err := env.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status, "run %d did not complete: %v", run.ID, loaded.InterruptReason)
	assert.Greater(t, loaded.QuotaCost, 0, "run %d should record a positive quota cost", run.ID)
	return loaded
}

func (env *testEnv) listRunVideos(t *testing.T, runID int64) []*state.Video {
	t.Helper()
	ctx := context.Background()
	rows, err := env.db.QueryContext(ctx,
		`SELECT id, video_id, channel_id, discovered_at, discovered_in_run_id,
		        processing_state, final_outcome, deferred_reason, error_message, completed_at
		 FROM video WHERE discovered_in_run_id = ?;`, runID)
	require.NoError(t, err)
	defer rows.Close()

	var out []*state.Video
	for rows.Next() {
		var v state.Video
		var deferredReason, errorMessage, completedAt sql.NullString
		err := rows.Scan(&v.ID, &v.VideoID, &v.ChannelID, &v.DiscoveredAt, &v.DiscoveredInRunID,
			&v.ProcessingState, &v.FinalOutcome, &deferredReason, &errorMessage, &completedAt)
		require.NoError(t, err)
		if deferredReason.Valid {
			v.DeferredReason = &deferredReason.String
		}
		if errorMessage.Valid {
			v.ErrorMessage = &errorMessage.String
		}
		if completedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
			v.CompletedAt = &t
		}
		out = append(out, &v)
	}
	require.NoError(t, rows.Err())
	return out
}

func makeVideo(id, title string, publishedAt time.Time) mockVideo {
	return mockVideo{
		ID:              id,
		Title:           title,
		ChannelID:       "UC1",
		ChannelTitle:    "Test Channel",
		Description:     "description",
		DurationSeconds: 270,
		PublishedAt:     publishedAt,
		ThumbnailURL:    "http://example.com/thumb.jpg",
	}
}

// ---------- invariant tests ----------

// TestINV1_CountReconciliation verifies that every discovered video is accounted
// for in the five terminal buckets.
func TestINV1_CountReconciliation(t *testing.T) {
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{
			makeVideo("V1", "Jazz Live Session", publishedAt),
			makeVideo("V2", "Rock Concert", publishedAt),
			makeVideo("V3", "Jazz Club Recording", publishedAt),
		},
	)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	run := env.runProductionRun(t)

	require.Equal(t, 3, run.VideosDiscovered, "all videos discovered")
	total := run.VideosAccepted + run.VideosRejected + run.VideosDeferred + run.VideosFailed + run.VideosNotYetEvaluated
	assert.Equal(t, run.VideosDiscovered, total,
		"INV-1 violated: discovered=%d, sum of buckets=%d", run.VideosDiscovered, total)

	// The jazz route matches V1 and V3; V2 is rejected.
	assert.GreaterOrEqual(t, run.VideosAccepted, 1)
	assert.GreaterOrEqual(t, run.VideosRejected, 1)
}

// TestINV2_QuotaDeferral verifies that quota exhaustion mid-run defers eligible
// work rather than falsely rejecting it.
func TestINV2_QuotaDeferral(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{
			makeVideo("V1", "Jazz Live Session", publishedAt),
			makeVideo("V2", "Jazz Club Recording", publishedAt),
		},
	)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	// Discovery needs 1 (subscriptions) + 1 (channels) + 1 (playlistItems) +
	// 1 (videos.list) = 4 units. Each playlist insert costs 50. Setting the
	// limit to 54 lets the first insert succeed and defers the second.
	require.NoError(t, env.ledger.SetDailyLimit(ctx, 54))

	run := env.runProductionRun(t)

	require.Equal(t, 2, run.VideosDiscovered)
	assert.GreaterOrEqual(t, run.VideosDeferred, 1, "quota exhaustion should defer at least one video")
	assert.Equal(t, 0, run.VideosFailed, "quota exhaustion must not mark videos failed")

	// A subsequent run with ample quota should process the deferred item.
	require.NoError(t, env.ledger.SetDailyLimit(ctx, 1_000_000))
	run2 := env.runProductionRun(t)
	require.Equal(t, 0, run2.VideosDiscovered, "deferred-only run should discover no new videos")
	var retriedID int64
	require.NoError(t, env.db.QueryRowContext(ctx, `SELECT video_id FROM deferred_item WHERE run_id=? ORDER BY id LIMIT 1`, run.ID).Scan(&retriedID))
	var originOutcome, receivingOutcome string
	require.NoError(t, env.db.QueryRowContext(ctx, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, run.ID, retriedID).Scan(&originOutcome))
	require.NoError(t, env.db.QueryRowContext(ctx, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, run2.ID, retriedID).Scan(&receivingOutcome))
	assert.Equal(t, string(state.FinalOutcomeDeferred), originOutcome, "origin disposition must remain immutable")
	assert.Equal(t, string(state.FinalOutcomeAccepted), receivingOutcome, "retry disposition must belong to receiving run")
	assert.Equal(t, 1, run2.VideosAccepted, "receiving run counts retried accepted work")

	var pendingDeferred int
	row := env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deferred_item WHERE status = ?;`, "pending")
	require.NoError(t, row.Scan(&pendingDeferred))
	assert.Equal(t, 0, pendingDeferred, "deferred item should be completed after retry")

	var acceptedVideos int
	row = env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM video WHERE final_outcome = ?;`, state.FinalOutcomeAccepted)
	require.NoError(t, row.Scan(&acceptedVideos))
	assert.GreaterOrEqual(t, acceptedVideos, 2, "deferred item should be accepted on retry")
}

// TestINV3_IdempotentPlaylistActions verifies that re-executing a completed run
// produces zero new playlist insertions.
func TestINV3_IdempotentPlaylistActions(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{makeVideo("V1", "Jazz Live Session", publishedAt)},
	)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	run := env.runProductionRun(t)
	require.Equal(t, 1, run.VideosAccepted)
	require.Len(t, env.server.Insertions(), 1)

	// Re-execute the same completed run.
	env.pipeline.Execute(ctx, run.ID)

	assert.Len(t, env.server.Insertions(), 1, "INV-3 violated: re-execution produced new insertions")
}

// TestINV4_DecisionEvidenceCompleteness verifies that every routing outcome can
// be traced through route decisions and condition audits.
func TestINV4_DecisionEvidenceCompleteness(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{makeVideo("V1", "Jazz Live Session", publishedAt)},
	)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	run := env.runProductionRun(t)
	require.Equal(t, 1, run.VideosAccepted)

	videos := env.listRunVideos(t, run.ID)
	require.Len(t, videos, 1)

	decisions, err := env.stateStore.GetRouteDecisionsForVideo(ctx, videos[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, decisions, "INV-4 violated: no route decisions recorded")

	for _, d := range decisions {
		audits, err := env.stateStore.GetConditionAuditsForDecision(ctx, d.ID)
		require.NoError(t, err)
		assert.NotEmpty(t, audits, "INV-4 violated: decision %d has no condition audits", d.ID)
		for _, a := range audits {
			assert.NotEmpty(t, a.Field)
			assert.NotEmpty(t, a.Operator)
			assert.NotNil(t, a.ActualValue, "INV-4 violated: audit %d missing actual value", a.ID)
		}
	}
}

// TestINV5_ConfigAttribution verifies that each run records the configuration
// version active at the time of the run, not the current configuration.
func TestINV5_ConfigAttribution(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{makeVideo("V1", "Jazz Live Session", publishedAt)},
	)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	run1 := env.runProductionRun(t)
	require.NotNil(t, run1.ConfigVersionID)
	cv1, err := env.configStore.Get(ctx, *run1.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cv1.Snapshot.Routes, 1)
	assert.Equal(t, "jazz", cv1.Snapshot.Routes[0].Conditions[0].Value)

	// Change the route condition value.
	route, err := env.routerStore.GetRoute(ctx, cv1.Snapshot.Routes[0].ID)
	require.NoError(t, err)
	route.Conditions[0].Value = "classical"
	require.NoError(t, env.routerStore.UpdateRoute(ctx, route))

	run2 := env.runProductionRun(t)
	require.NotNil(t, run2.ConfigVersionID)
	cv2, err := env.configStore.Get(ctx, *run2.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cv2.Snapshot.Routes, 1)
	assert.Equal(t, "classical", cv2.Snapshot.Routes[0].Conditions[0].Value)

	// Historical run still shows the old config.
	assert.Equal(t, "jazz", cv1.Snapshot.Routes[0].Conditions[0].Value,
		"INV-5 violated: historical run config mutated")
}

// TestINV6_HistoricalCorpusIntegrity verifies that imported historical
// fingerprints and natively discovered fingerprints produce identical duplicate
// detection results.
func TestINV6_HistoricalCorpusIntegrity(t *testing.T) {
	ctx := context.Background()
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		nil,
	)

	// Seed two duplicate-reference fingerprints for the same normalized title,
	// one from migration and one from native discovery.
	batchID := "batch-1"
	_, err := duplicate.SaveFingerprint(ctx, env.db, "MIG1", "UC1", "user_import", "Jazz Live Session", true, &batchID)
	require.NoError(t, err)
	_, err = duplicate.SaveFingerprint(ctx, env.db, "NAT1", "UC1", "api_discovery", "Jazz Live Session", true, nil)
	require.NoError(t, err)

	// Now detect a new similar title. Both references should be candidates and
	// produce the same Jaccard score because they share the same normalized
	// token set.
	match, err := duplicate.NewDetector(env.db).Detect(ctx, "NEW1", "Live Jazz Session")
	require.NoError(t, err)
	require.NotNil(t, match, "INV-6 violated: similar title not detected against mixed corpus")
	assert.Greater(t, match.Similarity, 0.0)

	// Verify both reference sources are present in the corpus.
	var sources int
	row := env.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT source) FROM title_fingerprint WHERE video_id IN (?, ?);`,
		"MIG1", "NAT1")
	require.NoError(t, row.Scan(&sources))
	assert.Equal(t, 2, sources, "INV-6 violated: both import sources must coexist")
}

// TestINV7_DistinctOutcomeStates verifies that (a) videos never evaluated remain
// not_yet_evaluated and (b) final-route skips are recorded as skipped route
// decisions, not as not_yet_evaluated video states.
func TestINV7_DistinctOutcomeStates(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{makeVideo("V1", "Jazz Live Session", publishedAt)},
	)

	// Create a route with a final flag so subsequent routes are skipped.
	route1 := &router.Route{
		Name:                 "Jazz",
		EvaluationOrder:      1,
		IsFinal:              true,
		IsEnabled:            true,
		ConditionComposition: "OR",
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"},
		},
		Actions: []router.RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"},
		},
	}
	_, err := env.routerStore.CreateRoute(ctx, route1)
	require.NoError(t, err)

	// Add a second route that would also match but should be skipped because
	// route 1 is final.
	route2 := &router.Route{
		Name:                 "Music",
		EvaluationOrder:      2,
		IsEnabled:            true,
		ConditionComposition: "OR",
		Conditions: []router.RouteCondition{
			{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "session"},
		},
		Actions: []router.RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLmusic"},
		},
	}
	_, err = env.routerStore.CreateRoute(ctx, route2)
	require.NoError(t, err)

	run := env.runProductionRun(t)
	require.Equal(t, 1, run.VideosAccepted)

	videos := env.listRunVideos(t, run.ID)
	require.Len(t, videos, 1)
	assert.NotEqual(t, state.FinalOutcomeNotYetEvaluated, videos[0].FinalOutcome,
		"INV-7 violated: evaluated video is still not_yet_evaluated")

	decisions, err := env.stateStore.GetRouteDecisionsForVideo(ctx, videos[0].ID)
	require.NoError(t, err)
	require.Len(t, decisions, 2)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	assert.Equal(t, state.RouteDecisionSkipped, decisions[1].Decision)
	require.NotNil(t, decisions[1].SkipReason)
	assert.Equal(t, state.SkipReasonFinalRouteMatched, *decisions[1].SkipReason,
		"INV-7 violated: second route should be skipped due to final route match")
}

// TestINV8_ModeIsolation verifies that dry-run, replay, and backtest do not
// mutate production playlists, duplicate corpus, statistics, or video state.
func TestINV8_ModeIsolation(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-1 * time.Hour)
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		[]mockVideo{makeVideo("V1", "Jazz Live Session", publishedAt)},
	)
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	// Establish a production baseline.
	prod := env.runProductionRun(t)
	require.Equal(t, 1, prod.VideosAccepted)
	productionInsertions := len(env.server.Insertions())
	require.Equal(t, 1, productionInsertions)

	var productionRefCount int
	row := env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM title_fingerprint WHERE is_duplicate_reference = 1;`)
	require.NoError(t, row.Scan(&productionRefCount))
	require.Equal(t, 1, productionRefCount)

	var productionProcessed int
	row = env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM video WHERE final_outcome != ?;`, state.FinalOutcomeNotYetEvaluated)
	require.NoError(t, row.Scan(&productionProcessed))
	require.Equal(t, 1, productionProcessed)

	// Dry-run against current config.
	dr, err := env.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	env.pipeline.ExecuteDryRun(ctx, dr.ID)
	drLoaded, err := env.stateStore.GetRun(ctx, dr.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, drLoaded.Status)

	// Replay.
	rp, err := env.pipeline.Replay(ctx, nil)
	require.NoError(t, err)
	env.pipeline.ExecuteReplay(ctx, rp.ID)
	rpLoaded, err := env.stateStore.GetRun(ctx, rp.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, rpLoaded.Status)

	// Backtest with a stricter proposed config.
	bt, err := env.pipeline.Backtest(ctx, &config.ProposalSnapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes: []config.ProposalRoute{
			{
				ID:                   "9f4d3f2c-1c3a-4d9e-9e1d-7b6f5e4d3c2b",
				Name:                 "Classical",
				EvaluationOrder:      1,
				IsEnabled:            true,
				ConditionComposition: "OR",
				Conditions: []config.SnapshotCondition{
					{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "classical"},
				},
				Actions: []config.SnapshotAction{
					{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLclassical"},
				},
			},
		},
	})
	require.NoError(t, err)
	env.pipeline.ExecuteBacktest(ctx, bt.ID)
	btLoaded, err := env.stateStore.GetRun(ctx, bt.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, btLoaded.Status)

	// Verify production state did not change.
	assert.Len(t, env.server.Insertions(), productionInsertions,
		"INV-8 violated: isolated modes produced playlist insertions")

	var refCount int
	row = env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM title_fingerprint WHERE is_duplicate_reference = 1;`)
	require.NoError(t, row.Scan(&refCount))
	assert.Equal(t, productionRefCount, refCount,
		"INV-8 violated: duplicate-reference corpus changed")

	var processed int
	row = env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM video WHERE final_outcome != ?;`, state.FinalOutcomeNotYetEvaluated)
	require.NoError(t, row.Scan(&processed))
	assert.Equal(t, productionProcessed, processed,
		"INV-8 violated: isolated modes changed video processing state")

	// All isolated runs must be labeled distinctly from production.
	assert.Equal(t, state.RunTypeDryRun, drLoaded.RunType)
	assert.Equal(t, state.RunTypeReplay, rpLoaded.RunType)
	assert.Equal(t, state.RunTypeBacktest, btLoaded.RunType)
}

// TestINV9_APIMetadataCompliance verifies that no active raw metadata records
// remain past their 30-day expiry window.
func TestINV9_APIMetadataCompliance(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	env := setupEnv(t,
		[]mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}},
		nil,
	)

	// Insert an active metadata record that expired 1 day ago.
	title := "Stale Title"
	meta := metadata.TransientMetadata{
		VideoID:     "OLD1",
		Title:       &title,
		ChannelName: &title,
		Description: &title,
		FetchedAt:   now.Add(-31 * 24 * time.Hour),
		ExpiresAt:   now.Add(-1 * 24 * time.Hour),
		IsActive:    true,
	}
	require.NoError(t, env.metaStore.Upsert(ctx, meta))

	staleBefore, err := env.meta.CountStaleActive(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, staleBefore, 1, "test setup: stale record should exist")

	// Run the metadata lifecycle.
	require.NoError(t, env.meta.RunLifecycle(ctx))

	staleAfter, err := env.meta.CountStaleActive(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, staleAfter, "INV-9 violated: %d stale active records remain", staleAfter)

	// Verify the record is now inactive and raw fields are cleared.
	loaded, err := env.metaStore.Get(ctx, "OLD1")
	require.NoError(t, err)
	assert.False(t, loaded.IsActive)
	assert.Nil(t, loaded.Title)
	assert.NotNil(t, loaded.ExpiredAt)
}
