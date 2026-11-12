-- Final outcomes are never reprocessed (BR-039, run-initiation ARS-006 §1.1).
-- closed_reason marks a queue entry closed because its video already has a
-- final outcome. NULL on every existing row; no triggers (migration 028 is
-- a later, separate step).
ALTER TABLE deferred_item ADD COLUMN closed_reason TEXT
  CHECK (closed_reason IS NULL OR closed_reason = 'final_outcome');
ALTER TABLE error_entry ADD COLUMN closed_reason TEXT
  CHECK (closed_reason IS NULL OR closed_reason = 'final_outcome');
