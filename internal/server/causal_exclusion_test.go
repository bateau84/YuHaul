package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestCausalExclusionProjection(t *testing.T) {
	index, version, cfg := 0, 1, int64(42)
	reason := state.SkipReasonExclusion
	d := state.RouteDecision{RouteID: 7, Decision: state.RouteDecisionSkipped, SkipReason: &reason, ExclusionIndex: &index, ExclusionSemanticsVersion: &version}
	e := &runRouteEvidence{routes: map[int64]config.SnapshotRoute{7: {Exclusions: []config.SnapshotExclusion{{ExclusionType: "word", ExclusionValue: "secret"}}}}}
	for _, tc := range []struct{ typ, value, field, comparison string }{
		{"channel", "x", "channel_id", "exact"}, {"video", "x", "video_id", "exact"},
		{"title", "x", "title", "trimmed_equal_fold"}, {"word", "x", "title", "lowercase_substring"},
		{"duration_min", "12", "duration_seconds", "strict_less_than"}, {"duration_max", "12", "duration_seconds", "strict_greater_than"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			e.routes[7].Exclusions[0] = config.SnapshotExclusion{ExclusionType: tc.typ, ExclusionValue: tc.value}
			got := projectCausalExclusion(d, &cfg, e)
			require.Equal(t, "available", got.Status)
			require.Equal(t, tc.field, *got.Field)
			require.Equal(t, tc.comparison, *got.Comparison)
			require.True(t, *got.Matched)
			require.Equal(t, tc.value, *got.ConfiguredValue)
			require.Equal(t, "available", got.ConfiguredValueStatus)
		})
	}
	for _, tc := range []struct {
		name     string
		decision state.RouteDecision
		evidence *runRouteEvidence
		status   string
	}{
		{"legacy", state.RouteDecision{RouteID: 7, Decision: state.RouteDecisionSkipped, SkipReason: &reason}, e, "not_recorded"},
		{"missing pair", state.RouteDecision{RouteID: 7, Decision: state.RouteDecisionSkipped, SkipReason: &reason, ExclusionIndex: &index}, e, "invalid_reference"},
		{"missing route", d, &runRouteEvidence{}, "definition_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.status, projectCausalExclusion(tc.decision, &cfg, tc.evidence).Status)
		})
	}
	version = 2
	require.Equal(t, "unsupported", projectCausalExclusion(d, &cfg, e).Status)
	version, index = 1, 2
	require.Equal(t, "invalid_reference", projectCausalExclusion(d, &cfg, e).Status)
	index = 0
	e.routes[7].Exclusions[0] = config.SnapshotExclusion{ExclusionType: "duration_min", ExclusionValue: "bad"}
	require.Equal(t, "invalid_reference", projectCausalExclusion(d, &cfg, e).Status)
	e.routes[7].Exclusions[0].ExclusionType = "unknown secret"
	require.Equal(t, "unsupported", projectCausalExclusion(d, &cfg, e).Status)
}

