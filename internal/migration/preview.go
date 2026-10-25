package migration

// Secure staging and read-only validation of an uploaded SQLite corpus source
// (ARS-002). This is the reusable server-side capability behind the web
// import: it copies an untrusted upload stream to a randomly named private
// temp file, opens it read-only, validates it with the same DiscoverSchema
// rules the importer uses, and reports a point-in-time preview without ever
// mutating the destination. Preflight adds the complete source-row count an
// accepted import needs before it creates a job.
//
// The raw upload and the sample titles are transient only: the staged file is
// removed on every return path of Preview (including a panic, via defer) and
// titles are neither logged nor persisted here. Nothing in this package
// registers a route; the HTTP handler and import job manager layer on top of
// these primitives in internal/corpusimport.

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// DefaultMaxUploadBytes is the default hard maximum SQLite upload size
	// (500 MiB) enforced before upload data is retained, per ARS-002.
	DefaultMaxUploadBytes int64 = 524288000

	// MaxUploadBytesEnvVar names the server configuration override for the
	// upload size cap. An invalid value fails server configuration rather than
	// silently weakening the cap (see MaxUploadBytesFromEnv).
	MaxUploadBytesEnvVar = "YUHAUL_CORPUS_UPLOAD_MAX_BYTES"

	// PreviewSampleTitleLimit is the maximum number of transient raw sample
	// titles a preview reports (ARS-002: "up to ten sample titles").
	PreviewSampleTitleLimit = 10

	// StagingFilePrefix and StagingFileSuffix frame the server-generated staged
	// upload name. The random body is unpredictable, so a staged upload cannot
	// be guessed or addressed by a client-supplied path; the client filename is
	// never used.
	StagingFilePrefix = "corpus-upload-"
	StagingFileSuffix = ".sqlite"
)

// sqliteMagic is the 16-byte SQLite database header signature
// "SQLite format 3\x00". A staged upload that does not begin with it cannot be
// a SQLite database and is rejected before any SQL is issued against it.
var sqliteMagic = []byte("SQLite format 3\x00")

// Source-rejection sentinels. The web layer maps these to 4xx responses
// (ErrUploadTooLarge -> 413; the rest -> 400). Any other returned error is an
// internal failure (500). None of these ever echo file contents, titles,
// paths, or SQL to the client (ARS-002 failure behavior).
var (
	// ErrUploadTooLarge is returned when the upload stream exceeds the
	// configured maximum size. The partial staged file is removed.
	ErrUploadTooLarge = errors.New("migration: upload exceeds the configured maximum size")

	// ErrUploadEmpty is returned when the upload stream contains no bytes.
	ErrUploadEmpty = errors.New("migration: upload is empty")

	// ErrInvalidSource is returned when the staged upload is not a readable
	// SQLite database (unrecognized header, corrupt, or truncated content).
	// ErrSchemaNotDiscoverable (returned unwrapped) covers a readable database
	// whose schema is missing or ambiguous; both are source rejections.
	ErrInvalidSource = errors.New("migration: upload is not a readable SQLite database")
)

// PreviewResult is the point-in-time preview of a staged source file. The JSON
// field names are the exact ARS-002 preview response contract. Preview counts
// are estimates observed at preview time; the final committed import counts are
// authoritative if the corpus changes before confirmation. There is no date or
// date-range field because the accepted source contract supplies no date.
type PreviewResult struct {
	SourceRowCount      int64    `json:"source_row_count"`
	AlreadyPresentCount int64    `json:"already_present_count"`
	SampleTitles        []string `json:"sample_titles"`
}

// Stager securely stages untrusted upload streams into a private directory and
// enforces a hard byte cap. A Stager is safe for concurrent use.
type Stager struct {
	dir      string
	maxBytes int64
}

// NewStager creates a Stager that writes staged files under dir (created with
// mode 0700 if absent) and rejects uploads larger than maxBytes bytes. maxBytes
// must be positive; the caller supplies it from MaxUploadBytesFromEnv or an
// equivalent configured value.
func NewStager(dir string, maxBytes int64) (*Stager, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("migration: max upload bytes must be positive, got %d", maxBytes)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("migration: create staging directory: %w", err)
	}
	return &Stager{dir: dir, maxBytes: maxBytes}, nil
}

