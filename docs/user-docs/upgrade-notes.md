---
type: guide
title: Upgrade Notes
description: Operator steps for releases that change the YuHaul database, including backups and rollback.
tags: [operations, upgrade, rollback, migration, release-notes]
timestamp: 2026-11-24T00:00:00Z
---

**Status:** current for releases up to and including database version 31 (migration `031`). Migration `030` ships in PR #4 (`2930a2d`; on `main` as `eed90f1`). Release check `6d65f7ef` proved the version 30 steps on real binaries, upgrading over a `debad57` (version 29) database and rolling back to `debad57`. Migration `031` ships in PR #5; release check `df803f9f` passed for it. The **database version 32** section below is written for the re-queue release candidate. That release is not shipped, and no release check has yet proved its upgrade or rollback steps (2026-11-24).

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

The legacy-cohort check is one read-only `sqlite3` command. It is the exact command that was run on the production database before and after `967df28` was deployed, and every `[BLOCKS]` count was 0 both times (Loom records `997bc861` and `61ae9f35`). Run it as one invocation: it is a single statement, so it reads one consistent snapshot, and `-readonly` stops it from writing anything. Its output is `label | count` lines only, with no titles, video IDs or run IDs. The first two lines (`schema.migration_version`, `schema.migration_dirty`) confirm the database is at version 29 or 30 and not dirty. It does not use the new column, so it works on a database that has not been migrated yet.

The database path in the command is the production path of the system it was run on. Replace it with yours (the default is `/data/yuhaul.db`, from `YUHAUL_DB_PATH`), and run it with YuHaul stopped or on the copy from step 3.

