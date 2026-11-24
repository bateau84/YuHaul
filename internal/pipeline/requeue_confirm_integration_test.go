//go:build integration

package pipeline

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

// Tests of PreviewStranded and RequeueStranded (ARS-005 §4; §11.4 A1–A7, §11.5
// E1 confirm part; ARS-006 K6). Real migrated SQLite, the real pipeline and
// the counting fake YouTube endpoint. Version-0 history and guards are seeded
// because only pre-024 data produces those shapes; the confirm itself is
// never seeded.

// requeueRowSnapshot is every table the confirm must leave byte-identical.
var requeueRowSnapshot = []string{"video", "run", "error_entry", "playlist_send_guard", "executed_action",
	"route_decision", "run_video_outcome", "transient_metadata", "quota_ledger"}

func (e *runNowEnv) confirm(t *testing.T) *RequeueResult {
	t.Helper()
	res, err := e.pipeline.RequeueStranded(e.ctx, nil)
	require.NoError(t, err)
	return res
}

// seedPlanned inserts n stranded videos (unfinished, in one ended run) and
// returns the run and the video ids.
func (e *runNowEnv) seedPlanned(t *testing.T, n int) (int64, []int64) {
	t.Helper()
	run := e.sRun(t, "manual", "failed", 1, "")
	return run, e.seedUnfinished(t, run, "rq", n)
}

func (e *runNowEnv) seedUnfinished(t *testing.T, run int64, prefix string, n int) []int64 {
	t.Helper()
	tx, err := e.db().BeginTx(e.ctx, nil)
	require.NoError(t, err)
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		res, err := tx.ExecContext(e.ctx, `INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state) VALUES (?, 'UC1', ?, 'not_yet_evaluated')`,
			fmt.Sprintf("%s-%05d", prefix, i), run)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		ids = append(ids, id)
	}
	require.NoError(t, tx.Commit())
	return ids
}

// requeuedItems maps video id to the number of requeued_by_user items.
func (e *runNowEnv) requeuedItems(t *testing.T) map[int64]int {
	t.Helper()
	rows, err := e.db().QueryContext(e.ctx, `SELECT video_id, COUNT(*) FROM deferred_item WHERE deferral_reason='requeued_by_user' GROUP BY video_id`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		require.NoError(t, rows.Scan(&id, &n))
		out[id] = n
	}
	require.NoError(t, rows.Err())
	return out
}

func (e *runNowEnv) requireOneItemEach(t *testing.T, ids []int64, msg string, args ...any) {
	t.Helper()
	got := e.requeuedItems(t)
	for _, id := range ids {
		require.Equal(t, 1, got[id], "video %d: %s", id, fmt.Sprintf(msg, args...))
	}
}

// requireNoRequeuedItems fails when any requeued_by_user item exists.
func (e *runNowEnv) requireNoRequeuedItems(t *testing.T, msg string, args ...any) {
	t.Helper()
	require.Empty(t, e.requeuedItems(t), "%s", fmt.Sprintf(msg, args...))
}

// rowsAbove dumps the deferred items with id above floor, one line per row.
func (e *runNowEnv) deferredDump(t *testing.T, where string, args ...any) []string {
	t.Helper()
	rows, err := e.db().QueryContext(e.ctx, `SELECT id, video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at, retry_run_id
FROM deferred_item WHERE `+where+` ORDER BY id`, args...)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, video, run int64
		var reason, status, created string
		var retryAfter, retryRun sql.NullString
		var count int
		require.NoError(t, rows.Scan(&id, &video, &run, &reason, &retryAfter, &count, &status, &created, &retryRun))
		out = append(out, fmt.Sprintf("%d|%d|%d|%s|%v|%d|%s|%s|%v", id, video, run, reason, retryAfter, count, status, created, retryRun))
	}
	require.NoError(t, rows.Err())
	return out
}

// requeueMix is the BR-037 "Verified by" seed set: every kind of unfinished
// video, plus the finished, retained and unavailable controls.
type requeueMix struct {
	failed, completed, stopped, interrupted, legacyRunning int64
	stranded                                               []StrandedVideo
	finals                                                 []int64
	retained                                               []int64
	queued, queuedErrors, removed, notEnded, noOrigin      []int64
	blocked                                                []int64
	errorsOnly                                             []int64
}

