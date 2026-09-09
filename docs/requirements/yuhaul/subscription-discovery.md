---
type: requirement
title: Subscription Discovery
description: The system must discover newly published videos from the user's subscribed YouTube channels without continuous monitoring.
tags: [requirement, discovery, MUST]
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

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 2)

## Verified by

- Test: after a new video is published by a subscribed channel, the system discovers it within the next scheduled or manual run
- Test: no persistent YouTube API connections exist between runs

## Depends on

- account-connection.md
