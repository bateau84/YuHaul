package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/state"
)

func notMatched(routeID int64) state.RouteDecision {
	return state.RouteDecision{ID: 1, RouteID: routeID, Decision: state.RouteDecisionNotMatched}
}

func evidenceFor(routeID int64, composition string, conditions ...config.SnapshotCondition) *runRouteEvidence {
	return &runRouteEvidence{routes: map[int64]config.SnapshotRoute{
		routeID: {ID: routeID, ConditionComposition: composition, Conditions: conditions},
	}}
}

func cond(index int, field, operator, value string) config.SnapshotCondition {
	var c config.SnapshotCondition
	raw := fmt.Sprintf(`{"condition_index":%d,"field":%q,"operator":%q,"value":%q}`, index, field, operator, value)
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		panic(err)
	}
	return c
}

func TestProjectFailedCondition(t *testing.T) {
	and := evidenceFor(7, "AND",
		cond(0, "title", "contains", "jazz"),
		cond(1, "channel_name", "equals", "Fireship"),
		cond(2, "duration_seconds", "between", "60,600"))

	t.Run("first failed condition in order with the count of the others", func(t *testing.T) {
		got := projectFailedCondition(notMatched(7), and, []int{1, 2})
		require.Equal(t, &failedCondition{Field: "channel_name", Operator: "equals", Value: "Fireship", Composition: "AND", ConditionCount: 3, FurtherFailed: 1}, got)
	})
	t.Run("only one failed", func(t *testing.T) {
		got := projectFailedCondition(notMatched(7), and, []int{0})
		require.Equal(t, 0, got.FurtherFailed)
		require.Equal(t, "jazz", got.Value)
	})
	t.Run("OR keeps composition and configured condition count", func(t *testing.T) {
		or := evidenceFor(7, "OR", cond(0, "title", "contains", "a"), cond(1, "title", "contains", "b"))
		got := projectFailedCondition(notMatched(7), or, []int{0, 1})
		require.Equal(t, "OR", got.Composition)
		require.Equal(t, 2, got.ConditionCount)
		require.Equal(t, 1, got.FurtherFailed)
	})
	t.Run("value is the configured one, released unchanged", func(t *testing.T) {
		raw := `<b>"x"</b> ` + "日本語"
		e := evidenceFor(7, "AND", cond(0, "title", "contains", raw))
		require.Equal(t, raw, projectFailedCondition(notMatched(7), e, []int{0}).Value)
	})

	for name, tc := range map[string]struct {
		decision state.RouteDecision
		evidence *runRouteEvidence
		failed   []int
	}{
		"matched decision":         {state.RouteDecision{RouteID: 7, Decision: state.RouteDecisionMatched}, and, []int{0}},
		"no failed audit":          {notMatched(7), and, nil},
		"route not in snapshot":    {notMatched(8), and, []int{0}},
		"no snapshot at all":       {notMatched(7), &runRouteEvidence{}, []int{0}},
		"condition not in route":   {notMatched(7), and, []int{9}},
		"unknown operator":         {notMatched(7), evidenceFor(7, "AND", cond(0, "title", "weird", "x")), []int{0}},
		"unknown field":            {notMatched(7), evidenceFor(7, "AND", cond(0, "secret", "equals", "x")), []int{0}},
		"incomplete snapshot cond": {notMatched(7), evidenceFor(7, "AND", config.SnapshotCondition{ConditionIndex: 0, Field: "title", Operator: "contains"}), []int{0}},
	} {
		t.Run("nil for "+name, func(t *testing.T) {
			if name == "incomplete snapshot cond" {
				var c config.SnapshotCondition
				require.NoError(t, json.Unmarshal([]byte(`{"condition_index":0,"field":"title","operator":"contains"}`), &c))
				tc.evidence = evidenceFor(7, "AND", c)
			}
			require.Nil(t, projectFailedCondition(tc.decision, tc.evidence, tc.failed))
		})
	}
}

