-- Stop run (run-initiation ADR-004 / ARS-003 §1.1).
-- stop_requested_at is the durable stop marker: set once, only on a running
-- row. A marked row can only become 'stopped'; 'stopped' requires a marker and
-- is final. The run.status CHECK is widened by the Go schema step
-- widenRunStatusCheck (SQLite cannot alter a CHECK in place).
ALTER TABLE run ADD COLUMN stop_requested_at TEXT;

CREATE TRIGGER run_stop_requested_set
BEFORE UPDATE OF stop_requested_at ON run
WHEN OLD.stop_requested_at IS NOT NEW.stop_requested_at
  AND (OLD.stop_requested_at IS NOT NULL OR NEW.stop_requested_at IS NULL OR OLD.status <> 'running')
BEGIN SELECT RAISE(ABORT, 'invalid run stop request'); END;

CREATE TRIGGER run_stop_requested_insert
BEFORE INSERT ON run
WHEN NEW.stop_requested_at IS NOT NULL OR NEW.status = 'stopped'
BEGIN SELECT RAISE(ABORT, 'invalid run stop request'); END;

CREATE TRIGGER run_stop_requested_status
BEFORE UPDATE OF status ON run
WHEN NEW.status IS NOT OLD.status AND (
     (OLD.stop_requested_at IS NOT NULL AND NEW.status <> 'stopped')
  OR (NEW.status = 'stopped' AND (OLD.stop_requested_at IS NULL OR OLD.status NOT IN ('running','interrupted')))
  OR OLD.status = 'stopped')
BEGIN SELECT RAISE(ABORT, 'invalid stopped transition'); END;

-- Permit the stop and discard handoff reasons in the deferred queue.
CREATE TABLE deferred_item_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    deferral_reason TEXT NOT NULL CHECK(deferral_reason IN ('quota_exhausted', 'api_rate_limited', 'authorization_pending', 'stopped_by_user', 'discarded_by_user')),
    retry_after TIMESTAMP,
    retry_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK(status IN ('pending', 'retrying', 'completed', 'discarded')),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    retry_run_id INTEGER REFERENCES run(id) ON DELETE SET NULL
);
INSERT INTO deferred_item_new (id, video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at, retry_run_id)
SELECT id, video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at, retry_run_id FROM deferred_item;
DROP TABLE deferred_item;
ALTER TABLE deferred_item_new RENAME TO deferred_item;
CREATE INDEX IF NOT EXISTS idx_deferred_item_status ON deferred_item(status);
CREATE INDEX deferred_item_retry_owner ON deferred_item(retry_run_id, status, video_id);
