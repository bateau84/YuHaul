package pipeline

// Tests for the ADR-012 proposal-only route identity anchors (task B7): dry-run
// drafts and backtest proposals whose route UUIDs are translated to internal
// integer keys in the same SQLite transaction as any identity anchor, the
// immutable ConfigVersion snapshot, and the run row.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

const proposalTestUUID = "2f6b9a7c-5d8e-4c3b-a1f0-9e8d7c6b5a49"

func TestProposalIngest_CapturesFixedContinuation(t *testing.T) {
	for _, mode := range []string{"dry-run", "backtest"} {
		for _, legacy := range []string{"skip", "flag", "", "arbitrary"} {
			t.Run(mode+"/"+legacy, func(t *testing.T) {
				ctx := context.Background()
				deps, cleanup := setupDryRunPipeline(t)
				defer cleanup()
				proposal := proposalSnapshot(proposalRoute(proposalTestUUID, "proposed", "jazz"))
				proposal.DuplicateBehavior = legacy
				var run *state.Run
				var err error
				if mode == "dry-run" {
					run, err = deps.pipeline.DryRun(ctx, proposal)
				} else {
					run, err = deps.pipeline.Backtest(ctx, proposal)
				}
				require.NoError(t, err)
				cv, err := deps.pipeline.configStore.Get(ctx, *run.ConfigVersionID)
				require.NoError(t, err)
				require.Equal(t, "continue", cv.Snapshot.DuplicateBehavior)
				require.Equal(t, proposal.DuplicateThreshold, cv.Snapshot.DuplicateThreshold)
				require.Equal(t, legacy, proposal.DuplicateBehavior, "capture does not rewrite the caller's input")
			})
		}
	}
}

func proposalRoute(id, name, value string) config.ProposalRoute {
	return config.ProposalRoute{
		ID:                   id,
		Name:                 name,
		EvaluationOrder:      1,
		IsFinal:              true,
		IsEnabled:            true,
		ConditionComposition: "AND",
		Conditions: []config.SnapshotCondition{
			{ConditionIndex: 0, Field: "title", Operator: "contains", Value: value},
		},
		Actions: []config.SnapshotAction{
			{ActionIndex: 0, ActionType: "playlist_insert", TargetPlaylistID: "PL" + name},
		},
	}
}

func proposalSnapshot(routes ...config.ProposalRoute) *config.ProposalSnapshot {
	return &config.ProposalSnapshot{
		DuplicateThreshold: 0.5,
		DuplicateBehavior:  "skip",
		Routes:             routes,
	}
}

// identityFor returns the internal route key mapped to publicID, or fails.
func identityFor(t *testing.T, ctx context.Context, deps *dryRunDeps, publicID string) int64 {
	t.Helper()
	var key int64
	err := deps.db.QueryRowContext(ctx,
		`SELECT route_id FROM route_identity WHERE public_id = ?;`, publicID).Scan(&key)
	require.NoError(t, err, "expected a registry mapping for %s", publicID)
	return key
}

func countRows(t *testing.T, ctx context.Context, deps *dryRunDeps, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, deps.db.QueryRowContext(ctx, query, args...).Scan(&n))
	return n
}

