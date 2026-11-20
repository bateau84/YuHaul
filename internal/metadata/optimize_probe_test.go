package metadata_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markerWords are distinctive folded words. Their trigrams are the probe's
// needles; none occurs in the realistic titles seeded below.
var markerWords = []string{"qzxjvk", "kwvbnh", "xjqzpf"}

func markerTrigrams() []string {
	var out []string
	for _, w := range markerWords {
		for i := 0; i+3 <= len(w); i++ {
			out = append(out, w[i:i+3])
		}
	}
	return out
}

// markerHits counts, over all title_search_data blocks, the whole marker
// trigrams that appear in their bytes.
func markerHits(t *testing.T, db *sql.DB) int {
	t.Helper()
	total := 0
	for _, tri := range markerTrigrams() {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM title_search_data WHERE instr(block, CAST(? AS BLOB)) > 0`, tri).Scan(&n))
		total += n
	}
	return total
}

func matchRowids(t *testing.T, db *sql.DB, phrase string) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT rowid FROM title_search WHERE title_search MATCH ? ORDER BY rowid`, `"`+phrase+`"`)
	require.NoError(t, err)
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		out = append(out, id)
	}
	require.NoError(t, rows.Err())
	return out
}

// R8 (ARS-001 §10.11): after the markers' entries are removed, their trigrams
// stay in live title_search_data segments (positive control); a complete
// OptimizeSearchIndex then leaves none, and search results are unchanged. This
// reads the segment bytes, so it fails when the optimize reports Complete
// without merging, for example when the first step is not the negative
// full-merge or when no step is.
func TestOptimizeRemovesMarkerTrigramsFromSegments_R8(t *testing.T) {
	ctx := context.Background()
	ms, _ := openIndexDB(t)
	db := ms.DB()
	old := metadata.OptimizeMergePages
	metadata.OptimizeMergePages = 2
	t.Cleanup(func() { metadata.OptimizeMergePages = old })

	seedHeld(t, ms, 300, "probe")
	now := time.Now().UTC()
	var markerIDs []string
	for i, w := range markerWords {
		title := fmt.Sprintf("%s remarkable title %d", w, i)
		id := fmt.Sprintf("marker%02d", i)
		markerIDs = append(markerIDs, id)
		require.NoError(t, ms.Upsert(ctx, metadata.TransientMetadata{
			VideoID: id, Title: &title, FetchedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), IsActive: true,
		}))
	}
	require.Positive(t, markerHits(t, db), "positive control: a held marker is in the segments")
	require.Equal(t, 303, count(t, db, "title_search_doc"))

	// Expire fires the purge trigger: logical removal at once, segments untouched.
	require.NoError(t, ms.Expire(ctx, markerIDs, now))
	require.Equal(t, 300, count(t, db, "title_search_doc"))
	for _, w := range markerWords {
		require.Empty(t, matchRowids(t, db, w), "logically removed at once")
	}
	before := markerHits(t, db)
	if before == 0 {
		t.Fatal("inconclusive: no marker trigram is left in the segments before the optimize, so the probe proves nothing")
	}
	wantMatch := matchRowids(t, db, "lifecycle")
	require.NotEmpty(t, wantMatch)
	wantEntries := entries(t, db)

	stats, err := metadata.OptimizeSearchIndex(ctx, db)
	require.NoError(t, err)
	require.True(t, stats.Complete)
	assert.Greater(t, stats.Steps, 1, "a small merge size takes several steps")

	assert.Zero(t, markerHits(t, db), "a complete optimize removed every marker trigram from the live segments (had %d)", before)
	assert.Equal(t, wantMatch, matchRowids(t, db, "lifecycle"), "search results are unchanged")
	assert.Equal(t, wantEntries, entries(t, db))

	// Interruption: a second round of removals, cut after the first step, is
	// incomplete and leaves trigrams; a later call finishes the job.
	for i, w := range markerWords {
		title := fmt.Sprintf("%s second round %d", w, i)
		id := fmt.Sprintf("again%02d", i)
		require.NoError(t, ms.Upsert(ctx, metadata.TransientMetadata{
			VideoID: id, Title: &title, FetchedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), IsActive: true,
		}))
		require.NoError(t, ms.Expire(ctx, []string{id}, now))
	}
	require.Positive(t, markerHits(t, db), "positive control for the second round")
	capped := metadata.OptimizeMaxSteps
	metadata.OptimizeMaxSteps = 1
	partial, err := metadata.OptimizeSearchIndex(ctx, db)
	metadata.OptimizeMaxSteps = capped
	require.NoError(t, err)
	assert.False(t, partial.Complete)
	assert.Positive(t, markerHits(t, db), "an incomplete optimize does not claim removal")
	done, err := metadata.OptimizeSearchIndex(ctx, db)
	require.NoError(t, err)
	require.True(t, done.Complete)
	assert.Zero(t, markerHits(t, db))
}
