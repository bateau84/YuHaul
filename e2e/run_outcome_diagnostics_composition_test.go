//go:build integration

// Composition proof for ARD-001 durable run diagnostics (run-outcome-
// diagnostics Task 1.5).
//
// The pipeline tests read stores directly and the API tests seed final rows;
// this file closes that gap. Every case runs the real pipeline and its real
// selector, persists through the real migrated SQLite store, closes and
// reopens that same database file, and then reads the producer-created state
// back through the registered HTTP routes (run summary, run-video list, and
// video detail). No final run status, selected count, route decision, action,
// or outcome row is ever seeded. Only YouTube HTTP is substituted; auth reads
// a synthetic token from encrypted SQLite storage, and discovery, metadata,
// and execution preserve OAuth2 transport while targeting the controlled
// provider. Every observed provider request is asserted to carry its bearer.
// A SQLite trigger is used where a real
// mid-run persistence failure boundary is required (the same technique the
// internal pipeline tests already use).
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

// diagSentinel is raw-provider-payload content. Its JSON markers make it
// unsafe diagnostic text: after a real provider failure the product must keep
// this body out of persistence and API output, exposing only the bounded
// status/vetted-reason summary instead (ARD-001).
const diagSentinel = "SENTINEL_SECRET_body_text"

// diagSentinelBody is a raw YouTube-style error payload carrying the
// sentinel, returned by the substituted provider on injected failures.
const diagSentinelBody = `{"error":{"code":500,"message":"` + diagSentinel + `","errors":[{"reason":"backendError"}]}}`

// quotaExceededBody is YouTube's real quota-rejection shape; the client maps
// its vetted reason to the product's quota-deferral path.
const quotaExceededBody = `{"error":{"code":403,"message":"The request cannot be completed because you have exceeded your quota.","errors":[{"reason":"quotaExceeded","message":"The request cannot be completed because you have exceeded your quota."}]}}`

// compRun is the decoded GET /api/runs/{id} projection of a run.
type compRun struct {
	RunType              string  `json:"run_type"`
	Status               string  `json:"status"`
	VideosDiscovered     int     `json:"videos_discovered"`
	VideosSelected       *int    `json:"videos_selected"`
	VideosEvaluated      int     `json:"videos_evaluated"`
	VideosAccepted       int     `json:"videos_accepted"`
	VideosDeferred       int     `json:"videos_deferred"`
	VideosFailed         int     `json:"videos_failed"`
	InterruptReason      *string `json:"interrupt_reason"`
	FailureReasonUnavail bool    `json:"failure_reason_unavailable"`
}

// compAction is one executed-action entry in a video detail response.
type compAction struct {
	Target        string     `json:"target_playlist_id"`
	Outcome       string     `json:"outcome"`
	OutcomeReason *string    `json:"outcome_reason"`
	ErrorMessage  *string    `json:"error_message"`
	ExecutedAt    *time.Time `json:"executed_at"`
}

// compDecision is one route decision in a video list/detail response.
type compDecision struct {
	Decision string            `json:"decision"`
	Audits   []json.RawMessage `json:"condition_audits"`
	Actions  []compAction      `json:"executed_actions"`
}

// compVideo is one video in a run-video list or video detail response.
type compVideo struct {
	VideoID         string         `json:"video_id"`
	ProcessingState string         `json:"processing_state"`
	FinalOutcome    *string        `json:"final_outcome"`
	ErrorMessage    *string        `json:"error_message"`
	RouteDecisions  []compDecision `json:"route_decisions"`
}

// getRunSummary reads GET /api/runs/{id} through the registered routes and
// returns the decoded run plus the raw body for sentinel checks.
func getRunSummary(t *testing.T, api *httptest.Server, runID int64) (compRun, []byte) {
	t.Helper()
	status, body := getJSON(t, guardClient(t, api.Client()), fmt.Sprintf("%s/api/runs/%d", api.URL, runID))
	require.Equal(t, http.StatusOK, status, string(body))
	var run compRun
	require.NoError(t, json.Unmarshal(body, &run))
	return run, body
}

