package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// Real SQLite -> selected snapshot -> detector -> router -> simulation ->
// completion/events. Metadata is cached input, not a substituted evaluator.
func TestSimulationDuplicateContinuationResults(t *testing.T) {
	for _, mode := range []string{"dry-run", "replay", "backtest"} {
		for _, tc := range []struct {
			name                          string
			partial, nonmatch, all, final bool
			wantDecisions, wantActions    int
			outcome                       state.VideoFinalOutcome
		}{
			{name: "two matching routes count once", wantDecisions: 2, wantActions: 2, outcome: state.FinalOutcomeAccepted},
			{name: "final stops later route", final: true, wantDecisions: 2, wantActions: 1, outcome: state.FinalOutcomeAccepted},
			{name: "partial exclusion plus match", partial: true, wantDecisions: 2, wantActions: 1, outcome: state.FinalOutcomeAccepted},
			{name: "partial exclusion plus nonmatch", partial: true, nonmatch: true, wantDecisions: 2, outcome: state.FinalOutcomeRejected},
			{name: "all excluded", all: true, wantDecisions: 2, outcome: state.FinalOutcomeExcluded},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				deps, cleanup := setupReplayPipeline(t)
				defer cleanup()
				prodID, err := deps.stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
				require.NoError(t, err)
				video := createRetainedVideo(t, ctx, deps, prodID, "candidate", "ch1", "jazz live session")
				_, err = duplicate.SaveFingerprint(ctx, deps.db, "reference", "imported", "user_import", "jazz live session", true, nil)
				require.NoError(t, err)
				deps.pipeline.detector = duplicate.NewDetector(deps.db)
				match, err := deps.pipeline.detector.Detect(ctx, video.VideoID, "jazz live session")
				require.NoError(t, err)
				require.NotNil(t, match, "prove this fixture actually is a distinct duplicate")
				first := proposalRoute(proposalTestUUID, "first", "jazz")
				first.IsFinal = tc.final
				second := proposalRoute("3f6b9a7c-5d8e-4c3b-a1f0-9e8d7c6b5a49", "second", "jazz")
				second.EvaluationOrder = 2
				if tc.partial || tc.all {
					first.Exclusions = []config.SnapshotExclusion{{ExclusionType: "channel", ExclusionValue: "ch1"}}
				}
				if tc.all {
					second.Exclusions = first.Exclusions
				}
				if tc.nonmatch {
					second.Conditions[0].Value = "rock"
				}
				proposal := proposalSnapshot(first, second)
				var run *state.Run
				if mode == "dry-run" {
					run, err = deps.pipeline.DryRun(ctx, proposal)
				} else {
					run, err = deps.pipeline.Backtest(ctx, proposal)
				}
				require.NoError(t, err)
				if mode == "replay" {
					// Select an immutable old skip snapshot, not a normalized copy.
					cv, err := deps.pipeline.configStore.Get(ctx, *run.ConfigVersionID)
					require.NoError(t, err)
					cv.Snapshot.DuplicateBehavior = "skip"
					legacyID, err := deps.pipeline.configStore.Create(ctx, cv.Snapshot, "legacy")
					require.NoError(t, err)
					run, err = deps.pipeline.Replay(ctx, &legacyID)
					require.NoError(t, err)
				}
				before := simulationProductionRows(t, deps.db, prodID)
				// Completion is externally visible to HTTP pollers. Its counters
				// must already be final when completed status is published.
				_, err = deps.db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER completed_counts BEFORE UPDATE OF status ON run
					WHEN NEW.id = %d AND NEW.status = 'completed' AND NEW.videos_discovered != 1
					BEGIN SELECT RAISE(ABORT, 'completion published before counters'); END`, run.ID))
				require.NoError(t, err)
				events := deps.pipeline.broadcast.Subscribe(run.ID)
				defer deps.pipeline.broadcast.Unsubscribe(run.ID, events)
				switch mode {
				case "dry-run":
					deps.pipeline.ExecuteDryRun(ctx, run.ID)
				case "replay":
					deps.pipeline.ExecuteReplay(ctx, run.ID)
				case "backtest":
					deps.pipeline.ExecuteBacktest(ctx, run.ID)
				}
				after, err := deps.stateStore.GetRun(ctx, run.ID)
				require.NoError(t, err)
				require.Equal(t, state.RunStatusCompleted, after.Status)
				require.Equal(t, 1, after.VideosDiscovered)
				wantCounts := []int{0, 0, 0}
				switch tc.outcome {
				case state.FinalOutcomeAccepted:
					wantCounts[0] = 1
				case state.FinalOutcomeRejected:
					wantCounts[1] = 1
				case state.FinalOutcomeExcluded:
					wantCounts[2] = 1
				}
				require.Equal(t, wantCounts, []int{after.VideosAccepted, after.VideosRejected, after.VideosExcluded})
				decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
				require.NoError(t, err)
				require.Len(t, decisions, tc.wantDecisions)
				if tc.final {
					require.Equal(t, state.RouteDecisionSkipped, decisions[1].Decision)
					require.Equal(t, skipReasonPtr(state.SkipReasonFinalRouteMatched), decisions[1].SkipReason)
					audits, err := deps.stateStore.GetConditionAuditsForDecision(ctx, decisions[1].ID)
					require.NoError(t, err)
					require.Empty(t, audits, "later route conditions are not evaluated")
				}
				actions, err := deps.stateStore.GetExecutedActionsForVideo(ctx, video.ID)
				require.NoError(t, err)
				require.Len(t, actions, tc.wantActions)
				for _, a := range actions {
					require.Equal(t, state.ActionOutcomeNotAttempted, a.Outcome)
					require.Equal(t, run.ID, a.RunID)
				}
				foundVideoEvent := false
				for len(events) > 0 {
					ev := <-events
					if ev.Type == EventRunVideo {
						var data videoPayload
						require.NoError(t, json.Unmarshal(ev.Payload, &data))
						require.Equal(t, string(tc.outcome), data.Outcome)
						require.Equal(t, tc.outcome == state.FinalOutcomeAccepted, data.MatchedRoute)
						foundVideoEvent = true
					}
				}
				require.True(t, foundVideoEvent)
				require.Equal(t, before, simulationProductionRows(t, deps.db, prodID), "simulation must leave production and history rows unchanged")
			})
		}
	}
}

func simulationProductionRows(t *testing.T, db *sql.DB, prodID int64) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, query := range []string{
		`SELECT * FROM video ORDER BY id`,
		`SELECT * FROM duplicate_match ORDER BY id`,
		`SELECT * FROM title_fingerprint ORDER BY id`,
		`SELECT * FROM channel ORDER BY id`,
		`SELECT * FROM route ORDER BY id`,
		`SELECT * FROM config_version ORDER BY id`,
		`SELECT * FROM run WHERE id = ?`,
		`SELECT * FROM route_decision WHERE run_id = ? ORDER BY id`,
		`SELECT * FROM executed_action WHERE run_id = ? ORDER BY id`,
		`SELECT * FROM quota_ledger ORDER BY id`,
	} {
		args := []any{}
		if query == `SELECT * FROM run WHERE id = ?` || query == `SELECT * FROM route_decision WHERE run_id = ? ORDER BY id` || query == `SELECT * FROM executed_action WHERE run_id = ? ORDER BY id` {
			args = append(args, prodID)
		}
		rows, err := db.Query(query, args...)
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		var contents [][]any
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			require.NoError(t, rows.Scan(pointers...))
			contents = append(contents, values)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		encoded, err := json.Marshal(contents)
		require.NoError(t, err)
		result[query] = string(encoded)
	}
	return result
}
