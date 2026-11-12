package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

func retryRun(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	run := seedRun(t, db)
	res, err := db.Exec(`INSERT INTO config_version (snapshot) VALUES ('{}')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE run SET config_version_id = ? WHERE id = ?`, id, run); err != nil {
		t.Fatal(err)
	}
	return run
}

func retryRequireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func deferredSibling(t *testing.T, s *Store, video, origin int64, status DeferredStatus) int64 {
	t.Helper()
	id, err := s.CreateDeferredItem(context.Background(), &DeferredItem{VideoID: video, RunID: origin, Reason: DeferredReasonQuotaExhausted, Status: status, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func claimDeferred(t *testing.T, s *Store, item, run int64, want bool) {
	t.Helper()
	err := s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		_, claimed, err := s.ClaimDeferredGroupTx(context.Background(), tx, item, run, time.Now())
		if err == nil && claimed != want {
			t.Errorf("claimed = %v, want %v", claimed, want)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClaimDeferredGroupReturnsExactReceipt(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	originA, originB, receiving := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, originA, "receipt")
	first := deferredSibling(t, s, video, originA, DeferredStatusPending)
	second := deferredSibling(t, s, video, originB, DeferredStatusPending)
	var got RetryGroupReceipt
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		var err error
		got, _, claimed, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, first, receiving, time.Now())
		if err == nil && !claimed {
			t.Fatal("claim not admitted")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := RetryGroupReceipt{ReceivingRunID: receiving, VideoID: video, Deferred: []RetryRowRef{{ID: first, OriginRunID: originA}, {ID: second, OriginRunID: originB}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("receipt = %#v, want %#v", got, want)
	}
}

func TestValidateRetryReceiptTracksRemovedSiblingsAndSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "receipt.db")
	db, err := sql.Open("sqlite", path+"?_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(db); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	origin, owner := retryRun(t, db), retryRun(t, db)
	video := seedVideo(t, db, origin, "receipt-reopen")
	discarded := deferredSibling(t, s, video, origin, DeferredStatusPending)
	var errorID int64
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		errorID, err = s.RetainRuntimeErrorTx(ctx, tx, video, owner, "retry", "test")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var receipt RetryGroupReceipt
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		var err error
		receipt, _, claimed, err = s.ClaimErrorGroupReceiptTx(ctx, tx, errorID, owner, time.Now())
		if err == nil && !claimed {
			t.Error("claim was not admitted")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE deferred_item SET status='discarded' WHERE id=?`, discarded); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path+"?_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err = NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	var got RetryReceiptValidation
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = s.ValidateRetryReceiptTx(ctx, tx, receipt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(got.Deferred) != 0 || len(got.Errors) != 1 || got.Errors[0].ID != errorID || len(got.Removed) != 1 || got.Removed[0].ID != discarded {
		t.Fatalf("validation = %+v", got)
	}
	// Dismissing the last live receipt member is explicit removal, not permission
	// to create replacement work.
	if _, err := db.ExecContext(ctx, `UPDATE error_entry SET status='dismissed' WHERE id=?`, errorID); err != nil {
		t.Fatal(err)
	}
	err = s.WithRetryTx(ctx, func(tx *sql.Tx) error { _, err := s.ValidateRetryReceiptTx(ctx, tx, receipt); return err })
	if !errors.Is(err, ErrWorkRemoved) {
		t.Fatalf("all removed validation = %v, want ErrWorkRemoved", err)
	}
}

func TestValidateRetryReceiptRejectsWrongOwnership(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner, other := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "receipt-owner")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	var receipt RetryGroupReceipt
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		var err error
		receipt, _, claimed, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, item, owner, time.Now())
		if err == nil && !claimed {
			t.Error("claim was not admitted")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE deferred_item SET retry_run_id=? WHERE id=?`, other, item); err != nil {
		t.Fatal(err)
	}
	var got RetryReceiptValidation
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = s.ValidateRetryReceiptTx(ctx, tx, receipt)
		return err
	})
	if !errors.Is(err, ErrWorkNotOwned) || len(got.NotOwned) != 1 || got.NotOwned[0].ID != item {
		t.Fatalf("wrong owner validation = %+v, %v", got, err)
	}
}

func TestFinishRetryFailureTxCreatesSinkAndResolvesOnlyLiveReceiptMembers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "finish-retry-failure")
	removed := deferredSibling(t, s, video, origin, DeferredStatusPending)
	live := deferredSibling(t, s, video, origin, DeferredStatusPending)
	var receipt RetryGroupReceipt
	retryRequireNoError(t, s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		var err error
		receipt, _, claimed, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, live, owner, time.Now())
		if err == nil && !claimed {
			return errors.New("claim not admitted")
		}
		return err
	}))
	if _, err := s.db.ExecContext(ctx, `UPDATE deferred_item SET status='discarded' WHERE id=?`, removed); err != nil {
		t.Fatal(err)
	}
	var sink int64
	retryRequireNoError(t, s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		sink, err = s.FinishRetryFailureTx(ctx, tx, receipt, RetryFailureFacts{Message: "provider failed", Detail: "bounded detail"})
		return err
	}))
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE id=? AND video_id=? AND run_id=? AND error_category='runtime' AND status='active'`, sink, video, owner).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("receiving-run sink count = %d", count)
	}
	var removedStatus, liveStatus string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM deferred_item WHERE id=?`, removed).Scan(&removedStatus); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM deferred_item WHERE id=?`, live).Scan(&liveStatus); err != nil {
		t.Fatal(err)
	}
	if removedStatus != "discarded" || liveStatus != "completed" {
		t.Fatalf("source states removed=%q live=%q", removedStatus, liveStatus)
	}
}

func TestFinishRetryFailureTxAllRemovedDoesNotCreateSink(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "all-retry-removed")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	var receipt RetryGroupReceipt
	retryRequireNoError(t, s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		var err error
		receipt, _, claimed, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, item, owner, time.Now())
		if err == nil && !claimed {
			return errors.New("claim not admitted")
		}
		return err
	}))
	if _, err := s.db.ExecContext(ctx, `UPDATE deferred_item SET status='discarded' WHERE id=?`, item); err != nil {
		t.Fatal(err)
	}
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, err := s.FinishRetryFailureTx(ctx, tx, receipt, RetryFailureFacts{Message: "failure"})
		return err
	})
	if !errors.Is(err, ErrWorkRemoved) {
		t.Fatalf("FinishRetryFailureTx error = %v, want ErrWorkRemoved", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND run_id=? AND error_category='runtime'`, video, owner).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("created %d runtime sinks for removed work", count)
	}
}

