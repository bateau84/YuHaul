package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// Run-initiation ARS-005 §6.1.3 evidence S1: migration 032 adds the nullable,
// CHECK-constrained, raise-only run.dispatch_stop column.

func dispatchStopDump(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT type||'|'||name, coalesce(sql,'') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name <> 'run' AND name <> 'run_dispatch_stop_raise_only'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func dispatchStopValue(t *testing.T, db *sql.DB, id int) sql.NullString {
	t.Helper()
	var v sql.NullString
	if err := db.QueryRow(`SELECT dispatch_stop FROM run WHERE id=?`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// assertDispatchStopRules checks the CHECK and the raise-only trigger on a
// database that has migration 032 and the widened run.status CHECK.
func assertDispatchStopRules(t *testing.T, db *sql.DB) {
	t.Helper()
	searchExec(t, db, `INSERT INTO run (id, run_type, status) VALUES (900,'scheduled','running'),(901,'scheduled','running'),(902,'scheduled','running')`)
	if v := dispatchStopValue(t, db, 900); v.Valid {
		t.Fatalf("new run dispatch_stop = %q, want NULL", v.String)
	}

	// The CHECK refuses quota and any unknown value, on insert and update.
	for _, bad := range []string{"quota", "none", "", "Authorization", "accounting"} {
		if _, err := db.Exec(`UPDATE run SET dispatch_stop=? WHERE id=900`, bad); err == nil {
			t.Fatalf("CHECK accepted %q", bad)
		}
		if _, err := db.Exec(`INSERT INTO run (run_type,status,dispatch_stop) VALUES ('manual','running',?)`, bad); err == nil {
			t.Fatalf("CHECK accepted %q on insert", bad)
		}
	}
	if v := dispatchStopValue(t, db, 900); v.Valid {
		t.Fatalf("a refused write changed the column: %q", v.String)
	}

	// NULL may be raised to either stored level.
	searchExec(t, db, `UPDATE run SET dispatch_stop='authorization' WHERE id=900`)
	searchExec(t, db, `UPDATE run SET dispatch_stop='accounting_or_integrity' WHERE id=901`)
	// authorization -> accounting_or_integrity is allowed.
	searchExec(t, db, `UPDATE run SET dispatch_stop='accounting_or_integrity' WHERE id=900`)
	// Equal values and an unrelated column update are allowed.
	searchExec(t, db, `UPDATE run SET dispatch_stop='accounting_or_integrity' WHERE id=900`)
	searchExec(t, db, `UPDATE run SET status='completed' WHERE id=900`)

	// Lowering and clearing are refused, and the stored value is kept.
	for _, c := range []struct {
		id  int
		set string
	}{
		{900, `UPDATE run SET dispatch_stop='authorization' WHERE id=900`},
		{900, `UPDATE run SET dispatch_stop=NULL WHERE id=900`},
		{901, `UPDATE run SET dispatch_stop='authorization' WHERE id=901`},
		{901, `UPDATE run SET dispatch_stop=NULL WHERE id=901`},
	} {
		_, err := db.Exec(c.set)
		if err == nil || !strings.Contains(err.Error(), "cannot be lowered or cleared") {
			t.Fatalf("%s: error = %v", c.set, err)
		}
		if v := dispatchStopValue(t, db, c.id); v.String != "accounting_or_integrity" {
			t.Fatalf("%s changed the value to %q", c.set, v.String)
		}
	}
	searchExec(t, db, `UPDATE run SET dispatch_stop='authorization' WHERE id=902`)
	if _, err := db.Exec(`UPDATE run SET dispatch_stop=NULL WHERE id=902`); err == nil {
		t.Fatal("clearing authorization was accepted")
	}
	if v := dispatchStopValue(t, db, 902); v.String != "authorization" {
		t.Fatalf("authorization was lost: %q", v.String)
	}
}

// TestMigration032UpgradeFrom031 upgrades a populated 031 database through
// the real migration, then through the in-place status-CHECK step.
func TestMigration032UpgradeFrom031(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "m032.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := searchMigratorThrough(t, db, 31).Up(); err != nil {
		t.Fatal(err)
	}
	searchExec(t, db, `INSERT INTO run (id,run_type,status) VALUES (1,'manual','completed'),(2,'scheduled','running'),(3,'scheduled','interrupted')`)
	searchExec(t, db, `INSERT INTO video (id, video_id, channel_id, processing_state, final_outcome) VALUES (1,'v','UC0','not_yet_evaluated','not_yet_evaluated')`)
	searchExec(t, db, `INSERT INTO run_video_outcome(run_id,video_id,updated_at,retry_claim_provenance) VALUES (2,1,'2026-11-20T00:00:00Z','{"version":1,"deferred_rows":[],"error_rows":[]}')`)
	searchExec(t, db, `INSERT INTO transient_metadata(video_id,title,expires_at) VALUES ('t','Title','2030-01-01')`)
	before := dispatchStopDump(t, db)
	var runsBefore int
	if err := db.QueryRow(`SELECT count(*) FROM run`).Scan(&runsBefore); err != nil {
		t.Fatal(err)
	}

	if err := searchMigratorThrough(t, db, 32).Up(); err != nil {
		t.Fatalf("031 to 032: %v", err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != 32 {
		t.Fatalf("version %d: %v", version, err)
	}

	// Old runs read NULL and keep their other values.
	var runsAfter, nulls int
	if err := db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE dispatch_stop IS NULL) FROM run`).Scan(&runsAfter, &nulls); err != nil || runsAfter != runsBefore || nulls != runsBefore {
		t.Fatalf("runs %d->%d, NULL dispatch_stop %d: %v", runsBefore, runsAfter, nulls, err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM run WHERE id=3`).Scan(&status); err != nil || status != "interrupted" {
		t.Fatalf("run status %q: %v", status, err)
	}
	if provenanceCount(t, db, `SELECT count(*) FROM pragma_table_info('run') WHERE name='dispatch_stop' AND type='TEXT' AND "notnull"=0 AND dflt_value IS NULL`) != 1 {
		t.Fatal("dispatch_stop must be a nullable TEXT column with no default")
	}

	// Nothing else changed; the 028 triggers, 029 search index and 031 column
	// are intact. The run table itself is compared separately below.
	after := dispatchStopDump(t, db)
	if len(before) != len(after) {
		t.Fatalf("schema objects changed: %d vs %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("object %s changed:\n%s\n%s", k, v, after[k])
		}
	}
	for _, name := range []string{"video_final_outcome_immutable", "run_video_outcome_final_immutable", "discovery_source_failure_immutable", "transient_metadata_search_purge_update", "run_dispatch_stop_raise_only"} {
		if provenanceCount(t, db, `SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name) != 1 {
			t.Fatalf("trigger %s is missing", name)
		}
	}
	searchInitialState(t, db)
	if provenanceCount(t, db, `SELECT count(*) FROM pragma_table_info('run_video_outcome') WHERE name='retry_claim_provenance'`) != 1 {
		t.Fatal("031 provenance column is missing")
	}
	var prov string
	if err := db.QueryRow(`SELECT retry_claim_provenance FROM run_video_outcome`).Scan(&prov); err != nil || prov == "" {
		t.Fatalf("031 receipt changed: %q %v", prov, err)
	}

	// Upgraded path: the status CHECK is still the old text, so the step runs
	// once and then is skipped. Idempotent, and the column survives it.
	var def string
	for i := 0; i < 3; i++ {
		if err := widenRunStatusCheck(context.Background(), db); err != nil {
			t.Fatalf("widenRunStatusCheck pass %d: %v", i, err)
		}
		if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='run'`).Scan(&def); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(def, "'stopped'") || !strings.Contains(def, "dispatch_stop TEXT") {
			t.Fatalf("pass %d: run definition lost the widening or the column:\n%s", i, def)
		}
	}
	if strings.Count(def, "dispatch_stop TEXT") != 1 {
		t.Fatalf("column defined more than once:\n%s", def)
	}
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q: %v", check, err)
	}
	assertDispatchStopRules(t, db)
}

// TestMigration032CleanInstallAndReopen runs the shipped chain on an empty
// database, where the status-CHECK step runs after 032, then reopens it.
func TestMigration032CleanInstallAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "clean032.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var def string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='run'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "'stopped'") || strings.Count(def, "dispatch_stop TEXT") != 1 {
		t.Fatalf("fresh install run definition:\n%s", def)
	}
	assertDispatchStopRules(t, db)
	searchExec(t, db, `UPDATE run SET dispatch_stop='authorization' WHERE id=902`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: the step is skipped, the value is kept and the trigger holds.
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v := dispatchStopValue(t, db, 902); v.String != "authorization" {
		t.Fatalf("value lost across reopen: %q", v.String)
	}
	if _, err := db.Exec(`UPDATE run SET dispatch_stop=NULL WHERE id=902`); err == nil {
		t.Fatal("clearing was accepted after reopen")
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version < 32 {
		t.Fatalf("version %d: %v", version, err)
	}
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q: %v", check, err)
	}
}
