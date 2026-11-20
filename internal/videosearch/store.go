package videosearch

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/titlekey"
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

// Path names the §6.1 candidate query a name search used.
type Path string

const (
	// PathIndexed asks the title search index for candidates.
	PathIndexed Path = "indexed"
	// PathScan reads every row that may hold a title.
	PathScan Path = "scan"
)

// candidateSQL is the ARS-001 §6.1 scan path. It is read-only and returns every
// row that may hold a title: the cheap filter only narrows; metadata.Held
// decides.
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

// indexedCandidateSQL is the ARS-001 §6.1 indexed path: the same columns and
// the same filter as candidateSQL, narrowed first by the index. The index text
// is never selected; the title comes from transient_metadata.
const indexedCandidateSQL = `
SELECT m.video_id, m.title, m.is_active, m.expired_at, m.fetched_at, m.expires_at,
       v.discovered_at, v.id IS NOT NULL
FROM title_search
JOIN title_search_doc d ON d.doc_id = title_search.rowid
JOIN transient_metadata m ON m.video_id = d.video_id
LEFT JOIN video v ON v.video_id = m.video_id
WHERE title_search MATCH ?
  AND m.is_active = 1
  AND m.expired_at IS NULL
  AND m.title IS NOT NULL AND m.title <> ''
  AND (v.id IS NOT NULL
       OR EXISTS (SELECT 1 FROM title_fingerprint f
                  WHERE f.video_id = m.video_id
                    AND (f.source = 'user_import' OR f.is_duplicate_reference = 1)))`

// useIndex is the ARS-001 §10.7 path rule. ready is metadata.SearchIndexReady.
// FTS5 matches nothing for fewer than 3 code points, and its handling of U+0000
// inside a query string is unverified, so both go to the scan, which is always
// correct.
func useIndex(ready bool, needleKey string) bool {
	return ready && utf8.RuneCountInString(needleKey) >= 3 && !strings.ContainsRune(needleKey, 0)
}

// ftsPhrase binds needleKey as one FTS5 string: inside it every character is
// literal (ARS-001 §10.6).
func ftsPhrase(needleKey string) string {
	return `"` + strings.ReplaceAll(needleKey, `"`, `""`) + `"`
}

// matchSet is what one candidate read produced. It carries no title.
type matchSet struct {
	Matches []Match
	Cutoff  time.Time
	Path    Path
	// Candidates counts rows the candidate query returned.
	Candidates int
	// CandidateTime is the path choice and candidate query, up to the last
	// row read. RecheckTime is Held and Contains over those rows.
	CandidateTime, RecheckTime time.Duration
}

// ScanMatches reads every candidate row inside tx on the scan path, then takes
// one cutoff and returns (using now for the cutoff) the sort keys of rows that
// are held at that cutoff and whose title contains needleKey (a Key result). It
// also returns the cutoff, which the caller reuses for ProjectMetadata. It
// writes nothing and changes no expiry. Titles are dropped as soon as they have
// been compared.
func ScanMatches(ctx context.Context, tx *sql.Tx, folder *titlekey.Folder, needleKey string, now func() time.Time) ([]Match, time.Time, error) {
	set, err := readMatches(ctx, tx, folder, needleKey, now, PathScan, time.Now())
	return set.Matches, set.Cutoff, err
}

// findMatches chooses the path (ARS-001 §10.7) and reads the matches. An error
// from the indexed query is returned as is: there is no fallback to the scan,
// which would hide damage that also blocks writes (§10.9).
func findMatches(ctx context.Context, tx *sql.Tx, folder *titlekey.Folder, needleKey string, now func() time.Time) (matchSet, error) {
	start := time.Now()
	ready, err := metadata.SearchIndexReady(ctx, tx)
	if err != nil {
		return matchSet{}, err
	}
	path := PathScan
	if useIndex(ready, needleKey) {
		path = PathIndexed
	}
	return readMatches(ctx, tx, folder, needleKey, now, path, start)
}

// readMatches runs the path's candidate query, takes the cutoff after its last
// row, and keeps the rows that Held and Contains accept. The index only
// proposes candidates; this re-check decides, and its list is the total.
func readMatches(ctx context.Context, tx *sql.Tx, folder *titlekey.Folder, needleKey string, now func() time.Time, path Path, start time.Time) (matchSet, error) {
	query, args := candidateSQL, []any(nil)
	if path == PathIndexed {
		query, args = indexedCandidateSQL, []any{ftsPhrase(needleKey)}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return matchSet{}, fmt.Errorf("videosearch: %s candidates: %w", path, err)
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
				return matchSet{}, err
			}
		}
		var (
			c     candidate
			title sql.NullString
		)
		if err := rows.Scan(&c.row.VideoID, &title, &c.row.IsActive, &c.row.ExpiredAt,
			&c.row.FetchedAt, &c.row.ExpiresAt, &c.disc, &c.has); err != nil {
			return matchSet{}, fmt.Errorf("videosearch: scan %s candidate: %w", path, err)
		}
		if title.Valid {
			c.row.Title = &title.String
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return matchSet{}, fmt.Errorf("videosearch: iterate %s candidates: %w", path, err)
	}
	// One cutoff, taken only after the result set is fully consumed (the same
	// order ReadLocalDisplay uses).
	cutoff := now().UTC()
	recheck := time.Now()

	var out []Match
	for _, c := range cands {
		if !metadata.Held(c.row, cutoff) || !titlekey.Contains(folder.Key(*c.row.Title), needleKey) {
			continue
		}
		disc, ok := time.Time{}, false
		if c.has {
			disc, ok = metadata.ParseLocalTime(c.disc)
		}
		out = append(out, Match{Key: SortKey{HasDisc: ok, Disc: disc, VideoID: c.row.VideoID}})
	}
	return matchSet{
		Matches: out, Cutoff: cutoff, Path: path, Candidates: len(cands),
		CandidateTime: recheck.Sub(start), RecheckTime: time.Since(recheck),
	}, nil
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
	rows, err := readMetadataRows(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	return projectRows(ids, rows, cutoff), nil
}

// ProjectMetadataAfterRead is ProjectMetadata for callers with no earlier
// cutoff (the video view): it reads the rows, then takes the cutoff, so a row
// whose expiry passes during the read counts as expired. It returns the cutoff.
func ProjectMetadataAfterRead(ctx context.Context, tx *sql.Tx, ids []string, now func() time.Time) (map[string]Metadata, time.Time, error) {
	rows, err := readMetadataRows(ctx, tx, ids)
	if err != nil {
		return nil, time.Time{}, err
	}
	cutoff := now().UTC()
	return projectRows(ids, rows, cutoff), cutoff, nil
}

func readMetadataRows(ctx context.Context, tx *sql.Tx, ids []string) ([]metadata.LocalRow, error) {
	if len(ids) > 50 {
		return nil, fmt.Errorf("videosearch: projection batch exceeds 50 IDs")
	}
	if len(ids) == 0 {
		return nil, nil
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
	return all, nil
}

func projectRows(ids []string, all []metadata.LocalRow, cutoff time.Time) map[string]Metadata {
	out := make(map[string]Metadata, len(ids))
	for _, id := range ids {
		out[id] = Metadata{TitleAvailability: NeverFetched, ChannelAvailability: NeverFetched}
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
	return out
}