// MaxUploadBytesFromEnv reads the upload size cap from
// YUHAUL_CORPUS_UPLOAD_MAX_BYTES. An unset variable yields
// DefaultMaxUploadBytes. A value that is not a positive integer byte count
// returns an error so invalid configuration fails loudly instead of disabling
// or silently weakening the cap.
func MaxUploadBytesFromEnv() (int64, error) {
	v := os.Getenv(MaxUploadBytesEnvVar)
	if v == "" {
		return DefaultMaxUploadBytes, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("migration: %s must be a positive integer byte count, got %q",
			MaxUploadBytesEnvVar, v)
	}
	return n, nil
}

// Stage copies the upload stream r into a randomly named file (mode 0600) under
// the stager directory, ignoring any client-supplied filename, and enforces the
// size cap. It returns the staged file's path; the caller owns it and must
// remove it. On an over-limit, empty, or I/O failure the partial file is removed
// before the error is returned, so no partial upload is left on disk.
//
// The cap is enforced by copying at most maxBytes+1 bytes: an over-limit stream
// is detected after one byte past the cap without buffering the whole upload,
// bounding both memory and disk use.
func (s *Stager) Stage(ctx context.Context, r io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	name, err := randomStagingName()
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, name)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("migration: create staging file: %w", err)
	}

	n, copyErr := io.Copy(f, io.LimitReader(r, s.maxBytes+1))
	if copyErr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("migration: write staging file: %w", copyErr)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("migration: close staging file: %w", err)
	}

	switch {
	case n > s.maxBytes:
		_ = os.Remove(path)
		return "", ErrUploadTooLarge
	case n == 0:
		_ = os.Remove(path)
		return "", ErrUploadEmpty
	}
	return path, nil
}

// Preview stages the upload stream r, then delegates to PreviewStaged and
// removes the staged file before returning on every path, including a panic
// (defer runs during unwinding). It performs no destination writes.
//
// Errors: ErrUploadTooLarge, ErrUploadEmpty, ErrInvalidSource, and
// ErrSchemaNotDiscoverable are source rejections (web layer: 413/400). Any
// other error (for example a destination read failure) is an internal failure.
func (s *Stager) Preview(ctx context.Context, r io.Reader, dest *sql.DB) (PreviewResult, error) {
	path, err := s.Stage(ctx, r)
	if err != nil {
		return PreviewResult{}, err
	}
	// Remove the staged source on every return path (ARS-002 invariant 7). A
	// defer also runs during a panic, so an unexpected crash cannot leave the
	// raw upload behind.
	defer os.Remove(path)

	return PreviewStaged(ctx, path, dest)
}

// PreviewStaged reports a point-in-time preview of an already staged source
// file against the destination database: source-table row count, source rows
// whose non-empty video_id already exists in the destination, and up to
// PreviewSampleTitleLimit raw sample titles. It validates the staged file with
// the same rules as Preview (SQLite header, read-only open, DiscoverSchema)
// and performs no destination writes. The caller owns stagedPath and must
// remove it; nothing here deletes the file.
//
// Errors: ErrInvalidSource and ErrSchemaNotDiscoverable are source rejections
// (web layer: 400); any other error is an internal failure.
func PreviewStaged(ctx context.Context, stagedPath string, dest *sql.DB) (PreviewResult, error) {
	if err := validateSQLiteHeader(stagedPath); err != nil {
		return PreviewResult{}, err
	}

	src, err := OpenSourceReadOnly(ctx, stagedPath)
	if err != nil {
		return PreviewResult{}, err
	}
	defer src.Close()

	schema, err := DiscoverSchema(ctx, src)
	if err != nil {
		if errors.Is(err, ErrSchemaNotDiscoverable) {
			// Missing or ambiguous schema: a readable database that does not
			// match the source contract. Surface it unchanged so callers can
			// classify it distinctly from malformed content.
			return PreviewResult{}, err
		}
		return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}

	return previewRows(ctx, src, dest, schema)
}

