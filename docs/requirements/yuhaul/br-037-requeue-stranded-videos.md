---
type: requirement
title: BR-037 — Re-queue Stranded Videos
description: The user can put every stranded known video into the deferred queue so normal runs process it, using daily quota with none reserved; videos whose playlist addition may already have been sent stay blocked and are reported; videos YouTube no longer returns end as "unavailable on YouTube"; no YouTube call by the action; requires a paged deferred queue.
tags: [requirement, runs, deferred-queue, recovery, run-14, send-guard, stranded, priority:MUST]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL let the user start a "re-queue stranded videos" action from the administration interface. After the user confirms a preview that shows the counts, the action SHALL give each stranded video exactly one pending deferred item that is eligible at once, so that normal production runs process it, a day's quota at a time. It SHALL NOT re-queue a video whose playlist addition may already have been sent. It SHALL report those videos instead. It SHALL make no YouTube call, and repeating it SHALL change nothing more. A re-queued video that YouTube no longer returns SHALL end with the final outcome "unavailable on YouTube", not as failed in the Errors queue.

## Example

The numbers are illustrative. Run 14 left 19,615 videos saved but never evaluated. The failed runs of 2026-11-06, which discovered 22,067 and 62,866 videos, also left saved videos unfinished. Discovery skips these videos because they are already known, and no queue holds them, so no run will ever process them.

The user first marks run 14 as ended ([BR-036](br-036-mark-legacy-run-as-ended.md)), then opens "Re-queue stranded videos". The preview shows: 70,000 videos will be added to the deferred queue; 41 are blocked because a playlist addition may already have been sent; 12 belong to run 52, which has not ended and can still continue them; 2 already have an Errors-queue entry and stay there; 3 were removed from a queue by the user; and 150 have a committed deferred or failed outcome but no queue item, so they are counted only. The user confirms. 70,000 pending deferred items now exist, each with the reason "re-queued by user", and YouTube received no request. The Deferred view shows them a page at a time, with the total. The next scheduled run claims and processes them while the day's quota lasts, and it may use the whole day's quota, so newly uploaded videos wait. Items it does not reach stay pending and later runs continue them. 300 of the re-queued videos have been deleted or made private: YouTube no longer returns them, so each ends as "unavailable on YouTube", is shown with that outcome in the run's details and counts, and does not appear in the Errors queue. The 41 blocked videos are unchanged and still listed. Pressing the action again shows 0 videos to re-queue.

## Definitions

Terms from other requirements: *production run*, *active*, *simulation*, *candidate set* and *ended* (`completed`, `failed`, or stopped by user) have the meanings in [BR-035](br-035-stop-active-run.md). A discarded run ([BR-017](br-017-manual-run-initiation.md)) and a run marked as ended ([BR-036](br-036-mark-legacy-run-as-ended.md)) are `failed`, so they are ended.

- A *known video* is a video that production discovery has saved. Records from a corpus import are not known videos in this sense. They are duplicate references only ([BR-009](br-009-historical-title-persistence-and-corpus-import.md)).
- A *committed outcome* is a final outcome of accepted, rejected, excluded, deferred, failed or unavailable on YouTube ([BR-032](br-032-processing-outcome-distinction.md)) currently recorded for the video. Not-yet-evaluated is not a committed outcome. A simulation's result never counts.
- A video is *not returned by YouTube* when YouTube successfully answers a request for that video's data and the answer does not include the video, for example because it was deleted or made private. A request that is refused, rate limited, unauthorized, fails or gets no answer does not show this.
- A known video is *unfinished* when it is not completed and has no committed outcome. These cases are all unfinished:
  - it was never evaluated;
  - its evaluation started but was never saved;
  - it was evaluated, at least one route matched, and its playlist actions were not carried out;
  - its playlist actions started, but no outcome was committed.

  An evaluated video with a committed rejected or excluded outcome is finished.