func TestFinishRetryFailureTxRejectsCrossQueueOwnerConflict(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner, other := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "cross-queue-owner")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	var receipt RetryGroupReceipt
	retryRequireNoError(t, s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		var err error
		receipt, _, claimed, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, item, owner, time.Now())
		if err == nil && !claimed {
			return errors.New("claim not admitted")
		}
		return err
	}))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO error_entry (video_id,run_id,error_category,error_message,status,created_at,retry_run_id) VALUES (?,?,'runtime','other owner','retrying',?,?)`, video, origin, time.Now().UTC().Format(time.RFC3339), other); err != nil {
		t.Fatal(err)
	}
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, err := s.FinishRetryFailureTx(ctx, tx, receipt, RetryFailureFacts{Message: "failure"})
		return err
	})
	if !errors.Is(err, ErrRetryOwnership) {
		t.Fatalf("FinishRetryFailureTx error = %v, want ErrRetryOwnership", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND run_id=? AND error_category='runtime' AND status='active'`, video, owner).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("created sink despite cross-queue conflict: %d", count)
	}
}

func TestFinishRetryFailureTxCallerRollbackUndoesSinkAndSourceResolution(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "rollback-retry-failure")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	var receipt RetryGroupReceipt
	retryRequireNoError(t, s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var claimed bool
		var err error
		receipt, _, claimed, err = s.ClaimDeferredGroupReceiptTx(ctx, tx, item, owner, time.Now())
		if err == nil && !claimed {
			return errors.New("claim not admitted")
		}
		return err
	}))
	_, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_retry_resolution BEFORE UPDATE OF status ON deferred_item WHEN OLD.id=`+fmt.Sprint(item)+` AND NEW.status='completed' BEGIN SELECT RAISE(ABORT,'injected resolution failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, err := s.FinishRetryFailureTx(ctx, tx, receipt, RetryFailureFacts{Message: "failure"})
		return err
	})
	if err == nil {
		t.Fatal("expected injected resolution failure")
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM deferred_item WHERE id=?`, item).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "retrying" {
		t.Fatalf("source status after rollback = %q", status)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE video_id=? AND run_id=? AND error_category='runtime' AND status='active'`, video, owner).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("runtime sink survived rollback: %d", count)
	}
}

