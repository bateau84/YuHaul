package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestLegacyCompletionCapturesMissingDecisionWithoutEnrichingWinner(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	first := createRoute(t, ctx, deps.db, "first", 1)
	second := createRoute(t, ctx, deps.db, "second", 2)
	for _, id := range []int64{first, second} {
		_, err := deps.db.ExecContext(ctx, `INSERT INTO route_exclusion (route_id, exclusion_type, exclusion_value) VALUES (?, 'video', 'video')`, id)
		require.NoError(t, err)
	}
	version, err := deps.pipeline.versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotNil(t, version)
	legacy := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: version, ActionAuthorityVersion: 0}
	legacyID, err := deps.stateStore.CreateRun(ctx, legacy)
	require.NoError(t, err)
	legacy, err = deps.stateStore.GetRun(ctx, legacyID)
	require.NoError(t, err)
	require.Zero(t, legacy.ActionAuthorityVersion)
	v := createPendingVideo(t, ctx, deps, legacyID, "video", "channel", "")
	reason := state.SkipReasonExclusion
	winner, err := deps.machine.RecordRouteDecision(ctx, &state.RouteDecision{VideoID: v.ID, RunID: legacyID, RouteID: first, EvaluationOrder: 1, Decision: state.RouteDecisionSkipped, SkipReason: &reason})
	require.NoError(t, err)
	routes, err := deps.pipeline.productionRoutes(ctx, legacyID)
	require.NoError(t, err)
	require.NoError(t, deps.pipeline.ValidateRunRoutes(ctx, legacyID, routes))
	_, err = deps.pipeline.completeLegacyEvaluation(ctx, legacy, v, routes)
	require.NoError(t, err)
	old, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, legacyID)
	require.NoError(t, err)
	require.Len(t, old, 2)
	require.Equal(t, *winner, old[0])
	require.Nil(t, old[0].ExclusionIndex)
	require.Nil(t, old[0].ExclusionSemanticsVersion)
	require.Equal(t, second, old[1].RouteID)
	require.Equal(t, state.RouteDecisionSkipped, old[1].Decision)
	require.Equal(t, reason, *old[1].SkipReason)
	require.NotNil(t, old[1].ExclusionIndex)
	require.Equal(t, 0, *old[1].ExclusionIndex)
	require.NotNil(t, old[1].ExclusionSemanticsVersion)
	require.Equal(t, 1, *old[1].ExclusionSemanticsVersion)

	// Change the saved configuration before starting a new run, without changing
	// the old snapshot. The same video must receive new run-scoped evidence.
	_, err = deps.db.ExecContext(ctx, `UPDATE route_exclusion SET exclusion_value = 'other' WHERE route_id = ?`, first)
	require.NoError(t, err)
	_, err = deps.db.ExecContext(ctx, `INSERT INTO route_exclusion (route_id, exclusion_type, exclusion_value) VALUES (?, 'video', 'video')`, first)
	require.NoError(t, err)
	newRunID := createRunOnly(t, ctx, deps.db, deps.stateStore)
	newRun, err := deps.stateStore.GetRun(ctx, newRunID)
	require.NoError(t, err)
	require.NotEqual(t, *version, *newRun.ConfigVersionID)
	newRoutes, err := deps.pipeline.productionRoutes(ctx, newRunID)
	require.NoError(t, err)
	_, err = deps.pipeline.router.EvaluateWithRoutes(ctx, newRunID, v.ID, router.VideoContext{VideoID: "video", ChannelID: "channel"}, deps.machine, newRoutes)
	require.NoError(t, err)
	fresh, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, newRunID)
	require.NoError(t, err)
	require.Len(t, fresh, 2)
	for _, d := range fresh {
		require.Equal(t, state.RouteDecisionSkipped, d.Decision)
		require.NotNil(t, d.ExclusionIndex)
		if d.RouteID == first {
			require.Equal(t, 1, *d.ExclusionIndex)
		} else {
			require.Equal(t, second, d.RouteID)
			require.Equal(t, 0, *d.ExclusionIndex)
		}
		require.NotNil(t, d.ExclusionSemanticsVersion)
		require.Equal(t, 1, *d.ExclusionSemanticsVersion)
	}
	after, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, legacyID)
	require.NoError(t, err)
	require.Equal(t, old, after)
}
