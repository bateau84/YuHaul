package quota

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func setStoredStop(t *testing.T, db *sql.DB, runID int64, v string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE run SET dispatch_stop = ? WHERE id = ?`, v, runID); err != nil {
		t.Fatalf("store dispatch_stop %q: %v", v, err)
	}
}

// S2: for each combination of stored value, latch and a quota row in the
// current bucket, RunDispatchStop returns the highest rank.
func TestRunDispatchStopReturnsHighestRank(t *testing.T) {
	stored := []struct {
		name string
		v    string
		rank DispatchStop
	}{
		{"none", "", StopNone},
		{"authorization", "authorization", StopAuthorization},
		{"accounting", "accounting_or_integrity", StopAccountingOrIntegrity},
	}
	latch := []struct {
		name string
		v    DispatchStop
		rank DispatchStop
	}{
		{"none", StopNone, StopNone},
		{"authorization", StopAuthorization, StopAuthorization},
		{"accounting", StopAccountingOrIntegrity, StopAccountingOrIntegrity},
	}
	for _, s := range stored {
		for _, la := range latch {
			for _, quotaRow := range []bool{false, true} {
				name := "stored=" + s.name + "/latch=" + la.name
				if quotaRow {
					name += "/quota"
				}
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					l, db := newTestLedger(t)
					runID := seedRun(t, db)
					if s.v != "" {
						setStoredStop(t, db, runID, s.v)
					}
					rctx := WithRun(ctx, runID)
					LatchRunStop(rctx, la.v)
					if quotaRow {
						if !admitAndFinalize(t, rctx, l, OpPlaylistItemsInsert, sql.NullInt64{Int64: runID, Valid: true}, OutcomeQuota) {
							t.Fatal("quota attempt was not admitted")
						}
					}
					want := max(s.rank, la.rank)
					if quotaRow {
						want = max(want, StopQuota)
					}
					got, err := l.RunDispatchStop(rctx)
					if err != nil {
						t.Fatalf("RunDispatchStop: %v", err)
					}
					if got != want {
						t.Fatalf("RunDispatchStop = %v, want %v", got, want)
					}
					// The quota level is never written to the column.
					var col sql.NullString
					if err := db.QueryRow(`SELECT dispatch_stop FROM run WHERE id = ?`, runID).Scan(&col); err != nil {
						t.Fatal(err)
					}
					if col.String != s.v {
						t.Fatalf("dispatch_stop column = %q, want %q (the quota level must never be stored)", col.String, s.v)
					}
				})
			}
		}
	}
}

// S2: a quota row from the previous bucket gives StopNone after the reset.
func TestRunDispatchStopQuotaEndsAtReset(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	runID := seedRun(t, db)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	rctx := WithRun(ctx, runID)

	admitAndFinalize(t, rctx, l, OpPlaylistItemsInsert, sql.NullInt64{Int64: runID, Valid: true}, OutcomeQuota)
	if got, err := l.RunDispatchStop(rctx); err != nil || got != StopQuota {
		t.Fatalf("same bucket = %v, %v; want quota", got, err)
	}
	now = now.Add(24 * time.Hour)
	if got, err := l.RunDispatchStop(rctx); err != nil || got != StopNone {
		t.Fatalf("after reset = %v, %v; want none", got, err)
	}
}

// S2: an unscoped context, and another run, read StopNone.
func TestRunDispatchStopIsPerRun(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	r := seedRun(t, db)
	other := seedRun(t, db)
	setStoredStop(t, db, r, "authorization")
	admitAndFinalize(t, WithRun(ctx, r), l, OpPlaylistItemsInsert, sql.NullInt64{Int64: r, Valid: true}, OutcomeQuota)

	if got, err := l.RunDispatchStop(ctx); err != nil || got != StopNone {
		t.Fatalf("unscoped = %v, %v; want none", got, err)
	}
	if got, err := l.RunDispatchStop(WithRun(ctx, other)); err != nil || got != StopNone {
		t.Fatalf("other run = %v, %v; want none", got, err)
	}
	if got, err := l.RunDispatchStop(WithRun(ctx, r)); err != nil || got != StopQuota {
		t.Fatalf("stopped run = %v, %v; want quota", got, err)
	}
}

// The latch is raise-only, survives context.WithoutCancel and a nested WithRun
// of the same run, and cannot hold the derived quota level.
func TestLatchRunStopIsRaiseOnly(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	runID := seedRun(t, db)
	rctx := WithRun(ctx, runID)

	LatchRunStop(rctx, StopAccountingOrIntegrity)
	LatchRunStop(rctx, StopAuthorization) // must not lower
	LatchRunStop(rctx, StopQuota)         // no-op: quota is derived
	for name, c := range map[string]context.Context{
		"same":          rctx,
		"withoutCancel": context.WithoutCancel(rctx),
		"nestedWithRun": WithRun(rctx, runID),
	} {
		if got, err := l.RunDispatchStop(c); err != nil || got != StopAccountingOrIntegrity {
			t.Fatalf("%s: RunDispatchStop = %v, %v; want accounting_or_integrity", name, got, err)
		}
	}

	// A fresh scope (a new execution) starts with a fresh latch.
	if got, err := l.RunDispatchStop(WithRun(ctx, runID)); err != nil || got != StopNone {
		t.Fatalf("fresh scope = %v, %v; want none", got, err)
	}

	// Quota alone is never latched.
	other := seedRun(t, db)
	octx := WithRun(ctx, other)
	LatchRunStop(octx, StopQuota)
	LatchRunStop(ctx, StopAccountingOrIntegrity) // unscoped: no-op
	if got, err := l.RunDispatchStop(octx); err != nil || got != StopNone {
		t.Fatalf("after LatchRunStop(quota) = %v, %v; want none", got, err)
	}
}

// A read error is returned, and a latched stop is still reported with it.
func TestRunDispatchStopReadError(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	runID := seedRun(t, db)
	rctx := WithRun(ctx, runID)
	LatchRunStop(rctx, StopAuthorization)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := l.RunDispatchStop(rctx)
	if err == nil {
		t.Fatal("expected a read error")
	}
	if got != StopAuthorization {
		t.Fatalf("stop with error = %v, want the latched authorization", got)
	}
}

// An unknown stored value is a fail-closed integrity result, not a no-op. The
// CHECK normally forbids it, so build the row without the CHECK in the way.
func TestRunDispatchStopUnknownStoredValue(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	runID := seedRun(t, db)
	if _, err := db.Exec(`PRAGMA ignore_check_constraints = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE run SET dispatch_stop = 'quota' WHERE id = ?`, runID); err != nil {
		t.Skipf("cannot write an invalid value: %v", err)
	}
	got, err := l.RunDispatchStop(WithRun(ctx, runID))
	if err == nil || !strings.Contains(err.Error(), "unknown dispatch_stop") {
		t.Fatalf("err = %v, want unknown dispatch_stop", err)
	}
	if got != StopAccountingOrIntegrity {
		t.Fatalf("stop = %v, want accounting_or_integrity", got)
	}
}

// W3 at the ledger: an admitted attempt whose row cannot be written latches
// accounting_or_integrity on the run it was acquired under, whichever context
// the caller finalizes with and whichever caller sees the error. Another run
// and a new execution of the same run are untouched.
func TestLedgerWriteFailureLatchesAccountingStop(t *testing.T) {
	op := sql.NullInt64{}
	for name, fail := range map[string]func(t *testing.T, l *Ledger, rctx context.Context) error{
		"hold finalize": func(t *testing.T, l *Ledger, rctx context.Context) error {
			h, err := l.Acquire(rctx, OpVideosList)
			if err != nil || h == nil {
				t.Fatalf("acquire: %v, %v", h, err)
			}
			if err := h.Admit(rctx); err != nil {
				t.Fatal(err)
			}
			// The caller's context carries no run scope: the hold remembers its own.
			return h.Finalize(context.Background(), op, OutcomeSuccess, sql.NullInt64{Int64: 200, Valid: true})
		},
		"record attempt": func(t *testing.T, l *Ledger, rctx context.Context) error {
			return l.RecordAttempt(rctx, op, OpVideosList, time.Now(), OutcomeSuccess, sql.NullInt64{Int64: 200, Valid: true})
		},
		"record calls": func(t *testing.T, l *Ledger, rctx context.Context) error {
			return l.RecordCalls(rctx, op, OpVideosList, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			l, db := newTestLedger(t)
			runID := seedRun(t, db)
			other := seedRun(t, db)
			rctx := WithRun(ctx, runID)
			if _, err := db.Exec(`CREATE TRIGGER refuse_row BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
				t.Fatal(err)
			}

			if err := fail(t, l, rctx); err == nil {
				t.Fatal("the attempt row must fail")
			}

			if got, err := l.RunDispatchStop(rctx); err != nil || got != StopAccountingOrIntegrity {
				t.Fatalf("RunDispatchStop = %v, %v; want accounting_or_integrity", got, err)
			}
			if got, err := l.RunDispatchStop(WithRun(ctx, other)); err != nil || got != StopNone {
				t.Fatalf("another run = %v, %v; want none", got, err)
			}
			if got, err := l.RunDispatchStop(WithRun(ctx, runID)); err != nil || got != StopNone {
				t.Fatalf("a new execution of the run = %v, %v; want none (the stored write is the caller's)", got, err)
			}
		})
	}
}
