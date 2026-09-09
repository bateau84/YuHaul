---
type: requirement
title: Error Queue
description: Failed work must be retained in an inspectable error queue with retry and resolution options; resolution preserves historical evidence.
tags: [requirement, errors, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL retain failed work in an inspectable error queue that exposes the failure reason and allows the user to retry or resolve the failed item. Resolution or dismissal of an error queue entry preserves the historical evidence of the failure; removing an entry from the active queue does not erase the historical record.

## Acceptance criteria

1. Videos that fail during processing are added to an error queue
2. Each error queue entry records the failure reason and the video identity
3. The user can inspect the error queue and see all failed items with their reasons
4. The user can retry a failed item, re-attempting its processing
5. The user can resolve or dismiss a failed item from the active error queue
6. Resolving or dismissing an error queue entry preserves the failure history; the record remains available for historical inspection

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 14)

## Verified by

- Test: a video that fails API access is added to the error queue with the failure reason
- Test: retrying a failed item re-attempts processing
- Test: resolving an item removes it from the active error queue
- Test: after resolving, the failure record remains available for historical inspection

## Depends on

- processing-state-persistence.md
