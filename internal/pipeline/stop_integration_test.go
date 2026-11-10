//go:build integration

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Stop tests (run-initiation ARS-003 §7.3–7.5). They reuse runNowEnv: real
// SQLite, the real pipeline and executor, and a local YouTube endpoint whose
// playlist POST for chosen videos stalls until the run's context ends.

// raceIterations is the repeat count ARS-003 §7.5 demands of every race test.
// YUHAUL_RACE_ITERATIONS lowers it for quick local runs only.
var raceIterations = func() int {
	if v, err := strconv.Atoi(os.Getenv("YUHAUL_RACE_ITERATIONS")); err == nil && v > 0 {
		return v
	}
	return 100
}()

func startExecution(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	return done
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("execution did not return")
	}
}

func (e *runNowEnv) waitBlocked(t *testing.T) string {
	t.Helper()
	select {
	case v := <-e.blocked:
		return v
	case <-time.After(30 * time.Second):
		t.Fatal("the provider call never stalled")
		return ""
	}
}

// pendingHandoffs maps external video ID to its count of due pending deferred
// items with the given reason.
func (e *runNowEnv) pendingHandoffs(t *testing.T, reason string) map[string]int {
	t.Helper()
	rows, err := e.db().QueryContext(e.ctx, `SELECT v.video_id, COUNT(*) FROM deferred_item d JOIN video v ON v.id=d.video_id
		WHERE d.status='pending' AND d.deferral_reason=? AND d.retry_after IS NULL GROUP BY v.video_id`, reason)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var v string
		var n int
		require.NoError(t, rows.Scan(&v, &n))
		out[v] = n
	}
	require.NoError(t, rows.Err())
	return out
}

// tableDump renders every row of the given tables for byte-for-byte checks.
func (e *runNowEnv) tableDump(t *testing.T, tables ...string) string {
	t.Helper()
	var b strings.Builder
	for _, tbl := range tables {
		rows, err := e.db().QueryContext(e.ctx, `SELECT * FROM `+tbl+` ORDER BY 1`)
		require.NoError(t, err)
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			fmt.Fprintf(&b, "%s:", tbl)
			for i, v := range vals {
				if bs, ok := v.([]byte); ok {
					v = string(bs)
				}
				fmt.Fprintf(&b, "%s=%v|", cols[i], v)
			}
			b.WriteString("\n")
		}
		require.NoError(t, rows.Close())
	}
	return b.String()
}

var guardTables = []string{"playlist_send_guard", "executed_action", "run_video_outcome"}

func (e *runNowEnv) assertStopped(t *testing.T, runID int64) *state.Run {
	t.Helper()
	r := e.run(t, runID)
	require.Equal(t, state.RunStatusStopped, r.Status, "reason: %v", str(r.InterruptReason))
	require.Equal(t, stopReason, str(r.InterruptReason))
	require.NotNil(t, r.StopRequestedAt)
	require.NotNil(t, r.CompletedAt)
	require.NotNil(t, r.DurationSeconds)
	require.Nil(t, r.InterruptCause)
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying' AND retry_run_id=?`, runID))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry WHERE status='retrying' AND retry_run_id=?`, runID))
	// Counters equal a fresh recompute.
	before := *r
	require.NoError(t, e.stateStore.RecomputeRunCounters(e.ctx, runID))
	after := e.run(t, runID)
	require.Equal(t, before.VideosAccepted, after.VideosAccepted)
	require.Equal(t, before.VideosFailed, after.VideosFailed)
	require.Equal(t, before.VideosDeferred, after.VideosDeferred)
	return r
}

// stalledRunNow starts a Run Now run with V2's POST stalled and waits until it
// is in flight.
func (e *runNowEnv) stalledRunNow(t *testing.T) (*RunNowAdmission, <-chan struct{}) {
	t.Helper()
	e.stallOn("V2")
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	done := startExecution(func() { adm.Execute(e.ctx) })
	require.Equal(t, "V2", e.waitBlocked(t))
	return adm, done
}

func (e *runNowEnv) stop(t *testing.T, runID int64, want string) {
	t.Helper()
	res, err := e.pipeline.StopRun(e.ctx, runID)
	require.NoError(t, err)
	require.Equal(t, want, res.Outcome)
}

