// Package migration implements the one-time historical corpus importer.
//
// It reads raw video IDs and titles from a source SQLite database, transforms
// each title into the canonical YuHaul fingerprint, and inserts the result into
// the destination database's title_fingerprint table with source='user_import',
// import_batch_id, and is_duplicate_reference=true so the row enters the shared
// exact/fuzzy duplicate candidate set. Raw titles are not retained in the
// destination.
//
// is_duplicate_reference is matching-candidate eligibility only. Import writes
// no video, route decision, playlist action, channel counter, or
// earliest-discovery evidence, and an optional source channel_id is stored as
// an association alone (OC-004, BR-010 AC6).
//
// Execution is chunked: RunChunked streams the source table and reconciles it
// in bounded destination transactions so that cancellation or a fatal failure
// preserves previously committed chunks (ADR-009). Every published counter
// describes only rows settled by committed transactions; the in-flight
// uncommitted chunk is never reported. Run delegates to the same path. Both
// entrypoints share one completion boundary: on normal completion the
// importer commits the durable success marker (migration 013's singleton
// corpus_import_metadata row, ADR-015), so the web job and the CLI wrapper
// advance the last-successful-import timestamp together.
//
// The package also provides the reusable server-side staging and preview
// capability behind the web import (preview.go): Stager copies an untrusted
// upload to a randomly named private temp file under a configurable size cap,
// and Preview opens it read-only, validates it with DiscoverSchema, and reports
// source row count, already-present video-ID count, and up to ten transient
// sample titles without mutating the destination. The HTTP handler and import
// job manager layer on top of these primitives in internal/corpusimport.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/fingerprint"

	_ "modernc.org/sqlite"
)

// ErrSchemaNotDiscoverable is returned when the source database does not
// contain exactly one table with the expected video_id + title columns.
var ErrSchemaNotDiscoverable = errors.New("migration: source database schema is not discoverable")

// Stable reason codes for row-level import errors. They are the allowed keys
// of Progress.ErrorCountsByReason; the reason counts of any Progress always
// sum to its Errors value (ARS-002 "error_counts_by_reason").
const (
	ReasonRowReadFailed           = "row_read_failed"
	ReasonEmptyVideoID            = "empty_video_id"
	ReasonEmptyFingerprint        = "empty_fingerprint"
	ReasonDestinationLookupFailed = "destination_lookup_failed"
	ReasonFingerprintInsertFailed = "fingerprint_insert_failed"
)

// DefaultChunkSize is the number of source rows reconciled per destination
// transaction when no explicit chunk size is given. The value is an
// implementation tuning detail and is not part of any public contract
// (ADR-009).
const DefaultChunkSize = 1000

// SourceSchema describes a discovered source table and its relevant columns.
type SourceSchema struct {
	TableName       string
	VideoIDColumn   string
	TitleColumn     string
	ChannelIDColumn string // empty if the source table has no channel_id column
}

// DiscoverSchema inspects sourceDB and returns the schema of the single table
// that contains both a video_id column and a title column. If zero or multiple
// tables match, it returns ErrSchemaNotDiscoverable.
func DiscoverSchema(ctx context.Context, sourceDB *sql.DB) (*SourceSchema, error) {
	rows, err := sourceDB.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name;")
	if err != nil {
		return nil, fmt.Errorf("migration: list source tables: %w", err)
	}
	defer rows.Close()

	var candidates []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("migration: scan table name: %w", err)
		}
		candidates = append(candidates, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migration: iterate table names: %w", err)
	}

	var matches []*SourceSchema
	for _, table := range candidates {
		schema, ok, err := inspectTable(ctx, sourceDB, table)
		if err != nil {
			return nil, err
		}
		if ok {
			matches = append(matches, schema)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w: no table with video_id and title columns found", ErrSchemaNotDiscoverable)
	case 1:
		return matches[0], nil
	default:
		var names []string
		for _, m := range matches {
			names = append(names, m.TableName)
		}
		return nil, fmt.Errorf("%w: multiple tables have video_id and title columns (%s)",
			ErrSchemaNotDiscoverable, strings.Join(names, ", "))
	}
}

