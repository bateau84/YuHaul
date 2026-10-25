---
type: requirement
title: BR-023 — Historical Statistics Over Time
description: Statistics viewable over time, not only lifetime counters
tags: [requirement, statistics, historical, temporal, priority:MUST]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL present statistics historically over time, allowing the user to observe trends, compare periods, and understand how metrics change rather than viewing only cumulative lifetime counters.

## Acceptance criteria

1. All key statistics (run outcomes, route acceptance rates, channel usefulness, quota consumption) are stored with timestamps and are queryable by time range.
2. The user can view statistics for configurable periods: daily, weekly, monthly, or custom date ranges.
3. Historical statistics are not overwritten or lost when new data arrives.
4. The user can compare statistics across two or more time periods.
5. Historical statistics survive application restarts.
6. Statistics presentation distinguishes between current-period and lifetime aggregates.
7. App-owned operational statistics have no automatic age-based expiration or rolling retention cap. Their historical periods remain available and their lifetime totals continue to include earlier activity when later runs occur, quota windows change, or the application restarts.
8. The scope of AC7 is YuHaul's own activity: discovery/evaluation/outcome counts, route match/non-match counts, executed/skipped action counts, run timing, and API-call/quota-consumption accounting, including channel breakdowns of that activity. It is not permission to retain provider-origin metadata, playlist-membership observations, provider statistics, or metrics derived from those data indefinitely. The boundary and unresolved cases below apply.
9. Accumulation preserves existing counting semantics. Repeated matches of the same video do not inflate the distinct-video Route List count (BR-020 AC7); a completed historical outcome is not rewritten by a later outcome for that video (OC-002). Rates are recomputed for their stated period and may decrease; current quota-window consumption resets with that window without erasing historical consumption. “Forever incrementing” does not require rates, remaining-quota estimates, or every counter to increase on every run.

## Retention meaning and boundary

For example, 10 recorded action executions followed by 2 later executions remain 12 in the lifetime execution count after a restart, even when the first run is older than 36 months. The earlier period remains inspectable as statistics. This is a product retention rule, not an infinite-capacity, backup, or data-loss guarantee, and does not require a particular storage model or indefinite retention of raw evidence.

| Accepted metric or data | Effect of the user's preference |
|---|---|
| BR-020 run/route activity, BR-018 consumption history, and BR-021 channel discovery/acceptance/action activity | No product-imposed age cap on app-owned operational tallies and their temporal history. Recording an action's success is not a claim about current playlist membership. |
| BR-020 acceptance rates and BR-021 operational yield | Their eligible historical counts remain cumulative; the ratio itself is not monotonic. |
| BR-021 AC1 “videos published” | Does not become an app-owned execution count merely because it is numeric. Indefinite provider-origin publication statistics are not authorized by this amendment; the accepted metric is not removed or silently capped. |
| BR-022 retention/value signals, channel retained counts/usefulness based on playlist membership, provider watch/engagement data | Unchanged conditional v1 obligations; indefinite retention and any derived-data authorization remain unresolved. Neither raw membership nor a derived retained/usefulness metric is made exempt by calling it “my analytics.” |
| BR-010 discovery/relevance credit based on duplicate/title evidence | Attribution remains as accepted. This amendment does not resolve indefinite retention/use of title-derived evidence, identifiers, or derived credit metrics. |

API metadata refresh/expiry is not itself an instruction to roll off app-owned operational counts. Conversely, retaining a count does not authorize keeping its provider-origin supporting data indefinitely. Any applicable external-policy conflict must be surfaced for reconciliation, not resolved by relabeling provider data as app-owned, deleting an accepted metric, or silently imposing a cap.

## Provenance of this amendment

- **Sourced:** On 2026-10-25 the user stated “diskspace is cheap”, “retention for my own analytics can be forever”, and “for now analytics are forever incrementing”. AC7–AC8 record that preference for app-owned operational analytics, not playlist membership or raw titles.
- **Necessary consequence:** Old activity must not age out of a lifetime total or its accepted historical-period views merely because new activity arrives. Restart durability was already required.
- **Specification detail:** AC9 distinguishes additive counts from ratios and current quota windows, preserving existing metric definitions rather than promising numerical monotonicity for every displayed metric.
- **Unresolved external authority:** The [proposed compliance analysis](../../architecture/yuhaul-youtube-compliance.md) describes refresh/deletion and an approval-dependent derived-metrics exception of up to 36 months, while also calling app-owned statistics permanent. Those are proposed policy interpretations, not a verified exemption or an accepted product expiration rule. Official-policy applicability was not reverified for this amendment; the requesting session reports a recent official-fetch failure. No compliance conclusion is claimed.

## Derived from

- [Anchor](../../anchors/youtube-discovery-routing.md) (AC23)
- User's explicit 2026-10-25 retention preference quoted in “Provenance of this amendment” above (AC7–AC9).

## Verified by

- Unit test: statistics query for a specific date range returns correct data.
- Unit test: period comparison produces correct deltas.
- Persistence test: historical statistics survive restart.
- Record app-owned operational counts in an earlier period, advance beyond 30 days and 36 months, record later activity, and restart: lifetime totals include both periods and each period remains queryable with its original counts. Quota-window rollover changes the current window, not the historical sum.
- Repeat a match for one video/route and add a later non-match: the distinct-video count is not inflated and the acceptance rate may fall without erasing earlier counts. Compare earlier run outcomes before and after a later run for the same video; historical outcomes remain unchanged.

## Depends on

- [BR-020](br-020-run-level-and-route-level-statistics.md) (run/route statistics provide underlying data)
- [BR-025](br-025-configuration-history-for-statistics-interpretation.md) (configuration history provides context for interpretation)
- [BR-018](br-018-api-consumption-accounting-and-visibility.md) (API-call/quota consumption history)
- [OC-002](oc-002-route-evaluation-outcome-semantics.md) (unchanged outcome meanings)

## Open questions

- **Compliance/provider authority, then product owner:** Establish current applicable policy and any required approval for provider publication statistics, playlist-derived retained/usefulness metrics, and title-derived credit metrics. If an accepted metric cannot be retained indefinitely under that authority, the user must choose the permitted bounded-history behavior or a separately authorized analytics scope. This amendment supplies neither approval nor permission to discard/cap the metric. This blocks extending the no-expiration promise to those metrics, not recording the app-owned operational retention preference.
