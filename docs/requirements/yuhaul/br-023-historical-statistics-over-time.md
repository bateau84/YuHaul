---
type: requirement
title: BR-023 — Historical Statistics Over Time
description: Statistics viewable over time, not only lifetime counters; which runs the Analytics period statistics count
tags: [requirement, statistics, historical, temporal, priority:MUST]
timestamp: 2026-11-21T00:00:00Z
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

10. **Which runs count (user decision, 2026-11-21).** The Analytics period statistics (run outcome trends, discovered, acceptance rate and route match rates) count only production runs, that is manual and scheduled runs. Dry runs, replays and backtests never count.
11. Among production runs, only runs with status completed, failed or stopped count. Each such run contributes its recorded accepted, rejected, deferred and failed numbers, and the other per-run outcome counts shown with them. A running or interrupted run counts toward nothing until it reaches one of those statuses; it then counts in the same way.
12. A run counts toward "discovered" only if its discovery finished, as defined in "When discovery finished" below. A run that is counted under AC11 but whose discovery did not finish still contributes its outcome counts; its discovered number is left out.
13. The acceptance rate for a period is the accepted count divided by the discovered count, both taken only from the period's runs whose discovery finished. When the period has no such run, or their discovered total is 0, the rate is shown as having no data, not as 0%.
14. Route match rates count the route decisions recorded by the runs that AC10 and AC11 include. A route decision from any other run never counts.

## When discovery finished

Discovery finished for a run when either:

1. YuHaul saved a channel-scan outcome for the run, and it says the scan completed: every channel in scope was visited and each was scanned or failed on its own, with no budget, quota or run-wide stop. A saved outcome that says the scan was cut short, or that no channel could be scanned, means discovery did not finish. A saved outcome always takes precedence over rule 2.
2. No channel-scan outcome is saved for the run (every run from before this evidence existed), and the last processing step the run recorded comes after discovery: duplicate detection, exclusion, routing or action execution. A run with no saved outcome whose last recorded step is discovery, or that recorded no step, is treated as not finished.

For example, a scheduled run that saved "scan completed" and then failed during routing counts its discovered videos and its outcome counts. A run that failed during discovery counts its outcome counts but not its discovered videos. An older run with no saved outcome that reached routing counts its discovered videos.

Rule 2 is weaker evidence than rule 1. Reaching a later step shows that the discovery step ended, not that every channel was scanned: an older run whose scan stopped early on budget or quota and then continued to routing is counted as finished. No saved evidence distinguishes those older runs.

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

## Provenance of the 2026-11-21 run-inclusion decision (AC10–AC14)

- **Sourced:** On 2026-11-21 the user decided which runs Analytics counts, relayed in the specification assignment; the original human conversation was not independently inspected. Production runs only (manual and scheduled); completed, failed and stopped runs count toward their recorded accepted, rejected, deferred and failed numbers; running and interrupted runs wait until terminal; only runs whose discovery finished count toward "discovered" and the acceptance rate; route match rates follow the same inclusion rule. The user's reason: failed runs re-discover the same videos, which inflates the total. For example, 27 failed scheduled runs recorded 62,866 discovered and 0 accepted.
- **Necessary consequence:** the acceptance rate's accepted count and discovered count come from the same runs (AC13); otherwise a period could show a rate above 100%.
- **Specification choices:**
  - AC11 applies the status rule to the other per-run outcome counts shown with accepted, rejected, deferred and failed (for example not yet evaluated and unavailable on YouTube), so one trend never mixes two inclusion rules.
  - AC13 shows "no data" rather than 0% when no run's discovery finished, because BR-032 forbids presenting absent processing as a negative result.
  - AC14 reads "the same inclusion rule" as the run-type and status rule of AC10–AC11. Route match rates do not also require finished discovery: a route decision exists only for a video that was routed, so repeated discovery does not inflate it.
  - "When discovery finished" treats a saved "no channel could be scanned" outcome as not finished. Such a run is always failed and scanned nothing successfully.