func inspectTable(ctx context.Context, sourceDB *sql.DB, table string) (*SourceSchema, bool, error) {
	rows, err := sourceDB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q);", table))
	if err != nil {
		return nil, false, fmt.Errorf("migration: pragma table_info for %q: %w", table, err)
	}
	defer rows.Close()

	var videoIDCol, titleCol, channelIDCol string
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return nil, false, fmt.Errorf("migration: scan column info for %q: %w", table, err)
		}
		lower := strings.ToLower(name)
		key := strings.ReplaceAll(lower, "_", "")
		switch key {
		case "videoid":
			videoIDCol = name
		case "title":
			titleCol = name
		case "channelid":
			channelIDCol = name
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("migration: iterate column info for %q: %w", table, err)
	}

	if videoIDCol == "" || titleCol == "" {
		return nil, false, nil
	}

	return &SourceSchema{
		TableName:       table,
		VideoIDColumn:   videoIDCol,
		TitleColumn:     titleCol,
		ChannelIDColumn: channelIDCol,
	}, true, nil
}

// ImportResult summarizes a migration run.
type ImportResult struct {
	Read     int // rows read from the source table
	Inserted int // new fingerprints inserted into the destination
	Skipped  int // rows skipped because the video_id already existed
	Errors   int // rows that could not be processed (nil title, normalization empty, etc.)
}

// Total returns the total number of source rows processed.
func (r ImportResult) Total() int { return r.Read }

// Progress is a reconciled snapshot of a chunked import. Every counter
// describes only rows settled by committed destination transactions: at every
// snapshot Processed equals Inserted+Skipped+Errors and never exceeds Total.
// Snapshots handed to the progress callback are copies and are safe to retain.
type Progress struct {
	Total               int            // source-row count established before any destination write
	Processed           int            // rows reconciled by committed transactions
	Inserted            int            // new title_fingerprint rows
	Skipped             int            // rows whose video_id already existed
	Errors              int            // rows failed with an explicit reason below
	ErrorCountsByReason map[string]int // stable reason code -> count; sums to Errors
	Completed           bool           // the whole source was consumed and committed and the durable success marker committed
	Cancelled           bool           // cancellation observed at a transaction boundary

	// TotalCorpusSize is the actual COUNT(*) of the destination
	// title_fingerprint table after the terminal committed transaction. It is
	// nil for intermediate snapshots and when a terminal state cannot read the
	// destination (ARS-002 failed-state semantics).
	TotalCorpusSize *int64
}

// snapshot returns a copy whose reason map cannot alias the live import state.
func (p Progress) snapshot() Progress {
	reasons := make(map[string]int, len(p.ErrorCountsByReason))
	for reason, n := range p.ErrorCountsByReason {
		reasons[reason] = n
	}
	p.ErrorCountsByReason = reasons
	return p
}

// chunkCounters accumulate one chunk's reconciled rows. They are applied to
// the published Progress only after the chunk's transaction commits.
type chunkCounters struct {
	inserted int
	skipped  int
	errors   int
	reasons  map[string]int
}

func (c *chunkCounters) fail(reason string) {
	if c.reasons == nil {
		c.reasons = make(map[string]int, 1)
	}
	c.errors++
	c.reasons[reason]++
}

// applyChunk folds one committed chunk into the published progress.
func (p *Progress) applyChunk(rows int, c chunkCounters) {
	p.Processed += rows
	p.Inserted += c.inserted
	p.Skipped += c.skipped
	p.Errors += c.errors
	if len(c.reasons) == 0 {
		return
	}
	if p.ErrorCountsByReason == nil {
		p.ErrorCountsByReason = make(map[string]int, len(c.reasons))
	}
	for reason, n := range c.reasons {
		p.ErrorCountsByReason[reason] += n
	}
}

// Importer migrates raw titles from a source DB into destination fingerprints.
// An Importer value must not be used concurrently.
type Importer struct {
	source    *sql.DB
	dest      *sql.DB
	batchID   string
	chunkSize int
}

// NewImporter creates an importer using DefaultChunkSize. batchID is stored in
// each imported title_fingerprint.import_batch_id row; each newly inserted row
// is also marked is_duplicate_reference=true so it participates in
// exact/fuzzy matching.
func NewImporter(source, dest *sql.DB, batchID string) *Importer {
	return NewImporterWithChunkSize(source, dest, batchID, DefaultChunkSize)
}

