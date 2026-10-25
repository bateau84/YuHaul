---
type: requirement
title: BR-007 — Exclusion Behaviors
description: Exclusion filters for channels, videos, titles, ignored words/phrases, and duration limits
tags: [requirement, exclusion, filtering, channels, titles, duration, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL support route-scoped exclusion rules that prevent an excluded video from matching that route before its conditions are evaluated. Excluding a video from one route shall not prevent another route without that exclusion from evaluating it.

## Acceptance criteria

1. The user can exclude specific channels from an individual route; matching videos are not evaluated against that route's conditions and remain eligible for other routes without that exclusion.
2. The user can exclude specific videos (by identifier) from an individual route; those videos are not evaluated against that route's conditions and remain eligible for other routes without that exclusion.
3. The user can exclude videos matching title patterns from an individual route, including exact title matches and configurable substring/word/phrase exclusions.
4. The user can define a list of ignored words or phrases for an individual route; videos whose titles contain any ignored item are excluded from that route.
5. The user can set duration limits (minimum and/or maximum) for an individual route so that videos outside the allowed duration range are excluded from that route.
6. Exclusion configuration is preserved across runs and restarts.
7. Each route's exclusions are evaluated before that route's conditions. An exclusion on one route does not skip evaluation by other routes that do not exclude the video.
8. The user can inspect which exclusion rule caused a video to be excluded in the evaluation history.
9. A channel, title, or ignored-word/phrase exclusion entry must have a non-empty value. A video exclusion must identify a specific video with a non-empty identifier. An invalid entry is not added to the route and the user receives an inline validation error.
10. Duration exclusion limits are optional. Each supplied limit must be numeric and at least zero; when both minimum and maximum are supplied, the minimum must not exceed the maximum. An empty side means no limit on that side, and two empty sides mean no duration exclusion. Invalid duration values are not accepted and are identified with an inline validation error.
11. A route must not save an exclusion category outside the supported channel, video, title, word/phrase, and duration categories.

## Derived from

- [Anchor](../../anchors/youtube-discovery-routing.md) (AC7)
- [Admin Interface Design Specification](../../design/yuhaul-admin-ui-design.md), §Route Editor — Exclusion semantics, Exclusion list entry states, Exclusion list interaction, and Duration limit interaction

## Verified by

- Unit test: video from excluded channel is not evaluated.
- Unit test: a video excluded from one route remains eligible for a second route without that exclusion.
- Unit test: video with excluded title pattern is not evaluated.
- Unit test: video outside duration limits is not evaluated.
- Unit test: exclusion event recorded in evaluation history.
- Validation test: empty channel/title/word-or-phrase values and empty video identifiers are rejected when added; invalid duration values (non-numeric, negative, or minimum greater than maximum) are rejected, while empty duration bounds mean no limit.
- Validation test: an unsupported exclusion category is not accepted as saved route configuration.
- Persistence test: exclusion configuration survives restart.

## Depends on

- BR-002 (discovery provides candidate videos)
