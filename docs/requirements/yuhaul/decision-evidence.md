---
type: requirement
title: Decision Evidence
description: Every meaningful route decision must be explainable from durable evidence.
tags: [requirement, evidence, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL record sufficient evidence for every meaningful route decision such that the user can inspect the decision and understand why a video was accepted, rejected, deferred, or failed.

## Acceptance criteria
1. Each route decision is associated with a durable evidence record
2. The evidence record identifies which route conditions matched or did not match
3. The evidence record identifies which actions were executed or not executed
4. The evidence record is retained across runs and available for historical inspection
5. Evidence records support the user's ability to explain any decision without external log analysis

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 15)

## Verified by

- Test: inspecting a decision record reveals which conditions matched and which actions executed
- Test: evidence records persist across application restarts
- Test: the user can explain a routing decision using only the system's evidence records

## Depends on

- processing-state-persistence.md
- route-conditions-actions.md
