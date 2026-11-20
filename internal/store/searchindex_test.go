package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	modernsqlite "modernc.org/sqlite"
)

func searchExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func searchSchema(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE name IN
		('title_search_doc','title_search','transient_metadata_search_purge_insert',
		'transient_metadata_search_purge_update','transient_metadata_search_purge_delete')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		result[name] = ddl
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != 5 {
		t.Fatalf("index objects: %v", result)
	}
	return result
}

func searchInitialState(t *testing.T, db *sql.DB) {
	t.Helper()
	var count, unbuilt, secureDelete int
	err := db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE id=1 AND key_version IS NULL AND built_at IS NULL) FROM title_search_state`).Scan(&count, &unbuilt)
	if err != nil || count != 1 || unbuilt != 1 {
		t.Fatalf("initial state: count=%d unbuilt=%d err=%v", count, unbuilt, err)
	}
	err = db.QueryRow(`SELECT coalesce((SELECT v FROM title_search_config WHERE k='secure-delete'), 0)`).Scan(&secureDelete)
	if err != nil || secureDelete != 0 {
		t.Fatalf("secure-delete=%d err=%v", secureDelete, err)
	}
	if _, err := db.Exec(`INSERT INTO title_search_state(id) VALUES(2)`); err == nil {
		t.Fatal("state accepted another id")
	}
}

func searchMigratorThrough(t *testing.T, db *sql.DB, maxVersion int) *migrate.Migrate {
	t.Helper()
	return searchMigratorOverlay(t, db, maxVersion, nil)
}

// searchMigratorOverlay caps the embedded sources at maxVersion and adds extra
// in-memory files. Nothing is written to the shipped migrations directory.
func searchMigratorOverlay(t *testing.T, db *sql.DB, maxVersion int, extra fstest.MapFS) *migrate.Migrate {
	t.Helper()
	files := fstest.MapFS{}
	for name, f := range extra {
		files[name] = f
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil {
			t.Fatalf("invalid embedded migration name %q", entry.Name())
		}
		if version > maxVersion {
			continue
		}
		data, err := migrationsFS.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = &fstest.MapFile{Data: data}
	}
	src, err := iofs.New(files, ".")
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
	return m
}

func TestSearchMigrationRecreateAndRollback(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteDSN(path.Join(t.TempDir(), "search.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := searchMigratorThrough(t, db, 28)
	if err := old.Up(); err != nil {
		t.Fatal(err)
	}
	searchExec(t, db, `INSERT INTO transient_metadata(video_id,title,expires_at) VALUES ('a','original','2030-01-01')`)
	upTo29 := searchMigratorThrough(t, db, 29)
	if err := upTo29.Migrate(29); err != nil {
		t.Fatal(err)
	}
	searchInitialState(t, db)
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != 29 {
		t.Fatalf("migration version %d: %v", version, err)
	}
	before := searchSchema(t, db)
	searchExec(t, db, `INSERT INTO title_search_doc(doc_id,video_id) VALUES (42,'a')`)
	searchExec(t, db, `INSERT INTO title_search(rowid,k) VALUES (42,'original')`)
	searchExec(t, db, `VACUUM`)
	var id int
	if err := db.QueryRow(`SELECT doc_id FROM title_search_doc WHERE video_id='a'`).Scan(&id); err != nil || id != 42 {
		t.Fatalf("doc id after vacuum: %d, %v", id, err)
	}
	if err := RecreateSearchIndex(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	after := searchSchema(t, db)
	for name, ddl := range before {
		if after[name] != ddl {
			t.Fatalf("DDL differs for %s: %q vs %q", name, ddl, after[name])
		}
	}
	var idxDDL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='run_video_outcome_video_run'`).Scan(&idxDDL); err != nil ||
		!strings.Contains(idxDDL, "run_video_outcome(video_id, run_id)") {
		t.Fatalf("RecreateSearchIndex must leave run_video_outcome_video_run in place: %q %v", idxDDL, err)
	}
	if err := old.Up(); err == nil || errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("028 source accepted 029 database: %v", err)
	}
	searchExec(t, db, `BEGIN;
		DROP TRIGGER transient_metadata_search_purge_insert;
		DROP TRIGGER transient_metadata_search_purge_update;
		DROP TRIGGER transient_metadata_search_purge_delete;
		DROP INDEX run_video_outcome_video_run;
		DROP TABLE title_search; DROP TABLE title_search_doc; DROP TABLE title_search_state;
		UPDATE schema_migrations SET version=28, dirty=0; COMMIT;`)
	if err := old.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("028 source after reset: %v", err)
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM transient_metadata WHERE video_id='a'`).Scan(&title); err != nil || title != "original" {
		t.Fatalf("metadata after reset: %q %v", title, err)
	}
	if err := upTo29.Migrate(29); err != nil {
		t.Fatalf("re-upgrade: %v", err)
	}
}

// Later migrations must preserve the index and its purge triggers. Compare the
// actual embedded head against the independently capped 029 schema.
func TestSearchIndexAtMigrationHead(t *testing.T) {
	open := func(name string) *sql.DB {
		t.Helper()
		db, err := sql.Open("sqlite", sqliteDSN(path.Join(t.TempDir(), name)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	historical := open("v29.db")
	if err := searchMigratorThrough(t, historical, 29).Migrate(29); err != nil {
		t.Fatal(err)
	}
	head := open("head.db")
	if err := ApplyMigrations(head); err != nil {
		t.Fatal(err)
	}
	want, got := searchSchema(t, historical), searchSchema(t, head)
	for name, ddl := range want {
		if got[name] != ddl {
			t.Fatalf("head DDL differs from migration 029 for %s: %q vs %q", name, got[name], ddl)
		}
	}
}

// A migration above 029 must leave the 029 index intact. The higher migration
// exists only in this scratch overlay; no placeholder ships.
func TestSearchIndexSurvivesHigherMigrationOverlay(t *testing.T) {
	open := func(name string) *sql.DB {
		t.Helper()
		db, err := sql.Open("sqlite", sqliteDSN(path.Join(t.TempDir(), name)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	historical := open("v29.db")
	if err := searchMigratorThrough(t, historical, 29).Migrate(29); err != nil {
		t.Fatal(err)
	}
	var head uint
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		prefix, _, _ := strings.Cut(entry.Name(), "_")
		n, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("invalid embedded migration name %q", entry.Name())
		}
		head = max(head, uint(n))
	}
	higher := head + 1
	overlay := open("overlay.db")
	m := searchMigratorOverlay(t, overlay, int(head), fstest.MapFS{
		fmt.Sprintf("%03d_scratch.up.sql", higher): {Data: []byte(`CREATE TABLE scratch_higher_migration (id INTEGER PRIMARY KEY);`)},
	})
	if err := m.Migrate(higher); err != nil {
		t.Fatal(err)
	}
	var version uint
	if err := overlay.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != higher {
		t.Fatalf("overlay version %d: %v", version, err)
	}
	want, got := searchSchema(t, historical), searchSchema(t, overlay)
	for name, ddl := range want {
		if got[name] != ddl {
			t.Fatalf("overlay DDL differs from migration 029 for %s: %q vs %q", name, got[name], ddl)
		}
	}
	searchInitialState(t, overlay)
}

func TestSearchPurgeReplacement(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteDSN(path.Join(t.TempDir(), "search.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ApplyMigrations(db); err != nil {
		t.Fatal(err)
	}
	searchInitialState(t, db)
	searchExec(t, db, `INSERT INTO transient_metadata(video_id,title,expires_at) VALUES ('a','first','2030'),('b','second','2030')`)
	searchExec(t, db, `INSERT INTO title_search_doc(doc_id,video_id) VALUES (1,'a'),(2,'b')`)
	searchExec(t, db, `INSERT INTO title_search(rowid,k) VALUES (1,'firstmarker'),(2,'secondmarker')`)
	searchExec(t, db, `UPDATE OR REPLACE transient_metadata SET video_id='b' WHERE video_id='a'`)
	for _, table := range []string{"title_search", "title_search_doc", "title_search_content", "title_search_docsize"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s retained %d rows: %v", table, n, err)
		}
	}
}

func TestSearchPurgeWrites(t *testing.T) {
	for _, statement := range []string{
		`INSERT OR REPLACE INTO transient_metadata(video_id,title,expires_at) VALUES ('a','new','2030')`,
		`UPDATE transient_metadata SET title='new' WHERE video_id='a'`,
		`UPDATE transient_metadata SET is_active=0 WHERE video_id='a'`,
		`UPDATE transient_metadata SET expired_at='2026' WHERE video_id='a'`,
		`UPDATE transient_metadata SET fetched_at='2026' WHERE video_id='a'`,
		`UPDATE transient_metadata SET expires_at='2026' WHERE video_id='a'`,
		`UPDATE transient_metadata SET video_id='c' WHERE video_id='a'`,
		`DELETE FROM transient_metadata WHERE video_id='a'`,
	} {
		t.Run(statement, func(t *testing.T) {
			db, err := sql.Open("sqlite", sqliteDSN(path.Join(t.TempDir(), "search.db")))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := ApplyMigrations(db); err != nil {
				t.Fatal(err)
			}
			searchExec(t, db, `INSERT INTO transient_metadata(video_id,title,expires_at) VALUES ('a','first','2030')`)
			searchExec(t, db, `INSERT INTO title_search_doc(doc_id,video_id) VALUES (1,'a')`)
			searchExec(t, db, `INSERT INTO title_search(rowid,k) VALUES (1,'firstmarker')`)
			searchExec(t, db, statement)
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM title_search WHERE title_search MATCH '"firstmarker"'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("stale match: %d, %v", n, err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM title_search_doc`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("stale mapping: %d, %v", n, err)
			}
		})
	}
}

