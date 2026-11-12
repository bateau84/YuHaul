package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
)

func guardFixture(t *testing.T) (*Store, PlaylistSendClaim) {
	t.Helper()
	db := openTestDB(t)
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = db.Exec(`INSERT INTO config_version(id,snapshot) VALUES(1,?)`, `{"routes":[{"id":1,"actions":[{"action_type":"playlist_insert","target_playlist_id":"PL exact"}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusRunning, ConfigVersionID: ptrInt64(1), ActionAuthorityVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE run SET mutation_safety_version=1 WHERE id=?`, run); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO route(name,evaluation_order,condition_composition) VALUES('r',1,'AND')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO video(video_id,channel_id,processing_state) VALUES('v','c','evaluated')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(1,?,1,1,'matched')`, run)
	if err != nil {
		t.Fatal(err)
	}
	return s, PlaylistSendClaim{VideoID: 1, RunID: run, RouteDecisionID: 1, TargetPlaylistID: "PL exact", ActionType: playlistInsert, AuthorizationID: "opaque-auth"}
}

func ptrInt64(v int64) *int64 { return &v }

func TestPlaylistSendGuardClaimAndResolutionAreAtomicAndOwned(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	now := timeNow()
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, now)
	if err != nil || result != PlaylistClaimed || receipt == nil {
		t.Fatalf("claim = %q, %v, %v", result, receipt, err)
	}
	projected, err := s.ListUnresolvedMutations(ctx, c.RunID, []int64{c.VideoID})
	if err != nil {
		t.Fatal(err)
	}
	items := projected[c.VideoID]
	if len(items) != 1 || items[0].TargetPlaylistID != c.TargetPlaylistID || !items[0].PreparedAt.Equal(now.UTC()) {
		t.Fatalf("unresolved mutations = %#v", projected)
	}
	otherRun, err := s.CreateRun(ctx, &Run{StartedAt: now, RunType: RunTypeManual, Status: RunStatusRunning, ConfigVersionID: ptrInt64(1)})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.ListUnresolvedMutations(ctx, otherRun, []int64{c.VideoID})
	if err != nil {
		t.Fatal(err)
	}
	if len(other[c.VideoID]) != 0 {
		t.Fatalf("guard leaked across runs: %#v", other)
	}
	winningReceipt := *receipt
	result, receipt2, err := s.ClaimPlaylistSend(ctx, c, now.Add(time.Second))
	if err != nil || result != PlaylistUncertainBlocked || receipt2 != nil {
		t.Fatalf("duplicate claim = %q, %v, %v", result, receipt2, err)
	}
	if err := s.ResolvePlaylistSend(ctx, *receipt, ActionOutcomeSucceeded, nil, &now); err != nil {
		t.Fatal(err)
	}
	var guards, successes int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&guards); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM executed_action WHERE outcome='succeeded'`).Scan(&successes); err != nil {
		t.Fatal(err)
	}
	if guards != 0 || successes != 1 {
		t.Fatalf("resolution state guards=%d successes=%d", guards, successes)
	}
	result, receipt, err = s.ClaimPlaylistSend(ctx, c, now.Add(2*time.Second))
	if err != nil || result != PlaylistAlreadyCompleted || receipt != nil {
		t.Fatalf("success veto = %q, %v, %v", result, receipt, err)
	}
	if err := s.ResolvePlaylistSend(ctx, winningReceipt, ActionOutcomeSucceeded, nil, &now); err == nil {
		t.Fatal("stale receipt resolved")
	}
}

