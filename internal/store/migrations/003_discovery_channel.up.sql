-- Add discovery-specific columns to the channel table for cached uploads
-- playlists and per-channel watermarks.
ALTER TABLE channel ADD COLUMN uploads_playlist_id TEXT;
ALTER TABLE channel ADD COLUMN discovery_enabled BOOLEAN NOT NULL DEFAULT 1;
