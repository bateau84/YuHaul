---
type: requirement
title: BR-028 — Historical Run Inspection
description: User can inspect historical runs and understand why videos were accepted, rejected, deferred, or failed
tags: [requirement, inspection, history, run-history, transparency, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL allow the user to inspect historical runs and understand the full evaluation history for any video, including why it was accepted, rejected, excluded, deferred, or failed, using only persisted evidence.

## Acceptance criteria

1. The user can browse a list of all historical runs with summary statistics (video count, outcome distribution, duration, quota consumed).
2. The user can select a run and see the list of videos processed in that run with their individual outcomes.
3. The user can select a video within a run and see the detailed evaluation path: which routes were evaluated, which conditions matched or failed, and the resulting disposition.
4. For excluded videos, the exclusion rule that prevented evaluation is shown.
5. For duplicate videos, the original video and discovery provenance are shown.
6. For deferred videos, the deferral reason (e.g., quota) is shown.
7. For failed videos, the failure reason and retry count are shown.
8. The user can search for a video across all historical runs by video identifier or title.
9. All inspection data is derived from persisted evidence, not reconstructed from live API calls.

## Derived from

- Anchor: youtube-discovery-routing.md (AC28)

## Verified by

- UI test: run list shows summary statistics for each run.
- UI test: video detail shows full evaluation path.
- Unit test: search by video ID returns all historical run entries.
- Evidence test: inspection data is complete without live API calls.

## Depends on

- BR-015 (explainable decisions provide underlying evidence)
- BR-011 (processing state persistence provides run records)
- BR-032 (outcome classification vocabulary)
