package state

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestListFailedConditionIndexesBatchesAndReturnsOnlyFailures(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	runID := seedRun(t, m)
	routeID := seedRoute(t, m.store.db)
	actual := "SECRET-COMPARED-VALUE"
	record := func(order int, audits []ConditionAudit) int64 {
		t.Helper()
		v, err := m.DiscoverVideo(ctx, runID, fmt.Sprintf("yt-video-fc-%d", order), "UCchannel")
		if err != nil {
			t.Fatalf("DiscoverVideo: %v", err)
		}
		d, err := m.RecordRouteEvaluation(ctx, &RouteDecision{
			VideoID: v.ID, RunID: runID, RouteID: routeID,
			EvaluationOrder: 1, Decision: RouteDecisionNotMatched,
		}, audits)
		if err != nil {
			t.Fatalf("RecordRouteEvaluation %d: %v", order, err)
		}
		return d.ID
	}
	a := record(1, []ConditionAudit{
		{ConditionIndex: 0, Field: "title", Operator: "contains", ExpectedValue: "x", ActualValue: &actual, Matched: false},
		{ConditionIndex: 1, Field: "title", Operator: "contains", ExpectedValue: "y", ActualValue: &actual, Matched: true},
		{ConditionIndex: 2, Field: "title", Operator: "contains", ExpectedValue: "z", ActualValue: &actual, Matched: false},
	})
	b := record(2, []ConditionAudit{
		{ConditionIndex: 0, Field: "title", Operator: "contains", ExpectedValue: "x", ActualValue: &actual, Matched: true},
	})
	c := record(3, []ConditionAudit{
		{ConditionIndex: 0, Field: "title", Operator: "contains", ExpectedValue: "x", ActualValue: &actual, Matched: false},
	})

	got, err := m.store.ListFailedConditionIndexes(ctx, []int64{a, b, c})
	if err != nil {
		t.Fatalf("ListFailedConditionIndexes: %v", err)
	}
	want := map[int64][]int{a: {0, 2}, c: {0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failed indexes = %v, want %v (decision %d, all conditions met, must be absent)", got, want, b)
	}

	empty, err := m.store.ListFailedConditionIndexes(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input = %v, %v; want empty map and no error", empty, err)
	}
}
