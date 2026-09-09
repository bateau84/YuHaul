---
type: requirement
title: Retention-Signal Configuration and Provenance
description: The user must designate which playlists serve as retention signals, and the system must distinguish YuHaul-routed membership from user-driven membership.
tags: [requirement, statistics, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL allow the user to designate specific playlists as retention/rotation playlists whose membership serves as a retention signal. The system SHALL distinguish membership caused by YuHaul's own routing action from membership caused by external or user-driven action. Membership caused by YuHaul itself SHALL NOT count as a retention signal. Retention signals from designated playlists are the primary input for usefulness metrics.

## Acceptance criteria

1. The user can designate any playlist as a retention/rotation playlist through the administration interface
2. For every video in a designated retention playlist, the system records whether the membership was caused by YuHaul routing the video to that playlist or by an external/user-driven action
3. A video routed by YuHaul to a designated retention playlist does NOT generate a retention signal from that playlist
4. A video added to a designated retention playlist by the user or an external process DOES generate a retention signal
5. Retention signals from designated playlists are the primary input for usefulness metrics in source/channel statistics
6. The designation of playlists as retention/rotation playlists is persisted across runs
7. The provenance (YuHaul-routed vs. user-driven) is retained alongside the retention signal for audit and inspection

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 22)

## Verified by

- Test: the user can designate a playlist as a retention playlist through the admin interface
- Test: a video routed by YuHaul to a designated retention playlist does not generate a retention signal
- Test: a video manually added to a designated retention playlist by the user generates a retention signal
- Test: the provenance of each retention signal (YuHaul-routed vs. user-driven) is recorded and visible
- Test: usefulness metrics in channel statistics use only genuine retention signals

## Depends on

- route-definition.md
- account-connection.md
