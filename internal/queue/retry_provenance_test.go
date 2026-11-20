package queue

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
)

// Run-initiation ARS-005 §5.1.1 and authorization ARS-001 "exact receipt and
// admission verification": the claim writes the exact row set once, recovery
// reads it, and no path rebuilds it from owner pointers.

type provenanceFixture struct {
	t    *testing.T
	path string
	db   *sql.DB
	s    *Store
}

func newProvenanceFixture(t *testing.T) *provenanceFixture {
	t.Helper()
	f := &provenanceFixture{t: t, path: filepath.Join(t.TempDir(), "prov.db")}
	f.open()
	t.Cleanup(func() { _ = f.db.Close() })
	return f
}

func (f *provenanceFixture) open() {
	f.t.Helper()
	db, err := store.Open(context.Background(), f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := NewStore(db)
	if err != nil {
		f.t.Fatal(err)
	}
	f.db, f.s = db, s
}

// reopen closes and reopens the same file, as a restart would.
func (f *provenanceFixture) reopen() {
	f.t.Helper()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.open()
}

func (f *provenanceFixture) claimAuto(item, run int64) (RetryGroupReceipt, bool) {
	f.t.Helper()
	var receipt RetryGroupReceipt
	var taken bool
	err := f.s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		receipt, _, taken, err = f.s.ClaimDeferredGroupReceiptTx(context.Background(), tx, item, run, time.Now())
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return receipt, taken
}

func (f *provenanceFixture) stored(run, video int64) sql.NullString {
	f.t.Helper()
	var v sql.NullString
	err := f.db.QueryRow(`SELECT retry_claim_provenance FROM run_video_outcome WHERE run_id=? AND video_id=?`, run, video).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		f.t.Fatal(err)
	}
	return v
}

func (f *provenanceFixture) forceRetrying(table string, id, owner int64) {
	f.t.Helper()
	if _, err := f.db.Exec(`UPDATE `+table+` SET status='retrying', retry_run_id=? WHERE id=?`, owner, id); err != nil {
		f.t.Fatal(err)
	}
}

// c6fb0f87: a same-owner sibling that appears after the claim must not be
// adopted by recovery, validation or a repeated claim.
func TestReceiptRecoveryUnexpectedSiblingProbe(t *testing.T) {
	ctx := context.Background()
	f := newProvenanceFixture(t)
	origin, owner := retryRun(t, f.db), retryRun(t, f.db)
	video := seedVideo(t, f.db, origin, "counterexample")
	first := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
	original, taken := f.claimAuto(first, owner)
	if !taken || !reflect.DeepEqual(original.Deferred, []RetryRowRef{{ID: first, OriginRunID: origin}}) {
		t.Fatalf("claim = %+v taken=%v", original, taken)
	}
	sibling := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
	f.forceRetrying("deferred_item", sibling, owner)
	f.reopen()

	recovered, err := f.s.RetryGroupReceiptForRunVideo(ctx, owner, video)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recovered, original) {
		t.Fatalf("recovered %+v, want the original %+v (sibling %d adopted)", recovered, original, sibling)
	}
	for name, receipt := range map[string]RetryGroupReceipt{"original": original, "recovered": recovered} {
		err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
			_, err := f.s.ValidateRetryReceiptTx(ctx, tx, receipt)
			return err
		})
		if !errors.Is(err, ErrWorkNotOwned) {
			t.Fatalf("validate %s receipt = %v, want ErrWorkNotOwned for the unexpected live sibling", name, err)
		}
	}
	// A repeated claim of the original row hands back the stored set; the
	// sibling is not a member, so claiming it is lost, not adopted.
	again, taken := f.claimAuto(first, owner)
	if !taken || !reflect.DeepEqual(again, original) {
		t.Fatalf("same-owner reclaim = %+v taken=%v, want stored %+v", again, taken, original)
	}
	if _, taken := f.claimAuto(sibling, owner); taken {
		t.Fatal("same-owner claim adopted an unexpected sibling")
	}
}