// A valid UUID absent from the registry gets one internal key, one registry
// mapping, and one soft-deleted inactive anchor row; the stored snapshot
// carries the internal integer key and the proposal's full route definition.
func TestProposalIngest_UnknownUUIDCreatesAnchor(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	route := proposalRoute(proposalTestUUID, "Proposed", "jazz")
	route.Description = "proposal only"
	run, err := deps.pipeline.DryRun(ctx, proposalSnapshot(route))
	require.NoError(t, err)
	require.NotNil(t, run.ConfigVersionID)

	key := identityFor(t, ctx, deps, proposalTestUUID)
	assert.Equal(t, 1, countRows(t, ctx, deps,
		`SELECT COUNT(*) FROM route_identity WHERE public_id = ?;`, proposalTestUUID),
		"the supplied UUID must be registered exactly once")

	// ADR-012 §4: the anchor is soft-deleted, disabled, ordered at the -id
	// sentinel, and copies the proposal's identity-row metadata.
	var name, description, composition string
	var isFinal, isEnabled bool
	var evaluationOrder int
	var createdAt, updatedAt, deletedAt string
	err = deps.db.QueryRowContext(ctx,
		`SELECT name, description, evaluation_order, is_final, is_enabled,
		        condition_composition, created_at, updated_at, deleted_at
		 FROM route WHERE id = ?;`, key).Scan(
		&name, &description, &evaluationOrder, &isFinal, &isEnabled,
		&composition, &createdAt, &updatedAt, &deletedAt)
	require.NoError(t, err, "the anchor route row must exist at the mapped key")
	assert.Equal(t, "Proposed", name)
	assert.Equal(t, "proposal only", description)
	assert.Equal(t, "AND", composition)
	assert.True(t, isFinal)
	assert.False(t, isEnabled, "an anchor is never enabled")
	assert.Equal(t, -key, int64(evaluationOrder), "ADR-003 inactive sentinel evaluation_order = -id")
	assert.NotEmpty(t, deletedAt, "the anchor must be committed soft-deleted")
	assert.NotEmpty(t, createdAt)
	assert.NotEmpty(t, updatedAt)

	// No route child rows: the evaluated definition lives only in the
	// run's immutable ConfigVersion snapshot.
	assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_condition WHERE route_id = ?;`, key))
	assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_action WHERE route_id = ?;`, key))
	assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_exclusion WHERE route_id = ?;`, key))

	// The anchor is not active configuration.
	active, err := deps.routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	assert.Empty(t, active, "a proposal anchor must not appear in the active route list")

	// The stored snapshot keeps the internal integer key and the proposal's
	// full evaluated route definition.
	cv, err := deps.pipeline.configStore.Get(ctx, *run.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cv.Snapshot.Routes, 1)
	stored := cv.Snapshot.Routes[0]
	assert.Equal(t, key, stored.ID, "stored snapshot route IDs remain internal integers")
	assert.Equal(t, "Proposed", stored.Name)
	assert.Equal(t, "jazz", stored.Conditions[0].Value)
	assert.Equal(t, "PLProposed", stored.Actions[0].TargetPlaylistID)

	// The run row committed with its snapshot link.
	loaded, err := deps.stateStore.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.ConfigVersionID)
	assert.Equal(t, *run.ConfigVersionID, *loaded.ConfigVersionID)
}

// A proposal UUID already in the registry reuses its mapping and leaves the
// existing route row completely unchanged, even when the proposal carries a
// different configuration.
func TestProposalIngest_ExistingUUIDReusedUnchanged(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	active := createRouteWithAction(t, ctx, deps, "Rock", "rock", "PLrock")

	var updatedBefore string
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT updated_at FROM route WHERE id = ?;`, active.ID).Scan(&updatedBefore))

	identitiesBefore := countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_identity;`)

	// The proposal reuses the active route's UUID with a different name and
	// condition: identity and the active row must not change.
	proposal := proposalRoute(active.PublicID, "JazzProposed", "jazz")
	run, err := deps.pipeline.DryRun(ctx, proposalSnapshot(proposal))
	require.NoError(t, err)

	assert.Equal(t, active.ID, identityFor(t, ctx, deps, active.PublicID),
		"the existing mapping must be reused at its existing key")
	assert.Equal(t, identitiesBefore, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_identity;`),
		"no new mapping may be created for a registered UUID")

	var name string
	var deletedAt any
	var updatedAfter string
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT name, deleted_at, updated_at FROM route WHERE id = ?;`, active.ID).Scan(
		&name, &deletedAt, &updatedAfter))
	assert.Equal(t, "Rock", name, "the active route row must not be renamed by the proposal")
	assert.Nil(t, deletedAt, "the active route row must not be soft-deleted by the proposal")
	assert.Equal(t, updatedBefore, updatedAfter, "the active route row must not be touched")

	cv, err := deps.pipeline.configStore.Get(ctx, *run.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cv.Snapshot.Routes, 1)
	assert.Equal(t, active.ID, cv.Snapshot.Routes[0].ID)
	assert.Equal(t, "JazzProposed", cv.Snapshot.Routes[0].Name,
		"the snapshot keeps the configuration this proposal evaluated")
}

// Repeated use of one previously unknown UUID converges on one mapping and one
// anchor across a dry-run and a backtest (ADR-012 §5).
func TestProposalIngest_RepeatedUnknownUUIDConverges(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	dryRun, err := deps.pipeline.DryRun(ctx, proposalSnapshot(
		proposalRoute(proposalTestUUID, "ProposedA", "jazz")))
	require.NoError(t, err)

	backtest, err := deps.pipeline.Backtest(ctx, proposalSnapshot(
		proposalRoute(proposalTestUUID, "ProposedB", "classical")))
	require.NoError(t, err)

	dryKey := identityFor(t, ctx, deps, proposalTestUUID)
	assert.Equal(t, 1, countRows(t, ctx, deps,
		`SELECT COUNT(*) FROM route_identity WHERE public_id = ?;`, proposalTestUUID),
		"repeated use must not create a second mapping")

	cvA, err := deps.pipeline.configStore.Get(ctx, *dryRun.ConfigVersionID)
	require.NoError(t, err)
	cvB, err := deps.pipeline.configStore.Get(ctx, *backtest.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cvA.Snapshot.Routes, 1)
	require.Len(t, cvB.Snapshot.Routes, 1)
	assert.Equal(t, dryKey, cvA.Snapshot.Routes[0].ID)
	assert.Equal(t, dryKey, cvB.Snapshot.Routes[0].ID,
		"the second proposal reuses the same internal key")
	assert.Equal(t, "ProposedA", cvA.Snapshot.Routes[0].Name)
	assert.Equal(t, "ProposedB", cvB.Snapshot.Routes[0].Name,
		"each run's snapshot keeps its own evaluated configuration")

	// Still exactly one anchor row for that identity.
	assert.Equal(t, 1, countRows(t, ctx, deps,
		`SELECT COUNT(*) FROM route WHERE id = ?;`, dryKey))
}

// A mapped identity whose route row no longer exists (snapshot-only history
// from an old hard delete) gets an anchor recreated at exactly its existing
// internal key (ADR-012 §3).
func TestProposalIngest_SnapshotOnlyIdentityRecreatesAnchorAtSameKey(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	const snapshotOnlyUUID = "6e5d4c3b-2a19-4f8e-8d7c-6b5a49382716"
	const snapshotOnlyKey = int64(42)
	_, err := deps.db.ExecContext(ctx,
		`INSERT INTO route_identity (route_id, public_id) VALUES (?, ?);`,
		snapshotOnlyKey, snapshotOnlyUUID)
	require.NoError(t, err)

	run, err := deps.pipeline.Backtest(ctx, proposalSnapshot(
		proposalRoute(snapshotOnlyUUID, "HistoricalOnly", "jazz")))
	require.NoError(t, err)

	assert.Equal(t, snapshotOnlyKey, identityFor(t, ctx, deps, snapshotOnlyUUID),
		"the existing key must be reused")
	assert.Equal(t, 1, countRows(t, ctx, deps,
		`SELECT COUNT(*) FROM route_identity WHERE route_id = ?;`, snapshotOnlyKey),
		"no second mapping may be created")

	var deletedAt any
	var evaluationOrder int
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT deleted_at, evaluation_order FROM route WHERE id = ?;`, snapshotOnlyKey).Scan(
		&deletedAt, &evaluationOrder))
	assert.NotNil(t, deletedAt, "the recreated anchor is soft-deleted")
	assert.Equal(t, -snapshotOnlyKey, int64(evaluationOrder))

	cv, err := deps.pipeline.configStore.Get(ctx, *run.ConfigVersionID)
	require.NoError(t, err)
	assert.Equal(t, snapshotOnlyKey, cv.Snapshot.Routes[0].ID)
}

