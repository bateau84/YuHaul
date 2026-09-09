---
type: requirement
title: Scheduled Runs
description: Processing runs must be configurable by the user on a schedule.
tags: [requirement, scheduling, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL support user-configured scheduling of processing runs, allowing the user to define when and how frequently automatic runs execute.

## Acceptance criteria
1. The user can configure a schedule for automatic processing runs
2. The system initiates processing runs according to the configured schedule
3. The user can modify or disable the schedule
4. Scheduled runs execute without manual intervention

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 16)

## Verified by

- Test: after configuring a schedule, a processing run initiates at the scheduled time
- Test: modifying the schedule changes when subsequent runs execute
- Test: disabling the schedule prevents automatic runs

## Depends on

- None
