---
type: requirement
title: BR-017 — Manual Run Initiation
description: User can trigger a processing run on demand
tags: [requirement, runs, manual, on-demand, recovery, priority:MUST]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL allow the user to manually initiate a processing run at any time through the administration interface, independent of the configured schedule.

## Acceptance criteria

1. The user can trigger a manual run from the administration interface.
2. A manual run processes the same pipeline as a scheduled run (discovery, evaluation, action execution).
3. Manual runs are recorded in the run history with a manual trigger indicator.
4. The user can inspect the outcome of a manual run.
5. A manual run does not interfere with or reset the configured schedule.
6. The system prevents concurrent runs (manual and scheduled) from executing simultaneously.

### Resuming a manual run stopped by its time limit

A manual run that hits its execution time limit stops as `interrupted` with its work saved. For example, Run Now starts run 30, which stops after its time limit during action execution. The next Run Now press continues run 30 from that checkpoint under its original rules. It does not start run 31.

**Definition.** A *time-limit-interrupted manual run* has all of these properties:

- it was started by Run Now (manual trigger);
- its saved status is `interrupted`;
- its saved interruption reason is that its execution time limit expired;
- it was created with the current guarded mutation-safety and action-authority versions, not version 0 or another legacy version.

7. When the user presses Run Now and at least one time-limit-interrupted manual run exists, the system SHALL resume the **oldest** one instead of starting a new run. Oldest means the earliest saved start time; ties go to the lowest run identifier. The press SHALL NOT create a run or capture a configuration snapshot.
8. Each accepted press SHALL resume exactly one run. If several time-limit-interrupted manual runs exist, the others stay `interrupted` and unchanged, and each later press resumes the next oldest. Run Now starts a new run, as before, only when no time-limit-interrupted manual run remains.
9. The resumed run SHALL keep its run identifier, its original configuration snapshot and its saved progress, and SHALL continue from its saved checkpoint. It SHALL NOT repeat a playlist addition already recorded as successful. It SHALL NOT send a playlist addition to a target that is blocked by a retained send guard: an earlier attempt whose outcome is unknown stays blocked and unresolved, as required by [BR-012](br-012-idempotent-playlist-actions.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md). Resuming does not reconstruct, release or bypass a guard. If the time limit stops the resumed run again, it becomes eligible again with all committed progress kept.
10. Run Now SHALL NOT resume, change or act on any other run, including: version-0 or other legacy runs; runs interrupted for another reason, such as shutdown or process loss; scheduled runs; queue-retry receiving runs; `failed` or `completed` runs; and dry-run, replay or backtest runs. These keep their existing recovery behavior. Startup recovery is unchanged.
11. If the oldest run that meets the definition cannot be continued safely because its configuration snapshot is missing or unreadable, or its saved checkpoint is not recognized, the press SHALL NOT resume it and SHALL NOT start a new run instead. It SHALL report a safe reason that identifies the run, and it SHALL make no YouTube call. The run stays `interrupted` and unchanged.
12. The acknowledgement of a resuming press SHALL identify the resumed run and say that it is a resumption, not a new run. [BR-033](br-033-honest-manual-run-progress.md) progress follows that run. Existing refusals still apply first: concurrent production (AC6), startup recovery in progress and shutdown in progress. A refused press resumes nothing and changes no run. Resuming does not change or reset the schedule (AC5).

## Derived from

- Anchor: youtube-discovery-routing.md (AC17)
- AC7–12: the user's explicit decision of 2026-11-10, relayed in the specification assignment (Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`): “a manual run interrupted by its time limit resumes when the user next presses Run Now”. The assignment also required version-0 and legacy runs to stay excluded and the replay guard to stay intact. The original human conversation was not independently inspected. This answers the open finding in [authorization ADR-007](../../architecture/youtube-authorization/decisions/adr-007-asynchronous-startup-resume-and-shutdown-interruption.md), which noted that such a run otherwise waited for the next process start.
- Specification choices in AC7–12, delegated by the assignment: oldest-first selection, which matches the existing startup-recovery order; one resumed run per press; no new run while a time-limit-interrupted manual run remains; and fail-closed handling of an unusable candidate, which follows BR-011 AC6 and the existing startup rule that corrupt saved state blocks production rather than being skipped silently.

## Verified by

- Unit test: manual run triggers full pipeline.
- Unit test: manual run recorded in history with manual indicator.
- Concurrency test: concurrent run attempt is prevented or queued.
- Time-limit resume (AC7–9): through the registered Run Now path with real SQLite and a controlled provider, let a manual run's time limit expire mid-run. The next press returns the same run ID, creates no run and no snapshot, resumes from the saved checkpoint and uses the original rules despite later rule edits. Seed a target with a retained send guard and one with a recorded success: zero further additions are sent to either.
- Several candidates (AC8): create three time-limit-interrupted manual runs, two with the same start time. Successive presses resume them oldest first, with the lower ID first on the tie, one per press. A new run is created only by the press after the last one finishes.
- Exclusions (AC10): seed version-0, legacy, shutdown-interrupted, scheduled, queue-retry, `failed`, `completed` and simulation runs. A press changes none of them; with no eligible run it starts a new run as before.
- Unusable candidate (AC11): a missing snapshot or an unrecognized checkpoint on the oldest candidate yields a safe reported refusal, no new run, no YouTube call and an unchanged row.
- Acknowledgement and refusals (AC12): the acknowledgement names the resumed run as a resumption; a press during an active run, startup recovery or shutdown is refused and resumes nothing; the next scheduled time is unchanged.

## Depends on

- BR-011 (processing state persistence)
