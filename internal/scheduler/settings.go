package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// Store persists scheduler configuration in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore creates a scheduler settings store backed by db. The
// scheduler_settings table is owned by the central migration system and must
// already exist.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("scheduler: db is required")
	}
	return &Store{db: db}, nil
}

const (
	keyScheduleExpression = "schedule_expression"
	keyScheduleEnabled    = "schedule_enabled"
)

// Schedule holds the user-configured schedule.
type Schedule struct {
	Expression string
	Enabled    bool
}

// GetSchedule returns the stored schedule, or a zero Schedule if none exists.
func (s *Store) GetSchedule(ctx context.Context) (*Schedule, error) {
	expr, _, err := s.get(ctx, keyScheduleExpression)
	if err != nil {
		return nil, err
	}
	enabledStr, ok, err := s.get(ctx, keyScheduleEnabled)
	if err != nil {
		return nil, err
	}
	enabled := false
	if ok {
		enabled, _ = strconv.ParseBool(enabledStr)
	}
	return &Schedule{Expression: expr, Enabled: enabled}, nil
}

// SetSchedule stores the schedule configuration.
func (s *Store) SetSchedule(ctx context.Context, sch *Schedule) error {
	if sch == nil {
		return errors.New("scheduler: schedule is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("scheduler: begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.setTx(ctx, tx, keyScheduleExpression, sch.Expression); err != nil {
		return err
	}
	if err := s.setTx(ctx, tx, keyScheduleEnabled, strconv.FormatBool(sch.Enabled)); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM scheduler_settings WHERE key = ?`, key,
	).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("scheduler: read setting %q: %w", key, err)
	}
	return value, true, nil
}

func (s *Store) setTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO scheduler_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("scheduler: write setting %q: %w", key, err)
	}
	return nil
}
