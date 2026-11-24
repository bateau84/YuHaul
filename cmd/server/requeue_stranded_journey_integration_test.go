//go:build integration

package main

// ARS-005 §11.9 test 42 (J1) and §11.8 test 35 (G2) over the real server.
//
// Each test re-executes this test binary into the real main() (TestMain in
// startup_composition_integration_test.go), so the migrated SQLite store, the
// registered HTTP routes, the production pipeline, the scheduler and the
// guarded YouTube adapter are the shipped ones. Only YouTube is a loopback
// fake. The prerequisites written before the process starts are what
// production already holds: a token, the Jazz route, a schedule, and two
// legacy version-0 runs that are still "running" with their unfinished videos.
// Everything after that is produced by the product: the run's end, the
// preview, the confirm, the Deferred view, both scheduled runs and their
// claims, receipts, outcomes, quota entries and playlist inserts, and a
// restart of the process on the same database file.
//
// Placement: the plan and ARS-005 §11.9 name `./e2e`. The real-process
// harness (serverProcess, the loopback fake, SIGTERM and restart) lives in
// package main, which ./e2e cannot import, so the fixtures live beside it:
//
//	go test -tags=integration -count=1 -run '^Test(J1|G2)' ./cmd/server
//
// J1 takes about two minutes: the real scheduler checks once a minute and
// fires on the first check after each process start. Quota-day rollover is not
// exercised (§11.6 test 19 owns it): J1's restart restores an available budget
// by raising the configured daily limit, which is a budget change, not a day
// boundary. internal/queue's TestDeferredPageG2PagingUnderChange stays as the
// store-level paging check; TestG2 below is the same property with the real
// run's claims and the real confirm.

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/scheduler"
)

// scheduledTickBudget bounds the wait for a scheduled run. The scheduler
// checks once a minute and an `@every 24h` schedule with no earlier firing is
// due at the first check after each start.
const scheduledTickBudget = 150 * time.Second

// postLog records the video id of every playlistItems.insert the fake
// received, in arrival order. The fake announces each insert on postReached.
type postLog struct {
	mu  sync.Mutex
	ids []string
}

func recordPosts(t *testing.T, yt *fakeYouTube) *postLog {
	t.Helper()
	l := &postLog{}
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case id := <-yt.postReached:
				l.mu.Lock()
				l.ids = append(l.ids, id)
				l.mu.Unlock()
			case <-done:
				return
			}
		}
	}()
	return l
}

func (l *postLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ids...)
}

// metadataScript answers videos.list: an id for which absent is true is
// cleanly left out of an otherwise ordinary 200 response; an id for which
// jazz is true gets a title the Jazz route accepts; every other id gets a
// title that route rejects.
type metadataScript struct {
	absent func(id string) bool
	jazz   func(id string) bool
}

func (m metadataScript) body(ids []string) string {
	published := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	var items []map[string]any
	for _, id := range ids {
		switch {
		case m.absent(id):
		case m.jazz(id):
			items = append(items, d7Item(id, "Jazz night "+id, published))
		default:
			items = append(items, d7Item(id, "Plain talk "+id, published))
		}
	}
	return d7Body(items)
}

// deferredPageRead is one GET /api/deferred page: the queue item ids and the
// external video ids in the order served, the cursor for the next page (0 when
// there is none) and the total of pending items.
type deferredPageRead struct {
	itemIDs  []int64
	videoIDs []string
	next     int64
	total    int
}

