---
type: requirement
title: BR-035 — Stop an Active Run
description: The user can stop any active run for good; its unfinished videos go to the deferred queue and the next new production run continues them automatically.
tags: [requirement, runs, stop, deferred-queue, recovery, send-guard, priority:MUST]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL let the user stop any active run from the administration interface, for any reason. A stopped run is stopped for good: it is never resumed, and it is shown as stopped by the user, not as a failure. For a production run, its unfinished videos SHALL be handed to the deferred queue so that the next new production run continues them automatically. The stop itself SHALL make no YouTube call.

## Example

Scheduled run 41 is adding videos to playlists. The user presses Stop for run 41 and confirms. Run 41 sends no new request to YouTube. One playlist addition was already sent and has no answer yet: it is shown as uncertain, its send guard stays, and it is never sent again automatically. A second addition had been prepared but not sent: its guard is released. Run 41 ends as "stopped by user". Its 9 unfinished videos each have one pending item in the deferred queue. The next run, whether Run Now or the schedule, claims those 9 videos and processes them, although discovery does not report them as new. It adds the prepared video to its playlist, and it does not send the uncertain one again.

## Definitions

- An *active run* is a run that is executing now: a manual (Run Now) run, a scheduled run, a queue-retry receiving run, a run being resumed (by Run Now or by startup recovery), or a dry run, replay or backtest. An `interrupted` run that is not executing, and a `completed`, `failed` or stopped run, is not active.
- A *production run* is a manual, scheduled or queue-retry receiving run. Dry runs, replays and backtests are *simulations*.
- An *unfinished video* of a run is a video in that run's candidate set that is not completed and has no committed final outcome for that run, as in [BR-017](br-017-manual-run-initiation.md) AC17.
- A stop *takes effect* when the system accepts it (AC3). It *settles* when the run's terminal stopped state and its queue handoff are saved (AC5).

## Acceptance criteria

### Who can stop what

1. **Any active run, any reason.** The user SHALL be able to stop any active run through the administration interface, with the same access that allows Run Now. No reason has to be given. The run types are listed under *active run* above.
2. **Confirmation.** A stop SHALL take effect only after the user confirms it for a named run. Before the user confirms, the interface SHALL say which run will be stopped, that it will never be resumed, and, for a production run, that its unfinished videos go to the deferred queue for the next run. Cancelling or leaving the confirmation SHALL change nothing. A confirmed stop applies only to the run named in it.
3. **Acknowledgement.** When the system accepts a stop, it SHALL acknowledge it with the run's identity and say that the run is stopping. Run progress ([BR-033](br-033-honest-manual-run-progress.md)) SHALL then show the run as stopping until it settles, and as stopped by user afterwards. It SHALL NOT show the run as running normally, completed or failed.
4. **Only the user stops.** Run Now, the schedule, startup recovery, shutdown, the time limit and queue retries SHALL NOT stop a run in this sense. Their existing outcomes (completed, failed, interrupted) are unchanged.

### What a stop does to the run

5. **Stopped for good, not a failure.** A stopped run SHALL end in a terminal state that run history, run inspection ([BR-028](br-028-historical-run-inspection.md)) and run filters show as "stopped by user". It SHALL be distinguishable from `completed`, `failed` and `interrupted`, and SHALL NOT be counted as a failed run or a completed run in run statistics. Run Now, startup recovery, the schedule and queue retries SHALL NOT resume, restart or continue it. The run keeps its identifier, trigger, start time, configuration snapshot, saved phase, counts, committed per-video outcomes and action evidence. Work committed before the stop stays committed.
6. **No YouTube call; no new requests.** The stop itself SHALL make no YouTube call. After a stop takes effect, the stopped run SHALL NOT start any new YouTube request: no read, no discovery page, no metadata fetch and no playlist addition.
7. **A prepared addition that was not sent.** For a playlist addition that the run had prepared, but whose request had not been sent when the stop took effect, the send guard SHALL be released and no attempt SHALL be recorded as sent. A later run may then perform that addition, subject to [BR-012](br-012-idempotent-playlist-actions.md).
8. **An addition already in flight.** For a playlist addition whose request was sent before the stop took effect and whose outcome is not known when the run settles, the system SHALL record the outcome as uncertain, keep its send guard, and never send it again automatically, as [OC-006](oc-006-refresh-authorization-and-call-safety.md) requires. Run inspection SHALL show it as uncertain. If the run records the request's actual result before it settles, that result stands.
9. **Durable once accepted.** Once a stop has been acknowledged, the stop SHALL survive a crash or process stop before it settles. On the next start, startup recovery SHALL NOT resume that run. It SHALL settle the stop instead, with the same result as AC5–8 and AC10–16. A request in flight at the crash is treated as in AC8.

