package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const publishedDayLayout = "2006-01-02"

// PublishedRange is an optional UTC calendar-day filter on a video's YouTube
// published date (run-initiation ADR-002). A nil bound is unbounded on that
// side; a range with no bound is "no filter".
type PublishedRange struct {
	From *string // canonical "YYYY-MM-DD" or nil
	To   *string
}

// RangeError describes why a requested range is invalid.
type RangeError struct {
	Field string // "published_from" | "published_to" | "" (reversed range)
	Kind  string // "invalid_date" | "reversed"
}

func (e *RangeError) Error() string {
	if e.Kind == "reversed" {
		return "the start day must not be after the end day"
	}
	return e.Field + " must be a valid calendar date (YYYY-MM-DD)"
}

// ParsePublishedRange validates from/to and returns the canonical range.
// Non-canonical forms such as "2026-1-05" are rejected. From is checked before
// To; the order is checked only when both are valid.
func ParsePublishedRange(from, to *string) (PublishedRange, *RangeError) {
	if from != nil && !canonicalDay(*from) {
		return PublishedRange{}, &RangeError{Field: "published_from", Kind: "invalid_date"}
	}
	if to != nil && !canonicalDay(*to) {
		return PublishedRange{}, &RangeError{Field: "published_to", Kind: "invalid_date"}
	}
	if from != nil && to != nil && *from > *to {
		return PublishedRange{}, &RangeError{Kind: "reversed"}
	}
	return PublishedRange{From: from, To: to}, nil
}

func canonicalDay(s string) bool {
	t, err := time.Parse(publishedDayLayout, s)
	return err == nil && t.Format(publishedDayLayout) == s
}

// IsSet reports whether any bound is present.
func (r PublishedRange) IsSet() bool { return r.From != nil || r.To != nil }

// Bounds returns lower = From 00:00Z (inclusive) and upper = To+1 day 00:00Z
// (exclusive). The range must have been validated.
func (r PublishedRange) Bounds() (lower, upper *time.Time) {
	if r.From != nil {
		t, _ := time.Parse(publishedDayLayout, *r.From)
		lower = &t
	}
	if r.To != nil {
		t, _ := time.Parse(publishedDayLayout, *r.To)
		t = t.AddDate(0, 0, 1)
		upper = &t
	}
	return lower, upper
}

// Contains reports whether t's UTC instant lies in the half-open range.
func (r PublishedRange) Contains(t time.Time) bool {
	lower, upper := r.Bounds()
	t = t.UTC()
	if lower != nil && t.Before(*lower) {
		return false
	}
	if upper != nil && !t.Before(*upper) {
		return false
	}
	return true
}

// publishedRange projects stored bounds, treating a non-canonical value
// (impossible past the migration triggers) as absent.
func (r *Run) publishedRange() *struct {
	From *string `json:"from"`
	To   *string `json:"to"`
} {
	if r.RunType != RunTypeBacktest && r.RunType != RunTypeReplay {
		return nil
	}
	out := &struct {
		From *string `json:"from"`
		To   *string `json:"to"`
	}{}
	if r.PublishedFrom != nil && canonicalDay(*r.PublishedFrom) {
		out.From = r.PublishedFrom
	}
	if r.PublishedTo != nil && canonicalDay(*r.PublishedTo) {
		out.To = r.PublishedTo
	}
	return out
}

// Range returns the run's stored range.
func (r *Run) Range() PublishedRange {
	return PublishedRange{From: r.PublishedFrom, To: r.PublishedTo}
}

// storedTimestampLayouts are the forms a transient_metadata timestamp can
// take: RFC 3339, the SQLite driver's time.Time String() form (migration 011)
// with a zone abbreviation ("+0000 UTC") or, for an unnamed zone, a repeated
// numeric offset ("+0200 +0200"), and a bare date-time read as UTC.
var storedTimestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700 -0700",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05",
}

func decodeStoredTimestamp(s sql.NullString) (time.Time, bool) {
	if !s.Valid {
		return time.Time{}, false
	}
	for _, layout := range storedTimestampLayouts {
		if t, err := time.Parse(layout, s.String); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ListRetainedVideosPublishedIn returns the retained videos whose locally held,
// currently eligible published date lies in rng. A video qualifies only when
// its transient_metadata row is active, not expired and unexpired at cutoff
// (the same release rule as current/local display) and its published_at
// decodes. Anything else means "no published date available locally" and the
// video is left out. Reads only video and transient_metadata; never calls
// YouTube (ADR-002 Decision 3).
func (s *Store) ListRetainedVideosPublishedIn(ctx context.Context, rng PublishedRange, cutoff time.Time) ([]Video, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT v.id, v.video_id, v.channel_id, v.discovered_at, v.discovered_in_run_id,
		        v.processing_state, v.final_outcome, v.deferred_reason, v.error_message, v.completed_at,
		        CAST(tm.published_at AS TEXT), tm.is_active, CAST(tm.expires_at AS TEXT), CAST(tm.expired_at AS TEXT)
		 FROM video v LEFT JOIN transient_metadata tm ON tm.video_id = v.video_id
		 ORDER BY v.discovered_at;`)
	if err != nil {
		return nil, fmt.Errorf("listing retained videos with published dates: %w", err)
	}
	defer rows.Close()

	out := make([]Video, 0)
	for rows.Next() {
		var publishedAt, expiresAt, expiredAt sql.NullString
		var isActive sql.NullInt64
		v, err := scanVideoWith(rows, &publishedAt, &isActive, &expiresAt, &expiredAt)
		if err != nil {
			return nil, fmt.Errorf("scanning retained video: %w", err)
		}
		if !isActive.Valid || isActive.Int64 != 1 || expiredAt.Valid {
			continue
		}
		if exp, ok := decodeStoredTimestamp(expiresAt); !ok || !exp.After(cutoff) {
			continue
		}
		if pub, ok := decodeStoredTimestamp(publishedAt); !ok || !rng.Contains(pub) {
			continue
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating retained videos: %w", err)
	}
	return out, nil
}
