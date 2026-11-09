//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// These tests cover the ten tests required by ADR-006. They use a real SQLite
// database, the real pipeline, executor, auth service, send guard, queue and
// state stores, and a local HTTP endpoint standing in for the YouTube Data API.

const (
	providerBackendError = `{"error":{"errors":[{"reason":"backendError"}],"code":500}}`
	providerBadRequest   = `{"error":{"errors":[{"reason":"invalidRequest"}],"code":400}}`
)

// isolationEnv drives a production run with three matched videos that all
// target the same playlist. routed holds them in action order.
type isolationEnv struct {
	*idempotencyTestEnv
	ctx    context.Context
	runID  int64
	routed []*state.Video

	mu       sync.Mutex
	attempts []playlistInsertion // every playlist POST the endpoint received
	respond  func(post int, playlistID, videoID string) (int, string)
}

// newIsolationEnv stops a real run after routing, before the action phase, in
// the interrupted state. p.Execute then resumes it at the action phase.
func newIsolationEnv(t *testing.T) *isolationEnv {
	t.Helper()
	publishedAt := time.Now().UTC().Add(-time.Hour)
	titles := []string{"Jazz Alpha Session", "Jazz Bravo Night", "Jazz Charlie Hour"}
	var videos []mockVideo
	for i, title := range titles {
		v := makeJazzVideo(fmt.Sprintf("V%d", i+1), publishedAt.Add(time.Duration(i)*time.Minute))
		v.Title = title
		videos = append(videos, v)
	}
	e := &isolationEnv{idempotencyTestEnv: setupIdempotencyTest(t, videos), ctx: context.Background()}
	e.server.insertDecision = func(playlistID, videoID string) (int, string) {
		e.mu.Lock()
		e.attempts = append(e.attempts, playlistInsertion{PlaylistID: playlistID, VideoID: videoID})
		post := len(e.attempts)
		respond := e.respond
		e.mu.Unlock()
		if respond == nil {
			return 0, ""
		}
		return respond(post, playlistID, videoID)
	}

	p := e.pipeline
	run, err := p.StartRun(e.ctx, state.RunTypeManual)
	require.NoError(t, err)
	e.runID = run.ID
	vs, err := p.phaseDiscovery(e.ctx, e.runID)
	require.NoError(t, err)
	vs, err = p.phaseExclusion(e.ctx, e.runID, vs)
	require.NoError(t, err)
	require.NoError(t, p.phaseDuplicateDetection(e.ctx, e.runID, vs))
	e.routed, err = p.phaseRouting(e.ctx, e.runID, vs)
	require.NoError(t, err)
	require.Len(t, e.routed, 3)
	require.NoError(t, e.stateStore.SetRunPhase(e.ctx, e.runID, string(PhaseActionExecution)))
	require.NoError(t, e.stateStore.InterruptRun(e.ctx, e.runID, "test: stopped before the action phase"))
	return e
}

func (e *isolationEnv) execute() { e.pipeline.Execute(e.ctx, e.runID) }

// whileRunning lets a test act as the running run (the send guard only
// accepts a claim for a running run) and then restores the interrupted state
// that Execute resumes from.
func (e *isolationEnv) whileRunning(t *testing.T, fn func()) {
	t.Helper()
	require.NoError(t, e.stateStore.ResumeRun(e.ctx, e.runID))
	fn()
	require.NoError(t, e.stateStore.SetRunPhase(e.ctx, e.runID, string(PhaseActionExecution)))
	require.NoError(t, e.stateStore.InterruptRun(e.ctx, e.runID, "test: interrupted"))
}

func (e *isolationEnv) run(t *testing.T) *state.Run {
	t.Helper()
	r, err := e.stateStore.GetRun(e.ctx, e.runID)
	require.NoError(t, err)
	return r
}

func (e *isolationEnv) postsFor(videoExternalID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, a := range e.attempts {
		if a.VideoID == videoExternalID {
			n++
		}
	}
	return n
}

func (e *isolationEnv) totalPosts() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.attempts)
}

