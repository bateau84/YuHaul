---
type: requirement
title: BR-027 — Historical Backtesting
description: Backtesting compares routing changes against retained observations without altering external state
tags: [requirement, backtesting, historical, comparison, priority:MUST]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

**First-use scope:** AC5 remains an accepted future requirement but is explicitly deferred from first-use launch. Only the same-app production-versus-simulation comparison is deferred; simulation isolation remains mandatory, and launch-critical subscription-to-playlist obligations are unchanged.

## Statement

The system SHALL support historical backtesting in which proposed routing changes are evaluated against retained historical observations (previously discovered videos and their metadata) to compare outcomes without altering any external YouTube state.

## Acceptance criteria

1. The user can select a set of historical observations (previously discovered videos) as the backtesting corpus.
2. The user can apply a proposed route configuration (or snapshot of routes at a point in time) to the backtesting corpus.
3. The backtesting run produces outcomes for each video in the corpus as if the proposed routes had been active.
4. No external YouTube API calls are executed during backtesting.
5. Backtesting results can be compared with actual historical production outcomes from the same YuHaul application to assess the impact of routing changes. This accepted future requirement is deferred from first-use launch; comparison with the former script or a broken version is not required.
6. Backtesting results are recorded and distinguishable from actual runs.
7. Backtesting does not affect idempotency state or operational statistics.

## Derived from

- Anchor: youtube-discovery-routing.md (AC27)
- User's 2026-10-25 initial-use decision, supplied for this amendment: retain AC5 as an accepted future same-app production-versus-simulation comparison, defer it from first-use launch, and require no comparison with the former script or a broken version. This does not defer simulation isolation or change subscription-to-playlist obligations.

## Verified by

- Unit test: backtesting evaluates retained corpus with proposed routes.
- Integration test: no external API calls during backtesting.
- Future AC5 comparison test: backtesting results can be compared with the same application's historical production outcomes. Missing comparison does not fail first-use launch acceptance; comparison with the former script or a broken version is not an acceptance criterion.
- Isolation test: backtesting does not affect idempotency or operational statistics.

## Depends on

- BR-008–BR-010 (duplicate detection for retained corpus)
- BR-015 (decision evidence for backtesting outcomes)
- BR-025 (configuration snapshots for historical route configurations)
