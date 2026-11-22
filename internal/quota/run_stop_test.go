package quota

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// admitAndFinalize drives one guarded call through the ledger and returns
// whether it was admitted.
func admitAndFinalize(t *testing.T, ctx context.Context, l *Ledger, op Operation, runID sql.NullInt64, category OutcomeCategory) bool {
	t.Helper()
	h, err := l.Acquire(ctx, op)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if h == nil {
		return false
	}
	if err := h.Admit(ctx); err != nil {
		t.Fatalf("admit: %v", err)
	}
	status := sql.NullInt64{Int64: 200, Valid: true}
	if category == OutcomeQuota {
		status.Int64 = 403
	}
	if err := h.Finalize(ctx, runID, category, status); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return true
}

// Q1b (ledger level): the run stop applies even when the local limit is
// unlimited, in both Acquire and CanAfford.
func TestRunStopAppliesWithUnlimitedDailyLimit(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	if err := l.SetDailyLimit(ctx, 0); err != nil {
		t.Fatal(err)
	}
	runID := seedRun(t, db)
	rctx := WithRun(ctx, runID)
	run := sql.NullInt64{Int64: runID, Valid: true}

	if ok, _, err := l.CanAfford(rctx, 50); err != nil || !ok {
		t.Fatalf("before refusal: ok=%v err=%v", ok, err)
	}
	if !admitAndFinalize(t, rctx, l, OpPlaylistItemsInsert, run, OutcomeSuccess) {
		t.Fatal("first call refused")
	}
	if !admitAndFinalize(t, rctx, l, OpPlaylistItemsInsert, run, OutcomeQuota) {
		t.Fatal("second call refused before the provider said so")
	}
	h, err := l.Acquire(rctx, OpPlaylistItemsInsert)
	if err != nil || h != nil {
		t.Fatalf("Acquire after quota row = %v, %v; want nil, nil", h, err)
	}
	if ok, rem, err := l.CanAfford(rctx, 1); err != nil || ok || rem != 0 {
		t.Fatalf("CanAfford after quota row = %v, %d, %v; want false, 0, nil", ok, rem, err)
	}
}

// Q3: the stop is per run and per bucket. Another run and unscoped calls are
// admitted; the stopped run is admitted again after the next reset.
func TestRunStopIsScopedToRunAndBucket(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	r := seedRun(t, db)
	s := seedRun(t, db)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	if !admitAndFinalize(t, WithRun(ctx, r), l, OpPlaylistItemsInsert, sql.NullInt64{Int64: r, Valid: true}, OutcomeQuota) {
		t.Fatal("refusal call not admitted")
	}
	if h, err := l.Acquire(WithRun(ctx, r), OpVideosList); err != nil || h != nil {
		t.Fatalf("run R admitted after its quota row: %v %v", h, err)
	}
	for name, c := range map[string]context.Context{"run S": WithRun(ctx, s), "unscoped": ctx} {
		h, err := l.Acquire(c, OpVideosList)
		if err != nil || h == nil {
			t.Fatalf("%s refused: %v %v", name, h, err)
		}
		if err := h.Cancel(); err != nil {
			t.Fatal(err)
		}
	}
	// 2026-11-23T08:00Z is the next Pacific midnight.
	now = time.Date(2026, 11, 23, 7, 59, 59, 0, time.UTC)
	if h, err := l.Acquire(WithRun(ctx, r), OpVideosList); err != nil || h != nil {
		t.Fatalf("run R admitted before reset: %v %v", h, err)
	}
	now = time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	h, err := l.Acquire(WithRun(ctx, r), OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("run R refused after reset: %v %v", h, err)
	}
	_ = h.Cancel()
}

// Q2 (ledger level): the stop is read from rows, so a new Ledger on the same
// database, as after a restart, still refuses the run.
func TestRunStopSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	r := seedRun(t, db)
	admitAndFinalize(t, WithRun(ctx, r), l, OpPlaylistItemsInsert, sql.NullInt64{Int64: r, Valid: true}, OutcomeQuota)

	restarted := New(db)
	if h, err := restarted.Acquire(WithRun(ctx, r), OpPlaylistItemsInsert); err != nil || h != nil {
		t.Fatalf("restarted ledger admitted: %v %v", h, err)
	}
	if ok, _, err := restarted.CanAfford(WithRun(ctx, r), 1); err != nil || ok {
		t.Fatalf("restarted CanAfford = %v, %v", ok, err)
	}
}

// Q4: a quota refusal is charged 1 unit whatever the operation costs. Other
// categories keep the operation cost.
func TestQuotaRefusalChargedOneUnit(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	r := seedRun(t, db)
	run := sql.NullInt64{Int64: r, Valid: true}
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		op       Operation
		category OutcomeCategory
		want     int
	}{
		{OpPlaylistItemsInsert, OutcomeQuota, 1},
		{OpSearchList, OutcomeQuota, 1},
		{OpPlaylistItemsInsert, OutcomePermanent, 50},
		{OpPlaylistItemsInsert, OutcomeSuccess, 50},
		{OpVideosList, OutcomeSuccess, 1},
		{OpVideosList, OutcomeQuota, 1},
	}
	for _, c := range cases {
		// Via RecordAttempt.
		if err := l.RecordAttempt(ctx, run, c.op, at, c.category, sql.NullInt64{Int64: 403, Valid: true}); err != nil {
			t.Fatal(err)
		}
		// Via Hold.Finalize.
		l2 := New(db)
		l2.now = func() time.Time { return at }
		h, err := l2.Acquire(ctx, c.op)
		if err != nil || h == nil {
			t.Fatalf("acquire: %v %v", h, err)
		}
		if err := h.Admit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := h.Finalize(ctx, run, c.category, sql.NullInt64{Int64: 403, Valid: true}); err != nil {
			t.Fatal(err)
		}
		rows, err := db.QueryContext(ctx, `SELECT units_consumed, call_count, http_status FROM quota_ledger ORDER BY id DESC LIMIT 2`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var units, calls int
			var status sql.NullInt64
			if err := rows.Scan(&units, &calls, &status); err != nil {
				t.Fatal(err)
			}
			if units != c.want || calls != 1 || status.Int64 != 403 {
				t.Errorf("%s/%s: units=%d calls=%d status=%d, want units=%d", c.op, c.category, units, calls, status.Int64, c.want)
			}
		}
		rows.Close()
	}
}