func (e *isolationEnv) failVideo(external string, status int, body string) {
	e.respond = func(_ int, _, videoID string) (int, string) {
		if videoID == external {
			return status, body
		}
		return 0, ""
	}
}

// seedGuard leaves an unresolved send guard for (video, target) exactly as a
// crash between guard claim and resolution does.
func (e *isolationEnv) seedGuard(t *testing.T, v *state.Video) {
	t.Helper()
	binding, err := e.pipeline.auth.Bind(e.ctx)
	require.NoError(t, err)
	identity, err := auth.BindingIdentity(binding)
	require.NoError(t, err)
	e.whileRunning(t, func() {
		result, _, err := e.stateStore.ClaimPlaylistSend(e.ctx, state.PlaylistSendClaim{
			RunID: e.runID, VideoID: v.ID, RouteDecisionID: e.matchedDecision(t, v.ID, e.runID),
			TargetPlaylistID: e.playlistID, ActionType: "playlist_insert", AuthorizationID: identity,
		}, time.Now().UTC())
		require.NoError(t, err)
		require.Equal(t, state.PlaylistClaimed, result)
	})
}

func (e *isolationEnv) matchedDecision(t *testing.T, videoID, runID int64) int64 {
	t.Helper()
	decisions, err := e.stateStore.GetRouteDecisionsForVideoInRun(e.ctx, videoID, runID)
	require.NoError(t, err)
	for _, d := range decisions {
		if d.Decision == state.RouteDecisionMatched {
			return d.ID
		}
	}
	t.Fatalf("no matched decision for video %d in run %d", videoID, runID)
	return 0
}

func (e *isolationEnv) rows(t *testing.T, query string, args ...any) []string {
	t.Helper()
	r, err := e.db().QueryContext(e.ctx, query, args...)
	require.NoError(t, err)
	defer r.Close()
	cols, err := r.Columns()
	require.NoError(t, err)
	var out []string
	for r.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, r.Scan(ptrs...))
		line := ""
		for _, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			line += fmt.Sprintf("%v|", v)
		}
		out = append(out, line)
	}
	require.NoError(t, r.Err())
	return out
}

func (e *isolationEnv) db() *sql.DB { return e.pipeline.db }

func (e *isolationEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, e.db().QueryRowContext(e.ctx, query, args...).Scan(&n))
	return n
}

func (e *isolationEnv) guardRows(t *testing.T, videoID int64) []string {
	return e.rows(t, `SELECT * FROM playlist_send_guard WHERE video_id=? ORDER BY rowid`, videoID)
}

func (e *isolationEnv) activeSinks(t *testing.T, videoID, runID int64) int {
	return e.count(t, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND run_id=? AND status='active'`, videoID, runID)
}

// requireFailedWithOneSink asserts the ADR-006 §1 committed state of a failed
// video: lifecycle, run outcome with a message, and exactly one active sink.
func (e *isolationEnv) requireFailedWithOneSink(t *testing.T, v *state.Video) {
	t.Helper()
	loaded, err := e.stateStore.GetVideo(e.ctx, v.ID)
	require.NoError(t, err)
	require.Equal(t, state.VideoStateFailed, loaded.ProcessingState)
	require.Equal(t, state.FinalOutcomeFailed, loaded.FinalOutcome)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome='failed' AND COALESCE(error_message,'')<>''`, e.runID, v.ID))
	require.Equal(t, 1, e.activeSinks(t, v.ID, e.runID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM error_entry WHERE video_id=?`, v.ID), "no second sink for the same video")
}

func (e *isolationEnv) requireRunOutcome(t *testing.T, v *state.Video, outcome state.VideoFinalOutcome) {
	t.Helper()
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome=?`, e.runID, v.ID, string(outcome)),
		"video %s should have outcome %s in the run", v.VideoID, outcome)
}