func (e *runNowEnv) seedMix(t *testing.T) requeueMix {
	t.Helper()
	var m requeueMix
	m.failed = e.sRun(t, "manual", "failed", 1, "")
	m.completed = e.sRun(t, "scheduled", "completed", 1, "")
	m.stopped = e.sRun(t, "manual", "stopped", 1, "")
	m.interrupted = e.sRun(t, "scheduled", "interrupted", 1, "")
	m.legacyRunning = e.sRun(t, "manual", "running", 0, "discovery")
	old := "2026-01-01T00:00:00Z"

	add := func(id, origin int64) {
		m.stranded = append(m.stranded, StrandedVideo{VideoID: id, OriginRunID: origin})
	}
	for i, st := range []string{"discovered", "not_yet_evaluated", "evaluating", "evaluated", "acted"} {
		add(e.sVideo(t, fmt.Sprintf("mx-s-%d", i), m.failed, st, nil), m.failed)
	}
	add(e.sVideo(t, "mx-s-completed-run", m.completed, "not_yet_evaluated", nil), m.completed)
	add(e.sVideo(t, "mx-s-stopped-run", m.stopped, "evaluating", fp("not_yet_evaluated")), m.stopped)
	matched := e.sVideo(t, "mx-s-matched", m.failed, "evaluated", nil)
	createRouteDecision(t, e.ctx, e.stateStore, matched, m.failed, e.routeID, 1, state.RouteDecisionMatched)
	add(matched, m.failed)

	// Finished: a re-queue would reopen a final outcome (BR-039, ARS-006 K6).
	m.finals = append(m.finals,
		e.sVideo(t, "mx-f-completed", m.failed, "completed", fp("accepted")),
		e.sVideo(t, "mx-f-rejected", m.failed, "evaluated", fp("rejected")),
		e.sVideo(t, "mx-f-excluded", m.failed, "evaluated", fp("excluded")),
		e.sVideo(t, "mx-f-unavailable", m.failed, "evaluated", fp("unavailable_on_youtube")))

	// Committed deferred or failed outcome with no queue item: counted only.
	m.retained = append(m.retained,
		e.sVideo(t, "mx-r-deferred", m.failed, "deferred", fp("deferred")),
		e.sVideo(t, "mx-r-failed", m.failed, "failed", fp("failed")))

	q := e.sVideo(t, "mx-q-pending", m.failed, "not_yet_evaluated", nil)
	e.sDeferred(t, q, m.failed, "pending", 0, old)
	m.queued = append(m.queued, q)
	q = e.sVideo(t, "mx-q-claimed", m.failed, "not_yet_evaluated", nil)
	e.sDeferred(t, q, m.failed, "retrying", m.interrupted, old)
	m.queued = append(m.queued, q)
	q = e.sVideo(t, "mx-q-error-active", m.failed, "not_yet_evaluated", nil)
	e.sError(t, q, m.failed, "active", 0, old)
	m.queuedErrors = append(m.queuedErrors, q)
	q = e.sVideo(t, "mx-q-error-claimed", m.failed, "not_yet_evaluated", nil)
	e.sError(t, q, m.failed, "retrying", m.interrupted, old)
	m.queuedErrors = append(m.queuedErrors, q)

	r := e.sVideo(t, "mx-rm-deferred", m.failed, "not_yet_evaluated", nil)
	e.sDeferred(t, r, m.failed, "discarded", 0, old)
	m.removed = append(m.removed, r)
	r = e.sVideo(t, "mx-rm-error", m.failed, "not_yet_evaluated", nil)
	e.sError(t, r, m.failed, "dismissed", 0, old)
	m.removed = append(m.removed, r)

	m.notEnded = append(m.notEnded,
		e.sVideo(t, "mx-o-interrupted", m.interrupted, "not_yet_evaluated", nil),
		e.sVideo(t, "mx-o-legacy", m.legacyRunning, "not_yet_evaluated", nil))
	m.noOrigin = append(m.noOrigin, e.sVideo(t, "mx-n-deleted-run", 0, "not_yet_evaluated", nil))

	g := e.sVideo(t, "mx-b-guard", m.failed, "not_yet_evaluated", nil)
	e.sGuard(t, g, m.failed)
	m.blocked = append(m.blocked, g)
	return m
}

// A1 (AC6–8, AC4) and K6 (ARS-006): the confirm gives exactly the stranded
// videos one pending, immediately eligible item, and changes nothing else.
func TestRequeueConfirmWritesOnlyTheStrandedItems(t *testing.T) {
	e := newRunNowEnv(t)
	m := e.seedMix(t)

	preview, err := e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.True(t, preview.Available)
	require.Equal(t, len(m.stranded), preview.Requeue)
	require.Equal(t, len(m.queued)+len(m.queuedErrors), preview.AlreadyQueued.Videos)
	require.Equal(t, len(m.queuedErrors), preview.AlreadyQueued.InErrorsQueue)
	require.Equal(t, len(m.removed), preview.RemovedByUser)
	require.Equal(t, len(m.notEnded), preview.NotEnded.Videos)
	require.Equal(t, len(m.noOrigin), preview.NoOriginRun)
	require.Equal(t, len(m.blocked), preview.Blocked.Videos)
	require.Equal(t, len(m.retained), preview.RetainedWithoutQueueItem.Videos)

	rowsBefore := e.tableDump(t, requeueRowSnapshot...)
	var floor int64
	require.NoError(t, e.db().QueryRow(`SELECT COALESCE(MAX(id), 0) FROM deferred_item`).Scan(&floor))
	oldItems := e.deferredDump(t, `id <= ?`, floor)
	requests := e.server.Requests()
	expected := preview.Requeue

	res, err := e.pipeline.RequeueStranded(e.ctx, &expected)
	require.NoError(t, err)
	require.Equal(t, len(m.stranded), res.Requeued)
	require.Zero(t, res.SkippedConcurrently)
	require.Equal(t, &expected, res.Expected)
	require.Equal(t, preview.StrandedReport, res.Report, "the confirm acted on the same scan the preview showed")

	// Nothing but new deferred items: every other row, including both Errors
	// entries and every run, is byte-identical, and the provider saw nothing.
	require.Equal(t, rowsBefore, e.tableDump(t, requeueRowSnapshot...))
	require.Equal(t, oldItems, e.deferredDump(t, `id <= ?`, floor), "earlier queue items and their history are unchanged")
	require.Equal(t, requests, e.server.Requests(), "no provider request")

	// The new items: one per stranded video, pending, due at once, origin run,
	// one shared created_at, owned by no run.
	var want []string
	rows, err := e.db().QueryContext(e.ctx, `SELECT created_at FROM deferred_item WHERE id > ? LIMIT 1`, floor)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var created string
	require.NoError(t, rows.Scan(&created))
	require.NoError(t, rows.Close())
	_, err = time.Parse(time.RFC3339, created)
	require.NoError(t, err)
	for _, s := range m.stranded {
		want = append(want, fmt.Sprintf("%d|%d|requeued_by_user|{ false}|0|pending|%s|{ false}", s.VideoID, s.OriginRunID, created))
	}
	var got []string
	for _, line := range e.deferredDump(t, `id > ?`, floor) {
		// The ids among one confirm's items follow the query plan, not the
		// video order; the contract is one item per video, not their ids.
		got = append(got, strings.SplitN(line, "|", 2)[1])
	}
	require.Len(t, got, len(want))
	require.ElementsMatch(t, want, got)

	// K6: no finished video gained an item, and every control kept its state.
	items := e.requeuedItems(t)
	var controls []int64
	for _, group := range [][]int64{m.finals, m.retained, m.queued, m.queuedErrors, m.removed, m.notEnded, m.noOrigin, m.blocked} {
		controls = append(controls, group...)
	}
	for _, id := range controls {
		require.Zero(t, items[id], "video %d must get no item", id)
	}
	require.Len(t, items, len(m.stranded))

	// AC10: a repeat changes nothing; the re-queued videos now have an item.
	again, err := e.pipeline.RequeueStranded(e.ctx, nil)
	require.NoError(t, err)
	require.Zero(t, again.Requeued)
	require.Zero(t, again.Report.Requeue, "nothing to re-queue")
	require.Equal(t, rowsBefore, e.tableDump(t, requeueRowSnapshot...))
	require.Len(t, e.deferredDump(t, `id > ?`, floor), len(want))
	after, err := e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.Zero(t, after.Requeue)
	require.Equal(t, preview.AlreadyQueued.Videos+len(m.stranded), after.AlreadyQueued.Videos)
}