- An *active queue item* is a deferred item that is pending or claimed, or an Errors-queue entry that is active or claimed ([BR-013](br-013-quota-limited-work-deferral.md), [BR-014](br-014-failed-work-retention-and-retry.md)).
- A video is *removed by the user* when its most recent queue item was removed by the user ([BR-014](br-014-failed-work-retention-and-retry.md) AC4; "Dismiss" in the deferred queue).
- A video's *runs have ended* when every production run whose candidate set includes it is ended.
- A video is *blocked* when the existing send safety rules would refuse at least one playlist addition for it, because an earlier addition may already have been sent. That is the case when any of these holds:
  1. *Unresolved send guard:* a send guard from any run is still held for the video ([BR-012](br-012-idempotent-playlist-actions.md), [OC-006](oc-006-refresh-authorization-and-call-safety.md)).
  2. *Uncertain addition:* a playlist addition for the video is recorded as uncertain ([BR-035](br-035-stop-active-run.md) AC8).
  3. *Ambiguous legacy evidence:* a production run with mutation-safety version 0 left evidence that it may have sent an addition for the video, and no recorded success for the same video and target resolves it. Such evidence is any of these:
     - a matching route decision, including one whose saved configuration or route can no longer be read;
     - a recorded addition attempt that did not succeed;
     - the video was left mid-action with no matching decision while the run was executing actions;
     - a queue retry of the video was left unfinished while the run was executing actions.

  The blocked set SHALL match what the send safety rules refuse when a run sends. If those rules would refuse an addition for the video, the video is blocked.
- A video is *stranded* when it is known and unfinished, has no active queue item, was not removed by the user, all its runs have ended, and it is not blocked.

## Acceptance criteria

### Who and when

1. **The user, explicitly.** The user SHALL be able to start the action through the administration interface, with the same access that allows Run Now. No other actor SHALL start it: not the schedule, startup or startup recovery, a run, Stop, Discard or Mark as ended.
2. **Refusals.** The system SHALL refuse the action, change nothing and make no YouTube call while a production run is active (including a resumed run and a queue-retry receiving run), while startup recovery is in progress, and after shutdown has begun. The refusal SHALL give a safe reason that says which of these applies, with no raw error text, database text, credential or provider payload. An active simulation does not cause a refusal.
3. **Checked again when it runs.** The system SHALL work out the stranded set and check the AC2 conditions again when it carries out the action, not only when it shows the preview.

### Preview and confirmation

4. **Counts before anything happens.** Before the user confirms, the interface SHALL show:
   - the number of stranded videos that will be re-queued;
   - the number of blocked videos, by blocking reason (AC15);
   - the number of unfinished videos that will not be re-queued because one of their runs has not ended, with the identifier of each such run;
   - the number of unfinished videos that will not be re-queued because they already have an active queue item, with those in the Errors queue counted separately;
   - the number of unfinished videos that will not be re-queued because the user removed them;
   - the number of known videos that have a committed deferred or failed outcome and no active queue item. These are counted only. The action SHALL NOT re-queue them.

   The action SHALL NOT move, re-queue or change a video's Errors-queue entry. Such a video stays in the Errors queue for the user to retry under [BR-014](br-014-failed-work-retention-and-retry.md). A video removed by the user stays out, as the definition of *stranded* says.

   Producing the preview SHALL change nothing and make no YouTube call. Cancelling or leaving the confirmation SHALL change nothing.
5. **Actual result reported.** The action applies to the stranded set as it is when the action is carried out (AC3). The acknowledgement SHALL report the number of videos actually re-queued, and the same categories as AC4. If the number re-queued differs from the number in the confirmed preview, the acknowledgement SHALL say so. If the stranded set is empty, the action SHALL change nothing and say that there is nothing to re-queue.

### What the action does

