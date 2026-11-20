package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// Receipt-aware run ending (authorization ARS-001 "authority at guard
// preparation and POST admission", ARS-005 §5.1.1): a refused retry claim is
// settled from the durable claim provenance. Removed or lost work is never
// recreated, an unexpected sibling or a missing claim identity blocks
// settlement, and unrelated work keeps its ordinary retention or handoff.

type settlementScene struct {
	f       *guardedProductionFixture
	qs      *queue.Store
	p       *Pipeline
	recv    int64 // the receiving run
	source  int64 // the claimed Errors entry of the originating run
	native  int64 // a native video of the receiving run, unfinished
	receipt queue.RetryGroupReceipt
	logs    *bytes.Buffer
}

func newSettlementScene(t *testing.T) *settlementScene {
	t.Helper()
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	t.Cleanup(func() { _ = f.db.Close() })
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	source, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime, Message: "original", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
	cfg := configVersionID(t, f)
	run.ConfigVersionID = &cfg
	sc := &settlementScene{f: f, qs: qs, source: source}
	require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		if run.ID, err = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true); err != nil {
			return err
		}
		var claimed bool
		sc.receipt, _, claimed, err = qs.ClaimErrorGroupReceiptTx(ctx, tx, source, run.ID, time.Now())
		if err == nil && !claimed {
			err = errRetryClaimLost
		}
		return err
	}))
	sc.recv = run.ID
	native, err := state.NewMachine(f.state).DiscoverVideo(ctx, sc.recv, "native-two", "fixture-channel")
	require.NoError(t, err)
	sc.native = native.ID
	sc.logs = &bytes.Buffer{}
	sc.p = &Pipeline{db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, logger: slog.New(slog.NewTextHandler(sc.logs, nil))}
	return sc
}

func configVersionID(t *testing.T, f *guardedProductionFixture) int64 {
	t.Helper()
	var id int64
	require.NoError(t, f.db.QueryRow(`SELECT config_version_id FROM run WHERE id=?`, f.runID).Scan(&id))
	return id
}

