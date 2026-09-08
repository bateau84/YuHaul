-- Defensive migration for statistics and advisory support.
--
-- The initial schema already created channel, retention_playlist, retention_signal,
-- and advisory tables. This migration ensures those tables exist in environments
-- bootstrapped through other paths and adds supporting indexes for analytics
-- queries introduced in T5.4.

CREATE TABLE IF NOT EXISTS channel (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id TEXT NOT NULL UNIQUE,
    channel_name TEXT,
    first_discovered_at TIMESTAMP,
    last_discovered_at TIMESTAMP,
    total_discovered INTEGER NOT NULL DEFAULT 0,
    total_routed INTEGER NOT NULL DEFAULT 0,
    total_retained INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS retention_playlist (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    playlist_id TEXT NOT NULL UNIQUE,
    playlist_name TEXT NOT NULL,
    designated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    un_designated_at TIMESTAMP,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    last_checked_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS retention_signal (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id TEXT NOT NULL,
    playlist_id TEXT NOT NULL REFERENCES retention_playlist(playlist_id) ON DELETE CASCADE,
    membership_source TEXT NOT NULL CHECK(membership_source IN ('yuhaul_routed', 'user_driven', 'unknown')),
    detected_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    is_retention_signal BOOLEAN NOT NULL,
    UNIQUE(video_id, playlist_id)
);

CREATE TABLE IF NOT EXISTS advisory (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    advisory_type TEXT NOT NULL CHECK(advisory_type IN ('low_usefulness', 'declining_yield', 'high_duplicate_rate')),
    generated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    status TEXT NOT NULL CHECK(status IN ('active', 'dismissed', 'ignored', 'acted_upon')),
    ignored_until TIMESTAMP,
    action_taken TEXT,
    dismissed_at TIMESTAMP,
    acted_upon_at TIMESTAMP,
    evidence_snapshot TEXT
);

CREATE INDEX IF NOT EXISTS idx_channel_channel_id ON channel(channel_id);
CREATE INDEX IF NOT EXISTS idx_advisory_channel_id ON advisory(channel_id);
CREATE INDEX IF NOT EXISTS idx_advisory_status ON advisory(status);
CREATE INDEX IF NOT EXISTS idx_retention_signal_playlist_id ON retention_signal(playlist_id);
CREATE INDEX IF NOT EXISTS idx_retention_signal_video_id ON retention_signal(video_id);
CREATE INDEX IF NOT EXISTS idx_duplicate_match_matched_fingerprint_id ON duplicate_match(matched_fingerprint_id);
CREATE INDEX IF NOT EXISTS idx_route_decision_rejection_reason ON route_decision(rejection_reason);
