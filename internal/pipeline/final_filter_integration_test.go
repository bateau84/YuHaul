//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// BR-039 main-loop filter, handoff exclusions and resume (ARS-006 §5, §6,
// tests K3, K4, K5, K19). Real migrated SQLite, the real pipeline and a
// counting local provider.

// finalizedElsewhere makes a video final the way another run's commit does.
func finalizedElsewhere(t *testing.T, e *runNowEnv, external, outcome string) int64 {
	t.Helper()
	return finalizedElsewhereIn(t, e, external, "evaluated", outcome)
}

// finalizedElsewhereIn also sets the processing state. A rejected video keeps
// processing_state 'evaluated', so it is not hidden by a completed-state skip.
func finalizedElsewhereIn(t *testing.T, e *runNowEnv, external, processing, outcome string) int64 {
	t.Helper()
	v, err := e.stateStore.GetVideoByExternalID(e.ctx, external)
	require.NoError(t, err)
	_, err = e.db().ExecContext(e.ctx, `UPDATE video SET processing_state=?, final_outcome=? WHERE id=?`, processing, outcome, v.ID)
	require.NoError(t, err)
	return v.ID
}

type runCounts struct {
	Discovered, Accepted, Rejected, Excluded, Deferred, Failed, NotYet, Unavailable int
	Selected                                                                        int
	Members                                                                         int
}

func countsOf(t *testing.T, env *idempotencyTestEnv, runID int64) runCounts {
	t.Helper()
	r, err := env.stateStore.GetRun(context.Background(), runID)
	require.NoError(t, err)
	c := runCounts{r.VideosDiscovered, r.VideosAccepted, r.VideosRejected, r.VideosExcluded, r.VideosDeferred,
		r.VideosFailed, r.VideosNotYetEvaluated, r.VideosUnavailable, -1, 0}
	if r.VideosSelected != nil {
		c.Selected = *r.VideosSelected
	}
	require.NoError(t, env.pipeline.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=?`, runID).Scan(&c.Members))
	return c
}

// K3 (AC3): the run counters and the run's member totals equal those of a run
// that had no final video seeded.
func TestFinalVideosDoNotChangeRunCountsK3(t *testing.T) {
	ctx := context.Background()
	jazz := []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))}

	with := setupIdempotencyTest(t, jazz)
	qs, err := queue.NewStore(with.pipeline.db)
	require.NoError(t, err)
	with.pipeline.queueStore = qs
	seedFinalVideos(t, with, qs, true)
	runWith, err := with.pipeline.StartRun(ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	with.pipeline.Execute(ctx, runWith.ID)

	without := setupIdempotencyTest(t, jazz)
	without.pipeline.queueStore, err = queue.NewStore(without.pipeline.db)
	require.NoError(t, err)
	runWithout, err := without.pipeline.StartRun(ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	without.pipeline.Execute(ctx, runWithout.ID)

	got, want := countsOf(t, with, runWith.ID), countsOf(t, without, runWithout.ID)
	require.Equal(t, 1, want.Accepted, "control run accepts the one video")
	require.Equal(t, want, got)
}

// K4: a run whose members include a video another run finalized hands over,
// sweeps and retains nothing for it, on every ending that retains work.
func TestFinalMembersAreNeverHandedOverK4(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		e := newRunNowEnv(t)
		adm, done := e.stalledRunNow(t)
		finalizedElsewhere(t, e, "V3", "rejected")
		e.stop(t, adm.RunID(), StopAccepted)
		waitDone(t, done)
		e.assertStopped(t, adm.RunID())
		require.Equal(t, map[string]int{"V2": 1}, e.pendingHandoffs(t, "stopped_by_user"), "V3 is final: no item")
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id='V3'`))
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry`))
	})

	t.Run("discard", func(t *testing.T) {
		e := newRunNowEnv(t)
		runID := e.unusableRun(t)
		finalizedElsewhere(t, e, "V3", "excluded")
		_, err := e.pipeline.DiscardRun(e.ctx, runID)
		require.NoError(t, err)
		require.Equal(t, map[string]int{"V2": 1}, e.pendingHandoffs(t, "discarded_by_user"))
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id='V3'`))
	})

	sweep := func(t *testing.T, makeFinal bool) map[string]int {
		e := newRunNowEnv(t)
		e.stallOn("V2")
		adm := e.interruptedAtLimit(t, testLimit)
		if makeFinal {
			finalizedElsewhere(t, e, "V3", "unavailable_on_youtube")
		}
		qs, err := e.pipeline.retryStore()
		require.NoError(t, err)
		require.NoError(t, qs.WithRetryTx(e.ctx, func(tx *sql.Tx) error {
			return sweepStrandedWork(e.ctx, qs, tx, adm.RunID(), "run stopped by a failure")
		}))
		out := map[string]int{}
		rows, err := e.db().QueryContext(e.ctx, `SELECT v.video_id, COUNT(*) FROM error_entry x JOIN video v ON v.id=x.video_id GROUP BY v.video_id`)
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var id string
			var n int
			require.NoError(t, rows.Scan(&id, &n))
			out[id] = n
		}
		require.NoError(t, rows.Err())
		return out
	}
	t.Run("failure sweep", func(t *testing.T) {
		require.Equal(t, map[string]int{"V2": 1, "V3": 1}, sweep(t, false), "control: an open V3 is retained")
		require.Equal(t, map[string]int{"V2": 1}, sweep(t, true), "a final V3 is not")
	})

	t.Run("authority failure", func(t *testing.T) {
		e := newRunNowEnv(t)
		e.stallOn("V2")
		adm := e.interruptedAtLimit(t, testLimit)
		finalizedElsewhere(t, e, "V3", "accepted")
		videos, err := e.pipeline.listRunVideos(e.ctx, adm.RunID())
		require.NoError(t, err)
		e.pipeline.retainAuthorityFailure(e.ctx, adm.RunID(), videos, errors.New("authority gone"))
		require.Equal(t, state.RunStatusFailed, e.run(t, adm.RunID()).Status)
		require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM error_entry x JOIN video v ON v.id=x.video_id WHERE v.video_id='V2'`))
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry x JOIN video v ON v.id=x.video_id WHERE v.video_id='V3'`))
	})
}

