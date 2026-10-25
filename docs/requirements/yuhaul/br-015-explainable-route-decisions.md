---
type: requirement
title: BR-015 — Explainable Route Decisions
description: Every meaningful route decision explainable from durable evidence
tags: [requirement, explainability, decisions, evidence, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL retain durable evidence for every meaningful route decision so that the user can understand why a video was accepted, rejected, deferred, excluded, or failed, using only the system's persisted records.

## Acceptance criteria

1. For every video processed, the system records the evaluation path: which routes were evaluated, which conditions passed or failed, and the resulting action or disposition.
2. Decision evidence includes the specific condition(s) that caused rejection or acceptance.
3. Decision evidence includes any exclusion rules that prevented evaluation.
4. Decision evidence includes duplicate detection results and provenance.
5. Decision evidence is durable and survives across runs and restarts.
6. The user can query a video by identifier and retrieve the full decision history.
7. Decision evidence is sufficient to reproduce the evaluation outcome without re-querying external APIs.

## Derived from

- Anchor: youtube-discovery-routing.md (AC15)

## Verified by

- Unit test: video decision record contains route-by-route evaluation path.
- Unit test: rejection record identifies the failing condition(s).
- Persistence test: decision evidence survives restart.
- Reproduction test: decision can be reconstructed from persisted evidence alone.

## Depends on

- BR-003–BR-006 (route evaluation provides decision logic)
- BR-032 (outcome classification vocabulary)
