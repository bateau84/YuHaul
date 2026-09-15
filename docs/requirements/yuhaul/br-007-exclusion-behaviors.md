---
type: requirement
title: BR-007 — Exclusion Behaviors
description: Exclusion filters for channels, videos, titles, ignored words/phrases, and duration limits
tags: [requirement, exclusion, filtering, channels, titles, duration, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL support configurable exclusion behaviors that prevent matching or processing of videos, channels, titles, words/phrases, and duration ranges before route evaluation.

## Acceptance criteria

1. The user can exclude specific channels so that videos from those channels are not discovered or evaluated.
2. The user can exclude specific videos (by identifier) so that they are not evaluated or processed.
3. The user can exclude videos matching title patterns, including exact title matches and configurable substring/word/phrase exclusions.
4. The user can define a list of ignored words or phrases; videos whose titles contain any ignored item are excluded.
5. The user can set duration limits (minimum and/or maximum) so that videos outside the allowed duration range are excluded.
6. Exclusion configuration is preserved across runs and restarts.
7. Exclusions are evaluated before route conditions; excluded videos do not consume route evaluation resources.
8. The user can inspect which exclusion rule caused a video to be excluded in the evaluation history.

## Derived from

- Anchor: youtube-discovery-routing.md (AC7)

## Verified by

- Unit test: video from excluded channel is not evaluated.
- Unit test: video with excluded title pattern is not evaluated.
- Unit test: video outside duration limits is not evaluated.
- Unit test: exclusion event recorded in evaluation history.
- Persistence test: exclusion configuration survives restart.

## Depends on

- BR-002 (discovery provides candidate videos)
