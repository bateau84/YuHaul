---
type: decision
title: ADR-003 — Discard the Unusable Time-Limit Run in One Transaction Under the Production Lock
description: Realize the user's Discard of the run that Run Now refuses as resume_unavailable as one SQLite transaction that rechecks eligibility with Run Now's own selector and validator, hands unfinished work to the deferred queue (ADR-004 handoff; formerly the ADR-006 sweep) and writes the failed transition, serialized against run starts by a short initiation mutex plus the production lock.
tags: [architecture, decision, runs, manual, recovery, discard, time-limit, concurrency, retention]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — technical realization of BR-017 AC13–21. Implemented, review pending (2026-11-10; deviations in [ARS-003 §9](../specifications/ars-003-stop-run-and-deferred-handoff.md#9-implementation-record-and-deviations)). The exact endpoint, interfaces and test list are [ARS-002](../specifications/ars-002-discard-unusable-time-limit-run.md). Independent review of the implementation is pending.

**Amended 2026-11-10 by [ADR-004](adr-004-stop-active-run-and-deferred-handoff.md) (amended BR-017 AC17, the AC11 startup rule and AC22):**

- Discard no longer runs the ADR-006 sweep. It calls ADR-004's `handOffUnfinishedTx` with reason `discarded_by_user`: unfinished videos get pending deferred items that are eligible at once, and claimed Errors-queue entries are resolved.
- Startup no longer exits on a discardable run, so the startup finding below is answered.
- Run reads expose `discardable`.

Everything else here is unchanged. Where the text below says "sweep" or "Error Queue" for discard, read the handoff and the deferred queue.

## Summary

The user discards a run with `POST /api/runs/{id}/discard`. The server takes the production lock without waiting, then opens one SQLite writer transaction. Inside it, it asks the same question Run Now asks — "which run would a press pick, and can it be resumed?" — using the same SQL and the same validator. The discard goes ahead only if the answer is "run `{id}`, and no". It then runs the existing ADR-006 retention sweep and marks the run `failed` with a closed discard reason, in the same transaction. Either all of it commits or none of it does.

Example: run 30 stopped at its time limit and its snapshot row is gone. `POST /api/runs/30` (Run Now) returns `409 resume_unavailable, reason snapshot_unavailable`. The user confirms Discard for run 30. `POST /api/runs/30/discard` returns `200 {"run_id":30,"status":"failed","outcome":"discarded","cause":"snapshot_unavailable"}`. Run 30's 12 unfinished videos now each have a pending deferred item, eligible at once (amended; originally an active Error Queue entry each). Its retry-claimed deferred item is `pending` again, and no provider request was made. The next new run claims all 12. The next Run Now press resumes the next eligible run or starts a new one.

## Satisfies

| Requirement | How |
|---|---|
| [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md) AC13 | Eligibility = "the candidate `selectRunNowCandidateTx` returns has this ID, and `validateResumableRow` rejects it". Both are the functions `AdmitRunNow` uses, so the discardable run is exactly the run a press would refuse. The check runs inside the discard's own transaction under the production lock. Only the HTTP handler calls `DiscardRun`; no scheduler, startup, shutdown or retry path does. |
| BR-017 AC14 | Server side: the request names one run in the path and acts on nothing else. Confirmation dialog and copy are the Designer's; the server's recheck makes a stale confirmation safe. |
| BR-017 AC15 | Sweep, failed transition, reason and counter recompute are one `WithRetryTx` transaction. A rollback leaves every row unchanged and returns `500 discard_failed`. A commit error is resolved by re-reading the row under the still-held lock (Decision 2). |
| BR-017 AC16 | Stored reason `diag:v1 phase=unknown operation=discard_run code=<cause>`, a closed vocabulary admitted by `diag.AdmitStored`, so run history and inspection show it through the existing projection. `operation=discard_run` is written by no other path, so a discarded run is distinguishable from a self-failed one. |
| BR-017 AC17 (amended) | ADR-004's `handOffUnfinishedTx(…, discarded_by_user)`, in the same transaction: claims released, one pending deferred item per unfinished video (reused if present), claimed error entries resolved, removed stays removed, and a reclaimable owner for later starts. *Superseded design:* `sweepStrandedWork` to the Errors queue. |
| BR-017 AC18–19 | The handoff makes no provider call and does not touch `playlist_send_guard` or action evidence. A video whose action is uncertain either keeps its ADR-006 §1 Errors-queue entry (committed `failed` outcome) or is unfinished and gets a pending deferred item; the next run is blocked by the guard and records it under ADR-006 §1. |
| BR-017 AC22 | ADR-004 Decision 6: `discardable` on run reads, from the same selector and validator. |
| BR-017 AC20 | Closed refusal codes with the run ID (ARS-002 §3). Checks run before any write; every refusal rolls back. Repeats are refused as `run_ended`. |
| BR-017 AC21 | The failed row is outside every resume selector (Run Now, startup, `ResumeRun` all require `running`/`interrupted`). The discard starts nothing and does not touch the scheduler. Serial equivalence with run starts comes from Decision 3. |

Satisfied elsewhere: retention semantics (ADR-006 §3); Run Now admission (ADR-001); Error Queue retry and removal ([BR-014](../../../requirements/yuhaul/br-014-failed-work-retention-and-retry.md)); confirmation, acknowledgement and refusal copy (Designer).

## Current source (read 2026-11-10)

- `internal/pipeline/run_now.go:151–245`: `AdmitRunNow` = `refuseWhileRecovering` → `productionMu.TryLock` → `WithRetryTx{ resumeOldestRunNowTx }`. The candidate `SELECT` and `validateResumableRow` (`:54–79`) are inlined in `resumeOldestRunNowTx`.
- `internal/pipeline/authority.go:265–301`: `retainThenFail` commits retention in one transaction and only **then** calls `machine.FailRun` on separate connections. A `FailRun` error leaves the run `interrupted` with the retention already committed.
- `internal/pipeline/authority.go:305–369`: `sweepStrandedWork(ctx, qs, tx, runID, reason)` runs entirely inside the caller's transaction and only reads `run.run_type` from the run row.
- `internal/state/machine.go:469–488` and `store.go:257–271, 908–929`: `FailRun` reads the run, reads the ledger, writes status/reason/`completed_at`/duration/quota, then recomputes counters — four statements on `s.db`, not a transaction.
- `internal/diag/diag.go:72–79, 316–372`: stored reasons are admitted only if they parse as `diag:v1` with registered phase, operation and code.
- `internal/server/server.go:191–199, 612–656`: `trackRun` refuses once shutdown begins and joins `runWG`, which `WaitRuns` drains.
- `internal/pipeline/startup_recovery.go:72–118` and `cmd/server/main.go:214–224`: startup classification reads every guarded `interrupted` manual row; one that fails `validateResumableRow` makes `BeginStartupRecovery` return an error and the process exits with status 1.

## Decision 1 — one transaction, not `retainThenFail`

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **New `DiscardRun`: recheck + `sweepStrandedWork` + guarded failed `UPDATE` + counter recompute in one `WithRetryTx` (chosen)** | Exactly AC15's all-or-nothing. Reuses the sweep body untouched. The guarded `UPDATE` is a second recheck at the write. | Needs tx-scoped variants of the failed write and counter recompute (`state.Store.DiscardRunTx`, `RecomputeRunCountersTx`). | **Chosen.** |
| Call `retainThenFail` with the sweep | Zero new finalization code | Retention commits before the failed write. If `FailRun` then errors, queue entries exist while the run is `interrupted`: the mixture AC15 forbids. It also falls back to `InterruptRun`, which rewrites `interrupt_reason` and clears `interrupt_cause`, so a failed discard would **change** the run and remove its Run Now eligibility. Its second attempt under a detached context would also run outside any recheck. | Rejected: violates AC15 and AC20 "changes nothing". |
| Change `retainThenFail` itself to one transaction | One shape for all failures | Changes ADR-006's verified, reviewed run-stopping path (the interrupt fallback is load-bearing there). No requirement asks for it. | Rejected: unjustified blast radius. |

Consequences: the failed write for a discard sets `completed_at`, `quota_cost` and the reason, and clears `interrupt_cause`. It does **not** set `duration_seconds` (left as stored, normally `NULL`): wall time since `started_at` would include the hours or days the run sat interrupted and would misreport execution time. Quota cost is read from the ledger before the transaction, while the lock is held; no execution can add ledger rows for this run while the lock is held.

## Decision 2 — eligibility is Run Now's own selector and validator

Extract the candidate `SELECT` from `resumeOldestRunNowTx` into `selectRunNowCandidateTx`. `AdmitRunNow` and `DiscardRun` both call it and then `validateResumableRow`. The discard rule is:

> `DiscardRun(id)` may proceed only if `selectRunNowCandidateTx` returns a row whose `id` equals the requested ID, and `validateResumableRow` returns a non-empty reason for it.

This is exactly "the run a Run Now press would refuse under AC11 at that moment". A usable candidate (`""`) is refused as `resumable`; a younger time-limit run as `not_oldest`; every AC10 kind as `not_eligible`. Rejected alternative: a separate discard predicate (for example "any interrupted Run Now run with a bad snapshot"). It would let the two drift and would permit discarding a younger unusable run, which AC13 forbids.

**Cause mapping (AC16).** ADR-001 already treats all five validator reasons as AC11 refusals (`resume_unavailable`). AC16 names three causes. The stored and returned cause is:

| `validateResumableRow` reason | Discard cause |
|---|---|
| `snapshot_unavailable` | `snapshot_unavailable` (missing) |
| `snapshot_unreadable`, `snapshot_inadmissible` | `snapshot_unreadable` |
| `checkpoint_unrecognized`, `terminal_timestamp_contradiction` | `checkpoint_unrecognized` |

The exact validator reason is logged (closed value) with the run ID. See Findings for the two collapsed rows.

**Commit uncertainty (AC15).** If `Commit` returns an error, `DiscardRun` re-reads `status` and `interrupt_reason` while it still holds the lock. `failed` with the discard reason → success. `interrupted` → `discard_failed` ("did not take effect"). A read error → `discard_outcome_unknown`; the user is told to reload the run, and atomicity guarantees that the saved row shows one of the two outcomes.

## Decision 3 — concurrency: a short initiation mutex in front of the production lock

AC21 asks for more than "no overlap": a Run Now press racing a discard must either get `resume_unavailable` for that run or proceed after the discard, and a second concurrent discard must be refused *because the run ended*. With only `productionMu.TryLock`, the loser of either race would get `409 production_run_active`, which is neither.

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **`initiationMu sync.Mutex` taken with blocking `Lock()` by `AdmitRunNow` and `DiscardRun` around their decision section, then `productionMu.TryLock()` as today (chosen)** | Discard/discard and discard/press races serialize for milliseconds and each sees the other's committed result. A real production run still refuses both immediately. Lock order is fixed (`initiationMu` → `productionMu`); scheduler and queue retry take only `productionMu`, so no cycle. | One more mutex in `Pipeline`. `AdmitRunNow` holds it during new-run creation (local DB work only: `prepareProductionRun`, insert, claims). | **Chosen.** |
| Discard uses blocking `productionMu.Lock()` | No new primitive | A discard during an active run would wait up to 30 minutes instead of being refused (AC20). | Rejected. |
| Discard uses `TryLock` only | Simplest | A concurrent second discard gets `production_run_active`, not "already ended" (AC21 wording); a concurrent press gets `production_run_active` rather than AC11 or AC7–12 behavior. | Rejected. |
| A durable lease column on `run` | Survives process boundaries | Single process, single writer; the in-memory lock is already authoritative (ADR-001 revisit trigger). | Rejected. |

Resulting serial orders:

- **Discard vs. Run Now press:** whichever takes `initiationMu` first completes. Press first → `resume_unavailable` for that run, lock released → discard succeeds. Discard first → commits → the press sees the remaining runs (AC7–12).
- **Discard vs. discard (same run):** second waits on `initiationMu`, then sees `status='failed'` → `409 run_ended`.
- **Discard vs. scheduled run or queue retry:** they take `productionMu` with `Lock()`. If they hold it first, the discard is refused `production_run_active` ("a run that starts first causes the discard to be refused"). If the discard holds it, they wait for its transaction (milliseconds) and then run; neither touches the discarded run.
- **Discard vs. shutdown:** the handler calls `trackRun()` first, so a discard is refused `503` once shutdown begins, and `WaitRuns` waits for an in-flight discard to finish its transaction.
- **Discard vs. startup recovery:** `refuseWhileRecovering()` first; the gate is one-way, so once open it stays open.

`AdmitRunNow` gains one step (take `initiationMu`, release it before returning in every branch, including the held-lock success). Its observable contract (ADR-001, ARS-001 §3.1) is unchanged except that a press made during a discard waits for that discard instead of failing.

## Decision 4 — reason encoding: extend the closed `diag` vocabulary, no schema change

Register operation `discard_run` and codes `snapshot_unavailable`, `snapshot_unreadable`, `checkpoint_unrecognized` in `internal/diag`. `Facts.Valid` requires: `operation=discard_run` ⇔ code is one of those three; and those codes carry no `status`, `reason` or `sqlite`. Phase is always `unknown` (the saved phase stays in `current_phase`).

Rejected: a new `discard_cause` column (migration, triggers, projection) — the reason column already reaches run history and inspection through `AdmitErrorText`, and the vocabulary rule makes `discard_run` unforgeable by other writers. Rejected: free prose such as "discarded by user" — `GuardErrorText` would replace it with `unknown_error`.

## Rules the implementation must preserve

1. Discard eligibility is computed only by `selectRunNowCandidateTx` + `validateResumableRow`, inside the discard transaction, under `productionMu`. No other predicate.
2. A discard is one `WithRetryTx`: recheck, `handOffUnfinishedTx` (amended; formerly `sweepStrandedWork`), guarded `UPDATE … WHERE id=? AND status='interrupted' AND interrupt_cause='time_limit' AND initiated_by='run_now' AND run_type='manual'` with `RowsAffected=1`, counter recompute. Any error rolls back everything.
3. A refused or failed discard writes nothing: no `InterruptRun`, no reason rewrite, no cause clear.
4. A discard makes no provider call, opens no auth client, and does not read or write `playlist_send_guard`, `action`, or attempt evidence.
5. Only `DiscardRun` writes `operation=discard_run`. Only the `POST /api/runs/{id}/discard` handler calls `DiscardRun`.
6. Lock order is `initiationMu` → `productionMu`. `initiationMu` is never held while executing a run.
7. A discard never starts, resumes or schedules a run and never calls the scheduler.

## Findings

| Finding | Disposition | Owner | Blocks? |
|---|---|---|---|
| **Startup blocks on the discardable run.** Startup classification also reads time-limit Run Now rows; an unusable one makes the process exit with status 1 (`main.go:221`). If the process restarts while a discardable run exists, the listener never starts, so the Discard endpoint is unreachable. BR-017 AC10 and AC11 say startup recovery is unchanged, so this design does not change it. The escape hatch works only within the process lifetime in which the run became unusable. | Escalated. **Answered 2026-11-10:** BR-017 AC11 "At process start" and BR-011 AC7. Startup now skips, logs and leaves unchanged every unusable time-limit Run Now row (ADR-004 Decision 5; ADR-005 amendment). | — | Closed |
| AC16 names three causes; the validator has five reasons. `snapshot_inadmissible` is shown as "unreadable" and `terminal_timestamp_contradiction` as "checkpoint not recognized". | Decided as the closest truthful AC16 cause. **Confirmed 2026-11-10:** BR-017 AC11 "Which conditions count" adopts this mapping. | — | Closed |
| Discarded runs keep `duration_seconds` as stored (normally `NULL`). | Decided (Decision 1). Run inspection may show no duration for a discarded run [unverified how the SPA renders `NULL`]. | Designer | No |
| Swept Error Queue entries keep the sweep's fixed message "run stopped before this video finished". | Superseded: discard no longer creates Errors-queue entries. New deferred items carry `deferral_reason='discarded_by_user'`; the Designer labels it. | Designer | No |

## Confirmation

ARS-002 §6 lists the tests. Load-bearing: the eligibility matrix with byte-for-byte unchanged rows; injected failure at each step of the transaction leaving every row unchanged; the commit-error re-read; zero provider requests and unchanged guard rows; the race tests (discard vs. press, discard vs. discard, discard vs. active run) each matching one serial order; restart after discard.

## Revisit triggers

- More than one process or writer (the in-memory locks stop being authoritative).
- ADR-006's `retainThenFail` becomes transactional; `DiscardRun` could then share it.
