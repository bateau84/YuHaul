package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// EndLegacyRunTx (run-initiation ARS-004 §2.1, test T1).

const endReasonText = "diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user"

func legacyRunSQL(t *testing.T, s *Store, runType, status string, safety int) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO run (started_at, run_type, status, mutation_safety_version, current_phase, videos_discovered, interrupt_reason)
		VALUES (?,?,?,?,'discovery',19615,'diag:v1 phase=discovery operation=discover code=timeout')`,
		formatRunTime(timeNow()), runType, status, safety)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func endTx(t *testing.T, s *Store, id int64, reason string) error {
	t.Helper()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	if err := s.EndLegacyRunTx(ctx, tx, id, reason); err != nil {
		return err
	}
	return tx.Commit()
}

func TestEndLegacyRunTxChangesOnlyStatusAndReason(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for _, status := range []string{"running", "interrupted"} {
		id := legacyRunSQL(t, s, "manual", status, 0)
		before, err := s.GetRun(ctx, id)
		require.NoError(t, err)
		require.NoError(t, endTx(t, s, id, endReasonText))
		after, err := s.GetRun(ctx, id)
		require.NoError(t, err)
		want := *before
		want.Status, want.InterruptReason = RunStatusFailed, &[]string{endReasonText}[0]
		require.Equal(t, want, *after, "only status and reason change (%s)", status)
		require.Nil(t, after.CompletedAt, "completed_at stays NULL")
	}
}

func TestEndLegacyRunTxRefusesEveryOtherRow(t *testing.T) {
	s := newStore(t)
	marked := legacyRunSQL(t, s, "manual", "running", 0)
	_, err := s.db.Exec(`UPDATE run SET stop_requested_at='2026-11-10T12:00:00Z' WHERE id=?`, marked)
	require.NoError(t, err)
	failed := legacyRunSQL(t, s, "manual", "failed", 0)
	for name, id := range map[string]int64{
		"version 1":         legacyRunSQL(t, s, "manual", "running", 1),
		"version 1 interr.": legacyRunSQL(t, s, "scheduled", "interrupted", 1),
		"dry run":           legacyRunSQL(t, s, "dry_run", "interrupted", 0),
		"replay":            legacyRunSQL(t, s, "replay", "running", 0),
		"backtest":          legacyRunSQL(t, s, "backtest", "interrupted", 0),
		"failed":            failed,
		"stop marker":       marked,
		"missing":           9999,
	} {
		var before string
		_ = s.db.QueryRow(`SELECT status || '|' || COALESCE(interrupt_reason,'') FROM run WHERE id=?`, id).Scan(&before)
		require.ErrorIs(t, endTx(t, s, id, endReasonText), ErrRunStopNotApplied, name)
		var after string
		_ = s.db.QueryRow(`SELECT status || '|' || COALESCE(interrupt_reason,'') FROM run WHERE id=?`, id).Scan(&after)
		require.Equal(t, before, after, name)
	}
}

func TestEndLegacyRunTxGuardsTheReason(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	id := legacyRunSQL(t, s, "manual", "running", 0)
	require.NoError(t, endTx(t, s, id, "raw error text with a secret"))
	r, err := s.GetRun(ctx, id)
	require.NoError(t, err)
	require.NotContains(t, *r.InterruptReason, "secret", "free prose never reaches the reason column")
}
