package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/advisory"
	"github.com/bateau84/YuHaul/internal/state"
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
		time.Now().UTC().Format(state.RunTimestampLayout))
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// seedRunOfType creates a completed run of an arbitrary run type so tests can
// mix production (scheduled/manual) and non-production evidence.
func seedRunOfType(t *testing.T, ctx context.Context, db *sql.DB, runType string) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, videos_discovered, videos_accepted, videos_rejected)
		 VALUES (?, ?, 'completed', 10, 3, 7);`,
		time.Now().UTC().Format(state.RunTimestampLayout), runType)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// seedVideo inserts an internal video row and returns its integer key.
func seedVideo(t *testing.T, ctx context.Context, db *sql.DB, videoID, channelID string, runID int64) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES (?, ?, ?, ?, 'completed', 'accepted');`, videoID, channelID, time.Now().UTC(), runID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// seedDecision records one route decision row for a video in a run.
func seedDecision(t *testing.T, ctx context.Context, db *sql.DB, videoKey, runID, routeID int64, decision string) {
	t.Helper()
	seedDecisionWithOrder(t, ctx, db, videoKey, runID, routeID, 1, decision)
}

// seedDecisionWithOrder records one route decision row carrying the given
// snapshotted evaluation order, mirroring what the pipeline persists.
func seedDecisionWithOrder(t *testing.T, ctx context.Context, db *sql.DB, videoKey, runID, routeID int64, order int, decision string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision, skip_reason, rejection_reason)
		 VALUES (?, ?, ?, ?, ?, NULL, NULL);`, videoKey, runID, routeID, order, decision)
	require.NoError(t, err)
}

func seedRoute(t *testing.T, ctx context.Context, db *sql.DB, name string, order int) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO route (name, evaluation_order, condition_composition)
		 VALUES (?, ?, 'AND');`, name, order)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	// Assign the public UUID the same way startup does: route rows seeded
	// after store.Open have no route_identity row yet.
	require.NoError(t, store.BackfillRouteIdentities(ctx, db))
	return id
}

// seedRouteWithoutIdentity inserts a route row with no route_identity mapping,
// reproducing the integrity gap the API must fail closed on instead of
// substituting an integer or a name.
func seedRouteWithoutIdentity(t *testing.T, ctx context.Context, db *sql.DB, name string, order int) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO route (name, evaluation_order, condition_composition)
		 VALUES (?, ?, 'AND');`, name, order)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// routePublicID reads the canonical public UUID assigned to a route key.
func routePublicID(t *testing.T, ctx context.Context, db *sql.DB, routeID int64) string {
	t.Helper()
	var publicID string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT public_id FROM route_identity WHERE route_id = ?;`, routeID).Scan(&publicID))
	return publicID
}

// snapshotRouteSpec describes one route entry in a seeded run snapshot: the
// internal route key, the label the run evaluated, and that run's evaluation
// order.
type snapshotRouteSpec struct {
	RouteKey int64
	Name     string
	Order    int
}

// seedRunWithSnapshotRoutes creates a completed manual run bound to a
// configuration snapshot carrying the given route labels and evaluation
// orders, so run-scoped statistics can be checked against the exact
// historical context the run evaluated.
func seedRunWithSnapshotRoutes(t *testing.T, ctx context.Context, db *sql.DB, specs []snapshotRouteSpec) int64 {
	t.Helper()
	routes := make([]string, 0, len(specs))
	for _, spec := range specs {
		routes = append(routes, fmt.Sprintf(
			`{"id":%d,"name":%q,"evaluation_order":%d,"is_final":false,"is_enabled":true,"condition_composition":"AND","conditions":[],"actions":[],"exclusions":[]}`,
			spec.RouteKey, spec.Name, spec.Order))
	}
	snapshot := fmt.Sprintf(`{"routes":[%s],"duplicate_threshold":0.5,"duplicate_behavior":"skip"}`,
		strings.Join(routes, ","))

	res, err := db.ExecContext(ctx,
		`INSERT INTO config_version (created_at, snapshot, change_description)
		 VALUES (?, ?, 'seed');`, time.Now().UTC(), snapshot)
	require.NoError(t, err)
	versionID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, config_version_id, videos_discovered, videos_accepted, videos_rejected)
		 VALUES (?, 'manual', 'completed', ?, 10, 3, 7);`,
		time.Now().UTC().Format(state.RunTimestampLayout), versionID)
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)
	return runID
}

// seedRunWithSnapshot creates a completed manual run bound to a configuration
// snapshot carrying the given route labels, so run-scoped statistics can be
// checked against the labels the run actually evaluated.
func seedRunWithSnapshot(t *testing.T, ctx context.Context, db *sql.DB, routeLabels map[int64]string) int64 {
	t.Helper()
	specs := make([]snapshotRouteSpec, 0, len(routeLabels))
	for id, name := range routeLabels {
		specs = append(specs, snapshotRouteSpec{RouteKey: id, Name: name, Order: 1})
	}
	return seedRunWithSnapshotRoutes(t, ctx, db, specs)
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
	// Public identity is the registry UUID, never the internal integer key.
	assert.Equal(t, routePublicID(t, ctx, db, routeID), routeStats[0].RouteID)
	assert.Equal(t, "Music", routeStats[0].RouteName)
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
		 VALUES (?, 'manual', 'completed', 5, 2, 3);`, now.Format(state.RunTimestampLayout))
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
	// ADR-014: relevance credit requires the matched reference's native video
	// observation; v0 was observed before v1, so v1 remains the relevance side.
	_, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v0', 'UC2', ?, NULL, 'completed', 'rejected');`, time.Now().UTC().Add(-time.Hour))
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
	assert.Equal(t, []MatchedRoute{{
		RouteID:    routePublicID(t, ctx, db, routeID),
		Name:       "Music",
		MatchCount: 1,
	}}, detail.MatchedRoutes)
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

	// ADR-014: credits follow the two native observations' chronology, so the
	// seeds use explicit, strictly increasing discovery times: v1 < v2 gives
	// v1 discovery, and v0 < v3 gives v3 relevance.
	base := time.Now().UTC()
	_, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'accepted');`, base, runID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v0', 'UC2', ?, ?, 'completed', 'accepted');`, base.Add(time.Minute), runID)
	require.NoError(t, err)
	resV3, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v3', 'UC1', ?, ?, 'completed', 'accepted');`, base.Add(3*time.Minute), runID)
	require.NoError(t, err)
	videoV3, err := resV3.LastInsertId()
	require.NoError(t, err)
	resV2, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v2', 'UC2', ?, ?, 'completed', 'accepted');`, base.Add(2*time.Minute), runID)
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
		 VALUES (?, 'manual', 'completed', 5, 2, 3);`, now.Format(state.RunTimestampLayout))
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
	assert.Equal(t, routePublicID(t, ctx, db, routeID), rates[0].RouteID)
	assert.Equal(t, "Music", rates[0].RouteName)
	assert.Equal(t, 1, rates[0].Evaluated)
	assert.Equal(t, 1, rates[0].Matched)
	assert.InDelta(t, 1.0, rates[0].MatchRate, 0.001)
}

func TestService_DuplicateProvenance(t *testing.T) {
	ctx := context.Background()
	db, statsStore, service, cleanup := setupStatsTest(t)
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

	// ADR-014: discovery credit comes from the reference's native video
	// observation, not the fingerprint's channel association. v1 was observed
	// natively before v2.
	_, err = db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES ('v1', 'UC1', ?, ?, 'completed', 'rejected');`, time.Now().UTC().Add(-time.Hour), runID)
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
	assert.Equal(t, "Original", provenance[0].DiscoveryCreditChannelName)
	assert.Equal(t, "UC2", provenance[0].ChannelID)

	// The aggregate breakdown follows the same pair chronology.
	breakdown, err := statsStore.DuplicateProvenanceBreakdown(ctx, "UC1")
	require.NoError(t, err)
	assert.Equal(t, DuplicateProvenanceBreakdown{DiscoveryCredit: 1}, breakdown)
	breakdown, err = statsStore.DuplicateProvenanceBreakdown(ctx, "UC2")
	require.NoError(t, err)
	assert.Equal(t, DuplicateProvenanceBreakdown{RelevanceCredit: 1}, breakdown)
}

