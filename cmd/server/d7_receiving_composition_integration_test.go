//go:build integration

package main

// D7 composition test (run-initiation ARS-005 §6.1, §6.1.1 and §11.7 test 31a;
// BR-037 AC11, AC13 and AC18). The real server composition (Run Now, the
// guarded YouTube adapter, the migrated SQLite store and the registered run
// readers) runs as a child process. Only YouTube is a loopback fake, and it
// answers every videos.list request with a body this test scripts exactly.
//
// 122 re-queued and quota-deferred videos have no stored inputs, so each needs
// a fetched input. They are claimed oldest first and fetched in requests of
// 50, 50 and 22 ids:
//
//	request 1 (d7-000..049): two items have an unparseable publishedAt;
//	request 2 (d7-050..099): one element has an empty id and sits before other
//	                         peers; two requested ids are not returned;
//	request 3 (d7-100..121): a clean answer that omits four ids.
//
// Titles decide the route (Jazz accepts, any other title rejects), so a
// decision can only come from the fetched title.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func d7ID(i int) string { return fmt.Sprintf("d7-%03d", i) }

// d7Jazz is true for the videos whose fetched title matches the Jazz route.
func d7Jazz(i int) bool { return i%2 == 0 || i == 3 }

func d7Title(i int) string {
	if d7Jazz(i) {
		return "Jazz night " + d7ID(i)
	}
	return "Plain talk " + d7ID(i)
}

// d7Provider scripts videos.list. It records the ids of every request.
type d7Provider struct {
	mu       sync.Mutex
	requests [][]string
	// body returns the response body for one request.
	body func(ids []string) string
}

func (p *d7Provider) handle(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("id"), ",")
	p.mu.Lock()
	p.requests = append(p.requests, ids)
	body := p.body
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body(ids)))
}

func (p *d7Provider) seen() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]string(nil), p.requests...)
}

func (p *d7Provider) setBody(f func(ids []string) string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body = f
}

func d7Item(id string, title string, publishedAt string) map[string]any {
	return map[string]any{"id": id,
		"snippet": map[string]any{
			"title": title, "channelId": "UC1", "channelTitle": "Test Channel", "description": "fetched",
			"publishedAt": publishedAt,
		},
		"contentDetails": map[string]any{"duration": "PT4M30S"},
	}
}

func d7Body(items []map[string]any) string {
	b, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// d7Seed stores n pending deferred items, one video each, created one second
// apart from two days ago in index order, with no stored inputs. reason picks
// each item's deferral reason.
func d7Seed(t *testing.T, dbPath string, n int, reason func(i int) string) {
	t.Helper()
	seedBase(t, dbPath)
	db, err := store.Open(t.Context(), dbPath)
	require.NoError(t, err)
	defer db.Close()
	origin, err := db.Exec(`INSERT INTO run (started_at, completed_at, run_type, status) VALUES (?, ?, 'manual', 'completed')`,
		time.Now().UTC().Add(-72*time.Hour), time.Now().UTC().Add(-71*time.Hour))
	require.NoError(t, err)
	originID, err := origin.LastInsertId()
	require.NoError(t, err)
	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	for i := 0; i < n; i++ {
		v, err := db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', ?, 'not_yet_evaluated', 'not_yet_evaluated')`, d7ID(i), originID)
		require.NoError(t, err)
		vid, err := v.LastInsertId()
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at) VALUES (?, ?, ?, 0, 'pending', ?)`,
			vid, originID, reason(i), base.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
	}
}

// d7Row is the identity of the queue row of one video: id, origin run, reason,
// age, retry time, status and owner. retry_count is separate (d7Retries): the
// run's claim counts one attempt, and a release does not undo it.
func d7Row(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	return scalar[string](t, db, `SELECT d.id||'|'||d.run_id||'|'||d.deferral_reason||'|'||d.created_at||'|'||IFNULL(d.retry_after,'-')||'|'||d.status||'|'||IFNULL(d.retry_run_id,'-')
		FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, id)
}

func d7Retries(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	return scalar[int](t, db, `SELECT d.retry_count FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE v.video_id=?`, id)
}

func d7Count(t *testing.T, db *sql.DB, table, id string, run int64) int {
	t.Helper()
	return scalar[int](t, db, `SELECT count(*) FROM `+table+` x JOIN video v ON v.id=x.video_id WHERE v.video_id=? AND x.run_id=?`, id, run)
}

func d7RunNow(t *testing.T, srv *serverProcess, db *sql.DB) int64 {
	t.Helper()
	code, body := srv.postJSON("/api/runs")
	require.Equal(t, 202, code, "%v", body)
	run := int64(body["run_id"].(float64))
	eventually(t, runDoneBudget, "the run to end", func() bool {
		return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, run) != "running"
	})
	require.Equal(t, "completed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, run), srv.out.String())
	return run
}

