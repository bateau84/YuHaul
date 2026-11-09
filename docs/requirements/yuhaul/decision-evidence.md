---
type: requirement
title: Decision Evidence
description: Every meaningful route decision must be explainable from durable evidence.
tags: [requirement, evidence, MUST]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL record sufficient evidence for every meaningful route decision such that the user can inspect the decision and understand why a video was accepted, rejected, deferred, or failed.

The user-approved generic-only legacy boundary is defined by [BR-015 AC13](br-015-explainable-route-decisions.md) and shared through [OC-005](oc-005-decision-evidence-attribution-and-availability.md): old generic-only exclusion records remain explicitly incomplete and display “rule unavailable”, without reconstruction. This bounded exception applies to the historical explanation criteria below, not to new exclusion capture. Every newly evaluated exclusion still requires its actual causal rule, comparison and permitted configured value. Provider-derived historical-input retention remains separately unresolved; this exception grants no retention permission.

## Acceptance criteria
1. Each route decision is associated with a durable evidence record
2. The evidence record identifies which route conditions matched or did not match
3. The evidence record identifies which actions were executed or not executed
4. The evidence record is retained across runs and available for historical inspection
5. Evidence records support the user's ability to explain any decision without external log analysis

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 15)
- User's 2026-11-07 “Yes, that is ok” answer to whether old exclusion records may say “rule unavailable”, relayed verbatim in the specification assignment; exact provenance and bounded scope are recorded in [BR-015](br-015-explainable-route-decisions.md).

## Verified by

- Test: inspecting a decision record reveals which conditions matched and which actions executed
- Test: evidence records persist across application restarts
- Test: the user can explain a routing decision using only the system's evidence records

## Depends on

- processing-state-persistence.md
- route-conditions-actions.md
