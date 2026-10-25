-- Unknown historical producers are never promoted to corrected authority.
ALTER TABLE run ADD COLUMN action_authority_version INTEGER NOT NULL DEFAULT 0
    CHECK(action_authority_version IN (0, 1));
ALTER TABLE deferred_item ADD COLUMN retry_run_id INTEGER REFERENCES run(id);
ALTER TABLE error_entry ADD COLUMN retry_run_id INTEGER REFERENCES run(id);

CREATE INDEX deferred_item_retry_owner ON deferred_item(retry_run_id, status, video_id);
CREATE INDEX error_entry_retry_owner ON error_entry(retry_run_id, status, video_id);
