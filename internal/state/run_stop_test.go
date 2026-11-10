package state

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Store backstops for a stop-marked run (run-initiation ARS-003 §3.2) and
// StopRunTx (§3.3).

func markedRun(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := insertRunSQL(t, s, "manual", "running", "run_now", nil)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE run SET stop_requested_at='2026-11-10T12:00:00Z' WHERE id=?`, id)
	require.NoError(t, err)
	return id
}

func TestMarkedRunRefusesEveryOtherTerminalWrite(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	id := markedRun(t, s)
	for name, err := range map[string]error{
		"CompleteRun":           s.CompleteRun(ctx, id, 1, 0),
		"FailRun":               s.FailRun(ctx, id, "x", 1, 0),
		"InterruptRun":          s.InterruptRun(ctx, id, "x"),
		"InterruptRunWithCause": s.InterruptRunWithCause(ctx, id, "x", InterruptCauseShutdown),
	} {
		require.True(t, errors.Is(err, ErrNotFound), "%s: %v", name, err)
	}
	r, err := s.GetRun(ctx, id)
	require.NoError(t, err)
	require.Equal(t, RunStatusRunning, r.Status)
	require.NotNil(t, r.StopRequestedAt)
}

func TestStopRunTxWritesTheFinalStoppedState(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	id := markedRun(t, s)
	started := time.Now().UTC().Add(-90 * time.Second)
	_, err := s.db.Exec(`UPDATE run SET started_at=? WHERE id=?`, formatRunTime(started), id)
	require.NoError(t, err)

	write := func(id int64) error {
		tx, err := s.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		if err := s.StopRunTx(ctx, tx, id, "diag:v1 phase=unknown operation=stop_run code=stopped_by_user", 7, time.Now().UTC()); err != nil {
			return err
		}
		return tx.Commit()
	}
	require.NoError(t, write(id))
	r, err := s.GetRun(ctx, id)
	require.NoError(t, err)
	require.Equal(t, RunStatusStopped, r.Status)
	require.Equal(t, 7, r.QuotaCost)
	require.Nil(t, r.InterruptCause)
	require.NotNil(t, r.CompletedAt)
	require.NotNil(t, r.DurationSeconds)
	require.InDelta(t, 90, *r.DurationSeconds, 5)

	// Final: a second write, and a write to an unmarked row, change nothing.
	require.ErrorIs(t, write(id), ErrRunStopNotApplied)
	unmarked, err := insertRunSQL(t, s, "manual", "running", "run_now", nil)
	require.NoError(t, err)
	require.ErrorIs(t, write(unmarked), ErrRunStopNotApplied)
	var status string
	require.NoError(t, s.db.QueryRow(`SELECT status FROM run WHERE id=?`, unmarked).Scan(&status))
	require.Equal(t, "running", status)
	var stopped sql.NullString
	require.NoError(t, s.db.QueryRow(`SELECT status FROM run WHERE id=?`, id).Scan(&stopped))
	require.Equal(t, "stopped", stopped.String)
}
