package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestValidateRunRoutes(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()
	runID := createRunOnly(t, ctx, deps.db, deps.stateStore)
	run, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	snapshot := `{"routes":[{"id":1,"evaluation_order":1,"is_final":false,"is_enabled":true,"condition_composition":"AND","conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"x"}],"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"p"}],"exclusions":[{"exclusion_type":"word","exclusion_value":"a"},{"exclusion_type":"word","exclusion_value":"b"}]},{"id":2,"evaluation_order":2,"is_final":false,"is_enabled":true,"condition_composition":"AND","conditions":[],"actions":[],"exclusions":[]}]}`
	setSnapshot := func(s string) {
		t.Helper()
		_, err := deps.db.ExecContext(ctx, `UPDATE config_version SET snapshot = ? WHERE id = ?`, s, *run.ConfigVersionID)
		require.NoError(t, err)
	}
	setSnapshot(snapshot)
	routes, err := deps.pipeline.dryRunRoutes(ctx, run)
	require.NoError(t, err)
	require.NoError(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))

	for _, tc := range []struct {
		name   string
		change func([]router.Route) []router.Route
	}{
		{"route omission", func(r []router.Route) []router.Route { return r[:1] }},
		{"route reorder", func(r []router.Route) []router.Route { r[0], r[1] = r[1], r[0]; return r }},
		{"duplicate route", func(r []router.Route) []router.Route { r[1] = r[0]; return r }},
		{"exclusion reorder", func(r []router.Route) []router.Route {
			r[0].Exclusions[0], r[0].Exclusions[1] = r[0].Exclusions[1], r[0].Exclusions[0]
			return r
		}},
		{"condition", func(r []router.Route) []router.Route { r[0].Conditions[0].Value = "other"; return r }},
		{"action", func(r []router.Route) []router.Route { r[0].Actions[0].TargetPlaylistID = "other"; return r }},
		{"enabled", func(r []router.Route) []router.Route { r[0].IsEnabled = false; return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := deps.pipeline.dryRunRoutes(ctx, run)
			require.NoError(t, err)
			require.Error(t, deps.pipeline.ValidateRunRoutes(ctx, runID, tc.change(r)))
		})
	}
	routes[0].Name = "changed label"
	routes[0].Conditions[0].ID = 42
	require.NoError(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))
	setSnapshot(`{"routes":[{"id":1,"exclusions":[{"exclusion_type":"word","exclusion_value":null}]}]}`)
	require.Error(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))
	setSnapshot(`{"routes":[{"id":1},{"id":1}]}`)
	require.Error(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))
	_, err = deps.db.ExecContext(ctx, `UPDATE run SET config_version_id = NULL WHERE id = ?`, runID)
	require.NoError(t, err)
	require.Error(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))
	require.Error(t, deps.pipeline.ValidateRunRoutes(ctx, -1, routes))
}

