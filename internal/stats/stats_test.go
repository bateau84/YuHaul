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

	channels, err := service.ChannelStats(ctx)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	assert.Equal(t, "UC1", channels[0].ChannelID)
	assert.Equal(t, 100, channels[0].TotalDiscovered)
	assert.Equal(t, 40, channels[0].TotalRouted)
	assert.Equal(t, 5, channels[0].TotalRetained)
	assert.InDelta(t, 0.4, channels[0].YieldRate, 0.001)
	assert.InDelta(t, 0.05, channels[0].UsefulnessRate, 0.001)
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
