package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sp(s string) *string { return &s }

// D1: parsing.
func TestParsePublishedRange(t *testing.T) {
	ok := []struct {
		name     string
		from, to *string
	}{
		{"both", sp("2026-10-01"), sp("2026-10-31")},
		{"same day", sp("2026-10-01"), sp("2026-10-01")},
		{"leap day", sp("2028-02-29"), nil},
		{"from only", sp("2026-10-01"), nil},
		{"to only", nil, sp("2026-10-01")},
		{"none", nil, nil},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			rng, err := ParsePublishedRange(c.from, c.to)
			require.Nil(t, err)
			assert.Equal(t, c.from != nil || c.to != nil, rng.IsSet())
		})
	}

	bad := []struct {
		name     string
		from, to *string
		want     RangeError
	}{
		{"non leap day", sp("2026-02-29"), nil, RangeError{"published_from", "invalid_date"}},
		{"impossible day", nil, sp("2026-02-30"), RangeError{"published_to", "invalid_date"}},
		{"non canonical", sp("2026-1-05"), nil, RangeError{"published_from", "invalid_date"}},
		{"empty", sp(""), nil, RangeError{"published_from", "invalid_date"}},
		{"empty to", nil, sp(""), RangeError{"published_to", "invalid_date"}},
		{"timestamp", sp("2026-10-01T00:00:00Z"), nil, RangeError{"published_from", "invalid_date"}},
		{"reversed", sp("2026-11-02"), sp("2026-11-01"), RangeError{"", "reversed"}},
		{"from checked first", sp("nope"), sp("also nope"), RangeError{"published_from", "invalid_date"}},
		{"invalid to beats order", sp("2026-11-02"), sp("2026-13-01"), RangeError{"published_to", "invalid_date"}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParsePublishedRange(c.from, c.to)
			require.NotNil(t, err)
			assert.Equal(t, c.want, *err)
		})
	}
}

func TestPublishedRangeContainsIsHalfOpenInUTC(t *testing.T) {
	rng, err := ParsePublishedRange(sp("2026-10-01"), sp("2026-10-31"))
	require.Nil(t, err)
	at := func(s string) time.Time {
		v, perr := time.Parse(time.RFC3339, s)
		require.NoError(t, perr)
		return v
	}
	assert.False(t, rng.Contains(at("2026-09-30T23:59:59Z")))
	assert.True(t, rng.Contains(at("2026-10-01T00:00:00Z")))
	assert.True(t, rng.Contains(at("2026-10-31T23:59:59.999Z")))
	assert.False(t, rng.Contains(at("2026-11-01T00:00:00Z")))
	// The instant decides, not the wall-clock date in the stored offset.
	assert.False(t, rng.Contains(at("2026-10-01T01:00:00+02:00")))
	assert.True(t, rng.Contains(at("2026-11-01T01:00:00+02:00")))

	open, _ := ParsePublishedRange(nil, sp("2026-10-31"))
	assert.True(t, open.Contains(at("1999-01-01T00:00:00Z")))
}
