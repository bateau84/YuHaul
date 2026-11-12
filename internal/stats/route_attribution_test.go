package stats

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRouteStats_K21_ExistingNumbersUnchanged is ARS-006 K21. On data where
// every action was attempted by its decision's run (all existing data), the
// run-attributed query gives the same numbers as the previous
// decision-run-joined query, which this test still runs as the reference.
func TestRouteStats_K21_ExistingNumbersUnchanged(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	at := time.Now().UTC().Add(-time.Minute)
	routeID := seedRoute(t, ctx, db, "Music", 1)
	outcomes := [][]string{
		{"succeeded", ""}, {"failed", ""}, {"deferred", "quota_exhausted"},
		{"not_attempted", "already_completed"}, {"succeeded", ""},
	}
	var runs []int64
	for r := 0; r < 2; r++ {
		runID := seedRun(t, ctx, db)
		runs = append(runs, runID)
		for i, o := range outcomes {
			videoKey := seedVideo(t, ctx, db, string(rune('a'+r))+string(rune('0'+i)), "UC1", runID)
			decisionKey := seedMatchedDecision(t, ctx, db, videoKey, runID, routeID)
			seedActionDisposition(t, ctx, db, decisionKey, runID, videoKey, "PLa", o[0], o[1], &at)
		}
		// A matched decision with no action at all must still count as matched.
		videoKey := seedVideo(t, ctx, db, string(rune('a'+r))+"none", "UC1", runID)
		seedMatchedDecision(t, ctx, db, videoKey, runID, routeID)
	}

	for _, runID := range runs {
		var oldSucceeded, oldFailed, oldExecuted int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(CASE WHEN ea.outcome = 'succeeded' THEN 1 ELSE 0 END), 0),
			        COALESCE(SUM(CASE WHEN ea.outcome = 'failed' THEN 1 ELSE 0 END), 0),
			        COALESCE(SUM(CASE WHEN ea.outcome IN ('succeeded', 'failed') THEN 1 ELSE 0 END), 0)
			 FROM route_decision rd
			 LEFT JOIN executed_action ea ON ea.route_decision_id = rd.id
			 WHERE rd.run_id = ? AND rd.route_id = ? AND rd.decision = 'matched';`,
			runID, routeID).Scan(&oldSucceeded, &oldFailed, &oldExecuted))

		stats, err := service.RouteStats(ctx, runID)
		require.NoError(t, err)
		require.Len(t, stats, 1)
		assert.Equal(t, 6, stats[0].VideosMatched)
		assert.Equal(t, 2, stats[0].ActionsSucceeded)
		assert.Equal(t, 1, stats[0].ActionsFailed)
		assert.Equal(t, 3, stats[0].ActionsExecuted)
		assert.Equal(t, oldSucceeded, stats[0].ActionsSucceeded)
		assert.Equal(t, oldFailed, stats[0].ActionsFailed)
		assert.Equal(t, oldExecuted, stats[0].ActionsExecuted)
	}
}

// TestRouteStats_ContinuationCountsForSendingRun proves that an attempt made
// by a later run under an earlier run's decision counts for the sending run
// under the decision's route, and not for the decision's run (ARS-006 §8.2).
func TestRouteStats_ContinuationCountsForSendingRun(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	at := time.Now().UTC().Add(-time.Minute)
	routeID := seedRoute(t, ctx, db, "Music", 1)
	run1 := seedRun(t, ctx, db)
	run2 := seedRun(t, ctx, db)

	videoKey := seedVideo(t, ctx, db, "cv1", "UC1", run1)
	decisionKey := seedMatchedDecision(t, ctx, db, videoKey, run1, routeID)
	seedActionDisposition(t, ctx, db, decisionKey, run1, videoKey, "PLa", "succeeded", "", &at)
	// Run 2 finishes the missing addition under run 1's decision.
	seedActionDisposition(t, ctx, db, decisionKey, run2, videoKey, "PLb", "succeeded", "", &at)

	first, err := service.RouteStats(ctx, run1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, 1, first[0].VideosMatched)
	assert.Equal(t, 1, first[0].ActionsExecuted, "run 1 counts only the send it made")

	second, err := service.RouteStats(ctx, run2)
	require.NoError(t, err)
	require.Len(t, second, 1, "the continuing run reports the route it sent under")
	assert.Equal(t, 1, second[0].ActionsSucceeded)
	assert.Equal(t, 1, second[0].ActionsExecuted)
}
