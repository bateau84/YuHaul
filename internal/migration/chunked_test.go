package migration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/stretchr/testify/require"
)

// seedSource creates the discoverable source table, applies the given raw
// INSERT statements, and returns the discovered schema.
func seedSource(t *testing.T, inserts ...string) (*sql.DB, *migration.SourceSchema) {
	t.Helper()
	ctx := context.Background()
	src := openSourceDB(t)
	_, err := src.ExecContext(ctx, `CREATE TABLE videos (video_id TEXT, title TEXT, channel_id TEXT);`)
	require.NoError(t, err)
	for _, stmt := range inserts {
		_, err := src.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	schema, err := migration.DiscoverSchema(ctx, src)
	require.NoError(t, err)
	return src, schema
}

// requireReconciled asserts the ARS-002 progress invariants: committed
// accounting always balances and never exceeds the preflight total.
func requireReconciled(t *testing.T, p migration.Progress) {
	t.Helper()
	require.Equal(t, p.Inserted+p.Skipped+p.Errors, p.Processed,
		"processed must equal inserted+skipped+errors")
	require.LessOrEqual(t, p.Processed, p.Total,
		"processed must not exceed the preflight source total")
	reasonSum := 0
	for _, n := range p.ErrorCountsByReason {
		require.GreaterOrEqual(t, n, 0)
		reasonSum += n
	}
	require.Equal(t, p.Errors, reasonSum, "reason counts must sum to errors")
}

func destCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM title_fingerprint;").Scan(&n))
	return n
}

// maxVideoIDMultiplicity is the largest number of fingerprint rows sharing one
// video_id; it is 1 when no video_id is duplicated.
func maxVideoIDMultiplicity(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT COALESCE(MAX(c), 0) FROM (SELECT COUNT(*) AS c FROM title_fingerprint GROUP BY video_id);").Scan(&n))
	return n
}

func processedOf(snapshots []migration.Progress) []int {
	out := make([]int, len(snapshots))
	for i, s := range snapshots {
		out[i] = s.Processed
	}
	return out
}

func TestRunChunkedCommitsBoundedChunks(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('v2', 'Rock Anthem (Official Video)'),
			('v3', 'Blues Groove'),
			('',   'Empty Identifier'),
			('v4', 'Soul classics mix'),
			('v5', 'Ambient Waves');`)
	dest := openDestDB(t)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-chunks", 2)
	var snapshots []migration.Progress
	p, err := imp.RunChunked(ctx, schema, func(s migration.Progress) {
		snapshots = append(snapshots, s)
	})
	require.NoError(t, err)

	require.True(t, p.Completed)
	require.False(t, p.Cancelled)
	require.Equal(t, 6, p.Total)
	require.Equal(t, 6, p.Processed)
	require.Equal(t, 5, p.Inserted)
	require.Zero(t, p.Skipped)
	require.Equal(t, 1, p.Errors)
	require.Equal(t, 1, p.ErrorCountsByReason[migration.ReasonEmptyVideoID])
	requireReconciled(t, p)
	require.NotNil(t, p.TotalCorpusSize)
	require.EqualValues(t, 5, *p.TotalCorpusSize)

	// One snapshot per committed chunk; snapshots carry committed rows only
	// and no terminal corpus size.
	require.Equal(t, []int{2, 4, 6}, processedOf(snapshots))
	for _, s := range snapshots {
		requireReconciled(t, s)
		require.Nil(t, s.TotalCorpusSize)
	}

	require.Equal(t, 5, destCount(t, dest))
	require.Equal(t, 1, maxVideoIDMultiplicity(t, dest))

	// The chunked path keeps the OC-004 row contract on every insert.
	got := readFingerprint(t, ctx, dest, "v1")
	require.Equal(t, "user_import", got.source)
	require.Equal(t, "batch-chunks", got.importBatchID.String)
	require.Equal(t, 1, got.isDuplicateRef)
}

func TestRunChunkedCancellationAfterCommittedChunk(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('v2', 'Rock Anthem'),
			('v3', 'Blues Groove'),
			('v4', 'Soul classics mix'),
			('v5', 'Ambient Waves');`)
	dest := openDestDB(t)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-cancel", 2)
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := imp.RunChunked(cancelCtx, schema, func(s migration.Progress) {
		if s.Processed == 2 {
			cancel() // deterministic cancellation at a committed boundary
		}
	})
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, p.Cancelled)
	require.False(t, p.Completed)
	require.Equal(t, 2, p.Processed)
	require.Equal(t, 2, p.Inserted)
	require.Zero(t, p.Skipped)
	require.Zero(t, p.Errors)
	requireReconciled(t, p)
	require.NotNil(t, p.TotalCorpusSize,
		"a cancelled import still reports its durable corpus size")
	require.EqualValues(t, 2, *p.TotalCorpusSize)

	// The committed chunk survived; nothing beyond it was written.
	require.Equal(t, 2, destCount(t, dest))

	// Append retry with the same source completes the corpus with no
	// duplicate video IDs (BR-009 AC8).
	retry := migration.NewImporterWithChunkSize(src, dest, "batch-retry", 2)
	p2, err := retry.RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.True(t, p2.Completed)
	require.Equal(t, 5, p2.Total)
	require.Equal(t, 5, p2.Processed)
	require.Equal(t, 3, p2.Inserted)
	require.Equal(t, 2, p2.Skipped)
	requireReconciled(t, p2)
	require.Equal(t, 5, destCount(t, dest))
	require.Equal(t, 1, maxVideoIDMultiplicity(t, dest))
}

