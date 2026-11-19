package metadata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/titlekey"
)

// This file maintains the derived title search index (video-search ARS-001
// §10). The index only proposes candidates: search decides with Held and
// titlekey.Contains, never with the index.

// IndexWriteBatch is the most rows one reconcile transaction may purge or add
// (ARS-001 §10.10). It is a variable only so tests can force small batches.
var IndexWriteBatch = 200

// ErrSearchIndexVersion reports that the stored key version differs from
// titlekey.Version(). The index must be recreated before it is reconciled
// (ARS-001 §10.9).
var ErrSearchIndexVersion = errors.New("metadata: search index built with another title key version")

// ReconcileStats counts what one ReconcileSearchIndex call changed.
type ReconcileStats struct {
	// OrphansRemoved counts FTS rows without a document and documents without
	// an FTS row.
	OrphansRemoved int
	// Purged counts entries removed because their source row is missing or no
	// longer Indexable.
	Purged int
	// Added counts entries added for Indexable rows that had none.
	Added int
	// Batches counts committed batches.
	Batches int
}

// Indexable reports whether row may have an index entry when written at now:
// it is held now, or it becomes held at its own fetched_at (a fetch time later
// than now) and stays held until expires_at.
func Indexable(row LocalRow, now time.Time) bool {
	fetched, ok := ParseLocalTime(row.FetchedAt)
	if !ok {
		return false
	}
	at := now
	if fetched.After(at) {
		at = fetched
	}
	return Held(row, at)
}

// dbx is the statement surface shared by *sql.Tx and *sql.Conn.
type dbx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// IndexTitleTx makes the index entry of videoID match the row as stored in tx.
// It must run in the same transaction, after the statement that wrote the row,
// and an error must abort that transaction so the title write fails with it.
// It reads the stored row instead of trusting the writer: an UPDATE that
// matched nothing adds nothing, and a kept expired_at is not indexed.
// f must not be shared between goroutines.
func IndexTitleTx(ctx context.Context, tx *sql.Tx, f *titlekey.Folder, videoID string, now time.Time) error {
	return indexTitle(ctx, tx, f, videoID, now)
}

func indexTitle(ctx context.Context, x dbx, f *titlekey.Folder, videoID string, now time.Time) error {
	if err := purgeEntry(ctx, x, videoID); err != nil {
		return err
	}
	row, found, err := readIndexRow(ctx, x, videoID)
	if err != nil {
		return err
	}
	if !found || !Indexable(row, now) {
		return nil
	}
	return addEntry(ctx, x, f, row)
}

// purgeEntry runs the migration 029 trigger statements for one video ID.
func purgeEntry(ctx context.Context, x dbx, videoID string) error {
	if _, err := x.ExecContext(ctx,
		`DELETE FROM title_search WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = ?)`, videoID); err != nil {
		return fmt.Errorf("metadata: purge search entry: %w", err)
	}
	if _, err := x.ExecContext(ctx, `DELETE FROM title_search_doc WHERE video_id = ?`, videoID); err != nil {
		return fmt.Errorf("metadata: purge search document: %w", err)
	}
	return nil
}

func readIndexRow(ctx context.Context, x dbx, videoID string) (LocalRow, bool, error) {
	var row LocalRow
	var title sql.NullString
	err := x.QueryRowContext(ctx, `
		SELECT video_id, title, is_active, expired_at, fetched_at, expires_at
		FROM transient_metadata WHERE video_id = ?`, videoID).
		Scan(&row.VideoID, &title, &row.IsActive, &row.ExpiredAt, &row.FetchedAt, &row.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LocalRow{}, false, nil
	}
	if err != nil {
		return LocalRow{}, false, fmt.Errorf("metadata: read row for search index: %w", err)
	}
	if title.Valid {
		row.Title = &title.String
	}
	return row, true, nil
}

