package stats

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/advisory"
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
	assert.Contains(t, body, `"Music"`)
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
		 VALUES (?, 'manual', 'completed', 5, 2, 3);`, now)
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
		 VALUES (?, 'manual', 'completed', 5, 2, 3);`, now)
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)

	routeID := seedRoute(t, ctx, db, "Music", 1)

	res, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'accepted');`, now, runID)
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
}
