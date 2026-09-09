package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/store/storetest"
)

// openTestDB returns a new in-memory SQLite database configured for tests.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return storetest.Open(t)
}

// newTestMachine returns a Machine backed by a fresh in-memory store.
func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	db := openTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	return NewMachine(store)
}

// seedRun creates a minimal run for use in tests.
func seedRun(t *testing.T, m *Machine) int64 {
	t.Helper()
	id, err := m.store.CreateRun(context.Background(), &Run{
		StartedAt: timeNow(),
		RunType:   RunTypeManual,
		Status:    RunStatusRunning,
	})
	if err != nil {
		t.Fatalf("creating run: %v", err)
	}
	return id
}

// seedRoute creates a minimal route so that route_decision FK constraints are
// satisfied in tests.
func seedRoute(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO route (name, evaluation_order, condition_composition, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?);`,
		"test-route", 1, "AND", timeNow(), timeNow())
	if err != nil {
		t.Fatalf("creating route: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading route id: %v", err)
	}
	return id
}

func timeNow() time.Time {
	ts, _ := time.Parse(time.RFC3339, "2026-09-08T00:00:00Z")
	return ts
}

func TestDiscoverVideoSetsNotYetEvaluated(t *testing.T) {
	m := newTestMachine(t)
	runID := seedRun(t, m)

	v, err := m.DiscoverVideo(context.Background(), runID, "yt-video-1", "UCchannel")
	if err != nil {
		t.Fatalf("DiscoverVideo failed: %v", err)
	}

	if v.ProcessingState != VideoStateNotYetEvaluated {
		t.Errorf("processing state = %q, want %q", v.ProcessingState, VideoStateNotYetEvaluated)
	}
	if v.FinalOutcome != FinalOutcomeNotYetEvaluated {
		t.Errorf("final outcome = %q, want %q", v.FinalOutcome, FinalOutcomeNotYetEvaluated)
	}

	// Reload from storage to confirm durability within the same connection.
	loaded, err := m.store.GetVideo(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("GetVideo failed: %v", err)
	}
	if loaded.ProcessingState != VideoStateNotYetEvaluated {
		t.Errorf("reloaded processing state = %q, want %q", loaded.ProcessingState, VideoStateNotYetEvaluated)
	}
}

func TestStateTransitions(t *testing.T) {
	m := newTestMachine(t)
	runID := seedRun(t, m)

	v, err := m.DiscoverVideo(context.Background(), runID, "yt-video-2", "UCchannel")
	if err != nil {
		t.Fatalf("DiscoverVideo failed: %v", err)
	}

	transitions := []struct {
		from VideoProcessingState
		to   VideoProcessingState
	}{
		{VideoStateNotYetEvaluated, VideoStateEvaluating},
		{VideoStateEvaluating, VideoStateEvaluated},
		{VideoStateEvaluated, VideoStateActed},
		{VideoStateActed, VideoStateCompleted},
	}

	for _, tr := range transitions {
		v, err = m.TransitionVideoState(context.Background(), v.ID, tr.from, tr.to)
		if err != nil {
			t.Fatalf("transition %q -> %q failed: %v", tr.from, tr.to, err)
		}
		if v.ProcessingState != tr.to {
			t.Errorf("after %q -> %q, state = %q, want %q", tr.from, tr.to, v.ProcessingState, tr.to)
		}
	}

	if v.FinalOutcome != FinalOutcomeAccepted {
		t.Errorf("final outcome after completion = %q, want %q", v.FinalOutcome, FinalOutcomeAccepted)
	}
	if v.CompletedAt == nil {
		t.Error("expected CompletedAt to be set after terminal state")
	}
}

func TestInvalidTransitionsRejected(t *testing.T) {
	m := newTestMachine(t)
	runID := seedRun(t, m)

	v, err := m.DiscoverVideo(context.Background(), runID, "yt-video-3", "UCchannel")
	if err != nil {
		t.Fatalf("DiscoverVideo failed: %v", err)
	}

	// Directly to completed is not allowed; must pass through acted.
	_, err = m.TransitionVideoState(context.Background(), v.ID,
		VideoStateNotYetEvaluated, VideoStateCompleted)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	// First process: open, write, close.
	{
		ctx := context.Background()
		db, err := store.Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("opening db: %v", err)
		}
		store, err := NewStore(db)
		if err != nil {
			t.Fatalf("creating store: %v", err)
		}
		mach := NewMachine(store)
		runID, err := store.CreateRun(ctx, &Run{
			StartedAt: parseTime(t, "2026-09-08T00:00:00Z"),
			RunType:   RunTypeManual,
			Status:    RunStatusRunning,
		})
		if err != nil {
			t.Fatalf("creating run: %v", err)
		}
		v, err := mach.DiscoverVideo(ctx, runID, "yt-restart", "UCchannel")
		if err != nil {
			t.Fatalf("discovering video: %v", err)
		}
		if _, err := mach.TransitionVideoState(ctx, v.ID,
			VideoStateNotYetEvaluated, VideoStateEvaluating); err != nil {
			t.Fatalf("transitioning: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("closing db: %v", err)
		}
	}

	// Second process: reopen and read back.
	{
		ctx := context.Background()
		db, err := store.Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("reopening db: %v", err)
		}
		defer db.Close()
		store, err := NewStore(db)
		if err != nil {
			t.Fatalf("creating store: %v", err)
		}

		v, err := store.GetVideoByExternalID(context.Background(), "yt-restart")
		if err != nil {
			t.Fatalf("loading video after restart: %v", err)
		}
		if v.ProcessingState != VideoStateEvaluating {
			t.Errorf("state after restart = %q, want %q", v.ProcessingState, VideoStateEvaluating)
		}
	}
}

func TestNotYetEvaluatedNotCollapsedToRejected(t *testing.T) {
	m := newTestMachine(t)
	runID := seedRun(t, m)

	v, err := m.DiscoverVideo(context.Background(), runID, "yt-video-4", "UCchannel")
	if err != nil {
		t.Fatalf("DiscoverVideo failed: %v", err)
	}

	// Quota exhaustion should leave the video deferred, not rejected.
	v, err = m.TransitionVideoState(context.Background(), v.ID,
		VideoStateNotYetEvaluated, VideoStateEvaluating)
	if err != nil {
		t.Fatalf("transition to evaluating failed: %v", err)
	}
	v, err = m.TransitionVideoState(context.Background(), v.ID,
		VideoStateEvaluating, VideoStateDeferred,
		WithDeferredReason("quota_exhausted"),
		WithFinalOutcome(FinalOutcomeDeferred))
	if err != nil {
		t.Fatalf("transition to deferred failed: %v", err)
	}
	if v.ProcessingState != VideoStateDeferred {
		t.Errorf("processing state = %q, want %q", v.ProcessingState, VideoStateDeferred)
	}
	if v.FinalOutcome != FinalOutcomeDeferred {
		t.Errorf("final outcome = %q, want %q", v.FinalOutcome, FinalOutcomeDeferred)
	}
	if v.DeferredReason == nil || *v.DeferredReason != "quota_exhausted" {
		t.Errorf("deferred reason = %v, want quota_exhausted", v.DeferredReason)
	}
}

func TestRouteDecisionAndExecutedAction(t *testing.T) {
	m := newTestMachine(t)
	db := m.store.db
	runID := seedRun(t, m)
	routeID := seedRoute(t, db)

	v, err := m.DiscoverVideo(context.Background(), runID, "yt-video-5", "UCchannel")
	if err != nil {
		t.Fatalf("DiscoverVideo failed: %v", err)
	}
	if _, err := m.TransitionVideoState(context.Background(), v.ID,
		VideoStateNotYetEvaluated, VideoStateEvaluating); err != nil {
		t.Fatalf("transition to evaluating failed: %v", err)
	}
	if _, err := m.TransitionVideoState(context.Background(), v.ID,
		VideoStateEvaluating, VideoStateEvaluated); err != nil {
		t.Fatalf("transition to evaluated failed: %v", err)
	}

	decision, err := m.RecordRouteDecision(context.Background(), &RouteDecision{
		VideoID:         v.ID,
		RunID:           runID,
		RouteID:         routeID,
		EvaluationOrder: 1,
		Decision:        RouteDecisionMatched,
	})
	if err != nil {
		t.Fatalf("RecordRouteDecision failed: %v", err)
	}
	if decision.ID == 0 {
		t.Error("expected route decision to receive an ID")
	}

	action, err := m.RecordExecutedAction(context.Background(), &ExecutedAction{
		RouteDecisionID:  decision.ID,
		RunID:            runID,
		VideoID:          v.ID,
		ActionType:       "playlist_insert",
		TargetPlaylistID: "PLtarget",
		Outcome:          ActionOutcomeSucceeded,
	})
	if err != nil {
		t.Fatalf("RecordExecutedAction failed: %v", err)
	}
	if action.ID == 0 {
		t.Error("expected executed action to receive an ID")
	}
	if action.ExecutedAt == nil {
		t.Error("expected ExecutedAt to be set")
	}

	decisions, err := m.store.GetRouteDecisionsForVideo(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("GetRouteDecisionsForVideo failed: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("got %d decisions, want 1", len(decisions))
	}

	actions, err := m.store.GetExecutedActionsForVideo(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("GetExecutedActionsForVideo failed: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
}

func TestExecutedActionUniqueConstraint(t *testing.T) {
	m := newTestMachine(t)
	db := m.store.db
	runID := seedRun(t, m)
	routeID := seedRoute(t, db)

	v, err := m.DiscoverVideo(context.Background(), runID, "yt-video-6", "UCchannel")
	if err != nil {
		t.Fatalf("DiscoverVideo failed: %v", err)
	}

	decision, err := m.RecordRouteDecision(context.Background(), &RouteDecision{
		VideoID:         v.ID,
		RunID:           runID,
		RouteID:         routeID,
		EvaluationOrder: 1,
		Decision:        RouteDecisionMatched,
	})
	if err != nil {
		t.Fatalf("RecordRouteDecision failed: %v", err)
	}

	a := &ExecutedAction{
		RouteDecisionID:  decision.ID,
		RunID:            runID,
		VideoID:          v.ID,
		ActionType:       "playlist_insert",
		TargetPlaylistID: "PLtarget",
		Outcome:          ActionOutcomeSucceeded,
	}
	if _, err := m.RecordExecutedAction(context.Background(), a); err != nil {
		t.Fatalf("first executed action failed: %v", err)
	}

	// Re-executing the same action in the same run must be rejected by the
	// UNIQUE(run_id, video_id, target_playlist_id, action_type) constraint.
	_, err = m.RecordExecutedAction(context.Background(), a)
	if err == nil {
		t.Fatal("expected duplicate executed action to fail")
	}
}

func TestCompleteRunRecomputesCounters(t *testing.T) {
	m := newTestMachine(t)
	runID := seedRun(t, m)

	for i := 0; i < 3; i++ {
		v, err := m.DiscoverVideo(context.Background(), runID, fmt.Sprintf("yt-count-%d", i), "UCchannel")
		if err != nil {
			t.Fatalf("discovering video: %v", err)
		}
		if i == 0 {
			transitions := []struct {
				from VideoProcessingState
				to   VideoProcessingState
			}{
				{VideoStateNotYetEvaluated, VideoStateEvaluating},
				{VideoStateEvaluating, VideoStateEvaluated},
				{VideoStateEvaluated, VideoStateActed},
				{VideoStateActed, VideoStateCompleted},
			}
			for _, tr := range transitions {
				if _, err := m.TransitionVideoState(context.Background(), v.ID, tr.from, tr.to); err != nil {
					t.Fatalf("transitioning %q -> %q: %v", tr.from, tr.to, err)
				}
			}
		}
	}

	run, err := m.CompleteRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("CompleteRun failed: %v", err)
	}
	if run.VideosDiscovered != 3 {
		t.Errorf("videos_discovered = %d, want 3", run.VideosDiscovered)
	}
	if run.VideosAccepted != 1 {
		t.Errorf("videos_accepted = %d, want 1", run.VideosAccepted)
	}
	if run.VideosNotYetEvaluated != 2 {
		t.Errorf("videos_not_yet_evaluated = %d, want 2", run.VideosNotYetEvaluated)
	}
}

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing time: %v", err)
	}
	return ts
}
