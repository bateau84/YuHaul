-- SQLite does not support dropping columns. Recreate the channel table without
-- the discovery columns to reverse this migration.
CREATE TABLE channel_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id TEXT NOT NULL UNIQUE,
    channel_name TEXT,
    first_discovered_at TIMESTAMP,
    last_discovered_at TIMESTAMP,
    total_discovered INTEGER NOT NULL DEFAULT 0,
    total_routed INTEGER NOT NULL DEFAULT 0,
    total_retained INTEGER NOT NULL DEFAULT 0
);

INSERT INTO channel_new
    (id, channel_id, channel_name, first_discovered_at, last_discovered_at,
     total_discovered, total_routed, total_retained)
SELECT
    id, channel_id, channel_name, first_discovered_at, last_discovered_at,
    total_discovered, total_routed, total_retained
FROM channel;

DROP TABLE channel;
ALTER TABLE channel_new RENAME TO channel;

CREATE INDEX IF NOT EXISTS idx_channel_channel_id ON channel(channel_id);
