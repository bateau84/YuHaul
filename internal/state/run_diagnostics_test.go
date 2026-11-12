package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createDiagRun creates a run and returns its ID.
func createDiagRun(t *testing.T, s *Store, runType RunType) int64 {
	t.Helper()
	id, err := s.CreateRun(context.Background(), &Run{
		StartedAt: timeNow(), RunType: runType, Status: RunStatusRunning,
	})
	require.NoError(t, err)
	return id
}

// createDiagRoute creates a route with the given evaluation order.
func createDiagRoute(t *testing.T, s *Store, order int) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO route (name, evaluation_order, condition_composition)
		 VALUES (?, ?, 'AND');`, "diag-route", order)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// TestRunSelectedCountDistinguishesUnknownFromZero proves the ARD-001
// selected-input contract at the store level: an uncaptured selection reads
// as NULL (unknown), a completed empty selection reads as 0, and capture is
// write-once so the first real count is never silently overwritten.
func TestRunSelectedCountDistinguishesUnknownFromZero(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	s, err := NewStore(db)
	require.NoError(t, err)

	runID := createDiagRun(t, s, RunTypeBacktest)

	// Unknown before any capture.
	run, err := s.GetRun(ctx, runID)
	require.NoError(t, err)
	require.Nil(t, run.VideosSelected, "an uncaptured selection must read as NULL, not zero")

	// Known zero after a completed empty selection.
	require.NoError(t, s.SetRunSelectedCount(ctx, runID, 0))
	run, err = s.GetRun(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, run.VideosSelected)
	assert.Equal(t, 0, *run.VideosSelected, "a captured empty selection must read as 0, not NULL")

	// Write-once: a later capture does not overwrite the first one.
	require.NoError(t, s.SetRunSelectedCount(ctx, runID, 7))
	run, err = s.GetRun(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, run.VideosSelected)
	assert.Equal(t, 0, *run.VideosSelected, "the first captured count must not be overwritten")

	// Negative counts are rejected.
	assert.Error(t, s.SetRunSelectedCount(ctx, runID, -1))

	// A missing run is an error, not a silent success.
	err = s.SetRunSelectedCount(ctx, runID+999, 3)
	assert.ErrorIs(t, err, ErrNotFound)

	// The list projection carries the same nullable meaning.
	runs, err := s.ListRuns(ctx)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.NotNil(t, runs[0].VideosSelected)
	assert.Equal(t, 0, *runs[0].VideosSelected)
}

// TestRunEvaluatedCountsDistinctVideosForRequestedRun proves videos_evaluated
// counts distinct videos with route-decision evidence in the requested run
// only: multiple decisions for one video count once, decisions from another
// run never leak in, and videos_discovered keeps its own meaning.
func TestRunEvaluatedCountsDistinctVideosForRequestedRun(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	s, err := NewStore(db)
	require.NoError(t, err)

	runA := createDiagRun(t, s, RunTypeManual)
	runB := createDiagRun(t, s, RunTypeBacktest)
	route1 := createDiagRoute(t, s, 1)
	route2 := createDiagRoute(t, s, 2)

	newVideo := func(t *testing.T, runID int64, externalID string) int64 {
		t.Helper()
		id, err := s.CreateVideo(ctx, &Video{
			VideoID: externalID, ChannelID: "c1", DiscoveredAt: timeNow(),
			DiscoveredInRunID: runID, ProcessingState: VideoStateNotYetEvaluated,
			FinalOutcome: FinalOutcomeNotYetEvaluated,
		})
		require.NoError(t, err)
		return id
	}
	decide := func(t *testing.T, runID, videoID, routeID int64, order int) {
		t.Helper()
		_, err := s.CreateRouteDecision(ctx, &RouteDecision{
			VideoID: videoID, RunID: runID, RouteID: routeID,
			EvaluationOrder: order, Decision: RouteDecisionMatched,
		})
		require.NoError(t, err)
	}

	// Run A: three discovered videos; two carry decision evidence, one of
	// them with two decisions (two routes evaluated for the same video).
	vA1 := newVideo(t, runA, "a1")
	vA2 := newVideo(t, runA, "a2")
	newVideo(t, runA, "a3")
	decide(t, runA, vA1, route1, 1)
	decide(t, runA, vA1, route2, 2)
	decide(t, runA, vA2, route1, 1)

	// Run B: its own single decision-bearing video.
	vB := newVideo(t, runB, "b1")
	decide(t, runB, vB, route1, 1)

	// Production counter semantics are unchanged: discovered counts
	// discovered videos, not decision-bearing ones.
	require.NoError(t, s.RecomputeRunCounters(ctx, runA))

	run, err := s.GetRun(ctx, runA)
	require.NoError(t, err)
	assert.Equal(t, 3, run.VideosDiscovered, "videos_discovered must keep its production meaning")
	assert.Equal(t, 2, run.VideosEvaluated, "videos_evaluated counts distinct decision-bearing videos")

	other, err := s.GetRun(ctx, runB)
	require.NoError(t, err)
	assert.Equal(t, 1, other.VideosEvaluated, "evaluated count must be scoped to the requested run")
}

// TestRunEvaluatedIsZeroWithoutDecisionEvidence proves an evaluation gap is
// visible as zero evaluated videos rather than an inferred outcome, and that
// an old run with no captured selection keeps a NULL selected count.
func TestRunEvaluatedIsZeroWithoutDecisionEvidence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	s, err := NewStore(db)
	require.NoError(t, err)

	runID := createDiagRun(t, s, RunTypeBacktest)

	run, err := s.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, 0, run.VideosEvaluated, "a run with no decision evidence must read 0 evaluated")
	assert.Nil(t, run.VideosSelected, "a legacy run keeps a NULL selected count")
}

// TestExecutedActionOutcomeReasonRoundTrip proves the nullable action reason
// persists and projects through run-scoped reads, with NULL meaning no reason
// was captured and without introducing a new action outcome.
func TestExecutedActionOutcomeReasonRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	s, err := NewStore(db)
	require.NoError(t, err)

	runID := createDiagRun(t, s, RunTypeManual)
	otherRun := createDiagRun(t, s, RunTypeManual)
	routeID := createDiagRoute(t, s, 1)

	videoID, err := s.CreateVideo(ctx, &Video{
		VideoID: "v1", ChannelID: "c1", DiscoveredAt: timeNow(),
		DiscoveredInRunID: runID, ProcessingState: VideoStateCompleted,
		FinalOutcome: FinalOutcomeAccepted,
	})
	require.NoError(t, err)

	decisionID, err := s.CreateRouteDecision(ctx, &RouteDecision{
		VideoID: videoID, RunID: runID, RouteID: routeID,
		EvaluationOrder: 1, Decision: RouteDecisionMatched,
	})
	require.NoError(t, err)

	reason := "already_completed"
	_, err = s.CreateExecutedAction(ctx, &ExecutedAction{
		RouteDecisionID: decisionID, RunID: runID, VideoID: videoID,
		ActionType: "playlist_insert", TargetPlaylistID: "PL1",
		Outcome: ActionOutcomeNotAttempted, OutcomeReason: &reason,
	})
	require.NoError(t, err)
	_, err = s.CreateExecutedAction(ctx, &ExecutedAction{
		RouteDecisionID: decisionID, RunID: runID, VideoID: videoID,
		ActionType: "playlist_insert", TargetPlaylistID: "PL2",
		Outcome: ActionOutcomeSucceeded,
	})
	require.NoError(t, err)

	actions, err := s.GetExecutedActionsForVideoInRun(ctx, videoID, runID)
	require.NoError(t, err)
	require.Len(t, actions, 2)

	byTarget := make(map[string]ExecutedAction, len(actions))
	for _, a := range actions {
		byTarget[a.TargetPlaylistID] = a
	}
	withReason := byTarget["PL1"]
	assert.Equal(t, ActionOutcomeNotAttempted, withReason.Outcome, "the outcome vocabulary is unchanged")
	require.NotNil(t, withReason.OutcomeReason)
	assert.Equal(t, "already_completed", *withReason.OutcomeReason)
	assert.Nil(t, byTarget["PL2"].OutcomeReason, "an uncaptured reason must read as NULL")

	// Run-scoped reads never return another run's actions.
	otherActions, err := s.GetExecutedActionsForVideoInRun(ctx, videoID, otherRun)
	require.NoError(t, err)
	assert.Empty(t, otherActions)

	// The unscoped video read carries the same reason.
	all, err := s.GetExecutedActionsForVideo(ctx, videoID)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// TestFailHistoricalRunStoresFailedWithDecisionCounters proves a simulation
// (dry-run/replay/backtest) phase failure is finalized as terminal failed —
// never completed — and keeps the decision-derived simulation counter
// semantics of CompleteHistoricalRun (System Architecture §Run Recovery /
// ADR-002). It records a terminal timestamp and the failure reason.
func TestFailHistoricalRunStoresFailedWithDecisionCounters(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	s, err := NewStore(db)
	require.NoError(t, err)
	m := NewMachine(s)

	originID := createDiagRun(t, s, RunTypeManual)
	runID := createDiagRun(t, s, RunTypeBacktest)
	routeID := createDiagRoute(t, s, 1)

	videoID, err := s.CreateVideo(ctx, &Video{
		VideoID: "hist1", ChannelID: "c1", DiscoveredAt: timeNow(),
		DiscoveredInRunID: originID, ProcessingState: VideoStateNotYetEvaluated,
		FinalOutcome: FinalOutcomeNotYetEvaluated,
	})
	require.NoError(t, err)
	_, err = s.CreateRouteDecision(ctx, &RouteDecision{
		VideoID: videoID, RunID: runID, RouteID: routeID,
		EvaluationOrder: 1, Decision: RouteDecisionMatched,
	})
	require.NoError(t, err)

	failed, err := m.FailHistoricalRun(ctx, runID, "routing exploded")
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, failed.Status,
		"a simulation phase failure must end terminal failed, not completed")
	require.NotNil(t, failed.InterruptReason)
	assert.Equal(t, "diag:v1 phase=unknown operation=unknown code=unknown_error", *failed.InterruptReason)
	require.NotNil(t, failed.CompletedAt, "a failed run records when it reached its terminal state")
	assert.Equal(t, 1, failed.VideosDiscovered,
		"simulation failure keeps the decision-derived counter semantics of completion")
	assert.Equal(t, 1, failed.VideosAccepted)
	assert.Equal(t, 1, failed.VideosEvaluated)

	// A missing run is an error, never a silent success.
	_, err = m.FailHistoricalRun(ctx, runID+999, "boom")
	assert.ErrorIs(t, err, ErrNotFound)
}