// getRunVideoList reads GET /api/runs/{id}/videos through the registered
// routes and returns the decoded videos, the reported total, and the raw body.
func getRunVideoList(t *testing.T, api *httptest.Server, runID int64) ([]compVideo, int, []byte) {
	t.Helper()
	status, body := getJSON(t, guardClient(t, api.Client()), fmt.Sprintf("%s/api/runs/%d/videos", api.URL, runID))
	require.Equal(t, http.StatusOK, status, string(body))
	var resp struct {
		Videos     []compVideo `json:"videos"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Videos, resp.Pagination.Total, body
}

// getRunVideoDetail reads GET /api/runs/{id}/videos/{videoId} through the
// registered routes and returns the decoded video plus the raw body.
func getRunVideoDetail(t *testing.T, api *httptest.Server, runID int64, videoID string) (compVideo, []byte) {
	t.Helper()
	status, body := getJSON(t, guardClient(t, api.Client()),
		fmt.Sprintf("%s/api/runs/%d/videos/%s", api.URL, runID, videoID))
	require.Equal(t, http.StatusOK, status, string(body))
	var resp struct {
		Video compVideo `json:"video"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Video, body
}

// reopenAndServe closes the original pipeline database handle and reopens the
// same migrated SQLite file, then serves the run/video reads through the
// registered HTTP routes backed only by the fresh connection. This proves the
// evidence is durable in SQLite rather than reachable only through the
// writer's handles. The pipeline is idle by the time every case calls this.
func reopenAndServe(t *testing.T, env *testEnv) (*httptest.Server, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.db.Close())

	db, err := store.Open(ctx, env.dbPath)
	require.NoError(t, err, "the same migrated database file must reopen cleanly")
	t.Cleanup(func() { _ = db.Close() })

	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	routerStore := router.NewStore(db)
	cfgStore := config.NewStore(db)

	api := server.NewServer(env.pipeline, env.broadcast,
		server.WithRouteStore(routerStore), server.WithRunStore(stateStore),
		server.WithRunVideoStore(stateStore), server.WithConfigVersionStore(cfgStore),
		server.WithConfigHandler(config.NewHandler(config.NewService(routerStore, cfgStore), nil)))
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv, db
}

