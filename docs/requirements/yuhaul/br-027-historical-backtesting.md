---
type: requirement
title: BR-027 — Historical Backtesting
description: Backtesting compares routing changes against retained observations without altering external state
tags: [requirement, backtesting, historical, comparison, priority:MUST]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

**First-use scope:** AC5 remains an accepted future requirement but is explicitly deferred from first-use launch. Only the same-app production-versus-simulation comparison is deferred; simulation isolation remains mandatory, and launch-critical subscription-to-playlist obligations are unchanged.

## Statement

The system SHALL support historical backtesting in which proposed routing changes are evaluated against retained historical observations (previously discovered videos and their metadata) to compare outcomes without altering any external YouTube state.

## Acceptance criteria

1. The user can select a set of historical observations (previously discovered videos) as the backtesting corpus.
2. The user can apply a proposed route configuration (or snapshot of routes at a point in time) to the backtesting corpus.
3. The backtesting run produces outcomes for each video in the corpus as if the proposed routes had been active.
4. No external YouTube API calls are executed during backtesting.
5. Backtesting results can be compared with actual historical production outcomes from the same YuHaul application to assess the impact of routing changes. This accepted future requirement is deferred from first-use launch; comparison with the former script or a broken version is not required.
6. Backtesting results are recorded and distinguishable from actual runs.
7. Backtesting does not affect idempotency state or operational statistics.

### Date range: filter by YouTube published date

The Backtest date controls narrow the corpus to videos published on YouTube within the chosen days. For example, the range 2026-10-01 to 2026-10-31 selects a video published at 2026-10-31T23:59:59Z and leaves out one published at 2026-11-01T00:00:00Z. Days are UTC days, both chosen days are included, and with no range nothing is filtered. These rules apply to the date controls of the Backtest view in each of its modes, including Replay.

8. The date range SHALL filter the retained videos by each video's YouTube published date. Days are UTC calendar days. A start day S selects videos published at or after S 00:00:00Z. An end day E selects videos published before 00:00:00Z on the day after E. Both S and E are included. S equal to E selects that single day.
9. When neither a start nor an end day is chosen, the date range SHALL filter nothing. Every otherwise selected video is included, including videos without a published date. When only one day is chosen, only that bound applies: a start day alone has no upper limit, and an end day alone has no lower limit.
10. When at least one day is chosen, a video with no published date available locally SHALL NOT be selected, because it cannot be shown to fall in the range. It is not evaluated, has no decision and is not reported as rejected, excluded or failed. The published date comes only from locally available information: current/local data under [BR-015 AC7](br-015-explainable-route-decisions.md). The backtest SHALL NOT call YouTube to obtain a missing date (AC4). It SHALL NOT substitute the discovery time, the import time or any other date.
11. If the start day is after the end day, or a chosen day is not a valid calendar date, the backtest SHALL NOT start. No run is recorded, no configuration snapshot is captured and nothing is evaluated. The user SHALL see that the range is invalid; for a reversed range, that the start day must not be after the end day.
12. The run's selected-input count SHALL be the number of distinct videos selected after the date filter and the existing corpus selection. Its evaluated and decision counts SHALL cover only selected videos. A video outside the range, or left out under AC10, SHALL NOT contribute to any count, outcome or outcome filter of that run. A valid range that selects no videos SHALL complete with a selected count of zero; it is not an error.
13. The applied range SHALL be saved with the run when the run starts: the start day and the end day, each either a date or “none”. The saved range SHALL survive restart, SHALL NOT change afterward and SHALL be shown when the user inspects the run, so that its results are attributable to the range used. A run without a range shows that no date filter was applied. Later edits to the date controls do not change any recorded run.

## Derived from

- Anchor: youtube-discovery-routing.md (AC27)
- AC8–13: the user's answer of 2026-11-10, relayed in the specification assignment (Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`): the purpose of the Backtest date pickers is to filter videos within a time frame, and they filter by YouTube **published** date within the selected range. The assigning session proposed UTC days, inclusion of both start and end days, and no filtering without a range; it reports that the user did not object, and the assignment records these defaults as accepted. The original human conversation was not independently inspected.
- Specification choices in AC8–13: a single chosen bound applies alone; a video without a local published date is left out of a ranged backtest rather than included or failed; an invalid calendar date is rejected like a reversed range; and a range that selects nothing completes with zero. Applying the rules to Replay mode follows from the accepted design, which places Replay inside the dual-mode Backtest view with the same date-range selection (US-012); see the index's open question.
- User's 2026-10-25 initial-use decision, supplied for this amendment: retain AC5 as an accepted future same-app production-versus-simulation comparison, defer it from first-use launch, and require no comparison with the former script or a broken version. This does not defer simulation isolation or change subscription-to-playlist obligations.

## Verified by

- Unit test: backtesting evaluates retained corpus with proposed routes.
- Integration test: no external API calls during backtesting.
- Future AC5 comparison test: backtesting results can be compared with the same application's historical production outcomes. Missing comparison does not fail first-use launch acceptance; comparison with the former script or a broken version is not an acceptance criterion.
- Isolation test: backtesting does not affect idempotency or operational statistics.
- Date boundaries (AC8): with real retained records published at S−1 23:59:59Z, S 00:00:00Z, E 23:59:59Z and E+1 00:00:00Z, the backtest selects exactly the middle two. A browser in a non-UTC time zone selects the same set. S = E selects one UTC day.
- No range and one bound (AC9): with no range every retained video is selected, including one with no published date. A start-only or end-only range applies only that bound.
- Missing date (AC10): with a range set, a video with no local published date has no decision, appears in no outcome category or count, and the controlled provider records zero calls.
- Invalid range (AC11): a reversed range and an invalid date each produce the invalid-range message and leave the run list, snapshots and decisions unchanged.
- Counts (AC12): the selected and evaluated counts equal the number of in-range distinct videos; a range with no matches completes with selected count 0.
- Saved range (AC13): after the run starts, change the date controls and restart the server. Run inspection still shows the original start and end days, or “none”, and the run's outcomes are unchanged.

## Depends on

- BR-008–BR-010 (duplicate detection for retained corpus)
- BR-015 (decision evidence for backtesting outcomes)
- BR-025 (configuration snapshots for historical route configurations)
