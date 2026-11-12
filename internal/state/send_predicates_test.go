package state

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// vetoed evaluates the shared LegacySendVeto for one subject against every run.
func vetoed(t *testing.T, s *Store, videoID int64, target *string, receiving int64, assumeActed int) bool {
	t.Helper()
	var arg any
	if target != nil {
		arg = *target
	}
	var got bool
	err := s.db.QueryRow(`SELECT EXISTS (
SELECT 1 FROM (SELECT ?1 AS video_id, ?2 AS target_playlist_id, ?3 AS receiving_run_id, ?4 AS assume_acted) subject
JOIN run old_run ON `+LegacySendVeto+`)`, videoID, arg, receiving, assumeActed).Scan(&got)
	if err != nil {
		t.Fatalf("evaluating LegacySendVeto: %v", err)
	}
	return got
}

// Branch 3 reads the video's live state. assume_acted = 1 evaluates it as it
// will be at send time; assume_acted = 0 is exactly ClaimPlaylistSend's text.
func TestLegacySendVetoAssumeActedControlsBranchThree(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	legacy, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusInterrupted})
	if err != nil {
		t.Fatal(err)
	}
	// The video is evaluated, not acted; the legacy run discovered it and
	// recorded no matched decision for it.
	if _, err := s.db.Exec(`UPDATE run SET current_phase='action_execution' WHERE id=?`, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE video SET discovered_in_run_id=? WHERE id=?`, legacy, c.VideoID); err != nil {
		t.Fatal(err)
	}
	target := c.TargetPlaylistID
	if vetoed(t, s, c.VideoID, &target, c.RunID, 0) {
		t.Fatal("assume_acted=0 vetoed a video that is not acted")
	}
	if !vetoed(t, s, c.VideoID, &target, c.RunID, 1) {
		t.Fatal("assume_acted=1 did not veto at send-time state")
	}
	// The claim itself keeps assume_acted = 0 and is unchanged.
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistClaimed || receipt == nil {
		t.Fatalf("claim = %q, %v, %v; want claimed", result, receipt, err)
	}
	// Once the video is actually acted, both modes agree.
	if _, err := s.db.Exec(`UPDATE video SET processing_state='acted' WHERE id=?`, c.VideoID); err != nil {
		t.Fatal(err)
	}
	if !vetoed(t, s, c.VideoID, &target, c.RunID, 0) || !vetoed(t, s, c.VideoID, &target, c.RunID, 1) {
		t.Fatal("acted video must be vetoed in both modes")
	}
	// The receiving run's own evidence never vetoes itself.
	if vetoed(t, s, c.VideoID, &target, legacy, 1) {
		t.Fatal("a run vetoed itself")
	}
}

// Branch 4 with a NULL target treats the target as unresolved: success on any
// one target does not clear it.
func TestLegacySendVetoBranchFourNullTarget(t *testing.T) {
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
		{`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,error_message,updated_at) VALUES(?,?,'failed','legacy failure',?)`, []any{retry, c.VideoID, timeNow().Format(time.RFC3339Nano)}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	targetA, targetB := "PL A", "PL B"
	if !vetoed(t, s, c.VideoID, nil, c.RunID, 0) || !vetoed(t, s, c.VideoID, &targetA, c.RunID, 0) {
		t.Fatal("unresolved retry must veto for NULL and unsucceeded targets")
	}
	if _, err := s.db.Exec(`INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(1,?,?,'playlist_insert',?,'succeeded')`, origin, c.VideoID, targetA); err != nil {
		t.Fatal(err)
	}
	if vetoed(t, s, c.VideoID, &targetA, c.RunID, 0) {
		t.Fatal("target A succeeded; it must not veto for A")
	}
	if !vetoed(t, s, c.VideoID, &targetB, c.RunID, 0) {
		t.Fatal("success on A must not clear B")
	}
	if !vetoed(t, s, c.VideoID, nil, c.RunID, 0) {
		t.Fatal("NULL target must stay vetoed after a success on one target")
	}
}

func heldGuards(t *testing.T, db *sql.DB, videoID int64) (targets []string, uncertain []bool) {
	t.Helper()
	// HeldSendGuard is correlated on subject.video_id, so it is evaluated as a
	// scalar subquery of a row that supplies that alias.
	var packed sql.NullString
	err := db.QueryRow(`SELECT (SELECT group_concat(target_playlist_id || '|' || uncertain, ';')
FROM (`+HeldSendGuard+` ORDER BY g.target_playlist_id)) FROM (SELECT ?1 AS video_id) subject`, videoID).Scan(&packed)
	if err != nil {
		t.Fatalf("evaluating HeldSendGuard: %v", err)
	}
	if !packed.Valid {
		return nil, nil
	}
	for _, item := range strings.Split(packed.String, ";") {
		target, flag, _ := strings.Cut(item, "|")
		targets = append(targets, target)
		uncertain = append(uncertain, flag == "1")
	}
	return targets, uncertain
}

func TestHeldSendGuardBlocksOnlyTargetsWithoutRecordedSuccess(t *testing.T) {
	s, c := guardFixture(t)
	ctx := context.Background()
	if targets, _ := heldGuards(t, s.db, c.VideoID); len(targets) != 0 {
		t.Fatalf("no guard held yet, got %v", targets)
	}
	result, receipt, err := s.ClaimPlaylistSend(ctx, c, timeNow())
	if err != nil || result != PlaylistClaimed || receipt == nil {
		t.Fatalf("claim = %q, %v, %v", result, receipt, err)
	}
	targets, uncertain := heldGuards(t, s.db, c.VideoID)
	if len(targets) != 1 || targets[0] != c.TargetPlaylistID || uncertain[0] {
		t.Fatalf("held = %v uncertain=%v; want the claimed target, not uncertain", targets, uncertain)
	}
	// A different video is not affected.
	if targets, _ := heldGuards(t, s.db, c.VideoID+1); len(targets) != 0 {
		t.Fatalf("guard leaked to another video: %v", targets)
	}
	// An uncertain addition recorded by the guard's own run is flagged.
	if _, err := s.db.Exec(`INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome,outcome_reason) VALUES(?,?,?,'playlist_insert',?,'failed','mutation_application_uncertain')`,
		c.RouteDecisionID, c.RunID, c.VideoID, c.TargetPlaylistID); err != nil {
		t.Fatal(err)
	}
	if targets, uncertain := heldGuards(t, s.db, c.VideoID); len(targets) != 1 || !uncertain[0] {
		t.Fatalf("held = %v uncertain=%v; want uncertain", targets, uncertain)
	}
	// Success recorded for the target (by any run) releases the block.
	other, err := s.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(1,?,?,'playlist_insert',?,'succeeded')`,
		other, c.VideoID, c.TargetPlaylistID); err != nil {
		t.Fatal(err)
	}
	if targets, _ := heldGuards(t, s.db, c.VideoID); len(targets) != 0 {
		t.Fatalf("guard still held after success on its target: %v", targets)
	}
}