func TestFreshClaimWritesExactMixedSetOnceAndItSurvivesLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newProvenanceFixture(t)
	originA, originB, owner, later := retryRun(t, f.db), retryRun(t, f.db), retryRun(t, f.db), retryRun(t, f.db)
	video := seedVideo(t, f.db, originA, "mixed")
	d1 := deferredSibling(t, f.s, video, originA, DeferredStatusPending)
	d2 := deferredSibling(t, f.s, video, originB, DeferredStatusPending)
	var e1, e2 int64
	if err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		if e1, err = f.s.RetainRuntimeErrorTx(ctx, tx, video, originA, "m", "d"); err != nil {
			return err
		}
		e2, err = f.s.RetainRuntimeErrorTx(ctx, tx, video, originB, "m", "d")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var receipt RetryGroupReceipt
	if err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var taken bool
		var err error
		receipt, _, taken, err = f.s.ClaimErrorGroupReceiptTx(ctx, tx, e2, owner, time.Now())
		if err == nil && !taken {
			t.Error("manual claim lost")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := RetryGroupReceipt{ReceivingRunID: owner, VideoID: video,
		Deferred: []RetryRowRef{{d1, originA}, {d2, originB}}, Errors: []RetryRowRef{{e1, originA}, {e2, originB}}}
	if !reflect.DeepEqual(receipt, want) {
		t.Fatalf("receipt %+v, want %+v", receipt, want)
	}
	canonical := f.stored(owner, video)
	if !canonical.Valid || !strings.HasPrefix(canonical.String, `{"version":1,"deferred_rows":[{"id":`) {
		t.Fatalf("stored value %v is not canonical version 1", canonical)
	}
	f.reopen()

	check := func(stage string) {
		t.Helper()
		if got := f.stored(owner, video); got != canonical {
			t.Fatalf("%s: stored value changed to %v", stage, got)
		}
		got, err := f.s.RetryGroupReceiptForRunVideo(ctx, owner, video)
		if err == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: recovered %+v, want %+v", stage, got, want)
		}
		if err != nil && !errors.Is(err, ErrWorkRemoved) {
			t.Fatalf("%s: %v", stage, err)
		}
	}
	check("after reopen")

	// An outcome upsert for the same membership keeps the value.
	if _, err := f.db.Exec(`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at) VALUES(?,?,NULL,'x')
		ON CONFLICT(run_id,video_id) DO UPDATE SET final_outcome=excluded.final_outcome,updated_at=excluded.updated_at`, owner, video); err != nil {
		t.Fatal(err)
	}
	check("after outcome upsert")

	// Removing a member (operator discard) cannot shrink the set; recovery still
	// reports the original members, including the removed one.
	if _, err := f.db.Exec(`UPDATE deferred_item SET status='discarded' WHERE id=?`, d1); err != nil {
		t.Fatal(err)
	}
	check("after discard of a member")
	var gotRemoved RetryReceiptValidation
	_ = f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		gotRemoved, err = f.s.ValidateRetryReceiptTx(ctx, tx, want)
		return err
	})
	if len(gotRemoved.Removed) != 1 || gotRemoved.Removed[0].ID != d1 || len(gotRemoved.Deferred) != 1 {
		t.Fatalf("validation of partly removed set = %+v", gotRemoved)
	}

	// Release keeps it and bars same-receiver reclaim; a later receiver claims
	// its own fresh set.
	if err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, err := f.s.ValidateRetryReceiptTx(ctx, tx, want)
		if err != nil {
			return err
		}
		return f.s.RequeueRetryReceiptTx(ctx, tx, want)
	}); err != nil {
		t.Fatal(err)
	}
	check("after release")
	if _, taken := f.claimAuto(d2, owner); taken {
		t.Fatal("released receiver reclaimed the same video")
	}
	check("after refused reclaim")
	laterReceipt, taken := f.claimAuto(d2, later)
	if !taken || !reflect.DeepEqual(laterReceipt.Deferred, []RetryRowRef{{d2, originB}}) || len(laterReceipt.Errors) != 0 {
		t.Fatalf("later receiver claim = %+v taken=%v", laterReceipt, taken)
	}
	if f.stored(later, video) == f.stored(owner, video) {
		t.Fatal("the later receiver must have its own stored set")
	}
	check("after later receiver")
}

