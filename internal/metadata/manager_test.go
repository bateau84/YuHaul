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

func setupLedger(t *testing.T, db interface{ ExecContext(context.Context, string, ...any) (any, error) }) *quota.Ledger {
	t.Helper()
	// The ledger expects its own tables, but it also works on the shared db.
	// We use a raw type assertion because quota.Ledger requires *sql.DB.
	return nil // populated per-test below
}

func newManager(t *testing.T, store *metadata.Store, fetcher metadata.Fetcher, now time.Time) (*metadata.Manager, *quota.Ledger) {
	t.Helper()
	ledger := quota.New(store.DB(), quota.WithDefaultLimit(10000))
	require.NoError(t, ledger.EnsureSchema(context.Background()))
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
	require.NoError(t, ledger.EnsureSchema(ctx))
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
