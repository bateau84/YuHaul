---
type: requirement
title: Dry-Run Evaluation
description: The user must be able to evaluate routing on the current pending workload without mutating external playlists or contaminating production state.
tags: [requirement, dry-run, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL support dry-run evaluation in which routing rules are applied to the current pending workload (newly discovered videos not yet processed) without executing any external playlist mutations, without marking videos as processed, without altering the duplicate corpus, without incrementing production routing statistics, and without satisfying idempotency records. Dry-run does not operate on previously rejected or previously processed videos.

## Acceptance criteria

1. The user can initiate a dry-run evaluation through the administration interface
2. During a dry-run, the system evaluates only the current pending workload against all routing rules and reports outcomes
3. During a dry-run, no videos are added to or removed from YouTube playlists
4. Dry-run results include which routes matched, which actions would have executed, and the overall outcome for each video
5. Dry-run results are available for inspection after completion
6. Dry-run processing does not mark videos as processed in the production processing state
7. Dry-run processing does not add titles to the production duplicate reference corpus
8. Dry-run processing does not increment production run/route statistics
9. Dry-run processing does not satisfy production idempotency records
10. Dry-run hypothetical results may be persisted but must remain distinctly identifiable as hypothetical, never mixed with production results
11. Dry-run does not reprocess previously rejected or previously processed videos

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 26)

## Verified by

- Test: a dry-run produces routing outcomes for the current pending workload without any YouTube API playlist mutations
- Test: a dry-run does not reprocess videos that were already processed or rejected in prior runs
- Test: after a dry-run, no videos are marked as processed in production state
- Test: after a dry-run, the production duplicate corpus is unchanged
- Test: after a dry-run, production run/route statistics are unchanged
- Test: dry-run results are persisted and identifiable as hypothetical

## Depends on

- route-definition.md
- route-conditions-actions.md
