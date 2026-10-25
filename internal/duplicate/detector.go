package duplicate

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// DefaultThreshold is the architecture-resolved default Jaccard threshold.
const DefaultThreshold = 0.5

// MatchType classifies how a duplicate was detected.
type MatchType string

const (
	// MatchTypeExact means an eligible reference carries the same canonical
	// title fingerprint under a different video identifier. The stored value
	// is fixed by the duplicate_match.match_type CHECK constraint; it names
	// the exact matching path, not a video-identifier comparison.
	MatchTypeExact MatchType = "exact_video_id"
	// MatchTypeFuzzy means a fuzzy title similarity exceeded the threshold.
	MatchTypeFuzzy MatchType = "fuzzy_title"
)

// Match describes a detected duplicate.
type Match struct {
	// FingerprintID is the internal TitleFingerprint.id of the matched
	// reference.
	FingerprintID int64
	// VideoID is the YouTube video ID of the matched reference. It is always
	// a different video from the one being evaluated: a video is never
	// returned as a duplicate of itself.
	VideoID string
	// Similarity is the Jaccard score. For exact matches it is always 1.0.
	Similarity float64
	// Type indicates whether this was an exact or fuzzy match.
	Type MatchType
	// ThresholdUsed is the effective fuzzy threshold for this evaluation,
	// including when the exact-title pass finds the match.
	ThresholdUsed float64
}

// Detector queries the Store B corpus for exact and fuzzy duplicates.
type Detector struct {
	db        *sql.DB
	logger    *slog.Logger
	threshold float64
}

// Option configures a Detector.
type Option func(*Detector)

// WithThreshold sets the Jaccard threshold. Values outside [0.0, 1.0] are
// clamped to that range.
func WithThreshold(threshold float64) Option {
	return func(d *Detector) {
		switch {
		case threshold < 0.0:
			d.threshold = 0.0
		case threshold > 1.0:
			d.threshold = 1.0
		default:
			d.threshold = threshold
		}
	}
}

// WithLogger sets the structured logger used by the detector. If nil, the
// default logger is used.
func WithLogger(logger *slog.Logger) Option {
	return func(d *Detector) {
		if logger != nil {
			d.logger = logger
		}
	}
}

