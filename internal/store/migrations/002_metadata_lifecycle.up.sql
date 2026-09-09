-- Add metadata lifecycle tracking to Store A.
--
-- Expired records must not retain raw titles, so title becomes nullable and an
-- expired_at timestamp is added for observability. Indexes support the
-- lifecycle sweep queries.

ALTER TABLE transient_metadata RENAME TO transient_metadata_old;

CREATE TABLE transient_metadata (
    video_id TEXT PRIMARY KEY,
    title TEXT,
    channel_name TEXT,
    description TEXT,
    published_at TIMESTAMP,
    duration_seconds INTEGER,
    thumbnail_url TEXT,
    fetched_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    refreshed_at TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    expired_at TIMESTAMP,
    is_active BOOLEAN NOT NULL DEFAULT 1
);

INSERT INTO transient_metadata (
    video_id, title, channel_name, description, published_at,
    duration_seconds, thumbnail_url, fetched_at, refreshed_at,
    expires_at, expired_at, is_active
)
SELECT
    video_id, title, channel_name, description, published_at,
    duration_seconds, thumbnail_url, fetched_at, refreshed_at,
    expires_at, NULL, is_active
FROM transient_metadata_old;

DROP TABLE transient_metadata_old;

CREATE INDEX idx_transient_metadata_expires_at ON transient_metadata(expires_at);
CREATE INDEX idx_transient_metadata_is_active ON transient_metadata(is_active);
CREATE INDEX idx_transient_metadata_active_expiry ON transient_metadata(is_active, expires_at);
