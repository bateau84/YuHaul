---
type: guide
title: Upgrade Notes
description: Operator steps for releases that change the YuHaul database, including backups and rollback.
tags: [operations, upgrade, rollback, migration, release-notes]
timestamp: 2026-11-20T00:00:00Z
---

**Status:** current for releases up to and including database version 30 (migration `030`). Migration `030` ships in PR #4 (`2930a2d`). Release check `6d65f7ef` proved the steps below on real binaries, upgrading over a `debad57` (version 29) database and rolling back to `debad57`.

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
