package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestDryRunExclusionBeforeDuplicate(t *testing.T) {
	for _, tc := range []struct {
		name           string
		allExcluded    bool
		brokenDetector bool
		threshold      float64
		wantMatch      bool
		nonmatch       bool
	}{
		{name: "all excluded matching reference", allExcluded: true, threshold: 0.55},
		{name: "all excluded bypasses failing lookup", allExcluded: true, brokenDetector: true, threshold: 0.55},
		{name: "partial exclusion reaches failing lookup", brokenDetector: true, threshold: 0.55},
		{name: "partial exclusion matches proposed threshold", threshold: 0.55, wantMatch: true},
		{name: "partial exclusion uses strict proposed threshold", threshold: 0.99},
		{name: "zero proposed threshold is retained", threshold: 0, wantMatch: true},
		{name: "partial exclusion plus eligible nonmatch is rejected once", threshold: 0.55, wantMatch: true, nonmatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			deps, cleanup := setupDryRunPipeline(t)
			defer cleanup()
			prodID, err := deps.stateStore.CreateRun(ctx, &state.Run{
				StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning,
			})
			require.NoError(t, err)
			v := createPendingVideo(t, ctx, deps, prodID, "candidate", "ch1", "jazz live session today")
			_, err = duplicate.SaveFingerprint(ctx, deps.db, "reference", "other", "user_import", "jazz live session recording", true, nil)
			require.NoError(t, err)
			// Preserve same-ID imported eligibility without treating it as a match.
			_, err = duplicate.SaveFingerprint(ctx, deps.db, "candidate", "ch1", "user_import", "jazz live session today", true, nil)
			require.NoError(t, err)
			var detectionLog bytes.Buffer
			deps.pipeline.detector = duplicate.NewDetector(deps.db, duplicate.WithLogger(slog.New(slog.NewTextHandler(&detectionLog, &slog.HandlerOptions{Level: slog.LevelDebug}))))
			if tc.brokenDetector {
				brokenDB, _, _, _, closeDB := setupPipelineTest(t)
				closeDB()
				deps.pipeline.detector = duplicate.NewDetector(brokenDB)
			}
			first := proposalRoute(proposalTestUUID, "excluded", "jazz")
			first.IsFinal = false
			first.Exclusions = []config.SnapshotExclusion{{ExclusionType: "channel", ExclusionValue: "ch1"}}
			second := proposalRoute("3f6b9a7c-5d8e-4c3b-a1f0-9e8d7c6b5a49", "eligible", "jazz")
			second.EvaluationOrder = 2
			if tc.nonmatch {
				second.Conditions[0].Value = "does not match"
			}
			second.Actions = []config.SnapshotAction{{ActionType: "playlist_insert", TargetPlaylistID: "PLjazz"}}
			if tc.allExcluded {
				second.Exclusions = first.Exclusions
			}
			proposal := proposalSnapshot(first, second)
			proposal.DuplicateThreshold = tc.threshold
			run, err := deps.pipeline.DryRun(ctx, proposal)
			require.NoError(t, err)
			// Later settings must not replace the immutable proposal threshold.
			require.NoError(t, config.NewService(deps.routerStore, config.NewStore(deps.db)).SetDuplicateThreshold(ctx, 0.1))
			deps.pipeline.ExecuteDryRun(ctx, run.ID)
			after, err := deps.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, run.ID)
			require.NoError(t, err)
			actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, v.ID)
			require.NoError(t, err)
			if tc.brokenDetector && !tc.allExcluded {
				require.Equal(t, state.RunStatusFailed, after.Status)
				require.NotNil(t, after.InterruptReason)
				require.Contains(t, *after.InterruptReason, "database is closed")
				require.Empty(t, decisions, "detection must precede conditions")
				require.Empty(t, actions)
			} else {
				require.Equal(t, state.RunStatusCompleted, after.Status)
				require.Nil(t, after.InterruptReason)
				require.Equal(t, 1, after.VideosDiscovered, "report the evaluated pending workload")
				require.Len(t, decisions, 2)
				require.Equal(t, skipReasonPtr(state.SkipReasonExclusion), decisions[0].SkipReason)
				if tc.allExcluded {
					require.Equal(t, 1, after.VideosExcluded)
					require.Equal(t, skipReasonPtr(state.SkipReasonExclusion), decisions[1].SkipReason)
					require.Empty(t, actions)
				} else if tc.nonmatch {
					require.Equal(t, 1, after.VideosRejected)
					require.Zero(t, after.VideosAccepted)
					require.Zero(t, after.VideosExcluded)
					require.Equal(t, state.RouteDecisionNotMatched, decisions[1].Decision)
					require.Empty(t, actions)
				} else {
					// Preserve existing dry-run continuation, including on a match.
					require.Equal(t, 1, after.VideosAccepted)
					require.Equal(t, state.RouteDecisionMatched, decisions[1].Decision)
					require.Len(t, actions, 1)
					require.Equal(t, run.ID, actions[0].RunID)
					require.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
				}
				for _, d := range decisions {
					if d.SkipReason != nil {
						audits, err := deps.stateStore.GetConditionAuditsForDecision(ctx, d.ID)
						require.NoError(t, err)
						require.Empty(t, audits)
					}
				}
			}
			// The real detector's existing diagnostic distinguishes a successful
			// fuzzy lookup from a no-match; no production match row is needed.
			if tc.wantMatch {
				require.Contains(t, detectionLog.String(), "fuzzy duplicate detected")
				require.Contains(t, detectionLog.String(), "matched_fingerprint_id=")
				require.Contains(t, detectionLog.String(), "threshold="+strconv.FormatFloat(tc.threshold, 'g', -1, 64))
			} else {
				require.NotContains(t, detectionLog.String(), "duplicate detected")
			}
			videoAfter, err := deps.stateStore.GetVideo(ctx, v.ID)
			require.NoError(t, err)
			require.Equal(t, v.ProcessingState, videoAfter.ProcessingState)
			require.Equal(t, v.FinalOutcome, videoAfter.FinalOutcome)
			for _, query := range []string{
				"SELECT COUNT(*) FROM duplicate_match",
				"SELECT COUNT(*) FROM channel",
				"SELECT COUNT(*) FROM executed_action WHERE outcome != 'not_attempted'",
			} {
				var count int
				require.NoError(t, deps.db.QueryRowContext(ctx, query).Scan(&count))
				require.Zero(t, count, query)
			}
			var count int
			require.NoError(t, deps.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM executed_action WHERE run_id != ?", run.ID).Scan(&count))
			require.Zero(t, count, "no production action evidence")
			require.NoError(t, deps.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM title_fingerprint WHERE is_duplicate_reference = 1").Scan(&count))
			require.Equal(t, 2, count)
		})
	}
}
