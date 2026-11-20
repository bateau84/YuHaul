---
type: requirement
title: BR-036 — Mark a Stuck Legacy Run as Ended
description: The user can mark an old pre-safety-version production run that is stuck as running or interrupted as failed; only its status and a safe reason change, and no YouTube call is made.
tags: [requirement, runs, legacy, run-14, end-run, priority:MUST]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL let the user mark a stuck legacy run as ended from the administration interface. The action SHALL change only the run's status, to `failed`, and record a safe reason saying that the user ended it as a legacy run. It SHALL NOT change the run's videos, queue items, send guards, actions or decisions, and it SHALL make no YouTube call. The action itself does not process or queue its unfinished videos. Once the run is ended, the user can re-queue them with [BR-037](br-037-requeue-stranded-videos.md).

## Example

Run 14 was created before the send-guard safety version existed (mutation-safety version 0). It still shows as `running`, although nothing is executing it. Stop, Discard, Run Now resume and startup recovery all exclude it, so it can never end by itself. The user opens run 14, which offers "Mark as ended". The confirmation says that run 14 will be marked failed, that marking it as ended does not process its unfinished videos, and that they can be re-queued afterwards. The user confirms. Run 14 is now `failed`, with the reason "ended by user (legacy run)". Its 19,615 saved unevaluated videos, its queue items and its guards are exactly as before, and YouTube received no request. Run 14 no longer offers the action. Pressing it again in a stale browser tab changes nothing and says run 14 has already ended. The user can now re-queue run 14's stranded videos ([BR-037](br-037-requeue-stranded-videos.md)).

## Definitions

- A *legacy run* is a production run (manual, scheduled or queue-retry receiving run, as defined in [BR-035](br-035-stop-active-run.md)) whose mutation-safety version is 0, meaning it was created before guarded playlist sending existed.
- A run is *endable* when all of these hold at that moment:
  - it is a legacy run;
  - its saved status is `running` or `interrupted`;
  - it is not executing now;
  - no production run is active, startup recovery is not in progress, and shutdown has not begun.
- *Active*, *production run* and *unfinished video* have the meanings in BR-035. The run being ended does not count as an active production run merely because its saved status is `running`.

## Acceptance criteria

### Who and which run

1. **The user, for an endable run.** The user SHALL be able to mark an endable run as ended through the administration interface, with the same access that allows Run Now. No other actor (Run Now, the schedule, startup recovery, shutdown, the time limit or queue retries) SHALL mark a run as ended in this sense.
2. **Eligibility is checked when the action is carried out.** The system SHALL check every endable condition again when it carries out the action, not only when it shows the action or the confirmation. A run that has stopped being endable in between SHALL be refused under AC9.

### Confirmation

3. **Named confirmation.** The action SHALL take effect only after the user confirms it for a named run. Before the user confirms, the interface SHALL show the run's identifier, say that the run will be marked failed, and say that marking it as ended does not process its unfinished videos and that they can be re-queued afterwards ([BR-037](br-037-requeue-stranded-videos.md)) (amended 2026-11-11). Cancelling or leaving the confirmation SHALL change nothing. A confirmed action applies only to the run named in it.

### What the action does

4. **Status and reason only.** A successful action SHALL save the run's status as `failed` together with a safe reason saying that the user ended it as a legacy run, for example "ended by user (legacy run)". The status and the reason are saved together or not at all. If they cannot be saved, the run SHALL stay in its previous status, unchanged, and the user SHALL be told that the action did not take effect.
5. **Nothing else changes.** The action SHALL NOT create, change, release, claim, reclaim or remove any of the run's videos, per-video outcomes, route decisions, playlist actions, action evidence, send guards, or queue items in the deferred queue or the Errors queue. The run keeps its identifier, trigger, start time, configuration snapshot, saved phase and counts. An action whose outcome is uncertain stays uncertain and its guard stays. Its unfinished videos are not handed to any queue: unlike Stop and Discard ([BR-035](br-035-stop-active-run.md) AC10–16), this action leaves them unprocessed. After the run is ended, a separate user action can re-queue them ([BR-037](br-037-requeue-stranded-videos.md) AC14).
6. **No YouTube call.** The action SHALL make no YouTube call of any kind, and SHALL NOT start, resume or continue any run.
7. **Shown truthfully.** Run history and run inspection ([BR-028](br-028-historical-run-inspection.md)) SHALL show the run as `failed` with the AC4 reason, so it can be told apart from a run that failed by itself and from a discarded run ([BR-017](br-017-manual-run-initiation.md) AC16). The acknowledgement SHALL identify the run and say that it is now marked failed.
8. **Never resumed; never blocks.** After the action, Run Now, startup recovery, the schedule and queue retries SHALL NOT resume, restart or continue the run. The ended run SHALL NOT make a process start, a run start, a Run Now press or a queue retry fail or be refused.

### Refusals, repeats and races

9. **Refusals.** The system SHALL refuse the action, change nothing and make no YouTube call when any of these applies:
   - the run does not exist;
   - the run's mutation-safety version is 1 or higher;
   - the run is not a production run (a dry run, replay or backtest);
   - the run is already terminal: `completed`, `failed` (including already ended by this action or discarded) or stopped by user;
   - the run is executing now;
   - a production run is active;
   - startup recovery is in progress;
   - shutdown has begun.

   The refusal SHALL identify the run (or the requested identifier, if the run does not exist) and give a safe reason saying which of these applies, with no raw error text, database text, credential or provider payload.
10. **Idempotent.** Repeating the action for the same run, including a double submit or two requests at the same time, SHALL leave exactly the result of one action: one status change and one reason. Every request after the first effective one SHALL change nothing and SHALL be answered with the same run identity, saying that the run has already ended.
11. **Races with run starts.** The action and any production run start, startup recovery or shutdown SHALL behave as if one happened completely before the other. Either the action takes effect first, or it is refused under AC9 with nothing changed. The saved state SHALL never mix the two.