func TestCausalExclusionMalformedSnapshotHTTP(t *testing.T) {
	ctx := context.Background()
	db, store, routes, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routes)
	versions := config.NewStore(db)
	cfg, err := versions.Create(ctx, config.Snapshot{Routes: []config.SnapshotRoute{{ID: route.ID, Name: "Historic"}}}, "test")
	require.NoError(t, err)
	runID, videoID := createRunWithDecision(t, ctx, store, route.ID, &cfg)
	_, err = db.ExecContext(ctx, `UPDATE route_decision SET decision='skipped', skip_reason='exclusion', exclusion_index=0, exclusion_semantics_version=1 WHERE run_id=? AND video_id=?`, runID, videoID)
	require.NoError(t, err)
	srv := newRunVideoServer(store, routes, versions)
	for _, tc := range []struct{ name, exclusion, status, valueStatus string }{
		{"missing value", `{"exclusion_type":"word"}`, "definition_unavailable", "unavailable"},
		{"null value", `{"exclusion_type":"word","exclusion_value":null}`, "definition_unavailable", "unavailable"},
		{"missing type", `{"exclusion_value":"secret"}`, "definition_unavailable", "unavailable"},
		{"null type", `{"exclusion_type":null,"exclusion_value":"secret"}`, "definition_unavailable", "unavailable"},
		{"empty value", `{"exclusion_type":"word","exclusion_value":""}`, "available", "available"},
		{"invalid duration", `{"exclusion_type":"duration_min","exclusion_value":"bad"}`, "invalid_reference", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := fmt.Sprintf(`{"routes":[{"id":%d,"name":"Historic","exclusions":[%s]}]}`, route.ID, tc.exclusion)
			_, err := db.ExecContext(ctx, `UPDATE config_version SET snapshot=? WHERE id=?`, snapshot, cfg)
			require.NoError(t, err)
			for _, path := range []string{fmt.Sprintf("/api/runs/%d/videos", runID), fmt.Sprintf("/api/runs/%d/videos/v1", runID)} {
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				require.Equal(t, http.StatusOK, rec.Code)
				require.NotContains(t, rec.Body.String(), "secret")
				var body map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				var cause *causalExclusion
				if raw, ok := body["videos"]; ok {
					var videos []videoListItem
					require.NoError(t, json.Unmarshal(raw, &videos))
					cause = videos[0].RouteDecisions[0].CausalExclusion
				} else {
					var video videoDetail
					require.NoError(t, json.Unmarshal(body["video"], &video))
					cause = video.RouteDecisions[0].CausalExclusion
				}
				require.Equal(t, tc.status, cause.Status)
				require.Equal(t, tc.valueStatus, cause.ConfiguredValueStatus)
				if tc.name == "empty value" {
					require.Equal(t, "", *cause.ConfiguredValue)
				} else {
					require.Nil(t, cause.ConfiguredValue)
				}
				if tc.name == "missing value" || tc.name == "null value" {
					require.Equal(t, "word", *cause.ExclusionType)
					require.Equal(t, "lowercase_substring", *cause.Comparison)
				}
				if tc.status != "available" {
					require.Nil(t, cause.Matched)
				}
			}
		})
	}
}

func TestNonExclusionReferenceWarnsWithoutInventingCause(t *testing.T) {
	ctx := context.Background()
	db, store, routes, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routes)
	versions := config.NewStore(db)
	cfg, err := versions.Create(ctx, config.Snapshot{Routes: []config.SnapshotRoute{{ID: route.ID}}}, "test")
	require.NoError(t, err)
	runID, videoID := createRunWithDecision(t, ctx, store, route.ID, &cfg)
	// Simulate an externally corrupted historical row; normal writes reject it.
	_, err = db.ExecContext(ctx, `DROP TRIGGER route_decision_exclusion_reference_update`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE route_decision SET exclusion_index=0, exclusion_semantics_version=1 WHERE run_id=? AND video_id=?`, runID, videoID)
	require.NoError(t, err)
	srv := newRunVideoServer(store, routes, versions)
	var logs bytes.Buffer
	srv.logger = slog.New(slog.NewTextHandler(&logs, nil))
	for _, path := range []string{fmt.Sprintf("/api/runs/%d/videos", runID), fmt.Sprintf("/api/runs/%d/videos/v1", runID)} {
		logs.Reset()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"causal_exclusion":null`)
		require.Contains(t, logs.String(), "non-exclusion decision has causal reference")
	}
}