- **Existing evidence used:** rule 1 is the run's saved channel-scan outcome added by migration `030` ([discovery source isolation ARS-002 §4.5](../../architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md#45-per-run-channel-scan-outcome-added-2026-11-20-review-833a06fb-major-1)); its stored values are `completed`, `cut_short`, `no_source_scanned`, or none for runs from before `030`, simulations, and runs that never reached the scan. Rule 2 is the run's recorded current phase, which the pipeline sets to `discovery`, `duplicate_detection`, `exclusion`, `routing` and `action_execution` in that order and does not clear at the end. These are existing facts referenced as evidence; this requirement does not prescribe a field, query or schema.

## Derived from

- [Anchor](../../anchors/youtube-discovery-routing.md) (AC23)
- User's explicit 2026-10-25 retention preference quoted in “Provenance of this amendment” above (AC7–AC9).
- User's 2026-11-21 decision on which runs Analytics counts, quoted in “Provenance of the 2026-11-21 run-inclusion decision” above (AC10–AC14).

## Verified by

- Unit test: statistics query for a specific date range returns correct data.
- Unit test: period comparison produces correct deltas.
- Persistence test: historical statistics survive restart.
- Record app-owned operational counts in an earlier period, advance beyond 30 days and 36 months, record later activity, and restart: lifetime totals include both periods and each period remains queryable with its original counts. Quota-window rollover changes the current window, not the historical sum.
- Repeat a match for one video/route and add a later non-match: the distinct-video count is not inflated and the acceptance rate may fall without erasing earlier counts. Compare earlier run outcomes before and after a later run for the same video; historical outcomes remain unchanged.

- Run inclusion (AC10–AC11): in one period, record a completed manual run, a failed scheduled run, a stopped manual run, a running run, an interrupted run, and one each of dry run, replay and backtest. The period's outcome counts equal the sum of the first three runs only. After the interrupted run ends as completed, it is added.
- Discovered (AC12, rule 1): a failed scheduled run whose saved scan outcome is "cut short", with 500 discovered and 3 failed, adds 3 to failed and 0 to discovered. A run whose saved outcome is "scan completed" and that then failed during routing adds its discovered count. A saved "cut short" outcome with a last recorded step of routing still adds 0 to discovered.
- Discovered (AC12, rule 2): with no saved scan outcome, a run whose last recorded step is routing adds its discovered count; one whose last recorded step is discovery, or that recorded no step, adds 0.
- Acceptance rate (AC13): a period with one finished-discovery run (100 discovered, 10 accepted) and one failed run whose discovery did not finish (2,000 discovered, 0 accepted) shows 10%, not about 0.5%. A period containing only the second run shows no data, not 0%.
- Route match rates (AC14): decisions from a failed scheduled run count; decisions from an interrupted run, a dry run, a replay or a backtest in the same period do not.

## Depends on

- [BR-020](br-020-run-level-and-route-level-statistics.md) (run/route statistics provide underlying data)
- [BR-025](br-025-configuration-history-for-statistics-interpretation.md) (configuration history provides context for interpretation)
- [BR-018](br-018-api-consumption-accounting-and-visibility.md) (API-call/quota consumption history)
- [OC-002](oc-002-route-evaluation-outcome-semantics.md) (unchanged outcome meanings)
- [BR-032](br-032-processing-outcome-distinction.md) (absence of processing is never a negative result; AC13)
- [BR-035](br-035-stop-active-run.md) (the stopped status) and [BR-002](br-002-subscription-discovery-without-live-monitoring.md) (channel scan completion and failure, AC12)

## Open questions

- **User, non-blocking — older runs counted by rule 2.** Before release, check the 27 failed scheduled runs in the user's example. If they have no saved scan outcome and recorded a step after discovery, rule 2 counts their discovered videos and the inflation stays. The user then decides whether such older failed runs should be left out of "discovered" instead. Also confirm the AC14 reading that route match rates do not additionally require finished discovery.

- **Compliance/provider authority, then product owner:** Establish current applicable policy and any required approval for provider publication statistics, playlist-derived retained/usefulness metrics, and title-derived credit metrics. If an accepted metric cannot be retained indefinitely under that authority, the user must choose the permitted bounded-history behavior or a separately authorized analytics scope. This amendment supplies neither approval nor permission to discard/cap the metric. This blocks extending the no-expiration promise to those metrics, not recording the app-owned operational retention preference.
