---
type: requirement
title: Failure Retention
description: Temporary API, authorization, and runtime failures must not silently lose eligible work; quota exhaustion is handled separately as an operational state.
tags: [requirement, errors, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL retain work that fails due to temporary API errors, authorization issues, or runtime failures, ensuring no eligible work is silently lost. Quota exhaustion is not classified as a failure; it is an operational state handled through the deferral mechanism.

## Acceptance criteria

1. Videos that fail due to temporary API errors, authorization issues, or runtime failures are recorded in the system, not discarded
2. The failure reason is categorized (API error, authorization, runtime) — quota exhaustion is excluded from this categorization
3. The system does not silently drop or skip videos that encountered transient failures
4. Failed work remains in an inspectable state until explicitly retried or removed by the user
5. Temporary failures do not produce false negative routing decisions

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 30)

## Verified by

- Test: a video that fails due to a temporary API error is recorded with the failure category
- Test: a video that encounters quota exhaustion is handled through the deferral mechanism, not the failure mechanism
- Test: failed work is inspectable and does not appear as a routing rejection

## Depends on

- error-queue.md
- quota-deferral.md
