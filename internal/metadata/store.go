package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store provides persistence for TransientMetadata records.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Upsert inserts or updates a Store A record.
func (s *Store) Upsert(ctx context.Context, m TransientMetadata) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO transient_metadata (
			video_id, title, channel_name, description, published_at,
			duration_seconds, thumbnail_url, fetched_at, refreshed_at,
			expires_at, expired_at, is_active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(video_id) DO UPDATE SET
			title = excluded.title,
			channel_name = excluded.channel_name,
			description = excluded.description,
			published_at = excluded.published_at,
			duration_seconds = excluded.duration_seconds,
			thumbnail_url = excluded.thumbnail_url,
			fetched_at = excluded.fetched_at,
			refreshed_at = excluded.refreshed_at,
			expires_at = excluded.expires_at,
			expired_at = excluded.expired_at,
			is_active = excluded.is_active;`,
		m.VideoID, m.Title, m.ChannelName, m.Description, m.PublishedAt,
		m.DurationSeconds, m.ThumbnailURL, m.FetchedAt, m.RefreshedAt,
		m.ExpiresAt, m.ExpiredAt, m.IsActive,
	)
	if err != nil {
		return fmt.Errorf("metadata: upsert %s: %w", m.VideoID, err)
	}
	return nil
}

// Get returns the Store A record for videoID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, videoID string) (*TransientMetadata, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT video_id, title, channel_name, description, published_at,
		       duration_seconds, thumbnail_url, fetched_at, refreshed_at,
		       expires_at, expired_at, is_active
		FROM transient_metadata
		WHERE video_id = ?;`, videoID)
	m, err := scanMetadata(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("metadata: get %s: %w", videoID, ErrNotFound)
		}
		return nil, fmt.Errorf("metadata: get %s: %w", videoID, err)
	}
	return m, nil
}

// readLocalDisplayRows reads only the fields needed for projection. Eligibility
// is deliberately decided by Manager after this query has fully completed.
func (s *Store) readLocalDisplayRows(ctx context.Context, ids []string) ([]LocalRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 100 {
		return nil, fmt.Errorf("metadata: local display batch exceeds 100 IDs")
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	query := `SELECT video_id, title, channel_name, duration_seconds, fetched_at, expires_at, expired_at, is_active FROM transient_metadata WHERE video_id IN (`
	for i := range ids {
		if i > 0 {
			query += ","
		}
		query += "?"
	}
	query += ")"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("metadata: read local display: %w", err)
	}
	defer rows.Close()
	out := make([]LocalRow, 0, len(ids))
	for rows.Next() {
		var row LocalRow
		var title, channel sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(&row.VideoID, &title, &channel, &duration, &row.FetchedAt, &row.ExpiresAt, &row.ExpiredAt, &row.IsActive); err != nil {
			return nil, fmt.Errorf("metadata: scan local display: %w", err)
		}
		if title.Valid {
			row.Title = &title.String
		}
		if channel.Valid {
			row.ChannelName = &channel.String
		}
		if duration.Valid {
			n := int(duration.Int64)
			row.DurationSeconds = &n
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: iterate local display: %w", err)
	}
	return out, nil
}

// ErrNotFound is returned when a Store A record does not exist.
var ErrNotFound = errors.New("metadata not found")

// CleanupExpired deletes transient_metadata records whose fetched_at is before
// cutoff. It performs only local database operations and consumes no API quota.
// Returns the number of records deleted.
func (s *Store) CleanupExpired(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 10000
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM transient_metadata
		WHERE video_id IN (
			SELECT video_id FROM transient_metadata
			WHERE fetched_at < ?
			ORDER BY fetched_at ASC
			LIMIT ?
		);`, cutoff.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("metadata: cleanup expired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("metadata: cleanup expired rows affected: %w", err)
	}
	return int(n), nil
}

// ListActivePastExpiry returns active records whose expires_at is before now.
func (s *Store) ListActivePastExpiry(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT video_id
		FROM transient_metadata
		WHERE is_active = 1 AND expires_at < ?
		ORDER BY expires_at ASC
		LIMIT ?;`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: list active past expiry: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("metadata: scan expired id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: iterate expired ids: %w", err)
	}
	return ids, nil
}