// K6 (ARS-006): the preview counts and the confirm inserts no final video,
// and a video that became final between the scan and the write is skipped.
func TestRequeueFinalVideosGetNoItem(t *testing.T) {
	finals := []struct{ state, outcome string }{{"completed", "accepted"}, {"evaluated", "rejected"}, {"evaluated", "excluded"}, {"evaluated", "unavailable_on_youtube"}}
	t.Run("preview and confirm", func(t *testing.T) {
		e := newRunNowEnv(t)
		run, ids := e.seedPlanned(t, 4)
		var finalIDs []int64
		for i, f := range finals {
			finalIDs = append(finalIDs, e.sVideo(t, fmt.Sprintf("k6-%d", i), run, f.state, fp(f.outcome)))
		}
		preview, err := e.pipeline.PreviewStranded(e.ctx)
		require.NoError(t, err)
		require.Equal(t, 4, preview.Requeue, "final videos are in no category")
		require.Zero(t, preview.AlreadyQueued.Videos+preview.RemovedByUser+preview.NotEnded.Videos+preview.Blocked.Videos+preview.NoOriginRun)
		res := e.confirm(t)
		require.Equal(t, len(ids), res.Requeued)
		items := e.requeuedItems(t)
		for _, id := range finalIDs {
			require.Zero(t, items[id], "final video %d must get no item", id)
		}
		require.Len(t, items, len(ids))
	})
	for _, f := range finals {
		t.Run("finished in between: "+f.outcome, func(t *testing.T) {
			e := newRunNowEnv(t)
			_, ids := e.seedPlanned(t, 4)
			e.hookBeforeWrite(t, func() {
				e.sExec(t, `UPDATE video SET processing_state=?, final_outcome=? WHERE id=?`, f.state, f.outcome, ids[0])
			})
			res, err := e.pipeline.RequeueStranded(e.ctx, nil)
			require.NoError(t, err)
			require.Equal(t, 3, res.Requeued)
			require.Equal(t, 1, res.SkippedConcurrently)
			require.Zero(t, e.requeuedItems(t)[ids[0]])
		})
	}
}

// hookBeforeWrite installs a requeueTx seam that runs fn once between the scan
// (phase A) and the write (phase B), then writes normally.
func (e *runNowEnv) hookBeforeWrite(t *testing.T, fn func()) {
	t.Helper()
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	var once sync.Once
	e.pipeline.requeueTx = func(ctx context.Context, body func(*sql.Tx) error) error {
		once.Do(fn)
		return qs.WithRetryTx(ctx, body)
	}
}

// Preview: availability follows the gate and the registered runs, the counts
// are current either way, and it writes nothing and calls no provider.
func TestPreviewStrandedAvailability(t *testing.T) {
	e := newRunNowEnv(t)
	_, ids := e.seedPlanned(t, 3)
	sim := e.legacySpec(t, "dry_run", "running")
	sim.initiatedBy = nil
	simID := e.seedRun(t, sim)
	prod := e.seedRun(t, e.eligible(t, time.Now().UTC()))
	rows := e.tableDump(t, strandedSnapshot...)
	requests := e.server.Requests()

	p, err := e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.True(t, p.Available)
	require.Empty(t, p.UnavailableReason)
	require.Equal(t, len(ids), p.Requeue)

	e.pipeline.registerRun(simID)
	p, err = e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.True(t, p.Available, "a running simulation does not make it unavailable")
	e.pipeline.unregisterRun(simID)

	e.pipeline.registerRun(prod)
	p, err = e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.False(t, p.Available)
	require.Equal(t, RequeueUnavailableProductionRun, p.UnavailableReason)
	require.Equal(t, len(ids), p.Requeue, "the counts stay current when unavailable")
	e.pipeline.unregisterRun(prod)

	e.pipeline.producerGate.Store(producersGated)
	e.pipeline.registerRun(prod)
	p, err = e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.False(t, p.Available)
	require.Equal(t, RequeueUnavailableStartupRecovery, p.UnavailableReason, "the gate wins over a registered run")
	require.Equal(t, len(ids), p.Requeue)

	require.Equal(t, rows, e.tableDump(t, strandedSnapshot...))
	require.Equal(t, requests, e.server.Requests())
}

