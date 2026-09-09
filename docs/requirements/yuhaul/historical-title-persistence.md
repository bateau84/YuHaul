---
type: requirement
title: Historical Title Persistence
description: Historical video titles must be persisted and the existing corpus must be importable.
tags: [requirement, duplicates, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL persist the titles of all previously processed videos and SHALL support importing the existing historical corpus of titles from a prior installation.
## Acceptance criteria

1. Every processed video's title is stored and retained indefinitely for audit and inspection
2. All historically observed titles — regardless of processing outcome — are retained and inspectable
3. Only titles from videos that were routed to at least one playlist participate in the duplicate-reference corpus used by duplicate detection
4. The system provides a mechanism to import the existing corpus of titles from a prior installation
5. Imported titles are treated identically to titles captured by the current system

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 9)

## Verified by

- Test: a video processed in a prior run has its title available for duplicate detection in a later run
- Test: imported historical titles produce the same duplicate detection results as titles captured natively

## Depends on

- duplicate-detection.md
