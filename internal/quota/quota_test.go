package quota

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "quota.db")
	db, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestLedger(t *testing.T) (*Ledger, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	l := New(db)
	return l, db
}

func TestUnitCosts(t *testing.T) {
	tests := []struct {
		op   Operation
		want int
	}{
		{OpSubscriptionsList, 1},
		{OpChannelsList, 1},
		{OpPlaylistItemsList, 1},
		{OpVideosList, 1},
		{OpPlaylistItemsInsert, 50},
		{OpSearchList, 100},
		{"unknown_operation", 1},
	}
	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			if got := tt.op.UnitCost(); got != tt.want {
				t.Errorf("%s.UnitCost() = %d, want %d", tt.op, got, tt.want)
			}
		})
	}
}

func TestRecordAndConsumed(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	if err := l.Record(ctx, sql.NullInt64{}, OpVideosList); err != nil {
		t.Fatalf("record videos.list: %v", err)
	}
	if err := l.Record(ctx, sql.NullInt64{}, OpPlaylistItemsInsert); err != nil {
		t.Fatalf("record playlist insert: %v", err)
	}
	if err := l.Record(ctx, sql.NullInt64{}, OpSubscriptionsList); err != nil {
		t.Fatalf("record background subscription list: %v", err)
	}

	date := time.Now().UTC().Format("2006-01-02")
	consumed, err := l.ConsumedOn(ctx, date)
	if err != nil {
		t.Fatalf("consumed: %v", err)
	}
	want := 1 + 50 + 1
	if consumed != want {
		t.Errorf("consumed = %d, want %d", consumed, want)
	}
}

func TestRecordCalls(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	if err := l.RecordCalls(ctx, sql.NullInt64{}, OpVideosList, 5); err != nil {
		t.Fatalf("record calls: %v", err)
	}

	consumed, err := l.ConsumedToday(ctx)
	if err != nil {
		t.Fatalf("consumed today: %v", err)
	}
	if consumed != 5 {
		t.Errorf("consumed = %d, want 5", consumed)
	}
}

func TestConsumedByRun(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)

	runID := seedRun(t, db)
	otherRunID := seedRun(t, db)

	if err := l.Record(ctx, sql.NullInt64{Int64: runID, Valid: true}, OpVideosList); err != nil {
		t.Fatalf("record run video list: %v", err)
	}
	if err := l.Record(ctx, sql.NullInt64{Int64: runID, Valid: true}, OpPlaylistItemsInsert); err != nil {
		t.Fatalf("record run playlist insert: %v", err)
	}
	if err := l.Record(ctx, sql.NullInt64{Int64: otherRunID, Valid: true}, OpSearchList); err != nil {
		t.Fatalf("record other run search: %v", err)
	}

	consumed, err := l.ConsumedByRun(ctx, runID)
	if err != nil {
		t.Fatalf("consumed by run: %v", err)
	}
	want := 1 + 50
	if consumed != want {
		t.Errorf("consumed by run = %d, want %d", consumed, want)
	}

	consumed, err = l.ConsumedByRun(ctx, 12345)
	if err != nil {
		t.Fatalf("consumed by missing run: %v", err)
	}
	if consumed != 0 {
		t.Errorf("consumed by missing run = %d, want 0", consumed)
	}
}

