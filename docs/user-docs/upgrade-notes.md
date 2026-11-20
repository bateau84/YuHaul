---
type: guide
title: Upgrade Notes
description: Operator steps for releases that change the YuHaul database, including backups and rollback.
tags: [operations, upgrade, rollback, migration, release-notes]
timestamp: 2026-11-20T00:00:00Z
---

**Status:** current for releases up to and including database version 30 (migration `030`). The migration is in the candidate on branch `interim-uploads-isolation`; it is not yet released.

YuHaul upgrades its SQLite database automatically when a new image starts. Some upgrades cannot be undone by starting the older image again. This page lists those upgrades and what to do.

## Before any upgrade

1. Stop YuHaul, so no run is writing.
2. Copy the database file (and any `-wal` and `-shm` files next to it) to a safe place. Label the copy with the image version it belongs to.
3. Deploy the new image.

## Database version 30: failed channel scans

**What changes.** Migration `030` adds the record of channel scans that failed (for example when YouTube reports that a channel's uploads playlist was not found). It also adds the run's channel-scan outcome. Errors then shows a read-only **Channel scan errors** section, and Run Detail shows which channels could not be scanned.

**Rollback.** After `030` has been applied, **an image built before `030` fails at startup**, because it does not know database version 30. Rolling back the image alone does not work. To roll back:

1. Stop YuHaul.
2. Restore the database copy taken before the first `030` image started.
3. Start the older image.

Anything recorded after that copy was taken, including runs, decisions, playlist actions and channel scan errors, is not in the restored database. Do not edit the migration table by hand to make an older image start.

Technical contract: [Discovery ARS-002](../architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md#release-notes).
