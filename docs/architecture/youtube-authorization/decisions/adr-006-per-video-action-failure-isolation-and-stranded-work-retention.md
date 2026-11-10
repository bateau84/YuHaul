---
type: decision
title: ADR-006 — Per-Video Action Failure Isolation and Stranded-Work Retention
description: A handled per-video failure, including a guard-blocked target, is recorded and retained while independent videos continue; a run-stopping failure hands every unfinished video to a retained sink before the run becomes failed.
tags: [architecture, pipeline, failure-semantics, work-preservation, idempotency, recovery]
timestamp: 2026-11-09T00:00:00Z
---

**Status:** decided technical contract. Implemented 2026-11-09 with the ten required tests (`internal/pipeline/failure_isolation_test.go`, `integration` build tag). Independent final review (round 4, 2026-11-09; reviewer session `ses_e3e8dc3feffeedsw0eB59kxvM3`, Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`) verified this decision, with ADR-005 and ADR-007 and their follow-up fixes, on the assembled binary using child-process kill and SIGTERM tests. It found no material defect. Required tests 2, 3, 5, 6, 8, 9 and 10 are integration-only: they run under the `integration` build tag and have no default-suite proof. Cancellation handling (§2 case 1 and its `interrupted` outcome) belongs to [ADR-007](adr-007-asynchronous-startup-resume-and-shutdown-interruption.md) and was implemented with it on 2026-11-09: `failRun` now interrupts a run whose context ended, with no sweep. This decision does not authorize live operations.

**Realization notes.**

- The executor tags handled failures (`videoActionFailure`, or a `mutationFailure` with no accounting error). Any other error, including every auxiliary accounting failure, stops the run. The pipeline passes the executor a marker so the executor does not write the sink. The pipeline writes the failed lifecycle, outcome and the one active `error_entry` for (video, run) in one `WithRetryTx` transaction.
- `retainThenFail` (`authority.go`) is the shared "retain, then `FailRun`; otherwise `InterruptRun`" helper. Authority failures and the new `sweepStrandedWork` both use it. The panic recovery path in `Execute` uses it too.
- `sweepStrandedWork` treats a retry-claimed video as one whose deferred or error rows carry `retry_run_id` = this run, in any status. Those rows are returned to `pending`/`active` through `RequeueRetryGroupTx`, which keeps `retry_run_id` as the most-recent-owner marker (the queue's existing convention). That marker is what makes a repeated sweep a no-op and keeps a removed group removed. Other unfinished videos get a runtime sink through `queue.RetainRunSinkTx`, which reuses any active entry for (video, run).
- After a confirmed success followed by an accounting failure, the pipeline commits `accepted` for a video whose actions have all succeeded, then stops the run. A video with an action still outstanding stays unfinished and is swept.
- A resumed run skips a video already `failed` with a committed `failed` run outcome.
- **2026-11-10 (implemented, review pending):** a user Stop or Discard does **not** use this sweep. It hands unfinished work to the deferred queue instead ([run-initiation ADR-004](../../run-initiation/decisions/adr-004-stop-active-run-and-deferred-handoff.md), BR-035 AC10–16, BR-017 AC17 as amended). The sweep's unfinished-video query is extracted into `listUnfinishedRunVideosTx` and shared, with no behavior change. Run-stopping failures still retain to the Errors queue as §3 says.

A production run no longer stops at the first video whose playlist action fails or is blocked by a send guard. That video is recorded as `failed`, with an active Error Queue entry, in one transaction. The run then continues with the other videos. A run stops only when the run itself cannot safely continue, for example because a database write fails, accounting fails, the snapshot authority is invalid, or the process is shutting down. Before the run becomes terminal `failed`, every video it has not finished is handed to a retained queue. If that handoff cannot commit, the run stays resumable (`interrupted`) instead.

For example, a run has videos A, B and C matched to playlist P. Video A's insertion into P has an unresolved send guard from an earlier crash. A's action is recorded `not_attempted` / `mutation_application_uncertain`, and A becomes `failed` with an active runtime error entry. B and C are inserted normally, and the run completes with `VideosFailed=1`. A's guard still prevents any POST of A to P, including from a retry of its error entry.

## Derived from

- [BR-030 — Temporary Failure Work Preservation](../../../requirements/yuhaul/br-030-temporary-failure-work-preservation.md), AC1, AC2, AC5 and AC6.
- [OC-001 — YouTube API Quota and Failure Semantics](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md): a transient error retains the item and "other work in the run continues if independent", and a permanent error sends the item to the Error Queue while "other work continues".
- [BR-012 — Idempotent Playlist Actions](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md) and [OC-006 — Refresh Authorization and Call Safety](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md) G4–5: no blind replay of a mutation whose application is uncertain.
- [ADR-002 — Failed-Phase Runs Terminate as failed](../../adr-002-run-status-failed-terminal-state.md): terminal `failed` versus resumable `interrupted`. The retention-before-failure rule already applies to authority failures.
- [ARS-001 — Authorization State, Dispatch and Send Guard](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md): guard evidence, primary versus auxiliary failure, and handoff composition.
- [ADR-002 — Exact Retry Group Receipts](adr-002-exact-retry-group-receipts.md) and [ADR-005 — Startup Recovery for Guarded Production Runs](adr-005-startup-recovery-for-guarded-production-runs.md). This decision replaces ADR-005's phrase "Other independently eligible work follows existing semantics".
- Reviewer session `ses_e3e8dc3feffeedsw0eB59kxvM3`, Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`.

## Satisfies

| Authority | Disposition |
|---|---|
| BR-030 AC2, AC5; OC-001 transient/permanent | Satisfied. A failed or blocked video is retained, and independent videos continue. |
| BR-030 AC1, AC6 | Satisfied. A run-stopping failure retains all unfinished work before it becomes terminal. Otherwise the run stays resumable, and the retained rows are inspectable. |
| BR-012; OC-006 G4–5; ARS-001 guard rules | Preserved. No guard is released, no POST is repeated, and a retained sink grants no send permission. |
| ADR-002 run status | Preserved, with one clarification: `failed` means that a run-level phase failed. A per-video failure inside a completed phase is a video outcome, not a run failure. |
| BR-032 | Preserved. Per-video `failed` remains distinct, and run counters report it. |

## Decision question

When one video's action phase ends in failure, should the run stop, and what happens to the videos it has not yet processed?

## Source findings (read 2026-11-09)

- `internal/pipeline/pipeline.go:731-741` `phaseActionExecution` returns on the first `executeVideoActions` error. `Execute` then calls `failRun` with "action execution phase failed".
- `executeVideoActions` (`pipeline.go:858-871`) records the failing video as `failed` and returns an error for every executor failure, including a guard block (`executor.go:658-664`) and ordinary provider failures.
- For a non-authority error, `failRun` (`pipeline.go:476-508`) calls `finalizeFailedRun` directly. Videos still in `evaluated`/`acted` stay with no outcome and no queue row in a terminal run that is never resumed. Deferred groups claimed by `StartRun` stay `retrying` and owned by that failed run. This is the stranding.
- A guard block found during a quota deferral (`executor.go:398-461`) returns an error without `retainActionFailure`. The video then becomes `failed` with no retained sink.
- `retainAuthorityFailure` (`authority.go:239-290`) already applies "retain all unfinished, then fail; if retention fails, interrupt" for authority errors.
- The retry path (`retry.go`, `processRetryVideo`) already commits a per-video failure with its sink through `FinishRetryFailureTx` and returns nil, so the retry loop continues.

## Options considered

| Option | Benefits | Costs and failure modes |
|---|---|---|
| **A. Isolate handled per-video failure; retain unfinished work on run-stopping failure (chosen)** | Meets OC-001's explicit "other work continues". Reuses the existing retry-path and authority-retention patterns. No schema change. | Requires a precise handled versus run-stopping classification. A completed run can now contain failed videos, so the run status alone no longer signals video failures. |
| B. Keep stop-on-first-failure and only add the stranded-work sweep | Smallest change in the loop. | One permanently guarded video blocks every later run's remaining work again and again. This contradicts OC-001 "other work continues" and BR-030 AC2 for independent items. Rejected. |
| C. Stop the run as `interrupted` instead of `failed` | No sweep needed. | A deterministic block (an unresolved guard) re-fails on every resume, so work never moves forward. It also misreports an unrecoverable condition (ADR-002 Option B). Rejected. |
| D. Continue past any error, including storage and accounting failures | Maximum progress. | Breaks ARS-001's rule that an accounting failure is a run failure. It can leave a video with no committed disposition and hides local corruption. Rejected. |

## Decision and exact contract

### 1. Handled per-video outcome: record, retain, continue

For a fresh-origin production video, `executeVideoActions` returns a **handled** result with a nil error, and `phaseActionExecution` continues to the next video, only when this video's disposition for the current run has committed:

| Outcome | Required committed state |
|---|---|
| accepted | Existing `markVideoAccepted` transaction. |
| deferred | Existing deferral with a pending deferred sink. This is unchanged. |
| failed | The run-video `failed` lifecycle and outcome, **and** one active `error_entry` for (video, this run), written in **one** writer transaction. The category and bounded detail follow the existing `retainActionFailure` classification. |

The `failed` row applies to every action-level failure of this video: permanent or transient provider failure, an admitted uncertain mutation with no auxiliary failure, a guard block found at claim, **and** a guard block found during a quota deferral. Action evidence keeps its existing rules. A blocked target is recorded `not_attempted` / `mutation_application_uncertain` with no `executed_at`, and an earlier attempted `failed` row is never downgraded. The video's later actions are recorded `not_attempted` / `prior_action_failed`. A video never continues with its own remaining targets after one of its actions fails.

If an `error_entry` is already active for this video **and this run**, reuse it. Never create a second active sink for the same (video, run). An active sink from an earlier run is not reused as this run's sink.

### 2. Run-stopping failure: stop the phase

Return a non-nil error and stop the run in these cases:

1. The execution context is done (`ctx.Err() != nil`). Follow [ADR-007](adr-007-asynchronous-startup-resume-and-shutdown-interruption.md) §3: the run becomes `interrupted`, and this decision's sweep does not run. `executeVideoActions` does not write a new `failed` lifecycle for the cancelled video. Action and guard evidence that the executor has already committed under ARS-001 stays. The video stays `acted`/`evaluated` and is reprocessed on resume, where an unresolved guard blocks it under §1.
2. `authorityError`. The existing `retainAuthorityFailure` handles it, unchanged.
3. An auxiliary accounting failure under ARS-001: `postCommitAccountingError`, a `mutationFailure` with a non-nil accounting error, or success followed by an accounting failure. The video's own already-committed disposition stays as it is.
4. Any failure to read run authority or decisions, or to commit the §1 disposition and sink transaction.
5. A failed guarded handoff composition (`guardedRetryHandoffError`, or a rolled-back auth/quota handoff).

In the retry path, cases 3 and 5 keep their existing `processRetryVideo` behaviour. This decision does not change it.

### 3. Stranded-work retention before terminal failure

For every production run-stopping failure in any phase, except cases 2.1 and 2.2, `failRun` must first run a retention sweep:

1. **Select unfinished members:** take the run's candidate set (`listRunVideos`). Exclude videos whose processing state is `completed`, and exclude videos that already have a committed final outcome for this run (accepted, rejected, excluded, deferred or failed).
2. **Video owned by a live retry receipt of this run** (`deferred_item`/`error_entry` `status='retrying'` with `retry_run_id` = this run): return the still-owned group to `pending` through the existing exact-receipt requeue. Preserve the original IDs, `run_id`, reasons and history. Do not create a new runtime sink for it. A removed group stays removed.
3. **Any other unfinished video:** keep one active runtime `error_entry` for (video, this run), with the bounded `diag.Reason` of the stopping cause.
4. Commit all of steps 2–3 in **one** writer transaction (`WithRetryTx`). If the first attempt fails, retry once under `runFinalizationContext`. Call `FailRun` only after this transaction commits. If retention still fails, call `InterruptRun`, which leaves the run resumable, and never `FailRun`. This is the same order as `retainAuthorityFailure`. Implement both with one shared helper.

The sweep makes **no provider calls and does not touch `playlist_send_guard`**. It writes no action evidence, increments no counters other than through the existing recompute in `FailRun`, and creates no attempt timestamps.

### 4. Run status

If every phase returns nil, the run is `completed`, even when some videos are `failed`. The recomputed counters (`VideosFailed`) and the per-video outcomes report them. `failed` is used only after §2 cases 3–5 or another existing phase failure, and only after §3 commits.

### 5. Replay-safety invariants (unchanged and binding)

- A guard is removed only by its owning live invocation under ARS-001. Continuing the run, the sweep, a queue retry, dismissal and reconnection never remove it.
- Two videos are independent when their (`video_id`, `target_playlist_id`, `action_type`) guard keys differ. Every target is still checked through its own claim.
- A retry of a retained guard-blocked entry goes through the normal claim. It is blocked again and records `not_attempted` with zero POSTs. The error queue never becomes a bypass.
- Quota admission stays per action (`CanAfford`, final shared admission). Continuing to other videos spends nothing that the run would not have spent without the failure.

## Consequences

The immediate benefit: one stuck target no longer starves every independent video, and no failed run leaves work without a queue row. The costs are:

- One classification rule and one shared retention helper.
- The atomic failed-plus-sink write for fresh-origin failures.
- Operators must read the run counters or per-video outcomes, not only the run status, to see per-video failures.

Each run that matches a permanently guarded video adds one retained entry for it. This is visible, bounded per (video, run), and intentional: the uncertainty stays inspectable. Nothing resolves guards automatically.

## Required tests

Use a disposable SQLite database with the real pipeline, executor, queue and state stores and a controlled fake YouTube endpoint. Do not use live credentials.

1. **Guard-blocked continues:** three matched videos A, B, C, with a guard pre-seeded for (A, P). Result: A `failed`, one active `error_entry` (A, run), action `not_attempted`/`mutation_application_uncertain`, `executed_at` NULL. B and C `accepted`. Zero POSTs for (A, P). The guard row is unchanged. The run is `completed` with `VideosFailed=1`.
2. **Blocked during quota deferral:** quota exhausted with a guard on (A, P). A `failed` with an active sink; other videos `deferred` with sinks; no video without a disposition.
3. **Permanent and transient provider failure** on B (a permanent 4xx rejection, then separately a 5xx): B `failed` with an active sink; A and C proceed. One POST each, no replay.
4. **Admitted uncertain mutation (no accounting failure)** on A: the guard is kept, the action stays `failed`/`mutation_application_uncertain` with the attempt time, the run continues, and a same-run resume never downgrades the action row.
5. **Atomicity:** inject a failure into the failed-plus-sink transaction. Neither row commits. The run stops through §3: the video becomes an unfinished member and is retained, and the run is `failed`. Inject sink-sweep failure as well: the run is `interrupted`, `FailRun` is not called, and the candidates are still owned and resumable.
6. **Accounting failure** after a known success on B: B stays `accepted` and its guard is removed, the run stops, C (not yet processed) has an active runtime sink, and the run is `failed`.
7. **Retry-owned stranding:** `StartRun` claims a deferred group, then the binding or discovery fails. The group returns to `pending` with its original IDs, `run_id`, reason and history. No duplicate sink is created. A removed group stays removed.
8. **No resurrection or duplication:** run the sweep twice through crash and restart. One active sink per (video, run), no counters changed twice.
9. **Retry of a retained guard-blocked entry:** zero POSTs, blocked again, new `not_attempted` evidence for the receiving run, the original run's history unchanged, and the guard unchanged.
10. **Mutation guard on a removed or dismissed entry:** a later run's claim for (A, P) is still blocked.

## Reconsideration

Revisit if OC-001 or BR-030 changes so that the first failure must halt a run, or if per-video failure needs its own run-level status. In both cases a product owner must decide first.
