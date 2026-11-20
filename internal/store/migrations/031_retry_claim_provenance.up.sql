-- Durable retry claim provenance (run-initiation ARS-005 section 5.1.1).
--
-- retry_claim_provenance holds the exact set of queue rows one receiving run
-- claimed for one video: canonical JSON version 1 with deferred_rows and
-- error_rows, each a strictly ascending list of {id, origin_run_id}. Only the
-- queue claim writes it, once, on inserting the membership row; no later write
-- (outcome upsert, release, removal) touches it. NULL means no trustworthy
-- claim identity: native work, terminal history, or work claimed before this
-- migration. Old rows are not backfilled from owner pointers, counts or
-- timestamps, and Go treats an unfinished retry member with NULL as an
-- integrity failure. Value validity is checked in Go, not by a CHECK, so a
-- malformed value can be observed and refused rather than blocking its write.
ALTER TABLE run_video_outcome ADD COLUMN retry_claim_provenance TEXT;

-- Legacy Store A defaults (run-initiation ARS-005 section 6.1.1). Before the
-- corrected writers, a missing description or channel name was stored as ''
-- and a missing duration as 0. Clear exactly those defaults, once. The
-- migration version is the marker: no startup sweep and no read-side rule
-- may treat '' or 0 as missing. A real '' or 0 in an old row also becomes
-- NULL, which readiness-gated paths re-fetch. None of these columns is in the
-- title-search triggers (029), so the search index is untouched. There is no
-- down step that restores values.
UPDATE transient_metadata SET description      = NULL WHERE description      = '';
UPDATE transient_metadata SET channel_name     = NULL WHERE channel_name     = '';
UPDATE transient_metadata SET duration_seconds = NULL WHERE duration_seconds = 0;
