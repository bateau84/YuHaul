package pipeline

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/state"
)

func strp(s string) *string { return &s }

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

// seedPublished retains a video whose local metadata row carries publishedAt.
// The row goes through the real metadata store, so the SQLite driver writes
// the published instant in its own stored format (migration 011).
func seedPublished(t *testing.T, ctx context.Context, deps *replayDeps, runID int64, videoID string, publishedAt time.Time) {
	t.Helper()
	_, err := deps.machine.DiscoverVideo(ctx, runID, videoID, "ch1")
	require.NoError(t, err)
	now := time.Now().UTC()
	title := "jazz " + videoID
	require.NoError(t, deps.metaStore.Upsert(ctx, metadata.TransientMetadata{
		VideoID:     videoID,
		Title:       &title,
		PublishedAt: &publishedAt,
		FetchedAt:   now,
		ExpiresAt:   now.Add(30 * 24 * time.Hour),
		IsActive:    true,
	}))
}

func seedProductionRun(t *testing.T, ctx context.Context, deps *replayDeps) int64 {
	t.Helper()
	id, err := deps.stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)
	return id
}

// startHistorical creates and executes a Replay or Backtest with rng.
func startHistorical(t *testing.T, ctx context.Context, deps *replayDeps, mode string, rng state.PublishedRange) *state.Run {
	t.Helper()
	var run *state.Run
	var err error
	if mode == "replay" {
		run, err = deps.pipeline.Replay(ctx, nil, rng)
		require.NoError(t, err)
		deps.pipeline.ExecuteReplay(ctx, run.ID)
	} else {
		run, err = deps.pipeline.Backtest(ctx, proposalSnapshot(proposalRoute(proposalTestUUID, "jazz", "jazz")), rng)
		require.NoError(t, err)
		deps.pipeline.ExecuteBacktest(ctx, run.ID)
	}
	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	return loaded
}

func decidedVideos(t *testing.T, ctx context.Context, deps *replayDeps, runID int64) []string {
	t.Helper()
	rows, err := deps.db.QueryContext(ctx,
		`SELECT DISTINCT v.video_id FROM route_decision rd JOIN video v ON v.id = rd.video_id WHERE rd.run_id = ?`, runID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// D2 + D8: half-open UTC boundaries through both historical modes.
func TestHistoricalRange_HalfOpenUTCBoundaries(t *testing.T) {
	for _, mode := range []string{"backtest", "replay"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			deps, cleanup := setupReplayPipeline(t)
			defer cleanup()
			createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
			prod := seedProductionRun(t, ctx, deps)

			seedPublished(t, ctx, deps, prod, "before", mustTime(t, "2026-09-30T23:59:59Z"))
			seedPublished(t, ctx, deps, prod, "start", mustTime(t, "2026-10-01T00:00:00Z"))
			seedPublished(t, ctx, deps, prod, "end", mustTime(t, "2026-10-31T23:59:59Z"))
			seedPublished(t, ctx, deps, prod, "after", mustTime(t, "2026-11-01T00:00:00Z"))
			// Same calendar day as the start, but 23:00Z on 09-30: outside.
			seedPublished(t, ctx, deps, prod, "offset_out", mustTime(t, "2026-10-01T01:00:00+02:00"))
			// 01:00+02:00 on 11-01 is 23:00Z on 10-31: inside.
			seedPublished(t, ctx, deps, prod, "offset_in", mustTime(t, "2026-11-01T01:00:00+02:00"))

			run := startHistorical(t, ctx, deps, mode, state.PublishedRange{From: strp("2026-10-01"), To: strp("2026-10-31")})

			assert.Equal(t, state.RunStatusCompleted, run.Status)
			require.NotNil(t, run.VideosSelected)
			assert.Equal(t, 3, *run.VideosSelected)
			assert.Equal(t, 3, run.VideosEvaluated)
			assert.Equal(t, []string{"end", "offset_in", "start"}, decidedVideos(t, ctx, deps, run.ID))
		})
	}
}

// D3: no range keeps today's set; one bound applies only that bound.
func TestHistoricalRange_NoRangeAndOneBound(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	prod := seedProductionRun(t, ctx, deps)

	seedPublished(t, ctx, deps, prod, "old", mustTime(t, "2026-09-15T12:00:00Z"))
	seedPublished(t, ctx, deps, prod, "new", mustTime(t, "2026-10-15T12:00:00Z"))
	seedPublished(t, ctx, deps, prod, "expired", mustTime(t, "2026-10-15T12:00:00Z"))
	_, err := deps.db.ExecContext(ctx, `UPDATE transient_metadata SET is_active = 0, expired_at = ? WHERE video_id = 'expired'`, time.Now().UTC())
	require.NoError(t, err)
	_, err = deps.machine.DiscoverVideo(ctx, prod, "nometa", "ch1")
	require.NoError(t, err)

	all := startHistorical(t, ctx, deps, "backtest", state.PublishedRange{})
	require.NotNil(t, all.VideosSelected)
	assert.Equal(t, 4, *all.VideosSelected, "no range selects every retained video, dated or not")

	startOnly := startHistorical(t, ctx, deps, "backtest", state.PublishedRange{From: strp("2026-10-01")})
	assert.Equal(t, []string{"new"}, decidedVideos(t, ctx, deps, startOnly.ID))

	endOnly := startHistorical(t, ctx, deps, "backtest", state.PublishedRange{To: strp("2026-09-30")})
	assert.Equal(t, []string{"old"}, decidedVideos(t, ctx, deps, endOnly.ID))
}