6. **One pending deferred item per stranded video.** After a successful action, each stranded video SHALL have exactly one pending item in the deferred queue that is eligible for the next run at once, with no waiting time. The item's reason SHALL say that the user re-queued it. Inspection of the deferred queue SHALL distinguish this reason from quota exhaustion, rate limiting, pending authorization, stopped by user and discarded by user. The video's earlier queue items and their history SHALL stay unchanged.
7. **Nothing else changes.** The action SHALL NOT change any video's processing state or outcomes, route decisions, playlist actions, action evidence, send guards, runs (status, reason, phase, counts or configuration snapshot) or other queue items. Blocked, removed and still-owned videos (AC4) SHALL stay exactly as they were. The action SHALL NOT clear, release or weaken any send safety rule.
8. **No YouTube call; no run.** The action SHALL make no YouTube call of any kind. It SHALL NOT start, resume or continue any run.
9. **All or none.** The items from AC6 SHALL be saved together or not at all. If they cannot be saved, nothing SHALL change and the user SHALL be told that the action did not take effect. After a crash during the action, the saved state SHALL show either no item from this action or all of them, never a part.
10. **Repeating is harmless.** A repeat of the action, a double submit, or two requests at the same time SHALL leave exactly the result of one action: each re-queued video has one active queue item. Once a video has its item, it is no longer stranded, because it has an active queue item. A later repeat therefore re-queues only videos that have become stranded since.

### Processing afterwards

11. **Normal runs process them.** Production runs that start as new runs, manual or scheduled, SHALL claim the items from AC6 and process their videos as [BR-035](br-035-stop-active-run.md) AC14–15 require for items handed over by a stop. That includes claiming only while quota remains, leaving unreached items pending, and never passing an item over for one that became eligible later. Each video is evaluated under the receiving run's own configuration. Earlier decisions stay as history. If the run cannot finish a video, for example because quota runs out, the video is retained under the existing rules ([BR-013](br-013-quota-limited-work-deferral.md), [BR-014](br-014-failed-work-retention-and-retry.md)), except as AC13 says, and later runs continue it. This action sets no batch limit of its own: daily quota is the limit, and none is reserved for new uploads (AC17).
12. **Send safety still applies.** When a run sends a playlist addition for a re-queued video, the existing rules ([BR-012](br-012-idempotent-playlist-actions.md), [OC-006](oc-006-refresh-authorization-and-call-safety.md)) still apply. An addition that those rules refuse SHALL NOT be sent.
13. **No outcome from missing inputs.** A run SHALL NOT commit rejected or excluded for a re-queued video because the inputs that its routes need, such as the title or other metadata, are missing or expired ([BR-032](br-032-processing-outcome-distinction.md) AC2–3). For such a video, exactly one of these SHALL happen:
    1. the run obtains the inputs under the normal quota rules ([BR-019](br-019-quota-aware-processing.md)) and evaluates the video;
    2. the video is *not returned by YouTube*, and the run commits the final outcome *unavailable on YouTube* (AC18);
    3. otherwise, for example when quota, rate limiting, authorization or a failed request prevents an answer, the video stays retained with a reason and is not lost ([BR-030](br-030-temporary-failure-work-preservation.md)). It SHALL NOT be committed as unavailable on YouTube.

### Interaction with Mark as ended

14. **Mark as ended first, then re-queue.** Marking a legacy run as ended ([BR-036](br-036-mark-legacy-run-as-ended.md)) SHALL still not queue or process its videos. Until it is ended, a stuck legacy run has not ended, so its unfinished videos are not stranded: the preview counts them under that run (AC4), and the action does not queue them. Once the run is ended, its unfinished videos that are not blocked or removed are stranded, and this action re-queues them. For run 14, the user marks it as ended and then starts this action.

### Reporting blocked videos

15. **Blocked videos are reported, not queued.** The preview and the acknowledgement SHALL let the user identify each blocked video, its blocking reason (unresolved send guard, uncertain addition or ambiguous legacy evidence), and the run whose evidence blocks it. The action SHALL offer no way to re-queue a blocked video or to override its block. A blocked video stays blocked until its evidence is resolved under the existing rules.

