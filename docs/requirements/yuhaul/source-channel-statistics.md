---
type: requirement
title: Source/Channel Statistics
description: Statistics about channel yield and usefulness must be available, measuring both route-matching yield and downstream retention in personal playlists.
tags: [requirement, statistics, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL provide per-channel statistics that measure both yield (how often channel content is discovered, accepted, or rejected by routes) and usefulness (how often channel content subsequently appears in the user's designated personal retention playlists).

## Acceptance criteria

1. Each channel has statistics on how many videos were discovered from it
2. Each channel has statistics on how many of its videos were accepted by routes
3. Each channel has statistics on how many of its videos were rejected or excluded, with counts broken down by rejection reason
4. Each channel has statistics on how many of its videos were retained in the user's designated personal playlists
5. Each channel has a usefulness measure: the count and proportion of discovered videos that subsequently appear in personal playlists
6. Channel statistics are retained across runs for historical comparison and trending
7. Channel statistics are accessible through the administration interface

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 21)

## Verified by

- Test: after processing, each channel has statistics reflecting its discovered, accepted, rejected, and retained video counts
- Test: each channel has a usefulness measure showing how many discovered videos made it into personal playlists
- Test: channel statistics from prior runs are available for comparison
- Test: channel statistics are viewable in the administration interface

## Depends on

- retention-signals.md
- processing-state-persistence.md
- route-definition.md
