package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store persists and queries the error and deferred queues.
type Store struct {
	db *sql.DB
}

// NewStore creates a queue store backed by db. The caller retains ownership of
// db. Required tables and indexes are created idempotently.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("queue: db is required")
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("queue: migrating store: %w", err)
	}
	return s, nil
}

// migrate creates the tables required by the queue. It is idempotent and
// matches the canonical schema in internal/store/migrations/001_initial_schema.up.sql.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS error_entry (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
			run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
			error_category TEXT NOT NULL CHECK(error_category IN ('api_error', 'authorization', 'runtime')),
			error_message TEXT NOT NULL,
			error_detail TEXT,
			status TEXT NOT NULL CHECK(status IN ('active', 'retrying', 'dismissed', 'resolved')),
			created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			resolved_at TIMESTAMP,
			retry_count INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS deferred_item (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
			run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
			deferral_reason TEXT NOT NULL CHECK(deferral_reason IN ('quota_exhausted', 'api_rate_limited')),
			retry_after TIMESTAMP,
			retry_count INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL CHECK(status IN ('pending', 'retrying', 'completed', 'discarded')),
			created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);`,
		`CREATE INDEX IF NOT EXISTS idx_error_entry_status ON error_entry(status);`,
		`CREATE INDEX IF NOT EXISTS idx_error_entry_video_id ON error_entry(video_id);`,
		`CREATE INDEX IF NOT EXISTS idx_error_entry_run_id ON error_entry(run_id);`,
		`CREATE INDEX IF NOT EXISTS idx_deferred_item_status ON deferred_item(status);`,
		`CREATE INDEX IF NOT EXISTS idx_deferred_item_video_id ON deferred_item(video_id);`,
		`CREATE INDEX IF NOT EXISTS idx_deferred_item_run_id ON deferred_item(run_id);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing schema: %w", err)
		}
	}
	return nil
}

// CreateErrorEntry persists a new error queue entry and returns its ID.
func (s *Store) CreateErrorEntry(ctx context.Context, e *ErrorEntry) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO error_entry (video_id, run_id, error_category, error_message, error_detail, status, created_at, retry_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
		e.VideoID, e.RunID, e.Category, e.Message, e.Detail, e.Status, e.CreatedAt.UTC().Format(time.RFC3339), e.RetryCount)
	if err != nil {
		return 0, fmt.Errorf("creating error entry: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading error entry id: %w", err)
	}
	return id, nil
}

// ListErrorEntries returns error entries filtered by status. Pass an empty
// string to return entries of every status.
func (s *Store) ListErrorEntries(ctx context.Context, status ErrorStatus) ([]ErrorEntry, error) {
	var rows *sql.Rows
	var err error

	if status == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT e.id, e.video_id, v.video_id, e.run_id, e.error_category, e.error_message,
			        e.error_detail, e.status, e.created_at, e.resolved_at, e.retry_count
			 FROM error_entry e
			 JOIN video v ON v.id = e.video_id
			 ORDER BY e.created_at DESC;`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT e.id, e.video_id, v.video_id, e.run_id, e.error_category, e.error_message,
			        e.error_detail, e.status, e.created_at, e.resolved_at, e.retry_count
			 FROM error_entry e
			 JOIN video v ON v.id = e.video_id
			 WHERE e.status = ?
			 ORDER BY e.created_at DESC;`, status)
	}
	if err != nil {
		return nil, fmt.Errorf("querying error entries: %w", err)
	}
	defer rows.Close()

	return scanErrorEntries(rows)
}

// GetErrorEntry loads a single error entry by ID.
func (s *Store) GetErrorEntry(ctx context.Context, id int64) (*ErrorEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT e.id, e.video_id, v.video_id, e.run_id, e.error_category, e.error_message,
		        e.error_detail, e.status, e.created_at, e.resolved_at, e.retry_count
		 FROM error_entry e
		 JOIN video v ON v.id = e.video_id
		 WHERE e.id = ?;`, id)
	e, err := scanErrorEntry(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("error entry %d: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("loading error entry %d: %w", id, err)
	}
	return e, nil
}

// DismissErrorEntry marks an error entry as dismissed, preserving the row for
// historical inspection.
func (s *Store) DismissErrorEntry(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE error_entry
		 SET status = ?, resolved_at = ?
		 WHERE id = ? AND status != ?;`,
		ErrorStatusDismissed, time.Now().UTC().Format(time.RFC3339), id, ErrorStatusDismissed)
	if err != nil {
		return fmt.Errorf("dismissing error entry %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("error entry %d: %w", id, ErrNotFound)
	}
	return nil
}

// ResolveErrorEntry marks an error entry as resolved.
func (s *Store) ResolveErrorEntry(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE error_entry
		 SET status = ?, resolved_at = ?
		 WHERE id = ?;`,
		ErrorStatusResolved, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("resolving error entry %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("error entry %d: %w", id, ErrNotFound)
	}
	return nil
}

// SetErrorEntryRetrying marks an error entry as retrying and increments its
// retry counter.
func (s *Store) SetErrorEntryRetrying(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE error_entry
		 SET status = ?, retry_count = retry_count + 1
		 WHERE id = ?;`,
		ErrorStatusRetrying, id)
	if err != nil {
		return fmt.Errorf("marking error entry %d retrying: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("error entry %d: %w", id, ErrNotFound)
	}
	return nil
}

