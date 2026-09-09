---
type: requirement
title: Duplicate Provenance
description: Duplicate provenance must be retained to distinguish discovery credit from source relevance.
tags: [requirement, duplicates, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL retain sufficient provenance information for each duplicate detection to distinguish which source or route originally discovered a video from other sources or routes that subsequently matched it.

## Acceptance criteria
1. Each duplicate detection record identifies the original source that first discovered the video
2. Each duplicate detection record identifies which route or processing path flagged the duplicate
3. Provenance data is retained across runs and available for inspection
4. Provenance data supports distinguishing discovery credit from simple route matching

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 10)

## Verified by

- Test: inspection of a duplicate record reveals which source first discovered the video
- Test: inspection of a duplicate record reveals which route flagged it as a duplicate
- Test: provenance data persists across application restarts

## Depends on

- duplicate-detection.md
