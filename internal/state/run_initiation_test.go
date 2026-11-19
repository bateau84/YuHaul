package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"

	"github.com/stretchr/testify/require"
)

// M2/M3/M5 of run-initiation ARS-001 §8.1 for initiated_by and interrupt_cause
// (the published-range columns are not part of this change).

func insertRunSQL(t *testing.T, s *Store, runType, status string, initiatedBy, cause any) (int64, error) {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO run (started_at, run_type, status, initiated_by, interrupt_cause) VALUES (?,?,?,?,?)`,
		formatRunTime(time.Now()), runType, status, initiatedBy, cause)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id, nil
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(openTestDB(t))
	require.NoError(t, err)
	return s
}

// M2: the initiator is validated against the run type at insert and never
// changes afterwards.
func TestM2_InitiatorGuard(t *testing.T) {
	s := newStore(t)

	for _, ok := range []struct{ runType, by string }{
		{"manual", "run_now"}, {"manual", "queue_retry"}, {"scheduled", "schedule"},
	} {
		_, err := insertRunSQL(t, s, ok.runType, "running", ok.by, nil)
		require.NoError(t, err, "%s/%s", ok.runType, ok.by)
	}
	for _, rt := range []string{"manual", "scheduled", "dry_run", "replay", "backtest"} {
		_, err := insertRunSQL(t, s, rt, "running", nil, nil)
		require.NoError(t, err, "a NULL initiator is always allowed (%s)", rt)
	}

	for _, bad := range []struct{ runType, by string }{
		{"scheduled", "run_now"}, {"scheduled", "queue_retry"}, {"manual", "schedule"},
		{"manual", "operator"}, {"dry_run", "run_now"}, {"backtest", "schedule"}, {"manual", ""},
	} {
		_, err := insertRunSQL(t, s, bad.runType, "running", bad.by, nil)
		require.ErrorContains(t, err, "invalid run initiator", "%s/%q", bad.runType, bad.by)
	}

	runNow, err := insertRunSQL(t, s, "manual", "running", "run_now", nil)
	require.NoError(t, err)
	legacy, err := insertRunSQL(t, s, "manual", "running", nil, nil)
	require.NoError(t, err)
	for _, update := range []struct {
		name, query string
		id          int64
	}{
		{"change", `UPDATE run SET initiated_by='queue_retry' WHERE id=?`, runNow},
		{"clear", `UPDATE run SET initiated_by=NULL WHERE id=?`, runNow},
		{"claim a pre-024 row", `UPDATE run SET initiated_by='run_now' WHERE id=?`, legacy},
	} {
		_, err := s.db.Exec(update.query, update.id)
		require.ErrorContains(t, err, "run initiator is immutable", update.name)
	}
	_, err = s.db.Exec(`UPDATE run SET status='completed' WHERE id=?`, runNow)
	require.NoError(t, err, "unrelated updates stay possible")
	_, err = s.db.Exec(`UPDATE run SET initiated_by='run_now' WHERE id=?`, runNow)
	require.NoError(t, err, "an assignment of the same value is not a change")
}

// M3: the cause is a closed set and only exists on an interrupted row; the
// store's writers keep it consistent with the status.
func TestM3_InterruptCauseGuard(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	for _, cause := range []string{"time_limit", "shutdown"} {
		_, err := insertRunSQL(t, s, "manual", "interrupted", "run_now", cause)
		require.NoError(t, err, cause)
	}
	for _, bad := range []struct{ status, cause string }{
		{"interrupted", "crash"}, {"interrupted", ""}, {"running", "time_limit"},
		{"completed", "shutdown"}, {"failed", "time_limit"},
	} {
		_, err := insertRunSQL(t, s, "manual", bad.status, "run_now", bad.cause)
		require.ErrorContains(t, err, "invalid run interrupt cause", "%s/%q", bad.status, bad.cause)
	}

	id, err := insertRunSQL(t, s, "manual", "running", "run_now", nil)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE run SET interrupt_cause='time_limit' WHERE id=?`, id)
	require.ErrorContains(t, err, "invalid run interrupt cause", "a running row cannot carry a cause")
	_, err = s.db.Exec(`UPDATE run SET status='interrupted', interrupt_cause='crash' WHERE id=?`, id)
	require.ErrorContains(t, err, "invalid run interrupt cause")

	// Writers: cause together with status, cleared by every other writer.
	require.NoError(t, s.InterruptRunWithCause(ctx, id, "diag:v1 phase=action_execution operation=load_run code=timeout", InterruptCauseTimeLimit))
	r, err := s.GetRun(ctx, id)
	require.NoError(t, err)
	require.Equal(t, RunStatusInterrupted, r.Status)
	require.NotNil(t, r.InterruptCause)
	require.Equal(t, "time_limit", *r.InterruptCause)

	require.NoError(t, s.ResumeRun(ctx, id))
	r, err = s.GetRun(ctx, id)
	require.NoError(t, err)
	require.Nil(t, r.InterruptCause, "ResumeRun clears the cause with the reason")
	require.Nil(t, r.InterruptReason)

	require.NoError(t, s.InterruptRunWithCause(ctx, id, "diag:v1 phase=action_execution operation=load_run code=canceled", InterruptCauseShutdown))
	require.NoError(t, s.InterruptRun(ctx, id, "restart"))
	r, err = s.GetRun(ctx, id)
	require.NoError(t, err)
	require.Equal(t, RunStatusInterrupted, r.Status)
	require.Nil(t, r.InterruptCause, "InterruptRun does not know a cause and must clear a previous one")

	require.ErrorIs(t, s.InterruptRunWithCause(ctx, 9999, "x", InterruptCauseTimeLimit), ErrNotFound)
	require.Error(t, s.InterruptRunWithCause(ctx, id, "x", "bogus"), "an unknown cause is rejected by the schema")
}