// deferredPage reads one page of the Deferred view after the given item id
// (0 starts at the beginning).
func deferredPage(t *testing.T, srv *serverProcess, limit int, after int64) deferredPageRead {
	t.Helper()
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if after > 0 {
		q.Set("after_id", strconv.FormatInt(after, 10))
	}
	status, body := srv.do(http.MethodGet, "/api/deferred?"+q.Encode())
	require.Equal(t, http.StatusOK, status, body)
	page := jsonMap(t, body)
	read := deferredPageRead{total: int(page["total"].(float64))}
	for _, it := range page["deferred"].([]any) {
		item := it.(map[string]any)
		read.itemIDs = append(read.itemIDs, int64(item["id"].(float64)))
		read.videoIDs = append(read.videoIDs, item["external_video_id"].(string))
	}
	if next := page["page"].(map[string]any)["next_after_id"]; next != nil {
		read.next = int64(next.(float64))
	}
	return read
}

// deferredWalk pages the Deferred view with the id cursor, starting after the
// given item id, until the last page, and returns the external video ids in
// the order served, the size of each page and the last reported total.
func deferredWalk(t *testing.T, srv *serverProcess, limit int, after int64) (ids []string, pages []int, total int) {
	t.Helper()
	for {
		page := deferredPage(t, srv, limit, after)
		ids = append(ids, page.videoIDs...)
		if len(page.videoIDs) > 0 {
			pages = append(pages, len(page.videoIDs))
		}
		total = page.total
		if page.next == 0 {
			return ids, pages, total
		}
		after = page.next
	}
}

