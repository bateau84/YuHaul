---
type: requirement
title: BR-026 — Dry-Run Evaluation
description: Dry-run evaluates routes without mutating external YouTube state
tags: [requirement, dry-run, evaluation, external-state, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL support dry-run evaluation mode in which routes are evaluated against videos and outcomes are recorded, but no external YouTube state is mutated (no playlist additions, no API calls that modify data).

## Acceptance criteria

1. The user can initiate a dry-run from the administration interface.
2. During dry-run, the system evaluates videos against all routes and records what actions would have been taken.
3. During dry-run, no YouTube API calls that mutate external state are executed (playlist adds, playlist removes).
4. Read-only API calls (video listing, channel enumeration) may be executed during dry-run to obtain necessary metadata.
5. Dry-run outcomes are recorded and visible in the run history with a dry-run indicator.
6. Dry-run results can be compared with actual run results to validate route configuration.
7. Dry-run does not affect idempotency state: videos processed in dry-run are not marked as already-processed.

## Derived from

- Anchor: youtube-discovery-routing.md (AC26)

## Verified by

- Unit test: dry-run records what actions would be taken without executing them.
- Integration test: no playlist mutation API calls during dry-run.
- Unit test: dry-run does not affect idempotency state.
- History test: dry-run marked as dry-run in run history.

## Depends on

- BR-003–BR-006 (route evaluation logic)
- BR-015 (decision evidence for dry-run outcomes)