// Duplicate display names remain independent identities: each proposal route
// keeps its own UUID, key, anchor, and snapshot entry (ADR-011/ADR-012).
func TestProposalIngest_DuplicateNamesStayIndependent(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	const uuidA = "aaaaaaaa-1111-4aaa-8bbb-000000000001"
	const uuidB = "bbbbbbbb-2222-4bbb-8ccc-000000000002"

	run, err := deps.pipeline.DryRun(ctx, proposalSnapshot(
		proposalRoute(uuidA, "Music", "jazz"),
		proposalRoute(uuidB, "Music", "rock")))
	require.NoError(t, err)

	keyA := identityFor(t, ctx, deps, uuidA)
	keyB := identityFor(t, ctx, deps, uuidB)
	assert.NotEqual(t, keyA, keyB, "same-named proposal routes must not merge identity")

	cv, err := deps.pipeline.configStore.Get(ctx, *run.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cv.Snapshot.Routes, 2)
	assert.Equal(t, keyA, cv.Snapshot.Routes[0].ID)
	assert.Equal(t, keyB, cv.Snapshot.Routes[1].ID)
	assert.Equal(t, "Music", cv.Snapshot.Routes[0].Name)
	assert.Equal(t, "Music", cv.Snapshot.Routes[1].Name)
}

