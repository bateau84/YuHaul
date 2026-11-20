//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type authorityTokenFence struct{ calls int }

func (s *authorityTokenFence) ActiveToken(context.Context) (*oauth2.Token, error) {
	s.calls++
	return &oauth2.Token{AccessToken: "should not be requested"}, nil
}

func TestProductionAuthority_CapturedConfiguration(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	require.Equal(t, 1, run.ActionAuthorityVersion)
	_, err = p.db.ExecContext(ctx, `UPDATE route_action SET target_playlist_id='PLchanged'; UPDATE route_condition SET value='rock'; UPDATE route SET is_enabled=0,is_final=1`)
	require.NoError(t, err)
	p.Execute(ctx, run.ID)
	loaded, err := env.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status)
	ins := env.server.Insertions()
	require.Len(t, ins, 1)
	cv, err := config.NewStore(p.db).Get(ctx, *run.ConfigVersionID)
	require.NoError(t, err)
	require.Equal(t, cv.Snapshot.Routes[0].Actions[0].TargetPlaylistID, ins[0].PlaylistID)
	require.NotEqual(t, "PLchanged", ins[0].PlaylistID)
}

func TestProductionAuthority_ConcurrentExecuteSerializesRecovery(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	run, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; env.pipeline.Execute(ctx, run.ID) }()
	}
	close(start)
	wg.Wait()
	loaded, err := env.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status)
	require.Equal(t, 1, loaded.VideosAccepted)
	require.Len(t, env.server.Insertions(), 1)
	var routed int
	require.NoError(t, env.pipeline.db.QueryRowContext(ctx, `SELECT total_routed FROM channel WHERE channel_id='UC1'`).Scan(&routed))
	require.Equal(t, 1, routed)
}

func TestProductionAuthority_RetentionDeadlineInterruptsThenResumes(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	videos, err := p.phaseDiscovery(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, videos, 1)

	lockConn, err := p.db.Conn(ctx)
	require.NoError(t, err)
	_, err = lockConn.ExecContext(ctx, `BEGIN IMMEDIATE`)
	require.NoError(t, err)
	locked := make(chan struct{})
	go func() {
		defer close(locked)
		time.Sleep(11 * time.Second)
		_, _ = lockConn.ExecContext(context.Background(), `ROLLBACK`)
		_ = lockConn.Close()
	}()

	expired, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	p.retainAuthorityFailure(expired, run.ID, videos, fmt.Errorf("captured snapshot unreadable: %s", "safe test reason"))
	cancel()
	<-locked

	loaded, err := env.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusInterrupted, loaded.Status)
	require.NotNil(t, loaded.InterruptReason)
	require.Equal(t, "diag:v1 phase=unknown operation=execute_action code=action_authority_invalid", *loaded.InterruptReason)
	require.NotContains(t, *loaded.InterruptReason, "safe test reason")
	queueStore, err := queue.NewStore(p.db)
	require.NoError(t, err)
	entries, err := queueStore.ListErrorEntries(ctx, queue.ErrorStatusActive)
	require.NoError(t, err)
	require.Empty(t, entries, "no queue handoff committed while the writer remained locked")

	// A normal Execute call is the real recovery path: it resumes the interrupted
	// run and processes its still-run-owned video after the transient lock clears.
	p.Execute(ctx, run.ID)
	loaded, err = env.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusCompleted, loaded.Status)
	entries, err = queueStore.ListErrorEntries(ctx, queue.ErrorStatusActive)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestProductionAuthority_LegacyFullVector(t *testing.T) {
	for _, kind := range []string{"compatible", "nonmatch", "order", "skip", "audit_missing", "audit_actual_null", "audit_expected", "audit_match", "metadata_missing", "metadata_expired"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
			p := env.pipeline
			_, err := router.NewStore(p.db).CreateRoute(ctx, &router.Route{Name: "Nonmatch", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND", Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "classical"}}, Actions: []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLnever"}}})
			require.NoError(t, err)
			cfgID, err := p.versioner.ActiveVersionID(ctx)
			require.NoError(t, err)
			run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: cfgID}
			run.ID, err = p.stateStore.CreateRun(ctx, run)
			require.NoError(t, err)
			vs, err := p.phaseDiscovery(ctx, run.ID)
			require.NoError(t, err)
			_, err = p.phaseRouting(ctx, run.ID, vs)
			require.NoError(t, err)
			ds, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, run.ID)
			require.NoError(t, err)
			require.Len(t, ds, 2)
			query := ""
			switch kind {
			case "nonmatch":
				query = `UPDATE route_decision SET rejection_reason='changed' WHERE decision='not_matched'`
			case "order":
				query = `UPDATE route_decision SET evaluation_order=20 WHERE decision='not_matched'`
			case "skip":
				query = `UPDATE route_decision SET decision='skipped',skip_reason='disabled',rejection_reason=NULL WHERE decision='not_matched'`
			case "audit_missing":
				query = `DELETE FROM condition_audit WHERE route_decision_id=(SELECT id FROM route_decision WHERE decision='not_matched')`
			case "audit_actual_null":
				query = `UPDATE condition_audit SET actual_value=NULL WHERE route_decision_id=(SELECT id FROM route_decision WHERE decision='not_matched')`
			case "audit_expected":
				query = `UPDATE condition_audit SET expected_value='jazz' WHERE route_decision_id=(SELECT id FROM route_decision WHERE decision='not_matched')`
			case "audit_match":
				query = `UPDATE condition_audit SET matched=1 WHERE route_decision_id=(SELECT id FROM route_decision WHERE decision='not_matched')`
			case "metadata_missing":
				query = `DELETE FROM transient_metadata`
			case "metadata_expired":
				query = `UPDATE transient_metadata SET expires_at='2000-01-01T00:00:00Z'`
			}
			if query != "" {
				_, err = p.db.ExecContext(ctx, query)
				require.NoError(t, err)
			}
			_, err = p.executor.Execute(ctx, run.ID, vs[0].ID, []RouteMatch{{ds[0].ID, ds[0].RouteID}})
			if kind == "compatible" {
				require.ErrorContains(t, err, "run lacks guarded dispatch capability")
				require.Empty(t, env.server.Insertions())
			} else {
				require.Error(t, err)
				require.Empty(t, env.server.Insertions())
			}
		})
	}
}