// S1 (AC1, AC5, AC17) and S3 (AC7–8, sent branch) for a Run Now run: V1 is
// committed, V2's POST is admitted and in flight, V3 is untouched.
func TestS1_StopRunNowRun(t *testing.T) {
	e := newRunNowEnv(t)
	adm, done := e.stalledRunNow(t)
	e.stop(t, adm.RunID(), StopAccepted)
	waitDone(t, done)
	r := e.assertStopped(t, adm.RunID())

	require.Equal(t, map[string]int{"V2": 1, "V3": 1}, e.pendingHandoffs(t, "stopped_by_user"))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry`), "the handoff creates no Errors-queue entry")
	require.Equal(t, 1, r.VideosAccepted, "V1 stays committed")
	// S3: the admitted POST keeps its guard and shows as uncertain.
	v2, err := e.stateStore.GetVideoByExternalID(e.ctx, "V2")
	require.NoError(t, err)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM playlist_send_guard WHERE video_id=?`, v2.ID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM executed_action WHERE video_id=? AND run_id=? AND outcome='failed' AND outcome_reason='mutation_application_uncertain'`, v2.ID, adm.RunID()))
	// Statistics count only completed runs.
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM run WHERE status='completed'`))
	// Re-entry on a stopped run is rejected and sends nothing.
	posts := e.totalPosts()
	e.pipeline.Execute(e.ctx, adm.RunID())
	require.Equal(t, posts, e.totalPosts())
	require.Equal(t, state.RunStatusStopped, e.run(t, adm.RunID()).Status)
	// A stop after settling is refused with the final status.
	_, err = e.pipeline.StopRun(e.ctx, adm.RunID())
	var refused *StopRefusedError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, StopRunEnded, refused.Code)
	require.Equal(t, "stopped", refused.Status)
}

func TestS1_StopScheduledRun(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	done := startExecution(func() { e.pipeline.Execute(e.ctx, run.ID) })
	e.waitBlocked(t)
	e.stop(t, run.ID, StopAccepted)
	waitDone(t, done)
	e.assertStopped(t, run.ID)
	require.Equal(t, map[string]int{"V2": 1, "V3": 1}, e.pendingHandoffs(t, "stopped_by_user"))
}

func TestS1_StopResumedRunNowRun(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit)
	e.stallOn("V3")
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, adm.Resumed())
	require.Equal(t, first.RunID(), adm.RunID())
	done := startExecution(func() { adm.Execute(e.ctx) })
	require.Equal(t, "V3", e.waitBlocked(t))
	e.stop(t, adm.RunID(), StopAccepted)
	waitDone(t, done)
	e.assertStopped(t, adm.RunID())
	require.Equal(t, 1, e.postsFor("V2"), "the guarded target is never re-sent")
	// V2 hit its guard in the resumed run: a handled failure (ADR-006 §1) that
	// is committed, so it is not handed over. Only V3 was unfinished.
	require.Equal(t, map[string]int{"V3": 1}, e.pendingHandoffs(t, "stopped_by_user"))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM error_entry`))
}

func TestS1_StopStartupResumedRun(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit)
	e.stallOn("V3")
	ids, err := e.pipeline.BeginStartupRecovery(e.ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{first.RunID()}, ids)
	done := startExecution(func() { _ = e.pipeline.ResumeRecovered(e.ctx, ids) })
	require.Equal(t, "V3", e.waitBlocked(t))
	e.stop(t, first.RunID(), StopAccepted)
	waitDone(t, done)
	e.assertStopped(t, first.RunID())
}

// S1 for a queue-retry run: stop the first run, then retry one handed-over item
// and stop that retry run.
func TestS1_StopQueueRetryRun(t *testing.T) {
	e := newRunNowEnv(t)
	adm, done := e.stalledRunNow(t)
	e.stop(t, adm.RunID(), StopAccepted)
	waitDone(t, done)
	e.assertStopped(t, adm.RunID())

	var itemID int64
	v3, err := e.stateStore.GetVideoByExternalID(e.ctx, "V3")
	require.NoError(t, err)
	require.NoError(t, e.db().QueryRow(`SELECT id FROM deferred_item WHERE video_id=? AND status='pending'`, v3.ID).Scan(&itemID))

	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	e.pipeline.queueStore = qs // RetryQueueEntry requires an explicit queue store
	e.stallOn("V3")
	retryDone := make(chan error, 1)
	go func() { retryDone <- e.pipeline.RetryQueueEntry(e.ctx, itemID, false) }()
	select {
	case v := <-e.blocked:
		require.Equal(t, "V3", v)
	case err := <-retryDone:
		t.Fatalf("retry returned before reaching the provider: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the retry never reached the provider")
	}
	var retryRunID int64
	require.NoError(t, e.db().QueryRow(`SELECT MAX(id) FROM run`).Scan(&retryRunID))
	require.NotEqual(t, adm.RunID(), retryRunID)
	e.stop(t, retryRunID, StopAccepted)
	select {
	case err := <-retryDone:
		require.NoError(t, err, "a user-stopped retry is not an error")
	case <-time.After(30 * time.Second):
		t.Fatal("retry did not return")
	}
	e.assertStopped(t, retryRunID)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='pending' AND retry_after IS NULL`, v3.ID))
}