// armDecisionFailure installs a SQLite trigger that aborts route-decision
// inserts once `allow` decisions already exist — a real persistence failure
// during routing, after the selector has established its input set.
func armDecisionFailure(t *testing.T, ctx context.Context, db *sql.DB, allow int) {
	t.Helper()
	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TRIGGER injected_decision_failure BEFORE INSERT ON route_decision
		 WHEN (SELECT COUNT(*) FROM route_decision) >= %d
		 BEGIN SELECT RAISE(ABORT, 'injected decision failure'); END;`, allow))
	require.NoError(t, err)
}

// createTwoActionRoute creates one enabled matching route with two playlist
// insert actions, for evidence that distinguishes per-action attempts.
func createTwoActionRoute(t *testing.T, env *testEnv, name, value, first, second string) {
	t.Helper()
	_, err := env.routerStore.CreateRoute(context.Background(), &router.Route{
		Name:                 name,
		EvaluationOrder:      1,
		IsEnabled:            true,
		ConditionComposition: "OR",
		Conditions: []router.RouteCondition{{
			ConditionIndex: 0, Field: router.ConditionFieldTitle,
			Operator: router.ConditionOperatorContains, Value: value,
		}},
		Actions: []router.RouteAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: first},
			{ActionIndex: 1, ActionType: "playlist_insert", TargetPlaylistID: second},
		},
	})
	require.NoError(t, err)
}

// actionsByTarget indexes one decision's executed actions by target playlist.
func actionsByTarget(t *testing.T, actions []compAction) map[string]compAction {
	t.Helper()
	require.NotEmpty(t, actions)
	byTarget := make(map[string]compAction, len(actions))
	for _, a := range actions {
		byTarget[a.Target] = a
	}
	return byTarget
}

func compositionChannels() []mockChannel {
	return []mockChannel{{ID: "UC1", Title: "Test Channel", UploadsPlaylistID: "UUPL1"}}
}

// TestComposition_PartialEvaluationFailureThroughHTTP runs the real production
// pipeline into a real mid-routing persistence failure and proves the durable
// evidence — terminal failed status, the known selected count that survived
// the failure, the partially evaluated count, the unchanged discovered count,
// and the safe failure reason — is readable through the registered run
// summary, run-video list, and video detail routes of a reopened SQLite
// database (ARD-001; run-outcome-diagnostics Task 1.5).
func TestComposition_PartialEvaluationFailureThroughHTTP(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	env := setupDiagnosticCompositionEnv(t, compositionChannels(), []mockVideo{
		makeVideo("V1", "jazz one", now.Add(-3*time.Hour)),
		makeVideo("V2", "rock concert", now.Add(-2*time.Hour)),
		makeVideo("V3", "jazz two", now.Add(-time.Hour)),
	})
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	run, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	// One decision insert is allowed; the next aborts. This is a real
	// persistence failure during routing, after the production selector
	// established its three-input set.
	armDecisionFailure(t, ctx, env.db, 1)
	env.pipeline.Execute(ctx, run.ID)
	env.server.assertBearerRequests(t)

	api, db := reopenAndServe(t, env)

	summary, summaryBody := getRunSummary(t, api, run.ID)
	assert.Equal(t, string(state.RunStatusFailed), summary.Status,
		"an unrecoverable routing failure must end terminal failed")
	require.NotNil(t, summary.VideosSelected,
		"the selector's input set must survive the mid-run failure")
	assert.Equal(t, 3, *summary.VideosSelected)
	assert.Equal(t, 1, summary.VideosEvaluated,
		"videos_evaluated counts persisted decision evidence, not the selected input")
	assert.Equal(t, 3, summary.VideosDiscovered,
		"videos_discovered keeps discovery meaning, independent of evaluation")
	require.NotNil(t, summary.InterruptReason,
		"a failed run exposes its safe failure reason")
	assert.Equal(t, "diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_CONSTRAINT", *summary.InterruptReason,
		"the safe diagnostic must preserve the typed SQLite code and known operation")
	assert.False(t, summary.FailureReasonUnavail, "safe reasons must stay visible")

	// The evaluated projection agrees with its own durable query source on the
	// reopened database, not with the API projection alone.
	var evaluatedSources int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT video_id) FROM route_decision WHERE run_id = ?`, run.ID).
		Scan(&evaluatedSources))
	assert.Equal(t, evaluatedSources, summary.VideosEvaluated,
		"videos_evaluated must equal the distinct decision-bearing video count")

	videos, total, listBody := getRunVideoList(t, api, run.ID)
	assert.Equal(t, 3, total, "the requested-run list covers every run video")
	require.Len(t, videos, 3)
	states := make(map[string]int)
	var evaluatedVideo *compVideo
	for i := range videos {
		states[videos[i].ProcessingState]++
		if len(videos[i].RouteDecisions) > 0 {
			require.Nil(t, evaluatedVideo,
				"exactly one video may carry decision evidence")
			evaluatedVideo = &videos[i]
		}
	}
	assert.Equal(t, map[string]int{
		string(state.VideoStateEvaluated):       1,
		string(state.VideoStateEvaluating):      1,
		string(state.VideoStateNotYetEvaluated): 1,
	}, states, "partial routing leaves one evaluated, one mid-evaluation, one untouched")
	require.NotNil(t, evaluatedVideo, "partial evaluation leaves durable decision evidence")

	detail, detailBody := getRunVideoDetail(t, api, run.ID, evaluatedVideo.VideoID)
	require.Len(t, detail.RouteDecisions, 1)
	assert.Equal(t, "matched", detail.RouteDecisions[0].Decision)
	require.Len(t, detail.RouteDecisions[0].Audits, 1,
		"a persisted decision carries its condition audit")
	assert.Empty(t, detail.RouteDecisions[0].Actions,
		"a run that never reached actions must record no action rows")
	require.NotNil(t, detail.FinalOutcome)
	assert.Equal(t, string(state.FinalOutcomeNotYetEvaluated), *detail.FinalOutcome,
		"an evaluated-but-unactioned video keeps not_yet_evaluated; missing evidence is never rejection")

	// Videos the failing phase never reached keep explicit missing evidence.
	for _, v := range videos {
		if v.VideoID == evaluatedVideo.VideoID {
			continue
		}
		other, _ := getRunVideoDetail(t, api, run.ID, v.VideoID)
		assert.Empty(t, other.RouteDecisions,
			"video %s was never evaluated and must carry no decision", v.VideoID)
		require.NotNil(t, other.FinalOutcome)
		assert.Equal(t, string(state.FinalOutcomeNotYetEvaluated), *other.FinalOutcome,
			"an untouched video is never reclassified by the failing run")
	}

	require.NotContains(t, string(summaryBody), diagSentinel)
	require.NotContains(t, string(listBody), diagSentinel)
	require.NotContains(t, string(detailBody), diagSentinel)
}