### Races

16. **One order at a time.** The action and any of these SHALL behave as if one happened completely before the other: a production run start (Run Now, schedule or queue retry), startup recovery, shutdown, Discard, Mark as ended, and the user removing or retrying a queue item. The saved state SHALL never mix them. If the other operation happens first and makes the action not allowed (AC2), the action is refused and changes nothing.

### Quota (user decision, 2026-11-11)

17. **No quota reserved for new uploads.** While re-queued items remain, runs MAY spend all of each day's quota on deferred work, including re-queued videos, so newly uploaded videos may wait for a later day. Neither this action nor its items SHALL cause any part of the daily quota to be reserved for new uploads or discovery. The existing processing order and quota rules ([BR-013](br-013-quota-limited-work-deferral.md), [BR-019](br-019-quota-aware-processing.md)) are unchanged.

### Videos that YouTube no longer returns (user decision, 2026-11-11)

18. **Unavailable on YouTube is a final outcome.** When a run commits *unavailable on YouTube* for a re-queued video (AC13, case 2):
    - the run SHALL NOT evaluate the video's routes or attempt any playlist addition for it;
    - the video's re-queued item SHALL no longer be active, and its history SHALL be kept;
    - the system SHALL NOT create an Errors-queue entry or a new deferred item for the video because of this, and SHALL NOT record it as failed;
    - the outcome is committed, so the video is finished: later runs and later re-queue actions SHALL NOT process or re-queue it;
    - the run's details SHALL show the video with this outcome, and run outcome counts and outcome filters ([BR-032](br-032-processing-outcome-distinction.md) AC4–5) SHALL show it as its own category, not counted as accepted, rejected, excluded, deferred or failed.

### Prerequisite

19. **A paged deferred queue first.** The action SHALL NOT be available to the user until the Deferred view, and the existing deferred-queue read that serves it (`GET /api/deferred`), return the deferred queue a page at a time instead of every item at once. With about 70,000 pending items, opening the Deferred view SHALL load one bounded page, SHALL show the total number of items, and SHALL let the user reach every item by paging. The reason shown for each item ([BR-013](br-013-quota-limited-work-deferral.md) AC4; AC6 of this requirement) and the user's ability to remove an item ([BR-014](br-014-failed-work-retention-and-retry.md) AC4) SHALL be unchanged.

## Derived from

- Statement, AC1–2, AC4 (the count), AC6, AC7, AC8, AC10, AC11, AC12, AC15: the user's explicit approval of 2026-11-11, relayed in the specification assignment. The original human conversation was not independently inspected. The assignment says:
  - the user starts the action explicitly;
  - it finds known videos that are unfinished, have no committed outcome and no active queue item, and puts them in the deferred queue, eligible at once;
  - normal runs then process them, a day's quota at a time;
  - it never re-queues a video with an uncertain playlist add, an unresolved send guard or ambiguous legacy evidence; the existing veto keeps blocking those, and the action reports them;
  - it makes no YouTube call;
  - repeating it is idempotent;
  - a confirmation shows a count before anything happens;
  - it is refused while a production run is active, during startup recovery and during shutdown;
  - the interaction with BR-036 must be explicit.
