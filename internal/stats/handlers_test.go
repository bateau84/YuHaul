package stats

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/advisory"
	"github.com/bateau84/YuHaul/internal/state"
)

func TestHandler_RunStats(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	runID := seedRun(t, ctx, db)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/stats", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "run_id")
	assert.Contains(t, body, "videos_discovered")
}

func TestHandler_ListChannels(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 10, 5, 1)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/channels", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "UC1")
}

func TestHandler_GetChannel(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 100, 40, 5)
	runID := seedRun(t, ctx, db)
	routeID := seedRoute(t, ctx, db, "Music", 1)

	res, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'accepted');`, time.Now().UTC(), runID)
	require.NoError(t, err)
	videoID, err := res.LastInsertId()
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason, rejection_reason)
		 VALUES (?, ?, ?, 1, 'matched', NULL, NULL);`, videoID, runID, routeID)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/channels/UC1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"yield":`)
	assert.Contains(t, body, `"duplicate_provenance":`)
	assert.Contains(t, body, `"retention_signals":`)
	assert.Contains(t, body, `"matched_routes":`)

	// matched_routes entries are route-identity objects, not name strings.
	var payload struct {
		MatchedRoutes []MatchedRoute `json:"matched_routes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.MatchedRoutes, 1)
	assert.Equal(t, routePublicID(t, ctx, db, routeID), payload.MatchedRoutes[0].RouteID)
	assert.Equal(t, "Music", payload.MatchedRoutes[0].Name)
	assert.Equal(t, 1, payload.MatchedRoutes[0].MatchCount)
}

func TestHandler_GetChannel_RealResponseShape(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 100, 40, 5)
	runID := seedRun(t, ctx, db)

	publishedAt := time.Now().UTC().Add(-24 * time.Hour)
	_, err := db.ExecContext(ctx,
		`INSERT INTO transient_metadata (video_id, title, published_at, fetched_at, expires_at)
		 VALUES ('v1', 'Test Title', ?, ?, ?);`, publishedAt, time.Now().UTC(), time.Now().UTC().Add(30*24*time.Hour))
	require.NoError(t, err)
	res, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'accepted');`, time.Now().UTC(), runID)
	require.NoError(t, err)
	videoID, err := res.LastInsertId()
	require.NoError(t, err)

	// A duplicate-match whose discovery-credit fingerprint has no channel row
	// must not break the channel-detail read path (nullable LEFT JOIN columns).
	_, err = db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, channel_id, source)
		 VALUES ('v0', 'fp0', 'UC_absent', 'api_discovery');`)
	require.NoError(t, err)
	var fingerprintID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM title_fingerprint WHERE video_id = 'v0';`).Scan(&fingerprintID))
	_, err = db.ExecContext(ctx,
		`INSERT INTO duplicate_match (video_id, matched_fingerprint_id, similarity_score, match_type, threshold_used)
		 VALUES (?, ?, 0.95, 'fuzzy_title', 0.9);`, videoID, fingerprintID)
	require.NoError(t, err)

	var channelID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM channel WHERE channel_id = 'UC1';`).Scan(&channelID))
	advisoryStore := advisory.NewStore(db)
	service.WithAdvisories(advisoryStore)
	_, err = advisoryStore.CreateAdvisory(ctx, channelID, advisory.AdvisoryTypeLowUsefulness, map[string]any{
		"window_days": 90,
		"discovered":  100,
		"retained":    5,
	})
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/channels/UC1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	// Advisories use the web client's vocabulary: advisory_type + channel_id,
	// never the old type/channel_id_text pair.
	rawAdvisories, ok := payload["advisories"].([]any)
	require.True(t, ok, "advisories must be a JSON array, got %T", payload["advisories"])
	require.Len(t, rawAdvisories, 1)
	first, ok := rawAdvisories[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "low_usefulness", first["advisory_type"])
	assert.Equal(t, "UC1", first["channel_id"])
	assert.NotContains(t, first, "type")
	assert.NotContains(t, first, "channel_id_text")
	assert.NotContains(t, rec.Body.String(), "channel_id_text")

	// Recent videos come from real pipeline state joined with metadata.
	rawVideos, ok := payload["recent_videos"].([]any)
	require.True(t, ok, "recent_videos must be a JSON array, got %T", payload["recent_videos"])
	require.Len(t, rawVideos, 1)
	video, ok := rawVideos[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v1", video["video_id"])
	assert.Equal(t, "Test Title", video["title"])
	assert.Equal(t, "accepted", video["final_outcome"])
	assert.NotNil(t, video["published_at"])
}

