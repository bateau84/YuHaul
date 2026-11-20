---
type: requirement
title: Subscription Discovery
description: The system must discover newly published videos from the user's subscribed YouTube channels without continuous monitoring.
tags: [requirement, discovery, MUST]
timestamp: 2026-11-18T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL discover newly published videos from channels the user is subscribed to on YouTube, operating through scheduled or on-demand polling rather than continuous live monitoring.

## Acceptance criteria
1. The system retrieves the list of channels the user is subscribed to via the YouTube API
2. The system identifies videos published by those channels since the last discovery run
3. Newly discovered videos are made available for downstream processing (routing, duplicate detection)
4. The system does not continuously poll or maintain persistent connections to YouTube during idle periods

For a channel whose uploads scan failed, “since the last discovery run” does not mean treating that run as successful coverage. [BR-002 AC6–9](br-002-subscription-discovery-without-live-monitoring.md) governs the next eligible poll, coverage, and when saved observations from successful pages can be routed.

For restart after saving a page when the only source is confirmed ineligible, see [BR-002 AC10](br-002-subscription-discovery-without-live-monitoring.md). In that case, no further uploads poll is made for the ineligible source, but the saved upload observations are not cancelled.

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 2)

## Verified by

- Test: after a new video is published by a subscribed channel, the system discovers it within the next scheduled or manual run
- Test: no persistent YouTube API connections exist between runs

## Depends on

- account-connection.md
