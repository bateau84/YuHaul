package pipeline

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/queue"
)

// Review follow-up 35e62dd4, finding 0bfa9e12 (ARS-005 section 6.2): an
// unresolved send guard protects only its own receipt. The receiving run that
// cannot take a binding still settles every other valid receipt as
// deferred/authorization_pending and stores the stop, while the guarded
// receipt keeps its guard, decision, audit and queue row.

// guard records an unresolved send guard for the video of receipt, the shape
// an interrupted real playlist insert leaves.
func (rf *receivingFixture) guard(receipt queue.RetryGroupReceipt) {
	rf.t.Helper()
	_, err := rf.db.Exec(`INSERT INTO playlist_send_guard(video_id,target_playlist_id,action_type,run_id,route_decision_id,authorization_id,prepared_at)
		SELECT video_id, 'PLreceiving', 'playlist_insert', run_id, id, 'opaque', ? FROM route_decision WHERE run_id=? AND video_id=?`,
		"2026-01-01T00:00:00Z", rf.recvRun, receipt.VideoID)
	require.NoError(rf.t, err)
}

const guardDump = `SELECT COALESCE(group_concat(video_id||target_playlist_id||run_id||route_decision_id||authorization_id||prepared_at),'') FROM playlist_send_guard`

// requireGuardedKept checks that the guarded receipt's disposition was not
// touched: no run outcome, same guard, decision and audit, same queue row (back
// in the queue through the run's retention, not deferred by the settlement),
// no action and no Errors entry.
func (rf *receivingFixture) requireGuardedKept(receipt queue.RetryGroupReceipt, guards, decision string) {
	rf.t.Helper()
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND video_id=? AND final_outcome IS NOT NULL`, rf.recvRun, receipt.VideoID),
		"the guarded receipt gets no deferral")
	require.Equal(rf.t, guards, rf.scalar(guardDump), "the guard is untouched")
	require.Equal(rf.t, decision, rf.decisionDump(receipt.VideoID), "decision and audit are untouched")
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM executed_action WHERE video_id=?`, receipt.VideoID))
	require.Equal(rf.t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry WHERE video_id=?`, receipt.VideoID))
	require.Equal(rf.t, "1", rf.scalar(`SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, receipt.VideoID), "its exact queue row, no second one")
	require.Equal(rf.t, "quota_exhausted", rf.scalar(`SELECT deferral_reason FROM deferred_item WHERE id=?`, receipt.Deferred[0].ID), "its original reason")
}

func TestReceivingNoBindingGuardedReceiptDoesNotBlockItsPeers(t *testing.T) {
	for _, c := range []struct {
		name         string
		guardedFirst bool
		storedStop   bool
	}{
		{name: "guarded first, restored stop", guardedFirst: true, storedStop: true},
		{name: "guarded first, newly detected refusal", guardedFirst: true},
		{name: "guarded last, restored stop", storedStop: true},
		{name: "guarded last, newly detected refusal"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			logs := rf.captureLogs()
			var guarded queue.RetryGroupReceipt
			var ready, notReady queue.RetryGroupReceipt
			arrangeGuarded := func() {
				guarded = rf.claimNew(1)[0]
				rf.decide(guarded) // a saved evaluation whose send was in flight
				rf.guard(guarded)
			}
			if c.guardedFirst {
				arrangeGuarded()
			}
			ready = rf.claimNew(1)[0]
			seedCurrentMetadata(t, rf.db, rf.videoText(ready))
			notReady = rf.claimNew(1)[0] // no Store A row: settles through readiness
			if !c.guardedFirst {
				arrangeGuarded()
			}
			guards := rf.scalar(guardDump)
			decision := rf.decisionDump(guarded.VideoID)
			require.NotEmpty(t, decision)

			rf.runWithoutBinding(c.storedStop)

			require.Equal(t, "failed", rf.scalar(`SELECT status FROM run WHERE id=?`, rf.recvRun), "the run still ends and retains the guarded work")
			require.Contains(t, logs.String(), "unresolved send guard")
			require.Contains(t, logs.String(), "mutation_application_uncertain")
			rf.requireSettledLocally(ready)
			rf.requireSettledLocally(notReady)
			rf.requireGuardedKept(guarded, guards, decision)
			require.Equal(t, "pending", rf.scalar(`SELECT status FROM deferred_item WHERE id=?`, guarded.Deferred[0].ID),
				"retained by the run's sweep, not replayed")
		})
	}
}

// Only a guarded receipt: nothing else settles, but the authorization stop is
// still a fact about the run and is stored, whatever the order of events.
func TestReceivingNoBindingOnlyGuardedReceiptStillStoresTheStop(t *testing.T) {
	rf := newReceivingFixture(t)
	guarded := rf.claimNew(1)[0]
	rf.decide(guarded)
	rf.guard(guarded)
	guards, decision := rf.scalar(guardDump), rf.decisionDump(guarded.VideoID)

	rf.runWithoutBinding(false)

	require.Equal(t, "authorization", rf.storedStop())
	rf.requireGuardedKept(guarded, guards, decision)
	rf.requireNoProviderWork()
}

// Accounting still outranks authorization, with a guard in the mix: under a
// stored accounting_or_integrity stop nothing settles as an authorization
// deferral, the higher stop is never lowered, and the guard is untouched.
func TestReceivingNoBindingGuardedMixStillFailsClosedUnderAnAccountingStop(t *testing.T) {
	rf := newReceivingFixture(t)
	guarded := rf.claimNew(1)[0]
	rf.decide(guarded)
	rf.guard(guarded)
	peer := rf.claimNew(1)[0]
	seedCurrentMetadata(t, rf.db, rf.videoText(peer))
	guards, decision := rf.scalar(guardDump), rf.decisionDump(guarded.VideoID)
	_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='accounting_or_integrity' WHERE id=?`, rf.recvRun)
	require.NoError(t, err)

	rf.runWithoutBinding(false)

	require.Equal(t, "accounting_or_integrity", rf.storedStop())
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND deferred_reason='authorization_pending'`, rf.recvRun))
	rf.requireGuardedKept(guarded, guards, decision)
	require.Equal(t, "0", rf.scalar(`SELECT COUNT(*) FROM error_entry`))
	rf.requireNoProviderWork()
}