// M5: reads round-trip both fields; the public JSON exposes them as the
// snake_case closed-set values (null when unproven or legacy).
func TestM5_ProjectionRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	limited, err := insertRunSQL(t, s, "manual", "interrupted", "run_now", "time_limit")
	require.NoError(t, err)
	retry, err := insertRunSQL(t, s, "manual", "interrupted", "queue_retry", nil)
	require.NoError(t, err)
	legacy, err := insertRunSQL(t, s, "manual", "completed", nil, nil)
	require.NoError(t, err)

	want := map[int64][2]string{limited: {"run_now", "time_limit"}, retry: {"queue_retry", "<nil>"}, legacy: {"<nil>", "<nil>"}}
	check := func(source string, r Run) {
		w, ok := want[r.ID]
		if !ok {
			return
		}
		got := [2]string{ptrText(r.InitiatedBy), ptrText(r.InterruptCause)}
		require.Equal(t, w, got, "%s run %d", source, r.ID)
	}
	runs, err := s.ListRuns(ctx)
	require.NoError(t, err)
	for _, r := range runs {
		check("ListRuns", r)
	}
	for id := range want {
		r, err := s.GetRun(ctx, id)
		require.NoError(t, err)
		check("GetRun", *r)
	}
	// ListInterruptedRuns only lists guarded rows; the seeded rows are unguarded.
	interrupted, err := s.ListInterruptedRuns(ctx)
	require.NoError(t, err)
	require.Empty(t, interrupted)

	r, err := s.GetRun(ctx, limited)
	require.NoError(t, err)
	body, err := json.Marshal(r)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(body, &fields))
	require.Equal(t, "run_now", fields["initiated_by"])
	require.Equal(t, "time_limit", fields["interrupt_cause"])
	for _, goName := range []string{"InitiatedBy", "InterruptCause"} {
		require.NotContains(t, fields, goName, "Go field name leaked into the public JSON")
	}

	for id, want := range map[int64][2]any{retry: {"queue_retry", nil}, legacy: {nil, nil}} {
		r, err := s.GetRun(ctx, id)
		require.NoError(t, err)
		body, err := json.Marshal(r)
		require.NoError(t, err)
		var f map[string]any
		require.NoError(t, json.Unmarshal(body, &f))
		require.Contains(t, f, "initiated_by", "key must stay present")
		require.Contains(t, f, "interrupt_cause", "key must stay present")
		require.Equal(t, want[0], f["initiated_by"])
		require.Equal(t, want[1], f["interrupt_cause"])
	}
}

func ptrText(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// Discovery ADR-001 D6: discovery_incomplete is derived from the ledger, true
// only for a production run with a permanent 404 playlist_items_list row.
func TestRunDiscoveryIncompleteFromLedger(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ledger := quota.New(s.db)
	record := func(runID int64, op quota.Operation, cat quota.OutcomeCategory, status int64) {
		t.Helper()
		require.NoError(t, ledger.RecordAttempt(ctx, sql.NullInt64{Int64: runID, Valid: true}, op, time.Now(), cat, sql.NullInt64{Int64: status, Valid: true}))
	}
	mk := func(runType string) int64 {
		t.Helper()
		id, err := insertRunSQL(t, s, runType, "completed", nil, nil)
		require.NoError(t, err)
		return id
	}

	skipped := mk("manual")
	record(skipped, quota.OpPlaylistItemsList, quota.OutcomePermanent, 404)
	clean := mk("manual")
	record(clean, quota.OpPlaylistItemsList, quota.OutcomeSuccess, 200)
	otherOp := mk("scheduled")
	record(otherOp, quota.OpVideosList, quota.OutcomePermanent, 404)
	other403 := mk("scheduled")
	record(other403, quota.OpPlaylistItemsList, quota.OutcomePermanent, 403)
	dry := mk("dry_run")
	record(dry, quota.OpPlaylistItemsList, quota.OutcomePermanent, 404)
	untouched := mk("manual")

	for id, want := range map[int64]bool{skipped: true, clean: false, otherOp: false, other403: false, dry: false, untouched: false} {
		r, err := s.GetRun(ctx, id)
		require.NoError(t, err)
		require.Equal(t, want, r.DiscoveryIncomplete, "GetRun run %d", id)
	}
	runs, err := s.ListRuns(ctx)
	require.NoError(t, err)
	for _, r := range runs {
		require.Equal(t, r.ID == skipped, r.DiscoveryIncomplete, "ListRuns run %d", r.ID)
	}
	b, err := json.Marshal(mustGet(t, s, skipped))
	require.NoError(t, err)
	require.Contains(t, string(b), `"discovery_incomplete":true`)
}

func mustGet(t *testing.T, s *Store, id int64) *Run {
	t.Helper()
	r, err := s.GetRun(context.Background(), id)
	require.NoError(t, err)
	return r
}