func TestHandler_ListChannels_AdvisoryBadges(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 100, 40, 5)

	var channelID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM channel WHERE channel_id = 'UC1';`).Scan(&channelID))
	advisoryStore := advisory.NewStore(db)
	service.WithAdvisories(advisoryStore)
	_, err := advisoryStore.CreateAdvisory(ctx, channelID, advisory.AdvisoryTypeLowUsefulness, map[string]any{
		"window_days": 90,
		"discovered":  100,
		"retained":    5,
	})
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/channels", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	channels, ok := payload["channels"].([]any)
	require.True(t, ok, "channels must be a JSON array, got %T", payload["channels"])
	require.Len(t, channels, 1)
	row, ok := channels[0].(map[string]any)
	require.True(t, ok)
	advisories, ok := row["advisories"].([]any)
	require.True(t, ok, "channel row must carry advisories, got %T", row["advisories"])
	require.Len(t, advisories, 1)
	first, ok := advisories[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "low_usefulness", first["advisory_type"])
	assert.Equal(t, "UC1", first["channel_id"])
}

func TestHandler_RetentionPlaylistCRUD(t *testing.T) {
	_, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	body := strings.NewReader(`{"playlist_id":"PL1","playlist_name":"Rotation"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/retention-playlists", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), "PL1")

	req = httptest.NewRequest(http.MethodGet, "/api/settings/retention-playlists", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Rotation")
}

func TestHandler_Trends(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	now := time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, videos_discovered, videos_accepted, videos_rejected)
		 VALUES (?, 'manual', 'completed', 5, 2, 3);`, now.Format(state.RunTimestampLayout))
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/trends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "buckets")
}

func TestHandler_RouteMatchRates(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, videos_discovered, videos_accepted, videos_rejected)
		 VALUES (?, 'manual', 'completed', 5, 2, 3);`, now.Format(state.RunTimestampLayout))
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)

	routeID := seedRoute(t, ctx, db, "Music", 1)

	res, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'accepted');`, now.Format(state.RunTimestampLayout), runID)
	require.NoError(t, err)
	videoID, err := res.LastInsertId()
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason, rejection_reason)
		 VALUES (?, ?, ?, 1, 'matched', NULL, NULL);`, videoID, runID, routeID)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/route-match-rates", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "routes")
	assert.Contains(t, body, "Music")
	assert.Contains(t, body, `"match_rate":1`)

	// The analytics route reference is a UUID string, never an integer key.
	var payload struct {
		Routes []RouteMatchRate `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Routes, 1)
	assert.Regexp(t, canonicalUUIDv4, payload.Routes[0].RouteID)
	assert.Equal(t, routePublicID(t, ctx, db, routeID), payload.Routes[0].RouteID)
}

func TestHandler_Trends_RangeFilterExcludesOutOfRangeRuns(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)

	now := time.Now().UTC()
	// Seed through the production write path so the stored timestamp format
	// matches what the pipeline actually writes.
	seedRunWithCounters := func(startedAt time.Time, discovered, accepted, rejected int) {
		t.Helper()
		id, err := stateStore.CreateRun(ctx, &state.Run{
			StartedAt: startedAt,
			RunType:   state.RunTypeManual,
			Status:    state.RunStatusCompleted,
		})
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`UPDATE run SET videos_discovered = ?, videos_accepted = ?, videos_rejected = ? WHERE id = ?;`,
			discovered, accepted, rejected, id)
		require.NoError(t, err)
	}
	// Run outside the requested range (40 days ago): must be excluded.
	seedRunWithCounters(now.Add(-40*24*time.Hour), 100, 90, 10)
	// Run inside the requested range (2 days ago): must be included.
	seedRunWithCounters(now.Add(-2*24*time.Hour), 5, 2, 3)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	from := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	to := now.Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/trends?from="+url.QueryEscape(from)+"&to="+url.QueryEscape(to), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		From    string           `json:"from"`
		To      string           `json:"to"`
		Buckets []TemporalBucket `json:"buckets"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	require.Len(t, payload.Buckets, 1, "out-of-range run must not produce a bucket")
	assert.Equal(t, 5, payload.Buckets[0].VideosDiscovered)
	assert.Equal(t, 2, payload.Buckets[0].VideosAccepted)
}