// NewImporterWithChunkSize creates an importer that commits at most chunkSize
// source rows per destination transaction. A chunkSize <= 0 selects
// DefaultChunkSize.
func NewImporterWithChunkSize(source, dest *sql.DB, batchID string, chunkSize int) *Importer {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	return &Importer{
		source:    source,
		dest:      dest,
		batchID:   batchID,
		chunkSize: chunkSize,
	}
}

// Run executes the import in bounded committed chunks and returns the
// aggregate accounting. It is append-only and idempotent: a source row whose
// video_id already exists is counted as skipped and that existing row is not
// written at all (BR-009 AC8). Only newly inserted valid rows are added, each
// as an eligible duplicate reference. On cancellation or a fatal failure it
// returns the committed counters together with the error; RunChunked documents
// the full contract.
func (i *Importer) Run(ctx context.Context, schema *SourceSchema) (ImportResult, error) {
	p, err := i.RunChunked(ctx, schema, nil)
	result := ImportResult{
		Read:     p.Processed,
		Inserted: p.Inserted,
		Skipped:  p.Skipped,
		Errors:   p.Errors,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// RunChunked executes the import with bounded commits. It first establishes
// the complete source-row count (Total) before any destination write, then
// streams the source table in chunks of at most chunkSize rows, committing
// each chunk in one destination transaction.
//
// Error and cancellation semantics:
//
//   - report, when non-nil, is invoked after every committed chunk with a
//     snapshot of committed counters only; the in-flight chunk is never
//     reported.
//   - When ctx is done, the in-flight uncommitted transaction is rolled back,
//     previously committed chunks are preserved, the returned Progress has
//     Cancelled=true with committed counters, and the returned error wraps the
//     context error.
//   - A source-read, transaction, begin, or commit failure fails the
//     operation: the returned error describes the cause, the in-flight chunk
//     is rolled back, and the returned counters cover only committed rows.
//     Prior committed chunks remain durable. A fatal Progress has Cancelled
//     and Completed both false.
//   - Row-level invalid records increment Errors with an explicit reason code,
//     are skipped, and do not stop other rows or their chunk's commit.
//   - A successful return has Completed=true, Cancelled=false, and a non-nil
//     TotalCorpusSize equal to the actual destination COUNT(*). On
//     cancellation or failure the corpus size is read best-effort and may be
//     nil when the destination is unreadable.
//   - A successful return also commits the durable success marker
//     (corpus_import_metadata.last_successful_corpus_import_at, ADR-015)
//     before Completed is set. A marker write/commit failure fails the
//     operation and keeps the previous timestamp; a cancellation that takes
//     effect before the marker commit yields a cancelled result. This applies
//     to every entrypoint because Importer.Run delegates here.
//
// The importer does not log and does not persist raw source titles; source
// rows are held in memory only for the current chunk.
func (i *Importer) RunChunked(ctx context.Context, schema *SourceSchema, report func(Progress)) (Progress, error) {
	if schema == nil {
		return Progress{}, errors.New("migration: schema is required")
	}

	p := Progress{ErrorCountsByReason: map[string]int{}}

	total, err := i.countSourceRows(ctx, schema)
	if err != nil {
		if cErr := ctx.Err(); cErr != nil {
			return i.cancelledResult(p, ctx, cErr)
		}
		return p, fmt.Errorf("migration: count source rows in %q: %w", schema.TableName, err)
	}
	p.Total = total

	rows, err := i.source.QueryContext(ctx, selectQuery(schema))
	if err != nil {
		if cErr := ctx.Err(); cErr != nil {
			return i.cancelledResult(p, ctx, cErr)
		}
		return p, fmt.Errorf("migration: query source table %q: %w", schema.TableName, err)
	}
	defer rows.Close()

	pending := make([]sourceEntry, 0, min(i.chunkSize, 1024))
	for {
		if cErr := ctx.Err(); cErr != nil {
			return i.cancelledResult(p, ctx, cErr)
		}

		pending = pending[:0]
		for len(pending) < i.chunkSize && rows.Next() {
			pending = append(pending, scanSourceEntry(rows, schema))
		}
		if len(pending) == 0 {
			break
		}

		if err := i.commitChunk(ctx, pending, &p); err != nil {
			if cErr := ctx.Err(); cErr != nil {
				return i.cancelledResult(p, ctx, cErr)
			}
			_ = i.attachCorpusSize(&p, ctx)
			return p, err
		}
		if report != nil {
			report(p.snapshot())
		}
	}

	if err := rows.Err(); err != nil {
		if cErr := ctx.Err(); cErr != nil {
			return i.cancelledResult(p, ctx, cErr)
		}
		_ = i.attachCorpusSize(&p, ctx)
		return p, fmt.Errorf("migration: iterate source rows: %w", err)
	}

	if err := i.attachCorpusSize(&p, ctx); err != nil {
		// Without a readable actual destination count the terminal result
		// cannot satisfy the completion contract, so the operation fails even
		// though every chunk committed.
		return p, fmt.Errorf("migration: %w", err)
	}

	if err := i.recordSuccessfulImport(ctx); err != nil {
		if cErr := ctx.Err(); cErr != nil {
			// Cancellation took effect before the marker transaction could
			// commit, so it wins the race: the previous marker value stands
			// and the operation is cancelled, not completed (ADR-015).
			return i.cancelledResult(p, ctx, cErr)
		}
		// The durable success marker is part of a normal successful
		// completion. A metadata write/commit failure rolls back its
		// transaction, keeps the previous timestamp, and fails the operation
		// at the job level even though every corpus chunk is committed
		// (ADR-015 / ARS-002 "Failure behavior").
		return p, fmt.Errorf("migration: record successful import timestamp: %w", err)
	}
	p.Completed = true
	return p, nil
}

// recordSuccessfulImport commits the durable success marker of a fully
// reconciled import: it sets the singleton corpus_import_metadata row's
// last_successful_corpus_import_at to the YuHaul wall-clock time captured
// immediately before the update, in UTC, as canonical RFC 3339 Nano text
// (ADR-015 / ARS-002 "Update and completion boundary").
//
// It runs only after the whole source was consumed, every chunk committed,
// and the final destination corpus count was read. The marker transaction
// commit is the durable success boundary: a begin/update/commit failure
// rolls back, leaves the previous timestamp unchanged, and fails the
// operation. ctx bounds the transaction, so a cancellation that arrives
// before the commit aborts the marker write and the caller reports the
// cancellation; a commit that wins makes the cancellation too late.
func (i *Importer) recordSuccessfulImport(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := i.dest.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import metadata transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The singleton row is the only valid target (CHECK (singleton_id = 1)
	// in migration 013); anything other than exactly one affected row means
	// the marker was not recorded and must not be reported as success.
	res, err := tx.ExecContext(ctx,
		`UPDATE corpus_import_metadata SET last_successful_corpus_import_at = ? WHERE singleton_id = 1;`,
		now)
	if err != nil {
		return fmt.Errorf("update last successful import timestamp: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read import metadata update result: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("update last successful import timestamp: expected the singleton metadata row, affected %d rows", affected)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import metadata transaction: %w", err)
	}
	return nil
}

// cancelledResult finalizes a cancellation observed at a transaction boundary:
// committed counters are preserved, cancellation is marked explicitly, and the
// destination corpus size is read best-effort (it may be nil).
func (i *Importer) cancelledResult(p Progress, ctx context.Context, cause error) (Progress, error) {
	p.Cancelled = true
	if p.ErrorCountsByReason == nil {
		p.ErrorCountsByReason = map[string]int{}
	}
	_ = i.attachCorpusSize(&p, ctx)
	return p, fmt.Errorf("migration: import cancelled: %w", cause)
}

// attachCorpusSize records the actual destination title_fingerprint row count.
// The count runs on a context that survives caller cancellation so a cancelled
// import can still report its durable partial corpus; a read failure leaves
// TotalCorpusSize nil and returns the error.
func (i *Importer) attachCorpusSize(p *Progress, ctx context.Context) error {
	var count int64
	err := i.dest.QueryRowContext(context.WithoutCancel(ctx),
		"SELECT COUNT(*) FROM title_fingerprint;").Scan(&count)
	if err != nil {
		p.TotalCorpusSize = nil
		return fmt.Errorf("count destination fingerprints: %w", err)
	}
	p.TotalCorpusSize = &count
	return nil
}

// sourceEntry is one streamed source row, or the scan failure that prevented
// reading it. The raw title is held in memory only until its chunk commits and
// is never persisted or logged.
type sourceEntry struct {
	videoID   string
	title     string
	channelID string
	scanErr   error
}

// commitChunk reconciles one chunk inside a single destination transaction.
// The chunk's counters are applied to p only after the commit succeeds, so a
// begin, row-processing, or commit failure reports only previously committed
// rows. Row-level failures are recorded per row and do not abort the chunk.
func (i *Importer) commitChunk(ctx context.Context, chunk []sourceEntry, p *Progress) error {
	tx, err := i.dest.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration: begin destination transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var counters chunkCounters
	for _, entry := range chunk {
		switch {
		case entry.scanErr != nil:
			counters.fail(ReasonRowReadFailed)
		case strings.TrimSpace(entry.videoID) == "":
			counters.fail(ReasonEmptyVideoID)
		default:
			fp, tokens, ok := fingerprint.FromTitle(entry.title)
			if !ok {
				counters.fail(ReasonEmptyFingerprint)
				continue
			}
			exists, err := i.fingerprintExists(ctx, tx, entry.videoID)
			if err != nil {
				counters.fail(ReasonDestinationLookupFailed)
				continue
			}
			if exists {
				counters.skipped++
				continue
			}
			if err := i.insertFingerprint(ctx, tx, entry.videoID, fp, tokens, entry.channelID); err != nil {
				counters.fail(ReasonFingerprintInsertFailed)
				continue
			}
			counters.inserted++
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration: commit destination transaction: %w", err)
	}
	p.applyChunk(len(chunk), counters)
	return nil
}

// countSourceRows returns the complete source-table row count established
// before any destination write.
func (i *Importer) countSourceRows(ctx context.Context, schema *SourceSchema) (int, error) {
	count, err := countTableRows(ctx, i.source, schema.TableName)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// countTableRows returns the complete row count of table in db. It is shared
// by the importer's pre-write total and by Preflight, so both report the same
// authoritative source-row count (ARS-002).
func countTableRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %q;", table)).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func selectQuery(schema *SourceSchema) string {
	selectCols := []string{schema.VideoIDColumn, schema.TitleColumn}
	if schema.ChannelIDColumn != "" {
		selectCols = append(selectCols, schema.ChannelIDColumn)
	}
	return fmt.Sprintf("SELECT %s FROM %q;", strings.Join(selectCols, ", "), schema.TableName)
}

// scanSourceEntry reads one source row. A scan failure is returned as a
// row-level entry error rather than aborting the source iteration.
func scanSourceEntry(rows *sql.Rows, schema *SourceSchema) sourceEntry {
	var videoID, title string
	var channelID sql.NullString

	targets := []any{&videoID, &title}
	if schema.ChannelIDColumn != "" {
		targets = append(targets, &channelID)
	}
	if err := rows.Scan(targets...); err != nil {
		return sourceEntry{scanErr: err}
	}
	return sourceEntry{videoID: videoID, title: title, channelID: channelID.String}
}

func (i *Importer) fingerprintExists(ctx context.Context, tx *sql.Tx, videoID string) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx,
		"SELECT id FROM title_fingerprint WHERE video_id = ?;", videoID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (i *Importer) insertFingerprint(ctx context.Context, tx *sql.Tx, videoID, fp, tokens, channelID string) error {
	// is_duplicate_reference=1 makes the new row an exact/fuzzy matching
	// candidate (OC-004 guarantee 1). It records eligibility only: this INSERT
	// touches no video, route, action, or channel-credit table.
	_, err := tx.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference, import_batch_id)
		 VALUES (?, ?, ?, ?, 'user_import', 1, ?);`,
		videoID, fp, tokens, channelID, i.batchID)
	return err
}
