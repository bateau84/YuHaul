package stats

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/advisory"
	"github.com/bateau84/YuHaul/internal/store"
)

func setupStatsTest(t *testing.T) (*sql.DB, *Store, *Service, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	statsStore := NewStore(db)
	service := NewService(statsStore, &fakeSettings{}, nil)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, statsStore, service, cleanup
}

type fakeSettings struct{}

func (f *fakeSettings) GetSetting(ctx context.Context, key string) (string, error) {
	return "", nil
}

func seedChannel(t *testing.T, ctx context.Context, db *sql.DB, channelID, name string, discovered, routed, retained int) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered, total_routed, total_retained)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
		  channel_name = excluded.channel_name,
		  total_discovered = excluded.total_discovered,
		  total_routed = excluded.total_routed,
		  total_retained = excluded.total_retained;`,
		channelID, name, time.Now().UTC(), time.Now().UTC(), discovered, routed, retained)
	require.NoError(t, err)
}

func seedRun(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, videos_discovered, videos_accepted, videos_rejected)
		 VALUES (?, 'manual', 'completed', 10, 3, 7);`,
		time.Now().UTC())
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func seedRoute(t *testing.T, ctx context.Context, db *sql.DB, name string, order int) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO route (name, evaluation_order, condition_composition)
		 VALUES (?, ?, 'AND');`, name, order)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func TestService_RunStats(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 10, 3, 1)
	runID := seedRun(t, ctx, db)

	stats, err := service.RunStats(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, runID, stats.RunID)
	assert.Equal(t, 10, stats.VideosDiscovered)
	assert.Equal(t, 3, stats.VideosAccepted)
	assert.Equal(t, 7, stats.VideosRejected)
	assert.NotNil(t, stats.RejectionByReason)
	assert.NotNil(t, stats.QuotaByOperation)
}

func TestService_RouteStats(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

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

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 1)
	assert.Equal(t, routeID, routeStats[0].RouteID)
	assert.Equal(t, 1, routeStats[0].VideosEvaluated)
	assert.Equal(t, 1, routeStats[0].VideosMatched)
}

func TestService_ChannelStats(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 100, 40, 5)

	var channelID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM channel WHERE channel_id = 'UC1';`).Scan(&channelID))
	advisoryStore := advisory.NewStore(db)
	service.WithAdvisories(advisoryStore)
	active, err := advisoryStore.CreateAdvisory(ctx, channelID, advisory.AdvisoryTypeLowUsefulness, map[string]any{
		"window_days": 90,
		"discovered":  100,
		"retained":    5,
	})
	require.NoError(t, err)
	dismissed, err := advisoryStore.CreateAdvisory(ctx, channelID, advisory.AdvisoryTypeHighDuplicateRate, map[string]any{
		"window_days": 90,
		"discovered":  100,
	})
	require.NoError(t, err)
	require.NoError(t, advisoryStore.DismissAdvisory(ctx, dismissed.ID))

	channels, err := service.ChannelStats(ctx)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	assert.Equal(t, "UC1", channels[0].ChannelID)
	assert.Equal(t, 100, channels[0].TotalDiscovered)
	assert.Equal(t, 40, channels[0].TotalRouted)
	assert.Equal(t, 5, channels[0].TotalRetained)
	assert.InDelta(t, 0.4, channels[0].Yield, 0.001)
	assert.InDelta(t, 0.05, channels[0].UsefulnessRate, 0.001)
	// Only the active advisory surfaces on the channel row; the dismissed one
	// does not.
	require.Len(t, channels[0].Advisories, 1)
	assert.Equal(t, active.ID, channels[0].Advisories[0].ID)
	assert.Equal(t, "low_usefulness", channels[0].Advisories[0].AdvisoryType)
	assert.Equal(t, "UC1", channels[0].Advisories[0].ChannelID)
}

