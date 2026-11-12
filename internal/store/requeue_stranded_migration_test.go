package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
)

const unavailable = "unavailable_on_youtube"

func tableSQL(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	var def string
	require.NoError(t, db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&def))
	return def
}

func requireHealthy(t *testing.T, db *sql.DB) {
	t.Helper()
	var check string
	require.NoError(t, db.QueryRow(`PRAGMA integrity_check`).Scan(&check))
	require.Equal(t, "ok", check)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&n))
	require.Zero(t, n)
}

// seed025 fills a database migrated through 025 with rows that the 026 rebuild
// and the CHECK rewrite must leave untouched.
func seed025(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO run (run_type,status) VALUES ('scheduled','completed'),('manual','completed')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO video (id, video_id, channel_id, processing_state, final_outcome) VALUES
		(1,'a','c','deferred','deferred'),(2,'b','c','completed','accepted'),(3,'d','c','failed','failed')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, deferred_reason, updated_at) VALUES
		(1,1,'deferred','quota_exhausted','2026-11-01T00:00:00Z'),(1,2,'accepted',NULL,'2026-11-01T00:00:00Z')`)
	require.NoError(t, err)
	for i, st := range []string{"pending", "retrying", "completed", "discarded"} {
		_, err = db.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status,retry_count,retry_run_id) VALUES (1,1,'quota_exhausted',?,?,CASE WHEN ?='retrying' THEN 2 END)`, st, i, st)
		require.NoError(t, err)
	}
	// Leave a gap: ids 5..6 were used then deleted, so the AUTOINCREMENT
	// high-water mark (6) exceeds max(id) (4).
	_, err = db.Exec(`INSERT INTO deferred_item (id,video_id,run_id,deferral_reason,status) VALUES (5,1,1,'quota_exhausted','pending'),(6,1,1,'quota_exhausted','pending')`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM deferred_item WHERE id IN (5,6)`)
	require.NoError(t, err)
}

func dumpRows(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	r, err := db.Query(query)
	require.NoError(t, err)
	defer r.Close()
	var out []string
	for r.Next() {
		var s string
		require.NoError(t, r.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, r.Err())
	return out
}

const dumpDeferred = `SELECT id||'|'||video_id||'|'||run_id||'|'||deferral_reason||'|'||ifnull(retry_after,'')||'|'||retry_count||'|'||status||'|'||created_at||'|'||ifnull(retry_run_id,'') FROM deferred_item ORDER BY id`

// M1: a 025 database upgrades to 026 keeping every deferred row, id, index and
// the sequence high-water mark; the new column, indexes and reason are present.
func TestMigration026PreservesDeferredItemsAndAddsSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m026.db")
	db := migrateTo(t, path, 25)
	seed025(t, db)
	before := dumpRows(t, db, dumpDeferred)
	require.Len(t, before, 4)
	var seqBefore int
	require.NoError(t, db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='deferred_item'`).Scan(&seqBefore))
	require.Equal(t, 6, seqBefore)
	require.NoError(t, db.Close())

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Equal(t, before, dumpRows(t, db, dumpDeferred))
	var seq int
	require.NoError(t, db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='deferred_item'`).Scan(&seq))
	require.Equal(t, seqBefore, seq, "AUTOINCREMENT high-water mark survives the rebuild")
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name='deferred_item_new'`).Scan(&n))
	require.Zero(t, n)

	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name IN
		('idx_deferred_item_status','deferred_item_retry_owner','deferred_item_video_status','error_entry_video_status')`).Scan(&n))
	require.Equal(t, 4, n)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_index_info('deferred_item_video_status') WHERE name IN ('video_id','status')`).Scan(&n))
	require.Equal(t, 2, n)

	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM run WHERE videos_unavailable = 0`).Scan(&n))
	require.Equal(t, 2, n, "existing runs default to zero")

	_, err = db.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status) VALUES (1,1,'requeued_by_user','pending')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status) VALUES (1,1,'nope','pending')`)
	require.ErrorContains(t, err, "CHECK constraint failed")
	var id int
	require.NoError(t, db.QueryRow(`SELECT MAX(id) FROM deferred_item`).Scan(&id))
	require.Equal(t, 7, id, "new ids continue after the preserved high-water mark")

	// Existing rows in the widened tables are intact.
	require.Equal(t, []string{"1|deferred", "2|accepted", "3|failed"},
		dumpRows(t, db, `SELECT id||'|'||final_outcome FROM video ORDER BY id`))
	require.Equal(t, []string{"1|1|deferred|quota_exhausted", "1|2|accepted|"},
		dumpRows(t, db, `SELECT run_id||'|'||video_id||'|'||final_outcome||'|'||ifnull(deferred_reason,'') FROM run_video_outcome ORDER BY video_id`))
	requireHealthy(t, db)
}

