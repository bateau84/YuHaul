package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunVideoInspectionMembershipAndProjection(t *testing.T) {
	for _, mode := range []RunType{RunTypeReplay, RunTypeBacktest, RunTypeDryRun} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			m := newTestMachine(t)
			origin := seedRun(t, m)
			runID, err := m.store.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: mode, Status: RunStatusCompleted})
			require.NoError(t, err)
			route1 := seedRoute(t, m.store.db)
			_, err = m.store.db.ExecContext(ctx, `UPDATE route SET evaluation_order = 0 WHERE id = ?`, route1)
			require.NoError(t, err)
			route2 := seedRoute(t, m.store.db)
			for _, tc := range []struct {
				name    string
				first   RouteDecisionType
				outcome VideoFinalOutcome
			}{
				{"matched", RouteDecisionMatched, FinalOutcomeAccepted},
				{"partial exclusion nonmatch", RouteDecisionNotMatched, FinalOutcomeRejected},
				{"all excluded", RouteDecisionSkipped, FinalOutcomeExcluded},
			} {
				videoID, err := m.store.CreateVideo(ctx, &Video{VideoID: tc.name, ChannelID: "channel", DiscoveredAt: timeNow(), DiscoveredInRunID: origin,
					ProcessingState: VideoStateFailed, FinalOutcome: FinalOutcomeFailed})
				require.NoError(t, err)
				skip := SkipReasonExclusion
				for i, route := range []int64{route1, route2} {
					d := &RouteDecision{VideoID: videoID, RunID: runID, RouteID: route, EvaluationOrder: i, Decision: RouteDecisionSkipped, SkipReason: &skip}
					if i == 0 && tc.first != RouteDecisionSkipped {
						d.Decision, d.SkipReason = tc.first, nil
					}
					_, err := m.store.CreateRouteDecision(ctx, d)
					require.NoError(t, err)
				}
				t.Run(tc.name, func(t *testing.T) {
					videos, total, err := m.store.ListRunVideos(ctx, runID, 1, 0, tc.outcome)
					require.NoError(t, err)
					// Each category is unique even though it has two decision rows.
					require.EqualValues(t, 1, total)
					require.Len(t, videos, 1)
					require.Equal(t, tc.outcome, videos[0].FinalOutcome)
					detail, err := m.store.GetVideoByExternalIDInRun(ctx, runID, tc.name)
					require.NoError(t, err)
					require.Equal(t, videos[0], *detail)
					require.Equal(t, origin, detail.DiscoveredInRunID)
					persisted, err := m.store.GetVideo(ctx, videoID)
					require.NoError(t, err)
					require.Equal(t, FinalOutcomeFailed, persisted.FinalOutcome)
				})
			}
			videos, total, err := m.store.ListRunVideos(ctx, runID, 1, 2, "")
			require.NoError(t, err)
			require.EqualValues(t, 3, total)
			require.Len(t, videos, 1)
			require.Equal(t, "matched", videos[0].VideoID)
			_, err = m.store.GetVideoByExternalIDInRun(ctx, runID+100, "matched")
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}