// seedVideoAt inserts a video row with an explicit discovered_at value. A
// time.Time binds driver-encoded; a string binds raw stored text, letting
// chronology tests reproduce both supported encodings and malformed values.
func seedVideoAt(t *testing.T, ctx context.Context, db *sql.DB, videoID, channelID string, discoveredAt any, runID int64) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
		 VALUES (?, ?, ?, ?, 'completed', 'rejected');`, videoID, channelID, discoveredAt, runID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// seedFingerprintRow inserts a title_fingerprint row and returns its key.
// channelID may be nil to model an import with no channel association.
func seedFingerprintRow(t *testing.T, ctx context.Context, db *sql.DB, videoID string, channelID any, source string, isReference bool) int64 {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, channel_id, source, is_duplicate_reference)
		 VALUES (?, ?, ?, ?, ?);`, videoID, "fp-"+videoID, channelID, source, isReference)
	require.NoError(t, err)
	var id int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM title_fingerprint WHERE video_id = ?;`, videoID).Scan(&id))
	return id
}

// seedMatchRow records a duplicate_match between an evaluated video and a
// matched fingerprint.
func seedMatchRow(t *testing.T, ctx context.Context, db *sql.DB, videoKey, fingerprintID int64) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO duplicate_match (video_id, matched_fingerprint_id, similarity_score, match_type, threshold_used)
		 VALUES (?, ?, 0.95, 'fuzzy_title', 0.9);`, videoKey, fingerprintID)
	require.NoError(t, err)
}

// videoDuplicateCredit returns a channel video's duplicate-credit tag, or ""
// when the video holds no chronology-based credit.
func videoDuplicateCredit(t *testing.T, ctx context.Context, store *Store, channelID, videoID string) string {
	t.Helper()
	rows, err := store.ListRecentVideoProvenance(ctx, channelID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.VideoID == videoID && r.DuplicateCredit != nil {
			return *r.DuplicateCredit
		}
	}
	return ""
}

// An imported fingerprint's channel association is matching-eligibility
// evidence only; discovery credit comes from the native video observations
// (ADR-014 §3, BR-010 AC6).
func TestDuplicateProvenance_ImportedAssociationEarnsNoDiscoveryCredit(t *testing.T) {
	ctx := context.Background()
	db, statsStore, _, cleanup := setupStatsTest(t)
	defer cleanup()
	runID := seedRun(t, ctx, db)
	seedChannel(t, ctx, db, "UC-native", "Native", 1, 0, 0)
	seedChannel(t, ctx, db, "UC-cover", "Cover", 1, 0, 0)
	seedChannel(t, ctx, db, "UC-import", "Imported", 1, 0, 0)

	// Import v1 with an association-only channel, so it stays an eligible
	// reference. Native discovery later observes v1 from UC-native, and
	// UC-cover's v2 matches it.
	fpV1 := seedFingerprintRow(t, ctx, db, "v1", "UC-import", "user_import", true)
	seedVideoAt(t, ctx, db, "v1", "UC-native", time.Now().UTC().Add(-time.Hour), runID)
	v2 := seedVideoAt(t, ctx, db, "v2", "UC-cover", time.Now().UTC(), runID)
	seedMatchRow(t, ctx, db, v2, fpV1)

	for channelID, want := range map[string]DuplicateProvenanceBreakdown{
		"UC-import": {},
		"UC-native": {DiscoveryCredit: 1},
		"UC-cover":  {RelevanceCredit: 1},
	} {
		got, err := statsStore.DuplicateProvenanceBreakdown(ctx, channelID)
		require.NoError(t, err)
		assert.Equal(t, want, got, "breakdown for %s", channelID)
	}

	rows, err := statsStore.ListDuplicateProvenance(ctx, "UC-cover")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "UC-native", rows[0].DiscoveryCreditChannelID,
		"the imported channel association must not receive discovery credit")
	assert.Equal(t, "Native", rows[0].DiscoveryCreditChannelName)

	importedRows, err := statsStore.ListDuplicateProvenance(ctx, "UC-import")
	require.NoError(t, err)
	assert.Empty(t, importedRows)

	assert.Equal(t, "relevance", videoDuplicateCredit(t, ctx, statsStore, "UC-cover", "v2"))
}

