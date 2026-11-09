---
type: requirement
title: BR-030 — Temporary Failure Work Preservation
description: Temporary API, quota, authorization, and runtime failures do not silently lose eligible work
tags: [requirement, failure, resilience, work-preservation, priority:MUST]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL ensure that temporary failures — including API errors, quota exhaustion, authorization lapses, and runtime crashes — do not silently lose eligible work, and that all work in progress at the time of failure is recoverable.

## Acceptance criteria

1. When a runtime crash or unexpected shutdown occurs, work in progress is recoverable from persisted state on next startup.
2. When a YouTube API call fails due to a transient error, the affected work item is retained for retry rather than dropped.
3. When access authorization expires mid-run, the system resolves available automatic renewal under [BR-034](br-034-automatic-youtube-token-refresh.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md) before requiring interactive reauthorization. If recovery cannot safely continue, in-progress work is preserved/deferred (not lost) with the applicable renewal-unavailable or reconnect-required reason. Missing/rejected refresh authorization prompts reconnection; expiry alone does not. Renewal does not authorize blind replay of an uncertain playlist mutation.
4. When quota is exhausted mid-run, remaining work is deferred with a quota-exhaustion reason.
5. The system never silently discards a video that was in the processing pipeline at the time of failure.
6. The user can verify after a failure that all eligible work was preserved.

## Derived from

- Anchor: youtube-discovery-routing.md (AC30)
- User's 2026-11-07 refresh amendment recorded in [BR-034](br-034-automatic-youtube-token-refresh.md); changes expiry remediation only, preserving the existing work-retention promise.

## Verified by

- Resilience test: crash mid-run; on restart, all in-progress work is recovered.
- API failure test: transient API error retains work for retry.
- Auth test: mid-run access expiry renews when possible; unavailable renewal preserves remaining work with its safe reason, and missing/rejected refresh authorization offers reconnecting. Uncertain mutations remain protected under OC-006.
- Quota test: quota exhaustion mid-run defers remaining work with reason.
- Completeness test: no video is silently lost during any failure mode.

## Depends on

- BR-011 (processing state persistence)
- BR-013 (quota deferral)
- BR-014 (failed work retention)
- OC-001 (failure semantics contract)
