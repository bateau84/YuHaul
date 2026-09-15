---
type: requirement
title: BR-002 — Subscription-Based Discovery Without Live Monitoring
description: System discovers newly published videos from subscribed channels via scheduled polling, not continuous monitoring
tags: [requirement, discovery, subscriptions, polling, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL discover newly published videos from the user's subscribed YouTube channels by polling the YouTube Data API at intervals, without maintaining continuous or real-time monitoring connections.

## Acceptance criteria

1. On each run, the system enumerates the user's subscribed channels and queries for videos published since the last known state for each channel.
2. The system does not maintain persistent WebSocket, Server-Sent Events, or equivalent real-time push connections to YouTube.
3. Newly discovered videos are queued for route evaluation and are not lost if a run is interrupted before evaluation completes.
4. The system handles YouTube API pagination correctly when a channel's publication volume exceeds a single API response.
5. Channel subscription list is refreshed from the YouTube API at a user-configurable interval; stale channel data is not silently used beyond its freshness window.
6. The system tolerates partial channel enumeration failures: channels that fail are skipped in the current run and retried in the next run without losing already-discovered videos from successful channels.

## Derived from

- Anchor: youtube-discovery-routing.md (AC2)

## Verified by

- Unit test: run discovers videos published after last-known state; videos before last-known state are not re-discovered.
- Integration test: no persistent connections remain open after run completes.
- Edge test: partial channel failure does not prevent processing of successful channels.

## Depends on

- None
