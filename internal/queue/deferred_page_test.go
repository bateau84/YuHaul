package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	const total = 70000
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
	if first.Page.TotalPages != 1400 || first.Page.PrevBeforeID != nil || first.Page.NextAfterID == nil {
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
	if seen != total || pages != 350 {
		t.Fatalf("forward walk saw %d items over %d pages", seen, pages)
	}

	// Backward from the last page by cursor.
	expectPos(t, "last", getDeferredPage(t, server.URL, "page=last"), 1400, 69951, 70000)
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

	expectPos(t, "page=700", getDeferredPage(t, server.URL, "page=700"), 700, 34951, 35000)
	past := getDeferredPage(t, server.URL, "page=1401")
	if len(past.Deferred) != 0 || past.Page.Number != 1401 || past.Page.FirstPosition != nil || past.Page.LastPosition != nil {
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
