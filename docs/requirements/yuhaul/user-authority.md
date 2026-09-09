---
type: requirement
title: User Authority
description: The system must provide evidence-backed advice but not autonomously subscribe, unsubscribe, or make material policy changes.
tags: [requirement, authority, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL provide evidence-backed advice and recommendations to the user, but SHALL NOT autonomously subscribe to channels, unsubscribe from channels, or make material policy changes without explicit user action.

## Acceptance criteria

1. The system can present recommendations about channels or routes based on evidence
2. The system does not execute subscription to any YouTube channel without the user explicitly performing that action
3. The system does not execute unsubscription from any YouTube channel without the user explicitly performing that action
4. The system does not autonomously modify routing rules, thresholds, or exclusion lists
5. Material policy changes require explicit user confirmation
6. All automated actions are limited to playlist mutations explicitly authorized by route actions

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 31)

## Verified by

- Test: the system presents channel or route recommendations but does not act on them automatically
- Test: no YouTube API subscription calls are made by the system without explicit user action
- Test: no YouTube API unsubscription calls are made by the system without explicit user action
- Test: routing rules are not modified without explicit user action
- Test: the system's automated actions are limited to playlist mutations defined in route actions

## Depends on

- decision-evidence.md
- route-conditions-actions.md