func TestRunChunkedRowErrorsReconcileByReason(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('',   'Empty Identifier'),
			('v2', NULL),
			('v3', '(Official Video)'),
			('v4', 'Rock Anthem (Official Video)');`)
	dest := openDestDB(t)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-reasons", 1)
	p, err := imp.RunChunked(ctx, schema, nil)
	require.NoError(t, err)

	require.True(t, p.Completed)
	require.Equal(t, 5, p.Total)
	require.Equal(t, 5, p.Processed)
	require.Equal(t, 2, p.Inserted)
	require.Equal(t, 3, p.Errors)
	require.Equal(t, map[string]int{
		migration.ReasonRowReadFailed:    1,
		migration.ReasonEmptyVideoID:     1,
		migration.ReasonEmptyFingerprint: 1,
	}, p.ErrorCountsByReason)
	requireReconciled(t, p)

	// Invalid rows never enter the corpus; valid rows around them do.
	rows, err := dest.QueryContext(ctx, "SELECT video_id FROM title_fingerprint ORDER BY video_id;")
	require.NoError(t, err)
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"v1", "v4"}, ids)
}

func TestRunChunkedInsertFailureIsRowLevel(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('vbad', 'Rejected by trigger'),
			('v2', 'Rock Anthem');`)
	dest := openDestDB(t)
	// A genuine per-row destination insert failure: the trigger aborts only
	// the offending statement, so the chunk must still commit its valid rows.
	_, err := dest.ExecContext(ctx, `
		CREATE TRIGGER reject_vbad BEFORE INSERT ON title_fingerprint
		WHEN NEW.video_id = 'vbad'
		BEGIN SELECT RAISE(ABORT, 'insert rejected'); END;`)
	require.NoError(t, err)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-insertfail", 2)
	p, err := imp.RunChunked(ctx, schema, nil)
	require.NoError(t, err)

	require.True(t, p.Completed)
	require.Equal(t, 3, p.Total)
	require.Equal(t, 3, p.Processed)
	require.Equal(t, 2, p.Inserted)
	require.Equal(t, 1, p.Errors)
	require.Equal(t, map[string]int{migration.ReasonFingerprintInsertFailed: 1},
		p.ErrorCountsByReason)
	requireReconciled(t, p)

	require.Equal(t, 2, destCount(t, dest))
	var rejected int
	require.NoError(t, dest.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM title_fingerprint WHERE video_id = 'vbad';").Scan(&rejected))
	require.Zero(t, rejected)
}