// d7YouTube is a fake YouTube whose playlist inserts do not block: the fake
// announces each one on a small channel that this test drains.
func d7YouTube(t *testing.T) (*fakeYouTube, *d7Provider) {
	t.Helper()
	yt := newFakeYouTube(t)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-yt.postReached:
			case <-done:
				return
			}
		}
	}()
	prov := &d7Provider{}
	yt.setVideosHook(prov.handle)
	return yt, prov
}

func d7Range(from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, d7ID(i))
	}
	return out
}

// D7: a successful, accounted response whose middle chunk has one anonymous
// element still evaluates the 48 identified peers in the same run. The two
// requested ids without an identifiable return stay pending, nobody in that
// chunk is absent, and the next chunk is still requested.
func TestD7_AnonymousElementDoesNotWithholdIdentifiedPeers(t *testing.T) {
	const (
		badA, badB         = 3, 17    // request 1: publishedAt does not parse
		lostRequeued       = 60       // request 2: requeued_by_user, no identifiable return
		lostOrdinary       = 75       // request 2: quota_exhausted, no identifiable return
		goneRQ1, goneRQ2   = 100, 101 // request 3: requeued_by_user, truly omitted
		goneOrd1, goneOrd2 = 102, 103 // request 3: quota_exhausted, truly omitted
	)
	reason := func(i int) string {
		switch i {
		case lostRequeued, goneRQ1, goneRQ2:
			return "requeued_by_user"
		}
		return "quota_exhausted"
	}
	omitted := map[int]bool{lostRequeued: true, lostOrdinary: true, goneRQ1: true, goneRQ2: true, goneOrd1: true, goneOrd2: true}
	valid := func(i int) bool { return !omitted[i] && i != badA && i != badB }

	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	d7Seed(t, dbPath, 122, reason)
	yt, prov := d7YouTube(t)
	published := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	prov.setBody(func(ids []string) string {
		var items []map[string]any
		for _, id := range ids {
			var i int
			_, err := fmt.Sscanf(id, "d7-%d", &i)
			require.NoError(t, err)
			switch {
			case omitted[i]:
				continue
			case i == badA || i == badB:
				items = append(items, d7Item(id, d7Title(i), "not a time"))
			default:
				items = append(items, d7Item(id, d7Title(i), published))
			}
			if i == 54 { // the anonymous element, before the remaining peers
				items = append(items, d7Item("", "Jazz anonymous", published))
			}
		}
		return d7Body(items)
	})

	db := openReadDB(t, dbPath)
	srv := startServer(t, dbPath, yt.srv.URL)
	srv.waitOverview()
	waitLog(t, srv, "scheduler started")
	before := map[string]string{}
	for _, i := range []int{badA, badB, lostRequeued, lostOrdinary, goneRQ1, goneRQ2, goneOrd1, goneOrd2} {
		before[d7ID(i)] = d7Row(t, db, d7ID(i))
	}

	run := d7RunNow(t, srv, db)

	// Three accounted requests of 50, 50 and 22, each id requested once, oldest
	// claim first. This is the claim's own order, not an assumption of the test.
	got := prov.seen()
	require.Len(t, got, 3, "one request per chunk; the unusable middle chunk did not stop the third")
	require.Equal(t, d7Range(0, 50), got[0])
	require.Equal(t, d7Range(50, 100), got[1])
	require.Equal(t, d7Range(100, 122), got[2])
	require.Equal(t, 3, scalar[int](t, db, `SELECT count(*) FROM quota_ledger WHERE run_id=? AND operation_type='videos_list'`, run), "one ledger entry per request")

	// Every identified peer is evaluated in this run, on its fetched title.
	evaluated := 0
	for i := 0; i < 122; i++ {
		if !valid(i) {
			continue
		}
		evaluated++
		id := d7ID(i)
		require.Equal(t, 1, d7Count(t, db, "route_decision", id, run), "%s has this run's decision", id)
		want := "rejected"
		if d7Jazz(i) {
			want = "accepted"
		}
		require.Equal(t, want, scalar[string](t, db, `SELECT IFNULL(o.final_outcome,'') FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE v.video_id=? AND o.run_id=?`, id, run), id)
		require.Equal(t, d7Title(i), scalar[string](t, db, `SELECT title FROM transient_metadata WHERE video_id=?`, id), "%s was decided on the fetched title", id)
		posts := 0
		if d7Jazz(i) {
			posts = 1
		}
		require.Equal(t, posts, yt.postCount(id), id)
		require.Zero(t, d7Count(t, db, "error_entry", id, run), id)
	}
	require.Equal(t, 114, evaluated, "122 less the 2 bad publishedAt, the 2 unidentified in request 2 and the 4 omitted in request 3")

	// The four unresolved ids of requests 1 and 2 are released exactly as they
	// were: pending, same queue row, age, origin and reason, with no decision,
	// attempt, guard, Errors entry or outcome.
	for _, i := range []int{badA, badB, lostRequeued, lostOrdinary} {
		id := d7ID(i)
		require.Equal(t, before[id], d7Row(t, db, id), "%s keeps its exact queue identity", id)
		require.Equal(t, 1, d7Retries(t, db, id), "%s: this run's one claim, no same-run reclaim", id)
		require.Equal(t, "not_yet_evaluated/not_yet_evaluated", scalar[string](t, db, `SELECT processing_state||'/'||IFNULL(final_outcome,'') FROM video WHERE video_id=?`, id), id)
		require.Contains(t, []string{"", "not_yet_evaluated"}, scalar[string](t, db, `SELECT IFNULL(o.final_outcome,'') FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE v.video_id=? AND o.run_id=?`, id, run), "%s has no run outcome", id)
		for _, table := range []string{"route_decision", "executed_action", "playlist_send_guard", "error_entry"} {
			require.Zero(t, d7Count(t, db, table, id, run), "%s: %s", id, table)
		}
		require.Zero(t, yt.postCount(id), id)
		require.Zero(t, scalar[int](t, db, `SELECT count(*) FROM transient_metadata WHERE video_id=?`, id), "%s: nothing is stored for an unusable input", id)
	}
	// The requeued id the response did not identify is not unavailable.
	require.NotEqual(t, "unavailable_on_youtube", scalar[string](t, db, `SELECT IFNULL(final_outcome,'') FROM video WHERE video_id=?`, d7ID(lostRequeued)))

	// Request 3 was a clean answer, so its four omissions are real absences:
	// two requeued ids are unavailable_on_youtube (completed, no Errors entry),
	// two ordinary ids fail as video_unavailable with one Errors entry each.
	for _, i := range []int{goneRQ1, goneRQ2} {
		id := d7ID(i)
		require.Equal(t, "unavailable_on_youtube", scalar[string](t, db, `SELECT o.final_outcome FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE v.video_id=? AND o.run_id=?`, id, run), id)
		require.Equal(t, "evaluated/unavailable_on_youtube", scalar[string](t, db, `SELECT processing_state||'/'||final_outcome FROM video WHERE video_id=?`, id), id)
		require.Contains(t, d7Row(t, db, id), "|completed|", id)
		require.Zero(t, d7Count(t, db, "error_entry", id, run), id)
		require.Zero(t, d7Count(t, db, "route_decision", id, run), id)
	}
	for _, i := range []int{goneOrd1, goneOrd2} {
		id := d7ID(i)
		require.Equal(t, "failed", scalar[string](t, db, `SELECT o.final_outcome FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE v.video_id=? AND o.run_id=?`, id, run), id)
		require.Contains(t, scalar[string](t, db, `SELECT o.error_message FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE v.video_id=? AND o.run_id=?`, id, run), "code=video_unavailable", id)
		require.Equal(t, 1, scalar[int](t, db, `SELECT count(*) FROM error_entry x JOIN video v ON v.id=x.video_id WHERE v.video_id=? AND x.status='active'`, id), "one active Errors entry for %s", id)
	}
	require.Equal(t, 2, scalar[int](t, db, `SELECT videos_unavailable FROM run WHERE id=?`, run))
	require.Equal(t, 2, scalar[int](t, db, `SELECT count(*) FROM error_entry WHERE run_id=?`, run), "only the two ordinary omissions reach Errors")

	// The run's unavailable filter, through the registered reader, lists exactly
	// the two requeued omissions.
	var listed struct {
		Videos []struct {
			VideoID string `json:"video_id"`
		} `json:"videos"`
	}
	code, body := srv.do(http.MethodGet, "/api/runs/"+itoa(run)+"/videos?outcome="+string(state.FinalOutcomeUnavailableOnYouTube))
	require.Equal(t, 200, code, body)
	require.NoError(t, json.Unmarshal([]byte(body), &listed), body)
	var filtered []string
	for _, v := range listed.Videos {
		filtered = append(filtered, v.VideoID)
	}
	require.ElementsMatch(t, []string{d7ID(goneRQ1), d7ID(goneRQ2)}, filtered, body)

	// A fresh run with the four formerly unresolved ids now parseable processes
	// exactly those four. The already-final peers are not requested again.
	decisions := map[string]int{}
	for i := 0; i < 122; i++ {
		decisions[d7ID(i)] = scalar[int](t, db, `SELECT count(*) FROM route_decision x JOIN video v ON v.id=x.video_id WHERE v.video_id=?`, d7ID(i))
	}
	prov.setBody(func(ids []string) string {
		var items []map[string]any
		for _, id := range ids {
			var i int
			_, err := fmt.Sscanf(id, "d7-%d", &i)
			require.NoError(t, err)
			items = append(items, d7Item(id, d7Title(i), published))
		}
		return d7Body(items)
	})
	second := d7RunNow(t, srv, db)
	all := prov.seen()
	require.Len(t, all, 4, "the corrected run makes one more request")
	require.Equal(t, []string{d7ID(badA), d7ID(badB), d7ID(lostRequeued), d7ID(lostOrdinary)}, all[3], "only the four formerly unresolved ids are requested")
	for i := 0; i < 122; i++ {
		id := d7ID(i)
		fresh := d7Count(t, db, "route_decision", id, second)
		if i == badA || i == badB || i == lostRequeued || i == lostOrdinary {
			require.Equal(t, 1, fresh, "%s is decided by the corrected run", id)
			continue
		}
		require.Zero(t, fresh, "%s was already settled", id)
		require.Equal(t, decisions[id], scalar[int](t, db, `SELECT count(*) FROM route_decision x JOIN video v ON v.id=x.video_id WHERE v.video_id=?`, id), id)
	}
	require.Equal(t, 1, yt.postCount(d7ID(badA)), "d7-003 now has a Jazz title")
	require.Equal(t, 1, yt.postCount(d7ID(lostRequeued)))
	require.Zero(t, yt.postCount(d7ID(badB)))
	require.Zero(t, yt.postCount(d7ID(lostOrdinary)))
	srv.sigterm()
}

