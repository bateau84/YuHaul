package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

// ADR-006 / ARS-001 / BR-020 AC7: the route-list match count is the number of
// distinct videos matched by a route across production runs (scheduled and
// manual). Repeated matched decisions for one video contribute once, and
// dry-run, replay, and backtest decisions never contribute.
func TestListRouteMatchCounts_DistinctVideosProductionOnly(t *testing.T) {
	ctx := context.Background()
	stateStore, routerStore, cleanup := setupTestDB(t)
	defer cleanup()

	matchedRoute, err := routerStore.CreateRoute(ctx, &Route{
		Name: "Matched", EvaluationOrder: 1, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []RouteCondition{{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "music"}},
		Actions:              []RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL1"}},
	})
	require.NoError(t, err)
	unmatchedRoute, err := routerStore.CreateRoute(ctx, &Route{
		Name: "Unmatched", EvaluationOrder: 2, IsEnabled: true,
		ConditionComposition: "AND",
		Conditions:           []RouteCondition{{ConditionIndex: 0, Field: ConditionFieldTitle, Operator: ConditionOperatorContains, Value: "ambient"}},
		Actions:              []RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL2"}},
	})
	require.NoError(t, err)

	newRun := func(t *testing.T, runType state.RunType) int64 {
		t.Helper()
		id, err := stateStore.CreateRun(ctx, &state.Run{RunType: runType, Status: state.RunStatusRunning})
		require.NoError(t, err)
		return id
	}
	discover := func(t *testing.T, runID int64, videoID string) int64 {
		t.Helper()
		v, err := state.NewMachine(stateStore).DiscoverVideo(ctx, runID, videoID, "ch1")
		require.NoError(t, err)
		return v.ID
	}
	decide := func(t *testing.T, videoID, runID, routeID int64, decision string) {
		t.Helper()
		_, err := routerStore.DB().ExecContext(ctx,
			`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision)
			 VALUES (?, ?, ?, 1, ?);`, videoID, runID, routeID, decision)
		require.NoError(t, err)
	}

	scheduled := newRun(t, state.RunTypeScheduled)
	manual := newRun(t, state.RunTypeManual)
	dryRun := newRun(t, state.RunTypeDryRun)
	replay := newRun(t, state.RunTypeReplay)
	backtest := newRun(t, state.RunTypeBacktest)

	v1 := discover(t, scheduled, "v1")
	v2 := discover(t, scheduled, "v2")
	v3 := discover(t, dryRun, "v3")
	v4 := discover(t, backtest, "v4")

	// Production evidence: v1 matches in both production runs (counts once),
	// v2 is a second distinct video (counts separately).
	decide(t, v1, scheduled, matchedRoute, "matched")
	decide(t, v1, manual, matchedRoute, "matched")
	decide(t, v2, scheduled, matchedRoute, "matched")
	// Non-matched decisions never count.
	decide(t, v1, scheduled, unmatchedRoute, "not_matched")

	// Non-production evidence is excluded regardless of run mode.
	decide(t, v1, dryRun, matchedRoute, "matched")
	decide(t, v3, dryRun, matchedRoute, "matched")
	decide(t, v3, replay, matchedRoute, "matched")
	decide(t, v4, backtest, matchedRoute, "matched")

	counts, err := routerStore.ListRouteMatchCounts(ctx)
	require.NoError(t, err)

	assert.Equal(t, int64(2), counts[matchedRoute],
		"distinct production videos v1+v2; repeated runs and non-production runs must not inflate the count")
	_, hasUnmatched := counts[unmatchedRoute]
	assert.False(t, hasUnmatched, "routes without matched production decisions are omitted (callers render an explicit zero)")
}