func TestRunChunkedDestinationLookupFailureIsRowLevel(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('v2', 'Rock Anthem'),
			('v3', 'Blues Groove');`)
	dest := openDestDB(t)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-lookupfail", 1)
	p, err := imp.RunChunked(ctx, schema, func(s migration.Progress) {
		switch s.Processed {
		case 1:
			// Break the destination identity table before the second chunk.
			_, e := dest.ExecContext(ctx,
				"ALTER TABLE title_fingerprint RENAME TO title_fingerprint_hidden;")
			require.NoError(t, e)
		case 2:
			_, e := dest.ExecContext(ctx,
				"ALTER TABLE title_fingerprint_hidden RENAME TO title_fingerprint;")
			require.NoError(t, e)
		}
	})
	require.NoError(t, err)

	require.True(t, p.Completed)
	require.Equal(t, 3, p.Processed)
	require.Equal(t, 2, p.Inserted)
	require.Equal(t, 1, p.Errors)
	require.Equal(t, map[string]int{migration.ReasonDestinationLookupFailed: 1},
		p.ErrorCountsByReason)
	requireReconciled(t, p)
	require.NotNil(t, p.TotalCorpusSize)
	require.EqualValues(t, 2, *p.TotalCorpusSize)
	require.Equal(t, 2, destCount(t, dest))
}

func TestRunChunkedAccountsDuplicateSourceIDs(t *testing.T) {
	ctx := context.Background()
	// v1 repeats inside the source, and two distinct video IDs share one
	// title: only same-ID repeats are skips; same titles are not (BR-009 AC8).
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('v2', 'Jazz Live Session'),
			('v1', 'Jazz Live Session'),
			('v1', 'Jazz Live Session');`)
	dest := openDestDB(t)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-dupes", 2)
	p, err := imp.RunChunked(ctx, schema, nil)
	require.NoError(t, err)

	require.True(t, p.Completed)
	require.Equal(t, 4, p.Total)
	require.Equal(t, 4, p.Processed)
	require.Equal(t, 2, p.Inserted,
		"distinct video IDs with the same title are both imported")
	require.Equal(t, 2, p.Skipped,
		"same-ID source repeats are skipped, not re-inserted")
	require.Zero(t, p.Errors)
	requireReconciled(t, p)
	require.Equal(t, 2, destCount(t, dest))
	require.Equal(t, 1, maxVideoIDMultiplicity(t, dest))
}

func TestRunChunkedRetryPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title, channel_id) VALUES
			('v1', 'Jazz Live Session', 'UC1'),
			('v2', 'Rock Anthem', 'UC2');`)
	dest := openDestDB(t)

	first := migration.NewImporterWithChunkSize(src, dest, "batch-first", 1)
	p1, err := first.RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.Equal(t, 2, p1.Inserted)

	before := []fingerprintRow{
		readFingerprint(t, ctx, dest, "v1"),
		readFingerprint(t, ctx, dest, "v2"),
	}

	retry := migration.NewImporterWithChunkSize(src, dest, "batch-retry", 1)
	p2, err := retry.RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.True(t, p2.Completed)
	require.Equal(t, 2, p2.Processed)
	require.Zero(t, p2.Inserted)
	require.Equal(t, 2, p2.Skipped)
	require.Zero(t, p2.Errors)
	requireReconciled(t, p2)

	require.Equal(t, before[0], readFingerprint(t, ctx, dest, "v1"),
		"a retry must leave committed rows byte-identical")
	require.Equal(t, before[1], readFingerprint(t, ctx, dest, "v2"),
		"a retry must leave committed rows byte-identical")
	require.Equal(t, 2, destCount(t, dest))
}

func TestRunChunkedEmptySource(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t) // discoverable table with no rows
	dest := openDestDB(t)
	_, err := dest.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, source, is_duplicate_reference)
		 VALUES ('native1', 'native fingerprint', 'native', 'api_discovery', 0);`)
	require.NoError(t, err)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-empty", 1)
	p, err := imp.RunChunked(ctx, schema, nil)
	require.NoError(t, err)

	require.True(t, p.Completed)
	require.Zero(t, p.Total)
	require.Zero(t, p.Processed)
	require.Equal(t, map[string]int{}, p.ErrorCountsByReason)
	requireReconciled(t, p)
	require.NotNil(t, p.TotalCorpusSize)
	require.EqualValues(t, 1, *p.TotalCorpusSize,
		"corpus size is the actual destination count, not an import-derived sum")
}