// An import with no channel association and a same-ID import retry hold the
// same no-credit boundary: only native video chronology earns credit.
func TestDuplicateProvenance_ImportWithoutChannelAssociationEarnsNoCredit(t *testing.T) {
	ctx := context.Background()
	db, statsStore, _, cleanup := setupStatsTest(t)
	defer cleanup()
	runID := seedRun(t, ctx, db)
	seedChannel(t, ctx, db, "UC-cover", "Cover", 1, 0, 0)

	fpV1 := seedFingerprintRow(t, ctx, db, "v1", nil, "user_import", true)
	v2 := seedVideoAt(t, ctx, db, "v2", "UC-cover", time.Now().UTC(), runID)
	seedMatchRow(t, ctx, db, v2, fpV1)

	got, err := statsStore.DuplicateProvenanceBreakdown(ctx, "UC-cover")
	require.NoError(t, err)
	assert.Equal(t, DuplicateProvenanceBreakdown{}, got,
		"an association-less import reference earns no chronology credit")

	rows, err := statsStore.ListDuplicateProvenance(ctx, "UC-cover")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Empty(t, rows[0].DiscoveryCreditChannelID)
	assert.Empty(t, rows[0].DiscoveryCreditChannelName)
}

// Discovery and relevance follow the pair's native chronology in both match
// directions: the earlier observation is discovery regardless of which side
// the detector returned as the reference (ADR-014 §5).
func TestDuplicateProvenance_BothOrderDirections(t *testing.T) {
	ctx := context.Background()
	db, statsStore, _, cleanup := setupStatsTest(t)
	defer cleanup()
	runID := seedRun(t, ctx, db)
	for _, ch := range []struct{ id, name string }{
		{"UC-A", "Alpha"}, {"UC-B", "Beta"}, {"UC-C", "Gamma"}, {"UC-D", "Delta"},
	} {
		seedChannel(t, ctx, db, ch.id, ch.name, 1, 0, 0)
	}
	base := time.Now().UTC()

	// Pair A: the reference v1 (UC-A) was observed first; UC-B's v2 matches it.
	fpV1 := seedFingerprintRow(t, ctx, db, "v1", "UC-A", "api_discovery", true)
	seedVideoAt(t, ctx, db, "v1", "UC-A", base, runID)
	v2 := seedVideoAt(t, ctx, db, "v2", "UC-B", base.Add(time.Hour), runID)
	seedMatchRow(t, ctx, db, v2, fpV1)

	// Pair B (converse): the evaluated v3 (UC-C) predates the matched
	// reference v4 (UC-D), so chronology — not match direction — flips roles.
	fpV4 := seedFingerprintRow(t, ctx, db, "v4", "UC-D", "api_discovery", true)
	seedVideoAt(t, ctx, db, "v4", "UC-D", base.Add(3*time.Hour), runID)
	v3 := seedVideoAt(t, ctx, db, "v3", "UC-C", base.Add(2*time.Hour), runID)
	seedMatchRow(t, ctx, db, v3, fpV4)

	for channelID, want := range map[string]DuplicateProvenanceBreakdown{
		"UC-A": {DiscoveryCredit: 1},
		"UC-B": {RelevanceCredit: 1},
		"UC-C": {DiscoveryCredit: 1},
		"UC-D": {RelevanceCredit: 1},
	} {
		got, err := statsStore.DuplicateProvenanceBreakdown(ctx, channelID)
		require.NoError(t, err)
		assert.Equal(t, want, got, "breakdown for %s", channelID)
	}

	// Per-video tags attach to the chronologically earlier/later video.
	assert.Equal(t, "discovery", videoDuplicateCredit(t, ctx, statsStore, "UC-A", "v1"))
	assert.Equal(t, "relevance", videoDuplicateCredit(t, ctx, statsStore, "UC-B", "v2"))
	assert.Equal(t, "discovery", videoDuplicateCredit(t, ctx, statsStore, "UC-C", "v3"))
	assert.Equal(t, "relevance", videoDuplicateCredit(t, ctx, statsStore, "UC-D", "v4"))

	// Detailed rows stay anchored at the evaluated video and name the
	// chronology-derived discovery channel in both directions.
	rowsB, err := statsStore.ListDuplicateProvenance(ctx, "UC-B")
	require.NoError(t, err)
	require.Len(t, rowsB, 1)
	assert.Equal(t, "UC-A", rowsB[0].DiscoveryCreditChannelID)
	assert.Equal(t, "Alpha", rowsB[0].DiscoveryCreditChannelName)
	rowsC, err := statsStore.ListDuplicateProvenance(ctx, "UC-C")
	require.NoError(t, err)
	require.Len(t, rowsC, 1)
	assert.Equal(t, "UC-C", rowsC[0].DiscoveryCreditChannelID,
		"the evaluated video is the earlier observation, so its own channel is the discovery source")
	assert.Equal(t, "Gamma", rowsC[0].DiscoveryCreditChannelName)
}