// S2 (AC6): after the cancel no provider request starts, wherever the run was.
func TestS2_NoProviderCallsAfterAccept(t *testing.T) {
	cases := []struct {
		name string
		hit  func(r *http.Request) bool
	}{
		{"discovery page", func(r *http.Request) bool { return r.URL.Path == "/playlistItems" && r.Method == http.MethodGet }},
		{"metadata read", func(r *http.Request) bool { return r.URL.Path == "/videos" }},
		{"playlist POST", func(r *http.Request) bool { return r.URL.Path == "/playlistItems" && r.Method == http.MethodPost }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRunNowEnv(t)
			paused, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			e.server.setGate(func(r *http.Request) {
				if !tc.hit(r) {
					return
				}
				hold := false
				once.Do(func() { hold = true })
				if hold {
					paused <- struct{}{}
					select {
					case <-release:
					case <-time.After(30 * time.Second):
					}
				}
			})
			adm, err := e.pipeline.AdmitRunNow(e.ctx)
			require.NoError(t, err)
			done := startExecution(func() { adm.Execute(e.ctx) })
			select {
			case <-paused:
			case <-time.After(30 * time.Second):
				t.Fatal("the provider call never paused")
			}
			e.stop(t, adm.RunID(), StopAccepted)
			cancelledAt := time.Now()
			close(release)
			waitDone(t, done)
			e.assertStopped(t, adm.RunID())
			for _, rec := range e.server.RequestLog() {
				require.False(t, rec.At.After(cancelledAt), "request %s %s started after the stop was acknowledged", rec.Method, rec.Path)
			}
		})
	}
}

// S4: a stop that arrives while the run waits for productionMu settles without
// any provider call once the run gets the lock.
func TestS4_PreAttachStopSettlesWithoutProviderCalls(t *testing.T) {
	e := newRunNowEnv(t)
	run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	requests := e.server.Requests()
	e.pipeline.productionMu.Lock()
	done := startExecution(func() { e.pipeline.Execute(e.ctx, run.ID) })
	time.Sleep(50 * time.Millisecond)
	e.stop(t, run.ID, StopAccepted)
	e.pipeline.productionMu.Unlock()
	waitDone(t, done)
	e.assertStopped(t, run.ID)
	require.Equal(t, requests, e.server.Requests(), "no provider call")
}

// S5: a settle that fails twice leaves the row running with its marker; the
// next stop answers already_stopping and a restart settles it.
func TestS5_SettleFailureIsFinishedByRestart(t *testing.T) {
	e := newRunNowEnv(t)
	adm, done := e.stalledRunNow(t)
	_, err := e.db().Exec(`CREATE TRIGGER block_settle BEFORE UPDATE OF status ON run WHEN NEW.status='stopped' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	e.stop(t, adm.RunID(), StopAccepted)
	waitDone(t, done)
	r := e.run(t, adm.RunID())
	require.Equal(t, state.RunStatusRunning, r.Status)
	require.NotNil(t, r.StopRequestedAt)
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='stopped_by_user'`), "the failed settle rolled back its handoff")
	e.stop(t, adm.RunID(), StopAlreadyStopping)

	_, err = e.db().Exec(`DROP TRIGGER block_settle`)
	require.NoError(t, err)
	ids, err := e.pipeline.classifyStartupRuns(e.ctx)
	require.NoError(t, err)
	require.Empty(t, ids, "a marked row is settled, never resumed")
	e.assertStopped(t, adm.RunID())
	require.Equal(t, map[string]int{"V2": 1, "V3": 1}, e.pendingHandoffs(t, "stopped_by_user"))
}