### Showing the action

12. **`endable` on run reads.** Every run read that run history and run inspection use SHALL report, for each run, an `endable` fact that is true exactly when the run is endable at the time of the read. The administration interface SHALL offer the action only for runs whose `endable` fact is true. The fact is advisory: AC2 still decides when the action is carried out, and a stale `true` never makes a run endable.

## Derived from

- Statement, AC1, AC3–6, AC9 (version ≥1 and terminal refusals, active production run, startup recovery, shutdown), AC10, AC12: the user's explicit decision of 2026-11-11, relayed in the specification assignment. The user chose to add a "Mark as ended" action for old, stuck pre-safety-version runs such as live run 14 (`mutation_safety_version=0`, status `running` or `interrupted`), which Stop, Discard, Resume and startup recovery all exclude. The action only changes the status to `failed` with a safe reason such as "ended by user (legacy run)". It must not touch videos, queues, guards, actions or decisions, and must make no YouTube call. The assignment named the confirmation content, the refusals, idempotency and the `endable` fact on run reads. The original human conversation was not independently inspected.
- AC5's "leaves them unprocessed": the user's earlier decision of 2026-11-10 to decline recovering run 14's backlog ([run14-recovery](../run14-recovery/index.md)), restated in this assignment. This BR is the deliberate exception to BR-035 AC16 ("nothing is stranded"), which applies only to stopped and discarded runs. **Amended 2026-11-11:** the user later approved re-queuing stranded videos ([BR-037](br-037-requeue-stranded-videos.md)), which supersedes the decision to leave run 14's backlog alone. Mark as ended itself still changes only the status and reason (AC5 unchanged). The Statement, Example and AC3's confirmation now say that the videos can be re-queued afterwards, so the confirmation does not suggest that they can never be processed.
- Specification choices, made within the assignment:
  - *Production runs only.* Current dry runs, replays and backtests are also saved with mutation-safety version 0 (source check: `internal/state/store.go:98–104` sets version 1 only for guard-ready manual or scheduled runs). Without this limit, an interrupted new simulation would count as "legacy". The user's intent names pre-safety-version production runs, so simulations are refused (AC9).
  - *Not executing now* (endable definition, AC9): a version-0 run cannot be resumed today, but the check keeps the action from ever ending a run that is actually running.
  - The confirmation also says the run will be marked failed (AC3), matching Discard (BR-017 AC14).
  - Status and reason saved together or not at all (AC4), and the recheck at execution (AC2), matching Discard (BR-017 AC13, AC15).
  - *Never blocks* (AC8): an ended run must not leave the system worse off, for example by making a later start or startup fail on a `failed` run that has no end-of-run data from the legacy era.
  - Refusing simulations and runs that are executing, and one serial order for races (AC11), matching BR-017 AC21 and BR-035 AC21.
- `endable` is the user's name for the fact. Its meaning is fixed here; how run reads carry it is an architecture choice.

## Verified by

Use the real server path with migrated SQLite and a controlled YouTube provider that counts requests.

- **Ending a stuck legacy run (AC1, AC4–7):** seed a run-14-shaped version-0 manual run with status `running`, a second version-0 scheduled run with status `interrupted`, unevaluated videos, a pending deferred item, a claimed Errors-queue entry, a retained send guard and an uncertain action. Snapshot every row. End each run. Each run is `failed` with the safe legacy reason, and its other run fields are unchanged. Every video, outcome, decision, action, guard and queue row is byte-for-byte unchanged. The provider records zero requests. Run history and inspection show the reason, distinct from a self-failed run and a discarded run.
- **Confirmation (AC3):** the confirmation names the run and says that it will be marked failed, that marking it as ended does not process its unfinished videos, and that they can be re-queued afterwards. Cancelling or leaving it changes no row.
- **Atomicity (AC4):** inject a failure into the save: the run keeps its earlier status, no reason is saved, and the user is told it did not take effect.
- **Never resumed or blocking (AC8):** after ending, restart the process, press Run Now, let a scheduled run start and retry a queue item. Each succeeds, none resumes the ended run, and startup does not block.
- **Refusals (AC9):** request the action for a missing run, a version-1 `running` run, a version-1 `interrupted` run, a `completed`, `failed`, discarded and stopped run, and an interrupted dry run, replay and backtest. Then request it for an endable run while a production run is active, during startup recovery and after shutdown has begun. Each is refused with its specific safe reason, every row is unchanged and the provider records zero requests.
- **Idempotency and races (AC10–11):** submit the action twice at once, and again after it succeeds: one status change, one reason, and the later answers say "already ended" for the same run. Race the action against a Run Now press and a scheduled start at least 100 times: every result matches one serial order and never a mixture.
- **`endable` (AC2, AC12):** with the seeds above, run reads report `endable` true only for the two stuck legacy runs, and false for every other run. It turns false for all runs while a production run is active and after a run is ended. The interface offers the action only where `endable` is true. Show the confirmation, then start a production run and confirm: the action is refused under AC9.

## Depends on

- [BR-017](br-017-manual-run-initiation.md) (Run Now access, concurrency, discard; AC1, AC7, AC9, AC11)
- [BR-028](br-028-historical-run-inspection.md) (run history and inspection; AC7, AC12)
- [BR-035](br-035-stop-active-run.md) (active and production run definitions, stopped status; AC9)
- [BR-012](br-012-idempotent-playlist-actions.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md) (send guards and uncertain actions stay; AC5)
- [BR-037](br-037-requeue-stranded-videos.md) (re-queuing the ended run's stranded videos afterwards; Statement, AC3, AC5)