func TestProductionAuthority_LegacyPrefixCompletion(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	second, err := router.NewStore(p.db).CreateRoute(ctx, &router.Route{Name: "Second", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND", Conditions: []router.RouteCondition{{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "jazz"}}, Actions: []router.RouteAction{{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLsecond"}}})
	require.NoError(t, err)
	cfgID, err := p.versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: cfgID}
	run.ID, err = p.stateStore.CreateRun(ctx, run)
	require.NoError(t, err)
	vs, err := p.phaseDiscovery(ctx, run.ID)
	require.NoError(t, err)
	_, err = p.db.ExecContext(ctx, `CREATE TRIGGER stop_second BEFORE INSERT ON route_decision WHEN NEW.route_id=`+itoa(int(second))+` BEGIN SELECT RAISE(ABORT,'interrupt'); END`)
	require.NoError(t, err)
	_, err = p.phaseRouting(ctx, run.ID, vs)
	require.ErrorContains(t, err, "interrupt")
	before, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, run.ID)
	require.NoError(t, err)
	require.Len(t, before, 1)
	_, err = p.db.ExecContext(ctx, `DROP TRIGGER stop_second; UPDATE route_condition SET value='rock'; UPDATE route_action SET target_playlist_id='PLchanged'`)
	require.NoError(t, err)
	require.NoError(t, p.stateStore.InterruptRun(ctx, run.ID, "restart"))
	p.Execute(ctx, run.ID)
	after, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, run.ID)
	require.NoError(t, err)
	require.Len(t, after, 2)
	require.Equal(t, before[0], after[0])
	loaded, err := p.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, 0, loaded.ActionAuthorityVersion)
	require.Equal(t, state.RunStatusFailed, loaded.Status)
	require.Empty(t, env.server.Insertions())
	var audits int
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM condition_audit`).Scan(&audits))
	require.Equal(t, 2, audits)
}

func TestProductionAuthority_NativeRecordedEvidenceSurvivesMetadataExpiry(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	vs, err := p.phaseDiscovery(ctx, run.ID)
	require.NoError(t, err)
	_, err = p.phaseRouting(ctx, run.ID, vs)
	require.NoError(t, err)
	_, err = p.db.ExecContext(ctx, `DELETE FROM transient_metadata; UPDATE route_condition SET value='rock'; UPDATE route_action SET target_playlist_id='PLchanged'`)
	require.NoError(t, err)
	require.NoError(t, p.stateStore.SetRunPhase(ctx, run.ID, string(PhaseActionExecution)))
	require.NoError(t, p.stateStore.InterruptRun(ctx, run.ID, "restart"))
	p.Execute(ctx, run.ID)
	require.Len(t, env.server.Insertions(), 1)
	require.NotEqual(t, "PLchanged", env.server.Insertions()[0].PlaylistID)
}

func TestProductionAuthority_ExactTargetCompletionBeforeQuota(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	run, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	p.Execute(ctx, run.ID)
	v, err := p.stateStore.GetVideoByExternalID(ctx, "V1")
	require.NoError(t, err)
	require.Len(t, env.server.Insertions(), 1)
	// Evaluate the same persisted video in a fresh production run, as a receiving
	// retry will do. Discovery identity and old action evidence are unchanged.
	next, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	m, err := metadata.NewStore(p.db).Get(ctx, v.VideoID)
	require.NoError(t, err)
	_, routes, err := productionAuthority(ctx, p.db, p.configStore, next.ID)
	require.NoError(t, err)
	_, err = p.router.EvaluateWithRoutes(ctx, next.ID, v.ID, videoContextFromMetadata(v.VideoID, v.ChannelID, m), p.machine, routes)
	require.NoError(t, err)
	ds, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, next.ID)
	require.NoError(t, err)
	require.NoError(t, p.ledger.SetDailyLimit(ctx, 10))
	out, err := p.executor.Execute(ctx, next.ID, v.ID, []RouteMatch{{ds[0].ID, ds[0].RouteID}, {ds[0].ID, ds[0].RouteID}})
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeAccepted, out)
	require.Len(t, env.server.Insertions(), 1)
	// The suppression is explicit current-run evidence, never a fabricated
	// success or attempt timestamp (ARD-001/BR-012).
	suppressed, err := p.stateStore.GetExecutedActionsForVideoInRun(ctx, v.ID, next.ID)
	require.NoError(t, err)
	require.Len(t, suppressed, 1, "duplicate matches must not create repeated evidence")
	require.Equal(t, state.ActionOutcomeNotAttempted, suppressed[0].Outcome)
	require.NotNil(t, suppressed[0].OutcomeReason)
	require.Equal(t, "already_completed", *suppressed[0].OutcomeReason)
	require.Nil(t, suppressed[0].ExecutedAt)
	_, err = p.db.ExecContext(ctx, `UPDATE route_action SET target_playlist_id='PLnew'`)
	require.NoError(t, err)
	changed, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	_, routes, err = productionAuthority(ctx, p.db, p.configStore, changed.ID)
	require.NoError(t, err)
	_, err = p.router.EvaluateWithRoutes(ctx, changed.ID, v.ID, videoContextFromMetadata(v.VideoID, v.ChannelID, m), p.machine, routes)
	require.NoError(t, err)
	ds, err = p.stateStore.GetRouteDecisionsForVideoInRun(ctx, v.ID, changed.ID)
	require.NoError(t, err)
	out, err = p.executor.Execute(ctx, changed.ID, v.ID, []RouteMatch{{ds[0].ID, ds[0].RouteID}})
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeDeferred, out)
	// The quota deferral is recorded without an attempt claim.
	deferredActions, err := p.stateStore.GetExecutedActionsForVideoInRun(ctx, v.ID, changed.ID)
	require.NoError(t, err)
	require.Len(t, deferredActions, 1)
	require.Equal(t, state.ActionOutcomeDeferred, deferredActions[0].Outcome)
	require.NotNil(t, deferredActions[0].OutcomeReason)
	require.Equal(t, "quota_exhausted", *deferredActions[0].OutcomeReason)
	require.Nil(t, deferredActions[0].ExecutedAt)
	require.NoError(t, p.ledger.SetDailyLimit(ctx, 100000))
	out, err = p.executor.Execute(ctx, changed.ID, v.ID, []RouteMatch{{ds[0].ID, ds[0].RouteID}})
	require.NoError(t, err)
	require.Equal(t, state.FinalOutcomeAccepted, out)
	require.Len(t, env.server.Insertions(), 2)
	require.Equal(t, "PLnew", env.server.Insertions()[1].PlaylistID)
	// The same-run retry replaced the deferral row with the real attempt's
	// outcome instead of duplicating or violating the action identity.
	retried, err := p.stateStore.GetExecutedActionsForVideoInRun(ctx, v.ID, changed.ID)
	require.NoError(t, err)
	require.Len(t, retried, 1)
	require.Equal(t, state.ActionOutcomeSucceeded, retried[0].Outcome)
	require.NotNil(t, retried[0].ExecutedAt)
}

func TestProductionAuthority_LegacyContradictionPreservesAllWork(t *testing.T) {
	ctx := context.Background()
	env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour)), makeJazzVideo("V2", time.Now().UTC().Add(-time.Hour))})
	p := env.pipeline
	_, err := p.db.ExecContext(ctx, `UPDATE route_condition SET value='classical'`)
	require.NoError(t, err)
	id, err := p.versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	legacy := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: id}
	legacy.ID, err = env.stateStore.CreateRun(ctx, legacy)
	require.NoError(t, err)
	vs, err := p.phaseDiscovery(ctx, legacy.ID)
	require.NoError(t, err)
	require.Len(t, vs, 2)
	_, err = p.db.ExecContext(ctx, `UPDATE route_condition SET value='jazz'`)
	require.NoError(t, err)
	// Persist historical live-route evidence explicitly: today's recording
	// router correctly refuses a route that contradicts the captured snapshot.
	m, err := p.meta.GetDisplayMetadata(ctx, vs[0].VideoID)
	require.NoError(t, err)
	routes, err := p.router.Routes(ctx)
	require.NoError(t, err)
	expected := p.router.ExpectedEvaluations(legacy.ID, vs[0].ID, videoContextFromMetadata(vs[0].VideoID, vs[0].ChannelID, m), routes)
	require.Len(t, expected, 1)
	require.Equal(t, state.RouteDecisionMatched, expected[0].Decision.Decision)
	_, err = p.machine.RecordRouteEvaluation(ctx, &expected[0].Decision, expected[0].Audits)
	require.NoError(t, err)
	before, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, legacy.ID)
	require.NoError(t, err)
	audits, err := env.stateStore.GetConditionAuditsForDecision(ctx, before[0].ID)
	require.NoError(t, err)
	require.Equal(t, "jazz", audits[0].ExpectedValue)
	require.NoError(t, env.stateStore.SetRunPhase(ctx, legacy.ID, string(PhaseActionExecution)))
	require.NoError(t, env.stateStore.InterruptRun(ctx, legacy.ID, "old engine interrupted"))
	_, err = p.executor.Execute(ctx, legacy.ID, vs[0].ID, []RouteMatch{{before[0].ID, before[0].RouteID}})
	require.ErrorContains(t, err, "snapshot mismatch")
	p.Execute(ctx, legacy.ID)
	require.Empty(t, env.server.Insertions())
	after, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	afterAudits, err := env.stateStore.GetConditionAuditsForDecision(ctx, before[0].ID)
	require.NoError(t, err)
	require.Equal(t, audits, afterAudits)
	loaded, err := env.stateStore.GetRun(ctx, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, 0, loaded.ActionAuthorityVersion)
	require.Equal(t, id, loaded.ConfigVersionID)
	require.Equal(t, state.RunStatusFailed, loaded.Status)
	var retained, actions int
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT video_id) FROM error_entry WHERE run_id=? AND status='active'`, legacy.ID).Scan(&retained))
	require.Equal(t, 2, retained)
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action`).Scan(&actions))
	require.Zero(t, actions)
	p.Execute(ctx, legacy.ID)
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM error_entry WHERE run_id=?`, legacy.ID).Scan(&retained))
	require.Equal(t, 2, retained)
	qs, err := queue.NewStore(p.db)
	require.NoError(t, err)
	p.queueStore = qs
	entries, err := qs.ListErrorEntries(ctx, queue.ErrorStatusActive)
	require.NoError(t, err)
	key := []byte("01234567890123456789012345678901")
	tokens, err := auth.NewSQLiteTokenStore(p.db, key)
	require.NoError(t, err)
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "local-test", RefreshToken: "local-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	authService, err := auth.NewService(&auth.ServiceConfig{ClientID: "test", ClientSecret: "test", RedirectURL: "http://localhost/callback", EncryptionKey: string(key)}, tokens, auth.NewInMemoryStateStore())
	require.NoError(t, err)
	p.auth = authService
	p.executor = NewExecutor(p.db, p.ledger, authService, WithYouTubeBaseURL(env.server.URL()))
	svc := queue.NewService(qs, nil).WithRetryCoordinator(p)
	entry, err := svc.RetryError(ctx, entries[0].ID)
	require.NoError(t, err)
	require.Equal(t, queue.ErrorStatusResolved, entry.Status)
	require.NotNil(t, entry.RetryRunID)
	require.NotEqual(t, legacy.ID, *entry.RetryRunID)
	var retrySafetyVersion int
	require.NoError(t, p.db.QueryRowContext(ctx, `SELECT mutation_safety_version FROM run WHERE id=?`, *entry.RetryRunID).Scan(&retrySafetyVersion))
	require.Equal(t, 1, retrySafetyVersion)
	require.Empty(t, env.server.Insertions(), "legacy ambiguous work must veto a retry send")
	fresh, err := p.stateStore.GetRun(ctx, *entry.RetryRunID)
	require.NoError(t, err)
	require.Equal(t, 1, fresh.ActionAuthorityVersion)
	require.Equal(t, state.RunStatusCompleted, fresh.Status)
	unchanged, err := p.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
}