func TestHandler_Trends_RangeBoundaries(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)

	// Fixed window so boundary semantics are deterministic: from is inclusive,
	// to is exclusive. Runs are seeded through the production write path
	// (state.Store.CreateRun) so the test exercises the real stored format.
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
	seeds := []struct {
		name       string
		startedAt  time.Time
		discovered int
	}{
		{"before window", from.Add(-time.Second), 100},
		{"exactly at from (inclusive)", from, 10},
		{"inside window", from.Add(72 * time.Hour), 20},
		{"inside window with sub-second time", from.Add(72*time.Hour + 500*time.Millisecond), 4},
		{"exactly at to (exclusive)", to, 40},
		{"after window", to.Add(time.Second), 80},
	}
	for _, s := range seeds {
		id, err := stateStore.CreateRun(ctx, &state.Run{
			StartedAt: s.startedAt,
			RunType:   state.RunTypeManual,
			Status:    state.RunStatusCompleted,
		})
		require.NoError(t, err, s.name)
		_, err = db.ExecContext(ctx,
			`UPDATE run SET videos_discovered = ? WHERE id = ?;`, s.discovered, id)
		require.NoError(t, err, s.name)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/trends?from="+url.QueryEscape(from.Format(time.RFC3339))+"&to="+url.QueryEscape(to.Format(time.RFC3339)), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Buckets []TemporalBucket `json:"buckets"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	total := 0
	for _, b := range payload.Buckets {
		total += b.VideosDiscovered
	}
	// Only the three in-range runs (10 + 20 + 4) survive; the run at exactly
	// `to` and both out-of-window runs are excluded.
	assert.Equal(t, 34, total, "expected from-inclusive, to-exclusive filtering")
}

func TestHandler_RouteMatchRates_RangeFilterExcludesOutOfRangeRuns(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	now := time.Now().UTC()
	routeID := seedRoute(t, ctx, db, "Music", 1)

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)

	seedDecision := func(startedAt time.Time, decision string) {
		t.Helper()
		runID, err := stateStore.CreateRun(ctx, &state.Run{
			StartedAt: startedAt,
			RunType:   state.RunTypeManual,
			Status:    state.RunStatusCompleted,
		})
		require.NoError(t, err)
		videoID := "v-" + strconv.FormatInt(runID, 10)
		res, err := db.ExecContext(ctx,
			`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
			 VALUES (?, 'UC1', ?, ?, 'completed', 'accepted');`, videoID, startedAt, runID)
		require.NoError(t, err)
		vid, err := res.LastInsertId()
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason, rejection_reason)
			 VALUES (?, ?, ?, 1, ?, NULL, NULL);`, vid, runID, routeID, decision)
		require.NoError(t, err)
	}

	// Out-of-range run (40 days ago, matched): must not be counted.
	seedDecision(now.Add(-40*24*time.Hour), "matched")
	// In-range run (2 days ago, not matched): the only decision counted.
	seedDecision(now.Add(-2*24*time.Hour), "not_matched")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	from := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	to := now.Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/route-match-rates?from="+url.QueryEscape(from)+"&to="+url.QueryEscape(to), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Routes []RouteMatchRate `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	require.Len(t, payload.Routes, 1, "only the in-range decision set must appear")
	assert.Equal(t, 1, payload.Routes[0].Evaluated)
	assert.Equal(t, 0, payload.Routes[0].Matched)
	assert.Equal(t, 0.0, payload.Routes[0].MatchRate)
}

