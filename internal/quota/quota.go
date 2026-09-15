// Package quota implements YuHaul's QuotaLedger: per-operation/day cost tracking,
// pre-flight budget checks, and remaining-capacity reporting.
//
// The ledger does not assume a fixed provider reset time. When a reset schedule
// is configured (or an explicit next-reset time is reported by the API), the
// ledger uses it to compute bucket dates and the next reset. Until then, reset
// timing is reported as unknown.
package quota

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// DefaultDailyLimit is the default YouTube Data API v3 daily unit budget.
const DefaultDailyLimit = 10_000

// Operation names the categories of external API calls whose costs are tracked.
type Operation string

// Known YouTube Data API v3 operations and their unit costs per architecture.
const (
	OpSubscriptionsList   Operation = "subscriptions_list"
	OpChannelsList        Operation = "channels_list"
	OpPlaylistItemsList   Operation = "playlist_items_list"
	OpVideosList          Operation = "videos_list"
	OpPlaylistItemsInsert Operation = "playlist_items_insert"
	OpSearchList          Operation = "search_list"
)

// unitCosts maps each operation to its documented unit cost.
var unitCosts = map[Operation]int{
	OpSubscriptionsList:   1,
	OpChannelsList:        1,
	OpPlaylistItemsList:   1,
	OpVideosList:          1,
	OpPlaylistItemsInsert: 50,
	OpSearchList:          100,
}

// UnitCost returns the documented unit cost for op. Unknown operations are
// priced at 1 unit so they are still accounted for rather than silently lost.
func (op Operation) UnitCost() int {
	if c, ok := unitCosts[op]; ok {
		return c
	}
	return 1
}

// Priority orders processing phases when quota is scarce. Lower numeric values
// are processed first; action execution and metadata refresh are deferred first.
type Priority int

const (
	PriorityDiscovery       Priority = 1
	PriorityMetadataFetch   Priority = 2
	PriorityRouting         Priority = 3
	PriorityActionExecution Priority = 4
	PriorityMetadataRefresh Priority = 5
)

// BudgetSnapshot is the remaining quota state for the current bucket.
type BudgetSnapshot struct {
	Date       string    // ISO date (YYYY-MM-DD) of the current quota bucket.
	DailyLimit int       // Configured daily limit in units.
	Consumed   int       // Units already consumed in this bucket.
	Remaining  int       // Units still available in this bucket (>= 0).
	NextReset  time.Time // Estimated next reset time, if known.
	ResetKnown bool      // True when NextReset is based on a configured schedule or API value.
}

// Ledger tracks API quota consumption in SQLite.
type Ledger struct {
	db           *sql.DB
	logger       *slog.Logger
	defaultLimit int
}

// Option configures a Ledger.
type Option func(*Ledger)

// WithLogger sets the structured logger used by the ledger. If nil, the
// default logger is used.
func WithLogger(logger *slog.Logger) Option {
	return func(l *Ledger) {
		if logger != nil {
			l.logger = logger
		}
	}
}

// WithDefaultLimit sets the daily limit used when no setting has been stored.
func WithDefaultLimit(limit int) Option {
	return func(l *Ledger) {
		l.defaultLimit = limit
	}
}

