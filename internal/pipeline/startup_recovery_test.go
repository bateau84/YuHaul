package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartupClassification(t *testing.T) {
	ctx := context.Background()
	db, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()
	_, err := db.Exec(`INSERT INTO config_version (id,snapshot) VALUES (1,'{"routes":[],"duplicate_threshold":0,"duplicate_behavior":"continue"}')`)
	require.NoError(t, err)
	insert := func(kind, status string, safety, authority int, phase any) int64 {
		res, err := db.Exec(`INSERT INTO run (started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version,current_phase) VALUES ('2026-01-01T00:00:00Z',?,?,?,?,?,?)`, kind, status, 1, safety, authority, phase)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return id
	}
	first := insert("manual", "running", 1, 1, "routing")
	second := insert("scheduled", "interrupted", 1, 1, nil)
	legacy := insert("manual", "interrupted", 0, 1, nil)
	simulation := insert("dry_run", "running", 0, 0, nil)
	p := &Pipeline{db: db}
	ids, err := p.classifyStartupRuns(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{first, second}, ids)
	var status, reason, phase string
	require.NoError(t, db.QueryRow(`SELECT status,interrupt_reason,current_phase FROM run WHERE id=?`, first).Scan(&status, &reason, &phase))
	require.Equal(t, "interrupted", status)
	require.Equal(t, "routing", phase)
	require.Equal(t, "diag:v1 phase=routing operation=load_run code=unknown_error", reason)
	for _, id := range []int64{legacy, simulation} {
		var got string
		require.NoError(t, db.QueryRow(`SELECT status FROM run WHERE id=?`, id).Scan(&got))
		if id == legacy {
			require.Equal(t, "interrupted", got)
		} else {
			require.Equal(t, "running", got)
		}
	}
	ids, err = p.classifyStartupRuns(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{first, second}, ids)
}

func TestStartupClassificationRollsBackInvalidRun(t *testing.T) {
	ctx := context.Background()
	db, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()
	_, err := db.Exec(`INSERT INTO config_version (id,snapshot) VALUES (1,'{"routes":[]}'); INSERT INTO run (started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version) VALUES ('2026-01-01','manual','running',1,1,1); INSERT INTO run (started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version,current_phase) VALUES ('2026-01-02','manual','running',1,1,1,'invalid')`)
	require.NoError(t, err)
	_, err = (&Pipeline{db: db}).classifyStartupRuns(ctx)
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM run WHERE status='running'`).Scan(&count))
	require.Equal(t, 2, count)
}

func TestStartupClassificationWriteFailureRollsBack(t *testing.T) {
	db, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()
	_, err := db.Exec(`INSERT INTO config_version (id,snapshot) VALUES (1,'{"routes":[]}'); INSERT INTO run (started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version) VALUES ('2026-01-01','manual','running',1,1,1); CREATE TRIGGER reject_startup BEFORE UPDATE ON run BEGIN SELECT RAISE(ABORT, 'failed'); END`)
	require.NoError(t, err)
	_, err = (&Pipeline{db: db}).classifyStartupRuns(context.Background())
	require.Error(t, err)
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM run`).Scan(&status))
	require.Equal(t, "running", status)
}

func TestStartupClassificationBlocksCompletedWithoutTimestamp(t *testing.T) {
	db, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()
	_, err := db.Exec(`INSERT INTO config_version (id,snapshot) VALUES (1,'{"routes":[]}'); INSERT INTO run (started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version) VALUES ('2026-01-01','manual','completed',1,1,1)`)
	require.NoError(t, err)
	_, err = (&Pipeline{db: db}).classifyStartupRuns(context.Background())
	var ce *startupCheckError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, "terminal_timestamp_contradiction", ce.Check)
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM run`).Scan(&status))
	require.Equal(t, "completed", status, "row must not be rewritten")
	// A well-formed completed row is terminal and untouched, not a block.
	_, err = db.Exec(`UPDATE run SET completed_at='2026-01-01T00:01:00Z'`)
	require.NoError(t, err)
	ids, err := (&Pipeline{db: db}).classifyStartupRuns(context.Background())
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestRecoverStartupLogsRunAndCheckOnly(t *testing.T) {
	db, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()
	_, err := db.Exec(`INSERT INTO config_version (id,snapshot) VALUES (1,'{"routes":[]}'); INSERT INTO run (id,started_at,run_type,status,config_version_id,mutation_safety_version,action_authority_version,current_phase) VALUES (7,'2026-01-01','manual','running',1,1,1,'SECRET_SENTINEL')`)
	require.NoError(t, err)
	var logs bytes.Buffer
	p := &Pipeline{db: db, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	require.Error(t, p.RecoverStartup(context.Background()))
	out := logs.String()
	require.Contains(t, out, "startup recovery blocked")
	require.Contains(t, out, "run_id=7")
	require.Contains(t, out, "check=checkpoint")
	require.NotContains(t, out, "SECRET_SENTINEL")
}
