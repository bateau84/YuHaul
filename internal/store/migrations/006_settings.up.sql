-- Add settings table for configurable effective-configuration values.
--
-- Settings are stored as key/value pairs. Values that need type conversion are
-- parsed at read time. Default settings are inserted so that the effective
-- configuration is always defined.

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Default effective-configuration values.
INSERT OR IGNORE INTO settings (key, value) VALUES ('duplicate_threshold', '0.5');
INSERT OR IGNORE INTO settings (key, value) VALUES ('duplicate_behavior', 'skip');