- **User decisions of 2026-11-11, after review of the first draft and of [run-initiation ADR-006](../../architecture/run-initiation/decisions/adr-006-requeue-stranded-videos.md).** These were relayed in an amendment assignment, and the original human conversation was not independently inspected.
  1. *Videos that YouTube no longer returns* (Statement, definitions, AC13, AC18): a re-queued video that YouTube no longer returns, because it was deleted or made private, gets the final outcome "unavailable on YouTube". It does not go to the Errors queue as failed. It stays visible in the run's details and in the outcome counts as its own category. This replaces ADR-006 Decision 6's default, which put such videos in the Errors queue as failed. [BR-032](br-032-processing-outcome-distinction.md) AC1 is amended to admit the category.
  2. *Quota* (AC11, AC17): while the backlog drains, it may use each day's quota, and new uploads wait. No quota is reserved.
  3. *Earlier answers to the three open questions* (AC4, definitions): Errors-queue entries are excluded, because only 2 are live and the user retries them manually; videos the user removed stay out; the preview counts committed deferred or failed videos that have no queue item.
  4. *Prerequisite* (AC19): the Deferred view and `GET /api/deferred` must be paginated, because about 70,000 items would otherwise load at once.
  5. *Claiming while quota remains* (AC11): the user asked whether this satisfies [BR-035](br-035-stop-active-run.md) AC14, or to adjust it. AC14 literally required the next run to claim *every* handed-over item. A run that claims only while quota remains does not do that when quota runs short. BR-035 AC14 is therefore adjusted (see its Derived from) so that a run claims items while it has quota, leaves the rest pending and eligible, and never passes over an item for one that became eligible later. Its outcome is unchanged: every item is claimed by a later run, and no item is stranded.
- *Stranded* is the state [BR-035](br-035-stop-active-run.md) AC16 forbids for new stops and discards. BR-037 is the remedy for videos that were stranded before BR-035, or by runs that failed without a handoff.
- **Supersedes** the user's decision of 2026-11-10 to leave run 14's backlog alone ([run14-recovery](../run14-recovery/index.md)). The run14-recovery proposal (its OC-001) stays declined. BR-037 is a different and simpler capability. It has no separate batch or request budget, and it adds no new discovery. Work is processed by normal runs under their own configuration, limited by daily quota.
- "One-time" in the assignment is read as a one-off action that the user starts, not something that runs automatically. Repeating it is harmless (AC10). Whether the control should stay available afterwards is a design question (see the index).
- The definitions of *unfinished*, *committed outcome* and *blocked* come from a source check on 2026-11-11. These facts are evidence for the definitions, not their source:
  - Saved processing states are `discovered`, `not_yet_evaluated`, `evaluating`, `evaluated`, `acted`, `completed`, `deferred` and `failed` (`internal/store/migrations/001_initial_schema.up.sql:80–83`; edges in `internal/state/machine.go:51–64`).
  - Rejected and excluded videos stay `evaluated`, with their outcome recorded (`internal/pipeline/pipeline.go:792–803`). Accepted videos become `completed` (`pipeline.go:1123–1125`). So an `evaluated` video with a matched route and no outcome is "evaluated but not acted on", and an `acted` video with no outcome is "acted on, outcome unknown".
  - The stop and discard handoff already treats a video as unfinished when it is not `completed` and has no `run_video_outcome` of accepted, rejected, excluded, deferred or failed (`internal/pipeline/authority.go:363–382`).
  - The send-time veto (`internal/state/playlist_guard.go:125–204` and the guard insert at `:210–219`) refuses on: an existing guard for the same video and target; a matched version-0 decision with an unreadable configuration or route, or with a captured playlist target that has no recorded success; a version-0 action record with no matching success; a version-0 `acted` video with no matched decision while the run was in action execution; and a version-0 retry left unfinished in action execution without success for the target. Category 3 of *blocked* restates these conditions as behavior, and the definition requires the blocked set to follow the veto.
  - *Production check for critic finding F4, 2026-11-11* (relayed in the requirements assignment, Loom Action b820f6ca, Plan revision 19; not re-run here): ADR-006 warned that a claim rewrites the retry evidence that the last veto condition above reads, so an ended version-0 run in action execution with open retry rows could lose its block. In production, the version-0 runs in action execution are runs 17 and 21, and they have 0 open retry rows. So F4 does not apply to the current data, and the *blocked* definition needs no change.
  - Dry runs evaluate already saved production videos and do not save new ones. Corpus imports write duplicate fingerprints, not videos (`internal/duplicate/detector.go:250`).
