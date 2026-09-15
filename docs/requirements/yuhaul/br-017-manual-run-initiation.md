---
type: requirement
title: BR-017 — Manual Run Initiation
description: User can trigger a processing run on demand
tags: [requirement, runs, manual, on-demand, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL allow the user to manually initiate a processing run at any time through the administration interface, independent of the configured schedule.

## Acceptance criteria

1. The user can trigger a manual run from the administration interface.
2. A manual run processes the same pipeline as a scheduled run (discovery, evaluation, action execution).
3. Manual runs are recorded in the run history with a manual trigger indicator.
4. The user can inspect the outcome of a manual run.
5. A manual run does not interfere with or reset the configured schedule.
6. The system prevents concurrent runs (manual and scheduled) from executing simultaneously.

## Derived from

- Anchor: youtube-discovery-routing.md (AC17)

## Verified by

- Unit test: manual run triggers full pipeline.
- Unit test: manual run recorded in history with manual indicator.
- Concurrency test: concurrent run attempt is prevented or queued.

## Depends on

- BR-011 (processing state persistence)
