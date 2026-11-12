package videosearch

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
)

// ctxCheckEvery is how many candidate rows are read between context checks
// (ARS-001 §2).
const ctxCheckEvery = 1000

// Availability says whether a title or channel name may be shown.
type Availability string

const (
	// Held: the row passes the Run Detail rule (metadata.ProjectLocal).
	Held Availability = "held"
	// Expired: the row is known expired (marker set, or expires_at passed).
	Expired Availability = "expired"
	// NeverFetched: there is no row, or the row holds no value.
	NeverFetched Availability = "never_fetched"
	// Withheld: a value exists but the row is not trustworthy (inactive,
	// malformed lifecycle times, or a future fetch time).
	Withheld Availability = "withheld"
)

func availability(a string) Availability {
	switch a {
	case "current_local":
		return Held
	case "known_expired":
		return Expired
	case "withheld":
		return Withheld
	default:
		return NeverFetched
	}
}

// Match is a held title that contains the search text. It carries no title.
type Match struct {
	Key SortKey
}

// candidateSQL is ARS-001 §6.1. It is read-only and returns every row that may
// hold a title: the cheap filter only narrows; metadata.Held decides.
const candidateSQL = `
SELECT m.video_id, m.title, m.is_active, m.expired_at, m.fetched_at, m.expires_at,
       v.discovered_at, v.id IS NOT NULL
FROM transient_metadata m
LEFT JOIN video v ON v.video_id = m.video_id
WHERE m.is_active = 1
  AND m.expired_at IS NULL
  AND m.title IS NOT NULL AND m.title <> ''
  AND (v.id IS NOT NULL
       OR EXISTS (SELECT 1 FROM title_fingerprint f
                  WHERE f.video_id = m.video_id
                    AND (f.source = 'user_import' OR f.is_duplicate_reference = 1)))`

// ScanMatches reads every candidate row inside tx, then takes one cutoff and
// returns (using now for the cutoff) the sort keys of rows that are held at that cutoff and whose title
// contains needleKey (a Key result). It also returns the cutoff, which the
// caller reuses for ProjectMetadata. It writes nothing and changes no expiry.
// Titles are dropped as soon as they have been compared.
func ScanMatches(ctx context.Context, tx *sql.Tx, folder *Folder, needleKey string, now func() time.Time) ([]Match, time.Time, error) {
	rows, err := tx.QueryContext(ctx, candidateSQL)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("videosearch: scan candidates: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		row  metadata.LocalRow
		disc any
		has  bool
	}
	var (
		cands []candidate
		n     int
	)
	for rows.Next() {
		if n++; n%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return nil, time.Time{}, err
			}
		}
		var (
			c     candidate
			title sql.NullString
		)
		if err := rows.Scan(&c.row.VideoID, &title, &c.row.IsActive, &c.row.ExpiredAt,
			&c.row.FetchedAt, &c.row.ExpiresAt, &c.disc, &c.has); err != nil {
			return nil, time.Time{}, fmt.Errorf("videosearch: scan candidate: %w", err)
		}
		if title.Valid {
			c.row.Title = &title.String
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("videosearch: iterate candidates: %w", err)
	}
	// One cutoff, taken only after the result set is fully consumed (the same
	// order ReadLocalDisplay uses).
	cutoff := now().UTC()

	var out []Match
	for _, c := range cands {
		if !metadata.Held(c.row, cutoff) || !Contains(folder.Key(*c.row.Title), needleKey) {
			continue
		}
		disc, ok := time.Time{}, false
		if c.has {
			disc, ok = metadata.ParseLocalTime(c.disc)
		}
		out = append(out, Match{Key: SortKey{HasDisc: ok, Disc: disc, VideoID: c.row.VideoID}})
	}
	return out, cutoff, nil
}

// Metadata is the availability of one video's title and channel name at the
// request cutoff. Text is non-nil only when the matching availability is Held.
type Metadata struct {
	Title, ChannelName                     *string
	TitleAvailability, ChannelAvailability Availability
}

// ProjectMetadata reads transient_metadata for ids (at most 50) inside tx and
// projects each row with metadata.ProjectLocal at cutoff. An id with no row is
// NeverFetched for both fields. It writes nothing.
func ProjectMetadata(ctx context.Context, tx *sql.Tx, ids []string, cutoff time.Time) (map[string]Metadata, error) {
	if len(ids) > 50 {
		return nil, fmt.Errorf("videosearch: projection batch exceeds 50 IDs")
	}
	out := make(map[string]Metadata, len(ids))
	for _, id := range ids {
		out[id] = Metadata{TitleAvailability: NeverFetched, ChannelAvailability: NeverFetched}
	}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	q := `SELECT video_id, title, channel_name, duration_seconds, fetched_at, expires_at, expired_at, is_active
FROM transient_metadata WHERE video_id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("videosearch: read metadata: %w", err)
	}
	defer rows.Close()
	var all []metadata.LocalRow
	for rows.Next() {
		var (
			r              metadata.LocalRow
			title, channel sql.NullString
			duration       sql.NullInt64
		)
		if err := rows.Scan(&r.VideoID, &title, &channel, &duration, &r.FetchedAt, &r.ExpiresAt, &r.ExpiredAt, &r.IsActive); err != nil {
			return nil, fmt.Errorf("videosearch: scan metadata: %w", err)
		}
		if title.Valid {
			r.Title = &title.String
		}
		if channel.Valid {
			r.ChannelName = &channel.String
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("videosearch: iterate metadata: %w", err)
	}
	for _, r := range all {
		d := metadata.ProjectLocal(r, cutoff)
		out[r.VideoID] = Metadata{
			Title:               d.Title,
			ChannelName:         d.ChannelName,
			TitleAvailability:   availability(d.TitleAvailability),
			ChannelAvailability: availability(d.ChannelNameAvailability),
		}
	}
	return out, nil
}
