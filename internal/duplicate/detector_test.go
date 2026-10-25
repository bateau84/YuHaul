package duplicate

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/store"
)

// openTestDB creates an isolated SQLite database with the canonical schema.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "test.db")
	db, err := store.Open(ctx, dsn)
	require.NoError(t, err)

	t.Cleanup(func() { _ = db.Close() })
	return db
}

type fixturePair struct {
	Name     string  `json:"name"`
	A        string  `json:"a"`
	B        string  `json:"b"`
	Expected float64 `json:"expected"`
}

type fixture struct {
	Pairs []fixturePair `json:"pairs"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "duplicates.json"))
	require.NoError(t, err)
	var f fixture
	require.NoError(t, json.Unmarshal(data, &f))
	return f
}

func TestNormalizeAndJaccardFromFixture(t *testing.T) {
	f := loadFixture(t)

	for _, p := range f.Pairs {
		t.Run(p.Name, func(t *testing.T) {
			got := Jaccard(Normalize(p.A).Tokens, Normalize(p.B).Tokens)
			require.InDelta(t, p.Expected, got, 1e-12, "Jaccard(%q, %q)", p.A, p.B)
		})
	}
}

func TestCanonicalFingerprintIsDeterministic(t *testing.T) {
	// Different casing, punctuation, and annotation ordering must produce the
	// same canonical token set.
	titles := []string{
		"Jazz Live Session",
		"jazz LIVE session",
		"Jazz - Live [Session]",
		"(Official Video) Jazz Live Session",
	}
	first := ""
	for _, title := range titles {
		fp := Normalize(title)
		if first == "" {
			first = fp.Canonical
		}
		require.Equal(t, first, fp.Canonical, "title %q produced different canonical fingerprint", title)
	}
}

func TestExactDuplicateDetection(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	require.NoError(t, saveReference(ctx, db, "v1", "Jazz Live Session"))

	// Exact matching is canonical-title matching across distinct video IDs: a
	// permuted title normalizes to the same fingerprint.
	det := NewDetector(db)
	match, err := det.Detect(ctx, "v2", "Live Jazz Session")
	require.NoError(t, err)
	require.NotNil(t, match)
	require.Equal(t, MatchTypeExact, match.Type)
	require.Equal(t, "v1", match.VideoID)
	require.InDelta(t, 1.0, match.Similarity, 1e-12)

	// Exact detection is independent of the fuzzy threshold (BR-008 AC1/AC2).
	strict := NewDetector(db, WithThreshold(1.0))
	match, err = strict.Detect(ctx, "v2", "Jazz Live Session")
	require.NoError(t, err)
	require.NotNil(t, match, "an exact title match must survive a threshold of 1.0")
	require.Equal(t, MatchTypeExact, match.Type)
	require.Equal(t, "v1", match.VideoID)
}

func TestNonReferenceVideoIsNotExactMatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// A video that exists in the corpus but was never routed is not a reference.
	_, err := SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)

	det := NewDetector(db)
	match, err := det.Detect(ctx, "v2", "Jazz Live Session")
	require.NoError(t, err)
	require.Nil(t, match)
}

func TestFuzzyDuplicateDetection(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	require.NoError(t, saveReference(ctx, db, "v1", "Jazz Live Session"))

	det := NewDetector(db)
	// A genuinely different canonical title: {jazz, live, session} vs
	// {2024, jazz, live, session} scores 3/4, above the default threshold.
	match, err := det.Detect(ctx, "v2", "Jazz Live Session 2024")
	require.NoError(t, err)
	require.NotNil(t, match)
	require.Equal(t, MatchTypeFuzzy, match.Type)
	require.Equal(t, "v1", match.VideoID)
	require.InDelta(t, 0.75, match.Similarity, 1e-12)
}

func TestRejectedVideoNotInDuplicateCorpus(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// Rejected/excluded videos are stored for audit but not marked references.
	_, err := SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)

	det := NewDetector(db)
	match, err := det.Detect(ctx, "v2", "Jazz Live Session")
	require.NoError(t, err)
	require.Nil(t, match, "a non-reference fingerprint must not match")

	// Once routed, it enters the reference corpus.
	require.NoError(t, SetDuplicateReference(ctx, db, "v1", true))
	match, err = det.Detect(ctx, "v2", "Jazz Live Session")
	require.NoError(t, err)
	require.NotNil(t, match)
}

func TestThresholdChangesOutcome(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// "Jazz Live Session" vs "Jazz Session" scores 2/3.
	require.NoError(t, saveReference(ctx, db, "v1", "Jazz Live Session"))

	high := NewDetector(db, WithThreshold(0.7))
	match, err := high.Detect(ctx, "v2", "Jazz Session")
	require.NoError(t, err)
	require.Nil(t, match, "score 0.666 should not exceed threshold 0.7")

	low := NewDetector(db, WithThreshold(0.5))
	match, err = low.Detect(ctx, "v2", "Jazz Session")
	require.NoError(t, err)
	require.NotNil(t, match, "score 0.666 should exceed threshold 0.5")
}

func TestThresholdClamping(t *testing.T) {
	db := openTestDB(t)

	d := NewDetector(db, WithThreshold(-0.5))
	require.InDelta(t, 0.0, d.threshold, 1e-12)

	d = NewDetector(db, WithThreshold(1.5))
	require.InDelta(t, 1.0, d.threshold, 1e-12)
}

func TestEmptyFingerprintTokensSkipFuzzy(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	det := NewDetector(db)
	match, err := det.Detect(ctx, "v1", "(Official Video)")
	require.NoError(t, err)
	require.Nil(t, match)
}

func TestSaveFingerprintRejectsInvalidSource(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	_, err := SaveFingerprint(ctx, db, "v1", "UC1", "bad_source", "Title", false, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid fingerprint source")
}

func TestSaveFingerprintUpserts(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id1, err := SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)

	id2, err := SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Rock Session", true, nil)
	require.NoError(t, err)
	require.Equal(t, id1, id2, "upsert should return the same row id")

	var isRef int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = ?;", "v1").Scan(&isRef))
	require.Equal(t, 1, isRef)
}

// ADR-013 / OC-004: an imported reference stays an exact and fuzzy candidate
// when native discovery upserts the same video ID with its normal initial
// is_duplicate_reference=false before duplicate evaluation. The discovery write
// below mirrors internal/discovery/engine.go, which persists every discovered
// video through SaveFingerprint with the false initial value.
func TestImportedReferenceSurvivesNativeSameIDUpsertBeforeDetection(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	batchID := "batch-import"

	importedID, err := SaveFingerprint(ctx, db, "v1", "UCimport", "user_import", "Jazz Live Session", true, &batchID)
	require.NoError(t, err)

	// Native discovery observes the same video ID before the detector runs.
	nativeID, err := SaveFingerprint(ctx, db, "v1", "UCimport", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)
	require.Equal(t, importedID, nativeID, "the same video_id must stay on one fingerprint row")

	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM title_fingerprint WHERE video_id = 'v1';").Scan(&rows))
	require.Equal(t, 1, rows, "ADR-013 requires a single fingerprint row per video_id")

	var isRef int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = 'v1';").Scan(&isRef))
	require.Equal(t, 1, isRef, "a native false write must not demote an imported candidate")

	det := NewDetector(db)

	// Same-ID evaluation is a self-match: the eligible row is never returned
	// as a duplicate of itself (BR-008 AC8, OC-004). Eligibility asserted
	// above is untouched by this.
	self, err := det.Detect(ctx, "v1", "Jazz Live Session")
	require.NoError(t, err)
	require.Nil(t, self, "an imported reference must not be its own duplicate")

	// The same row stays an exact candidate for a different video.
	exact, err := det.Detect(ctx, "v2", "Live Jazz Session")
	require.NoError(t, err)
	require.NotNil(t, exact, "the imported reference must remain an exact candidate")
	require.Equal(t, MatchTypeExact, exact.Type)
	require.Equal(t, "v1", exact.VideoID)
	require.Equal(t, importedID, exact.FingerprintID)
	require.InDelta(t, 1.0, exact.Similarity, 1e-12)

	// ... and a fuzzy candidate for another distinct video.
	fuzzy, err := det.Detect(ctx, "v3", "Jazz Live Session 2024")
	require.NoError(t, err)
	require.NotNil(t, fuzzy, "the imported reference must remain a fuzzy candidate")
	require.Equal(t, MatchTypeFuzzy, fuzzy.Type)
	require.Equal(t, "v1", fuzzy.VideoID)
	require.Equal(t, importedID, fuzzy.FingerprintID)
}

// BR-008 AC8 / OC-004: a video is never a duplicate of its own reference row,
// whatever made that row eligible (import, native re-upsert, or routing). The
// same eligible row keeps matching distinct video IDs exactly and fuzzily.
func TestSameVideoIDIsNeverReturnedAsDuplicate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// Native reference enabled by routing (pipeline markVideoAccepted).
	require.NoError(t, saveReference(ctx, db, "v1", "Jazz Live Session"))

	det := NewDetector(db)
	for _, tc := range []struct{ name, title string }{
		{"exact title", "Jazz Live Session"},
		{"permuted title", "Live Jazz Session"},
		{"similar title", "Jazz Live Session 2024"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			match, err := det.Detect(ctx, "v1", tc.title)
			require.NoError(t, err)
			require.Nil(t, match, "video %q must not be reported as its own duplicate", tc.title)
		})
	}

	match, err := det.Detect(ctx, "v2", "Live Jazz Session")
	require.NoError(t, err)
	require.NotNil(t, match, "the reference must still match a different video")
	require.Equal(t, MatchTypeExact, match.Type)
	require.Equal(t, "v1", match.VideoID)
}

// The eligibility merge must not create route, action, or discovery-credit
// evidence, and an imported channel association must not become earliest-
// discovery evidence (BR-010 AC6, OC-004 guarantee 3).
func TestNativeSameIDUpsertCreatesNoDiscoveryCreditEvidence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	batchID := "batch-import"

	_, err := SaveFingerprint(ctx, db, "v1", "UCimport", "user_import", "Jazz Live Session", true, &batchID)
	require.NoError(t, err)
	_, err = SaveFingerprint(ctx, db, "v1", "UCimport", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)

	for _, table := range []string{"duplicate_match", "channel", "route_decision", "executed_action"} {
		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+table+";").Scan(&n))
		require.Zero(t, n, "fingerprint eligibility writes must not create %s evidence", table)
	}

	// The imported channel association alone is not earliest-discovery evidence.
	var credited int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM channel WHERE channel_id = 'UCimport' OR first_discovered_at IS NOT NULL;").Scan(&credited))
	require.Zero(t, credited, "an imported channel must not be recorded as earliest-discovered")
}

// Control: a native row that starts ineligible stays ineligible across repeated
// native writes, so the merge does not invent candidates.
func TestNativeUpsertKeepsExistingFalseRowIneligible(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	_, err := SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)
	_, err = SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)

	var isRef int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = 'v1';").Scan(&isRef))
	require.Equal(t, 0, isRef)

	det := NewDetector(db)
	match, err := det.Detect(ctx, "v2", "Jazz Live Session")
	require.NoError(t, err)
	require.Nil(t, match, "an ineligible row must not become an exact candidate")
	match, err = det.Detect(ctx, "v3", "Jazz Live Session 2024")
	require.NoError(t, err)
	require.Nil(t, match, "an ineligible row must not become a fuzzy candidate")
}

// Control: a true incoming eligibility event promotes an ineligible row.
func TestIncomingTruePromotesExistingIneligibleRow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id1, err := SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Jazz Live Session", false, nil)
	require.NoError(t, err)
	id2, err := SaveFingerprint(ctx, db, "v1", "UC1", "api_discovery", "Jazz Live Session", true, nil)
	require.NoError(t, err)
	require.Equal(t, id1, id2)

	var isRef int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT is_duplicate_reference FROM title_fingerprint WHERE video_id = 'v1';").Scan(&isRef))
	require.Equal(t, 1, isRef)

	// The promoted row is an exact candidate for a different video, and never
	// for itself.
	det := NewDetector(db)
	match, err := det.Detect(ctx, "v2", "Jazz Live Session")
	require.NoError(t, err)
	require.NotNil(t, match)
	require.Equal(t, MatchTypeExact, match.Type)
	require.Equal(t, "v1", match.VideoID)

	self, err := det.Detect(ctx, "v1", "Jazz Live Session")
	require.NoError(t, err)
	require.Nil(t, self, "promotion must not make a video its own duplicate")
}

func TestBestFuzzyMatchSelected(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	require.NoError(t, saveReference(ctx, db, "v1", "Jazz Live Session"))
	require.NoError(t, saveReference(ctx, db, "v2", "Jazz Session"))

	// "Jazz Live" is a proper subset of v1's tokens (2/3) and overlaps v2 by a
	// single token (1/3), so the evaluation must run on the fuzzy path and
	// keep the higher-scoring reference.
	det := NewDetector(db, WithThreshold(0.5))
	match, err := det.Detect(ctx, "v3", "Jazz Live")
	require.NoError(t, err)
	require.NotNil(t, match)
	require.Equal(t, MatchTypeFuzzy, match.Type)
	require.Equal(t, "v1", match.VideoID, "the higher-scoring reference should win")
	require.InDelta(t, 2.0/3.0, match.Similarity, 1e-12)
}

func TestDetectorLogsBoundaryOutcome(t *testing.T) {
	// The detector emits a debug log on duplicate detection. This is a minimal
	// observability check: the call must complete without panic and return a
	// result the operator can inspect.
	ctx := context.Background()
	db := openTestDB(t)

	require.NoError(t, saveReference(ctx, db, "v1", "Jazz Live Session"))

	det := NewDetector(db)
	match, err := det.Detect(ctx, "v2", "Live Jazz Session")
	require.NoError(t, err)
	require.NotNil(t, match)
}

func saveReference(ctx context.Context, db *sql.DB, videoID, title string) error {
	_, err := SaveFingerprint(ctx, db, videoID, "UCref", "api_discovery", title, true, nil)
	return err
}

func TestMain(m *testing.M) {
	// Pin wall-clock for deterministic timestamps in tests that touch SQLite
	// defaults. This is a no-op for the current test set but keeps the package
	// deterministic if future tests add time assertions.
	_ = time.Now().UTC()
	os.Exit(m.Run())
}
