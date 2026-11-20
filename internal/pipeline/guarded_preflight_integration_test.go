//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Bounded action-pipeline proof: version1 fixture supplies captured matched
// authority; all action admission, state, queue, and ledger implementations real.
func TestReviewPreflightQuotaMustRetainFreshWork(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	require.NoError(t, f.ledger.SetDailyLimit(ctx, 49))
	m := state.NewMachine(f.state)
	_, err := m.TransitionVideoState(ctx, f.videoID, state.VideoStateNotYetEvaluated, state.VideoStateEvaluating)
	require.NoError(t, err)
	_, err = m.TransitionVideoState(ctx, f.videoID, state.VideoStateEvaluating, state.VideoStateEvaluated)
	require.NoError(t, err)
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL("http://127.0.0.1:1"))
	p := NewPipeline(f.db, m, f.state, nil, nil, nil, e, f.ledger, nil, f.auth, nil, WithConfigStore(config.NewStore(f.db)), WithQueueStore(qs))
	p.executionBinding = &f.binding
	v, err := f.state.GetVideo(ctx, f.videoID)
	require.NoError(t, err)
	outcome, err := p.executeVideoActions(ctx, f.runID, v)
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, outcome)
	v, err = f.state.GetVideo(ctx, f.videoID)
	require.NoError(t, err)
	var sinks, actions, consumption, guards int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM deferred_item WHERE video_id=? AND status='pending'`, f.videoID).Scan(&sinks))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM executed_action WHERE run_id=? AND outcome='deferred'`, f.runID).Scan(&actions))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&consumption))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard`).Scan(&guards))
	var requested sql.NullString
	require.NoError(t, f.db.QueryRow(`SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`, f.runID, f.videoID).Scan(&requested))
	t.Logf("returned=%s global=%s/%s requested=%v sinks=%d actions=%d calls=%d guards=%d", outcome, v.ProcessingState, v.FinalOutcome, requested, sinks, actions, consumption, guards)
	require.Equal(t, state.FinalOutcomeDeferred, v.FinalOutcome, "handled deferral must commit global disposition")
	require.Equal(t, 1, sinks, "handled deferral must retain pending work")
	require.Equal(t, sql.NullString{String: string(state.FinalOutcomeDeferred), Valid: true}, requested)
	require.Equal(t, 1, actions)
	require.Zero(t, consumption)
	require.Zero(t, guards)
}

func TestGuardedReceivingRunQuotaPreflightRecordsUncertainVeto(t *testing.T) {
	for _, tc := range []struct {
		name        string
		limit       int
		withHandoff bool
	}{{"quota_preflight", 50, false}, {"quota_preflight_handoff", 50, true}, {"guard_claim", 100, false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixture(t)
			defer f.db.Close()
			require.NoError(t, f.ledger.SetDailyLimit(ctx, tc.limit))
			var posts atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}]}}`)
			}))
			defer provider.Close()
			e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))
			_, err := e.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
			require.Error(t, err)
			before, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
			require.NoError(t, err)
			require.Len(t, before, 1)
			require.Equal(t, state.ActionOutcomeFailed, before[0].Outcome)
			require.NotNil(t, before[0].ExecutedAt)
			original, err := f.state.GetRun(ctx, f.runID)
			require.NoError(t, err)
			tx, err := f.db.BeginTx(ctx, nil)
			require.NoError(t, err)
			runID, err := f.state.CreateGuardedMutationRunTx(ctx, tx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: original.ConfigVersionID, ActionAuthorityVersion: 1}, true)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			decision := createRouteDecision(t, ctx, f.state, f.videoID, runID, f.routeID, 1, state.RouteDecisionMatched)
			if tc.withHandoff {
				ctx = withGuardedDeferral(ctx, func(*sql.Tx, queue.DeferredReason) error {
					t.Fatal("uncertain guard must veto deferral handoff")
					return nil
				})
			}
			out, err := e.ExecuteBound(ctx, runID, f.videoID, []RouteMatch{{RouteDecisionID: decision, RouteID: f.routeID}}, f.binding)
			require.Error(t, err)
			require.Equal(t, state.FinalOutcomeFailed, out)
			actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, runID)
			require.NoError(t, err)
			require.Len(t, actions, 1)
			require.Equal(t, state.ActionOutcomeNotAttempted, actions[0].Outcome)
			require.NotNil(t, actions[0].OutcomeReason)
			require.Equal(t, "mutation_application_uncertain", *actions[0].OutcomeReason)
			require.Nil(t, actions[0].ExecutedAt)
			after, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
			require.NoError(t, err)
			require.Equal(t, before, after)
			var guards, calls int
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, f.runID).Scan(&guards))
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&calls))
			require.Equal(t, 1, guards)
			require.Equal(t, 1, calls)
			require.Equal(t, int32(1), posts.Load())
		})
	}
}

