-- Revert metadata lifecycle changes.
--
-- Restores title as NOT NULL. Any expired rows (title IS NULL) are dropped
-- because the previous schema cannot represent them. This matches the
-- pre-migration semantics where expired records were deleted outright.

PRAGMA foreign_keys = OFF;

ALTER TABLE transient_metadata RENAME TO transient_metadata_old;

CREATE TABLE transient_metadata (
    video_id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    channel_name TEXT,
    description TEXT,
    published_at TIMESTAMP,
    duration_seconds INTEGER,
    thumbnail_url TEXT,
    fetched_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    refreshed_at TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT 1
);

INSERT INTO transient_metadata (
    video_id, title, channel_name, description, published_at,
    duration_seconds, thumbnail_url, fetched_at, refreshed_at,
    expires_at, is_active
)
SELECT
    video_id, title, channel_name, description, published_at,
    duration_seconds, thumbnail_url, fetched_at, refreshed_at,
    expires_at, is_active
FROM transient_metadata_old
WHERE title IS NOT NULL;

DROP TABLE transient_metadata_old;

PRAGMA foreign_keys = ON;