// 1. A guard-blocked video fails with one sink; independent videos continue.
func TestADR006_GuardBlockedVideoContinuesRun(t *testing.T) {
	e := newIsolationEnv(t)
	a, b, c := e.routed[0], e.routed[1], e.routed[2]
	e.seedGuard(t, a)
	guardBefore := e.guardRows(t, a.ID)
	require.Len(t, guardBefore, 1)

	e.execute()

	run := e.run(t)
	require.Equal(t, state.RunStatusCompleted, run.Status, "interrupt reason: %v", run.InterruptReason)
	require.Equal(t, 1, run.VideosFailed)
	require.Equal(t, 2, run.VideosAccepted)
	e.requireFailedWithOneSink(t, a)
	e.requireRunOutcome(t, b, state.FinalOutcomeAccepted)
	e.requireRunOutcome(t, c, state.FinalOutcomeAccepted)

	require.Equal(t, []string{"not_attempted|mutation_application_uncertain|<nil>|"},
		e.rows(t, `SELECT outcome, outcome_reason, executed_at FROM executed_action WHERE run_id=? AND video_id=?`, e.runID, a.ID))
	require.Zero(t, e.postsFor(a.VideoID), "the guard forbids any POST of A to P")
	require.Equal(t, 1, e.postsFor(b.VideoID))
	require.Equal(t, 1, e.postsFor(c.VideoID))
	require.Equal(t, guardBefore, e.guardRows(t, a.ID), "the guard row is unchanged")
}

// 2. A guard found during a quota deferral is a handled failure; the other
// videos defer; no video is left without a disposition.
func TestADR006_GuardBlockedDuringQuotaDeferral(t *testing.T) {
	e := newIsolationEnv(t)
	a, b, c := e.routed[0], e.routed[1], e.routed[2]
	e.seedGuard(t, a)
	guardBefore := e.guardRows(t, a.ID)
	require.NoError(t, e.ledger.SetDailyLimit(e.ctx, 10))

	e.execute()

	run := e.run(t)
	require.Equal(t, state.RunStatusCompleted, run.Status, "interrupt reason: %v", run.InterruptReason)
	require.Equal(t, 1, run.VideosFailed)
	require.Equal(t, 2, run.VideosDeferred)
	e.requireFailedWithOneSink(t, a)
	for _, v := range []*state.Video{b, c} {
		e.requireRunOutcome(t, v, state.FinalOutcomeDeferred)
		require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND run_id=? AND status='pending'`, v.ID, e.runID))
	}
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND COALESCE(final_outcome,'')=''`, e.runID),
		"no video is left without a disposition")
	require.Zero(t, e.totalPosts())
	require.Equal(t, guardBefore, e.guardRows(t, a.ID))
}

// 3. A permanent or transient provider failure on B fails only B, with one
// POST per video and no replay.
func TestADR006_ProviderFailureFailsOnlyThatVideo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"permanent_4xx", http.StatusBadRequest, providerBadRequest},
		{"transient_5xx", http.StatusServiceUnavailable, providerBackendError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newIsolationEnv(t)
			a, b, c := e.routed[0], e.routed[1], e.routed[2]
			e.failVideo(b.VideoID, tc.status, tc.body)

			e.execute()

			run := e.run(t)
			require.Equal(t, state.RunStatusCompleted, run.Status, "interrupt reason: %v", run.InterruptReason)
			require.Equal(t, 1, run.VideosFailed)
			require.Equal(t, 2, run.VideosAccepted)
			e.requireFailedWithOneSink(t, b)
			e.requireRunOutcome(t, a, state.FinalOutcomeAccepted)
			e.requireRunOutcome(t, c, state.FinalOutcomeAccepted)
			for _, v := range []*state.Video{a, b, c} {
				require.Equal(t, 1, e.postsFor(v.VideoID), "one POST each, no replay (%s)", v.VideoID)
			}
			var category string
			require.NoError(t, e.db().QueryRowContext(e.ctx, `SELECT error_category FROM error_entry WHERE video_id=?`, b.ID).Scan(&category))
			require.Equal(t, string(queue.ErrorCategoryAPIError), category)
		})
	}
}

