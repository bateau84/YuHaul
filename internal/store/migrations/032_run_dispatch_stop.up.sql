-- Run dispatch stop (run-initiation ARS-005 section 6.1.3). The run has proved
-- it must not start another run-owned YouTube request. Only the two levels that
-- cannot be read from the quota ledger are stored; the quota level is derived
-- from the run's own quota_ledger rows (quota-stop ARS-001 section 1) and is
-- never written here. NULL means no stored stop; old runs stay NULL.
ALTER TABLE run ADD COLUMN dispatch_stop TEXT
    CHECK (dispatch_stop IS NULL OR dispatch_stop IN ('authorization', 'accounting_or_integrity'));

-- Raise-only: authorization (rank 1) may become accounting_or_integrity
-- (rank 3); nothing lowers or clears a stored value.
CREATE TRIGGER run_dispatch_stop_raise_only
BEFORE UPDATE OF dispatch_stop ON run
WHEN OLD.dispatch_stop IS NOT NULL AND (
    NEW.dispatch_stop IS NULL OR
    (CASE NEW.dispatch_stop WHEN 'authorization' THEN 1 ELSE 3 END) <
    (CASE OLD.dispatch_stop WHEN 'authorization' THEN 1 ELSE 3 END))
BEGIN
    SELECT RAISE(ABORT, 'run dispatch_stop cannot be lowered or cleared');
END;
