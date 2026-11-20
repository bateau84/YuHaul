//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// logicalRows compares all columns, including NULLs, without depending on SQLite
// file layout. Queries are fixed test-owned SQL, never user input.
func logicalRows(t *testing.T, db *sql.DB, query string, args ...any) [][]any {
	t.Helper()
	rows, err := db.Query(query, args...)
	require.NoError(t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	require.NoError(t, err)
	var result [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			dest[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(dest...))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		result = append(result, vals)
	}
	require.NoError(t, rows.Err())
	return result
}

func TestStartupRecoveryLegacyCohortNoninterferenceAfterReopen(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, nil)
	p := env.pipeline
	// This is a real new production run, committed before its worker starts.
	guarded, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	// All following rows are synthetic historical prerequisites, not live data.
	// Queue rows are inserted after StartRun so this new run owns none of them.
	tx, err := p.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO config_version (id,snapshot,change_description) VALUES (999,'{"routes":[],"duplicate_threshold":0.85,"duplicate_behavior":"continue"}','original legacy snapshot');
 INSERT INTO run(id,started_at,run_type,status,config_version_id,current_phase,mutation_safety_version,action_authority_version,videos_discovered) VALUES
 (140,'2025-01-01T00:00:00Z','manual','running',999,'discovery',0,0,19615),
 (141,'2025-01-02T00:00:00Z','scheduled','interrupted',999,'routing',0,1,0),
 (142,'2025-01-03T00:00:00Z','manual','failed',999,'routing',0,0,0),
 (143,'2025-01-04T00:00:00Z','scheduled','completed',999,'action_execution',0,0,0),
 (144,'2025-01-05T00:00:00Z','dry_run','interrupted',999,'routing',0,0,0);
 WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<19615)
 INSERT INTO video(video_id,channel_id,discovered_in_run_id,processing_state,final_outcome) SELECT printf('legacy-%05d',n),'legacy-channel',140,'not_yet_evaluated','not_yet_evaluated' FROM seq;
 INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at) SELECT 140,id,'not_yet_evaluated','2025-01-01T00:00:00Z' FROM video WHERE discovered_in_run_id=140;
 INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) SELECT id,143,?,1,'matched' FROM video WHERE video_id='legacy-00001';
 INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome,executed_at) SELECT d.id,143,d.video_id,'playlist_insert','legacy-target','succeeded','2025-01-04T00:00:00Z' FROM route_decision d WHERE d.run_id=143;
 INSERT INTO deferred_item(video_id,run_id,deferral_reason,status,retry_after) SELECT id,140,'quota_exhausted','pending','2099-01-01T00:00:00Z' FROM video WHERE video_id='legacy-00002';
 INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) SELECT id,140,'runtime','legacy failure','active' FROM video WHERE video_id='legacy-00003'`, env.routeID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	var pending int
	require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM video WHERE discovered_in_run_id=140 AND processing_state='not_yet_evaluated'`).Scan(&pending))
	require.Equal(t, 19615, pending)
	queries := map[string]string{
		"runs":      `SELECT * FROM run WHERE id<>? ORDER BY id`,
		"snapshots": `SELECT * FROM config_version ORDER BY id`,
		"videos":    `SELECT * FROM video ORDER BY id`,
		"outcomes":  `SELECT * FROM run_video_outcome ORDER BY run_id,video_id`,
		"decisions": `SELECT * FROM route_decision ORDER BY id`,
		"audits":    `SELECT * FROM condition_audit ORDER BY id`,
		"actions":   `SELECT * FROM executed_action ORDER BY id`,
		"deferred":  `SELECT * FROM deferred_item ORDER BY id`,
		"errors":    `SELECT * FROM error_entry ORDER BY id`,
		"guards":    `SELECT * FROM playlist_send_guard ORDER BY video_id,target_playlist_id,action_type`,
	}
	snapshot := func(db *sql.DB) map[string][][]any {
		out := make(map[string][][]any, len(queries))
		for name, q := range queries {
			if name == "runs" {
				out[name] = logicalRows(t, db, q, guarded.ID)
			} else {
				out[name] = logicalRows(t, db, q)
			}
		}
		return out
	}
	before := snapshot(p.db)
	ids, err := p.classifyStartupRuns(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{guarded.ID}, ids)
	require.Equal(t, before, snapshot(p.db), "classification changed protected logical rows")
	// Close and rebuild the actual persistence/auth/pipeline objects; only the
	// external YouTube service is local. The input set for that service is empty.
	p = reopenRetryPipeline(t, p)
	p.discovery = discovery.NewEngine(p.auth, p.machine, p.ledger, p.db, discovery.WithBaseURL(env.server.URL()))
	require.NoError(t, p.RecoverStartup(ctx))
	got, err := p.stateStore.GetRun(ctx, guarded.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, got.Status, fmt.Sprint(got.InterruptReason))
	require.Equal(t, guarded.ConfigVersionID, got.ConfigVersionID)
	require.Equal(t, before, snapshot(p.db), "startup execution changed protected logical rows")
	require.Empty(t, env.server.Insertions())
	var foreignCalls, guardedCalls int
	require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id<>? OR run_id IS NULL`, guarded.ID).Scan(&foreignCalls))
	require.Zero(t, foreignCalls)
	require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id=?`, guarded.ID).Scan(&guardedCalls))
	require.Positive(t, guardedCalls, "real discovery must run for the guarded run")
}

func TestStartupRecoveryReopenRunningGuardedRun(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, nil)
	original, err := env.pipeline.StartRun(ctx, state.RunTypeScheduled)
	require.NoError(t, err)
	p := reopenRetryPipeline(t, env.pipeline)
	p.discovery = discovery.NewEngine(p.auth, p.machine, p.ledger, p.db, discovery.WithBaseURL(env.server.URL()))
	before, err := p.stateStore.GetRun(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusRunning, before.Status)
	require.NoError(t, p.RecoverStartup(ctx))
	after, err := p.stateStore.GetRun(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, after.Status)
	require.Equal(t, original.ConfigVersionID, after.ConfigVersionID)
	var runs, calls int
	require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM run`).Scan(&runs))
	require.Equal(t, 1, runs)
	require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id=?`, original.ID).Scan(&calls))
	require.Positive(t, calls)
	require.Empty(t, env.server.Insertions())
}