// end drives the real failRun → finishRun path with a refusal of the claim.
// markedStop adds a Stop request: its marker and the control flag.
func (sc *settlementScene) end(t *testing.T, cause error, markedStop bool) {
	t.Helper()
	ctx := context.Background()
	ctl := sc.p.registerRun(sc.recv)
	if markedStop {
		_, err := sc.f.db.Exec(`UPDATE run SET stop_requested_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), sc.recv)
		require.NoError(t, err)
		ctl.mu.Lock()
		ctl.stopRequested = true
		ctl.mu.Unlock()
	}
	refusal := &retryAdmissionRefusal{receipt: sc.receipt, cause: cause}
	sc.p.failRun(ctx, sc.recv, "retry failed", fmt.Errorf("wrapped: %w", refusal))
	require.Same(t, refusal, ctl.refusedRetry)
	sc.p.finishRun(ctx, sc.recv)
}

// endGeneric ends the run with an ordinary failure that is not a refusal, so
// the generic failure sweep decides.
func (sc *settlementScene) endGeneric(t *testing.T) {
	t.Helper()
	sc.p.registerRun(sc.recv)
	sc.p.failRun(context.Background(), sc.recv, "retry failed", errors.New("processing failed"))
	sc.p.finishRun(context.Background(), sc.recv)
}

func (sc *settlementScene) status(t *testing.T) state.RunStatus {
	t.Helper()
	run, err := sc.f.state.GetRun(context.Background(), sc.recv)
	require.NoError(t, err)
	return run.Status
}

func (sc *settlementScene) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, sc.f.db.QueryRow(query, args...).Scan(&n))
	return n
}

func (sc *settlementScene) sourceStatus(t *testing.T) queue.ErrorStatus {
	t.Helper()
	e, err := sc.qs.GetErrorEntry(context.Background(), sc.source)
	require.NoError(t, err)
	return e.Status
}

func TestRefusedRetryRemovedWorkIsNotRecreatedByFailure(t *testing.T) {
	sc := newSettlementScene(t)
	require.NoError(t, sc.qs.DismissErrorEntry(context.Background(), sc.source))
	sc.end(t, queue.ErrWorkRemoved, false)

	require.Equal(t, state.RunStatusFailed, sc.status(t))
	// The run that ended because the person removed its work saves a closed
	// code, not unknown_error.
	var reason sql.NullString
	require.NoError(t, sc.f.db.QueryRow(`SELECT interrupt_reason FROM run WHERE id=?`, sc.recv).Scan(&reason))
	require.Equal(t, "diag:v1 phase=unknown operation=retry code=retry_work_removed", reason.String)
	require.NotContains(t, sc.logs.String(), "code=unknown_error")
	require.Equal(t, queue.ErrorStatusDismissed, sc.sourceStatus(t), "removed source stays removed")
	require.Zero(t, sc.count(t, `SELECT count(*) FROM error_entry WHERE video_id=? AND id<>?`, sc.f.videoID, sc.source), "no sink for the refused video")
	require.Zero(t, sc.count(t, `SELECT count(*) FROM deferred_item WHERE video_id=?`, sc.f.videoID))
	require.Equal(t, 1, sc.count(t, `SELECT count(*) FROM error_entry WHERE video_id=? AND run_id=? AND status='active'`, sc.native, sc.recv),
		"an unrelated unfinished native video keeps its ordinary retention")
}

func TestRefusedRetryLostOwnershipLeavesTheOtherOwnersRows(t *testing.T) {
	ctx := context.Background()
	sc := newSettlementScene(t)
	other := sc.f.runID // another running run takes the source row over
	_, err := sc.f.db.ExecContext(ctx, `UPDATE error_entry SET retry_run_id=? WHERE id=?`, other, sc.source)
	require.NoError(t, err)
	sc.end(t, queue.ErrWorkNotOwned, false)

	require.Equal(t, state.RunStatusFailed, sc.status(t))
	require.Equal(t, queue.ErrorStatusRetrying, sc.sourceStatus(t))
	require.Equal(t, 1, sc.count(t, `SELECT count(*) FROM error_entry WHERE id=? AND retry_run_id=?`, sc.source, other))
	require.Zero(t, sc.count(t, `SELECT count(*) FROM error_entry WHERE video_id=? AND id<>?`, sc.f.videoID, sc.source))
	require.Equal(t, 1, sc.count(t, `SELECT count(*) FROM error_entry WHERE video_id=? AND run_id=?`, sc.native, sc.recv))
}

func TestRefusedRetryMarkedStopSettlesFromProvenance(t *testing.T) {
	t.Run("removed work is not recreated and the native video is handed off", func(t *testing.T) {
		sc := newSettlementScene(t)
		require.NoError(t, sc.qs.DismissErrorEntry(context.Background(), sc.source))
		sc.end(t, queue.ErrWorkRemoved, true)

		require.Equal(t, state.RunStatusStopped, sc.status(t))
		require.Equal(t, queue.ErrorStatusDismissed, sc.sourceStatus(t))
		require.Zero(t, sc.count(t, `SELECT count(*) FROM deferred_item WHERE video_id=?`, sc.f.videoID), "removed work is not recreated")
		require.Equal(t, 1, sc.count(t, `SELECT count(*) FROM deferred_item WHERE video_id=? AND status='pending' AND deferral_reason='stopped_by_user'`, sc.native))
	})
	t.Run("a Stop seen at the guard hands the claimed video off like any Stop", func(t *testing.T) {
		sc := newSettlementScene(t)
		sc.end(t, state.ErrPlaylistSendStopped, true)

		require.Equal(t, state.RunStatusStopped, sc.status(t))
		require.Equal(t, queue.ErrorStatusResolved, sc.sourceStatus(t), "the exact claimed entry is released")
		require.Equal(t, 1, sc.count(t, `SELECT count(*) FROM deferred_item WHERE video_id=? AND status='pending' AND deferral_reason='stopped_by_user'`, sc.f.videoID))
		require.Equal(t, 1, sc.count(t, `SELECT count(*) FROM deferred_item WHERE video_id=? AND status='pending'`, sc.native))
	})
}

// c6fb0f87 at the end paths: a sibling that gained the receiving run as owner
// after the claim is not a member. It is never released or completed as one;
// the ending is blocked instead.
func TestUnexpectedSameOwnerSiblingBlocksEveryEnding(t *testing.T) {
	for _, tt := range []struct {
		name       string
		markedStop bool
		wantStatus state.RunStatus
	}{
		{"marked stop", true, state.RunStatusRunning},
		{"ordinary failure", false, state.RunStatusInterrupted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			sc := newSettlementScene(t)
			sibling, err := sc.qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: sc.f.videoID, RunID: sc.f.runID, Category: queue.ErrorCategoryRuntime, Message: "later sibling", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
			require.NoError(t, err)
			_, err = sc.f.db.ExecContext(ctx, `UPDATE error_entry SET status='retrying', retry_run_id=? WHERE id=?`, sc.recv, sibling)
			require.NoError(t, err)
			if tt.markedStop {
				sc.end(t, state.ErrPlaylistSendStopped, true)
			} else {
				sc.endGeneric(t)
			}

			require.Equal(t, tt.wantStatus, sc.status(t))
			require.Equal(t, queue.ErrorStatusRetrying, sc.sourceStatus(t), "the settlement rolled back as a whole")
			require.Equal(t, 1, sc.count(t, `SELECT count(*) FROM error_entry WHERE id=? AND status='retrying' AND retry_run_id=?`, sibling, sc.recv))
			require.Zero(t, sc.count(t, `SELECT count(*) FROM deferred_item`))
		})
	}
}

// Legacy unfinished work (migration 031 leaves its provenance NULL) fails
// closed: it is neither handed off nor released, and a Stop stays unsettled.
func TestLegacyNullProvenanceBlocksEveryEnding(t *testing.T) {
	for _, tt := range []struct {
		name       string
		markedStop bool
		cause      error // nil: an ordinary failure, not a refusal
		wantStatus state.RunStatus
	}{
		{"marked stop", true, state.ErrPlaylistSendStopped, state.RunStatusRunning},
		{"integrity refusal", false, queue.ErrRetryProvenance, state.RunStatusInterrupted},
		{"ordinary failure", false, nil, state.RunStatusInterrupted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sc := newSettlementScene(t)
			_, err := sc.f.db.Exec(`UPDATE run_video_outcome SET retry_claim_provenance=NULL WHERE run_id=? AND video_id=?`, sc.recv, sc.f.videoID)
			require.NoError(t, err)
			if tt.cause == nil {
				sc.endGeneric(t)
			} else {
				sc.end(t, tt.cause, tt.markedStop)
			}

			require.Equal(t, tt.wantStatus, sc.status(t))
			require.Equal(t, queue.ErrorStatusRetrying, sc.sourceStatus(t), "no release, no completion")
			require.Zero(t, sc.count(t, `SELECT count(*) FROM deferred_item`), "no synthetic handoff")
			switch {
			case tt.markedStop:
				require.Contains(t, sc.logs.String(), "code=stop_settlement_blocked")
				require.Contains(t, sc.logs.String(), "cause_diagnostic")
				require.Contains(t, sc.logs.String(), "code=retry_claim_unverifiable")
			case tt.cause != nil:
				// The stored interrupt reason and the log name the integrity code.
				var reason sql.NullString
				require.NoError(t, sc.f.db.QueryRow(`SELECT interrupt_reason FROM run WHERE id=?`, sc.recv).Scan(&reason))
				require.Equal(t, "diag:v1 phase=unknown operation=retry code=retry_claim_unverifiable", reason.String)
				require.Contains(t, sc.logs.String(), "code=retry_claim_unverifiable")
				require.NotContains(t, sc.logs.String(), "code=unknown_error", "an integrity refusal never reads unknown_error")
			default:
				// Retention stopped on the unusable claim: the stored reason says
				// so instead of repeating the unrelated failure.
				var reason sql.NullString
				require.NoError(t, sc.f.db.QueryRow(`SELECT interrupt_reason FROM run WHERE id=?`, sc.recv).Scan(&reason))
				require.Equal(t, "diag:v1 phase=unknown operation=retain_failure code=retry_claim_unverifiable", reason.String)
			}
			if tt.markedStop {
				require.NotContains(t, sc.logs.String(), "code=unknown_error")
			}
		})
	}
}

// A Stop the fence sees before the context is cancelled is the user's action:
// it is not logged as a failed phase.
func TestStopAtTheFenceIsNotLoggedAsAFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  func(sc *settlementScene) error
	}{
		{"retry refusal", func(sc *settlementScene) error {
			return &retryAdmissionRefusal{receipt: sc.receipt, cause: state.ErrPlaylistSendStopped}
		}},
		{"native", func(*settlementScene) error { return fmt.Errorf("execute: %w", state.ErrPlaylistSendStopped) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := newSettlementScene(t)
			_, err := sc.f.db.Exec(`UPDATE run SET stop_requested_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), sc.recv)
			require.NoError(t, err)
			sc.p.registerRun(sc.recv)
			sc.p.failRun(context.Background(), sc.recv, "action execution phase failed", tc.err(sc))
			sc.p.finishRun(context.Background(), sc.recv)
			require.NotContains(t, sc.logs.String(), "level=ERROR")
			require.NotContains(t, sc.logs.String(), "pipeline phase failed")
			require.Contains(t, sc.logs.String(), "code=stopped_by_user")
		})
	}
}

// The startup failure diagnostics use registered closed codes, not unknown_error.
func TestStartupFailureDiagnostics(t *testing.T) {
	block := &startupCheckError{RunID: 4, Check: "stop_settlement", Cause: fmt.Errorf("handoff: %w", queue.ErrRetryProvenance)}
	require.Equal(t, "diag:v1 phase=unknown operation=finalize_run code=stop_settlement_blocked", StartupFailureDiagnostic(fmt.Errorf("startup: %w", block)))
	attrs := fmt.Sprint(stopSettlementAttrs(block.Cause))
	require.Contains(t, attrs, "code=stop_settlement_blocked")
	require.Contains(t, attrs, "code=retry_claim_unverifiable")
	require.NotContains(t, attrs, "unknown_error")
	// Another check keeps its own classification, and an unusable claim names its code.
	require.Equal(t, "diag:v1 phase=unknown operation=load_run code=retry_claim_unverifiable", StartupFailureDiagnostic(fmt.Errorf("x: %w", queue.ErrRetryProvenance)))
	require.Equal(t, "diag:v1 phase=unknown operation=load_run code=retry_work_removed", StartupFailureDiagnostic(queue.ErrWorkRemoved))
}
