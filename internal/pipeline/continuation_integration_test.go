//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/stats"
)

// BR-039 continuation (ARS-006 §7, tests K9–K11, K13–K15, D1 regression).
// Real migrated SQLite, the real pipeline and executor, the guarded send path,
// and a counting local provider. The BR-039 example: one video matched to
// "Lofi" (PLjazz) and "Study" (PLsecond); "Lofi" is sent, "Study" is not.

const (
	contLofi  = "PLjazz"
	contStudy = "PLsecond"

	contQuotaBody     = `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`
	contNotFoundBody  = `{"error":{"errors":[{"reason":"playlistNotFound"}],"code":404,"message":"Not Found"}}`
	contModeQuota     = "quota"
	contModePermanent = "permanent"
)

type contEnv struct {
	*idempotencyTestEnv
	ctx     context.Context
	qs      *queue.Store
	studyID int64 // the "Study" route

	mu       sync.Mutex
	mode     map[string]string // playlist -> "", quota or permanent
	attempts []string          // every playlist POST the provider received
}

func newContEnv(t *testing.T) *contEnv {
	t.Helper()
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	qs, err := queue.NewStore(env.pipeline.db)
	require.NoError(t, err)
	env.pipeline.queueStore = qs
	studyID, err := router.NewStore(env.pipeline.db).CreateRoute(ctx, &router.Route{Name: "Study", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "jazz"}},
		Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: contStudy}}})
	require.NoError(t, err)
	e := &contEnv{idempotencyTestEnv: env, ctx: ctx, qs: qs, studyID: studyID, mode: map[string]string{}}
	env.server.insertDecision = func(playlistID, _ string) (int, string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.attempts = append(e.attempts, playlistID)
		switch e.mode[playlistID] {
		case contModeQuota:
			return 403, contQuotaBody
		case contModePermanent:
			return 404, contNotFoundBody
		}
		return 0, ""
	}
	return e
}

func (e *contEnv) setMode(playlist, mode string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode[playlist] = mode
}

func (e *contEnv) attemptsAt(playlist string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, a := range e.attempts {
		if a == playlist {
			n++
		}
	}
	return n
}

func (e *contEnv) allAttempts() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.attempts)
}

func (e *contEnv) one(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	return e.countRows(t, query, args...)
}

func (e *contEnv) text(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s sql.NullString
	require.NoError(t, e.pipeline.db.QueryRow(query, args...).Scan(&s))
	return s.String
}

// scheduled runs one scheduled production run to its end.
func (e *contEnv) scheduled(t *testing.T) int64 {
	t.Helper()
	r, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	e.pipeline.Execute(e.ctx, r.ID)
	loaded, err := e.stateStore.GetRun(e.ctx, r.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status, "run %d reason: %v", r.ID, loaded.InterruptReason)
	return r.ID
}