func TestValidateRunRoutesRequiredSnapshotFields(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()
	runID := createRunOnly(t, ctx, deps.db, deps.stateStore)
	run, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	const valid = `{"routes":[{"id":1,"evaluation_order":0,"is_enabled":false,"is_final":false,"condition_composition":"","conditions":[{"condition_index":0,"field":"","operator":"","value":""}],"actions":[{"action_index":0,"action_type":"","target_playlist_id":""}],"exclusions":[{"exclusion_type":"","exclusion_value":""}]}]}`
	write := func(s []byte) {
		t.Helper()
		_, err := deps.db.ExecContext(ctx, `UPDATE config_version SET snapshot = ? WHERE id = ?`, string(s), *run.ConfigVersionID)
		require.NoError(t, err)
	}
	write([]byte(valid))
	routes, err := deps.pipeline.dryRunRoutes(ctx, run)
	require.NoError(t, err)
	for _, mode := range []string{"manual", "scheduled", "dry_run", "replay", "backtest"} {
		t.Run(mode, func(t *testing.T) {
			_, err := deps.db.ExecContext(ctx, `UPDATE run SET run_type = ? WHERE id = ?`, mode, runID)
			require.NoError(t, err)
			write([]byte(valid))
			require.NoError(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))
			for _, malformed := range []string{
				`{"routes":[null]}`,
				`{"routes":[{"id":"wrong"}]}`,
				`{"routes":[{"id":1,"evaluation_order":0,"is_enabled":true,"is_final":false,"condition_composition":"AND","conditions":[null]}]}`,
				`{"routes":[{"id":1,"evaluation_order":0,"is_enabled":true,"is_final":false,"condition_composition":"AND","actions":[null]}]}`,
			} {
				write([]byte(malformed))
				require.Error(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))
			}
			for _, group := range []struct {
				name   string
				fields []string
			}{
				{"route", []string{"id", "evaluation_order", "is_enabled", "is_final", "condition_composition"}},
				{"conditions", []string{"condition_index", "field", "operator", "value"}},
				{"actions", []string{"action_index", "action_type", "target_playlist_id"}},
			} {
				for _, field := range group.fields {
					for _, absent := range []bool{true, false} {
						t.Run(fmt.Sprintf("%s/%s/missing_%t", group.name, field, absent), func(t *testing.T) {
							var doc map[string]any
							require.NoError(t, json.Unmarshal([]byte(valid), &doc))
							route := doc["routes"].([]any)[0].(map[string]any)
							target := route
							if group.name != "route" {
								target = route[group.name].([]any)[0].(map[string]any)
							}
							if absent {
								delete(target, field)
							} else {
								target[field] = nil
							}
							data, err := json.Marshal(doc)
							require.NoError(t, err)
							write(data)
							require.Error(t, deps.pipeline.ValidateRunRoutes(ctx, runID, routes))
						})
					}
				}
			}
		})
	}
}

