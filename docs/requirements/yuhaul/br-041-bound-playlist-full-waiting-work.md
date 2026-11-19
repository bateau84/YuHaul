---
type: requirement
title: BR-041 — Bound Playlist-Full Waiting Work
description: Keep at most 5,000 distinct waiting videos per actual playlist, with targeted continuation and visible oldest-first eviction.
tags: [requirement, playlist, capacity, deferred-queue, retry, eviction]
timestamp: 2026-11-18T00:00:00Z
---

**Status:** accepted  
**Priority:** MUST

## Statement

YuHaul SHALL keep a playlist-full waiting list for each actual YouTube playlist, shared across every rule, route and run targeting it. It SHALL keep at most 5,000 distinct waiting videos per playlist. Admitting a new distinct missing add at the limit SHALL evict the oldest waiting add for that playlist, with visible history and no automatic retry or requeue of the evicted add.

## Acceptance criteria

1. **Count missing adds by video and actual playlist.** One video can wait once on playlist A and once on playlist B. Two routes to the same actual playlist for the same video represent one waiting add, regardless of route, run, playlist display name or configuration edits. A repeat observation, retry that finds it still full, or change of claim owner does not add a second entry or reset its age. An in-progress claim still counts until it leaves waiting work by a committed disposition; claiming alone does not make a queue slot free. Work deferred solely for other reasons does not count and is not evicted by this limit.
2. **Evict only on overflow, oldest first.** A new distinct admission at 4,999 makes 5,000 and evicts nothing. At 5,000 it displaces the waiting add enrolled earliest for that playlist. Settled admission order breaks any time tie, consistently across restart. Concurrent admission, repeated reports and claim/eviction races SHALL have the same visible result as a single order of effective transitions, with no duplicate, excess entry, or partial admission/eviction after a failed save or restart.
3. **Keep eviction visible and scoped.** Queue inspection SHALL let the user identify the video, target playlist, and that the missing add was evicted because the playlist's waiting list reached its limit. It is no longer active capacity work. Eviction is not Dismiss by the user, an attempted add, a failed add, rejection, acceptance, or any other new final video outcome. It SHALL NOT modify previously committed run evidence or reverse recorded success. Eviction affects only that missing add: independent work for other targets or other deferral reasons is not deleted. Another active source for the same video does not authorize automatic retry or requeue of the evicted target.
4. **Retry only the missing add, when eligible.** In the ordinary production retry path, retained playlist-full work SHALL be reconsidered for room and be eligible to continue when room is established under [BR-040](br-040-respect-youtube-playlist-capacity.md) and existing production authority, quota and guard rules allow it. While still full it stays waiting at its existing age and does not block other eligible work. Use the retained exact target and the decision authorizing the queued add; do not use current routes to replace that target, redo a success, or make new additions as part of this retry, even if no target has yet succeeded. For partly done videos the original-decision rule in BR-039 applies. If authority cannot be established, do not guess a target or send; preserve the item with a safe reason under the applicable retention rules. No background polling, retry deadline, or slot reservation on YouTube is promised.
5. **Eviction is not a recovery signal.** Neither later room, rediscovery, retry of other work for the video, startup, resume, stop/discard handoff, nor [BR-037](br-037-requeue-stranded-videos.md) SHALL cause the evicted missing add to be automatically attempted or requeued. A stale claim cannot reinstate work or authorize a new send after eviction. Eviction does not recall an already admitted send, supply proof of non-application, or clear an unresolved guard. Record the actual result of any already admitted send under its existing authority, without recreating an evicted waiting entry. A remaining evicted add is still missing, not successful; other successful adds alone do not make the video accepted under BR-039.
6. **Keep other blockers distinct.** A later quota, authorization or temporary failure that prevents checking or using room is not evidence that the playlist has room. It SHALL NOT on its own remove established capacity waiting work, change its age, or create a second active capacity entry. Processing separate work for the same video SHALL NOT treat a live capacity-waiting add as completed or erase it because the routes changed. Existing quota and retry precedence still applies among eligible work; oldest-first here is an eviction rule, not a new promise of a send order or priority over other work.

## Derived from

- AC1–5: user decisions relayed in the 2026-11-18 specification assignment (Loom Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`): 5,000 waiting videos **per actual YouTube playlist**, shared across routes/rules; evict the oldest on overflow, keep visible history, do not automatically retry or requeue an evicted missing add, do not make a final result out of eviction or erase successful adds, and do not count other deferral reasons. The original human conversation was not independently inspected.
- Necessary consequences: count each video once per target; retries may not use sibling work to evade eviction; preserve exact target and decision authority to make the intended missing-add-only retry possible. Displacing the oldest without recording the new add, or retaining the new add without displacing the oldest, breaks the accepted policy.
- Specification choices: age starts at enrollment in the current waiting list, not publication, discovery, last retry or reconciliation; settled admission order resolves ties. A claim is not removal. Priority MUST, as for related production retention obligations.
- AC6 applies existing retention and eligibility rules: without it a failed capacity read could silently release a waiting slot or change which add is oldest.

## Verified by

- Fill A across two routes and runs to 4,999 and then 5,000; include the same video on both routes, misleadingly equal names for A and B, a rename, other-reason items and a second target for the same video. Repeat/retry the oldest at capacity without eviction or age change. Admit distinct video 5,001; only A's oldest is evicted, the latest enters, and B and the other-reason work are unchanged. Use tied timestamps; reopen and inspect the history and active counts.
- Overlap admissions and repeat them; inject save failure at the admission/eviction boundary. Pause a claim before send, evict it, then continue; no add or requeue based on the evicted claim. Separately use an already admitted send to verify actual evidence survives without resurrection. After room opens, normal production sends retained adds under the existing guard and quota rules and omits the evicted target, including when a different live queue item for its video is processed or Re-queue stranded videos is invoked. No historical run outcome or recorded success changes due to eviction.
- Defer a video with one success and two missing targets; remove or change its current routes, make room on one target and leave the other full. The retry uses the original decision, sends only the eligible missing add, does not repeat the success, and retains only still-authorized waiting work. Repeat with no prior success. Exercise each BR-039 final outcome and an unreadable original decision as no-send controls.
- Prevent the capacity recheck for the oldest entry with quota or authorization refusal. It stays waiting at the original age, no add is sent, and the blocking cause is not shown as new evidence of fullness.

## Depends on

- [BR-040](br-040-respect-youtube-playlist-capacity.md) (capacity and run behavior) and [OC-007](oc-007-playlist-capacity-deferral-handoff.md) (handoff, evidence and replay authority).
- [BR-039](br-039-final-outcomes-are-never-reprocessed.md), [BR-035](br-035-stop-active-run.md) and [BR-037](br-037-requeue-stranded-videos.md) (finality, handoff and requeue boundaries).

## Outside scope

This requirement does not add a manual restore, replay, or guard-clear action for an evicted add. Whether to introduce a new explicit recovery capability is a separate user decision only if requested. Existing controls for other live eligible work are unchanged.
