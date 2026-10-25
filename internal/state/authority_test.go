package state

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRecordRunVideoOutcomeIdempotentAndImmutable(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	runID := seedRun(t, m)
	v, err := m.DiscoverVideo(ctx, runID, "outcome-video", "channel")
	if err != nil {
		t.Fatal(err)
	}
	want := FinalOutcomeAccepted
	completed := time.Now().UTC()
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.RecordRunVideoOutcomeTx(ctx, tx, runID, v.ID, &want, nil, nil, &completed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := m.store.db.QueryRowContext(ctx, `SELECT updated_at FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, v.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	tx, err = m.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.RecordRunVideoOutcomeTx(ctx, tx, runID, v.ID, &want, nil, nil, &completed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := m.store.db.QueryRowContext(ctx, `SELECT updated_at FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, v.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("idempotent write changed updated_at: %q -> %q", before, after)
	}
	conflict := FinalOutcomeRejected
	tx, err = m.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = m.store.RecordRunVideoOutcomeTx(ctx, tx, runID, v.ID, &conflict, nil, nil, nil)
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("conflicting terminal outcome accepted")
	}
}

func TestRecordRunVideoOutcomeClearsProvisionalClassification(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	runID := seedRun(t, m)
	v, err := m.DiscoverVideo(ctx, runID, "provisional-outcome", "channel")
	if err != nil {
		t.Fatal(err)
	}
	var provisional VideoFinalOutcome
	if err := m.store.db.QueryRowContext(ctx, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, v.ID).Scan(&provisional); err != nil {
		t.Fatal(err)
	}
	if provisional != FinalOutcomeNotYetEvaluated {
		t.Fatalf("discovery outcome = %q", provisional)
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.RecordRunVideoOutcomeTx(ctx, tx, runID, v.ID, nil, nil, nil, nil); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var outcome sql.NullString
	if err := m.store.db.QueryRowContext(ctx, `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, v.ID).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.Valid {
		t.Fatalf("provisional classification after evaluation start = %q, want SQL NULL", outcome.String)
	}
}

func TestTransitionRunVideoStateCapturesDispositionAtomically(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	runID := seedRun(t, m)
	v, err := m.DiscoverVideo(ctx, runID, "deferred-outcome", "channel")
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]VideoProcessingState{{VideoStateNotYetEvaluated, VideoStateEvaluating}, {VideoStateEvaluating, VideoStateEvaluated}, {VideoStateEvaluated, VideoStateActed}} {
		if _, err := m.TransitionVideoState(ctx, v.ID, edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	reason := "quota_exhausted"
	if _, err := m.TransitionRunVideoState(ctx, runID, v.ID, VideoStateActed, VideoStateDeferred, FinalOutcomeDeferred, &reason, nil); err != nil {
		t.Fatal(err)
	}
	var gotOutcome, gotReason string
	if err := m.store.db.QueryRowContext(ctx, `SELECT final_outcome,deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, v.ID).Scan(&gotOutcome, &gotReason); err != nil {
		t.Fatal(err)
	}
	if gotOutcome != string(FinalOutcomeDeferred) || gotReason != reason {
		t.Fatalf("captured = %q/%q", gotOutcome, gotReason)
	}
}

func TestTransitionRunVideoStateRollsBackLifecycleOnCaptureFailure(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	runID := seedRun(t, m)
	v, err := m.DiscoverVideo(ctx, runID, "rollback-outcome", "channel")
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]VideoProcessingState{{VideoStateNotYetEvaluated, VideoStateEvaluating}, {VideoStateEvaluating, VideoStateEvaluated}, {VideoStateEvaluated, VideoStateActed}} {
		if _, err := m.TransitionVideoState(ctx, v.ID, edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.TransitionRunVideoState(ctx, runID, v.ID, VideoStateActed, VideoStateDeferred, FinalOutcomeDeferred, nil, nil); err == nil {
		t.Fatal("missing reason unexpectedly recorded")
	}
	after, err := m.store.GetVideo(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProcessingState != VideoStateActed {
		t.Fatalf("lifecycle transition escaped rollback: %q", after.ProcessingState)
	}
}

func TestRunAuthorityDefaultsAndExplicitCreation(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	legacy := seedRun(t, m)
	r, err := m.store.GetRun(ctx, legacy)
	if err != nil || r.ActionAuthorityVersion != 0 {
		t.Fatalf("generic producer = %+v, %v", r, err)
	}
	if err := m.store.InterruptRun(ctx, legacy, "restart"); err != nil {
		t.Fatal(err)
	}
	runs, err := m.store.ListInterruptedRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].ActionAuthorityVersion != 0 {
		t.Fatalf("resume marker = %+v, %v", runs, err)
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO config_version (snapshot) VALUES ('{}')`)
	if err != nil {
		t.Fatal(err)
	}
	versionID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.store.CreateRunTx(ctx, tx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusRunning, ConfigVersionID: &versionID, ActionAuthorityVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r, err = m.store.GetRun(ctx, id)
	if err != nil || r.ActionAuthorityVersion != 1 {
		t.Fatalf("explicit producer = %+v, %v", r, err)
	}
	if _, err := m.store.CreateRun(ctx, &Run{StartedAt: timeNow(), RunType: RunTypeManual, Status: RunStatusRunning, ActionAuthorityVersion: 2}); err == nil {
		t.Fatal("unsupported marker accepted")
	}
	runs, err = m.store.ListRuns(ctx)
	if err != nil || len(runs) != 2 {
		t.Fatalf("list = %+v, %v", runs, err)
	}
	// Simulate an externally corrupted database: the reader must not coerce it.
	if _, err := m.store.db.Exec(`PRAGMA ignore_check_constraints = ON; UPDATE run SET action_authority_version = 2 WHERE id = ?`, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.GetRun(ctx, legacy); err == nil {
		t.Fatal("unsupported stored marker read successfully")
	}
	if _, err := m.store.db.Exec(`UPDATE run SET action_authority_version = NULL WHERE id = ?`, legacy); err == nil {
		t.Fatal("NULL marker accepted")
	}
}
