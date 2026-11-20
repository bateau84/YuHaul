---
type: Log
title: Run 14 recovery specification log
description: Changes to the provisional controlled recovery contract.
tags: [recovery, specification, log]
timestamp: 2026-11-11T00:00:00Z
---

## 2026-11-11

- **Backlog decision superseded by BR-037.** Added a note to the [index](index.md): the user's 2026-11-11 approval of re-queuing stranded videos ([BR-037](../yuhaul/br-037-requeue-stranded-videos.md)) supersedes the 2026-11-10 decision to leave run 14's backlog alone. The proposal and [OC-001](oc-001-controlled-orphaned-work-recovery.md) stay declined, because BR-037 is a different capability. No code, database or live operation.
- **Pointer to BR-036.** Added a note to the [index](index.md): the user's 2026-11-11 decision lets a stuck legacy run such as run 14 be marked as ended ([BR-036](../yuhaul/br-036-mark-legacy-run-as-ended.md)), changing only its status and reason. The recovery decline is unchanged. No code, database or live operation.

## 2026-11-10

- **Declined by the user.** Marked the [contract](oc-001-controlled-orphaned-work-recovery.md) and [index](index.md) declined. Source: the user's explicit decision, relayed in the specification assignment (Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`): leave run 14 as it is, with no recovery and no backfill; its saved unevaluated videos stay unprocessed. The four open user choices are closed. No accepted requirement changed. No code, database or live operation.

## 2026-11-07

- Created [provisional recovery contract](oc-001-controlled-orphaned-work-recovery.md) and [topic index](index.md). Preserved accepted BRs; separated read-only assessment, retention repair and action activation. No live operation authorized or performed.
