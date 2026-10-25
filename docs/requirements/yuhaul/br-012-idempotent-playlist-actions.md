---
type: requirement
title: BR-012 — Idempotent Playlist Actions
description: Repeated execution does not repeat already completed playlist mutations
tags: [requirement, idempotency, playlist, actions, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL ensure that repeated execution of a run does not repeat playlist actions that were already completed in a prior run for the same video and route combination.

## Acceptance criteria

1. The system records completion of each playlist action with the video, route, target playlist, and timestamp.
2. Before executing a playlist action, the system checks whether the same action was previously completed for the same video and route.
3. If the action was already completed, it is skipped and recorded as already-applied rather than re-executed.
4. The idempotency check survives across runs and restarts.
5. The user can inspect which actions were skipped due to prior completion.

## Derived from

- Anchor: youtube-discovery-routing.md (AC12)

## Verified by

- Unit test: same video and route processed twice; second run records action as already-applied.
- Persistence test: idempotency state survives restart.
- History test: skipped actions visible in run history.

## Depends on

- BR-011 (processing state persistence provides completion tracking)
