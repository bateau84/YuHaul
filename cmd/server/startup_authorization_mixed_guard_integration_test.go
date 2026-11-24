//go:build integration

package main

// Review follow-up 35e62dd4, finding 0bfa9e12, through the real server
// composition (ARS-005 section 6.2). The process starts over a database that
// holds two live receipts of one receiving run and no connected account: the
// first holds a saved evaluation and an unresolved send guard (a send that was
// in flight), the second is an unguarded video whose inputs are held locally.
// Startup recovery cannot bind. The guarded receipt keeps its disposition; the
// peer must still settle as deferred/authorization_pending, the stop must be
// stored, and the registered run-video API must say so.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

type mixedSeed struct {
	runID, guardedItem, peerItem, guardedVideo, peerVideo int64
}

// seedGuardedAndPeer leaves an interrupted receiving run that owns two claims.
// V1 (claimed first) has a saved matched decision and a playlist_send_guard row.
// V2 has its inputs in the local metadata store and no evaluation. storedStop
// restores a stored authorization stop; without it the refusal is detected only
// when the recovered execution tries to bind.
func seedGuardedAndPeer(t *testing.T, dbPath string, storedStop bool) mixedSeed {
	t.Helper()
	ctx := context.Background()
	seedBase(t, dbPath)
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`DELETE FROM auth_tokens`)
	require.NoError(t, err)

	routerStore, cfgStore := router.NewStore(db), config.NewStore(db)
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	machine := state.NewMachine(stateStore)
	p := pipeline.NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, pipeline.NewBroadcaster(),
		pipeline.WithVersioner(config.NewService(routerStore, cfgStore)), pipeline.WithConfigStore(cfgStore))

	origin, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	var videos [2]*state.Video
	var items [2]int64
	for i, id := range []string{"V1", "V2"} {
		videos[i], err = machine.DiscoverVideo(ctx, origin.ID, id, "UC1")
		require.NoError(t, err)
		items[i], err = qs.CreateDeferredItem(ctx, &queue.DeferredItem{
			VideoID: videos[i].ID, RunID: origin.ID, Reason: queue.DeferredReasonQuotaExhausted,
			Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC().Add(-time.Duration(48-i) * time.Hour).Truncate(time.Second),
		})
		require.NoError(t, err)
	}
	title, channel, description, duration := "Jazz V2", "Test Channel", "d", 120
	_, err = metadata.NewStore(db).UpsertFetched(ctx, []metadata.TransientMetadata{{
		VideoID: "V2", Title: &title, ChannelName: &channel, Description: &description, DurationSeconds: &duration,
	}}, 24*time.Hour, time.Now())
	require.NoError(t, err)
	require.NoError(t, stateStore.CompleteRun(ctx, origin.ID, 1, 0))

	recv, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	for _, item := range items {
		require.Equal(t, recv.ID, scalar[int64](t, db, `SELECT retry_run_id FROM deferred_item WHERE id=?`, item))
	}

	var routeID int64
	require.NoError(t, db.QueryRow(`SELECT id FROM route WHERE name='Jazz'`).Scan(&routeID))
	_, err = machine.TransitionVideoState(ctx, videos[0].ID, state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	decision, err := machine.RecordRouteDecision(ctx, &state.RouteDecision{VideoID: videos[0].ID, RunID: recv.ID, RouteID: routeID, EvaluationOrder: 1, Decision: state.RouteDecisionMatched})
	require.NoError(t, err)
	_, err = machine.TransitionVideoState(ctx, videos[0].ID, state.VideoStateEvaluating, state.VideoStateEvaluated)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO playlist_send_guard(video_id,target_playlist_id,action_type,run_id,route_decision_id,authorization_id,prepared_at) VALUES(?,?,?,?,?,?,?)`,
		videos[0].ID, "PLjazz", "playlist_insert", recv.ID, decision.ID, "opaque", "2026-01-01T00:00:00Z")
	require.NoError(t, err)
	if storedStop {
		_, err = db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, recv.ID)
		require.NoError(t, err)
	}
	require.NoError(t, stateStore.InterruptRun(ctx, recv.ID, "test: process stopped"))
	return mixedSeed{runID: recv.ID, guardedItem: items[0], peerItem: items[1], guardedVideo: videos[0].ID, peerVideo: videos[1].ID}
}

func TestStartupRecoveryGuardedReceiptDoesNotBlockItsPeerFromAuthorizationSettlement(t *testing.T) {
	for _, c := range []struct {
		name       string
		storedStop bool
	}{
		{name: "restored authorization stop", storedStop: true},
		{name: "newly detected authorization refusal"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
			seed := seedGuardedAndPeer(t, dbPath, c.storedStop)
			yt := newFakeYouTube(t, "V1", "V2")
			db := openReadDB(t, dbPath)
			const guardDump = `SELECT group_concat(video_id||target_playlist_id||run_id||route_decision_id||authorization_id||prepared_at) FROM playlist_send_guard`
			const itemDump = `SELECT group_concat(id||':'||deferral_reason||':'||created_at||':'||run_id) FROM deferred_item WHERE id IN (?,?)`
			guards := scalar[string](t, db, guardDump)
			items := scalar[string](t, db, itemDump, seed.guardedItem, seed.peerItem)
			decision := scalar[string](t, db, `SELECT group_concat(id||':'||route_id||':'||decision) FROM route_decision WHERE video_id=?`, seed.guardedVideo)

			srv := startServer(t, dbPath, yt.srv.URL)
			eventually(t, runDoneBudget, "startup recovery to finish", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })
			logs := srv.out.String()

			require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, seed.runID), "the run ends: it has no binding\n%s", logs)
			require.Equal(t, "authorization", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, seed.runID), "the stop is stored, guard or not\n%s", logs)
			require.Equal(t, "deferred/authorization_pending", scalar[string](t, db,
				`SELECT final_outcome||'/'||deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, seed.runID, seed.peerVideo), logs)
			require.Equal(t, "pending", scalar[string](t, db, `SELECT status FROM deferred_item WHERE id=?`, seed.peerItem))
			require.Equal(t, "pending", scalar[string](t, db, `SELECT status FROM deferred_item WHERE id=?`, seed.guardedItem), "the guarded work is retained, not replayed")
			require.Equal(t, items, scalar[string](t, db, itemDump, seed.guardedItem, seed.peerItem), "exact rows, reasons and ages")
			require.Equal(t, 0, scalar[int](t, db, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome IS NOT NULL`, seed.runID, seed.guardedVideo),
				"the guarded receipt gets no deferral")
			require.Equal(t, guards, scalar[string](t, db, guardDump), "the guard is untouched")
			require.Equal(t, decision, scalar[string](t, db, `SELECT group_concat(id||':'||route_id||':'||decision) FROM route_decision WHERE video_id=?`, seed.guardedVideo), "the saved decision is untouched")
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM error_entry`))
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM executed_action`))
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM quota_ledger WHERE run_id=?`, seed.runID))
			require.Zero(t, yt.requestCount(), "no provider request\n%s", logs)
			require.Contains(t, logs, "mutation_application_uncertain")

			// The registered API says the same thing the rows do.
			code, body := srv.do(http.MethodGet, "/api/runs/"+itoa(seed.runID)+"/videos?outcome=deferred")
			require.Equal(t, http.StatusOK, code, body)
			var list struct {
				Videos []struct {
					ID                  int64   `json:"id"`
					FinalOutcome        *string `json:"final_outcome"`
					DeferredReason      *string `json:"deferred_reason"`
					UnresolvedMutations []struct {
						TargetPlaylistID string `json:"target_playlist_id"`
					} `json:"unresolved_mutations"`
				} `json:"videos"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &list), body)
			require.Len(t, list.Videos, 1, "only the peer is deferred in this run: %s", body)
			require.Equal(t, seed.peerVideo, list.Videos[0].ID)
			require.Equal(t, "deferred", *list.Videos[0].FinalOutcome)
			require.Equal(t, "authorization_pending", *list.Videos[0].DeferredReason)
			code, body = srv.do(http.MethodGet, "/api/runs/"+itoa(seed.runID)+"/videos")
			require.Equal(t, http.StatusOK, code, body)
			require.NoError(t, json.Unmarshal([]byte(body), &list), body)
			var sawGuarded bool
			for _, v := range list.Videos {
				if v.ID == seed.guardedVideo {
					sawGuarded = true
					require.Len(t, v.UnresolvedMutations, 1, "the guarded send stays visible as unresolved: %s", body)
					require.Equal(t, "PLjazz", v.UnresolvedMutations[0].TargetPlaylistID)
				}
			}
			require.True(t, sawGuarded, body)
			srv.sigterm()
		})
	}
}
