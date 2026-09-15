---
type: requirement
title: BR-023 — Historical Statistics Over Time
description: Statistics viewable over time, not only lifetime counters
tags: [requirement, statistics, historical, temporal, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL present statistics historically over time, allowing the user to observe trends, compare periods, and understand how metrics change rather than viewing only cumulative lifetime counters.

## Acceptance criteria

1. All key statistics (run outcomes, route acceptance rates, channel usefulness, quota consumption) are stored with timestamps and are queryable by time range.
2. The user can view statistics for configurable periods: daily, weekly, monthly, or custom date ranges.
3. Historical statistics are not overwritten or lost when new data arrives.
4. The user can compare statistics across two or more time periods.
5. Historical statistics survive application restarts.
6. Statistics presentation distinguishes between current-period and lifetime aggregates.

## Derived from

- Anchor: youtube-discovery-routing.md (AC23)

## Verified by

- Unit test: statistics query for a specific date range returns correct data.
- Unit test: period comparison produces correct deltas.
- Persistence test: historical statistics survive restart.

## Depends on

- BR-020 (run/route statistics provide underlying data)
- BR-025 (configuration history provides context for interpretation)
