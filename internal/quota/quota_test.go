package quota

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
)

func TestAdmitIfRevalidatesAfterScheduleRead(t *testing.T) {
	for _, scenario := range []string{"expired", "replaced", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			l, db := newTestLedger(t)
			if err := l.SetDailyLimit(ctx, 1); err != nil {
				t.Fatal(err)
			}
			before := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
			var clock atomic.Int64
			clock.Store(before.UnixNano())
			l.now = func() time.Time { return time.Unix(0, clock.Load()) }
			h, err := l.Acquire(ctx, OpVideosList)
			if err != nil || h == nil {
				t.Fatalf("acquire: %v, %v", h, err)
			}
			db.SetMaxOpenConns(1)
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			waits := db.Stats().WaitCount
			var generation atomic.Int64
			generation.Store(1)
			refused := errors.New("authority invalid")
			done := make(chan error, 1)
			go func() {
				done <- h.AdmitIf(ctx, func(at time.Time) error {
					if !at.Before(before.Add(time.Second)) || generation.Load() != 1 {
						return refused
					}
					return nil
				})
			}()
			for db.Stats().WaitCount == waits {
				select {
				case <-ctx.Done():
					_ = conn.Close()
					t.Fatal("admission did not reach schedule read")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if scenario == "expired" {
				clock.Store(before.Add(time.Second).UnixNano())
			}
			if scenario == "replaced" {
				generation.Store(2)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if scenario != "valid" {
				if !errors.Is(err, refused) {
					t.Fatalf("admit error = %v", err)
				}
				if err := h.Cancel(); err != nil {
					t.Fatal(err)
				}
				consumed, err := l.ConsumedOn(ctx, "2026-01-02")
				if err != nil || consumed != 0 {
					t.Fatalf("consumed = %d, %v", consumed, err)
				}
				next, err := l.Acquire(ctx, OpVideosList)
				if err != nil || next == nil {
					t.Fatalf("capacity not released: %v, %v", next, err)
				}
				if err := next.Cancel(); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !h.AdmissionTime().Equal(before) {
				t.Fatalf("admission time = %v", h.AdmissionTime())
			}
			if err := h.Cancel(); err == nil {
				t.Fatal("admitted hold was cancelled")
			}
			if next, err := l.Acquire(ctx, OpVideosList); err != nil || next != nil {
				t.Fatalf("admitted capacity not held: %v, %v", next, err)
			}
			if err := h.Finalize(ctx, sql.NullInt64{}, OutcomeSuccess, sql.NullInt64{}); err != nil {
				t.Fatal(err)
			}
			if next, err := l.Acquire(ctx, OpVideosList); err != nil || next != nil {
				t.Fatalf("committed capacity not held: %v, %v", next, err)
			}
			consumed, err := l.ConsumedOn(ctx, "2026-01-02")
			if err != nil || consumed != 1 {
				t.Fatalf("consumed = %d, %v", consumed, err)
			}
		})
	}
}

func TestAdmitIfCancellationDuringValidation(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := l.Acquire(ctx, OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("acquire: %v, %v", h, err)
	}
	err = h.AdmitIf(ctx, func(time.Time) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("admission error = %v, want context.Canceled", err)
	}
	if err := h.Cancel(); err != nil {
		t.Fatalf("cancel provisional hold: %v", err)
	}
}

func TestAdmitScheduleReadCrossesReset(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, db := newTestLedger(t)
	if err := l.SetResetSchedule(ctx, "UTC", "06:00"); err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 1, 2, 5, 59, 59, 0, time.UTC)
	after := before.Add(2 * time.Second)
	var clock atomic.Int64
	clock.Store(before.UnixNano())
	l.now = func() time.Time { return time.Unix(0, clock.Load()) }
	h, err := l.Acquire(ctx, OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("acquire: hold=%v err=%v", h, err)
	}

	// Occupy the only connection so the real schedule read waits across reset.
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waits := db.Stats().WaitCount
	done := make(chan error, 1)
	go func() { done <- h.Admit(ctx) }()
	for db.Stats().WaitCount == waits {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			t.Fatal("admission did not reach the SQLite read")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	clock.Store(after.UnixNano())
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("admitted a hold from the previous bucket")
	}
	if err := h.Cancel(); err != nil {
		t.Fatalf("refused hold must remain cancellable: %v", err)
	}
	h, err = l.Acquire(ctx, OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("acquire current bucket: hold=%v err=%v", h, err)
	}
	if err := h.Admit(ctx); err != nil {
		t.Fatal(err)
	}
	if !h.AdmissionTime().Equal(after) {
		t.Fatalf("admission time = %v, want %v", h.AdmissionTime(), after)
	}
	if err := h.Finalize(ctx, sql.NullInt64{}, OutcomeSuccess, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
	consumed, err := l.ConsumedOn(ctx, "2026-01-02")
	if err != nil || consumed != 1 {
		t.Fatalf("current bucket consumed = %d, err = %v", consumed, err)
	}
}

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

func TestRecordAttempt(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	if err := l.SetResetSchedule(ctx, "UTC", "06:00"); err != nil {
		t.Fatal(err)
	}
	// This is before the reset, so completion on a later day must not move it
	// to the current bucket.
	admitted := time.Date(2020, 1, 2, 5, 59, 0, 123, time.UTC)
	runID := seedRun(t, db)
	categories := []OutcomeCategory{OutcomeSuccess, OutcomeQuota, OutcomeTransient, OutcomeAuth, OutcomePermanent}
	for i, category := range categories {
		status := sql.NullInt64{Int64: 401, Valid: i != 2}
		if err := l.RecordAttempt(ctx, sql.NullInt64{Int64: runID, Valid: true}, OpVideosList, admitted, category, status); err != nil {
			t.Fatalf("record %s: %v", category, err)
		}
	}
	if err := l.RecordCalls(ctx, sql.NullInt64{}, OpVideosList, 2); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT date, operation_type, units_consumed, call_count, run_id, recorded_at, outcome_category, http_status FROM quota_ledger ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i, category := range categories {
		if !rows.Next() {
			t.Fatalf("missing row %d: %v", i, rows.Err())
		}
		var date, operation string
		var units, count int
		var gotRun, status sql.NullInt64
		var at time.Time
		var gotCategory sql.NullString
		if err := rows.Scan(&date, &operation, &units, &count, &gotRun, &at, &gotCategory, &status); err != nil {
			t.Fatal(err)
		}
		if date != "2020-01-01" || operation != string(OpVideosList) || units != 1 || count != 1 || !gotRun.Valid || gotRun.Int64 != runID || !at.Equal(admitted) || !gotCategory.Valid || gotCategory.String != string(category) || status.Valid != (i != 2) || (status.Valid && status.Int64 != 401) {
			t.Fatalf("unexpected attempt %d: %s %s %d %d %+v %s %+v %+v", i, date, operation, units, count, gotRun, at, gotCategory, status)
		}
	}
	if !rows.Next() {
		t.Fatal("missing legacy-style record")
	}
	var date, operation string
	var units, count int
	var gotRun, status sql.NullInt64
	var at time.Time
	var category sql.NullString
	if err := rows.Scan(&date, &operation, &units, &count, &gotRun, &at, &category, &status); err != nil {
		t.Fatal(err)
	}
	if category.Valid || status.Valid || units != 2 || count != 2 {
		t.Fatalf("legacy-style record: category=%+v status=%+v units=%d count=%d", category, status, units, count)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra rows: %v", rows.Err())
	}
	if got, err := l.ConsumedOn(ctx, "2020-01-01"); err != nil || got != len(categories) {
		t.Fatalf("historical consumption = %d, %v", got, err)
	}
	if got, err := l.ConsumedByRun(ctx, runID); err != nil || got != len(categories) {
		t.Fatalf("run consumption = %d, %v", got, err)
	}
}

func TestRecordAttemptRejectsInvalidAndCanceled(t *testing.T) {
	l, db := newTestLedger(t)
	ctx := context.Background()
	if err := l.RecordAttempt(ctx, sql.NullInt64{}, OpVideosList, time.Time{}, OutcomeSuccess, sql.NullInt64{}); err == nil {
		t.Fatal("zero admission time accepted")
	}
	if err := l.RecordAttempt(ctx, sql.NullInt64{}, OpVideosList, time.Now(), "invalid", sql.NullInt64{}); err == nil {
		t.Fatal("invalid category accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := l.RecordAttempt(canceled, sql.NullInt64{}, OpVideosList, time.Now(), OutcomeTransient, sql.NullInt64{}); err == nil {
		t.Fatal("canceled recording accepted")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM quota_ledger`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("row count = %d, %v", count, err)
	}
}

func TestAttemptMigrationPreservesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (version uint64, dirty bool)`,
		`INSERT INTO schema_migrations VALUES (22, false)`,
		`CREATE TABLE quota_ledger (id INTEGER PRIMARY KEY, date TEXT, operation_type TEXT, units_consumed INTEGER, call_count INTEGER, run_id INTEGER, recorded_at DATETIME)`,
		`INSERT INTO quota_ledger (date, operation_type, units_consumed, call_count, recorded_at) VALUES ('2020-01-01', 'videos_list', 3, 3, '2020-01-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ApplyMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var category sql.NullString
	var status sql.NullInt64
	var units int
	if err := db.QueryRow(`SELECT outcome_category, http_status, units_consumed FROM quota_ledger`).Scan(&category, &status, &units); err != nil {
		t.Fatal(err)
	}
	if category.Valid || status.Valid || units != 3 {
		t.Fatalf("legacy row: category=%+v status=%+v units=%d", category, status, units)
	}
	if got, err := New(db).ConsumedOn(context.Background(), "2020-01-01"); err != nil || got != 3 {
		t.Fatalf("legacy consumption = %d, %v", got, err)
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

func TestReservationsLastUnitAndCancellation(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)
	if err := l.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	const n = 20
	results := make(chan *Hold, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { h, err := l.Acquire(ctx, OpVideosList); results <- h; errs <- err }()
	}
	var winner *Hold
	for i := 0; i < n; i++ {
		h := <-results
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if h != nil {
			if winner != nil {
				t.Fatal("multiple reservations")
			}
			winner = h
		}
	}
	if winner == nil {
		t.Fatal("no reservation")
	}
	if ok, _, err := l.CanAfford(ctx, 1); err != nil || ok {
		t.Fatalf("held preflight: %v %v", ok, err)
	}
	if err := winner.Cancel(); err != nil {
		t.Fatal(err)
	}
	h, err := l.Acquire(ctx, OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("acquire after cancel: %v", err)
	}
	if err := h.Admit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Cancel(); err == nil {
		t.Fatal("cancel admitted")
	}
	if err := h.Finalize(ctx, sql.NullInt64{}, OutcomeTransient, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
	if err := h.Finalize(ctx, sql.NullInt64{}, OutcomeTransient, sql.NullInt64{}); err == nil {
		t.Fatal("duplicate finalize")
	}
	if n, err := l.ConsumedToday(ctx); err != nil || n != 1 {
		t.Fatalf("consumed %d: %v", n, err)
	}
}

func TestReservationRecordingFailureKeepsDebit(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	if err := l.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	h, err := l.Acquire(ctx, OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := h.Admit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_quota BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(FAIL, 'failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := h.Finalize(ctx, sql.NullInt64{}, OutcomeSuccess, sql.NullInt64{}); err == nil {
		t.Fatal("expected recording failure")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_quota`); err != nil {
		t.Fatal(err)
	}
	if err := h.Finalize(ctx, sql.NullInt64{}, OutcomeSuccess, sql.NullInt64{}); err == nil {
		t.Fatal("unsafe retry")
	}
	next, err := l.Acquire(ctx, OpVideosList)
	if err != nil || next != nil {
		t.Fatalf("failed debit lost: %v %v", next, err)
	}
	if n, err := l.ConsumedToday(ctx); err != nil || n != 0 {
		t.Fatalf("fabricated row %d: %v", n, err)
	}
}

func TestReservationRetainsBucketAfterRollover(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)
	if err := l.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 23, 59, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	h, err := l.Acquire(ctx, OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := h.Admit(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	next, err := l.Acquire(ctx, OpVideosList)
	if err != nil || next == nil {
		t.Fatalf("new bucket: %v", err)
	}
	if err := next.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := h.Finalize(ctx, sql.NullInt64{}, OutcomeSuccess, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
	if n, err := l.ConsumedOn(ctx, "2026-01-01"); err != nil || n != 1 {
		t.Fatalf("original bucket %d: %v", n, err)
	}
}
