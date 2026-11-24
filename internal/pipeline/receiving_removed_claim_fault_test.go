package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
)

// Assessment d3160532, finding e1e3351e (ARS-005 section 6.1.3, writers W1 and
// W3; authorization ARS-001 retained-disposition rule 7). The no-binding
// settlement has one rule: a pure refusal (removed or lost claim), a pure guard
// veto and a cancellation keep their ordinary endings, and a real storage fault
// that accompanies any of them governs the ending. These tests give the
// reviewer's ordering: the discard commits after the pass validated the receipt
// and before W1 revalidates it, then the standalone stop transaction fails at
// COMMIT. They also pin the pure controls, so the rule is not narrowed to one
// branch.

// discardOnFirstDecision commits the same status change as the queue's discard
// (DiscardDeferredItem) in the transaction of the first route decision of a
// video: after the pass validated the receipt, before W1 revalidates it.
func (rf *receivingFixture) discardOnFirstDecision() {
	rf.t.Helper()
	_, err := rf.db.Exec(`CREATE TRIGGER discard_on_first_decision AFTER INSERT ON route_decision
		BEGIN UPDATE deferred_item SET status='discarded' WHERE video_id=NEW.video_id AND status='retrying'; END`)
	require.NoError(rf.t, err)
}

// failAuthorizationStopCommit lets the UPDATE of dispatch_stop='authorization'
// succeed and makes the transaction that carries it fail at COMMIT (a deferred
// foreign key), as a failing standalone stop writer does. A transaction that
// rolls back for another reason (removal, veto) never reaches the failure.
func (rf *receivingFixture) failAuthorizationStopCommit() {
	rf.t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE fixture_parent(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE fixture_child(parent_id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER fail_authorization_stop_commit AFTER UPDATE OF dispatch_stop ON run WHEN NEW.dispatch_stop='authorization'
			BEGIN INSERT INTO fixture_child(parent_id) VALUES(999); END`,
	} {
		_, err := rf.db.Exec(stmt)
		require.NoError(rf.t, err, stmt)
	}
}

// removedClaim is a locally ready matching receipt followed by a peer.
func (rf *receivingFixture) removedClaimAndPeer() (target, peer queue.RetryGroupReceipt) {
	rf.t.Helper()
	target = rf.claimNew(1)[0]
	seedCurrentMetadata(rf.t, rf.db, rf.videoText(target))
	peer = rf.claimNew(1)[0]
	seedCurrentMetadata(rf.t, rf.db, rf.videoText(peer))
	return target, peer
}

// requireRemovedRowKept checks the removed row is still the removed row: not
// recreated, not resurrected, no second queue row, no outcome, no Errors entry.
func (rf *receivingFixture) requireRemovedRowKept(r queue.RetryGroupReceipt) {
	rf.t.Helper()
	require.Equal(rf.t, "discarded", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, r.Deferred[0].ID))
	require.Equal(rf.t, "1", rf.scalar(`SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, r.VideoID))
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry WHERE video_id=?`, r.VideoID))
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome IS NOT NULL`, rf.recvRun, r.VideoID))
}

func TestReceivingNoBindingRemovedClaimPlusStopCommitFaultFailsClosed(t *testing.T) {
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	target, peer := rf.removedClaimAndPeer()
	rf.discardOnFirstDecision()
	rf.failAuthorizationStopCommit()

	rf.runWithoutBinding(false)

	require.Equal(t, "failed", rf.scalar(`SELECT status FROM run WHERE id=?`, rf.recvRun))
	require.Equal(t, "diag:v1 phase=unknown operation=account_quota code=sqlite_error sqlite=SQLITE_CONSTRAINT",
		rf.scalar(`SELECT interrupt_reason FROM run WHERE id=?`, rf.recvRun),
		"the storage fault, not the removal and not the binding error\n%s", logs.String())
	require.Equal(t, "accounting_or_integrity", rf.storedStop())
	rf.requireRemovedRowKept(target)
	require.Equal(t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, peer.Deferred[0].ID), "the peer's exact work is retained")
	require.Equal(t, "quota_exhausted", rf.scalar(`SELECT deferral_reason FROM deferred_item WHERE id=?`, peer.Deferred[0].ID))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome IS NOT NULL`, rf.recvRun), "no settlement is claimed")
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action`))
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry`))
	rf.requireNoProviderWork()
}

// Pure removal keeps its ordinary ending: the stop is stored on its own, the
// pass ends at the refusal, the removed row stays removed, and the run's reason
// is not a storage fault.
func TestReceivingNoBindingPureRemovedClaimKeepsItsOrdinaryEnding(t *testing.T) {
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	target, peer := rf.removedClaimAndPeer()
	rf.discardOnFirstDecision()

	rf.runWithoutBinding(false)

	require.Equal(t, "failed", rf.scalar(`SELECT status FROM run WHERE id=?`, rf.recvRun))
	require.NotContains(t, rf.scalar(`SELECT interrupt_reason FROM run WHERE id=?`, rf.recvRun), "sqlite_error", logs.String())
	require.Equal(t, "authorization", rf.storedStop(), "the stop is a fact about the run and is stored on its own")
	rf.requireRemovedRowKept(target)
	require.Equal(t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, peer.Deferred[0].ID), "the pass does not continue past a real refusal; the peer is retained")
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome IS NOT NULL`, rf.recvRun, peer.VideoID))
	rf.requireNoProviderWork()
}

