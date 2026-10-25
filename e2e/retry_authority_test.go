//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestRetryAuthority_QuotaProposalChangedRouteAndSiblings(t *testing.T) {
	for _, match := range []bool{true, false} {
		t.Run(map[bool]string{true: "changed target", false: "new nonmatch"}[match], func(t *testing.T) {
			ctx := context.Background()
			env := setupEnv(t, []mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}}, []mockVideo{makeVideo("V1", "Jazz Live Session", time.Now().UTC().Add(-time.Hour))})
			routeID := env.createRoute(t, "Jazz", "jazz", "PLold")
			require.NoError(t, env.ledger.SetDailyLimit(ctx, 4))
			old := env.runProductionRun(t)
			require.Empty(t, env.server.Insertions())
			qs, err := queue.NewStore(env.db)
			require.NoError(t, err)
			items, err := qs.ListDeferredItems(ctx, queue.DeferredStatusPending)
			require.NoError(t, err)
			require.Len(t, items, 1)
			// Legacy siblings are aliases, including an unowned retrying row.
			_, err = env.db.ExecContext(ctx, `INSERT INTO deferred_item(video_id,run_id,deferral_reason,status,created_at) VALUES(?,?,'quota_exhausted','retrying',?)`, items[0].VideoID, old.ID, time.Now().UTC())
			require.NoError(t, err)
			stillLimited, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			env.pipeline.Execute(ctx, stillLimited.ID)
			requeued, err := qs.ListDeferredItems(ctx, "")
			require.NoError(t, err)
			require.Len(t, requeued, 2, "quota retry must reuse, not append, siblings")
			for _, d := range requeued {
				require.Equal(t, queue.DeferredStatusPending, d.Status)
				require.Equal(t, 1, d.RetryCount, "no second attempt in the same run")
			}
			var proposal config.ProposalSnapshot
			require.NoError(t, json.Unmarshal([]byte(`{"duplicate_threshold":0.9,"routes":[{"id":"11111111-1111-4111-8111-111111111111","name":"Forbidden","evaluation_order":1,"is_enabled":true,"condition_composition":"AND","conditions":[{"field":"title","operator":"contains","value":"jazz"}],"actions":[{"action_type":"add_to_playlist","target_playlist_id":"PLforbidden"}]}]}`), &proposal))
			// Direct pipeline callers use internal action vocabulary; HTTP normally
			// translates add_to_playlist before this seam.
			proposal.Routes[0].Actions[0].ActionType = "playlist_insert"
			bt, err := env.pipeline.Backtest(ctx, &proposal)
			require.NoError(t, err)
			env.pipeline.ExecuteBacktest(ctx, bt.ID)
			ds, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, items[0].VideoID, bt.ID)
			require.NoError(t, err)
			require.Len(t, ds, 1)
			require.Equal(t, state.RouteDecisionMatched, ds[0].Decision)
			r, err := env.routerStore.GetRoute(ctx, routeID)
			require.NoError(t, err)
			r.Actions[0].TargetPlaylistID = "PLreceiving"
			if !match {
				r.Conditions[0].Value = "classical"
			}
			require.NoError(t, env.routerStore.UpdateRoute(ctx, r))
			require.NoError(t, env.ledger.SetDailyLimit(ctx, 1000000))
			retry, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			// Claim is durable before Execute or any receiving decision.
			ids, err := qs.ListRetryVideoIDs(ctx, retry.ID)
			require.NoError(t, err)
			require.Equal(t, []int64{items[0].VideoID}, ids)
			env.pipeline.Execute(ctx, retry.ID)
			api := inspectionServer(env)
			defer api.Close()
			if match {
				inspectList(t, api, retry.ID, "?outcome=accepted&page_size=1", 1, "V1")
				inspectDetail(t, api, retry.ID, "V1", "accepted", "matched", "PLreceiving", "succeeded")
			} else {
				inspectList(t, api, retry.ID, "?outcome=rejected&page_size=1", 1, "V1")
				inspectDetail(t, api, retry.ID, "V1", "rejected", "not_matched", "", "")
			}
			// The proposal's hypothetical action stays inspectable under its own
			// run after receiving-run reevaluation changes the production outcome.
			inspectDetail(t, api, bt.ID, "V1", "accepted", "matched", "PLforbidden", "not_attempted")
			all, err := qs.ListDeferredItems(ctx, "")
			require.NoError(t, err)
			require.Len(t, all, 2)
			for _, d := range all {
				require.Equal(t, queue.DeferredStatusCompleted, d.Status)
				require.Equal(t, old.ID, d.RunID)
				require.Equal(t, retry.ID, *d.RetryRunID)
				require.Equal(t, 2, d.RetryCount)
			}
			ins := env.server.Insertions()
			if match {
				require.Len(t, ins, 1)
				require.Equal(t, "PLreceiving", ins[0].PlaylistID)
			} else {
				require.Empty(t, ins)
			}
			var invalid int
			require.NoError(t, env.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action a JOIN route_decision d ON d.id=a.route_decision_id WHERE a.run_id<>d.run_id OR a.video_id<>d.video_id`).Scan(&invalid))
			require.Zero(t, invalid)
			v, err := env.stateStore.GetVideo(ctx, items[0].VideoID)
			require.NoError(t, err)
			require.Equal(t, old.ID, v.DiscoveredInRunID)
			unchanged, err := env.stateStore.GetRun(ctx, old.ID)
			require.NoError(t, err)
			require.Equal(t, old, unchanged)
		})
	}
}
