---
type: requirement
title: Exclusion Behaviors
description: The system must support route-scoped exclusion rules for channels, videos, titles, words/phrases, and duration limits.
tags: [requirement, routes, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL support configurable exclusion rules scoped to individual routes for channels, videos, titles, words or phrases, and duration limits. A video excluded from one route remains eligible for evaluation by other routes.

## Acceptance criteria

1. The user can define channel-based exclusion rules per route
2. The user can define video-identity exclusion rules per route
3. The user can define title-content exclusion rules based on specified words or phrases, per route
4. The user can define exact-title exclusion rules where a video whose title matches an excluded entry exactly is excluded — exact match is defined as character-for-character equality after trimming leading and trailing whitespace and normalizing Unicode to NFC form — per route
5. The user can set minimum and/or maximum duration limits that exclude videos outside the range, per route
6. A video excluded from a specific route is not evaluated by that route but remains eligible for evaluation by other routes
7. Exclusion rules are persisted across runs

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 7)

## Verified by

- Test: a video from a channel excluded from Route A is not matched by Route A but is matched by Route B (which does not exclude that channel)
- Test: a video whose title exactly matches an excluded entry (after whitespace trim and Unicode NFC normalization) is excluded from that route
- Test: exclusion rules survive application restart

## Depends on

- route-definition.md
