package store

import (
	"database/sql"
	"errors"
	"path"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

// Discovery ARS-002 section 7 test 1: migration 030 is one additive table.

func sourceFailureSchemaDump(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT type||'|'||name||'|'||coalesce(sql,'') FROM sqlite_master
		WHERE name NOT LIKE 'discovery_source_failure%' AND name <> 'run' AND name NOT LIKE 'sqlite_%' AND tbl_name NOT LIKE 'discovery_source_failure%'
		ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestMigration030AddsOnlyTheSourceFailureTable(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteDSN(path.Join(t.TempDir(), "m030.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A populated post-Search (029) database.
	if err := searchMigratorThrough(t, db, 29).Up(); err != nil {
		t.Fatal(err)
	}
	searchExec(t, db, `INSERT INTO run (run_type,status) VALUES ('manual','completed'),('scheduled','completed')`)
	searchExec(t, db, `INSERT INTO video (id, video_id, channel_id, processing_state, final_outcome) VALUES (1,'a','UC'||substr('x',1,0)||'c','completed','accepted')`)
	searchExec(t, db, `INSERT INTO transient_metadata(video_id,title,expires_at) VALUES ('a','original','2030-01-01')`)
	before := sourceFailureSchemaDump(t, db)
	var runs, videos int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM run), (SELECT COUNT(*) FROM video)`).Scan(&runs, &videos); err != nil {
		t.Fatal(err)
	}

	head := searchMigratorThrough(t, db, 30)
	if err := head.Up(); err != nil {
		t.Fatalf("029 to 030: %v", err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != 30 {
		t.Fatalf("version %d: %v", version, err)
	}
	after := sourceFailureSchemaDump(t, db)
	if len(before) != len(after) {
		t.Fatalf("schema objects changed: %d vs %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("existing object changed:\n%s\n%s", before[i], after[i])
		}
	}
	// The run.discovery_scan_outcome column: NULL on existing rows, CHECKed.
	var nulls int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run WHERE discovery_scan_outcome IS NULL`).Scan(&nulls); err != nil || nulls != 2 {
		t.Fatalf("existing runs must read NULL: %d %v", nulls, err)
	}
	for _, v := range []string{"completed", "cut_short", "no_source_scanned"} {
		searchExec(t, db, `UPDATE run SET discovery_scan_outcome = ? WHERE id = 1`, v)
	}
	if _, err := db.Exec(`UPDATE run SET discovery_scan_outcome = 'other' WHERE id = 1`); err == nil {
		t.Fatal("the scan outcome CHECK must refuse an unknown value")
	}
	searchExec(t, db, `UPDATE run SET discovery_scan_outcome = NULL WHERE id = 1`)
	var runs2, videos2 int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM run), (SELECT COUNT(*) FROM video)`).Scan(&runs2, &videos2); err != nil || runs != runs2 || videos != videos2 {
		t.Fatalf("rows changed: %d/%d vs %d/%d %v", runs, videos, runs2, videos2, err)
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM transient_metadata WHERE video_id='a'`).Scan(&title); err != nil || title != "original" {
		t.Fatalf("metadata: %q %v", title, err)
	}

	chB := "UC" + "BBBBBBBBBBBBBBBBBBBBBB"
	ins := `INSERT INTO discovery_source_failure (run_id, channel_id, occurred_at, category, operation, http_status, reason)
	        VALUES (?, ?, '2026-11-20T00:00:00Z', ?, ?, ?, ?)`
	searchExec(t, db, ins, 1, chB, "permanent", "playlist_items_list", 404, "playlistNotFound")

	// Immutable.
	for _, upd := range []string{
		`UPDATE discovery_source_failure SET reason = 'x'`,
		`UPDATE discovery_source_failure SET run_id = 2`,
	} {
		if _, err := db.Exec(upd); err == nil {
			t.Fatalf("%s must abort", upd)
		}
	}
	// One permanent row per run and channel, in SQL too; a transient one is not limited.
	if _, err := db.Exec(ins, 1, chB, "permanent", "playlist_items_list", 404, "playlistNotFound"); err == nil {
		t.Fatal("a second permanent row for the same run and channel must violate the unique index")
	}
	searchExec(t, db, ins, 2, chB, "permanent", "playlist_items_list", 404, "playlistNotFound")
	searchExec(t, db, ins, 1, chB, "transient", "playlist_items_list", 500, "backendError")
	searchExec(t, db, ins, 1, chB, "transient", "playlist_items_list", 500, "backendError")

	// CHECKs.
	for name, args := range map[string][]any{
		"short channel":    {1, "UCshort", "permanent", "playlist_items_list", 404, "playlistNotFound"},
		"wrong prefix":     {1, "XX" + "BBBBBBBBBBBBBBBBBBBBBB", "permanent", "playlist_items_list", 404, "playlistNotFound"},
		"category":         {1, chB, "other", "playlist_items_list", 404, "playlistNotFound"},
		"operation":        {1, chB, "permanent", "videos_list", 404, "playlistNotFound"},
		"status too low":   {1, chB, "permanent", "playlist_items_list", 99, "playlistNotFound"},
		"status too high":  {1, chB, "permanent", "playlist_items_list", 600, "playlistNotFound"},
		"empty reason":     {1, chB, "permanent", "playlist_items_list", 404, ""},
		"over-long reason": {1, chB, "permanent", "playlist_items_list", 404, string(make([]byte, 65))},
	} {
		if name == "over-long reason" {
			args[5] = "0123456789012345678901234567890123456789012345678901234567890123x"
		}
		if _, err := db.Exec(ins, args...); err == nil {
			t.Fatalf("%s must violate a CHECK", name)
		}
	}
	// A run's deletion removes its rows.
	searchExec(t, db, `PRAGMA foreign_keys = ON`)
	searchExec(t, db, `DELETE FROM run WHERE id = 2`)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM discovery_source_failure WHERE run_id = 2`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after run delete: %d %v", n, err)
	}

	// An older embedded chain ending at 029 refuses the version-30 database.
	old := searchMigratorThrough(t, db, 29)
	if err := old.Up(); err == nil || errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("a chain ending at 029 accepted a version-30 database: %v", err)
	}
}