func TestPlaylistSendGuardPersistsAcrossReopenAndRejectsTerminalResolution(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	result, g, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistClaimed {
		t.Fatalf("claim: %q %v", result, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("guard did not persist: %d %v", count, err)
	}
	if err := s.CompleteRun(ctx, c.RunID, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePlaylistSend(ctx, *g, ActionOutcomeSucceeded, nil, &time.Time{}); err == nil {
		t.Fatal("terminal run resolution succeeded")
	}
	result, again, err := s.ClaimPlaylistSend(ctx, c, timeNow().Add(time.Minute))
	if err == nil || result != "" || again != nil {
		t.Fatalf("terminal run claimed: %q %v %v", result, again, err)
	}
}

func TestPlaylistSendGuardSurvivesCloseReopenAndReconnect(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "guard.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	// Build valid run/action authority directly in this durable database.
	c := PlaylistSendClaim{VideoID: 1, TargetPlaylistID: "PL exact", ActionType: playlistInsert, AuthorizationID: "opaque-auth"}
	_, err = db.Exec(`INSERT INTO config_version(id,snapshot) VALUES(1,?)`, `{"routes":[{"id":1,"actions":[{"action_type":"playlist_insert","target_playlist_id":"PL exact"}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusRunning, ConfigVersionID: ptrInt64(1), ActionAuthorityVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE run SET mutation_safety_version=1 WHERE id=?`, run); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO route(name,evaluation_order,condition_composition) VALUES('r',1,'AND')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO video(video_id,channel_id,processing_state) VALUES('v','c','evaluated')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(1,?,1,1,'matched')`, run)
	if err != nil {
		t.Fatal(err)
	}
	c.RunID, c.RouteDecisionID = run, 1
	_, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || receipt == nil {
		t.Fatalf("claim: %v %v", receipt, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err = NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	result, loadedReceipt, err := s.ClaimPlaylistSend(ctx, c, timeNow().Add(time.Minute))
	if err != nil || result != PlaylistUncertainBlocked || loadedReceipt != nil {
		t.Fatalf("reopened claim = %q, %v, %v", result, loadedReceipt, err)
	}
	// A later authorization identity does not affect the target-keyed veto.
	c.AuthorizationID = "new-authorization"
	result, _, err = s.ClaimPlaylistSend(ctx, c, timeNow().Add(2*time.Minute))
	if err != nil || result != PlaylistUncertainBlocked {
		t.Fatalf("reconnected claim = %q, %v", result, err)
	}
}

func TestPlaylistSendClaimBlocksLegacyEligibilityAfterDismissalAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO config_version(id,snapshot) VALUES(1,?)`, `{"routes":[{"id":1,"actions":[{"action_type":"playlist_insert","target_playlist_id":"PL exact"}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO route(name,evaluation_order,condition_composition) VALUES('r',1,'AND')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO video(video_id,channel_id,processing_state) VALUES('v','c','evaluated')`)
	if err != nil {
		t.Fatal(err)
	}
	legacyID, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusCompleted, ConfigVersionID: ptrInt64(1)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(1,?,1,1,'matched')`, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(1,?,'runtime','old failure','dismissed')`, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	receivingID, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusRunning, ConfigVersionID: ptrInt64(1), ActionAuthorityVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE run SET mutation_safety_version=1 WHERE id=?`, receivingID); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(1,?,1,1,'matched')`, receivingID)
	if err != nil {
		t.Fatal(err)
	}
	c := PlaylistSendClaim{VideoID: 1, RunID: receivingID, RouteDecisionID: 2, TargetPlaylistID: "PL exact", ActionType: playlistInsert, AuthorizationID: "auth"}
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistUncertainBlocked || receipt != nil {
		t.Fatalf("legacy claim = %q, %v, %v", result, receipt, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err = NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	result, receipt, err = s.ClaimPlaylistSend(ctx, c, timeNow().Add(time.Minute))
	if err != nil || result != PlaylistUncertainBlocked || receipt != nil {
		t.Fatalf("reopened legacy claim = %q, %v, %v", result, receipt, err)
	}
}

func TestPlaylistSendClaimAllowsNewTargetWhenLegacyTargetsAreDurablyResolved(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO config_version(id,snapshot) VALUES(2,?)`, `{"routes":[{"id":1,"actions":[{"action_type":"playlist_insert","target_playlist_id":"old-target"}]}]}`); err != nil {
		t.Fatal(err)
	}
	legacyID, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusCompleted, ConfigVersionID: ptrInt64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(1,?,1,1,'matched')`, legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(2,?,1,'playlist_insert','old-target','succeeded')`, legacyID); err != nil {
		t.Fatal(err)
	}
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistClaimed || receipt == nil {
		t.Fatalf("claim after legacy target resolution = %q, %v, %v; want claimed", result, receipt, err)
	}
}

func TestPlaylistSendClaimFailsClosedWhenLegacyMatchedRouteIsMissingFromSnapshot(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO config_version(id,snapshot) VALUES(2,?)`, `{"routes":[]}`); err != nil {
		t.Fatal(err)
	}
	legacyID, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusCompleted, ConfigVersionID: ptrInt64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(1,?,1,1,'matched')`, legacyID); err != nil {
		t.Fatal(err)
	}
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistUncertainBlocked || receipt != nil {
		t.Fatalf("claim with missing legacy route = %q, %v, %v; want uncertain_blocked", result, receipt, err)
	}
}

func TestPlaylistSendClaimBlocksLegacyOwnedActedVideoWithoutOutcomeOrDecisionHistory(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	legacyRun, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusInterrupted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE run SET current_phase='action_execution' WHERE id=?`, legacyRun); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE video SET processing_state='acted', discovered_in_run_id=? WHERE id=?`, legacyRun, c.VideoID); err != nil {
		t.Fatal(err)
	}
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistUncertainBlocked || receipt != nil {
		t.Fatalf("claim with incomplete interrupted legacy evidence = %q, %v, %v; want uncertain_blocked", result, receipt, err)
	}
}

func TestPlaylistSendClaimDoesNotBlockNewVideoForUnrelatedLegacyActedVideo(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	legacyRun, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE run SET current_phase='action_execution' WHERE id=?`, legacyRun); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE video SET processing_state='acted' WHERE id=?`, c.VideoID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at) VALUES(?,?,NULL,?)`, legacyRun, c.VideoID, timeNow().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO video(video_id,channel_id,processing_state) VALUES('v2','c','evaluated')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(2,?,1,1,'matched')`, c.RunID); err != nil {
		t.Fatal(err)
	}
	c.VideoID = 2
	c.RouteDecisionID = 2
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistClaimed || receipt == nil {
		t.Fatalf("unrelated legacy recovery blocked new video: %q, %v, %v; want claimed", result, receipt, err)
	}
}

func TestPlaylistSendClaimIgnoresLegacyPhaseForVideoOwnedByAnotherRun(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	legacyRun, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusInterrupted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE run SET current_phase='action_execution' WHERE id=?`, legacyRun); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE video SET processing_state='acted', discovered_in_run_id=? WHERE id=?`, c.RunID, c.VideoID); err != nil {
		t.Fatal(err)
	}
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistClaimed || receipt == nil {
		t.Fatalf("unrelated legacy run blocked video owned by receiving run: %q, %v, %v; want claimed", result, receipt, err)
	}
}

func TestPlaylistSendClaimBlocksLegacyRetryOwnerWithFailedOutcomeAndMissingDecision(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	origin, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusInterrupted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE run SET current_phase='action_execution' WHERE id=?`, retry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE video SET discovered_in_run_id=? WHERE id=?`, origin, c.VideoID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO error_entry(video_id,run_id,error_category,error_message,status,retry_run_id) VALUES(?,?,'runtime','legacy','retrying',?)`, c.VideoID, origin, retry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,error_message,updated_at) VALUES(?,?,'failed','legacy failure',?)`, retry, c.VideoID, timeNow().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistUncertainBlocked || receipt != nil {
		t.Fatalf("retry-owned legacy claim = %q, %v, %v; want uncertain_blocked", result, receipt, err)
	}
	if _, err := s.db.Exec(`INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(1,?,?,'playlist_insert',?,'succeeded')`, origin, c.VideoID, c.TargetPlaylistID); err != nil {
		t.Fatal(err)
	}
	result, receipt, err = s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistAlreadyCompleted || receipt != nil {
		t.Fatalf("resolved retry target claim = %q, %v, %v; want already_completed", result, receipt, err)
	}
	if _, err := s.db.Exec(`INSERT INTO video(video_id,channel_id,processing_state) VALUES('unrelated','c','evaluated')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(2,?,1,1,'matched')`, c.RunID); err != nil {
		t.Fatal(err)
	}
	c.VideoID, c.RouteDecisionID = 2, 2
	result, receipt, err = s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistClaimed || receipt == nil {
		t.Fatalf("unrelated new video claim = %q, %v, %v; want claimed", result, receipt, err)
	}
}

// A retry-owned run whose outcome is final must not veto the guard, while an
// unresolved one must. unavailable_on_youtube is final (ARS-005 O2 inventory).
func TestPlaylistSendClaimLegacyRetryVetoByOutcome(t *testing.T) {
	for _, tc := range []struct {
		outcome, errMsg string
		want            PlaylistClaimResult
	}{
		{"failed", "legacy failure", PlaylistUncertainBlocked},
		{"not_yet_evaluated", "", PlaylistUncertainBlocked},
		{"unavailable_on_youtube", "", PlaylistClaimed},
		{"accepted", "", PlaylistClaimed},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			s, c := guardFixture(t)
			ctx := context.Background()
			origin, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusCompleted})
			if err != nil {
				t.Fatal(err)
			}
			retry, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusInterrupted})
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range []struct {
				sql  string
				args []any
			}{
				{`UPDATE run SET current_phase='action_execution' WHERE id=?`, []any{retry}},
				{`UPDATE video SET discovered_in_run_id=? WHERE id=?`, []any{origin, c.VideoID}},
				{`INSERT INTO error_entry(video_id,run_id,error_category,error_message,status,retry_run_id) VALUES(?,?,'runtime','legacy','retrying',?)`, []any{c.VideoID, origin, retry}},
				{`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,error_message,updated_at) VALUES(?,?,?,NULLIF(?,''),?)`, []any{retry, c.VideoID, tc.outcome, tc.errMsg, timeNow().Format(time.RFC3339Nano)}},
			} {
				if _, err := s.db.Exec(q.sql, q.args...); err != nil {
					t.Fatal(err)
				}
			}
			result, _, err := s.ClaimPlaylistSend(ctx, c, timeNow())
			if err != nil || result != tc.want {
				t.Fatalf("claim with retry outcome %q = %q, %v; want %q", tc.outcome, result, err, tc.want)
			}
		})
	}
}

func TestPlaylistSendResolutionFailureRetainsGuardAndNotIssuedDeferredResolution(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	_, g, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil {
		t.Fatal(err)
	}
	// A prior non-success action row is updated by resolution; success and its
	// guard deletion remain one transaction.
	if _, err := s.db.Exec(`INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(?,?,?,?,?,'deferred')`, c.RouteDecisionID, c.RunID, c.VideoID, c.ActionType, c.TargetPlaylistID); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePlaylistSend(ctx, *g, ActionOutcomeSucceeded, nil, ptrTime(timeNow())); err != nil {
		t.Fatalf("not-issued/deferred resolution: %v", err)
	}
	var guards int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&guards); err != nil || guards != 0 {
		t.Fatalf("guard after resolution = %d, %v", guards, err)
	}
	if err := s.ResolvePlaylistSend(ctx, *g, ActionOutcomeFailed, nil, nil); err == nil {
		t.Fatal("failed/uncertain outcome released guard")
	}
}

func TestPlaylistSendResolutionTxSupportsCallerCommitRollbackAndNoAttempt(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	_, g, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil {
		t.Fatal(err)
	}

	// A caller can compose resolution with other writes and roll the whole unit
	// back; the resolver itself must not commit the transaction.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePlaylistSendTx(ctx, tx, *g, ActionOutcomeNotAttempted, ptrString("authorization_pending"), nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var guards, outcomes int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&guards); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM executed_action`).Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	if guards != 1 || outcomes != 0 {
		t.Fatalf("rollback left guards=%d outcomes=%d, want 1 and 0", guards, outcomes)
	}

	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePlaylistSendTx(ctx, tx, *g, ActionOutcomeNotAttempted, ptrString("authorization_pending"), nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var outcome string
	var executedAt *string
	if err := s.db.QueryRow(`SELECT outcome, executed_at FROM executed_action`).Scan(&outcome, &executedAt); err != nil {
		t.Fatal(err)
	}
	if outcome != string(ActionOutcomeNotAttempted) || executedAt != nil {
		t.Fatalf("no-attempt evidence outcome=%q executed_at=%v", outcome, executedAt)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&guards); err != nil || guards != 0 {
		t.Fatalf("committed guard count=%d err=%v", guards, err)
	}
}

func TestPlaylistSendResolutionRollsBackWhenRetainedHandoffFails(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	_, g, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_retained_handoff BEFORE INSERT ON error_entry BEGIN SELECT RAISE(ABORT, 'injected handoff failure'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePlaylistSendTx(ctx, tx, *g, ActionOutcomeNotAttempted, ptrString("authorization_pending"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status,created_at) VALUES(?,?,'runtime','refused','active',?)`, c.VideoID, c.RunID, timeNow().Format(time.RFC3339)); err == nil {
		t.Fatal("handoff unexpectedly succeeded")
	}
	_ = tx.Rollback()
	var guards, outcomes int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playlist_send_guard`).Scan(&guards); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM executed_action`).Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	if guards != 1 || outcomes != 0 {
		t.Fatalf("after failed handoff guards=%d outcomes=%d, want 1 and 0", guards, outcomes)
	}
}

func ptrString(v string) *string { return &v }

func ptrTime(v time.Time) *time.Time { return &v }