// seedNotMatchedRun creates a run linked to a snapshot whose single route has
// three conditions, and n videos that each failed conditions 1 and 2. The
// audits record a distinctive compared value that must never be served.
func seedNotMatchedRun(t *testing.T, ctx context.Context, n int) (srvFor func(RunVideoStore) *Server, runID int64) {
	t.Helper()
	db, store, routes, _, cleanup := setupRunVideoTest(t)
	t.Cleanup(cleanup)
	route := createRouteForRunVideoTests(t, ctx, routes)
	versions := config.NewStore(db)
	cfg, err := versions.Create(ctx, config.Snapshot{Routes: []config.SnapshotRoute{{
		ID: route.ID, Name: "Historic", ConditionComposition: "AND",
		Conditions: []config.SnapshotCondition{
			cond(0, "title", "contains", "jazz"),
			cond(1, "channel_name", "equals", "Fireship"),
			cond(2, "duration_seconds", "greater_than", "300"),
		},
	}}}, "test")
	require.NoError(t, err)
	runID, err = store.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusCompleted, ConfigVersionID: &cfg})
	require.NoError(t, err)
	actual := "COMPARED-VALUE-MUST-NOT-LEAK"
	for i := range n {
		vid, err := store.CreateVideo(ctx, &state.Video{VideoID: fmt.Sprintf("v%d", i), ChannelID: "c", DiscoveredAt: time.Now().UTC(), DiscoveredInRunID: runID, ProcessingState: state.VideoStateCompleted, FinalOutcome: state.FinalOutcomeRejected})
		require.NoError(t, err)
		did, err := store.CreateRouteDecision(ctx, &state.RouteDecision{VideoID: vid, RunID: runID, RouteID: route.ID, EvaluationOrder: 1, Decision: state.RouteDecisionNotMatched})
		require.NoError(t, err)
		for idx, matched := range []bool{true, false, false} {
			_, err = store.CreateConditionAudit(ctx, &state.ConditionAudit{RouteDecisionID: did, ConditionIndex: idx, Field: "title", Operator: "contains", ExpectedValue: "STORED-EXPECTED", ActualValue: &actual, Matched: matched})
			require.NoError(t, err)
		}
	}
	return func(s RunVideoStore) *Server {
		srv := newRunVideoServer(store, routes, versions)
		if s != nil {
			srv.runVideoStore = s
		}
		return srv
	}, runID
}

type countingStore struct {
	RunVideoStore
	calls int
	ids   []int64
	err   error
}

func (c *countingStore) ListFailedConditionIndexes(ctx context.Context, ids []int64) (map[int64][]int, error) {
	c.calls++
	c.ids = append([]int64(nil), ids...)
	if c.err != nil {
		return nil, c.err
	}
	return c.RunVideoStore.ListFailedConditionIndexes(ctx, ids)
}

func TestRunVideoListCarriesFailedConditionFromSnapshotInOneBatchedRead(t *testing.T) {
	ctx := context.Background()
	srvFor, runID := seedNotMatchedRun(t, ctx, 3)
	base := srvFor(nil)
	counter := &countingStore{RunVideoStore: base.runVideoStore}
	srv := srvFor(counter)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/runs/%d/videos", runID), nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, counter.calls, "failed conditions must be read once for the page, not once per row")
	require.Len(t, counter.ids, 3)
	// The snapshot value is served; the stored expected value and the video's
	// compared value are not.
	require.NotContains(t, rec.Body.String(), "COMPARED-VALUE-MUST-NOT-LEAK")
	require.NotContains(t, rec.Body.String(), "STORED-EXPECTED")

	var body struct {
		Videos []struct {
			RouteDecisions []map[string]json.RawMessage `json:"route_decisions"`
		} `json:"videos"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Videos, 3)
	for _, v := range body.Videos {
		require.JSONEq(t,
			`{"field":"channel_name","operator":"equals","value":"Fireship","composition":"AND","condition_count":3,"further_failed":1}`,
			string(v.RouteDecisions[0]["failed_condition"]))
	}

	// Decision Detail is unchanged: no failed_condition there.
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/runs/%d/videos/v0", runID), nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "failed_condition")
}

func TestRunVideoListWithoutSnapshotOmitsFailedCondition(t *testing.T) {
	ctx := context.Background()
	srvFor, runID := seedNotMatchedRun(t, ctx, 1)
	srv := srvFor(nil)
	// The run's snapshot cannot be read (OC-003 incomplete context).
	srv.configVersionStore = noopConfigVersionStore{}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/runs/%d/videos", runID), nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "failed_condition")
	require.Contains(t, rec.Body.String(), `"decision":"not_matched"`)
}

func TestRunVideoListFailedConditionReadFailureIsNotSilentlyDropped(t *testing.T) {
	ctx := context.Background()
	srvFor, runID := seedNotMatchedRun(t, ctx, 1)
	srv := srvFor(&countingStore{RunVideoStore: srvFor(nil).runVideoStore, err: errors.New("audit read failed")})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/runs/%d/videos", runID), nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
