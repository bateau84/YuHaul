// Package migration implements the one-time historical corpus importer.
//
// It reads raw video titles and IDs from a source SQLite database, transforms
// each title into the canonical YuHaul fingerprint, and inserts the result into
// the destination database's title_fingerprint table with source='user_import'.
// Raw titles are not retained in the destination.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/bateau84/YuHaul/internal/fingerprint"

	_ "modernc.org/sqlite"
)

// ErrSchemaNotDiscoverable is returned when the source database does not
// contain exactly one table with the expected video_id + title columns.
var ErrSchemaNotDiscoverable = errors.New("migration: source database schema is not discoverable")

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

// Importer migrates raw titles from a source DB into destination fingerprints.
type Importer struct {
	source  *sql.DB
	dest    *sql.DB
	batchID string
}

// NewImporter creates an importer. batchID is stored in each imported
// title_fingerprint.import_batch_id row.
func NewImporter(source, dest *sql.DB, batchID string) *Importer {
	return &Importer{
		source:  source,
		dest:    dest,
		batchID: batchID,
	}
}

// Run executes the one-time import using the discovered schema. It is
// idempotent: re-running the same import does not duplicate fingerprints
// because title_fingerprint.video_id is UNIQUE.
func (i *Importer) Run(ctx context.Context, schema *SourceSchema) (ImportResult, error) {
	if schema == nil {
		return ImportResult{}, errors.New("migration: schema is required")
	}

	tx, err := i.dest.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, fmt.Errorf("migration: begin destination transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	selectCols := []string{schema.VideoIDColumn, schema.TitleColumn}
	if schema.ChannelIDColumn != "" {
		selectCols = append(selectCols, schema.ChannelIDColumn)
	}
	query := fmt.Sprintf("SELECT %s FROM %q;", strings.Join(selectCols, ", "), schema.TableName)

	rows, err := i.source.QueryContext(ctx, query)
	if err != nil {
		return ImportResult{}, fmt.Errorf("migration: query source table %q: %w", schema.TableName, err)
	}
	defer rows.Close()

	var result ImportResult
	for rows.Next() {
		result.Read++

		var videoID, title string
		var channelID sql.NullString
		var scanTargets []any
		scanTargets = append(scanTargets, &videoID, &title)
		if schema.ChannelIDColumn != "" {
			scanTargets = append(scanTargets, &channelID)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			result.Errors++
			continue
		}

		if strings.TrimSpace(videoID) == "" {
			result.Errors++
			continue
		}

		fp, tokens, ok := fingerprint.FromTitle(title)
		if !ok {
			result.Errors++
			continue
		}

		exists, err := i.fingerprintExists(ctx, tx, videoID)
		if err != nil {
			result.Errors++
			continue
		}
		if exists {
			result.Skipped++
			continue
		}

		if err := i.insertFingerprint(ctx, tx, videoID, fp, tokens, channelID.String); err != nil {
			result.Errors++
			continue
		}
		result.Inserted++
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("migration: iterate source rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("migration: commit destination transaction: %w", err)
	}

	return result, nil
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
	_, err := tx.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, import_batch_id)
		 VALUES (?, ?, ?, ?, 'user_import', ?);`,
		videoID, fp, tokens, channelID, i.batchID)
	return err
}
