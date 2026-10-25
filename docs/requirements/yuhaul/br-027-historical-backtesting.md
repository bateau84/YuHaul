---
type: requirement
title: BR-027 — Historical Backtesting
description: Backtesting compares routing changes against retained observations without altering external state
tags: [requirement, backtesting, historical, comparison, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL support historical backtesting in which proposed routing changes are evaluated against retained historical observations (previously discovered videos and their metadata) to compare outcomes without altering any external YouTube state.

## Acceptance criteria

1. The user can select a set of historical observations (previously discovered videos) as the backtesting corpus.
2. The user can apply a proposed route configuration (or snapshot of routes at a point in time) to the backtesting corpus.
3. The backtesting run produces outcomes for each video in the corpus as if the proposed routes had been active.
4. No external YouTube API calls are executed during backtesting.
5. Backtesting results can be compared with the actual historical outcomes to assess the impact of routing changes.
6. Backtesting results are recorded and distinguishable from actual runs.
7. Backtesting does not affect idempotency state or operational statistics.

## Derived from

- Anchor: youtube-discovery-routing.md (AC27)

## Verified by

- Unit test: backtesting evaluates retained corpus with proposed routes.
- Integration test: no external API calls during backtesting.
- Comparison test: backtesting results can be diffed with historical actuals.
- Isolation test: backtesting does not affect idempotency or operational statistics.

## Depends on

- BR-008–BR-010 (duplicate detection for retained corpus)
- BR-015 (decision evidence for backtesting outcomes)
- BR-025 (configuration snapshots for historical route configurations)
