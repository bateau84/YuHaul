---
type: decision
title: ADR-002 — Failed-Phase Runs Terminate as `failed`, Not `completed`
description: Add `failed` as a fourth terminal Run status so a run whose processing phase fails is not reported `completed`, preserving the OC-002 outcome vocabulary and the resumable-vs-terminal distinction in run recovery.
tags: [architecture, decision, yuhaul, run-status, outcome-semantics, recovery]
timestamp: 2026-11-05T00:00:00Z
---

**Status:** accepted — the terminal-`failed` versus resumable-`interrupted` rule is the accepted run-status policy recorded in [YuHaul System Architecture](yuhaul-system-architecture.md) §Run Recovery. Production and dry-run/replay/backtest failure handling are implemented; bounded tests/browser evidence and remaining limits are recorded in [ARD-001 — Durable Run Diagnostics and Simulation Input Visibility](run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md), §Verification and composition.

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [OC-002 — Route Evaluation Outcome Semantics](../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md)

## Satisfies

- [BR-032 — Processing Outcome Distinction](../requirements/yuhaul/br-032-processing-outcome-distinction.md) — run-level status distinguishes a failed run from a completed run.
- [OC-002 — Route Evaluation Outcome Semantics](../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) — `failed` is a shared terminal classification, not a local variant.
- [BR-028 — Historical Run Inspection](../requirements/yuhaul/br-028-historical-run-inspection.md) — the operator sees a truthful run status rather than `completed` with an `interrupt_reason` free-text.

## Problem

Before this correction, `Pipeline.failRun` called `InterruptRun` and then completion, overwriting failure with `completed`. A phase failure such as "discovery: no active token" therefore looked completed with an `interrupt_reason` and zero counters. The production panic path had the same defect. This contradicted truthful run inspection and prevented resumable-versus-terminal distinction. These are historical findings, not current source behavior.

The architecture defined `failed` as a terminal outcome at the video level (Tier 1 and the primary-outcome classification) but, at decision time, the Run entity `status` enum listed only `running | completed | interrupted` — the run-level vocabulary was missing the `failed` terminal state. The accepted [YuHaul System Architecture](yuhaul-system-architecture.md) now records all four states and the §Run Recovery rule: an unrecoverable processing-phase failure is terminal `failed`, a resumable interruption is `interrupted`, and normal completion is `completed`.

## Decision drivers

- Run status must distinguish **normal completion** from **unrecoverable failure** (OC-002 shared vocabulary; BR-032).
- Run recovery must distinguish **resumable interruption** (crash at a phase boundary) from **terminal failure** (a phase returned an error). Failed runs must not be auto-resumed.
- Minimal change: no schema migration; reuse the existing `interrupt_reason` column.

## Options considered

### Option A — Add `failed` as a fourth terminal Run status (chosen)

Add `RunStatusFailed = "failed"`; `failRun` and the panic path set `failed` with the reason, terminal timestamps, quota cost, and recomputed counters via a new `Machine.FailRun`.

- **Mechanism:** `Store.FailRun` writes `status=failed, interrupt_reason=reason, completed_at, duration_seconds, quota_cost`; `Machine.FailRun` mirrors `CompleteRun` (quota cost + duration + `RecomputeRunCounters`).
- **Benefits:** truthful status; `failed` is terminal and excluded from `ListInterruptedRuns` resume; no schema change.
- **Costs:** one new status constant + one new store/machine method; `interrupt_reason` is reused for both `interrupted` and `failed` (documented semantic broadening).

### Option B — Treat a failed phase as `interrupted` (resumable)

Keep three statuses; a phase error leaves the run `interrupted` and the scheduler auto-resumes it.

- **Mechanism:** drop the `completeRun` call in `failRun`; retain `InterruptRun`.
- **Benefits:** no new status; resume machinery reuses existing `interrupted` path.
- **Costs:** conflates an unrecoverable error with a resumable crash — the resume would immediately re-fail (e.g. "no active token" again) or re-run a panicked phase over partially-written state; the status is no longer truthful and the distinction the finding demands is not made. Rejected.

### Option C — Add a distinct `failure_reason` column

- **Mechanism:** schema migration adding `failure_reason TEXT` alongside `interrupt_reason`.
- **Costs:** migration + store/model plumbing for a single-user tool; the semantic gain (separating the two reason fields) does not change any consumer behavior because `status` already discriminates. Rejected as non-minimal.