- Specification choices, made within the assignment:
  - *Runs must have ended* (definition, AC4, AC14). A video in a run that can still continue it, such as an interrupted run that startup recovery or Run Now will resume, already has an owner. Queuing it as well could let two runs process it. Requiring an ended run also makes the BR-036 order explicit: mark as ended, then re-queue.
  - *Removed stays removed* (definition, AC4), matching [BR-014](br-014-failed-work-retention-and-retry.md) AC4 and [BR-035](br-035-stop-active-run.md) AC12: re-queuing would undo the user's own removal.
  - The preview shows the counts that are not re-queued, and the user can identify each blocked video (AC4, AC15). The assignment asked the action to report blocked videos.
  - The execution recheck, reporting the actual result, all or none, and one order for races (AC3, AC5, AC9, AC16) match Discard (BR-017) and Mark as ended (BR-036).
  - A distinct re-queue reason (AC6), so the deferred queue can say why each item exists ([BR-013](br-013-quota-limited-work-deferral.md) AC4).
  - *No outcome from missing inputs* (AC13) is a necessary consequence of BR-032 AC2–3 and BR-030 AC5 for this backlog. Stored metadata expires (`internal/metadata/manager.go:270–271`), and routing falls back to empty metadata when it cannot read it (`internal/pipeline/pipeline.go:747–750`). So without AC13, much of an old backlog could be committed as rejected without ever being evaluated on real inputs.
  - Priority MUST, like the neighboring run-control requirements. The assignment did not state a priority.
  - *Not returned by YouTube* means a successful answer that lacks the video (definition, AC13 case 3). Without this, a quota refusal or a network failure could end a video for good, which would lose work that BR-030 requires to be kept.
  - AC18's consequences: no routing or addition, the item no longer active, committed and therefore finished. These follow from "final outcome" and "not in the Errors queue". Without them, the video would either be retried for ever or stranded with an active item that nothing processes.
  - Only re-queued videos get the new outcome, because the user decided it for re-queued videos. Whether other retried videos (quota-deferred items or stop handoffs) should also use it is a non-blocking question in the index. So is a playlist addition that YouTube refuses because the video no longer exists. Both follow existing rules until the user decides.
  - AC4's count of videos that already have an active queue item, with the Errors queue shown separately, makes the user's exclusion visible, as the other excluded categories are.
  - AC19 makes the user's prerequisite observable (one bounded page, a total, every item reachable). It does not fix the page size or the paging mechanism; those are for the Designer and the Architect.

## Verified by

Use the real server path with migrated SQLite and a controlled YouTube provider that counts requests.

- **Stranded set (definitions, AC4, AC6–7):** seed known videos in ended production runs (`failed`, discarded, marked ended, stopped, `completed`) in each unfinished case: `discovered`, `not_yet_evaluated`, `evaluating`, `evaluated` with a matched decision and no outcome, and `acted` with no outcome and no guard, from a version-1 run. Seed controls that must not be queued:
  - finished: `completed`, `evaluated`+rejected, `evaluated`+excluded, and committed deferred or failed;
  - an active queue item: a pending deferred item, a claimed deferred item, an active Errors entry and a claimed Errors entry;
  - removed by the user;
  - in an `interrupted` version-1 run, and in an unended version-0 `running` run;
  - blocked: one per veto condition in *blocked* category 3, one with a held guard, and one uncertain addition.

  Also seed known videos with a committed deferred or failed outcome and no queue item, and a video with a committed unavailable-on-YouTube outcome. Snapshot every row, then run the action. Only the stranded videos get exactly one pending, immediately eligible deferred item with the re-queue reason. Every other row is unchanged, including both Errors-queue entries. The preview counts match each category (with Errors-queue entries counted separately, and the committed deferred or failed count), and the blocked list names each video's reason and run.
