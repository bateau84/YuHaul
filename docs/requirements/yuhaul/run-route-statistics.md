---
type: requirement
title: Run-Level and Route-Level Statistics
description: Operational statistics must be available at both the run level and the route level, with rejection counts broken down by reason.
tags: [requirement, statistics, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL provide operational statistics at both the run level and the route level, capturing processing outcomes for each run and each route within a run, with rejection counts broken down by rejection reason.

## Acceptance criteria

1. Each run has a summary count of videos by processing state: completed (routed to at least one playlist), deferred (held for quota or operational reasons), not-yet-evaluated (routing did not reach this video), and failed (encountered an error requiring intervention). Per-route decision counts (accepted, rejected, skipped) and per-action outcome counts (succeeded, failed) are available as drill-downs.
2. Each route within a run has statistics including videos matched, actions executed, and outcomes
3. Rejection counts at both run and route level are broken down by rejection reason (e.g., excluded channel, excluded title, non-matching conditions)
4. Run-level and route-level statistics are retained across runs for historical comparison
5. Statistics are accessible through the administration interface

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 20)

## Verified by

- Test: after a run, the system reports run-level statistics matching actual processing outcomes
- Test: after a run, the system reports route-level statistics for each route
- Test: rejection counts include breakdown by rejection reason
- Test: statistics from prior runs are available for comparison

## Depends on

- processing-state-persistence.md
- route-definition.md
