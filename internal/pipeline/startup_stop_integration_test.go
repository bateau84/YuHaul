//go:build integration

package pipeline

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

// Startup tests for Stop and unusable time-limit rows (run-initiation
// ARS-003 §7.6: U2–U5; BR-017 AC11, BR-011 AC7).

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (e *runNowEnv) captureLogs() *lockedBuffer {
	buf := &lockedBuffer{}
	e.pipeline.logger = slog.New(slog.NewTextHandler(buf, nil))
	return buf
}

func (e *runNowEnv) scheduledRecoverable(t *testing.T, at time.Time) seedSpec {
	s := e.eligible(t, at)
	s.runType = "scheduled"
	s.initiatedBy = sp("schedule")
	s.cause = nil
	return s
}

// U3: unusable time-limit Run Now rows no longer block startup. They stay
// unchanged and are logged by run ID and closed cause; everything recoverable
// resumes; Run Now refuses the oldest, which then discards.
func TestU3_UnusableTimeLimitRowsDoNotBlockStartup(t *testing.T) {
	reasons := []struct{ reason, cause string }{
		{"snapshot_unavailable", DiscardCauseSnapshotUnavailable},
		{"snapshot_unreadable", DiscardCauseSnapshotUnreadable},
		{"snapshot_inadmissible", DiscardCauseSnapshotUnreadable},
		{"checkpoint_unrecognized", DiscardCauseCheckpointUnrecognized},
		{"terminal_timestamp_contradiction", DiscardCauseCheckpointUnrecognized},
	}
	for _, tc := range reasons {
		t.Run(tc.reason, func(t *testing.T) {
			e := newRunNowEnv(t)
			logs := e.captureLogs()
			at := time.Now().UTC().Add(-4 * time.Hour)

			oldest := e.eligible(t, at)
			switch tc.reason {
			case "snapshot_unavailable":
				oldest.configID = nil
			case "snapshot_unreadable":
				id := e.newConfigVersion(t, `not json`)
				oldest.configID = &id
			case "snapshot_inadmissible":
				id := e.newConfigVersion(t, `{"routes":[{"id":0}]}`)
				oldest.configID = &id
			case "checkpoint_unrecognized":
				oldest.phase = sp("bogus")
			case "terminal_timestamp_contradiction":
				oldest.completedAt = sp(time.Now().UTC().Format(state.RunTimestampLayout))
			}
			oldestID := e.seedRun(t, oldest)
			younger := e.eligible(t, at.Add(time.Hour))
			younger.phase = sp("bogus")
			youngerID := e.seedRun(t, younger)
			scheduledID := e.seedRun(t, e.scheduledRecoverable(t, at.Add(2*time.Hour)))
			usableID := e.seedRun(t, e.eligible(t, at.Add(3*time.Hour)))
			before := e.dumpRuns(t, oldestID, youngerID)

			ids, err := e.pipeline.BeginStartupRecovery(e.ctx)
			require.NoError(t, err)
			require.Equal(t, []int64{scheduledID, usableID}, ids, "the recoverable rows resume")
			require.Equal(t, before, e.dumpRuns(t, oldestID, youngerID), "unusable rows are unchanged")
			out := logs.String()
			require.Contains(t, out, "startup recovery left unusable run")
			for _, id := range []int64{oldestID, youngerID} {
				require.Contains(t, out, "run_id="+itoa(int(id)))
			}
			require.Contains(t, out, "cause="+tc.cause)
			require.NotContains(t, out, "not json", "no raw text")

			// The gate opens; Run Now refuses the oldest and then discards it.
			e.pipeline.OpenProducers()
			_, err = e.pipeline.AdmitRunNow(e.ctx)
			var unavailable *ResumeUnavailableError
			require.ErrorAs(t, err, &unavailable)
			require.Equal(t, oldestID, unavailable.RunID)
			require.Equal(t, tc.reason, unavailable.Reason)
			id, cause, ok, err := e.pipeline.DiscardableRun(e.ctx)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, oldestID, id)
			require.Equal(t, tc.cause, cause)
			res, err := e.pipeline.DiscardRun(e.ctx, oldestID)
			require.NoError(t, err)
			require.Equal(t, tc.cause, res.Cause)
		})
	}
}

