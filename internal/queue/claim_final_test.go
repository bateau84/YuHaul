package queue

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
)

// BR-039 claim classification (ARS-006 §3, §4) on real migrated SQLite.

func exec(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	res, err := db.Exec(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func setFinal(t *testing.T, db *sql.DB, video int64, outcome string) {
	t.Helper()
	exec(t, db, `UPDATE video SET final_outcome=?, processing_state='completed' WHERE id=?`, outcome, video)
}

func claimResult(t *testing.T, s *Store, item, run int64) (RetryGroupReceipt, ClaimResult) {
	t.Helper()
	var receipt RetryGroupReceipt
	var result ClaimResult
	err := s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		receipt, _, result, err = s.ClaimDeferredGroupResultTx(context.Background(), tx, item, run, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt, result
}

func memberships(t *testing.T, db *sql.DB, run int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=?`, run).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type claimItem struct {
	status, closed             string
	retryRun                   sql.NullInt64
	retryCount                 int
	createdAt                  string
	videoOutcome, videoProcess string
}

func claimItemRow(t *testing.T, db *sql.DB, id int64) claimItem {
	t.Helper()
	var r claimItem
	var closed sql.NullString
	err := db.QueryRow(`SELECT d.status, d.closed_reason, d.retry_run_id, d.retry_count, d.created_at, v.final_outcome, v.processing_state
		FROM deferred_item d JOIN video v ON v.id=d.video_id WHERE d.id=?`, id).Scan(&r.status, &closed, &r.retryRun, &r.retryCount, &r.createdAt, &r.videoOutcome, &r.videoProcess)
	if err != nil {
		t.Fatal(err)
	}
	r.closed = closed.String
	return r
}

// K1 (queue level): a run's claim closes a final video's deferred family
// instead of reprocessing it, whatever the final outcome.
func TestClaimClosesFinalVideoFamilyK1(t *testing.T) {
	for _, outcome := range []string{"accepted", "rejected", "excluded", "unavailable_on_youtube"} {
		t.Run(outcome, func(t *testing.T) {
			s := newTestStore(t)
			origin, run := retryRun(t, s.db), retryRun(t, s.db)
			video := seedVideo(t, s.db, origin, "final-"+outcome)
			setFinal(t, s.db, video, outcome)
			first := deferredSibling(t, s, video, origin, DeferredStatusPending)
			second := deferredSibling(t, s, video, origin, DeferredStatusPending)
			errID := exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, video, origin)
			before := claimItemRow(t, s.db, first)

			receipt, result := claimResult(t, s, first, run)
			if result != ClaimClosedFinal {
				t.Fatalf("result = %v, want ClaimClosedFinal", result)
			}
			if len(receipt.Deferred)+len(receipt.Errors) != 0 {
				t.Fatalf("closed claim returned a receipt: %#v", receipt)
			}
			for _, id := range []int64{first, second} {
				got := claimItemRow(t, s.db, id)
				if got.status != "completed" || got.closed != ClosedReasonFinalOutcome {
					t.Fatalf("item %d = %s/%s, want completed/final_outcome", id, got.status, got.closed)
				}
				if got.retryCount != before.retryCount || got.createdAt != before.createdAt || got.retryRun.Valid {
					t.Fatalf("item %d history changed: %+v vs %+v", id, got, before)
				}
				if got.videoOutcome != outcome || got.videoProcess != "completed" {
					t.Fatalf("video changed: %s/%s", got.videoOutcome, got.videoProcess)
				}
			}
			if n := memberships(t, s.db, run); n != 0 {
				t.Fatalf("run has %d membership rows for a final video", n)
			}
			var status string
			if err := s.db.QueryRow(`SELECT status FROM error_entry WHERE id=?`, errID).Scan(&status); err != nil || status != "active" {
				t.Fatalf("scheduled claim touched the Errors entry: %q, %v", status, err)
			}
		})
	}
}

// K1: the run still claims the other, open videos.
func TestClaimOpenVideoUnchangedAlongsideFinalK1(t *testing.T) {
	s := newTestStore(t)
	origin, run := retryRun(t, s.db), retryRun(t, s.db)
	final := seedVideo(t, s.db, origin, "final")
	setFinal(t, s.db, final, "accepted")
	open := seedVideo(t, s.db, origin, "open")
	finalItem := deferredSibling(t, s, final, origin, DeferredStatusPending)
	openItem := deferredSibling(t, s, open, origin, DeferredStatusPending)

	if _, r := claimResult(t, s, finalItem, run); r != ClaimClosedFinal {
		t.Fatalf("final result = %v", r)
	}
	receipt, r := claimResult(t, s, openItem, run)
	if r != ClaimTaken || len(receipt.Deferred) != 1 {
		t.Fatalf("open result = %v, receipt %#v", r, receipt)
	}
	got := claimItemRow(t, s.db, openItem)
	if got.status != "retrying" || got.videoOutcome != "not_yet_evaluated" || got.videoProcess != "not_yet_evaluated" {
		t.Fatalf("open claim not as today: %+v", got)
	}
	if n := memberships(t, s.db, run); n != 1 {
		t.Fatalf("memberships = %d, want 1 (the open video only)", n)
	}
}

// A manual claim of a final video closes the Errors family too.
func TestManualClaimClosesErrorsOfFinalVideo(t *testing.T) {
	s := newTestStore(t)
	origin, run := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "manual-final")
	setFinal(t, s.db, video, "rejected")
	errID := exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, video, origin)
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)

	var claimed bool
	err := s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		_, _, claimed, err = s.ClaimErrorGroupReceiptTx(context.Background(), tx, errID, run, time.Now())
		return err
	})
	if err != nil || claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	var status, closed string
	var resolved sql.NullString
	if err := s.db.QueryRow(`SELECT status, closed_reason, resolved_at FROM error_entry WHERE id=?`, errID).Scan(&status, &closed, &resolved); err != nil {
		t.Fatal(err)
	}
	if status != "resolved" || closed != ClosedReasonFinalOutcome || !resolved.Valid {
		t.Fatalf("error entry = %s/%s/%v", status, closed, resolved)
	}
	if got := claimItemRow(t, s.db, item); got.status != "completed" || got.closed != ClosedReasonFinalOutcome {
		t.Fatalf("deferred sibling = %+v", got)
	}
}

// A partly done video's claim does not reset it for routing: it becomes
// evaluated with outcome not_yet_evaluated, keeping its decisions and success.
func TestClaimPartlyDoneVideoIsNotResetForRouting(t *testing.T) {
	s := newTestStore(t)
	origin, run := retryRun(t, s.db), retryRun(t, s.db)
	route := exec(t, s.db, `INSERT INTO route(name,evaluation_order,condition_composition) VALUES('r',1,'AND')`)
	video := seedVideo(t, s.db, origin, "partly")
	exec(t, s.db, `UPDATE video SET processing_state='acted', final_outcome='deferred', completed_at=CURRENT_TIMESTAMP WHERE id=?`, video)
	decision := exec(t, s.db, `INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(?,?,?,1,'matched')`, video, origin, route)
	exec(t, s.db, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(?,?,?,'playlist_insert','PL1','succeeded')`, decision, origin, video)
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)

	if _, r := claimResult(t, s, item, run); r != ClaimTaken {
		t.Fatalf("result = %v, want ClaimTaken", r)
	}
	var process, outcome string
	var completed sql.NullString
	if err := s.db.QueryRow(`SELECT processing_state, final_outcome, completed_at FROM video WHERE id=?`, video).Scan(&process, &outcome, &completed); err != nil {
		t.Fatal(err)
	}
	if process != "evaluated" || outcome != "not_yet_evaluated" {
		t.Fatalf("partly done reset to %s/%s, want evaluated/not_yet_evaluated", process, outcome)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM executed_action WHERE video_id=? AND outcome='succeeded'`, video).Scan(&n); err != nil || n != 1 {
		t.Fatalf("success rows = %d, %v", n, err)
	}
}

// K8a (M2): the run that itself made the video final is acknowledged on
// re-claim: same receipt, no reset, no closing, outcome unchanged.
func TestClaimAcknowledgesRunsOwnFinalOutcomeK8a(t *testing.T) {
	s := newTestStore(t)
	origin, run, other := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "own-final")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	first, r := claimResult(t, s, item, run)
	if r != ClaimTaken {
		t.Fatalf("initial claim = %v", r)
	}
	// The run commits accepted, then is killed before acknowledging.
	setFinal(t, s.db, video, "accepted")
	exec(t, s.db, `UPDATE run_video_outcome SET final_outcome='accepted' WHERE run_id=? AND video_id=?`, run, video)
	before := claimItemRow(t, s.db, item)

	again, r := claimResult(t, s, item, run)
	if r != ClaimTaken {
		t.Fatalf("own-final claim = %v, want ClaimTaken", r)
	}
	if len(again.Deferred) != len(first.Deferred) || again.Deferred[0] != first.Deferred[0] || again.ReceivingRunID != run {
		t.Fatalf("receipt = %#v, want %#v", again, first)
	}
	after := claimItemRow(t, s.db, item)
	if after != before || after.status != "retrying" || after.closed != "" {
		t.Fatalf("own-final claim changed the item: %+v -> %+v", before, after)
	}
	if after.videoOutcome != "accepted" || after.videoProcess != "completed" {
		t.Fatalf("outcome changed: %+v", after)
	}
	if n := memberships(t, s.db, run); n != 1 {
		t.Fatalf("memberships = %d, want 1", n)
	}
	// Another run never takes a live owner's final video.
	if _, r := claimResult(t, s, item, other); r != ClaimLost {
		t.Fatalf("other run result = %v, want ClaimLost", r)
	}
}

// A final video held by a run whose own outcome differs is closed-final for
// that run; rows still held by the live run stay as they are (the main-loop
// dispatch of ARS-006 §5 acknowledges or closes them).
func TestClaimOwnerWithDifferentOutcomeIsClosedFinal(t *testing.T) {
	s := newTestStore(t)
	origin, run := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "other-final")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	if _, r := claimResult(t, s, item, run); r != ClaimTaken {
		t.Fatal("setup claim")
	}
	setFinal(t, s.db, video, "rejected") // another run finalized it
	if _, r := claimResult(t, s, item, run); r != ClaimClosedFinal {
		t.Fatalf("result = %v, want ClaimClosedFinal", r)
	}
}

// K8: a run finalizes X and acknowledges its claim while a user retry of X's
// Errors entry and a Run Now claim arrive. Every result is one serial order.
func TestFinalizeRaceK8(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "k8.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		origin, scheduled, retryRunID, runNow := retryRun(t, db), retryRun(t, db), retryRun(t, db), retryRun(t, db)
		video := seedVideo(t, db, origin, "k8-"+itoa(int64(i)))
		item := deferredSibling(t, s, video, origin, DeferredStatusPending)
		errID := exec(t, db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, video, origin)
		if _, r := claimResult(t, s, item, scheduled); r != ClaimTaken {
			t.Fatalf("iter %d: scheduled claim = %v", i, r)
		}

		start := make(chan struct{})
		type retryResult struct {
			final *AlreadyFinalError
			lost  bool
			err   error
		}
		aDone := make(chan error, 1)
		bDone := make(chan retryResult, 1)
		cDone := make(chan ClaimResult, 1)
		cErr := make(chan error, 1)
		go func() { // the scheduled run commits rejected and acknowledges
			<-start
			aDone <- s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				if _, err := tx.Exec(`UPDATE video SET final_outcome='rejected', processing_state='completed' WHERE id=?`, video); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE run_video_outcome SET final_outcome='rejected' WHERE run_id=? AND video_id=?`, scheduled, video); err != nil {
					return err
				}
				return s.AcknowledgeRetryGroupTx(ctx, tx, video, scheduled)
			})
		}()
		go func() { // the user's retry of the Errors entry
			<-start
			var r retryResult
			r.err = s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var err error
				r.final, r.lost, err = s.RefuseFinalRetryTx(ctx, tx, errID, true, time.Now())
				if err != nil || r.final != nil || r.lost {
					return err
				}
				_, _, claimed, err := s.ClaimErrorGroupReceiptTx(ctx, tx, errID, retryRunID, time.Now())
				if err == nil && claimed {
					t.Errorf("iter %d: user retry claimed a video its live owner was finalizing", i)
				}
				return err
			})
			bDone <- r
		}()
		go func() { // Run Now
			<-start
			var res ClaimResult
			err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var err error
				_, _, res, err = s.ClaimDeferredGroupResultTx(ctx, tx, item, runNow, time.Now())
				return err
			})
			cErr <- err
			cDone <- res
		}()
		close(start)
		if err := <-aDone; err != nil {
			t.Fatalf("iter %d: finalizing run: %v", i, err)
		}
		b := <-bDone
		if b.err != nil {
			t.Fatalf("iter %d: user retry: %v", i, b.err)
		}
		if err := <-cErr; err != nil {
			t.Fatalf("iter %d: run now: %v", i, err)
		}
		c := <-cDone

		var outcome string
		if err := db.QueryRow(`SELECT final_outcome FROM video WHERE id=?`, video).Scan(&outcome); err != nil || outcome != "rejected" {
			t.Fatalf("iter %d: outcome = %q, %v", i, outcome, err)
		}
		// Serial order: either the run's acknowledgement came first (the retry
		// sees a final video; Run Now finds nothing to claim) or the live owner
		// made both of them lose. Neither ever owns the video.
		if c == ClaimTaken {
			t.Fatalf("iter %d: Run Now took a finalized video", i)
		}
		for _, r := range []int64{retryRunID, runNow} {
			if n := memberships(t, db, r); n != 0 {
				t.Fatalf("iter %d: run %d has %d memberships", i, r, n)
			}
		}
		if b.final != nil && (b.final.Outcome != "rejected" || b.final.EntryID != errID) {
			t.Fatalf("iter %d: refusal = %+v", i, b.final)
		}
		var open int
		if err := db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE video_id=? AND status IN ('pending','retrying')`, video).Scan(&open); err != nil || open != 0 {
			t.Fatalf("iter %d: %d open deferred items after finalize, %v", i, open, err)
		}
	}
}

// K2 (queue level): the refusal looks the entry up whatever its status, closes
// the family, and a second refusal is the same answer with nothing changed.
func TestRefuseFinalRetryIsRepeatableK2(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin := retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "k2")
	setFinal(t, s.db, video, "accepted")
	errID := exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, video, origin)
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)

	refuse := func(id int64, errorQueue bool) *AlreadyFinalError {
		t.Helper()
		var final *AlreadyFinalError
		err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
			var lost bool
			var err error
			final, lost, err = s.RefuseFinalRetryTx(ctx, tx, id, errorQueue, time.Now())
			if lost {
				t.Fatal("unexpected lost")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return final
	}
	for round := range 2 {
		for _, c := range []struct {
			id         int64
			errorQueue bool
		}{{errID, true}, {item, false}} {
			f := refuse(c.id, c.errorQueue)
			if f == nil || f.Outcome != "accepted" || f.EntryID != c.id || f.ErrorQueue != c.errorQueue || !errors.Is(f, ErrVideoAlreadyFinal) {
				t.Fatalf("round %d entry %d: %+v", round, c.id, f)
			}
		}
	}
	var status, closed string
	if err := s.db.QueryRow(`SELECT status, closed_reason FROM error_entry WHERE id=?`, errID).Scan(&status, &closed); err != nil || status != "resolved" || closed != ClosedReasonFinalOutcome {
		t.Fatalf("error entry = %s/%s, %v", status, closed, err)
	}
	if got := claimItemRow(t, s.db, item); got.status != "completed" || got.closed != ClosedReasonFinalOutcome {
		t.Fatalf("deferred = %+v", got)
	}
	var runs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("runs = %d (only the seeded origin), %v", runs, err)
	}
	// A missing entry is ErrNotFound; an open video is not refused.
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, _, err := s.RefuseFinalRetryTx(ctx, tx, 9999, true, time.Now())
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing entry err = %v", err)
	}
	openVideo := seedVideo(t, s.db, origin, "k2-open")
	openItem := deferredSibling(t, s, openVideo, origin, DeferredStatusPending)
	if f := refuse(openItem, false); f != nil {
		t.Fatalf("open video refused: %+v", f)
	}
}
