---
type: requirement
title: BR-003 — Multiple Ordered Route Definition
description: User can create, read, update, delete, and reorder routes that are evaluated in a declared order
tags: [requirement, routes, configuration, ordering, crud, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL allow the user to create, read, update, delete, and reorder multiple routes, each with a user-assigned evaluation order, such that a video's eligibility is tested against routes in the declared sequence.

## Acceptance criteria

1. The user can create a route through the administration interface, providing a name and initial configuration.
2. The user can read (inspect) the full configuration of any individual route, including its conditions, actions, exclusions, evaluation order, and enabled/final status.
3. The user can update a route's name, description, conditions, actions, exclusions, evaluation order, enabled status, and final flag through the administration interface.
4. The user can delete a route through the administration interface. Deleting a route removes it from the evaluation sequence; historical run data referencing the deleted route is preserved for interpretability.
5. The user can reorder routes, and the declared order determines evaluation sequence.
6. The user can enable and disable routes. Disabled routes are skipped during evaluation without affecting the order of enabled routes.
7. Each route has a user-defined evaluation order (ordinal position) that determines processing sequence.
8. Route order and configuration are preserved across runs and restarts.
9. The user can inspect the current route list and ordering at any time.
10. Route names must be non-empty. The system rejects route creation or update when required fields (name, at least one condition, at least one action) are missing or invalid.
11. Any route configuration change (create, update, delete, reorder) takes effect on the next evaluation run; an in-progress run uses the configuration snapshot captured at run initiation.

## Derived from

- Anchor: youtube-discovery-routing.md (AC3)

## Verified by

- Unit test: routes evaluate in declared order; reordering changes evaluation sequence.
- Persistence test: route configuration and order survive restart.
- UI test: disabled route is skipped; enabled routes maintain their relative order.
- CRUD test: create, read, update, delete operations each produce the expected state change.
- Validation test: route creation with empty name or missing conditions/actions is rejected.
- History test: deleted route's historical run data remains inspectable.

## Depends on

- None
