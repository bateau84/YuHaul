---
type: requirement
title: BR-004 — Route Conditions and Playlist Actions
description: Each route contains configurable conditions that, when satisfied by a video, trigger one or more playlist actions
tags: [requirement, routes, conditions, actions, playlist, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL allow each route to contain configurable conditions and actions, and execute the route's configured playlist actions when the route's configured condition composition is satisfied by a video.

## Acceptance criteria

1. A route can contain one or more conditions that are evaluated against video metadata (title, channel, duration, publication date, or other available metadata).
2. Each condition supports configurable matching semantics (exact, contains, pattern, threshold-based) as appropriate to the condition type.
3. Conditions within a route are composed conjunctively (AND) by default; the user can select disjunctive (OR) composition where supported.
4. A route specifies one or more playlist actions (add to playlist, with target playlist identified) to execute when conditions are satisfied.
5. Playlist actions reference user-configured YouTube playlists by a user-meaningful identifier.
6. Conditions and actions are modifiable without requiring a new route; changes persist and take effect on the next evaluation.
7. Route configuration is preserved across runs and restarts.
8. Before a route is saved, each action must have a supported action type and its required parameters. In v1, the available action is **Add to playlist**, and a target playlist must be selected; a save missing the action type or target playlist is rejected with a validation error.

## Derived from

- [Anchor](../../anchors/youtube-discovery-routing.md) (AC4)
- [Admin Interface Design Specification](../../design/yuhaul-admin-ui-design.md), §Route Editor — Action configuration UI and Validation rules

## Verified by

- Unit test: route with two conditions evaluates conjunctively; video satisfying one but not both does not trigger action.
- Unit test: route with OR composition triggers action when any condition is satisfied.
- Integration test: satisfied route conditions cause playlist action against configured playlist.
- Route-editor validation test: an action without a supported type or target playlist cannot be saved and produces a validation error.
- Persistence test: route configuration survives restart.

## Depends on

- [BR-003 — Multiple Ordered Route Definition](br-003-multiple-ordered-route-definition.md) (route ordering provides evaluation sequence)
