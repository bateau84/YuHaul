---
type: requirement
title: BR-003 — Multiple Ordered Route Definition
description: User can define multiple routes that are evaluated in a declared order
tags: [requirement, routes, configuration, ordering, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL allow the user to define multiple routes, each with a user-assigned evaluation order, such that a video's eligibility is tested against routes in the declared sequence.

## Acceptance criteria

1. The user can create, rename, reorder, enable, and disable routes through the administration interface.
2. Each route has a user-defined evaluation order (ordinal position) that determines processing sequence.
3. Route order is preserved across runs and restarts.
4. Disabled routes are skipped during evaluation without affecting the order of enabled routes.
5. The user can inspect the current route ordering at any time.
6. Routes persist across application restarts.

## Derived from

- Anchor: youtube-discovery-routing.md (AC3)

## Verified by

- Unit test: routes evaluate in declared order; reordering changes evaluation sequence.
- Persistence test: route order survives restart.
- UI test: disabled route is skipped; enabled routes maintain their relative order.

## Depends on

- None