func TestCausalExclusionRegisteredListAndDetail(t *testing.T) {
	ctx := context.Background()
	db, store, routes, _, cleanup := setupRunVideoTest(t)
	defer cleanup()
	route := createRouteForRunVideoTests(t, ctx, routes)
	versions := config.NewStore(db)
	cfg, err := versions.Create(ctx, config.Snapshot{Routes: []config.SnapshotRoute{{ID: route.ID, Name: "Historic", Exclusions: []config.SnapshotExclusion{{ExclusionType: "title", ExclusionValue: "first"}, {ExclusionType: "word", ExclusionValue: "secret"}, {ExclusionType: "word", ExclusionValue: "secret"}}}}}, "test")
	require.NoError(t, err)
	runID, videoID := createRunWithDecision(t, ctx, store, route.ID, &cfg)
	_, err = db.ExecContext(ctx, `UPDATE route_decision SET decision='skipped', skip_reason='exclusion', exclusion_index=2, exclusion_semantics_version=1 WHERE run_id=? AND video_id=?`, runID, videoID)
	require.NoError(t, err)
	srv := newRunVideoServer(store, routes, versions)
	for _, path := range []string{fmt.Sprintf("/api/runs/%d/videos", runID), fmt.Sprintf("/api/runs/%d/videos/v1", runID)} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		var decisions []routeDecisionSummary
		if raw, ok := body["videos"]; ok {
			var videos []videoListItem
			require.NoError(t, json.Unmarshal(raw, &videos))
			decisions = videos[0].RouteDecisions
		} else {
			var video videoDetail
			require.NoError(t, json.Unmarshal(body["video"], &video))
			d := video.RouteDecisions[0]
			decisions = []routeDecisionSummary{{RouteName: d.RouteName, RouteNameSource: d.RouteNameSource, CausalExclusion: d.CausalExclusion}}
		}
		require.Len(t, decisions, 1)
		require.Equal(t, "Historic", decisions[0].RouteName)
		require.Equal(t, "run_snapshot", decisions[0].RouteNameSource)
		require.Equal(t, "available", decisions[0].CausalExclusion.Status)
		require.Equal(t, 2, *decisions[0].CausalExclusion.ExclusionIndex)
		require.Equal(t, "lowercase_substring", *decisions[0].CausalExclusion.Comparison)
		require.Equal(t, "secret", *decisions[0].CausalExclusion.ConfiguredValue)
		require.Equal(t, "available", decisions[0].CausalExclusion.ConfiguredValueStatus)
	}
}

// The configured value is released exactly as configured: markup, quotes and
// non-ASCII text are neither escaped nor normalized by the projection.
func TestCausalExclusionConfiguredValueExact(t *testing.T) {
	index, version, cfg := 0, 1, int64(42)
	reason := state.SkipReasonExclusion
	d := state.RouteDecision{RouteID: 7, Decision: state.RouteDecisionSkipped, SkipReason: &reason, ExclusionIndex: &index, ExclusionSemanticsVersion: &version}
	for _, value := range []string{
		`<script>alert("x")</script> & <b>bold</b>`,
		`He said "hi" and 'bye' \ back`,
		"  Ünïcödé 日本語 🎵 café  ",
		strings.Repeat("long ", 200),
	} {
		e := &runRouteEvidence{routes: map[int64]config.SnapshotRoute{7: {Exclusions: []config.SnapshotExclusion{{ExclusionType: "word", ExclusionValue: value}}}}}
		got := projectCausalExclusion(d, &cfg, e)
		require.Equal(t, "available", got.Status)
		require.Equal(t, "available", got.ConfiguredValueStatus)
		require.Equal(t, value, *got.ConfiguredValue)
	}
}

func TestCausalExclusionUnresolvedKeepsIdentity(t *testing.T) {
	index, version, cfg := 3, 1, int64(42)
	reason := state.SkipReasonExclusion
	d := state.RouteDecision{RouteID: 7, Decision: state.RouteDecisionSkipped, SkipReason: &reason, ExclusionIndex: &index, ExclusionSemanticsVersion: &version}
	// Route resolves, rule has a type but no value.
	var route config.SnapshotRoute
	require.NoError(t, json.Unmarshal([]byte(`{"exclusions":[{},{},{},{"exclusion_type":"title"}]}`), &route))
	e := &runRouteEvidence{routes: map[int64]config.SnapshotRoute{7: route}}
	got := projectCausalExclusion(d, &cfg, e)
	require.Equal(t, "definition_unavailable", got.Status)
	require.Equal(t, "unavailable", got.ConfiguredValueStatus)
	require.Nil(t, got.ConfiguredValue)
	require.Nil(t, got.Matched)
	require.Equal(t, 3, *got.ExclusionIndex)
	require.Equal(t, int64(42), *got.ConfigVersionID)
	require.Equal(t, "title", *got.ExclusionType)
	require.Equal(t, "trimmed_equal_fold", *got.Comparison)
	// Route cannot be resolved at all: identity scalars stay, value unavailable.
	got = projectCausalExclusion(d, &cfg, &runRouteEvidence{})
	require.Equal(t, "definition_unavailable", got.Status)
	require.Equal(t, "unavailable", got.ConfiguredValueStatus)
	require.Equal(t, 3, *got.ExclusionIndex)
	require.Equal(t, int64(42), *got.ConfigVersionID)
}