// 4. An admitted uncertain mutation keeps its guard and failed/uncertain
// evidence, the run continues, and a same-run resume neither replays the POST
// nor downgrades the action row.
func TestADR006_UncertainMutationContinuesAndResumeKeepsEvidence(t *testing.T) {
	e := newIsolationEnv(t)
	a, b, c := e.routed[0], e.routed[1], e.routed[2]
	e.failVideo(a.VideoID, http.StatusInternalServerError, providerBackendError)

	// A is processed and fails; the run is then interrupted before B and C.
	var handled state.VideoFinalOutcome
	e.whileRunning(t, func() {
		var err error
		handled, err = e.pipeline.executeVideoActions(e.ctx, e.runID, a)
		require.NoError(t, err, "a handled failure is not a run-stopping error")
	})
	require.Equal(t, state.FinalOutcomeFailed, handled)
	e.requireFailedWithOneSink(t, a)
	evidence := e.rows(t, `SELECT outcome, outcome_reason, executed_at IS NOT NULL FROM executed_action WHERE run_id=? AND video_id=?`, e.runID, a.ID)
	require.Equal(t, []string{"failed|mutation_application_uncertain|1|"}, evidence)
	guardBefore := e.guardRows(t, a.ID)
	require.Len(t, guardBefore, 1, "the uncertain mutation keeps its guard")

	e.respond = nil
	e.execute() // same-run resume

	run := e.run(t)
	require.Equal(t, state.RunStatusCompleted, run.Status, "interrupt reason: %v", run.InterruptReason)
	require.Equal(t, 1, run.VideosFailed)
	require.Equal(t, 2, run.VideosAccepted)
	e.requireRunOutcome(t, b, state.FinalOutcomeAccepted)
	e.requireRunOutcome(t, c, state.FinalOutcomeAccepted)
	require.Equal(t, 1, e.postsFor(a.VideoID), "resume must not replay A's uncertain POST")
	require.Equal(t, evidence,
		e.rows(t, `SELECT outcome, outcome_reason, executed_at IS NOT NULL FROM executed_action WHERE run_id=? AND video_id=?`, e.runID, a.ID),
		"resume never downgrades the action row")
	require.Equal(t, guardBefore, e.guardRows(t, a.ID))
	e.requireFailedWithOneSink(t, a)
}

// 5a. A fault inside the failed-plus-sink transaction commits neither row; the
// run stops through the retention sweep and becomes failed.
func TestADR006_DispositionTransactionIsAtomic(t *testing.T) {
	e := newIsolationEnv(t)
	a, b, c := e.routed[0], e.routed[1], e.routed[2]
	e.failVideo(a.VideoID, http.StatusInternalServerError, providerBackendError)
	// Only the handled-failure sink (an api_error) is rejected, so the sweep's
	// runtime sinks can still commit.
	_, err := e.db().ExecContext(e.ctx, `CREATE TRIGGER inject_disposition_failure BEFORE INSERT ON error_entry
		WHEN NEW.error_category='api_error' BEGIN SELECT RAISE(ABORT,'injected disposition failure'); END`)
	require.NoError(t, err)

	e.execute()

	run := e.run(t)
	require.Equal(t, state.RunStatusFailed, run.Status)
	require.Equal(t, 1, e.totalPosts(), "the run stopped after A")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry WHERE error_category='api_error'`), "the sink did not commit")
	aLoaded, err := e.stateStore.GetVideo(e.ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, state.VideoStateActed, aLoaded.ProcessingState, "the failed lifecycle did not commit")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome IN ('accepted','rejected','excluded','deferred','failed')`, e.runID, a.ID))
	for _, v := range []*state.Video{a, b, c} {
		require.Equal(t, 1, e.activeSinks(t, v.ID, e.runID), "unfinished video %s is retained by the sweep", v.VideoID)
	}
	require.Equal(t, 3, e.count(t, `SELECT COUNT(*) FROM error_entry WHERE run_id=? AND error_category='runtime'`, e.runID))
	require.Equal(t, 1, len(e.guardRows(t, a.ID)), "the guard stays")
}

