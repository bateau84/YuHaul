---
type: decision
title: ADR-004 — Stop an Active Run and Hand Unfinished Work to the Deferred Queue
description: Stop a run by durably marking it and cancelling its execution context with a stop cause; settle it as a new terminal status `stopped` in one transaction with a deferred-queue handoff that Discard shares; startup settles crashed stops and no longer blocks on an unusable time-limit Run Now run.
tags: [architecture, decision, runs, stop, discard, deferred-queue, recovery, send-guard, concurrency, migration]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — technical realization of [BR-035](../../../requirements/yuhaul/br-035-stop-active-run.md), [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md) AC11 (startup rule), AC17 (amended) and AC22, and [BR-011](../../../requirements/yuhaul/br-011-processing-state-persistence.md) AC7. Implemented, review pending (2026-11-10; deviations in [ARS-003 §9](../specifications/ars-003-stop-run-and-deferred-handoff.md#9-implementation-record-and-deviations)). The exact contract is [ARS-003](../specifications/ars-003-stop-run-and-deferred-handoff.md). Discard ([ADR-003](adr-003-discard-unusable-time-limit-run.md), [ARS-002](../specifications/ars-002-discard-unusable-time-limit-run.md)) is amended to use the handoff defined here. Independent review of the implementation is pending.

## Summary

**Stop.** `POST /api/runs/{id}/stop` finds the run's in-process *run control*, writes a durable `stop_requested_at` marker on the run row, and cancels the run's execution context with the cause `ErrRunStoppedByUser`. From that moment no new YouTube request starts. The existing ADR-007 cancellation rules decide each playlist addition: one not yet sent has its guard released with a `not_attempted` row; one already sent stays uncertain and keeps its guard. When the executor unwinds, still holding the production lock, it *settles* the stop in one SQLite transaction. The run becomes the new terminal status `stopped`, and every unfinished video is handed to the deferred queue, eligible at once.

**Handoff.** One transaction-scoped function, `handOffUnfinishedTx`, is used by both Stop and Discard. It releases every queue claim the ended run holds: deferred items go back to `pending`, and claimed Errors-queue entries become `resolved`. It clears any waiting time on the video's pending deferred items. It creates one new pending deferred item only where the video has none and the user did not remove one. The next new production run already claims every eligible pending item; this ADR also makes a `stopped` claim owner reclaimable, so a stopped run never blocks a start.

**Startup.** Startup settles any run that carries a stop marker instead of resuming it. It leaves an unusable time-limit Run Now run `interrupted`, logs it, and keeps starting. Every other invalid row still blocks startup.

Example (BR-035): scheduled run 41 is in action execution. The user stops it. One POST is in flight; a second addition has its guard claimed but is not dispatched. The server answers `202 {"run_id":41,"status":"running","stop":"accepted"}`. The cancel makes the unsent addition release its guard (`not_attempted`, no attempt time). The in-flight POST is recorded as `failed`/`mutation_application_uncertain` and keeps its guard. The executor returns and settles: run 41 becomes `stopped`. Its 9 unfinished videos each have one pending deferred item with `retry_after` NULL. The next scheduled run claims all 9, adds the previously unsent video, and is blocked by the guard for the uncertain one (ADR-006 §1 records it as a handled failure).

## Satisfies

| Requirement | How |
|---|---|
| BR-035 AC1, AC4 | Every execution entry point (production `executeHeld`, `RetryQueueEntry`, `executeHistorical`) registers a run control. Only the stop handler calls `StopRun`. No other path sets `stop_requested_at` or the `ErrRunStoppedByUser` cause. |
| BR-035 AC2 | Server side: the request names one run in the path. Confirmation copy belongs to the Designer. |
| BR-035 AC3 | `202` acknowledgement with the run ID. Run reads expose `stop_requested_at`; `status='running'` together with a marker reads as *stopping*, and `stopped` after settling. |
| BR-035 AC5 | New status value `stopped` (migration 025). Every terminal check includes it. Statistics count only `completed`, and no reader counts `stopped` as `failed`. No resume path selects it. Settling writes only status, reason, `completed_at`, duration, quota and counters. |
| BR-035 AC6 | The cancel happens before the acknowledgement, and every provider call already observes the execution context (ADR-007 §3.2). |
| BR-035 AC7–8 | Unchanged ADR-007 §3.2 rules: a claimed-but-not-admitted guard is released with `not_attempted`; an admitted POST is recorded as uncertain and its guard is kept. A result that the executor records before settling stands. |
| BR-035 AC9 | The marker commits before the cancel. Startup classification settles every marked, unsettled row, with the same handoff, and never resumes it. |
| BR-035 AC10–16; BR-017 AC17 | `handOffUnfinishedTx` (Decision 2), plus a reclaimable `stopped` owner in `retryOwner` and claim eligibility. Discard calls the same function with the discard reason. |
| BR-035 AC17 | A simulation settles to `stopped` with no queue, guard or action write. Counters are recomputed from decisions. |
| BR-035 AC18–21 | Per-run control arbitration (Decision 3) plus a guarded marker `UPDATE` and a database trigger backstop. Stop is not gated by startup recovery. |
| BR-017 AC11 (start), AC10; BR-011 AC7 | Startup classification skips, logs and leaves unchanged any unusable time-limit Run Now row. All other checks still block (Decision 5; ADR-005 amendment). |
| BR-017 AC22 | Run reads compute `discardable` with the same selector and validator as Discard (Decision 6). |

Satisfied elsewhere: guard and uncertainty semantics (authorization ARS-001, ADR-007 §3.2); handled per-video failure in the next run (ADR-006 §1); Run Now resume (ADR-001); confirmation and progress copy (Designer).

## Current source (read 2026-11-10)

- `internal/store/migrations/001_initial_schema.up.sql:19`: `run.status CHECK(status IN ('running','completed','interrupted','failed'))`. Twelve foreign keys reference `run`, and `deferred_item.run_id` is `ON DELETE CASCADE`. `020_…up.sql:6`: `deferral_reason CHECK IN ('quota_exhausted','api_rate_limited','authorization_pending')`. No table references `deferred_item`.
- `internal/store/store.go:75–93`: `ApplyMigrations` runs golang-migrate's `database/sqlite` driver with `&sqlite.Config{}`, so each file runs inside one transaction. `PRAGMA foreign_keys` cannot change inside a transaction, so the standard table rebuild of `run` cannot run safely as a migration file: `DROP TABLE run` with foreign keys on would cascade-delete `deferred_item` rows.
- `internal/queue/retry.go:196–205, 463–469`: claim eligibility is `pending` with `retry_after` due, or `retrying` owned by NULL or a `failed` run. `retryOwner` (`:261–289`) treats a `failed` owner as reclaimable and any other ended owner as `ErrRetryOwnership`. `createClaimedRunLocked` (`internal/pipeline/pipeline.go:236–259`) returns that error, so the whole start fails.
- `internal/pipeline/authority.go:305–369`: `sweepStrandedWork` requeues retry-owned groups and sends every other unfinished video to the Errors queue (`RetainRunSinkTx`, `:357`).
- `internal/pipeline/pipeline.go:308–338`: `executeHeld` treats only `completed` and `failed` as terminal. Production terminal writers: `failRun` (`:526–560`), `retainThenFail`, `retainAuthorityFailure`, `interruptRunWithCause`, `completeRun`, `finalizeFailedRun`. Simulations: `executeHistorical`, `completeHistoricalRun`, `failHistoricalRun` (`replay.go:123–312`) and `ExecuteDryRun` (`dryrun.go:61`).
- `internal/pipeline/executor.go:695–770, 911`: a done context before admission yields `DispatchRefusalCanceled`, and `releaseCanceledClaim` removes the guard with a `not_attempted` row. After admission the attempt is recorded as uncertain (ADR-007 realization notes).
- `internal/state/playlist_guard.go:27`: "Persisted guards are uncertainty, never proof of an attempt." A guard row carries no admission marker.
- `internal/pipeline/startup_recovery.go:72–145`: one invalid row returns `startupCheckError`, and `cmd/server/main.go:221` exits with status 1.
- `internal/server/server.go:191–199`: `trackRun` refuses once shutdown has begun. Simulations, Run Now and queue retries run in goroutines tracked by `runWG`.
- `internal/stats/store.go:386, 1049`: statistics select only `status = 'completed'`. No Go reader counts `status = 'failed'` [grep of `internal`, `cmd`].
- `web/src/types.ts:86`: `RunStatus = 'running' | 'completed' | 'failed' | 'interrupted'`.

## Decision 1 — a real `stopped` status, widened by a guarded schema patch

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **New status value `stopped`; migration 025 SQL adds the marker column, triggers and deferred reasons; a Go schema step in `ApplyMigrations` widens the `run.status` CHECK with SQLite's documented `writable_schema` procedure (chosen)** | Truthful storage: every reader sees a distinct value, and no `failed` count can include stopped runs by accident. O(1); no data copy; foreign keys untouched. | Two mechanisms (SQL file plus one idempotent Go step). `writable_schema` must be allowed by modernc [unverified]: a spike test is the first implementation task. | **Chosen.** |
| Rebuild `run` with the 12-step procedure on a dedicated connection with `foreign_keys=OFF` | Fully standard | Copies every run row; recreates six triggers and the indexes; one mistake cascades into deleting queue rows. Still needs a Go step outside golang-migrate. | **Fallback** if the spike shows `writable_schema` is blocked. |
| Set golang-migrate `NoTxWrap` and rebuild in SQL | One mechanism | Global switch: every earlier migration runs without a transaction on fresh databases. | Rejected. |
| Store `status='failed'` plus a `stopped_at` marker, and project "stopped" in the API | No CHECK change; already terminal and reclaimable everywhere | Silent wrong answers: any present or future `status='failed'` count or filter includes stopped runs (violates AC5). | Rejected. |

The schema step runs at the end of `ApplyMigrations`, after `m.Up()`. It is a no-op when the `run` definition already admits `'stopped'`. It runs in one `BEGIN IMMEDIATE` transaction:

1. read `PRAGMA schema_version`;
2. switch `writable_schema` on;
3. rewrite the exact CHECK text, requiring exactly one row changed and the old text present;
4. set `schema_version` to the old value plus 1;
5. switch `writable_schema` off;
6. commit.

It then requires `PRAGMA integrity_check` = `ok`. Until it has run, inserting `stopped` fails the CHECK loudly; nothing is silently wrong.

## Decision 2 — one deferred-queue handoff for Stop and Discard

`handOffUnfinishedTx(ctx, tx, runID, reason)` runs inside the caller's transaction, with no provider call and no guard or action write. It does the following, in order:

1. **Release every claim of the ended run.** Every `deferred_item` with `status='retrying'` and `retry_run_id=R` becomes `pending` with `retry_after=NULL`. Every `error_entry` with `status='retrying'` and `retry_run_id=R` becomes `resolved` with `resolved_at` set. `retry_run_id` stays as the most-recent-owner marker (queue convention). This covers all of R's claims, not only unfinished videos, so AC13 holds even for inconsistent leftovers.
2. **Unfinished set.** This is ADR-006 §3's definition, extracted unchanged from `sweepStrandedWork` into `listUnfinishedRunVideosTx`: the candidate set of R, not `completed`, and no committed final outcome for R.
3. **For each unfinished video V:**
   - make every `pending` deferred item of V eligible now (`retry_after=NULL`);
   - *removed*: V has a `deferred_item` with `status='discarded'`, or an `error_entry` with `status='dismissed'`, whose `retry_run_id=R`. A removed video gets nothing new (AC12);
   - otherwise, if V has no `pending` deferred item, insert one: `run_id=R`, `deferral_reason=reason`, `retry_after=NULL`, `retry_count=0`, `status='pending'`.

`reason` is `stopped_by_user` or `discarded_by_user`, two new `deferral_reason` values. Reused items keep their identity, origin, reason, history and count (AC10). The one write a reused item gets is `retry_after=NULL`, which AC13 requires.

| Option | Verdict |
|---|---|
| **New handoff to the deferred queue (chosen)** | Required: BR-035 AC10–16 and the amended BR-017 AC17. |
| Reuse `sweepStrandedWork` | Rejected: it puts unclaimed unfinished videos in the Errors queue (`authority.go:357`), which no run start claims, and it leaves claimed error entries active. |
| Change `sweepStrandedWork` itself | Rejected: ADR-006's run-stopping failures still go to the Errors queue by accepted requirement (BR-030, OC-001). Only the unfinished-set query is shared. |

**The next run claims the work (AC14–15).** `createClaimedRunLocked` already claims every candidate that `ListDeferredRetryCandidates` returns, and those videos enter the run through their `run_video_outcome` membership row, even though discovery does not report known videos. With `retry_after` NULL and no live owner, every handed-over item is a candidate. Change: `retryOwner`, `claimRetryGroupTx`'s eligibility query and `ListDeferredRetryCandidates` treat an owner with status `stopped` exactly like `failed`, as reclaimable. A claim held by a `completed` run stays an integrity error (see Findings).

**Atomic with the terminal write.** For Stop: handoff, `status='stopped'`, reason, counters, in one transaction. For Discard: handoff, `status='failed'`, discard reason, counters, in one transaction (ARS-002 §2.4, amended).

## Decision 3 — the stop mechanism and its arbitration

**Run control.** `Pipeline.runs` is a mutex-protected map from run ID to `*runControl`, holding `mu`, `cancel context.CancelCauseFunc`, `stopRequested`, `finalizing`. A run is registered where it becomes `running` in this process:

- `createClaimedRunLocked` (Run Now new runs and scheduled runs);
- `AdmitRunNow`'s resume branch;
- `RetryQueueEntry` after its creation commit;
- `DryRun`, `Replay` and `Backtest` after their insert;
- `executeHeld` before `ResumeRun` (startup recovery).

The executor *attaches* its context with `ctx, cancel := context.WithCancelCause(ctx)`. If a stop was requested before the attach, the attach returns a context that is already cancelled, and the executor goes straight to settling. The run is unregistered after settling or after the normal terminal write, and by `RunNowAdmission.Release`.

**Accepting a stop** (`StopRun`):

1. Read the row: missing → `run_not_found`; `completed`/`failed`/`stopped` → `run_ended`.
2. Look up the control. No control → `run_not_active` (`not_executing`), or `already_stopping` if the row is `running` with a marker.
3. Under `ctl.mu`: `finalizing` → `run_not_active` (`ending`); `stopRequested` → `already_stopping`.
4. Otherwise run the guarded `UPDATE run SET stop_requested_at=? WHERE id=? AND status='running' AND stop_requested_at IS NULL`, which must change exactly one row.
5. Then set `stopRequested`, call `cancel(ErrRunStoppedByUser)`, release `ctl.mu`, and acknowledge.

A marker write error changes nothing, and the run continues. The stop *takes effect* at the cancel. A request admitted before it is "already sent" (AC8); none can start after it (AC6).

**Run ends by itself (AC21).** Every terminal writer first calls `ctl.beginFinalize()`, which sets `finalizing` under `ctl.mu` and returns false if `stopRequested` is set. The writers are:

- `retainThenFail` (and through it `retainAuthorityFailure` and the panic path);
- `interruptRunWithCause` (the shutdown and time-limit paths);
- `completeRun` and `finalizeFailedRun`;
- `completeHistoricalRun` and `failHistoricalRun`, and the dry-run equivalents.

On false the writer does nothing, and the executor's deferred finalizer settles the stop. The two flags are taken under one mutex, so exactly one side wins and the saved state never mixes the two. Backstops:

- the store's terminal `UPDATE`s add `AND stop_requested_at IS NULL`;
- trigger `run_stop_requested_status` aborts any status change of a marked row to anything except `stopped`.

**Settling.** The executor's deferred finalizer settles the stop. For production runs it runs inside `executeHeld`/`RetryQueueEntry` while `productionMu` is still held, so the handoff is committed before any later start or discard can run (AC14).

- Production: `settleStopTx` = recheck (`status='running'`, marker set) + `handOffUnfinishedTx(…, stopped_by_user)` + `UPDATE … status='stopped'` + counter recompute.
- Simulation: the same status write and `RecomputeRunCountersFromDecisions` only.

The write uses `runFinalizationContext` and is retried once. If it still fails, the row stays `running` with its marker ("stopping"), and startup settles it.

**Guards (AC7–8).** Nothing new: ADR-007 §3.2 applies to any done context, and the stop cause is only a cause. `interruptCauseFor` returns `""` for `ErrRunStoppedByUser`, so a stop can never be written as `interrupt_cause='shutdown'`.

**Crash (AC9).** The marker is committed before the cancel and before the acknowledgement. A guard left by a crash has no admission proof, so it is kept as uncertain, as ADR-005 already does for any crashed run. A not-yet-sent addition interrupted by a *crash*, not by the in-process cancel, therefore keeps its guard (Findings).

| Option | Verdict |
|---|---|
| **Durable marker + cause-tagged cancel + in-memory arbitration + settle on unwind (chosen)** | Reuses ADR-007's cancellation and guard rules unchanged; one process, one writer. |
| Settle from the HTTP handler while the executor is still running | Rejected: the handoff would race the executor's own dispositions and claims. |
| A durable lease or state machine column for "finalizing" | Rejected: the in-memory control is authoritative in the single-process model (ADR-001 revisit trigger). The marker alone is enough for crash survival. |

## Decision 4 — refusals, shutdown and startup interplay

- **Shutdown:** the handler calls `trackRun()` first, so it refuses with `503` once shutdown begins, and `WaitRuns` waits for an in-flight marker write. A stop accepted before shutdown settles under the detached finalization context, or at the next start.
- **Startup recovery:** the stop endpoint is not gated by the producer gate (AC19). The recovery worker's current run is registered and stoppable, and the worker then continues with its next run. A classified run that the worker has not reached is `interrupted` and has no control, so it is refused `run_not_active` (AC19).
- **Production lock:** `StopRun` takes neither `initiationMu` nor `productionMu`, so a stop is never delayed by the run it stops. Settling runs under the lock the executor already holds.

## Decision 5 — startup classification (amends ADR-005)

In `classifyStartupRuns`' transaction, before today's per-row loop:

1. **Settle marked rows.** Select every row with `stop_requested_at IS NOT NULL AND status IN ('running','interrupted')`, of any run type. Settle each: `settleStopTx` for production, the status-only write for simulations. They are never returned for resume.
2. **Unusable time-limit Run Now rows.** In the loop, a row that fails a check — the terminal-timestamp check or `validateResumableRow` — is *skipped* if it is `run_type='manual' AND initiated_by='run_now' AND status='interrupted' AND interrupt_cause='time_limit' AND mutation_safety_version=1 AND action_authority_version=1`. That applies whether or not it is the oldest. Startup logs `startup recovery left unusable run` with `run_id`, the closed discard `cause` and the validator `reason`, and does not change the row.
3. Every other failing row still returns `startupCheckError` and blocks startup (BR-011 AC7).

The query adds `initiated_by`, `interrupt_cause` and `stop_requested_at`. Usable time-limit rows are still resumed by startup, unchanged.

## Decision 6 — the discardable fact (BR-017 AC22)

`Pipeline.DiscardableRun(ctx) (runID int64, cause string, ok bool, err error)` runs `selectRunNowCandidateTx` + `validateResumableRow` in a read-only transaction, without locks. `GET /api/runs/{id}` and every item of `GET /api/runs` carry `"discardable": true|false|null` (null = could not be determined) and `"discard_cause"` when true. The list computes it once per request. At most one run is ever `true`, by construction. The field is advisory: `DiscardRun` rechecks.

## Rules the implementation must preserve

1. Only `StopRun` writes `stop_requested_at` or cancels with `ErrRunStoppedByUser`; only `settleStopTx` and the simulation settle write `status='stopped'`.
2. The marker commits before the cancel; the cancel happens before the acknowledgement.
3. Stop-versus-own-end arbitration happens only through `runControl` (`stopRequested` vs. `finalizing` under one mutex); the store guards and the trigger are backstops.
4. Production stop settling happens while `productionMu` is held by the executing run.
5. `handOffUnfinishedTx` is the only path that hands work to the deferred queue for an ended run; it makes no provider call and touches no guard or action row.
6. A `stopped` row is terminal everywhere: `executeHeld`'s re-entry check, startup, Run Now, the receipt validator, and claim ownership, where it is reclaimable.
7. Startup's skip rule applies only to the exact time-limit Run Now predicate; every other invalid row still blocks.

## Findings

| Finding | Disposition | Owner | Blocks? |
|---|---|---|---|
| A prepared-but-unsent addition interrupted by a **crash** after an acknowledged stop keeps its guard: a persisted guard carries no proof that it was not sent. BR-035 AC9 asks for "the same result as AC7". | Fail closed under OC-006: the guard is treated as uncertain, exactly as ADR-005 does for any crash. AC7 holds for every in-process stop. Revisit only with an admission marker on the guard row, which is new machinery. | Requirements (confirm the reading) | No |
| AC10 says "exactly one pending item". A video may already have several pending deferred siblings from different origins (a pre-existing queue shape). | The handoff never creates a second item; siblings are claimed as one group (`claimRetryGroupTx`). Existing siblings are not merged, because merging would lose identity, which AC10 also protects. | Requirements (confirm) | No |
| A claim held by a **completed** run still makes every start fail (`retry.go:283`). | Unchanged: it signals an integrity defect, and BR-035 AC15 covers stopped and discarded runs only. Test S18 proves that completing a run leaves no claims. Revisit if one is observed. | Architect | No |
| `writable_schema` availability in modernc SQLite. | [unverified]; the spike test (ARS-003 M0) decides between the chosen procedure and the rebuild fallback. | Implementer | Blocks the migration task only |
| Settle failure inside the live process leaves the run "stopping" until the next start. | Accepted: work is still owned by the run and not lost; startup settles it. | — | No |

## Revisit triggers

- More than one process or writer.
- A guard admission marker is added (the crash case of AC7 could then release).
- Users need a stop to wait for in-flight POST results instead of aborting them.