func TestRunChunkedRejectsInvalidSourceBeforeWrites(t *testing.T) {
	ctx := context.Background()
	dest := openDestDB(t)

	t.Run("nil schema", func(t *testing.T) {
		src := openSourceDB(t)
		imp := migration.NewImporterWithChunkSize(src, dest, "batch-nil", 1)
		p, err := imp.RunChunked(ctx, nil, nil)
		require.Error(t, err)
		require.False(t, p.Completed)
		require.Zero(t, p.Processed)
		require.Zero(t, destCount(t, dest))
	})

	t.Run("source table missing after discovery", func(t *testing.T) {
		src, schema := seedSource(t,
			`INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
		_, err := src.ExecContext(ctx, "DROP TABLE videos;")
		require.NoError(t, err)

		imp := migration.NewImporterWithChunkSize(src, dest, "batch-gone", 1)
		p, err := imp.RunChunked(ctx, schema, nil)
		require.Error(t, err)
		require.False(t, p.Cancelled)
		require.False(t, p.Completed)
		require.Zero(t, p.Processed)
		require.Zero(t, p.Total)
		require.Zero(t, destCount(t, dest),
			"an invalid source must not cause destination writes")
	})
}

func TestRunChunkedDestinationFailureKeepsCommittedChunks(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('v2', 'Rock Anthem'),
			('v3', 'Blues Groove');`)
	dest, destPath := openDestDBFile(t)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-dbfail", 2)
	p, err := imp.RunChunked(ctx, schema, func(s migration.Progress) {
		if s.Processed == 2 {
			require.NoError(t, dest.Close()) // database-level failure between chunks
		}
	})
	require.Error(t, err)
	require.False(t, p.Cancelled)
	require.False(t, p.Completed)
	require.Equal(t, 2, p.Processed, "only committed counters are reported")
	require.Equal(t, 2, p.Inserted)
	requireReconciled(t, p)
	require.Nil(t, p.TotalCorpusSize, "an unreadable destination yields a nil corpus size")

	// The committed chunk is durable across the failure.
	reopened, err := sql.Open("sqlite", destPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	require.Equal(t, 2, destCount(t, reopened))
}

func TestRunChunkedHonorsPreCancelledContext(t *testing.T) {
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
	dest := openDestDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-precancel", 1)
	p, err := imp.RunChunked(ctx, schema, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, p.Cancelled)
	require.Zero(t, p.Processed)
	require.Zero(t, destCount(t, dest))
}

func TestRunUsesChunkedAccounting(t *testing.T) {
	// The CLI Run path delegates to the chunked writer: identical input must
	// produce identical accounting through either entry point.
	ctx := context.Background()
	insertSQL := `INSERT INTO videos (video_id, title, channel_id) VALUES
		('v1', 'Jazz Live Session', 'UC1'),
		('v2', '(Official Video)', NULL),
		('',   'Empty Identifier', NULL),
		('v3', 'Rock Anthem', NULL);`

	srcCLI, schemaCLI := seedSource(t, insertSQL)
	destCLI := openDestDB(t)
	got, err := migration.NewImporter(srcCLI, destCLI, "batch-cli").Run(ctx, schemaCLI)
	require.NoError(t, err)

	srcWeb, schemaWeb := seedSource(t, insertSQL)
	destWeb := openDestDB(t)
	p, err := migration.NewImporterWithChunkSize(srcWeb, destWeb, "batch-web", 1).
		RunChunked(ctx, schemaWeb, nil)
	require.NoError(t, err)

	require.Equal(t, migration.ImportResult{
		Read:     p.Processed,
		Inserted: p.Inserted,
		Skipped:  p.Skipped,
		Errors:   p.Errors,
	}, got)
	require.Equal(t, 4, got.Read)
	require.Equal(t, 2, got.Inserted)
	require.Equal(t, 2, got.Errors) // empty video ID plus empty-normalizing title
	require.Equal(t, destCount(t, destCLI), destCount(t, destWeb))
}

func TestRunChunkedDoesNotPersistRawTitles(t *testing.T) {
	ctx := context.Background()
	rawTitle := "Jazz Live Session (Official) — MIXED Case"
	src, schema := seedSource(t,
		fmt.Sprintf(`INSERT INTO videos (video_id, title) VALUES ('v1', %q);`, rawTitle))
	dest := openDestDB(t)

	imp := migration.NewImporterWithChunkSize(src, dest, "batch-privacy", 1)
	p, err := imp.RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.Equal(t, 1, p.Inserted)

	var hits int
	require.NoError(t, dest.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM title_fingerprint
		 WHERE fingerprint LIKE ? OR fingerprint_tokens LIKE ?
		   OR COALESCE(channel_id, '') LIKE ? OR COALESCE(import_batch_id, '') LIKE ?;`,
		"%"+rawTitle+"%", "%"+rawTitle+"%", "%"+rawTitle+"%", "%"+rawTitle+"%",
	).Scan(&hits))
	require.Zero(t, hits, "raw source titles must not be persisted")

	var transient int
	require.NoError(t, dest.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM transient_metadata;").Scan(&transient))
	require.Zero(t, transient)
}
