---
type: requirement
title: Duplicate Detection
description: The system must detect historical duplicates using exact and configurable fuzzy matching, operating against the corpus of videos that were routed to playlists.
tags: [requirement, duplicates, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL detect whether a discovered video is a historical duplicate of a previously processed video, supporting both exact matching and configurable fuzzy matching based on title similarity. Duplicate detection operates against a corpus of titles from videos that were routed to at least one playlist — the duplicate-reference corpus — rather than against every video the system has ever inspected.

## Acceptance criteria

1. The system detects exact duplicates by comparing video identifiers
2. The system detects fuzzy duplicates by comparing title similarity against a user-configurable threshold
3. The fuzzy matching threshold is adjustable by the user
4. Duplicate detection operates against the corpus of titles from videos that were routed to at least one playlist — videos that were rejected, excluded, failed, or never evaluated are not included in the duplicate-reference corpus
5. All historically observed titles are retained for audit and inspection purposes, even if they are not eligible as duplicate references
6. Detected duplicates are flagged and handled according to user-configured duplicate behavior

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 8)

## Verified by

- Test: a video with an identical identifier to a previously routed video is detected as an exact duplicate
- Test: a video with a title similar above the threshold to a previously routed video is detected as a fuzzy duplicate
- Test: a video that was rejected or excluded in a prior run does not appear in the duplicate-reference corpus
- Test: adjusting the threshold changes which videos are considered fuzzy duplicates
- Test: duplicate detection operates against historical data from prior runs

## Depends on

- None
