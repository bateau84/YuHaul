//go:build integration

package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestRoutingRecovery_PartialRouteDecisions(t *testing.T) {
	for _, first := range []string{"excluded", "matched", "final"} {
		t.Run(first, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
			p := env.pipeline
			_, err := duplicate.SaveFingerprint(ctx, p.db, "imported-reference", "imported", "user_import", "Jazz Live Session", true, nil)
			require.NoError(t, err)
			rs := router.NewStore(p.db)
			secondID, err := rs.CreateRoute(ctx, &router.Route{
				Name: "Second", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "OR",
				Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "jazz"}},
				Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLsecond"}},
			})
			require.NoError(t, err)
			if first == "excluded" {
				_, err = p.db.ExecContext(ctx, `INSERT INTO route_exclusion (route_id, exclusion_type, exclusion_value) VALUES (?, 'video', 'V1')`, env.routeID)
				require.NoError(t, err)
			} else if first == "final" {
				_, err = p.db.ExecContext(ctx, `UPDATE route SET is_final = 1 WHERE id = ?`, env.routeID)
				require.NoError(t, err)
			}
			run, err := p.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			videos, err := p.phaseDiscovery(ctx, run.ID)
			require.NoError(t, err)
			videos, err = p.phaseExclusion(ctx, run.ID, videos)
			require.NoError(t, err)
			require.Len(t, videos, 1)
			require.NoError(t, p.phaseDuplicateDetection(ctx, run.ID, videos))
			var matchCount int
			require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM duplicate_match WHERE video_id = ?`, videos[0].ID).Scan(&matchCount))
			require.Equal(t, 1, matchCount, "a distinct duplicate must still reach routing and recovery")
			_, err = p.db.ExecContext(ctx, `CREATE TRIGGER interrupt_routing BEFORE INSERT ON route_decision WHEN NEW.route_id = `+itoa(int(secondID))+` BEGIN SELECT RAISE(ABORT, 'injected routing interruption'); END`)
			require.NoError(t, err)
			_, err = p.phaseRouting(ctx, run.ID, videos)
			require.ErrorContains(t, err, "injected routing interruption")
			before, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, videos[0].ID, run.ID)
			require.NoError(t, err)
			require.Len(t, before, 1)
			audits, err := env.stateStore.GetConditionAuditsForDecision(ctx, before[0].ID)
			require.NoError(t, err)
			require.NoError(t, env.stateStore.InterruptRun(ctx, run.ID, "injected interruption"))
			_, err = p.db.ExecContext(ctx, `DROP TRIGGER interrupt_routing`)
			require.NoError(t, err)
			// Completed evidence, not a fresh condition evaluation, must control recovery.
			_, err = p.db.ExecContext(ctx, `UPDATE route_condition SET value = 'no longer matches' WHERE route_id = ?`, env.routeID)
			require.NoError(t, err)
			p.Execute(ctx, run.ID)
			after, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, videos[0].ID, run.ID)
			require.NoError(t, err)
			require.Len(t, after, 2)
			require.Equal(t, before[0], after[0])
			afterAudits, err := env.stateStore.GetConditionAuditsForDecision(ctx, before[0].ID)
			require.NoError(t, err)
			require.Equal(t, audits, afterAudits)
			wantInsertions := 2
			if first == "final" {
				require.Equal(t, state.RouteDecisionSkipped, after[1].Decision)
				require.Equal(t, state.SkipReasonFinalRouteMatched, *after[1].SkipReason)
				wantInsertions = 1
			} else {
				require.Equal(t, state.RouteDecisionMatched, after[1].Decision)
				if first == "excluded" {
					wantInsertions = 1
				}
			}
			loaded, err := env.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			require.Equal(t, state.RunStatusCompleted, loaded.Status)
			require.Zero(t, loaded.VideosNotYetEvaluated)
			require.Len(t, env.server.Insertions(), wantInsertions)
			p.Execute(ctx, run.ID)
			require.Len(t, env.server.Insertions(), wantInsertions, "reexecution must not repeat actions")
			require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM duplicate_match WHERE video_id = ?`, videos[0].ID).Scan(&matchCount))
			require.Equal(t, 1, matchCount, "recovery does not repeat duplicate evidence")
		})
	}
}

func TestRoutingRecovery_InterruptedConditionEvidence(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	videos, err := p.phaseDiscovery(ctx, run.ID)
	require.NoError(t, err)
	_, err = p.db.ExecContext(ctx, `CREATE TRIGGER interrupt_audit BEFORE INSERT ON condition_audit BEGIN SELECT RAISE(ABORT, 'injected evidence interruption'); END`)
	require.NoError(t, err)
	_, err = p.phaseRouting(ctx, run.ID, videos)
	require.ErrorContains(t, err, "injected evidence interruption")
	decisions, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, videos[0].ID, run.ID)
	require.NoError(t, err)
	require.Empty(t, decisions, "a decision must not survive without its condition evidence")
	require.NoError(t, env.stateStore.InterruptRun(ctx, run.ID, "injected evidence interruption"))
	_, err = p.db.ExecContext(ctx, `DROP TRIGGER interrupt_audit`)
	require.NoError(t, err)
	p.Execute(ctx, run.ID)
	decisions, err = env.stateStore.GetRouteDecisionsForVideoInRun(ctx, videos[0].ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	audits, err := env.stateStore.GetConditionAuditsForDecision(ctx, decisions[0].ID)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.True(t, audits[0].Matched)
	loaded, err := env.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status)
	require.Zero(t, loaded.VideosNotYetEvaluated)
	require.Len(t, env.server.Insertions(), 1)
}
