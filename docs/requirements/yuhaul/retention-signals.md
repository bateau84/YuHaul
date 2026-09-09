---
type: requirement
title: Retention/Value Signals
description: User-retention and value signals must be measured, distinguishing YuHaul-routed playlist membership from user-driven retention.
tags: [requirement, statistics, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL measure user-retention and value signals for videos that were accepted by routes. The primary automatic retention signal is whether a video subsequently appears in the user's designated retention/rotation playlists — personal playlists that the user has specifically designated as retention signals, independent of YuHaul's own routing actions. Membership caused by YuHaul itself (i.e., the system routing a video to a playlist) does NOT count as a retention signal. Where the content provider supplies reliable retention or engagement data, the system MAY capture supplementary signals.

## Acceptance criteria

1. The user can designate specific playlists as retention/rotation playlists (the canonical source of retention signals)
2. A video's presence in a designated retention playlist is recorded as a retention signal only when the membership was not caused by YuHaul's own routing action
3. The system distinguishes YuHaul-routed membership from external/user-driven membership for every playlist-video pair
4. Where the content provider supplies reliable retention or engagement data (such as watch time or completion), the system captures it as supplementary signal
5. Retention signals are associated with the specific video and route that matched
6. Retention signal data is retained across runs for historical analysis
7. The system degrades gracefully when supplementary provider retention data is unavailable, not producing false signals

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 22)

## Verified by

- Test: a video routed by YuHaul to Playlist X does not count Playlist X membership as a retention signal for that video
- Test: a video manually added by the user to a designated retention playlist IS recorded as a retention signal
- Test: when supplementary provider retention data is available, it is captured and associated with the video
- Test: when supplementary retention data is unavailable, no false or placeholder signals are recorded
- Test: captured retention data persists for historical analysis

## Depends on

- retention-signal-configuration.md
- run-route-statistics.md
- account-connection.md
- route-definition.md
