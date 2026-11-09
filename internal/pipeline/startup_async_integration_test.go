//go:build integration

package pipeline

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

// ADR-007 required test 2: while recovered runs resume, a manual start and a
// queue retry are refused, and both recovered runs execute in started_at/id
// order before any new run exists.
func TestRecoveredRunsAreNotOvertakenByNewProducers(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline

	first, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	second, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.Less(t, first.ID, second.ID)
	require.NoError(t, env.stateStore.InterruptRun(ctx, first.ID, "test: process stopped"))
	require.NoError(t, env.stateStore.InterruptRun(ctx, second.ID, "test: process stopped"))

	reached := make(chan struct{})
	release := make(chan struct{})
	env.server.beforeInsert = func() {
		select {
		case <-reached:
		default:
			close(reached)
		}
		<-release
	}

	ids, err := p.BeginStartupRecovery(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{first.ID, second.ID}, ids)

	done := make(chan error, 1)
	go func() { done <- p.ResumeRecovered(ctx, ids) }()
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the first recovered run never reached its playlist insertion")
	}

	runCount := func() (n int) {
		require.NoError(t, p.db.QueryRowContext(ctx, `SELECT count(*) FROM run`).Scan(&n))
		return n
	}
	require.Equal(t, 2, runCount())
	_, err = p.StartRun(ctx, state.RunTypeManual)
	require.ErrorIs(t, err, ErrStartupRecoveryActive)
	require.ErrorIs(t, p.RetryQueueEntry(ctx, 1, true), ErrStartupRecoveryActive)
	require.Equal(t, 2, runCount(), "a refused producer creates no run")

	close(release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("recovered runs did not finish")
	}

	var order []int64
	rows, err := p.db.QueryContext(ctx, `SELECT id FROM run WHERE status='completed' ORDER BY completed_at, id`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		order = append(order, id)
	}
	require.NoError(t, rows.Close())
	require.Equal(t, []int64{first.ID, second.ID}, order)
	require.Equal(t, 2, runCount(), "no new run existed while recovery ran")

	p.OpenProducers()
	next, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.Greater(t, next.ID, second.ID)
}

// ADR-007 required test 7: an expired execution deadline interrupts the run
// with a timeout diagnostic. Nothing is failed or swept, so every unfinished
// video stays owned by the resumable run, which finishes it after a restart.
func TestExpiredRunDeadlineInterruptsAndResumes(t *testing.T) {
	e := newIsolationEnv(t)
	// Set once; the handler of the timed-out POST may still be reading the
	// hook after the test moves on, so only an atomic flag changes later.
	var stall atomic.Bool
	stall.Store(true)
	e.server.beforeInsert = func() {
		if stall.Load() {
			time.Sleep(900 * time.Millisecond)
		}
	}

	deadline, cancel := context.WithTimeout(e.ctx, 400*time.Millisecond)
	defer cancel()
	e.pipeline.Execute(deadline, e.runID)

	run := e.run(t)
	require.Equal(t, state.RunStatusInterrupted, run.Status)
	require.Nil(t, run.CompletedAt)
	require.NotNil(t, run.InterruptReason)
	require.Contains(t, *run.InterruptReason, "code=timeout")
	require.NotNil(t, run.CurrentPhase)
	require.Equal(t, string(PhaseActionExecution), *run.CurrentPhase)

	db := e.pipeline.db
	var sinks, failedVideos int
	require.NoError(t, db.QueryRow(`SELECT (SELECT count(*) FROM error_entry)+(SELECT count(*) FROM deferred_item)`).Scan(&sinks))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM video WHERE final_outcome='failed' OR processing_state='failed'`).Scan(&failedVideos))
	require.Zero(t, sinks, "an interrupted run needs no sweep: it is the resumable owner")
	require.Zero(t, failedVideos, "a deadline is not a video failure")
	require.Equal(t, 1, e.totalPosts())

	// A restart resumes the same run. The admitted POST left its guard, so V1
	// is blocked without a second POST; V2 and V3 complete.
	stall.Store(false)
	ids, err := e.pipeline.BeginStartupRecovery(e.ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{e.runID}, ids)
	require.NoError(t, e.pipeline.ResumeRecovered(e.ctx, ids))
	run = e.run(t)
	require.Equal(t, state.RunStatusCompleted, run.Status)
	require.Equal(t, 1, run.VideosFailed)
	require.Equal(t, 2, run.VideosAccepted)
	require.Equal(t, 1, e.postsFor("V1"))
	require.Equal(t, 1, e.postsFor("V2"))
	require.Equal(t, 1, e.postsFor("V3"))
}