- **Veto agreement (AC15, AC12):** for each blocked seed, call the send path for each of its targets: the send safety rules refuse. For each re-queued seed, they do not refuse, before any other run touches it.
- **No YouTube call (AC4, AC8):** the provider records zero requests during the preview and the action. No run starts.
- **Refusals (AC2–3):** request the action while a Run Now run, a scheduled run, a queue-retry run and a resumed run are active, during startup recovery and after shutdown has begun. Each is refused with its reason, and every row is unchanged. With a dry run active, the action succeeds. Show the preview, start a production run, then confirm: the action is refused.
- **Atomicity and repeats (AC5, AC9–10):** inject a save failure: there is no new item, and the user is told. Kill the process during the action: on restart there are either no items from it or all of them. Submit twice at once, then again: one item per video, and the later answers report 0 to re-queue. Remove one item between preview and confirmation: the acknowledgement reports the different number.
- **Processing (AC11, AC13):** start a new scheduled run, and separately a new Run Now run, with discovery returning none of these videos and a quota that covers only part of them. The run claims items while it has quota, evaluates under its own snapshot and adds to playlists until quota runs out. Items it claimed but could not finish are quota-deferred, items it did not claim stay pending and eligible, and the next run continues with them before any item that became eligible later (BR-035 AC14). Give some videos expired metadata: none is committed rejected or excluded for that reason, and each one either gets its inputs within quota or stays retained with a reason.
- **Unavailable on YouTube (AC13, AC18):** give some re-queued videos expired metadata and have the provider answer successfully without them. Each is committed as unavailable on YouTube: no route is evaluated, no addition is attempted, no Errors-queue entry or new deferred item exists, its item is no longer active, and run details, outcome counts and the outcome filter show it in its own category. Have the provider instead refuse for quota, rate-limit, or fail with a network error and with a 5xx: none of those videos is committed as unavailable, and each stays retained with a reason. A later run and a later re-queue action leave the unavailable videos alone.
- **No reserved quota (AC17):** with re-queued items that need more than a day's quota and discovery returning new uploads, the run may spend the whole day's quota on deferred work. Nothing stops it from doing so to keep quota for the new uploads. The new uploads are processed on a later day.
- **Paged deferred queue (AC19):** with 70,000 pending items, opening the Deferred view loads one bounded page, shows the total of 70,000, and paging reaches every item exactly once. Reasons and removal work as before. The action is not offered until this holds.
- **BR-036 order (AC14):** seed a run-14-shaped version-0 `running` run with unevaluated videos. The preview counts them under that run, and the action queues none of them. Mark the run as ended. The next action re-queues them, except blocked or removed ones.
- **Races (AC16):** race the action against each operation listed in AC16 at least 100 times. Every result matches one serial order, with no duplicate item and no item for a video that the other operation gave an active item first.

## Depends on

- [BR-035](br-035-stop-active-run.md) (definitions; AC14–15 claiming and processing, AC14 amended 2026-11-11 for claiming while quota remains; AC16 the forbidden stranded state; AC8 uncertain additions)
- [BR-036](br-036-mark-legacy-run-as-ended.md) (ending a stuck legacy run; AC14)
- [BR-017](br-017-manual-run-initiation.md) (Run Now access, discard, resume; AC1, AC16)
- [BR-012](br-012-idempotent-playlist-actions.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md) (send guards and the veto; *blocked*, AC12, AC15)
- [BR-013](br-013-quota-limited-work-deferral.md) and [BR-019](br-019-quota-aware-processing.md) (deferred queue, quota; AC6, AC11, AC13)
- [BR-014](br-014-failed-work-retention-and-retry.md) (Errors queue, removal; definitions, AC4)
- [BR-030](br-030-temporary-failure-work-preservation.md) and [BR-032](br-032-processing-outcome-distinction.md) (outcome categories, including unavailable on YouTube; missing inputs; AC13, AC18)
- [BR-009](br-009-historical-title-persistence-and-corpus-import.md) (corpus records are not known videos)
