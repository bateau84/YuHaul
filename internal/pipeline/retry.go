package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

var errRetryClaimLost = errors.New("retry already owned or disposed")

// RetryQueueEntry owns the full manual retry lifecycle. The queue service must
// not acknowledge the row itself: nil also covers a durable quota requeue.
func (p *Pipeline) RetryQueueEntry(ctx context.Context, entryID int64, errorQueue bool) error {
	if p.queueStore == nil || p.versioner == nil || p.configStore == nil {
		return fmt.Errorf("pipeline: retry requires queue and configuration stores")
	}
	run, err := p.prepareProductionRun(ctx, state.RunTypeManual)
	if err != nil {
		return err
	}
	p.productionMu.Lock()
	defer p.productionMu.Unlock()
	err = p.queueStore.WithRetryTx(ctx, func(tx *sql.Tx) error {
		id, err := p.stateStore.CreateRunTx(ctx, tx, run)
		if err != nil {
			return err
		}
		run.ID = id
		var claimed bool
		if errorQueue {
			_, claimed, err = p.queueStore.ClaimErrorGroupTx(ctx, tx, entryID, id, time.Now())
		} else {
			_, claimed, err = p.queueStore.ClaimDeferredGroupTx(ctx, tx, entryID, id, time.Now())
		}
		if err != nil {
			return err
		}
		if !claimed {
			return errRetryClaimLost
		}
		// Manual queue retry has no discovery workload. A restart dispatches
		// owned retry work first and then completes this empty actions phase.
		_, err = tx.ExecContext(ctx, `UPDATE run SET current_phase = ? WHERE id = ?`, string(PhaseActionExecution), id)
		return err
	})
	if err != nil {
		if errors.Is(err, errRetryClaimLost) {
			return nil // Return the existing queue result; no orphan run committed.
		}
		return err
	}
	p.broadcastEvent(run.ID, EventRunStarted, nil)
	if err := p.processDeferredItems(ctx, run.ID); err != nil {
		p.failRun(ctx, run.ID, "manual retry failed", err)
		return err
	}
	p.completeRun(ctx, run.ID)
	return nil
}

// A retry's global state was reset by the claim transaction, not inferred from
// old decisions. Resume a persisted prefix normally; terminal state means only
// acknowledgement remains. Never replay the originating run's matches.
func (p *Pipeline) processRetryVideo(ctx context.Context, runID, videoID int64) error {
	qs, err := queue.NewStore(p.db)
	if err != nil {
		return err
	}
	v, err := p.stateStore.GetVideo(ctx, videoID)
	if err != nil {
		return err
	}
	var workErr error
	if v.FinalOutcome == state.FinalOutcomeNotYetEvaluated {
		var survivors []*state.Video
		routes, err := p.productionRoutes(ctx, runID)
		if err != nil {
			return err
		}
		survivors, workErr = p.runExclusionGate(ctx, runID, []*state.Video{v}, routes, true)
		if workErr == nil && len(survivors) > 0 {
			workErr = p.detectVideos(ctx, runID, survivors)
			if workErr == nil {
				_, workErr = p.routeVideos(ctx, runID, survivors)
			}
		}
	}
	if workErr == nil {
		v, err = p.stateStore.GetVideo(ctx, videoID)
		if err != nil {
			return err
		}
		if v.FinalOutcome == state.FinalOutcomeNotYetEvaluated && (v.ProcessingState == state.VideoStateEvaluated || v.ProcessingState == state.VideoStateActed) {
			_, workErr = p.executeVideoActions(ctx, runID, v)
		}
	}
	var retainedFailure string
	err = qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var outcome state.VideoFinalOutcome
		if err := tx.QueryRowContext(ctx, `SELECT final_outcome FROM video WHERE id = ?`, videoID).Scan(&outcome); err != nil {
			return err
		}
		if workErr != nil || outcome == state.FinalOutcomeFailed {
			message := "retry failed"
			if workErr != nil {
				message = workErr.Error()
			}
			if _, err := qs.RetainRuntimeErrorTx(ctx, tx, videoID, runID, message, "receiving-run retry"); err != nil {
				return err
			}
			retainedFailure = message
			return qs.AcknowledgeRetryGroupTx(ctx, tx, videoID, runID)
		}
		if outcome == state.FinalOutcomeDeferred {
			// The executor has retained pending deferred work. Resolve the error
			// group only with that handoff, not merely because dispatch returned.
			var retained bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE video_id=? AND status IN ('pending','retrying'))`, videoID).Scan(&retained); err != nil {
				return err
			}
			if !retained {
				return fmt.Errorf("retry video %d deferred without retained work", videoID)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE error_entry SET status='resolved', resolved_at=? WHERE video_id=? AND retry_run_id=? AND status='retrying'`, time.Now().UTC().Format(time.RFC3339), videoID, runID); err != nil {
				return err
			}
			return qs.RequeueRetryGroupTx(ctx, tx, videoID, runID)
		}
		if outcome != state.FinalOutcomeAccepted && outcome != state.FinalOutcomeRejected && outcome != state.FinalOutcomeExcluded {
			return fmt.Errorf("retry video %d has no durable disposition: %s", videoID, outcome)
		}
		return qs.AcknowledgeRetryGroupTx(ctx, tx, videoID, runID)
	})
	if err == nil && retainedFailure != "" {
		p.logger.WarnContext(ctx, "retry handed to retained runtime error", "run_id", runID, "video_id", videoID, "error", retainedFailure)
	}
	return err
}

// Includes requeued and acknowledged rows: a quota requeue must not become a
// second ordinary phase attempt in the same receiving run.
func (p *Pipeline) withoutRetryVideos(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	out := make([]*state.Video, 0, len(videos))
	for _, v := range videos {
		var retry bool
		if err := p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deferred_item WHERE video_id=? AND retry_run_id=? UNION ALL SELECT 1 FROM error_entry WHERE video_id=? AND retry_run_id=?)`, v.ID, runID, v.ID, runID).Scan(&retry); err != nil {
			return nil, err
		}
		if !retry {
			out = append(out, v)
		}
	}
	return out, nil
}
