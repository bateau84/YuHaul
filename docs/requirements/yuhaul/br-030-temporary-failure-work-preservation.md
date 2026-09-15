---
type: requirement
title: BR-030 — Temporary Failure Work Preservation
description: Temporary API, quota, authorization, and runtime failures do not silently lose eligible work
tags: [requirement, failure, resilience, work-preservation, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL ensure that temporary failures — including API errors, quota exhaustion, authorization lapses, and runtime crashes — do not silently lose eligible work, and that all work in progress at the time of failure is recoverable.

## Acceptance criteria

1. When a runtime crash or unexpected shutdown occurs, work in progress is recoverable from persisted state on next startup.
2. When a YouTube API call fails due to a transient error, the affected work item is retained for retry rather than dropped.
3. When authorization expires mid-run, in-progress work is deferred (not lost) and the user is prompted to re-authorize.
4. When quota is exhausted mid-run, remaining work is deferred with a quota-exhaustion reason.
5. The system never silently discards a video that was in the processing pipeline at the time of failure.
6. The user can verify after a failure that all eligible work was preserved.

## Derived from

- Anchor: youtube-discovery-routing.md (AC30)

## Verified by

- Resilience test: crash mid-run; on restart, all in-progress work is recovered.
- API failure test: transient API error retains work for retry.
- Auth test: expired auth mid-run defers remaining work.
- Quota test: quota exhaustion mid-run defers remaining work with reason.
- Completeness test: no video is silently lost during any failure mode.

## Depends on

- BR-011 (processing state persistence)
- BR-013 (quota deferral)
- BR-014 (failed work retention)
- OC-001 (failure semantics contract)