```sh
sqlite3 -readonly -batch /data/disk2/opt/yuhaul/yuhaul.db <<'SQL'
.headers off
.mode list
.separator " | "
WITH
rc AS (
  SELECT id, status,
    (run_type IN ('manual','scheduled') AND status IN ('running','interrupted')) AS unfinished,
    (run_type IN ('manual','scheduled') AND status IN ('running','interrupted') AND stop_requested_at IS NOT NULL) AS marked,
    (run_type = 'manual' AND status = 'interrupted' AND interrupt_cause = 'time_limit' AND initiated_by = 'run_now') AS discard_shape
  FROM run
),
q AS (
  SELECT 'deferred' AS kind, video_id, retry_run_id AS owner,
         CASE status WHEN 'retrying' THEN 'claimed' ELSE 'waiting' END AS st
    FROM deferred_item WHERE status IN ('pending','retrying')
  UNION ALL
  SELECT 'error', video_id, retry_run_id,
         CASE status WHEN 'retrying' THEN 'claimed' ELSE 'waiting' END
    FROM error_entry WHERE status IN ('active','retrying')
),
qc AS (
  SELECT q.kind, q.st, q.video_id, q.owner,
    (COALESCE(v.final_outcome,'') IN ('accepted','rejected','excluded','unavailable_on_youtube')) AS on_final,
    (q.owner IS NULL) AS o_none,
    COALESCE(r.unfinished,0) AS o_unfin,
    COALESCE(r.marked,0) AS o_marked,
    COALESCE(r.discard_shape,0) AS o_disc,
    (q.owner IS NOT NULL AND r.id IS NOT NULL AND r.unfinished = 0) AS o_term,
    (q.owner IS NOT NULL AND r.id IS NULL) AS o_missing,
    EXISTS (SELECT 1 FROM run_video_outcome o WHERE o.run_id = q.owner AND o.video_id = q.video_id) AS has_member
  FROM q
  LEFT JOIN video v ON v.id = q.video_id
  LEFT JOIN rc r ON r.id = q.owner
),
m AS (
  SELECT o.run_id, rc.marked, rc.discard_shape,
    (COALESCE(v.final_outcome,'') IN ('accepted','rejected','excluded','unavailable_on_youtube')
       OR v.processing_state = 'completed'
       OR COALESCE(o.final_outcome,'') IN ('accepted','rejected','excluded','deferred','failed','unavailable_on_youtube')) AS finished,
    (v.discovered_in_run_id = o.run_id
       OR EXISTS (SELECT 1 FROM route_decision d WHERE d.video_id = o.video_id AND d.run_id = o.run_id)) AS native_ev,
    (EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = o.video_id AND d.retry_run_id = o.run_id)
       OR EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = o.video_id AND e.retry_run_id = o.run_id)) AS owner_row
  FROM run_video_outcome o
  JOIN rc ON rc.id = o.run_id AND rc.unfinished
  JOIN video v ON v.id = o.video_id
),
mu AS (SELECT * FROM m WHERE NOT finished)
SELECT 'schema.migration_version' AS label, COALESCE((SELECT version FROM schema_migrations LIMIT 1), -1) AS n
UNION ALL SELECT 'schema.migration_dirty', COALESCE((SELECT dirty FROM schema_migrations LIMIT 1), -1)
UNION ALL SELECT 'A.open_queue_rows.total', COUNT(*) FROM qc
UNION ALL SELECT 'A.open_queue_rows.on_final_video (cleanup, not cohort)', COALESCE(SUM(on_final),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_unfinished_run.total [BLOCKS]', COALESCE(SUM(st='claimed' AND NOT on_final AND o_unfin),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_unfinished_run.deferred', COALESCE(SUM(st='claimed' AND NOT on_final AND o_unfin AND kind='deferred'),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_unfinished_run.error', COALESCE(SUM(st='claimed' AND NOT on_final AND o_unfin AND kind='error'),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_marked_stop_run [BLOCKS]', COALESCE(SUM(st='claimed' AND NOT on_final AND o_marked),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_discard_shaped_run [BLOCKS]', COALESCE(SUM(st='claimed' AND NOT on_final AND o_disc),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_unfinished_run.without_membership', COALESCE(SUM(st='claimed' AND NOT on_final AND o_unfin AND NOT has_member),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_terminal_run (anomaly)', COALESCE(SUM(st='claimed' AND NOT on_final AND o_term),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_none (anomaly)', COALESCE(SUM(st='claimed' AND NOT on_final AND o_none),0) FROM qc
UNION ALL SELECT 'A.claimed.owner_missing_run (anomaly)', COALESCE(SUM(st='claimed' AND NOT on_final AND o_missing),0) FROM qc
UNION ALL SELECT 'A.waiting.owner_none (ordinary queue)', COALESCE(SUM(st='waiting' AND NOT on_final AND o_none),0) FROM qc
UNION ALL SELECT 'A.waiting.owner_terminal_run (released marker)', COALESCE(SUM(st='waiting' AND NOT on_final AND o_term),0) FROM qc
UNION ALL SELECT 'A.waiting.owner_unfinished_run (see B)', COALESCE(SUM(st='waiting' AND NOT on_final AND o_unfin),0) FROM qc
UNION ALL SELECT 'B.unfinished_runs.memberships.total', COUNT(*) FROM m
UNION ALL SELECT 'B.unfinished_runs.memberships.finished_member (history)', COALESCE(SUM(finished),0) FROM m
UNION ALL SELECT 'B.unfinished_runs.memberships.unfinished_member', COUNT(*) FROM mu
UNION ALL SELECT 'B.unfinished_member.native_proven (no owner row)', COALESCE(SUM(native_ev AND NOT owner_row),0) FROM mu
UNION ALL SELECT 'B.unfinished_member.AMBIGUOUS.total [BLOCKS]', COALESCE(SUM(NOT (native_ev AND NOT owner_row)),0) FROM mu
UNION ALL SELECT 'B.unfinished_member.AMBIGUOUS.owner_row_no_native', COALESCE(SUM(owner_row AND NOT native_ev),0) FROM mu
UNION ALL SELECT 'B.unfinished_member.AMBIGUOUS.owner_row_and_native', COALESCE(SUM(owner_row AND native_ev),0) FROM mu
UNION ALL SELECT 'B.unfinished_member.AMBIGUOUS.no_owner_no_native', COALESCE(SUM(NOT owner_row AND NOT native_ev),0) FROM mu
UNION ALL SELECT 'B.unfinished_member.AMBIGUOUS.in_marked_stop_run [BLOCKS]', COALESCE(SUM(marked AND NOT (native_ev AND NOT owner_row)),0) FROM mu
UNION ALL SELECT 'B.unfinished_member.AMBIGUOUS.in_discard_shaped_run [BLOCKS]', COALESCE(SUM(discard_shape AND NOT (native_ev AND NOT owner_row)),0) FROM mu
UNION ALL SELECT 'C.runs.production_unfinished', COALESCE(SUM(unfinished),0) FROM rc
UNION ALL SELECT 'C.runs.marked_stop', COALESCE(SUM(marked),0) FROM rc
UNION ALL SELECT 'C.runs.discard_shaped', COALESCE(SUM(discard_shape AND unfinished),0) FROM rc
UNION ALL SELECT 'C.runs.marked_stop_holding_claimed_rows [BLOCKS]', COUNT(DISTINCT CASE WHEN st='claimed' AND NOT on_final AND o_marked THEN owner END) FROM qc
UNION ALL SELECT 'C.runs.discard_shaped_holding_claimed_rows [BLOCKS]', COUNT(DISTINCT CASE WHEN st='claimed' AND NOT on_final AND o_disc THEN owner END) FROM qc
UNION ALL SELECT 'C.runs.marked_stop_with_ambiguous_members [BLOCKS]', COUNT(DISTINCT CASE WHEN marked AND NOT (native_ev AND NOT owner_row) THEN run_id END) FROM mu
UNION ALL SELECT 'C.runs.discard_shaped_with_ambiguous_members [BLOCKS]', COUNT(DISTINCT CASE WHEN discard_shape AND NOT (native_ev AND NOT owner_row) THEN run_id END) FROM mu
UNION ALL SELECT 'D.history.queue_rows_closed_with_owner', (SELECT COUNT(*) FROM deferred_item WHERE status IN ('completed','discarded') AND retry_run_id IS NOT NULL) + (SELECT COUNT(*) FROM error_entry WHERE status IN ('dismissed','resolved') AND retry_run_id IS NOT NULL)
UNION ALL SELECT 'D.history.queue_rows_closed_total', (SELECT COUNT(*) FROM deferred_item WHERE status IN ('completed','discarded')) + (SELECT COUNT(*) FROM error_entry WHERE status IN ('dismissed','resolved'))
UNION ALL SELECT 'D.history.memberships_of_ended_or_nonproduction_runs', (SELECT COUNT(*) FROM run_video_outcome o JOIN rc ON rc.id = o.run_id WHERE NOT rc.unfinished)
UNION ALL SELECT 'D.memberships.all', (SELECT COUNT(*) FROM run_video_outcome);
SQL
```

