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
