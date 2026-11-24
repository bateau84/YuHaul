package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func claimBatch(t *testing.T, s *Store, run int64, items []DeferredItem) (BatchClaim, error) {
	t.Helper()
	var claim BatchClaim
	err := s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		claim, err = s.ClaimDeferredBatchTx(context.Background(), tx, run, items, time.Now())
		return err
	})
	return claim, err
}

func listBatch(t *testing.T, s *Store, run int64, limit int) []DeferredItem {
	t.Helper()
	items, err := s.ListDeferredRetryCandidatesForRun(context.Background(), time.Now(), run, limit)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// B1 (queue level): with 1,200 eligible items a run takes 500, 500 and 200 in
// three claim transactions, in the existing order, and no video twice.
func TestBatchClaimBoundsEachTransactionAndNeverClaimsAVideoTwiceB1(t *testing.T) {
	s := newTestStore(t)
	origin, run := retryRun(t, s.db), retryRun(t, s.db)
	seedDeferredBulk(t, s.db, origin, 1200, 3)

	seen := map[int64]bool{}
	var sizes []int
	for {
		items := listBatch(t, s, run, 500)
		if len(items) == 0 {
			break
		}
		claim, err := claimBatch(t, s, run, items)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(claim.Receipts))
		for _, r := range claim.Receipts {
			if seen[r.VideoID] {
				t.Fatalf("video %d claimed twice", r.VideoID)
			}
			seen[r.VideoID] = true
		}
	}
	if fmt.Sprint(sizes) != "[500 500 200]" {
		t.Fatalf("batch sizes = %v, want [500 500 200]", sizes)
	}
	if n := memberships(t, s.db, run); n != 1200 {
		t.Fatalf("memberships = %d, want 1200", n)
	}
}