Every line labelled `[BLOCKS]` must print `0`. A non-zero `[BLOCKS]` line is unfinished retry work that an older version claimed and that version 31 would leave without a stored claim: it would not be dispatched, handed off or settled, so do not deploy until you understand the count. Lines without `[BLOCKS]` are for context: ordinary queue rows, native unfinished work (`native_proven`), closed history and anomalies. The definitions are in [run-initiation ARS-005 section 5.1.1](../architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md#511-durable-retry-claim-provenance).

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

## Database version 32: run dispatch stop, and re-queue of stranded videos (release candidate)

This section describes the re-queue release candidate. It is not shipped. Its upgrade and rollback steps below follow the version 30 and 31 pattern and have **not** been rehearsed on real binaries; the release check must prove them before they are relied on (2026-11-24).

**What changes.** Migration `032` adds one nullable column, `run.dispatch_stop`, with a check that allows only `authorization` or `accounting_or_integrity`, and a trigger that refuses to lower or clear a stored value. Every existing run reads empty (`NULL`); nothing is filled in afterwards. The release also adds the **Re-queue stranded videos** action on the Errors page, Deferred tab, and changes the Mark as ended text so that it says the videos can be re-queued afterwards. Re-queue uses the schema that version 30 already carries (migration `026`); no other table changes.

**Deploy only onto a version 31 database** written by `d1eba51` (PR #5) or `8d7e009` (PR #6, which added no migration), or an equivalent build.

**Upgrade steps.**

1. Stop YuHaul, so no run is writing.
2. Copy `yuhaul.db` together with its `yuhaul.db-wal` and `yuhaul.db-shm` files, if present, to a safe place. The default location is `/data/yuhaul.db` (`YUHAUL_DB_PATH`). Copy all three files from the same stopped state. Label the copy with the image it belongs to and `version 31`.
3. Deploy the new image.
4. Check that the startup log has **no** `startup recovery blocked` line, and that the database is at version 32 and not dirty, with YuHaul stopped or on a copy: `sqlite3 /data/yuhaul.db "SELECT version, dirty FROM schema_migrations;"` should print `32|0`.

**Rollback.** Migration `032` has no down file. An image that does not know version 32 is expected to refuse to start with `no migration found for version 32`, as `debad57` did for version 30 and `967df28` for version 31. The database would stay at version 32 and not be damaged. Rolling back the image alone does not work. To roll back, stop YuHaul, restore all three files from the backup exactly as copied (removing any newer `-wal` and `-shm` files), and start the older image. Anything recorded after the backup, including runs, decisions, playlist actions and re-queued items, is not in the restored database. Do not edit `schema_migrations` by hand to make an older image start.

**Warnings.**

- **A run that was stopped for authorization on a version 31 build and then resumed on version 32 has no stored stop.** It meets the authorization check again and the normal rules decide again ([ARS-005 section 6.1.3](../architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md)).
- **A video that YouTube no longer returns can end as `unavailable on YouTube`.** That outcome and everything that reads it shipped with migration `026` in PR #4, so images from `967df28` on know it. The earlier note in ARS-005 section 1.2 ("do not run an older binary once a row holds it") applies to images built before PR #4. This is read from the source at `8d7e009` and was not exercised in a rollback.

**Before using Re-queue for the first time.** A run that has not ended holds its unfinished videos, and the preview lists them as held ("Run {id} has not ended"). Run 14 is such a run: it is a version 0 run still saved as `running`. End it first with **Mark run 14 as ended** on its Run Detail page, or its videos stay held. Re-queue only adds items to the Deferred queue. It sends nothing to YouTube and starts no run; the next new run processes the items, as far as each day's quota allows. It is refused while a production run is active or startup recovery is in progress.

**Not yet established.** (1) The one-off count of old deferred rows with a future `retry_after` ([ADR-006 decision 5](../architecture/run-initiation/decisions/adr-006-requeue-stranded-videos.md)) has no recorded result; if it is not zero, the claim order must be revisited before release. (2) Neither the upgrade from version 31 nor the rollback has been run on real binaries. (3) The re-queue timing targets were measured on a synthetic fixture on one development host, not on the production database.

Technical contract: [run-initiation ARS-005](../architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md) sections 4, 6.1.3, 11.9 and 12.
