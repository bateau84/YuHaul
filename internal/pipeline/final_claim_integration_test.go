//go:build integration

package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// BR-039 claim closing (ARS-006 §3, §4) on the assembled pipeline, real
// migrated SQLite and the counting provider. The routes still match "jazz", so
// any reprocessing of a seeded video would insert into a playlist.

type finalSeed struct {
	video   int64
	outcome string
	item    int64
	errID   int64 // 0 when none
}

func seedFinalVideos(t *testing.T, env *idempotencyTestEnv, qs *queue.Store, withErrors bool) []finalSeed {
	t.Helper()
	ctx := context.Background()
	db := env.pipeline.db
	res, err := db.ExecContext(ctx, `INSERT INTO run (started_at, run_type, status) VALUES (?, 'manual', 'completed')`, time.Now().UTC())
	require.NoError(t, err)
	origin, err := res.LastInsertId()
	require.NoError(t, err)
	var seeds []finalSeed
	for _, outcome := range []string{"accepted", "rejected", "excluded", "unavailable_on_youtube"} {
		res, err := db.ExecContext(ctx, `INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', ?, 'completed', ?)`, "final-"+outcome, origin, outcome)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		item, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: id, RunID: origin, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
		seed := finalSeed{video: id, outcome: outcome, item: item}
		if withErrors && (outcome == "accepted" || outcome == "rejected") {
			res, err := db.ExecContext(ctx, `INSERT INTO error_entry (video_id, run_id, error_category, error_message, status) VALUES (?, ?, 'runtime', 'm', 'active')`, id, origin)
			require.NoError(t, err)
			seed.errID, err = res.LastInsertId()
			require.NoError(t, err)
		}
		seeds = append(seeds, seed)
	}
	return seeds
}

type snapshot struct{ video, item, errStatus string }

func snapshotFinal(t *testing.T, env *idempotencyTestEnv, s finalSeed) snapshot {
	t.Helper()
	var out snapshot
	require.NoError(t, env.pipeline.db.QueryRow(`SELECT processing_state||'/'||final_outcome FROM video WHERE id=?`, s.video).Scan(&out.video))
	require.NoError(t, env.pipeline.db.QueryRow(`SELECT status||'/'||COALESCE(closed_reason,'')||'/'||retry_count FROM deferred_item WHERE id=?`, s.item).Scan(&out.item))
	if s.errID != 0 {
		require.NoError(t, env.pipeline.db.QueryRow(`SELECT status||'/'||COALESCE(closed_reason,'') FROM error_entry WHERE id=?`, s.errID).Scan(&out.errStatus))
	}
	return out
}

