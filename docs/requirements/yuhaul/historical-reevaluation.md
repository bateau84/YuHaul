---
type: requirement
title: Historical Re-evaluation Semantics
description: Configuration changes must never silently reprocess historical videos; re-evaluation is an explicit user-triggered operation.
tags: [requirement, routes, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL NOT silently reprocess previously observed or rejected videos when a route configuration changes. Historical re-evaluation is available only as an explicit user-triggered operation (replay or backtest). Production runs after a configuration change apply the new configuration only to newly discovered videos.

## Acceptance criteria

1. When a route configuration changes, subsequent production runs evaluate only newly discovered videos against the updated route — previously observed videos are not automatically re-evaluated
2. The user can explicitly trigger re-evaluation of historical videos through a replay or backtest operation
3. Explicit replay re-processes retained historical observations through the current routing configuration, producing outcomes that are recorded as replay results distinct from production results
4. The user can see whether a video's outcome was produced by a production run, a replay, or a backtest
5. Re-evaluation behavior is documented and visible to the user

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 25)

## Verified by

- Test: after a route configuration change, a subsequent production run does not re-evaluate previously observed videos
- Test: explicit replay re-processes historical videos and records replay outcomes distinct from production
- Test: run inspection distinguishes production outcomes from replay and backtest outcomes

## Depends on

- route-definition.md
- route-conditions-actions.md
- configuration-history.md
