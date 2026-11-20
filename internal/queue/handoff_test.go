package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Tests for HandOffUnfinishedTx and the stopped-owner claim rules
// (run-initiation ARS-003 §2, tests H1–H4 and Q1) on a real migrated SQLite.

// dump renders every deferred_item and error_entry row so a test can assert
// "unchanged" byte for byte.
func dumpQueues(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT id,video_id,run_id,deferral_reason,ifnull(retry_after,'NULL'),retry_count,status,ifnull(retry_run_id,'NULL') FROM deferred_item ORDER BY id`,
		`SELECT id,video_id,run_id,error_category,status,ifnull(resolved_at,'NULL'),retry_count,ifnull(retry_run_id,'NULL') FROM error_entry ORDER BY id`,
	} {
		rows, err := db.Query(q)
		require.NoError(t, err)
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			fmt.Fprintln(&b, vals...)
		}
		require.NoError(t, rows.Err())
		rows.Close()
	}
	return b.String()
}

func handoff(t *testing.T, s *Store, run int64, reason DeferredReason, ids ...int64) (HandoffResult, error) {
	t.Helper()
	var res HandoffResult
	err := s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		res, err = s.HandOffUnfinishedTx(context.Background(), tx, run, reason, ids)
		return err
	})
	return res, err
}

type deferredRow struct {
	ID, RunID      int64
	Reason, Status string
	RetryAfter     sql.NullString
	RetryCount     int
	RetryRunID     sql.NullInt64
}

func deferredRows(t *testing.T, db *sql.DB, video int64) []deferredRow {
	t.Helper()
	rows, err := db.Query(`SELECT id, run_id, deferral_reason, status, retry_after, retry_count, retry_run_id FROM deferred_item WHERE video_id=? ORDER BY id`, video)
	require.NoError(t, err)
	defer rows.Close()
	var out []deferredRow
	for rows.Next() {
		var r deferredRow
		require.NoError(t, rows.Scan(&r.ID, &r.RunID, &r.Reason, &r.Status, &r.RetryAfter, &r.RetryCount, &r.RetryRunID))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// H1 (all six cases at once): see ARS-003 §7.2.
func TestHandOffUnfinishedCases(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	db := s.db
	other, r := retryRun(t, db), retryRun(t, db)
	vA := seedVideo(t, db, r, "a")
	vB := seedVideo(t, db, r, "b")
	vC := seedVideo(t, db, r, "c")
	vD := seedVideo(t, db, r, "d")
	vE := seedVideo(t, db, r, "e")
	vF := seedVideo(t, db, r, "f")
	vDone := seedVideo(t, db, r, "done")

	// (b) a pending item from another run with a future retry_after.
	future := time.Now().UTC().Add(6 * time.Hour)
	bID, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: vB, RunID: other, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, RetryAfter: &future, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	// (c) a deferred item R claimed.
	cID, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: vC, RunID: other, Reason: DeferredReasonAPIRateLimited, Status: DeferredStatusPending, RetryCount: 2, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	claimDeferred(t, s, cID, r, true)
	// (d) an Errors-queue entry R claimed.
	dErr, err := s.CreateErrorEntry(ctx, &ErrorEntry{VideoID: vD, RunID: other, Category: ErrorCategoryRuntime, Message: "m", Detail: "d", Status: ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	require.NoError(t, s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, claimed, err := s.ClaimErrorGroupTx(ctx, tx, dErr, r, time.Now())
		require.True(t, claimed)
		return err
	}))
	// (e) a deferred item the user discarded while R held it.
	eID, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: vE, RunID: other, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	claimDeferred(t, s, eID, r, true)
	_, err = db.Exec(`UPDATE deferred_item SET status='discarded' WHERE id=?`, eID)
	require.NoError(t, err)
	// (f) an error entry the user dismissed while R held it.
	fErr, err := s.CreateErrorEntry(ctx, &ErrorEntry{VideoID: vF, RunID: other, Category: ErrorCategoryRuntime, Message: "m", Detail: "d", Status: ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	require.NoError(t, s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, claimed, err := s.ClaimErrorGroupTx(ctx, tx, fErr, r, time.Now())
		require.True(t, claimed)
		return err
	}))
	_, err = db.Exec(`UPDATE error_entry SET status='dismissed' WHERE id=?`, fErr)
	require.NoError(t, err)

	// A committed video's leftover claim (H3).
	doneID, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: vDone, RunID: other, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	claimDeferred(t, s, doneID, r, true)

	// Controls that must not change: another run's claim, and a guard/action-free
	// guard table.
	guardsBefore := countRows(t, db, "playlist_send_guard") + countRows(t, db, "executed_action")

	res, err := handoff(t, s, r, DeferredReasonStoppedByUser, vA, vB, vC, vD, vE, vF)
	require.NoError(t, err)

	// (a) one new pending item with the handoff reason, due now.
	a := deferredRows(t, db, vA)
	require.Len(t, a, 1)
	require.Equal(t, "pending", a[0].Status)
	require.Equal(t, "stopped_by_user", a[0].Reason)
	require.Equal(t, r, a[0].RunID)
	require.False(t, a[0].RetryAfter.Valid)
	require.Zero(t, a[0].RetryCount)
	// (b) the same row, due now, nothing new.
	b := deferredRows(t, db, vB)
	require.Len(t, b, 1)
	require.Equal(t, bID, b[0].ID)
	require.False(t, b[0].RetryAfter.Valid, "AC13: made eligible now")
	require.Equal(t, "quota_exhausted", b[0].Reason)
	// (c) the same row pending with its original identity.
	c := deferredRows(t, db, vC)
	require.Len(t, c, 1)
	require.Equal(t, cID, c[0].ID)
	require.Equal(t, "pending", c[0].Status)
	require.Equal(t, other, c[0].RunID)
	require.Equal(t, "api_rate_limited", c[0].Reason)
	require.Equal(t, 3, c[0].RetryCount, "the claim's count increment stays; the handoff adds none")
	// (d) the entry resolved with history, plus a new pending item.
	var status string
	var resolved sql.NullString
	require.NoError(t, db.QueryRow(`SELECT status, resolved_at FROM error_entry WHERE id=?`, dErr).Scan(&status, &resolved))
	require.Equal(t, "resolved", status)
	require.True(t, resolved.Valid)
	d := deferredRows(t, db, vD)
	require.Len(t, d, 1)
	require.Equal(t, "pending", d[0].Status)
	// (e) and (f): removed work is not re-created.
	e := deferredRows(t, db, vE)
	require.Len(t, e, 1)
	require.Equal(t, "discarded", e[0].Status)
	require.Empty(t, deferredRows(t, db, vF))
	require.NoError(t, db.QueryRow(`SELECT status FROM error_entry WHERE id=?`, fErr).Scan(&status))
	require.Equal(t, "dismissed", status)
	// H3: a leftover claim for a video outside the unfinished set is released too.
	done := deferredRows(t, db, vDone)
	require.Len(t, done, 1)
	require.Equal(t, "pending", done[0].Status)

	require.Equal(t, 0, countRows(t, db, "deferred_item WHERE status='retrying' AND retry_run_id="+fmt.Sprint(r)))
	require.Equal(t, 0, countRows(t, db, "error_entry WHERE status='retrying' AND retry_run_id="+fmt.Sprint(r)))
	require.Equal(t, guardsBefore, countRows(t, db, "playlist_send_guard")+countRows(t, db, "executed_action"))
	require.Equal(t, HandoffResult{Requeued: 2, Created: 2, Resolved: 1, Removed: 2}, res)

	// H2: a second handoff changes nothing.
	before := dumpQueues(t, db)
	res, err = handoff(t, s, r, DeferredReasonStoppedByUser, vA, vB, vC, vD, vE, vF)
	require.NoError(t, err)
	require.Equal(t, before, dumpQueues(t, db))
	require.Equal(t, HandoffResult{Removed: 2}, res)
}

func countRows(t *testing.T, db *sql.DB, tableAndWhere string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+tableAndWhere).Scan(&n))
	return n
}

func TestHandOffRejectsOtherReasons(t *testing.T) {
	s := newTestStore(t)
	r := retryRun(t, s.db)
	v := seedVideo(t, s.db, r, "v")
	for _, reason := range []DeferredReason{DeferredReasonQuotaExhausted, "", "nope"} {
		_, err := handoff(t, s, r, reason, v)
		require.Error(t, err, string(reason))
	}
	require.Zero(t, countRows(t, s.db, "deferred_item"))
}

// H4: a failure after step 2 rolls back steps 1 and 2.
func TestHandOffIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	db := s.db
	other, r := retryRun(t, db), retryRun(t, db)
	v := seedVideo(t, db, r, "v")
	item, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: v, RunID: other, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	claimDeferred(t, s, item, r, true)
	w := seedVideo(t, db, r, "w")
	_, err = db.Exec(`CREATE TRIGGER reject_handoff BEFORE INSERT ON deferred_item BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	before := dumpQueues(t, db)
	_, err = handoff(t, s, r, DeferredReasonDiscardedByUser, w)
	require.ErrorContains(t, err, "injected")
	require.Equal(t, before, dumpQueues(t, db), "steps 1-2 rolled back")
}

