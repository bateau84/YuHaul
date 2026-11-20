package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
)

// F1 (ARS-006 §10.1 test 1): migration 027 adds closed_reason to deferred_item
// and error_entry, NULL on every existing row; an unknown value aborts.
func TestMigration027ClosedReasonColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f1.db")
	pre := migrateTo(t, path, 26)
	_, err := pre.Exec(`INSERT INTO run (id,run_type,status) VALUES (1,'scheduled','completed')`)
	require.NoError(t, err)
	_, err = pre.Exec(`INSERT INTO video (id,video_id,channel_id,processing_state) VALUES (1,'a','c','failed')`)
	require.NoError(t, err)
	for _, st := range []string{"pending", "retrying", "completed", "discarded"} {
		_, err = pre.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status,retry_run_id) VALUES (1,1,'quota_exhausted',?,CASE WHEN ?='retrying' THEN 1 END)`, st, st)
		require.NoError(t, err)
	}
	for _, st := range []string{"active", "retrying", "dismissed", "resolved"} {
		_, err = pre.Exec(`INSERT INTO error_entry (video_id,run_id,error_category,error_message,status) VALUES (1,1,'runtime','m',?)`, st)
		require.NoError(t, err)
	}
	require.NoError(t, pre.Close())

	db, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, table := range []string{"deferred_item", "error_entry"} {
		var rows, nulls int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*), COUNT(*) - COUNT(closed_reason) FROM `+table).Scan(&rows, &nulls))
		require.Equal(t, 4, rows, table)
		require.Equal(t, rows, nulls, "%s: closed_reason must be NULL on every existing row", table)

		_, err = db.Exec(`UPDATE ` + table + ` SET closed_reason = 'bogus' WHERE id = 1`)
		require.Error(t, err, "%s: an unknown closed_reason must abort", table)
		_, err = db.Exec(`UPDATE ` + table + ` SET closed_reason = 'final_outcome' WHERE id = 1`)
		require.NoError(t, err, "%s: final_outcome is the one accepted value", table)
		_, err = db.Exec(`UPDATE ` + table + ` SET closed_reason = NULL WHERE id = 1`)
		require.NoError(t, err)
	}
	requireHealthy(t, db)
}
