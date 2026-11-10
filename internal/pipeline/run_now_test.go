//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Tests for Run Now admission and the interruption cause (run-initiation
// ADR-001, ARS-001 §8.2–8.3). They use real SQLite, the real pipeline,
// executor, auth service, send guard and queue, and a local HTTP endpoint for
// the YouTube Data API. Interruptions are produced by a provider that blocks
// a playlist POST until the execution context ends.

// runNowEnv drives three matched videos (V1, V2, V3) to one playlist.
type runNowEnv struct {
	*idempotencyTestEnv
	ctx context.Context

	mu       sync.Mutex
	attempts []playlistInsertion // every playlist POST the endpoint received
	block    map[string]bool     // external video IDs whose POST stalls
	blocked  chan string         // signalled when a stalled POST arrives
	release  chan struct{}
}

func newRunNowEnv(t *testing.T) *runNowEnv {
	t.Helper()
	publishedAt := time.Now().UTC().Add(-time.Hour)
	var videos []mockVideo
	for i := 1; i <= 3; i++ {
		v := makeJazzVideo(fmt.Sprintf("V%d", i), publishedAt.Add(time.Duration(i)*time.Minute))
		v.Title = fmt.Sprintf("Jazz Session %d", i)
		videos = append(videos, v)
	}
	e := &runNowEnv{
		idempotencyTestEnv: setupIdempotencyTest(t, videos),
		ctx:                context.Background(),
		block:              map[string]bool{},
		blocked:            make(chan string, 16),
		release:            make(chan struct{}),
	}
	e.server.insertDecision = func(playlistID, videoID string) (int, string) {
		e.mu.Lock()
		e.attempts = append(e.attempts, playlistInsertion{PlaylistID: playlistID, VideoID: videoID})
		stall := e.block[videoID]
		e.mu.Unlock()
		if !stall {
			return 0, ""
		}
		e.blocked <- videoID
		select {
		case <-e.release:
		case <-time.After(30 * time.Second):
		}
		// The client has gone; answer a failure so nothing is recorded as sent.
		return 500, providerBackendError
	}
	t.Cleanup(func() { close(e.release) }) // runs before the server's Close
	return e
}

func (e *runNowEnv) stallOn(videoIDs ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.block = map[string]bool{}
	for _, id := range videoIDs {
		e.block[id] = true
	}
}

func (e *runNowEnv) postsFor(videoID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, a := range e.attempts {
		if a.VideoID == videoID {
			n++
		}
	}
	return n
}

func (e *runNowEnv) totalPosts() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.attempts)
}

func (e *runNowEnv) db() *sql.DB { return e.pipeline.db }

func (e *runNowEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, e.db().QueryRowContext(e.ctx, query, args...).Scan(&n))
	return n
}

func (e *runNowEnv) run(t *testing.T, id int64) *state.Run {
	t.Helper()
	r, err := e.stateStore.GetRun(e.ctx, id)
	require.NoError(t, err)
	return r
}

func str(r *string) string {
	if r == nil {
		return "<nil>"
	}
	return *r
}

// interruptedAtLimit presses Run Now and runs it under a real
// WithTimeoutCause deadline. It requires that a stalled POST was reached, so a
// deadline that fired during discovery cannot pass for the scenario.
func (e *runNowEnv) interruptedAtLimit(t *testing.T, limit time.Duration) *RunNowAdmission {
	t.Helper()
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeoutCause(e.ctx, limit, ErrRunTimeLimit)
	defer cancel()
	adm.Execute(ctx)
	select {
	case <-e.blocked:
	default:
		t.Fatalf("run %d hit its %s limit before reaching a stalled provider call; raise the limit", adm.RunID(), limit)
	}
	r := e.run(t, adm.RunID())
	require.Equal(t, state.RunStatusInterrupted, r.Status, "reason: %v", r.InterruptReason)
	require.Equal(t, state.InterruptCauseTimeLimit, str(r.InterruptCause))
	return adm
}

// testLimit is long enough for discovery, routing and the first provider call
// to finish on a slow, race-instrumented machine.
const testLimit = 4 * time.Second

// ---- C: interruption cause -------------------------------------------------

