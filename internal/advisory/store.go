package advisory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/state"
)

// Store persists advisories and their user-action state.
type Store struct {
	db *sql.DB
}

// NewStore returns an advisory store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateAdvisory persists a new advisory.
func (s *Store) CreateAdvisory(ctx context.Context, channelID int64, typ AdvisoryType, evidence map[string]any) (*Advisory, error) {
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("advisory: marshal evidence: %w", err)
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO advisory (channel_id, advisory_type, status, evidence_snapshot)
		 VALUES (?, ?, ?, ?);`,
		channelID, string(typ), string(AdvisoryStatusActive), string(evidenceJSON))
	if err != nil {
		return nil, fmt.Errorf("advisory: create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("advisory: read id: %w", err)
	}
	return s.GetAdvisory(ctx, id)
}

// GetAdvisory loads an advisory by ID.
func (s *Store) GetAdvisory(ctx context.Context, id int64) (*Advisory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT a.id, a.channel_id, c.channel_id, c.channel_name, a.advisory_type, a.status,
		        a.generated_at, a.ignored_until, a.action_taken, a.dismissed_at, a.acted_upon_at,
		        a.evidence_snapshot
		 FROM advisory a
		 JOIN channel c ON c.id = a.channel_id
		 WHERE a.id = ?;`, id)
	adv, err := scanAdvisory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("advisory %d: %w", id, state.ErrNotFound)
		}
		return nil, fmt.Errorf("advisory: load %d: %w", id, err)
	}
	return &adv, nil
}

// ListActiveAdvisories returns all active advisories.
func (s *Store) ListActiveAdvisories(ctx context.Context) ([]Advisory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.channel_id, c.channel_id, c.channel_name, a.advisory_type, a.status,
		        a.generated_at, a.ignored_until, a.action_taken, a.dismissed_at, a.acted_upon_at,
		        a.evidence_snapshot
		 FROM advisory a
		 JOIN channel c ON c.id = a.channel_id
		 WHERE a.status = 'active'
		 ORDER BY a.generated_at DESC;`)
	if err != nil {
		return nil, fmt.Errorf("advisory: list active: %w", err)
	}
	defer rows.Close()

	out := make([]Advisory, 0)
	for rows.Next() {
		adv, err := scanAdvisory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, adv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("advisory: iterate active: %w", err)
	}
	return out, nil
}

// ListAdvisoriesForChannel returns advisories for a channel.
func (s *Store) ListAdvisoriesForChannel(ctx context.Context, channelIDText string) ([]Advisory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.channel_id, c.channel_id, c.channel_name, a.advisory_type, a.status,
		        a.generated_at, a.ignored_until, a.action_taken, a.dismissed_at, a.acted_upon_at,
		        a.evidence_snapshot
		 FROM advisory a
		 JOIN channel c ON c.id = a.channel_id
		 WHERE c.channel_id = ?
		 ORDER BY a.generated_at DESC;`, channelIDText)
	if err != nil {
		return nil, fmt.Errorf("advisory: list for channel %s: %w", channelIDText, err)
	}
	defer rows.Close()

	out := make([]Advisory, 0)
	for rows.Next() {
		adv, err := scanAdvisory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, adv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("advisory: iterate channel: %w", err)
	}
	return out, nil
}

// DismissAdvisory permanently dismisses an advisory.
func (s *Store) DismissAdvisory(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE advisory SET status = ?, dismissed_at = ? WHERE id = ? AND status = 'active';`,
		string(AdvisoryStatusDismissed), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("advisory: dismiss %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("advisory %d: %w", id, state.ErrNotFound)
	}
	return nil
}

// IgnoreAdvisory ignores an advisory for 30 days.
func (s *Store) IgnoreAdvisory(ctx context.Context, id int64) error {
	ignoredUntil := time.Now().UTC().Add(30 * 24 * time.Hour)
	res, err := s.db.ExecContext(ctx,
		`UPDATE advisory SET status = ?, ignored_until = ? WHERE id = ? AND status = 'active';`,
		string(AdvisoryStatusIgnored), ignoredUntil, id)
	if err != nil {
		return fmt.Errorf("advisory: ignore %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("advisory %d: %w", id, state.ErrNotFound)
	}
	return nil
}

// ActOnAdvisory records the action a user took on an advisory.
func (s *Store) ActOnAdvisory(ctx context.Context, id int64, action string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE advisory SET status = ?, action_taken = ?, acted_upon_at = ? WHERE id = ? AND status = 'active';`,
		string(AdvisoryStatusActedUpon), action, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("advisory: act %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("advisory %d: %w", id, state.ErrNotFound)
	}
	return nil
}

// HasActiveAdvisoryOfType reports whether a channel has an active advisory of
// the given type.
func (s *Store) HasActiveAdvisoryOfType(ctx context.Context, channelID int64, typ AdvisoryType) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM advisory WHERE channel_id = ? AND advisory_type = ? AND status = 'active';`,
		channelID, string(typ)).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("advisory: check active %s: %w", typ, err)
	}
	return n > 0, nil
}

// ExpireIgnoredAdvisories transitions ignored advisories whose ignore window
// has expired back to active so they can be re-evaluated.
func (s *Store) ExpireIgnoredAdvisories(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE advisory SET status = 'active', ignored_until = NULL
		 WHERE status = 'ignored' AND ignored_until <= ?;`, now.UTC())
	if err != nil {
		return fmt.Errorf("advisory: expire ignored: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAdvisory(sc scanner) (Advisory, error) {
	var a Advisory
	var ignoredUntil, actionTaken, dismissedAt, actedUponAt sql.NullString
	var evidenceJSON string
	err := sc.Scan(&a.ID, &a.ChannelID, &a.ChannelIDText, &a.ChannelName,
		&a.Type, &a.Status, &a.GeneratedAt, &ignoredUntil, &actionTaken,
		&dismissedAt, &actedUponAt, &evidenceJSON)
	if err != nil {
		return Advisory{}, err
	}
	if ignoredUntil.Valid {
		t, _ := time.Parse(time.RFC3339Nano, ignoredUntil.String)
		a.IgnoredUntil = &t
	}
	if actionTaken.Valid {
		a.ActionTaken = &actionTaken.String
	}
	if dismissedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, dismissedAt.String)
		a.DismissedAt = &t
	}
	if actedUponAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, actedUponAt.String)
		a.ActedUponAt = &t
	}
	if evidenceJSON != "" {
		_ = json.Unmarshal([]byte(evidenceJSON), &a.EvidenceSnapshot)
	}
	return a, nil
}

var _ scanner = (*sql.Row)(nil)
