package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
)

func TestRecordRouteEvaluationCancellationLeavesNoPartialEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cancel-contention.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machineStore, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMachine(machineStore)
	runID := seedRun(t, m)
	routeID := seedRoute(t, db)
	video, err := m.DiscoverVideo(ctx, runID, "cancel-contention-video", "channel")
	if err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	lock, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.ExecContext(ctx, `INSERT INTO route (name, evaluation_order, condition_composition, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, "held-writer", 2, "AND", timeNow(), timeNow()); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, recordErr := m.RecordRouteEvaluation(cancelCtx, &RouteDecision{VideoID: video.ID, RunID: runID, RouteID: routeID, Decision: RouteDecisionNotMatched}, []ConditionAudit{{Field: "title", Operator: "equals", ExpectedValue: "x"}})
	if !errors.Is(recordErr, context.DeadlineExceeded) {
		t.Fatalf("RecordRouteEvaluation error = %v, want context deadline", recordErr)
	}
	assertNoRouteEvidence(t, db, video.ID, runID, routeID)
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Once the sustained competing writer is gone, the same evaluation must be
	// retryable and commit both decision and audit as one unit.
	decision, err := m.RecordRouteEvaluation(ctx, &RouteDecision{VideoID: video.ID, RunID: runID, RouteID: routeID, Decision: RouteDecisionNotMatched}, []ConditionAudit{{Field: "title", Operator: "equals", ExpectedValue: "x"}})
	if err != nil {
		t.Fatalf("retry after releasing writer: %v", err)
	}
	if decision.ID == 0 {
		t.Fatal("route decision ID is zero")
	}
	assertRouteEvidenceCounts(t, db, video.ID, runID, routeID, 1, 1)
}

func TestRecordRouteEvaluationAuditFailureRollsBackDecision(t *testing.T) {
	m := newTestMachine(t)
	runID := seedRun(t, m)
	routeID := seedRoute(t, m.store.db)
	video, err := m.DiscoverVideo(context.Background(), runID, "audit-rollback-video", "channel")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.RecordRouteEvaluation(context.Background(), &RouteDecision{VideoID: video.ID, RunID: runID, RouteID: routeID, Decision: RouteDecisionNotMatched}, []ConditionAudit{{Field: "", Operator: "equals"}})
	if err == nil {
		t.Fatal("expected invalid audit error")
	}
	assertNoRouteEvidence(t, m.store.db, video.ID, runID, routeID)
}

func assertNoRouteEvidence(t *testing.T, db *sql.DB, videoID, runID, routeID int64) {
	t.Helper()
	assertRouteEvidenceCounts(t, db, videoID, runID, routeID, 0, 0)
}

func assertRouteEvidenceCounts(t *testing.T, db *sql.DB, videoID, runID, routeID int64, decisions, audits int) {
	t.Helper()
	var gotDecisions, gotAudits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM route_decision WHERE video_id=? AND run_id=? AND route_id=?`, videoID, runID, routeID).Scan(&gotDecisions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM condition_audit WHERE route_decision_id IN (SELECT id FROM route_decision WHERE video_id=? AND run_id=? AND route_id=?)`, videoID, runID, routeID).Scan(&gotAudits); err != nil {
		t.Fatal(err)
	}
	if gotDecisions != decisions || gotAudits != audits {
		t.Fatalf("evidence counts decisions=%d audits=%d, want %d/%d", gotDecisions, gotAudits, decisions, audits)
	}
}
