---
type: requirement
title: BR-032 — Processing Outcome Distinction
description: Outcomes distinguish rejected, deferred, failed, and not-yet-evaluated; absence of processing is never negative
tags: [requirement, outcomes, classification, semantics, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL distinguish between rejected, deferred, failed, and not-yet-evaluated work in all processing outcomes, and SHALL NOT represent the absence of processing as a negative routing decision.

## Acceptance criteria

1. Each processed video is classified into exactly one outcome category: accepted, rejected, excluded, deferred, failed, or unavailable on YouTube. *Unavailable on YouTube* is final and is used only where a requirement assigns it, currently [BR-037](br-037-requeue-stranded-videos.md) AC13 and AC18 for a re-queued video that YouTube no longer returns. It is not a rejection or a failure. (Amended 2026-11-11.)
2. Videos that have not yet been evaluated are classified as not-yet-evaluated, not as rejected.
3. The absence of processing (e.g., a video not yet reached in the queue) is never represented as a rejection or negative outcome.
4. Outcome categories are consistently used across run history, statistics, inspection UI, and decision evidence.
5. The user can filter run history by outcome category.
6. Outcome semantics are durable and consistent across runs.
7. Accepted, rejected, excluded and unavailable on YouTube are final outcomes; deferred, failed and not-yet-evaluated are not. A final outcome is never changed or reprocessed, as [BR-039](br-039-final-outcomes-are-never-reprocessed.md) requires. (Added 2026-11-12.)

## Derived from

- Anchor: youtube-discovery-routing.md (AC32)
- [Admin Interface Design Specification](../../design/yuhaul-admin-ui-design.md), §View: Run Detail (AC5's filter by outcome type)
- AC1 amendment, the *unavailable on YouTube* category: the user's decision of 2026-11-11 that a re-queued video that YouTube no longer returns gets that final outcome and stays visible in run details and outcome counts as its own category ([BR-037](br-037-requeue-stranded-videos.md), Derived from). AC4–5 then apply to it as to every other category.
- AC7: the user's decision of 2026-11-12 that a final outcome is never overwritten or reprocessed, relayed in the requirements assignment; the normative home is [BR-039](br-039-final-outcomes-are-never-reprocessed.md).

## Verified by

- Unit test: unprocessed video is classified as not-yet-evaluated.
- Unit test: no video is classified as rejected without an actual route evaluation producing rejection.
- Consistency test: outcome categories match across history, statistics, and inspection views, including unavailable on YouTube as its own category.
- UI test: selecting an outcome category in a historical run filters its video-outcome list to matching items; clearing the filter restores the full list.
- Semantics test: absence of processing never appears as negative outcome.

## Depends on

- None
