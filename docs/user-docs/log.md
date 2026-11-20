---
type: Log
title: User and Operator Documentation Change Log
description: Changes to the user and operator documentation bundle.
tags: [changelog, user-docs, operations]
timestamp: 2026-11-20T00:00:00Z
---

# User and Operator Documentation Change Log

## 2026-11-20

**Updated:** [Upgrade notes](/docs/user-docs/upgrade-notes.md). Added the database version 31 section (migration `031`, PR #5), from release check `df803f9f`: stop with no run in progress, the legacy-cohort check with the query, backup of the database and its `-wal` and `-shm` files, the deploy checks (`title search index ready`, no `startup recovery blocked`, `31|0`), rollback by restoring all three files (`967df28` refuses version 31), and the one-way clearing and no-hand-edit warnings.

**Updated:** [Upgrade notes](/docs/user-docs/upgrade-notes.md). `030` now ships in PR #4 (`2930a2d`). Added the release check's (`6d65f7ef`) operator steps, the version-30 check, rollback to `debad57`, and the warning not to deploy onto a database already at version 30 from an interim build.

**Created:** [Upgrade notes](/docs/user-docs/upgrade-notes.md) with the backup step and the database version 30 rollback rule: after `030` is applied, a pre-`030` image fails at startup until a pre-`030` backup is restored. Follows [Discovery ARS-002](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md) Release notes.
