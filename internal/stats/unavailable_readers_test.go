package stats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

// TestO2UnavailableOnYouTubeCarriedByStatsReaders (ARS-005 §6.4, test O2):
// the per-run projection and the temporal aggregation both carry
// videos_unavailable as its own field, separate from rejected and failed.
func TestO2UnavailableOnYouTubeCarriedByStatsReaders(t *testing.T) {
	db, s, _, cleanup := setupStatsTest(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().UTC()
	var runID int64
	for _, n := range []int{3, 4} {
		res, err := db.ExecContext(ctx,
			`INSERT INTO run (started_at, run_type, status, videos_discovered, videos_rejected, videos_failed, videos_unavailable)
			 VALUES (?, 'manual', 'completed', 10, 1, 1, ?);`, now.Format(state.RunTimestampLayout), n)
		require.NoError(t, err)
		runID, err = res.LastInsertId()
		require.NoError(t, err)
	}

	rs, err := s.GetRunStats(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, 4, rs.VideosUnavailable)
	assert.Equal(t, 1, rs.VideosRejected)
	assert.Equal(t, 1, rs.VideosFailed)
	raw, err := json.Marshal(rs)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"videos_unavailable":4`)

	buckets, err := s.GetTemporalStats(ctx, now.Add(-time.Hour), now.Add(time.Hour), "day")
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, 7, buckets[0].VideosUnavailable, "aggregation sums the new counter")
	assert.Equal(t, 2, buckets[0].VideosRejected)
	assert.Equal(t, 2, buckets[0].VideosFailed)
}