// A postcondition violation (a claim the steps cannot release) rolls back.
func TestHandOffPostconditionIsEnforced(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	other, r := retryRun(t, s.db), retryRun(t, s.db)
	v := seedVideo(t, s.db, r, "v")
	item, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: v, RunID: other, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	claimDeferred(t, s, item, r, true)
	// Step 1 releases the claim, but a trigger turns it back into 'retrying'.
	_, err = s.db.Exec(`CREATE TRIGGER reclaim AFTER UPDATE OF status ON deferred_item WHEN NEW.status='pending'
		BEGIN UPDATE deferred_item SET status='retrying' WHERE id=NEW.id; END`)
	require.NoError(t, err)
	_, err = handoff(t, s, r, DeferredReasonStoppedByUser, v)
	require.True(t, errors.Is(err, ErrRetryOwnership), "got %v", err)
}

// Q1: a retrying row owned by a stopped run is reclaimable by a new claim; the
// same row owned by a completed run stays an integrity error.
func TestStoppedOwnerIsReclaimable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	db := s.db
	origin, owner, next := retryRun(t, db), retryRun(t, db), retryRun(t, db)
	v := seedVideo(t, db, origin, "v")
	item, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: v, RunID: origin, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	claimDeferred(t, s, item, owner, true)

	// owner completes with a leftover claim: integrity error for a new claim.
	_, err = db.Exec(`UPDATE run SET status='completed', completed_at='2026-01-01T00:00:00Z' WHERE id=?`, owner)
	require.NoError(t, err)
	err = s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		_, _, err := s.ClaimDeferredGroupTx(ctx, tx, item, next, time.Now())
		return err
	})
	require.True(t, errors.Is(err, ErrRetryOwnership), "completed owner: %v", err)

	// A stopped owner is reclaimable, and listed as a candidate.
	other := retryRun(t, db)
	v2 := seedVideo(t, db, origin, "v2")
	item2, err := s.CreateDeferredItem(ctx, &DeferredItem{VideoID: v2, RunID: origin, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	claimDeferred(t, s, item2, other, true)
	_, err = db.Exec(`UPDATE run SET stop_requested_at='2026-01-01T00:00:00Z' WHERE id=?`, other)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE run SET status='stopped', completed_at='2026-01-01T00:00:01Z' WHERE id=?`, other)
	require.NoError(t, err)
	cands, err := s.ListDeferredRetryCandidates(ctx, time.Now())
	require.NoError(t, err)
	var ids []int64
	for _, c := range cands {
		ids = append(ids, c.ID)
	}
	require.Contains(t, ids, item2)
	claimDeferred(t, s, item2, next, true)
	rows := deferredRows(t, db, v2)
	require.Len(t, rows, 1)
	require.Equal(t, "retrying", rows[0].Status)
	require.Equal(t, next, rows[0].RetryRunID.Int64)
}