// A2 (AC2–3): every refusal changes nothing and calls no provider; a running
// simulation does not refuse; a production run started after the preview
// refuses the confirmation.
func TestRequeueRefusals(t *testing.T) {
	refused := func(t *testing.T, e *runNowEnv, want error) {
		t.Helper()
		rows := e.tableDump(t, strandedSnapshot...)
		requests := e.server.Requests()
		res, err := e.pipeline.RequeueStranded(e.ctx, nil)
		require.ErrorIs(t, err, want)
		require.Nil(t, res)
		require.Equal(t, rows, e.tableDump(t, strandedSnapshot...), "nothing changed")
		require.Equal(t, requests, e.server.Requests(), "no provider request")
		e.requireNoRequeuedItems(t, "refused")
	}
	t.Run("a Run Now run holds the production lock", func(t *testing.T) {
		e := newRunNowEnv(t)
		e.seedPlanned(t, 3)
		adm, done := e.stalledRunNow(t)
		start := time.Now()
		refused(t, e, ErrProductionRunActive)
		require.Less(t, time.Since(start), 5*time.Second, "refuses without waiting")
		e.stop(t, adm.RunID(), StopAccepted)
		waitDone(t, done)
	})
	t.Run("a scheduled run registered before Execute", func(t *testing.T) {
		e := newRunNowEnv(t)
		e.seedPlanned(t, 3)
		run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
		require.NoError(t, err)
		refused(t, e, ErrProductionRunActive)
		e.pipeline.unregisterRun(run.ID)
		require.NotNil(t, e.confirm(t))
	})
	t.Run("a queue-retry run", func(t *testing.T) {
		e := newRunNowEnv(t)
		adm, done := e.stalledRunNow(t)
		e.stop(t, adm.RunID(), StopAccepted)
		waitDone(t, done)
		var itemID int64
		v3, err := e.stateStore.GetVideoByExternalID(e.ctx, "V3")
		require.NoError(t, err)
		require.NoError(t, e.db().QueryRow(`SELECT id FROM deferred_item WHERE video_id=? AND status='pending'`, v3.ID).Scan(&itemID))
		qs, err := queue.NewStore(e.db())
		require.NoError(t, err)
		e.pipeline.queueStore = qs
		e.seedPlanned(t, 3)
		e.stallOn("V3")
		retryDone := make(chan error, 1)
		go func() { retryDone <- e.pipeline.RetryQueueEntry(e.ctx, itemID, false) }()
		require.Equal(t, "V3", e.waitBlocked(t))
		refused(t, e, ErrProductionRunActive)
		var retryRun int64
		require.NoError(t, e.db().QueryRow(`SELECT MAX(id) FROM run`).Scan(&retryRun))
		e.stop(t, retryRun, StopAccepted)
		require.NoError(t, <-retryDone)
	})
	t.Run("a resumed run", func(t *testing.T) {
		e := newRunNowEnv(t)
		e.stallOn("V2")
		_ = e.interruptedAtLimit(t, testLimit)
		e.stallOn()
		e.seedPlanned(t, 3)
		adm, err := e.pipeline.AdmitRunNow(e.ctx)
		require.NoError(t, err)
		require.True(t, adm.Resumed())
		refused(t, e, ErrProductionRunActive)
		adm.Release()
	})
	t.Run("startup recovery in progress", func(t *testing.T) {
		e := newRunNowEnv(t)
		e.seedPlanned(t, 3)
		e.pipeline.producerGate.Store(producersGated)
		e.pipeline.productionMu.Lock() // a lock attempt would report production_run_active instead
		refused(t, e, ErrStartupRecoveryActive)
		e.pipeline.productionMu.Unlock()
		require.True(t, e.pipeline.initiationMu.TryLock(), "initiationMu stays free")
		e.pipeline.initiationMu.Unlock()
	})
	t.Run("a running simulation does not refuse", func(t *testing.T) {
		e := newRunNowEnv(t)
		_, ids := e.seedPlanned(t, 3)
		sim := e.legacySpec(t, "dry_run", "running")
		sim.initiatedBy = nil
		simID := e.seedRun(t, sim)
		e.pipeline.registerRun(simID)
		res := e.confirm(t)
		require.Equal(t, len(ids), res.Requeued)
		require.Equal(t, state.RunStatusRunning, e.run(t, simID).Status, "the simulation is untouched")
	})
	t.Run("a run that starts after the preview refuses the confirmation", func(t *testing.T) {
		e := newRunNowEnv(t)
		_, ids := e.seedPlanned(t, 3)
		preview, err := e.pipeline.PreviewStranded(e.ctx)
		require.NoError(t, err)
		require.True(t, preview.Available)
		adm, err := e.pipeline.AdmitRunNow(e.ctx)
		require.NoError(t, err)
		_, err = e.pipeline.RequeueStranded(e.ctx, &preview.Requeue)
		require.ErrorIs(t, err, ErrProductionRunActive)
		e.requireNoRequeuedItems(t, "refused")
		again, err := e.pipeline.PreviewStranded(e.ctx)
		require.NoError(t, err)
		require.False(t, again.Available)
		adm.Release()
		res, err := e.pipeline.RequeueStranded(e.ctx, &preview.Requeue)
		require.NoError(t, err)
		require.Equal(t, len(ids), res.Requeued)
	})
}

// abortInsertsAfter makes the insert of the (n+1)th new queue item fail. Ids
// are sequential, so a bound on NEW.id is exact and costs nothing per row.
func (e *runNowEnv) abortInsertsAfter(t *testing.T, n int) {
	t.Helper()
	var floor int64
	require.NoError(t, e.db().QueryRow(`SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='deferred_item'), 0)`).Scan(&floor))
	e.sExec(t, fmt.Sprintf(`CREATE TRIGGER requeue_inject AFTER INSERT ON deferred_item
WHEN NEW.id > %d BEGIN SELECT RAISE(ABORT, 'injected'); END`, floor+int64(n)))
}

// A3 (AC9): a failing insert, anywhere in the chunked write, leaves no item.
func TestRequeueIsAtomic(t *testing.T) {
	e := newRunNowEnv(t)
	_, ids := e.seedPlanned(t, 2500) // two chunks: 2,000 and 500
	rows := e.tableDump(t, strandedSnapshot...)
	requests := e.server.Requests()

	for _, abortAt := range []int{1500, 2100} { // inside the first chunk, then inside the second
		e.abortInsertsAfter(t, abortAt)
		res, err := e.pipeline.RequeueStranded(e.ctx, nil)
		require.ErrorIs(t, err, ErrRequeueNotApplied, "abort at %d", abortAt)
		require.Nil(t, res)
		require.NotContains(t, err.Error(), "injected", "no raw database text reaches the caller")
		require.Equal(t, rows, e.tableDump(t, strandedSnapshot...), "abort at %d: not one item survives", abortAt)
		e.sExec(t, `DROP TRIGGER requeue_inject`)
	}
	require.Equal(t, requests, e.server.Requests())

	// Still correct afterwards: the same videos are re-queued, all of them.
	res := e.confirm(t)
	require.Equal(t, len(ids), res.Requeued)
	e.requireOneItemEach(t, ids, "after the failed attempts")
}

