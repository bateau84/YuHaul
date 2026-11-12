//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// V2 of ARS-005 §11.2, step-3 form: the stranded scan does not exist yet, so
// its blocked set is the shared predicates evaluated the way the scan will
// evaluate them (HeldSendGuard OR LegacySendVeto with assume_acted = 1, NULL
// target). The send side is a real scheduled production run on real SQLite
// against a local fake YouTube: it claims the queued work, evaluates the
// video against the captured configuration and sends through
// ClaimPlaylistSend and the guarded dispatch.

const (
	v2TargetA = "PLv2targetA"
	v2TargetB = "PLjazz" // the target of the harness's "Jazz" route
)

// v2Seed is one stranded video. Every seed is routed to target B; the branch 4
// seed is also routed to A (its title also matches the A route).
type v2Seed struct {
	name    string
	title   string
	blocked bool // expected scan result, from the veto definitions, not from the SQL
}

func v2Seeds() []v2Seed {
	return []v2Seed{
		{"branch1_matched_decision", "Jazz Marimba Concert Recording", true},
		{"branch1_missing_config", "Jazz Harpsichord Midnight Sonata", true},
		{"branch1_missing_route", "Jazz Theremin Desert Journey", true},
		{"branch2_unresolved_action", "Jazz Banjo Festival Highlights", true},
		{"branch3_not_acted_at_scan", "Jazz Accordion Harbour Evening", true},
		{"branch4_target_a_succeeded", "Jazz Bassoon Branch4 Workshop", true},
		{"held_guard", "Jazz Cello Cathedral Rehearsal", true},
		{"uncertain_addition_with_guard", "Jazz Oboe Riverside Picnic", true},
		{"guard_on_succeeded_target", "Jazz Trumpet Lighthouse Session", false},
		{"control_no_evidence", "Jazz Vibraphone Winter Anthology", false},
	}
}

type v2Env struct {
	*idempotencyTestEnv
	ctx      context.Context
	cfgID    int64
	jazzID   int64
	routeAID int64
	ghostID  int64
	evidence int64 // a version-1 production run that hosts non-vetoing evidence
	videos   map[string]*state.Video
	deferred map[string]int64

	mu    sync.Mutex
	posts []playlistInsertion // every playlist POST the fake YouTube received
}

func (e *v2Env) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := e.pipeline.db.ExecContext(e.ctx, query, args...)
	require.NoError(t, err, query)
}

func (e *v2Env) legacyRun(t *testing.T, cfgID *int64, phase string) int64 {
	t.Helper()
	id, err := e.stateStore.CreateRun(e.ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusInterrupted, ConfigVersionID: cfgID})
	require.NoError(t, err)
	if phase != "" {
		require.NoError(t, e.stateStore.SetRunPhase(e.ctx, id, phase))
	}
	return id
}

func (e *v2Env) discover(t *testing.T, name string, runID int64) *state.Video {
	t.Helper()
	var title string
	for _, s := range v2Seeds() {
		if s.name == name {
			title = s.title
		}
	}
	v, err := e.machine.DiscoverVideo(e.ctx, runID, "v2-"+name, "UC1")
	require.NoError(t, err)
	now := time.Now().UTC()
	channel := "Test Channel"
	require.NoError(t, metadata.NewStore(e.pipeline.db).Upsert(e.ctx, metadata.TransientMetadata{
		VideoID: v.VideoID, Title: &title, ChannelName: &channel,
		FetchedAt: now, ExpiresAt: now.Add(24 * time.Hour), IsActive: true,
	}))
	e.videos[name] = v
	return v
}

func (e *v2Env) decision(t *testing.T, v *state.Video, runID, routeID int64, d state.RouteDecisionType) int64 {
	t.Helper()
	return createRouteDecision(t, e.ctx, e.stateStore, v.ID, runID, routeID, 1, d)
}

