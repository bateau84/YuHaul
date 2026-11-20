package state

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRunMutationSafetyVersionRequiresExplicitGuardedProductionCapability(t *testing.T) {
	db := openTestDB(t)
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config_version(id,snapshot) VALUES(1,'{}')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	production := &Run{StartedAt: time.Now().UTC(), RunType: RunTypeManual, Status: RunStatusRunning, ConfigVersionID: ptrInt64(1)}

	legacyID, err := s.CreateRun(ctx, production)
	if err != nil {
		t.Fatal(err)
	}
	checkMutationSafetyVersion(t, db, legacyID, 0)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	guardedID, err := s.CreateGuardedMutationRunTx(ctx, tx, production, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	checkMutationSafetyVersion(t, db, guardedID, 1)

	for _, runType := range []RunType{RunTypeDryRun, RunTypeReplay, RunTypeBacktest} {
		t.Run(string(runType), func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.CreateGuardedMutationRunTx(ctx, tx, &Run{StartedAt: time.Now().UTC(), RunType: runType, Status: RunStatusRunning, ConfigVersionID: ptrInt64(1)}, true)
			if err == nil {
				t.Fatal("simulation accepted guarded-mutation capability")
			}
			_ = tx.Rollback()
		})
	}
}

func checkMutationSafetyVersion(t *testing.T, db *sql.DB, id int64, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT mutation_safety_version FROM run WHERE id=?`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("mutation_safety_version=%d, want %d", got, want)
	}
}