// queueRow is every stored column of one video's queue rows, in id order. Two
// reads that return the same string saw the same rows, claim count and owner
// included.
func queueRow(t *testing.T, db *sql.DB, externalID string) string {
	t.Helper()
	rows, err := db.Query(`SELECT d.id||'|'||d.video_id||'|'||d.run_id||'|'||d.deferral_reason||'|'||IFNULL(d.retry_after,'-')||'|'||
			d.retry_count||'|'||d.status||'|'||d.created_at||'|'||IFNULL(d.retry_run_id,'-')||'|'||IFNULL(d.closed_reason,'-')
		FROM deferred_item d JOIN video v ON v.id = d.video_id WHERE v.video_id = ? ORDER BY d.id`, externalID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return strings.Join(out, ";")
}

func runOutcome(t *testing.T, db *sql.DB, run int64, externalID string) string {
	t.Helper()
	var out sql.NullString
	err := db.QueryRow(`SELECT o.final_outcome FROM run_video_outcome o JOIN video v ON v.id = o.video_id WHERE o.run_id = ? AND v.video_id = ?`, run, externalID).Scan(&out)
	if err == sql.ErrNoRows {
		return "(none)"
	}
	require.NoError(t, err)
	return out.String
}

// waitScheduledRun waits for the first run after the given id to finish and
// returns its id.
func waitScheduledRun(t *testing.T, db *sql.DB, srv *serverProcess, after int64) int64 {
	t.Helper()
	var run int64
	eventually(t, scheduledTickBudget, "the scheduler's run to start", func() bool {
		return scalar[int](t, db, `SELECT count(*) FROM run WHERE id > ?`, after) > 0
	})
	run = scalar[int64](t, db, `SELECT min(id) FROM run WHERE id > ?`, after)
	require.Equal(t, "scheduled", scalar[string](t, db, `SELECT run_type FROM run WHERE id = ?`, run))
	eventually(t, runDoneBudget, "the scheduled run to end", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id = ?`, run) != "running"
	})
	require.Equal(t, "completed", scalar[string](t, db, `SELECT status FROM run WHERE id = ?`, run), srv.out.String())
	return run
}

func run14ID(i int) string { return fmt.Sprintf("run14-%04d", i) }

func youngerID(i int) string { return fmt.Sprintf("younger-%04d", i) }

// TestJ1_RequeueStrandedScheduledRunRestartOldestFirst is §11.9 test 42:
// seed the run-14 shape, end it, preview, confirm, page the Deferred view,
// run a scheduled run with partial quota where some videos are absent from
// the provider, restart, and let the next run continue oldest first.
//
// Run 14 holds 200 unfinished videos. The provider matches four of them (the
// Jazz route: 0001, 0003, 0054, 0110) and cleanly leaves two out (0020, 0130).
// The daily budget of 54 units pays the four metadata reads (200 ids in
// requests of 50) and exactly one 50-unit insertion. A second legacy run, 15,
// stays unended with two younger videos until after the restart.
func TestJ1_RequeueStrandedScheduledRunRestartOldestFirst(t *testing.T) {
	const total, runningLegacy = 200, 14
	matched := map[string]bool{run14ID(1): true, run14ID(3): true, run14ID(54): true, run14ID(110): true}
	absent := map[string]bool{run14ID(20): true, run14ID(130): true}
	younger := []string{youngerID(0), youngerID(1)}

	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seedBase(t, dbPath)
	db := openReadDB(t, dbPath)

	require.NoError(t, mustScheduler(t, db).SetSchedule(context.Background(), &scheduler.Schedule{Expression: "@every 24h", Enabled: true}))
	for _, r := range []struct {
		id      int
		started string
	}{{runningLegacy, "2026-10-20T06:00:00Z"}, {15, "2026-10-21T06:00:00Z"}} {
		_, err := db.Exec(`INSERT INTO run (id, started_at, run_type, status, mutation_safety_version, action_authority_version, current_phase)
			VALUES (?, ?, 'manual', 'running', 0, 0, 'discovery')`, r.id, r.started)
		require.NoError(t, err)
	}
	insertVideo := func(id string, run int) {
		_, err := db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', ?, 'not_yet_evaluated', 'not_yet_evaluated')`, id, run)
		require.NoError(t, err)
	}
	for i := 0; i < total; i++ {
		insertVideo(run14ID(i), runningLegacy)
	}
	for _, id := range younger {
		insertVideo(id, 15)
	}

	yt := newFakeYouTube(t)
	posts := recordPosts(t, yt)
	prov := &d7Provider{}
	prov.setBody(metadataScript{
		absent: func(id string) bool { return absent[id] },
		jazz:   func(id string) bool { return matched[id] || strings.HasPrefix(id, "younger-") },
	}.body)
	yt.setVideosHook(prov.handle)

	// First process: a 54-unit daily budget.
	srv := startServer(t, dbPath, yt.srv.URL, "YUHAUL_DAILY_QUOTA=54")
	srv.waitOverview()
	waitLog(t, srv, "scheduler started")
	startedAt := time.Now()
	// lastRun is the newest run the steps below have already accounted for.
	lastRun := int64(15)
	notYetDue := func(step string) {
		t.Helper()
		require.Less(t, time.Since(startedAt), 45*time.Second, "%s took too long: the scheduler's next run would be due before it ended", step)
		require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM run WHERE id > ?`, lastRun), "no scheduled run may start before %s", step)
	}

	// 1-2. While run 14 has not ended, it and run 15 hold every video.
	status, body := srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	preview := jsonMap(t, body)
	require.Equal(t, true, preview["available"], body)
	require.EqualValues(t, 0, preview["requeue"], body)
	require.Equal(t, map[string]any{"videos": float64(total + len(younger)), "runs": []any{
		map[string]any{"run_id": float64(runningLegacy), "videos": float64(total)},
		map[string]any{"run_id": float64(15), "videos": float64(len(younger))},
	}}, preview["not_ended"], body)
	// End run 14 over HTTP; run 15 stays.
	status, body = srv.do(http.MethodPost, "/api/runs/14/end")
	require.Equal(t, http.StatusOK, status, body)

	// 3. Preview: run 14's 200 are stranded; run 15's two are not.
	status, body = srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	preview = jsonMap(t, body)
	require.EqualValues(t, total, preview["requeue"], body)
	require.Equal(t, map[string]any{"videos": float64(len(younger)), "runs": []any{
		map[string]any{"run_id": float64(15), "videos": float64(len(younger))},
	}}, preview["not_ended"], body)

	// 4. Confirm: 200 items, no provider request, no run, no quota entry.
	requests, runs, ledger := yt.requestCount(), scalar[int](t, db, `SELECT count(*) FROM run`), scalar[int](t, db, `SELECT count(*) FROM quota_ledger`)
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":200}`)
	require.Equal(t, http.StatusOK, status, body)
	res := jsonMap(t, body)
	require.Equal(t, "requeued", res["outcome"], body)
	require.EqualValues(t, total, res["requeued"], body)
	require.Equal(t, false, res["differs_from_preview"], body)
	require.Equal(t, requests, yt.requestCount(), "the confirm makes no provider request")
	require.Equal(t, runs, scalar[int](t, db, `SELECT count(*) FROM run`), "the confirm starts no run")
	require.Equal(t, ledger, scalar[int](t, db, `SELECT count(*) FROM quota_ledger`), "the confirm spends no quota")

	// 5. Page the Deferred view: four pages of 50 in run-14 order.
	var want []string
	for i := 0; i < total; i++ {
		want = append(want, run14ID(i))
	}
	ids, pages, listed := deferredWalk(t, srv, 50, 0)
	require.Equal(t, []int{50, 50, 50, 50}, pages)
	require.Equal(t, total, listed)
	require.Equal(t, want, ids)
	notYetDue("the Deferred walk")

	// 6. The scheduler's first run: partial quota, two videos absent.
	run16 := waitScheduledRun(t, db, srv, lastRun)
	lastRun = run16

	// Provider requests: 200 ids in four reads of 50, in claim order, then one
	// insertion for the oldest match; the rest of the budget is gone.
	var reads [][]string
	for i := 0; i < total; i += 50 {
		reads = append(reads, want[i:i+50])
	}
	require.Equal(t, reads, prov.seen(), "four metadata reads in claim order")
	require.Equal(t, []string{run14ID(1)}, posts.all(), "one insertion: the oldest match the budget could pay")
	require.Equal(t, 54, scalar[int](t, db, `SELECT IFNULL(SUM(units_consumed),0) FROM quota_ledger`), "4 metadata units + one 50-unit insertion")
	require.Equal(t, 4, scalar[int](t, db, `SELECT IFNULL(SUM(call_count),0) FROM quota_ledger WHERE operation_type NOT LIKE '%insert%'`))

	// Absent videos: unavailable_on_youtube, queue row completed, no Errors,
	// no decision, no send. Matched videos the budget could not pay stay
	// pending in place. Everything else was evaluated and finished.
	for id := range absent {
		require.Equal(t, "unavailable_on_youtube", runOutcome(t, db, run16, id), id)
		require.Equal(t, "completed", scalar[string](t, db, `SELECT d.status FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, id), id)
		require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM route_decision r JOIN video v ON v.id=r.video_id WHERE v.video_id=?`, id), id)
		require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM error_entry e JOIN video v ON v.id=e.video_id WHERE v.video_id=?`, id), id)
		require.Zero(t, posts.countOf(id), id)
	}
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM error_entry`), "no Errors entry for anything this run did")
	pending := []string{run14ID(3), run14ID(54), run14ID(110)}
	require.Equal(t, "accepted", runOutcome(t, db, run16, run14ID(1)))
	for _, id := range pending {
		require.Equal(t, "pending", scalar[string](t, db, `SELECT d.status FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, id), id)
		require.Contains(t, queueRow(t, db, id), "|requeued_by_user|", id)
	}
	ids, _, listed = deferredWalk(t, srv, 50, 0)
	require.Equal(t, pending, ids, "the Deferred view lists exactly the three matches the budget could not pay")
	require.Equal(t, 3, listed)

	// Run detail: two unavailable videos, filterable, the accepted witness
	// not among them.
	status, body = srv.do(http.MethodGet, "/api/runs/"+strconv.FormatInt(run16, 10))
	require.Equal(t, http.StatusOK, status, body)
	require.EqualValues(t, 2, jsonMap(t, body)["videos_unavailable"], body)
	status, body = srv.do(http.MethodGet, "/api/runs/"+strconv.FormatInt(run16, 10)+"/videos?outcome=unavailable_on_youtube")
	require.Equal(t, http.StatusOK, status, body)
	list := jsonMap(t, body)
	require.EqualValues(t, 2, list["pagination"].(map[string]any)["total"], body)
	var listedUnavailable []string
	for _, v := range list["videos"].([]any) {
		listedUnavailable = append(listedUnavailable, v.(map[string]any)["video_id"].(string))
	}
	require.ElementsMatch(t, []string{run14ID(20), run14ID(130)}, listedUnavailable)

	// The Errors queue is empty over HTTP too.
	status, body = srv.do(http.MethodGet, "/api/errors")
	require.Equal(t, http.StatusOK, status, body)
	require.Zero(t, jsonMap(t, body)["total"], body)

	// The younger videos belong to the unended run 15: no item, no outcome.
	for _, id := range younger {
		require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, id), id)
	}

	// 7. Restart on the same file with the budget restored.
	before := map[string]string{}
	for _, id := range pending {
		before[id] = queueRow(t, db, id)
	}
	srv.sigterm()
	reads, postsBefore := prov.seen(), posts.all()
	srv = startServer(t, dbPath, yt.srv.URL, "YUHAUL_DAILY_QUOTA=10000")
	srv.waitOverview()
	waitLog(t, srv, "scheduler started")
	startedAt = time.Now()
	for _, id := range pending {
		require.Equal(t, before[id], queueRow(t, db, id), "a restart changes no queue row")
	}

	// Run 15 ends; its two videos are re-queued and are younger than the
	// three pending matches.
	status, body = srv.do(http.MethodPost, "/api/runs/15/end")
	require.Equal(t, http.StatusOK, status, body)
	status, body = srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	require.EqualValues(t, len(younger), jsonMap(t, body)["requeue"], body)
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":2}`)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "requeued", jsonMap(t, body)["outcome"], body)
	for _, id := range pending {
		require.Equal(t, before[id], queueRow(t, db, id), "confirming run 15's videos leaves the older rows alone")
	}
	ids, _, listed = deferredWalk(t, srv, 50, 0)
	require.Equal(t, append(append([]string(nil), pending...), younger...), ids)
	require.Equal(t, 5, listed)
	notYetDue("the second confirm")

	// 8. The next run continues oldest first: the three older matches, then
	// the two younger videos, each sent exactly once.
	run17 := waitScheduledRun(t, db, srv, lastRun)
	require.Equal(t, append(append([]string(nil), postsBefore...), append(append([]string(nil), pending...), younger...)...), posts.all(), "inserts continue oldest first with no repeat")
	allReads := prov.seen()
	// The three older matches kept the inputs run 16 fetched; only the two
	// younger videos need a read. No omitted video is read again.
	require.Equal(t, [][]string{younger}, allReads[len(reads):], "the second run reads only what it has no stored inputs for")
	for _, id := range append(append([]string(nil), pending...), younger...) {
		require.Equal(t, "accepted", runOutcome(t, db, run17, id), id)
		require.Equal(t, "completed", scalar[string](t, db, `SELECT d.status FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, id), id)
	}
	for id := range absent {
		require.Equal(t, "(none)", runOutcome(t, db, run17, id), "an omitted video is not a member of the later run")
		require.Zero(t, posts.countOf(id))
	}
	_, _, listed = deferredWalk(t, srv, 50, 0)
	require.Zero(t, listed, "nothing is left pending")
	require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM error_entry`))
	status, body = srv.do(http.MethodGet, "/api/runs/"+strconv.FormatInt(run17, 10))
	require.Equal(t, http.StatusOK, status, body)
	require.EqualValues(t, 0, jsonMap(t, body)["videos_unavailable"], body)
	require.Equal(t, 6, len(posts.all()), "run 16's one insertion and run 17's five")
	require.Equal(t, len(posts.all()), scalar[int](t, db, `SELECT count(*) FROM executed_action WHERE outcome = 'succeeded'`), "one succeeded action per provider insertion")
	require.Equal(t, 3, scalar[int](t, db, `SELECT count(*) FROM executed_action WHERE outcome = 'deferred' AND run_id = ?`, run16), "run 16's three quota deferrals are recorded as deferred, not attempted")
	require.Equal(t, 9, scalar[int](t, db, `SELECT count(*) FROM executed_action`), "no other action outcome exists")
	srv.sigterm()
}

func (l *postLog) countOf(id string) int {
	n := 0
	for _, got := range l.all() {
		if got == id {
			n++
		}
	}
	return n
}

func mustScheduler(t *testing.T, db *sql.DB) *scheduler.Store {
	t.Helper()
	s, err := scheduler.NewStore(db)
	require.NoError(t, err)
	return s
}

// doWithin is do with its own client timeout. Starting a run claims its first
// 500 items before the 202, which takes longer than do's 5 seconds under -race.
func (p *serverProcess) doWithin(timeout time.Duration, method, path string) (int, string) {
	p.t.Helper()
	req, err := http.NewRequest(method, "http://"+p.addr+path, nil)
	require.NoError(p.t, err)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	require.NoError(p.t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(p.t, err)
	return resp.StatusCode, strings.TrimSpace(string(out))
}

// g2RunBudget bounds each wait on the 1,000-item run. It takes seconds
// normally and much longer under -race.
const g2RunBudget = 5 * time.Minute

func g2Pending(i int) string  { return fmt.Sprintf("g2-pending-%04d", i) }
func g2Stranded(i int) string { return fmt.Sprintf("g2-stranded-%04d", i) }

func seqIDs(from, to int) []int64 {
	var out []int64
	for i := from; i <= to; i++ {
		out = append(out, int64(i))
	}
	return out
}

// TestG2_DeferredPagingWhileARunClaimsAndTheConfirmInserts is §11.8 test 35 on
// the real server: while a reader pages the Deferred view, a real production
// run claims 1,000 items and the real confirm inserts 500.
//
// The queue holds 1,500 pending items, ids 1..1500. The 1,000 oldest by age
// are ids 51..1050; ids 1..50 and 1051..1500 are younger and are the
// witnesses. Another 500 videos are stranded and not queued. The daily budget
// of 20 units is exactly one metadata read per 50 of the 1,000 oldest, so the
// run claims two batches of 500 and then stops claiming (the third batch would
// be the witnesses), and every video is rejected, so nothing is sent.
//
// The reader fetches page 1 before the run, page 2 while the run is held at
// the provider inside its second batch (500 items completed, 500 claimed and
// retrying), and the rest after the run has ended and the confirm has added
// 500 items. The confirm cannot overlap a production run (BR-037 AC2), so the
// real confirm is asserted refused while the run is held and is applied once
// the run has released its producer slot; both changes happen inside one
// paging walk.
func TestG2_DeferredPagingWhileARunClaimsAndTheConfirmInserts(t *testing.T) {
	const pendingN, claimedFrom, claimedTo, strandedN = 1500, 51, 1050, 500
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seedBase(t, dbPath)
	db := openReadDB(t, dbPath)

	_, err := db.Exec(`INSERT INTO run (id, started_at, completed_at, run_type, status) VALUES (100, '2026-11-06T06:00:00Z', '2026-11-06T07:00:00Z', 'scheduled', 'failed')`)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	tx, err := db.Begin()
	require.NoError(t, err)
	for i := 1; i <= pendingN; i++ {
		res, err := tx.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', 100, 'not_yet_evaluated', 'not_yet_evaluated')`, g2Pending(i))
		require.NoError(t, err)
		vid, err := res.LastInsertId()
		require.NoError(t, err)
		created := now.Add(-24 * time.Hour).Add(time.Duration(i) * time.Second) // witnesses: younger
		if i >= claimedFrom && i <= claimedTo {
			created = now.Add(-72 * time.Hour).Add(time.Duration(i) * time.Second) // oldest by age
		}
		_, err = tx.Exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at) VALUES (?, 100, 'quota_exhausted', 0, 'pending', ?)`, vid, created)
		require.NoError(t, err)
	}
	for i := 1; i <= strandedN; i++ {
		_, err := tx.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', 100, 'not_yet_evaluated', 'not_yet_evaluated')`, g2Stranded(i))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	require.Equal(t, int64(pendingN), scalar[int64](t, db, `SELECT max(id) FROM deferred_item`))

	// The provider rejects every video's title (nothing is sent) and holds the
	// first metadata read of the run's second batch: the 11th, after ten reads
	// of 50 for the first 500.
	yt := newFakeYouTube(t)
	posts := recordPosts(t, yt)
	prov := &d7Provider{}
	prov.setBody(metadataScript{absent: func(string) bool { return false }, jazz: func(string) bool { return false }}.body)
	var (
		callsMu sync.Mutex
		calls   int
		reached = make(chan struct{})
		release = make(chan struct{})
		once    sync.Once
	)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	yt.setVideosHook(func(w http.ResponseWriter, r *http.Request) {
		callsMu.Lock()
		calls++
		n := calls
		callsMu.Unlock()
		if n == 11 {
			close(reached)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		prov.handle(w, r)
	})

	srv := startServer(t, dbPath, yt.srv.URL, "YUHAUL_DAILY_QUOTA=20")
	srv.waitOverview()

	status, body := srv.do(http.MethodGet, "/api/stranded-videos")
	require.Equal(t, http.StatusOK, status, body)
	require.EqualValues(t, strandedN, jsonMap(t, body)["requeue"], body)

	// Page 1, before the run: items 1..50, 1,500 in the queue.
	first := deferredPage(t, srv, 50, 0)
	require.Equal(t, seqIDs(1, 50), first.itemIDs)
	require.Equal(t, pendingN, first.total)
	require.EqualValues(t, 50, first.next)

	// The witnesses' rows, byte for byte, before the run.
	witnessRows := func() string {
		return scalar[string](t, db, `SELECT group_concat(r, char(10)) FROM (
			SELECT d.id||'|'||d.video_id||'|'||d.run_id||'|'||d.deferral_reason||'|'||IFNULL(d.retry_after,'-')||'|'||d.retry_count||'|'||d.status||'|'||d.created_at||'|'||IFNULL(d.retry_run_id,'-') AS r
			FROM deferred_item d WHERE d.id <= 50 OR d.id >= 1051 ORDER BY d.id)`)
	}
	witnessesBefore := witnessRows()
	requestsBefore := yt.requestCount()

	// A real Run Now run claims the 1,000 oldest in two batches.
	status, body = srv.doWithin(60*time.Second, http.MethodPost, "/api/runs")
	require.Equal(t, http.StatusAccepted, status, body)
	run := int64(jsonMap(t, body)["run_id"].(float64))
	select {
	case <-reached:
	case <-time.After(g2RunBudget):
		t.Fatalf("the run never reached its second batch's first metadata read\n%s", srv.out.String())
	}

	// Held inside batch 2: 500 completed, 500 claimed by this run.
	require.Equal(t, 500, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='completed'`))
	require.Equal(t, 500, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='retrying' AND retry_run_id=?`, run))
	require.Equal(t, 500, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='pending'`))

	// The confirm is refused while the run is active, and writes nothing.
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":500}`)
	require.Equal(t, http.StatusConflict, status, body)
	require.Equal(t, "production_run_active", jsonMap(t, body)["code"], body)
	require.Equal(t, pendingN, scalar[int](t, db, `SELECT count(*) FROM deferred_item`))

	// Page 2 of the same walk, mid-run: the cursor jumps over 51..1050.
	second := deferredPage(t, srv, 50, first.next)
	require.Equal(t, seqIDs(1051, 1100), second.itemIDs, "claimed and completed items are gone from the walk")
	require.Equal(t, 500, second.total)
	require.EqualValues(t, 1100, second.next)

	once.Do(func() { close(release) })
	eventually(t, g2RunBudget, "the run to end", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id = ?`, run) != "running"
	})
	require.Equal(t, "completed", scalar[string](t, db, `SELECT status FROM run WHERE id = ?`, run), srv.out.String())

	// The run claimed exactly the 1,000 oldest, read each once, spent exactly
	// the budget, sent nothing and left every witness row as it was.
	var wantReads [][]string
	for i := claimedFrom; i <= claimedTo; i += 50 {
		var ids []string
		for j := i; j < i+50; j++ {
			ids = append(ids, g2Pending(j))
		}
		wantReads = append(wantReads, ids)
	}
	require.Equal(t, wantReads, prov.seen(), "twenty reads of 50, oldest 1,000 in claim order, no read for a witness")
	require.Empty(t, posts.all(), "every title is rejected, so nothing is sent")
	require.Equal(t, 20, scalar[int](t, db, `SELECT IFNULL(SUM(units_consumed),0) FROM quota_ledger`))
	require.Equal(t, 1000, scalar[int](t, db, `SELECT count(*) FROM deferred_item WHERE status='completed' AND id BETWEEN 51 AND 1050`))
	require.Equal(t, 1000, scalar[int](t, db, `SELECT count(*) FROM run_video_outcome WHERE run_id=? AND final_outcome='rejected'`, run))
	require.Equal(t, witnessesBefore, witnessRows(), "the 500 witnesses were not claimed or changed")
	requestsAfterRun := yt.requestCount()
	require.Greater(t, requestsAfterRun, requestsBefore)

	// The real confirm, now that the run has released its producer slot,
	// appends 500 items with no provider request and no new quota entry.
	ledger := scalar[int](t, db, `SELECT count(*) FROM quota_ledger`)
	runs := scalar[int](t, db, `SELECT count(*) FROM run`)
	status, body = srv.doBody(http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":500}`)
	require.Equal(t, http.StatusOK, status, body)
	res := jsonMap(t, body)
	require.Equal(t, "requeued", res["outcome"], body)
	require.EqualValues(t, strandedN, res["requeued"], body)
	require.Equal(t, requestsAfterRun, yt.requestCount(), "the confirm makes no provider request")
	require.Equal(t, ledger, scalar[int](t, db, `SELECT count(*) FROM quota_ledger`))
	require.Equal(t, runs, scalar[int](t, db, `SELECT count(*) FROM run`))

	// The rest of the walk, from the cursor held since page 2.
	var rest []int64
	for after := second.next; after != 0; {
		page := deferredPage(t, srv, 50, after)
		rest = append(rest, page.itemIDs...)
		require.Equal(t, 1000, page.total)
		after = page.next
	}
	require.Equal(t, append(seqIDs(1101, 1500), seqIDs(1501, 2000)...), rest,
		"each item present throughout is returned once, then the 500 new items, all after the original last id")
	seen := map[int64]int{}
	for _, id := range append(append(append([]int64(nil), first.itemIDs...), second.itemIDs...), rest...) {
		seen[id]++
	}
	for id, n := range seen {
		require.Equal(t, 1, n, "item %d returned more than once", id)
		require.False(t, id >= claimedFrom && id <= claimedTo, "claimed item %d was returned", id)
	}
	require.Len(t, seen, 50+50+len(rest), "the three reads share no item")
	srv.sigterm()
}