func TestService_TemporalStats(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	now := time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, videos_discovered, videos_accepted, videos_rejected)
		 VALUES (?, 'manual', 'completed', 5, 2, 3);`, now)
	require.NoError(t, err)

	buckets, err := service.TemporalStats(ctx, now.Add(-24*time.Hour), now.Add(24*time.Hour), "day")
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, 5, buckets[0].VideosDiscovered)
	assert.Equal(t, 2, buckets[0].VideosAccepted)
}

func TestService_RetentionPlaylist(t *testing.T) {
	ctx := context.Background()
	_, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	playlist, err := service.DesignateRetentionPlaylist(ctx, "PL1", "My Rotation")
	require.NoError(t, err)
	assert.Equal(t, "PL1", playlist.PlaylistID)
	assert.Equal(t, "My Rotation", playlist.PlaylistName)
	assert.True(t, playlist.IsActive)

	playlists, err := service.RetentionPlaylists(ctx)
	require.NoError(t, err)
	require.Len(t, playlists, 1)

	err = service.RemoveRetentionPlaylist(ctx, playlist.ID)
	require.NoError(t, err)

	playlists, err = service.RetentionPlaylists(ctx)
	require.NoError(t, err)
	require.Len(t, playlists, 1)
	assert.False(t, playlists[0].IsActive)
}

func TestService_RetentionSignal_UserDrivenCounts(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 1, 1, 0)
	runID := seedRun(t, ctx, db)
	_, err := service.DesignateRetentionPlaylist(ctx, "PL1", "Rotation")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'accepted');`, time.Now().UTC(), runID)
	require.NoError(t, err)

	err = service.RecordRetentionSignal(ctx, "v1", "PL1", "user_driven")
	require.NoError(t, err)
	err = service.RefreshChannelRetained(ctx, "UC1")
	require.NoError(t, err)

	signals, err := service.RetentionSignals(ctx, "UC1")
	require.NoError(t, err)
	require.Len(t, signals, 1)
	assert.True(t, signals[0].IsRetentionSignal)
	assert.Equal(t, "user_driven", signals[0].MembershipSource)
}

func TestService_ChannelDetail(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 100, 40, 5)
	runID := seedRun(t, ctx, db)
	routeID := seedRoute(t, ctx, db, "Music", 1)

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

	_, err = db.ExecContext(ctx,
		`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason, rejection_reason)
		 VALUES (?, ?, ?, 1, 'matched', NULL, NULL);`, videoID, runID, routeID)
	require.NoError(t, err)

	// Provenance sources: a fingerprint first seen on another channel (making
	// v1 a relevance-credit duplicate) and a user-driven retention signal.
	_, err = db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, channel_id, source, is_duplicate_reference)
		 VALUES ('v0', 'fp0', 'UC2', 'api_discovery', 1);`)
	require.NoError(t, err)
	var fingerprintID int64
	err = db.QueryRowContext(ctx, `SELECT id FROM title_fingerprint WHERE video_id = 'v0';`).Scan(&fingerprintID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO duplicate_match (video_id, matched_fingerprint_id, similarity_score, match_type, threshold_used)
		 VALUES (?, ?, 0.97, 'fuzzy_title', 0.9);`, videoID, fingerprintID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO retention_playlist (playlist_id, playlist_name, designated_at, is_active)
		 VALUES ('PL1', 'Rotation', ?, 1);`, time.Now().UTC())
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO retention_signal (video_id, playlist_id, membership_source, is_retention_signal)
		 VALUES ('v1', 'PL1', 'user_driven', 1);`)
	require.NoError(t, err)

	var channelID int64
	err = db.QueryRowContext(ctx, `SELECT id FROM channel WHERE channel_id = ?;`, "UC1").Scan(&channelID)
	require.NoError(t, err)

	advisoryStore := advisory.NewStore(db)
	service.WithAdvisories(advisoryStore)
	_, err = advisoryStore.CreateAdvisory(ctx, channelID, advisory.AdvisoryTypeLowUsefulness, map[string]any{
		"window_days": 90,
		"discovered":  100,
		"retained":    5,
	})
	require.NoError(t, err)

	detail, err := service.ChannelDetail(ctx, "UC1")
	require.NoError(t, err)
	assert.Equal(t, "UC1", detail.ChannelID)
	assert.InDelta(t, 0.4, detail.Yield, 0.001)
	assert.NotNil(t, detail.DuplicateProvenance)
	assert.NotNil(t, detail.RetentionSignals)
	assert.Equal(t, []string{"Music"}, detail.MatchedRoutes)
	require.Len(t, detail.RecentVideos, 1)
	assert.Equal(t, "v1", detail.RecentVideos[0].VideoID)
	assert.Equal(t, "Test Title", detail.RecentVideos[0].Title)
	assert.NotNil(t, detail.RecentVideos[0].PublishedAt)
	require.Len(t, detail.Advisories, 1)
	assert.Equal(t, "low_usefulness", detail.Advisories[0].AdvisoryType)
	assert.Equal(t, "active", detail.Advisories[0].Status)
	assert.Equal(t, "UC1", detail.Advisories[0].ChannelID)
	require.Len(t, detail.RecentVideoProvenance, 1)
	require.NotNil(t, detail.RecentVideoProvenance[0].DuplicateCredit)
	assert.Equal(t, "relevance", *detail.RecentVideoProvenance[0].DuplicateCredit)
	require.NotNil(t, detail.RecentVideoProvenance[0].RetentionSource)
	assert.Equal(t, "user_driven", *detail.RecentVideoProvenance[0].RetentionSource)
}

