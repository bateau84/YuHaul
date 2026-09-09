-- Down migration for T5.4 statistics/advisory support.
--
-- Removing analytics indexes only; the underlying tables are shared with other
-- subsystems and are left intact to avoid data loss.

DROP INDEX IF EXISTS idx_channel_channel_id;
DROP INDEX IF EXISTS idx_advisory_channel_id;
DROP INDEX IF EXISTS idx_advisory_status;
DROP INDEX IF EXISTS idx_retention_signal_playlist_id;
DROP INDEX IF EXISTS idx_retention_signal_video_id;
DROP INDEX IF EXISTS idx_duplicate_match_matched_fingerprint_id;
DROP INDEX IF EXISTS idx_route_decision_rejection_reason;
