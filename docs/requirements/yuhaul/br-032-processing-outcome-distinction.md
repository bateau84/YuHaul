---
type: requirement
title: BR-032 — Processing Outcome Distinction
description: Outcomes distinguish rejected, deferred, failed, and not-yet-evaluated; absence of processing is never negative
tags: [requirement, outcomes, classification, semantics, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL distinguish between rejected, deferred, failed, and not-yet-evaluated work in all processing outcomes, and SHALL NOT represent the absence of processing as a negative routing decision.

## Acceptance criteria

1. Each processed video is classified into exactly one outcome category: accepted, rejected, excluded, deferred, or failed.
2. Videos that have not yet been evaluated are classified as not-yet-evaluated, not as rejected.
3. The absence of processing (e.g., a video not yet reached in the queue) is never represented as a rejection or negative outcome.
4. Outcome categories are consistently used across run history, statistics, inspection UI, and decision evidence.
5. The user can filter run history by outcome category.
6. Outcome semantics are durable and consistent across runs.

## Derived from

- Anchor: youtube-discovery-routing.md (AC32)

## Verified by

- Unit test: unprocessed video is classified as not-yet-evaluated.
- Unit test: no video is classified as rejected without an actual route evaluation producing rejection.
- Consistency test: outcome categories match across history, statistics, and inspection views.
- Semantics test: absence of processing never appears as negative outcome.

## Depends on

- None