// D7 negatives through the same composition. A response whose envelope is not
// valid is not trusted at all, however valid its prefix looks, and proves no
// absence. A clean answer with an explicit empty items array is the control:
// it proves omission.
func TestD7_InvalidEnvelopeIsNotTrustedAndCleanEmptyProvesOmission(t *testing.T) {
	peer := func(id string) string {
		b, err := json.Marshal(d7Item(id, "Jazz night "+id, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)))
		require.NoError(t, err)
		return string(b)
	}
	for _, tc := range []struct {
		name string
		body func() string
		// trusted is whether the peers are evaluated; the requeued item then
		// ends unavailable_on_youtube only when the response proves omission.
		peers, requeued string
	}{
		{"continuation token after valid prefix", func() string {
			return `{"items":[` + peer("d7-000") + `,` + peer("d7-001") + `],"nextPageToken":"more"}`
		}, "pending", "pending"},
		{"truncated after valid prefix", func() string { return `{"items":[` + peer("d7-000") + `,` + peer("d7-001") + `,{"id":""` }, "pending", "pending"},
		{"duplicate peer next to an anonymous element", func() string {
			return `{"items":[` + peer("d7-000") + `,{"id":""},` + peer("d7-000") + `]}`
		}, "pending", "pending"},
		{"foreign id next to an anonymous element", func() string {
			return `{"items":[` + peer("d7-000") + `,{"id":""},` + peer("zzz-foreign") + `]}`
		}, "pending", "pending"},
		{"clean empty items", func() string { return `{"items":[]}` }, "failed", "unavailable_on_youtube"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// d7-000 and d7-001 are ordinary; d7-002 is requeued by the user.
			reason := func(i int) string {
				if i == 2 {
					return "requeued_by_user"
				}
				return "quota_exhausted"
			}
			dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
			d7Seed(t, dbPath, 3, reason)
			yt, prov := d7YouTube(t)
			prov.setBody(func([]string) string { return tc.body() })
			db := openReadDB(t, dbPath)
			srv := startServer(t, dbPath, yt.srv.URL)
			srv.waitOverview()
			waitLog(t, srv, "scheduler started")
			before := map[string]string{}
			for i := 0; i < 3; i++ {
				before[d7ID(i)] = d7Row(t, db, d7ID(i))
			}

			run := d7RunNow(t, srv, db)

			require.Equal(t, [][]string{d7Range(0, 3)}, prov.seen())
			for i := 0; i < 3; i++ {
				id := d7ID(i)
				outcome := scalar[string](t, db, `SELECT IFNULL(o.final_outcome,'') FROM run_video_outcome o JOIN video v ON v.id=o.video_id WHERE v.video_id=? AND o.run_id=?`, id, run)
				want := tc.peers
				if i == 2 {
					want = tc.requeued
				}
				if want == "pending" {
					require.Equal(t, before[id], d7Row(t, db, id), "%s is released unchanged", id)
					require.Equal(t, 1, d7Retries(t, db, id), "%s: this run's one claim", id)
					require.Contains(t, []string{"", "not_yet_evaluated"}, outcome, id)
					for _, table := range []string{"route_decision", "executed_action", "error_entry"} {
						require.Zero(t, d7Count(t, db, table, id, run), "%s: %s", id, table)
					}
					require.Zero(t, yt.postCount(id), id)
					continue
				}
				require.Equal(t, want, outcome, id)
			}
			srv.sigterm()
		})
	}
}