// New returns a Ledger backed by db. The quota tables are owned by the central
// migration system and must already exist.
func New(db *sql.DB, opts ...Option) *Ledger {
	l := &Ledger{
		db:           db,
		logger:       slog.Default(),
		defaultLimit: DefaultDailyLimit,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Record records a single API call in the ledger.
func (l *Ledger) Record(ctx context.Context, runID sql.NullInt64, op Operation) error {
	return l.RecordCalls(ctx, runID, op, 1)
}

// RecordCalls records multiple API calls of the same type in one ledger row.
func (l *Ledger) RecordCalls(ctx context.Context, runID sql.NullInt64, op Operation, callCount int) error {
	if callCount <= 0 {
		return nil
	}
	units := op.UnitCost() * callCount
	date, err := l.bucketDate(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("quota: determine bucket date: %w", err)
	}

	_, err = l.db.ExecContext(ctx,
		`INSERT INTO quota_ledger (date, operation_type, units_consumed, call_count, run_id, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		date, string(op), units, callCount, runID, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("quota: record %s x%d: %w", op, callCount, err)
	}

	l.logger.DebugContext(ctx, "quota recorded",
		"operation", string(op),
		"units", units,
		"call_count", callCount,
		"date", date,
	)
	return nil
}

// ConsumedOn returns the total units consumed for the given bucket date.
func (l *Ledger) ConsumedOn(ctx context.Context, date string) (int, error) {
	var consumed sql.NullInt64
	err := l.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(units_consumed), 0) FROM quota_ledger WHERE date = ?`, date,
	).Scan(&consumed)
	if err != nil {
		return 0, fmt.Errorf("quota: sum consumption for %s: %w", date, err)
	}
	return int(consumed.Int64), nil
}

// ConsumedByRun returns the total units consumed on behalf of runID across all
// bucket dates. Runs that pre-date per-run attribution report 0.
func (l *Ledger) ConsumedByRun(ctx context.Context, runID int64) (int, error) {
	var consumed sql.NullInt64
	err := l.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(units_consumed), 0) FROM quota_ledger WHERE run_id = ?`, runID,
	).Scan(&consumed)
	if err != nil {
		return 0, fmt.Errorf("quota: sum consumption for run %d: %w", runID, err)
	}
	return int(consumed.Int64), nil
}

// ConsumedToday returns the total units consumed for the current bucket date.
func (l *Ledger) ConsumedToday(ctx context.Context) (int, error) {
	date, err := l.bucketDate(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	return l.ConsumedOn(ctx, date)
}

// Budget returns the remaining quota state for the current bucket.
func (l *Ledger) Budget(ctx context.Context) (*BudgetSnapshot, error) {
	limit, err := l.DailyLimit(ctx)
	if err != nil {
		return nil, fmt.Errorf("quota: read daily limit: %w", err)
	}

	date, err := l.bucketDate(ctx, time.Now())
	if err != nil {
		return nil, fmt.Errorf("quota: determine bucket date: %w", err)
	}

	consumed, err := l.ConsumedOn(ctx, date)
	if err != nil {
		return nil, err
	}

	remaining := limit - consumed
	if remaining < 0 {
		remaining = 0
	}

	nextReset, known, err := l.nextReset(ctx, time.Now())
	if err != nil {
		return nil, fmt.Errorf("quota: compute next reset: %w", err)
	}

	return &BudgetSnapshot{
		Date:       date,
		DailyLimit: limit,
		Consumed:   consumed,
		Remaining:  remaining,
		NextReset:  nextReset,
		ResetKnown: known,
	}, nil
}

// CanAfford reports whether estimate additional units can be spent without
// exceeding the daily limit. It returns the remaining capacity for the current
// bucket. If the limit is unlimited (<= 0), the check always succeeds.
func (l *Ledger) CanAfford(ctx context.Context, estimate int) (bool, int, error) {
	budget, err := l.Budget(ctx)
	if err != nil {
		return false, 0, err
	}

	if budget.DailyLimit <= 0 {
		return true, budget.Remaining, nil
	}

	ok := budget.Consumed+estimate <= budget.DailyLimit
	remaining := budget.Remaining
	if !ok {
		remaining = budget.DailyLimit - budget.Consumed - estimate
		if remaining < 0 {
			remaining = 0
		}
	}

	if !ok {
		l.logger.WarnContext(ctx, "quota pre-flight check failed",
			"estimate", estimate,
			"consumed", budget.Consumed,
			"daily_limit", budget.DailyLimit,
			"date", budget.Date,
		)
	}
	return ok, remaining, nil
}

// DailyLimit returns the configured daily limit, falling back to the default.
func (l *Ledger) DailyLimit(ctx context.Context) (int, error) {
	v, ok, err := l.getSetting(ctx, "daily_limit")
	if err != nil {
		return 0, err
	}
	if !ok {
		return l.defaultLimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("quota: invalid daily_limit setting %q: %w", v, err)
	}
	return n, nil
}

// SetDailyLimit stores the daily limit.
func (l *Ledger) SetDailyLimit(ctx context.Context, limit int) error {
	return l.setSetting(ctx, "daily_limit", strconv.Itoa(limit))
}

// ResetSchedule describes when the provider's daily quota bucket resets.
type ResetSchedule struct {
	Timezone  string // IANA timezone name, e.g. "America/Los_Angeles".
	TimeOfDay string // Time of day in "HH:MM" or "HH:MM:SS".
}

// ResetSchedule returns the stored reset schedule, if any.
func (l *Ledger) ResetSchedule(ctx context.Context) (*ResetSchedule, bool, error) {
	tz, okTZ, err := l.getSetting(ctx, "reset_timezone")
	if err != nil {
		return nil, false, err
	}
	tod, okTOD, err := l.getSetting(ctx, "reset_time")
	if err != nil {
		return nil, false, err
	}
	if !okTZ || !okTOD {
		return nil, false, nil
	}
	return &ResetSchedule{Timezone: tz, TimeOfDay: tod}, true, nil
}

// SetResetSchedule stores the daily reset schedule and clears any stale
// API-reported next-reset value.
func (l *Ledger) SetResetSchedule(ctx context.Context, tz, timeOfDay string) error {
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("quota: invalid timezone %q: %w", tz, err)
	}
	if _, err := parseTimeOfDay(timeOfDay); err != nil {
		return fmt.Errorf("quota: invalid time_of_day %q: %w", timeOfDay, err)
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("quota: begin schedule transaction: %w", err)
	}
	defer tx.Rollback()

	if err := l.setSettingTx(ctx, tx, "reset_timezone", tz); err != nil {
		return err
	}
	if err := l.setSettingTx(ctx, tx, "reset_time", timeOfDay); err != nil {
		return err
	}
	// Clear a previously-recorded absolute reset time because the schedule changed.
	if _, err := tx.ExecContext(ctx, `DELETE FROM quota_settings WHERE key = 'reset_next_at'`); err != nil {
		return fmt.Errorf("quota: clear reset_next_at: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("quota: commit schedule transaction: %w", err)
	}
	return nil
}

// RecordAPIReset records an explicit next-reset time reported by the provider.
// This takes precedence over the configured schedule until the time passes.
func (l *Ledger) RecordAPIReset(ctx context.Context, resetTime time.Time) error {
	return l.setSetting(ctx, "reset_next_at", resetTime.UTC().Format(time.RFC3339))
}

// settings helpers.

func (l *Ledger) getSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := l.db.QueryRowContext(ctx,
		`SELECT value FROM quota_settings WHERE key = ?`, key,
	).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("quota: read setting %q: %w", key, err)
	}
	return value, true, nil
}

func (l *Ledger) setSetting(ctx context.Context, key, value string) error {
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO quota_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("quota: write setting %q: %w", key, err)
	}
	return nil
}

func (l *Ledger) setSettingTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO quota_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("quota: write setting %q: %w", key, err)
	}
	return nil
}

// bucketDate returns the ISO date of the quota bucket containing now.
func (l *Ledger) bucketDate(ctx context.Context, now time.Time) (string, error) {
	sch, ok, err := l.ResetSchedule(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		// Without a configured schedule, fall back to UTC calendar day.
		return now.UTC().Format("2006-01-02"), nil
	}

	loc, err := time.LoadLocation(sch.Timezone)
	if err != nil {
		return "", fmt.Errorf("quota: load timezone %q: %w", sch.Timezone, err)
	}
	offset, err := parseTimeOfDay(sch.TimeOfDay)
	if err != nil {
		return "", fmt.Errorf("quota: parse time of day %q: %w", sch.TimeOfDay, err)
	}

	return bucketDateFor(now, loc, offset), nil
}

// nextReset returns the next reset time, if one can be determined.
func (l *Ledger) nextReset(ctx context.Context, now time.Time) (time.Time, bool, error) {
	// A provider-reported absolute reset time takes precedence while it is still
	// in the future.
	v, ok, err := l.getSetting(ctx, "reset_next_at")
	if err != nil {
		return time.Time{}, false, err
	}
	if ok {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("quota: parse reset_next_at: %w", err)
		}
		if t.After(now) {
			return t, true, nil
		}
		// Expired; drop it and fall back to the schedule.
		if err := l.setSetting(ctx, "reset_next_at", ""); err != nil {
			return time.Time{}, false, err
		}
	}

	sch, ok, err := l.ResetSchedule(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	if !ok {
		return time.Time{}, false, nil
	}

	loc, err := time.LoadLocation(sch.Timezone)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("quota: load timezone %q: %w", sch.Timezone, err)
	}
	offset, err := parseTimeOfDay(sch.TimeOfDay)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("quota: parse time of day %q: %w", sch.TimeOfDay, err)
	}
	return nextResetAfter(now, loc, offset), true, nil
}

// time helpers.

func parseTimeOfDay(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("expected HH:MM or HH:MM:SS")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid hour: %w", err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid minute: %w", err)
	}
	sec := 0
	if len(parts) == 3 {
		sec, err = strconv.Atoi(parts[2])
		if err != nil {
			return 0, fmt.Errorf("invalid second: %w", err)
		}
	}
	if h < 0 || h > 23 || m < 0 || m > 59 || sec < 0 || sec > 59 {
		return 0, fmt.Errorf("time out of range")
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, nil
}

// nextResetAfter returns the next reset at or after now for the given schedule.
func nextResetAfter(now time.Time, loc *time.Location, offset time.Duration) time.Time {
	t := now.In(loc)
	year, month, day := t.Date()
	todayReset := time.Date(year, month, day, 0, 0, 0, 0, loc).Add(offset)
	if todayReset.Equal(now) || todayReset.After(now) {
		return todayReset.UTC()
	}
	return todayReset.Add(24 * time.Hour).UTC()
}

// bucketDateFor returns the ISO date (in loc) of the bucket containing now.
func bucketDateFor(now time.Time, loc *time.Location, offset time.Duration) string {
	t := now.In(loc)
	year, month, day := t.Date()
	todayReset := time.Date(year, month, day, 0, 0, 0, 0, loc).Add(offset)
	if now.Equal(todayReset) || now.After(todayReset) {
		return time.Date(year, month, day, 0, 0, 0, 0, loc).Format("2006-01-02")
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc).Add(-24 * time.Hour).Format("2006-01-02")
}
