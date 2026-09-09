-- Quota configuration settings (daily limit, reset schedule, API-reported reset).
CREATE TABLE IF NOT EXISTS quota_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