// U4: every other invalid row still blocks startup, leaving the row unchanged.
func TestU4_OtherCorruptionStillBlocksStartup(t *testing.T) {
	cases := map[string]func(e *runNowEnv) seedSpec{
		"corrupt scheduled row": func(e *runNowEnv) seedSpec {
			s := e.scheduledRecoverable(t, time.Now().UTC().Add(-2*time.Hour))
			s.phase = sp("bogus")
			return s
		},
		"manual run_now with shutdown cause": func(e *runNowEnv) seedSpec {
			s := e.eligible(t, time.Now().UTC().Add(-2*time.Hour))
			s.cause = sp("shutdown")
			s.phase = sp("bogus")
			return s
		},
		"running run_now row": func(e *runNowEnv) seedSpec {
			s := e.eligible(t, time.Now().UTC().Add(-2*time.Hour))
			s.status = "running"
			s.cause = nil
			s.phase = sp("bogus")
			return s
		},
		"queue retry row": func(e *runNowEnv) seedSpec {
			s := e.eligible(t, time.Now().UTC().Add(-2*time.Hour))
			s.initiatedBy = sp("queue_retry")
			s.phase = sp("bogus")
			return s
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			e := newRunNowEnv(t)
			id := e.seedRun(t, build(e))
			before := e.dumpRuns(t, id)
			_, err := e.pipeline.BeginStartupRecovery(e.ctx)
			var check *startupCheckError
			require.True(t, errors.As(err, &check), "%v", err)
			require.Equal(t, id, check.RunID)
			require.Equal(t, before, e.dumpRuns(t, id))
		})
	}
}

// U5: a marked row whose settle fails blocks startup (never resumed, never
// skipped).
func TestU5_SettleFailureBlocksStartup(t *testing.T) {
	e := newRunNowEnv(t)
	spec := e.scheduledRecoverable(t, time.Now().UTC().Add(-time.Hour))
	spec.status = "running"
	spec.cause = nil
	id := e.seedRun(t, spec)
	_, err := e.db().Exec(`UPDATE run SET stop_requested_at='2026-11-10T12:00:00Z' WHERE id=?`, id)
	require.NoError(t, err)
	_, err = e.db().Exec(`CREATE TRIGGER block_settle BEFORE UPDATE OF status ON run WHEN NEW.status='stopped' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	before := e.dumpRuns(t, id)

	_, err = e.pipeline.BeginStartupRecovery(e.ctx)
	var check *startupCheckError
	require.True(t, errors.As(err, &check), "%v", err)
	require.Equal(t, "stop_settlement", check.Check)
	require.Equal(t, id, check.RunID)
	require.Equal(t, before, e.dumpRuns(t, id))

	_, err = e.db().Exec(`DROP TRIGGER block_settle`)
	require.NoError(t, err)
	ids, err := e.pipeline.classifyStartupRuns(e.ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.Equal(t, state.RunStatusStopped, e.run(t, id).Status)
}

// U2 (AC19): two recovered runs; stopping the first while it resumes ends it
// stopped and the worker resumes the second. A stop of the second before the
// worker reaches it is refused as not executing.
func TestU2_StopDuringRecovery(t *testing.T) {
	e := newRunNowEnv(t)
	e.stallOn("V2")
	first := e.interruptedAtLimit(t, testLimit) // V1 sent, V2 guarded, V3 untouched
	e.stallOn("V3")
	secondSpec := e.scheduledRecoverable(t, time.Now().UTC())
	secondID := e.seedRun(t, secondSpec)

	ids, err := e.pipeline.BeginStartupRecovery(e.ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{first.RunID(), secondID}, ids)
	done := startExecution(func() { _ = e.pipeline.ResumeRecovered(e.ctx, ids) })
	require.Equal(t, "V3", e.waitBlocked(t))

	_, err = e.pipeline.StopRun(e.ctx, secondID)
	var refused *StopRefusedError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, StopRunNotActive, refused.Code)
	require.Equal(t, StopReasonNotExecuting, refused.Reason)

	e.stop(t, first.RunID(), StopAccepted)
	waitDone(t, done)
	e.assertStopped(t, first.RunID())
	require.Equal(t, state.RunStatusCompleted, e.run(t, secondID).Status, "the worker resumed the second run")
	require.True(t, strings.Contains(e.tableDump(t, "run"), "stopped"))
}