// A3 (AC9), crash form: the writer process is killed with SIGKILL while phase
// B holds the writer; the reopened database holds none or all of the items.
const requeueKillEnv = "YUHAUL_REQUEUE_KILL_DB"

func TestRequeueKillChild(t *testing.T) {
	path := os.Getenv(requeueKillEnv)
	if path == "" {
		t.Skip("helper process of TestRequeueKilledDuringWrite")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	p := NewPipeline(db, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	p.requeueTx = func(ctx context.Context, fn func(*sql.Tx) error) error {
		fmt.Println("REQUEUE_WRITING")
		return qs.WithRetryTx(ctx, fn)
	}
	res, err := p.RequeueStranded(ctx, nil)
	require.NoError(t, err)
	fmt.Printf("REQUEUE_DONE %d\n", res.Requeued)
}

func TestRequeueKilledDuringWrite(t *testing.T) {
	const planned = 6000 // three chunks
	ctx := context.Background()
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	db, err := store.Open(ctx, template)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO run (started_at, run_type, status, action_authority_version, mutation_safety_version) VALUES ('2026-01-01T00:00:00Z','manual','failed',1,1)`)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	for i := 0; i < planned; i++ {
		_, err = tx.ExecContext(ctx, `INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state) VALUES (?, 'UC1', 1, 'not_yet_evaluated')`, fmt.Sprintf("kill-%05d", i))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	require.NoError(t, db.Close())
	for _, suffix := range []string{"-wal", "-shm"} {
		_, err := os.Stat(template + suffix)
		require.True(t, os.IsNotExist(err), "the template must be a single file: %s", suffix)
	}
	raw, err := os.ReadFile(template)
	require.NoError(t, err)

	// run starts the helper over a fresh copy and returns once it has finished.
	// A delay >= 0 kills it with SIGKILL that long after it printed
	// REQUEUE_WRITING (the writer lock is requested right after); a negative
	// delay lets it finish. done reports whether the helper had already
	// printed REQUEUE_DONE.
	run := func(i int, delay time.Duration) (path string, done bool, took time.Duration) {
		path = filepath.Join(dir, fmt.Sprintf("child-%d.db", i))
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		cmd := exec.Command(os.Args[0], "-test.run=^TestRequeueKillChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), requeueKillEnv+"="+path)
		out, err := cmd.StdoutPipe()
		require.NoError(t, err)
		require.NoError(t, cmd.Start())
		sc := bufio.NewScanner(out)
		var began time.Time
		for sc.Scan() {
			if sc.Text() == "REQUEUE_WRITING" {
				began = time.Now()
				break
			}
		}
		require.False(t, began.IsZero(), "the helper never reached the write")
		finished := make(chan bool, 1)
		go func() {
			for sc.Scan() {
				if strings.HasPrefix(sc.Text(), "REQUEUE_DONE") {
					finished <- true
					return
				}
			}
			finished <- false
		}()
		if delay >= 0 {
			select {
			case done = <-finished:
			case <-time.After(delay):
			}
			_ = cmd.Process.Kill()
		} else {
			done = <-finished
		}
		took = time.Since(began)
		_ = cmd.Wait()
		return path, done, took
	}
	count := func(path string) (n int, integrity string) {
		db, err := store.Open(ctx, path)
		require.NoError(t, err)
		defer db.Close()
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='requeued_by_user'`).Scan(&n))
		require.NoError(t, db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity))
		return n, integrity
	}

	// Calibrate: how long the write takes when nobody kills it.
	path, done, took := run(0, -1)
	require.True(t, done)
	n, integrity := count(path)
	require.Equal(t, planned, n)
	require.Equal(t, "ok", integrity)

	var none, all, killedAfterCommit int
	for i := 1; i <= raceIterations; i++ {
		path, done, _ := run(i, time.Duration(rand.Int64N(int64(2*took)+1)))
		n, integrity := count(path)
		require.Equal(t, "ok", integrity, "iteration %d", i)
		switch n {
		case 0:
			require.False(t, done, "iteration %d: the helper reported completion but no item survived", i)
			none++
		case planned:
			all++
			if !done {
				killedAfterCommit++
			}
		default:
			t.Fatalf("iteration %d: %d of %d items survived a SIGKILL: the write is not atomic", i, n, planned)
		}
	}
	t.Logf("write took %s; after SIGKILL: none %d, all %d (%d killed after the commit, before the report)", took, none, all, killedAfterCommit)
}