// K1: a scheduled run and a Run Now run each close the final videos' queue
// items instead of reprocessing them, and start and complete normally.
func TestRunClaimsCloseFinalVideosK1(t *testing.T) {
	for _, runType := range []state.RunType{state.RunTypeScheduled, state.RunTypeManual} {
		t.Run(string(runType), func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, nil)
			p := env.pipeline
			qs, err := queue.NewStore(p.db)
			require.NoError(t, err)
			p.queueStore = qs
			seeds := seedFinalVideos(t, env, qs, true)

			run, err := p.StartRun(ctx, runType)
			require.NoError(t, err)
			p.Execute(ctx, run.ID)

			loaded, err := env.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			require.Equal(t, state.RunStatusCompleted, loaded.Status)
			require.Empty(t, env.server.Insertions(), "a final video is never re-sent")
			for _, s := range seeds {
				got := snapshotFinal(t, env, s)
				require.Equal(t, "completed/"+s.outcome, got.video, "outcome unchanged")
				require.Equal(t, "completed/final_outcome/0", got.item, "closed with the reason, history kept")
				if s.errID != 0 {
					require.Equal(t, "active/", got.errStatus, "a run claim leaves Errors entries alone")
				}
				var members, decisions int
				require.NoError(t, p.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=?`, run.ID, s.video).Scan(&members))
				require.NoError(t, p.db.QueryRow(`SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=?`, run.ID, s.video).Scan(&decisions))
				require.Zero(t, members, "no membership for a final video")
				require.Zero(t, decisions)
			}
		})
	}
}

// K2: both user retry routes answer 409 video_already_final before any run is
// created or bound. Authorization is disconnected, so any Bind would fail
// differently; the provider receives no request; the second retry is the same
// answer with nothing changed.
func TestUserRetryOfFinalVideoIsRefusedWithoutRunOrBindK2(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, nil)
	p := env.pipeline
	qs, err := queue.NewStore(p.db)
	require.NoError(t, err)
	p.queueStore = qs
	seeds := seedFinalVideos(t, env, qs, true)
	require.NoError(t, p.auth.Disconnect(ctx), "a Bind from here on would fail")
	var runsBefore int
	require.NoError(t, p.db.QueryRow(`SELECT COUNT(*) FROM run`).Scan(&runsBefore))
	requests := env.server.Requests()

	svc := queue.NewService(qs, nil).WithRetryCoordinator(p)
	post := func(h http.Handler, id int64) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+itoa(int(id))+"/retry", nil))
		return rec
	}
	for _, s := range seeds {
		for _, route := range []struct {
			handler http.Handler
			id      int64
		}{{svc.DeferredHandler(), s.item}, {svc.ErrorHandler(), s.errID}} {
			if route.id == 0 {
				continue
			}
			first := post(route.handler, route.id)
			require.Equal(t, http.StatusConflict, first.Code, first.Body.String())
			require.Contains(t, first.Body.String(), `"code":"video_already_final"`)
			require.Contains(t, first.Body.String(), `"final_outcome":"`+s.outcome+`"`)
			again := post(route.handler, route.id)
			require.Equal(t, http.StatusConflict, again.Code)
			require.JSONEq(t, first.Body.String(), again.Body.String())
		}
		got := snapshotFinal(t, env, s)
		require.Equal(t, "completed/"+s.outcome, got.video)
		require.Equal(t, "completed/final_outcome/0", got.item)
		if s.errID != 0 {
			require.Equal(t, "resolved/final_outcome", got.errStatus)
		}
	}
	var runsAfter int
	require.NoError(t, p.db.QueryRow(`SELECT COUNT(*) FROM run`).Scan(&runsAfter))
	require.Equal(t, runsBefore, runsAfter, "a refused retry creates no run")
	require.Equal(t, requests, env.server.Requests(), "no provider call")
	require.Empty(t, env.server.Insertions())
}

// A retry of an open video still creates its run. Authorization is bound
// after the claim; when binding fails, the run fails and the work is not lost.
func TestRetryBindFailureFailsRunAfterClaim(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, nil)
	p := env.pipeline
	qs, err := queue.NewStore(p.db)
	require.NoError(t, err)
	p.queueStore = qs
	res, err := p.db.ExecContext(ctx, `INSERT INTO run (started_at, run_type, status) VALUES (?, 'manual', 'completed')`, time.Now().UTC())
	require.NoError(t, err)
	origin, _ := res.LastInsertId()
	res, err = p.db.ExecContext(ctx, `INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES ('open-v', 'UC1', ?, 'completed', 'deferred')`, origin)
	require.NoError(t, err)
	video, _ := res.LastInsertId()
	item, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: video, RunID: origin, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	require.NoError(t, p.auth.Disconnect(ctx))

	err = p.RetryQueueEntry(ctx, item, false)
	require.ErrorContains(t, err, "bind authorization for manual retry")
	var status string
	require.NoError(t, p.db.QueryRow(`SELECT status FROM run WHERE id=(SELECT MAX(id) FROM run)`).Scan(&status))
	require.Equal(t, "failed", status)
	require.Empty(t, env.server.Insertions())
}