// Q2 (AC14–15) with a stop or a discard handoff: the next run claims every
// handed-over video and sends zero POSTs to the uncertain target.
func TestQ2_NextRunClaimsHandedOverVideos(t *testing.T) {
	for _, start := range []string{"scheduled", "run_now"} {
		t.Run(start, func(t *testing.T) {
			e := newRunNowEnv(t)
			adm, done := e.stalledRunNow(t)
			e.stop(t, adm.RunID(), StopAccepted)
			waitDone(t, done)
			e.assertStopped(t, adm.RunID())
			e.stallOn()
			v2Posts := e.postsFor("V2")

			var next int64
			if start == "scheduled" {
				run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
				require.NoError(t, err)
				next = run.ID
				e.pipeline.Execute(e.ctx, next)
			} else {
				a, err := e.pipeline.AdmitRunNow(e.ctx)
				require.NoError(t, err)
				require.False(t, a.Resumed(), "a stopped run is never a resume candidate")
				next = a.RunID()
				a.Execute(e.ctx)
			}
			require.NotEqual(t, adm.RunID(), next)
			r := e.run(t, next)
			require.Equal(t, state.RunStatusCompleted, r.Status, "reason: %v", str(r.InterruptReason))
			for _, v := range []string{"V2", "V3"} {
				vid, err := e.stateStore.GetVideoByExternalID(e.ctx, v)
				require.NoError(t, err)
				require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=?`, next, vid.ID), "%s is a member of the next run", v)
			}
			require.Equal(t, v2Posts, e.postsFor("V2"), "zero POSTs to the uncertain target")
			require.Equal(t, 1, e.postsFor("V3"), "the formerly unsent target is sent once")
			require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying'`), "S6: a completed run leaves no claims")
			require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry WHERE status='retrying'`))
		})
	}
}

// Q3: a usable resume candidate is resumed and never claims handed-over items.
func TestQ3_ResumeDoesNotClaimHandedOverItems(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit)
	v3, err := e.stateStore.GetVideoByExternalID(e.ctx, "V3")
	require.NoError(t, err)
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	_, err = qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: v3.ID, RunID: first.RunID(), Reason: queue.DeferredReasonStoppedByUser, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	e.stallOn()
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, adm.Resumed())
	adm.Execute(e.ctx)
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE retry_run_id IS NOT NULL`), "the resume claimed nothing")
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='pending' AND deferral_reason='stopped_by_user'`))
}

// outcomeOf reports whether the iteration ended with the run's own outcome or
// with the stop, and asserts the shared invariants of both.
type endKind string

const (
	endOwn     endKind = "own"
	endStopped endKind = "stopped"
)

// R1 (AC20): two concurrent presses give one accepted and one already_stopping,
// and one handoff.
func TestR1_DoubleStopPress(t *testing.T) {
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		adm, done := e.stalledRunNow(t)
		results := make(chan string, 2)
		var wg sync.WaitGroup
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := e.pipeline.StopRun(e.ctx, adm.RunID())
				if err != nil {
					results <- "error:" + err.Error()
					return
				}
				results <- res.Outcome
			}()
		}
		wg.Wait()
		close(results)
		var got []string
		for r := range results {
			got = append(got, r)
		}
		// The loser either arrives while the run is stopping (already_stopping)
		// or after it settled, when the row is final (run_ended/stopped).
		sort.Strings(got)
		require.Equal(t, StopAccepted, got[0], "iteration %d: exactly one press is accepted: %v", i, got)
		require.Contains(t, []string{StopAlreadyStopping, "error:run 1 cannot be stopped: run_ended"}, got[1], "iteration %d: %v", i, got)
		waitDone(t, done)
		e.assertStopped(t, adm.RunID())
		require.Equal(t, map[string]int{"V2": 1, "V3": 1}, e.pendingHandoffs(t, "stopped_by_user"), "iteration %d: one handoff", i)
		require.Equal(t, 2, e.count(t, `SELECT COUNT(*) FROM deferred_item`))
	}
}

// R2 (AC21): the run's own ending races a stop. Each iteration is either the
// run's own outcome saved with the stop refused and nothing changed, or
// `stopped` with its handoff. Never failed plus deferred items, never
// Errors-queue sinks plus stopped.
func TestR2_StopRacesTheRunsOwnEnding(t *testing.T) {
	for _, mode := range []string{"completion", "time_limit", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			counts := map[endKind]int{}
			for i := 0; i < raceIterations; i++ {
				kind := raceStopAgainstEnding(t, mode, i)
				counts[kind]++
			}
			t.Logf("%s: %v", mode, counts)
		})
	}
}

func raceStopAgainstEnding(t *testing.T, mode string, iteration int) endKind {
	t.Helper()
	e := newRunNowEnv(t)
	release := make(chan struct{})
	e.server.insertDecision = func(playlistID, videoID string) (int, string) {
		if videoID != "V3" {
			return 0, ""
		}
		e.blocked <- videoID
		select {
		case <-release:
		case <-time.After(30 * time.Second):
		}
		return 0, "" // V3 is applied: the run's last step succeeds
	}
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	execCtx, cancelExec := context.WithCancelCause(e.ctx)
	defer cancelExec(nil)
	done := startExecution(func() { adm.Execute(execCtx) })
	e.waitBlocked(t)

	stopErr := make(chan error, 1)
	var stopRes *StopResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-time.After(time.Duration(iteration%5) * 200 * time.Microsecond)
		stopRes, err = e.pipeline.StopRun(e.ctx, adm.RunID())
		stopErr <- err
	}()
	go func() {
		defer wg.Done()
		close(release)
		switch mode {
		case "shutdown":
			cancelExec(context.Canceled)
		case "time_limit":
			cancelExec(ErrRunTimeLimit)
		}
	}()
	wg.Wait()
	waitDone(t, done)
	stopped := <-stopErr
	r := e.run(t, adm.RunID())

	// Invariants shared by every outcome.
	require.NotEqual(t, state.RunStatusFailed, r.Status, "iteration %d: never failed", iteration)
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE status='retrying'`))
	failedOutcomes := e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome='failed'`, adm.RunID())
	require.Equal(t, failedOutcomes, e.count(t, `SELECT COUNT(*) FROM error_entry`), "iteration %d: no sink beyond committed failures", iteration)

	if stopped == nil {
		require.Equal(t, StopAccepted, stopRes.Outcome)
		e.assertStopped(t, adm.RunID())
		require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason<>'stopped_by_user'`))
		return endStopped
	}
	var refused *StopRefusedError
	require.True(t, errors.As(stopped, &refused), "iteration %d: %v", iteration, stopped)
	require.Nil(t, r.StopRequestedAt, "iteration %d: a refused stop leaves no marker", iteration)
	require.NotEqual(t, state.RunStatusStopped, r.Status)
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='stopped_by_user'`), "iteration %d: refused stop wrote a handoff", iteration)
	switch mode {
	case "completion":
		require.Equal(t, state.RunStatusCompleted, r.Status, "iteration %d", iteration)
	case "time_limit":
		require.Equal(t, state.RunStatusInterrupted, r.Status, "iteration %d", iteration)
		require.Equal(t, state.InterruptCauseTimeLimit, str(r.InterruptCause))
	case "shutdown":
		require.Equal(t, state.RunStatusInterrupted, r.Status, "iteration %d", iteration)
		require.Equal(t, state.InterruptCauseShutdown, str(r.InterruptCause))
	}
	return endOwn
}

// R3: a stop settling under productionMu while Run Now and a scheduled start
// arrive. Starts see the handed-over items only after the settle commits; a
// discard during the settle is refused.
func TestR3_StopSettleVersusStartsAndDiscard(t *testing.T) {
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		adm, done := e.stalledRunNow(t)
		var attempts sync.WaitGroup
		var mu sync.Mutex
		var admitted *RunNowAdmission
		attempts.Add(2)
		go func() {
			defer attempts.Done()
			for {
				a, err := e.pipeline.AdmitRunNow(e.ctx)
				if err == nil {
					mu.Lock()
					admitted = a
					mu.Unlock()
					return
				}
				if !errors.Is(err, ErrProductionRunActive) {
					t.Errorf("iteration %d: Run Now press: %v", i, err)
					return
				}
				time.Sleep(200 * time.Microsecond)
			}
		}()
		go func() {
			defer attempts.Done()
			for {
				_, err := e.pipeline.DiscardRun(e.ctx, adm.RunID())
				var refused *DiscardRefusedError
				switch {
				case errors.Is(err, ErrProductionRunActive):
					// Refused while the stop settles. Once the run has stopped,
					// a start may hold the lock for good, so stop probing.
					var status string
					if qerr := e.db().QueryRow(`SELECT status FROM run WHERE id=?`, adm.RunID()).Scan(&status); qerr == nil && status == "stopped" {
						return
					}
					time.Sleep(200 * time.Microsecond)
					continue
				case errors.As(err, &refused):
					return // once settled the run has ended
				default:
					t.Errorf("iteration %d: discard of a stop-settling run: %v", i, err)
					return
				}
			}
		}()
		e.stop(t, adm.RunID(), StopAccepted)
		waitDone(t, done)
		attempts.Wait()
		e.assertStopped(t, adm.RunID())
		mu.Lock()
		a := admitted
		mu.Unlock()
		require.NotNil(t, a)
		require.False(t, a.Resumed())
		// The new run claimed the handed-over items: they are committed before
		// the lock was released.
		for _, v := range []string{"V2", "V3"} {
			vid, err := e.stateStore.GetVideoByExternalID(e.ctx, v)
			require.NoError(t, err)
			require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=?`, a.RunID(), vid.ID), "iteration %d: %s claimed", i, v)
		}
		a.Release()
	}
}