// A failed proposal-ingest transaction leaves no partial mapping, anchor,
// ConfigVersion, or run (ADR-012 §5).
func TestProposalIngest_RollbackLeavesNoPartialRecords(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	configsBefore := countRows(t, ctx, deps, `SELECT COUNT(*) FROM config_version;`)
	runsBefore := countRows(t, ctx, deps, `SELECT COUNT(*) FROM run;`)
	routesBefore := countRows(t, ctx, deps, `SELECT COUNT(*) FROM route;`)

	// Inject a genuine persistence failure at the run insert, the last write
	// of the ingest transaction, so anchor/mapping/snapshot inserts must roll
	// back with it.
	_, err := deps.db.ExecContext(ctx,
		`CREATE TRIGGER fail_run_insert BEFORE INSERT ON run
		 BEGIN SELECT RAISE(ABORT, 'injected run insert failure'); END;`)
	require.NoError(t, err)

	_, err = deps.pipeline.DryRun(ctx, proposalSnapshot(
		proposalRoute(proposalTestUUID, "RolledBack", "jazz")))
	require.Error(t, err)

	var mapped int
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM route_identity WHERE public_id = ?;`, proposalTestUUID).Scan(&mapped))
	assert.Zero(t, mapped, "a failed transaction must not commit a mapping")
	assert.Equal(t, routesBefore, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route;`),
		"a failed transaction must not commit an anchor")
	assert.Equal(t, configsBefore, countRows(t, ctx, deps, `SELECT COUNT(*) FROM config_version;`),
		"a failed transaction must not commit a snapshot")
	assert.Equal(t, runsBefore, countRows(t, ctx, deps, `SELECT COUNT(*) FROM run;`),
		"a failed transaction must not commit a run")

	// After the failing trigger is removed the same UUID ingests normally.
	_, err = deps.db.ExecContext(ctx, `DROP TRIGGER fail_run_insert;`)
	require.NoError(t, err)
	_, err = deps.pipeline.DryRun(ctx, proposalSnapshot(
		proposalRoute(proposalTestUUID, "RolledBack", "jazz")))
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, ctx, deps,
		`SELECT COUNT(*) FROM route_identity WHERE public_id = ?;`, proposalTestUUID))
}