// 5b. When the sweep cannot commit either, the run is interrupted, not failed,
// and its candidates are still owned and resumable.
func TestADR006_SweepFailureInterruptsInsteadOfFailing(t *testing.T) {
	e := newIsolationEnv(t)
	a, b, c := e.routed[0], e.routed[1], e.routed[2]
	e.failVideo(a.VideoID, http.StatusInternalServerError, providerBackendError)
	_, err := e.db().ExecContext(e.ctx, `CREATE TRIGGER inject_sink_failure BEFORE INSERT ON error_entry
		BEGIN SELECT RAISE(ABORT,'injected sink failure'); END`)
	require.NoError(t, err)

	e.execute()

	run := e.run(t)
	require.Equal(t, state.RunStatusInterrupted, run.Status, "FailRun must not be called without retention")
	require.NotNil(t, run.InterruptReason)
	require.Nil(t, run.CompletedAt, "a run left resumable has no completion time")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry`))
	for _, v := range []*state.Video{a, b, c} {
		loaded, err := e.stateStore.GetVideo(e.ctx, v.ID)
		require.NoError(t, err)
		require.NotEqual(t, state.VideoStateFailed, loaded.ProcessingState)
		require.Equal(t, e.runID, loaded.DiscoveredInRunID, "candidates stay owned by the run")
	}

	// Resume once storage recovers: no replay, A is blocked by its guard and
	// recorded, B and C are acted on.
	_, err = e.db().ExecContext(e.ctx, `DROP TRIGGER inject_sink_failure`)
	require.NoError(t, err)
	e.respond = nil
	e.execute()
	run = e.run(t)
	require.Equal(t, state.RunStatusCompleted, run.Status, "interrupt reason: %v", run.InterruptReason)
	require.Equal(t, 1, run.VideosFailed)
	require.Equal(t, 2, run.VideosAccepted)
	require.Equal(t, 1, e.postsFor(a.VideoID), "A is never POSTed twice")
	e.requireFailedWithOneSink(t, a)
}

// 6. Accounting failure after a known success stops the run; the success stays
// committed and unprocessed work is retained.
func TestADR006_AccountingFailureAfterSuccessStopsRun(t *testing.T) {
	e := newIsolationEnv(t)
	a, b, c := e.routed[0], e.routed[1], e.routed[2]
	e.respond = func(post int, _, _ string) (int, string) {
		if post == 2 {
			// B's POST will succeed; its quota accounting write will not.
			_, err := e.db().ExecContext(e.ctx, `CREATE TRIGGER inject_accounting_failure BEFORE INSERT ON quota_ledger
				BEGIN SELECT RAISE(ABORT,'injected accounting failure'); END`)
			require.NoError(t, err)
		}
		return 0, ""
	}

	e.execute()

	run := e.run(t)
	require.Equal(t, state.RunStatusFailed, run.Status)
	require.Equal(t, 2, e.totalPosts(), "C was never processed")
	e.requireRunOutcome(t, a, state.FinalOutcomeAccepted)
	e.requireRunOutcome(t, b, state.FinalOutcomeAccepted)
	bLoaded, err := e.stateStore.GetVideo(e.ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, state.VideoStateCompleted, bLoaded.ProcessingState)
	require.Zero(t, len(e.guardRows(t, b.ID)), "the confirmed success removed its guard")
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM executed_action WHERE run_id=? AND video_id=? AND outcome='succeeded'`, e.runID, b.ID))
	require.Equal(t, 1, e.activeSinks(t, c.ID, e.runID), "unprocessed C has an active runtime sink")
	require.Zero(t, e.activeSinks(t, b.ID, e.runID), "accepted B needs no sink")
	require.Equal(t, 2, run.VideosAccepted)
}