func TestClaimRollbackLeavesNoProvenanceOrTransitions(t *testing.T) {
	ctx := context.Background()
	f := newProvenanceFixture(t)
	origin, owner := retryRun(t, f.db), retryRun(t, f.db)
	video := seedVideo(t, f.db, origin, "rollback")
	if _, err := f.db.Exec(`UPDATE video SET processing_state='deferred', final_outcome='deferred', deferred_reason='quota' WHERE id=?`, video); err != nil {
		t.Fatal(err)
	}
	item := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
	boom := errors.New("injected failure after claim writes, before commit")
	err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		if _, taken, err := f.s.ClaimDeferredGroupTx(ctx, tx, item, owner, time.Now()); err != nil || !taken {
			t.Errorf("claim = %v, %v", taken, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	f.reopen()
	var members int
	var status string
	var retryRun sql.NullInt64
	var count int
	var outcome string
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM run_video_outcome), status, retry_run_id, retry_count, (SELECT final_outcome FROM video WHERE id=?) FROM deferred_item WHERE id=?`, video, item).
		Scan(&members, &status, &retryRun, &count, &outcome); err != nil {
		t.Fatal(err)
	}
	if members != 0 || status != "pending" || retryRun.Valid || count != 0 || outcome != "deferred" {
		t.Fatalf("rollback leaked: members=%d status=%s owner=%v count=%d outcome=%s", members, status, retryRun, count, outcome)
	}
	// The same claim then commits and resets in one transaction with the value.
	if _, taken := f.claimAuto(item, owner); !taken {
		t.Fatal("claim after rollback lost")
	}
	if !f.stored(owner, video).Valid {
		t.Fatal("committed claim wrote no provenance")
	}
	if err := f.db.QueryRow(`SELECT final_outcome FROM video WHERE id=?`, video).Scan(&outcome); err != nil || outcome != "not_yet_evaluated" {
		t.Fatalf("reset after commit: %q %v", outcome, err)
	}
}

func TestLosingAndFinalClosePathsMintNoProvenance(t *testing.T) {
	ctx := context.Background()
	f := newProvenanceFixture(t)
	origin, owner, rival := retryRun(t, f.db), retryRun(t, f.db), retryRun(t, f.db)
	video := seedVideo(t, f.db, origin, "losing")
	item := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
	if _, taken := f.claimAuto(item, owner); !taken {
		t.Fatal("first claim lost")
	}
	if _, taken := f.claimAuto(item, rival); taken {
		t.Fatal("rival claimed a live owner's video")
	}
	if f.stored(rival, video).Valid {
		t.Fatal("a losing claim minted provenance")
	}
	final := seedVideo(t, f.db, origin, "final")
	if _, err := f.db.Exec(`UPDATE video SET processing_state='completed', final_outcome='accepted' WHERE id=?`, final); err != nil {
		t.Fatal(err)
	}
	finalItem := deferredSibling(t, f.s, final, origin, DeferredStatusPending)
	var result ClaimResult
	if err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		_, _, result, err = f.s.ClaimDeferredGroupResultTx(ctx, tx, finalItem, rival, time.Now())
		return err
	}); err != nil || result != ClaimClosedFinal {
		t.Fatalf("final close = %v, %v", result, err)
	}
	var members int
	if err := f.db.QueryRow(`SELECT count(*) FROM run_video_outcome WHERE video_id=?`, final).Scan(&members); err != nil || members != 0 {
		t.Fatalf("final close minted membership/provenance: %d %v", members, err)
	}
}

func TestLegacyNullProvenanceFailsClosed(t *testing.T) {
	ctx := context.Background()
	f := newProvenanceFixture(t)
	origin, owner := retryRun(t, f.db), retryRun(t, f.db)
	video := seedVideo(t, f.db, origin, "legacy")
	item := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
	// Legacy shape: membership and owner pointers exist, provenance is NULL.
	f.forceRetrying("deferred_item", item, owner)
	if _, err := f.db.Exec(`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at) VALUES(?,?,NULL,'x')`, owner, video); err != nil {
		t.Fatal(err)
	}
	before := f.snapshotQueue(item)

	if _, err := f.s.RetryGroupReceiptForRunVideo(ctx, owner, video); !errors.Is(err, ErrRetryProvenance) {
		t.Fatalf("loader = %v, want ErrRetryProvenance", err)
	}
	err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, _, _, err := f.s.ClaimDeferredGroupReceiptTx(ctx, tx, item, owner, time.Now())
		return err
	})
	if !errors.Is(err, ErrRetryProvenance) {
		t.Fatalf("same-owner claim = %v, want ErrRetryProvenance", err)
	}
	handBuilt := RetryGroupReceipt{ReceivingRunID: owner, VideoID: video, Deferred: []RetryRowRef{{item, origin}}}
	err = f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, err := f.s.ValidateRetryReceiptTx(ctx, tx, handBuilt)
		return err
	})
	if !errors.Is(err, ErrRetryProvenance) {
		t.Fatalf("validate = %v, want ErrRetryProvenance", err)
	}
	if after := f.snapshotQueue(item); after != before {
		t.Fatalf("failing closed changed state: %s -> %s", before, after)
	}
	if f.stored(owner, video).Valid {
		t.Fatal("a value was written to legacy NULL provenance")
	}

	// Terminal legacy history needs no receipt: nothing is unfinished.
	if _, err := f.db.Exec(`UPDATE deferred_item SET status='completed' WHERE id=?`, item); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RetryGroupReceiptForRunVideo(ctx, owner, video); !errors.Is(err, ErrWorkRemoved) {
		t.Fatalf("terminal legacy loader = %v, want ErrWorkRemoved", err)
	}
}

func (f *provenanceFixture) snapshotQueue(item int64) string {
	f.t.Helper()
	var s string
	if err := f.db.QueryRow(`SELECT status||'|'||coalesce(retry_run_id,'')||'|'||retry_count FROM deferred_item WHERE id=?`, item).Scan(&s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

func TestUnusableStoredProvenanceNeverAuthorizesWork(t *testing.T) {
	ctx := context.Background()
	for name, value := range map[string]string{
		"malformed json":       `{"version":1,`,
		"not an object":        `[]`,
		"unsupported version":  `{"version":2,"deferred_rows":[{"id":1,"origin_run_id":1}],"error_rows":[]}`,
		"float version":        `{"version":1.0,"deferred_rows":[{"id":1,"origin_run_id":1}],"error_rows":[]}`,
		"duplicate field":      `{"version":1,"version":1,"deferred_rows":[{"id":1,"origin_run_id":1}],"error_rows":[]}`,
		"duplicate member":     `{"version":1,"deferred_rows":[{"id":1,"origin_run_id":1},{"id":1,"origin_run_id":1}],"error_rows":[]}`,
		"unsorted members":     `{"version":1,"deferred_rows":[{"id":2,"origin_run_id":1},{"id":1,"origin_run_id":1}],"error_rows":[]}`,
		"unknown field":        `{"version":1,"deferred_rows":[{"id":1,"origin_run_id":1}],"error_rows":[],"extra":1}`,
		"unknown member field": `{"version":1,"deferred_rows":[{"id":1,"origin_run_id":1,"x":1}],"error_rows":[]}`,
		"duplicate member key": `{"version":1,"deferred_rows":[{"id":1,"id":1,"origin_run_id":1}],"error_rows":[]}`,
		"missing array":        `{"version":1,"deferred_rows":[{"id":1,"origin_run_id":1}]}`,
		"null array":           `{"version":1,"deferred_rows":null,"error_rows":[]}`,
		"empty set":            `{"version":1,"deferred_rows":[],"error_rows":[]}`,
		"non-positive id":      `{"version":1,"deferred_rows":[{"id":0,"origin_run_id":1}],"error_rows":[]}`,
		"overflowing id":       `{"version":1,"deferred_rows":[{"id":9223372036854775808,"origin_run_id":1}],"error_rows":[]}`,
		"string id":            `{"version":1,"deferred_rows":[{"id":"1","origin_run_id":1}],"error_rows":[]}`,
		"trailing data":        `{"version":1,"deferred_rows":[{"id":1,"origin_run_id":1}],"error_rows":[]} {}`,
		"empty text":           ``,
	} {
		t.Run(name, func(t *testing.T) {
			f := newProvenanceFixture(t)
			origin, owner := retryRun(t, f.db), retryRun(t, f.db)
			video := seedVideo(t, f.db, origin, "bad")
			item := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
			f.forceRetrying("deferred_item", item, owner)
			if _, err := f.db.Exec(`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at,retry_claim_provenance) VALUES(?,?,NULL,'x',?)`, owner, video, value); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.RetryGroupReceiptForRunVideo(ctx, owner, video); !errors.Is(err, ErrRetryProvenance) {
				t.Fatalf("loader = %v", err)
			}
			err := f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
				_, _, _, err := f.s.ClaimDeferredGroupReceiptTx(ctx, tx, item, owner, time.Now())
				return err
			})
			if !errors.Is(err, ErrRetryProvenance) {
				t.Fatalf("claim = %v", err)
			}
			if got := f.stored(owner, video); got.String != value {
				t.Fatalf("value was replaced: %q", got.String)
			}
		})
	}
}

func TestStoredProvenanceDecodesByIdentityNotEncoding(t *testing.T) {
	d, e, err := decodeRetryProvenance(` { "error_rows" : [ {"origin_run_id":9,"id":4} ], "version" : 1, "deferred_rows":[{"id":1,"origin_run_id":2},{"id":3,"origin_run_id":2}] } `)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, []RetryRowRef{{1, 2}, {3, 2}}) || !reflect.DeepEqual(e, []RetryRowRef{{4, 9}}) {
		t.Fatalf("decoded %v %v", d, e)
	}
	enc, err := encodeRetryProvenance(RetryGroupReceipt{Deferred: d, Errors: e})
	if err != nil || enc != `{"version":1,"deferred_rows":[{"id":1,"origin_run_id":2},{"id":3,"origin_run_id":2}],"error_rows":[{"id":4,"origin_run_id":9}]}` {
		t.Fatalf("encoded %q %v", enc, err)
	}
}

// Foreign, replaced and removed rows around a stored set: never adopted,
// never recreated, and members stay visible as removed.
func TestStoredSetIgnoresReplacedAndForeignRows(t *testing.T) {
	ctx := context.Background()
	f := newProvenanceFixture(t)
	origin, owner := retryRun(t, f.db), retryRun(t, f.db)
	video := seedVideo(t, f.db, origin, "replaced")
	other := seedVideo(t, f.db, origin, "foreign")
	item := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
	original, taken := f.claimAuto(item, owner)
	if !taken {
		t.Fatal("claim lost")
	}
	// The member is removed; a new row for the same video takes the same owner
	// (a replacement), and a foreign video's row also names the owner.
	if _, err := f.db.Exec(`UPDATE deferred_item SET status='discarded' WHERE id=?`, item); err != nil {
		t.Fatal(err)
	}
	replacement := deferredSibling(t, f.s, video, origin, DeferredStatusPending)
	f.forceRetrying("deferred_item", replacement, owner)
	foreign := deferredSibling(t, f.s, other, origin, DeferredStatusPending)
	f.forceRetrying("deferred_item", foreign, owner)

	got, err := f.s.RetryGroupReceiptForRunVideo(ctx, owner, video)
	if err != nil || !reflect.DeepEqual(got, original) {
		t.Fatalf("recovered %+v, %v; want %+v", got, err, original)
	}
	err = f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		v, err := f.s.ValidateRetryReceiptTx(ctx, tx, got)
		if len(v.Removed) != 1 || v.Removed[0].ID != item {
			t.Errorf("removed member not reported: %+v", v)
		}
		return err
	})
	if !errors.Is(err, ErrWorkNotOwned) {
		t.Fatalf("replacement row must not be adopted: %v", err)
	}
	// The foreign video has no stored set for this run.
	if _, err := f.s.RetryGroupReceiptForRunVideo(ctx, owner, other); !errors.Is(err, ErrRetryProvenance) {
		t.Fatalf("foreign video loader = %v, want ErrRetryProvenance", err)
	}
	// A copied receipt that differs from the stored identity is refused.
	forged := got
	forged.Deferred = []RetryRowRef{{ID: replacement, OriginRunID: origin}}
	err = f.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, err := f.s.ValidateRetryReceiptTx(ctx, tx, forged)
		return err
	})
	if !errors.Is(err, ErrRetryProvenance) {
		t.Fatalf("forged receipt = %v, want ErrRetryProvenance", err)
	}
}
