package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestMigration014PreservesLegacyEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	src, err := iofs.New(os.DirFS("migrations"), ".")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(13); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
	 INSERT INTO config_version (id, snapshot, change_description) VALUES (1, '{ "duplicate_behavior": "skip", "routes": [] }', 'legacy bytes');
	 INSERT INTO run (id, run_type, status, config_version_id) VALUES (1, 'manual', 'interrupted', 1), (2, 'backtest', 'completed', 1);
	 INSERT INTO video (id, video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (1, 'legacy-video', 'channel', 1, 'acted', 'deferred');
	 INSERT INTO route (id, name, evaluation_order, condition_composition) VALUES (1, 'legacy-route', 1, 'AND');
	 INSERT INTO route_decision (id, video_id, run_id, route_id, evaluation_order, decision) VALUES (1, 1, 1, 1, 1, 'matched'), (2, 1, 2, 1, 1, 'matched');
	 INSERT INTO condition_audit (route_decision_id, condition_index, field, operator, expected_value, actual_value, matched) VALUES (1, 0, 'title', 'contains', 'jazz', 'jazz', 1);
	 INSERT INTO executed_action (route_decision_id, run_id, video_id, action_type, target_playlist_id, outcome) VALUES (1, 1, 1, 'playlist_insert', 'old-target', 'succeeded'), (2, 2, 1, 'playlist_insert', 'hypothetical', 'not_attempted');
	 INSERT INTO error_entry (video_id, run_id, error_category, error_message, error_detail, status) VALUES (1, 1, 'runtime', 'old error', 'details', 'retrying');
	 INSERT INTO deferred_item (video_id, run_id, deferral_reason, status) VALUES (1, 1, 'quota_exhausted', 'pending'), (1, 2, 'api_rate_limited', 'retrying');`)
	if err != nil {
		t.Fatal(err)
	}
	// Remember every original column, including stored JSON and timestamps as
	// SQL hex bytes, rather than comparing a normalized model projection.
	tables := []string{"config_version", "run", "video", "route", "route_decision", "condition_audit", "executed_action", "error_entry", "deferred_item"}
	queries, before := map[string]string{}, map[string]string{}
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM ` + table + ` LIMIT 0`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		for i, column := range columns {
			columns[i] = `CASE WHEN "` + column + `" IS NULL THEN 'NULL' ELSE typeof("` + column + `") || ':' || hex("` + column + `") END`
		}
		queries[table] = `SELECT ` + strings.Join(columns, ",") + ` FROM ` + table + ` ORDER BY id`
		before[table] = evidenceBytes(t, db, queries[table])
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if after := evidenceBytes(t, db, queries[table]); after != before[table] {
			t.Errorf("%s history changed\nbefore %s\nafter %s", table, before[table], after)
		}
	}
	var unknown, deferredNull, errorNull int
	if err := db.QueryRow(`SELECT count(*) FROM run WHERE action_authority_version = 0`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM deferred_item WHERE retry_run_id IS NULL`).Scan(&deferredNull); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM error_entry WHERE retry_run_id IS NULL`).Scan(&errorNull); err != nil {
		t.Fatal(err)
	}
	if unknown != 2 || deferredNull != 2 || errorNull != 1 {
		t.Fatalf("inferred historical authority: %d/%d/%d", unknown, deferredNull, errorNull)
	}
	for _, query := range []string{
		`UPDATE run SET action_authority_version = 2 WHERE id = 1`,
		`UPDATE run SET action_authority_version = NULL WHERE id = 1`,
		`UPDATE deferred_item SET retry_run_id = 999 WHERE id = 1`,
		`UPDATE error_entry SET retry_run_id = 999 WHERE id = 1`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Errorf("invalid extension accepted: %s", query)
		}
	}
	// A receiving owner is not an origin FK and has no delete cascade.
	if _, err := db.Exec(`INSERT INTO run (id, run_type, status) VALUES (3, 'manual', 'running'); UPDATE deferred_item SET retry_run_id = 3 WHERE id = 1; UPDATE error_entry SET retry_run_id = 3 WHERE id = 1;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM run WHERE id = 3`); err == nil {
		t.Fatal("deleting receiving owner silently removed retry evidence")
	}
}

func evidenceBytes(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result strings.Builder
	for rows.Next() {
		values, dest := make([]string, len(columns)), make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result.String()
}
