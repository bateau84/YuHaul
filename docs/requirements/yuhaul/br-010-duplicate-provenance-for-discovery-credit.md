---
type: requirement
title: BR-010 — Duplicate Provenance for Discovery Credit
description: Duplicate provenance retained to distinguish discovery credit from other source relevance
tags: [requirement, duplicate, provenance, discovery-credit, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL retain provenance for duplicate detections so that discovery credit (which channel first surfaced the content) is distinguishable from other source relevance (later duplicates from other channels).

## Acceptance criteria

1. When a duplicate is detected, the system records which channel/video was the original discovery and which is the duplicate.
2. Discovery credit is attributed to the earliest-discovered instance of a video.
3. Later duplicates retain their channel/channel association but do not receive discovery credit.
4. Provenance information is available in the evaluation history and statistics.
5. Provenance data survives across runs and restarts.

## Derived from

- Anchor: youtube-discovery-routing.md (AC10)

## Verified by

- Unit test: first-discovered instance receives credit; later duplicates do not.
- History test: provenance data available in evaluation records.
- Persistence test: provenance survives restart.

## Depends on

- BR-008 (duplicate detection identifies originals and duplicates)
- BR-009 (historical corpus provides matching baseline)
