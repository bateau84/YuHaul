---
type: requirement
title: Temporal Statistics
description: Statistics must be viewable historically over time, not only as lifetime counters.
tags: [requirement, statistics, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL present statistics historically over time, allowing the user to observe changes and trends rather than viewing only lifetime aggregate counters.

## Acceptance criteria
1. Statistics are recorded with timestamps that allow temporal grouping
2. The user can view statistics for individual runs, specific time ranges, or aggregated periods
3. Lifetime counters are available but do not replace temporal views
4. Historical statistical data is retained across application restarts

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 23)

## Verified by

- Test: the user can view statistics for a specific date range
- Test: statistics from past runs are available alongside current statistics
- Test: temporal data persists across application restarts

## Depends on

- run-route-statistics.md
- source-channel-statistics.md
