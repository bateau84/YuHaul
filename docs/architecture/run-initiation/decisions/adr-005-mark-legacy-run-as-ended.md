---
type: decision
title: ADR-005 — Mark a Stuck Legacy Run as Ended with a Status-and-Reason Write Under the Initiation Locks
description: Realize BR-036 as one guarded UPDATE that changes only status and reason of a version-0 production run that is running or interrupted and not executing, serialized like Discard (initiationMu, then the production lock), with a lock-free precheck so repeats always answer "already ended", a closed end_legacy_run diagnostic, and an endable fact on run reads computed alongside discardable; startup classification needs no change.
tags: [architecture, decision, runs, legacy, run-14, end-run, concurrency, startup]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted — technical realization of [BR-036](../../../requirements/yuhaul/br-036-mark-legacy-run-as-ended.md) AC1–AC12. Implemented 2026-11-11, review pending ([ARS-004 §9](../specifications/ars-004-mark-legacy-run-as-ended.md#9-implementation-record-and-deviations)). The exact contract is [ARS-004](../specifications/ars-004-mark-legacy-run-as-ended.md). It reuses the Discard pattern ([ADR-003](adr-003-discard-unusable-time-limit-run.md)) for locking and commit uncertainty, and the Stop run control ([ADR-004](adr-004-stop-active-run-and-deferred-handoff.md)) for "executing now". Independent review is recommended before implementation.

## Summary

The user marks a stuck legacy run as ended with `POST /api/runs/{id}/end`. The server first answers any permanent refusal (not found, already ended, not a production run, not a legacy run) from a lock-free read. It then takes the same locks as Discard: the startup gate, a blocking `initiationMu`, and a non-waiting `productionMu.TryLock`. In one SQLite transaction it rechecks every condition, confirms that the run is not executing and that no production run is registered, and runs one guarded `UPDATE`. That `UPDATE` sets `status='failed'` and stores the closed diagnostic `diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user`. Nothing else is written: no handoff, no counter recompute, no `completed_at`, no quota, and no provider call.

Example: run 14 is manual, `mutation_safety_version=0`, `status='running'`, and has no run control. `POST /api/runs/14/end` returns `200 {"run_id":14,"status":"failed","outcome":"ended"}`. Its 19,615 unevaluated videos, its queue rows and every guard and action are unchanged. A second press returns `409 {"code":"run_ended","run_id":14,"status":"failed"}`, even if a scheduled run is executing by then. `GET /api/runs/14` reads `"endable":false` and an `interrupt_reason` with operation `end_legacy_run`.

Limits: the action does not process, queue or release run 14's unfinished videos (BR-036 AC5). Queue claims that run 14 held become reclaimable by later runs under the existing failed-owner rule (see Findings). The `endable` read does not see the production lock held for milliseconds by an admission or a discard; the action itself does.

## Satisfies

| Requirement | How |
|---|---|
| BR-036 AC1 | Only the `POST /api/runs/{id}/end` handler calls `EndLegacyRun`. It is registered next to Run Now, so it has the same access. No scheduler, startup, shutdown, time-limit or retry path calls it, and only `EndLegacyRunTx` writes `operation=end_legacy_run` (Decision 4). |
| AC2 | Every endable condition is rechecked inside the write transaction, under both locks. The guarded `UPDATE` repeats the row conditions in its `WHERE`. |
| AC3 | Server side: the path names one run, and the server acts on nothing else. Confirmation copy is the Designer's ([design §Mark a Legacy Run as Ended](../../../design/yuhaul-admin-ui-design.md)). |
| AC4 | Status and reason are one `UPDATE`, in one transaction, so they are saved together or not at all. Rollback → `500 end_failed` with the unchanged earlier status. Commit uncertainty is resolved by re-reading the row, as Discard does. |
| AC5 | The write changes only `status`, `interrupt_reason`, and `interrupt_cause` (set to NULL, which it already is on every version-0 row; Decision 1). No handoff, no counter recompute, and no queue, guard, action, decision, outcome or video write. |
| AC6 | No provider client, auth binding, scheduler call, `Execute`, `ResumeRun` or `StartRun` on this path. |
| AC7 | The read projection already admits registered diagnostics through `AdmitErrorText`. The SPA identifies the action by `operation=end_legacy_run`, distinct from `discard_run`, `stop_run` and self-failure reasons. |
| AC8 | A `failed` version-0 row is outside every resume and recovery selector, and startup classification never reads it (Decision 6). A `failed` claim owner is already reclaimable (`internal/queue/retry.go:288`), so the run never makes a start fail. |
| AC9 | Closed refusal codes (ARS-004 §3), each tested. |
| AC10 | Idempotent by state: the guarded `UPDATE` requires `status IN ('running','interrupted')`. The lock-free precheck answers `run_ended` before any gate or lock, so a repeat always says "already ended" (Decision 3). |
| AC11 | `initiationMu` → `productionMu` serializes the action against Run Now, Discard, scheduled starts and queue retries. The startup gate and `trackRun` order it against recovery and shutdown (Decision 3). |
| AC12 | `endable` on every run read, computed in the same server view as `discardable`, from the same SQL predicate and in-process state (Decision 5). |

Satisfied elsewhere: confirmation, acknowledgement and refusal copy (Designer); access control (same as Run Now, [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md)); guard semantics ([BR-012](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md), [OC-006](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md)), which this action does not touch.

## Current source (read 2026-11-11)

- `internal/state/store.go:94–119`: only `CreateGuardedMutationRunTx(…, true)` sets `mutation_safety_version=1`. Its only callers are `createClaimedRunLocked` (`pipeline.go:255`) and `RetryQueueEntry` (`retry.go:62`), both with `true`. Dry run and replay use `CreateRun` (version 0). So every version-0 manual or scheduled row predates guarded sending, and every simulation is version 0.
- `internal/pipeline/startup_recovery.go:101–106`: the classification query reads only `run_type IN ('manual','scheduled')`, `status IN ('running','interrupted') OR (status='completed' AND completed_at IS NULL)`, `mutation_safety_version <> 0`, `stop_requested_at IS NULL`. `markedStopRuns` (`:196`) reads only rows with a stop marker.
- `internal/state/store.go:150–166` (`ListInterruptedRuns`) and `internal/pipeline/run_now.go:221–226` (Run Now selector) require `status='interrupted'` and version 1.
- `internal/pipeline/stop.go:58–91`: `runControl`, `registerRun`, `control(id)`, guarded by `runsMu`. Registration points are listed in ARS-003 §3.1. All of them handle runs created or resumed by current code.
- `internal/pipeline/discard.go:76–171`: the Discard lock sequence, `discardTx` seam, `queue.ErrCommit` handling and `confirmDiscard` re-read.
- `internal/pipeline/pipeline.go:110–119`: the `producerGate`; `refuseWhileRecovering` refuses only while it is gated.
- `internal/server/server.go:194–201`: `trackRun` refuses once `closing` is set or `runBase` is done. `internal/server/run_control.go:98–160`: the `discardView` projection on `GET /api/runs`, `GET /api/runs/{id}` and the overview's `last_run`.
- `internal/store/migrations/024_run_initiation.up.sql`: `run_interrupt_cause_update` fires only on `UPDATE OF interrupt_cause` with a non-NULL new value. `025_run_stop.up.sql`: `run_stop_requested_status` aborts a status change of a marked row to anything but `stopped`. No other trigger exists on `run`.
- `internal/diag/diag.go:74–101`: closed `operations` and `codes`; `Facts.Valid` binds `discard_run` and `stop_run` to their own codes.
- `internal/queue/retry.go:265–293`: `retryOwner` treats `failed` and `stopped` owners as reclaimable, and `running` or `interrupted` owners as live.

## Decision 1 — the saved shape: status and reason only

The write is:

```sql
UPDATE run SET status='failed', interrupt_reason=?, interrupt_cause=NULL
WHERE id=? AND run_type IN ('manual','scheduled') AND mutation_safety_version=0
  AND status IN ('running','interrupted') AND stop_requested_at IS NULL
```

`completed_at`, `duration_seconds`, `quota_cost`, counters, `current_phase`, `started_at`, `config_version_id`, `initiated_by` and `selected_video_count` are not written. `completed_at` stays as stored (NULL for run 14).

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **Status + reason only, guarded `UPDATE`, `completed_at` unchanged (chosen)** | Exactly BR-036 Statement, AC4, AC5 and its verification ("its other run fields are unchanged"). One statement, no ledger read, no second connection. | A `failed` row with NULL `completed_at` is a new shape. Run inspection shows no end time. | **Chosen.** Decision 6 shows that no reader depends on `completed_at` for this row. |
| Also set `completed_at=COALESCE(completed_at, now)`, like Discard | Every terminal row then has an end time. | Changes a run field that BR-036 says stays unchanged. Not needed by startup (Decision 6). | Rejected: it weakens an accepted requirement for convenience. |
| Reuse `FailRun` | No new store code | Writes `completed_at`, duration, quota and recomputes counters on separate statements, so it is not atomic and changes counts (AC5). | Rejected. |
| Reuse `DiscardRun` with a wider predicate | Shares locking | Runs the deferred handoff, which AC5 forbids, and writes `completed_at` and counters. | Rejected. |

`interrupt_cause=NULL` keeps the 024 invariant that a cause exists only on an `interrupted` row, as Discard and Stop do. It is a no-op on every row the predicate can match: the only writer of `interrupt_cause` is `interruptRunWithCause`, which runs only for an executing run, and no current path executes a version-0 row. `interrupt_reason` is the run's single reason column, so any earlier legacy reason on an `interrupted` row is replaced; BR-036 AC4 requires that this column carry the end reason.

## Decision 2 — eligibility: one closed predicate plus in-process checks

A run is endable exactly when all of these hold at the moment of the write:

- **Row** (`legacyEndableRow`): `run_type IN ('manual','scheduled') AND mutation_safety_version=0 AND status IN ('running','interrupted') AND stop_requested_at IS NULL`. Queue-retry runs are `manual`, so the three production kinds of BR-035 are covered.
- **Not executing now:** `p.control(id) == nil`. No current registration point can register a version-0 row (Current source), so this check never refuses a real legacy run today. It is kept because BR-036 requires that the action never end a run that is actually running.
- **No production run active:** `productionMu.TryLock()` succeeds, and no registered run control belongs to a `manual` or `scheduled` row. The second check covers the gap in which a scheduled run has been created and registered under the lock, but `Execute` has not yet re-taken it.
- **Not recovering, not shutting down:** the producer gate is not closed, and `trackRun` accepts.

Rejected: Run Now's selector and validator (ADR-003). They select only version-1 time-limit runs. Also rejected: "any non-terminal run without a control". That would include version-1 rows that startup classifies, and simulations, which BR-036 AC9 refuses.

## Decision 3 — concurrency and refusal precedence

The order is:

1. lock-free permanent precheck;
2. `trackRun` (in the handler);
3. `refuseWhileRecovering`;
4. `initiationMu.Lock()`;
5. `productionMu.TryLock()`;
6. a transaction that does the row recheck, then the executing check, then the registry check, then the `UPDATE`.

The lock order is the same as Discard: `initiationMu` → `productionMu` → `runsMu`. Lookups of the run-control registry take `runsMu` briefly. `StopRun` takes only `runsMu`, and `ctl.mu`, so no cycle exists.

**Why a lock-free precheck.** BR-036 AC10 says every request after the first effective one is answered "already ended". With Discard's precedence, a repeat that arrives while a scheduled run executes would get `production_run_active`. The four permanent refusals are safe to answer without locks:

- `run_not_found` is true at the time of the read.
- A terminal status is final. No path moves `completed`, `failed` or `stopped` back (ADR-003 invariant 4, ARS-003 triggers).
- `run_type` and `mutation_safety_version` are never updated.

A stale precheck therefore cannot refuse wrongly. It also cannot accept wrongly, because it never accepts: it either refuses or passes the request on to the locked path, which rechecks everything.

Resulting serial orders:

- **End vs. end:** the second waits on `initiationMu`, then sees `failed` → `run_ended`.
- **End vs. Run Now press:** whichever takes `initiationMu` first. Press first → it holds `productionMu` for its execution → end refused `production_run_active`, unchanged. End first → commits → the press proceeds and never selects the ended row.
- **End vs. scheduled start or queue retry:** they take `productionMu` with `Lock()`. If they hold it first, or their run is registered, the end is refused. If the end holds it, they wait for its transaction (milliseconds).
- **End vs. Discard:** both serialize on `initiationMu`, and both may succeed, in either order. They never match the same row: Discard requires version 1.
- **End vs. Stop:** they cannot target the same row, because Stop needs a control and the end requires none. They do not share locks.
- **End vs. startup recovery:** the gate refuses it until recovery has finished. After that it is never closed again.
- **End vs. shutdown:** `trackRun` refuses it once shutdown begins. `WaitRuns` waits for an end that is in flight.

| Option | Verdict |
|---|---|
| **Discard's locks plus a lock-free permanent precheck (chosen)** | Satisfies AC10 and AC11 with existing primitives. |
| Discard's precedence without a precheck | Rejected: a repeat during a production run would not say "already ended" (AC10). |
| No locks; rely on the guarded `UPDATE` alone | Rejected: it cannot refuse while a production run is active (AC9), and a race with a start would have no serial order (AC11). |

## Decision 4 — reason: a closed `diag` operation

Register operation `end_legacy_run` and code `ended_by_user`. `Facts.Valid` requires `operation=end_legacy_run` ⇔ `code=ended_by_user`, with no status, reason or sqlite field. The stored value is `diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user`. The saved phase stays in `current_phase`. It passes `AdmitStored` and `GuardErrorText` unchanged. The UI branches on the operation and renders "ended by user (legacy run)". Rejected: free prose, because `GuardErrorText` would replace it with `unknown_error`. Rejected: a new column, which would need a migration for a value the reason column already carries.

## Decision 5 — the `endable` fact on run reads

`Pipeline.EndableRuns(ctx)` returns the set of endable run IDs. It takes no lock and works as follows:

- An empty set while the gate is closed.
- Otherwise, in one read-only transaction: the rows matching `legacyEndableRow`, minus any with a control. The set is empty if any registered control belongs to a production row.

The server computes it once per read, in the same view as `DiscardableRun`, and makes every item false once shutdown has begun. Each item gets `"endable": true|false`. Every item gets `null` when the read fails. The fact is advisory: the action rechecks.

Known approximation: the read cannot see `productionMu` held by a Run Now admission or a Discard before either registers a run. Those holds are local database work only (milliseconds). Taking `TryLock` inside a GET would make concurrent presses fail with `production_run_active`, so it is rejected. A stale `true` is refused by the action (AC12).

## Decision 6 — startup classification: no change needed

An ended row is `status='failed'` and `mutation_safety_version=0`. The ADR-005 classification query excludes it on both counts (`startup_recovery.go:104–105`), whatever its `completed_at`. It never carries a stop marker, because the guarded `UPDATE` requires none and trigger `run_stop_requested_set` allows a marker only on `running` rows, so `markedStopRuns` never returns it. Run Now, `ListInterruptedRuns` and the resume `UPDATE` all require `interrupted` and version 1. Startup therefore never reads, resumes or blocks on an ended run. That is also true today for the stuck row: startup ignores version-0 rows. Ending changes nothing startup sees, and it removes the row from nothing startup does. No amendment to [authorization ADR-005](../../youtube-authorization/decisions/adr-005-startup-recovery-for-guarded-production-runs.md) is needed. Its version-0 history veto (rule 9) reads `route_decision` and `executed_action`, not `status`, so it is unaffected.

## Rules the implementation must preserve

1. Only the `POST /api/runs/{id}/end` handler calls `EndLegacyRun`. Only `EndLegacyRunTx` writes `operation=end_legacy_run`.
2. The write is the single guarded `UPDATE` of Decision 1, with `RowsAffected=1`, in one transaction. No other statement writes in that transaction.
3. A refused or failed end writes nothing.
4. No provider, auth, scheduler, execution or queue call on this path.
5. Lock order `initiationMu` → `productionMu`. Neither is held while a run executes on behalf of this action, which never executes one.
6. `EndableRuns` and the action use the same `legacyEndableRow` SQL text.
7. The precheck may only refuse. It never decides acceptance.

## Findings

| Finding | Disposition | Owner | Blocks? |
|---|---|---|---|
| Queue rows that the legacy run holds as `retrying` (owner `running`/`interrupted`, so not reclaimable today) become reclaimable once the run is `failed` (`retry.go:288`). A later run may then process those videos. The action itself writes no queue row (AC5), and AC8 requires that the run not block starts. Whether live run 14 holds any such claim is [unverified]. | Accepted as an existing rule. Changing it would need a special owner class. Requirements should confirm that "unfinished videos stay unprocessed" does not extend to queue items the run had claimed. | Requirements | No |
| A `failed` row with NULL `completed_at` and NULL `duration_seconds`. | Decided (Decision 1). The Designer's display does not show an end time [unverified how the SPA renders NULL `completed_at` for a failed run]. | Designer | No |
| An earlier `interrupt_reason` on an `interrupted` legacy row is replaced by the end reason. | Decided: one reason column, and AC4 requires the end reason there. | — | No |
| `endable` may read `true` for milliseconds while an admission or discard holds `productionMu` before registering. | Accepted (Decision 5). The action refuses. | — | No |
| The Designer's "Save failed" refusal names the earlier status. | Provided: `end_failed` carries `status` when known (ARS-004 §3). | — | No |

## Confirmation

ARS-004 §6 lists the tests. These carry the most weight:

- the byte-for-byte snapshot with zero provider requests;
- the refusal matrix;
- the repeat answered `run_ended` while a production run is active;
- the end-vs-press and end-vs-end races;
- a process restart after ending, for which startup must not block.

## Revisit triggers

- Startup classification is widened to version-0 or terminal rows. It must then treat a `failed` row with NULL `completed_at` as terminal.
- More than one process or writer.
- Any code path starts registering or executing version-0 rows.
