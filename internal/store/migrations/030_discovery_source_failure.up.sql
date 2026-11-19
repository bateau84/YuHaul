-- Durable, immutable record of one failed uploads-source scan: one row per
-- failed scan of one channel in one run (discovery ARS-002 section 1). Rows
-- leave only when their run is deleted. The CHECKs on category, reason and
-- http_status are permissive so later incident kinds need no table rebuild;
-- the closed vocabulary is applied in Go at write and at read.
CREATE TABLE discovery_source_failure (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id       INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    channel_id   TEXT    NOT NULL CHECK (length(channel_id) = 24 AND substr(channel_id, 1, 2) = 'UC'),
    occurred_at  TEXT    NOT NULL,
    category     TEXT    NOT NULL CHECK (category IN ('permanent', 'transient')),
    operation    TEXT    NOT NULL CHECK (operation = 'playlist_items_list'),
    http_status  INTEGER CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
    reason       TEXT    CHECK (reason IS NULL OR length(reason) BETWEEN 1 AND 64)
);

CREATE UNIQUE INDEX discovery_source_failure_permanent_once
    ON discovery_source_failure(run_id, channel_id) WHERE category = 'permanent';
CREATE INDEX discovery_source_failure_category_id ON discovery_source_failure(category, id);
CREATE INDEX discovery_source_failure_run_id      ON discovery_source_failure(run_id, id);

CREATE TRIGGER discovery_source_failure_immutable
    BEFORE UPDATE ON discovery_source_failure
    BEGIN SELECT RAISE(ABORT, 'discovery_source_failure is immutable'); END;
