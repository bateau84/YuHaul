package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
)

// Durable uploads-source failure record (discovery ARS-002). One immutable row
// is one failed scan of one channel in one run. Nothing here reads or changes
// a floor or any eligibility state, so a row can never block or suppress a
// later poll.

// sourceChannelID is the canonical YouTube channel ID shape. It is the only
// channel identity stored or released for a source failure.
var sourceChannelID = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)

// ValidSourceChannelID reports whether id is a canonical YouTube channel ID.
func ValidSourceChannelID(id string) bool { return sourceChannelID.MatchString(id) }

// Source failure vocabulary. The table's CHECKs are deliberately wider than
// this, so a later incident kind needs no rebuild; this is the closed set that
// is written and released today.
const (
	SourceFailurePermanent = "permanent"
	SourceFailureTransient = "transient"

	sourceFailureOperation = "playlist_items_list"
)

// RecordUploadsSourceFailureTx writes the permanent 404 playlistNotFound
// incident of channelID in runID inside tx. The caller commits it before any
// further provider request. A second permanent row for the same run and
// channel is a no-op: the earlier row and its time stand. channelID must pass
// ValidSourceChannelID.
func RecordUploadsSourceFailureTx(ctx context.Context, tx *sql.Tx, runID int64, channelID string, at time.Time) error {
	if !ValidSourceChannelID(channelID) {
		return errors.New("state: invalid source channel id")
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO discovery_source_failure (run_id, channel_id, occurred_at, category, operation, http_status, reason)
		 VALUES (?, ?, ?, 'permanent', 'playlist_items_list', 404, 'playlistNotFound')
		 ON CONFLICT (run_id, channel_id) WHERE category = 'permanent' DO NOTHING;`,
		runID, channelID, formatRunTime(at)); err != nil {
		return fmt.Errorf("record uploads source failure: %w", err)
	}
	return nil
}

type rowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// PermanentSourceFailureChannels returns the channels that already have a
// permanent failure recorded in runID. A resumed discovery skips them.
func PermanentSourceFailureChannels(ctx context.Context, q rowsQueryer, runID int64) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT channel_id FROM discovery_source_failure WHERE run_id = ? AND category = 'permanent';`, runID)
	if err != nil {
		return nil, fmt.Errorf("list recorded source failures: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan recorded source failure: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recorded source failures: %w", err)
	}
	return out, nil
}

// SourceFailure is the released projection of one row (ARS-002 section 4.2).
// It carries no playlist ID, token, URL or error text.
type SourceFailure struct {
	ID    int64 `json:"id"`
	RunID int64 `json:"run_id"`
	// ChannelID is nil when the stored value fails validation.
	ChannelID *string `json:"channel_id"`
	// ChannelName is the channel's current local name, not a snapshot.
	ChannelName *string `json:"channel_name"`
	// OccurredAt is UTC RFC 3339, nil when the stored time cannot be read.
	OccurredAt *string `json:"occurred_at"`
	Category   string  `json:"category"`
	Operation  string  `json:"operation"`
	HTTPStatus *int    `json:"http_status"`
	// Reason is a provider reason admitted by diag, else nil.
	Reason *string `json:"reason"`
}

// SourceFailureQuery selects a page of failures, newest first.
type SourceFailureQuery struct {
	RunID         int64 // 0: every run
	PermanentOnly bool
	BeforeID      int64 // 0: from the newest
	Limit         int
}

// SourceFailurePage is one page. NextBeforeID is the last returned row's ID,
// set only when an older row exists. Withheld counts rows whose category or
// operation is outside the released vocabulary.
type SourceFailurePage struct {
	Items        []SourceFailure `json:"items"`
	NextBeforeID *int64          `json:"next_before_id"`
	Withheld     int             `json:"-"`
}

// ListSourceFailures reads one page. Unreadable rows are never replaced by an
// empty page: a storage error is returned.
func (s *Store) ListSourceFailures(ctx context.Context, q SourceFailureQuery) (*SourceFailurePage, error) {
	if q.Limit < 1 {
		return nil, errors.New("state: source failure limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.run_id, f.channel_id, c.channel_name, f.occurred_at, f.category, f.operation, f.http_status, f.reason
		   FROM discovery_source_failure f
		   LEFT JOIN channel c ON c.channel_id = f.channel_id
		  WHERE (? = 0 OR f.run_id = ?)
		    AND (? = 0 OR f.category = 'permanent')
		    AND (? = 0 OR f.id < ?)
		  ORDER BY f.id DESC
		  LIMIT ?;`,
		q.RunID, q.RunID, boolInt(q.PermanentOnly), q.BeforeID, q.BeforeID, q.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("list source failures: %w", err)
	}
	defer rows.Close()

	page := &SourceFailurePage{Items: []SourceFailure{}}
	var lastID int64
	n := 0
	for rows.Next() {
		var (
			f                     SourceFailure
			channelID, occurredAt string
			channelName, reason   sql.NullString
			status                sql.NullInt64
			category, operation   string
		)
		if err := rows.Scan(&f.ID, &f.RunID, &channelID, &channelName, &occurredAt, &category, &operation, &status, &reason); err != nil {
			return nil, fmt.Errorf("scan source failure: %w", err)
		}
		n++
		if n > q.Limit {
			page.NextBeforeID = &lastID
			break
		}
		lastID = f.ID
		if (category != SourceFailurePermanent && category != SourceFailureTransient) || operation != sourceFailureOperation {
			page.Withheld++
			continue
		}
		f.Category, f.Operation = category, operation
		if ValidSourceChannelID(channelID) {
			f.ChannelID = &channelID
		}
		if channelName.Valid && channelName.String != "" {
			f.ChannelName = &channelName.String
		}
		if t, ok := parseSourceFailureTime(occurredAt); ok {
			s := t.UTC().Format(time.RFC3339)
			f.OccurredAt = &s
		}
		if status.Valid {
			v := int(status.Int64)
			f.HTTPStatus = &v
		}
		if reason.Valid && diag.ProviderReasonAdmitted(reason.String) {
			f.Reason = &reason.String
		}
		page.Items = append(page.Items, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source failures: %w", err)
	}
	return page, nil
}

func parseSourceFailureTime(s string) (time.Time, bool) {
	for _, layout := range []string{RunTimestampLayout, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
