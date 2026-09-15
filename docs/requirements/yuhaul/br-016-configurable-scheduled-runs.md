---
type: requirement
title: BR-016 — Configurable Scheduled Runs
description: User configures when processing runs execute on a schedule
tags: [requirement, scheduling, runs, configuration, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL allow the user to configure a schedule for processing runs, including interval, time-of-day, and day-of-week as appropriate.

## Acceptance criteria

1. The user can set a run schedule through the administration interface (e.g., every N hours, daily at a specific time, weekly on specific days).
2. Scheduled runs execute at or near the configured time without requiring manual intervention.
3. The user can modify or disable the schedule at any time.
4. If a scheduled run time is missed (e.g., application was down), the system handles the missed run according to user preference (run immediately on next start, skip, or run at next scheduled time).
5. The schedule configuration is persisted across restarts.
6. The user can view the current schedule and the next scheduled run time.

## Derived from

- Anchor: youtube-discovery-routing.md (AC16)

## Verified by

- Unit test: schedule configuration persists across restart.
- Integration test: run executes at configured time.
- Test: missed run handled per user preference.

## Depends on

- None