func TestGuardedReceivingRunQuotaVetoRecordsAllActions(t *testing.T) {
	for _, handoff := range []bool{false, true} {
		t.Run(fmt.Sprintf("handoff_%t", handoff), func(t *testing.T) {
			ctx := context.Background()
			f := newGuardedProductionFixtureWithTargets(t, []string{"first", "second"})
			defer f.db.Close()
			require.NoError(t, f.ledger.SetDailyLimit(ctx, 100))
			var posts atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}]}}`)
			}))
			defer provider.Close()
			e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))
			_, err := e.ExecuteBound(ctx, f.runID, f.videoID, []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}, f.binding)
			require.Error(t, err)
			before, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
			require.NoError(t, err)
			original, err := f.state.GetRun(ctx, f.runID)
			require.NoError(t, err)
			tx, err := f.db.BeginTx(ctx, nil)
			require.NoError(t, err)
			runID, err := f.state.CreateGuardedMutationRunTx(ctx, tx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: original.ConfigVersionID, ActionAuthorityVersion: 1}, true)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			decision := createRouteDecision(t, ctx, f.state, f.videoID, runID, f.routeID, 1, state.RouteDecisionMatched)
			if handoff {
				ctx = withGuardedDeferral(ctx, func(*sql.Tx, queue.DeferredReason) error {
					t.Fatal("uncertain guard must veto deferral handoff")
					return nil
				})
			}
			var firstPass []state.ExecutedAction
			for i := 0; i < 2; i++ {
				out, err := e.ExecuteBound(ctx, runID, f.videoID, []RouteMatch{{RouteDecisionID: decision, RouteID: f.routeID}}, f.binding)
				require.Error(t, err)
				require.Equal(t, state.FinalOutcomeFailed, out)
				actions, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, runID)
				require.NoError(t, err)
				require.Len(t, actions, 2)
				reasons := map[string]string{}
				for _, a := range actions {
					require.Equal(t, state.ActionOutcomeNotAttempted, a.Outcome)
					require.Nil(t, a.ExecutedAt)
					require.NotNil(t, a.OutcomeReason)
					reasons[a.TargetPlaylistID] = *a.OutcomeReason
				}
				require.Equal(t, map[string]string{"first": "mutation_application_uncertain", "second": "prior_action_failed"}, reasons)
				if i == 0 {
					firstPass = actions
				} else {
					require.Equal(t, firstPass, actions)
				}
			}
			after, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
			require.NoError(t, err)
			require.Equal(t, before, after)
			var guards, calls int
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, f.runID).Scan(&guards))
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&calls))
			require.Equal(t, 1, guards)
			require.Equal(t, 1, calls)
			require.Equal(t, int32(1), posts.Load())
		})
	}
}
func TestReviewPreflightMustPreserveUncertainAttemptOnReentry(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	require.NoError(t, f.ledger.SetDailyLimit(ctx, 50))
	var posts atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(500)
		io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}]}}`)
	}))
	defer provider.Close()
	e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))
	matches := []RouteMatch{{RouteDecisionID: f.decisionID, RouteID: f.routeID}}
	_, err := e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.Error(t, err)
	before, err := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, state.ActionOutcomeFailed, before[0].Outcome)
	require.NotNil(t, before[0].ExecutedAt)
	out, err := e.ExecuteBound(ctx, f.runID, f.videoID, matches, f.binding)
	require.Error(t, err)
	require.Equal(t, state.FinalOutcomeFailed, out)
	after, readErr := f.state.GetExecutedActionsForVideoInRun(ctx, f.videoID, f.runID)
	require.NoError(t, readErr)
	require.Len(t, after, 1)
	t.Logf("reentry=%s err=%v before=%s/%v after=%s/%v reason=%v", out, err, before[0].Outcome, before[0].ExecutedAt, after[0].Outcome, after[0].ExecutedAt, after[0].OutcomeReason)
	require.Equal(t, int32(1), posts.Load())
	require.Equal(t, before[0].Outcome, after[0].Outcome, "budget refusal must not erase known original attempt")
	require.Equal(t, before[0].ExecutedAt, after[0].ExecutedAt)
}