// C1: a real deadline carrying ErrRunTimeLimit stores the time-limit cause,
// keeps the checkpoint and records code=timeout.
func TestC1_TimeLimitStoresCause(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")

	adm := e.interruptedAtLimit(t, testLimit)

	r := e.run(t, adm.RunID())
	require.NotNil(t, r.CurrentPhase)
	require.Equal(t, string(PhaseActionExecution), *r.CurrentPhase, "the checkpoint is kept")
	require.NotNil(t, r.InterruptReason)
	require.Contains(t, *r.InterruptReason, "code=timeout")
	require.Equal(t, state.InitiatedByRunNow, str(r.InitiatedBy))
}

// C2: cancellation of the parent context (shutdown) stores the shutdown cause.
func TestC2_ShutdownStoresCause(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { defer close(done); adm.Execute(ctx) }()
	<-e.blocked
	cancel()
	<-done

	r := e.run(t, adm.RunID())
	require.Equal(t, state.RunStatusInterrupted, r.Status)
	require.Equal(t, state.InterruptCauseShutdown, str(r.InterruptCause))
}

// C3: a provider network timeout whose retention then fails ends in the
// interrupted fallback with the reason code=timeout, yet no proven cause.
func TestC3_NonLimitTimeoutHasNoCause(t *testing.T) {
	e := newRunNowEnv(t)
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	defer adm.Release()
	_, err = e.pipeline.phaseDiscovery(e.ctx, adm.RunID())
	require.NoError(t, err)
	// Retention cannot write, so failure handling falls back to interruption.
	_, err = e.db().Exec(`CREATE TRIGGER reject_retention BEFORE INSERT ON error_entry BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)

	e.pipeline.failRun(e.ctx, adm.RunID(), "action execution phase failed", &net.DNSError{Err: "i/o timeout", IsTimeout: true})

	r := e.run(t, adm.RunID())
	require.Equal(t, state.RunStatusInterrupted, r.Status)
	require.NotNil(t, r.InterruptReason)
	require.Contains(t, *r.InterruptReason, "code=timeout", "the reason alone looks like a time limit")
	require.Nil(t, r.InterruptCause, "a text match must never become a cause")
}

// C4: a plain deadline without the cause (the queue-retry wrapper's shape)
// stores no cause.
func TestC4_PlainDeadlineHasNoCause(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(e.ctx, testLimit)
	defer cancel()
	adm.Execute(ctx)
	require.Len(t, e.blocked, 1, "the deadline must fire at the stalled call")

	r := e.run(t, adm.RunID())
	require.Equal(t, state.RunStatusInterrupted, r.Status)
	require.Nil(t, r.InterruptCause)
	require.Contains(t, *r.InterruptReason, "code=timeout")
}

// ---- A: admission ----------------------------------------------------------

type countingVersioner struct {
	inner Versioner
	calls int
}

func (c *countingVersioner) ActiveVersionID(ctx context.Context) (*int64, error) {
	c.calls++
	return c.inner.ActiveVersionID(ctx)
}

// A1: the next press resumes the same run under its saved checkpoint. It
// captures no configuration, inserts no run and claims no deferred group, and
// no target that already succeeded or is guarded is sent again.
func TestA1_PressResumesTimeLimitedRun(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit) // V1 sent, V2's POST in flight, V3 untouched
	runID := first.RunID()
	require.Equal(t, 1, e.postsFor("V1"))
	require.Equal(t, 1, e.postsFor("V2"))
	require.Zero(t, e.postsFor("V3"))
	selectedBefore := e.run(t, runID).VideosSelected
	require.NotNil(t, selectedBefore)

	// Unowned deferred work that a new run would claim.
	v2, err := e.stateStore.GetVideoByExternalID(e.ctx, "V2")
	require.NoError(t, err)
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	_, err = qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: v2.ID, RunID: runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	guardBefore := e.count(t, `SELECT COUNT(*) FROM playlist_send_guard WHERE video_id=?`, v2.ID)
	require.Equal(t, 1, guardBefore, "the in-flight POST left a retained send guard")

	spy := &countingVersioner{inner: e.pipeline.versioner}
	e.pipeline.versioner = spy
	runs := e.count(t, `SELECT COUNT(*) FROM run`)
	versions := e.count(t, `SELECT COUNT(*) FROM config_version`)
	e.stallOn()

	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, adm.Resumed())
	require.Equal(t, runID, adm.RunID())
	require.Equal(t, state.RunStatusRunning, e.run(t, runID).Status, "running before Execute")
	require.Nil(t, e.run(t, runID).InterruptReason)
	require.Nil(t, e.run(t, runID).InterruptCause)

	adm.Execute(e.ctx)

	require.Zero(t, spy.calls, "a resume must not capture configuration")
	require.Equal(t, runs, e.count(t, `SELECT COUNT(*) FROM run`))
	require.Equal(t, versions, e.count(t, `SELECT COUNT(*) FROM config_version`))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE retry_run_id IS NOT NULL`), "no deferred group is claimed")
	r := e.run(t, runID)
	require.Equal(t, state.RunStatusCompleted, r.Status, "reason: %v", r.InterruptReason)
	require.Equal(t, selectedBefore, r.VideosSelected)
	require.Equal(t, 1, e.postsFor("V1"), "V1 already succeeded")
	require.Equal(t, 1, e.postsFor("V2"), "V2 is guarded: never sent again")
	require.Equal(t, 1, e.postsFor("V3"), "only the remaining target is sent")
	require.Equal(t, guardBefore, e.count(t, `SELECT COUNT(*) FROM playlist_send_guard WHERE video_id=?`, v2.ID), "the guard is retained")
}

