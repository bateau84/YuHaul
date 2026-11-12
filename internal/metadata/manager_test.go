package metadata_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T) *metadata.Store {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return metadata.NewStore(db)
}

func TestReadLocalDisplayRequiresActiveUnexpiredMembershipBatch(t *testing.T) {
	ms := openTestDB(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	title, channel, zero := "cached title", "cached channel", 0
	for _, item := range []metadata.TransientMetadata{
		{VideoID: "ok", Title: &title, ChannelName: &channel, DurationSeconds: &zero, FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), IsActive: true},
		{VideoID: "future-deadline", Title: &title, FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Nanosecond), IsActive: true},
		{VideoID: "deadline", Title: &title, FetchedAt: now.Add(-time.Hour), ExpiresAt: now, IsActive: true},
		{VideoID: "inactive", Title: &title, FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), IsActive: false},
	} {
		require.NoError(t, ms.Upsert(context.Background(), item))
	}
	manager := metadata.NewManager(ms, nil, nil, metadata.WithNow(func() time.Time { return now }))
	got, err := manager.ReadLocalDisplay(context.Background(), []string{"ok", "future-deadline", "deadline", "inactive", "missing"})
	require.NoError(t, err)
	require.Equal(t, "current_local", got["ok"].DurationAvailability)
	require.Equal(t, "current_local", got["future-deadline"].TitleAvailability)
	require.NotNil(t, got["ok"].DurationSeconds)
	require.Equal(t, 0, *got["ok"].DurationSeconds)
	require.Equal(t, "known_expired", got["deadline"].TitleAvailability)
	require.Nil(t, got["deadline"].Title)
	require.Equal(t, "withheld", got["inactive"].TitleAvailability)
	require.Nil(t, got["inactive"].Title)
	_, err = manager.ReadLocalDisplay(context.Background(), make([]string, 101))
	require.Error(t, err)
}

func TestReadLocalDisplayWithholdsUntrustworthyLifecycleAndEmptyNames(t *testing.T) {
	ms := openTestDB(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	title, empty, duration := "title", "", 0
	for _, item := range []metadata.TransientMetadata{
		{VideoID: "bad-fetch", Title: &title, FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), IsActive: true},
		{VideoID: "marker", Title: &title, FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), IsActive: true},
		{VideoID: "malformed-marker", Title: &title, ChannelName: &title, DurationSeconds: &duration, FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), IsActive: true},
		{VideoID: "cleared-expired", FetchedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), IsActive: false},
		{VideoID: "empty", Title: &empty, ChannelName: &empty, DurationSeconds: &duration, FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), IsActive: true},
	} {
		require.NoError(t, ms.Upsert(context.Background(), item))
	}
	_, err := ms.DB().Exec(`UPDATE transient_metadata SET fetched_at='not-a-timestamp' WHERE video_id='bad-fetch'`)
	require.NoError(t, err)
	_, err = ms.DB().Exec(`UPDATE transient_metadata SET expired_at=? WHERE video_id='marker'`, now.Add(-time.Minute))
	require.NoError(t, err)
	_, err = ms.DB().Exec(`UPDATE transient_metadata SET expired_at='not-a-timestamp' WHERE video_id='malformed-marker'`)
	require.NoError(t, err)
	_, err = ms.DB().Exec(`UPDATE transient_metadata SET expired_at=? WHERE video_id='cleared-expired'`, now.Add(-time.Hour))
	require.NoError(t, err)
	manager := metadata.NewManager(ms, nil, nil, metadata.WithNow(func() time.Time { return now }))
	got, err := manager.ReadLocalDisplay(context.Background(), []string{"bad-fetch", "marker", "malformed-marker", "cleared-expired", "empty", "missing"})
	require.NoError(t, err)
	assert.Nil(t, got["bad-fetch"].Title)
	assert.Equal(t, "withheld", got["bad-fetch"].TitleAvailability)
	assert.Nil(t, got["bad-fetch"].SourceFetchedAt)
	assert.Nil(t, got["marker"].Title)
	assert.Equal(t, "known_expired", got["marker"].TitleAvailability)
	assert.Nil(t, got["malformed-marker"].Title)
	assert.Nil(t, got["malformed-marker"].ChannelName)
	assert.Nil(t, got["malformed-marker"].DurationSeconds)
	assert.Equal(t, "withheld", got["malformed-marker"].TitleAvailability)
	assert.Equal(t, "withheld", got["malformed-marker"].ChannelNameAvailability)
	assert.Equal(t, "withheld", got["malformed-marker"].DurationAvailability)
	assert.Equal(t, "known_expired", got["cleared-expired"].TitleAvailability)
	assert.Equal(t, "known_expired", got["cleared-expired"].ChannelNameAvailability)
	assert.Equal(t, "known_expired", got["cleared-expired"].DurationAvailability)
	assert.Nil(t, got["cleared-expired"].Title)
	assert.Nil(t, got["cleared-expired"].DurationSeconds)
	assert.Nil(t, got["empty"].Title)
	assert.Equal(t, "unknown", got["empty"].TitleAvailability)
	assert.Nil(t, got["empty"].ChannelName)
	assert.Equal(t, "current_local", got["empty"].DurationAvailability)
	assert.Nil(t, got["missing"].Title)
	assert.Equal(t, "unknown", got["missing"].TitleAvailability)
	assert.Equal(t, "current_local_not_necessarily_evaluated", got["missing"].Attribution)
}

