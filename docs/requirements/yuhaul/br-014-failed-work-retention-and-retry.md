---
type: requirement
title: BR-014 — Failed Work Retention and Retry
description: Failed work retained in inspectable error queue with retry and removal options
tags: [requirement, failure, retry, error-queue, resilience, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL retain failed work in an inspectable error queue, providing the user with the ability to retry, inspect the failure reason, or remove failed work.

## Acceptance criteria

1. When a processing action fails (API error, network error, quota error after retry, or other transient/permanent failure), the work item is recorded in an error queue with the failure reason and timestamp.
2. The user can inspect the error queue and see each failed item's video, route, action, failure reason, and number of retry attempts.
3. The user can manually retry a failed work item.
4. The user can remove a failed work item from the queue, accepting that it will not be retried.
5. The error queue is durable and survives application restarts.
6. The system distinguishes transient failures (eligible for automatic retry) from permanent failures (requiring user intervention).

## Derived from

- Anchor: youtube-discovery-routing.md (AC14)

## Verified by

- Unit test: failed work item appears in error queue with reason.
- Unit test: manual retry re-queues the item.
- Unit test: removal prevents further retry.
- Persistence test: error queue survives restart.
- Classification test: transient vs permanent failure distinguished.

## Depends on

- BR-011 (processing state persistence)
- OC-001 (failure semantics contract)
