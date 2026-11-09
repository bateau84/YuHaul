-- ARD-001 durable run diagnostics (run-outcome-diagnostics Task 1.1).
--
-- selected_video_count is NULL when a run's input selection was never
-- captured (historical runs stay unknown and are never backfilled) and 0
-- when a selection completed and found no inputs.
--
-- outcome_reason explains an existing action outcome (for example an
-- already_completed no-attempt); it is explanatory evidence, not a new
-- action outcome category.
ALTER TABLE run ADD COLUMN selected_video_count INTEGER;
ALTER TABLE executed_action ADD COLUMN outcome_reason TEXT;
