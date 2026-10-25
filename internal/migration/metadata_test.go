package migration_test

// Tests for the ADR-015 / ARS-002 durable completion marker written by the
// shared importer: migration 013's corpus_import_metadata singleton row
// advances only on a normal successful completion, in one transaction after
// every chunk committed and the final corpus count was read, and is readable
// after reopening the database.

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/stretchr/testify/require"
)

// readMarker returns the raw stored completion timestamp of the singleton
// metadata row. It fails the test when the row is absent or unreadable.
func readMarker(t *testing.T, db *sql.DB) sql.NullString {
	t.Helper()
	var stored sql.NullString
	err := db.QueryRowContext(context.Background(),
		`SELECT last_successful_corpus_import_at FROM corpus_import_metadata WHERE singleton_id = 1;`).
		Scan(&stored)
	require.NoError(t, err)
	return stored
}

// requireMarkerParses asserts the stored value is canonical RFC 3339 Nano in
// UTC and returns it parsed.
func requireMarkerParses(t *testing.T, stored string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, stored)
	require.NoError(t, err, "stored marker must be RFC 3339 Nano: %q", stored)
	require.Equal(t, parsed.UTC().Format(time.RFC3339Nano), stored,
		"stored marker must be the canonical UTC representation")
	require.True(t, strings.HasSuffix(stored, "Z"), "marker must be UTC: %q", stored)
	return parsed
}

// requireMarkerUnchanged asserts the stored marker still equals want exactly.
func requireMarkerUnchanged(t *testing.T, db *sql.DB, want sql.NullString) {
	t.Helper()
	got := readMarker(t, db)
	require.Equal(t, want, got, "the previous completion timestamp must stand")
}

// successfulMarker runs one successful import and returns its stored marker,
// establishing the prior value the non-completion scenarios must preserve.
func successfulMarker(t *testing.T, src *sql.DB, schema *migration.SourceSchema, dest *sql.DB, batch string) sql.NullString {
	t.Helper()
	p, err := migration.NewImporterWithChunkSize(src, dest, batch, 2).
		RunChunked(context.Background(), schema, nil)
	require.NoError(t, err)
	require.True(t, p.Completed)
	stored := readMarker(t, dest)
	require.True(t, stored.Valid, "a completed import must record a timestamp")
	requireMarkerParses(t, stored.String)
	return stored
}

