package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

// searchIndexDDL must match the corresponding statements in migration 029.
const searchIndexDDL = `
CREATE TABLE title_search_doc (
    doc_id INTEGER PRIMARY KEY,
    video_id TEXT NOT NULL UNIQUE
);

CREATE VIRTUAL TABLE title_search USING fts5(
    k,
    tokenize = 'trigram case_sensitive 1',
    detail = full
);

CREATE TRIGGER transient_metadata_search_purge_insert
BEFORE INSERT ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = NEW.video_id);
    DELETE FROM title_search_doc WHERE video_id = NEW.video_id;
END;

CREATE TRIGGER transient_metadata_search_purge_update
AFTER UPDATE OF video_id, title, is_active, expired_at, fetched_at, expires_at ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id IN (OLD.video_id, NEW.video_id));
    DELETE FROM title_search_doc WHERE video_id IN (OLD.video_id, NEW.video_id);
END;

CREATE TRIGGER transient_metadata_search_purge_delete
AFTER DELETE ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = OLD.video_id);
    DELETE FROM title_search_doc WHERE video_id = OLD.video_id;
END;`

// RecreateSearchIndex atomically discards and recreates the derived index.
// Its caller must reconcile the index before treating it as ready.
func RecreateSearchIndex(ctx context.Context, db *sql.DB) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire search index connection: %w", err)
	}
	discarded := false
	defer func() {
		closeErr := conn.Close()
		if discarded && errors.Is(closeErr, sql.ErrConnDone) {
			closeErr = nil
		}
		err = errors.Join(err, closeErr)
	}()

	closed := false
	defer func() {
		if closed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, rollbackErr := conn.ExecContext(cleanupCtx, "ROLLBACK"); rollbackErr != nil {
			// A failed BEGIN or COMMIT can have taken effect even when its
			// caller saw an error. Never return an uncertain transaction.
			discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
			if errors.Is(discardErr, driver.ErrBadConn) {
				discarded = true
				discardErr = nil
			}
			err = errors.Join(err, fmt.Errorf("rollback search index transaction: %w", rollbackErr), discardErr)
		}
	}()

	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin search index transaction: %w", err)
	}
	const drop = `
DROP TRIGGER transient_metadata_search_purge_insert;
DROP TRIGGER transient_metadata_search_purge_update;
DROP TRIGGER transient_metadata_search_purge_delete;
DROP TABLE title_search;
DROP TABLE title_search_doc;`
	if _, err = conn.ExecContext(ctx, drop+searchIndexDDL+`
UPDATE title_search_state SET key_version = NULL, built_at = NULL WHERE id = 1;`); err != nil {
		return fmt.Errorf("recreate search index: %w", err)
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit search index recreation: %w", err)
	}
	closed = true
	return nil
}
