-- Scheduler support: track run phase for resume and store user schedule.
ALTER TABLE run ADD COLUMN current_phase TEXT;

CREATE TABLE IF NOT EXISTS scheduler_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