func TestRetryGroupClaimRecoveryRequeueAndAcknowledge(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner, contender := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "siblings")
	a := deferredSibling(t, s, video, origin, DeferredStatusPending)
	b := deferredSibling(t, s, video, contender, DeferredStatusRetrying) // legacy unowned
	completed := deferredSibling(t, s, video, origin, DeferredStatusCompleted)
	discarded := deferredSibling(t, s, video, origin, DeferredStatusDiscarded)
	if _, err := s.db.Exec(`UPDATE video SET processing_state = 'acted', final_outcome = 'deferred', deferred_reason = 'old', error_message = 'old', completed_at = CURRENT_TIMESTAMP WHERE id = ?`, video); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ListDeferredRetryCandidates(ctx, time.Now())
	if err != nil || len(candidates) != 1 || candidates[0].ID != a {
		t.Fatalf("candidates = %v, %v", candidates, err)
	}
	claimDeferred(t, s, a, owner, true)
	var admittedOutcome sql.NullString
	if err := s.db.QueryRow(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, owner, video).Scan(&admittedOutcome); err != nil || admittedOutcome.Valid {
		t.Fatalf("receiving-run admission outcome = %+v, %v; want SQL NULL", admittedOutcome, err)
	}
	for _, id := range []int64{a, b} {
		item, err := s.GetDeferredItem(ctx, id)
		if err != nil || item.RetryRunID == nil || *item.RetryRunID != owner || item.Status != DeferredStatusRetrying || item.RetryCount != 1 {
			t.Fatalf("claimed sibling: %+v, %v", item, err)
		}
	}
	var work, outcome string
	var markers int
	var discovery int64
	if err := s.db.QueryRow(`SELECT processing_state, final_outcome, discovered_in_run_id, (deferred_reason IS NULL AND error_message IS NULL AND completed_at IS NULL) FROM video WHERE id = ?`, video).Scan(&work, &outcome, &discovery, &markers); err != nil {
		t.Fatal(err)
	}
	if work != "not_yet_evaluated" || outcome != "not_yet_evaluated" || discovery != origin || markers != 1 {
		t.Fatalf("reset = %s/%s discovery %d markers %d", work, outcome, discovery, markers)
	}
	if _, err := s.db.Exec(`UPDATE video SET processing_state = 'evaluated' WHERE id = ?`, video); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE run SET status = 'interrupted' WHERE id = ?`, owner); err != nil {
		t.Fatal(err)
	}
	// Distinct recovery membership exists before any new decision; same-owner
	// resume retains its durable progress and does not add attempts.
	reopened, err := NewStore(s.db)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := reopened.ListRetryVideoIDs(ctx, owner)
	if err != nil || len(ids) != 1 || ids[0] != video {
		t.Fatalf("recovery = %v, %v", ids, err)
	}
	claimDeferred(t, reopened, b, owner, true)
	if _, err := s.db.Exec(`UPDATE run_video_outcome SET final_outcome='accepted' WHERE run_id=? AND video_id=?`, owner, video); err != nil {
		t.Fatal(err)
	}
	claimDeferred(t, reopened, b, owner, true)
	if err := s.db.QueryRow(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, owner, video).Scan(&admittedOutcome); err != nil || !admittedOutcome.Valid || admittedOutcome.String != "accepted" {
		t.Fatalf("same-owner resume reset run outcome = %+v, %v", admittedOutcome, err)
	}
	claimDeferred(t, s, a, contender, false)
	if err := s.db.QueryRow(`SELECT processing_state FROM video WHERE id = ?`, video).Scan(&work); err != nil || work != "evaluated" {
		t.Fatalf("resume reset progress: %s, %v", work, err)
	}
	item, _ := s.GetDeferredItem(ctx, a)
	if item.RetryCount != 1 {
		t.Fatal("same-owner resume incremented attempt")
	}
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error { return s.RequeueRetryGroupTx(ctx, tx, video, owner) }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{a, b} {
		item, err := s.GetDeferredItem(ctx, id)
		if err != nil || item.Status != DeferredStatusPending || *item.RetryRunID != owner {
			t.Fatalf("requeued = %+v, %v", item, err)
		}
	}
	claimDeferred(t, s, b, contender, true)
	for range 2 {
		if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE video SET processing_state = 'completed', final_outcome = 'rejected' WHERE id = ?`, video); err != nil {
				return err
			}
			return s.AcknowledgeRetryGroupTx(ctx, tx, video, contender)
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{a, b, completed, discarded} {
		item, err := s.GetDeferredItem(ctx, id)
		want := DeferredStatusCompleted
		if id == discarded {
			want = DeferredStatusDiscarded
		}
		if err != nil || item.Status != want {
			t.Fatalf("terminal = %+v, %v", item, err)
		}
	}
}