### Unfinished work goes to the deferred queue (production runs)

This section is the normative home for handing a run's unfinished videos to the next run. [BR-017](br-017-manual-run-initiation.md) AC17 (discard) applies it too, so in AC10–16 "the ended run" means a stopped or a discarded run. Simulations hand nothing over (AC17).

10. **One pending deferred item per unfinished video.** When the stop settles, each unfinished video of the ended run SHALL have exactly one pending item in the deferred queue. The item carries the stop reason (or the discard reason). If the video already has a deferred item that is pending, or that the ended run had claimed, that item SHALL be reused and made pending, keeping its identity, origin, reason and history. No duplicate is created.
11. **Claimed errors move to the deferred queue.** If the ended run had claimed an Errors-queue entry for an unfinished video (a queue retry), that entry SHALL no longer be active in the Errors queue, and its history SHALL be kept. The video's work continues from its deferred item (AC10).
12. **Removed stays removed.** If the user removed a video's queue item while the ended run had it claimed, the item stays removed and no new item is created for that video.
13. **No waiting and no leftover claims.** Each item from AC10 SHALL be eligible for the next run at once, with no waiting time. When the stop or discard has settled, no queue item SHALL still be claimed by the ended run.
14. **The next new run claims and processes the videos.** The next production run that starts as a new run, manual or scheduled, SHALL claim every item from AC10 and process its video in that run: route evaluation and playlist actions, under their send guards. This SHALL happen even though discovery does not report these videos as new, because they are already known. The videos SHALL appear in that run's inspection. A Run Now press that resumes an earlier run instead (BR-017 AC7) does not have to claim them, and they wait for the next new run. If that run cannot finish a video, for example because quota runs out, the video is retained again under its existing rules ([BR-013](br-013-quota-limited-work-deferral.md), [BR-014](br-014-failed-work-retention-and-retry.md)).
15. **An ended run never blocks later runs.** A stopped or discarded run, and any queue item it once claimed, SHALL NOT make a later run start, Run Now press or queue retry fail or be refused.
16. **Nothing is stranded.** From the moment the stop or discard settles, no unfinished video of the ended run SHALL be without an active queue item, unless the user removed it (AC12). A video that is known to discovery but has no active queue item and no committed outcome is exactly the state that stranded run 14, and it is forbidden here.

### Simulations

17. **Stopping a simulation.** Stopping a dry run, replay or backtest SHALL end it as stopped by user (AC5) and make no YouTube call. It SHALL NOT create, change or claim any queue item, send guard or action evidence. Results recorded before the stop stay visible and SHALL be shown as incomplete because the run was stopped.

### Refusals, races and repeats

18. **Refusals.** The system SHALL refuse a stop, change nothing and make no YouTube call when: the run does not exist; the run has already ended (`completed`, `failed` or stopped); the run is not active (for example, `interrupted` and not executing, including a discardable run under BR-017); or shutdown has begun. The refusal SHALL identify the run and give a safe reason saying which of these applies. A run refused during shutdown is interrupted by the shutdown as before and can be stopped once it is active again.
19. **During startup recovery.** A run that startup recovery is resuming is active and SHALL be stoppable. After that stop, startup recovery SHALL continue with the other runs it is recovering, and the stopped run SHALL NOT be resumed again. A stop request for a run that startup recovery has not yet started resuming is refused as not active (AC18).
20. **Double press.** A repeated or double-submitted stop of the same run SHALL leave exactly the result of one stop: one terminal state, and one queue item per unfinished video. The repeat is answered with the same run identity and says that the run is already stopping or stopped.
21. **Race with the run ending on its own.** A stop and the run ending by itself, whether completed, failed, interrupted by its time limit or interrupted by shutdown, SHALL behave as if one happened completely before the other. Either the run's own outcome stands and the stop is refused as already ended or not active (AC18), with nothing changed. Or the stop takes effect, and the run ends stopped as in AC5–16. The saved state SHALL never mix the two.

## Derived from

