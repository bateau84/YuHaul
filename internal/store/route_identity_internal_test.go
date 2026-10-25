package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSnapshotRouteIDs covers the lenient ConfigVersion snapshot extraction
// used by the ARS-004 backfill: malformed documents are skipped without
// repair (OC-003), and within a valid document only integer routes[].id
// values are recoverable identity keys.
func TestSnapshotRouteIDs(t *testing.T) {
	cases := []struct {
		name     string
		snapshot string
		want     []int64
	}{
		{
			name:     "valid snapshot",
			snapshot: `{"routes":[{"id":1},{"id":42}],"duplicate_threshold":0.9}`,
			want:     []int64{1, 42},
		},
		{
			name:     "duplicate ids are collected",
			snapshot: `{"routes":[{"id":3},{"id":3}]}`,
			want:     []int64{3, 3},
		},
		{
			name:     "malformed json is skipped",
			snapshot: `{"routes":[{"id":1,`,
			want:     nil,
		},
		{
			name:     "non-integer ids are invalid",
			snapshot: `{"routes":[{"id":"9"},{"id":7.5},{"id":-1.5},{"id":null},{}]}`,
			want:     []int64{},
		},
		{
			name:     "exponential spelling is not an integer id",
			snapshot: `{"routes":[{"id":1e3}]}`,
			want:     []int64{},
		},
		{
			name:     "valid ids survive invalid siblings",
			snapshot: `{"routes":[{"id":"9"},{"id":7.5},{"id":10}]}`,
			want:     []int64{10},
		},
		{
			name:     "null routes",
			snapshot: `{"routes":null}`,
			want:     []int64{},
		},
		{
			name:     "empty object",
			snapshot: `{}`,
			want:     []int64{},
		},
		{
			name:     "json null document",
			snapshot: `null`,
			want:     []int64{},
		},
		{
			name:     "non-object document",
			snapshot: `[{"id":1}]`,
			want:     []int64{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snapshotRouteIDs(tc.snapshot)
			if len(tc.want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