// ListActiveBefore returns active records whose expires_at is before cutoff.
// This is used to refresh records that are nearing expiry.
func (s *Store) ListActiveBefore(ctx context.Context, cutoff time.Time, limit int) ([]RefreshCandidate, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT video_id, fetched_at, expires_at
		FROM transient_metadata
		WHERE is_active = 1 AND expires_at < ?
		ORDER BY expires_at ASC
		LIMIT ?;`, cutoff.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: list active before cutoff: %w", err)
	}
	defer rows.Close()

	out := make([]RefreshCandidate, 0)
	for rows.Next() {
		var c RefreshCandidate
		if err := rows.Scan(&c.VideoID, &c.FetchedAt, &c.ExpiresAt); err != nil {
			return nil, fmt.Errorf("metadata: scan refresh candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: iterate refresh candidates: %w", err)
	}
	return out, nil
}

// ListActiveRoutedBefore returns active records for videos that have at least
// one successful executed action and whose expires_at is before cutoff.
func (s *Store) ListActiveRoutedBefore(ctx context.Context, cutoff time.Time, limit int) ([]RefreshCandidate, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT tm.video_id, tm.fetched_at, tm.expires_at
		FROM transient_metadata tm
		JOIN video v ON v.video_id = tm.video_id
		WHERE tm.is_active = 1
		  AND tm.expires_at < ?
		  AND EXISTS (
				SELECT 1 FROM executed_action ea
				WHERE ea.video_id = v.id AND ea.outcome = 'succeeded'
			)
		ORDER BY tm.expires_at ASC
		LIMIT ?;`, cutoff.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: list active routed before cutoff: %w", err)
	}
	defer rows.Close()

	out := make([]RefreshCandidate, 0)
	for rows.Next() {
		var c RefreshCandidate
		if err := rows.Scan(&c.VideoID, &c.FetchedAt, &c.ExpiresAt); err != nil {
			return nil, fmt.Errorf("metadata: scan routed candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: iterate routed candidates: %w", err)
	}
	return out, nil
}

