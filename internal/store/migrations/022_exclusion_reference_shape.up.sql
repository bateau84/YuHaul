DROP TRIGGER route_decision_exclusion_reference_insert;
DROP TRIGGER route_decision_exclusion_reference_update;

CREATE TRIGGER route_decision_exclusion_reference_insert
BEFORE INSERT ON route_decision
WHEN (NEW.exclusion_index IS NULL) <> (NEW.exclusion_semantics_version IS NULL)
  OR (NEW.exclusion_index IS NOT NULL AND (
      typeof(NEW.exclusion_index) <> 'integer'
      OR typeof(NEW.exclusion_semantics_version) <> 'integer'
      OR NEW.exclusion_index < 0 OR NEW.exclusion_semantics_version <= 0
      OR NEW.decision IS NOT 'skipped' OR NEW.skip_reason IS NOT 'exclusion'))
BEGIN
    SELECT RAISE(ABORT, 'invalid route decision exclusion reference');
END;

CREATE TRIGGER route_decision_exclusion_reference_update
BEFORE UPDATE OF exclusion_index, exclusion_semantics_version, decision, skip_reason ON route_decision
WHEN (NEW.exclusion_index IS NULL) <> (NEW.exclusion_semantics_version IS NULL)
  OR (NEW.exclusion_index IS NOT NULL AND (
      typeof(NEW.exclusion_index) <> 'integer'
      OR typeof(NEW.exclusion_semantics_version) <> 'integer'
      OR NEW.exclusion_index < 0 OR NEW.exclusion_semantics_version <= 0
      OR NEW.decision IS NOT 'skipped' OR NEW.skip_reason IS NOT 'exclusion'))
BEGIN
    SELECT RAISE(ABORT, 'invalid route decision exclusion reference');
END;