// Without orderable native chronology — missing reference record, equal
// times, or unparseable values — neither side earns discovery or relevance
// credit, and the fingerprint association never stands in (ADR-014 §3, §6).
func TestDuplicateProvenance_NoOrderableChronology(t *testing.T) {
	base := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		seed func(t *testing.T, ctx context.Context, db *sql.DB, runID int64)
	}{
		{
			name: "reference has no native video row",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB, runID int64) {
				fp := seedFingerprintRow(t, ctx, db, "v1", "UC-import", "user_import", true)
				v2 := seedVideoAt(t, ctx, db, "v2", "UC-B", base, runID)
				seedMatchRow(t, ctx, db, v2, fp)
			},
		},
		{
			name: "equal discovery times",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB, runID int64) {
				fp := seedFingerprintRow(t, ctx, db, "v1", "UC-A", "api_discovery", true)
				seedVideoAt(t, ctx, db, "v1", "UC-A", base, runID)
				v2 := seedVideoAt(t, ctx, db, "v2", "UC-B", base, runID)
				seedMatchRow(t, ctx, db, v2, fp)
			},
		},
		{
			name: "unparseable evaluated time",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB, runID int64) {
				fp := seedFingerprintRow(t, ctx, db, "v1", "UC-A", "api_discovery", true)
				seedVideoAt(t, ctx, db, "v1", "UC-A", base.Format(time.RFC3339Nano), runID)
				v2 := seedVideoAt(t, ctx, db, "v2", "UC-B", "not-a-timestamp", runID)
				seedMatchRow(t, ctx, db, v2, fp)
			},
		},
		{
			name: "unparseable reference time",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB, runID int64) {
				fp := seedFingerprintRow(t, ctx, db, "v1", "UC-A", "api_discovery", true)
				seedVideoAt(t, ctx, db, "v1", "UC-A", "still not a timestamp", runID)
				v2 := seedVideoAt(t, ctx, db, "v2", "UC-B", base.Format(time.RFC3339Nano), runID)
				seedMatchRow(t, ctx, db, v2, fp)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, statsStore, _, cleanup := setupStatsTest(t)
			defer cleanup()
			runID := seedRun(t, ctx, db)
			seedChannel(t, ctx, db, "UC-A", "Alpha", 1, 0, 0)
			seedChannel(t, ctx, db, "UC-B", "Beta", 1, 0, 0)
			tc.seed(t, ctx, db, runID)

			for _, channelID := range []string{"UC-A", "UC-B", "UC-import"} {
				got, err := statsStore.DuplicateProvenanceBreakdown(ctx, channelID)
				require.NoError(t, err)
				assert.Equal(t, DuplicateProvenanceBreakdown{}, got, "breakdown for %s", channelID)
			}
			assert.Equal(t, "", videoDuplicateCredit(t, ctx, statsStore, "UC-B", "v2"))

			rows, err := statsStore.ListDuplicateProvenance(ctx, "UC-B")
			require.NoError(t, err)
			require.Len(t, rows, 1, "the match row remains listed with unknown attribution")
			assert.Empty(t, rows[0].DiscoveryCreditChannelID)
			assert.Empty(t, rows[0].DiscoveryCreditChannelName)
		})
	}
}

// A same-ID match is a self-reference, not a duplicate event: legacy rows
// earn neither discovery nor relevance credit in any projection, while
// ordinary distinct pairs on the same channel still do (ADR-014 §4).
func TestDuplicateProvenance_LegacySameIDSelfMatchExcluded(t *testing.T) {
	ctx := context.Background()
	db, statsStore, _, cleanup := setupStatsTest(t)
	defer cleanup()
	runID := seedRun(t, ctx, db)
	seedChannel(t, ctx, db, "UC-A", "Alpha", 1, 0, 0)
	seedChannel(t, ctx, db, "UC-B", "Beta", 1, 0, 0)
	base := time.Now().UTC()

	// Legacy self-match: v1's own fingerprint matched v1 itself.
	fpV1 := seedFingerprintRow(t, ctx, db, "v1", "UC-A", "api_discovery", true)
	v1 := seedVideoAt(t, ctx, db, "v1", "UC-A", base, runID)
	seedMatchRow(t, ctx, db, v1, fpV1)

	// A genuine distinct pair still earns its credits.
	fpV0 := seedFingerprintRow(t, ctx, db, "v0", "UC-A", "api_discovery", true)
	seedVideoAt(t, ctx, db, "v0", "UC-A", base.Add(-time.Hour), runID)
	v2 := seedVideoAt(t, ctx, db, "v2", "UC-B", base.Add(time.Hour), runID)
	seedMatchRow(t, ctx, db, v2, fpV0)

	gotA, err := statsStore.DuplicateProvenanceBreakdown(ctx, "UC-A")
	require.NoError(t, err)
	assert.Equal(t, DuplicateProvenanceBreakdown{DiscoveryCredit: 1}, gotA,
		"the self-match must not add discovery or relevance credit")
	gotB, err := statsStore.DuplicateProvenanceBreakdown(ctx, "UC-B")
	require.NoError(t, err)
	assert.Equal(t, DuplicateProvenanceBreakdown{RelevanceCredit: 1}, gotB)

	rowsA, err := statsStore.ListDuplicateProvenance(ctx, "UC-A")
	require.NoError(t, err)
	assert.Empty(t, rowsA, "a self-match row is not a duplicate-provenance event")

	assert.Equal(t, "", videoDuplicateCredit(t, ctx, statsStore, "UC-A", "v1"))
	assert.Equal(t, "discovery", videoDuplicateCredit(t, ctx, statsStore, "UC-A", "v0"))
	assert.Equal(t, "relevance", videoDuplicateCredit(t, ctx, statsStore, "UC-B", "v2"))
}