// Expire marks the given records as expired, clearing raw metadata fields.
func (s *Store) Expire(ctx context.Context, videoIDs []string, now time.Time) error {
	if len(videoIDs) == 0 {
		return nil
	}
	// The UPDATE fires the title-search triggers, which read the FTS table. A
	// deferred transaction would take a read snapshot first and then fail its
	// write upgrade with SQLITE_BUSY without waiting, so take the write lock
	// up front (the connection's busy_timeout then applies).
	expiredAt := now.UTC()
	err := inWriteTx(ctx, s.db, func(c *sql.Conn) error {
		stmt, err := c.PrepareContext(ctx, `
		UPDATE transient_metadata
		SET title = NULL,
		    channel_name = NULL,
		    description = NULL,
		    thumbnail_url = NULL,
		    expired_at = ?,
		    is_active = 0
		WHERE video_id = ?;`)
		if err != nil {
			return fmt.Errorf("metadata: prepare expire statement: %w", err)
		}
		defer stmt.Close()
		for _, id := range videoIDs {
			if _, err := stmt.ExecContext(ctx, expiredAt, id); err != nil {
				return fmt.Errorf("metadata: expire %s: %w", id, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("metadata: expire transaction: %w", err)
	}
	return nil
}

// Refresh updates Store A records with fresh metadata, resetting TTL fields.
func (s *Store) Refresh(ctx context.Context, items []TransientMetadata, ttl time.Duration, now time.Time) error {
	if len(items) == 0 {
		return nil
	}
	fetchedAt := now.UTC()
	expiresAt := fetchedAt.Add(ttl)
	// Write lock first; see Expire.
	err := inWriteTx(ctx, s.db, func(c *sql.Conn) error {
		stmt, err := c.PrepareContext(ctx, `
		UPDATE transient_metadata
		SET title = ?,
		    channel_name = ?,
		    description = ?,
		    published_at = ?,
		    duration_seconds = ?,
		    thumbnail_url = ?,
		    fetched_at = ?,
		    refreshed_at = ?,
		    expires_at = ?,
		    expired_at = NULL,
		    is_active = 1
		WHERE video_id = ?;`)
		if err != nil {
			return fmt.Errorf("metadata: prepare refresh statement: %w", err)
		}
		defer stmt.Close()
		for _, m := range items {
			if _, err := stmt.ExecContext(ctx,
				m.Title, m.ChannelName, m.Description, m.PublishedAt,
				m.DurationSeconds, m.ThumbnailURL, fetchedAt, fetchedAt,
				expiresAt, m.VideoID,
			); err != nil {
				return fmt.Errorf("metadata: refresh %s: %w", m.VideoID, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("metadata: refresh transaction: %w", err)
	}
	return nil
}

// UpsertFetched writes freshly fetched records in one write-lock-first
// transaction, inserting a row that was evicted or never held and updating one
// that exists. A nil field is written as NULL, never as a default, so a missing
// or invalid input stays distinguishable from a real empty or zero one. TTL
// fields are reset from now and ttl. The title-search triggers fire inside the
// same transaction. It returns the number of rows written, and fails if any
// statement does not write exactly one row.
func (s *Store) UpsertFetched(ctx context.Context, items []TransientMetadata, ttl time.Duration, now time.Time) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	fetchedAt := now.UTC()
	expiresAt := fetchedAt.Add(ttl)
	saved := 0
	err := inWriteTx(ctx, s.db, func(c *sql.Conn) error {
		stmt, err := c.PrepareContext(ctx, `
		INSERT INTO transient_metadata (
			video_id, title, channel_name, description, published_at,
			duration_seconds, thumbnail_url, fetched_at, refreshed_at,
			expires_at, expired_at, is_active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 1)
		ON CONFLICT(video_id) DO UPDATE SET
			title = excluded.title,
			channel_name = excluded.channel_name,
			description = excluded.description,
			published_at = excluded.published_at,
			duration_seconds = excluded.duration_seconds,
			thumbnail_url = excluded.thumbnail_url,
			fetched_at = excluded.fetched_at,
			refreshed_at = excluded.refreshed_at,
			expires_at = excluded.expires_at,
			expired_at = NULL,
			is_active = 1;`)
		if err != nil {
			return fmt.Errorf("metadata: prepare fetched upsert: %w", err)
		}
		defer stmt.Close()
		for _, m := range items {
			res, err := stmt.ExecContext(ctx,
				m.VideoID, m.Title, m.ChannelName, m.Description, m.PublishedAt,
				m.DurationSeconds, m.ThumbnailURL, fetchedAt, fetchedAt, expiresAt)
			if err != nil {
				return fmt.Errorf("metadata: upsert fetched %s: %w", m.VideoID, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("metadata: upsert fetched %s rows affected: %w", m.VideoID, err)
			}
			if n != 1 {
				return fmt.Errorf("metadata: upsert fetched %s wrote %d rows", m.VideoID, n)
			}
			saved++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("metadata: fetched upsert transaction: %w", err)
	}
	return saved, nil
}

// inputRow is a stored row with every route input and its TTL state.
type inputRow struct {
	LocalRow
	Description *string
}

// readInputRows reads the route inputs of ids (at most 50) in one statement.
func (s *Store) readInputRows(ctx context.Context, ids []string) ([]inputRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 50 {
		return nil, fmt.Errorf("metadata: input read exceeds 50 IDs")
	}
	args := make([]any, len(ids))
	query := `SELECT video_id, title, channel_name, description, duration_seconds, fetched_at, expires_at, expired_at, is_active FROM transient_metadata WHERE video_id IN (`
	for i, id := range ids {
		args[i] = id
		if i > 0 {
			query += ","
		}
		query += "?"
	}
	query += ")"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("metadata: read input rows: %w", err)
	}
	defer rows.Close()
	var out []inputRow
	for rows.Next() {
		var row inputRow
		var title, channel, desc sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(&row.VideoID, &title, &channel, &desc, &duration, &row.FetchedAt, &row.ExpiresAt, &row.ExpiredAt, &row.IsActive); err != nil {
			return nil, fmt.Errorf("metadata: scan input row: %w", err)
		}
		if title.Valid {
			row.Title = &title.String
		}
		if channel.Valid {
			row.ChannelName = &channel.String
		}
		if desc.Valid {
			row.Description = &desc.String
		}
		if duration.Valid {
			n := int(duration.Int64)
			row.DurationSeconds = &n
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: iterate input rows: %w", err)
	}
	return out, nil
}

// GetFingerprint returns the Store B fingerprint for videoID, or an empty
// string if no fingerprint exists.
func (s *Store) GetFingerprint(ctx context.Context, videoID string) (string, error) {
	var fp string
	err := s.db.QueryRowContext(ctx, `
		SELECT fingerprint FROM title_fingerprint
		WHERE video_id = ?;`, videoID).Scan(&fp)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("metadata: get fingerprint %s: %w", videoID, err)
	}
	return fp, nil
}

func scanMetadata(row *sql.Row) (*TransientMetadata, error) {
	var m TransientMetadata
	err := row.Scan(
		&m.VideoID, &m.Title, &m.ChannelName, &m.Description, &m.PublishedAt,
		&m.DurationSeconds, &m.ThumbnailURL, &m.FetchedAt, &m.RefreshedAt,
		&m.ExpiresAt, &m.ExpiredAt, &m.IsActive,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
