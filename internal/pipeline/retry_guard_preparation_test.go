package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestRetryGuardPreparationRefusesLostReceipt(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "lost"
		if stop {
			name = "marked-stop-precedes-loss"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixtureWithTargets(t, []string{"first", "second"})
			defer f.db.Close()
			qs, err := queue.NewStore(f.db)
			require.NoError(t, err)
			source, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime, Message: "existing", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			rs, cs := router.NewStore(f.db), config.NewStore(f.db)
			versioner := config.NewService(rs, cs)
			run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
			run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
			require.NoError(t, err)
			var receipt queue.RetryGroupReceipt
			require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
				var e error
				run.ID, e = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
				if e != nil {
					return e
				}
				var claimed bool
				receipt, _, claimed, e = qs.ClaimErrorGroupReceiptTx(ctx, tx, source, run.ID, time.Now())
				if e == nil && !claimed {
					return errors.New("not claimed")
				}
				return e
			}))
			_, err = f.db.Exec(`UPDATE error_entry SET status='active',retry_run_id=NULL WHERE id=?`, source)
			require.NoError(t, err)
			if stop {
				_, err = f.db.Exec(`UPDATE run SET stop_requested_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), run.ID)
				require.NoError(t, err)
			}
			var posts int
			provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posts++ }))
			defer provider.Close()
			p := &Pipeline{db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, auth: f.auth, versioner: versioner, configStore: cs, router: router.NewEngine(rs), detector: duplicate.NewDetector(f.db), meta: metadata.NewManager(metadata.NewStore(f.db), nil, nil), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), executor: NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL)), executionBinding: &f.binding}
			p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
			err = p.processRetryVideo(context.WithValue(ctx, guardedRetryGroupKey{}, receipt), run.ID, f.videoID)
			if stop {
				require.ErrorIs(t, err, state.ErrPlaylistSendStopped)
			} else {
				require.ErrorIs(t, err, queue.ErrWorkNotOwned)
			}
			var refusal *retryAdmissionRefusal
			require.ErrorAs(t, err, &refusal)
			require.Equal(t, receipt, refusal.receipt)
			require.Zero(t, posts)
			for _, table := range []string{"playlist_send_guard", "quota_ledger", "executed_action"} {
				var count int
				require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE run_id=?`, run.ID).Scan(&count))
				require.Zero(t, count, table)
			}
			var count int
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM error_entry WHERE run_id=?`, run.ID).Scan(&count))
			require.Zero(t, count)
		})
	}
}
