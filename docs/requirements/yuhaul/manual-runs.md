---
type: requirement
title: Manual Runs
description: The user must be able to manually initiate processing runs at any time.
tags: [requirement, scheduling, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL allow the user to manually initiate a processing run at any time, independent of the configured schedule.

## Acceptance criteria
1. The user can trigger a processing run through the administration interface
2. A manually initiated run processes videos according to the same rules as a scheduled run
3. Manual runs do not interfere with the next scheduled run
4. The user can observe the progress and completion of a manual run

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 17)

## Verified by

- Test: the user triggers a manual run and the system begins processing
- Test: a manual run produces the same outcomes as a scheduled run for the same input state
- Test: a manual run does not prevent the next scheduled run from executing

## Depends on

- scheduled-runs.md
- processing-state-persistence.md
