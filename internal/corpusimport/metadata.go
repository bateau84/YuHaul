package corpusimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrMetadataRead is returned when the durable last-successful-import marker
// cannot be read: the singleton row is missing, the stored timestamp is
// malformed, or the destination query failed (HTTP: 500). A read failure
// never erases or replaces the last committed value (ADR-015).
var ErrMetadataRead = errors.New("corpusimport: failed to read corpus import metadata")

// ImportMetadata is the wire shape of GET /api/corpus/import/metadata
// (ARS-002). It is the durable marker row and nothing else: no import
// history, job ID, counts, raw title, or source filename is part of it.
type ImportMetadata struct {
	// LastSuccessfulCorpusImportAt is the canonical RFC 3339 Nano UTC
	// completion time of the most recent successful import, or null when no
	// reliable completion time is recorded (migration 013, ADR-015).
	LastSuccessfulCorpusImportAt *string `json:"last_successful_corpus_import_at"`
}

// ImportMetadata reads the durable last-successful-import marker from the
// destination database. It is independent of the process-local job lifecycle
// (ADR-009) and returns the same value after a process restart, while
// GET /api/corpus/import endpoints may 404. A nil timestamp is a valid 200
// result meaning no reliable completion time is recorded; a missing singleton
// row, malformed stored timestamp, or database read failure is ErrMetadataRead.
func (m *Manager) ImportMetadata(ctx context.Context) (ImportMetadata, error) {
	var stored sql.NullString
	err := m.dest.QueryRowContext(ctx,
		`SELECT last_successful_corpus_import_at FROM corpus_import_metadata WHERE singleton_id = 1;`).
		Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ImportMetadata{}, fmt.Errorf("%w: singleton metadata row is missing", ErrMetadataRead)
	case err != nil:
		return ImportMetadata{}, fmt.Errorf("%w: %v", ErrMetadataRead, err)
	case !stored.Valid:
		// NULL: no reliable completion time has been recorded. That is an
		// honest unknown, not an error and not a sentinel date.
		return ImportMetadata{}, nil
	}
	// A stored value that is not canonical RFC 3339 Nano is corrupt metadata,
	// not a value to echo back as if it were a completion time.
	if _, err := time.Parse(time.RFC3339Nano, stored.String); err != nil {
		return ImportMetadata{}, fmt.Errorf("%w: stored timestamp is not RFC 3339 Nano", ErrMetadataRead)
	}
	return ImportMetadata{LastSuccessfulCorpusImportAt: &stored.String}, nil
}
