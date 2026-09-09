---
type: requirement
title: Final Route
description: A route can be marked as final, terminating further evaluation after a successful match.
tags: [requirement, routes, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL allow a route to be marked as final. When a video matches a final route, the system SHALL NOT evaluate that video against any subsequent routes.

## Acceptance criteria
1. The user can mark a route as final
2. When a video matches a final route, evaluation of that video stops after the final route's actions execute
3. Routes evaluated before the final route are not affected by the final route designation
4. A final route that does not match a video does not block evaluation of subsequent routes

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 6)

## Verified by

- Test: a video matching a final route is not evaluated against routes ordered after it
- Test: a video not matching a final route continues to be evaluated against subsequent routes
- Test: a final route at position 3 does not affect evaluation at positions 1 and 2

## Depends on

- route-definition.md
- multi-route-matching.md