// A4: a commit failure whose result cannot be told is reported as unknown, and
// the next confirm is correct whether or not the first one took effect.
func TestRequeueCommitUncertainty(t *testing.T) {
	t.Run("commit failed after it took effect", func(t *testing.T) {
		e := newRunNowEnv(t)
		_, ids := e.seedPlanned(t, 5)
		qs, err := queue.NewStore(e.db())
		require.NoError(t, err)
		e.pipeline.requeueTx = func(ctx context.Context, fn func(*sql.Tx) error) error {
			if err := qs.WithRetryTx(ctx, fn); err != nil {
				return err
			}
			return fmtCommitErr()
		}
		res, err := e.pipeline.RequeueStranded(e.ctx, nil)
		require.ErrorIs(t, err, ErrRequeueOutcomeUnknown)
		require.Nil(t, res)
		e.requireOneItemEach(t, ids, "the commit did take effect")
		e.pipeline.requeueTx = nil
		again := e.confirm(t)
		require.Zero(t, again.Requeued)
		require.Zero(t, again.Report.Requeue)
		e.requireOneItemEach(t, ids, "a repeat adds nothing")
	})
	t.Run("commit failed without taking effect", func(t *testing.T) {
		e := newRunNowEnv(t)
		_, ids := e.seedPlanned(t, 5)
		e.pipeline.requeueTx = func(ctx context.Context, fn func(*sql.Tx) error) error { return fmtCommitErr() }
		_, err := e.pipeline.RequeueStranded(e.ctx, nil)
		require.ErrorIs(t, err, ErrRequeueOutcomeUnknown)
		e.requireNoRequeuedItems(t, "nothing was written")
		e.pipeline.requeueTx = nil
		res := e.confirm(t)
		require.Equal(t, len(ids), res.Requeued)
		e.requireOneItemEach(t, ids, "the next confirm completes it")
	})
	t.Run("a read failure is not applied and leaks nothing", func(t *testing.T) {
		e := newRunNowEnv(t)
		e.seedPlanned(t, 2)
		e.sExec(t, `ALTER TABLE deferred_item RENAME TO deferred_item_x`)
		_, err := e.pipeline.RequeueStranded(e.ctx, nil)
		require.ErrorIs(t, err, ErrRequeueNotApplied)
		require.NotContains(t, err.Error(), "deferred_item")
		_, err = e.pipeline.PreviewStranded(e.ctx)
		require.Error(t, err)
	})
}

// A5 (AC5, AC10): two concurrent confirms, and a third, give one result.
func TestRequeueConcurrentConfirms(t *testing.T) {
	for i := 0; i < 20; i++ {
		e := newRunNowEnv(t)
		_, ids := e.seedPlanned(t, 30)
		var wg sync.WaitGroup
		results := make([]*RequeueResult, 2)
		errs := make([]error, 2)
		start := make(chan struct{})
		for n := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[n], errs[n] = e.pipeline.RequeueStranded(e.ctx, nil)
			}()
		}
		close(start)
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		require.Equal(t, len(ids), results[0].Requeued+results[1].Requeued, "iteration %d", i)
		require.True(t, (results[0].Requeued == 0) != (results[1].Requeued == 0), "iteration %d: one requeued, one had nothing to do", i)
		e.requireOneItemEach(t, ids, "iteration %d", i)
		require.Zero(t, e.confirm(t).Requeued, "a third confirm")
		e.requireOneItemEach(t, ids, "after the third confirm")
	}
}

// A6 (AC5, AC16): the queue changes between the scan and the write. The
// affected videos are skipped, the rest is written, and the result says so.
func TestRequeueSkipsVideosThatChangedBetweenScanAndWrite(t *testing.T) {
	e := newRunNowEnv(t)
	run, ids := e.seedPlanned(t, 8)
	qs, err := queue.NewStore(e.db())
	require.NoError(t, err)
	now := time.Now().UTC()
	preview, err := e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)

	e.hookBeforeWrite(t, func() {
		// 0: an Errors entry appears, and the user dismisses it.
		eid, err := qs.CreateErrorEntry(e.ctx, &queue.ErrorEntry{VideoID: ids[0], RunID: run, Category: queue.ErrorCategoryRuntime, Message: "late", Status: queue.ErrorStatusActive, CreatedAt: now})
		require.NoError(t, err)
		require.NoError(t, qs.DismissErrorEntry(e.ctx, eid))
		// 1: a deferred item appears, and the user discards it.
		did, err := qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: ids[1], RunID: run, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: now.Add(time.Second)})
		require.NoError(t, err)
		require.NoError(t, qs.DiscardDeferredItem(e.ctx, did))
		// 2: a deferred item appears and stays active.
		_, err = qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: ids[2], RunID: run, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: now})
		require.NoError(t, err)
		// 3: an Errors entry appears and stays active.
		_, err = qs.CreateErrorEntry(e.ctx, &queue.ErrorEntry{VideoID: ids[3], RunID: run, Category: queue.ErrorCategoryRuntime, Message: "late", Status: queue.ErrorStatusActive, CreatedAt: now})
		require.NoError(t, err)
		// 4: the video finishes.
		e.sExec(t, `UPDATE video SET processing_state='evaluated', final_outcome='rejected' WHERE id=?`, ids[4])
		// 5: a send guard is held for it.
		e.sGuard(t, ids[5], run)
	})
	requestsBefore := e.server.Requests()
	res, err := e.pipeline.RequeueStranded(e.ctx, &preview.Requeue)
	require.NoError(t, err)
	require.Equal(t, 8, res.Report.Requeue, "the report is the scan the confirm started from")
	require.Equal(t, 2, res.Requeued)
	require.Equal(t, 6, res.SkippedConcurrently)
	require.NotEqual(t, *res.Expected, res.Requeued, "the caller can tell the number differs from the preview")
	items := e.requeuedItems(t)
	require.Equal(t, map[int64]int{ids[6]: 1, ids[7]: 1}, items)
	require.Equal(t, requestsBefore, e.server.Requests())
	// The user's own removals are untouched: still the latest item of 0 and 1.
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND status='dismissed'`, ids[0]))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='discarded'`, ids[1]))
}

// ---- A7: races (AC16), each raceIterations times ---------------------------

