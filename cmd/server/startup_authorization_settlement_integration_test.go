//go:build integration

package main

// Assessment d8d611d7 blockers 1 and 2 through the real server composition
// (ARS-005 section 6.2): the process is started against a database that holds
// a receiving run's live claim, with no connected account. The startup
// recovery cannot take an authorization binding, so the receipt must settle
// locally as deferred/authorization_pending. The executor must not fail it,
// no Errors entry may appear, and the loopback provider sees no request.
//
// The seed is built with the real stores and the real claim transaction; only
// the saved decision of the second case is written through the state machine,
// the way the receiving run's own evaluation does it.

import (
	"context"
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

type claimedSeed struct {
	runID, itemID, videoID int64
	itemCreatedAt          string
}

// seedClaimedWithoutAccount leaves an interrupted receiving run that owns one
// claimed deferred item for video V1 ("Jazz V1", which the Jazz route
// matches) and no stored authorization. localInputs puts the video's inputs
// in the local metadata store; saved gives the receiving run a finished
// evaluation with a matched decision and no action yet.
func seedClaimedWithoutAccount(t *testing.T, dbPath string, localInputs, saved bool) claimedSeed {
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
	video, err := machine.DiscoverVideo(ctx, origin.ID, "V1", "UC1")
	require.NoError(t, err)
	if localInputs {
		title, channel, description, duration := "Jazz V1", "Test Channel", "d", 120
		_, err = metadata.NewStore(db).UpsertFetched(ctx, []metadata.TransientMetadata{{
			VideoID: "V1", Title: &title, ChannelName: &channel, Description: &description, DurationSeconds: &duration,
		}}, 24*time.Hour, time.Now())
		require.NoError(t, err)
	}
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	created := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	itemID, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{
		VideoID: video.ID, RunID: origin.ID, Reason: queue.DeferredReasonQuotaExhausted,
		Status: queue.DeferredStatusPending, CreatedAt: created,
	})
	require.NoError(t, err)
	require.NoError(t, stateStore.CompleteRun(ctx, origin.ID, 1, 0))

	// The next run claims the pending item in its creating transaction.
	recv, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.Equal(t, "retrying", scalar[string](t, db, `SELECT status FROM deferred_item WHERE id=?`, itemID))
	require.Equal(t, recv.ID, scalar[int64](t, db, `SELECT retry_run_id FROM deferred_item WHERE id=?`, itemID))

	if saved {
		var routeID int64
		require.NoError(t, db.QueryRow(`SELECT id FROM route WHERE name='Jazz'`).Scan(&routeID))
		_, err = machine.TransitionVideoState(ctx, video.ID, state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
		require.NoError(t, err)
		_, err = machine.RecordRouteDecision(ctx, &state.RouteDecision{VideoID: video.ID, RunID: recv.ID, RouteID: routeID, EvaluationOrder: 1, Decision: state.RouteDecisionMatched})
		require.NoError(t, err)
		_, err = machine.TransitionVideoState(ctx, video.ID, state.VideoStateEvaluating, state.VideoStateEvaluated)
		require.NoError(t, err)
	}
	require.NoError(t, stateStore.InterruptRun(ctx, recv.ID, "test: process stopped"))
	return claimedSeed{runID: recv.ID, itemID: itemID, videoID: video.ID, itemCreatedAt: scalar[string](t, db, `SELECT created_at FROM deferred_item WHERE id=?`, itemID)}
}

func TestStartupRecoveryWithoutAnAccountSettlesTheClaimAsAuthorizationPending(t *testing.T) {
	for _, c := range []struct {
		name               string
		localInputs, saved bool
	}{
		{name: "locally ready match, evaluation not yet saved", localInputs: true},
		{name: "saved decision, inputs not held locally", saved: true},
		{name: "saved decision, inputs held locally", localInputs: true, saved: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
			seed := seedClaimedWithoutAccount(t, dbPath, c.localInputs, c.saved)
			yt := newFakeYouTube(t, "V1")
			db := openReadDB(t, dbPath)
			savedBefore := scalar[string](t, db, `SELECT COALESCE(group_concat(id||':'||route_id||':'||decision),'') FROM route_decision WHERE run_id=? AND video_id=?`, seed.runID, seed.videoID)
			require.Equal(t, c.saved, savedBefore != "")

			srv := startServer(t, dbPath, yt.srv.URL)
			eventually(t, runDoneBudget, "startup recovery to finish", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })

			require.Equal(t, "authorization", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, seed.runID), "stored with the first settlement\n%s", srv.out.String())
			require.Equal(t, "deferred/authorization_pending", scalar[string](t, db,
				`SELECT final_outcome||'/'||deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, seed.runID, seed.videoID), srv.out.String())
			require.Equal(t, "deferred", scalar[string](t, db, `SELECT processing_state FROM video WHERE id=?`, seed.videoID))
			require.Equal(t, "pending/quota_exhausted/"+seed.itemCreatedAt, scalar[string](t, db,
				`SELECT status||'/'||deferral_reason||'/'||created_at FROM deferred_item WHERE id=?`, seed.itemID), "the exact queue row, its reason and age are kept")
			require.Equal(t, 1, scalar[int](t, db, `SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, seed.videoID), "no second queue row")
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM error_entry`), "no Errors entry")
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM executed_action`), "no action attempted")
			require.Equal(t, 1, scalar[int](t, db, `SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=? AND decision='matched'`, seed.runID, seed.videoID),
				"one matched decision: a saved one is kept, not rerouted; an unsaved evaluation made its single local one")
			if c.saved {
				require.Equal(t, savedBefore, scalar[string](t, db, `SELECT group_concat(id||':'||route_id||':'||decision) FROM route_decision WHERE run_id=? AND video_id=?`, seed.runID, seed.videoID),
					"the saved decision is byte-for-byte what it was")
			}
			require.Zero(t, yt.requestCount(), "no provider request\n%s", srv.out.String())
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM quota_ledger WHERE run_id=?`, seed.runID))
			srv.sigterm()
		})
	}
}
