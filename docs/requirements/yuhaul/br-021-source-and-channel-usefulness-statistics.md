---
type: requirement
title: BR-021 — Source and Channel Usefulness Statistics
description: Source and channel contribution to value measured
tags: [requirement, statistics, channels, sources, usefulness, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL maintain and present statistics measuring how useful each source channel and video source is at producing content that the user values (routes accepted, playlist additions, retention signals).

## Acceptance criteria

1. Each channel has accumulated statistics: videos published, videos discovered, videos accepted by routes, videos added to playlists, acceptance rate.
2. Channel statistics include discovery credit attribution (first-discovered vs duplicate).
3. The user can rank or sort channels by usefulness metrics.
4. Channel statistics are available over configurable time periods, not only lifetime.
5. Channel statistics are durable and survive restarts.
6. The user can view channel statistics through the administration interface.

## Derived from

- Anchor: youtube-discovery-routing.md (AC21)

## Verified by

- Unit test: channel statistics include publish count, discovery count, acceptance count.
- Unit test: channel ranking by acceptance rate works correctly.
- Persistence test: channel statistics survive restart.

## Depends on

- BR-010 (duplicate provenance provides discovery credit data)
- BR-020 (run/route statistics provide underlying data)