func TestSearchRecreateFailureRollsBack(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteDSN(path.Join(t.TempDir(), "search.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := ApplyMigrations(db); err != nil {
		t.Fatal(err)
	}
	searchExec(t, db, `DROP TRIGGER transient_metadata_search_purge_delete`)
	if err := RecreateSearchIndex(context.Background(), db); err == nil {
		t.Fatal("damaged index unexpectedly recreated")
	}
	// The preceding drops were rolled back and the pooled connection is usable.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'transient_metadata_search_purge_%'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rollback trigger count %d: %v", n, err)
	}
	searchExec(t, db, `BEGIN IMMEDIATE; ROLLBACK;`)
}

// faultConnector wraps the real SQLite driver only at transaction control
// statements; all schema and transaction work is performed by SQLite.
type faultConnector struct {
	dsn          string
	cancel       context.CancelFunc
	failRollback bool
	failCommit   bool
	closed       atomic.Int32
	rollbacks    atomic.Int32
}

func (c *faultConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&modernsqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &faultConn{Conn: conn, owner: c}, nil
}

func (*faultConnector) Driver() driver.Driver { return &modernsqlite.Driver{} }

type faultConn struct {
	driver.Conn
	owner *faultConnector
}

func (c *faultConn) Close() error {
	c.owner.closed.Add(1)
	return c.Conn.Close()
}

