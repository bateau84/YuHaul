---
type: requirement
title: Multi-Route Matching
description: A single video may match more than one route during evaluation, until a matching final route terminates evaluation.
tags: [requirement, routes, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL evaluate every video against all enabled routes in order, allowing a video to match multiple routes and execute the actions of each matching route, until a matching final route terminates further evaluation for that video.

## Acceptance criteria

1. A video that matches the conditions of two or more routes triggers the actions of all matching routes, unless a final route terminates evaluation first
2. Matching one route does not prevent a video from being evaluated against subsequent routes, except when a matching final route terminates evaluation
3. The order of route evaluation is respected regardless of how many routes a video matches
4. When a video matches a final route, evaluation of that video stops after the final route's actions execute

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 5)

## Verified by

- Test: a video matching two non-final routes has actions from both routes executed
- Test: a video matching a final route at position 3 is not evaluated against routes ordered after position 3
- Test: route evaluation order is preserved across multiple matches

## Depends on

- route-definition.md
- route-conditions-actions.md
