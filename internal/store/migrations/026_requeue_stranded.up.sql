-- Re-queue stranded videos (run-initiation ADR-006 / ARS-005 §1.1).
-- Adds the requeued_by_user deferral reason, the (video_id, status) indexes the
-- stranded scan and the active-item predicates need, and the per-run
-- unavailable counter. The video and run_video_outcome final_outcome CHECKs are
-- widened by the Go schema step widenFinalOutcomeChecks (SQLite cannot alter a
-- CHECK in place).
CREATE TABLE deferred_item_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    deferral_reason TEXT NOT NULL CHECK(deferral_reason IN ('quota_exhausted', 'api_rate_limited', 'authorization_pending', 'stopped_by_user', 'discarded_by_user', 'requeued_by_user')),
    retry_after TIMESTAMP,
    retry_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK(status IN ('pending', 'retrying', 'completed', 'discarded')),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    retry_run_id INTEGER REFERENCES run(id) ON DELETE SET NULL
);
INSERT INTO deferred_item_new (id, video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at, retry_run_id)
SELECT id, video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at, retry_run_id FROM deferred_item;
-- Keep the AUTOINCREMENT high-water mark, which may exceed max(id).
DELETE FROM sqlite_sequence WHERE name = 'deferred_item_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'deferred_item_new', seq FROM sqlite_sequence WHERE name = 'deferred_item';
DROP TABLE deferred_item;
ALTER TABLE deferred_item_new RENAME TO deferred_item;
CREATE INDEX IF NOT EXISTS idx_deferred_item_status ON deferred_item(status);
CREATE INDEX deferred_item_retry_owner ON deferred_item(retry_run_id, status, video_id);

CREATE INDEX deferred_item_video_status ON deferred_item(video_id, status);
CREATE INDEX error_entry_video_status ON error_entry(video_id, status);
ALTER TABLE run ADD COLUMN videos_unavailable INTEGER NOT NULL DEFAULT 0;
