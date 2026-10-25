---
type: requirement
title: BR-025 — Configuration History for Statistics Interpretation
description: Route and configuration history sufficient to interpret historical statistics
tags: [requirement, configuration, history, statistics, traceability, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL maintain sufficient configuration history so that the user can interpret historical statistics in the context of what routing rules, conditions, exclusions, and settings were active at the time those statistics were generated.

## Acceptance criteria

1. Changes to route configuration (conditions, actions, order, enabled/disabled) are recorded with timestamps and the previous state.
2. Changes to exclusion rules are recorded with timestamps and the previous state.
3. Changes to duplicate detection settings (threshold, algorithm) are recorded with timestamps.
4. The user can view the configuration snapshot that was active during any historical run.
5. Historical statistics can be viewed alongside the active configuration for the corresponding period.
6. Configuration history is durable and survives restarts.
7. The user can compare configuration differences between two time points.

## Derived from

- Anchor: youtube-discovery-routing.md (AC25)

## Verified by

- Unit test: configuration change recorded with timestamp and previous state.
- Unit test: run record links to the active configuration snapshot.
- History test: configuration diff between two dates produces correct result.
- Persistence test: configuration history survives restart.

## Depends on

- BR-003 (route configuration provides configuration items)
- BR-004 (conditions and actions provide configuration items)
- BR-007 (exclusion configuration provides configuration items)
- OC-003 (configuration snapshot contract)
