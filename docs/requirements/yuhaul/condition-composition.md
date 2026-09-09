---
type: requirement
title: Condition Composition Semantics
description: The system must define how multiple conditions within a route are composed with explicit AND/OR semantics.
tags: [requirement, routes, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL define explicit composition semantics for multiple conditions within a route, specifying whether conditions are combined with ALL (AND) or ANY (OR) logic, and SHALL apply those semantics consistently during evaluation. The user SHALL be able to toggle the composition mode per route.

## Acceptance criteria

1. The user can set the composition logic for conditions within a route to either ALL (AND) or ANY (OR)
2. ALL composition: a video matches only when every condition in the route is satisfied
3. ANY composition: a video matches when at least one condition in the route is satisfied
4. The composition mode is visible in route configuration and decision evidence
5. The composition semantics are applied consistently across all route evaluations
6. The default composition mode is ALL

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 4)

## Verified by

- Test: a route set to ALL matches only when all conditions are satisfied
- Test: a route set to ANY matches when any single condition is satisfied
- Test: changing the composition mode from ALL to ANY changes which videos match
- Test: composition mode is reflected in decision evidence for inspected runs

## Depends on

- route-conditions-actions.md
