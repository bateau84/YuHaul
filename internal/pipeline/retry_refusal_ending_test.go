package pipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestRetryRefusalEndingDoesNotSweep(t *testing.T) {
	for _, markedStop := range []bool{false, true} {
		t.Run(fmt.Sprintf("markedStop=%t", markedStop), func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			qs, err := queue.NewStore(f.db)
			require.NoError(t, err)
			id, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime, Message: "original", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			receipt := queue.RetryGroupReceipt{ReceivingRunID: f.runID, VideoID: f.videoID, Errors: []queue.RetryRowRef{{ID: id, OriginRunID: f.runID}}}
			cause := error(queue.ErrWorkNotOwned)
			if markedStop {
				_, err = f.db.Exec(`UPDATE run SET stop_requested_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), f.runID)
				require.NoError(t, err)
				cause = state.ErrPlaylistSendStopped
			}
			p := &Pipeline{db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			ctl := p.registerRun(f.runID)
			refusal := &retryAdmissionRefusal{receipt: receipt, cause: cause}
			p.failRun(ctx, f.runID, "retry failed", fmt.Errorf("wrapped: %w", refusal))
			require.Same(t, refusal, ctl.refusedRetry)
			require.Equal(t, receipt, ctl.refusedRetry.receipt)
			p.finishRun(ctx, f.runID)
			run, err := f.state.GetRun(ctx, f.runID)
			require.NoError(t, err)
			if markedStop {
				require.Equal(t, state.RunStatusRunning, run.Status)
			} else {
				require.Equal(t, state.RunStatusInterrupted, run.Status)
			}
			entry, err := qs.GetErrorEntry(ctx, id)
			require.NoError(t, err)
			require.Equal(t, queue.ErrorStatusActive, entry.Status)
			var count int
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM error_entry WHERE run_id=?`, f.runID).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}