// Driver-encoded and RFC3339 stored timestamps must compare as chronological
// instants: lexicographic text order disagrees with chronology for these
// pairs, so a text-ordering implementation would award the wrong channels
// (ADR-014 §6).
func TestDuplicateProvenance_TimestampEncodingOrdering(t *testing.T) {
	ctx := context.Background()
	db, statsStore, _, cleanup := setupStatsTest(t)
	defer cleanup()
	runID := seedRun(t, ctx, db)
	for _, ch := range []struct{ id, name string }{
		{"UC-A", "Alpha"}, {"UC-B", "Beta"}, {"UC-C", "Gamma"}, {"UC-D", "Delta"},
	} {
		seedChannel(t, ctx, db, ch.id, ch.name, 1, 0, 0)
	}

	// Reference v1 stored as RFC3339 text (09:00Z); evaluated v2 stored in
	// the legacy driver encoding (10:00Z) written as raw bytes so the driver's
	// own time normalization cannot rewrite it. Lexical text order would
	// award UC-B because "2026-01-02 " sorts before "2026-01-02T".
	fpV1 := seedFingerprintRow(t, ctx, db, "v1", "UC-A", "api_discovery", true)
	seedVideoAt(t, ctx, db, "v1", "UC-A", "2026-01-02T09:00:00Z", runID)
	v2 := seedVideoAt(t, ctx, db, "v2", "UC-B", []byte("2026-01-02 10:00:00 +0000 UTC"), runID)
	seedMatchRow(t, ctx, db, v2, fpV1)

	// Converse with swapped encodings: evaluated v3 RFC3339 (11:00Z),
	// reference v4 legacy driver encoding (12:00Z).
	fpV4 := seedFingerprintRow(t, ctx, db, "v4", "UC-D", "api_discovery", true)
	seedVideoAt(t, ctx, db, "v4", "UC-D", []byte("2026-01-02 12:00:00 +0000 UTC"), runID)
	v3 := seedVideoAt(t, ctx, db, "v3", "UC-C", "2026-01-02T11:00:00Z", runID)
	seedMatchRow(t, ctx, db, v3, fpV4)

	for channelID, want := range map[string]DuplicateProvenanceBreakdown{
		"UC-A": {DiscoveryCredit: 1},
		"UC-B": {RelevanceCredit: 1},
		"UC-C": {DiscoveryCredit: 1},
		"UC-D": {RelevanceCredit: 1},
	} {
		got, err := statsStore.DuplicateProvenanceBreakdown(ctx, channelID)
		require.NoError(t, err)
		assert.Equal(t, want, got, "breakdown for %s", channelID)
	}
}

// canonicalUUIDv4 matches the ARS-004 public route identity format: lowercase,
// hyphenated, version 4, RFC variant. It is the only acceptable wire form of a
// route identity field.
var canonicalUUIDv4 = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Two routes may share a display name while staying separate identities, and
// only production evidence contributes distinct-video counts (ARS-001).
func TestService_ChannelDetail_MatchedRoutes_DuplicateNamesProductionOnly(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 6, 0, 0)

	// Same name, distinct UUIDs, plus a soft-deleted route that still has
	// production evidence and a route matched only by non-production runs.
	routeAmbientOne := seedRoute(t, ctx, db, "Ambient", 1)
	routeAmbientTwo := seedRoute(t, ctx, db, "Ambient", 2)
	routeDeleted := seedRoute(t, ctx, db, "Deep Cut", 3)
	routeDryOnly := seedRoute(t, ctx, db, "Dry Run Only", 4)
	_, err := db.ExecContext(ctx, `UPDATE route SET deleted_at = ? WHERE id = ?;`,
		time.Now().UTC(), routeDeleted)
	require.NoError(t, err)

	prodOne := seedRunOfType(t, ctx, db, "manual")
	prodTwo := seedRunOfType(t, ctx, db, "scheduled")
	v1 := seedVideo(t, ctx, db, "v1", "UC1", prodOne)
	v2 := seedVideo(t, ctx, db, "v2", "UC1", prodOne)
	v3 := seedVideo(t, ctx, db, "v3", "UC1", prodOne)

	// v1 matched Ambient-1 in both production runs: it contributes once.
	seedDecision(t, ctx, db, v1, prodOne, routeAmbientOne, "matched")
	seedDecision(t, ctx, db, v2, prodOne, routeAmbientOne, "matched")
	seedDecision(t, ctx, db, v1, prodOne, routeAmbientTwo, "matched")
	seedDecision(t, ctx, db, v1, prodTwo, routeAmbientOne, "matched")
	seedDecision(t, ctx, db, v2, prodTwo, routeDeleted, "matched")

	// Dry-run, replay, and backtest evidence never reaches this projection.
	for _, runType := range []string{"dry_run", "replay", "backtest"} {
		runID := seedRunOfType(t, ctx, db, runType)
		seedDecision(t, ctx, db, v3, runID, routeAmbientOne, "matched")
		seedDecision(t, ctx, db, v3, runID, routeDryOnly, "matched")
	}

	detail, err := service.ChannelDetail(ctx, "UC1")
	require.NoError(t, err)

	ambientOneID := routePublicID(t, ctx, db, routeAmbientOne)
	ambientTwoID := routePublicID(t, ctx, db, routeAmbientTwo)
	deletedID := routePublicID(t, ctx, db, routeDeleted)
	dryOnlyID := routePublicID(t, ctx, db, routeDryOnly)

	matched := detail.MatchedRoutes
	require.Len(t, matched, 3, "same-named and soft-deleted routes stay separate entries")

	byID := make(map[string]MatchedRoute, len(matched))
	for _, m := range matched {
		byID[m.RouteID] = m
	}
	require.Len(t, byID, 3, "each entry must carry a distinct UUID")

	require.Contains(t, byID, ambientOneID)
	assert.Equal(t, "Ambient", byID[ambientOneID].Name)
	assert.Equal(t, 2, byID[ambientOneID].MatchCount)
	require.Contains(t, byID, ambientTwoID)
	assert.Equal(t, "Ambient", byID[ambientTwoID].Name)
	assert.Equal(t, 1, byID[ambientTwoID].MatchCount)
	require.Contains(t, byID, deletedID)
	assert.Equal(t, "Deep Cut", byID[deletedID].Name)
	assert.Equal(t, 1, byID[deletedID].MatchCount)
	assert.NotContains(t, byID, dryOnlyID, "production-only: non-production matches must not appear")

	// Order: name ascending, then canonical UUID ascending for the tie.
	assert.Equal(t, "Ambient", matched[0].Name)
	assert.Equal(t, "Ambient", matched[1].Name)
	assert.Equal(t, "Deep Cut", matched[2].Name)
	assert.Less(t, matched[0].RouteID, matched[1].RouteID, "same-named routes order by UUID")

	for _, m := range matched {
		assert.Regexp(t, canonicalUUIDv4, m.RouteID, "route_id must be a canonical UUIDv4")
	}
}