func TestRunChunkedCompletionTimestampAdvancesOnSuccess(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES
			('v1', 'Jazz Live Session'),
			('v2', 'Rock Anthem');`)
	dest := openDestDB(t)

	stored := readMarker(t, dest)
	require.False(t, stored.Valid,
		"a fresh destination starts with a NULL completion timestamp")

	before := time.Now().UTC().Add(-time.Second)
	imp := migration.NewImporterWithChunkSize(src, dest, "batch-marker", 1)
	p, err := imp.RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.True(t, p.Completed)

	stored = readMarker(t, dest)
	require.True(t, stored.Valid, "a completed import must commit the success marker")
	parsed := requireMarkerParses(t, stored.String)
	after := time.Now().UTC().Add(time.Second)
	require.False(t, parsed.Before(before), "marker %v predates the import", parsed)
	require.False(t, parsed.After(after), "marker %v postdates the import", parsed)

	// A second successful import advances the marker over the prior value.
	prior := parsed
	p2, err := migration.NewImporterWithChunkSize(src, dest, "batch-marker-2", 1).
		RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.True(t, p2.Completed)
	stored2 := readMarker(t, dest)
	require.True(t, stored2.Valid)
	require.False(t, requireMarkerParses(t, stored2.String).Before(prior),
		"a later successful import must not move the timestamp backwards")
}

func TestCompletedImportVariantsAdvanceTimestamp(t *testing.T) {
	ctx := context.Background()

	t.Run("all rows skipped as already present", func(t *testing.T) {
		src, schema := seedSource(t,
			`INSERT INTO videos (video_id, title) VALUES
				('v1', 'Jazz Live Session'),
				('v2', 'Rock Anthem');`)
		dest := openDestDB(t)
		_, err := dest.ExecContext(ctx,
			`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, source, is_duplicate_reference)
			 VALUES ('v1', 'fp1', 'tok1', 'user_import', 1), ('v2', 'fp2', 'tok2', 'user_import', 1);`)
		require.NoError(t, err)

		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-skips", 1).
			RunChunked(ctx, schema, nil)
		require.NoError(t, err)
		require.True(t, p.Completed)
		require.Zero(t, p.Inserted)
		require.Equal(t, 2, p.Skipped)
		stored := readMarker(t, dest)
		require.True(t, stored.Valid,
			"an all-skipped import is a normal completion and records a timestamp")
		requireMarkerParses(t, stored.String)
	})

	t.Run("empty valid source", func(t *testing.T) {
		src, schema := seedSource(t)
		dest := openDestDB(t)

		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-empty", 1).
			RunChunked(ctx, schema, nil)
		require.NoError(t, err)
		require.True(t, p.Completed)
		require.Zero(t, p.Processed)
		stored := readMarker(t, dest)
		require.True(t, stored.Valid,
			"an import with zero rows is a normal completion and records a timestamp")
		requireMarkerParses(t, stored.String)
	})

	t.Run("reason-accounted row errors", func(t *testing.T) {
		src, schema := seedSource(t,
			`INSERT INTO videos (video_id, title) VALUES
				('v1', 'Jazz Live Session'),
				('',   'Empty Identifier'),
				('v2', NULL);`)
		dest := openDestDB(t)

		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-rowerr", 1).
			RunChunked(ctx, schema, nil)
		require.NoError(t, err)
		require.True(t, p.Completed)
		require.Equal(t, 2, p.Errors, "reason-accounted row errors are expected here")
		stored := readMarker(t, dest)
		require.True(t, stored.Valid,
			"reason-accounted row errors do not prevent the completion marker")
		requireMarkerParses(t, stored.String)
	})
}

func TestNonCompletedImportsKeepPreviousTimestamp(t *testing.T) {
	ctx := context.Background()
	// Each subtest first records a successful marker, then exercises one
	// non-completion outcome and proves the prior timestamp stands.
	seed := `INSERT INTO videos (video_id, title) VALUES
		('v1', 'Jazz Live Session'),
		('v2', 'Rock Anthem'),
		('v3', 'Blues Groove'),
		('v4', 'Soul classics mix'),
		('v5', 'Ambient Waves');`

	t.Run("cancelled at a committed chunk boundary", func(t *testing.T) {
		src, schema := seedSource(t, seed)
		dest := openDestDB(t)
		prior := successfulMarker(t, src, schema, dest, "batch-prior")

		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-cancel", 2).
			RunChunked(cancelCtx, schema, func(s migration.Progress) {
				if s.Processed == 2 {
					cancel()
				}
			})
		require.ErrorIs(t, err, context.Canceled)
		require.True(t, p.Cancelled)
		require.False(t, p.Completed)
		requireMarkerUnchanged(t, dest, prior)
	})

	t.Run("cancelled after the final committed chunk but before the marker", func(t *testing.T) {
		src, schema := seedSource(t, seed)
		dest := openDestDB(t)
		prior := successfulMarker(t, src, schema, dest, "batch-prior")

		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		// The last report callback fires after the final chunk commit and
		// before finalization: cancellation there must win the race, leaving
		// the previous marker and a cancelled (not completed) result.
		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-late-cancel", 2).
			RunChunked(cancelCtx, schema, func(s migration.Progress) {
				if s.Processed == s.Total {
					cancel()
				}
			})
		require.ErrorIs(t, err, context.Canceled)
		require.True(t, p.Cancelled)
		require.False(t, p.Completed)
		require.Equal(t, 5, p.Processed, "every chunk stayed committed")
		requireMarkerUnchanged(t, dest, prior)
	})

	t.Run("fatal source failure", func(t *testing.T) {
		src, schema := seedSource(t,
			`INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
		dest := openDestDB(t)
		prior := successfulMarker(t, src, schema, dest, "batch-prior")

		_, err := src.ExecContext(ctx, "DROP TABLE videos;")
		require.NoError(t, err)
		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-fatal", 1).
			RunChunked(ctx, schema, nil)
		require.Error(t, err)
		require.False(t, p.Completed)
		requireMarkerUnchanged(t, dest, prior)
	})

	t.Run("final corpus count failure", func(t *testing.T) {
		src, schema := seedSource(t, seed)
		dest := openDestDB(t)
		prior := successfulMarker(t, src, schema, dest, "batch-prior")

		// Break the destination count after the last chunk commits but
		// before the marker step: the operation fails even though every chunk
		// is committed, and the timestamp stands.
		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-countfail", 2).
			RunChunked(ctx, schema, func(s migration.Progress) {
				if s.Processed == s.Total {
					_, e := dest.ExecContext(ctx,
						"ALTER TABLE title_fingerprint RENAME TO title_fingerprint_hidden;")
					require.NoError(t, e)
				}
			})
		require.Error(t, err)
		require.False(t, p.Cancelled)
		require.False(t, p.Completed)
		_, err = dest.ExecContext(ctx,
			"ALTER TABLE title_fingerprint_hidden RENAME TO title_fingerprint;")
		require.NoError(t, err)
		requireMarkerUnchanged(t, dest, prior)
	})

	t.Run("metadata write failure", func(t *testing.T) {
		src, schema := seedSource(t, seed)
		dest := openDestDB(t)
		prior := successfulMarker(t, src, schema, dest, "batch-prior")

		// The success marker transaction fails after every chunk committed:
		// the update rolls back, the prior value stands, and the operation is
		// a job-level failure, not a completion.
		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-markerfail", 2).
			RunChunked(ctx, schema, func(s migration.Progress) {
				if s.Processed == s.Total {
					_, e := dest.ExecContext(ctx,
						"ALTER TABLE corpus_import_metadata RENAME TO corpus_import_metadata_hidden;")
					require.NoError(t, e)
				}
			})
		require.Error(t, err)
		require.False(t, p.Cancelled)
		require.False(t, p.Completed)
		_, err = dest.ExecContext(ctx,
			"ALTER TABLE corpus_import_metadata_hidden RENAME TO corpus_import_metadata;")
		require.NoError(t, err)
		requireMarkerUnchanged(t, dest, prior)
	})

	t.Run("missing singleton row", func(t *testing.T) {
		src, schema := seedSource(t,
			`INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
		dest := openDestDB(t)
		_, err := dest.ExecContext(ctx,
			"DELETE FROM corpus_import_metadata WHERE singleton_id = 1;")
		require.NoError(t, err)

		// A marker update that cannot reach exactly the singleton row is a
		// failure, never a silent success.
		p, err := migration.NewImporterWithChunkSize(src, dest, "batch-norow", 1).
			RunChunked(ctx, schema, nil)
		require.Error(t, err)
		require.False(t, p.Completed)
		require.ErrorContains(t, err, "singleton metadata row")
	})
}

func TestRunRecordsCompletionTimestamp(t *testing.T) {
	// The CLI wrapper (cmd/migrate) calls Importer.Run; it shares the same
	// successful-completion boundary as the web manager's RunChunked.
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
	dest := openDestDB(t)

	before := time.Now().UTC().Add(-time.Second)
	result, err := migration.NewImporter(src, dest, "batch-cli").Run(ctx, schema)
	require.NoError(t, err)
	require.Equal(t, 1, result.Inserted)

	stored := readMarker(t, dest)
	require.True(t, stored.Valid, "the CLI path must record the completion timestamp")
	parsed := requireMarkerParses(t, stored.String)
	require.False(t, parsed.Before(before))
	require.False(t, parsed.After(time.Now().UTC().Add(time.Second)))
}

func TestCompletionTimestampSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
	dest, destPath := openDestDBFile(t)

	p, err := migration.NewImporterWithChunkSize(src, dest, "batch-durable", 1).
		RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.True(t, p.Completed)
	stored := readMarker(t, dest)
	require.True(t, stored.Valid)
	require.NoError(t, dest.Close())

	reopened, err := sql.Open("sqlite", destPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	again := readMarker(t, reopened)
	require.Equal(t, stored, again,
		"the completion timestamp must survive closing and reopening the database")
	requireMarkerParses(t, again.String)
}

func TestCompletionTimestampStoresNoSourceContent(t *testing.T) {
	ctx := context.Background()
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES ('v1', 'Jazz Live Session');`)
	dest := openDestDB(t)

	p, err := migration.NewImporterWithChunkSize(src, dest, "batch-privacy", 1).
		RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.True(t, p.Completed)

	// The metadata row carries exactly one timestamp value: no title,
	// filename, job id, counts, or history.
	stored := readMarker(t, dest)
	require.True(t, stored.Valid)
	requireMarkerParses(t, stored.String)

	var cols []string
	rows, err := dest.QueryContext(ctx,
		`SELECT name FROM pragma_table_info('corpus_import_metadata') ORDER BY cid;`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		cols = append(cols, name)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"singleton_id", "last_successful_corpus_import_at"}, cols)

	var rowCount int
	require.NoError(t, dest.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM corpus_import_metadata;").Scan(&rowCount))
	require.Equal(t, 1, rowCount, "no import history is retained")
}

// TestCompletionTimestampPrivacyScan proves neither the raw source title nor
// the source database path is stored anywhere in the destination metadata
// table.
func TestCompletionTimestampPrivacyScan(t *testing.T) {
	ctx := context.Background()
	rawTitle := "Zebra Uncommon Title Marker"
	src, schema := seedSource(t,
		`INSERT INTO videos (video_id, title) VALUES ('v1', 'Zebra Uncommon Title Marker');`)
	dest := openDestDB(t)

	p, err := migration.NewImporterWithChunkSize(src, dest, "batch-privacy-scan", 1).
		RunChunked(ctx, schema, nil)
	require.NoError(t, err)
	require.True(t, p.Completed)

	var hits int
	require.NoError(t, dest.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM corpus_import_metadata
		 WHERE COALESCE(last_successful_corpus_import_at, '') LIKE ?;`,
		"%"+rawTitle+"%",
	).Scan(&hits))
	require.Zero(t, hits, "the metadata row must not contain raw source content")

	var markerCount int
	require.NoError(t, dest.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM corpus_import_metadata WHERE singleton_id <> 1;`).Scan(&markerCount))
	require.Zero(t, markerCount, "only the singleton row is valid")
}
