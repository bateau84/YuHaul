-- Normalize run timestamps to RFC3339 text.
--
-- The SQLite driver stores a bound time.Time in Go's String() format
-- ("2006-01-02 15:04:05.999999999 -0700 MST"), which does not compare
-- chronologically against RFC3339 strings in the range predicates used by
-- the analytics trend queries (GET /api/analytics/trends and
-- GET /api/analytics/route-match-rates): the from-day was excluded and the
-- to-day was included. Run writers now format RFC3339 explicitly; this
-- migration rewrites rows written in the old format. All run writers store
-- UTC timestamps, so only the "+0000 UTC" form is converted; rows with any
-- other offset are left untouched rather than silently mislabeled.
UPDATE run
SET started_at = substr(started_at, 1, 10) || 'T'
      || substr(started_at, 12, instr(started_at, ' +') - 12) || 'Z'
WHERE started_at LIKE '% +0000 UTC';

UPDATE run
SET completed_at = substr(completed_at, 1, 10) || 'T'
      || substr(completed_at, 12, instr(completed_at, ' +') - 12) || 'Z'
WHERE completed_at LIKE '% +0000 UTC';
