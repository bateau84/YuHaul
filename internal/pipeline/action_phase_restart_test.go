//go:build integration

package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// snapshotDB captures the committed database exactly as a SIGKILL at this
// instant would leave it. VACUUM INTO reads a consistent committed view.
func snapshotDB(t *testing.T, db *sql.DB, dst string) {
	t.Helper()
	_, err := db.Exec(`VACUUM INTO ?`, dst)
	require.NoError(t, err)
}

// TestActionPhaseKillRestart kills a real production run at the two action
// phase points the reviewer reproduced (send guard committed before the
// provider call; provider applied before the response is processed), restarts
// over the surviving database and requires bounded, safe recovery.
func TestActionPhaseKillRestart(t *testing.T) {
	for _, point := range []string{"guard", "post"} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
			snap := filepath.Join(t.TempDir(), "killed.db")
			taken := 0
			take := func() { taken++; snapshotDB(t, env.pipeline.db, snap) }
			if point == "guard" {
				env.server.beforeInsert = take
			} else {
				env.server.afterInsert = take
			}
			run, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			env.pipeline.Execute(ctx, run.ID)
			require.Equal(t, 1, taken, "kill point must be reached exactly once")
			require.Len(t, env.server.Insertions(), 1)
			env.server.ResetInsertions()
			env.server.beforeInsert, env.server.afterInsert = nil, nil

			// Restart: fresh objects over the killed database; only the external
			// provider survives.
			var logs bytes.Buffer
			p := openRetryPipeline(t, snap, env.server.URL(), slog.New(slog.NewTextHandler(&logs, nil)))
			p.discovery = discovery.NewEngine(p.auth, p.machine, p.ledger, p.db, discovery.WithBaseURL(env.server.URL()))

			// Preconditions describe a crash in the action phase.
			var runStatus, videoState string
			var guards, actions int
			require.NoError(t, p.db.QueryRow(`SELECT status FROM run WHERE id=?`, run.ID).Scan(&runStatus))
			require.NoError(t, p.db.QueryRow(`SELECT processing_state FROM video WHERE video_id='V1'`).Scan(&videoState))
			require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, run.ID).Scan(&guards))
			require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM executed_action WHERE run_id=?`, run.ID).Scan(&actions))
			require.Equal(t, "running", runStatus)
			require.Equal(t, "acted", videoState)
			require.Equal(t, 1, guards)
			require.Equal(t, 0, actions)

			require.NoError(t, p.RecoverStartup(ctx))

			require.Empty(t, env.server.Insertions(), "replay protection: the guarded send must not be repeated")
			got, err := p.stateStore.GetRun(ctx, run.ID)
			require.NoError(t, err)
			reason := ""
			if got.InterruptReason != nil {
				reason = *got.InterruptReason
			}
			require.NotContains(t, reason, "invalid processing-state transition")
			require.NotContains(t, reason, "transition video")
			require.Contains(t, logs.String(), "recovered runs")
			require.Contains(t, logs.String(), "count=1")
			t.Logf("run status=%s reason=%q", got.Status, reason)

			// The guard still vetoes: it is intact and no action row was invented.
			require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, run.ID).Scan(&guards))
			require.Equal(t, 1, guards)
			require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM executed_action WHERE run_id=? AND outcome='succeeded'`, run.ID).Scan(&actions))
			require.Equal(t, 0, actions)

			// The guard veto was reached and recorded; it is what stopped the
			// recovered action, not a state-machine error.
			var vetoes int
			require.NoError(t, p.db.QueryRow(`SELECT count(*) FROM executed_action WHERE run_id=? AND outcome_reason='mutation_application_uncertain'`, run.ID).Scan(&vetoes))
			require.Equal(t, 1, vetoes)
			require.NoError(t, p.db.QueryRow(`SELECT processing_state FROM video WHERE video_id='V1'`).Scan(&videoState))
			require.NotEqual(t, "completed", videoState)
			require.NotContains(t, logs.String(), "invalid processing-state transition")
		})
	}
}