// A channel with no production match evidence exposes an empty array, not null.
func TestService_ChannelDetail_MatchedRoutesEmpty(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 1, 0, 0)

	detail, err := service.ChannelDetail(ctx, "UC1")
	require.NoError(t, err)
	require.NotNil(t, detail.MatchedRoutes, "matched_routes must be an array, not null")
	assert.Empty(t, detail.MatchedRoutes)
}

// Run-scoped statistics label routes from the run's immutable snapshot, while
// cross-run analytics show the current persisted label for the same UUID.
func TestService_RouteStats_UsesRunSnapshotLabel(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	routeID := seedRoute(t, ctx, db, "Ambient", 1)
	runID := seedRunWithSnapshot(t, ctx, db, map[int64]string{routeID: "Ambient"})
	videoKey := seedVideo(t, ctx, db, "v1", "UC1", runID)
	seedDecision(t, ctx, db, videoKey, runID, routeID, "matched")

	// The route is renamed after the run evaluated it.
	_, err := db.ExecContext(ctx, `UPDATE route SET name = 'Ambient Renamed' WHERE id = ?;`, routeID)
	require.NoError(t, err)
	publicID := routePublicID(t, ctx, db, routeID)

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 1)
	assert.Equal(t, publicID, routeStats[0].RouteID)
	assert.Equal(t, "Ambient", routeStats[0].RouteName, "run-scoped label comes from the run snapshot")
	assert.Equal(t, 1, routeStats[0].VideosMatched)

	now := time.Now().UTC()
	rates, err := service.RouteMatchRates(ctx, now.Add(-24*time.Hour), now.Add(24*time.Hour), "day")
	require.NoError(t, err)
	require.Len(t, rates, 1)
	assert.Equal(t, publicID, rates[0].RouteID)
	assert.Equal(t, "Ambient Renamed", rates[0].RouteName, "cross-run analytics use the current label")
}