// addEntry inserts the document and FTS rows of an Indexable row. Callers must
// have purged any earlier entry for the row.
func addEntry(ctx context.Context, x dbx, f *titlekey.Folder, row LocalRow) error {
	if row.Title == nil {
		return nil
	}
	res, err := x.ExecContext(ctx, `INSERT INTO title_search_doc(video_id) VALUES (?)`, row.VideoID)
	if err != nil {
		return fmt.Errorf("metadata: add search document: %w", err)
	}
	docID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("metadata: search document id: %w", err)
	}
	if _, err := x.ExecContext(ctx, `INSERT INTO title_search(rowid, k) VALUES (?, ?)`, docID, f.Key(*row.Title)); err != nil {
		return fmt.Errorf("metadata: add search entry: %w", err)
	}
	return nil
}

// SearchIndexReady reports, read in tx, that title_search_state.built_at is
// set and key_version equals titlekey.Version().
func SearchIndexReady(ctx context.Context, tx *sql.Tx) (bool, error) {
	var version, builtAt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT key_version, built_at FROM title_search_state WHERE id = 1`).Scan(&version, &builtAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("metadata: read search index state: %w", err)
	}
	return builtAt.Valid && version.Valid && version.String == titlekey.Version(), nil
}

// reconcileScanPage bounds how many index rows one batch examines. It is
// larger than IndexWriteBatch because examining a row that needs no change is
// cheap, while each change costs index writes.
const reconcileScanPage = 2000

// ReconcileSearchIndex repairs the index in batches (ARS-001 §10.4). Each batch
// is one write-lock-first transaction that reads its work under the lock,
// applies at most IndexWriteBatch changes, examines at most reconcileScanPage
// rows and commits. The scan is one pass per call, tracked by keyset cursors,
// so the cost is linear in the index and source sizes:
//
//  1. FTS rows with no document are removed (cursor: FTS rowid);
//  2. documents with no FTS row, or whose source row is missing or not
//     Indexable, are removed (cursor: doc_id);
//  3. Indexable rows with no document are added, walking the source table by
//     primary-key range (cursor: video_id); every walked row counts against
//     the page, so expired rows cost scan budget, not unbounded time.
//
// Each phase finishes before the next starts, so step 3 sees every document
// removed by step 2. The batch that finishes step 3 records key_version and
// built_at. It returns counts only.
//
// It returns ErrSearchIndexVersion when a stored key_version differs from
// titlekey.Version(). Committed batches stay committed when a later one fails.
func ReconcileSearchIndex(ctx context.Context, db *sql.DB, now time.Time) (ReconcileStats, error) {
	var stats ReconcileStats
	st := reconcileCursor{}
	f := titlekey.NewFolder()
	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		var batch ReconcileStats
		var final bool
		saved := st
		err := inWriteTx(ctx, db, func(c *sql.Conn) error {
			var err error
			batch, final, err = reconcileBatch(ctx, c, f, now.UTC(), &st, IndexWriteBatch)
			if err != nil {
				return err
			}
			if hooks.beforeCommit != nil {
				return hooks.beforeCommit(batch)
			}
			return nil
		})
		if err != nil {
			st = saved
			return stats, err
		}
		// A batch's counters apply only once its COMMIT succeeded.
		stats.OrphansRemoved += batch.OrphansRemoved
		stats.Purged += batch.Purged
		stats.Added += batch.Added
		stats.Batches++
		if final {
			return stats, nil
		}
	}
}

type reconcilePhase int

const (
	phaseFTS reconcilePhase = iota
	phaseDocs
	phaseRows
	phaseDone
)

// reconcileCursor carries scan progress across the batches of one reconcile.
// Rows behind a cursor stay correct through the triggers and IndexTitleTx.
type reconcileCursor struct {
	phase    reconcilePhase
	after    int64  // last FTS rowid or doc_id examined
	rowAfter string // last candidate video_id examined
}

func reconcileBatch(ctx context.Context, c *sql.Conn, f *titlekey.Folder, now time.Time, st *reconcileCursor, limit int) (ReconcileStats, bool, error) {
	var out ReconcileStats
	if limit < 1 {
		limit = 1
	}
	var stored sql.NullString
	err := c.QueryRowContext(ctx, `SELECT key_version FROM title_search_state WHERE id = 1`).Scan(&stored)
	if err != nil {
		return out, false, fmt.Errorf("metadata: read search index state: %w", err)
	}
	if stored.Valid && stored.String != titlekey.Version() {
		return out, false, ErrSearchIndexVersion
	}

	changes := func() int { return out.OrphansRemoved + out.Purged + out.Added }
	scanLeft := reconcileScanPage
	for st.phase != phaseDone && changes() < limit && scanLeft > 0 {
		var examined int
		var full bool
		switch st.phase {
		case phaseFTS:
			ids, err := collectInts(ctx, c, `SELECT rowid FROM title_search WHERE rowid > ? ORDER BY rowid LIMIT ?`, st.after, scanLeft)
			if err != nil {
				return out, false, err
			}
			full = len(ids) == cap(ids)
			for _, id := range ids {
				has, err := exists(ctx, c, `SELECT 1 FROM title_search_doc WHERE doc_id = ?`, id)
				if err != nil {
					return out, false, err
				}
				if !has {
					if _, err := c.ExecContext(ctx, `DELETE FROM title_search WHERE rowid = ?`, id); err != nil {
						return out, false, fmt.Errorf("metadata: remove orphan search entry: %w", err)
					}
					out.OrphansRemoved++
				}
				st.after = id
				examined++
				if changes() >= limit {
					full = true
					break
				}
			}
		case phaseDocs:
			docs, err := collectDocs(ctx, c, st.after, scanLeft)
			if err != nil {
				return out, false, err
			}
			full = len(docs) == cap(docs)
			for _, d := range docs {
				has, err := exists(ctx, c, `SELECT 1 FROM title_search WHERE rowid = ?`, d.docID)
				if err != nil {
					return out, false, err
				}
				if !has {
					if _, err := c.ExecContext(ctx, `DELETE FROM title_search_doc WHERE doc_id = ?`, d.docID); err != nil {
						return out, false, fmt.Errorf("metadata: remove orphan search document: %w", err)
					}
					out.OrphansRemoved++
				} else {
					row, found, err := readIndexRow(ctx, c, d.videoID)
					if err != nil {
						return out, false, err
					}
					if !found || !Indexable(row, now) {
						if err := purgeEntry(ctx, c, d.videoID); err != nil {
							return out, false, err
						}
						out.Purged++
					}
				}
				st.after = d.docID
				examined++
				if changes() >= limit {
					full = true
					break
				}
			}
		case phaseRows:
			// Walk the source table by primary-key range, so every row examined
			// counts against scanLeft whether or not it is a candidate. Expired
			// rows are never deleted in production, so a filter in SQL would let
			// one batch walk the whole table. Indexable and the document probe
			// decide in Go.
			rows, err := collectRows(ctx, c, st.rowAfter, scanLeft)
			if err != nil {
				return out, false, err
			}
			full = len(rows) == cap(rows)
			for _, row := range rows {
				if Indexable(row, now) {
					has, err := exists(ctx, c, `SELECT 1 FROM title_search_doc WHERE video_id = ?`, row.VideoID)
					if err != nil {
						return out, false, err
					}
					if !has {
						if err := addEntry(ctx, c, f, row); err != nil {
							return out, false, err
						}
						out.Added++
					}
				}
				st.rowAfter = row.VideoID
				examined++
				if changes() >= limit {
					full = true
					break
				}
			}
		}
		scanLeft -= examined
		if !full {
			// The phase ran out of rows.
			st.phase++
			st.after = 0
		} else {
			break // this batch's page or change budget is used up
		}
	}

	final := st.phase == phaseDone
	if final {
		if _, err := c.ExecContext(ctx, `UPDATE title_search_state SET key_version = ?, built_at = ? WHERE id = 1`,
			titlekey.Version(), now.UTC().Format(time.RFC3339Nano)); err != nil {
			return out, false, fmt.Errorf("metadata: record search index state: %w", err)
		}
	}
	return out, final, nil
}

func exists(ctx context.Context, x dbx, query string, arg any) (bool, error) {
	var one int
	err := x.QueryRowContext(ctx, query, arg).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("metadata: probe search index: %w", err)
	}
	return true, nil
}

type docRef struct {
	docID   int64
	videoID string
}

// collectInts reads every row before returning so the connection is free for
// writes. The returned slice's capacity equals the requested limit.
func collectInts(ctx context.Context, x dbx, query string, after int64, limit int) ([]int64, error) {
	rows, err := x.QueryContext(ctx, query, after, limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: scan search index: %w", err)
	}
	defer rows.Close()
	out := make([]int64, 0, limit)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("metadata: scan search index: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func collectDocs(ctx context.Context, x dbx, after int64, limit int) ([]docRef, error) {
	rows, err := x.QueryContext(ctx, `SELECT doc_id, video_id FROM title_search_doc WHERE doc_id > ? ORDER BY doc_id LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: scan search documents: %w", err)
	}
	defer rows.Close()
	out := make([]docRef, 0, limit)
	for rows.Next() {
		var d docRef
		if err := rows.Scan(&d.docID, &d.videoID); err != nil {
			return nil, fmt.Errorf("metadata: scan search documents: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// collectRows reads the next page of source rows after video_id after, in
// primary-key order. The slice's capacity equals the requested limit.
func collectRows(ctx context.Context, x dbx, after string, limit int) ([]LocalRow, error) {
	rows, err := x.QueryContext(ctx, `
		SELECT video_id, title, is_active, expired_at, fetched_at, expires_at
		FROM transient_metadata WHERE video_id > ? ORDER BY video_id LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: scan search candidates: %w", err)
	}
	defer rows.Close()
	out := make([]LocalRow, 0, limit)
	for rows.Next() {
		var row LocalRow
		var title sql.NullString
		if err := rows.Scan(&row.VideoID, &title, &row.IsActive, &row.ExpiredAt, &row.FetchedAt, &row.ExpiresAt); err != nil {
			return nil, fmt.Errorf("metadata: scan search candidates: %w", err)
		}
		if title.Valid {
			row.Title = &title.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// testHooks are nil in production. Tests set them to observe or fail the
// transaction boundaries without sleeping.
type testHooks struct {
	beforeCommit func(ReconcileStats) error
	rollback     func(ctx context.Context, c *sql.Conn) error
	commit       func(ctx context.Context, c *sql.Conn) error
}

var hooks testHooks

// inWriteTx runs fn in a transaction that takes the write lock first
// (ARS-001 §10.5): a dedicated connection executes BEGIN IMMEDIATE, fn and
// COMMIT. fn must not leave the transaction open. It returns nil only after a
// successful COMMIT.
//
// Cleanup is armed before BEGIN, because cancellation can fail a statement that
// SQLite did execute. On any exit without a confirmed COMMIT it rolls back on
// the same connection under a fresh bounded context. If closure cannot be
// confirmed the physical connection is discarded rather than pooled.
func inWriteTx(ctx context.Context, db *sql.DB, fn func(c *sql.Conn) error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("metadata: acquire search index connection: %w", err)
	}
	discarded := false
	defer func() {
		closeErr := conn.Close()
		if discarded && errors.Is(closeErr, sql.ErrConnDone) {
			closeErr = nil
		}
		err = errors.Join(err, closeErr)
	}()

	committed := false
	defer func() {
		if committed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var rollbackErr error
		if hooks.rollback != nil {
			rollbackErr = hooks.rollback(cleanupCtx, conn)
		} else {
			_, rollbackErr = conn.ExecContext(cleanupCtx, "ROLLBACK")
		}
		// SQLite itself confirms autocommit mode when no transaction is open.
		if rollbackErr != nil && !strings.Contains(rollbackErr.Error(), "no transaction is active") {
			discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
			if errors.Is(discardErr, driver.ErrBadConn) {
				discarded = true
				discardErr = nil
			}
			err = errors.Join(err, fmt.Errorf("metadata: roll back search index transaction: %w", rollbackErr), discardErr)
		}
	}()

	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("metadata: begin search index transaction: %w", err)
	}
	if err = fn(conn); err != nil {
		return err
	}
	var commitErr error
	if hooks.commit != nil {
		commitErr = hooks.commit(ctx, conn)
	} else {
		_, commitErr = conn.ExecContext(ctx, "COMMIT")
	}
	if commitErr != nil {
		// The commit may or may not have happened; never report it as done.
		return fmt.Errorf("metadata: commit search index transaction: %w", commitErr)
	}
	committed = true
	return nil
}
