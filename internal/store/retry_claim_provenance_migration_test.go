package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Run-initiation ARS-005 §5.1.1 and §6.1.1: migration 031 adds the nullable
// retry_claim_provenance column and clears the Store A defaults, once.

func provenanceSchemaDump(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT type||'|'||name, coalesce(sql,'') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name <> 'run_video_outcome'`)
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

func provenanceCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// TestMigration031UpgradeFrom028Through029And030 upgrades a populated 028
// database one real migration at a time and checks what 031 changes and what
// it must not.
func TestMigration031UpgradeFrom028Through029And030(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "m031.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := searchMigratorThrough(t, db, 28).Up(); err != nil {
		t.Fatal(err)
	}

	// Legacy population: runs, a video with a legacy retry claim (membership
	// and owner pointers, no provenance), terminal history, native membership,
	// guards-bearing tables untouched, and Store A rows with old defaults.
	searchExec(t, db, `INSERT INTO run (run_type,status) VALUES ('manual','completed'),('scheduled','running'),('scheduled','running')`)
	searchExec(t, db, `INSERT INTO video (id, video_id, channel_id, processing_state, final_outcome) VALUES
		(1,'legacy-claim','UC0','not_yet_evaluated','not_yet_evaluated'),
		(2,'native','UC0','completed','accepted'),
		(3,'terminal','UC0','completed','accepted')`)
	searchExec(t, db, `INSERT INTO deferred_item (video_id,run_id,deferral_reason,status,retry_count,retry_run_id)
		VALUES (1,1,'quota_exhausted','retrying',1,2),(3,1,'quota_exhausted','completed',1,2)`)
	searchExec(t, db, `INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at) VALUES
		(2,1,NULL,'2026-11-01T00:00:00Z'),(3,2,'accepted','2026-11-01T00:00:00Z'),(2,3,'accepted','2026-11-01T00:00:00Z')`)
	searchExec(t, db, `INSERT INTO transient_metadata(video_id,title,channel_name,description,duration_seconds,expires_at) VALUES
		('empty','Keep Me','','',0,'2030-01-01'),
		('real','Keep Me Too','Chan','Desc',123,'2030-01-01'),
		('titleless','','','',0,'2030-01-01')`)
	searchExec(t, db, `INSERT INTO transient_metadata(video_id,title,expires_at) VALUES ('nulls','Nulls','2030-01-01')`)

	if err := searchMigratorThrough(t, db, 29).Up(); err != nil {
		t.Fatalf("028 to 029: %v", err)
	}
	if err := searchMigratorThrough(t, db, 30).Up(); err != nil {
		t.Fatalf("029 to 030: %v", err)
	}
	searchInitialState(t, db)
	before := provenanceSchemaDump(t, db)
	var fetched []string
	rows, err := db.Query(`SELECT video_id||'|'||title||'|'||fetched_at||'|'||expires_at||'|'||is_active FROM transient_metadata ORDER BY video_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		fetched = append(fetched, s)
	}
	rows.Close()
	searchRows := provenanceCount(t, db, `SELECT count(*) FROM title_search_doc`)

	if err := searchMigratorThrough(t, db, 31).Up(); err != nil {
		t.Fatalf("030 to 031: %v", err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != 31 {
		t.Fatalf("version %d: %v", version, err)
	}

	// Only the one column changes; every other object, including the 028
	// finality triggers, the 029 Search objects and the 030 table, is identical.
	after := provenanceSchemaDump(t, db)
	if len(before) != len(after) {
		t.Fatalf("schema objects changed: %d vs %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("object %s changed:\n%s\n%s", k, v, after[k])
		}
	}
	for _, name := range []string{"video_final_outcome_immutable", "run_video_outcome_final_immutable", "discovery_source_failure_immutable", "transient_metadata_search_purge_update"} {
		if provenanceCount(t, db, `SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name) != 1 {
			t.Fatalf("trigger %s is missing", name)
		}
	}
	if provenanceCount(t, db, `SELECT count(*) FROM pragma_table_info('run_video_outcome') WHERE name='retry_claim_provenance' AND type='TEXT' AND "notnull"=0 AND dflt_value IS NULL`) != 1 {
		t.Fatal("retry_claim_provenance must be a nullable TEXT column with no default")
	}

	// Legacy stays NULL: no backfill from owner pointers, counts or history.
	if got := provenanceCount(t, db, `SELECT count(*) FROM run_video_outcome WHERE retry_claim_provenance IS NULL`); got != 3 {
		t.Fatalf("legacy membership rows with NULL provenance = %d, want 3", got)
	}
	if got := provenanceCount(t, db, `SELECT count(*) FROM deferred_item WHERE status='retrying' AND retry_run_id=2 AND video_id=1`); got != 1 {
		t.Fatalf("legacy owner pointer changed: %d", got)
	}
	if got := provenanceCount(t, db, `SELECT count(*) FROM run_video_outcome`); got != 3 {
		t.Fatalf("membership rows = %d, want 3", got)
	}

	// Store A: exactly the three defaults cleared; nothing else.
	for _, c := range []struct {
		id                   string
		desc, name, duration any
	}{
		{"empty", nil, nil, nil},
		{"real", "Desc", "Chan", int64(123)},
		{"titleless", nil, nil, nil},
		{"nulls", nil, nil, nil},
	} {
		var desc, name sql.NullString
		var dur sql.NullInt64
		if err := db.QueryRow(`SELECT description, channel_name, duration_seconds FROM transient_metadata WHERE video_id=?`, c.id).Scan(&desc, &name, &dur); err != nil {
			t.Fatal(err)
		}
		if (c.desc == nil) == desc.Valid || (c.name == nil) == name.Valid || (c.duration == nil) == dur.Valid {
			t.Fatalf("%s: description=%v channel_name=%v duration=%v", c.id, desc, name, dur)
		}
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM transient_metadata WHERE video_id='titleless'`).Scan(&title); err != nil || title != "" {
		t.Fatalf("title must not be cleared: %q %v", title, err)
	}
	rows, err = db.Query(`SELECT video_id||'|'||title||'|'||fetched_at||'|'||expires_at||'|'||is_active FROM transient_metadata ORDER BY video_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		if i >= len(fetched) || fetched[i] != s {
			t.Fatalf("row %d changed: %q vs %q", i, fetched[i], s)
		}
		i++
	}
	if i != len(fetched) {
		t.Fatalf("Store A rows %d, want %d", i, len(fetched))
	}
	if got := provenanceCount(t, db, `SELECT count(*) FROM title_search_doc`); got != searchRows {
		t.Fatalf("search documents changed: %d vs %d", got, searchRows)
	}
	searchInitialState(t, db)

	// The migration version is the marker: a later real '' or 0 is kept, also
	// across a normal reopen and a second Up.
	searchExec(t, db, `UPDATE transient_metadata SET description='', duration_seconds=0 WHERE video_id='real'`)
	if err := searchMigratorThrough(t, db, 31).Up(); err != nil && err.Error() != "no change" {
		t.Fatalf("second Up: %v", err)
	}
	var desc sql.NullString
	var dur sql.NullInt64
	if err := db.QueryRow(`SELECT description, duration_seconds FROM transient_metadata WHERE video_id='real'`).Scan(&desc, &dur); err != nil || !desc.Valid || desc.String != "" || !dur.Valid || dur.Int64 != 0 {
		t.Fatalf("later real values were erased: %v %v %v", desc, dur, err)
	}
}

// TestMigration031CleanDatabaseAndReopen runs the shipped chain on an
// empty database through Open and checks the column holds a value across reopen.
func TestMigration031CleanDatabaseAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "clean.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	searchExec(t, db, `INSERT INTO run (run_type,status) VALUES ('scheduled','running')`)
	searchExec(t, db, `INSERT INTO video (id, video_id, channel_id, processing_state) VALUES (1,'v','UC0','not_yet_evaluated')`)
	searchExec(t, db, `INSERT INTO run_video_outcome(run_id,video_id,updated_at,retry_claim_provenance) VALUES (1,1,'2026-11-20T00:00:00Z','{"version":1,"deferred_rows":[],"error_rows":[]}')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v string
	if err := db.QueryRow(`SELECT retry_claim_provenance FROM run_video_outcome`).Scan(&v); err != nil || v == "" {
		t.Fatalf("value lost across reopen: %q %v", v, err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version < 31 {
		t.Fatalf("version %d: %v", version, err)
	}
}
