-- Run initiation (run-initiation ADR-001 / ARS-001 §1).
-- initiated_by records who started a production run; interrupt_cause records
-- why a run was interrupted. Both are NULL for every pre-024 row and are never
-- backfilled: their origin cannot be proven, so they never qualify for the Run
-- Now resume (fail closed).
ALTER TABLE run ADD COLUMN initiated_by TEXT;
ALTER TABLE run ADD COLUMN interrupt_cause TEXT;

CREATE TRIGGER run_initiated_by_insert
BEFORE INSERT ON run
WHEN NEW.initiated_by IS NOT NULL AND NOT (
    (NEW.initiated_by IN ('run_now','queue_retry') AND NEW.run_type = 'manual')
    OR (NEW.initiated_by = 'schedule' AND NEW.run_type = 'scheduled'))
BEGIN
    SELECT RAISE(ABORT, 'invalid run initiator');
END;

CREATE TRIGGER run_initiated_by_immutable
BEFORE UPDATE OF initiated_by ON run
WHEN OLD.initiated_by IS NOT NEW.initiated_by
BEGIN
    SELECT RAISE(ABORT, 'run initiator is immutable');
END;

CREATE TRIGGER run_interrupt_cause_insert
BEFORE INSERT ON run
WHEN NEW.interrupt_cause IS NOT NULL
  AND (NEW.interrupt_cause NOT IN ('time_limit','shutdown') OR NEW.status <> 'interrupted')
BEGIN
    SELECT RAISE(ABORT, 'invalid run interrupt cause');
END;

CREATE TRIGGER run_interrupt_cause_update
BEFORE UPDATE OF interrupt_cause ON run
WHEN NEW.interrupt_cause IS NOT NULL
  AND (NEW.interrupt_cause NOT IN ('time_limit','shutdown') OR NEW.status <> 'interrupted')
BEGIN
    SELECT RAISE(ABORT, 'invalid run interrupt cause');
END;

-- Published-date range for Backtest/Replay (run-initiation ADR-002 / ARS-001 §1).
-- Calendar days (YYYY-MM-DD, UTC), written once in the run's INSERT and never
-- changed. NULL on every pre-024 row: those runs selected all retained videos.
ALTER TABLE run ADD COLUMN published_from TEXT;
ALTER TABLE run ADD COLUMN published_to TEXT;

CREATE TRIGGER run_published_range_insert
BEFORE INSERT ON run
WHEN (NEW.published_from IS NOT NULL OR NEW.published_to IS NOT NULL)
  AND (
    NEW.run_type NOT IN ('backtest','replay')
    OR (NEW.published_from IS NOT NULL AND NOT (
        typeof(NEW.published_from) = 'text' AND length(NEW.published_from) = 10
        AND NEW.published_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND IFNULL(date(NEW.published_from) = NEW.published_from, 0)))
    OR (NEW.published_to IS NOT NULL AND NOT (
        typeof(NEW.published_to) = 'text' AND length(NEW.published_to) = 10
        AND NEW.published_to GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND IFNULL(date(NEW.published_to) = NEW.published_to, 0)))
    OR (NEW.published_from IS NOT NULL AND NEW.published_to IS NOT NULL
        AND NEW.published_from > NEW.published_to))
BEGIN
    SELECT RAISE(ABORT, 'invalid run published range');
END;

CREATE TRIGGER run_published_range_immutable
BEFORE UPDATE OF published_from, published_to ON run
WHEN OLD.published_from IS NOT NEW.published_from OR OLD.published_to IS NOT NEW.published_to
BEGIN
    SELECT RAISE(ABORT, 'run published range is immutable');
END;
