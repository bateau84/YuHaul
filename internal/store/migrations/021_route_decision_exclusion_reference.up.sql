ALTER TABLE route_decision ADD COLUMN exclusion_index INTEGER
    CHECK (exclusion_index IS NULL OR exclusion_index >= 0);
ALTER TABLE route_decision ADD COLUMN exclusion_semantics_version INTEGER
    CHECK (exclusion_semantics_version IS NULL OR exclusion_semantics_version > 0);
CREATE TRIGGER route_decision_exclusion_reference_insert
BEFORE INSERT ON route_decision
WHEN (NEW.exclusion_index IS NULL) <> (NEW.exclusion_semantics_version IS NULL)
  OR (NEW.exclusion_index IS NOT NULL AND
      (NEW.decision <> 'skipped' OR NEW.skip_reason <> 'exclusion'))
BEGIN
    SELECT RAISE(ABORT, 'invalid route decision exclusion reference');
END;
CREATE TRIGGER route_decision_exclusion_reference_update
BEFORE UPDATE OF exclusion_index, exclusion_semantics_version, decision, skip_reason ON route_decision
WHEN (NEW.exclusion_index IS NULL) <> (NEW.exclusion_semantics_version IS NULL)
  OR (NEW.exclusion_index IS NOT NULL AND
      (NEW.decision <> 'skipped' OR NEW.skip_reason <> 'exclusion'))
BEGIN
    SELECT RAISE(ABORT, 'invalid route decision exclusion reference');
END;