// A route key without a registry mapping fails every identity-bearing stats
// read instead of emitting an integer, a name, or a partial result.
func TestService_RouteIdentityMissingFailsClosed(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One", 1, 0, 0)
	runID := seedRun(t, ctx, db)
	routeID := seedRouteWithoutIdentity(t, ctx, db, "Ghost", 1)
	videoKey := seedVideo(t, ctx, db, "v1", "UC1", runID)
	seedDecision(t, ctx, db, videoKey, runID, routeID, "matched")

	_, err := service.RouteStats(ctx, runID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing route identity mapping")

	now := time.Now().UTC()
	_, err = service.RouteMatchRates(ctx, now.Add(-24*time.Hour), now.Add(24*time.Hour), "day")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing route identity mapping")

	_, err = service.ChannelDetail(ctx, "UC1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing route identity mapping")
}

// An undecodable stored snapshot is OC-003 incomplete configuration context,
// not a failed read: route statistics keep serving with the current persisted
// label for that run (matching the run-video decision projection) while the
// route identity still comes from the registry.
func TestService_RouteStats_UndecodableSnapshotFallsBack(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	routeID := seedRoute(t, ctx, db, "Ambient", 1)
	res, err := db.ExecContext(ctx,
		`INSERT INTO config_version (created_at, snapshot, change_description)
		 VALUES (?, 'not-json', 'seed');`, time.Now().UTC())
	require.NoError(t, err)
	versionID, err := res.LastInsertId()
	require.NoError(t, err)
	res, err = db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, config_version_id)
		 VALUES (?, 'manual', 'completed', ?);`,
		time.Now().UTC().Format(state.RunTimestampLayout), versionID)
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)

	videoKey := seedVideo(t, ctx, db, "v1", "UC1", runID)
	seedDecision(t, ctx, db, videoKey, runID, routeID, "matched")

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err, "incomplete snapshot context must not fail run statistics")
	require.Len(t, routeStats, 1)
	assert.Equal(t, routePublicID(t, ctx, db, routeID), routeStats[0].RouteID)
	assert.Equal(t, "Ambient", routeStats[0].RouteName)
}

// --- Run-scoped evaluation_order projection (ARS-001) ---

// A soft-deleted route keeps the evaluation order its run actually saw. The
// ADR-003 delete-time sentinel (-route_id) on the current row is internal
// bookkeeping and must never surface as the run's order.
func TestService_RouteStats_SoftDeletedRouteUsesSnapshotOrder(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	routeID := seedRoute(t, ctx, db, "Deep Cut", 2)
	runID := seedRunWithSnapshotRoutes(t, ctx, db, []snapshotRouteSpec{
		{RouteKey: routeID, Name: "Deep Cut", Order: 2},
	})
	videoKey := seedVideo(t, ctx, db, "v1", "UC1", runID)
	seedDecisionWithOrder(t, ctx, db, videoKey, runID, routeID, 2, "matched")

	// ADR-003 DeleteRoute: soft delete moves the row to the -id sentinel.
	_, err := db.ExecContext(ctx,
		`UPDATE route SET deleted_at = ?, evaluation_order = -id WHERE id = ?;`,
		time.Now().UTC(), routeID)
	require.NoError(t, err)

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 1)
	assert.Equal(t, routePublicID(t, ctx, db, routeID), routeStats[0].RouteID)
	require.NotNil(t, routeStats[0].EvaluationOrder,
		"the run snapshot must supply the historical order for a soft-deleted route")
	assert.Equal(t, 2, *routeStats[0].EvaluationOrder,
		"the snapshot order is the run's order, not the -id sentinel on the current row")
}

// A proposal-only identity anchor (ADR-012) carries the same -id sentinel on
// its current row. Route stats for the proposal run must project the order
// the proposal evaluated, never the anchor sentinel.
func TestService_RouteStats_ProposalAnchorUsesSnapshotOrder(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	anchorKey := seedRoute(t, ctx, db, "Proposed", 1)
	runID := seedRunWithSnapshotRoutes(t, ctx, db, []snapshotRouteSpec{
		{RouteKey: anchorKey, Name: "Proposed", Order: 1},
	})
	videoKey := seedVideo(t, ctx, db, "v1", "UC1", runID)
	seedDecisionWithOrder(t, ctx, db, videoKey, runID, anchorKey, 1, "matched")

	// ADR-012 anchor shape: inactive, soft-deleted, evaluation_order = -id.
	_, err := db.ExecContext(ctx,
		`UPDATE route SET deleted_at = ?, is_enabled = 0, evaluation_order = -id WHERE id = ?;`,
		time.Now().UTC(), anchorKey)
	require.NoError(t, err)

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 1)
	require.NotNil(t, routeStats[0].EvaluationOrder,
		"the proposal snapshot must supply the historical order for an anchor route")
	assert.Equal(t, 1, *routeStats[0].EvaluationOrder)
	assert.Greater(t, *routeStats[0].EvaluationOrder, 0,
		"a proposal-only anchor's -id sentinel must never surface as evaluation order")
}

// When no usable snapshot order exists, one consistent positive
// route_decision.evaluation_order for the run is the fallback evidence.
func TestService_RouteStats_DecisionOrderFallback(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	routeID := seedRoute(t, ctx, db, "Legacy", 5)
	runID := seedRun(t, ctx, db) // no linked ConfigVersion snapshot
	v1 := seedVideo(t, ctx, db, "v1", "UC1", runID)
	v2 := seedVideo(t, ctx, db, "v2", "UC1", runID)
	seedDecisionWithOrder(t, ctx, db, v1, runID, routeID, 3, "matched")
	seedDecisionWithOrder(t, ctx, db, v2, runID, routeID, 3, "not_matched")

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 1)
	require.NotNil(t, routeStats[0].EvaluationOrder,
		"agreeing positive decision orders must backfill the run's order")
	assert.Equal(t, 3, *routeStats[0].EvaluationOrder)
}

// The snapshot wins when it carries a usable order even if the persisted
// decision rows disagree with it: the immutable snapshot is the primary
// historical source.
func TestService_RouteStats_SnapshotOrderWinsOverDecisions(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	routeID := seedRoute(t, ctx, db, "Ambient", 7)
	runID := seedRunWithSnapshotRoutes(t, ctx, db, []snapshotRouteSpec{
		{RouteKey: routeID, Name: "Ambient", Order: 7},
	})
	v1 := seedVideo(t, ctx, db, "v1", "UC1", runID)
	v2 := seedVideo(t, ctx, db, "v2", "UC1", runID)
	seedDecisionWithOrder(t, ctx, db, v1, runID, routeID, 7, "matched")
	seedDecisionWithOrder(t, ctx, db, v2, runID, routeID, 7, "not_matched")

	// The current row is reordered after the run.
	_, err := db.ExecContext(ctx, `UPDATE route SET evaluation_order = 1 WHERE id = ?;`, routeID)
	require.NoError(t, err)

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 1)
	require.NotNil(t, routeStats[0].EvaluationOrder)
	assert.Equal(t, 7, *routeStats[0].EvaluationOrder,
		"neither a later reorder nor the current row order may rewrite the run's order")
}

// A snapshot entry whose stored order is non-positive carries no usable
// historical order; the field falls through to decision evidence or is
// omitted, and the non-positive value is never emitted or transformed.
func TestService_RouteStats_UnusableSnapshotOrderFallsThrough(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	routeID := seedRoute(t, ctx, db, "Odd", 1)
	runID := seedRunWithSnapshotRoutes(t, ctx, db, []snapshotRouteSpec{
		{RouteKey: routeID, Name: "Odd", Order: 0},
	})
	v1 := seedVideo(t, ctx, db, "v1", "UC1", runID)
	v2 := seedVideo(t, ctx, db, "v2", "UC1", runID)
	seedDecisionWithOrder(t, ctx, db, v1, runID, routeID, 2, "matched")
	seedDecisionWithOrder(t, ctx, db, v2, runID, routeID, 2, "not_matched")

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 1)
	require.NotNil(t, routeStats[0].EvaluationOrder,
		"an unusable snapshot order must fall through to agreeing decision evidence")
	assert.Equal(t, 2, *routeStats[0].EvaluationOrder)
}

// With neither a usable snapshot order nor one consistent positive decision
// order, evaluation_order is omitted — never zero, never a sentinel, never a
// transformed value.
func TestService_RouteStats_OrderOmittedWhenUnavailableOrInconsistent(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, ctx context.Context, db *sql.DB) int64
	}{
		{
			// Persisted decisions disagree: no single order is evidence.
			name: "inconsistent decision orders",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB) int64 {
				routeID := seedRoute(t, ctx, db, "Shifty", 1)
				runID := seedRun(t, ctx, db)
				v1 := seedVideo(t, ctx, db, "v1", "UC1", runID)
				v2 := seedVideo(t, ctx, db, "v2", "UC1", runID)
				seedDecisionWithOrder(t, ctx, db, v1, runID, routeID, 1, "matched")
				seedDecisionWithOrder(t, ctx, db, v2, runID, routeID, 2, "not_matched")
				return runID
			},
		},
		{
			// Decision rows all carry the negative sentinel band value.
			name: "non-positive decision orders",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB) int64 {
				routeID := seedRoute(t, ctx, db, "Sentinel", 1)
				runID := seedRun(t, ctx, db)
				v1 := seedVideo(t, ctx, db, "v1", "UC1", runID)
				seedDecisionWithOrder(t, ctx, db, v1, runID, routeID, -3, "matched")
				return runID
			},
		},
		{
			// Route has no decision rows in the run and no snapshot entry.
			name: "no historical evidence",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB) int64 {
				seedRoute(t, ctx, db, "Silent", 1)
				return seedRun(t, ctx, db)
			},
		},
		{
			// OC-003 incomplete context: snapshot undecodable, decisions
			// disagree. The current row order is still not a fallback.
			name: "undecodable snapshot and inconsistent decisions",
			seed: func(t *testing.T, ctx context.Context, db *sql.DB) int64 {
				routeID := seedRoute(t, ctx, db, "Corrupt", 4)
				res, err := db.ExecContext(ctx,
					`INSERT INTO config_version (created_at, snapshot, change_description)
					 VALUES (?, 'not-json', 'seed');`, time.Now().UTC())
				require.NoError(t, err)
				versionID, err := res.LastInsertId()
				require.NoError(t, err)
				res, err = db.ExecContext(ctx,
					`INSERT INTO run (started_at, run_type, status, config_version_id)
					 VALUES (?, 'manual', 'completed', ?);`,
					time.Now().UTC().Format(state.RunTimestampLayout), versionID)
				require.NoError(t, err)
				runID, err := res.LastInsertId()
				require.NoError(t, err)
				v1 := seedVideo(t, ctx, db, "v1", "UC1", runID)
				v2 := seedVideo(t, ctx, db, "v2", "UC1", runID)
				seedDecisionWithOrder(t, ctx, db, v1, runID, routeID, 4, "matched")
				seedDecisionWithOrder(t, ctx, db, v2, runID, routeID, -4, "not_matched")
				return runID
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, _, service, cleanup := setupStatsTest(t)
			defer cleanup()

			runID := tc.seed(t, ctx, db)

			routeStats, err := service.RouteStats(ctx, runID)
			require.NoError(t, err)
			require.Len(t, routeStats, 1)
			assert.Nil(t, routeStats[0].EvaluationOrder,
				"without one consistent positive historical order the field must be omitted")

			// Wire shape: the JSON key is absent, not zero/null.
			raw, err := json.Marshal(routeStats[0])
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "evaluation_order",
				"an unavailable historical order must be omitted from the response, not zero-filled")
		})
	}
}

// The ordinary case is unchanged: an active route whose run snapshot carries
// its order still reports that positive order alongside its counts.
func TestService_RouteStats_ActiveRouteKeepsSnapshotOrder(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	routeOne := seedRoute(t, ctx, db, "Ambient", 1)
	routeTwo := seedRoute(t, ctx, db, "Focus", 2)
	runID := seedRunWithSnapshotRoutes(t, ctx, db, []snapshotRouteSpec{
		{RouteKey: routeOne, Name: "Ambient", Order: 1},
		{RouteKey: routeTwo, Name: "Focus", Order: 2},
	})
	v1 := seedVideo(t, ctx, db, "v1", "UC1", runID)
	v2 := seedVideo(t, ctx, db, "v2", "UC1", runID)
	seedDecisionWithOrder(t, ctx, db, v1, runID, routeOne, 1, "matched")
	seedDecisionWithOrder(t, ctx, db, v2, runID, routeTwo, 2, "not_matched")

	routeStats, err := service.RouteStats(ctx, runID)
	require.NoError(t, err)
	require.Len(t, routeStats, 2)

	byID := make(map[string]RouteStats, len(routeStats))
	for _, rs := range routeStats {
		byID[rs.RouteID] = rs
	}
	ambient := byID[routePublicID(t, ctx, db, routeOne)]
	require.NotNil(t, ambient.EvaluationOrder)
	assert.Equal(t, 1, *ambient.EvaluationOrder)
	assert.Equal(t, 1, ambient.VideosMatched)
	focus := byID[routePublicID(t, ctx, db, routeTwo)]
	require.NotNil(t, focus.EvaluationOrder)
	assert.Equal(t, 2, *focus.EvaluationOrder)
	assert.Equal(t, 1, focus.VideosNotMatched)
}