// Proposal evaluation never mutates the active route configuration: the
// active route list, the active ConfigVersion, and the route-mutation
// snapshot callback are all untouched (ADR-012 §3/§7, BR-026/BR-027
// isolation).
func TestProposalIngest_DoesNotMutateActiveConfiguration(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	active := createRouteWithAction(t, ctx, deps, "Rock", "rock", "PLrock")

	snapshotCalls := 0
	deps.routerStore.WithOnChange(func(context.Context, string) error {
		snapshotCalls++
		return nil
	})

	activeRoutesBefore, err := deps.routerStore.ListRoutes(ctx)
	require.NoError(t, err)

	// Materialize the initial active configuration snapshot (the versioner
	// creates it lazily when no history exists yet).
	_, err = deps.pipeline.versioner.ActiveVersionID(ctx)
	require.NoError(t, err)

	configsBefore := map[int64]string{}
	rows, err := deps.db.QueryContext(ctx, `SELECT id, snapshot FROM config_version;`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		var snapshotJSON string
		require.NoError(t, rows.Scan(&id, &snapshotJSON))
		configsBefore[id] = snapshotJSON
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, configsBefore, "route creation must have snapshotted the active configuration")
	require.NoError(t, rows.Close())

	proposal := proposalRoute(active.PublicID, "RenamedInProposal", "jazz")
	proposal.EvaluationOrder = 1
	_, err = deps.pipeline.DryRun(ctx, proposalSnapshot(proposal,
		proposalRoute(proposalTestUUID, "BrandNew", "classical")))
	require.NoError(t, err)

	activeRoutesAfter, err := deps.routerStore.ListRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, activeRoutesBefore, 1)
	require.Len(t, activeRoutesAfter, 1, "anchors must not become active routes")
	assert.Equal(t, activeRoutesBefore[0].Name, activeRoutesAfter[0].Name,
		"the proposal must not rename the active route")
	assert.Equal(t, activeRoutesBefore[0].UpdatedAt, activeRoutesAfter[0].UpdatedAt,
		"the proposal must not touch the active route row")

	// Every pre-existing ConfigVersion row is byte-for-byte unchanged. The
	// proposal adds exactly its own immutable snapshot row (ADR-012 §4);
	// it never rewrites or replaces stored configuration.
	configsAfter := map[int64]string{}
	rows, err = deps.db.QueryContext(ctx, `SELECT id, snapshot FROM config_version;`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		var snapshotJSON string
		require.NoError(t, rows.Scan(&id, &snapshotJSON))
		configsAfter[id] = snapshotJSON
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	assert.Len(t, configsAfter, len(configsBefore)+1,
		"the proposal stores exactly one snapshot of its own")
	for id, snapshotJSON := range configsBefore {
		assert.Equal(t, snapshotJSON, configsAfter[id],
			"no stored ConfigVersion snapshot may be mutated by the proposal")
	}
	assert.Zero(t, snapshotCalls,
		"the anchor must not invoke the route-mutation ConfigVersion callback")
}

// Decisions produced by a proposal run satisfy route_decision.route_id →
// route.id through the anchor (ADR-012 §6) and public projection resolves
// the original UUID.
func TestProposalIngest_DecisionsSatisfyForeignKey(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)
	video := createPendingVideo(t, ctx, deps, prodRunID, "vid_jazz", "ch1", "cool jazz live")

	run, err := deps.pipeline.Backtest(ctx, proposalSnapshot(
		proposalRoute(proposalTestUUID, "Proposed", "jazz")))
	require.NoError(t, err)
	deps.pipeline.ExecuteBacktest(ctx, run.ID)

	key := identityFor(t, ctx, deps, proposalTestUUID)
	decisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, run.ID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, decisions[0].Decision)
	assert.Equal(t, key, decisions[0].RouteID,
		"route decisions store the mapped internal integer key")

	// Foreign-key integrity: every decision route key resolves to a route
	// row and its registry mapping.
	var fkViolations int
	require.NoError(t, deps.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check;`).Scan(&fkViolations))
	assert.Zero(t, fkViolations, "no foreign-key violation may exist after proposal evaluation")

	var joined int
	require.NoError(t, deps.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		 FROM route_decision rd
		 JOIN route r ON r.id = rd.route_id
		 JOIN route_identity ri ON ri.route_id = rd.route_id
		 WHERE rd.run_id = ?;`, run.ID).Scan(&joined))
	assert.Equal(t, 1, joined, "each decision must resolve through route and route_identity")

	// Public projection returns the original UUID.
	identities, err := deps.routerStore.ListRouteIdentities(ctx)
	require.NoError(t, err)
	assert.Equal(t, proposalTestUUID, identities[key])
}

