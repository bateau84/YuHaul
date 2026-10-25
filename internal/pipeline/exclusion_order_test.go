package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/youtube"
)

// Only YouTube is faked: discovery, filtering, detection, routing and storage
// all use their production implementations.
func exclusionDiscovery(t *testing.T, deps *exclusionDeps) *discovery.Engine {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items any
		switch r.URL.Path {
		case "/subscriptions":
			items = []any{map[string]any{"snippet": map[string]any{"resourceId": map[string]any{"channelId": "ch1"}}}}
		case "/channels":
			items = []any{map[string]any{"id": "ch1", "contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": "uploads"}}}}
		case "/playlistItems":
			items = []any{map[string]any{"snippet": map[string]any{"resourceId": map[string]any{"videoId": "candidate"}, "channelId": "ch1", "publishedAt": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)}}}
		case "/videos":
			items = []any{map[string]any{"id": "candidate", "snippet": map[string]any{"title": "Jazz Session", "channelId": "ch1", "publishedAt": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)}, "contentDetails": map[string]any{"duration": "PT3M"}, "status": map[string]any{"privacyStatus": "public"}}}
		default:
			t.Errorf("unexpected YouTube request: %s", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"items": items}); err != nil {
			t.Errorf("encode YouTube response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return discovery.NewEngine(&fakeTokenSource{}, deps.machine, quota.New(deps.db), deps.db,
		discovery.WithClientFactory(func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) *youtube.Client {
			return youtube.NewClient(ctx, token, youtube.WithBaseURL(server.URL), youtube.WithRecorder(recorder))
		}))
}

func addExclusionOrderRoute(t *testing.T, deps *exclusionDeps, order int, excludes, enabled bool) router.Route {
	t.Helper()
	r := router.Route{Name: "route", EvaluationOrder: order, IsEnabled: enabled, ConditionComposition: "AND",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldChannelID, Operator: router.ConditionOperatorEquals, Value: "ch1"}},
		Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLtest"}},
	}
	if excludes {
		r.Exclusions = []router.RouteExclusion{{ExclusionType: router.ExclusionTypeChannel, ExclusionValue: "ch1"}}
	}
	_, err := deps.routerStore.CreateRoute(context.Background(), &r)
	require.NoError(t, err)
	return r
}

