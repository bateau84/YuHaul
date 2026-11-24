package pipeline

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
)

// Assessment 44b4cee1, finding 994c674a (ARS-005 section 6.1.3, writers W1 and
// W3): when the execution has no binding and the authorization stop or its
// settlement cannot be written, the run must not finish with only the binding
// error. It keeps the classified storage fault as its durable reason, stops
// dispatch, attempts the stored accounting_or_integrity stop, retains the exact
// work and makes no request. A pure guard veto is the one thing the pass
// continues past.

// failAuthorizationStopWrites makes the database refuse every write of
// dispatch_stop='authorization', as a failing writer would, while still
// permitting accounting_or_integrity (the W3 stop).
func (rf *receivingFixture) failAuthorizationStopWrites() {
	rf.t.Helper()
	_, err := rf.db.Exec(`CREATE TRIGGER refuse_authorization_stop BEFORE UPDATE OF dispatch_stop ON run
		WHEN NEW.dispatch_stop = 'authorization'
		BEGIN SELECT RAISE(ABORT, 'injected stop write fault'); END`)
	require.NoError(rf.t, err)
}

// requireStopWriteFaultEnding checks everything a stop-writer fault must leave:
// the run's durable reason and stored stop, no fabricated outcome, no request,
// debit, action or Errors row, and every exact queue row back in the queue.
func (rf *receivingFixture) requireStopWriteFaultEnding(logs string, receipts ...queue.RetryGroupReceipt) {
	rf.t.Helper()
	require.Equal(rf.t, "failed", rf.scalar(`SELECT status FROM run WHERE id=?`, rf.recvRun))
	require.Equal(rf.t, "diag:v1 phase=unknown operation=account_quota code=sqlite_error sqlite=SQLITE_CONSTRAINT",
		rf.scalar(`SELECT interrupt_reason FROM run WHERE id=?`, rf.recvRun),
		"the registered safe storage reason, not the binding error and not unknown_error\n%s", logs)
	require.Equal(rf.t, "accounting_or_integrity", rf.storedStop(), "W3 stored the integrity stop; the authorization stop could not be written")
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome IS NOT NULL`, rf.recvRun),
		"no settlement is claimed as committed")
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action`))
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry`))
	for _, r := range receipts {
		require.Equal(rf.t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, r.Deferred[0].ID), "the exact work is retained")
		require.Equal(rf.t, "quota_exhausted", rf.scalar(`SELECT deferral_reason FROM deferred_item WHERE id=?`, r.Deferred[0].ID))
		require.Equal(rf.t, "1", rf.scalar(`SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, r.VideoID))
	}
	rf.requireNoProviderWork()
}

// The direct case: nothing is guarded, the first settlement's stop write fails.
func TestReceivingNoBindingStopWriteFaultEndsTheRunWithTheStorageFault(t *testing.T) {
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	ready := rf.claimNew(1)[0]
	seedCurrentMetadata(t, rf.db, rf.videoText(ready))
	notReady := rf.claimNew(1)[0]
	rf.failAuthorizationStopWrites()

	rf.runWithoutBinding(false)

	rf.requireStopWriteFaultEnding(logs.String(), ready, notReady)
	require.Contains(t, logs.String(), "receiving settlement without a binding failed")
}

// The compound case: a guarded receipt vetoes its settlement and the stop write
// that follows the veto fails too. The storage fault governs, whichever order
// the receipts are in; the pass does not continue past it as if it were a veto.
func TestReceivingNoBindingGuardVetoPlusStopWriteFaultFailsClosed(t *testing.T) {
	for _, guardedFirst := range []bool{true, false} {
		name := "guarded last"
		if guardedFirst {
			name = "guarded first"
		}
		t.Run(name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			logs := rf.captureLogs()
			var guarded, peer queue.RetryGroupReceipt
			arrange := func() {
				guarded = rf.claimNew(1)[0]
				rf.decide(guarded)
				rf.guard(guarded)
			}
			if guardedFirst {
				arrange()
			}
			peer = rf.claimNew(1)[0]
			seedCurrentMetadata(t, rf.db, rf.videoText(peer))
			if !guardedFirst {
				arrange()
			}
			guards, decision := rf.scalar(guardDump), rf.decisionDump(guarded.VideoID)
			rf.failAuthorizationStopWrites()

			rf.runWithoutBinding(false)

			rf.requireStopWriteFaultEnding(logs.String(), guarded, peer)
			require.Equal(t, guards, rf.scalar(guardDump), "the guard is untouched")
			require.Equal(t, decision, rf.decisionDump(guarded.VideoID), "decision and audit are untouched")
		})
	}
}

// Without a fault the veto stays local (the mixed-guard behavior), and a veto
// joined with another fault is never mistaken for it.
func TestIsPureGuardVeto(t *testing.T) {
	veto := &receiptGuardedError{videoID: 7}
	require.True(t, isPureGuardVeto(veto))
	require.True(t, isPureGuardVeto(fmt.Errorf("retry video 7: %w", veto)), "a single wrap is still the veto")
	require.False(t, isPureGuardVeto(errors.Join(veto, errors.New("stop write failed"))), "a join carries another fault")
	require.False(t, isPureGuardVeto(errors.New("stop write failed")))
	require.False(t, isPureGuardVeto(nil))
}
