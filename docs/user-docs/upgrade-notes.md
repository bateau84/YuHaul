---
type: guide
title: Upgrade Notes
description: Operator steps for releases that change the YuHaul database, including backups and rollback.
tags: [operations, upgrade, rollback, migration, release-notes]
timestamp: 2026-11-20T00:00:00Z
---

**Status:** current for releases up to and including database version 31 (migration `031`). Migration `030` ships in PR #4 (`2930a2d`; on `main` as `eed90f1`). Release check `6d65f7ef` proved the version 30 steps on real binaries, upgrading over a `debad57` (version 29) database and rolling back to `debad57`. Migration `031` ships in PR #5; release check `df803f9f` passed for it.

YuHaul upgrades its SQLite database automatically when a new image starts. Some upgrades cannot be undone by starting the older image again. This page lists those upgrades and what to do.

## Before any upgrade

1. Stop YuHaul, so no run is writing.
2. Copy the database file (and any `-wal` and `-shm` files next to it) to a safe place. Label the copy with the image version it belongs to.
3. Deploy the new image.

## Database version 30: failed channel scans

**What changes.** Migration `030` adds the record of channel scans that failed (for example when YouTube reports that a channel's uploads playlist was not found). It also adds the run's channel-scan outcome. Errors then shows a read-only **Channel scan errors** section, and Run Detail shows which channels could not be scanned.

**Deploy only onto a version 29 database.** The supported starting point is the production database written by `debad57` (version 29). **Do not deploy onto a database that has already reached version 30 from an interim or candidate build** (any test build of `030` before PR #4's final commits). Such a database counts `030` as applied and is never upgraded again, so it can lack parts of `030`, such as the run's scan-outcome column. If a database reached version 30 that way, restore its version 29 copy first, or start from a fresh database.

**Upgrade steps.**

1. Stop YuHaul, so no run is writing.
2. Copy `yuhaul.db` together with its `yuhaul.db-wal` and `yuhaul.db-shm` files, if present, to a safe place. The default location is `/data/yuhaul.db` (`YUHAUL_DB_PATH`). Copy all three files from the same stopped state. Label the copy `debad57 / version 29`.
3. Deploy the new image.
4. Check the startup log for `title search index ready`, and check that the database is at version 30 and not dirty. For example, with YuHaul stopped or on the copy: `sqlite3 /data/yuhaul.db "SELECT version, dirty FROM schema_migrations;"` should print `30|0`.

**Rollback.** After `030` has been applied, **an image built before `030` fails at startup** (`debad57` exits with `no migration found for version 30`), because it does not know database version 30. The database stays at version 30 and is not damaged. Rolling back the image alone does not work. To roll back:

1. Stop YuHaul.
2. Restore the copy from step 2: put back `yuhaul.db` and its `-wal` and `-shm` files exactly as copied, removing any newer `-wal` and `-shm` files.
3. Start `debad57`. It starts again on the restored version 29 database.

Anything recorded after that copy was taken, including runs, decisions, playlist actions and channel scan errors, is not in the restored database. Do not edit the migration table by hand to make an older image start.

Technical contract: [Discovery ARS-002](../architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md#release-notes).

## Database version 31: exact retry claims

**What changes.** Migration `031` adds one column, `run_video_outcome.retry_claim_provenance`, which stores the exact queue rows a retry run claimed for a video. It also clears, once, the old empty values that earlier versions stored for a missing video description or channel name (`''`) and a missing duration (`0`); they become empty (`NULL`) and are fetched again when a rule needs them. Nothing else in the database changes.

**Deploy only onto a version 30 database** written by `967df28` (`eed90f1` on `main`) or an equivalent build. Do not run an older YuHaul against a version 31 database afterwards: it is not a safe writer for it.

**Before deploying.**

1. Stop YuHaul **at a moment when no run is in progress**. A run that is unfinished when YuHaul stops is resumed or settled by the new version, so start from a quiet moment if you can.
2. Re-run the legacy-cohort check below against the stopped database (or a copy) and confirm that **every `[BLOCKS]` line prints `0`**. A non-zero line means unfinished work that an older version claimed for a retry; version 31 treats such work as unverifiable and will not dispatch or hand it off. Do not deploy until you understand the count.
3. Back up the database: copy `yuhaul.db` together with its `yuhaul.db-wal` and `yuhaul.db-shm` files, if present, from the same stopped state. The default location is `/data/yuhaul.db` (`YUHAUL_DB_PATH`). Label the copy `967df28 / version 30`.

The legacy-cohort check is read-only. Save it as `legacy-cohort-check.sql` and run `sqlite3 -readonly /data/yuhaul.db < legacy-cohort-check.sql`. It works on a version 29 or version 30 database, because it does not use the new column. On the production database, before and after `967df28` was deployed, every `[BLOCKS]` count was 0 (Loom records `997bc861` and `61ae9f35`); those runs used the researcher's query, which is not stored in this repository. The query below is a reconstruction of those checks, tried on a seeded database where it prints 0 for a clean database and for native unfinished work, and 1 for each of a claimed queue row, a foreign unfinished membership and a Stop-marked run holding work.

```sql
-- Legacy retry cohort check. Read-only: run it against a stopped database or a
-- copy. Every [BLOCKS] line must print 0 before you deploy version 31.
SELECT '[BLOCKS] queue rows claimed by an unfinished run' AS check_name, count(*) AS n
FROM (
  SELECT retry_run_id FROM deferred_item WHERE status = 'retrying'
  UNION ALL
  SELECT retry_run_id FROM error_entry WHERE status = 'retrying'
)
WHERE retry_run_id IN (SELECT id FROM run WHERE status IN ('running', 'interrupted'))
UNION ALL
SELECT '[BLOCKS] unfinished memberships that are not provably native', count(*)
FROM run_video_outcome o
JOIN run r ON r.id = o.run_id
JOIN video v ON v.id = o.video_id
WHERE r.status IN ('running', 'interrupted')
  AND r.run_type IN ('manual', 'scheduled')
  AND (o.final_outcome IS NULL OR o.final_outcome = 'not_yet_evaluated')
  AND NOT (
    (v.discovered_in_run_id = o.run_id
       OR EXISTS (SELECT 1 FROM route_decision d WHERE d.run_id = o.run_id AND d.video_id = o.video_id))
    AND NOT EXISTS (SELECT 1 FROM deferred_item q WHERE q.video_id = o.video_id AND q.retry_run_id = o.run_id)
    AND NOT EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = o.video_id AND e.retry_run_id = o.run_id)
  )
UNION ALL
SELECT '[BLOCKS] Stop-marked runs that still hold work', count(*)
FROM run r
WHERE r.stop_requested_at IS NOT NULL
  AND r.status IN ('running', 'interrupted')
  AND (
    EXISTS (SELECT 1 FROM deferred_item q WHERE q.retry_run_id = r.id AND q.status = 'retrying')
    OR EXISTS (SELECT 1 FROM error_entry e WHERE e.retry_run_id = r.id AND e.status = 'retrying')
    OR EXISTS (SELECT 1 FROM run_video_outcome o
               WHERE o.run_id = r.id AND (o.final_outcome IS NULL OR o.final_outcome = 'not_yet_evaluated'))
  )
UNION ALL
SELECT 'info: open deferred and error queue rows', (SELECT count(*) FROM deferred_item WHERE status IN ('pending', 'retrying'))
                                                  + (SELECT count(*) FROM error_entry WHERE status IN ('active', 'retrying'))
UNION ALL
SELECT 'info: unfinished production runs', count(*) FROM run WHERE status IN ('running', 'interrupted') AND run_type IN ('manual', 'scheduled');
```

**Deploying.**

1. Deploy the new image.
2. Check the startup log for `title search index ready`.
3. Check that the startup log has **no** `startup recovery blocked` line. That line means a run could not be settled or resumed and startup stopped.
4. Check that the database is at version 31 and not dirty, with YuHaul stopped or on a copy: `sqlite3 /data/yuhaul.db "SELECT version, dirty FROM schema_migrations;"` should print `31|0`.

**Rollback.** After `031` has been applied, **`967df28` refuses to start** (`no migration found for version 31`), because it does not know database version 31. The database stays at version 31 and is not damaged. Rolling back the image alone does not work. To roll back:

1. Stop YuHaul.
2. Restore all three files from the backup: put back `yuhaul.db`, `yuhaul.db-wal` and `yuhaul.db-shm` exactly as copied, and remove any newer `-wal` and `-shm` files.
3. Start `967df28`. It starts again on the restored version 30 database.

Anything recorded after the backup was taken, including runs, decisions, playlist actions and retry claims, is not in the restored database.

**Warnings.**

- **Clearing the old empty values is one-way.** Migration `031` has no down step. A real empty description or a real zero duration stored before the migration also becomes empty, and only the backup brings the old values back. Values stored after the migration are kept.
- **Never edit `schema_migrations` by hand** to make an older image start or to skip a migration. The database would then claim a version whose changes are missing or present, and neither image would be safe.

Technical contract: [run-initiation ARS-005 section 5.1.1](../architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md#511-durable-retry-claim-provenance) and [section 6.1.1](../architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md).
