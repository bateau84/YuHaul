---
type: requirement
title: Idempotent Execution
description: Repeated execution must not repeat already completed playlist actions.
tags: [requirement, state, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL ensure that repeated execution of a processing run does not duplicate playlist actions that have already been completed.

## Acceptance criteria
1. The system tracks which playlist actions have been successfully completed for each video
2. Re-executing a run does not add a video to a playlist if it was already added in a prior execution
3. Idempotency is maintained even if the same run is executed multiple times manually
4. The system distinguishes between a completed action and a deferred or failed action for idempotency purposes

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 12)

## Verified by

- Test: executing the same run twice does not result in duplicate playlist entries
- Test: manually re-running a completed run produces no additional playlist mutations

## Depends on

- processing-state-persistence.md
- route-conditions-actions.md