func TestExclusionBeforeDuplicate_Production(t *testing.T) {
	for _, tc := range []struct {
		name                                                                                     string
		allExcluded, reference, brokenDetector, noRoutes, disabledSecond, noExclusions, nonmatch bool
		wantStatus                                                                               state.RunStatus
		wantExcluded, wantAccepted, wantRejected, wantMatches                                    int
	}{
		{name: "all excluded matching reference", allExcluded: true, reference: true, wantStatus: state.RunStatusCompleted, wantExcluded: 1},
		{name: "all excluded bypasses failing lookup", allExcluded: true, brokenDetector: true, wantStatus: state.RunStatusCompleted, wantExcluded: 1},
		{name: "partial exclusion still detects", reference: true, wantStatus: state.RunStatusCompleted, wantAccepted: 1, wantMatches: 1},
		{name: "partial exclusion still acts without match", wantStatus: state.RunStatusCompleted, wantAccepted: 1},
		{name: "partial exclusion plus nonmatch", nonmatch: true, wantStatus: state.RunStatusCompleted, wantRejected: 1},
		{name: "duplicate continues to partial exclusion plus nonmatch", reference: true, nonmatch: true, wantStatus: state.RunStatusCompleted, wantRejected: 1, wantMatches: 1},
		{name: "partial exclusion reaches failing lookup", brokenDetector: true, wantStatus: state.RunStatusFailed},
		{name: "no exclusions still detects", noExclusions: true, reference: true, wantStatus: state.RunStatusCompleted, wantAccepted: 1, wantMatches: 1},
		{name: "no enabled routes is not exclusion", noRoutes: true, brokenDetector: true, wantStatus: state.RunStatusFailed},
		{name: "disabled route cannot rescue exclusion", brokenDetector: true, disabledSecond: true, wantStatus: state.RunStatusCompleted, wantExcluded: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			deps, cleanup := setupExclusionPipeline(t)
			defer cleanup()
			if !tc.noRoutes {
				addExclusionOrderRoute(t, deps, 1, !tc.noExclusions, true)
				second := addExclusionOrderRoute(t, deps, 2, tc.allExcluded, !tc.disabledSecond)
				if tc.nonmatch {
					second.Conditions[0].Value = "other-channel"
					require.NoError(t, deps.routerStore.UpdateRoute(ctx, &second))
				}
			}
			_, err := duplicate.SaveFingerprint(ctx, deps.db, "candidate", "imported", "user_import", "Jazz Session", true, nil)
			require.NoError(t, err)
			if tc.reference {
				_, err := duplicate.SaveFingerprint(ctx, deps.db, "reference", "other", "user_import", "Jazz Session", true, nil)
				require.NoError(t, err)
			}
			if tc.brokenDetector {
				// A real detector with an unavailable database fails on any lookup.
				brokenDB, _, _, _, closeDB := setupPipelineTest(t)
				closeDB()
				deps.pipeline.detector = duplicate.NewDetector(brokenDB)
				_, err := deps.pipeline.detector.Detect(ctx, "candidate", "Jazz Session")
				require.Error(t, err, "fault must be at the real lookup boundary")
			}
			deps.pipeline.discovery = exclusionDiscovery(t, deps)
			run, err := deps.pipeline.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			events := deps.pipeline.broadcast.Subscribe(run.ID)
			defer deps.pipeline.broadcast.Unsubscribe(run.ID, events)
			deps.pipeline.Execute(ctx, run.ID)
			meta, metaErr := deps.pipeline.meta.GetDisplayMetadata(ctx, "candidate")
			require.NoError(t, metaErr)
			require.NotNil(t, meta.Title)
			require.Equal(t, "Jazz Session", *meta.Title)
			after, err := deps.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			if after.InterruptReason != nil {
				t.Log(*after.InterruptReason)
			}
			require.Equal(t, tc.wantStatus, after.Status, "%v", after.InterruptReason)
			require.Equal(t, 1, after.VideosDiscovered)
			require.Equal(t, tc.wantExcluded, after.VideosExcluded)
			require.Equal(t, tc.wantAccepted, after.VideosAccepted)
			require.Equal(t, tc.wantRejected, after.VideosRejected)
			var matches int
			require.NoError(t, deps.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM duplicate_match").Scan(&matches))
			require.Equal(t, tc.wantMatches, matches)
			var eligible bool
			require.NoError(t, deps.db.QueryRowContext(ctx, "SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = 'candidate'").Scan(&eligible))
			require.True(t, eligible, "exclusion must not revoke an imported same-ID reference")
			videos, err := deps.pipeline.listRunVideos(ctx, run.ID)
			require.NoError(t, err)
			require.Len(t, videos, 1)
			if tc.wantStatus == state.RunStatusCompleted {
				wantOutcome := state.FinalOutcomeRejected
				if tc.wantAccepted > 0 {
					wantOutcome = state.FinalOutcomeAccepted
				} else if tc.wantExcluded > 0 {
					wantOutcome = state.FinalOutcomeExcluded
				}
				require.Equal(t, wantOutcome, videos[0].FinalOutcome)
				videoEvents, completedEvents := 0, 0
				for len(events) > 0 {
					event := <-events
					switch event.Type {
					case EventRunVideo:
						var data videoPayload
						require.NoError(t, json.Unmarshal(event.Payload, &data))
						require.Equal(t, string(wantOutcome), data.Outcome)
						require.Equal(t, tc.wantAccepted > 0, data.MatchedRoute)
						videoEvents++
					case EventRunCompleted:
						var data completedPayload
						require.NoError(t, json.Unmarshal(event.Payload, &data))
						require.Equal(t, []int{tc.wantAccepted, tc.wantRejected, tc.wantExcluded}, []int{data.VideosAccepted, data.VideosRejected, data.VideosExcluded})
						completedEvents++
					}
				}
				require.Equal(t, 1, videoEvents)
				require.Equal(t, 1, completedEvents)
			}
			decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, videos[0].ID, run.ID)
			require.NoError(t, err)
			if tc.wantExcluded > 0 {
				require.Len(t, decisions, enabledRouteCountMust(t, deps))
				for _, d := range decisions {
					require.Equal(t, state.RouteDecisionSkipped, d.Decision)
					require.Equal(t, skipReasonPtr(state.SkipReasonExclusion), d.SkipReason)
					audits, err := deps.stateStore.GetConditionAuditsForDecision(ctx, d.ID)
					require.NoError(t, err)
					require.Empty(t, audits)
				}
				actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, videos[0].ID)
				require.NoError(t, err)
				require.Empty(t, actions)
			} else if tc.wantRejected > 0 {
				require.Len(t, decisions, 2)
				require.Equal(t, skipReasonPtr(state.SkipReasonExclusion), decisions[0].SkipReason)
				require.Equal(t, state.RouteDecisionNotMatched, decisions[1].Decision)
				actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, videos[0].ID)
				require.NoError(t, err)
				require.Empty(t, actions)
			} else if tc.wantAccepted > 0 {
				require.Len(t, decisions, 2)
				if !tc.noExclusions {
					require.Equal(t, skipReasonPtr(state.SkipReasonExclusion), decisions[0].SkipReason)
				}
				require.Equal(t, state.RouteDecisionMatched, decisions[1].Decision)
			}
		})
	}
}