func TestService_RecentVideoProvenance_DiscoveryCreditWins(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 2, 0, 0)
	runID := seedRun(t, ctx, db)

	// UC1 published v1 first (own fingerprint), UC2 published v2 matching it,
	// and UC1 also published v3 matching a fingerprint first seen on UC2.
	// v1 must show discovery credit (stronger), v3 relevance credit.
	_, err := db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, channel_id, source, is_duplicate_reference)
		 VALUES ('v1', 'fp1', 'UC1', 'api_discovery', 1);`)
	require.NoError(t, err)
	var fpV1 int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM title_fingerprint WHERE video_id = 'v1';`).Scan(&fpV1))
	_, err = db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, channel_id, source)
		 VALUES ('v0', 'fp0', 'UC2', 'api_discovery');`)
	require.NoError(t, err)
	var fpV0 int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM title_fingerprint WHERE video_id = 'v0';`).Scan(&fpV0))

	_, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'accepted');`, time.Now().UTC(), runID)
	require.NoError(t, err)
	resV3, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v3', 'UC1', ?, ?, 'completed', 'accepted');`, time.Now().UTC(), runID)
	require.NoError(t, err)
	videoV3, err := resV3.LastInsertId()
	require.NoError(t, err)
	resV2, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v2', 'UC2', ?, ?, 'completed', 'accepted');`, time.Now().UTC(), runID)
	require.NoError(t, err)
	videoV2, err := resV2.LastInsertId()
	require.NoError(t, err)

	// v2 (UC2) matched v1's fingerprint: v1 gets discovery credit.
	_, err = db.ExecContext(ctx,
		`INSERT INTO duplicate_match (video_id, matched_fingerprint_id, similarity_score, match_type, threshold_used)
		 VALUES (?, ?, 0.95, 'fuzzy_title', 0.9);`, videoV2, fpV1)
	require.NoError(t, err)
	// v3 (UC1) matched v0's fingerprint (UC2): v3 gets relevance credit.
	_, err = db.ExecContext(ctx,
		`INSERT INTO duplicate_match (video_id, matched_fingerprint_id, similarity_score, match_type, threshold_used)
		 VALUES (?, ?, 0.95, 'fuzzy_title', 0.9);`, videoV3, fpV0)
	require.NoError(t, err)

	detail, err := service.ChannelDetail(ctx, "UC1")
	require.NoError(t, err)

	byVideo := map[string]VideoProvenance{}
	for _, p := range detail.RecentVideoProvenance {
		byVideo[p.VideoID] = p
	}
	require.NotNil(t, byVideo["v1"].DuplicateCredit)
	assert.Equal(t, "discovery", *byVideo["v1"].DuplicateCredit)
	require.NotNil(t, byVideo["v3"].DuplicateCredit)
	assert.Equal(t, "relevance", *byVideo["v3"].DuplicateCredit)
}

func TestService_RouteMatchRates(t *testing.T) {
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

	rates, err := service.RouteMatchRates(ctx, now.Add(-24*time.Hour), now.Add(24*time.Hour), "day")
	require.NoError(t, err)
	require.Len(t, rates, 1)
	assert.Equal(t, routeID, rates[0].RouteID)
	assert.Equal(t, "Music", rates[0].RouteName)
	assert.Equal(t, 1, rates[0].Evaluated)
	assert.Equal(t, 1, rates[0].Matched)
	assert.InDelta(t, 1.0, rates[0].MatchRate, 0.001)
}

func TestService_DuplicateProvenance(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Original", 1, 1, 0)
	seedChannel(t, ctx, db, "UC2", "Cover", 1, 0, 0)
	runID := seedRun(t, ctx, db)

	_, err := db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, first_seen_at, is_duplicate_reference)
		 VALUES ('v1', 'abc', 'a b c', 'UC1', 'api_discovery', ?, 1);`, time.Now().UTC())
	require.NoError(t, err)
	var fpID int64
	err = db.QueryRowContext(ctx, `SELECT id FROM title_fingerprint WHERE video_id = 'v1';`).Scan(&fpID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v2', 'UC2', ?, ?, 'completed', 'rejected');`, time.Now().UTC(), runID)
	require.NoError(t, err)
	var videoID int64
	err = db.QueryRowContext(ctx, `SELECT id FROM video WHERE video_id = 'v2';`).Scan(&videoID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO duplicate_match (video_id, matched_fingerprint_id, similarity_score, match_type, threshold_used)
		 VALUES (?, ?, 0.8, 'fuzzy_title', 0.5);`, videoID, fpID)
	require.NoError(t, err)

	provenance, err := service.DuplicateProvenance(ctx, "UC2")
	require.NoError(t, err)
	require.Len(t, provenance, 1)
	assert.Equal(t, "v2", provenance[0].VideoID)
	assert.Equal(t, "UC1", provenance[0].DiscoveryCreditChannelID)
	assert.Equal(t, "UC2", provenance[0].ChannelID)
}
