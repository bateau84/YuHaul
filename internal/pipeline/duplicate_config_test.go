package pipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// Exercise the persisted settings -> snapshot -> real detector -> recorded
// evidence path. Changing settings after initiation must not change the run.
func TestDuplicatePhaseUsesRunThreshold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold float64
		title     string
		wantMatch bool
	}{
		{"strict fuzzy", 0.99, "jazz live session today", false},
		{"configured fuzzy", 0.55, "jazz live session today", true},
		{"zero is a configured threshold", 0, "jazz live session today", true},
		{"exact still matches", 0.99, "jazz live session recording", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			deps, cleanup := setupReplayPipeline(t)
			defer cleanup()
			deps.pipeline.detector = duplicate.NewDetector(deps.db)
			svc := config.NewService(deps.routerStore, config.NewStore(deps.db))
			require.NoError(t, svc.SetDuplicateThreshold(ctx, tc.threshold))
			run, err := deps.pipeline.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			require.NoError(t, svc.SetDuplicateThreshold(ctx, 0.1))
			_, err = duplicate.SaveFingerprint(ctx, deps.db, "reference", "channel", "user_import",
				"jazz live session recording", true, nil)
			require.NoError(t, err)
			video := createRetainedVideo(t, ctx, deps, run.ID, "candidate", "channel", tc.title)
			require.NoError(t, deps.pipeline.phaseDuplicateDetection(ctx, run.ID, []*state.Video{video}))
			// A run-specific threshold must not mutate the long-lived detector
			// shared with other concurrent production and historical runs.
			sharedMatch, err := deps.pipeline.detector.Detect(ctx, "another-candidate", "jazz live session today")
			require.NoError(t, err)
			require.NotNil(t, sharedMatch)
			require.Equal(t, duplicate.DefaultThreshold, sharedMatch.ThresholdUsed)
			var count int
			require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM duplicate_match WHERE video_id = ?`, video.ID).Scan(&count))
			if !tc.wantMatch {
				require.Zero(t, count)
				return
			}
			require.Equal(t, 1, count)
			var threshold, similarity float64
			require.NoError(t, deps.db.QueryRowContext(ctx,
				`SELECT threshold_used, similarity_score FROM duplicate_match WHERE video_id = ?`, video.ID).Scan(&threshold, &similarity))
			require.Equal(t, tc.threshold, threshold)
			if tc.name != "exact still matches" {
				require.InDelta(t, 0.6, similarity, 0.00001)
			}
		})
	}
}

func TestHistoricalEvaluationUsesSelectedThreshold(t *testing.T) {
	for _, mode := range []string{"replay", "backtest"} {
		for _, threshold := range []float64{0.55, 0.99} {
			t.Run(fmt.Sprintf("%s/%.2f", mode, threshold), func(t *testing.T) {
				ctx := context.Background()
				deps, cleanup := setupReplayPipeline(t)
				defer cleanup()
				deps.pipeline.detector = duplicate.NewDetector(deps.db)
				route := createReplayRouteWithAction(t, ctx, deps, "Jazz", "jazz", "PLjazz")
				prodID, err := deps.stateStore.CreateRun(ctx, &state.Run{
					StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning,
				})
				require.NoError(t, err)
				video := createRetainedVideo(t, ctx, deps, prodID, "candidate", "channel", "jazz live session today")
				_, err = duplicate.SaveFingerprint(ctx, deps.db, "reference", "channel", "user_import",
					"jazz live session recording", true, nil)
				require.NoError(t, err)
				svc := config.NewService(deps.routerStore, config.NewStore(deps.db))
				require.NoError(t, svc.SetDuplicateThreshold(ctx, threshold))
				selectedID, err := svc.ActiveVersionID(ctx)
				require.NoError(t, err)
				// Current settings and the shared detector disagree with the strict
				// snapshot, which must still allow the candidate through routing.
				require.NoError(t, svc.SetDuplicateThreshold(ctx, 0.1))
				var run *state.Run
				if mode == "replay" {
					run, err = deps.pipeline.Replay(ctx, selectedID)
				} else {
					run, err = deps.pipeline.Backtest(ctx, &config.ProposalSnapshot{
						DuplicateThreshold: threshold,
						Routes: []config.ProposalRoute{{
							ID: route.PublicID, Name: route.Name, EvaluationOrder: 1,
							IsEnabled: true, IsFinal: true, ConditionComposition: "AND",
							Conditions: []config.SnapshotCondition{{Field: "title", Operator: "contains", Value: "jazz"}},
							Actions:    []config.SnapshotAction{{ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"}},
						}},
					})
				}
				require.NoError(t, err)
				selectedDetector, err := deps.pipeline.detectorForRun(ctx, run.ID)
				require.NoError(t, err)
				match, err := selectedDetector.Detect(ctx, video.VideoID, "jazz live session today")
				require.NoError(t, err)
				if threshold == 0.99 {
					require.Nil(t, match)
				} else {
					require.NotNil(t, match)
					require.Equal(t, threshold, match.ThresholdUsed)
				}
				if mode == "replay" {
					deps.pipeline.ExecuteReplay(ctx, run.ID)
				} else {
					deps.pipeline.ExecuteBacktest(ctx, run.ID)
				}
				loaded, err := deps.stateStore.GetRun(ctx, run.ID)
				require.NoError(t, err)
				require.Equal(t, state.RunStatusCompleted, loaded.Status)
				decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
				require.NoError(t, err)
				require.Len(t, decisions, 1, "matches and nonmatches both continue to eligible routes")
				require.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
				require.Equal(t, 1, loaded.VideosAccepted)
				require.Equal(t, 1, loaded.VideosDiscovered)
				require.Zero(t, loaded.VideosRejected)
				require.Zero(t, loaded.VideosExcluded)
				actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
				require.NoError(t, err)
				require.Len(t, actions, 1)
				require.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
				var count int
				require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM duplicate_match`).Scan(&count))
				require.Zero(t, count, "historical evaluation must not write production duplicate evidence")
			})
		}
	}
}