func TestExclusionGatePreflight(t *testing.T) {
	for _, mutate := range []bool{true, false} {
		for _, tc := range []string{"mismatch", "duplicate order", "missing link", "null value", "empty input"} {
			t.Run(fmt.Sprintf("mutate_%t/%s", mutate, tc), func(t *testing.T) {
				ctx := context.Background()
				deps, cleanup := setupDryRunPipeline(t)
				defer cleanup()
				id := createRoute(t, ctx, deps.db, "route", 1)
				runID := createRunOnly(t, ctx, deps.db, deps.stateStore)
				run, err := deps.stateStore.GetRun(ctx, runID)
				require.NoError(t, err)
				snapshot := fmt.Sprintf(`{"routes":[{"id":%d,"evaluation_order":1,"is_final":false,"is_enabled":true,"condition_composition":"AND","exclusions":[{"exclusion_type":"video","exclusion_value":"v"},{"exclusion_type":"video","exclusion_value":"v"},{"exclusion_type":"video","exclusion_value":"other"}]}]}`, id)
				_, err = deps.db.ExecContext(ctx, `UPDATE config_version SET snapshot = ? WHERE id = ?`, snapshot, *run.ConfigVersionID)
				require.NoError(t, err)
				routes, err := deps.pipeline.dryRunRoutes(ctx, run)
				require.NoError(t, err)
				v := createPendingVideo(t, ctx, deps, runID, "v", "channel", "")
				if !mutate {
					_, err = deps.db.ExecContext(ctx, `UPDATE run SET run_type = 'dry_run' WHERE id = ?`, runID)
					require.NoError(t, err)
				}
				videos := []*state.Video{v}
				switch tc {
				case "mismatch", "empty input":
					routes[0].Exclusions[0].ExclusionValue = "different"
				case "duplicate order":
					routes[0].Exclusions[1], routes[0].Exclusions[2] = routes[0].Exclusions[2], routes[0].Exclusions[1]
				case "missing link":
					_, err = deps.db.ExecContext(ctx, `UPDATE run SET config_version_id = NULL WHERE id = ?`, runID)
					require.NoError(t, err)
				case "null value":
					_, err = deps.db.ExecContext(ctx, `UPDATE config_version SET snapshot = json_set(snapshot, '$.routes[0].exclusions[0].exclusion_value', NULL) WHERE id = ?`, *run.ConfigVersionID)
					require.NoError(t, err)
				}
				if tc == "empty input" {
					videos = nil
				}
				_, err = deps.pipeline.runExclusionGate(ctx, runID, videos, routes, mutate)
				require.Error(t, err)
				decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
				require.NoError(t, err)
				require.Empty(t, decisions)
				current, err := deps.stateStore.GetVideo(ctx, v.ID)
				require.NoError(t, err)
				require.Equal(t, v.ProcessingState, current.ProcessingState)
				require.Equal(t, v.FinalOutcome, current.FinalOutcome)
				var audits int
				require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT count(*) FROM condition_audit`).Scan(&audits))
				require.Zero(t, audits)
			})
		}
	}
}

func TestGatePreservesStoredLegacyExclusion(t *testing.T) {
	for _, mutate := range []bool{true, false} {
		t.Run(fmt.Sprintf("mutate_%t", mutate), func(t *testing.T) {
			ctx := context.Background()
			deps, cleanup := setupDryRunPipeline(t)
			defer cleanup()
			id := createRoute(t, ctx, deps.db, "route", 1)
			runID := createRunOnly(t, ctx, deps.db, deps.stateStore)
			run, err := deps.stateStore.GetRun(ctx, runID)
			require.NoError(t, err)
			snapshot := fmt.Sprintf(`{"routes":[{"id":%d,"evaluation_order":1,"is_final":false,"is_enabled":true,"condition_composition":"AND","exclusions":[{"exclusion_type":"video","exclusion_value":"old"}]}]}`, id)
			_, err = deps.db.ExecContext(ctx, `UPDATE config_version SET snapshot = ? WHERE id = ?`, snapshot, *run.ConfigVersionID)
			require.NoError(t, err)
			routes, err := deps.pipeline.dryRunRoutes(ctx, run)
			require.NoError(t, err)
			v := createPendingVideo(t, ctx, deps, runID, "new", "channel", "")
			reason := state.SkipReasonExclusion
			old, err := deps.machine.RecordRouteDecision(ctx, &state.RouteDecision{VideoID: v.ID, RunID: runID, RouteID: id, EvaluationOrder: 1, Decision: state.RouteDecisionSkipped, SkipReason: &reason})
			require.NoError(t, err)
			if !mutate {
				_, err = deps.db.ExecContext(ctx, `UPDATE run SET run_type = 'dry_run' WHERE id = ?`, runID)
				require.NoError(t, err)
			}
			remaining, err := deps.pipeline.runExclusionGate(ctx, runID, []*state.Video{v}, routes, mutate)
			require.NoError(t, err)
			require.Empty(t, remaining)
			decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
			require.NoError(t, err)
			require.Len(t, decisions, 1)
			require.Equal(t, old.ID, decisions[0].ID)
			require.Nil(t, decisions[0].ExclusionIndex)
			require.Nil(t, decisions[0].ExclusionSemanticsVersion)
		})
	}
}

func TestLegacyCompletionRejectsUnboundRoutesBeforeWrites(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()
	createRoute(t, ctx, deps.db, "route", 1)
	runID := createRunOnly(t, ctx, deps.db, deps.stateStore)
	run, err := deps.stateStore.GetRun(ctx, runID)
	require.NoError(t, err)
	v := createPendingVideo(t, ctx, deps, runID, "video", "channel", "")
	routes, err := deps.pipeline.productionRoutes(ctx, runID)
	require.NoError(t, err)
	require.NotEmpty(t, routes)
	routes[0].IsEnabled = !routes[0].IsEnabled
	_, err = deps.pipeline.completeLegacyEvaluation(ctx, run, v, routes)
	require.Error(t, err)
	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, runID)
	require.NoError(t, err)
	require.Empty(t, decisions)
}