// K5: an interrupted run whose members include a video another run finalized,
// resumed by Run Now and by startup recovery, makes no transition, decision,
// outcome or request for it.
func TestResumeSkipsFinalMemberK5(t *testing.T) {
	for _, via := range []string{"run_now", "startup"} {
		t.Run(via, func(t *testing.T) {
			e := newRunNowEnv(t)
			e.stallOn("V2")
			adm := e.interruptedAtLimit(t, testLimit)
			v3 := finalizedElsewhereIn(t, e, "V3", "evaluated", "rejected")
			e.stallOn()
			before := videoRowsDump(t, e.db(), v3)
			posts := e.postsFor("V3")

			switch via {
			case "run_now":
				resumed, err := e.pipeline.AdmitRunNow(e.ctx)
				require.NoError(t, err)
				require.True(t, resumed.Resumed())
				require.Equal(t, adm.RunID(), resumed.RunID())
				resumed.Execute(e.ctx)
			case "startup":
				require.NoError(t, e.pipeline.RecoverStartup(e.ctx))
			}

			require.NotEqual(t, state.RunStatusInterrupted, e.run(t, adm.RunID()).Status, "the run was resumed and ended")
			require.Equal(t, posts, e.postsFor("V3"), "no request for the final video")
			require.Equal(t, before, videoRowsDump(t, e.db(), v3), "no transition, decision, outcome or action")
		})
	}
}

