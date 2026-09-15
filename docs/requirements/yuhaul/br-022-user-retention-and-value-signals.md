---
type: requirement
title: BR-022 — User Retention and Value Signals
description: User retention signals measured where reliable provider evidence permits
tags: [requirement, statistics, retention, signals, value, priority:SHOULD]
---

**Status:** proposed
**Priority:** SHOULD

## Statement

The system SHOULD measure user-retention and value signals where reliable evidence from the YouTube provider permits, to assess whether discovered content is ultimately valued by the user.

## Acceptance criteria

1. The system measures playlist retention (videos added to playlists that remain in playlists after a configurable period) where YouTube API provides the necessary data.
2. The system can measure whether videos added to playlists are subsequently watched or engaged with, if the YouTube API exposes reliable watch/engagement data.
3. Retention signal measurement is optional and degrades gracefully when provider data is unavailable.
4. Retention signals are associated with the route and channel that discovered the content.
5. The user can view retention metrics through the administration interface.
6. Retention data is durable and survives restarts.
7. The system does not fabricate retention signals when provider data is unreliable or unavailable.

## Derived from

- Anchor: youtube-discovery-routing.md (AC22)

## Verified by

- Unit test: retention signal recorded when provider data available.
- Unit test: no retention signal recorded when provider data unavailable; no fabrication.
- Persistence test: retention data survives restart.

## Depends on

- BR-010 (discovery provenance links retention to discovery route)
- BR-021 (channel statistics provide context)