// raceConfirmWith runs one confirm against one other operation per iteration,
// on a fresh database. check sees both results after both returned.
func raceConfirmWith(t *testing.T, setup func(t *testing.T, e *runNowEnv) (other func() error, check func(confirmErr, otherErr error))) {
	t.Helper()
	var confirmedFirst, otherFirst int
	for i := 0; i < raceIterations; i++ {
		e := newRunNowEnv(t)
		other, check := setup(t, e)
		var confirmErr, otherErr error
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		// A short random head start for one side, so that both orders occur.
		lead := time.Duration(rand.IntN(1500)) * time.Microsecond
		confirmLeads := rand.IntN(2) == 0
		pause := func(leads bool) {
			if leads {
				time.Sleep(lead)
			}
		}
		go func() {
			defer wg.Done()
			<-start
			pause(!confirmLeads)
			_, confirmErr = e.pipeline.RequeueStranded(e.ctx, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			pause(confirmLeads)
			otherErr = other()
		}()
		close(start)
		wg.Wait()
		if confirmErr == nil {
			confirmedFirst++
		} else {
			otherFirst++
		}
		check(confirmErr, otherErr)
	}
	t.Logf("confirm accepted in %d iterations, refused in %d", confirmedFirst, otherFirst)
}

// retryWhileActive repeats fn while it reports a production run.
func retryWhileActive(fn func() error) error {
	for {
		if err := fn(); !errors.Is(err, ErrProductionRunActive) {
			return err
		}
	}
}

// serialConfirm is the assertion every race shares: the confirm either ran in
// full or was refused with nothing written, and never anything else.
func serialConfirm(t *testing.T, e *runNowEnv, confirmErr error, planned []int64, i string) (accepted bool) {
	t.Helper()
	switch {
	case confirmErr == nil:
		e.requireOneItemEach(t, planned, "%s", i)
		return true
	case errors.Is(confirmErr, ErrProductionRunActive):
		e.requireNoRequeuedItems(t, "%s: a refused confirm wrote nothing", i)
		return false
	}
	t.Fatalf("%s: unexpected confirm error: %v", i, confirmErr)
	return false
}

func TestRequeueRaceRunNowPress(t *testing.T) {
	raceConfirmWith(t, func(t *testing.T, e *runNowEnv) (func() error, func(error, error)) {
		_, planned := e.seedPlanned(t, 4)
		var adm *RunNowAdmission
		other := func() error {
			return retryWhileActive(func() (err error) { adm, err = e.pipeline.AdmitRunNow(e.ctx); return })
		}
		return other, func(confirmErr, otherErr error) {
			require.NoError(t, otherErr, "the press never fails")
			accepted := serialConfirm(t, e, confirmErr, planned, "press")
			if accepted {
				// Confirm first: the new run claims the items that exist, or
				// leaves them pending. Never anything else.
				require.Equal(t, len(planned), e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='requeued_by_user'
 AND ((status='pending' AND retry_run_id IS NULL) OR (status='retrying' AND retry_run_id=?))`, adm.RunID()))
			}
			adm.Release()
			if !accepted {
				require.Equal(t, len(planned), e.confirm(t).Requeued, "once the press is released the confirm is accepted")
			}
		}
	})
}

func TestRequeueRaceScheduledStart(t *testing.T) {
	raceConfirmWith(t, func(t *testing.T, e *runNowEnv) (func() error, func(error, error)) {
		_, planned := e.seedPlanned(t, 4)
		var run *state.Run
		other := func() (err error) { run, err = e.pipeline.StartRun(e.ctx, state.RunTypeScheduled); return }
		return other, func(confirmErr, otherErr error) {
			require.NoError(t, otherErr)
			accepted := serialConfirm(t, e, confirmErr, planned, "scheduled")
			e.pipeline.unregisterRun(run.ID)
			if !accepted {
				require.Equal(t, len(planned), e.confirm(t).Requeued)
			}
		}
	})
}

func TestRequeueRaceQueueRetry(t *testing.T) {
	raceConfirmWith(t, func(t *testing.T, e *runNowEnv) (func() error, func(error, error)) {
		failed, planned := e.seedPlanned(t, 4)
		// A video the user retries: it has current inputs, so the retry run
		// needs no provider call and rejects it on its title.
		q := e.sVideo(t, "rq-retried", failed, "not_yet_evaluated", nil)
		e.sDeferred(t, q, failed, "pending", 0, "2026-01-01T00:00:00Z")
		seedCurrentMetadata(t, e.db(), "rq-retried")
		var itemID int64
		require.NoError(t, e.db().QueryRow(`SELECT id FROM deferred_item WHERE video_id=?`, q).Scan(&itemID))
		qs, err := queue.NewStore(e.db())
		require.NoError(t, err)
		e.pipeline.queueStore = qs
		other := func() error { return e.pipeline.RetryQueueEntry(e.ctx, itemID, false) }
		return other, func(confirmErr, otherErr error) {
			require.NoError(t, otherErr)
			accepted := serialConfirm(t, e, confirmErr, planned, "retry")
			require.Zero(t, e.requeuedItems(t)[q], "the retried video was already queued: no re-queue item either way")
			require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM run_video_outcome WHERE video_id=?`, q), "the retry ran")
			if !accepted {
				require.Equal(t, len(planned), e.confirm(t).Requeued)
			}
		}
	})
}

func TestRequeueRaceDiscard(t *testing.T) {
	raceConfirmWith(t, func(t *testing.T, e *runNowEnv) (func() error, func(error, error)) {
		_, planned := e.seedPlanned(t, 4)
		broken := e.eligible(t, time.Now().UTC().Add(-2*time.Hour))
		broken.phase = sp("bogus")
		discarded := e.seedRun(t, broken)
		owned := e.seedUnfinished(t, discarded, "rq-discard", 3)
		other := func() error {
			return retryWhileActive(func() error { _, err := e.pipeline.DiscardRun(e.ctx, discarded); return err })
		}
		return other, func(confirmErr, otherErr error) {
			require.NoError(t, otherErr)
			accepted := serialConfirm(t, e, confirmErr, planned, "discard")
			// The discarded run's videos belong to it until the discard hands
			// them over, and to the handoff afterwards: never to the confirm.
			for _, id := range owned {
				require.Zero(t, e.requeuedItems(t)[id], "video %d gets no re-queue item", id)
				require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND deferral_reason='discarded_by_user'`, id))
				require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, id), "exactly one active item")
			}
			if !accepted {
				require.Equal(t, len(planned), e.confirm(t).Requeued)
			}
		}
	})
}

func TestRequeueRaceMarkAsEnded(t *testing.T) {
	var confirmFirst, endFirst int
	defer func() {
		t.Logf("confirm before the end in %d iterations, end before the confirm in %d", confirmFirst, endFirst)
	}()
	raceConfirmWith(t, func(t *testing.T, e *runNowEnv) (func() error, func(error, error)) {
		_, planned := e.seedPlanned(t, 3)
		legacy := e.seedRun(t, e.legacySpec(t, "manual", "running"))
		owned := e.seedUnfinished(t, legacy, "rq-legacy", 4)
		other := func() error {
			return retryWhileActive(func() error { _, err := e.pipeline.EndLegacyRun(e.ctx, legacy); return err })
		}
		return other, func(confirmErr, otherErr error) {
			require.NoError(t, otherErr)
			accepted := serialConfirm(t, e, confirmErr, planned, "end")
			// One serial order: the confirm ran before the end (the run still
			// owned its videos: none re-queued) or after it (all of them). A
			// subset is the mixed state AC16 forbids.
			got := e.requeuedItems(t)
			var n int
			for _, id := range owned {
				n += got[id]
			}
			if !accepted {
				require.Zero(t, n)
			} else {
				require.Contains(t, []int{0, len(owned)}, n, "the run's videos are all re-queued or none")
				if n == 0 {
					confirmFirst++
				} else {
					endFirst++
				}
			}
			if n == 0 {
				// The rest is re-queued by the next confirm: the run's videos,
				// and the others too when the first confirm was refused.
				want := len(owned)
				if !accepted {
					want += len(planned)
				}
				require.Equal(t, want, e.confirm(t).Requeued)
			}
			e.requireOneItemEach(t, append(append([]int64{}, planned...), owned...), "every video ends with exactly one item")
		}
	})
}

func TestRequeueRaceDismissAndRetry(t *testing.T) {
	for _, queueKind := range []string{"errors", "deferred"} {
		t.Run(queueKind, func(t *testing.T) {
			raceConfirmWith(t, func(t *testing.T, e *runNowEnv) (func() error, func(error, error)) {
				failed, planned := e.seedPlanned(t, 4)
				q := e.sVideo(t, "rq-dismissed", failed, "not_yet_evaluated", nil)
				qs, err := queue.NewStore(e.db())
				require.NoError(t, err)
				var other func() error
				if queueKind == "errors" {
					id, err := qs.CreateErrorEntry(e.ctx, &queue.ErrorEntry{VideoID: q, RunID: failed, Category: queue.ErrorCategoryRuntime, Message: "seed", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
					require.NoError(t, err)
					other = func() error { return qs.DismissErrorEntry(e.ctx, id) }
				} else {
					id, err := qs.CreateDeferredItem(e.ctx, &queue.DeferredItem{VideoID: q, RunID: failed, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
					require.NoError(t, err)
					other = func() error { return qs.DiscardDeferredItem(e.ctx, id) }
				}
				return other, func(confirmErr, otherErr error) {
					require.NoError(t, otherErr)
					serialConfirm(t, e, confirmErr, planned, "dismiss")
					// Either order leaves the removed video out: queued before the
					// removal, removed after it.
					require.Zero(t, e.requeuedItems(t)[q])
					if queueKind == "errors" {
						require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND status='dismissed'`, q))
					} else {
						require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='discarded'`, q))
					}
					require.Equal(t, 1, e.count(t, `SELECT (SELECT COUNT(*) FROM deferred_item WHERE video_id=?1) + (SELECT COUNT(*) FROM error_entry WHERE video_id=?1)`, q),
						"the removed video keeps its one item and gets no second")
				}
			})
		})
	}
}

// E1, confirm part (AC14): a run-14-shaped legacy run owns its unevaluated
// videos until it is marked as ended. Before that the confirm re-queues none
// of them; after it, every one that is neither blocked nor queued, with the
// run as origin. The held item is claimed by the next run.
func TestRequeueRun14ShapeBeforeAndAfterMarkAsEnded(t *testing.T) {
	e := newRunNowEnv(t)
	run14 := e.sRun(t, "manual", "running", 0, "discovery")
	ids := e.seedUnfinished(t, run14, "e1", 200)
	for _, id := range ids[:3] {
		e.sGuard(t, id, run14)
	}
	e.sDeferred(t, ids[3], run14, "retrying", run14, "2026-01-01T00:00:00Z")
	rows := e.tableDump(t, requeueRowSnapshot...)
	requests := e.server.Requests()

	res := e.confirm(t)
	require.Zero(t, res.Requeued)
	require.Equal(t, 196, res.Report.NotEnded.Videos)
	require.Equal(t, 3, res.Report.Blocked.Videos)
	require.Equal(t, 1, res.Report.AlreadyQueued.Videos)
	e.requireNoRequeuedItems(t, "run 14 has not ended")
	require.Equal(t, rows, e.tableDump(t, requeueRowSnapshot...))

	_, err := e.pipeline.EndLegacyRun(e.ctx, run14)
	require.NoError(t, err)
	preview, err := e.pipeline.PreviewStranded(e.ctx)
	require.NoError(t, err)
	require.Equal(t, 196, preview.Requeue)
	res, err = e.pipeline.RequeueStranded(e.ctx, &preview.Requeue)
	require.NoError(t, err)
	require.Equal(t, 196, res.Requeued)
	items := e.requeuedItems(t)
	require.Len(t, items, 196)
	for _, id := range ids[4:] {
		require.Equal(t, 1, items[id])
	}
	require.Equal(t, 196, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='requeued_by_user' AND run_id=?`, run14))
	require.Equal(t, requests, e.server.Requests(), "no provider request")

	// The next new run claims them with the held item, in creation order.
	run, err := e.pipeline.StartRun(e.ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status='retrying' AND retry_run_id=?`, ids[3], run.ID),
		"the item run 14 held is claimed by the next run")
	require.Equal(t, 196, e.count(t, `SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='requeued_by_user' AND status='retrying' AND retry_run_id=?`, run.ID))
	e.pipeline.unregisterRun(run.ID)
}
