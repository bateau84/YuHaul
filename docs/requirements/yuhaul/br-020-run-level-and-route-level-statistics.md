---
type: requirement
title: BR-020 — Run-Level and Route-Level Statistics
description: Operational statistics available at run and route level
tags: [requirement, statistics, runs, routes, operational, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL maintain and present operational statistics at both the run level and the route level, including video counts, evaluation outcomes, action execution counts, and timing.

## Acceptance criteria

1. Each run has a summary record with: videos discovered, videos evaluated, videos accepted/rejected/deferred/failed/excluded, actions executed, actions skipped, duration, and quota consumed.
2. Each route has accumulated statistics: videos matched, videos not matched, actions executed, and acceptance rate.
3. Run-level statistics are available for each historical run, not only the most recent.
4. Route-level statistics can be filtered or grouped by time period.
5. Statistics are durable and survive restarts.
6. The user can view statistics through the administration interface.

## Derived from

- Anchor: youtube-discovery-routing.md (AC20)

## Verified by

- Unit test: run summary contains expected fields with correct values.
- Unit test: route statistics accumulate across runs.
- Persistence test: statistics survive restart.
- UI test: statistics viewable in administration interface.

## Depends on

- BR-032 (outcome classification vocabulary)
- BR-015 (decision evidence provides underlying data)