func (c *faultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if query == "ROLLBACK" {
		c.owner.rollbacks.Add(1)
		if c.owner.failRollback {
			return nil, errors.New("injected rollback failure")
		}
	}
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	if err == nil && query == "BEGIN IMMEDIATE" && c.owner.cancel != nil {
		c.owner.cancel()
	}
	if err == nil && query == "COMMIT" && c.owner.failCommit {
		return nil, errors.New("injected lost commit acknowledgement")
	}
	return result, err
}

func TestSearchRecreateTransactionCleanup(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancel       bool
		failRollback bool
		failCommit   bool
		wantDiscard  bool
	}{
		{name: "cancellation after begin rolls back", cancel: true},
		{name: "failed rollback discards connection", cancel: true, failRollback: true, wantDiscard: true},
		{name: "lost commit acknowledgement discards connection", failCommit: true, wantDiscard: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := sqliteDSN(path.Join(t.TempDir(), "search.db"))
			setup, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			if err := ApplyMigrations(setup); err != nil {
				t.Fatal(err)
			}
			searchExec(t, setup, `UPDATE title_search_state SET key_version='old', built_at='old' WHERE id=1`)
			if err := setup.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fault := &faultConnector{dsn: dsn, failRollback: tc.failRollback, failCommit: tc.failCommit}
			if tc.cancel {
				fault.cancel = cancel
			}
			db := sql.OpenDB(fault)
			db.SetMaxOpenConns(1)
			defer db.Close()
			err = RecreateSearchIndex(ctx, db)
			if err == nil {
				t.Fatal("expected transaction error")
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			if tc.failRollback && !strings.Contains(err.Error(), "injected rollback failure") {
				t.Fatalf("rollback failure lost: %v", err)
			}
			if errors.Is(err, sql.ErrConnDone) {
				t.Fatalf("expected discard must not add a closed-connection error: %v", err)
			}
			if fault.rollbacks.Load() != 1 {
				t.Fatalf("rollback attempts: %d", fault.rollbacks.Load())
			}
			if got := fault.closed.Load(); (got > 0) != tc.wantDiscard {
				t.Fatalf("physical connection closes=%d, want discard=%v", got, tc.wantDiscard)
			}
			// A fresh or safely reused connection can acquire a write lock.
			searchExec(t, db, `BEGIN IMMEDIATE; ROLLBACK;`)
			var version sql.NullString
			if err := db.QueryRow(`SELECT key_version FROM title_search_state WHERE id=1`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if tc.failCommit {
				if version.Valid {
					t.Fatalf("SQLite committed despite lost acknowledgement; got %v", version)
				}
			} else if !version.Valid || version.String != "old" {
				t.Fatalf("rollback did not preserve state: %v", version)
			}
		})
	}
}
