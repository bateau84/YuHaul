-- YuHaul initial schema
-- Two-store SQLite model: Store A (transient API metadata) and Store B (permanent YuHaul data).
-- Includes FTS5 virtual table for fingerprint search and all entity relationships.

-- Store B: permanent configuration snapshots
CREATE TABLE IF NOT EXISTS config_version (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    snapshot TEXT NOT NULL,
    change_description TEXT
);

-- Store B: processing runs
CREATE TABLE IF NOT EXISTS run (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    completed_at TIMESTAMP,
    run_type TEXT NOT NULL CHECK(run_type IN ('scheduled', 'manual', 'dry_run', 'replay', 'backtest')),
    status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'interrupted')),
    config_version_id INTEGER REFERENCES config_version(id) ON DELETE SET NULL,
    videos_discovered INTEGER NOT NULL DEFAULT 0,
    videos_accepted INTEGER NOT NULL DEFAULT 0,
    videos_rejected INTEGER NOT NULL DEFAULT 0,
    videos_deferred INTEGER NOT NULL DEFAULT 0,
    videos_failed INTEGER NOT NULL DEFAULT 0,
    videos_not_yet_evaluated INTEGER NOT NULL DEFAULT 0,
    quota_cost INTEGER NOT NULL DEFAULT 0,
    duration_seconds REAL,
    interrupt_reason TEXT
);

-- Store B: channels discovered through processing
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