// seed builds the legacy evidence of every veto branch; each video is
// stranded (not_yet_evaluated) apart from that evidence.
func (e *v2Env) seed(t *testing.T) {
	t.Helper()
	// Branch 1, matched decision: the captured config routes to B, no success on B.
	r := e.legacyRun(t, &e.cfgID, "")
	v := e.discover(t, "branch1_matched_decision", r)
	e.decision(t, v, r, e.jazzID, state.RouteDecisionMatched)

	// Branch 1, missing config.
	r = e.legacyRun(t, nil, "")
	v = e.discover(t, "branch1_missing_config", r)
	e.decision(t, v, r, e.jazzID, state.RouteDecisionMatched)

	// Branch 1, missing route: the decision names a route the snapshot lacks.
	r = e.legacyRun(t, &e.cfgID, "")
	v = e.discover(t, "branch1_missing_route", r)
	e.decision(t, v, r, e.ghostID, state.RouteDecisionMatched)

	// Branch 2: an unresolved playlist_insert action in the legacy run.
	r = e.legacyRun(t, &e.cfgID, "")
	v = e.discover(t, "branch2_unresolved_action", r)
	d := e.decision(t, v, r, e.jazzID, state.RouteDecisionNotMatched)
	e.exec(t, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(?,?,?,'playlist_insert',?,'failed')`, d, r, v.ID, v2TargetB)

	// Branch 3: discovered by a legacy run in action_execution, no matched
	// decision, and the video is NOT acted (it is not_yet_evaluated).
	r = e.legacyRun(t, &e.cfgID, string(PhaseActionExecution))
	v = e.discover(t, "branch3_not_acted_at_scan", r)
	require.Equal(t, state.VideoStateNotYetEvaluated, v.ProcessingState)

	// Branch 4: a legacy retry run in action_execution holds the video's
	// retained error entry; the origin run recorded success for A only.
	// The retry run ended (failed), so the new run may reclaim the video.
	retry := e.legacyRun(t, &e.cfgID, string(PhaseActionExecution))
	e.exec(t, `UPDATE run SET status='failed' WHERE id=?`, retry)
	v = e.discover(t, "branch4_target_a_succeeded", e.evidence)
	d = e.decision(t, v, e.evidence, e.jazzID, state.RouteDecisionNotMatched)
	e.exec(t, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(?,?,?,'playlist_insert',?,'succeeded')`, d, e.evidence, v.ID, v2TargetA)
	e.exec(t, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status,retry_run_id) VALUES(?,?,'runtime','legacy','retrying',?)`, v.ID, e.evidence, retry)
	e.exec(t, `INSERT INTO run_video_outcome(run_id,video_id,final_outcome,error_message,updated_at) VALUES(?,?,'failed','legacy failure',?)`, retry, v.ID, time.Now().UTC().Format(time.RFC3339Nano))

	// A held guard on B.
	v = e.discover(t, "held_guard", e.evidence)
	d = e.decision(t, v, e.evidence, e.jazzID, state.RouteDecisionNotMatched)
	e.guard(t, v, v2TargetB, d)

	// An uncertain addition on B with its guard.
	v = e.discover(t, "uncertain_addition_with_guard", e.evidence)
	d = e.decision(t, v, e.evidence, e.jazzID, state.RouteDecisionNotMatched)
	e.guard(t, v, v2TargetB, d)
	e.exec(t, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome,outcome_reason) VALUES(?,?,?,'playlist_insert',?,'failed','mutation_application_uncertain')`, d, e.evidence, v.ID, v2TargetB)

	// A guard whose target (A) already succeeded; the video is routed to B only.
	v = e.discover(t, "guard_on_succeeded_target", e.evidence)
	d = e.decision(t, v, e.evidence, e.jazzID, state.RouteDecisionNotMatched)
	e.guard(t, v, v2TargetA, d)
	e.exec(t, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(?,?,?,'playlist_insert',?,'succeeded')`, d, e.evidence, v.ID, v2TargetA)

	// A control with no evidence at all.
	e.discover(t, "control_no_evidence", e.evidence)
}

func (e *v2Env) guard(t *testing.T, v *state.Video, target string, decisionID int64) {
	t.Helper()
	e.exec(t, `INSERT INTO playlist_send_guard(video_id,target_playlist_id,action_type,run_id,route_decision_id,authorization_id,prepared_at) VALUES(?,?,'playlist_insert',?,?,'seed-auth',?)`,
		v.ID, target, e.evidence, decisionID, time.Now().UTC().Format(time.RFC3339Nano))
}

// scanBlocked evaluates the shared predicates as the scan will (ARS-005 §3.1):
// the video is unfinished, the target is NULL, and the receiving run will have
// made it acted (assume_acted). It returns the external ids that are blocked.
func (e *v2Env) scanBlocked(t *testing.T, assumeActed int) []string {
	t.Helper()
	rows, err := e.pipeline.db.QueryContext(e.ctx, `
SELECT subject.youtube_video_id FROM (
  SELECT v.id AS video_id, v.video_id AS youtube_video_id, CAST(NULL AS TEXT) AS target_playlist_id,
         0 AS receiving_run_id, ?1 AS assume_acted
  FROM video v
  WHERE v.processing_state <> 'completed'
    AND (v.final_outcome IS NULL OR v.final_outcome = 'not_yet_evaluated')) subject
WHERE EXISTS (`+state.HeldSendGuard+`)
   OR EXISTS (SELECT 1 FROM run old_run WHERE `+state.LegacySendVeto+`)
ORDER BY subject.youtube_video_id`, assumeActed)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		out = append(out, id)
	}
	require.NoError(t, rows.Err())
	return out
}

func (e *v2Env) postsFor(externalID, playlist string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, p := range e.posts {
		if p.VideoID == externalID && (playlist == "" || p.PlaylistID == playlist) {
			n++
		}
	}
	return n
}

func newV2Env(t *testing.T) *v2Env {
	t.Helper()
	ctx := context.Background()
	e := &v2Env{idempotencyTestEnv: setupIdempotencyTest(t, nil), ctx: ctx, videos: map[string]*state.Video{}, deferred: map[string]int64{}}
	e.server.insertDecision = func(playlistID, videoID string) (int, string) {
		e.mu.Lock()
		e.posts = append(e.posts, playlistInsertion{PlaylistID: playlistID, VideoID: videoID})
		e.mu.Unlock()
		return 0, "" // the fake accepts every insert
	}

	db := e.pipeline.db
	rs := router.NewStore(db)
	var err error
	e.jazzID = e.routeID
	// Every seed matches "jazz" and goes to B; the branch 4 seed also matches
	// "branch4" and goes to A.
	e.routeAID, err = rs.CreateRoute(ctx, &router.Route{Name: "Branch4 to A", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "OR",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "branch4"}},
		Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: v2TargetA}}})
	require.NoError(t, err)
	cfg, err := config.NewService(rs, config.NewStore(db)).ActiveVersionID(ctx)
	require.NoError(t, err)
	e.cfgID = *cfg
	// Created after the snapshot, so the snapshot lacks it (missing-route seed).
	// It matches nothing the seeds contain.
	e.ghostID, err = rs.CreateRoute(ctx, &router.Route{Name: "Ghost", EvaluationOrder: 3, IsEnabled: true, ConditionComposition: "OR",
		Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: router.ConditionFieldTitle, Operator: router.ConditionOperatorContains, Value: "zzz-never-matches"}},
		Actions:    []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLghost"}}})
	require.NoError(t, err)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	e.evidence, err = e.stateStore.CreateGuardedMutationRunTx(ctx, tx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusInterrupted, ConfigVersionID: &e.cfgID, ActionAuthorityVersion: 1}, true)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return e
}

func TestV2_ScanAndSendTimeVetoAgree(t *testing.T) {
	e := newV2Env(t)
	e.seed(t)

	// The oracle is the veto definitions in the seeds table, not the SQL.
	var wantBlocked, wantUnblocked []string
	for _, s := range v2Seeds() {
		ext := e.videos[s.name].VideoID
		if s.blocked {
			wantBlocked = append(wantBlocked, ext)
		} else {
			wantUnblocked = append(wantUnblocked, ext)
		}
	}
	sort.Strings(wantBlocked)
	sort.Strings(wantUnblocked)
	require.NotEmpty(t, wantBlocked)
	require.NotEmpty(t, wantUnblocked)

	// 1. The scan-side predicate (assume_acted = 1).
	blocked := e.scanBlocked(t, 1)
	require.Equal(t, wantBlocked, blocked, "scan-side blocked set")

	// Branch 3 is blocked only because the scan assumes the receiving run will
	// have acted the video: with the live (not acted) state it is not blocked.
	b3 := e.videos["branch3_not_acted_at_scan"]
	require.Contains(t, blocked, b3.VideoID)
	require.NotContains(t, e.scanBlocked(t, 0), b3.VideoID, "branch 3 seed must not be acted at scan time")
	live, err := e.stateStore.GetVideo(e.ctx, b3.ID)
	require.NoError(t, err)
	require.NotEqual(t, state.VideoStateActed, live.ProcessingState)

	// 2. A pending deferred item for every seed, inserted directly. The
	// blocked ones stand in for a hypothetical unblocked re-queue so they
	// reach the send path (ARS-005 §11.2); the confirm does not exist yet.
	qs, err := queue.NewStore(e.pipeline.db)
	require.NoError(t, err)
	var origin int64
	for _, s := range v2Seeds() {
		v := e.videos[s.name]
		require.NoError(t, e.pipeline.db.QueryRowContext(e.ctx, `SELECT discovered_in_run_id FROM video WHERE id=?`, v.ID).Scan(&origin))
		id, err := qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: v.ID, RunID: origin, Reason: queue.DeferredReasonRequeuedByUser, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
		e.deferred[s.name] = id
	}

	// 3. A real scheduled production run: claim, evaluation, guarded send.
	run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	e.pipeline.Execute(e.ctx, run.ID)
	finished, err := e.stateStore.GetRun(e.ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, finished.Status, "interrupt reason: %v", finished.InterruptReason)

	count := func(query string, args ...any) int {
		var n int
		require.NoError(t, e.pipeline.db.QueryRowContext(e.ctx, query, args...).Scan(&n))
		return n
	}
	inBlocked := map[string]bool{}
	for _, id := range blocked {
		inBlocked[id] = true
	}
	for _, s := range v2Seeds() {
		v := e.videos[s.name]
		t.Run(s.name, func(t *testing.T) {
			ext := v.VideoID
			require.Equal(t, s.blocked, inBlocked[ext], "scan result must match the seed definition")
			// Every seed was claimed by the run and evaluated into a matched route.
			require.Equal(t, 1, count(`SELECT COUNT(*) FROM deferred_item WHERE id=? AND retry_run_id=?`, e.deferred[s.name], run.ID), "the run must claim the queued item")
			require.Equal(t, 1, count(`SELECT COUNT(*) FROM route_decision WHERE run_id=? AND video_id=? AND route_id=? AND decision='matched'`, run.ID, v.ID, e.jazzID), "evaluated and matched to B's route")

			bSucceeded := count(`SELECT COUNT(*) FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=? AND outcome='succeeded'`, run.ID, v.ID, v2TargetB)
			bRefused := count(`SELECT COUNT(*) FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=? AND outcome='not_attempted' AND outcome_reason='mutation_application_uncertain'`, run.ID, v.ID, v2TargetB)
			if s.blocked {
				// Refused by ClaimPlaylistSend: uncertain_blocked evidence, no POST, no insert.
				require.Equal(t, 1, bRefused, "send-time refusal evidence")
				require.Zero(t, bSucceeded)
				require.Zero(t, e.postsFor(ext, ""), "the provider must see no playlistItems.insert for a blocked video")
				require.Zero(t, count(`SELECT COUNT(*) FROM executed_action WHERE video_id=? AND target_playlist_id=? AND outcome='succeeded' AND run_id=?`, v.ID, v2TargetB, run.ID))
				return
			}
			// Unblocked: inserted to B exactly once, here and at the provider.
			require.Zero(t, bRefused)
			require.Equal(t, 1, bSucceeded, "recorded insert to B")
			require.Equal(t, 1, e.postsFor(ext, v2TargetB), "exactly one insert to B at the provider")
			require.Zero(t, e.postsFor(ext, v2TargetA), "an unblocked seed is never sent to A")
		})
	}

	// Branch 4: target A is already completed (no send), target B is refused.
	b4 := e.videos["branch4_target_a_succeeded"]
	require.Zero(t, e.postsFor(b4.VideoID, v2TargetA), "A already succeeded; it must not be sent again")
	require.Zero(t, e.postsFor(b4.VideoID, v2TargetB))
	require.Equal(t, 0, count(`SELECT COUNT(*) FROM executed_action WHERE run_id=? AND video_id=? AND target_playlist_id=? AND outcome IN ('failed','deferred')`, run.ID, b4.ID, v2TargetA))

	// Totals: nothing but the unblocked seeds reached the provider.
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Len(t, e.posts, len(wantUnblocked), fmt.Sprintf("posts: %v", e.posts))
}
