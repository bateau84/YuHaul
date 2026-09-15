---
type: requirement
title: BR-005 — Multi-Route Video Matching
description: A single video may satisfy conditions of multiple routes during evaluation
tags: [requirement, routes, matching, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL allow a single video to match conditions of multiple routes during evaluation, and each matching route's actions are independently recorded unless a prior route's final flag terminates evaluation.

## Acceptance criteria

1. A video is evaluated against all enabled routes in declared order (unless terminated by a final route).
2. When a video satisfies conditions of multiple non-final routes, all matching routes are recorded and their actions are eligible for execution.
3. The user can inspect which routes matched a given video in the evaluation history.
4. Matching against multiple routes does not cause duplicate execution of the same playlist action for the same video.

## Derived from

- Anchor: youtube-discovery-routing.md (AC5)

## Verified by

- Unit test: video satisfying two non-final routes records both matches; each route's action executes once.
- Unit test: same playlist action for same video is not repeated on re-evaluation.

## Depends on

- BR-003 (route ordering)
- BR-004 (conditions and actions)
