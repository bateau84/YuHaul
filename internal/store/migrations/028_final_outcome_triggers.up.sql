-- Final outcomes are never reprocessed (BR-039, run-initiation ARS-006 §1.2).
-- A final outcome on video or run_video_outcome cannot change to another
-- value. Writing the same value is allowed. Never apply this before the claim
-- reset is conditional on the video class (ARS-006 §9 invariant 7).
CREATE TRIGGER video_final_outcome_immutable
BEFORE UPDATE OF final_outcome ON video
WHEN OLD.final_outcome IN ('accepted','rejected','excluded','unavailable_on_youtube')
 AND NEW.final_outcome IS NOT OLD.final_outcome
BEGIN SELECT RAISE(ABORT, 'final outcome is immutable'); END;

CREATE TRIGGER run_video_outcome_final_immutable
BEFORE UPDATE OF final_outcome ON run_video_outcome
WHEN OLD.final_outcome IN ('accepted','rejected','excluded','unavailable_on_youtube')
 AND NEW.final_outcome IS NOT OLD.final_outcome
BEGIN SELECT RAISE(ABORT, 'final outcome is immutable'); END;
