//go:build integration

package e2e

// Durable composition regression for OC-004, ADR-013, and ADR-014.
//
// Every other test in this area covers one boundary on its own: the migration
// tests prove the real importer writes eligible references and no credit rows,
// the discovery/pipeline tests prove native same-ID persistence and duplicate
// evaluation, and the stats tests prove pairwise provenance from seeded rows.
// This file is the single assembled proof across all four stages against one
// real SQLite database:
//
//  1. IMPORT — a prior-system source corpus in the real input schema is read
//     by the real migration.DiscoverSchema + migration.Importer into the
//     production destination database opened through store.Open.
//  2. NATIVE PERSISTENCE — the real discovery engine and pipeline persist the
//     same imported video ID plus a distinct title-matching video from
//     UC_native before the duplicate phase runs. Only the YouTube HTTP
//     boundary is substituted (the approved base-URL seam, ADR-001); no
//     product-owned persistence is faked.
//  3. DUPLICATE EVALUATION — the real detector runs inside the production
//     pipeline and its durable duplicate_match output is asserted.
//  4. STATISTICS — the real stats service reads the same database back for
//     discovery/relevance provenance.
//
// It proves what the split tests cannot: the imported channel association
// never earns discovery or route credit, a same-ID native observation is never
// recorded as a self-duplicate while the imported reference still matches a
// distinct video, and pairwise credit comes only from orderable native video
// chronology — never when the native reference row is missing and never when
// the two discovery times are equal.
//
// Group-global earliest provenance is deliberately not asserted here: that is
// an unresolved BR-010 semantics question owned by the requirements owner
// (see ADR-014 open questions).

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/stats"
)

// Channel and import identities for the composition. importedChannelID exists
// only as the optional association on the prior-system source row: the YouTube
// seam never subscribes or serves it, so no native observation can ever carry
// it. nativeChannelID is the single subscribed channel of the mock seam.
const (
	importedChannelID = "UC_imported"
	nativeChannelID   = "UC_native"
	nativeChannelName = "Native Jazz Channel"
	importBatchID     = "e2e-import-batch"

	// equalDiscoveredAt is the identical discovery timestamp written over both
	// native observations for the equal-timestamps boundary.
	equalDiscoveredAt = "2026-01-02T10:00:00Z"
)

// creditEvidenceTables are the durable tables that carry discovery credit,
// duplicate events, or route/action credit. Importing must not write any of
// them (OC-004 guarantee 3, BR-010 AC6).
var creditEvidenceTables = []string{
	"video",
	"channel",
	"duplicate_match",
	"route_decision",
	"executed_action",
}

// corpusRow is one prior-system source record in the real import input shape
// discovered by migration.DiscoverSchema: video_id + title, optional channel_id.
type corpusRow struct {
	videoID   string
	title     string
	channelID string
}

// newSourceCorpus builds a source SQLite database with the prior-system table
// shape the importer discovers (a single videos table with video_id, title and
// an optional channel_id column) and inserts the given rows.
func newSourceCorpus(t *testing.T, rows []corpusRow) *sql.DB {
	t.Helper()
	ctx := context.Background()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "prior-system.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.ExecContext(ctx,
		`CREATE TABLE videos (video_id TEXT NOT NULL, title TEXT NOT NULL, channel_id TEXT);`)
	require.NoError(t, err)
	for _, r := range rows {
		_, err := db.ExecContext(ctx,
			`INSERT INTO videos (video_id, title, channel_id) VALUES (?, ?, ?);`,
			r.videoID, r.title, r.channelID)
		require.NoError(t, err)
	}
	return db
}

// nativeMockVideo is a video served by the YouTube seam from the one native
// channel under test.
func nativeMockVideo(id, title string, publishedAt time.Time) mockVideo {
	return mockVideo{
		ID:              id,
		Title:           title,
		ChannelID:       nativeChannelID,
		ChannelTitle:    nativeChannelName,
		Description:     "native observation served by the YouTube seam",
		DurationSeconds: 270,
		PublishedAt:     publishedAt,
		ThumbnailURL:    "http://example.com/thumb.jpg",
	}
}

