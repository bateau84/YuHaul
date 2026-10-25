---
type: requirement
title: BR-019 — Quota-Aware Processing
description: Processing avoids unnecessary external API expenditure
tags: [requirement, quota, efficiency, cost-awareness, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL minimize unnecessary external API expenditure by avoiding redundant calls, caching reusable results, and short-circuiting processing when the outcome is already determined.

## Acceptance criteria

1. The system does not re-query data that was successfully fetched in the current run and is still within a freshness window.
2. The system avoids querying channels or videos that have already been fully processed in the current run.
3. The system short-circuits evaluation for a video whose outcome is already determined (e.g., already added to playlist, already excluded) without consuming additional API quota.
4. The user can observe the efficiency gains (calls avoided) in the run statistics.
5. Efficiency behavior is configurable (e.g., freshness windows can be adjusted).

## Derived from

- Anchor: youtube-discovery-routing.md (AC19)

## Verified by

- Unit test: cached data is reused without re-querying within freshness window.
- Unit test: already-processed video is skipped without additional API calls.
- Efficiency test: run with no new videos produces minimal API calls.

## Depends on

- BR-011 (processing state persistence enables skip-ahead)
- BR-018 (consumption tracking proves efficiency)
