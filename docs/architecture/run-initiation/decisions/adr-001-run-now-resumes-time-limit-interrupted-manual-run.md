---
type: decision
title: ADR-001 — Run Now Resumes the Oldest Time-Limit-Interrupted Manual Run
description: Identify a time-limit interruption and a Run Now origin with explicit durable run fields, and admit a resume or a new run atomically under the production execution lock, refusing instead of waiting when production is active.
tags: [architecture, decision, runs, manual, recovery, time-limit, concurrency]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — technical realization of BR-017 AC7–12. Implemented 2026-11-10 in commit `2476375`; independent review pending. Realization deviations (validator order, migration split, quota test stub) are in [ARS-001 §10](../specifications/ars-001-run-initiation-schema-and-api.md#10-implementation-record-and-deviations). The exact schema, API and test contract is [ARS-001](../specifications/ars-001-run-initiation-schema-and-api.md).

## Summary

When a Run Now run hits its 30-minute limit, the pipeline now saves *why* it stopped (`interrupt_cause = 'time_limit'`) and *who started it* (`initiated_by = 'run_now'`). The next Run Now press looks for the oldest such run while holding the production execution lock. If it finds one, it validates the run's saved snapshot and checkpoint and moves the run back to `running` in one transaction. It then hands the held lock to the background execution, which continues from the saved checkpoint. If it finds none, it starts a new run as before. If another production run holds the lock, the press is refused with `409` instead of waiting.

Example: run 30 (Run Now) stops after 30 minutes in `action_execution`. Failure handling records `status='interrupted'` and `interrupt_cause='time_limit'`. The user presses Run Now. The server returns `202 {"run_id":30,"status":"running","admission":"resumed"}`. No run 31 and no new configuration snapshot is created. Execution resumes at `action_execution` under run 30's original snapshot. Playlist additions that already succeeded, or that a send guard blocks, are not sent again.

## Satisfies

| Requirement | How |
|---|---|
| [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md) AC7–8 | Selection of the oldest eligible run (`started_at`, then `id`) under the lock. One resumed run per accepted press. A new run is created only when no candidate exists. |
| BR-017 AC9 | Same run ID, snapshot and checkpoint. Execution goes through the existing `Execute` body, so idempotency, send guards and authority checks are unchanged ([BR-012](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md), [OC-006](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md)). A new time-limit stop sets the cause again. |
| BR-017 AC10 | The eligibility predicate requires `run_type='manual'`, `initiated_by='run_now'`, `interrupt_cause='time_limit'` and guarded version 1. This excludes legacy, shutdown, process-loss, scheduled, queue-retry, terminal and simulation runs. Startup classification is unchanged. |
| BR-017 AC11 | The same per-row checks as startup classification. A failed check refuses the press with the run ID and a closed reason. It writes nothing and makes no provider call. |
| BR-017 AC12 | The acknowledgement carries `admission: "resumed"` and the resumed `run_id`. The refusal order is: shutdown, startup recovery, active production, then the candidate check. The schedule is untouched. |
| BR-017 AC5–6 | The scheduler path is unchanged. Concurrent production is *prevented* by refusing the press (AC6 allows prevention or queuing). |
| [BR-033](../../../requirements/yuhaul/br-033-honest-manual-run-progress.md) | The acknowledged run is already `running` with its saved phase, so existing polling of `GET /api/runs/{id}` follows it truthfully. |

Satisfied elsewhere: startup recovery and shutdown interruption ([authorization ADR-005](../../youtube-authorization/decisions/adr-005-startup-recovery-for-guarded-production-runs.md), [ADR-007](../../youtube-authorization/decisions/adr-007-asynchronous-startup-resume-and-shutdown-interruption.md)); per-video failure isolation ([ADR-006](../../youtube-authorization/decisions/adr-006-per-video-action-failure-isolation-and-stranded-work-retention.md)). The resume acknowledgement copy, refusal copy and Run Now control presentation belong to the Designer.

## Current source (read 2026-11-10)

- `internal/server/server.go:592–622`: `handleStartRun` calls `StartRun(manual)` and then runs `Execute` in a goroutine under `context.WithTimeout(runBase, 30*time.Minute)`. `trackedRetries.RetryQueueEntry` (`:215–230`) uses the same plain 30-minute timeout.
- `internal/pipeline/pipeline.go:212–251`: `StartRun` blocks on `productionMu.Lock()`. `Execute` (`:286–465`) holds `productionMu` for the whole run, so a press during an active run currently *waits* until that run ends. `isResume` is derived only from `status == interrupted` (`:349`).
- `pipeline.go:531–537`: `failRun` turns any error after the execution context ended into `interrupted`, with `diag.InterruptReason(err, ctx.Err(), message)`. That text is a diagnostic string such as `diag:v1 phase=… operation=… code=timeout`.
- `internal/diag/diag.go:196–207`: `code=timeout` is also produced for `net.Error` timeouts. `retainThenFail` (`internal/pipeline/authority.go:265–300`) can store such a failure reason on an `interrupted` row when retention fails.
- `internal/pipeline/retry.go:36–105`: a queue retry creates a `run_type='manual'` guarded run. No stored field distinguishes it from a Run Now run.
- `internal/pipeline/startup_recovery.go:75–163`: startup selects guarded `manual`/`scheduled` rows that are `running` or `interrupted`, ordered `started_at, id`. Each row is validated with the checks `terminal_timestamp_contradiction`, `authority`, `checkpoint`, `snapshot_readable` and `snapshot_admissible`.

## Decision 1 — record the interruption cause and the initiator explicitly

Add two nullable columns to `run` (migration 024, specified in ARS-001):

- `initiated_by` ∈ {`run_now`, `schedule`, `queue_retry`}. It is written once at insert by `StartRun`/the Run Now admission (`run_now` for manual, `schedule` for scheduled) and by `RetryQueueEntry` (`queue_retry`). It is `NULL` for every pre-024 row and for simulation runs.
- `interrupt_cause` ∈ {`time_limit`, `shutdown`}. It is written only by `failRun`'s ended-context branch, in the same `UPDATE` that sets `status='interrupted'`. `time_limit` is written if and only if `errors.Is(context.Cause(ctx), pipeline.ErrRunTimeLimit)`. `shutdown` is written if `errors.Is(ctx.Err(), context.Canceled)`. In every other case it is `NULL`. Every other interruption writer (`InterruptRun`, startup reclassification) writes `NULL`. `ResumeRun` and the resume admission clear it together with `interrupt_reason`.

The Run Now execution context is created with `context.WithTimeoutCause(runBase, 30*time.Minute, pipeline.ErrRunTimeLimit)`. The time limit itself does not change.

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **Explicit `interrupt_cause` + `initiated_by` (chosen)** | Exact, closed, testable predicate. Writer and reader agree by construction. Not coupled to diagnostic text. | One forward migration. Pre-024 rows cannot qualify. | **Chosen.** |
| Parse `interrupt_reason` for `code=timeout` | No schema change | False positives: a provider `net.Error` timeout stored through the retention-failure fallback reads as `code=timeout` (`diag.go:204`, `authority.go:286`). A queue-retry run is also `manual` with the same 30-minute timeout. Diagnostic text is a safety projection, not a lifecycle contract. | Rejected: wrong runs would be resumed (violates AC10). |
| Infer the origin from the data shape (no discoveries, retry claims, phase set at creation) | No schema change | A Run Now run also claims deferred groups (`pipeline.go:229–243`). A retry run's phase may change. The inference is heuristic. | Rejected. |
| Only `interrupt_cause`, relying on the retry wrapper not setting the cause | One column fewer | Safety depends on a convention in an unrelated wrapper. A later change to `trackedRetries` would silently make retries eligible. | Rejected: the column is cheap; the coupling is not. |

**Consequence:** rows written before migration 024, including guarded version-1 manual runs already interrupted by the old timeout, have `initiated_by IS NULL` and never qualify. Their origin and cause cannot be proven, so they fail closed. They are still resumed by the unchanged startup recovery at the next process start (ADR-005). This does not weaken BR-017: the definition requires a *saved* time-limit reason, which those rows lack.

## Decision 2 — atomic admission under the execution lock, with refusal instead of waiting

Replace the Run Now handler's `StartRun` call with a new pipeline operation, `AdmitRunNow` (ARS-001 §API). It runs these steps in order:

1. Refuse with `ErrStartupRecoveryActive` while the startup gate is closed (existing behavior). The server refuses earlier, in `trackRun`, during shutdown.
2. `productionMu.TryLock()`. If the lock is held, return `ErrProductionRunActive`; the server responds `409`. Nothing is read or written.
3. With the lock held, run one SQLite transaction: select the oldest eligible row and validate it with the shared per-row checks used by startup classification. Then either move it from `interrupted` to `running` with a guarded `UPDATE`, or, if no row exists, create the new run exactly as `StartRun` does today, with `initiated_by='run_now'`. A failed check returns `ResumeUnavailableError{RunID, Reason}`, rolls back and releases the lock.
4. Return an admission that still holds the lock. The server's goroutine calls `admission.Execute(ctx)`. It runs the existing `Execute` body without taking the lock again; for a resumed admission it treats the run as a resume, so the start phase comes from `current_phase`. It releases the lock on return. If the handler cannot start the goroutine, it calls `admission.Release()`.

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **TryLock + one transaction + lock hand-off (chosen)** | No window between choosing and executing a run. Each accepted press owns exactly one run. A second press sees `409`. The acknowledgement can truthfully say `running`. | A small internal change: `Execute` is split into a locked wrapper and a held-lock body. Run Now stops waiting behind an active run. | **Chosen.** |
| Select, then release the lock and let `Execute` lock again | No refactor | Two presses can choose the same run. The second `Execute` may then resume a run that has since been interrupted for another reason (violates AC8/AC10), or acknowledge a resumption that never happens. | Rejected. |
| A durable claim column/lease on `run` | Survives process boundaries | Adds a lifecycle state for a single-process, single-writer deployment that already serializes production in memory. A crash already leaves `running`, which startup reclassifies. | Rejected: no demonstrated need. |
| Keep the blocking `Lock()` for Run Now | Unchanged HTTP behavior | The request waits up to 30 minutes, and the candidate chosen after waiting may differ from the one the user saw. BR-017 AC12's "refused press resumes nothing" cannot be observed. | Rejected. |

**Behavior change to note:** a Run Now press while any production run is executing (manual, scheduled or queue retry) now gets `409 production_run_active` instead of a request that hangs until that run finishes. This applies whether or not a resumable candidate exists, so the press has a single, predictable rule. BR-017 AC6 explicitly allows prevention, and the accepted design already keeps Run Now unavailable while a run is in progress, with the server authoritative (admin UI design §Run Now). The scheduler's `StartRun(scheduled)` keeps its blocking lock, so a due schedule still queues behind an active run (AC5 unchanged). Queue retry is unchanged.

**Crash safety:** the admission commits `status='running'` while preserving `current_phase`. If the process dies before or during execution, startup classification finds a guarded `running` row, reclassifies it as `interrupted` (cause `NULL`) and resumes it from the same checkpoint. A run already eligible for Run Now is also resumed by startup recovery, which is unchanged.

## Rules the implementation must preserve

1. Eligibility is exactly: `run_type='manual' AND initiated_by='run_now' AND status='interrupted' AND interrupt_cause='time_limit' AND mutation_safety_version=1 AND action_authority_version=1`, ordered `started_at ASC, id ASC`. Only the first row is considered; a younger row is never chosen because the oldest is unusable.
2. A resume press never calls the versioner or config store to capture configuration, never inserts a run and never claims queue groups. Work already owned by the run is processed by the existing `processDeferredItems`.
3. Selection, validation and the `interrupted → running` write happen in one transaction while holding `productionMu`. The lock is held continuously until the admitted execution returns.
4. A refusal writes nothing, makes no provider call and creates no run.
5. Per-row validation is one shared function used by both startup classification and admission, so the two can never disagree about what is resumable. Startup's observable behavior does not change.
6. `interrupt_cause` is written only together with `status='interrupted'`, and is cleared whenever `interrupt_reason` is cleared.

## Confirmation

ARS-001 §Verification lists the tests. The load-bearing ones are: the registered `POST /api/runs` path with real SQLite and a controlled provider, a real time-limit expiry, the same run ID and checkpoint on the next press, zero sends to guarded and already-succeeded targets, oldest-first with a tie, the exclusion matrix, unusable-candidate refusal, `409` while another run executes, and crash after admission followed by startup resume.

## Revisit triggers

- More than one process or writer becomes supported (the in-memory lock stops being authoritative).
- The coordinator proposal is accepted, and dispatch moves to a shared owner ([coordinator ARD-001](../../discovery-processing-coordinator/design/ard-001-discovery-processing-budget-coordinator.md)).
- Users need Run Now to queue behind an active run rather than be refused.