// importedComposition is one assembled import → native discovery → duplicate
// evaluation → statistics instance over a unique temp destination database.
type importedComposition struct {
	env        *testEnv
	statsStore *stats.Store

	importedID string
	matchingID string
	// fpID is the title_fingerprint.id the import created for importedID; the
	// distinct match must point at exactly this reference.
	fpID int64
}

// setupImportedCorpus builds the composition: the real source corpus, the
// e2e environment (production schema, real pipeline, YouTube seam), and the
// real migration.Importer run against the production destination DB.
//
// When observeImportedID is true the seam also serves the imported video ID
// natively (the ADR-013 same-ID case); when it is false only matchingID is
// observed, leaving the imported reference with no native video row.
func setupImportedCorpus(t *testing.T, importedID, matchingID, title string, observeImportedID bool) *importedComposition {
	t.Helper()
	ctx := context.Background()
	publishedAt := time.Now().UTC().Add(-2 * time.Hour)

	source := newSourceCorpus(t, []corpusRow{
		{videoID: importedID, title: title, channelID: importedChannelID},
	})

	// The imported ID is published first so its native observation is the
	// chronologically earlier member of the recorded pair.
	var nativeVideos []mockVideo
	if observeImportedID {
		nativeVideos = append(nativeVideos, nativeMockVideo(importedID, title, publishedAt))
	}
	nativeVideos = append(nativeVideos, nativeMockVideo(matchingID, title, publishedAt.Add(time.Minute)))

	env := setupEnv(t,
		[]mockChannel{{ID: nativeChannelID, Title: nativeChannelName, UploadsPlaylistID: "UUnative"}},
		nativeVideos,
	)

	// Real input mapping: the importer discovers the source schema itself.
	schema, err := migration.DiscoverSchema(ctx, source)
	require.NoError(t, err)
	require.Equal(t, "videos", schema.TableName)
	require.Equal(t, "video_id", schema.VideoIDColumn)
	require.Equal(t, "title", schema.TitleColumn)
	require.Equal(t, "channel_id", schema.ChannelIDColumn)

	result, err := migration.NewImporter(source, env.db, importBatchID).Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 1, result.Read)
	require.Equal(t, 1, result.Inserted, "the source row must be added to the corpus")
	require.Equal(t, 0, result.Skipped)
	require.Equal(t, 0, result.Errors)

	var fpID int64
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT id FROM title_fingerprint WHERE video_id = ?;`, importedID).Scan(&fpID))

	return &importedComposition{
		env:        env,
		statsStore: stats.NewStore(env.db),
		importedID: importedID,
		matchingID: matchingID,
		fpID:       fpID,
	}
}

// countRows runs one counting query against the composition database.
func (c *importedComposition) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, c.env.db.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

// assertImportedReferenceOnly asserts the stage-1 boundary: the import created
// exactly one eligible matching reference and wrote no credit-bearing row.
func (c *importedComposition) assertImportedReferenceOnly(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	var isRef int
	var source, batch string
	require.NoError(t, c.env.db.QueryRowContext(ctx,
		`SELECT is_duplicate_reference, source, import_batch_id
		   FROM title_fingerprint WHERE video_id = ?;`, c.importedID).
		Scan(&isRef, &source, &batch))
	assert.Equal(t, 1, isRef, "a valid imported row must be an eligible duplicate reference")
	assert.Equal(t, "user_import", source)
	assert.Equal(t, importBatchID, batch)

	require.Equal(t, 1, c.countRows(t,
		`SELECT COUNT(*) FROM title_fingerprint WHERE video_id = ?;`, c.importedID),
		"the corpus holds exactly one row for the imported video ID")

	for _, table := range creditEvidenceTables {
		assert.Zero(t, c.countRows(t, fmt.Sprintf("SELECT COUNT(*) FROM %s;", table)),
			"import must not write %s rows: eligibility is not credit", table)
	}
}

// runProduction creates the production route and executes the real pipeline
// (discovery → duplicate detection → exclusion → routing → actions) against
// the YouTube seam.
func (c *importedComposition) runProduction(t *testing.T) *state.Run {
	t.Helper()
	c.env.createRoute(t, "Jazz", "jazz", "PLjazz")
	return c.env.runProductionRun(t)
}

// newStatsService wires the real stats service to the composition database,
// with the production config store as its settings reader.
func (c *importedComposition) newStatsService() *stats.Service {
	return stats.NewService(c.statsStore, c.env.configStore, c.env.ledger)
}

// assertDistinctMatchRecordsImportedReference asserts the durable duplicate
// evaluation of the evaluated native video: exactly one duplicate_match row,
// pointing at the imported reference's fingerprint.
func (c *importedComposition) assertDistinctMatchRecordsImportedReference(t *testing.T, evaluatedID string) {
	t.Helper()
	ctx := context.Background()

	require.Equal(t, 1, c.countRows(t,
		`SELECT COUNT(*) FROM duplicate_match dm
		   INNER JOIN video v ON v.id = dm.video_id
		  WHERE v.video_id = ?;`, evaluatedID),
		"a distinct native video must match the imported eligible reference")

	var matchedFingerprintID int64
	var matchType string
	var similarity float64
	require.NoError(t, c.env.db.QueryRowContext(ctx,
		`SELECT dm.matched_fingerprint_id, dm.match_type, dm.similarity_score
		   FROM duplicate_match dm
		   INNER JOIN video v ON v.id = dm.video_id
		  WHERE v.video_id = ?;`, evaluatedID).
		Scan(&matchedFingerprintID, &matchType, &similarity))
	require.Equal(t, c.fpID, matchedFingerprintID, "the match must point at the imported reference row")
	assert.Equal(t, "exact_video_id", matchType, "identical canonical titles take the exact path")
	assert.InDelta(t, 1.0, similarity, 1e-9)
}

// assertImportedAssociationEarnsNoCredit reads the stats service's provenance
// output for the import-time channel association: no provenance event, a zero
// credit breakdown, and no channel-discovery record to serve a detail for.
func (c *importedComposition) assertImportedAssociationEarnsNoCredit(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	svc := c.newStatsService()

	rows, err := svc.DuplicateProvenance(ctx, importedChannelID)
	require.NoError(t, err)
	assert.Empty(t, rows, "the imported channel association must never appear as a provenance event")

	breakdown, err := c.statsStore.DuplicateProvenanceBreakdown(ctx, importedChannelID)
	require.NoError(t, err)
	assert.Equal(t, stats.DuplicateProvenanceBreakdown{}, breakdown,
		"the imported channel association must earn no discovery or relevance credit")

	_, err = svc.ChannelDetail(ctx, importedChannelID)
	assert.ErrorIs(t, err, state.ErrNotFound,
		"import must not fabricate a channel-discovery record for the imported association")
}

// assertChronologyCredit asserts the stage-4 output for a pair whose two native
// observations are strictly ordered: the earlier native observation earns
// discovery, the later matching video earns relevance, both belong to the
// native channel, and the imported association still earns nothing.
func (c *importedComposition) assertChronologyCredit(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	svc := c.newStatsService()

	// Headline invariant first: nothing about this pair may credit the
	// imported channel association.
	c.assertImportedAssociationEarnsNoCredit(t)

	detail, err := svc.ChannelDetail(ctx, nativeChannelID)
	require.NoError(t, err)
	assert.Equal(t, 2, detail.TotalDiscovered, "both native observations belong to the native channel")
	assert.Equal(t, 2, detail.TotalRouted, "route completion belongs to the natively observed channel")
	assert.Equal(t, stats.DuplicateProvenanceBreakdown{DiscoveryCredit: 1, RelevanceCredit: 1},
		detail.DuplicateProvenance, "pairwise credit follows native chronology")

	require.Len(t, detail.RecentVideoProvenance, 2)
	credit, ok := provenanceCredit(detail.RecentVideoProvenance, c.importedID)
	require.True(t, ok, "the native reference video must carry provenance")
	assert.Equal(t, "discovery", credit,
		"the earlier native observation earns discovery credit")
	credit, ok = provenanceCredit(detail.RecentVideoProvenance, c.matchingID)
	require.True(t, ok, "the matching video must carry provenance")
	assert.Equal(t, "relevance", credit,
		"relevance stays on the matching video's channel")

	require.Len(t, detail.MatchedRoutes, 1, "route evidence exists for the native channel")
	assert.Equal(t, 2, detail.MatchedRoutes[0].MatchCount)

	rows, err := svc.DuplicateProvenance(ctx, nativeChannelID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, c.matchingID, rows[0].VideoID, "provenance rows anchor on the evaluated video")
	assert.Equal(t, nativeChannelID, rows[0].ChannelID)
	assert.Equal(t, c.fpID, rows[0].MatchedFingerprintID)
	assert.Equal(t, nativeChannelID, rows[0].DiscoveryCreditChannelID)
	assert.Equal(t, nativeChannelName, rows[0].DiscoveryCreditChannelName)
}

// assertNoChronologyCredit asserts the stage-4 output when the recorded pair
// has no orderable native chronology: the match row stays listed with unknown
// attribution, neither video holds a credit tag, no channel earns discovery or
// relevance credit, and the imported association is never substituted.
func (c *importedComposition) assertNoChronologyCredit(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	svc := c.newStatsService()

	// Headline invariant first: absent orderable chronology, neither the
	// native channel nor the imported channel association may be credited.
	c.assertImportedAssociationEarnsNoCredit(t)

	detail, err := svc.ChannelDetail(ctx, nativeChannelID)
	require.NoError(t, err)
	assert.Equal(t, stats.DuplicateProvenanceBreakdown{}, detail.DuplicateProvenance,
		"no chronology-based discovery or relevance credit may be awarded")
	assert.Empty(t, detail.RecentVideoProvenance,
		"neither video may hold a chronology-based credit tag")

	rows, err := svc.DuplicateProvenance(ctx, nativeChannelID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the recorded match remains listed with unknown attribution")
	assert.Equal(t, c.matchingID, rows[0].VideoID)
	assert.Empty(t, rows[0].DiscoveryCreditChannelID, "no discovery credit channel is provable")
	assert.Empty(t, rows[0].DiscoveryCreditChannelName)
}

// provenanceCredit returns the duplicate-credit tag of a video in the per-video
// provenance output. ok is false when the video holds no credit tag.
func provenanceCredit(rows []stats.VideoProvenance, videoID string) (string, bool) {
	for _, r := range rows {
		if r.VideoID != videoID {
			continue
		}
		if r.DuplicateCredit == nil {
			return "", false
		}
		return *r.DuplicateCredit, true
	}
	return "", false
}

// TestE2E_ImportedCorpusImportDiscoveryDuplicateStatisticsComposition is the
// assembled import → native discovery → duplicate evaluation → statistics
// regression for OC-004, ADR-013, and ADR-014.
func TestE2E_ImportedCorpusImportDiscoveryDuplicateStatisticsComposition(t *testing.T) {
	// The same-ID case: the imported video ID is observed natively before the
	// duplicate phase, so eligibility must survive the native upsert while the
	// evaluation must not produce a self-duplicate.
	t.Run("imported association earns no credit and the same-ID native observation is not a self-duplicate", func(t *testing.T) {
		const title = "Midnight Jazz Live Session"
		const importedID = "vid_imported_same_id"
		const matchingID = "vid_native_match"

		comp := setupImportedCorpus(t, importedID, matchingID, title, true)

		// Stage 1: one eligible reference, no credit rows.
		comp.assertImportedReferenceOnly(t)

		// Stages 2 and 3: the production run persists both videos natively and
		// evaluates them with the real detector.
		run := comp.runProduction(t)
		require.Equal(t, 2, run.VideosDiscovered, "both videos must be persisted before detection")
		require.Equal(t, 2, run.VideosAccepted, "the jazz route accepts both natively observed videos")

		// Native persistence: both observations belong to UC_native, and the
		// imported association never became a native observation.
		require.Equal(t, 2, comp.countRows(t,
			`SELECT COUNT(*) FROM video WHERE channel_id = ?;`, nativeChannelID))
		require.Zero(t, comp.countRows(t,
			`SELECT COUNT(*) FROM video WHERE channel_id = ?;`, importedChannelID),
			"the import association must not appear as a native observation")

		// ADR-013: the native same-ID write left exactly one fingerprint row
		// that is still an eligible matching candidate. The exact-match
		// assertion below is the timing-precise proof — the exact pass filters
		// on is_duplicate_reference = 1, so it can only be produced while the
		// imported row was eligible after the native write and before
		// detection. This post-run flag read also holds when the row was
		// re-promoted by route acceptance, so it corroborates rather than
		// carries that claim.
		require.Equal(t, 1, comp.countRows(t,
			`SELECT COUNT(*) FROM title_fingerprint WHERE video_id = ?;`, importedID),
			"native same-ID persistence must not create a second fingerprint row")
		require.Equal(t, 1, comp.countRows(t,
			`SELECT COUNT(*) FROM title_fingerprint WHERE video_id = ? AND is_duplicate_reference = 1;`,
			importedID),
			"the imported reference must be eligible after the composed run")

		// OC-004 guarantee 2/4: the same-ID observation is not its own duplicate.
		require.Zero(t, comp.countRows(t,
			`SELECT COUNT(*) FROM duplicate_match dm
			   INNER JOIN video v ON v.id = dm.video_id
			  WHERE v.video_id = ?;`, importedID),
			"the same-ID native observation must not be recorded as a duplicate of itself")

		// OC-004 guarantee 1/ADR-013 §6: a distinct video still matches it.
		comp.assertDistinctMatchRecordsImportedReference(t, matchingID)

		// Stage 4: real stats service output over these real rows.
		comp.assertChronologyCredit(t)
	})

	// The missing-evidence case: the imported reference never gets a native
	// video row, so its fingerprint channel association must not stand in for
	// chronology (ADR-014 §3 — the direct falsification of attributing credit
	// from title_fingerprint.channel_id).
	t.Run("imported reference without a native observation yields no chronology credit", func(t *testing.T) {
		ctx := context.Background()
		const title = "Late Night Jazz Reprise"
		const importedID = "vid_imported_no_native_row"
		const matchingID = "vid_native_cover"

		comp := setupImportedCorpus(t, importedID, matchingID, title, false)
		comp.assertImportedReferenceOnly(t)

		run := comp.runProduction(t)
		require.Equal(t, 1, run.VideosDiscovered)

		// Precondition: the matched reference has no native video record.
		require.Zero(t, comp.countRows(t,
			`SELECT COUNT(*) FROM video WHERE video_id = ?;`, importedID),
			"this case requires an import-only reference with no native observation")

		comp.assertDistinctMatchRecordsImportedReference(t, matchingID)

		// The imported association is still what the single fingerprint row
		// carries — present, eligible, and still not credit.
		var assocChannel, assocSource string
		var isRef int
		require.NoError(t, comp.env.db.QueryRowContext(ctx,
			`SELECT channel_id, source, is_duplicate_reference
			   FROM title_fingerprint WHERE video_id = ?;`, importedID).
			Scan(&assocChannel, &assocSource, &isRef))
		require.Equal(t, importedChannelID, assocChannel)
		require.Equal(t, "user_import", assocSource)
		require.Equal(t, 1, isRef)

		comp.assertNoChronologyCredit(t)
	})

	// The unorderable-evidence case: equal native discovery times are not
	// chronology, so neither side earns credit (ADR-014 §3).
	t.Run("equal native discovery timestamps yield no chronology credit", func(t *testing.T) {
		ctx := context.Background()
		const title = "Evening Jazz Quartet"
		const importedID = "vid_imported_equal_times"
		const matchingID = "vid_native_equal_times"

		comp := setupImportedCorpus(t, importedID, matchingID, title, true)
		comp.assertImportedReferenceOnly(t)

		run := comp.runProduction(t)
		require.Equal(t, 2, run.VideosDiscovered)
		comp.assertDistinctMatchRecordsImportedReference(t, matchingID)

		// Baseline: the composed path wrote strictly ordered native times, so
		// the pair is credited from chronology.
		comp.assertChronologyCredit(t)

		// Equal stored discovery times are reachable when two native writes
		// land in the same stored-timestamp granularity; normalize both
		// observations produced by the real path to one identical instant.
		res, err := comp.env.db.ExecContext(ctx,
			`UPDATE video SET discovered_at = ? WHERE video_id IN (?, ?);`,
			equalDiscoveredAt, importedID, matchingID)
		require.NoError(t, err)
		updated, err := res.RowsAffected()
		require.NoError(t, err)
		require.Equal(t, int64(2), updated, "both native observations must share the equal timestamp")

		comp.assertNoChronologyCredit(t)
	})
}
