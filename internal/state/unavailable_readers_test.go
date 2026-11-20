package state

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allFinalOutcomes enumerates every final outcome the vocabulary defines.
var allFinalOutcomes = []VideoFinalOutcome{
	FinalOutcomeAccepted, FinalOutcomeRejected, FinalOutcomeExcluded,
	FinalOutcomeDeferred, FinalOutcomeFailed, FinalOutcomeNotYetEvaluated,
	FinalOutcomeUnavailableOnYouTube,
}

// TestO2UnavailableOnYouTubeIsItsOwnCounterAndFilter (ARS-005 §6.4, test O2)
// records one video per outcome and asserts every state-package reader counts
// unavailable_on_youtube in its own category, never folded into rejected,
// excluded or failed.
func TestO2UnavailableOnYouTubeIsItsOwnCounterAndFilter(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	runID := seedRun(t, m)

	for _, o := range allFinalOutcomes {
		require.True(t, o.Valid(), "outcome %q must be a valid vocabulary value", o)
		v, err := m.DiscoverVideo(ctx, runID, "yt-"+string(o), "UCchannel")
		require.NoError(t, err)

		tx, err := m.store.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		outcome := o
		var reason, message *string
		switch o {
		case FinalOutcomeDeferred:
			r := "quota_exhausted"
			reason = &r
		case FinalOutcomeFailed:
			msg := "action execution failed"
			message = &msg
		}
		require.NoError(t, m.store.RecordRunVideoOutcomeTx(ctx, tx, runID, v.ID, &outcome, reason, message, nil))
		require.NoError(t, tx.Commit())
	}

	require.NoError(t, m.store.RecomputeRunCounters(ctx, runID))

	check := func(label string, r Run) {
		t.Helper()
		assert.Equal(t, 1, r.VideosUnavailable, "%s: videos_unavailable", label)
		assert.Equal(t, 1, r.VideosAccepted, "%s: accepted", label)
		assert.Equal(t, 1, r.VideosRejected, "%s: rejected must not absorb unavailable", label)
		assert.Equal(t, 1, r.VideosExcluded, "%s: excluded must not absorb unavailable", label)
		assert.Equal(t, 1, r.VideosDeferred, "%s: deferred", label)
		assert.Equal(t, 1, r.VideosFailed, "%s: failed must not absorb unavailable", label)
		assert.Equal(t, 1, r.VideosNotYetEvaluated, "%s: not_yet_evaluated", label)
	}

	got, err := m.store.GetRun(ctx, runID)
	require.NoError(t, err)
	check("GetRun", *got)

	runs, err := m.store.ListRuns(ctx)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	check("ListRuns", runs[0])

	// Every outcome filters to exactly its own video.
	for _, o := range allFinalOutcomes {
		videos, total, err := m.store.ListRunVideos(ctx, runID, 50, 0, o)
		require.NoError(t, err)
		require.EqualValues(t, 1, total, fmt.Sprintf("filter %q", o))
		require.Len(t, videos, 1)
		assert.Equal(t, o, videos[0].FinalOutcome)
	}

	// Replay/backtest recompute has no unavailable category and resets it.
	require.NoError(t, m.store.RecomputeRunCountersFromDecisions(ctx, runID))
	got, err = m.store.GetRun(ctx, runID)
	require.NoError(t, err)
	assert.Zero(t, got.VideosUnavailable)
}