// A2: the resumed run evaluates with its original snapshot even after the
// active configuration changed.
func TestA2_ResumeUsesOriginalSnapshot(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit)
	e.stallOn()

	_, err := e.db().Exec(`UPDATE route_action SET target_playlist_id='PLchanged' WHERE route_id=?`, e.routeID)
	require.NoError(t, err)
	changed, err := e.pipeline.versioner.ActiveVersionID(e.ctx)
	require.NoError(t, err)
	require.NotEqual(t, *e.run(t, first.RunID()).ConfigVersionID, *changed, "the active configuration really changed")

	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, adm.Resumed())
	adm.Execute(e.ctx)

	require.Equal(t, state.RunStatusCompleted, e.run(t, first.RunID()).Status)
	for _, ins := range e.server.Insertions() {
		require.Equal(t, e.playlistID, ins.PlaylistID, "video %s went to the changed target", ins.VideoID)
	}
	require.Equal(t, 1, e.postsFor("V3"))
}

type seedSpec struct {
	startedAt   time.Time
	runType     string
	status      string
	initiatedBy *string
	cause       *string
	safety      int
	authority   int
	phase       *string
	completedAt *string
	configID    *int64
}

func sp(s string) *string { return &s }

// seedRun inserts one run row exactly as the spec says. Rows that cannot be
// produced through the real pipeline (pre-024 history, other run types) are
// seeded; the eligible shape is also exercised through real interruptions in
// A1, A2, A9 and A10.
func (e *runNowEnv) seedRun(t *testing.T, s seedSpec) int64 {
	t.Helper()
	res, err := e.db().ExecContext(e.ctx, `INSERT INTO run (started_at, run_type, status, config_version_id, action_authority_version,
 mutation_safety_version, initiated_by, interrupt_cause, current_phase, completed_at, interrupt_reason)
 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		s.startedAt.UTC().Format(state.RunTimestampLayout), s.runType, s.status, s.configID, s.authority, s.safety,
		s.initiatedBy, s.cause, s.phase, s.completedAt, "diag:v1 phase=action_execution operation=load_run code=timeout")
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func (e *runNowEnv) eligible(t *testing.T, startedAt time.Time) seedSpec {
	t.Helper()
	cfg, err := e.pipeline.versioner.ActiveVersionID(e.ctx)
	require.NoError(t, err)
	return seedSpec{startedAt: startedAt, runType: "manual", status: "interrupted", initiatedBy: sp("run_now"),
		cause: sp("time_limit"), safety: 1, authority: 1, phase: sp(string(PhaseActionExecution)), configID: cfg}
}

func (e *runNowEnv) dumpRuns(t *testing.T, ids ...int64) []string {
	t.Helper()
	var out []string
	for _, id := range ids {
		var parts []string
		rows, err := e.db().QueryContext(e.ctx, `SELECT * FROM run WHERE id=?`, id)
		require.NoError(t, err)
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				parts = append(parts, fmt.Sprintf("%s=%v", cols[i], v))
			}
		}
		require.NoError(t, rows.Close())
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

// A3: eligible runs resume oldest first (lower ID on a tie), one per press;
// once none remain the next press creates a new run.
func TestA3_OldestFirstWithTie(t *testing.T) {
	e := newRunNowEnv(t)
	base := time.Now().UTC().Add(-3 * time.Hour)
	young := e.seedRun(t, e.eligible(t, base.Add(time.Hour)))
	tieHigh := e.seedRun(t, e.eligible(t, base))
	tieLow := e.seedRun(t, e.eligible(t, base))
	// tieHigh was inserted before tieLow, so it has the lower ID.
	order := []int64{tieHigh, tieLow, young}
	require.Less(t, tieHigh, tieLow)

	for i, want := range order {
		adm, err := e.pipeline.AdmitRunNow(e.ctx)
		require.NoError(t, err, "press %d", i)
		require.True(t, adm.Resumed(), "press %d", i)
		require.Equal(t, want, adm.RunID(), "press %d", i)
		adm.Execute(e.ctx)
		require.Equal(t, state.RunStatusCompleted, e.run(t, want).Status, "press %d", i)
	}

	runs := e.count(t, `SELECT COUNT(*) FROM run`)
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	defer adm.Release()
	require.False(t, adm.Resumed())
	require.Equal(t, runs+1, e.count(t, `SELECT COUNT(*) FROM run`))
	created := e.run(t, adm.RunID())
	require.Equal(t, state.InitiatedByRunNow, str(created.InitiatedBy))
	require.Equal(t, state.RunTypeManual, created.RunType)
}

// A4: nothing but an exactly eligible row is resumed. Every other shape is
// left byte-for-byte unchanged and the press starts a new run.
func TestA4_IneligibleRowsAreNeverResumed(t *testing.T) {
	e := newRunNowEnv(t)
	at := time.Now().UTC().Add(-time.Hour)
	mod := func(f func(*seedSpec)) seedSpec { s := e.eligible(t, at); f(&s); return s }
	done := time.Now().UTC().Format(state.RunTimestampLayout)
	cases := map[string]seedSpec{
		"mutation safety 0":    mod(func(s *seedSpec) { s.safety = 0 }),
		"authority version 0":  mod(func(s *seedSpec) { s.authority = 0 }),
		"pre-024 NULL origin":  mod(func(s *seedSpec) { s.initiatedBy = nil }),
		"queue retry":          mod(func(s *seedSpec) { s.initiatedBy = sp("queue_retry") }),
		"scheduled":            mod(func(s *seedSpec) { s.runType = "scheduled"; s.initiatedBy = sp("schedule") }),
		"shutdown cause":       mod(func(s *seedSpec) { s.cause = sp("shutdown") }),
		"NULL cause":           mod(func(s *seedSpec) { s.cause = nil }),
		"failed":               mod(func(s *seedSpec) { s.status = "failed"; s.cause = nil; s.completedAt = &done }),
		"completed":            mod(func(s *seedSpec) { s.status = "completed"; s.cause = nil; s.completedAt = &done }),
		"dry_run":              mod(func(s *seedSpec) { s.runType = "dry_run"; s.initiatedBy = nil; s.safety = 0; s.authority = 0 }),
		"replay":               mod(func(s *seedSpec) { s.runType = "replay"; s.initiatedBy = nil; s.safety = 0; s.authority = 0 }),
		"backtest":             mod(func(s *seedSpec) { s.runType = "backtest"; s.initiatedBy = nil; s.safety = 0; s.authority = 0 }),
		"running (not paused)": mod(func(s *seedSpec) { s.status = "running"; s.cause = nil }),
	}
	var ids []int64
	for _, spec := range cases {
		ids = append(ids, e.seedRun(t, spec))
	}
	before := e.dumpRuns(t, ids...)

	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	defer adm.Release()

	require.False(t, adm.Resumed(), "an ineligible row was resumed")
	require.NotContains(t, ids, adm.RunID())
	require.Equal(t, before, e.dumpRuns(t, ids...), "seeded rows must be unchanged")
}

// A5: an unusable oldest candidate refuses the press with its ID and a closed
// reason. Nothing is written, no run is created, no provider call is made,
// the lock is released, and a younger valid candidate is not chosen.
func TestA5_UnusableCandidateRefuses(t *testing.T) {
	cases := []struct {
		reason string
		break_ func(t *testing.T, e *runNowEnv, s *seedSpec)
	}{
		{"snapshot_unavailable", func(t *testing.T, e *runNowEnv, s *seedSpec) {
			// The snapshot row disappears after the run was linked to it
			// (ON DELETE SET NULL); the delete happens below, after seeding.
			id := e.newConfigVersion(t, `{"routes":[]}`)
			s.configID = &id
		}},
		{"snapshot_unreadable", func(t *testing.T, e *runNowEnv, s *seedSpec) {
			id := e.newConfigVersion(t, `not json`)
			s.configID = &id
		}},
		{"snapshot_inadmissible", func(t *testing.T, e *runNowEnv, s *seedSpec) {
			id := e.newConfigVersion(t, `{"routes":[{"id":0}]}`)
			s.configID = &id
		}},
		{"checkpoint_unrecognized", func(t *testing.T, e *runNowEnv, s *seedSpec) { s.phase = sp("bogus") }},
		{"terminal_timestamp_contradiction", func(t *testing.T, e *runNowEnv, s *seedSpec) {
			s.completedAt = sp(time.Now().UTC().Format(state.RunTimestampLayout))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			e := newRunNowEnv(t)
			old := e.eligible(t, time.Now().UTC().Add(-2*time.Hour))
			tc.break_(t, e, &old)
			oldID := e.seedRun(t, old)
			if tc.reason == "snapshot_unavailable" {
				_, err := e.db().Exec(`DELETE FROM config_version WHERE id=?`, *old.configID)
				require.NoError(t, err)
			}
			youngID := e.seedRun(t, e.eligible(t, time.Now().UTC().Add(-time.Hour)))
			before := e.dumpRuns(t, oldID, youngID)
			runs := e.count(t, `SELECT COUNT(*) FROM run`)
			requestsBefore := e.server.Requests()

			adm, err := e.pipeline.AdmitRunNow(e.ctx)

			require.Nil(t, adm)
			var unavailable *ResumeUnavailableError
			require.ErrorAs(t, err, &unavailable)
			require.Equal(t, oldID, unavailable.RunID)
			require.Equal(t, tc.reason, unavailable.Reason)
			require.Equal(t, before, e.dumpRuns(t, oldID, youngID), "a refusal changes nothing, and the younger run is not chosen")
			require.Equal(t, runs, e.count(t, `SELECT COUNT(*) FROM run`))
			require.Equal(t, requestsBefore, e.server.Requests(), "no provider call")
			require.True(t, e.pipeline.productionMu.TryLock(), "the lock was released")
			e.pipeline.productionMu.Unlock()
		})
	}
}

// A6: while another run holds the execution lock, a press is refused at once
// and changes nothing.
func TestA6_BusyRefusesWithoutWaiting(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V1")
	other, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { defer close(done); other.Execute(ctx) }()
	<-e.blocked // the other run is executing and holds the lock
	waiting := e.seedRun(t, e.eligible(t, time.Now().UTC().Add(-time.Hour)))
	before := e.dumpRuns(t, waiting)
	runs := e.count(t, `SELECT COUNT(*) FROM run`)

	result := make(chan error, 1)
	go func() { _, err := e.pipeline.AdmitRunNow(e.ctx); result <- err }()
	select {
	case err := <-result:
		require.ErrorIs(t, err, ErrProductionRunActive)
	case <-time.After(3 * time.Second):
		t.Fatal("a press during an active run must be refused, not wait")
	}
	require.Equal(t, before, e.dumpRuns(t, waiting))
	require.Equal(t, runs, e.count(t, `SELECT COUNT(*) FROM run`))
	cancel()
	<-done
}

// A7: while startup recovery is open the press is refused before any lock
// attempt or change.
func TestA7_StartupGateRefuses(t *testing.T) {
	e := newRunNowEnv(t)
	waiting := e.seedRun(t, e.eligible(t, time.Now().UTC().Add(-time.Hour)))
	before := e.dumpRuns(t, waiting)
	e.pipeline.producerGate.Store(producersGated)

	adm, err := e.pipeline.AdmitRunNow(e.ctx)

	require.Nil(t, adm)
	require.ErrorIs(t, err, ErrStartupRecoveryActive)
	require.Equal(t, before, e.dumpRuns(t, waiting))
	require.True(t, e.pipeline.productionMu.TryLock(), "no lock was taken")
	e.pipeline.productionMu.Unlock()
}

// A8: two simultaneous presses with one eligible run: exactly one is
// admitted, the other is refused, and the run is resumed once.
func TestA8_ConcurrentPressesAdmitOne(t *testing.T) {
	e := newRunNowEnv(t)
	id := e.seedRun(t, e.eligible(t, time.Now().UTC().Add(-time.Hour)))
	runs := e.count(t, `SELECT COUNT(*) FROM run`)

	type result struct {
		adm *RunNowAdmission
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; a, err := e.pipeline.AdmitRunNow(e.ctx); results <- result{a, err} }()
	}
	close(start)
	r1, r2 := <-results, <-results
	var winner *RunNowAdmission
	var refusals int
	for _, r := range []result{r1, r2} {
		if r.err != nil {
			require.ErrorIs(t, r.err, ErrProductionRunActive)
			refusals++
		} else {
			winner = r.adm
		}
	}
	require.Equal(t, 1, refusals)
	require.NotNil(t, winner)
	require.True(t, winner.Resumed())
	require.Equal(t, id, winner.RunID())
	require.Equal(t, runs, e.count(t, `SELECT COUNT(*) FROM run`))
	require.Equal(t, state.RunStatusRunning, e.run(t, id).Status)
	winner.Release()
	require.Equal(t, state.RunStatusRunning, e.run(t, id).Status, "Release leaves the run for startup recovery")
}

// A9: a resumed run that hits the limit again is eligible again, with its
// earlier decisions and actions and its selected count intact.
func TestA9_ReLimitedRunIsEligibleAgain(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit)
	runID := first.RunID()
	selected := e.run(t, runID).VideosSelected
	decisions := e.count(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=?`, runID)
	e.stallOn("V3")

	second, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, second.Resumed())
	require.Equal(t, runID, second.RunID())
	ctx, cancel := context.WithTimeoutCause(e.ctx, testLimit, ErrRunTimeLimit)
	second.Execute(ctx)
	cancel()
	require.Len(t, e.blocked, 1, "the second limit fired at V3's stalled call")

	r := e.run(t, runID)
	require.Equal(t, state.RunStatusInterrupted, r.Status)
	require.Equal(t, state.InterruptCauseTimeLimit, str(r.InterruptCause))
	require.Equal(t, selected, r.VideosSelected)
	require.Equal(t, decisions, e.count(t, `SELECT COUNT(*) FROM route_decision WHERE run_id=?`, runID))
	require.Equal(t, 1, e.postsFor("V1"))

	e.stallOn()
	third, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, third.Resumed())
	require.Equal(t, runID, third.RunID())
	third.Execute(e.ctx)
	require.Equal(t, state.RunStatusCompleted, e.run(t, runID).Status)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run`), "one run throughout")
}

// A10: process loss after the admission commits leaves a running row; startup
// classification reclassifies it (cause NULL) and resumes it from its phase.
func TestA10_CrashAfterAdmissionIsResumedByStartup(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit)
	runID := first.RunID()
	e.stallOn()

	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.True(t, adm.Resumed())
	adm.Release() // the process is gone: the admission never executed
	require.Equal(t, state.RunStatusRunning, e.run(t, runID).Status)

	ids, err := e.pipeline.classifyStartupRuns(e.ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{runID}, ids)
	r := e.run(t, runID)
	require.Equal(t, state.RunStatusInterrupted, r.Status)
	require.Nil(t, r.InterruptCause)
	require.NotNil(t, r.CurrentPhase)
	require.Equal(t, string(PhaseActionExecution), *r.CurrentPhase)

	require.NoError(t, e.pipeline.ResumeRecovered(e.ctx, ids))
	require.Equal(t, state.RunStatusCompleted, e.run(t, runID).Status)
	require.Equal(t, 1, e.postsFor("V3"))
}

// Release and Execute are single-use: Execute after Release must not run
// without the lock, and the lock is freed exactly once.
func TestAdmissionIsSingleUse(t *testing.T) {
	e := newRunNowEnv(t)
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	adm.Release()
	adm.Release()
	require.True(t, e.pipeline.productionMu.TryLock(), "released once")
	e.pipeline.productionMu.Unlock()

	adm.Execute(e.ctx) // must not touch the unlocked mutex or run anything
	require.Equal(t, state.RunStatusRunning, e.run(t, adm.RunID()).Status)
	require.Zero(t, e.totalPosts())
}

func (e *runNowEnv) newConfigVersion(t *testing.T, snapshot string) int64 {
	t.Helper()
	res, err := e.db().ExecContext(e.ctx, `INSERT INTO config_version (created_at, snapshot, change_description) VALUES (?,?,?)`,
		time.Now().UTC(), snapshot, "run now test")
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}
