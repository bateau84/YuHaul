---
type: decision
title: ADR-007 — Asynchronous Startup Resume and Shutdown Interruption
description: Classify synchronously, then serve HTTP while recovered runs resume in the background behind a production-producer gate; SIGTERM interrupts resumably within a bounded grace period.
tags: [architecture, startup, recovery, shutdown, kubernetes, availability]
timestamp: 2026-11-09T00:00:00Z
---

**Status:** decided technical contract. Implemented 2026-11-09 with the nine required tests (see Realization notes). Independent final review (round 4, 2026-11-09; reviewer session `ses_e3e8dc3feffeedsw0eB59kxvM3`, Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`) verified this decision, with ADR-005 and ADR-006 and their follow-up fixes, on the assembled binary using child-process kill and SIGTERM tests. It found no material defect. The proof gaps below remain. This decision does not authorize live rollout.

Startup recovery is split into two parts. Classification stays a short synchronous step before the server listens. Resuming recovered runs moves to a background worker that starts **after** the HTTP listener is up. Until that worker finishes, a gate inside the pipeline refuses new production runs and queue retries with `503`. Read endpoints, including the probe path `/api/overview`, keep working throughout. The scheduler starts only after the worker finishes. SIGTERM cancels one process-wide context, and a run stopped by that cancellation becomes `interrupted`, not `failed`.

For example, a pod restarts with one interrupted run whose discovery takes 40 minutes. Classification commits in milliseconds. The server starts listening, liveness passes, and the run resumes in the background. A user clicking "Start run" receives `503 {"error":"startup recovery in progress"}`. When the run finishes, the gate opens and the scheduler starts. If Kubernetes sends SIGTERM at minute 20, the run is marked `interrupted` at its saved phase and resumes at the next start. A POST that was in flight keeps its guard.

## Derived from

- [ADR-005 — Startup Recovery for Guarded Production Runs](adr-005-startup-recovery-for-guarded-production-runs.md), §1 and §6. This decision refines ADR-005: it keeps the rule "before producers act; recovery set not overtaken" and drops the realization in which resume blocks the HTTP listener.
- [BR-030 — Temporary Failure Work Preservation](../../../requirements/yuhaul/br-030-temporary-failure-work-preservation.md) AC1 and [BR-011 — Processing State Persistence](../../../requirements/yuhaul/br-011-processing-state-persistence.md): a crash or shutdown leaves recoverable work.
- [ADR-002 — Failed-Phase Runs Terminate as failed](../../adr-002-run-status-failed-terminal-state.md): process-loss interruption is resumable and is not a phase failure.
- [ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md) and [ADR-004](adr-004-atomic-quota-admission-and-single-attempt-transport.md): cancellation before admission is `not_issued`, and cancellation after admission is `uncertain` with the guard kept.
- [ARS-003 — Traefik BasicAuth Kubernetes Contract](../../ars-003-traefik-basic-auth-kubernetes-contract.md): probes are internal paths. `Recreate` provides single ownership.
- Reviewer session `ses_e3e8dc3feffeedsw0eB59kxvM3`, Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`.

## Satisfies

| Authority | Disposition |
|---|---|
| ADR-005 §1, §6 | Preserved. No producer acts before classification commits. New runs and retries cannot overtake the recovery set. |
| BR-030 AC1; BR-011 | Satisfied for graceful shutdown. Cancellation becomes resumable `interrupted`. A hard kill is still covered by ADR-005. |
| BR-012; OC-006 G4–5 | Preserved. Cancellation never releases a guard of unknown outcome and never repeats a POST. |
| Quota (BR-013; OC-001) | Satisfied for the reported risk. Probe-driven kill loops no longer re-spend discovery reads. Crash-complete accounting is still the ADR-003/004 boundary. |

## Source findings (read 2026-11-09)

- `cmd/server/main.go:206-210` runs `pl.RecoverStartup(ctx)` with `context.Background()` before `sch.Start` and before `ListenAndServe`. `RecoverStartup` (`internal/pipeline/startup_recovery.go:17-36`) calls `Execute` synchronously for every recovered run, so a whole run can execute before the server listens.
- `signal.Notify` is installed only after the listener starts (`main.go:238-240`). A SIGTERM during recovery therefore kills the process with Go's default behaviour, and no interruption is recorded.
- `k8s/deployment.yaml` has a liveness probe (`/api/overview`, `initialDelaySeconds: 10`, `periodSeconds: 30`, default `failureThreshold: 3`) and a readiness probe, but no startup probe and no `terminationGracePeriodSeconds` (default 30 s). The shutdown path takes up to 15 s for HTTP plus 15 s for the import, which already uses the whole default grace period.
- A manual run executes under `context.WithTimeout(context.Background(), 30*time.Minute)` (`internal/server/server.go:521-526`), so shutdown does not cancel it. Any cancellation reaches `failRun`, which makes the run terminal `failed`.
- `StartRun` and `RetryQueueEntry` wait on `productionMu`. Go's mutex is not FIFO, so mutex order alone cannot guarantee that the recovery set is not overtaken.

## Options considered

| Option | Benefits | Costs and failure modes |
|---|---|---|
| **A. Synchronous classification, asynchronous resume behind a producer gate, process-wide cancel context (chosen)** | Liveness is independent of run length. ADR-005's ordering guarantee is kept. Graceful shutdown is resumable. No new persistence. | One gate flag with a typed refusal, HTTP 503 mappings, and a reordered `main`. |
| B. Add a startup probe only | Five YAML lines. | Recovery runs a complete production run, which has no upper time bound, so any finite `failureThreshold × periodSeconds` still ends in a kill loop. It does nothing about SIGTERM. Rejected as insufficient. |
| C. Serve read-only health while synchronous recovery blocks the rest | Probes pass. | Needs a second temporary mux or listener handover. User-facing read endpoints stay unavailable for the whole run, for no safety reason. Option A gives the same safety more simply. Rejected. |
| D. Remove or relax the liveness probe | Trivial. | Loses detection of a real hang. Does not address SIGTERM. Rejected. |

## Decision and exact contract

### 1. Startup order (server composition)

1. Install signal handling **first**: `appCtx` is cancelled on SIGINT or SIGTERM (for example with `signal.NotifyContext`). Every later step uses `appCtx`.
2. Open the database, migrate, and construct dependencies. These steps are unchanged.
3. **Close the production-producer gate**, then run ADR-005 classification synchronously under `appCtx`. Classification failure keeps today's behaviour: the process exits non-zero, and no producer or provider call happens. The classification transaction is short and makes no provider calls.
4. Start the HTTP listener.
5. If the recovery set is empty, open the gate and start the scheduler. Otherwise launch **one** background recovery worker. It calls `Execute(appCtx, id)` for each ID, oldest first (`started_at`, then `id`), one after another. When the worker returns, whatever each run's outcome, open the gate and then start the scheduler under `appCtx`.
6. If `appCtx` is cancelled before the worker finishes, the gate never opens and the scheduler never starts.

The gate is opt-in for server composition only. A `Pipeline` built by tests or by inspection tooling without startup recovery is not gated. This keeps ADR-005's rule that opening a database does not activate recovery.

### 2. The production-producer gate

- **Location:** inside the pipeline, checked at the start of `StartRun` and `RetryQueueEntry`, before any configuration capture, run creation, queue claim or wait on `productionMu`. Gating every producer at one site covers the HTTP manual start, queue retries and any future producer.
- **Closed gate:** the producer returns the typed `ErrStartupRecoveryActive` and writes nothing.
- **HTTP mapping:**
  - `POST /api/runs` → `503 {"error":"startup recovery in progress"}`.
  - Error-entry and deferred-item retry endpoints → the same status and body.
  - `Retry-After` is optional.
  - No other status or body changes.
- **Not gated:**
  - Read endpoints, including `/api/overview`.
  - Configuration and route edits: resumed runs use their captured snapshot.
  - Account connect and disconnect: ARS-001 fences authority changes.
  - Queue dismiss and remove: ARS-001 ownership rechecks apply.
  - Dry-run, replay and backtest: simulations are provider-free and guard-free.
- **One-way:** the gate never closes again during the life of the process.

### 3. Shutdown and cancellation

1. **Run-stopping cause:** in production `Execute`, a phase that stops because `ctx.Err() != nil` is classified as **interruption**. Call `InterruptRun` under `runFinalizationContext`, with the bounded diagnostic from `diag.Reason(ctx.Err(), …)` (registered code `canceled` or `timeout`, saved phase). Keep `current_phase` and every committed disposition. Do not call `FailRun`, and do not run the ADR-006 sweep. Panic, authority and other errors keep their existing paths.
2. **Provider calls:** cancellation before admission is `not_issued`. The owned guard is released only together with its retained disposition. Cancellation after admission is `uncertain`: the guard is kept and there is no replay (ARS-001). No new rule is added.
3. **Classification:** a cancellation during classification rolls back or commits. Both states are eligible again at the next start (ADR-005 §6).
4. **Run contexts:** the manual-run goroutine, every queue retry and the scheduler use contexts derived from `appCtx`. The existing 30-minute manual-run timeout stays, because it is outside this decision, but it now also yields `interrupted` (see Open findings).
   - **Queue retries (extension, 2026-11-09):** `RetryQueueEntry` creates a receiving run and may POST, so it is a production run like a manual start. It runs on a context derived from `appCtx` with the same 30-minute bound. It is tracked by the same run drain as manual starts, so the drain waits for it.
   - **Refusal:** once `appCtx` is cancelled or the drain has begun, a new retry is refused before any run or claim is written. The endpoints answer `503 {"error":"server is shutting down"}`, as a manual start does.
   - **Client disconnect:** the request context does not govern the retry. A disconnect alone never cancels it, so it can never become an unrecoverable outcome. Only `appCtx` cancellation or the 30-minute bound interrupts it, and then the run ends `interrupted` under rule 1.
   - **Interrupted retry response:** a retry that returns an error after `appCtx` was cancelled, whether before or after it created its run, is reported as the same refusal: `503 {"error":"server is shutting down"}` on both retry endpoints, never `500`. A run it already created stays `interrupted`.
   - **Interruption reason:** the stored reason keeps the registered `canceled` or `timeout` code and the step facts of the failing phase and operation. A cancelled queue retry stores `phase=action_execution operation=retry`, not `unknown`.
5. **Signal handler order:** cancel `appCtx`. Wait for the recovery worker, the scheduler and any active `Execute` for at most **15 s**. Then shut down HTTP (at most 10 s), then the corpus import (at most 15 s), then close the database. If a step exceeds its budget, log it and continue. A kill after this point is process loss, which ADR-005 recovers safely.
6. **Grace period:** `k8s/deployment.yaml` sets `terminationGracePeriodSeconds: 60`, above the 40 s in-process total. Keep `strategy: Recreate`, which is ADR-005's ownership premise. The existing liveness and readiness probes stay. A startup probe is **not required** by this decision, because the listener now starts after migration and classification only.

## Consequences

The pod becomes live within seconds of migration and classification, whatever the run length. Kill loops caused by probes stop, along with their repeated discovery reads. Users see a clear `503` instead of a hanging request while recovery runs. A shutdown during a run leaves `interrupted` work that resumes at the next start, instead of a terminal `failed` run with stranded work. The costs are:

- One gate flag.
- One typed error and two HTTP mappings.
- A reordered `main`.
- One manifest field.
- Cancellation classification in `Execute`.

The readiness probe may report Ready during recovery. This is intentional, because reads are safe and producers are refused explicitly.

## Required tests

1. **Composition (child process, disposable SQLite, fake YouTube):** seed a version-1 interrupted run whose discovery is held open by the fake endpoint. Then:
   - `GET /api/overview` returns 200 within 2 s of start, while the run is still executing.
   - `POST /api/runs` and both retry endpoints return `503` with the exact body and create no run, claim or snapshot.
   - The scheduler has not ticked.
   - After the fake endpoint is released and the run finishes, the gate opens: `POST /api/runs` returns 202, and the scheduler starts.
2. **No overtaking:** with two recovered runs, issue a manual start while the first resumes. It is refused, and both recovered runs execute in `started_at`/`id` order before any new run exists.
3. **SIGTERM during resume (before admission):** send SIGTERM while discovery is in flight. The process exits within the budget, and the run is `interrupted` with a `canceled` diagnostic at its saved phase, never `failed`. Restart resumes the same run ID and snapshot.
4. **SIGTERM during an admitted POST:** the fake endpoint holds the response, then SIGTERM. The guard is kept, the action is `failed`/`mutation_application_uncertain` with its attempt time if admitted, and the run is `interrupted`. After restart: zero further POSTs for that target, and other targets proceed.
5. **SIGTERM between claim and admission:** cancellation is seen before dispatch. The result is `not_issued`, the guard is removed atomically with the retained disposition, and the run is `interrupted`.
6. **SIGTERM during classification** (an injected pause inside the transaction): exit; the next start classifies the same rows once, with no duplicate worker.
7. **Manual-run timeout:** an expired deadline yields `interrupted` with a `timeout` diagnostic, and no unfinished video is left without a resumable owner.
8. **Gate scope:** a `Pipeline` built without startup composition is not gated. Simulations, reads, configuration edits and queue dismiss work during recovery.
9. **Manifest check:** `terminationGracePeriodSeconds` is at least 60, `strategy` is `Recreate`, and probes are unchanged.

## Realization notes (2026-11-09)

- **Gate.** `Pipeline` holds a one-way state (ungated, gated, open). `BeginStartupRecovery` moves ungated to gated, then classifies. `ResumeRecovered` runs the IDs one after another and stops when the context ends. `OpenProducers` opens the gate for good. `StartRun` and `RetryQueueEntry` call `refuseWhileRecovering` first. `RecoverStartup` stays as the synchronous, ungated composition for tools and tests.
- **Typed refusal.** The sentinel lives in `internal/queue` (`queue.ErrStartupRecoveryActive`, text `startup recovery in progress`), and `pipeline.ErrStartupRecoveryActive` is the same value. The queue HTTP handlers map it, and they cannot import `pipeline`. `POST /api/runs`, `POST /api/errors/{id}/retry` and `POST /api/deferred/{id}/retry` answer `503 {"error":"startup recovery in progress"}`. `Retry-After` is not sent.
- **`main`.** `signal.NotifyContext` is installed first and `ctx` is passed to every later step. The listener is created synchronously with `net.Listen`, so a bind failure is fatal and the port is open before any recovered run starts. A cancelled classification logs `shutdown during startup classification` and returns, with exit status 0. The drain waits for the recovery worker, the scheduler and the manual runs together, for at most 15 s. HTTP then gets 10 s, then the corpus import 15 s, then the database closes. An overrun is logged and shutdown continues.
- **Manual runs.** `server.WithRunContext(ctx)` parents the manual-run goroutine, and the 30-minute timeout stays on top. `Server.WaitRuns` refuses new manual starts with `503` once shutdown begins and waits for the active ones.
- **Queue retries.** `Server.RetryCoordinator(inner)` wraps the pipeline and is what `main` passes to `queue.Service.WithRetryCoordinator`. It registers the retry with the same `trackRun`/`WaitRuns` drain as a manual start and runs it on a context derived from `runBase` (30-minute bound), ignoring the request context. `trackRun` also refuses once `runBase` is cancelled, which closes the gap between SIGTERM and `WaitRuns`. A refusal is `queue.ErrShuttingDown`, which the two retry endpoints map to `503 {"error":"server is shutting down"}`. Before this extension a retry ran synchronously on the request context. SIGTERM did not cancel it, HTTP shutdown timed out, the run stayed `running`, and the admitted attempt had no uncertain evidence or quota finalization. The reproduction is reviewer evidence `retry-long.json`.
- **Safe reasons.** `diag.InterruptReason(err, ctxErr, fallback)` takes the code from the context and the phase and operation from the failing step. The literal `manual retry failed` routes to `action_execution`/`retry`. A guard-blocked video fails with `errGuardBlocked`, which carries the registered `mutation_application_uncertain` code through `diag.WithCode`, so its retained error entry is no longer `unknown_error`. The normal-run path (`recordHandledVideoFailure`) stores that vetted reason as both the headline (`error_message`) and the detail, so `GET /api/errors` serves the code with `error_message_unavailable` absent. `WithCode` accepts only that code and carries no provider status, reason or SQLite fact.
- **Cancellation.** `failRun` calls `InterruptRun` under `runFinalizationContext` when `ctx.Err() != nil`. The reason is `diag.Reason(ctx.Err(), …)` (`canceled` or `timeout`). It does not call `FailRun` or the ADR-006 sweep. A panic or an authority error keeps its existing path. In `Executor.execute`, a stop with a done context returns at once and writes no skip evidence and no sink.
- **Guard release for a POST that was never sent.** `executeGuardedAction` maps a done caller context with no admission receipt to the new `youtube.DispatchRefusalCanceled`. `releaseCanceledClaim` then removes the claiming invocation's own guard in one transaction with a `not_attempted` action row that has no attempt time and no reason. The action-reason vocabulary is closed ([run-outcome diagnostics ARS-001](../../run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md)), so no reason was added. No queue entry is written, because the interrupted run is the owner. The write uses the 5 s detached finalization context. If it cannot commit, the guard stays and the target blocks as uncertain on resume.
- **Writes after admission.** Quota `Hold.Finalize`, the uncertain-attempt evidence row and the known-success resolution use the same bounded detached context. Without that, a SIGTERM during an admitted POST could not record the attempt time (required test 4). A hold that has been admitted is also accounted once. No unbounded background work is added.
- **Changed existing test.** `TestFailRunFinalizesExpiredExecutionContext` asserted the old rule (an expired context makes the run `failed`). It is now `TestFailRunInterruptsExpiredExecutionContext` and asserts `interrupted`, `code=timeout`, no `completed_at`.

| Required test | Where |
|---|---|
| 1 Composition | `cmd/server/startup_composition_integration_test.go` `TestStartupResumeIsBackgroundBehindProducerGate` (child process, `integration` tag) |
| 2 No overtaking | `internal/pipeline/startup_async_integration_test.go` `TestRecoveredRunsAreNotOvertakenByNewProducers` |
| 3 SIGTERM during resume | `cmd/server/startup_composition_integration_test.go` `TestSigtermDuringResumeInterruptsAndRestartResumesSameRun` |
| 4 SIGTERM during admitted POST | `cmd/server/startup_composition_integration_test.go` `TestSigtermDuringAdmittedPostKeepsGuardAndNeverReplays` |
| 5 Between claim and admission | `internal/pipeline/startup_gate_test.go` `TestCancelBetweenClaimAndAdmissionReleasesGuardAndInterruptsRun` |
| 6 During classification | `internal/pipeline/startup_gate_test.go` `TestStartupClassificationCancelledInsideTransactionIsRetriedOnce` and `cmd/server/startup_composition_integration_test.go` `TestSigtermDuringClassificationExitsAndNextStartClassifiesOnce`. The pause is a SQLite trigger that runs until interrupted. |
| 7 Manual-run timeout | `internal/pipeline/startup_async_integration_test.go` `TestExpiredRunDeadlineInterruptsAndResumes` |
| 8 Gate scope | `internal/pipeline/startup_gate_test.go` `TestProducerGateScope` |
| 9 Manifest | `cmd/server/manifest_test.go` `TestDeploymentManifestShutdownContract` |
| 10 SIGTERM during a queue retry's admitted POST (extension) | `cmd/server/startup_composition_integration_test.go` `TestSigtermDuringQueueRetryAdmittedPostInterruptsRetryRun` (child process, `integration` tag): the retry run is `interrupted` with `phase=action_execution operation=retry code=canceled`, the attempt is `failed`/`mutation_application_uncertain` with its time, the guard is kept, the quota hold is finalized, and the server exits without an HTTP shutdown failure |
| 11 Retry refused during the drain (extension) | `internal/server/retry_lifecycle_test.go` `TestRetryCoordinatorJoinsShutdownLifecycle` and `TestRetryCoordinatorRefusesOnceApplicationContextIsCancelled`; `internal/queue/handlers_shutdown_test.go` `TestRetryEndpointsAnswer503WhileShuttingDown` |
| Safe reasons (extension) | `internal/diag/diag_test.go` `TestWithCodeMarksOnlyRegisteredUncertainMutation`, `TestInterruptReasonKeepsContextCodeAndStepFacts`; `internal/pipeline/guard_blocked_reason_test.go`; `internal/pipeline/guard_blocked_api_integration_test.go` `TestGuardBlockedErrorEntryServedWithRegisteredCode` (normal run, served by `GET /api/errors`) |
| Interrupted retry response (extension) | `internal/server/retry_lifecycle_test.go` `TestInterruptedRetryAnswers503OnBothEndpoints` |

Not proven by these tests:

- **Test 5** drives the executor with a dispatcher that cancels after the claim, then calls `failRun` with the executor's error. It does not run the whole `Execute` loop, and it is not a child process.
- **Test 7** applies the deadline to `Execute` directly. It does not wait out the real 30-minute server timeout.
- **The 15 s drain budget** is not exercised by a test. A run that ignores cancellation and overruns the budget is not covered.
- **Test 11** proves the refusal at the server wrapper and the HTTP mapping separately. No child-process test sends a retry request while the drain is running, because the drain finishes within milliseconds once the admitted POST is released.
- **Test 1** does not observe a scheduler tick. It observes that `scheduler started` is absent until the gate opens.

Final-review proof gaps (round 4, as the reviewer stated them). The review did not prove:

- SIGTERM during classification;
- two-run no-overtake;
- drain overrun;
- the real 30-minute deadline;
- Kubernetes behaviour;
- the `wedge_save` timing window.

## Advisory: `/api/account` after a cancelled save

After a token save is cancelled (for example by SIGTERM or a deadline), `/api/account` reports `persistence_failed` until the next token use. The next use heals it, so no operator action is needed. This is a status-reporting limit, not a safety defect: no guard, quota or replay rule depends on it. Source: round 4 final review.

## Open findings

- **The 30-minute manual-run deadline** now interrupts instead of failing, but an interrupted run resumes only at the next process start (ADR-005 narrowed resume to startup). A long manual run can therefore wait for the next restart. This does not block: the work is preserved and visible. Owner: the product or requirements owner, if a timely automatic resume is wanted. Revisit if users report waiting runs.
- **Revisit a startup probe** if migration plus classification is observed to take more than about 60 s, for example a large ARS-004 backfill.
