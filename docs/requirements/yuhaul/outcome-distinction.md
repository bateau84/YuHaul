---
type: requirement
title: Outcome Distinction
description: Processing outcomes must distinguish video/run processing states, per-route decision outcomes, and per-action execution outcomes; absence of processing is never represented as a negative routing decision.
tags: [requirement, state, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL maintain a three-tiered outcome model that distinguishes: (a) the video/run processing state (the overall lifecycle stage of a video), (b) per-route decision outcomes (whether a specific route accepted or rejected the video), and (c) per-action execution outcomes (whether a specific action succeeded, failed, or was deferred). The system SHALL NOT represent the absence of processing as a negative routing decision.

## Acceptance criteria

1. The video/run processing state tracks the overall lifecycle: discovered, evaluating, evaluated, acted, completed, deferred, or not-yet-evaluated
2. `not-yet-evaluated` means the video has not begun or completed routing for operational reasons (quota exhaustion, interruption, or other system-level reasons) — it does NOT include videos that were evaluated but skipped subsequent routes because an earlier final route matched
3. Each route that evaluates a video records a per-route decision outcome: accepted, rejected (with reason), or skipped (due to exclusion or disabled route)
4. A per-route decision of `skipped` due to an earlier final route match is recorded at the route level — the video WAS evaluated by the matching route, only subsequent routes were skipped
5. Each action executed on a video records a per-action execution outcome: succeeded, failed (with error), or deferred (with reason)
6. A video accepted by one route and rejected by another shows both per-route outcomes independently
7. A video whose action fails due to a transient error shows a per-action failure outcome while its per-route outcomes are preserved
8. A video deferred due to quota shows a deferred processing state and per-action deferred outcomes for unexecuted actions
9. The distinction between these three tiers is visible in run inspection and statistics

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 32)

## Verified by

- Test: a video not yet processed due to quota exhaustion is labeled as not-yet-evaluated
- Test: a video accepted by a final route and skipped by subsequent routes shows accepted at the matching route and skipped at subsequent routes — not as not-yet-evaluated
- Test: a video accepted by Route A and rejected by Route B shows both outcomes per route
- Test: a video deferred due to quota shows deferred state with per-action deferred outcomes
- Test: a video that encountered an API error shows per-action failure while per-route outcomes are preserved
- Test: these distinctions appear in run inspection and statistics views

## Depends on

- processing-state-persistence.md
- run-route-statistics.md
- run-inspection.md
