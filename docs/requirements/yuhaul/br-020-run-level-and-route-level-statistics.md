---
type: requirement
title: BR-020 — Run-Level and Route-Level Statistics
description: Operational statistics available at run and route level
tags: [requirement, statistics, runs, routes, operational, priority:MUST]
---

**Status:** accepted
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
7. The Route List match count is the number of distinct videos matched by that route within the displayed time scope; repeated matched decisions for the same video and route count once.

## Derived from

- Anchor: youtube-discovery-routing.md (AC20)
- [Admin Interface Design Specification](../../design/yuhaul-admin-ui-design.md), §Route List — Route row details (match count is the number of videos matched by the route)

The design names videos, rather than match decisions, as the counted items. This requirement specifies the ordinary entity-count reading: one video contributes at most once per route and displayed time scope.

## Verified by

- Unit test: run summary contains expected fields with correct values.
- Unit test: route statistics accumulate across runs.
- Unit test: repeated matched decisions for the same video and route contribute one video to the Route List match count within the displayed time scope.
- Persistence test: statistics survive restart.
- UI test: statistics viewable in administration interface.

## Depends on

- BR-032 (outcome classification vocabulary)
- BR-015 (decision evidence provides underlying data)