- AC1–16, AC18–21: the user's explicit decision of 2026-11-10, relayed in the specification assignment. The user can stop any active production run (manual, scheduled or queue retry) from the UI for any reason. A stopped run is stopped for good, never resumed, and has a distinct "stopped by user" status or reason, not a failure. Unfinished work goes to the deferred queue so the next run, manual or scheduled, continues it automatically. A not-yet-sent addition has its guard released. An addition in flight is recorded as uncertain, keeps its guard and is never resent. The stop itself makes no YouTube call. The assignment named the cases in AC18–21. The original human conversation was not independently inspected.
- AC14–16 (the next run really claims the work): the assignment required this to be stated explicitly, because run 14 was stranded by exactly this gap. A source check on 2026-11-10 found these facts. They are evidence for the obligation, not its source:
  - discovery skips every video that is already known (`internal/discovery/engine.go:327–333`), so an unfinished video reaches a later run only through a queue claim (`listRunVideos`, `internal/pipeline/pipeline.go:1120–1124`);
  - a new run start claims only deferred items that are pending with no waiting time left, or claimed by a run that is `failed` or by nobody (`internal/queue/retry.go:463–469`, `:196–205`);
  - an item claimed by a run in any other ended state is treated as an integrity error, and `createClaimedRunLocked` returns that error, so the whole run start fails (`internal/queue/retry.go:258–289`; `internal/pipeline/pipeline.go:251–254`);
  - the existing sweep sends unfinished videos that no queue item covers to the Errors queue (`internal/pipeline/authority.go:357`), which run starts never claim.
- AC17: the user suggested that simulations can be stopped, simply ended with no queue effects, and left the decision to this specification. Adopted as suggested.
- Specification choices, delegated by the assignment:
  - the confirmation (AC2), because the stop cannot be undone, matching discard (BR-017 AC14);
  - durability of an acknowledged stop across a crash (AC9). Without it, startup recovery would resume a run the user stopped;
  - moving a claimed Errors-queue entry to the deferred queue (AC11), because the user asked for unfinished work in the deferred queue and an Errors-queue entry is never claimed automatically;
  - removed stays removed (AC12), as in BR-017 AC17;
  - refusal once shutdown has begun (AC18), matching discard;
  - stoppable during startup recovery (AC19), because a recovered run can run for a long time;
  - one serial order for races (AC21).

## Verified by

Use the real server path with migrated SQLite and a controlled YouTube provider that counts requests.

- **Stop each run type (AC1, AC5, AC17):** start a Run Now run, a scheduled run, a queue-retry run, a Run Now resume, a dry run, a replay and a backtest. Stop each one. Each ends as stopped by user; run history shows it as stopped, not failed or completed, and the failed-run count is unchanged. Restart the process and press Run Now: no stopped run is resumed.
- **Confirmation (AC2):** cancelling or leaving the confirmation leaves every row unchanged.
- **No calls after the stop (AC6):** pause the provider inside a discovery page and inside a playlist addition, stop the run, then release it. The provider records zero requests started after the stop was accepted.
- **Guards (AC7–8):** pause one addition before it is sent and one after it is sent. Stop the run. The unsent addition's guard row is gone and no attempt is recorded. The sent addition is shown as uncertain, its guard row stays, and the next run sends zero requests for that target while it does add the unsent one.
- **Crash durability (AC9):** kill the process after the stop is acknowledged but before it settles. On restart, the run is not resumed, it ends stopped, and its unfinished videos are in the deferred queue.
- **Handoff (AC10–13, AC16):** stop a run whose unfinished videos include: a video with no queue item; one with a pending deferred item; one whose deferred item the run claimed; one whose Errors-queue entry the run claimed; and one whose item the user removed while claimed. Afterwards, each unfinished video except the removed one has exactly one pending deferred item that is eligible now, and the claimed Errors-queue entry is no longer active, with its history kept. The removed item stays removed. No queue item is claimed by the stopped run. No unfinished video is left without an active item.
- **Next run claims (AC14–15):** after that stop, start a new scheduled run and, separately, a new Run Now run, each with discovery returning none of these videos. The run claims every handed-over item, evaluates its routes and performs its additions, and run inspection lists the videos. The run start does not fail. Repeat with a run that the discard in BR-017 ended.
- **Refusals (AC18–19):** stop a run that does not exist, an ended run, an interrupted run that is not executing, and a run during shutdown. Each is refused with its reason, and every row is unchanged. During startup recovery, stop the run being resumed: it ends stopped, recovery continues with the next run, and the stopped run is not resumed.
- **Double press and races (AC20–21):** submit the same stop twice at once: one stop, and one deferred item per video. Race a stop against the run completing, failing, reaching its time limit and being interrupted by shutdown, at least 100 times each. Every result matches one serial order and is never a mixture.

## Depends on

- [BR-012](br-012-idempotent-playlist-actions.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md) (send guards and uncertain additions; AC7–8)
- [BR-013](br-013-quota-limited-work-deferral.md) (deferred work; AC10–14)
- [BR-014](br-014-failed-work-retention-and-retry.md) (Errors queue and removal; AC11–12)
- [BR-017](br-017-manual-run-initiation.md) (Run Now resume and discard; AC14, AC18)
- [BR-028](br-028-historical-run-inspection.md) and [BR-032](br-032-processing-outcome-distinction.md) (inspection and outcome categories; AC5)
- [BR-033](br-033-honest-manual-run-progress.md) (progress; AC3)
