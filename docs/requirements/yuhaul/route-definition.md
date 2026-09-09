---
type: requirement
title: Route Definition
description: The user must be able to define multiple ordered routing rules that evaluate discovered videos.
tags: [requirement, routes, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL allow the user to define multiple named routes, each with a user-assigned evaluation order, that determine how discovered videos are processed.

## Acceptance criteria
1. The user can create, edit, and delete named routes
2. Each route has an evaluation order that the user can set and adjust
3. The user can enable or disable individual routes without deleting them
4. Routes are evaluated in the user-defined order during a processing run

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 3)

## Verified by

- Test: user creates two routes and the system evaluates them in the specified order
- Test: disabling a route causes it to be skipped during evaluation

## Depends on

- None
