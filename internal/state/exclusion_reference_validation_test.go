package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateRouteDecisionRejectsInvalidExclusionReferences(t *testing.T) {
	store := newTestMachine(t).store
	zero, negative, one := 0, -1, 1
	exclusion, disabled := SkipReasonExclusion, SkipReasonDisabled
	cases := []struct {
		name     string
		decision RouteDecision
	}{
		{"missing index", RouteDecision{ExclusionSemanticsVersion: &one}},
		{"missing version", RouteDecision{ExclusionIndex: &zero}},
		{"null reason", RouteDecision{Decision: RouteDecisionSkipped, ExclusionIndex: &zero, ExclusionSemanticsVersion: &one}},
		{"wrong reason", RouteDecision{Decision: RouteDecisionSkipped, SkipReason: &disabled, ExclusionIndex: &zero, ExclusionSemanticsVersion: &one}},
		{"wrong decision", RouteDecision{Decision: RouteDecisionMatched, SkipReason: &exclusion, ExclusionIndex: &zero, ExclusionSemanticsVersion: &one}},
		{"negative index", RouteDecision{Decision: RouteDecisionSkipped, SkipReason: &exclusion, ExclusionIndex: &negative, ExclusionSemanticsVersion: &one}},
		{"zero version", RouteDecision{Decision: RouteDecisionSkipped, SkipReason: &exclusion, ExclusionIndex: &zero, ExclusionSemanticsVersion: &zero}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.CreateRouteDecision(context.Background(), &tc.decision)
			require.ErrorContains(t, err, "exclusion reference")
		})
	}
}