-- Store A: transient API metadata with 30-day TTL
CREATE TABLE IF NOT EXISTS transient_metadata (
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

-- Store B: durable title fingerprints
CREATE TABLE IF NOT EXISTS title_fingerprint (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id TEXT NOT NULL UNIQUE,
    fingerprint TEXT NOT NULL,
    fingerprint_tokens TEXT,
    channel_id TEXT,
    source TEXT NOT NULL CHECK(source IN ('api_discovery', 'user_import')),
    first_seen_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    is_duplicate_reference BOOLEAN NOT NULL DEFAULT 0,
    import_batch_id TEXT
);

-- Store B: discovered videos and their processing state
CREATE TABLE IF NOT EXISTS video (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id TEXT NOT NULL UNIQUE,
    channel_id TEXT NOT NULL,
    discovered_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    discovered_in_run_id INTEGER REFERENCES run(id) ON DELETE SET NULL,
    processing_state TEXT NOT NULL CHECK(processing_state IN (
        'discovered', 'evaluating', 'evaluated', 'acted', 'completed', 'deferred', 'failed', 'not_yet_evaluated'
    )),
    final_outcome TEXT CHECK(final_outcome IN ('accepted', 'rejected', 'deferred', 'failed', 'not_yet_evaluated')),
    deferred_reason TEXT,
    error_message TEXT,
    completed_at TIMESTAMP
);

-- Store B: routing rules
CREATE TABLE IF NOT EXISTS route (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    evaluation_order INTEGER NOT NULL UNIQUE,
    is_final BOOLEAN NOT NULL DEFAULT 0,
    is_enabled BOOLEAN NOT NULL DEFAULT 1,
    condition_composition TEXT NOT NULL CHECK(condition_composition IN ('AND', 'OR')),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Store B: route conditions
CREATE TABLE IF NOT EXISTS route_condition (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    route_id INTEGER NOT NULL REFERENCES route(id) ON DELETE CASCADE,
    condition_index INTEGER NOT NULL,
    field TEXT NOT NULL,
    operator TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE(route_id, condition_index)
);

-- Store B: route actions
CREATE TABLE IF NOT EXISTS route_action (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    route_id INTEGER NOT NULL REFERENCES route(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL CHECK(action_type IN ('playlist_insert')),
    target_playlist_id TEXT NOT NULL,
    action_index INTEGER NOT NULL,
    UNIQUE(route_id, action_index)
);

-- Store B: route-scoped exclusions
CREATE TABLE IF NOT EXISTS route_exclusion (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    route_id INTEGER NOT NULL REFERENCES route(id) ON DELETE CASCADE,
    exclusion_type TEXT NOT NULL CHECK(exclusion_type IN ('channel', 'video', 'title', 'word', 'duration_max')),
    exclusion_value TEXT NOT NULL
);

-- Store B: per-video per-route decisions
CREATE TABLE IF NOT EXISTS route_decision (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    route_id INTEGER NOT NULL REFERENCES route(id) ON DELETE CASCADE,
    evaluation_order INTEGER NOT NULL,
    decision TEXT NOT NULL CHECK(decision IN ('matched', 'not_matched', 'deferred', 'skipped')),
    skip_reason TEXT CHECK(skip_reason IN ('final_route_matched', 'exclusion', 'disabled')),
    rejection_reason TEXT,
    UNIQUE(video_id, run_id, route_id)
);

-- Store B: per-condition audit trail for each route decision
CREATE TABLE IF NOT EXISTS condition_audit (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    route_decision_id INTEGER NOT NULL REFERENCES route_decision(id) ON DELETE CASCADE,
    condition_index INTEGER NOT NULL,
    field TEXT NOT NULL,
    operator TEXT NOT NULL,
    expected_value TEXT NOT NULL,
    actual_value TEXT,
    matched BOOLEAN NOT NULL,
    UNIQUE(route_decision_id, condition_index)
);

-- Store B: executed playlist actions with idempotency guarantee
CREATE TABLE IF NOT EXISTS executed_action (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    route_decision_id INTEGER NOT NULL REFERENCES route_decision(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL CHECK(action_type IN ('playlist_insert')),
    target_playlist_id TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('succeeded', 'failed', 'not_attempted', 'deferred')),
    error_message TEXT,
    executed_at TIMESTAMP,
    UNIQUE(run_id, video_id, target_playlist_id, action_type)
);

-- Store B: duplicate detection results
CREATE TABLE IF NOT EXISTS duplicate_match (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    matched_fingerprint_id INTEGER REFERENCES title_fingerprint(id) ON DELETE SET NULL,
    similarity_score REAL NOT NULL CHECK(similarity_score >= 0.0 AND similarity_score <= 1.0),
    match_type TEXT NOT NULL CHECK(match_type IN ('exact_video_id', 'fuzzy_title')),
    source_route_id INTEGER REFERENCES route(id) ON DELETE SET NULL,
    threshold_used REAL NOT NULL CHECK(threshold_used >= 0.0 AND threshold_used <= 1.0)
);

-- Store B: quota accounting ledger
CREATE TABLE IF NOT EXISTS quota_ledger (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    date TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    units_consumed INTEGER NOT NULL,
    call_count INTEGER NOT NULL,
    run_id INTEGER REFERENCES run(id) ON DELETE SET NULL,
    recorded_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Store B: error queue entries
CREATE TABLE IF NOT EXISTS error_entry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    error_category TEXT NOT NULL CHECK(error_category IN ('api_error', 'authorization', 'runtime')),
    error_message TEXT NOT NULL,
    error_detail TEXT,
    status TEXT NOT NULL CHECK(status IN ('active', 'retrying', 'dismissed', 'resolved')),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    resolved_at TIMESTAMP,
    retry_count INTEGER NOT NULL DEFAULT 0
);

-- Store B: deferred work queue
CREATE TABLE IF NOT EXISTS deferred_item (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    deferral_reason TEXT NOT NULL CHECK(deferral_reason IN ('quota_exhausted', 'api_rate_limited')),
    retry_after TIMESTAMP,
    retry_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK(status IN ('pending', 'retrying', 'completed', 'discarded')),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Store B: user-designated retention playlists
CREATE TABLE IF NOT EXISTS retention_playlist (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    playlist_id TEXT NOT NULL UNIQUE,
    playlist_name TEXT NOT NULL,
    designated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    un_designated_at TIMESTAMP,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    last_checked_at TIMESTAMP
);

-- Store B: retention signal observations
CREATE TABLE IF NOT EXISTS retention_signal (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id TEXT NOT NULL,
    playlist_id TEXT NOT NULL REFERENCES retention_playlist(playlist_id) ON DELETE CASCADE,
    membership_source TEXT NOT NULL CHECK(membership_source IN ('yuhaul_routed', 'user_driven', 'unknown')),
    detected_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    is_retention_signal BOOLEAN NOT NULL,
    UNIQUE(video_id, playlist_id)
);

-- Store B: advisories generated from channel statistics
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

-- Indexes for load-bearing lookups
CREATE INDEX IF NOT EXISTS idx_title_fingerprint_video_id ON title_fingerprint(video_id);
CREATE INDEX IF NOT EXISTS idx_title_fingerprint_channel_id ON title_fingerprint(channel_id);
CREATE INDEX IF NOT EXISTS idx_title_fingerprint_is_duplicate_reference ON title_fingerprint(is_duplicate_reference);

CREATE INDEX IF NOT EXISTS idx_video_channel_id ON video(channel_id);
CREATE INDEX IF NOT EXISTS idx_video_discovered_in_run_id ON video(discovered_in_run_id);
CREATE INDEX IF NOT EXISTS idx_video_processing_state ON video(processing_state);
CREATE INDEX IF NOT EXISTS idx_video_final_outcome ON video(final_outcome);

CREATE INDEX IF NOT EXISTS idx_route_decision_video_id ON route_decision(video_id);
CREATE INDEX IF NOT EXISTS idx_route_decision_run_id ON route_decision(run_id);
CREATE INDEX IF NOT EXISTS idx_route_decision_route_id ON route_decision(route_id);

CREATE INDEX IF NOT EXISTS idx_executed_action_run_id ON executed_action(run_id);
CREATE INDEX IF NOT EXISTS idx_executed_action_video_id ON executed_action(video_id);

CREATE INDEX IF NOT EXISTS idx_duplicate_match_video_id ON duplicate_match(video_id);
CREATE INDEX IF NOT EXISTS idx_duplicate_match_matched_fingerprint_id ON duplicate_match(matched_fingerprint_id);

CREATE INDEX IF NOT EXISTS idx_quota_ledger_date ON quota_ledger(date);
CREATE INDEX IF NOT EXISTS idx_quota_ledger_run_id ON quota_ledger(run_id);

CREATE INDEX IF NOT EXISTS idx_error_entry_status ON error_entry(status);
CREATE INDEX IF NOT EXISTS idx_deferred_item_status ON deferred_item(status);

CREATE INDEX IF NOT EXISTS idx_advisory_channel_id ON advisory(channel_id);
CREATE INDEX IF NOT EXISTS idx_advisory_status ON advisory(status);

CREATE INDEX IF NOT EXISTS idx_retention_signal_playlist_id ON retention_signal(playlist_id);

-- FTS5 virtual table backed by title_fingerprint.fingerprint_tokens
CREATE VIRTUAL TABLE IF NOT EXISTS title_fingerprint_fts USING fts5(
    fingerprint_tokens,
    content='title_fingerprint',
    content_rowid='id'
);

-- Keep the FTS5 index synchronized with the source table
CREATE TRIGGER IF NOT EXISTS title_fingerprint_fts_insert AFTER INSERT ON title_fingerprint
BEGIN
    INSERT INTO title_fingerprint_fts(rowid, fingerprint_tokens) VALUES (NEW.id, NEW.fingerprint_tokens);
END;

CREATE TRIGGER IF NOT EXISTS title_fingerprint_fts_delete AFTER DELETE ON title_fingerprint
BEGIN
    INSERT INTO title_fingerprint_fts(title_fingerprint_fts) VALUES('delete', OLD.id);
END;

CREATE TRIGGER IF NOT EXISTS title_fingerprint_fts_update AFTER UPDATE ON title_fingerprint
BEGIN
    INSERT INTO title_fingerprint_fts(title_fingerprint_fts) VALUES('delete', OLD.id);
    INSERT INTO title_fingerprint_fts(rowid, fingerprint_tokens) VALUES (NEW.id, NEW.fingerprint_tokens);
END;