func TestProductionAuthority_ExecutorRejectsWholeInvalidSet(t *testing.T) {
	for _, kind := range []string{"simulation", "foreign_run", "foreign_decision", "simulation_decision", "wrong_video", "wrong_route", "nonmatch", "absent_route", "snapshot_unlinked", "snapshot_corrupt"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			env := setupIdempotencyTest(t, []mockVideo{makeJazzVideo("V1", time.Now().UTC().Add(-time.Hour))})
			p := env.pipeline
			fence := &authorityTokenFence{}
			p.executor.(*Executor).auth = fence
			run, err := p.StartRun(ctx, state.RunTypeManual)
			require.NoError(t, err)
			vs, err := p.phaseDiscovery(ctx, run.ID)
			require.NoError(t, err)
			_, err = p.phaseRouting(ctx, run.ID, vs)
			require.NoError(t, err)
			ds, err := env.stateStore.GetRouteDecisionsForVideoInRun(ctx, vs[0].ID, run.ID)
			require.NoError(t, err)
			valid := RouteMatch{ds[0].ID, ds[0].RouteID}
			bad := valid
			switch kind {
			case "simulation":
				_, err = p.db.ExecContext(ctx, `UPDATE run SET run_type='backtest' WHERE id=?`, run.ID)
			case "snapshot_unlinked":
				_, err = p.db.ExecContext(ctx, `UPDATE run SET config_version_id=NULL WHERE id=?`, run.ID)
			case "snapshot_corrupt":
				_, err = p.db.ExecContext(ctx, `UPDATE config_version SET snapshot='{' WHERE id=?`, *run.ConfigVersionID)
			case "foreign_run":
				other, e := p.StartRun(ctx, state.RunTypeManual)
				require.NoError(t, e)
				run = other
			case "foreign_decision", "simulation_decision":
				other := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ConfigVersionID: run.ConfigVersionID}
				if kind == "simulation_decision" {
					other.RunType = state.RunTypeBacktest
				}
				other.ID, err = p.stateStore.CreateRun(ctx, other)
				require.NoError(t, err)
				d, e := p.machine.RecordRouteDecision(ctx, &state.RouteDecision{VideoID: vs[0].ID, RunID: other.ID, RouteID: ds[0].RouteID, EvaluationOrder: 1, Decision: state.RouteDecisionMatched})
				require.NoError(t, e)
				bad.RouteDecisionID = d.ID
			case "wrong_video":
				v, e := p.machine.DiscoverVideo(ctx, run.ID, "Vother", "ch-other")
				require.NoError(t, e)
				_, err = p.db.ExecContext(ctx, `UPDATE route_decision SET video_id=? WHERE id=?`, v.ID, ds[0].ID)
			case "wrong_route":
				bad.RouteID++
			case "nonmatch":
				_, err = p.db.ExecContext(ctx, `UPDATE route_decision SET decision='not_matched' WHERE id=?`, ds[0].ID)
			case "absent_route":
				id, e := router.NewStore(p.db).CreateRoute(ctx, &router.Route{Name: "not captured", EvaluationOrder: 2, IsEnabled: true, ConditionComposition: "AND"})
				require.NoError(t, e)
				d, e := p.machine.RecordRouteDecision(ctx, &state.RouteDecision{VideoID: vs[0].ID, RunID: run.ID, RouteID: id, EvaluationOrder: 2, Decision: state.RouteDecisionMatched})
				require.NoError(t, e)
				bad = RouteMatch{d.ID, id}
			}
			require.NoError(t, err)
			_, err = p.executor.Execute(ctx, run.ID, vs[0].ID, []RouteMatch{valid, bad})
			require.Error(t, err)
			require.Zero(t, fence.calls, "reference admission precedes token access")
			require.Empty(t, env.server.Insertions())
			var n int
			require.NoError(t, p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM executed_action`).Scan(&n))
			require.Zero(t, n)
		})
	}
}
