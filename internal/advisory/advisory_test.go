package advisory

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

func setupAdvisoryTest(t *testing.T) (*sql.DB, *Store, *Service, *fakeChannelReader, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)

	advisoryStore := NewStore(db)
	reader := &fakeChannelReader{}
	service := NewService(advisoryStore, reader, &fakeSettings{})

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, advisoryStore, service, reader, cleanup
}

type fakeSettings struct{}

func (f *fakeSettings) GetSetting(ctx context.Context, key string) (string, error) {
	return "", nil
}

type fakeChannelReader struct {
	channels []ChannelStats
	provenance map[string][]DuplicateProvenance
}

func (f *fakeChannelReader) ListChannelStats(ctx context.Context) ([]ChannelStats, error) {
	return f.channels, nil
}

func (f *fakeChannelReader) ListDuplicateProvenance(ctx context.Context, channelIDText string) ([]DuplicateProvenance, error) {
	return f.provenance[channelIDText], nil
}

func seedChannel(t *testing.T, ctx context.Context, db *sql.DB, channelID, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered, total_routed, total_retained)
		 VALUES (?, ?, ?, ?, 10, 3, 0)
		 ON CONFLICT(channel_id) DO UPDATE SET total_retained = excluded.total_retained;`,
		channelID, name, time.Now().UTC(), time.Now().UTC())
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func TestService_GenerateAdvisories_LowUsefulness(t *testing.T) {
	ctx := context.Background()
	db, _, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Low Usefulness")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Low Usefulness", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	err := service.GenerateAdvisories(ctx)
	require.NoError(t, err)

	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)
	assert.Equal(t, AdvisoryTypeLowUsefulness, advisories[0].Type)
	assert.Equal(t, "UC1", advisories[0].ChannelIDText)
}

func TestService_GenerateAdvisories_DoesNotDuplicateActive(t *testing.T) {
	ctx := context.Background()
	db, _, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Low Usefulness")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Low Usefulness", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	err := service.GenerateAdvisories(ctx)
	require.NoError(t, err)
	err = service.GenerateAdvisories(ctx)
	require.NoError(t, err)

	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)
}

func TestService_Dismiss(t *testing.T) {
	ctx := context.Background()
	db, store, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Dismiss")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Dismiss", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	err := service.GenerateAdvisories(ctx)
	require.NoError(t, err)
	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)

	err = service.Dismiss(ctx, advisories[0].ID)
	require.NoError(t, err)

	adv, err := store.GetAdvisory(ctx, advisories[0].ID)
	require.NoError(t, err)
	assert.Equal(t, AdvisoryStatusDismissed, adv.Status)
	assert.NotNil(t, adv.DismissedAt)

	active, err := service.ListActive(ctx)
	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestService_Ignore(t *testing.T) {
	ctx := context.Background()
	db, store, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Ignore")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Ignore", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	err := service.GenerateAdvisories(ctx)
	require.NoError(t, err)
	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)

	err = service.Ignore(ctx, advisories[0].ID)
	require.NoError(t, err)

	adv, err := store.GetAdvisory(ctx, advisories[0].ID)
	require.NoError(t, err)
	assert.Equal(t, AdvisoryStatusIgnored, adv.Status)
	assert.NotNil(t, adv.IgnoredUntil)
}

func TestService_Act(t *testing.T) {
	ctx := context.Background()
	db, store, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Act")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Act", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	err := service.GenerateAdvisories(ctx)
	require.NoError(t, err)
	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)

	err = service.Act(ctx, advisories[0].ID, "unsubscribed")
	require.NoError(t, err)

	adv, err := store.GetAdvisory(ctx, advisories[0].ID)
	require.NoError(t, err)
	assert.Equal(t, AdvisoryStatusActedUpon, adv.Status)
	require.NotNil(t, adv.ActionTaken)
	assert.Equal(t, "unsubscribed", *adv.ActionTaken)
	assert.NotNil(t, adv.ActedUponAt)
}

func TestService_GenerateAdvisories_ExpiresIgnored(t *testing.T) {
	ctx := context.Background()
	db, _, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Expire")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Expire", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	err := service.GenerateAdvisories(ctx)
	require.NoError(t, err)
	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)

	err = service.Ignore(ctx, advisories[0].ID)
	require.NoError(t, err)

	// Force ignore window to have expired.
	_, err = db.ExecContext(ctx, `UPDATE advisory SET ignored_until = ? WHERE id = ?;`, time.Now().UTC().Add(-time.Hour), advisories[0].ID)
	require.NoError(t, err)

	err = service.GenerateAdvisories(ctx)
	require.NoError(t, err)

	advisories, err = service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)
	assert.Nil(t, advisories[0].IgnoredUntil)
}
