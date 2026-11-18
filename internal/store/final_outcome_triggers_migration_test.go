package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/golang-migrate/migrate/v4"
	migrationsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
)

var finalOutcomes = []string{"accepted", "rejected", "excluded", "unavailable_on_youtube"}

// seedTriggerVideo inserts one video and one run_video_outcome row with the
// given outcome. run_video_outcome is the only table with a deferred or failed
// CHECK on its reason columns, so those outcomes carry a reason.
func seedTriggerVideo(t *testing.T, db *sql.DB, id int, outcome any) {
	t.Helper()
	_, err := db.Exec(`INSERT OR IGNORE INTO run (id,run_type,status) VALUES (1,'scheduled','completed')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO video (id,video_id,channel_id,processing_state,final_outcome) VALUES (?,?,'c','evaluated',?)`,
		id, "v"+string(rune('a'+id)), outcome)
	require.NoError(t, err)
	reason, msg := any(nil), any(nil)
	switch outcome {
	case "deferred":
		reason = "quota_exhausted"
	case "failed":
		msg = "boom"
	}
	_, err = db.Exec(`INSERT INTO run_video_outcome (run_id,video_id,final_outcome,deferred_reason,error_message,updated_at) VALUES (1,?,?,?,?,'2026-01-01T00:00:00Z')`,
		id, outcome, reason, msg)
	require.NoError(t, err)
}

func triggerNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='trigger' AND name IN ('video_final_outcome_immutable','run_video_outcome_final_immutable') ORDER BY name`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	require.NoError(t, rows.Err())
	return names
}

// F2 (ARS-006 §10.1 test 2): migration 028 refuses any change to a final
// outcome on video and run_video_outcome, and allows every other transition.
func TestMigration028FinalOutcomeTriggers(t *testing.T) {
	db := openTestDB(t)
	require.Equal(t, []string{"run_video_outcome_final_immutable", "video_final_outcome_immutable"}, triggerNames(t, db))

	for i, outcome := range finalOutcomes {
		id := i + 1
		seedTriggerVideo(t, db, id, outcome)

		for _, to := range []string{"not_yet_evaluated", "deferred", "failed", "accepted", "rejected", "excluded", "unavailable_on_youtube"} {
			if to == outcome {
				continue
			}
			_, err := db.Exec(`UPDATE video SET final_outcome=? WHERE id=?`, to, id)
			require.ErrorContains(t, err, "final outcome is immutable", "video %s -> %s", outcome, to)
		}
		_, err := db.Exec(`UPDATE video SET final_outcome=NULL WHERE id=?`, id)
		require.ErrorContains(t, err, "final outcome is immutable", "video %s -> NULL", outcome)

		_, err = db.Exec(`UPDATE run_video_outcome SET final_outcome='not_yet_evaluated' WHERE video_id=?`, id)
		require.ErrorContains(t, err, "final outcome is immutable", "run_video_outcome %s", outcome)

		// A write of the same value, and a change to another column, pass.
		_, err = db.Exec(`UPDATE video SET final_outcome=? WHERE id=?`, outcome, id)
		require.NoError(t, err, "video %s -> same value", outcome)
		_, err = db.Exec(`UPDATE video SET processing_state='completed' WHERE id=?`, id)
		require.NoError(t, err, "processing_state alone must not abort for %s", outcome)
		_, err = db.Exec(`UPDATE run_video_outcome SET final_outcome=? WHERE video_id=?`, outcome, id)
		require.NoError(t, err, "run_video_outcome %s -> same value", outcome)

		var got string
		require.NoError(t, db.QueryRow(`SELECT final_outcome FROM video WHERE id=?`, id).Scan(&got))
		require.Equal(t, outcome, got, "an aborted update leaves the outcome unchanged")
		require.NoError(t, db.QueryRow(`SELECT final_outcome FROM run_video_outcome WHERE video_id=?`, id).Scan(&got))
		require.Equal(t, outcome, got)
	}

	// An upsert that would rewrite a final run outcome aborts too: the claim and
	// outcome writers use ON CONFLICT DO UPDATE.
	_, err := db.Exec(`INSERT INTO run_video_outcome (run_id,video_id,final_outcome,updated_at) VALUES (1,1,'not_yet_evaluated','2026-01-02T00:00:00Z')
		ON CONFLICT(run_id,video_id) DO UPDATE SET final_outcome=excluded.final_outcome`)
	require.ErrorContains(t, err, "final outcome is immutable")

	// Non-final starting points move freely, including into a final outcome.
	seedTriggerVideo(t, db, 10, "deferred")
	seedTriggerVideo(t, db, 11, nil)
	seedTriggerVideo(t, db, 12, "not_yet_evaluated")
	seedTriggerVideo(t, db, 13, "failed")
	for id, to := range map[int]string{10: "accepted", 11: "rejected", 12: "excluded", 13: "unavailable_on_youtube"} {
		_, err := db.Exec(`UPDATE video SET final_outcome=? WHERE id=?`, to, id)
		require.NoError(t, err, "video %d -> %s", id, to)
	}
	// run_video_outcome: NULL -> accepted and deferred -> accepted succeed.
	_, err = db.Exec(`UPDATE run_video_outcome SET final_outcome='accepted' WHERE video_id=11`)
	require.NoError(t, err, "run_video_outcome NULL -> accepted")
	_, err = db.Exec(`UPDATE run_video_outcome SET final_outcome='accepted', deferred_reason=NULL WHERE video_id=10`)
	require.NoError(t, err, "run_video_outcome deferred -> accepted")
	// accepted -> deferred aborts.
	_, err = db.Exec(`UPDATE run_video_outcome SET final_outcome='deferred', deferred_reason='quota_exhausted' WHERE video_id=10`)
	require.ErrorContains(t, err, "final outcome is immutable", "run_video_outcome accepted -> deferred")

	requireHealthy(t, db)
}

// F2 (down file) and K22 (ARS-006 §10.4 test 27): the down file removes both
// triggers, and the pre-BR-039 unconditional claim reset then succeeds on a
// final video. With 028 applied, that reset aborts.
func TestMigration028DownFileRestoresOldClaimReset(t *testing.T) {
	const oldReset = `UPDATE video SET processing_state = 'not_yet_evaluated', final_outcome = 'not_yet_evaluated', deferred_reason = NULL, error_message = NULL, completed_at = NULL WHERE id = ?`

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k22.db")
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	seedTriggerVideo(t, db, 1, "accepted")

	_, err = db.Exec(oldReset, 1)
	require.ErrorContains(t, err, "final outcome is immutable")

	down, err := os.ReadFile(filepath.Join("migrations", "028_final_outcome_triggers.down.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	require.Empty(t, triggerNames(t, db))

	_, err = db.Exec(oldReset, 1)
	require.NoError(t, err, "the old reset must succeed once the triggers are dropped")
	var got string
	require.NoError(t, db.QueryRow(`SELECT final_outcome FROM video WHERE id=1`).Scan(&got))
	require.Equal(t, "not_yet_evaluated", got)
	requireHealthy(t, db)
}

// The embedded down file is picked up by golang-migrate: stepping down from 28
// removes the triggers and stepping up restores them.
func TestMigration028DownThenUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "down-up.db")
	db := migrateTo(t, path, 28)
	require.Len(t, triggerNames(t, db), 2)

	src, err := iofs.New(os.DirFS("migrations"), ".")
	require.NoError(t, err)
	driver, err := migrationsqlite.WithInstance(db, &migrationsqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	require.NoError(t, err)

	require.NoError(t, m.Steps(-1))
	require.Empty(t, triggerNames(t, db))
	v, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(27), v)

	require.NoError(t, m.Steps(1))
	require.Len(t, triggerNames(t, db), 2)
}

// Upgrading an existing database with final videos must not abort: 028 adds
// triggers only, and the Go schema steps that follow edit table definitions
// without writing rows.
func TestMigration028UpgradeKeepsFinalRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	pre := migrateTo(t, path, 27)
	_, err := pre.Exec(`INSERT INTO run (id,run_type,status) VALUES (1,'scheduled','completed')`)
	require.NoError(t, err)
	_, err = pre.Exec(`INSERT INTO video (id,video_id,channel_id,processing_state,final_outcome) VALUES (1,'a','c','completed','accepted')`)
	require.NoError(t, err)
	require.NoError(t, pre.Close())

	db, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var got string
	require.NoError(t, db.QueryRow(`SELECT final_outcome FROM video WHERE id=1`).Scan(&got))
	require.Equal(t, "accepted", got)
	require.Len(t, triggerNames(t, db), 2)
	_, err = db.Exec(`UPDATE video SET final_outcome='rejected' WHERE id=1`)
	require.ErrorContains(t, err, "final outcome is immutable")
	requireHealthy(t, db)
}
