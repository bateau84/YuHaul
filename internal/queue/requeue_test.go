package queue

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func requeuedRows(t *testing.T, db *sql.DB) map[int64][]string {
	t.Helper()
	rows, err := db.Query(`SELECT video_id, run_id || '|' || status || '|' || retry_count || '|' || COALESCE(retry_after, 'NULL') || '|' || COALESCE(retry_run_id, 'NULL') || '|' || created_at
FROM deferred_item WHERE deferral_reason = 'requeued_by_user' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var line string
		if err := rows.Scan(&id, &line); err != nil {
			t.Fatal(err)
		}
		out[id] = append(out[id], line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func insertRequeued(t *testing.T, s *Store, plan []RequeueItem, now time.Time) (int, error) {
	t.Helper()
	var n int
	err := s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		n, err = s.InsertRequeuedTx(context.Background(), tx, plan, now)
		return err
	})
	return n, err
}

// A planned video that no longer qualifies is skipped; the others get exactly
// one pending item with the shared fields (ARS-005 §4, BR-037 AC3, AC6, AC10).
func TestInsertRequeuedTxRechecksEachVideoUnderTheWriter(t *testing.T) {
	s := newTestStore(t)
	db := s.db
	run := seedRun(t, db)
	origin := seedRun(t, db)
	video := func(ext, state string, outcome any) int64 {
		t.Helper()
		res, err := db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', ?, ?, ?)`, ext, run, state, outcome)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	item := func(v int64, status string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at) VALUES (?,?,?,?,?,?)`,
			v, run, "quota_exhausted", 0, status, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	plain := video("plain", "not_yet_evaluated", nil)
	unevaluated := video("unevaluated", "discovered", "not_yet_evaluated")
	queued := video("queued", "not_yet_evaluated", nil)
	item(queued, "pending")
	claimed := video("claimed", "not_yet_evaluated", nil)
	item(claimed, "retrying")
	errored := video("errored", "not_yet_evaluated", nil)
	if _, err := db.Exec(`INSERT INTO error_entry (video_id,run_id,error_category,error_message,status,created_at) VALUES (?,?,'runtime','x','active','2026-01-01T00:00:00Z')`, errored, run); err != nil {
		t.Fatal(err)
	}
	removed := video("removed", "not_yet_evaluated", nil)
	item(removed, "discarded")
	completed := video("completed", "completed", "accepted")
	rejected := video("rejected", "evaluated", "rejected")
	unavailable := video("unavailable", "evaluated", "unavailable_on_youtube")
	finishedItem := video("done-item", "not_yet_evaluated", nil)
	item(finishedItem, "completed") // history only: not an active item

	plan := []RequeueItem{{plain, origin}, {unevaluated, origin}, {queued, origin}, {claimed, origin}, {errored, origin},
		{removed, origin}, {completed, origin}, {rejected, origin}, {unavailable, origin}, {finishedItem, origin}}
	now := time.Date(2026, 11, 24, 12, 30, 0, 0, time.FixedZone("x", 3600))
	n, err := insertRequeued(t, s, plan, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("inserted %d items, want 3 (plain, unevaluated, finishedItem)", n)
	}
	got := requeuedRows(t, db)
	want := fmt.Sprintf("%d|pending|0|NULL|NULL|2026-11-24T11:30:00Z", origin)
	for _, v := range []int64{plain, unevaluated, finishedItem} {
		if len(got[v]) != 1 || got[v][0] != want {
			t.Errorf("video %d: items %v, want one %q", v, got[v], want)
		}
	}
	for _, v := range []int64{queued, claimed, errored, removed, completed, rejected, unavailable} {
		if len(got[v]) != 0 {
			t.Errorf("video %d must get no item, got %v", v, got[v])
		}
	}

	// Repeating the same plan changes nothing: each video now has its item.
	n, err = insertRequeued(t, s, plan, now.Add(time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("repeat inserted %d, err %v; want 0", n, err)
	}
	if len(requeuedRows(t, db)) != 3 {
		t.Fatal("a repeat changed the items")
	}
}

// The plan is written in chunks of 2,000 pairs inside the caller's transaction:
// every pair of a plan that spans several chunks is written, with one
// created_at, and a failure in a later chunk leaves nothing when the caller
// rolls back.
func TestInsertRequeuedTxChunksShareOneTransaction(t *testing.T) {
	s := newTestStore(t)
	db := s.db
	run := seedRun(t, db)
	const total = 2*requeueChunk + 1
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	plan := make([]RequeueItem, 0, total)
	for i := 0; i < total; i++ {
		res, err := tx.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state) VALUES (?, 'UC1', ?, 'not_yet_evaluated')`, fmt.Sprintf("chunk-%05d", i), run)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		plan = append(plan, RequeueItem{id, run})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	n, err := insertRequeued(t, s, plan, time.Now())
	if err != nil || n != total {
		t.Fatalf("inserted %d, err %v; want %d", n, err, total)
	}
	var distinct int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT created_at) FROM deferred_item WHERE deferral_reason='requeued_by_user'`).Scan(&distinct); err != nil || distinct != 1 {
		t.Fatalf("created_at values: %d, err %v; want one shared", distinct, err)
	}

	// A failure in the last chunk rolls the earlier chunks back with it. Ids
	// are sequential, so the row past the second chunk is the one that fails.
	var last int64
	if err := db.QueryRow(`SELECT MAX(id) FROM deferred_item`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM deferred_item`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_last AFTER INSERT ON deferred_item
WHEN NEW.id > %d BEGIN SELECT RAISE(ABORT, 'injected'); END`, last+2*requeueChunk)); err != nil {
		t.Fatal(err)
	}
	if n, err := insertRequeued(t, s, plan, time.Now()); err == nil {
		t.Fatalf("the injected failure was not reported (inserted %d)", n)
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deferred_item`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d items survived a failed write, err %v; want none", left, err)
	}
}

func TestInsertRequeuedTxEmptyPlanAndMissingTransaction(t *testing.T) {
	s := newTestStore(t)
	n, err := insertRequeued(t, s, nil, time.Now())
	if err != nil || n != 0 {
		t.Fatalf("empty plan: inserted %d, err %v", n, err)
	}
	if _, err := s.InsertRequeuedTx(context.Background(), nil, []RequeueItem{{1, 1}}, time.Now()); err == nil {
		t.Fatal("a nil transaction must be refused")
	}
}

// The insert must walk the pairs and look each video up by primary key. With a
// plain JOIN the planner drives from the video table instead and rescans the
// pair list per video: 4,001 pairs took 27 s, and 100,000 would not finish
// (ARS-005 §11.9 P1 asks for phase B within 2 s).
func TestInsertRequeuedSQLWalksThePairsThenLooksUpEachVideo(t *testing.T) {
	db := newTestStore(t).db
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+insertRequeuedSQL, `[[1,1]]`, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var top []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if parent == 0 {
			top = append(top, detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(top, "\n")
	if len(top) == 0 || !strings.HasPrefix(top[0], "SCAN j VIRTUAL TABLE") ||
		!strings.Contains(joined, "SEARCH v USING INTEGER PRIMARY KEY") ||
		strings.Contains(joined, "MULTI-INDEX") || strings.Contains(joined, "SCAN v") {
		t.Fatalf("join order changed; top-level plan: %q", top)
	}
}
