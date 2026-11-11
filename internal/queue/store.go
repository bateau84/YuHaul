package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
)

// Store persists and queries the error and deferred queues.
type Store struct {
	db *sql.DB
}

// NewStore creates a queue store backed by db. The caller retains ownership of
// db. The error_entry and deferred_item tables are owned by the central
// migration system and must already exist.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("queue: db is required")
	}
	return &Store{db: db}, nil
}

// CreateErrorEntry persists a new error queue entry and returns its ID.
func (s *Store) CreateErrorEntry(ctx context.Context, e *ErrorEntry) (int64, error) {
	e.Message = diag.GuardErrorText(e.Message)
	e.Detail = diag.GuardErrorText(e.Detail)
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
			        e.error_detail, e.status, e.created_at, e.resolved_at, e.retry_count, e.retry_run_id
			 FROM error_entry e
			 JOIN video v ON v.id = e.video_id
			 ORDER BY e.created_at DESC;`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT e.id, e.video_id, v.video_id, e.run_id, e.error_category, e.error_message,
			        e.error_detail, e.status, e.created_at, e.resolved_at, e.retry_count, e.retry_run_id
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
		        e.error_detail, e.status, e.created_at, e.resolved_at, e.retry_count, e.retry_run_id
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
	message = diag.GuardErrorText(message)
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
	if !d.Reason.Valid() {
		return 0, errors.New("creating deferred item: invalid reason")
	}
	return s.createDeferredItem(ctx, s.db, d)
}

// CreateDeferredItemTx persists a deferred work item inside tx.
func (s *Store) CreateDeferredItemTx(ctx context.Context, tx *sql.Tx, d *DeferredItem) (int64, error) {
	if tx == nil {
		return 0, errors.New("creating deferred item: transaction is required")
	}
	if !d.Reason.Valid() {
		return 0, errors.New("creating deferred item: invalid reason")
	}
	var retryAfter *string
	if d.RetryAfter != nil {
		rs := d.RetryAfter.UTC().Format(time.RFC3339)
		retryAfter = &rs
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at)
		 SELECT ?, ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM deferred_item WHERE video_id = ? AND status IN ('pending','retrying'));`,
		d.VideoID, d.RunID, d.Reason, retryAfter, d.RetryCount, d.Status, d.CreatedAt.UTC().Format(time.RFC3339), d.VideoID)
	if err != nil {
		return 0, fmt.Errorf("creating deferred item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading deferred item id: %w", err)
	}
	return id, nil
}

