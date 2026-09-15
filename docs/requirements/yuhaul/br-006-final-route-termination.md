---
type: requirement
title: BR-006 — Final Route Termination
description: A route marked final terminates further route evaluation after successful match
tags: [requirement, routes, final, termination, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL support marking a route as final, such that when a video successfully matches that route, no further routes are evaluated for that video in the current run.

## Acceptance criteria

1. The user can mark any route as final through the administration interface.
2. When a video matches a final route's conditions, evaluation for that video stops; subsequent routes in order are not evaluated.
3. The final flag is evaluated at match time: a final route at position 3 terminates evaluation at position 3; a non-final route at position 3 allows evaluation to continue to position 4.
4. The final termination is recorded in the evaluation history so the user can see that evaluation was terminated and by which route.
5. The final flag is preserved across runs and restarts.
6. If no route is marked final, all routes are evaluated normally.

## Derived from

- Anchor: youtube-discovery-routing.md (AC6)

## Verified by

- Unit test: final route at position 2 terminates evaluation; position 3 is not reached.
- Unit test: non-final route at position 2 allows evaluation to continue.
- History test: final termination event recorded with terminating route identity.
- Persistence test: final flag survives restart.

## Depends on

- BR-003 (route ordering)
- BR-005 (multi-route matching context)