// K19 (critic F2): run R defers V's two targets for quota and is interrupted;
// a later run R2 takes V over, sends A and defers B; R is then resumed. R's
// main loop must drop V and write nothing for it.
func TestResumeAfterAnotherRunTookOverK19(t *testing.T) {
	type variant struct {
		name      string
		runType   state.RunType
		dismissed bool
	}
	for _, c := range []variant{
		{"run now resume, pending item", state.RunTypeManual, false},
		{"startup recovery, pending item", state.RunTypeScheduled, false},
		{"run now resume, item dismissed", state.RunTypeManual, true},
		{"startup recovery, item dismissed", state.RunTypeScheduled, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
			p := env.pipeline
			qs, err := queue.NewStore(p.db)
			require.NoError(t, err)
			p.queueStore = qs
			_, err = router.NewStore(p.db).CreateRoute(ctx, &router.Route{Name: "Second", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND",
				Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "jazz"}},
				Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLsecond"}}})
			require.NoError(t, err)

			var mu sync.Mutex
			posts := 0
			refuseSecond := false
			env.server.insertDecision = func(playlistID, _ string) (int, string) {
				mu.Lock()
				defer mu.Unlock()
				posts++
				if refuseSecond && playlistID == "PLsecond" {
					return http.StatusForbidden, `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`
				}
				return 0, ""
			}
			postCount := func() int { mu.Lock(); defer mu.Unlock(); return posts }
			// The pre-flight prices a video's whole action batch, so a limit with
			// no room defers both targets of R's evaluation.
			noRoom := func() {
				consumed, err := p.ledger.ConsumedToday(ctx)
				require.NoError(t, err)
				require.NoError(t, p.ledger.SetDailyLimit(ctx, consumed+49))
			}

			// 1. R matches V to A and B; both are deferred for quota; R is interrupted.
			noRoom()
			r, err := p.StartRun(ctx, c.runType)
			require.NoError(t, err)
			vs, err := p.phaseDiscovery(ctx, r.ID)
			require.NoError(t, err)
			vs, err = p.phaseExclusion(ctx, r.ID, vs)
			require.NoError(t, err)
			require.NoError(t, p.phaseDuplicateDetection(ctx, r.ID, vs))
			routed, err := p.phaseRouting(ctx, r.ID, vs)
			require.NoError(t, err)
			require.Len(t, routed, 1)
			videoID := routed[0].ID
			v, err := p.stateStore.GetVideo(ctx, videoID)
			require.NoError(t, err)
			outcome, err := p.executeVideoActions(ctx, r.ID, v)
			require.NoError(t, err)
			require.Equal(t, state.FinalOutcomeDeferred, outcome)
			require.Zero(t, env.countRows(t, `SELECT COUNT(*) FROM executed_action WHERE video_id=? AND outcome='succeeded'`, videoID))
			require.Positive(t, env.countRows(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND run_id=? AND status='pending'`, videoID, r.ID))
			require.NoError(t, p.stateStore.SetRunPhase(ctx, r.ID, string(PhaseActionExecution)))
			if c.runType == state.RunTypeManual {
				require.NoError(t, p.stateStore.InterruptRunWithCause(ctx, r.ID, "test: time limit", state.InterruptCauseTimeLimit))
			} else {
				require.NoError(t, p.stateStore.InterruptRun(ctx, r.ID, "test: killed"))
			}

			// 2. R2 claims V (open, so it evaluates it), sends A and defers B.
			require.NoError(t, p.ledger.SetDailyLimit(ctx, 1_000_000))
			mu.Lock()
			refuseSecond = true
			mu.Unlock()
			r2, err := p.StartRun(ctx, state.RunTypeScheduled)
			require.NoError(t, err)
			p.Execute(ctx, r2.ID)
			loaded, err := p.stateStore.GetRun(ctx, r2.ID)
			require.NoError(t, err)
			require.Equal(t, state.RunStatusCompleted, loaded.Status, "reason: %v", loaded.InterruptReason)
			require.Equal(t, []playlistInsertion{{PlaylistID: "PLjazz", VideoID: "V1"}}, env.server.Insertions())
			class, _, err := state.ClassifyVideoTx(ctx, p.db, videoID)
			require.NoError(t, err)
			require.Equal(t, state.VideoClassPartlyDone, class, "V holds one production success under R2's decision")
			var pending int64
			require.NoError(t, p.db.QueryRow(`SELECT id FROM deferred_item WHERE video_id=? AND status='pending' ORDER BY id LIMIT 1`, videoID).Scan(&pending))
			if c.dismissed {
				require.NoError(t, qs.DiscardDeferredItem(ctx, pending))
				require.Zero(t, env.countRows(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status IN ('pending','retrying')`, videoID))
			}

			// 3. R is resumed. R must drop V and write nothing for it.
			mu.Lock()
			refuseSecond = false
			mu.Unlock()
			before := videoRowsDump(t, p.db, videoID)
			sends := postCount()
			switch c.runType {
			case state.RunTypeManual:
				adm, err := p.AdmitRunNow(ctx)
				require.NoError(t, err)
				require.True(t, adm.Resumed())
				require.Equal(t, r.ID, adm.RunID())
				adm.Execute(ctx)
			default:
				require.NoError(t, p.RecoverStartup(ctx))
			}
			ended, err := p.stateStore.GetRun(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, state.RunStatusCompleted, ended.Status, "reason: %v", ended.InterruptReason)
			require.Equal(t, sends, postCount(), "R sent nothing for V")
			require.Equal(t, before, videoRowsDump(t, p.db, videoID), "R wrote no transition, decision, outcome, action or queue change for V")
			require.Equal(t, int64(1), env.countRows(t, `SELECT COUNT(*) FROM executed_action ea JOIN run r ON r.id=ea.run_id WHERE ea.video_id=? AND ea.outcome='succeeded' AND r.run_type IN ('manual','scheduled')`, videoID),
				"V keeps exactly one production success")
			require.Equal(t, r2.ID, env.countRows(t, `SELECT rd.run_id FROM executed_action ea JOIN route_decision rd ON rd.id=ea.route_decision_id WHERE ea.video_id=? AND ea.outcome='succeeded'`, videoID),
				"the success is under R2's decision")
			if c.dismissed {
				require.Zero(t, env.countRows(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status IN ('pending','retrying')`, videoID))
			} else {
				require.Equal(t, int64(1), env.countRows(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending'`, videoID), "B stays queued for the next run")
			}
			class, _, err = state.ClassifyVideoTx(ctx, p.db, videoID)
			require.NoError(t, err)
			require.Equal(t, state.VideoClassPartlyDone, class)

			// Final clause: the next new run continues V under R2's decision
			// and sends only B. With R2's item dismissed nothing claims V; the
			// BR-037 re-queue (ARS-005 step 6) is what lists it as stranded.
			if !c.dismissed {
				r3, err := p.StartRun(ctx, state.RunTypeScheduled)
				require.NoError(t, err)
				p.Execute(ctx, r3.ID)
				done, err := p.stateStore.GetRun(ctx, r3.ID)
				require.NoError(t, err)
				require.Equal(t, state.RunStatusCompleted, done.Status, "reason: %v", done.InterruptReason)
				require.Equal(t, sends+1, postCount(), "exactly one request: B")
				require.Equal(t, []playlistInsertion{{PlaylistID: "PLjazz", VideoID: "V1"}, {PlaylistID: "PLsecond", VideoID: "V1"}}, env.server.Insertions(),
					"A was sent by R2, B by the next run, nothing twice")
				require.Zero(t, env.countRows(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=?`, r3.ID, videoID), "the next run makes no decision of its own for V")
				require.Equal(t, r2.ID, env.countRows(t, `SELECT rd.run_id FROM executed_action ea JOIN route_decision rd ON rd.id=ea.route_decision_id WHERE ea.run_id=? AND ea.video_id=? AND ea.target_playlist_id='PLsecond' AND ea.outcome='succeeded'`, r3.ID, videoID),
					"B is sent under R2's decision")
			}
		})
	}
}

func (env *idempotencyTestEnv) countRows(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, env.pipeline.db.QueryRow(query, args...).Scan(&n))
	return n
}

// Final retry dispatch (ARS-006 §5): a claimed retry whose video became final
// before processing is settled without evaluation or a send. Another run's
// outcome closes the owned rows with the reason; the run's own outcome (M2) is
// acknowledged as an ordinary completion.
func TestRetryDispatchOfFinalVideo(t *testing.T) {
	for _, tc := range []struct {
		name       string
		own        bool
		wantReason string
	}{
		{"another run finalized it", false, "final_outcome"},
		{"this run finalized it before an interruption (M2)", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, nil)
			p := env.pipeline
			qs, err := queue.NewStore(p.db)
			require.NoError(t, err)
			p.queueStore = qs
			res, err := p.db.ExecContext(ctx, `INSERT INTO run (started_at, run_type, status) VALUES (?, 'manual', 'completed')`, time.Now().UTC())
			require.NoError(t, err)
			origin, _ := res.LastInsertId()
			res, err = p.db.ExecContext(ctx, `INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES ('jazz-v', 'UC1', ?, 'completed', 'deferred')`, origin)
			require.NoError(t, err)
			video, _ := res.LastInsertId()
			item, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: video, RunID: origin, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)

			run, err := p.StartRun(ctx, state.RunTypeScheduled) // claims the item while V is open
			require.NoError(t, err)
			var status string
			require.NoError(t, p.db.QueryRow(`SELECT status FROM deferred_item WHERE id=?`, item).Scan(&status))
			require.Equal(t, "retrying", status)
			// V becomes final after the claim.
			_, err = p.db.ExecContext(ctx, `UPDATE video SET processing_state='completed', final_outcome='accepted' WHERE id=?`, video)
			require.NoError(t, err)
			if tc.own {
				_, err = p.db.ExecContext(ctx, `UPDATE run_video_outcome SET final_outcome='accepted' WHERE run_id=? AND video_id=?`, run.ID, video)
				require.NoError(t, err)
			}

			p.Execute(ctx, run.ID)

			loaded, err := p.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			require.Equal(t, state.RunStatusCompleted, loaded.Status, "reason: %v", loaded.InterruptReason)
			require.Empty(t, env.server.Insertions())
			var reason *string
			require.NoError(t, p.db.QueryRow(`SELECT status, closed_reason FROM deferred_item WHERE id=?`, item).Scan(&status, &reason))
			require.Equal(t, "completed", status)
			if tc.wantReason == "" {
				require.Nil(t, reason)
			} else {
				require.NotNil(t, reason)
				require.Equal(t, tc.wantReason, *reason)
			}
			var outcome string
			require.NoError(t, p.db.QueryRow(`SELECT final_outcome FROM video WHERE id=?`, video).Scan(&outcome))
			require.Equal(t, "accepted", outcome)
			require.Zero(t, env.countRows(t, `SELECT COUNT(*) FROM route_decision WHERE video_id=? AND run_id=?`, video, run.ID))
		})
	}
}