func (s *Store) createDeferredItem(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, d *DeferredItem) (int64, error) {
	var retryAfter *string
	if d.RetryAfter != nil {
		rs := d.RetryAfter.UTC().Format(time.RFC3339)
		retryAfter = &rs
	}
	res, err := exec.ExecContext(ctx,
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
			        d.retry_count, d.status, d.created_at, d.retry_run_id
			 FROM deferred_item d
			 JOIN video v ON v.id = d.video_id
			 ORDER BY d.created_at ASC;`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after,
			        d.retry_count, d.status, d.created_at, d.retry_run_id
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
		        d.retry_count, d.status, d.created_at, d.retry_run_id
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
		&detail, &e.Status, &e.CreatedAt, &resolvedAt, &e.RetryCount, &e.RetryRunID)
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
	out := make([]ErrorEntry, 0)
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
		&d.RetryCount, &d.Status, &d.CreatedAt, &d.RetryRunID)
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
	out := make([]DeferredItem, 0)
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

// Errors returned by ListDeferredPage for a malformed query.
var (
	ErrInvalidDeferredPage   = errors.New("queue: invalid deferred page query")
	ErrInvalidDeferredReason = errors.New("queue: invalid deferred reason")
)

// MaxDeferredPageLimit is the largest page ListDeferredPage serves.
const MaxDeferredPageLimit = 200

// DeferredPageQuery selects one page of deferred items (ARS-005 §7.1). At most
// one of AfterID, BeforeID, Page and Last is set.
type DeferredPageQuery struct {
	Include  DeferredStatus // "" (all) or DeferredStatusPending
	Reason   DeferredReason // "" = no filter
	Limit    int            // 1..MaxDeferredPageLimit
	AfterID  int64          // 0 = unset
	BeforeID int64          // 0 = unset
	Page     int            // 0 = unset; >= 1 = page number
	Last     bool           // jump to the last page
}

// DeferredPage is one page of deferred items with the counts around it.
type DeferredPage struct {
	Items                       []DeferredItem
	Total                       int                    // include + reason filter
	QueueTotal                  int                    // include only
	ByReason                    map[DeferredReason]int // include only
	Number                      int
	TotalPages                  int
	FirstPosition, LastPosition *int // 1-based positions in the filtered set; nil when Items is empty
	NextAfterID, PrevBeforeID   *int64
}

// ListDeferredPage returns one bounded page in ascending id order. Sequential
// paging uses id cursors; Page and Last jump by position. All reads share one
// read-only transaction so the counts and the rows agree.
func (s *Store) ListDeferredPage(ctx context.Context, q DeferredPageQuery) (DeferredPage, error) {
	var page DeferredPage
	if q.Limit < 1 || q.Limit > MaxDeferredPageLimit || q.AfterID < 0 || q.BeforeID < 0 || q.Page < 0 {
		return page, ErrInvalidDeferredPage
	}
	selectors := 0
	for _, set := range []bool{q.AfterID != 0, q.BeforeID != 0, q.Page != 0, q.Last} {
		if set {
			selectors++
		}
	}
	if selectors > 1 {
		return page, ErrInvalidDeferredPage
	}
	if q.Include != "" && q.Include != DeferredStatusPending {
		return page, fmt.Errorf("%w: include %q", ErrInvalidDeferredPage, q.Include)
	}
	if q.Reason != "" && !q.Reason.Valid() {
		return page, ErrInvalidDeferredReason
	}

	filter, args := "1=1", []any{}
	if q.Include == DeferredStatusPending {
		filter = "d.status = 'pending'"
	}
	filtered, filteredArgs := filter, args
	// ordered is the same predicate for queries that walk rows in id order. The
	// unary + keeps SQLite on the id (rowid) range so LIMIT stops early instead
	// of sorting every matching row.
	ordered := strings.ReplaceAll(filter, "d.status", "+d.status")
	if q.Reason != "" {
		filtered += " AND d.deferral_reason = ?"
		ordered += " AND +d.deferral_reason = ?"
		filteredArgs = []any{string(q.Reason)}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, fmt.Errorf("beginning deferred page read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	count := func(where string, a []any) (int, error) {
		var n int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deferred_item d WHERE `+where, a...).Scan(&n)
		return n, err
	}
	page.ByReason = map[DeferredReason]int{}
	rows, err := tx.QueryContext(ctx, `SELECT d.deferral_reason, COUNT(*) FROM deferred_item d WHERE `+filter+` GROUP BY d.deferral_reason`, args...)
	if err != nil {
		return page, fmt.Errorf("counting deferred reasons: %w", err)
	}
	for rows.Next() {
		var reason DeferredReason
		var n int
		if err := rows.Scan(&reason, &n); err != nil {
			_ = rows.Close()
			return page, fmt.Errorf("scanning deferred reasons: %w", err)
		}
		page.QueueTotal += n
		if q.Reason == "" || reason == q.Reason {
			page.Total += n
		}
		if reason.Valid() { // legacy rows with unknown text are never echoed
			page.ByReason[reason] = n
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return page, fmt.Errorf("iterating deferred reasons: %w", err)
	}
	_ = rows.Close()

	page.TotalPages = (page.Total + q.Limit - 1) / q.Limit
	if page.TotalPages < 1 {
		page.TotalPages = 1
	}
	jump := q.Page
	if q.Last {
		jump = page.TotalPages
	}

	const cols = `SELECT d.id, d.video_id, v.video_id, d.run_id, d.deferral_reason, d.retry_after,
		        d.retry_count, d.status, d.created_at, d.retry_run_id
		 FROM deferred_item d JOIN video v ON v.id = d.video_id WHERE `
	var itemRows *sql.Rows
	switch {
	case jump > page.TotalPages:
		page.Items = []DeferredItem{}
		page.Number = jump
		return page, tx.Commit()
	case jump > 0:
		itemRows, err = tx.QueryContext(ctx, cols+ordered+` ORDER BY d.id ASC LIMIT ? OFFSET ?`,
			append(append([]any{}, filteredArgs...), q.Limit, (jump-1)*q.Limit)...)
	case q.BeforeID != 0:
		itemRows, err = tx.QueryContext(ctx, cols+ordered+` AND d.id < ? ORDER BY d.id DESC LIMIT ?`,
			append(append([]any{}, filteredArgs...), q.BeforeID, q.Limit)...)
	default:
		itemRows, err = tx.QueryContext(ctx, cols+ordered+` AND d.id > ? ORDER BY d.id ASC LIMIT ?`,
			append(append([]any{}, filteredArgs...), q.AfterID, q.Limit)...)
	}
	if err != nil {
		return page, fmt.Errorf("querying deferred page: %w", err)
	}
	items, err := scanDeferredItems(itemRows)
	_ = itemRows.Close()
	if err != nil {
		return page, err
	}
	if q.BeforeID != 0 {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	page.Items = items
	page.Number = 1
	if jump > 0 {
		page.Number = jump
	}
	if len(items) > 0 {
		firstID, lastID := items[0].ID, items[len(items)-1].ID
		before, err := count(ordered+" AND d.id < ?", append(append([]any{}, filteredArgs...), firstID))
		if err != nil {
			return page, fmt.Errorf("locating deferred page: %w", err)
		}
		first, last := before+1, before+len(items)
		page.FirstPosition, page.LastPosition = &first, &last
		page.Number = before/q.Limit + 1
		if before > 0 {
			page.PrevBeforeID = &firstID
		}
		if last < page.Total {
			page.NextAfterID = &lastID
		}
	}
	if err := tx.Commit(); err != nil {
		return page, fmt.Errorf("closing deferred page read: %w", err)
	}
	return page, nil
}