// UpdateErrorEntryFailure re-records an active failure after a retry attempt.
func (s *Store) UpdateErrorEntryFailure(ctx context.Context, id int64, message string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE error_entry
		 SET status = ?, error_message = ?, resolved_at = NULL
		 WHERE id = ?;`,
		ErrorStatusActive, message, id)
	if err != nil {
		return fmt.Errorf("updating error entry %d failure: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("error entry %d: %w", id, ErrNotFound)
	}
	return nil
}

// CreateDeferredItem persists a new deferred work item and returns its ID.
func (s *Store) CreateDeferredItem(ctx context.Context, d *DeferredItem) (int64, error) {
	var retryAfter *string
	if d.RetryAfter != nil {
		rs := d.RetryAfter.UTC().Format(time.RFC3339)
		retryAfter = &rs
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?);`,
		d.VideoID, d.RunID, d.Reason, retryAfter, d.RetryCount, d.Status, d.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("creating deferred item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading deferred item id: %w", err)
	}
	return id, nil
}

// ListDeferredItems returns deferred items filtered by status. Pass an empty
// string to return entries of every status.
func (s *Store) ListDeferredItems(ctx context.Context, status DeferredStatus) ([]DeferredItem, error) {
	var rows *sql.Rows
	var err error

	if status == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after,
			        d.retry_count, d.status, d.created_at
			 FROM deferred_item d
			 JOIN video v ON v.id = d.video_id
			 ORDER BY d.created_at ASC;`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after,
			        d.retry_count, d.status, d.created_at
			 FROM deferred_item d
			 JOIN video v ON v.id = d.video_id
			 WHERE d.status = ?
			 ORDER BY d.created_at ASC;`, status)
	}
	if err != nil {
		return nil, fmt.Errorf("querying deferred items: %w", err)
	}
	defer rows.Close()

	return scanDeferredItems(rows)
}

// GetDeferredItem loads a single deferred item by ID.
func (s *Store) GetDeferredItem(ctx context.Context, id int64) (*DeferredItem, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after,
		        d.retry_count, d.status, d.created_at
		 FROM deferred_item d
		 JOIN video v ON v.id = d.video_id
		 WHERE d.id = ?;`, id)
	d, err := scanDeferredItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("deferred item %d: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("loading deferred item %d: %w", id, err)
	}
	return d, nil
}

// MarkDeferredRetrying marks a deferred item as retrying and increments its
// retry counter.
func (s *Store) MarkDeferredRetrying(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE deferred_item
		 SET status = ?, retry_count = retry_count + 1
		 WHERE id = ?;`,
		DeferredStatusRetrying, id)
	if err != nil {
		return fmt.Errorf("marking deferred item %d retrying: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deferred item %d: %w", id, ErrNotFound)
	}
	return nil
}

// MarkDeferredCompleted marks a deferred item as completed.
func (s *Store) MarkDeferredCompleted(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE deferred_item
		 SET status = ?
		 WHERE id = ?;`,
		DeferredStatusCompleted, id)
	if err != nil {
		return fmt.Errorf("completing deferred item %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deferred item %d: %w", id, ErrNotFound)
	}
	return nil
}

// MarkDeferredPending reverts a deferred item to pending after a failed retry.
func (s *Store) MarkDeferredPending(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE deferred_item
		 SET status = ?
		 WHERE id = ?;`,
		DeferredStatusPending, id)
	if err != nil {
		return fmt.Errorf("reverting deferred item %d to pending: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deferred item %d: %w", id, ErrNotFound)
	}
	return nil
}

// DiscardDeferredItem marks a deferred item as discarded, preserving the row
// for historical inspection.
func (s *Store) DiscardDeferredItem(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE deferred_item
		 SET status = ?
		 WHERE id = ? AND status != ?;`,
		DeferredStatusDiscarded, id, DeferredStatusDiscarded)
	if err != nil {
		return fmt.Errorf("discarding deferred item %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deferred item %d: %w", id, ErrNotFound)
	}
	return nil
}

// ErrNotFound indicates that a requested queue entry does not exist.
var ErrNotFound = errors.New("queue: not found")

func scanErrorEntry(sc interface{ Scan(dest ...any) error }) (*ErrorEntry, error) {
	e := &ErrorEntry{}
	var detail, resolvedAt sql.NullString
	err := sc.Scan(&e.ID, &e.VideoID, &e.ExternalVideoID, &e.RunID, &e.Category, &e.Message,
		&detail, &e.Status, &e.CreatedAt, &resolvedAt, &e.RetryCount)
	if err != nil {
		return nil, err
	}
	if detail.Valid {
		e.Detail = detail.String
	}
	if resolvedAt.Valid {
		t, _ := time.Parse(time.RFC3339, resolvedAt.String)
		e.ResolvedAt = &t
	}
	return e, nil
}

func scanErrorEntries(rows *sql.Rows) ([]ErrorEntry, error) {
	var out []ErrorEntry
	for rows.Next() {
		e, err := scanErrorEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning error entry: %w", err)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating error entries: %w", err)
	}
	return out, nil
}

func scanDeferredItem(sc interface{ Scan(dest ...any) error }) (*DeferredItem, error) {
	d := &DeferredItem{}
	var retryAfter sql.NullString
	err := sc.Scan(&d.ID, &d.VideoID, &d.ExternalVideoID, &d.RunID, &d.Reason, &retryAfter,
		&d.RetryCount, &d.Status, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	if retryAfter.Valid {
		t, _ := time.Parse(time.RFC3339, retryAfter.String)
		d.RetryAfter = &t
	}
	return d, nil
}

func scanDeferredItems(rows *sql.Rows) ([]DeferredItem, error) {
	var out []DeferredItem
	for rows.Next() {
		d, err := scanDeferredItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning deferred item: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating deferred items: %w", err)
	}
	return out, nil
}
