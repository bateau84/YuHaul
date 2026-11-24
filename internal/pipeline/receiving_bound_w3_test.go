package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store/storetest"
)

// Assessment 359587ee, finding ee32de34 (ARS-005 section 6.1.3, W1 and W3): a
// receiving run that started with a VALID binding and then met an authorization
// refusal must route a failed authorization-stop write through W3 exactly as the
// no-binding path does: the integrity stop is stored best-effort, the classified
// storage fault is the run's durable reason, no further provider work happens,
// and the exact work stays queued. Every caller of the receiving settlement
// ends through one boundary (receivingEnding / failClosedAtReceiving).

// runBoundWithAuthorizationRefusal runs the receiving run with a connected
// account whose access expired and cannot be renewed (no refresh credential):
// the binding is valid, and the first metadata read is refused as an
// authorization failure, which makes W1 settle the pass.
func (rf *receivingFixture) runBoundWithAuthorizationRefusal(storedStop ...bool) {
	rf.t.Helper()
	rf.setToken("")
	if len(storedStop) > 0 && storedStop[0] {
		_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
		require.NoError(rf.t, err)
	}
	_, err := rf.db.Exec(`UPDATE run SET status='interrupted', current_phase=? WHERE id=?`, string(PhaseActionExecution), rf.recvRun)
	require.NoError(rf.t, err)
	rf.p.Execute(context.Background(), rf.recvRun)
}

