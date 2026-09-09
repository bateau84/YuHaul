---
type: requirement
title: Route Conditions and Actions
description: Each route must support configurable conditions for matching and actions to execute on match.
tags: [requirement, routes, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
Each route SHALL contain configurable conditions that determine whether a video matches the route and configurable actions that execute when a video matches.

## Acceptance criteria
1. The user can add, edit, and remove conditions on a route
2. Conditions evaluate video properties (such as title, channel, duration) against user-defined criteria
3. The user can add, edit, and remove actions on a route
4. When a video matches a route's conditions, the route's actions are executed
5. The system supports adding a video to a YouTube playlist as a route action

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 4)

## Verified by

- Test: a route with a title-contains condition matches a video whose title contains the specified text
- Test: a route with a playlist-add action adds the matched video to the specified playlist
- Test: editing a condition changes which videos match the route

## Depends on

- route-definition.md
- account-connection.md
