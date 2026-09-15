---
type: requirement
title: BR-004 — Route Conditions and Playlist Actions
description: Each route contains configurable conditions that, when satisfied by a video, trigger one or more playlist actions
tags: [requirement, routes, conditions, actions, playlist, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL allow each route to contain a configurable set of conditions and, when all conditions of a route are satisfied by a video, execute the route's configured playlist actions.

## Acceptance criteria

1. A route can contain one or more conditions that are evaluated against video metadata (title, channel, duration, publication date, or other available metadata).
2. Each condition supports configurable matching semantics (exact, contains, pattern, threshold-based) as appropriate to the condition type.
3. Conditions within a route are composed conjunctively (AND) by default; the user can select disjunctive (OR) composition where supported.
4. A route specifies one or more playlist actions (add to playlist, with target playlist identified) to execute when conditions are satisfied.
5. Playlist actions reference user-configured YouTube playlists by a user-meaningful identifier.
6. Conditions and actions are modifiable without requiring a new route; changes persist and take effect on the next evaluation.
7. Route configuration is preserved across runs and restarts.

## Derived from

- Anchor: youtube-discovery-routing.md (AC4)

## Verified by

- Unit test: route with two conditions evaluates conjunctively; video satisfying one but not both does not trigger action.
- Unit test: route with OR composition triggers action when any condition is satisfied.
- Integration test: satisfied route conditions cause playlist action against configured playlist.
- Persistence test: route configuration survives restart.

## Depends on

- BR-003 (route ordering provides evaluation sequence)