// seedRun inserts a minimal run row so that quota_ledger FK constraints are
// satisfied in quota-package tests.
func seedRun(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO run (started_at, run_type, status) VALUES (?, ?, ?);`,
		time.Now().UTC(), "manual", "running")
	if err != nil {
		t.Fatalf("creating run: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading run id: %v", err)
	}
	return id
}

func TestBudgetAndCanAfford(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	if err := l.SetDailyLimit(ctx, 100); err != nil {
		t.Fatalf("set daily limit: %v", err)
	}
	if err := l.Record(ctx, sql.NullInt64{}, OpPlaylistItemsInsert); err != nil {
		t.Fatalf("record: %v", err)
	}

	budget, err := l.Budget(ctx)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	if budget.DailyLimit != 100 {
		t.Errorf("daily limit = %d, want 100", budget.DailyLimit)
	}
	if budget.Consumed != 50 {
		t.Errorf("consumed = %d, want 50", budget.Consumed)
	}
	if budget.Remaining != 50 {
		t.Errorf("remaining = %d, want 50", budget.Remaining)
	}

	ok, remaining, err := l.CanAfford(ctx, 40)
	if err != nil {
		t.Fatalf("can afford 40: %v", err)
	}
	if !ok {
		t.Errorf("can afford 40 = false, want true")
	}
	if remaining != 50 {
		t.Errorf("remaining for 40 = %d, want 50", remaining)
	}

	ok, remaining, err = l.CanAfford(ctx, 60)
	if err != nil {
		t.Fatalf("can afford 60: %v", err)
	}
	if ok {
		t.Errorf("can afford 60 = true, want false")
	}
	if remaining != 0 {
		t.Errorf("remaining for 60 = %d, want 0", remaining)
	}
}

func TestBudgetWithoutLimitUsesDefault(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	budget, err := l.Budget(ctx)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	if budget.DailyLimit != DefaultDailyLimit {
		t.Errorf("daily limit = %d, want default %d", budget.DailyLimit, DefaultDailyLimit)
	}
}

func TestResetScheduleBucketDate(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	// 2026-09-08 07:00 Los Angeles is before the 08:00 reset, so the active
	// bucket belongs to the previous calendar day.
	beforeReset := time.Date(2026, 9, 8, 7, 0, 0, 0, loc)
	if err := l.SetResetSchedule(ctx, "America/Los_Angeles", "08:00"); err != nil {
		t.Fatalf("set reset schedule: %v", err)
	}

	date, err := l.bucketDate(ctx, beforeReset)
	if err != nil {
		t.Fatalf("bucket date: %v", err)
	}
	if want := "2026-09-07"; date != want {
		t.Errorf("bucket date before reset = %q, want %q", date, want)
	}

	nextReset, known, err := l.nextReset(ctx, beforeReset)
	if err != nil {
		t.Fatalf("next reset: %v", err)
	}
	if !known {
		t.Fatal("next reset should be known")
	}
	wantReset := time.Date(2026, 9, 8, 8, 0, 0, 0, loc).UTC()
	if !nextReset.Equal(wantReset) {
		t.Errorf("next reset = %v, want %v", nextReset, wantReset)
	}

	// After the reset, the bucket belongs to the current calendar day.
	afterReset := time.Date(2026, 9, 8, 9, 0, 0, 0, loc)
	date, err = l.bucketDate(ctx, afterReset)
	if err != nil {
		t.Fatalf("bucket date after reset: %v", err)
	}
	if want := "2026-09-08"; date != want {
		t.Errorf("bucket date after reset = %q, want %q", date, want)
	}
}

func TestRecordAPIReset(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	future := time.Date(2026, 9, 9, 8, 0, 0, 0, loc).UTC()
	if err := l.RecordAPIReset(ctx, future); err != nil {
		t.Fatalf("record api reset: %v", err)
	}

	now := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)
	nextReset, known, err := l.nextReset(ctx, now)
	if err != nil {
		t.Fatalf("next reset: %v", err)
	}
	if !known {
		t.Fatal("next reset should be known from API")
	}
	if !nextReset.Equal(future) {
		t.Errorf("next reset = %v, want %v", nextReset, future)
	}

	// Once the API reset time has passed, the ledger falls back to any schedule.
	past := future.Add(-time.Hour)
	if err := l.SetResetSchedule(ctx, "America/Los_Angeles", "08:00"); err != nil {
		t.Fatalf("set schedule: %v", err)
	}
	nextReset, known, err = l.nextReset(ctx, past)
	if err != nil {
		t.Fatalf("next reset after api expiry: %v", err)
	}
	if !known {
		t.Fatal("next reset should be known from schedule after API time expired")
	}
	wantReset := time.Date(2026, 9, 9, 8, 0, 0, 0, loc).UTC()
	if !nextReset.Equal(wantReset) {
		t.Errorf("next reset after api expiry = %v, want %v", nextReset, wantReset)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "quota.db")

	// First process opens the ledger, records consumption, and closes.
	func() {
		db, err := store.Open(ctx, dsn)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer db.Close()
		l := New(db)
		if err := l.SetDailyLimit(ctx, 200); err != nil {
			t.Fatalf("set limit: %v", err)
		}
		if err := l.Record(ctx, sql.NullInt64{}, OpPlaylistItemsInsert); err != nil {
			t.Fatalf("record: %v", err)
		}
	}()

	// Reopen the same file in a new process-like context.
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	l := New(db)

	limit, err := l.DailyLimit(ctx)
	if err != nil {
		t.Fatalf("daily limit: %v", err)
	}
	if limit != 200 {
		t.Errorf("daily limit = %d, want 200", limit)
	}

	consumed, err := l.ConsumedToday(ctx)
	if err != nil {
		t.Fatalf("consumed today: %v", err)
	}
	if consumed != 50 {
		t.Errorf("consumed after reopen = %d, want 50", consumed)
	}

	// Verify the file is real, not in-memory.
	if _, err := os.Stat(dsn); err != nil {
		t.Fatalf("db file missing: %v", err)
	}
}

func TestInvalidResetSchedule(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	if err := l.SetResetSchedule(ctx, "not_a_timezone", "08:00"); err == nil {
		t.Error("expected error for invalid timezone")
	}
	if err := l.SetResetSchedule(ctx, "UTC", "25:00"); err == nil {
		t.Error("expected error for invalid time of day")
	}
	if err := l.SetResetSchedule(ctx, "UTC", "bad"); err == nil {
		t.Error("expected error for malformed time of day")
	}
}

func TestCanAffordUnlimited(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)

	if err := l.SetDailyLimit(ctx, 0); err != nil {
		t.Fatalf("set unlimited: %v", err)
	}
	ok, remaining, err := l.CanAfford(ctx, 1_000_000)
	if err != nil {
		t.Fatalf("can afford: %v", err)
	}
	if !ok {
		t.Error("unlimited limit should always afford")
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
}
