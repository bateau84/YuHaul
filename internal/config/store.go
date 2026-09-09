package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/state"
)

// Store persists ConfigVersion snapshots and reads effective settings.
type Store struct {
	db *sql.DB
}

// NewStore returns a config store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create persists a new ConfigVersion and returns its generated ID.
func (s *Store) Create(ctx context.Context, snapshot Snapshot, description string) (int64, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return 0, fmt.Errorf("config: marshal snapshot: %w", err)
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO config_version (created_at, snapshot, change_description)
		 VALUES (?, ?, ?);`,
		time.Now().UTC(), string(data), description)
	if err != nil {
		return 0, fmt.Errorf("config: insert version: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("config: read version id: %w", err)
	}
	return id, nil
}

// Get loads a ConfigVersion by ID.
func (s *Store) Get(ctx context.Context, id int64) (*ConfigVersion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, created_at, snapshot, change_description
		 FROM config_version WHERE id = ?;`, id)
	cv, err := scanConfigVersion(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("config version %d: %w", id, state.ErrNotFound)
		}
		return nil, fmt.Errorf("config: load version %d: %w", id, err)
	}
	return cv, nil
}

// List returns all ConfigVersions ordered from oldest to newest.
func (s *Store) List(ctx context.Context) ([]ConfigVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, snapshot, change_description
		 FROM config_version ORDER BY id;`)
	if err != nil {
		return nil, fmt.Errorf("config: list versions: %w", err)
	}
	defer rows.Close()

	var out []ConfigVersion
	for rows.Next() {
		cv, err := scanConfigVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("config: scan version: %w", err)
		}
		out = append(out, *cv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("config: iterate versions: %w", err)
	}
	return out, nil
}

// LatestID returns the most recent ConfigVersion ID, or nil if none exist.
func (s *Store) LatestID(ctx context.Context) (*int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM config_version ORDER BY id DESC LIMIT 1;`).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("config: latest version: %w", err)
	}
	if !id.Valid {
		return nil, nil
	}
	return &id.Int64, nil
}

// GetRunsForVersion returns runs that executed under the given config version.
func (s *Store) GetRunsForVersion(ctx context.Context, versionID int64) ([]state.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, completed_at, run_type, status, config_version_id,
		        videos_discovered, videos_accepted, videos_rejected, videos_deferred,
		        videos_failed, videos_not_yet_evaluated, quota_cost, duration_seconds,
		        interrupt_reason
		 FROM run WHERE config_version_id = ? ORDER BY id;`, versionID)
	if err != nil {
		return nil, fmt.Errorf("config: query runs for version %d: %w", versionID, err)
	}
	defer rows.Close()

	var out []state.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("config: scan run: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("config: iterate runs: %w", err)
	}
	return out, nil
}

// GetSetting reads a setting value by key. If the key is absent, an empty
// string and no error are returned so that callers can apply defaults.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?;`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("config: get setting %s: %w", key, err)
	}
	return value, nil
}

// SetSetting writes a setting value, replacing any existing value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value;`,
		key, value)
	if err != nil {
		return fmt.Errorf("config: set setting %s: %w", key, err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanConfigVersion(sc scanner) (*ConfigVersion, error) {
	cv := &ConfigVersion{}
	var createdAt string
	var snapshotJSON string
	var desc sql.NullString
	err := sc.Scan(&cv.ID, &createdAt, &snapshotJSON, &desc)
	if err != nil {
		return nil, err
	}
	cv.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		cv.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
	}
	if desc.Valid {
		cv.ChangeDescription = desc.String
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &cv.Snapshot); err != nil {
		return nil, fmt.Errorf("config: unmarshal snapshot: %w", err)
	}
	return cv, nil
}

func scanRun(sc scanner) (*state.Run, error) {
	r := &state.Run{}
	var completedAt, interruptReason sql.NullString
	var durationSeconds sql.NullFloat64
	var configVersionID sql.NullInt64
	err := sc.Scan(&r.ID, &r.StartedAt, &completedAt, &r.RunType, &r.Status,
		&configVersionID, &r.VideosDiscovered, &r.VideosAccepted, &r.VideosRejected,
		&r.VideosDeferred, &r.VideosFailed, &r.VideosNotYetEvaluated, &r.QuotaCost,
		&durationSeconds, &interruptReason)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
		r.CompletedAt = &t
	}
	if configVersionID.Valid {
		r.ConfigVersionID = &configVersionID.Int64
	}
	if durationSeconds.Valid {
		r.DurationSeconds = &durationSeconds.Float64
	}
	if interruptReason.Valid {
		r.InterruptReason = &interruptReason.String
	}
	return r, nil
}

// compile-time interface check.
var _ scanner = (*sql.Row)(nil)
