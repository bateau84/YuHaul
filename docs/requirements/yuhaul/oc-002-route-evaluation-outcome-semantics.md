---
type: obligation-contract
title: OC-002 — Route Evaluation Outcome Semantics
description: Shared vocabulary and classification rules for routing outcomes, consumed by evaluation, persistence, statistics, and inspection
tags: [obligation-contract, routes, outcomes, classification, semantics]
---

**Status:** proposed

## Participants
- **Evaluation Engine**: the component that evaluates videos against route conditions and determines outcomes.
- **State Manager**: the component that persists processing state, completion status, and idempotency records.
- **Statistics/Reporting**: the component that aggregates and presents run and route statistics.
- **Inspection UI**: the component that presents historical run data and decision explanations to the user.

## Preconditions
- Routes are configured and ordered (BR-003, BR-004).
- Exclusion filtering has been applied (BR-007).
- Duplicate detection has been applied (BR-008).
- The system uses the outcome vocabulary defined in this contract.

## Guarantees
1. Every video processed through the evaluation pipeline receives exactly one primary outcome classification: **accepted**, **rejected**, **excluded**, **deferred**, or **failed**.
2. **Accepted**: the video matched one or more route conditions and its playlist action was executed (or would have been in dry-run).
3. **Rejected**: the video was evaluated against all applicable routes and matched no route conditions.
4. **Excluded**: the video was prevented from evaluation by an exclusion rule (channel, video, title, word/phrase, or duration exclusion) before route evaluation.
5. **Deferred**: the video was not evaluated due to an operational constraint (quota exhaustion, authorization pending, or other temporary inability to process).
6. **Failed**: the video's evaluation or action execution attempted and encountered a failure that prevents completion (permanent API error, action execution failure after retry).
7. **Not-yet-evaluated**: a video that has been discovered but has not yet reached evaluation in the current or any prior run. This is never classified as rejected or any other negative outcome.
8. The classification of a video can change across runs (e.g., deferred in run N, accepted in run N+1 after re-authorization).
9. All downstream consumers (State Manager, Statistics, Inspection UI) use this shared vocabulary and do not introduce local classification variants.
10. Absence of processing is never represented as a negative routing decision.

## Semantic inputs and outputs
- **Input**: Video metadata, route conditions, exclusion results, duplicate detection results, API response status.
- **Output**: One of {accepted, rejected, excluded, deferred, failed, not-yet-evaluated} with supporting evidence.

## Failure semantics
- If the evaluation engine cannot determine an outcome due to internal error, the video is classified as **failed** with the internal error reason.
- If a route condition evaluation fails due to missing data, the video is **deferred** for that route (not rejected).
- If an action execution fails, the video's route-specific outcome is **failed** but other matching routes' outcomes are independently determined.

## Invariants
- Every processed video has exactly one primary outcome classification.
- Outcome classification is consistent across all consumers (evaluation, state, statistics, inspection).
- Not-yet-evaluated is the only classification for videos not yet processed; absence of data is never a negative outcome.
- Outcome records include sufficient evidence to reconstruct the evaluation path.

## Derived from
- Anchor: youtube-discovery-routing.md (AC32, AC15, AC20, AC28)

## Verified by
- Unit test: every video in a run has exactly one outcome classification.
- Unit test: unprocessed video classified as not-yet-evaluated.
- Consistency test: outcome categories match across state, statistics, and inspection views.
- Evidence test: each outcome record contains sufficient evaluation path data.

## Open questions
- None