func TestComposition_ReplayEvaluationFailureHasSourceFactsAfterReopen(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	env := setupDiagnosticCompositionEnv(t, compositionChannels(), []mockVideo{
		makeVideo("V1", "jazz one", now.Add(-time.Hour)),
	})
	env.createRoute(t, "Jazz", "jazz", "PLjazz")
	productionRun, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	env.pipeline.Execute(ctx, productionRun.ID)

	replayRun, err := env.pipeline.Replay(ctx, nil, state.PublishedRange{})
	require.NoError(t, err)
	var existingDecisions int
	require.NoError(t, env.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_decision`).Scan(&existingDecisions))
	armDecisionFailure(t, ctx, env.db, existingDecisions)
	env.pipeline.ExecuteReplay(ctx, replayRun.ID)
	api, _ := reopenAndServe(t, env)

	summary, body := getRunSummary(t, api, replayRun.ID)
	assert.Equal(t, string(state.RunStatusFailed), summary.Status)
	require.NotNil(t, summary.InterruptReason)
	assert.Equal(t, "diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_CONSTRAINT", *summary.InterruptReason)
	assert.False(t, summary.FailureReasonUnavail)
	assert.NotContains(t, string(body), "injected decision failure")
}

func TestComposition_DryRunEvaluationFailureHasSourceFactsAfterReopen(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	env := setupDiagnosticCompositionEnv(t, compositionChannels(), []mockVideo{
		makeVideo("V_PENDING", "jazz pending", now.Add(-time.Hour)),
	})
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	// Establish an ordinary durable pending input through the real state store.
	// The dry-run itself performs the selector, metadata call, and route
	// evaluation; only its SQLite write boundary is forced to fail.
	discoveryRun, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	_, err = env.stateStore.CreateVideo(ctx, &state.Video{
		VideoID: "V_PENDING", ChannelID: "UC1", DiscoveredAt: now,
		DiscoveredInRunID: discoveryRun.ID, ProcessingState: state.VideoStateNotYetEvaluated,
		FinalOutcome: state.FinalOutcomeNotYetEvaluated,
	})
	require.NoError(t, err)

	dryRun, err := env.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	armDecisionFailure(t, ctx, env.db, 0)
	env.pipeline.ExecuteDryRun(ctx, dryRun.ID)

	api, _ := reopenAndServe(t, env)
	summary, body := getRunSummary(t, api, dryRun.ID)
	assert.Equal(t, string(state.RunStatusFailed), summary.Status)
	require.NotNil(t, summary.InterruptReason)
	assert.Equal(t, "diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_CONSTRAINT", *summary.InterruptReason)
	assert.False(t, summary.FailureReasonUnavail)
	assert.NotContains(t, string(body), "injected decision failure")
}

// TestComposition_ActionOutcomesThroughHTTP executes two real production runs
// — a quota-deferred run and its automatically claimed retry run — and proves
// the durable per-run action evidence reads back through the registered routes
// of the reopened database: a real attempt with its timestamp, a quota
// deferral without one, and the retry's already_completed no-attempt
// suppression that never fabricates a second success (ARD-001).
func TestComposition_ActionOutcomesThroughHTTP(t *testing.T) {
	now := time.Now().UTC()
	env := setupDiagnosticCompositionEnv(t, compositionChannels(), []mockVideo{
		makeVideo("V1", "jazz one", now.Add(-time.Hour)),
	})
	createTwoActionRoute(t, env, "Jazz", "jazz", "PLjazzA", "PLjazzB")

	// The provider rejects the second target with its quotaExceeded reason:
	// a real quota deferral at the attempt boundary. The first target
	// succeeds normally.
	env.server.FailPlaylistInsert("PLjazzB", http.StatusForbidden, quotaExceededBody)
	run1 := env.runProductionRun(t)
	require.Equal(t, 1, run1.VideosDeferred, "the quota-rejected insert must defer its video")

	env.server.ClearFailures()
	run2 := env.runProductionRun(t) // StartRun claims the pending deferred work
	env.server.assertBearerRequests(t)
	require.Equal(t, 1, run2.VideosAccepted, "the retry run must accept the retried video")

	// Exactly one external mutation per action target, in order: the deferred
	// action was never attempted in run 1 and the completed one was never
	// repeated in run 2.
	assert.Equal(t, []playlistInsertion{
		{PlaylistID: "PLjazzA", VideoID: "V1"},
		{PlaylistID: "PLjazzB", VideoID: "V1"},
	}, env.server.Insertions())

	api, _ := reopenAndServe(t, env)

	origin, originBody := getRunSummary(t, api, run1.ID)
	assert.Equal(t, string(state.RunStatusCompleted), origin.Status)
	require.NotNil(t, origin.VideosSelected)
	assert.Equal(t, 1, *origin.VideosSelected)
	assert.Equal(t, 1, origin.VideosEvaluated)
	assert.Equal(t, 1, origin.VideosDiscovered)
	assert.Equal(t, 0, origin.VideosAccepted)
	assert.Equal(t, 1, origin.VideosDeferred)

	originDetail, originDetailBody := getRunVideoDetail(t, api, run1.ID, "V1")
	require.Len(t, originDetail.RouteDecisions, 1)
	originActions := actionsByTarget(t, originDetail.RouteDecisions[0].Actions)
	firstAttempt := originActions["PLjazzA"]
	assert.Equal(t, "succeeded", firstAttempt.Outcome)
	require.NotNil(t, firstAttempt.ExecutedAt, "a real attempt records its time")
	assert.Nil(t, firstAttempt.OutcomeReason, "a plain success carries no reason")
	secondAttempt := originActions["PLjazzB"]
	assert.Equal(t, "deferred", secondAttempt.Outcome)
	require.NotNil(t, secondAttempt.OutcomeReason)
	assert.Equal(t, "quota_exhausted", *secondAttempt.OutcomeReason)
	assert.Nil(t, secondAttempt.ExecutedAt, "a quota deferral is not an external attempt")

	retry, retryBody := getRunSummary(t, api, run2.ID)
	assert.Equal(t, string(state.RunStatusCompleted), retry.Status)
	require.NotNil(t, retry.VideosSelected,
		"the claimed retry group is the selector's established input set")
	assert.Equal(t, 1, *retry.VideosSelected)
	assert.Equal(t, 1, retry.VideosEvaluated)
	assert.Equal(t, 0, retry.VideosDiscovered,
		"the retry discovers no new videos; its input is the claimed retry work")
	assert.Equal(t, 1, retry.VideosAccepted)
	assert.Equal(t, 0, retry.VideosDeferred)

	retryDetail, retryDetailBody := getRunVideoDetail(t, api, run2.ID, "V1")
	require.Len(t, retryDetail.RouteDecisions, 1)
	retryActions := actionsByTarget(t, retryDetail.RouteDecisions[0].Actions)
	suppressed := retryActions["PLjazzA"]
	assert.Equal(t, "not_attempted", suppressed.Outcome,
		"a prior exact-target completion is suppressed, not re-succeeded")
	require.NotNil(t, suppressed.OutcomeReason)
	assert.Equal(t, "already_completed", *suppressed.OutcomeReason)
	assert.Nil(t, suppressed.ExecutedAt,
		"a suppressed no-attempt carries no attempt timestamp")
	completed := retryActions["PLjazzB"]
	assert.Equal(t, "succeeded", completed.Outcome)
	require.NotNil(t, completed.ExecutedAt, "the retried action is a real attempt")

	require.NotContains(t, string(originBody), diagSentinel)
	require.NotContains(t, string(originDetailBody), diagSentinel)
	require.NotContains(t, string(retryBody), diagSentinel)
	require.NotContains(t, string(retryDetailBody), diagSentinel)
}

// TestComposition_SimulationSelectorsThroughHTTP drives a real production
// routing failure that leaves never-evaluated inputs, then real dry-run and
// replay runs over them, and proves through the registered routes of the
// reopened database that simulation evidence — known selected counts,
// evaluated decisions, simulated not_attempted actions without attempt
// timestamps — is durable, run-scoped, and mutation-free (simulation
// isolation: no YouTube calls for dry-run, replay, or backtest).
func TestComposition_SimulationSelectorsThroughHTTP(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	env := setupDiagnosticCompositionEnv(t, compositionChannels(), []mockVideo{
		makeVideo("V1", "jazz one", now.Add(-2*time.Hour)),
		makeVideo("V2", "jazz two", now.Add(-time.Hour)),
	})
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	// A routing failure on the very first decision leaves both discovered
	// videos never evaluated — the real producer of pending dry-run input.
	run, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	armDecisionFailure(t, ctx, env.db, 0)
	env.pipeline.Execute(ctx, run.ID)
	// The simulations run against normal persistence; the production failure
	// above is the only injected boundary in this case.
	_, err = env.db.ExecContext(ctx, `DROP TRIGGER injected_decision_failure`)
	require.NoError(t, err)

	dry, err := env.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	env.pipeline.ExecuteDryRun(ctx, dry.ID)

	replay, err := env.pipeline.Replay(ctx, nil, state.PublishedRange{})
	require.NoError(t, err)
	env.pipeline.ExecuteReplay(ctx, replay.ID)

	// A backtest evaluates a proposed configuration (distinct simulated
	// target) over the same retained observations. The request baseline is
	// sampled around the whole entry path — run creation and execution — and
	// a backtest must reach the substituted provider zero times, not merely
	// insert nothing.
	requestsBeforeBacktest := env.server.Requests()
	assert.Greater(t, requestsBeforeBacktest, 0,
		"the request counter must be live; production discovery already used the provider")
	backtest, err := env.pipeline.Backtest(ctx, &config.ProposalSnapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes: []config.ProposalRoute{{
			ID:                   "9f4d3f2c-1c3a-4d9e-9e1d-7b6f5e4d3c2b",
			Name:                 "Jazz Simulated",
			EvaluationOrder:      1,
			IsEnabled:            true,
			ConditionComposition: "OR",
			Conditions: []config.SnapshotCondition{
				{ConditionIndex: 0, Field: "title", Operator: "contains", Value: "jazz"},
			},
			Actions: []config.SnapshotAction{
				{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PLbacktest"},
			},
		}},
	}, state.PublishedRange{})
	require.NoError(t, err)
	env.pipeline.ExecuteBacktest(ctx, backtest.ID)
	assert.Equal(t, requestsBeforeBacktest, env.server.Requests(),
		"a backtest must make zero calls to the substituted YouTube provider")

	assert.Empty(t, env.server.Insertions(),
		"simulation isolation: dry-run, replay, and backtest must never mutate playlists")
	env.server.assertBearerRequests(t)

	api, _ := reopenAndServe(t, env)

	// The dry-run's pending selector picked up exactly the never-evaluated
	// input of the failed production run.
	drySummary, dryBody := getRunSummary(t, api, dry.ID)
	assert.Equal(t, string(state.RunStatusCompleted), drySummary.Status)
	assert.Equal(t, string(state.RunTypeDryRun), drySummary.RunType)
	require.NotNil(t, drySummary.VideosSelected)
	assert.Equal(t, 1, *drySummary.VideosSelected,
		"the pending selector's input is the never-evaluated video")
	assert.Equal(t, 1, drySummary.VideosEvaluated)
	assert.Equal(t, 1, drySummary.VideosDiscovered,
		"the dry run's own counters describe its own evaluated input, not production discoveries")

	dryVideos, dryTotal, dryListBody := getRunVideoList(t, api, dry.ID)
	assert.Equal(t, 1, dryTotal)
	require.Len(t, dryVideos, 1)
	require.Len(t, dryVideos[0].RouteDecisions, 1)
	assert.Equal(t, "matched", dryVideos[0].RouteDecisions[0].Decision)
	dryDetail, dryDetailBody := getRunVideoDetail(t, api, dry.ID, dryVideos[0].VideoID)
	require.Len(t, dryDetail.RouteDecisions, 1)
	require.Len(t, dryDetail.RouteDecisions[0].Actions, 1)
	simulated := dryDetail.RouteDecisions[0].Actions[0]
	assert.Equal(t, "not_attempted", simulated.Outcome,
		"a simulated action is never an attempt")
	assert.Nil(t, simulated.ExecutedAt)
	assert.Nil(t, simulated.OutcomeReason,
		"simulation is not an idempotency suppression")

	// The replay's retained selector re-evaluates every retained observation.
	replaySummary, replayBody := getRunSummary(t, api, replay.ID)
	assert.Equal(t, string(state.RunStatusCompleted), replaySummary.Status)
	assert.Equal(t, string(state.RunTypeReplay), replaySummary.RunType)
	require.NotNil(t, replaySummary.VideosSelected)
	assert.Equal(t, 2, *replaySummary.VideosSelected)
	assert.Equal(t, 2, replaySummary.VideosEvaluated)
	assert.Equal(t, 2, replaySummary.VideosDiscovered)
	replayVideos, replayTotal, replayListBody := getRunVideoList(t, api, replay.ID)
	assert.Equal(t, 2, replayTotal)
	require.Len(t, replayVideos, 2)
	for _, v := range replayVideos {
		require.Len(t, v.RouteDecisions, 1)
		assert.Equal(t, "matched", v.RouteDecisions[0].Decision)
	}

	// The backtest's retained selector re-evaluates every retained
	// observation against its proposed configuration, and its evidence is
	// readable through the registered routes like every other run.
	btSummary, btBody := getRunSummary(t, api, backtest.ID)
	assert.Equal(t, string(state.RunStatusCompleted), btSummary.Status)
	assert.Equal(t, string(state.RunTypeBacktest), btSummary.RunType)
	require.NotNil(t, btSummary.VideosSelected)
	assert.Equal(t, 2, *btSummary.VideosSelected)
	assert.Equal(t, 2, btSummary.VideosEvaluated)
	assert.Equal(t, 2, btSummary.VideosDiscovered)
	btVideos, btTotal, btListBody := getRunVideoList(t, api, backtest.ID)
	assert.Equal(t, 2, btTotal)
	require.Len(t, btVideos, 2)
	for _, v := range btVideos {
		require.Len(t, v.RouteDecisions, 1)
		assert.Equal(t, "matched", v.RouteDecisions[0].Decision)
	}
	btDetail, btDetailBody := getRunVideoDetail(t, api, backtest.ID, btVideos[0].VideoID)
	require.Len(t, btDetail.RouteDecisions, 1)
	require.Len(t, btDetail.RouteDecisions[0].Actions, 1)
	btAction := btDetail.RouteDecisions[0].Actions[0]
	assert.Equal(t, "PLbacktest", btAction.Target,
		"the action evidence must carry the proposed configuration's target, not the active route's")
	assert.Equal(t, "not_attempted", btAction.Outcome,
		"a backtest action is simulated, never an attempt")
	assert.Nil(t, btAction.ExecutedAt)
	assert.Nil(t, btAction.OutcomeReason)

	// The backtest run is also discoverable through the registered run list.
	status, allRunsBody := getJSON(t, guardClient(t, api.Client()), api.URL+"/api/runs")
	require.Equal(t, http.StatusOK, status)
	var runsList struct {
		Runs []struct {
			ID      int64  `json:"id"`
			RunType string `json:"run_type"`
		} `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(allRunsBody, &runsList))
	var foundBacktest bool
	for _, entry := range runsList.Runs {
		if entry.ID == backtest.ID {
			foundBacktest = true
			assert.Equal(t, string(state.RunTypeBacktest), entry.RunType)
		}
	}
	require.True(t, foundBacktest, "the backtest run must appear in the registered run list")

	// The production failure evidence stays separately readable and unchanged.
	prodSummary, prodBody := getRunSummary(t, api, run.ID)
	assert.Equal(t, string(state.RunStatusFailed), prodSummary.Status)
	require.NotNil(t, prodSummary.VideosSelected)
	assert.Equal(t, 2, *prodSummary.VideosSelected)
	assert.Equal(t, 0, prodSummary.VideosEvaluated)
	assert.Equal(t, 2, prodSummary.VideosDiscovered)

	require.NotContains(t, string(dryBody), diagSentinel)
	require.NotContains(t, string(dryListBody), diagSentinel)
	require.NotContains(t, string(dryDetailBody), diagSentinel)
	require.NotContains(t, string(replayBody), diagSentinel)
	require.NotContains(t, string(replayListBody), diagSentinel)
	require.NotContains(t, string(btBody), diagSentinel)
	require.NotContains(t, string(btListBody), diagSentinel)
	require.NotContains(t, string(btDetailBody), diagSentinel)
	require.NotContains(t, string(allRunsBody), diagSentinel)
	require.NotContains(t, string(prodBody), diagSentinel)
}

