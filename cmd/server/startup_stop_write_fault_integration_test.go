//go:build integration

package main

// Assessment 44b4cee1, finding 994c674a, through the real server composition
// (ARS-005 section 6.1.3, writers W1 and W3). Startup recovery finds live
// receipts and no connected account, and the database refuses every write of
// dispatch_stop='authorization' (it still accepts accounting_or_integrity).
// The run must end with the classified storage fault as its durable reason and
// the stored integrity stop, keep the exact work, and make no provider request,
// debit, action or Errors row. A guard veto beside the fault must not turn it
// into a receipt-local disposition.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const stopWriteFaultReason = "diag:v1 phase=unknown operation=account_quota code=sqlite_error sqlite=SQLITE_CONSTRAINT"

func refuseAuthorizationStopWrites(t *testing.T, dbPath string) {
	t.Helper()
	db := openReadDB(t, dbPath)
	_, err := db.Exec(`CREATE TRIGGER refuse_authorization_stop BEFORE UPDATE OF dispatch_stop ON run
		WHEN NEW.dispatch_stop = 'authorization'
		BEGIN SELECT RAISE(ABORT, 'injected stop write fault'); END`)
	require.NoError(t, err)
}

func TestStartupRecoveryStopWriteFaultEndsTheRunWithTheStorageFault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seed := seedClaimedWithoutAccount(t, dbPath, true, false)
	refuseAuthorizationStopWrites(t, dbPath)
	yt := newFakeYouTube(t, "V1")
	db := openReadDB(t, dbPath)

	srv := startServer(t, dbPath, yt.srv.URL)
	eventually(t, runDoneBudget, "startup recovery to finish", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })
	logs := srv.out.String()

	require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, seed.runID), logs)
	require.Equal(t, stopWriteFaultReason, scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, seed.runID),
		"the storage fault, not the binding error and not unknown_error\n%s", logs)
	require.Equal(t, "accounting_or_integrity", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, seed.runID))
	require.Equal(t, 0, scalar[int](t, db, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome IS NOT NULL`, seed.runID))
	require.Equal(t, "pending/quota_exhausted/"+seed.itemCreatedAt, scalar[string](t, db,
		`SELECT status||'/'||deferral_reason||'/'||created_at FROM deferred_item WHERE id=?`, seed.itemID), "the exact work is retained")
	require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM error_entry`))
	require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM executed_action`))
	require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM quota_ledger WHERE run_id=?`, seed.runID))
	require.Zero(t, yt.requestCount(), "no provider request\n%s", logs)
	srv.sigterm()
}

func TestStartupRecoveryGuardVetoPlusStopWriteFaultFailsClosed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
	seed := seedGuardedAndPeer(t, dbPath, false)
	refuseAuthorizationStopWrites(t, dbPath)
	yt := newFakeYouTube(t, "V1", "V2")
	db := openReadDB(t, dbPath)
	const guardRows = `SELECT group_concat(video_id||target_playlist_id||run_id||route_decision_id||authorization_id||prepared_at) FROM playlist_send_guard`
	const decisionRows = `SELECT group_concat(id||':'||route_id||':'||decision) FROM route_decision WHERE video_id=?`
	guards := scalar[string](t, db, guardRows)
	decision := scalar[string](t, db, decisionRows, seed.guardedVideo)

	srv := startServer(t, dbPath, yt.srv.URL)
	eventually(t, runDoneBudget, "startup recovery to finish", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })
	logs := srv.out.String()

	require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, seed.runID), logs)
	require.Equal(t, stopWriteFaultReason, scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, seed.runID), logs)
	require.Equal(t, "accounting_or_integrity", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, seed.runID))
	require.Equal(t, 0, scalar[int](t, db, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome IS NOT NULL`, seed.runID),
		"neither the guarded receipt nor its peer is claimed as settled")
	for _, item := range []int64{seed.guardedItem, seed.peerItem} {
		require.Equal(t, "pending", scalar[string](t, db, `SELECT status FROM deferred_item WHERE id=?`, item), "the exact work is retained")
	}
	require.Equal(t, guards, scalar[string](t, db, guardRows), "the guard is untouched")
	require.Equal(t, decision, scalar[string](t, db, decisionRows, seed.guardedVideo), "the saved decision is untouched")
	require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM error_entry`))
	require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM executed_action`))
	require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM quota_ledger WHERE run_id=?`, seed.runID))
	require.Zero(t, yt.requestCount(), "no provider request\n%s", logs)
	srv.sigterm()
}
