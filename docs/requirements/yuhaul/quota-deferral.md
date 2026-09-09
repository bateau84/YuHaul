---
type: requirement
title: Quota Deferral
description: Work deferred due to quota limits must be retained, not lost or falsely rejected.
tags: [requirement, quota, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
When processing is limited by API quota exhaustion, the system SHALL defer remaining work for later processing rather than discarding it or recording it as a rejection.

## Acceptance criteria
1. The system detects when API quota is exhausted during a processing run
2. Videos not yet processed due to quota exhaustion are marked as deferred
3. Deferred work is retained and available for processing in a subsequent run
4. Quota exhaustion is not recorded as a routing rejection or processing failure

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 13)

## Verified by

- Test: when quota is exhausted mid-run, remaining videos are marked as deferred, not rejected
- Test: deferred videos are available for processing in the next run
- Test: quota exhaustion is reported as an operational state, not a rejection

## Depends on

- processing-state-persistence.md