// Dry-run and backtest proposals are isolated from each other and from
// production evaluation: each stores its own snapshot, anchors never enter the
// active route set, and production evaluation is unaffected.
func TestProposalIngest_DryRunBacktestAndProductionIsolation(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	active := createRouteWithAction(t, ctx, deps, "Rock", "rock", "PLrock")

	prodRun := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prodRunID, err := deps.stateStore.CreateRun(ctx, prodRun)
	require.NoError(t, err)
	video := createPendingVideo(t, ctx, deps, prodRunID, "vid_jazz", "ch1", "cool jazz live")

	// The dry-run proposal matches the pending video; the backtest proposal
	// (different UUID) does not.
	dryRun, err := deps.pipeline.DryRun(ctx, proposalSnapshot(
		proposalRoute(proposalTestUUID, "DryProposed", "jazz")))
	require.NoError(t, err)
	deps.pipeline.ExecuteDryRun(ctx, dryRun.ID)

	const backtestUUID = "7c6b5a49-3827-4f1e-9a8b-0c1d2e3f4a5b"
	backtest, err := deps.pipeline.Backtest(ctx, proposalSnapshot(
		proposalRoute(backtestUUID, "BackProposed", "classical")))
	require.NoError(t, err)
	deps.pipeline.ExecuteBacktest(ctx, backtest.ID)

	dryDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, dryRun.ID)
	require.NoError(t, err)
	require.Len(t, dryDecisions, 1)
	assert.Equal(t, state.RouteDecisionMatched, dryDecisions[0].Decision)

	btDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, backtest.ID)
	require.NoError(t, err)
	require.Len(t, btDecisions, 1)
	assert.Equal(t, state.RouteDecisionNotMatched, btDecisions[0].Decision,
		"the backtest proposal evaluates its own snapshot, not the dry-run's")

	// Both identities are anchored and mapped on top of the active route's
	// own identity, the active set is unchanged, and a production run still
	// evaluates only the active route.
	assert.Equal(t, 3, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_identity;`))
	activeRoutes, err := deps.routerStore.LoadActiveRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, activeRoutes, 1)
	assert.Equal(t, active.ID, activeRoutes[0].ID)

	prod := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	prod2ID, err := deps.stateStore.CreateRun(ctx, prod)
	require.NoError(t, err)
	prodVideo := createPendingVideo(t, ctx, deps, prod2ID, "vid_prod", "ch1", "cool jazz live")
	_, err = deps.pipeline.router.EvaluateWithRoutes(ctx, prod2ID, prodVideo.ID,
		router.VideoContext{VideoID: "vid_prod", ChannelID: "ch1", Title: "cool jazz live"},
		deps.machine, activeRoutes)
	require.NoError(t, err)
	prodDecisions, err := deps.stateStore.GetRouteDecisionsForVideoInRun(ctx, prodVideo.ID, prod2ID)
	require.NoError(t, err)
	for _, d := range prodDecisions {
		assert.NotEqual(t, identityFor(t, ctx, deps, proposalTestUUID), d.RouteID,
			"a proposal anchor must never be evaluated by production routing")
	}
}

// The pipeline rejects malformed, non-v4, and duplicate proposal route UUIDs
// before any run starts, and persists nothing (ADR-012 §1).
func TestProposalIngest_RejectsInvalidRouteIDs(t *testing.T) {
	tests := []struct {
		name  string
		route config.ProposalRoute
	}{
		{name: "numeric alias", route: proposalRoute("17", "Bad", "jazz")},
		{name: "malformed", route: proposalRoute("not-a-uuid", "Bad", "jazz")},
		{name: "empty", route: proposalRoute("", "Bad", "jazz")},
		{name: "non-v4 version", route: proposalRoute("00000000-0000-1000-8000-000000000000", "Bad", "jazz")},
		{name: "non-RFC variant", route: proposalRoute("00000000-0000-4000-0000-000000000000", "Bad", "jazz")},
		{name: "bare hex", route: proposalRoute("2f6b9a7c5d8e4c3ba1f09e8d7c6b5a49", "Bad", "jazz")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			deps, cleanup := setupDryRunPipeline(t)
			defer cleanup()

			_, err := deps.pipeline.DryRun(ctx, proposalSnapshot(tt.route))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid route id")
			assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM run;`))
			assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM config_version;`))
			assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route;`))
			assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_identity;`))
		})
	}

	t.Run("duplicate uuid within one proposal", func(t *testing.T) {
		ctx := context.Background()
		deps, cleanup := setupDryRunPipeline(t)
		defer cleanup()

		_, err := deps.pipeline.Backtest(ctx, proposalSnapshot(
			proposalRoute(proposalTestUUID, "First", "jazz"),
			proposalRoute(proposalTestUUID, "Second", "rock")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate route id")
		assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM run;`))
		assert.Zero(t, countRows(t, ctx, deps, `SELECT COUNT(*) FROM route_identity;`))
	})

	t.Run("uppercase hex is accepted and normalized", func(t *testing.T) {
		ctx := context.Background()
		deps, cleanup := setupDryRunPipeline(t)
		defer cleanup()

		upper := "2F6B9A7C-5D8E-4C3B-A1F0-9E8D7C6B5A49"
		_, err := deps.pipeline.DryRun(ctx, proposalSnapshot(
			proposalRoute(upper, "Upper", "jazz")))
		require.NoError(t, err)
		identityFor(t, ctx, deps, proposalTestUUID)
	})
}