// A guard veto whose own standalone stop commit fails: the reviewer's second
// writer point, reached only after the guarded transaction rolled back.
func TestReceivingNoBindingGuardVetoPlusStopCommitFaultFailsClosed(t *testing.T) {
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	guarded := rf.claimNew(1)[0]
	rf.decide(guarded)
	rf.guard(guarded)
	peer := rf.claimNew(1)[0]
	seedCurrentMetadata(t, rf.db, rf.videoText(peer))
	guards, decision := rf.scalar(guardDump), rf.decisionDump(guarded.VideoID)
	rf.failAuthorizationStopCommit()

	rf.runWithoutBinding(false)

	rf.requireStopWriteFaultEnding(logs.String(), guarded, peer)
	require.Equal(t, guards, rf.scalar(guardDump))
	require.Equal(t, decision, rf.decisionDump(guarded.VideoID))
}

// The distinction itself, at the classification seam: a disposition beside a
// fault is governed by the fault and never reads as the pure disposition.
func TestSettlementFaultClassification(t *testing.T) {
	ctx := t.Context()
	fault := errors.New("stop write failed")
	veto := &receiptGuardedError{videoID: 7}
	removed := fmt.Errorf("receipt: %w", queue.ErrWorkRemoved)
	receipt := queue.RetryGroupReceipt{ReceivingRunID: 1, VideoID: 7}

	// Pure dispositions are not governing.
	require.NoError(t, governingSettlementFault(ctx, removed))
	require.NoError(t, governingSettlementFault(ctx, &retryAdmissionRefusal{receipt: receipt, cause: removed}))
	require.NoError(t, governingSettlementFault(ctx, fmt.Errorf("retry video 7: %w", veto)))
	require.NoError(t, governingSettlementFault(ctx, nil))
	// A real fault is.
	require.Error(t, governingSettlementFault(ctx, fault))

	// Beside a fault, each disposition yields to it.
	for name, disposition := range map[string]error{"removal": removed, "guard veto": veto} {
		beside := &withStorageFault{disposition: disposition, fault: fault}
		require.Error(t, governingSettlementFault(ctx, beside), name)
		require.False(t, isRetryAdmissionRefusal(beside), name+": the chain exposes only the fault")
		require.False(t, isPureGuardVeto(beside), name)
		require.ErrorIs(t, beside, fault, name)
	}

	// The removal keeps its identity for protected retention, with the fault.
	refusal, ok := refusalBesideFault(receipt, &withStorageFault{disposition: removed, fault: fault})
	require.True(t, ok)
	var carried *retryAdmissionRefusal
	require.ErrorAs(t, refusal, &carried)
	require.Equal(t, int64(7), carried.protectedVideoID(), "the removed row stays protected")
	require.ErrorIs(t, refusal, fault)
	require.NotErrorIs(t, refusal, queue.ErrWorkRemoved, "so withIntegrityCode cannot relabel the run reason")
	require.Error(t, governingSettlementFault(ctx, refusal))
	// A veto beside a fault has no refusal to protect.
	_, ok = refusalBesideFault(receipt, &withStorageFault{disposition: veto, fault: fault})
	require.False(t, ok)

	// A cancelled execution is not governing for a pure refusal or veto.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.NoError(t, governingSettlementFault(cancelled, removed))
}