// NewDetector returns a duplicate detector backed by db.
func NewDetector(db *sql.DB, opts ...Option) *Detector {
	d := &Detector{
		db:        db,
		logger:    slog.Default(),
		threshold: DefaultThreshold,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// WithThreshold returns an independently configured copy. It does not mutate
// the shared detector, so concurrent runs can evaluate different snapshots.
func (d *Detector) WithThreshold(threshold float64) *Detector {
	configured := *d
	WithThreshold(threshold)(&configured)
	return &configured
}

// Detect checks videoID/title against the duplicate-reference corpus. It
// returns a non-nil Match when a duplicate is found and nil when no duplicate
// is detected. A reference that carries videoID itself is the evaluated video,
// never a duplicate of it: both passes exclude the evaluated video's own row
// (BR-008 AC8, OC-004), so a self-match cannot reach a caller or be persisted
// as a duplicate event. Errors are returned only for database or query
// failures.
func (d *Detector) Detect(ctx context.Context, videoID string, title string) (*Match, error) {
	fp := Normalize(title)
	if len(fp.Tokens) == 0 {
		d.logger.DebugContext(ctx, "no fingerprint tokens; skipping duplicate detection",
			slog.String("video_id", videoID),
		)
		return nil, nil
	}

	// Pass 1: exact canonical-title lookup against eligible references for a
	// different video.
	exact, err := d.exactMatch(ctx, videoID, fp.Canonical)
	if err != nil {
		return nil, fmt.Errorf("duplicate exact lookup: %w", err)
	}
	if exact != nil {
		exact.ThresholdUsed = d.threshold
		d.logger.DebugContext(ctx, "exact duplicate detected",
			slog.String("video_id", videoID),
			slog.Int64("matched_fingerprint_id", exact.FingerprintID),
			slog.String("matched_video_id", exact.VideoID),
		)
		return exact, nil
	}

	// Pass 2: fuzzy match via FTS5 token retrieval.
	match, err := d.fuzzyMatch(ctx, videoID, fp.Tokens)
	if err != nil {
		return nil, fmt.Errorf("duplicate fuzzy lookup: %w", err)
	}
	if match != nil {
		match.ThresholdUsed = d.threshold
		d.logger.DebugContext(ctx, "fuzzy duplicate detected",
			slog.String("video_id", videoID),
			slog.Int64("matched_fingerprint_id", match.FingerprintID),
			slog.Float64("similarity", match.Similarity),
			slog.Float64("threshold", d.threshold),
		)
	}
	return match, nil
}

// exactMatch returns the earliest eligible reference whose canonical
// fingerprint equals canonical for a video other than videoID. The evaluated
// video's own row is excluded because a video cannot be its own duplicate
// (BR-008 AC8, OC-004); an imported or routed row for videoID stays eligible
// for every other video. When several distinct references share the exact
// fingerprint, the oldest row wins so the match points at the first stored
// original (BR-008 AC6).
func (d *Detector) exactMatch(ctx context.Context, videoID, canonical string) (*Match, error) {
	var id int64
	var refVideoID string
	err := d.db.QueryRowContext(ctx,
		`SELECT id, video_id FROM title_fingerprint
		 WHERE fingerprint = ?
		   AND video_id != ?
		   AND is_duplicate_reference = 1
		 ORDER BY id
		 LIMIT 1;`,
		canonical, videoID,
	).Scan(&id, &refVideoID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Match{
		FingerprintID: id,
		VideoID:       refVideoID,
		Similarity:    1.0,
		Type:          MatchTypeExact,
	}, nil
}

func (d *Detector) fuzzyMatch(ctx context.Context, videoID string, tokens []string) (*Match, error) {
	// FTS5 MATCH syntax: "tok1 OR tok2 OR ...". All tokens are alphanumeric
	// after normalization, so they do not need escaping.
	query := strings.Join(tokens, " OR ")

	rows, err := d.db.QueryContext(ctx,
		`SELECT tf.id, tf.video_id, tf.fingerprint_tokens
		 FROM title_fingerprint tf
		 INNER JOIN title_fingerprint_fts ON tf.id = title_fingerprint_fts.rowid
		 WHERE title_fingerprint_fts MATCH ?
		   AND tf.is_duplicate_reference = 1
		   AND tf.video_id != ?;`,
		query, videoID,
	)
	if err != nil {
		return nil, fmt.Errorf("fts candidate query: %w", err)
	}
	defer rows.Close()

	var best *Match
	for rows.Next() {
		var refID int64
		var refVideoID string
		var refTokens string
		if err := rows.Scan(&refID, &refVideoID, &refTokens); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}

		candidateTokens := strings.Fields(refTokens)
		score := Jaccard(tokens, candidateTokens)
		if score > d.threshold {
			if best == nil || score > best.Similarity {
				best = &Match{
					FingerprintID: refID,
					VideoID:       refVideoID,
					Similarity:    score,
					Type:          MatchTypeFuzzy,
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("candidate rows: %w", err)
	}
	return best, nil
}

// execer is the subset of *sql.DB / *sql.Tx operations needed for fingerprint
// writes. Accepting a transaction lets callers keep fingerprint persistence
// atomic with the surrounding unit of work.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SaveFingerprint normalizes title and inserts a TitleFingerprint row. It is
// used by both the migration path and discovery path so that all fingerprints
// share the same normalization.
//
// Duplicate-reference eligibility is monotonic across upserts (ADR-013): a new
// row uses the supplied initial value, while a conflict resolves eligibility as
// existing OR incoming, so a native discovery write with false cannot demote an
// imported or otherwise eligible candidate before duplicate evaluation. The
// merge applies only to is_duplicate_reference; every other field keeps its
// existing write policy.
func SaveFingerprint(ctx context.Context, db execer, videoID, channelID, source, title string, isReference bool, importBatchID *string) (int64, error) {
	if source != "api_discovery" && source != "user_import" {
		return 0, fmt.Errorf("invalid fingerprint source %q", source)
	}
	fp := Normalize(title)
	var isRef int
	if isReference {
		isRef = 1
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO title_fingerprint (video_id, fingerprint, fingerprint_tokens, channel_id, source, is_duplicate_reference, import_batch_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(video_id) DO UPDATE SET
			 fingerprint = excluded.fingerprint,
			 fingerprint_tokens = excluded.fingerprint_tokens,
			 channel_id = excluded.channel_id,
			 source = excluded.source,
			 is_duplicate_reference = title_fingerprint.is_duplicate_reference OR excluded.is_duplicate_reference,
			 import_batch_id = excluded.import_batch_id;`,
		videoID, fp.Canonical, fp.Canonical, channelID, source, isRef, importBatchID,
	)
	if err != nil {
		return 0, fmt.Errorf("save fingerprint: %w", err)
	}
	return res.LastInsertId()
}

// SetDuplicateReference marks the TitleFingerprint for videoID as a duplicate
// reference. Callers should invoke this when a video is routed to a playlist.
func SetDuplicateReference(ctx context.Context, db execer, videoID string, isReference bool) error {
	var v int
	if isReference {
		v = 1
	}
	_, err := db.ExecContext(ctx,
		`UPDATE title_fingerprint SET is_duplicate_reference = ? WHERE video_id = ?;`,
		v, videoID,
	)
	if err != nil {
		return fmt.Errorf("set duplicate reference: %w", err)
	}
	return nil
}