// The control: no fault, the same stimulus. W1 stores the authorization stop and
// both exact receipts are deferred; the run is not ended by W3.
func TestReceivingBoundAuthorizationRefusalSettlesWithoutAFault(t *testing.T) {
	rf := newReceivingFixture(t)
	receipts := rf.claimNew(2)

	rf.runBoundWithAuthorizationRefusal()

	require.Equal(t, "authorization", rf.storedStop())
	for _, r := range receipts {
		rf.requireDeferred(r, "authorization_pending")
	}
	rf.requireNoProviderWork()
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry`))
}

func TestReceivingBoundStopWriteFaultEndsTheRunThroughW3(t *testing.T) {
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	receipts := rf.claimNew(2)
	rf.failAuthorizationStopWrites()

	rf.runBoundWithAuthorizationRefusal()

	rf.requireStopWriteFaultEnding(logs.String(), receipts...)
}

// The same, with the stop transaction failing only at COMMIT (the deferred
// foreign key), the shape of a failing writer that accepted the statement.
func TestReceivingBoundStopCommitFaultEndsTheRunThroughW3(t *testing.T) {
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	receipts := rf.claimNew(2)
	rf.failAuthorizationStopCommit()

	rf.runBoundWithAuthorizationRefusal()

	rf.requireStopWriteFaultEnding(logs.String(), receipts...)
}

// A pure guard veto with a working writer keeps its ordinary ending when bound:
// with the authorization stop already stored, the guarded receipt vetoes its own
// settlement at W1, the peer still settles, and W3 does not raise the integrity
// stop. (With no stop stored yet, bound execution reaches a guarded saved
// decision through the executor, which is a different, unchanged path.)
func TestReceivingBoundPureGuardVetoDoesNotRaiseTheIntegrityStop(t *testing.T) {
	rf := newReceivingFixture(t)
	guarded := rf.claimNew(1)[0]
	rf.decide(guarded)
	rf.guard(guarded)
	peer := rf.claimNew(1)[0]
	guards, decision := rf.scalar(guardDump), rf.decisionDump(guarded.VideoID)

	rf.runBoundWithAuthorizationRefusal(true)

	require.Equal(t, "authorization", rf.storedStop(), "no storage fault: not W3")
	rf.requireDeferred(peer, "authorization_pending")
	require.Equal(t, guards, rf.scalar(guardDump))
	require.Equal(t, decision, rf.decisionDump(guarded.VideoID))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome IS NOT NULL`, rf.recvRun, guarded.VideoID))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry`))
	rf.requireNoProviderWork()
}

// Which errors the bound ending routes through W3 and which keep their own
// handling.
func TestGoverningStorageFault(t *testing.T) {
	ctx := t.Context()
	receipt := queue.RetryGroupReceipt{ReceivingRunID: 1, VideoID: 7}
	storage := fmt.Errorf("settle: %w", sqliteConstraintError(t))
	removed := fmt.Errorf("receipt: %w", queue.ErrWorkRemoved)

	require.True(t, governingStorageFault(ctx, storage), "a classified SQLite fault")
	require.True(t, governingStorageFault(ctx, fmt.Errorf("process retry video 7: %w", storage)))
	refusal, ok := refusalBesideFault(receipt, &withStorageFault{disposition: removed, fault: storage})
	require.True(t, ok)
	require.True(t, governingStorageFault(ctx, refusal), "a refusal beside a fault")
	require.True(t, governingStorageFault(ctx, &withStorageFault{disposition: &receiptGuardedError{videoID: 7}, fault: storage}))

	require.False(t, governingStorageFault(ctx, nil))
	require.False(t, governingStorageFault(ctx, errors.New("plain error")), "not a storage fault")
	require.False(t, governingStorageFault(ctx, auth.ErrQuotaAccounting), "accounting has its own writer")
	require.False(t, governingStorageFault(ctx, &runStoppingError{cause: storage}), "W3 already ran")
	require.False(t, governingStorageFault(ctx, &retryAdmissionRefusal{receipt: receipt, cause: removed}), "a pure refusal")
	require.False(t, governingStorageFault(ctx, fmt.Errorf("retry video 7: %w", &receiptGuardedError{videoID: 7})), "a pure veto")
	require.False(t, governingStorageFault(ctx, fmt.Errorf("stopped: %w", state.ErrPlaylistSendStopped)), "a user Stop")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.False(t, governingStorageFault(cancelled, storage), "a cancelled execution stays resumable")
}

// sqliteConstraintError is a real driver error: a NOT NULL violation.
func sqliteConstraintError(t *testing.T) error {
	t.Helper()
	db := storetest.Open(t)
	_, err := db.Exec(`CREATE TABLE fixture_notnull(a INTEGER NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO fixture_notnull(a) VALUES(NULL)`)
	require.Error(t, err)
	return err
}

// The manual retry is the third caller of the receiving settlement. Its run
// ends through the same W3 boundary when the authorization stop cannot be
// written, and settles normally when it can.
func TestManualRetryBoundStopWriteFaultEndsTheRunThroughW3(t *testing.T) {
	for _, fault := range []bool{false, true} {
		name := "control: no fault"
		if fault {
			name = "stop write refused"
		}
		t.Run(name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			rf.captureLogs()
			ctx := context.Background()
			rf.seq++
			v, err := state.NewMachine(rf.state).DiscoverVideo(ctx, rf.runID, fmt.Sprintf("manual-%d", rf.seq), "fixture-channel")
			require.NoError(t, err)
			item, err := rf.qs.CreateDeferredItem(ctx, &queue.DeferredItem{
				VideoID: v.ID, RunID: rf.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending,
			})
			require.NoError(t, err)
			rf.setToken("")
			if fault {
				rf.failAuthorizationStopWrites()
			}

			err = rf.p.RetryQueueEntry(ctx, item, false)

			runID := rf.scalar(`SELECT id FROM run WHERE id > ? ORDER BY id DESC LIMIT 1`, rf.recvRun)
			stop := rf.scalar(`SELECT COALESCE(dispatch_stop,'') FROM run WHERE id=?`, runID)
			if !fault {
				require.Equal(t, "authorization", stop, "err=%v", err)
				require.Equal(t, "authorization_pending", rf.scalar(`SELECT deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, v.ID))
				return
			}
			require.Error(t, err)
			require.Equal(t, "accounting_or_integrity", stop)
			require.Equal(t, "failed", rf.scalar(`SELECT status FROM run WHERE id=?`, runID))
			require.Equal(t, "diag:v1 phase=unknown operation=account_quota code=sqlite_error sqlite=SQLITE_CONSTRAINT",
				rf.scalar(`SELECT interrupt_reason FROM run WHERE id=?`, runID))
			require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome IS NOT NULL`, runID))
			require.Equal(t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, item))
			require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry`))
		})
	}
}