// TestComposition_UnknownSelectionAndSafeSentinelThroughHTTP composes two real
// provider failures through the assembled product: a discovery outage before
// any selector runs (selection stays unknown, not zero) and a playlist-insert
// failure at attempt time (failed action evidence with an attempt timestamp,
// the run still completed per ADR-006).
// It proves both read back through the registered routes of the reopened
// database and that the raw provider error payload never crosses the
// diagnostic boundary while safe fields remain visible (ARD-001).
func TestComposition_UnknownSelectionAndSafeSentinelThroughHTTP(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	env := setupDiagnosticCompositionEnv(t, compositionChannels(), []mockVideo{
		makeVideo("V1", "jazz one", now.Add(-time.Hour)),
	})
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	// Provider outage during discovery: the run fails before its selector
	// establishes any input set, so the selected count must stay unknown.
	env.server.FailSubscriptions(http.StatusInternalServerError, diagSentinelBody)
	unknownRun, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	env.pipeline.Execute(ctx, unknownRun.ID)

	// Recovery: discovery succeeds, but the playlist insert for V1 fails with
	// a raw payload body — a real attempted action failure at the seam.
	env.server.ClearFailures()
	env.server.FailPlaylistInsert("PLjazz", http.StatusInternalServerError, diagSentinelBody)
	failedRun, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	env.pipeline.Execute(ctx, failedRun.ID)
	env.server.assertBearerRequests(t)

	api, db := reopenAndServe(t, env)

	unknown, unknownBody := getRunSummary(t, api, unknownRun.ID)
	assert.Equal(t, string(state.RunStatusFailed), unknown.Status)
	assert.Nil(t, unknown.VideosSelected,
		"a failure before the selector ran must keep the count unknown, not zero")
	assert.Equal(t, 0, unknown.VideosDiscovered)
	assert.Equal(t, 0, unknown.VideosEvaluated)
	require.NotNil(t, unknown.InterruptReason, "a failed run exposes its safe reason")
	assert.Contains(t, *unknown.InterruptReason, "backendError",
		"the vetted provider reason remains visible")
	assert.False(t, unknown.FailureReasonUnavail)

	provFailed, provFailedBody := getRunSummary(t, api, failedRun.ID)
	// ADR-006 §4: a per-video failure inside a completed action phase is a
	// video outcome, not a run failure. The counters and the video report it.
	assert.Equal(t, string(state.RunStatusCompleted), provFailed.Status,
		"an attempted action failure on one video does not fail the run")
	require.NotNil(t, provFailed.VideosSelected)
	assert.Equal(t, 1, *provFailed.VideosSelected)
	assert.Equal(t, 1, provFailed.VideosEvaluated)
	assert.Equal(t, 1, provFailed.VideosDiscovered)
	assert.Equal(t, 1, provFailed.VideosFailed)
	assert.Nil(t, provFailed.InterruptReason, "a completed run carries no failure reason")

	videos, total, listBody := getRunVideoList(t, api, failedRun.ID)
	assert.Equal(t, 1, total)
	require.Len(t, videos, 1)
	detail, detailBody := getRunVideoDetail(t, api, failedRun.ID, "V1")
	require.Len(t, detail.RouteDecisions, 1)
	actions := detail.RouteDecisions[0].Actions
	require.Len(t, actions, 1)
	assert.Equal(t, "failed", actions[0].Outcome)
	require.NotNil(t, actions[0].ExecutedAt,
		"a provider failure is a real external attempt")
	require.NotNil(t, actions[0].OutcomeReason)
	assert.Equal(t, "mutation_application_uncertain", *actions[0].OutcomeReason)
	require.NotNil(t, detail.FinalOutcome)
	assert.Equal(t, string(state.FinalOutcomeFailed), *detail.FinalOutcome)
	require.NotNil(t, detail.ErrorMessage)
	assert.Contains(t, *detail.ErrorMessage, "backendError",
		"the safe bounded provider reason remains visible on the video")

	// The raw provider payload is absent from every API readback and from the
	// durable queue detail, while safe fields remain.
	status, runsListBody := getJSON(t, guardClient(t, api.Client()), api.URL+"/api/runs")
	require.Equal(t, http.StatusOK, status)
	for name, body := range map[string][]byte{
		"run list":     runsListBody,
		"unknown run":  unknownBody,
		"failed run":   provFailedBody,
		"video list":   listBody,
		"video detail": detailBody,
	} {
		require.NotContains(t, string(body), diagSentinel,
			"%s must never expose the raw provider payload", name)
	}
	var queueDetail string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(error_detail), '') FROM error_entry`).Scan(&queueDetail))
	require.NotContains(t, queueDetail, diagSentinel,
		"the durable queue detail must hold only the vetted summary")
	assert.Contains(t, queueDetail, "backendError",
		"the vetted summary stays available in the queue")
}

// TestComposition_ZeroSelectionThroughHTTP proves a selector that genuinely
// completed with no inputs stores and serves a known zero — distinct from the
// unknown null — through the registered routes of the reopened database, for
// all three real selectors (production discovery, dry-run pending, replay
// retained).
func TestComposition_ZeroSelectionThroughHTTP(t *testing.T) {
	ctx := context.Background()
	env := setupDiagnosticCompositionEnv(t, compositionChannels(), nil) // the provider serves no videos
	env.createRoute(t, "Jazz", "jazz", "PLjazz")

	prod, err := env.pipeline.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	env.pipeline.Execute(ctx, prod.ID)

	dry, err := env.pipeline.DryRun(ctx, nil)
	require.NoError(t, err)
	env.pipeline.ExecuteDryRun(ctx, dry.ID)

	replay, err := env.pipeline.Replay(ctx, nil, state.PublishedRange{})
	require.NoError(t, err)
	env.pipeline.ExecuteReplay(ctx, replay.ID)
	env.server.assertBearerRequests(t)

	api, _ := reopenAndServe(t, env)

	for _, tc := range []struct {
		name    string
		runID   int64
		runType state.RunType
	}{
		{"production discovery", prod.ID, state.RunTypeManual},
		{"dry-run pending", dry.ID, state.RunTypeDryRun},
		{"replay retained", replay.ID, state.RunTypeReplay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary, _ := getRunSummary(t, api, tc.runID)
			assert.Equal(t, string(state.RunStatusCompleted), summary.Status)
			assert.Equal(t, string(tc.runType), summary.RunType)
			require.NotNil(t, summary.VideosSelected,
				"an empty selection is a known zero, never unknown")
			assert.Equal(t, 0, *summary.VideosSelected)
			assert.Equal(t, 0, summary.VideosEvaluated)
			assert.Equal(t, 0, summary.VideosDiscovered)
		})
	}
}
