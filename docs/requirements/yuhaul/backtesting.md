---
type: requirement
title: Historical Backtesting
description: Backtesting must compare routing changes against retained historical observations without altering external state or contaminating production state.
tags: [requirement, dry-run, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL support historical backtesting in which proposed routing configuration changes are evaluated against retained historical observations — a replay of past observations under a different configuration — without mutating external state, without marking videos as processed, without altering the duplicate corpus, and without incrementing production statistics. Backtesting is distinct from dry-run: dry-run processes the current pending workload, while backtesting replays historical observations.

## Acceptance criteria

1. The user can apply a proposed routing configuration to retained historical video observations
2. Backtesting uses retained video data and titles, not live API calls
3. Backtesting produces comparison results showing what would have changed under the proposed configuration compared to the actual historical outcomes
4. Backtesting does not mutate YouTube playlists, subscription lists, or any external state
5. Backtesting does not mark videos as processed in the production processing state
6. Backtesting does not add titles to the production duplicate reference corpus
7. Backtesting does not increment production run/route statistics
8. Backtesting results are available for the user to compare against actual historical outcomes
9. Backtesting is explicitly distinguished from dry-run in the UI and results — dry-run evaluates current pending workload, backtest replays historical observations

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 27)

## Verified by

- Test: backtesting produces results without making YouTube API playlist mutations
- Test: backtesting results show the difference between the proposed configuration and the actual historical outcomes
- Test: backtesting operates on retained historical data, not live API data
- Test: after backtesting, production processing state, duplicate corpus, and statistics are unchanged
- Test: the UI distinguishes backtesting results from dry-run results

## Depends on

- dry-run.md
- historical-title-persistence.md
- configuration-history.md
