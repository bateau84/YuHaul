---
type: requirement
title: BR-013 — Quota-Limited Work Deferral
description: Quota-limited work deferred rather than lost or falsely rejected
tags: [requirement, quota, deferral, resilience, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL defer work when YouTube API quota is exhausted or insufficient, ensuring that deferred work is not lost, falsely rejected, or silently dropped, and is available for processing when quota becomes available.

## Acceptance criteria

1. The system detects when a YouTube API call would exceed the remaining quota and defers remaining work rather than attempting and failing.
2. Deferred work is durably recorded with a reason (quota exhaustion) and is not classified as rejected or failed.
3. Deferred work is automatically re-queued for processing in the next available run when quota is expected to be available.
4. The user can inspect which work was deferred and why.
5. Quota exhaustion is treated as an operational state, not an error condition.
6. The system does not repeatedly attempt API calls that will exhaust quota in a short window.

## Derived from

- Anchor: youtube-discovery-routing.md (AC13, Context: application treats quota exhaustion as operational state, not error)

## Verified by

- Unit test: when quota is exhausted, remaining work is deferred with quota-reason.
- Unit test: deferred work is re-queued for next run.
- Unit test: deferred work is not classified as rejected or failed.
- History test: deferred items visible in inspection UI with reason.

## Depends on

- BR-011 (processing state persistence)
- OC-001 (quota and failure semantics contract)