func TestRetryClaimOutcomeAdmissionFailureRollsBackClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "admission-rollback")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_retry_membership BEFORE INSERT ON run_video_outcome BEGIN SELECT RAISE(ABORT, 'injected membership failure'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, _, err := s.ClaimDeferredGroupTx(ctx, tx, item, owner, time.Now())
		return err
	})
	if err == nil {
		t.Fatal("claim succeeded despite outcome admission failure")
	}
	deferred, err := s.GetDeferredItem(ctx, item)
	if err != nil || deferred.Status != DeferredStatusPending || deferred.RetryRunID != nil {
		t.Fatalf("claim was not rolled back: %+v, %v", deferred, err)
	}
	var outcomes int
	if err := s.db.QueryRow(`SELECT count(*) FROM run_video_outcome WHERE run_id=? AND video_id=?`, owner, video).Scan(&outcomes); err != nil || outcomes != 0 {
		t.Fatalf("outcome rows after rollback = %d, %v", outcomes, err)
	}
}

func TestRetryCrossQueueOwnersAndFailureHandoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner, contender := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "cross-queue")
	d := deferredSibling(t, s, video, origin, DeferredStatusPending)
	var entry int64
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		entry, err = s.RetainRuntimeErrorTx(ctx, tx, video, origin, "snapshot mismatch", "decision 1 route 1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, ok, err := s.ClaimErrorGroupTx(ctx, tx, entry, owner, time.Now())
		if !ok && err == nil {
			t.Error("manual claim lost")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	claimDeferred(t, s, d, contender, false)
	if _, err := s.db.Exec(`UPDATE run SET status = 'interrupted' WHERE id = ?`, owner); err != nil {
		t.Fatal(err)
	}
	claimDeferred(t, s, d, contender, false)
	if _, err := s.db.Exec(`UPDATE run SET status = 'failed' WHERE id = ?`, owner); err != nil {
		t.Fatal(err)
	}
	// A manual receiving run takes all siblings from the terminal-failed owner.
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, ok, err := s.ClaimErrorGroupTx(ctx, tx, entry, contender, time.Now())
		if !ok && err == nil {
			t.Error("failed owner not reclaimable")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var retained int64
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		// Same origin as the retrying entry: the handoff must not resolve its sink.
		retained, err = s.RetainRuntimeErrorTx(ctx, tx, video, origin, "still incompatible", "new diagnostic")
		if err != nil {
			return err
		}
		return s.AcknowledgeRetryGroupTx(ctx, tx, video, contender)
	}); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetErrorEntry(ctx, entry)
	if err != nil || e.Status != ErrorStatusResolved || *e.RetryRunID != contender || e.RunID != origin || e.Detail != "diag:v1 phase=unknown operation=unknown code=unknown_error" {
		t.Fatalf("old error = %+v, %v", e, err)
	}
	e, err = s.GetErrorEntry(ctx, retained)
	if err != nil || e.Status != ErrorStatusActive || retained == entry {
		t.Fatalf("retained failure = %+v, %v", e, err)
	}
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		id, err := s.RetainRuntimeErrorTx(ctx, tx, video, origin, "do not rewrite", "do not rewrite")
		if id != retained {
			t.Error("repeat failure appended another entry")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRetryClaimRollbackAndConflictingOwners(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner, contender := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "rollback")
	a := deferredSibling(t, s, video, origin, DeferredStatusPending)
	b := deferredSibling(t, s, video, origin, DeferredStatusPending)
	stop := errors.New("injected disposition persistence failure")
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, _, err := s.ClaimDeferredGroupTx(ctx, tx, a, owner, time.Now())
		if err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	item, err := s.GetDeferredItem(ctx, a)
	if err != nil || item.Status != DeferredStatusPending || item.RetryRunID != nil || item.RetryCount != 0 {
		t.Fatalf("rollback = %+v, %v", item, err)
	}
	if _, err := s.db.Exec(`UPDATE deferred_item SET status = 'retrying', retry_run_id = CASE WHEN id = ? THEN ? ELSE ? END WHERE video_id = ?`, a, owner, contender, video); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"claim", "acknowledge", "requeue"} {
		t.Run(operation, func(t *testing.T) {
			err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				switch operation {
				case "claim":
					_, _, err := s.ClaimDeferredGroupTx(ctx, tx, b, owner, time.Now())
					return err
				case "acknowledge":
					return s.AcknowledgeRetryGroupTx(ctx, tx, video, owner)
				default:
					return s.RequeueRetryGroupTx(ctx, tx, video, owner)
				}
			})
			if !errors.Is(err, ErrRetryOwnership) {
				t.Fatalf("conflicting owners: %v", err)
			}
		})
	}
}

func TestRetryClaimContentionAndPreDecisionRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retry.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	origin, a, b := retryRun(t, db), retryRun(t, db), retryRun(t, db)
	video := seedVideo(t, db, origin, "contended")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	deferredSibling(t, s, video, origin, DeferredStatusPending)
	start := make(chan struct{})
	type result struct {
		run     int64
		claimed bool
		err     error
	}
	results := make(chan result, 2)
	for _, run := range []int64{a, b} {
		go func() {
			<-start
			r := result{run: run}
			r.err = s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var err error
				_, r.claimed, err = s.ClaimDeferredGroupTx(ctx, tx, item, run, time.Now())
				return err
			})
			results <- r
		}()
	}
	close(start)
	var winner int64
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.claimed {
			if winner != 0 {
				t.Fatal("both competing runs claimed the video")
			}
			winner = r.run
		}
	}
	if winner == 0 {
		t.Fatal("no claim succeeded")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.ListRetryVideoIDs(ctx, winner)
	if err != nil || len(ids) != 1 || ids[0] != video {
		t.Fatalf("pre-decision recovery after reopen = %v, %v", ids, err)
	}
	claimDeferred(t, s, item, winner, true)
}

func TestRetryRunAdmissionAndEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, mode, status string
		linked, eligible   bool
	}{
		{"manual", "manual", "running", true, true},
		{"scheduled", "scheduled", "interrupted", true, true},
		{"dry-run", "dry_run", "running", true, false},
		{"backtest", "backtest", "running", true, false},
		{"replay", "replay", "running", true, false},
		{"completed", "manual", "completed", true, false},
		{"failed", "manual", "failed", true, false},
		{"missing-snapshot", "manual", "running", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			run := retryRun(t, s.db)
			if _, err := s.db.Exec(`UPDATE run SET run_type = ?, status = ?, config_version_id = CASE WHEN ? THEN config_version_id ELSE NULL END WHERE id = ?`, tc.mode, tc.status, tc.linked, run); err != nil {
				t.Fatal(err)
			}
			video := seedVideo(t, s.db, run, "admission")
			item := deferredSibling(t, s, video, run, DeferredStatusPending)
			err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				_, _, err := s.ClaimDeferredGroupTx(ctx, tx, item, run, time.Now())
				return err
			})
			if (err == nil) != tc.eligible {
				t.Fatalf("admission error = %v, eligible %v", err, tc.eligible)
			}
		})
	}
	t.Run("future retry is ineligible", func(t *testing.T) {
		s := newTestStore(t)
		run := retryRun(t, s.db)
		video := seedVideo(t, s.db, run, "future")
		item := deferredSibling(t, s, video, run, DeferredStatusPending)
		if _, err := s.db.Exec(`UPDATE deferred_item SET retry_after = ? WHERE id = ?`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), item); err != nil {
			t.Fatal(err)
		}
		claimDeferred(t, s, item, run, false)
	})
}