// A channel with no production match evidence returns an empty array, which is
// distinct from a failed read (ARS-001).
func TestHandler_ChannelDetail_MatchedRoutesEmptyArray(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 1, 0, 0)

	handler := NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/analytics/channels/UC1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"matched_routes":[]`)
}

// The route-stats wire shape carries evaluation_order only when the run's
// historical evidence supplies it: a soft-deleted route reports its snapshot
// order, while a route with no reliable historical order omits the field
// rather than exposing the current row's -id sentinel (ARS-001).
func TestHandler_RouteStats_EvaluationOrderWireShape(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	// Soft-deleted route whose snapshot recorded order 2.
	deletedID := seedRoute(t, ctx, db, "Deep Cut", 2)
	// Route with no historical order evidence at all.
	silentID := seedRoute(t, ctx, db, "Silent", 3)
	runID := seedRunWithSnapshotRoutes(t, ctx, db, []snapshotRouteSpec{
		{RouteKey: deletedID, Name: "Deep Cut", Order: 2},
	})
	videoKey := seedVideo(t, ctx, db, "v1", "UC1", runID)
	seedDecisionWithOrder(t, ctx, db, videoKey, runID, deletedID, 2, "matched")
	_, err := db.ExecContext(ctx,
		`UPDATE route SET deleted_at = ?, evaluation_order = -id WHERE id = ?;`,
		time.Now().UTC(), deletedID)
	require.NoError(t, err)

	handler := NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()

	req := httptest.NewRequest(http.MethodGet,
		"/api/runs/"+strconv.FormatInt(runID, 10)+"/route-stats", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload struct {
		RouteStats []struct {
			RouteID         string `json:"route_id"`
			RouteName       string `json:"route_name"`
			EvaluationOrder *int   `json:"evaluation_order"`
		} `json:"route_stats"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.RouteStats, 2)

	byID := make(map[string]*int, len(payload.RouteStats))
	for _, rs := range payload.RouteStats {
		byID[rs.RouteID] = rs.EvaluationOrder
	}
	deletedOrder, ok := byID[routePublicID(t, ctx, db, deletedID)]
	require.True(t, ok)
	require.NotNil(t, deletedOrder, "soft-deleted route must keep its snapshot order on the wire")
	assert.Equal(t, 2, *deletedOrder)
	silentOrder, ok := byID[routePublicID(t, ctx, db, silentID)]
	require.True(t, ok)
	assert.Nil(t, silentOrder, "a route with no historical order must omit evaluation_order on the wire")
	assert.NotContains(t, rec.Body.String(), "evaluation_order\":0",
		"an unavailable historical order must be omitted, not zero-filled")
	assert.NotRegexp(t, `"evaluation_order":-`, rec.Body.String(),
		"no negative sentinel order may appear on the wire")
}

// An unresolved route identity fails the whole endpoint through its existing
// error contract instead of emitting an integer, a name, or a partial list.
func TestHandler_RouteIdentityMissingFailsClosed(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 1, 0, 0)
	runID := seedRun(t, ctx, db)
	routeID := seedRouteWithoutIdentity(t, ctx, db, "Ghost", 1)
	videoKey := seedVideo(t, ctx, db, "v1", "UC1", runID)
	seedDecision(t, ctx, db, videoKey, runID, routeID, "matched")

	handler := NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()

	cases := []struct {
		name     string
		path     string
		wantBody string
	}{
		{"channel detail", "/api/analytics/channels/UC1", `{"error":"failed to load channel stats"}`},
		{"run route stats", "/api/runs/" + strconv.FormatInt(runID, 10) + "/route-stats", `{"error":"failed to load route stats"}`},
		{"route match rates", "/api/analytics/route-match-rates", `{"error":"failed to load route match rates"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			assert.JSONEq(t, tc.wantBody, rec.Body.String())
		})
	}
}
