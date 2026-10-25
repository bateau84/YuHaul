package state

import (
	"context"
	"testing"
)

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