// D4: a video without an eligible local published date is never selected.
func TestHistoricalRange_IneligibleDatesAreNotSelected(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	prod := seedProductionRun(t, ctx, deps)
	inRange := mustTime(t, "2026-10-15T12:00:00Z")

	seedPublished(t, ctx, deps, prod, "ok", inRange)
	for _, id := range []string{"inactive", "expired_at", "expires_past", "null_pub", "bad_pub"} {
		seedPublished(t, ctx, deps, prod, id, inRange)
	}
	_, err := deps.machine.DiscoverVideo(ctx, prod, "no_row", "ch1")
	require.NoError(t, err)

	exec := func(q string, args ...any) {
		_, err := deps.db.ExecContext(ctx, q, args...)
		require.NoError(t, err)
	}
	exec(`UPDATE transient_metadata SET is_active = 0 WHERE video_id = 'inactive'`)
	exec(`UPDATE transient_metadata SET expired_at = ? WHERE video_id = 'expired_at'`, time.Now().UTC())
	exec(`UPDATE transient_metadata SET expires_at = ? WHERE video_id = 'expires_past'`, time.Now().UTC().Add(-time.Minute))
	exec(`UPDATE transient_metadata SET published_at = NULL WHERE video_id = 'null_pub'`)
	exec(`UPDATE transient_metadata SET published_at = 'not a date' WHERE video_id = 'bad_pub'`)

	run := startHistorical(t, ctx, deps, "backtest", state.PublishedRange{From: strp("2026-10-01"), To: strp("2026-10-31")})
	assert.Equal(t, state.RunStatusCompleted, run.Status)
	require.NotNil(t, run.VideosSelected)
	assert.Equal(t, 1, *run.VideosSelected)
	assert.Equal(t, 1, run.VideosEvaluated)
	assert.Equal(t, []string{"ok"}, decidedVideos(t, ctx, deps, run.ID))
}

// D6: a valid range matching nothing completes with a known zero.
func TestHistoricalRange_ZeroMatchCompletes(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
	seedPublished(t, ctx, deps, seedProductionRun(t, ctx, deps), "v", mustTime(t, "2026-10-15T12:00:00Z"))

	run := startHistorical(t, ctx, deps, "replay", state.PublishedRange{From: strp("2020-01-01"), To: strp("2020-01-31")})
	assert.Equal(t, state.RunStatusCompleted, run.Status)
	require.NotNil(t, run.VideosSelected)
	assert.Equal(t, 0, *run.VideosSelected)
	assert.Equal(t, 0, run.VideosEvaluated)
}

// D7: the range is stored on the run, projected on reads, and immutable.
func TestHistoricalRange_PersistedProjectedAndImmutable(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	prod := seedProductionRun(t, ctx, deps)

	ranged := startHistorical(t, ctx, deps, "backtest", state.PublishedRange{From: strp("2026-10-01"), To: strp("2026-10-31")})
	unranged := startHistorical(t, ctx, deps, "replay", state.PublishedRange{})

	// Read through a fresh store over the same database, as after a restart.
	fresh, err := state.NewStore(deps.db)
	require.NoError(t, err)
	got, err := fresh.GetRun(ctx, ranged.ID)
	require.NoError(t, err)
	assert.Equal(t, `{"from":"2026-10-01","to":"2026-10-31"}`, publishedRangeJSON(t, got))
	listed, err := fresh.ListRuns(ctx)
	require.NoError(t, err)
	byID := map[int64]state.Run{}
	for _, r := range listed {
		byID[r.ID] = r
	}
	assert.Equal(t, `{"from":"2026-10-01","to":"2026-10-31"}`, publishedRangeJSON(t, ptr(byID[ranged.ID])))
	assert.Equal(t, `{"from":null,"to":null}`, publishedRangeJSON(t, ptr(byID[unranged.ID])))
	assert.Equal(t, `null`, publishedRangeJSON(t, ptr(byID[prod])))

	for _, stmt := range []string{
		`UPDATE run SET published_from = '2026-01-01' WHERE id = ?`,
		`UPDATE run SET published_to = NULL WHERE id = ?`,
	} {
		_, err := deps.db.ExecContext(ctx, stmt, ranged.ID)
		assert.Error(t, err, stmt)
	}
	_, err = deps.db.ExecContext(ctx, `UPDATE run SET published_from = '2026-01-01' WHERE id = ?`, unranged.ID)
	assert.Error(t, err, "NULL -> value is also rejected")
}

func ptr[T any](v T) *T { return &v }

func publishedRangeJSON(t *testing.T, r *state.Run) string {
	t.Helper()
	b, err := json.Marshal(r)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	raw, ok := m["published_range"]
	require.True(t, ok, "published_range must always be present")
	return string(raw)
}

// An invalid range is refused before any run, snapshot or config lookup exists.
func TestHistoricalRange_InvalidRangeCreatesNothing(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()

	count := func(table string) int {
		var n int
		require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n))
		return n
	}
	runs, versions := count("run"), count("config_version")

	bad := state.PublishedRange{From: strp("2026-11-02"), To: strp("2026-11-01")}
	_, err := deps.pipeline.Backtest(ctx, proposalSnapshot(proposalRoute(proposalTestUUID, "jazz", "jazz")), bad)
	assert.ErrorIs(t, err, ErrInvalidDateRange)
	unknownID := int64(999999) // would be "not found" if the config were looked up
	_, err = deps.pipeline.Replay(ctx, &unknownID, state.PublishedRange{From: strp("2026-02-30")})
	assert.ErrorIs(t, err, ErrInvalidDateRange)

	assert.Equal(t, runs, count("run"))
	assert.Equal(t, versions, count("config_version"))
}