// §1.3: a Finalize with no run uses the run the hold was acquired under; an
// explicit run wins.
func TestFinalizeAttributesHoldRun(t *testing.T) {
	ctx := context.Background()
	l, db := newTestLedger(t)
	r := seedRun(t, db)
	other := seedRun(t, db)

	// Explicit run wins, unscoped stays NULL, then a refusal with no explicit
	// run is attributed to the hold's run (and stops it).
	admitAndFinalize(t, WithRun(ctx, r), l, OpVideosList, sql.NullInt64{Int64: other, Valid: true}, OutcomeSuccess)
	admitAndFinalize(t, ctx, l, OpVideosList, sql.NullInt64{}, OutcomeSuccess)
	admitAndFinalize(t, WithRun(ctx, r), l, OpVideosList, sql.NullInt64{}, OutcomeQuota)

	var got []sql.NullInt64
	rows, err := db.QueryContext(ctx, `SELECT run_id FROM quota_ledger ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v sql.NullInt64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if len(got) != 3 || got[0] != (sql.NullInt64{Int64: other, Valid: true}) || got[1].Valid || got[2] != (sql.NullInt64{Int64: r, Valid: true}) {
		t.Fatalf("run attribution = %+v, want [%d NULL %d]", got, other, r)
	}
	// The bound-read style call (no explicit run) stopped run r.
	if h, err := l.Acquire(WithRun(ctx, r), OpVideosList); err != nil || h != nil {
		t.Fatalf("run not stopped by attributed refusal: %v %v", h, err)
	}
}

// Q6: the default quota day is Pacific; a stored schedule still wins.
func TestDefaultResetIsPacific(t *testing.T) {
	ctx := context.Background()
	l, _ := newTestLedger(t)
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 22, 0, 0, 44, 0, time.UTC), "2026-11-21"},
		{time.Date(2026, 11, 22, 7, 59, 59, 0, time.UTC), "2026-11-21"},
		{time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC), "2026-11-22"},
		{time.Date(2026, 7, 1, 6, 59, 59, 0, time.UTC), "2026-06-30"},
		{time.Date(2026, 7, 1, 7, 0, 0, 0, time.UTC), "2026-07-01"},
	}
	for _, c := range cases {
		got, err := l.bucketDate(ctx, c.at)
		if err != nil || got != c.want {
			t.Errorf("bucketDate(%s) = %q, %v; want %q", c.at.Format(time.RFC3339), got, err, c.want)
		}
	}
	if _, ok, err := l.ResetSchedule(ctx); err != nil || ok {
		t.Fatalf("ResetSchedule reports a stored schedule: %v %v", ok, err)
	}
	next, known, err := l.nextReset(ctx, time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC))
	if err != nil || !known || !next.Equal(time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("nextReset = %s, %v, %v", next, known, err)
	}
	b, err := l.Budget(ctx)
	if err != nil || !b.ResetKnown || b.NextReset.IsZero() {
		t.Fatalf("Budget reset = %+v, %v", b, err)
	}

	if err := l.SetResetSchedule(ctx, "UTC", "00:00"); err != nil {
		t.Fatal(err)
	}
	got, err := l.bucketDate(ctx, time.Date(2026, 11, 22, 0, 0, 44, 0, time.UTC))
	if err != nil || got != "2026-11-22" {
		t.Fatalf("UTC override bucket = %q, %v", got, err)
	}
}

// §6: the stop and the refusal are logged with run context.
func TestRunStopLogLines(t *testing.T) {
	ctx := context.Background()
	buf := &bytes.Buffer{}
	l, db := newTestLedger(t)
	l.logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := seedRun(t, db)
	rctx := WithRun(ctx, r)
	admitAndFinalize(t, rctx, l, OpPlaylistItemsInsert, sql.NullInt64{Int64: r, Valid: true}, OutcomeQuota)
	_, _ = l.Acquire(rctx, OpPlaylistItemsInsert)
	_, _, _ = l.CanAfford(rctx, 1)
	out := buf.String()
	for _, want := range []string{
		`"msg":"youtube quota refused; run dispatch stopped"`,
		`"http_status":403`,
		`"msg":"quota admission refused"`,
		`"msg":"quota pre-flight check failed"`,
		`"cause":"run_dispatch_stop"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %s:\n%s", want, out)
		}
	}
}

func TestWithRunIgnoresInvalidID(t *testing.T) {
	if _, ok := RunFromContext(WithRun(context.Background(), 0)); ok {
		t.Fatal("run 0 must not scope the context")
	}
	if id, ok := RunFromContext(WithRun(context.Background(), 7)); !ok || id != 7 {
		t.Fatalf("RunFromContext = %d, %v", id, ok)
	}
}
