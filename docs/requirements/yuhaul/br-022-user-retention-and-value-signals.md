---
type: requirement
title: BR-022 — User Retention and Value Signals
description: User retention signals are required when reliable provider evidence permits measurement
tags: [requirement, statistics, retention, signals, value, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL measure user-retention and value signals when reliable evidence from the YouTube provider permits measurement, to assess whether discovered content is ultimately valued by the user. This obligation applies only to signals supported by reliable provider evidence; it does not imply that YouTube exposes any particular signal.

## Acceptance criteria

1. When YouTube API provides reliable data sufficient to determine playlist retention (videos added to playlists that remain in playlists after a configurable period), the system measures that retention.
2. When YouTube API exposes reliable watch or engagement data for videos added to playlists, the system measures whether those videos are subsequently watched or engaged with. This criterion does not assume the API exposes such data.
3. Unavailability or unreliability of one signal excludes only that signal: the system still measures other retention/value signals for which reliable provider evidence is available, and degrades gracefully for the unavailable signal.
4. Retention signals are associated with the route and channel that discovered the content.
5. The user can view retention metrics through the administration interface.
6. Retention data is durable and survives restarts.
7. The system does not fabricate retention signals when provider data is unreliable or unavailable.

## Derived from

- Anchor: youtube-discovery-routing.md (AC22)

## Verified by

- With reliable playlist-retention evidence for a video, the corresponding retention metric is recorded and associated with the route and channel that discovered the video.
- With reliable watch/engagement evidence, the corresponding signal is recorded; when that evidence is absent or unreliable, no watch/engagement signal is shown or inferred.
- With reliable evidence for one signal and unavailable or unreliable evidence for another, the supported signal is still measured and the unsupported signal is not represented as a zero or negative value.
- A recorded retention signal remains available after an application restart.

## Depends on

- BR-010 (discovery provenance links retention to discovery route)
- BR-021 (channel statistics provide context)

## Open questions

- Anchor AC22 does not specify which YouTube-provider signals or what evidence threshold qualifies as reliable. This affects which signal types trigger this conditional MUST, not whether the obligation is mandatory when reliable evidence is available. No particular YouTube API capability is presumed.
