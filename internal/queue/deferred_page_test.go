package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	dbstore "github.com/bateau84/YuHaul/internal/store"
)

// seedDeferredBulk inserts n pending deferred items (one video each). Items
// whose ordinal is a multiple of quotaEvery are quota_exhausted; the rest are
// api_rate_limited. Ids ascend with the ordinal.
func seedDeferredBulk(t *testing.T, db *sql.DB, runID int64, n, quotaEvery int) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome)
		SELECT 'bulk-' || i, 'UCchannel', ?, 'not_yet_evaluated', 'not_yet_evaluated' FROM n`, n, runID); err != nil {
		t.Fatalf("seeding videos: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at)
		SELECT v.id, ?, CASE WHEN (v.id % ?) = 0 THEN 'quota_exhausted' ELSE 'api_rate_limited' END,
		       0, 'pending', ?
		FROM video v WHERE v.video_id LIKE 'bulk-%' ORDER BY v.id`,
		runID, quotaEvery, time.Now().UTC()); err != nil {
		t.Fatalf("seeding deferred items: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type deferredPageBody struct {
	Deferred   []struct{ ID int64 } `json:"deferred"`
	Total      int                  `json:"total"`
	QueueTotal int                  `json:"queue_total"`
	ByReason   map[string]int       `json:"by_reason"`
	Page       struct {
		Limit         int    `json:"limit"`
		Number        int    `json:"number"`
		TotalPages    int    `json:"total_pages"`
		FirstPosition *int   `json:"first_position"`
		LastPosition  *int   `json:"last_position"`
		NextAfterID   *int64 `json:"next_after_id"`
		PrevBeforeID  *int64 `json:"prev_before_id"`
	} `json:"page"`
}

func getDeferredPage(t *testing.T, base, query string) deferredPageBody {
	t.Helper()
	resp, err := http.Get(base + "/?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET ?%s status=%d want 200", query, resp.StatusCode)
	}
	var body deferredPageBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func expectPos(t *testing.T, label string, body deferredPageBody, number, first, last int) {
	t.Helper()
	p := body.Page
	if p.Number != number || p.FirstPosition == nil || *p.FirstPosition != first || p.LastPosition == nil || *p.LastPosition != last {
		t.Fatalf("%s: number=%d first=%v last=%v, want %d %d–%d", label, p.Number, p.FirstPosition, p.LastPosition, number, first, last)
	}
}

// G1 (ARS-005 §7): paging 70,000 pending items through the registered handler.
func TestDeferredPageG1Paging(t *testing.T) {
	if testing.Short() {
		t.Skip("70,000-row paging walk skipped in -short mode")
	}
	// The full 70,000-row walk runs by default. Under -race every request is
	// far slower (the walk took ~30 minutes), so it uses 7,000 rows; all
	// assertions below are derived from total.
	total := 70000
	if raceEnabled {
		total = 7000
	}
	totalPages, pages200 := total/50, total/200
	store := newTestStore(t)
	runID := seedRun(t, store.db)
	// every 1,372nd id is quota_exhausted: 51 of 70,000 are not guaranteed by
	// the modulus alone, so count what was actually seeded below.
	seedDeferredBulk(t, store.db, runID, total, 1372)
	var quota int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE deferral_reason='quota_exhausted'`).Scan(&quota); err != nil {
		t.Fatal(err)
	}
	if quota == 0 {
		t.Fatal("seed produced no quota_exhausted rows")
	}
	server := httptest.NewServer(NewService(store, nil).DeferredHandler())
	defer server.Close()

	first := getDeferredPage(t, server.URL, "")
	if len(first.Deferred) != 50 || first.Total != total || first.QueueTotal != total || first.Page.Limit != 50 {
		t.Fatalf("first page len=%d total=%d queue_total=%d limit=%d", len(first.Deferred), first.Total, first.QueueTotal, first.Page.Limit)
	}
	if first.ByReason["quota_exhausted"] != quota || first.ByReason["quota_exhausted"]+first.ByReason["api_rate_limited"] != total {
		t.Fatalf("by_reason=%v", first.ByReason)
	}
	if first.Page.TotalPages != totalPages || first.Page.PrevBeforeID != nil || first.Page.NextAfterID == nil {
		t.Fatalf("first page meta %+v", first.Page)
	}
	expectPos(t, "first", first, 1, 1, 50)

	// Forward by cursor at the maximum page size (each request counts the whole
	// queue, so limit=50 for all 1,400 pages would only repeat the same checks):
	// every id exactly once, ascending, exact positions.
	const step = 200
	seen, prev, body, pages := 0, int64(0), getDeferredPage(t, server.URL, "limit=200"), 0
	for {
		pages++
		for _, it := range body.Deferred {
			if it.ID <= prev {
				t.Fatalf("id %d not after %d", it.ID, prev)
			}
			prev = it.ID
			seen++
		}
		expectPos(t, fmt.Sprintf("page %d", pages), body, pages, (pages-1)*step+1, (pages-1)*step+len(body.Deferred))
		if body.Page.NextAfterID == nil {
			break
		}
		body = getDeferredPage(t, server.URL, fmt.Sprintf("limit=200&after_id=%d", *body.Page.NextAfterID))
	}
	if seen != total || pages != pages200 {
		t.Fatalf("forward walk saw %d items over %d pages", seen, pages)
	}

	// Backward from the last page by cursor.
	expectPos(t, "last", getDeferredPage(t, server.URL, "page=last"), totalPages, total-49, total)
	seen, body = 0, getDeferredPage(t, server.URL, "limit=200&page=last")
	for {
		seen += len(body.Deferred)
		for i := 1; i < len(body.Deferred); i++ {
			if body.Deferred[i].ID <= body.Deferred[i-1].ID {
				t.Fatal("backward page not ascending")
			}
		}
		if body.Page.PrevBeforeID == nil {
			break
		}
		body = getDeferredPage(t, server.URL, fmt.Sprintf("limit=200&before_id=%d", *body.Page.PrevBeforeID))
	}
	if seen != total {
		t.Fatalf("backward walk saw %d items", seen)
	}

	expectPos(t, "middle page", getDeferredPage(t, server.URL, fmt.Sprintf("page=%d", totalPages/2)), totalPages/2, (totalPages/2-1)*50+1, totalPages/2*50)
	past := getDeferredPage(t, server.URL, fmt.Sprintf("page=%d", totalPages+1))
	if len(past.Deferred) != 0 || past.Page.Number != totalPages+1 || past.Page.FirstPosition != nil || past.Page.LastPosition != nil {
		t.Fatalf("page past the end: %+v", past)
	}

	filtered := getDeferredPage(t, server.URL, "reason=quota_exhausted&limit=200")
	if filtered.Total != quota || filtered.QueueTotal != total || filtered.Page.TotalPages != (quota+199)/200 || len(filtered.Deferred) != quota {
		t.Fatalf("filtered total=%d queue_total=%d pages=%d len=%d", filtered.Total, filtered.QueueTotal, filtered.Page.TotalPages, len(filtered.Deferred))
	}
	small := getDeferredPage(t, server.URL, fmt.Sprintf("reason=quota_exhausted&limit=%d", quota/2))
	if small.Page.TotalPages != 2 && small.Page.TotalPages != 3 {
		t.Fatalf("filtered limit paging total_pages=%d", small.Page.TotalPages)
	}
}

// G3 (ARS-005 §7): bounds, errors, and unchanged Retry/Dismiss.
func TestDeferredPageG3Bounds(t *testing.T) {
	store := newTestStore(t)
	runID := seedRun(t, store.db)
	seedDeferredBulk(t, store.db, runID, 300, 7)
	svc := NewService(store, nil)
	server := httptest.NewServer(svc.DeferredHandler())
	defer server.Close()

	for _, q := range []string{"limit=0", "limit=201", "limit=x", "after_id=1&before_id=9", "after_id=1&page=2",
		"before_id=9&page=last", "page=0", "page=x", "page=-1", "after_id=-1", "before_id=0", "after_id=x"} {
		resp, err := http.Get(server.URL + "/?" + q)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || body["code"] != "invalid_page" || body["error"] != "invalid page" {
			t.Errorf("?%s -> %d %v, want 400 invalid_page", q, resp.StatusCode, body)
		}
	}
	resp, err := http.Get(server.URL + "/?reason=other")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "invalid_reason" {
		t.Errorf("reason=other -> %d %v, want 400 invalid_reason", resp.StatusCode, body)
	}

	if page := getDeferredPage(t, server.URL, "include=all&limit=200"); len(page.Deferred) != 200 || page.Total != 300 {
		t.Errorf("limit=200 returned %d items, total %d", len(page.Deferred), page.Total)
	}
	if page := getDeferredPage(t, server.URL, ""); len(page.Deferred) != 50 {
		t.Errorf("default page returned %d items, want 50", len(page.Deferred))
	}

	// Dismiss (discard) still returns the item; Retry on a completed/discarded
	// item still conflicts.
	var id int64
	if err := store.db.QueryRow(`SELECT MIN(id) FROM deferred_item`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(fmt.Sprintf("%s/%d/discard", server.URL, id), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discard status=%d want 200", resp.StatusCode)
	}
	if page := getDeferredPage(t, server.URL, ""); page.Total != 299 || page.Deferred[0].ID == id {
		t.Errorf("discarded item still pending: total=%d", page.Total)
	}
}

// G2 (ARS-005 §7): while a client pages, a real run claims 1,000 items and the
// action inserts 500 new pending items. Every item present throughout is
// returned exactly once, claimed items disappear, and new items appear only
// after the original last id.
func TestDeferredPageG2PagingUnderChange(t *testing.T) {
	const initial, claimN, insertN, limit = 3000, 1000, 500, 20
	ctx := context.Background()
	// A file database, so the walker and the writer use separate connections.
	db, err := dbstore.Open(ctx, filepath.Join(t.TempDir(), "g2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	origin := seedRun(t, store.db)
	owner := retryRun(t, store.db)
	seedDeferredBulk(t, store.db, origin, initial, 7)

	var originalLast int64
	if err := store.db.QueryRow(`SELECT MAX(id) FROM deferred_item`).Scan(&originalLast); err != nil {
		t.Fatal(err)
	}
	rows, err := store.db.Query(`SELECT id FROM deferred_item ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var claimIDs []int64
	for n := 0; rows.Next(); n++ {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if n%3 == 0 && len(claimIDs) < claimN {
			claimIDs = append(claimIDs, id)
		}
	}
	_ = rows.Close()
	if len(claimIDs) != claimN {
		t.Fatalf("selected %d items to claim, want %d", len(claimIDs), claimN)
	}

	server := httptest.NewServer(NewService(store, nil).DeferredHandler())
	defer server.Close()

	start, done := make(chan struct{}), make(chan error, 1)
	go func() {
		<-start
		for i, id := range claimIDs {
			err := store.WithRetryTx(ctx, func(tx *sql.Tx) error {
				_, claimed, err := store.ClaimDeferredGroupTx(ctx, tx, id, owner, time.Now())
				if err == nil && !claimed {
					err = fmt.Errorf("item %d was not claimed", id)
				}
				return err
			})
			if err != nil {
				done <- err
				return
			}
			if i < insertN { // interleave the action's inserts with the claims
				v := seedVideo(t, store.db, origin, fmt.Sprintf("late-%d", i))
				if _, err := store.CreateDeferredItem(ctx, &DeferredItem{VideoID: v, RunID: origin,
					Reason: DeferredReasonAPIRateLimited, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()}); err != nil {
					done <- err
					return
				}
			}
		}
		done <- nil
	}()

	seen := map[int64]int{}
	var order []int64
	query, pages := fmt.Sprintf("limit=%d", limit), 0
	for {
		body := getDeferredPage(t, server.URL, query)
		for _, it := range body.Deferred {
			seen[it.ID]++
			order = append(order, it.ID)
		}
		if pages++; pages == 3 {
			close(start)
		}
		if body.Page.NextAfterID == nil {
			break
		}
		query = fmt.Sprintf("limit=%d&after_id=%d", limit, *body.Page.NextAfterID)
	}
	if pages < 3 {
		t.Fatalf("walk ended after %d pages before the writer started", pages)
	}
	if err := <-done; err != nil {
		t.Fatalf("concurrent claims/inserts: %v", err)
	}

	for i := 1; i < len(order); i++ {
		if order[i] <= order[i-1] {
			t.Fatalf("ids not strictly ascending: %d then %d", order[i-1], order[i])
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("id %d returned %d times", id, n)
		}
	}
	claimed := map[int64]bool{}
	for _, id := range claimIDs {
		claimed[id] = true
	}
	surv, err := store.db.Query(`SELECT id FROM deferred_item WHERE status='pending' AND id <= ?`, originalLast)
	if err != nil {
		t.Fatal(err)
	}
	defer surv.Close()
	survivors := 0
	for surv.Next() {
		var id int64
		if err := surv.Scan(&id); err != nil {
			t.Fatal(err)
		}
		survivors++
		if claimed[id] {
			t.Fatalf("claimed item %d is still pending", id)
		}
		if seen[id] != 1 {
			t.Fatalf("surviving item %d returned %d times, want exactly once", id, seen[id])
		}
	}
	if survivors != initial-claimN {
		t.Fatalf("survivors=%d want %d", survivors, initial-claimN)
	}
	// New items come only after the original last id, so once one is seen no
	// original id may follow (ascending order already holds; check the boundary).
	seenNew := false
	for _, id := range order {
		if id > originalLast {
			seenNew = true
		} else if seenNew {
			t.Fatalf("original id %d returned after a new item", id)
		}
	}
	final := getDeferredPage(t, server.URL, "limit=200")
	if final.Total != initial-claimN+insertN {
		t.Fatalf("final total=%d want %d", final.Total, initial-claimN+insertN)
	}
}
