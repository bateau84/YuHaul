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
	return s.createVersion(ctx, s.db, snapshot, description)
}

// CreateTx persists a new ConfigVersion inside the caller's transaction and
// returns its generated ID. It exists so proposal ingest (ADR-012) can commit
// the translated immutable snapshot in the same SQLite transaction as its
// identity anchors and run row.
func (s *Store) CreateTx(ctx context.Context, tx *sql.Tx, snapshot Snapshot, description string) (int64, error) {
	return s.createVersion(ctx, tx, snapshot, description)
}

func (s *Store) createVersion(ctx context.Context, db execer, snapshot Snapshot, description string) (int64, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return 0, fmt.Errorf("config: marshal snapshot: %w", err)
	}

	res, err := db.ExecContext(ctx,
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

	out := make([]ConfigVersion, 0)
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

// MatchingID returns the newest immutable version with exactly this effective
// configuration, or nil when it has not been captured yet. History insertion
// alone is not activation: proposal snapshots share this table with saved ones.
func (s *Store) MatchingID(ctx context.Context, snapshot Snapshot) (*int64, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("config: marshal effective snapshot: %w", err)
	}
	var id int64
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM config_version WHERE snapshot = ? ORDER BY id DESC LIMIT 1;`, string(data)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: matching version: %w", err)
	}
	return &id, nil
}

// GetRunsForVersion returns runs that executed under the given config version.
func (s *Store) GetRunsForVersion(ctx context.Context, versionID int64) ([]state.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, completed_at, run_type, status, config_version_id,
		        videos_discovered, videos_accepted, videos_rejected, videos_excluded,
		        videos_deferred, videos_failed, videos_not_yet_evaluated, quota_cost,
		        duration_seconds, interrupt_reason
		 FROM run WHERE config_version_id = ? ORDER BY id;`, versionID)
	if err != nil {
		return nil, fmt.Errorf("config: query runs for version %d: %w", versionID, err)
	}
	defer rows.Close()

	out := make([]state.Run, 0)
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

// RouteIdentities returns the complete route_identity registry as an
// internal-key → public-UUID map (ADR-011/ARS-004). The registry is
// append-only and covers snapshot-only historical keys as well as active
// routes, so historical ConfigVersion snapshots resolve exactly like current
// ones. A key absent from the returned map has no registry mapping; callers
// must fail rather than fall back to an integer or a name.
func (s *Store) RouteIdentities(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT route_id, public_id FROM route_identity ORDER BY route_id;`)
	if err != nil {
		return nil, fmt.Errorf("config: list route identities: %w", err)
	}
	defer rows.Close()

	m := make(map[int64]string)
	for rows.Next() {
		var id int64
		var publicID string
		if err := rows.Scan(&id, &publicID); err != nil {
			return nil, fmt.Errorf("config: scan route identity: %w", err)
		}
		m[id] = publicID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("config: iterate route identities: %w", err)
	}
	return m, nil
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

// execer is satisfied by *sql.DB and *sql.Tx so writes can join a caller's
// transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
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
		&r.VideosExcluded, &r.VideosDeferred, &r.VideosFailed, &r.VideosNotYetEvaluated,
		&r.QuotaCost, &durationSeconds, &interruptReason)
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
