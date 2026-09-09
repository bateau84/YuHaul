---
type: requirement
title: Processing State Persistence
description: Processing state must survive across runs and application restarts.
tags: [requirement, state, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL persist all processing state such that it survives across individual runs and across application restarts without data loss, including the three-tiered outcome model: video/run processing states, per-route decision outcomes, and per-action execution outcomes.

## Acceptance criteria

1. The system records which videos have been processed in each run
2. The system records the video/run processing state for each video (discovered, evaluating, evaluated, acted, completed, deferred, not-yet-evaluated)
3. The system records per-route decision outcomes for each video-route evaluation (accepted, rejected with reason, skipped)
4. The system records per-action execution outcomes for each action (succeeded, failed with error, deferred with reason)
5. Processing state is retained after application restart and available to subsequent runs
6. The system can resume processing from the last known state without reprocessing previously completed work

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 11)

## Verified by

- Test: after application restart, the system reports the same processing state as before restart
- Test: a subsequent run does not reprocess videos already processed in prior runs
- Test: per-route and per-action outcomes are preserved across restarts

## Depends on

- None