func TestExclusionBeforeDuplicate_ResumePartialExclusionWrite(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupExclusionPipeline(t)
	defer cleanup()
	addExclusionOrderRoute(t, deps, 1, true, true)
	second := addExclusionOrderRoute(t, deps, 2, true, true)
	runID := createInterruptedRunAtExclusion(t, ctx, deps)
	v := discoverVideo(t, ctx, deps, runID, "candidate", "ch1", "Jazz Session")
	// Fail after the first durable decision, before the outcome transition.
	_, err := deps.db.ExecContext(ctx, `CREATE TRIGGER interrupt_exclusion BEFORE INSERT ON route_decision
		WHEN NEW.route_id = `+fmt.Sprint(second.ID)+` BEGIN SELECT RAISE(ABORT, 'interrupted exclusion'); END`)
	require.NoError(t, err)
	_, err = deps.pipeline.phaseExclusion(ctx, runID, []*state.Video{v})
	require.ErrorContains(t, err, "interrupted exclusion")
	_, err = deps.db.ExecContext(ctx, "DROP TRIGGER interrupt_exclusion")
	require.NoError(t, err)
	brokenDB, _, _, _, closeDB := setupPipelineTest(t)
	closeDB()
	deps.pipeline.detector = duplicate.NewDetector(brokenDB)
	deps.pipeline.Execute(ctx, runID)
	run, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, run.Status)
	require.Equal(t, 1, run.VideosExcluded)
	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
	require.NoError(t, err)
	require.Len(t, decisions, 2)
}

func enabledRouteCountMust(t *testing.T, deps *exclusionDeps) int {
	t.Helper()
	routes, err := deps.pipeline.router.Routes(context.Background())
	require.NoError(t, err)
	return enabledRouteCount(routes)
}

func TestExclusionBeforeDuplicate_Resume(t *testing.T) {
	for _, phase := range []Phase{PhaseExclusion, PhaseDuplicateDetect, PhaseRouting} {
		for _, recorded := range []bool{false, true} {
			t.Run(string(phase)+map[bool]string{false: "/unrecorded", true: "/recorded"}[recorded], func(t *testing.T) {
				ctx := context.Background()
				deps, cleanup := setupExclusionPipeline(t)
				defer cleanup()
				addExclusionOrderRoute(t, deps, 1, true, true)
				addExclusionOrderRoute(t, deps, 2, true, true)
				runID := createInterruptedRunAtExclusion(t, ctx, deps)
				v := discoverVideo(t, ctx, deps, runID, "candidate", "ch1", "Jazz Session")
				if recorded {
					survivors, err := deps.pipeline.phaseExclusion(ctx, runID, []*state.Video{v})
					require.NoError(t, err)
					require.Empty(t, survivors)
				}
				require.NoError(t, deps.stateStore.SetRunPhase(ctx, runID, string(phase)))
				brokenDB, _, _, _, closeDB := setupPipelineTest(t)
				closeDB()
				deps.pipeline.detector = duplicate.NewDetector(brokenDB)
				deps.pipeline.Execute(ctx, runID)
				run, err := deps.stateStore.GetRun(ctx, runID)
				require.NoError(t, err)
				require.Equal(t, state.RunStatusCompleted, run.Status)
				require.Equal(t, 1, run.VideosExcluded)
				decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
				require.NoError(t, err)
				require.Len(t, decisions, 2, "recovery must not duplicate exclusion evidence")
			})
		}
	}
}