// Preflight validates an already staged source file before an import is
// accepted (ARS-002): the SQLite header must be valid, the file must open
// read-only, DiscoverSchema must find exactly one source table, and the
// complete source-table row count must be readable. It returns the discovered
// schema and that row count, which become the import's authoritative total.
// Preflight performs no destination writes and does not remove stagedPath;
// the caller owns the file.
//
// Errors: ErrInvalidSource and ErrSchemaNotDiscoverable are source rejections
// (web layer: 400); a returned context error means the caller was cancelled;
// any other error is an internal failure.
func Preflight(ctx context.Context, stagedPath string) (*SourceSchema, int64, error) {
	if err := validateSQLiteHeader(stagedPath); err != nil {
		return nil, 0, err
	}

	src, err := OpenSourceReadOnly(ctx, stagedPath)
	if err != nil {
		return nil, 0, err
	}
	defer src.Close()

	schema, err := DiscoverSchema(ctx, src)
	if err != nil {
		if errors.Is(err, ErrSchemaNotDiscoverable) {
			return nil, 0, err
		}
		return nil, 0, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}

	total, err := countTableRows(ctx, src, schema.TableName)
	if err != nil {
		if cErr := ctx.Err(); cErr != nil {
			return nil, 0, cErr
		}
		return nil, 0, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	return schema, total, nil
}

// randomStagingName returns an unpredictable file name so a staged upload
// cannot be guessed, collided with, or addressed by a client-supplied path.
func randomStagingName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("migration: generate staging name: %w", err)
	}
	return StagingFilePrefix + hex.EncodeToString(b[:]) + StagingFileSuffix, nil
}

// validateSQLiteHeader rejects a staged file that does not begin with the
// SQLite signature, before any SQL is issued against it. A file shorter than
// the 16-byte header is rejected here; longer but truncated content is caught
// by the schema/data reads.
func validateSQLiteHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("migration: open staged file for validation: %w", err)
	}
	defer f.Close()

	hdr := make([]byte, len(sqliteMagic))
	if _, err := io.ReadFull(f, hdr); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	if !bytes.Equal(hdr, sqliteMagic) {
		return fmt.Errorf("%w: unrecognized file header", ErrInvalidSource)
	}
	return nil
}

// OpenSourceReadOnly opens a staged file as a read-only SQLite database using
// a native file: URI with mode=ro, so SQLite refuses writes and will not create
// or mutate the file or any sidecar. Callers (preview, preflight, and the web
// import worker) use it so an untrusted upload can never be written to.
func OpenSourceReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("migration: open source read-only: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	return db, nil
}

// previewRows streams the discovered source table once to establish the source
// row count, count source rows whose non-empty video_id already exists in the
// destination, and collect up to PreviewSampleTitleLimit raw sample titles.
// Source read errors are classified as ErrInvalidSource (malformed/truncated
// content); destination read errors are returned unchanged (internal).
func previewRows(ctx context.Context, src, dest *sql.DB, schema *SourceSchema) (PreviewResult, error) {
	destIDs, err := loadDestVideoIDs(ctx, dest)
	if err != nil {
		return PreviewResult{}, err
	}

	rows, err := src.QueryContext(ctx, selectQuery(schema))
	if err != nil {
		return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	defer rows.Close()

	result := PreviewResult{SampleTitles: []string{}}
	for rows.Next() {
		// Rows that fail to scan still count toward the source row total (they
		// may later count as import row-level errors) but yield no title sample
		// and can never match a destination video_id.
		entry := scanSourceEntry(rows, schema)
		result.SourceRowCount++
		if entry.scanErr != nil {
			continue
		}
		if len(result.SampleTitles) < PreviewSampleTitleLimit {
			result.SampleTitles = append(result.SampleTitles, entry.title)
		}
		if strings.TrimSpace(entry.videoID) != "" {
			if _, ok := destIDs[entry.videoID]; ok {
				result.AlreadyPresentCount++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return PreviewResult{}, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	return result, nil
}

// loadDestVideoIDs reads the destination video_id set once so the preview can
// count already-present source rows with a single query per side. This is a
// read-only point-in-time snapshot of the destination corpus.
//
// shortcut: preloads every destination video_id into memory. Fine at the
// intended single-user corpus scale (ARS-002 describes 10,000+ titles); if the
// destination ever grows large enough that this map is too big, switch to a
// per-row prepared point query or an ATTACH-based join.
func loadDestVideoIDs(ctx context.Context, dest *sql.DB) (map[string]struct{}, error) {
	rows, err := dest.QueryContext(ctx, "SELECT video_id FROM title_fingerprint;")
	if err != nil {
		return nil, fmt.Errorf("migration: read destination video ids: %w", err)
	}
	defer rows.Close()

	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("migration: scan destination video id: %w", err)
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migration: iterate destination video ids: %w", err)
	}
	return ids, nil
}
