---
type: requirement
title: BR-040 — Respect YouTube Playlist Capacity
description: Check room before a production playlist addition, defer only blocked targets, and show what actually happened in the run.
tags: [requirement, playlist, capacity, deferral, run-evidence]
timestamp: 2026-11-22T00:00:00Z
---

**Status:** accepted  
**Priority:** MUST

## Statement

Before sending a production add, YuHaul SHALL check whether the actual target YouTube playlist has room for one more item under the 5,000-item limit. When the add cannot proceed because the playlist is full, YuHaul SHALL make that reason visible in the run and retain the missing add as specified in [BR-041](br-041-bound-playlist-full-waiting-work.md).

## Acceptance criteria

1. **Check the target before adding.** With usable evidence that the playlist has 4,999 items, one otherwise eligible add may proceed. At 5,000 or above, YuHaul SHALL NOT send the add. The check SHALL account for YuHaul's own adds to that playlist, across routes and runs: the same evidence of one free slot cannot justify two sends unless evidence establishes that there is still room. Unknown application of an earlier add is not evidence that its slot is still free. *Which adds a count already includes (amended 2026-11-22 by user decision, "trust the count"):* a count YuHaul received after it received the response of a recorded successful add to that playlist covers that success. That add is treated as already included in the count and is not counted again. A success recorded after the count was received is not covered and must be accounted for in addition. An add whose application is unknown (an unresolved send guard) is never covered by any count. If YouTube refuses an add with its playlist-full error (`403 playlistContainsMaximumNumberOfVideos`) because the count lagged, that refusal is the backstop: it is one attempted add under existing accounting, and the add is deferred as playlist-full under AC3 and [OC-007](oc-007-playlist-capacity-deferral-handoff.md) Guarantee 2. A count that cannot establish room SHALL NOT be treated as either empty or full; do not send on that basis and preserve eligible work under the applicable existing failure or deferral rules.
2. **Defer this target, not the rest of the run.** A full playlist SHALL NOT by itself stop eligible additions to other playlists from proceeding, including other targets in the same video decision. Record each target's actual result. A missing add deferred for capacity is not a negative routing decision and does not by itself fail the run or finalize the video. When capacity alone prevents completion, the video's outcome in that run is deferred. When other causes also apply, preserve their actual evidence under the existing outcome rules; do not hide an uncertain add or an independently failed action by labeling it full.
3. **Report the difference between a check and a send.** The requested run's action evidence SHALL distinguish a capacity deferral with no add sent from an attempted add that was definitively refused for capacity under [OC-007](oc-007-playlist-capacity-deferral-handoff.md). Both show the target and the safe meaning “Playlist full — deferred”; only the latter counts as an attempted add. Actual capacity reads remain subject to existing authorization and quota accounting. Keep originating run evidence unchanged on retry or eviction. Capacity evidence and diagnostics SHALL NOT expose raw provider bodies, prose, credentials or raw errors.
4. **Preserve existing safety and isolation.** Apply the final-outcome, original-decision, recorded-success and guarded-send rules before sending on every production path. Room is a condition for sending, not permission to bypass any other check. Dry runs and backtests SHALL NOT perform this production capacity check, send additions, claim or change capacity waiting work, or report a production capacity refusal. They retain their existing hypothetical evaluation and isolation rules.

## Derived from

- AC1–3: user intent relayed in the 2026-11-18 specification assignment (Loom Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`): 5,000 videos per YouTube playlist; check before an add; reflect full in the run and queue missing adds until room opens. The assignment also identifies continued work for other targets and a full refusal after a capacity check. The original human conversation was not independently inspected. The limit is a product threshold, not a claim about a particular provider count or error field. The quoted label illustrates the accepted meaning, not required copy.
- AC1 *which adds a count already includes*: the user's "trust the count" decision, accepted "as recommended" on 2026-11-22 and relayed in the specification assignment for Loom Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`. It answers the capacity ARS-001 §4.2 question (finding 4) and reads "account for YuHaul's own adds" as satisfied, for a recorded success, by a count received after that success's response. The user accepted that YouTube's count may lag a confirmed add and that YouTube's own refusal is then the backstop. The original human conversation was not independently inspected. *Specification choice:* "after" means YuHaul received the count after it received the success's response, so the success cannot have been still in flight when the count was taken; the provider error is named as the accepted example of a definite playlist-full refusal, whose recognition remains governed by OC-007 Guarantee 2.
- Necessary consequences: a known-full target cannot be sent to just to confirm it is full; reusing the same last slot defeats the check; an unreadable count cannot prove room; a sent refusal cannot be counted as no attempt, nor a check as an add. AC4 applies existing obligations unchanged.

## Verified by

- Use the real production path with a controlled provider. From 4,999 items, have two eligible videos from separate routes target the same playlist: one add succeeds, the next has no add sent and shows capacity deferral. Include an eligible second playlist for the latter video and verify its add succeeds and stays recorded. Repeat across runs and after restart; a successful or possibly applied preceding send must not leave the earlier last-slot observation usable unchanged.
- Count coverage (AC1, 2026-11-22): record a success to a playlist, then receive a fresh count that already includes it; the success is not counted twice and the playlist's last slot stays usable. Record a success after a count was received; the next send from that count accounts for it. Leave an add with unknown application and take a later count; that add still counts against room. Make the provider count lag a confirmed add so YuHaul sends to a full playlist, and have YouTube refuse with `403 playlistContainsMaximumNumberOfVideos`: verify one attempted add with its quota, "Playlist full — deferred" evidence, the add waiting under BR-041, and no immediate replay.
- Exercise exactly 5,000, unavailable or insufficient count evidence, and stale room evidence followed by a recognized definitive refusal. Verify actual requests, run-scoped action/outcome inspection and attempt counts; do not label unknown application or failed reads as full. Use the assembled scenarios in OC-007 for guarded resolution.
- Run dry-run and backtest on the same inputs. Observe no new capacity check or mutations, no capacity queue/guard/production outcome changes, and no fake production results.

## Depends on

- [BR-039](br-039-final-outcomes-are-never-reprocessed.md), [BR-012](br-012-idempotent-playlist-actions.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md) (finality, exact-target success and send safety).
- [BR-018](br-018-api-consumption-accounting-and-visibility.md), [BR-019](br-019-quota-aware-processing.md), [BR-028](br-028-historical-run-inspection.md), [BR-030](br-030-temporary-failure-work-preservation.md) and [OC-002](oc-002-route-evaluation-outcome-semantics.md) (accounting, retention and diagnostics).
- [BR-026](br-026-dry-run-evaluation.md) and [BR-027](br-027-historical-backtesting.md) (simulations).

## Limits

The capacity check cannot reserve space on YouTube or prevent someone else from editing a playlist after it. The capacity feature does not remove anything from a YouTube playlist. The architect must establish what provider evidence is usable; a successful fixture is not evidence of a live provider guarantee.