// B6 (queue level, AC14): across consecutive runs the claims are exactly the
// created-at order, then id. A claimed and released item keeps its place, and a
// later handoff item comes after every older item. Unclaimed rows are
// untouched between runs.
func TestBatchClaimsFollowCreatedAtOrderAcrossRunsB6(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin := retryRun(t, s.db)
	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	item := func(name string, age time.Duration) (id, video int64) {
		video = seedVideo(t, s.db, origin, name)
		id, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: video, RunID: origin, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: base.Add(age)})
		if err != nil {
			t.Fatal(err)
		}
		return id, video
	}
	var want []int64
	for i := 0; i < 10; i++ { // A..J, created in this order
		id, _ := item(fmt.Sprintf("v%02d", i), time.Duration(i)*time.Minute)
		want = append(want, id)
	}
	// Q: created between items 3 and 4, claimed and released by an earlier run.
	qID, _ := item("q", 3*time.Minute+30*time.Second)
	earlier := retryRun(t, s.db)
	var receipt RetryGroupReceipt
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		receipt, _, _, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, qID, earlier, time.Now())
		if err != nil {
			return err
		}
		return s.RequeueRetryReceiptTx(ctx, tx, receipt)
	}); err != nil {
		t.Fatal(err)
	}
	want = append(want[:4], append([]int64{qID}, want[4:]...)...)
	// H: a stop handoff creates it last.
	hID, _ := item("h", 24*time.Hour)
	want = append(want, hID)

	var got []int64
	for run := 0; len(got) < len(want); run++ {
		r := retryRun(t, s.db)
		items := listBatch(t, s, r, 4) // quota for four per run
		if len(items) == 0 {
			t.Fatalf("run %d found no candidate after %v", run, got)
		}
		claim, err := claimBatch(t, s, r, items)
		if err != nil {
			t.Fatal(err)
		}
		for i, rec := range claim.Receipts {
			got = append(got, rec.Deferred[0].ID)
			if rec.Deferred[0].ID != items[i].ID {
				t.Fatalf("receipt %d = %d, candidate %d", i, rec.Deferred[0].ID, items[i].ID)
			}
		}
		// The run processes what it claimed (acknowledged), as quota allows.
		for _, rec := range claim.Receipts {
			exec(t, s.db, `UPDATE deferred_item SET status='completed' WHERE id=?`, rec.Deferred[0].ID)
		}
		// Every row not yet claimed is untouched: pending and unowned.
		var touched int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE status <> 'completed' AND (status <> 'pending' OR retry_run_id IS NOT NULL)`).Scan(&touched); err != nil {
			t.Fatal(err)
		}
		if touched != 0 {
			t.Fatalf("run %d left %d unclaimed rows touched", run, touched)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("claim order = %v, want %v", got, want)
	}
}

// R2: the claim rechecks the run row under the writer, and a refused batch
// claims nothing.
func TestBatchClaimRechecksTheRunRowUnderTheWriter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     string
		wantErr error // nil: claims
	}{
		{name: "running", set: ``},
		{name: "authorization stop still claims", set: `UPDATE run SET dispatch_stop='authorization' WHERE id=?`},
		{name: "stop requested", set: `UPDATE run SET stop_requested_at='2026-01-01T00:00:00Z' WHERE id=?`, wantErr: ErrRunNotClaiming},
		{name: "interrupted", set: `UPDATE run SET status='interrupted' WHERE id=?`, wantErr: ErrRunNotClaiming},
		{name: "accounting stop", set: `UPDATE run SET dispatch_stop='accounting_or_integrity' WHERE id=?`, wantErr: ErrRunDispatchStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			origin, run := retryRun(t, s.db), retryRun(t, s.db)
			video := seedVideo(t, s.db, origin, "r2")
			id := deferredSibling(t, s, video, origin, DeferredStatusPending)
			before := claimItemRow(t, s.db, id)
			if tc.set != "" {
				exec(t, s.db, tc.set, run)
			}
			items := listBatch(t, s, run, 10)
			claim, err := claimBatch(t, s, run, items)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if got := claimItemRow(t, s.db, id); got != before {
					t.Fatalf("refused batch changed the row: %+v vs %+v", got, before)
				}
				if n := memberships(t, s.db, run); n != 0 {
					t.Fatalf("refused batch left %d memberships", n)
				}
				return
			}
			if err != nil || len(claim.Receipts) != 1 {
				t.Fatalf("claim = %+v, %v; want one receipt", claim, err)
			}
		})
	}
}

// ARS-005 section 15: a final video in a claimed batch is closed, not a member,
// and a video a live owner holds is skipped. Neither stops the open claims.
func TestBatchClaimClosesFinalAndSkipsLostWithoutMembership(t *testing.T) {
	s := newTestStore(t)
	origin, run, other := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	final := seedVideo(t, s.db, origin, "final")
	setFinal(t, s.db, final, "accepted")
	finalItem := deferredSibling(t, s, final, origin, DeferredStatusPending)
	held := seedVideo(t, s.db, origin, "held")
	heldItem := deferredSibling(t, s, held, origin, DeferredStatusPending)
	open := seedVideo(t, s.db, origin, "open")
	openItem := deferredSibling(t, s, open, origin, DeferredStatusPending)

	// The candidate list is not authority: another run takes `held` after it.
	items := listBatch(t, s, run, 10)
	claimDeferred(t, s, heldItem, other, true)

	claim, err := claimBatch(t, s, run, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Receipts) != 1 || claim.Receipts[0].Deferred[0].ID != openItem || claim.ClosedFinal != 1 || claim.Lost != 1 {
		t.Fatalf("claim = %+v, want only the open item, one closed, one lost", claim)
	}
	if got := claimItemRow(t, s.db, finalItem); got.status != "completed" || got.closed != ClosedReasonFinalOutcome {
		t.Fatalf("final item = %+v", got)
	}
	if n := memberships(t, s.db, run); n != 1 {
		t.Fatalf("memberships = %d, want only the open video", n)
	}
}
