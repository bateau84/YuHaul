package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/state"
)

// BR-008 AC8 / OC-004 / ADR-014: the production duplicate phase must not
// persist a self-match. This run observes VSELF, whose ID already carries an
// imported reference that native discovery re-upserted before evaluation, and
// VOTHER, a distinct video with its own eligible reference. Only VOTHER may
// produce a duplicate_match row; VSELF's row keeps its matching eligibility
// and creates no match, route, or discovery-credit evidence.
func TestDuplicatePhasePersistsNoSelfMatch(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupReplayPipeline(t)
	defer cleanup()
	deps.pipeline.detector = duplicate.NewDetector(deps.db)

	batchID := "batch-import"
	selfRefID, err := duplicate.SaveFingerprint(ctx, deps.db, "VSELF", "UC1",
		"user_import", "Jazz Live Session", true, &batchID)
	require.NoError(t, err)
	// The native observation for the same ID, persisted by the discovery path
	// before duplicate evaluation (ADR-013).
	_, err = duplicate.SaveFingerprint(ctx, deps.db, "VSELF", "UC1",
		"api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)

	distinctRefID, err := duplicate.SaveFingerprint(ctx, deps.db, "VREF", "UC2",
		"user_import", "Rock Session", true, &batchID)
	require.NoError(t, err)

	prodRun := &state.Run{
		StartedAt: time.Now().UTC(),
		RunType:   state.RunTypeManual,
		Status:    state.RunStatusRunning,
	}
	captureNativeTestRun(t, ctx, deps.db, prodRun)
	runID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)

	selfVideo := createRetainedVideo(t, ctx, deps, runID, "VSELF", "UC1", "Jazz Live Session")
	otherVideo := createRetainedVideo(t, ctx, deps, runID, "VOTHER", "UC1", "Rock Session")

	// Native discovery also writes the evaluated video's own fingerprint row.
	_, err = duplicate.SaveFingerprint(ctx, deps.db, otherVideo.VideoID, otherVideo.ChannelID,
		"api_discovery", "Rock Session", false, nil)
	require.NoError(t, err)

	videos := []*state.Video{selfVideo, otherVideo}
	require.NoError(t, deps.pipeline.phaseDuplicateDetection(ctx, runID, videos))

	var count int
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM duplicate_match;`).Scan(&count))
	require.Equal(t, 1, count, "only a distinct duplicate may be persisted")

	var matchedVideoID int64
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT video_id FROM duplicate_match;`).Scan(&matchedVideoID))
	require.Equal(t, otherVideo.ID, matchedVideoID, "the self-observed video must not be recorded")

	var matchedFingerprintID int64
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT matched_fingerprint_id FROM duplicate_match;`).Scan(&matchedFingerprintID))
	require.Equal(t, distinctRefID, matchedFingerprintID,
		"the recorded reference must be the distinct one, never the evaluated video's own row")
	require.NotEqual(t, selfRefID, matchedFingerprintID)

	// The same-ID row keeps its matching eligibility for other videos.
	var isRef int
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = 'VSELF';`).Scan(&isRef))
	require.Equal(t, 1, isRef, "a self-match outcome must not revoke eligibility")

	// Duplicate evaluation creates no channel-discovery or routing evidence.
	var channels int
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channel;`).Scan(&channels))
	assert.Zero(t, channels, "detection must not create discovery-credit evidence")
}