// 7. A deferred group the run claimed returns to its queue when the run fails
// before acting on it. Its IDs, origin run, reason and history survive, no
// sink is created, and a removed group stays removed.
func TestADR006_ClaimedRetryWorkReturnsToQueueWhenRunFails(t *testing.T) {
	e := newIsolationEnv(t)
	require.NoError(t, e.ledger.SetDailyLimit(e.ctx, 10))
	e.execute()
	require.Equal(t, state.RunStatusCompleted, e.run(t).Status)
	require.Equal(t, 3, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='pending'`))
	require.NoError(t, e.ledger.SetDailyLimit(e.ctx, 1_000_000))

	second, err := e.pipeline.StartRun(e.ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.Equal(t, 3, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying' AND retry_run_id=?`, second.ID))
	removed := e.routed[1]
	var removedItem int64
	require.NoError(t, e.db().QueryRowContext(e.ctx, `SELECT id FROM deferred_item WHERE video_id=?`, removed.ID).Scan(&removedItem))
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	require.NoError(t, qs.DiscardDeferredItem(e.ctx, removedItem))
	history := `SELECT id, video_id, run_id, deferral_reason, retry_count, created_at FROM deferred_item ORDER BY id`
	before := e.rows(t, history)

	// The binding fails after the claim, before any claimed work is acted on.
	require.NoError(t, e.pipeline.auth.Disconnect(e.ctx))
	e.pipeline.Execute(e.ctx, second.ID)

	loaded, err := e.stateStore.GetRun(e.ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusFailed, loaded.Status)
	require.Equal(t, before, e.rows(t, history), "IDs, origin run, reason, retry count and creation time are preserved")
	for _, v := range e.routed {
		status := "pending"
		if v.ID == removed.ID {
			status = "discarded"
		}
		require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status=?`, v.ID, status),
			"video %s deferred item should be %s", v.VideoID, status)
	}
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying'`))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry`), "queue-owned work gets no duplicate sink and removed work is not resurrected")
}

// 8. Repeating the sweep through a crash between retention and the failed
// transition leaves one sink per (video, run) and changes no counters.
func TestADR006_SweepIsIdempotentAcrossCrashAndRestart(t *testing.T) {
	e := newIsolationEnv(t)
	p := e.pipeline
	cause := errors.New("routing phase failed")
	// The crash: retention commits, then the failed transition does not.
	_, err := e.db().ExecContext(e.ctx, `CREATE TRIGGER crash_before_failed BEFORE UPDATE OF status ON run
		WHEN NEW.status='failed' BEGIN SELECT RAISE(ABORT,'crash before failed transition'); END`)
	require.NoError(t, err)
	p.failRun(e.ctx, e.runID, "routing phase failed", cause)
	require.Equal(t, state.RunStatusInterrupted, e.run(t).Status)
	sinks := e.rows(t, `SELECT id, video_id, run_id, status, created_at FROM error_entry ORDER BY id`)
	require.Len(t, sinks, 3)
	routedBefore := e.rows(t, `SELECT channel_id, total_routed, total_retained FROM channel ORDER BY channel_id`)

	// Restart: the run is resumed to the same failure and the sweep runs again.
	_, err = e.db().ExecContext(e.ctx, `DROP TRIGGER crash_before_failed`)
	require.NoError(t, err)
	p.failRun(e.ctx, e.runID, "routing phase failed", cause)

	require.Equal(t, state.RunStatusFailed, e.run(t).Status)
	require.Equal(t, sinks, e.rows(t, `SELECT id, video_id, run_id, status, created_at FROM error_entry ORDER BY id`), "no sink was created, resurrected or duplicated")
	for _, v := range e.routed {
		require.Equal(t, 1, e.activeSinks(t, v.ID, e.runID))
	}
	require.Equal(t, routedBefore, e.rows(t, `SELECT channel_id, total_routed, total_retained FROM channel ORDER BY channel_id`), "the sweep changes no counters")
}

// guardBlockedRun runs test 1's scenario and returns A's retained sink.
func guardBlockedRun(t *testing.T) (*isolationEnv, *state.Video, int64) {
	t.Helper()
	e := newIsolationEnv(t)
	a := e.routed[0]
	e.seedGuard(t, a)
	e.execute()
	require.Equal(t, state.RunStatusCompleted, e.run(t).Status)
	var sink int64
	require.NoError(t, e.db().QueryRowContext(e.ctx, `SELECT id FROM error_entry WHERE video_id=? AND status='active'`, a.ID).Scan(&sink))
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	e.pipeline.queueStore = qs
	return e, a, sink
}

// 9. Retrying a retained guard-blocked entry goes through the normal claim: it
// is blocked again with zero POSTs, and nothing about the guard or the
// original run changes.
func TestADR006_RetryOfGuardBlockedEntryIsBlockedAgain(t *testing.T) {
	e, a, sink := guardBlockedRun(t)
	postsBefore := e.totalPosts()
	guardBefore := e.guardRows(t, a.ID)
	originalEvidence := e.rows(t, `SELECT * FROM executed_action WHERE run_id=? AND video_id=?`, e.runID, a.ID)
	originalOutcome := e.rows(t, `SELECT * FROM run_video_outcome WHERE run_id=? AND video_id=?`, e.runID, a.ID)

	require.NoError(t, e.pipeline.RetryQueueEntry(e.ctx, sink, true))

	require.Equal(t, postsBefore, e.totalPosts(), "the error queue is not a bypass: zero POSTs")
	require.Equal(t, guardBefore, e.guardRows(t, a.ID))
	require.Equal(t, originalEvidence, e.rows(t, `SELECT * FROM executed_action WHERE run_id=? AND video_id=?`, e.runID, a.ID))
	require.Equal(t, originalOutcome, e.rows(t, `SELECT * FROM run_video_outcome WHERE run_id=? AND video_id=?`, e.runID, a.ID))
	var receiving int64
	require.NoError(t, e.db().QueryRowContext(e.ctx, `SELECT retry_run_id FROM error_entry WHERE id=?`, sink).Scan(&receiving))
	require.NotZero(t, receiving)
	require.NotEqual(t, e.runID, receiving)
	require.Equal(t, []string{"not_attempted|mutation_application_uncertain|<nil>|"},
		e.rows(t, `SELECT outcome, outcome_reason, executed_at FROM executed_action WHERE run_id=? AND video_id=?`, receiving, a.ID),
		"blocked again: new no-attempt evidence for the receiving run")
	require.Equal(t, 1, e.activeSinks(t, a.ID, receiving), "the blocked retry is retained again")
}

// 10. Dismissing or removing the retained entry does not lift the guard: a
// later run's claim for the same target is still blocked.
func TestADR006_DismissedEntryDoesNotLiftGuard(t *testing.T) {
	e, a, sink := guardBlockedRun(t)
	qs := e.pipeline.queueStore
	require.NoError(t, qs.DismissErrorEntry(e.ctx, sink))
	postsBefore := e.totalPosts()
	guardBefore := e.guardRows(t, a.ID)

	later, err := e.pipeline.StartRun(e.ctx, state.RunTypeManual)
	require.NoError(t, err)
	decision := createRouteDecision(t, e.ctx, e.stateStore, a.ID, later.ID, e.routeID, 1, state.RouteDecisionMatched)
	binding, err := e.pipeline.auth.Bind(e.ctx)
	require.NoError(t, err)
	exec, ok := e.pipeline.executor.(*Executor)
	require.True(t, ok)

	_, err = exec.ExecuteBound(e.ctx, later.ID, a.ID, []RouteMatch{{RouteDecisionID: decision, RouteID: e.routeID}}, binding)

	require.Error(t, err)
	require.True(t, isHandledVideoFailure(err), "a blocked claim is a handled per-video failure")
	require.Equal(t, postsBefore, e.totalPosts(), "the later run did not POST")
	require.Equal(t, guardBefore, e.guardRows(t, a.ID))
	require.Equal(t, []string{"not_attempted|mutation_application_uncertain|<nil>|"},
		e.rows(t, `SELECT outcome, outcome_reason, executed_at FROM executed_action WHERE run_id=? AND video_id=?`, later.ID, a.ID))
	entry, err := qs.GetErrorEntry(e.ctx, sink)
	require.NoError(t, err)
	require.Equal(t, queue.ErrorStatusDismissed, entry.Status, "dismissal stays authoritative")
}