func TestErrorRetryLosingClaimRollsBackReceivingRun(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	owner := retryRun(t, s.db)
	video := seedVideo(t, s.db, owner, "atomic-run")
	item := deferredSibling(t, s, video, owner, DeferredStatusPending)
	claimDeferred(t, s, item, owner, true)
	var entry int64
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		entry, err = s.RetainRuntimeErrorTx(ctx, tx, video, owner, "retry", "retained")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	runs, err := state.NewStore(s.db)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := runs.GetRun(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	lost := errors.New("claim lost")
	var receiving int64
	err = s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		receiving, err = runs.CreateRunTx(ctx, tx, &state.Run{RunType: state.RunTypeManual, Status: state.RunStatusRunning, StartedAt: time.Now(), ConfigVersionID: linked.ConfigVersionID})
		if err != nil {
			return err
		}
		_, claimed, err := s.ClaimErrorGroupTx(ctx, tx, entry, receiving, time.Now())
		if err != nil {
			return err
		}
		if !claimed {
			return lost
		}
		return nil
	})
	if !errors.Is(err, lost) {
		t.Fatalf("losing request error = %v", err)
	}
	if _, err := runs.GetRun(ctx, receiving); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("orphan receiving run survived failed claim: %v", err)
	}
}

func TestErrorOwnerBlocksDeferredClaimWithoutOwnedDeferredRow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, owner, contender := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "error-owner-only")
	var entry int64
	if err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		entry, err = s.RetainRuntimeErrorTx(ctx, tx, video, origin, "retry", "details")
		if err != nil {
			return err
		}
		_, _, err = s.ClaimErrorGroupTx(ctx, tx, entry, owner, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	claimDeferred(t, s, item, contender, false)
	d, err := s.GetDeferredItem(ctx, item)
	if err != nil || d.Status != DeferredStatusPending || d.RetryRunID != nil {
		t.Fatalf("losing deferred claim changed work: %+v, %v", d, err)
	}
	// A completed owner with unfinished retry work is not silently reclaimed.
	if _, err := s.db.Exec(`UPDATE run SET status = 'completed' WHERE id = ?`, owner); err != nil {
		t.Fatal(err)
	}
	err = s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, _, err := s.ClaimDeferredGroupTx(ctx, tx, item, contender, time.Now())
		return err
	})
	if !errors.Is(err, ErrRetryOwnership) {
		t.Fatalf("completed unfinished owner = %v", err)
	}
}

