-- Reverse the run timestamp normalization by re-rendering RFC3339 text in
-- the driver's String() layout for UTC timestamps. Sub-second precision is
-- preserved; rows already in another format are left untouched.
UPDATE run
SET started_at = substr(started_at, 1, 10) || ' '
      || substr(started_at, 12, length(started_at) - 12) || ' +0000 UTC'
WHERE started_at LIKE '%Z';

UPDATE run
SET completed_at = substr(completed_at, 1, 10) || ' '
      || substr(completed_at, 12, length(completed_at) - 12) || ' +0000 UTC'
WHERE completed_at LIKE '%Z';