// partlyDone runs the original run R0: "Lofi" is sent, "Study" is deferred by a
// provider quota refusal, so V holds one production success and one pending
// queue item. It returns R0, V and R0's "Study" decision.
func (e *contEnv) partlyDone(t *testing.T) (r0, video, studyDecision int64) {
	t.Helper()
	e.setMode(contStudy, contModeQuota)
	r0 = e.scheduled(t)
	e.setMode(contStudy, "")
	video = e.one(t, `SELECT id FROM video WHERE video_id='V1'`)
	studyDecision = e.one(t, `SELECT id FROM route_decision WHERE run_id=? AND video_id=? AND route_id=? AND decision='matched'`, r0, video, e.studyID)
	class, _, err := state.ClassifyVideoTx(e.ctx, e.pipeline.db, video)
	require.NoError(t, err)
	require.Equal(t, state.VideoClassPartlyDone, class)
	require.Equal(t, int64(1), e.one(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending'`, video))
	require.Equal(t, []playlistInsertion{{PlaylistID: contLofi, VideoID: "V1"}}, e.server.Insertions())
	return r0, video, studyDecision
}

// disableRoutes edits the routes so that V matches nothing any more.
func (e *contEnv) disableRoutes(t *testing.T) {
	t.Helper()
	rs := router.NewStore(e.pipeline.db)
	routes, err := rs.ListRoutes(e.ctx)
	require.NoError(t, err)
	for i := range routes {
		routes[i].IsEnabled = false
		require.NoError(t, rs.UpdateRoute(e.ctx, &routes[i]))
	}
}

func (e *contEnv) routeActionAttempts(t *testing.T, runID int64) int {
	t.Helper()
	routes, err := stats.NewStore(e.pipeline.db).ListRouteStats(e.ctx, runID)
	require.NoError(t, err)
	n := 0
	for _, r := range routes {
		n += r.ActionsExecuted
	}
	return n
}

// K9: the BR-039 example. Run 41 sends only the missing target, under run
// 40's decision, and shows and counts it as its own.
func TestContinuationSendsOnlyMissingTargetK9(t *testing.T) {
	e := newContEnv(t)
	r40, video, studyDecision := e.partlyDone(t)
	e.disableRoutes(t) // V now matches nothing: a re-evaluation would reject it
	attempts40 := e.routeActionAttempts(t, r40)
	requests := len(e.server.log)

	r41 := e.scheduled(t)

	require.Zero(t, e.one(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=?`, r41, video), "no run-41 decision for V")
	for _, r := range e.server.log[requests:] {
		require.NotContains(t, r.Path, "/videos", "V's metadata is not fetched again")
	}
	require.Equal(t, []playlistInsertion{{PlaylistID: contLofi, VideoID: "V1"}, {PlaylistID: contStudy, VideoID: "V1"}}, e.server.Insertions(),
		"exactly one more insert, to the missing playlist")
	require.Equal(t, 1, e.attemptsAt(contLofi), "Lofi is never sent again")
	require.Equal(t, 2, e.attemptsAt(contStudy), "run 40's refused attempt and run 41's send")

	require.Equal(t, studyDecision, e.one(t, `SELECT route_decision_id FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=? AND outcome='succeeded'`, r41, video, contStudy),
		"Study's send is attributed to run 40's decision")
	require.Equal(t, "already_completed", e.text(t, `SELECT outcome_reason FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=? AND outcome='not_attempted'`, r41, video, contLofi))
	require.Equal(t, int64(1), e.one(t, `SELECT COUNT(*) FROM executed_action WHERE video_id=? AND target_playlist_id=? AND outcome='succeeded'`, video, contLofi), "Lofi's success is unchanged")

	require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
	require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, r41, video))

	continued, err := e.stateStore.ListContinuedActions(e.ctx, r41, video)
	require.NoError(t, err)
	require.Len(t, continued, 2)
	for _, c := range continued {
		require.Equal(t, r40, c.DecisionRunID)
	}
	require.Equal(t, 1, e.routeActionAttempts(t, r41), "run 41's route statistics count one attempt")
	require.Equal(t, attempts40, e.routeActionAttempts(t, r40), "run 40's do not change")
}

// K16, behavior: no later write of any outcome changes a succeeded row, in the
// sending run's own evidence or through a continuation.
func TestSucceededActionCannotBeDowngradedK16(t *testing.T) {
	e := newContEnv(t)
	r0, video, studyDecision := e.partlyDone(t)
	r41 := e.scheduled(t)
	reason := "quota_exhausted"
	for _, tc := range []struct{ run, decision int64 }{{r0, 0}, {r41, studyDecision}} {
		target, decision := contLofi, e.one(t, `SELECT route_decision_id FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=?`, tc.run, video, contLofi)
		if tc.run == r41 {
			target, decision = contStudy, tc.decision
		}
		before := e.text(t, `SELECT outcome||'|'||COALESCE(outcome_reason,'')||'|'||COALESCE(executed_at,'') FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=?`, tc.run, video, target)
		require.True(t, strings.HasPrefix(before, "succeeded|") || strings.HasPrefix(before, "not_attempted|"), before)
		if !strings.HasPrefix(before, "succeeded|") {
			continue
		}
		for _, outcome := range []state.ActionOutcome{state.ActionOutcomeFailed, state.ActionOutcomeDeferred, state.ActionOutcomeNotAttempted} {
			now := time.Now().UTC()
			require.NoError(t, recordActionEvidenceTx(e.ctx, e.pipeline.db, tc.run, video, action{RouteDecisionID: decision, ActionType: "playlist_insert", TargetPlaylistID: target}, outcome, &reason, &now))
			require.Equal(t, before, e.text(t, `SELECT outcome||'|'||COALESCE(outcome_reason,'')||'|'||COALESCE(executed_at,'') FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=?`, tc.run, video, target), "%s over succeeded", outcome)
		}
	}
	require.Equal(t, int64(2), e.one(t, `SELECT COUNT(*) FROM executed_action WHERE video_id=? AND outcome='succeeded'`, video))
}

// K10: a continuation that fails or defers again leaves V partly done, and
// the next send is again only the missing target.
func TestContinuationStillPartlyDoneK10(t *testing.T) {
	t.Run("permanent failure keeps V partly done and its send uncertain", func(t *testing.T) {
		e := newContEnv(t)
		_, video, _ := e.partlyDone(t)
		e.setMode(contStudy, contModePermanent)
		e.scheduled(t)

		require.Equal(t, "failed", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
		entry := e.one(t, `SELECT id FROM error_entry WHERE video_id=? AND status='active'`, video)
		class, _, err := state.ClassifyVideoTx(e.ctx, e.pipeline.db, video)
		require.NoError(t, err)
		require.Equal(t, state.VideoClassPartlyDone, class)
		attempts := e.allAttempts()

		// A refused insert that is not a quota refusal leaves the send guard
		// (ARS-001: the application state is unknown), so the retry is the
		// same continuation, refused at the claim: nothing is sent again.
		e.setMode(contStudy, "")
		require.NoError(t, e.pipeline.RetryQueueEntry(e.ctx, entry, true))
		require.Equal(t, attempts, e.allAttempts(), "no request, and Lofi is not resent")
		require.Equal(t, 1, e.attemptsAt(contLofi))
		require.Equal(t, 1, len(e.server.Insertions()))
		require.Equal(t, "failed", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
		require.Contains(t, e.text(t, `SELECT error_message FROM run_video_outcome WHERE run_id=(SELECT MAX(run_id) FROM run_video_outcome WHERE video_id=?) AND video_id=?`, video, video), "mutation_application_uncertain")
	})

	for _, via := range []string{"next run", "user retry"} {
		t.Run("quota again then "+via, func(t *testing.T) {
			e := newContEnv(t)
			_, video, _ := e.partlyDone(t)
			e.setMode(contStudy, contModeQuota)
			e.scheduled(t)

			item := e.one(t, `SELECT id FROM deferred_item WHERE video_id=? AND status='pending'`, video) // Study stays queued
			require.Equal(t, 1, e.attemptsAt(contLofi))
			class, _, err := state.ClassifyVideoTx(e.ctx, e.pipeline.db, video)
			require.NoError(t, err)
			require.Equal(t, state.VideoClassPartlyDone, class)

			e.setMode(contStudy, "")
			if via == "next run" {
				e.scheduled(t)
			} else {
				require.NoError(t, e.pipeline.RetryQueueEntry(e.ctx, item, false))
			}
			require.Equal(t, []playlistInsertion{{PlaylistID: contLofi, VideoID: "V1"}, {PlaylistID: contStudy, VideoID: "V1"}}, e.server.Insertions())
			require.Equal(t, 1, e.attemptsAt(contLofi), "only Study is sent")
			require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
		})
	}
}

// K11: send safety. A held guard on the missing target, or a version-0
// original run with that target unresolved, refuses the send.
func TestContinuationSendSafetyK11(t *testing.T) {
	t.Run("held guard", func(t *testing.T) {
		e := newContEnv(t)
		r0, video, studyDecision := e.partlyDone(t)
		_, err := e.pipeline.db.ExecContext(e.ctx, `INSERT INTO playlist_send_guard(video_id,target_playlist_id,action_type,run_id,route_decision_id,authorization_id,prepared_at) VALUES(?,?,'playlist_insert',?,?,'seed-auth',?)`,
			video, contStudy, r0, studyDecision, time.Now().UTC().Format(time.RFC3339Nano))
		require.NoError(t, err)
		attempts := e.allAttempts()

		e.scheduled(t)

		require.Equal(t, attempts, e.allAttempts(), "zero provider requests")
		require.Equal(t, 1, len(e.server.Insertions()))
		require.Contains(t, e.text(t, `SELECT error_message FROM run_video_outcome WHERE video_id=? AND run_id=(SELECT MAX(id) FROM run)`, video), "mutation_application_uncertain")
		require.Equal(t, int64(1), e.one(t, `SELECT COUNT(*) FROM playlist_send_guard WHERE video_id=? AND target_playlist_id=?`, video, contStudy), "the guard is kept")
	})

	t.Run("version-0 original run", func(t *testing.T) {
		e := newContEnv(t)
		r0, video, _ := e.partlyDone(t)
		_, err := e.pipeline.db.ExecContext(e.ctx, `UPDATE run SET action_authority_version=0, mutation_safety_version=0 WHERE id=?`, r0)
		require.NoError(t, err)
		attempts := e.allAttempts()

		e.scheduled(t)

		require.Equal(t, attempts, e.allAttempts(), "the legacy veto refuses; nothing is sent")
		require.Equal(t, 1, len(e.server.Insertions()))
		require.Equal(t, "failed", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
	})
}

// K13 (AC10): an unreadable or ambiguous original decision sends nothing,
// records no decision, and retains V with original_decision_unavailable. A
// user retry gives the same result.
func TestContinuationUnreadableOriginalDecisionK13(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(t *testing.T, e *contEnv, r0, video int64)
	}{
		{"snapshot unreadable", func(t *testing.T, e *contEnv, r0, _ int64) {
			e.exec(t, `UPDATE config_version SET snapshot='not json' WHERE id=(SELECT config_version_id FROM run WHERE id=?)`, r0)
		}},
		{"config missing", func(t *testing.T, e *contEnv, r0, _ int64) {
			e.exec(t, `UPDATE run SET config_version_id=NULL WHERE id=?`, r0)
		}},
		{"successes under two runs", func(t *testing.T, e *contEnv, r0, video int64) {
			res, err := e.pipeline.db.ExecContext(e.ctx, `INSERT INTO run (started_at, run_type, status, action_authority_version, mutation_safety_version) VALUES (?, 'manual', 'completed', 1, 1)`, time.Now().UTC())
			require.NoError(t, err)
			other, _ := res.LastInsertId()
			res, err = e.pipeline.db.ExecContext(e.ctx, `INSERT INTO route_decision (run_id, video_id, route_id, decision, evaluation_order) VALUES (?,?,?,'matched',1)`, other, video, e.routeID)
			require.NoError(t, err)
			d, _ := res.LastInsertId()
			e.exec(t, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome,executed_at) VALUES(?,?,?,'playlist_insert','PLelsewhere','succeeded',?)`, d, other, video, time.Now().UTC().Format(time.RFC3339Nano))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newContEnv(t)
			r0, video, _ := e.partlyDone(t)
			tc.spoil(t, e, r0, video)
			attempts := e.allAttempts()

			r41 := e.scheduled(t)
			require.Equal(t, attempts, e.allAttempts(), "zero sends")
			require.Zero(t, e.one(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=?`, r41, video), "zero decisions")
			require.Equal(t, "failed", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
			require.Contains(t, e.text(t, `SELECT error_message FROM run_video_outcome WHERE run_id=? AND video_id=?`, r41, video), "code=original_decision_unavailable")
			require.Equal(t, int64(1), e.one(t, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND status='active'`, video))

			entry := e.one(t, `SELECT id FROM error_entry WHERE video_id=? AND status='active'`, video)
			require.NoError(t, e.pipeline.RetryQueueEntry(e.ctx, entry, true))
			require.Equal(t, attempts, e.allAttempts(), "a user retry sends nothing either")
			require.Equal(t, int64(1), e.one(t, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND status='active'`, video), "the retry leaves one active Errors entry")
			require.Equal(t, "failed", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
		})
	}
}

func (e *contEnv) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := e.pipeline.db.ExecContext(e.ctx, query, args...)
	require.NoError(t, err, query)
}

// K14: the process dies after the "Study" guard claim. On restart V is
// continued under run 40's decision and nothing is sent again: the surviving
// guard makes the target uncertain, and it is listed with the sending run.
func TestContinuationCrashAfterGuardClaimK14(t *testing.T) {
	e := newContEnv(t)
	r0, video, studyDecision := e.partlyDone(t)

	// StartRun claims V's queue item for the receiving run; the claim below is
	// the real send claim a process would make just before its request.
	r41, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	claimed, _, err := e.stateStore.ClaimPlaylistSend(e.ctx, state.PlaylistSendClaim{RunID: r41.ID, VideoID: video, RouteDecisionID: studyDecision,
		TargetPlaylistID: contStudy, ActionType: "playlist_insert", AuthorizationID: "crashed-process", DecisionRunID: r0}, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, state.PlaylistClaimed, claimed)
	require.NoError(t, e.stateStore.InterruptRun(e.ctx, r41.ID, "test: killed after guard claim"))
	attempts := e.allAttempts()

	require.NoError(t, e.pipeline.RecoverStartup(e.ctx))

	require.Equal(t, attempts, e.allAttempts(), "nothing is resent: the target is uncertain")
	require.Equal(t, 1, len(e.server.Insertions()))
	require.Zero(t, e.one(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=?`, r41.ID, video), "V is continued under run 40, not re-evaluated")
	require.Equal(t, int64(1), e.one(t, `SELECT COUNT(*) FROM playlist_send_guard WHERE video_id=? AND target_playlist_id=?`, video, contStudy))
	listed, err := e.stateStore.ListUnresolvedMutations(e.ctx, r41.ID, []int64{video})
	require.NoError(t, err)
	require.Len(t, listed[video], 1, "the guard shows in the sending run's unresolved mutations")
}

// K15 (AC6): an open deferred video with decisions and no production success
// is evaluated under the receiving run, as before.
func TestContinuationLeavesOpenVideosAloneK15(t *testing.T) {
	e := newContEnv(t)
	consumed, err := e.ledger.ConsumedToday(e.ctx)
	require.NoError(t, err)
	require.NoError(t, e.ledger.SetDailyLimit(e.ctx, consumed+49)) // no room: both targets defer
	r0 := e.scheduled(t)
	require.Empty(t, e.server.Insertions())
	video := e.one(t, `SELECT id FROM video WHERE video_id='V1'`)
	class, _, err := state.ClassifyVideoTx(e.ctx, e.pipeline.db, video)
	require.NoError(t, err)
	require.Equal(t, state.VideoClassOpen, class)
	require.NoError(t, e.ledger.SetDailyLimit(e.ctx, 1_000_000))

	r1 := e.scheduled(t)

	require.NotEqual(t, r0, r1)
	require.Equal(t, int64(2), e.one(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=? AND decision='matched'`, r1, video), "evaluated under the receiving run")
	require.Len(t, e.server.Insertions(), 2)
	require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
}

// D1 regression (gate b39-gate-ii): R defers both of V's targets, R2 sends A
// and defers B, and V is then partly done under R2. Before continuation every
// run failed on it and a user retry answered 500.
func TestContinuationAfterTakeoverD1(t *testing.T) {
	setup := func(t *testing.T) (*contEnv, int64, int64) {
		e := newContEnv(t)
		consumed, err := e.ledger.ConsumedToday(e.ctx)
		require.NoError(t, err)
		require.NoError(t, e.ledger.SetDailyLimit(e.ctx, consumed+49))
		e.scheduled(t) // R: both targets deferred, V open
		require.NoError(t, e.ledger.SetDailyLimit(e.ctx, 1_000_000))
		e.setMode(contStudy, contModeQuota)
		r2 := e.scheduled(t) // R2: evaluates V, sends A, defers B
		e.setMode(contStudy, "")
		video := e.one(t, `SELECT id FROM video WHERE video_id='V1'`)
		class, _, err := state.ClassifyVideoTx(e.ctx, e.pipeline.db, video)
		require.NoError(t, err)
		require.Equal(t, state.VideoClassPartlyDone, class)
		require.Equal(t, []playlistInsertion{{PlaylistID: contLofi, VideoID: "V1"}}, e.server.Insertions())
		return e, r2, video
	}

	t.Run("next run completes with one insert to B", func(t *testing.T) {
		e, r2, video := setup(t)
		r4 := e.scheduled(t)
		require.Equal(t, []playlistInsertion{{PlaylistID: contLofi, VideoID: "V1"}, {PlaylistID: contStudy, VideoID: "V1"}}, e.server.Insertions())
		require.Equal(t, 1, e.attemptsAt(contLofi))
		require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
		require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, r4, video))
		continued, err := e.stateStore.ListContinuedActions(e.ctx, r4, video)
		require.NoError(t, err)
		require.NotEmpty(t, continued)
		for _, c := range continued {
			require.Equal(t, r2, c.DecisionRunID)
		}
	})

	t.Run("user retry succeeds", func(t *testing.T) {
		e, _, video := setup(t)
		item := e.one(t, `SELECT id FROM deferred_item WHERE video_id=? AND status='pending'`, video)
		require.NoError(t, e.pipeline.RetryQueueEntry(e.ctx, item, false))
		require.Equal(t, []playlistInsertion{{PlaylistID: contLofi, VideoID: "V1"}, {PlaylistID: contStudy, VideoID: "V1"}}, e.server.Insertions())
		require.Equal(t, "accepted", e.text(t, `SELECT final_outcome FROM video WHERE id=?`, video))
		require.True(t, strings.HasPrefix(e.text(t, `SELECT status FROM deferred_item WHERE id=?`, item), "completed"))
	})
}