func TestReadLocalDisplayUsesProjectionCutoffAfterQuery(t *testing.T) {
	ms := openTestDB(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	title := "crossed deadline"
	require.NoError(t, ms.Upsert(context.Background(), metadata.TransientMetadata{VideoID: "cross", Title: &title, FetchedAt: base.Add(-time.Minute), ExpiresAt: base.Add(time.Second), IsActive: true}))
	// Manager captures this cutoff only after Store has consumed the SQLite rows.
	manager := metadata.NewManager(ms, nil, nil, metadata.WithNow(func() time.Time { return base.Add(2 * time.Second) }))
	got, err := manager.ReadLocalDisplay(context.Background(), []string{"cross"})
	require.NoError(t, err)
	assert.Nil(t, got["cross"].Title)
	assert.Equal(t, "known_expired", got["cross"].TitleAvailability)
}

func setupLedger(t *testing.T, db interface {
	ExecContext(context.Context, string, ...any) (any, error)
}) *quota.Ledger {
	t.Helper()
	// The ledger expects its own tables, but it also works on the shared db.
	// We use a raw type assertion because quota.Ledger requires *sql.DB.
	return nil // populated per-test below
}

func newManager(t *testing.T, store *metadata.Store, fetcher metadata.Fetcher, now time.Time) (*metadata.Manager, *quota.Ledger) {
	t.Helper()
	ledger := quota.New(store.DB(), quota.WithDefaultLimit(10000))
	mgr := metadata.NewManager(store, fetcher, ledger, metadata.WithNow(func() time.Time { return now }))
	return mgr, ledger
}

func ptr(s string) *string { return &s }

func sampleMetadata(videoID string, title string, fetchedAt time.Time, expiresAt time.Time) metadata.TransientMetadata {
	return metadata.TransientMetadata{
		VideoID:   videoID,
		Title:     ptr(title),
		FetchedAt: fetchedAt.UTC(),
		ExpiresAt: expiresAt.UTC(),
		IsActive:  true,
	}
}

type fakeFetcher struct {
	returnMap map[string]metadata.TransientMetadata
	err       error
}

func (f *fakeFetcher) FetchMetadata(ctx context.Context, videoIDs []string) ([]metadata.TransientMetadata, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []metadata.TransientMetadata
	for _, id := range videoIDs {
		if m, ok := f.returnMap[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func TestManager_ExpireStale(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	fetcher := &fakeFetcher{}
	mgr, _ := newManager(t, s, fetcher, now)

	// fresh record
	require.NoError(t, s.Upsert(ctx, sampleMetadata("fresh", "Fresh Title", now.Add(-24*time.Hour), now.Add(29*24*time.Hour))))
	// stale record
	require.NoError(t, s.Upsert(ctx, sampleMetadata("stale", "Stale Title", now.Add(-40*24*time.Hour), now.Add(-10*24*time.Hour))))
	// already expired
	expired := sampleMetadata("expired", "Expired Title", now.Add(-60*24*time.Hour), now.Add(-30*24*time.Hour))
	expired.IsActive = false
	require.NoError(t, s.Upsert(ctx, expired))

	count, err := mgr.ExpireStale(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	stale, err := s.Get(ctx, "stale")
	require.NoError(t, err)
	require.False(t, stale.IsActive)
	require.Nil(t, stale.Title)
	require.Nil(t, stale.ChannelName)
	require.Nil(t, stale.Description)
	require.Nil(t, stale.ThumbnailURL)
	require.NotNil(t, stale.ExpiredAt)

	fresh, err := s.Get(ctx, "fresh")
	require.NoError(t, err)
	require.True(t, fresh.IsActive)
	require.NotNil(t, fresh.Title)
}

func TestManager_CountStaleActiveFalsification(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	fetcher := &fakeFetcher{}
	mgr, _ := newManager(t, s, fetcher, now)

	require.NoError(t, s.Upsert(ctx, sampleMetadata("stale", "Stale", now.Add(-40*24*time.Hour), now.Add(-10*24*time.Hour))))

	count, err := mgr.CountStaleActive(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	_, err = mgr.ExpireStale(ctx)
	require.NoError(t, err)

	count, err = mgr.CountStaleActive(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count, "falsification test: zero stale active records after expiry")
}

func TestManager_RefreshOnDemandRestoresDisplay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	fetcher := &fakeFetcher{
		returnMap: map[string]metadata.TransientMetadata{
			"old": {
				VideoID:     "old",
				Title:       ptr("Refreshed Title"),
				ChannelName: ptr("Refreshed Channel"),
			},
		},
	}
	mgr, _ := newManager(t, s, fetcher, now)

	expired := sampleMetadata("old", "Old Title", now.Add(-40*24*time.Hour), now.Add(-10*24*time.Hour))
	expired.IsActive = false
	expired.Title = nil
	expired.ExpiredAt = ptrTime(now.Add(-5 * 24 * time.Hour))
	require.NoError(t, s.Upsert(ctx, expired))

	_, err := mgr.GetDisplayMetadata(ctx, "old")
	require.True(t, metadata.IsMetadataExpired(err))

	require.NoError(t, mgr.RefreshOnDemand(ctx, "old"))

	meta, err := mgr.GetDisplayMetadata(ctx, "old")
	require.NoError(t, err)
	require.Equal(t, "Refreshed Title", *meta.Title)
	require.Equal(t, "Refreshed Channel", *meta.ChannelName)
	require.True(t, meta.IsActive)
	require.Nil(t, meta.ExpiredAt)
	require.True(t, meta.ExpiresAt.After(now))
}

func TestManager_RefreshRoutedNearExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	db := s.DB()

	// Insert a run, route, video, route_decision, and executed_action so the
	// video is considered "routed".
	res, err := db.ExecContext(ctx, "INSERT INTO config_version (snapshot) VALUES (?);", "{}")
	require.NoError(t, err)
	cfgID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx, "INSERT INTO run (run_type, status, config_version_id) VALUES (?, ?, ?);", "manual", "running", cfgID)
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx, "INSERT INTO route (name, evaluation_order, condition_composition, created_at, updated_at) VALUES (?, ?, ?, ?, ?);",
		"music", 1, "AND", now, now)
	require.NoError(t, err)
	routeID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx, "INSERT INTO video (video_id, channel_id, processing_state, final_outcome) VALUES (?, ?, ?, ?);",
		"routed", "UC1", "not_yet_evaluated", "not_yet_evaluated")
	require.NoError(t, err)
	videoRowID, err := res.LastInsertId()
	require.NoError(t, err)

	res, err = db.ExecContext(ctx, "INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision) VALUES (?, ?, ?, ?, ?);",
		videoRowID, runID, routeID, 1, "matched")
	require.NoError(t, err)
	decisionID, err := res.LastInsertId()
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type, target_playlist_id, outcome, executed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?);`,
		decisionID, runID, videoRowID, "playlist_insert", "PLtest", "succeeded", now)
	require.NoError(t, err)

	// Metadata nearing expiry for the routed video.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("routed", "Routed Title", now.Add(-25*24*time.Hour), now.Add(5*24*time.Hour))))

	fetcher := &fakeFetcher{
		returnMap: map[string]metadata.TransientMetadata{
			"routed": {
				VideoID: "routed",
				Title:   ptr("Routed Refreshed"),
			},
		},
	}
	mgr, _ := newManager(t, s, fetcher, now)

	refreshed, err := mgr.RefreshRoutedNearExpiry(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, refreshed)

	meta, err := s.Get(ctx, "routed")
	require.NoError(t, err)
	require.Equal(t, "Routed Refreshed", *meta.Title)
}

func TestManager_RefreshRoutedNearExpiry_SkipsUnrouted(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	// Unrouted video nearing expiry.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("unrouted", "Unrouted Title", now.Add(-25*24*time.Hour), now.Add(5*24*time.Hour))))

	fetcher := &fakeFetcher{}
	mgr, _ := newManager(t, s, fetcher, now)

	refreshed, err := mgr.RefreshRoutedNearExpiry(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, refreshed)
}

func TestManager_RefreshRespectsQuota(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	require.NoError(t, s.Upsert(ctx, sampleMetadata("v1", "One", now.Add(-25*24*time.Hour), now.Add(5*24*time.Hour))))

	fetcher := &fakeFetcher{}
	ledger := quota.New(s.DB(), quota.WithDefaultLimit(0)) // zero budget
	mgr := metadata.NewManager(s, fetcher, ledger, metadata.WithNow(func() time.Time { return now }))

	refreshed, err := mgr.RefreshActiveNearExpiry(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, refreshed)
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestManager_GetDisplayMetadata_NotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	mgr, _ := newManager(t, s, &fakeFetcher{}, now)

	_, err := mgr.GetDisplayMetadata(ctx, "missing")
	require.True(t, errors.Is(err, metadata.ErrNotFound))
}

func TestManager_CleanupExpired(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	mgr, _ := newManager(t, s, &fakeFetcher{}, now)

	// Record older than the default 30-day TTL.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("old", "Old Title", now.Add(-40*24*time.Hour), now.Add(-10*24*time.Hour))))
	// Record well within the TTL.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("fresh", "Fresh Title", now.Add(-24*time.Hour), now.Add(29*24*time.Hour))))

	deleted, err := mgr.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)

	_, err = s.Get(ctx, "old")
	require.True(t, errors.Is(err, metadata.ErrNotFound))

	fresh, err := s.Get(ctx, "fresh")
	require.NoError(t, err)
	require.NotNil(t, fresh.Title)
}

func TestManager_CleanupExpired_Idempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	mgr, _ := newManager(t, s, &fakeFetcher{}, now)

	require.NoError(t, s.Upsert(ctx, sampleMetadata("old", "Old Title", now.Add(-40*24*time.Hour), now.Add(-10*24*time.Hour))))

	deleted, err := mgr.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)

	deleted, err = mgr.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, deleted)
}

func TestManager_CleanupExpired_CustomTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	fetcher := &fakeFetcher{}
	ledger := quota.New(s.DB(), quota.WithDefaultLimit(10000))
	mgr := metadata.NewManager(s, fetcher, ledger,
		metadata.WithNow(func() time.Time { return now }),
		metadata.WithTTL(7*24*time.Hour),
	)

	// 10 days old is beyond the 7-day TTL.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("old", "Old Title", now.Add(-10*24*time.Hour), now.Add(20*24*time.Hour))))
	// 5 days old is within the 7-day TTL.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("fresh", "Fresh Title", now.Add(-5*24*time.Hour), now.Add(2*24*time.Hour))))

	deleted, err := mgr.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)

	_, err = s.Get(ctx, "old")
	require.True(t, errors.Is(err, metadata.ErrNotFound))

	fresh, err := s.Get(ctx, "fresh")
	require.NoError(t, err)
	require.NotNil(t, fresh.Title)
}

func TestManager_GetDisplayTitle_DegradesToFingerprint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	mgr, _ := newManager(t, s, &fakeFetcher{}, now)

	// Insert a Store B fingerprint for the video.
	_, err := s.DB().ExecContext(ctx, `
		INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, source, first_seen_at)
		VALUES (?, ?, ?, ?, ?);`, "v1", "daft punk lucky", "daft punk lucky", "api_discovery", now)
	require.NoError(t, err)

	// Active metadata returns the raw title.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("v1", "Get Lucky", now.Add(-24*time.Hour), now.Add(29*24*time.Hour))))
	title, err := mgr.GetDisplayTitle(ctx, "v1")
	require.NoError(t, err)
	require.Equal(t, "Get Lucky", title)

	// Expired metadata degrades to the fingerprint hash.
	expired := sampleMetadata("v1", "Get Lucky", now.Add(-40*24*time.Hour), now.Add(-10*24*time.Hour))
	expired.IsActive = false
	expired.Title = nil
	expired.ExpiredAt = ptrTime(now.Add(-5 * 24 * time.Hour))
	require.NoError(t, s.Upsert(ctx, expired))

	title, err = mgr.GetDisplayTitle(ctx, "v1")
	require.NoError(t, err)
	require.Equal(t, "daft punk lucky", title)
}

func TestManager_GetDisplayTitle_MissingMetadataAndFingerprint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	mgr, _ := newManager(t, s, &fakeFetcher{}, now)

	title, err := mgr.GetDisplayTitle(ctx, "missing")
	require.NoError(t, err)
	require.Equal(t, "[metadata expired]", title)
}

// TestManager_INV9_Compliance verifies the metadata TTL guarantees required by
// INV-9: expired records are deleted, non-expired records survive, titles fall
// back to the permanent fingerprint, and cleanup can safely be run repeatedly.
func TestManager_INV9_Compliance(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	s := openTestDB(t)
	mgr, _ := newManager(t, s, &fakeFetcher{}, now)

	// Seed Store B fingerprints so degraded titles have a stable fallback.
	_, err := s.DB().ExecContext(ctx, `
		INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, source, first_seen_at)
		VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?);`,
		"expired", "expired fingerprint", "expired fingerprint", "api_discovery", now,
		"fresh", "fresh fingerprint", "fresh fingerprint", "api_discovery", now,
	)
	require.NoError(t, err)

	// Expired record: fetched well beyond the 30-day TTL.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("expired", "Expired Title", now.Add(-40*24*time.Hour), now.Add(-10*24*time.Hour))))
	// Fresh record: fetched within the TTL and not yet expired.
	require.NoError(t, s.Upsert(ctx, sampleMetadata("fresh", "Fresh Title", now.Add(-24*time.Hour), now.Add(29*24*time.Hour))))

	deleted, err := mgr.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, deleted, "expired record should be cleaned")

	// Expired record must no longer exist in Store A.
	_, err = s.Get(ctx, "expired")
	require.True(t, errors.Is(err, metadata.ErrNotFound), "expired metadata should be removed")

	// Non-expired record must remain intact.
	fresh, err := s.Get(ctx, "fresh")
	require.NoError(t, err)
	require.NotNil(t, fresh.Title)
	require.Equal(t, "Fresh Title", *fresh.Title)

	// Display title for the cleaned expired record degrades to fingerprint.
	title, err := mgr.GetDisplayTitle(ctx, "expired")
	require.NoError(t, err)
	require.Equal(t, "expired fingerprint", title)

	// Cleanup must be idempotent: a second run finds nothing and does not error.
	deleted, err = mgr.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, deleted, "repeated cleanup should be a no-op")
}