// M2: both final_outcome CHECKs accept the new value and still reject others,
// and the widened definitions survive a reopen.
func TestWidenFinalOutcomeChecksAcceptsAndRejects(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m2.db")
	db := migrateTo(t, path, 25)
	seed025(t, db)
	require.NoError(t, db.Close())

	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Contains(t, tableSQL(t, db, "video"), unavailable)
	require.Contains(t, tableSQL(t, db, "run_video_outcome"), unavailable)

	_, err = db.Exec(`UPDATE video SET final_outcome=? WHERE id=3`, unavailable)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, updated_at) VALUES (2,3,?,'2026-11-01T00:00:00Z')`, unavailable)
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE video SET final_outcome='bogus' WHERE id=3`)
	require.ErrorContains(t, err, "CHECK constraint failed")
	_, err = db.Exec(`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, updated_at) VALUES (2,2,'bogus','2026-11-01T00:00:00Z')`)
	require.ErrorContains(t, err, "CHECK constraint failed")
	// The companion CHECKs of run_video_outcome are unchanged: the new outcome
	// carries neither a deferred reason nor an error message.
	_, err = db.Exec(`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, deferred_reason, updated_at) VALUES (2,1,?,'quota_exhausted','2026-11-01T00:00:00Z')`, unavailable)
	require.ErrorContains(t, err, "CHECK constraint failed")

	require.NoError(t, db.Close())
	db, err = store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.Equal(t, 1, strings.Count(tableSQL(t, db, "video"), unavailable), "reopen does not widen twice")
	require.Equal(t, 1, strings.Count(tableSQL(t, db, "run_video_outcome"), unavailable))
	requireHealthy(t, db)
}

// M3a: re-running the step is a no-op and bumps schema_version only once.
func TestWidenFinalOutcomeChecksIsIdempotent(t *testing.T) {
	db := migrateTo(t, filepath.Join(t.TempDir(), "m3a.db"), 26)
	require.NoError(t, store.ApplyMigrations(db))
	v := schemaVersion(t, db)
	require.NoError(t, store.ApplyMigrations(db))
	require.Equal(t, v, schemaVersion(t, db))
	require.Equal(t, 1, strings.Count(tableSQL(t, db, "video"), unavailable))
	require.Equal(t, 1, strings.Count(tableSQL(t, db, "run_video_outcome"), unavailable))
	requireHealthy(t, db)
}

// M3b: a database widened for only one table is completed per table.
func TestWidenFinalOutcomeChecksCompletesHalfWidenedDatabase(t *testing.T) {
	for _, widened := range []string{"video", "run_video_outcome"} {
		t.Run("only_"+widened, func(t *testing.T) {
			db := migrateTo(t, filepath.Join(t.TempDir(), "m3b.db"), 26)
			seed025(t, db)
			// Hand-widen one table exactly as the step does, leaving the other.
			def := tableSQL(t, db, widened)
			var oldCheck, newCheck string
			if widened == "video" {
				oldCheck = `'not_yet_evaluated'))`
				newCheck = `'not_yet_evaluated', 'unavailable_on_youtube'))`
			} else {
				oldCheck = `'failed','not_yet_evaluated')`
				newCheck = `'failed','not_yet_evaluated','unavailable_on_youtube')`
			}
			require.Contains(t, def, oldCheck)
			v := schemaVersion(t, db)
			_, err := db.Exec(`PRAGMA writable_schema=ON`)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE sqlite_schema SET sql = replace(sql, ?, ?) WHERE type='table' AND name=?`, oldCheck, newCheck, widened)
			require.NoError(t, err)
			_, err = db.Exec(`PRAGMA schema_version = ` + strconv.Itoa(v+1))
			require.NoError(t, err)
			_, err = db.Exec(`PRAGMA writable_schema=OFF`)
			require.NoError(t, err)
			require.Contains(t, tableSQL(t, db, widened), unavailable)

			require.NoError(t, store.ApplyMigrations(db))
			require.Contains(t, tableSQL(t, db, "video"), unavailable)
			require.Contains(t, tableSQL(t, db, "run_video_outcome"), unavailable)
			require.Equal(t, 1, strings.Count(tableSQL(t, db, "video"), unavailable))
			require.Equal(t, 1, strings.Count(tableSQL(t, db, "run_video_outcome"), unavailable))

			_, err = db.Exec(`UPDATE video SET final_outcome=? WHERE id=3`, unavailable)
			require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO run_video_outcome (run_id, video_id, final_outcome, updated_at) VALUES (2,3,?,'2026-11-01T00:00:00Z')`, unavailable)
			require.NoError(t, err)
			requireHealthy(t, db)
		})
	}
}
