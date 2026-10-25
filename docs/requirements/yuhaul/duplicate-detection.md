---
type: requirement
title: Duplicate Detection
description: The system must detect historical duplicates using exact and configurable fuzzy matching, operating against the corpus of videos that were routed to playlists.
tags: [requirement, duplicates, MUST]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Authority reconciliation

This accepted legacy requirement is not listed in the canonical BR/OC tables. The user's explicit 2026-10-25 “Continue, yes” decision supersedes AC6's configurable-handling promise only, as recorded in [BR-008 AC9–AC11](br-008-exact-and-fuzzy-duplicate-detection.md). Continue is fixed behavior, not a new configurable default or an authorization to implement skip. This is partial supersession, not retirement of this whole document. Its reference-corpus and exact-match identity conflicts with canonical BR-008/BR-009/OC-004 remain unresolved; see the [bundle index](index.md).

## Statement

The system SHALL detect whether a discovered video is a historical duplicate of a previously processed video, supporting both exact matching and configurable fuzzy matching based on title similarity. Duplicate detection operates against a corpus of titles from videos that were routed to at least one playlist — the duplicate-reference corpus — rather than against every video the system has ever inspected.

## Acceptance criteria

1. The system detects exact duplicates by comparing video identifiers
2. The system detects fuzzy duplicates by comparing title similarity against a user-configurable threshold
3. The fuzzy matching threshold is adjustable by the user
4. Duplicate detection operates against the corpus of titles from videos that were routed to at least one playlist — videos that were rejected, excluded, failed, or never evaluated are not included in the duplicate-reference corpus
5. All historically observed titles are retained for audit and inspection purposes, even if they are not eligible as duplicate references
6. Detected duplicates are flagged and continue through otherwise eligible routes/actions under [BR-008 AC9–AC11](br-008-exact-and-fuzzy-duplicate-detection.md), with provenance retained. **Superseded wording:** “Detected duplicates are flagged and handled according to user-configured duplicate behavior.” The new user decision replaces that selectable-handling promise; matching sensitivity remains configurable.

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 8)
- Explicit user decision, 2026-10-25, “Continue, yes”, recorded with its question context in [BR-008](br-008-exact-and-fuzzy-duplicate-detection.md); applies to AC6 only.

## Verified by

- Test: a video with an identical identifier to a previously routed video is detected as an exact duplicate
- Test: a video with a title similar above the threshold to a previously routed video is detected as a fuzzy duplicate
- Test: a video that was rejected or excluded in a prior run does not appear in the duplicate-reference corpus
- Test: adjusting the threshold changes which videos are considered fuzzy duplicates
- Test: duplicate detection operates against historical data from prior runs

## Depends on

- None
