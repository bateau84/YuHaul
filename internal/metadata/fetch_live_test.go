package metadata_test

import (
	"context"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Live requirement is never satisfied by a stored row, and Live is reported
// alongside Statuses for every returned ID of this call only.
func TestFetchForEvaluationLiveRequirementAlwaysReads(t *testing.T) {
	bound := &fakeBound{do: func(_ int, ids []string) (metadata.FetchResult, error) {
		res := metadata.FetchResult{Complete: true, Returned: ids, Live: map[string]metadata.LiveStatus{}}
		for _, id := range ids {
			res.Details = append(res.Details, detail(id))
			res.Live[id] = metadata.LiveStatus("upcoming")
		}
		return res, nil
	}}
	m, ms, _ := newFetchManager(t, bound, 1000)
	for _, id := range []string{"held", "plain"} {
		require.NoError(t, ms.Upsert(context.Background(), metadata.TransientMetadata{VideoID: id, Title: pv("T"), ChannelName: pv("C"), Description: pv(""), DurationSeconds: pv(0), FetchedAt: evalNow.Add(-time.Hour), ExpiresAt: evalNow.Add(time.Hour), IsActive: true}))
	}
	reqs := []metadata.InputRequirement{
		{VideoID: "held", Title: true, Live: true},
		{VideoID: "held", Description: true}, // merged: Live is kept
		{VideoID: "plain", Title: true},
		{VideoID: "fresh", Live: true},
	}
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, reqs)
	require.NoError(t, err)
	require.Len(t, bound.calls, 1)
	assert.Equal(t, []string{"held", "fresh"}, bound.calls[0], "held live row is read; plain current row is not")
	assert.Equal(t, metadata.FetchCurrent, out.Statuses["plain"])
	assert.Equal(t, metadata.FetchFetched, out.Statuses["held"])
	assert.Equal(t, map[string]metadata.LiveStatus{"held": "upcoming", "fresh": "upcoming"}, out.Live)
}

// Without a Live requirement nothing changes: no read, no Live map.
func TestFetchForEvaluationWithoutLiveReportsNoLive(t *testing.T) {
	bound := &fakeBound{do: func(_ int, ids []string) (metadata.FetchResult, error) {
		return metadata.FetchResult{Complete: true, Returned: ids, Details: []metadata.TransientMetadata{detail(ids[0])}}, nil
	}}
	m, _, _ := newFetchManager(t, bound, 1000)
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, allInputs("a"))
	require.NoError(t, err)
	assert.Empty(t, out.Live)
}

// Live is only reported for requested, returned IDs; failed reads report none.
func TestFetchForEvaluationLiveIgnoresStrayAndFailedReads(t *testing.T) {
	bound := &fakeBound{do: func(_ int, ids []string) (metadata.FetchResult, error) {
		return metadata.FetchResult{
			Complete: true, Returned: ids,
			Live: map[string]metadata.LiveStatus{ids[0]: "none", "stray": "live"},
		}, nil
	}}
	m, _, _ := newFetchManager(t, bound, 1000)
	out, err := m.FetchForEvaluation(context.Background(), auth.AuthorizationBinding{}, []metadata.InputRequirement{{VideoID: "a", Live: true}})
	require.NoError(t, err)
	assert.Equal(t, map[string]metadata.LiveStatus{"a": "none"}, out.Live)
	// Returned with no usable detail: unusable status, yet Live is still given.
	assert.Equal(t, metadata.FetchUnusable, out.Statuses["a"])
}