## Decision

In the context of a pre-merge MAJOR where a failed phase is reported `completed`, facing the choice between a terminal `failed` status, a resumable `interrupted` status, or a schema change, we decided **for adding `failed` as a terminal Run status** and **against** treating phase failures as resumable interruption or adding a new column, to achieve truthful run status and correct recovery semantics, accepting one new constant and one new store/machine method.

The `interrupt_reason` column is redefined as *"the reason the run is not in a normal terminal state"* — populated for both `interrupted` and `failed`. No migration is required; `status` is the discriminator.

## Realization

- `internal/state/models.go`
  - Add `RunStatusFailed RunStatus = "failed"` to the `RunStatus` const block.
- `internal/state/store.go`
  - Add `FailRun(ctx, id int64, reason string, durationSeconds float64, quotaCost int) error` writing `status = RunStatusFailed, interrupt_reason = reason, completed_at = now, duration_seconds, quota_cost`.
- `internal/state/machine.go`
  - Add `FailRun(ctx, runID int64, reason string) (*Run, error)` mirroring `CompleteRun`: read run, compute quota cost, compute duration, call `store.FailRun`, call `store.RecomputeRunCounters`, return the run.
- `internal/pipeline/pipeline.go`
  - `failRun` and `Execute` panic recovery use `Machine.FailRun` and error broadcast, never failure followed by normal completion/advisories. The later ARD-001 diagnostic boundary supplies a vetted reason, not raw provider text or an unrestricted panic chain. A recovered panic is an unrecoverable internal error, not a clean phase-boundary interruption, so it is terminal (`failed`), not resumable.
- `internal/scheduler/scheduler.go` — no change: `ListInterruptedRuns` continues to return only `interrupted` runs, so `failed` runs are terminal and are not auto-resumed; the operator initiates a fresh run.

Implementation status (2026-11-06): production phase failures/panics use `Machine.FailRun` under a five-second finalization context detached from execution cancellation. After every production phase succeeds, `completeRun` retries only terminal completion bookkeeping under that bound if its first attempt fails; it does not run provider work or advisories on the detached context. Authority-retention failure first retries the atomic queue handoff with a separately bounded context. If all unfinished candidates are retained, the run becomes `failed`; if retention still fails, it remains resumable and `InterruptRun` is attempted under a fresh bounded context. Failure to persist that interruption is logged and can still leave status `running` while the store is unavailable. `internal/pipeline/production_authority_test.go` covers an actual SQLite writer lock extending beyond the execution and retention deadlines, then unlocks the database, verifies durable `interrupted` with no premature queue handoff, and resumes the run through `Execute`. Dry-run/replay/backtest failures/panics use `Machine.FailHistoricalRun`, which computes simulation counters from decisions before terminal failure. Existing `internal/pipeline/diag_boundary_test.go` covers expired-context interruption and completion against the real state store. Since 2026-11-09 ([authorization ADR-007](youtube-authorization/decisions/adr-007-asynchronous-startup-resume-and-shutdown-interruption.md)), a cancelled or expired execution context ends in `interrupted` through `InterruptRun`, not `FailRun`; the failure paths in this paragraph apply to every other production stop. The real-browser no-token journey shows persisted `failed` on Run Detail while Errors is empty. See ARD-001 for attributed reports; exhaustive finalization/crash composition is still unverified, and no status policy changed in this realization update.

## Consequences

- **Positive:** run status is truthful; operational checks on `status == "failed"` work; OC-002/BR-032 run-level vocabulary is complete; failed runs are not silently auto-resumed.
- **Negative:** a run that fails with "no active token" is terminal — it requires the user to connect/re-authorize and start a new run, rather than being transparently retried by the scheduler. This is intentional: auth re-connection is a user action, and re-attempting without it cannot succeed.
- **Neutral:** successful runs and resume-from-interrupt behavior are unchanged.

## Confirmation

- Unit/integration test: induce a discovery-phase failure (no active token) → `GetRun` reports `status == "failed"` with `interrupt_reason` populated and `completed_at` set; `ListInterruptedRuns` does not return it.
- Product Acceptance: after the fix, a run with a valid token and one without produce `completed` and `failed` respectively; the failed run's status is `failed`, not `completed`.

## Open questions

- None.