func TestRetainErrorForRetryRequiresActiveExpectedOwner(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		status     ErrorStatus
		owner      any
		wantErr    error
		wantIDZero bool
	}{
		{name: "retrying owner", status: ErrorStatusRetrying, owner: int64(20)},
		{name: "dismissed is explicit removal", status: ErrorStatusDismissed, owner: int64(20), wantErr: ErrWorkRemoved, wantIDZero: true},
		{name: "resolved is explicit removal", status: ErrorStatusResolved, owner: int64(20), wantErr: ErrWorkRemoved, wantIDZero: true},
		{name: "wrong owner", status: ErrorStatusRetrying, owner: int64(21), wantErr: ErrWorkNotOwned, wantIDZero: true},
		{name: "unowned", status: ErrorStatusRetrying, owner: nil, wantErr: ErrWorkNotOwned, wantIDZero: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			origin, retry, other := retryRun(t, s.db), retryRun(t, s.db), retryRun(t, s.db)
			video := seedVideo(t, s.db, origin, tc.name)
			owner := tc.owner
			if owner != nil {
				owner = retry
				if tc.name == "wrong owner" {
					owner = other
				}
			}
			res, err := s.db.ExecContext(ctx, `INSERT INTO error_entry (video_id,run_id,error_category,error_message,error_detail,status,created_at,retry_run_id) VALUES (?,?,?,?,?,?,?,?)`, video, origin, ErrorCategoryAuthorization, "old", "old", tc.status, time.Now().UTC().Format(time.RFC3339), owner)
			if err != nil {
				t.Fatal(err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			gotID := int64(-1)
			err = s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var callErr error
				gotID, callErr = s.RetainAuthorizationPendingForRetryTx(ctx, tx, video, origin, retry, "new", "new")
				return callErr
			})
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && err != nil {
				t.Fatal(err)
			}
			if tc.wantIDZero && gotID != 0 {
				t.Fatalf("id = %d, want 0", gotID)
			}
			if tc.wantErr == nil && gotID != id {
				t.Fatalf("id = %d, want existing %d", gotID, id)
			}
		})
	}
}

func TestRetainErrorForRetryDistinguishesMissingSink(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, retry := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "missing-sink")
	var got int64 = -1
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = s.RetainAuthorizationPendingForRetryTx(ctx, tx, video, origin, retry, "new", "new")
		return err
	})
	if !errors.Is(err, ErrWorkRemoved) || got != 0 {
		t.Fatalf("missing sink = (%d, %v), want removed", got, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM error_entry WHERE video_id=? AND run_id=?`, video, origin).Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing sink resurrected: count=%d err=%v", count, err)
	}
}

func TestRetainDeferredForRetryRechecksDiscardInTransaction(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, retry := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "discard-race")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)
	if _, err := s.db.ExecContext(ctx, `UPDATE deferred_item SET retry_run_id=? WHERE id=?`, retry, item); err != nil {
		t.Fatal(err)
	}
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status='discarded' WHERE id=?`, item); err != nil {
			return err
		}
		_, err := s.RetainDeferredForRetryTx(ctx, tx, item, video, origin, retry)
		return err
	})
	if !errors.Is(err, ErrWorkRemoved) {
		t.Fatalf("discarded sink error = %v, want ErrWorkRemoved", err)
	}
	got, err := s.GetDeferredItem(ctx, item)
	if err != nil || got.Status != DeferredStatusPending {
		t.Fatalf("transaction rollback item=%+v err=%v", got, err)
	}
}

func TestRetainDeferredForRetryUsesExactReceiptWhenNewestSiblingRemoved(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	origin, retry := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "exact-deferred-receipt")
	older := deferredSibling(t, s, video, origin, DeferredStatusPending)
	newer := deferredSibling(t, s, video, origin, DeferredStatusPending)
	if _, err := s.db.ExecContext(ctx, `UPDATE deferred_item SET status='retrying', retry_run_id=? WHERE id IN (?,?)`, retry, older, newer); err != nil {
		t.Fatal(err)
	}
	var got int64
	err := s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE deferred_item SET status='discarded', retry_run_id=NULL WHERE id=?`, newer); err != nil {
			return err
		}
		var err error
		got, err = s.RetainDeferredForRetryTx(ctx, tx, older, video, origin, retry)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != older {
		t.Fatalf("retained id = %d, want exact receipt row %d", got, older)
	}
	item, err := s.GetDeferredItem(ctx, older)
	if err != nil || item.Status != DeferredStatusRetrying || item.RetryRunID == nil || *item.RetryRunID != retry {
		t.Fatalf("older receipt row = %+v, err=%v", item, err)
	}
	item, err = s.GetDeferredItem(ctx, newer)
	if err != nil || item.Status != DeferredStatusDiscarded {
		t.Fatalf("removed newer sibling = %+v, err=%v", item, err)
	}
}