func TestExclusionBeforeDuplicate_Historical(t *testing.T) {
	for _, mode := range []string{"replay", "backtest"} {
		for _, tc := range []struct {
			name                                   string
			allExcluded, reference, brokenDetector bool
			wantDecisions                          int
		}{
			{name: "all excluded matching reference", allExcluded: true, reference: true, wantDecisions: 2},
			{name: "all excluded bypasses failing lookup", allExcluded: true, brokenDetector: true, wantDecisions: 2},
			{name: "partial exclusion still detects", reference: true, wantDecisions: 2},
			{name: "partial exclusion reaches failing lookup", brokenDetector: true},
			{name: "partial exclusion still evaluates without match", wantDecisions: 2},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				deps, cleanup := setupReplayPipeline(t)
				defer cleanup()
				prodID, err := deps.stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
				require.NoError(t, err)
				v := createRetainedVideo(t, ctx, deps, prodID, "candidate", "ch1", "Jazz Session")
				// Preserve imported same-ID eligibility even for excluded observations.
				_, err = duplicate.SaveFingerprint(ctx, deps.db, "candidate", "ch1", "user_import", "Jazz Session", true, nil)
				require.NoError(t, err)
				if tc.reference {
					_, err = duplicate.SaveFingerprint(ctx, deps.db, "reference", "other", "user_import", "Jazz Session", true, nil)
					require.NoError(t, err)
				}
				deps.pipeline.detector = duplicate.NewDetector(deps.db)
				if tc.brokenDetector {
					brokenDB, _, _, _, closeDB := setupPipelineTest(t)
					closeDB()
					deps.pipeline.detector = duplicate.NewDetector(brokenDB)
				}
				first := proposalRoute(proposalTestUUID, "excluded", "Jazz")
				first.IsFinal = false
				first.Exclusions = []config.SnapshotExclusion{{ExclusionType: "channel", ExclusionValue: "ch1"}}
				second := proposalRoute("3f6b9a7c-5d8e-4c3b-a1f0-9e8d7c6b5a49", "other", "Jazz")
				second.EvaluationOrder = 2
				if tc.allExcluded {
					second.Exclusions = first.Exclusions
				}
				run, err := deps.pipeline.Backtest(ctx, proposalSnapshot(first, second))
				require.NoError(t, err)
				if mode == "replay" {
					run, err = deps.pipeline.Replay(ctx, run.ConfigVersionID)
					require.NoError(t, err)
					deps.pipeline.ExecuteReplay(ctx, run.ID)
				} else {
					deps.pipeline.ExecuteBacktest(ctx, run.ID)
				}
				after, err := deps.stateStore.GetRun(ctx, run.ID)
				require.NoError(t, err)
				if tc.brokenDetector && !tc.allExcluded {
					require.NotNil(t, after.InterruptReason)
					require.Contains(t, *after.InterruptReason, "database is closed")
				} else {
					require.Nil(t, after.InterruptReason)
					require.Equal(t, state.RunStatusCompleted, after.Status)
				}
				decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, run.ID)
				require.NoError(t, err)
				require.Len(t, decisions, tc.wantDecisions)
				if tc.allExcluded {
					require.Equal(t, 1, after.VideosExcluded)
					for _, d := range decisions {
						require.Equal(t, skipReasonPtr(state.SkipReasonExclusion), d.SkipReason)
						audits, err := deps.stateStore.GetConditionAuditsForDecision(ctx, d.ID)
						require.NoError(t, err)
						require.Empty(t, audits)
					}
				} else if tc.wantDecisions > 0 {
					require.Equal(t, skipReasonPtr(state.SkipReasonExclusion), decisions[0].SkipReason)
					require.Equal(t, state.RouteDecisionMatched, decisions[1].Decision)
				}
				productionVideo, err := deps.stateStore.GetVideo(ctx, v.ID)
				require.NoError(t, err)
				require.Equal(t, v.ProcessingState, productionVideo.ProcessingState)
				require.Equal(t, v.FinalOutcome, productionVideo.FinalOutcome)
				for _, query := range []string{
					"SELECT COUNT(*) FROM duplicate_match",
					"SELECT COUNT(*) FROM channel",
					"SELECT COUNT(*) FROM executed_action WHERE outcome != 'not_attempted'",
				} {
					var count int
					require.NoError(t, deps.db.QueryRowContext(ctx, query).Scan(&count))
					require.Zero(t, count, query)
				}
				var eligible bool
				require.NoError(t, deps.db.QueryRowContext(ctx, "SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = 'candidate'").Scan(&eligible))
				require.True(t, eligible)
			})
		}
	}
}
