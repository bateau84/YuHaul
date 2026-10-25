-- Durable corpus import completion metadata (ADR-015, ARS-002).
--
-- One singleton operational row records when the most recent successful
-- corpus import into THIS database completed. It is deliberately separate
-- from the `settings` table (effective configuration, not operational
-- metadata) and stores no import history, job state, raw title, upload
-- filename, source date, or counts. Job status stays process-local under
-- ADR-009; only this completion time is durable.
--
-- The value is canonical RFC 3339 Nano text in UTC, written by the shared
-- importer (migration.Importer.RunChunked) in one transaction after the
-- whole source is reconciled, every chunk is committed, and the final
-- destination corpus count is read. NULL means no reliable completion
-- timestamp is recorded.
--
-- No historical time is backfilled: publication dates, first_seen_at, source
-- video/run timestamps, file modification times, and import batch IDs do not
-- identify when an import into the current destination completed. A database
-- upgraded from an earlier version therefore starts with a NULL timestamp
-- until a successful post-migration import records one. No epoch/sentinel
-- date is used: it would be presented as a real completion time.

CREATE TABLE IF NOT EXISTS corpus_import_metadata (
    singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    last_successful_corpus_import_at TEXT
);

-- The singleton row starts with no recorded completion time.
INSERT INTO corpus_import_metadata (singleton_id, last_successful_corpus_import_at)
VALUES (1, NULL);