// Simultaneous proposals carrying the same previously unknown UUID converge
// on one durable mapping with interoperable results (ADR-012 §5).
func TestProposalIngest_ConcurrentUnknownUUIDConverges(t *testing.T) {
	ctx := context.Background()
	deps, cleanup := setupDryRunPipeline(t)
	defer cleanup()

	const workers = 2
	runs := make([]*state.Run, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runs[i], errs[i] = deps.pipeline.DryRun(ctx, proposalSnapshot(
				proposalRoute(proposalTestUUID, fmt.Sprintf("Concurrent%d", i), "jazz")))
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for i := 0; i < workers; i++ {
		if errs[i] == nil {
			succeeded++
			continue
		}
		// A loss may only be the identity race, never a guessed identity.
		assert.True(t, isProposalIdentityRace(errs[i]), "unexpected error: %v", errs[i])
	}
	require.Positive(t, succeeded, "at least one concurrent proposal must commit")

	key := identityFor(t, ctx, deps, proposalTestUUID)
	assert.Equal(t, 1, countRows(t, ctx, deps,
		`SELECT COUNT(*) FROM route_identity WHERE public_id = ?;`, proposalTestUUID),
		"concurrent proposals must converge on one mapping")
	assert.Equal(t, 1, countRows(t, ctx, deps,
		`SELECT COUNT(*) FROM route WHERE id = ?;`, key),
		"concurrent proposals must converge on one anchor")

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			continue
		}
		cv, err := deps.pipeline.configStore.Get(ctx, *runs[i].ConfigVersionID)
		require.NoError(t, err)
		require.Len(t, cv.Snapshot.Routes, 1)
		assert.Equal(t, key, cv.Snapshot.Routes[0].ID,
			"every committed proposal resolves to the shared internal key")
	}
}
