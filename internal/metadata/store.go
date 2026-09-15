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

	var ids []string
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

	var out []RefreshCandidate
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

	var out []RefreshCandidate
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metadata: begin expire transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
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

	expiredAt := now.UTC()
	for _, id := range videoIDs {
		if _, err := stmt.ExecContext(ctx, expiredAt, id); err != nil {
			return fmt.Errorf("metadata: expire %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metadata: commit expire transaction: %w", err)
	}
	return nil
}

// Refresh updates Store A records with fresh metadata, resetting TTL fields.
func (s *Store) Refresh(ctx context.Context, items []TransientMetadata, ttl time.Duration, now time.Time) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metadata: begin refresh transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
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

	fetchedAt := now.UTC()
	expiresAt := fetchedAt.Add(ttl)
	for _, m := range items {
		if _, err := stmt.ExecContext(ctx,
			m.Title, m.ChannelName, m.Description, m.PublishedAt,
			m.DurationSeconds, m.ThumbnailURL, fetchedAt, fetchedAt,
			expiresAt, m.VideoID,
		); err != nil {
			return fmt.Errorf("metadata: refresh %s: %w", m.VideoID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metadata: commit refresh transaction: %w", err)
	}
	return nil
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
