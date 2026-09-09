---
type: requirement
title: Run Inspection
description: The user must be able to inspect historical runs and understand why videos were accepted, rejected, deferred, or failed.
tags: [requirement, evidence, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL allow the user to inspect historical processing runs and understand the reason each video was accepted, rejected, deferred, or failed.

## Acceptance criteria
1. The user can browse a list of historical runs
2. The user can view the processing outcomes for each video in a selected run
3. For each video outcome, the system presents the reason (which route matched, which condition failed, what error occurred)
4. Run inspection data is retained across application restarts
5. Run inspection does not require external log analysis or manual correlation

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 28)

## Verified by

- Test: the user can select a historical run and see each video's outcome with explanation
- Test: the explanation identifies the specific route, condition, or error responsible for the outcome
- Test: historical run data persists across application restarts

## Depends on

- decision-evidence.md
- processing-state-persistence.md
- run-route-statistics.md
