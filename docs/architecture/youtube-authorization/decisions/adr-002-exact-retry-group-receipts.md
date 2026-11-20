---
type: decision
title: ADR-002 — Exact Retry Group Receipts
description: Preserve claimed queue-row identity through guarded dispatch and atomic retained-work handoff without recreating removed retry work.
tags: [architecture, decision, retry, concurrency, idempotency]
timestamp: 2026-11-18T00:00:00Z
---

**Status:** decided technical correction; implementation and independent conformance proof outstanding.

Carry the exact queue rows claimed for a retry through execution, restart and disposition. For example, a claim of row 1 still names only row 1 after restart even if row 2 now has the same owner. Row 2 blocks exact validation; it cannot become part of the claim. A removed retry is never reinterpreted as fresh work. Removing one sibling does not, by itself, invalidate another still-owned sibling; the applicable disposition still decides whether a new sink is permitted.

## Derived from

- [Authorization ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md), sections State and retained-work transaction ownership and Positive pre-admission authorization refusal.
- [ARS-005](../../duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md), durable deferred/error claim and sibling-alias contracts.
- [ARS-006](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md), receiving-run evidence and atomic disposition.
- [BR-014](../../../requirements/yuhaul/br-014-failed-work-retention-and-retry.md), [BR-013](../../../requirements/yuhaul/br-013-quota-limited-work-deferral.md), [BR-030](../../../requirements/yuhaul/br-030-temporary-failure-work-preservation.md), [BR-034](../../../requirements/yuhaul/br-034-automatic-youtube-token-refresh.md) and [OC-006](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md).

## Satisfies

| Authority | Classification and realization |
|---|---|
| BR-014 AC3–5; existing explicit-removal rule in authorization ARS-001 | Satisfied by exact claimed-row validation, preserved removal/history and suppression of fallback resurrection. |
| BR-013 AC2–3/6; ARS-005 sibling and ownership rules | Satisfied by one still-owned group handoff/requeue, not duplicate insertion or same-execution retry. |
| [BR-011 AC1, AC3–7](../../../requirements/yuhaul/br-011-processing-state-persistence.md); [BR-035 AC9–13, AC16](../../../requirements/yuhaul/br-035-stop-active-run.md) | Durable exact identity closes the restart ambiguity. Startup classification and the atomic Stop/Discard handoff are specified in authorization ADR-005 and run-initiation ARS-003/005. |
| BR-030 AC1–6; BR-034 AC7; OC-006 G4–5 | This artifact satisfies the ownership boundary: one pipeline-owned ordinary failure handoff, preserved eligible work and independent guard without defeating explicit removal. Renewal, adapter facts and full work-recovery proofs are satisfied elsewhere by authorization ARS-001 and its implementation obligations. |
| Requested-run outcome, action evidence, quota accounting, status/UI and simulation isolation | Satisfied elsewhere by authorization ARS-001 and ARS-005/006; unchanged. |
| Date selection, historical recovery permission and other unresolved product policy | Outside this correction; not resolved or authorized here. |

## Decision question

How does guarded resolution distinguish a still-owned retry group from removed retry work and genuinely fresh work after provider/auth waits, without two layers disposing the same claim?

## Drivers and constraints

- One application process, shared production execution gate and existing SQLite writer transactions remain the deployment model.
- Queue rows have stable IDs and immutable origin, but `retry_run_id` is mutable ownership/history, not the original admitted set. In disposable migrated SQLite, `TestReceiptRecoveryUnexpectedSiblingProbe` claimed `[{1 1}]`; after inserting a same-owner sibling, validation rejected the original but accepted `[{1 1} {2 1}]` reconstructed from owner history (Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`, report `c6fb0f87-e690-4a65-9b57-58a0a25e7913`). This is executed defect evidence, not proof of the correction.
- Siblings are aliases for one video evaluation, not independent action intents. Validate both queues, but automatic deferred admission does not claim unrelated active errors.
- No SQLite transaction spans network work. Removal is not cancellation of an admitted request and never clears its independent guard.
- A fresh work marker must be explicit; missing retry evidence is not a fresh-work marker.

## Options considered

| Option | Benefits | Costs and failure modes | Obligation impact |
|---|---|---|---|
| Re-query latest origin/category or only `video_id`/receiving owner at handoff | Smallest local patch; no schema change. | Loses the admission set, confuses missing work with fresh work, and can pick unrelated history. A category change cannot identify the originally claimed error. | Rejected: demonstrated removal/reanimation defect remains. |
| Carry the exact set only in memory, recover it from owner history | No migration; works for the live invocation when the original set survives in memory. | The executed counterexample shows that recovery can adopt an unexpected live row. | Rejected: cannot protect restart dispatch or startup Stop. |
| Store the exact set on existing receiving membership | One additive column; the original set survives removal, release and restart. | Requires compatible writers, an upgrade precondition and live-row validation under the writer; a stored descriptor is not permission. | Chosen: smallest complete realization for one claim per receiving run/video. |
| Separate immutable member table | Also preserves the exact set; useful if independent member queries or lifetimes become necessary. | Adds joins, writes and lifecycle coordination. A generation or digest alone could not enumerate an original member whose row is now missing. | Not selected: no independent membership lifecycle or query is required. |

## Decision

Use an immutable `RetryClaimReceipt` containing receiving run, video and the exact deferred/error row references. Persist it when the fresh claim commits; never infer or backfill it from owner history. Queue owns construction, recovery and validation. Pipeline owns propagation and run ending; executor owns action evidence and the send receipt. [Run-initiation ARS-005 §5.1.1](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md#511-durable-retry-claim-provenance) owns the storage and compatibility contract; [authorization ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md#exact-retry-ownership-receipt) owns its use at both send fences and disposition. This decision corrects the previous no-schema assumption (Action result `64a67036-3334-492b-8b2d-1e00670397f6`); it does not allocate a migration number or change release order.

Reuse the existing transaction callback rather than teach auth/state about queues or create a new worker. The callback captures an explicit fresh/retry origin and performs queue validation and lifecycle writes under the executor's existing writer transaction. Remove executor-side latest-row/`MAX(run_id)` ownership inference. Apply the same receipt check to generic runtime-error fallback, not only authorization/quota resolution.

### Failed guarded handoff fallback

Keep the original exact claim and independent send guard when a guarded refusal's composed handoff fails. Return the wrapped handoff/storage failure, not successful processing and not a removal inferred from the absence of a different error sink. This is a retryable **local persistence failure**, not permission to repeat the playlist request or clear the guard. Existing failure/interruption and failed-owner reclaim rules remain unchanged.

For example, a deferred-only retry receives a recognized quota rejection, but an injected write failure prevents its exact deferred row from returning to `pending`. The transaction rolls back. That row remains `retrying` with the same receiving owner, and its guard remains. There need not be any receiving-run runtime error row. A lookup for that nonexistent row cannot overturn the positive exact-claim validation or establish `ErrWorkRemoved`.

| Alternative | Result and decision |
|---|---|
| Preserve the exact source and propagate the original failed-handoff cause | Chosen. The durable claim set plus validated live ownership is the recovery path. No additional sink, worker, persisted state or automatic transaction retry is needed for this rollback. |
| Return nil, acknowledge the source, or treat the provider rejection as a committed deferral | Rejected. The handoff did not commit. Acknowledgement consumes the recovery path; nil hides the persistence failure. |
| Interpret an absent receiving-run runtime/category sink as removed retry work | Rejected. Exact live deferred/error rows, not the new diagnostic's category/origin lookup, determine source authority. |
| Create a runtime sink and acknowledge the deferred group after this failed handoff | Not selected for this rollback path. It replaces the required preserved original ownership with another disposition and adds another fallible write. Legitimate ordinary failure handoffs elsewhere remain allowed under the existing exact-receipt contract. |

**Minimal correction boundary:** `processRetryVideo` owns the outer fallback. After writer-transaction validation proves a still-owned deferred-only source for this failed handoff, retain it unchanged and propagate `workErr` with its original storage cause; do not invoke an error-only retention helper or acknowledgement. Preserve that cause through the executor and pipeline wrappers as well. If current exact validation instead finds removal, conflicting ownership or a storage-read failure, preserve that distinct result rather than pretending ownership still holds. Neither error-message matching nor a lookup for a newly classified runtime error can substitute for receipt validation. No new persisted lifecycle state is warranted.

Generic sink creation is prohibited after removed/not-owned validation, when no exact live source remains, when retry context is missing/invalid, and as a substitute for this rolled-back guarded handoff. It is **not** prohibited for all retries: a separately valid failure handoff may create/prove a receiving-run sink under the same writer transaction as exact source validation and applicable disposition/acknowledgement. A genuinely fresh origin retains its own permitted sink path. Source authority does not require an existing error of the new failure's category.

### One owner for ordinary retry failure

**Choose the pipeline's `processRetryVideo` as the sole generic retry-failure disposition owner.** For retry-origin work, `Executor.retainActionFailure` must not reactivate, recategorize, clear ownership of, or acknowledge source rows. The executor returns the failure and preserves truthful current/tail action evidence and the independent guard. The outer pipeline composes one failure transaction for failures from actions, evaluation or other retry phases. Fresh non-retry retention remains separate and must not become a fallback for missing retry context.

For example, two original error rows are claimed by receiving run B. One is dismissed while the POST waits; the provider returns HTTP 500. The surviving row remains `retrying` under B until the pipeline validates the original receipt. In one transaction the pipeline establishes one active **receiving-run runtime error** for B/video, records B's actual failed disposition together with its global lifecycle change, and resolves only the surviving exact source. The dismissed row and its history stay untouched. The failed admitted-action evidence and send guard survive; the retained error does not authorize another POST.

| Alternative | Benefit | Cost or failure mode | Decision |
|---|---|---|---|
| Outer pipeline owns generic failure handoff; executor only supplies action facts | Covers pre-action and action failures at their existing common seam; reuses `WithRetryTx`, runtime retention and run-video transaction writers. | Retry-origin lifecycle failure writes must join this transaction rather than commit early in `executeVideoActions`; cause/type propagation must survive wrappers. | Chosen: least new machinery, one acknowledgement authority. |
| Executor owns a complete generic handoff and returns a committed disposition | Can atomically compose its action facts with a pipeline callback. | Executor cannot cover pre-action failures; needs another owner for those paths and a committed-handoff protocol for the outer loop. | Viable with more coordination, but unnecessary for generic uncertainty, which never deletes the guard. Existing conclusive auth/quota callback composition stays unchanged. |
| Executor releases source errors; outer loop validates/retains again | Small local changes. | Already demonstrated: live errors become `active`/unowned, so the original receipt correctly fails; removing that write still leaves a nonexistent receiving-run runtime lookup. | Rejected: inconsistent ownership, not a validator defect. |

**Minimal transaction seam:** a pipeline-owned `FinishRetryFailureTx` operation accepts the caller-owned writer transaction, original exact receipt and bounded failure facts, and returns the exact retained sink ID or a typed failure. It composes existing queue validation/retention/exact acknowledgement and state lifecycle/outcome writers; names are descriptive, not mandated Go signatures. The [ARS](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md), section Ordinary retry failure transaction, defines ordering and reconciliation. It reuses the persisted claim set; it needs no separate handoff table, new outcome/category, worker or automatic database retry. Reuse `RetainRuntimeErrorTx` only behind successful exact validation and verify the actual active sink; `RetainRuntimeErrorForRetryTx` is not this seam because it only looks for an already-owned runtime row. An absent receiving sink is an insertion opportunity only while a live exact source authorizes this ordinary handoff.

Pipeline source ownership must remain intact until this transaction commits. Do not first release errors and then weaken the validator to accept `active`, acknowledge the new sink as a source, or recategorize old source diagnostics. Completed/resolved sources retain their origin, diagnostic/history and most-recent receiving owner for recovery and same-execution deduplication. A sink belongs to receiving run B, while the source rows keep their original runs.

Distinguish ordinary work failure from a failed composed guarded handoff using internal typed stage/cause information, preserved through wrapping; error text and the receipt's queue mix cannot establish that distinction. The latter follows the preceding preserve-and-propagate decision even with error siblings. Removed/not-owned/storage failures also stay distinct. A committed failure handoff is a handled **failed-video** disposition under existing run semantics, not fabricated video/run success and not a reason for another generic retention pass.

**Evidence and remaining owner:** Direct reads of `executor.go:397–444`, `retry.go:147–170`, `pipeline.go:838–866` and `queue/retry.go:430–446,484–537` show the inner release, outer nonexistent-sink lookup and separately committed lifecycle failure. Debugger Action record `40674881-7e78-427c-afe4-14b7ac44784e` reports controlled real-pipeline overlay reproductions with and without dismissal and with inner retention suppressed; that runtime evidence is attributed, not rerun here. Accept the double-disposition diagnosis; retain the exact validator. Pipeline implementation owner must realize this seam and its assembled tests; independent implementation review remains required.

**Source findings, not runtime proof:** At inspection, `internal/pipeline/retry.go:147–161` validates the receipt, then calls `RetainRuntimeErrorForRetryTx` with receiving-run origin. `internal/queue/retry.go:484–500` searches a runtime error and maps no row to `ErrWorkRemoved`. The deferred source in `TestClaimedDeferredRetryRequeueWriteFailurePreservesGuardAndActionTransaction` was not removed. Its `require.NoError` assertion at line 182 is stale; the inaccurate removal classification is a separate implementation defect. Also inspect `executor.go:297–314,415–439`: generic failure retention can encounter the same deferred-only/no-error distinction before the outer fallback. These are implementation-owner seams, not authority to weaken rollback or invent a new public error policy.

**Regression expectations:** The injected requeue failure returns a non-nil error preserving the transaction failure, not `ErrWorkRemoved`/`ErrWorkNotOwned` merely for an absent error sink. After the full `processRetryVideo` return, the exact deferred row retains its ID/origin/reason/history, `retrying` status and receiving owner; the guard remains; rolled-back current action/deferral evidence is absent; no replacement active runtime/deferred sink or successful acknowledgement exists; exactly one POST occurred, with actual-call accounting preserved independently. Contrast real removal, wrong owner, surviving owned siblings and successful committed handoff/reconciliation. The latter must remain handled deferral without a second POST or generic failure conversion. A later invocation/reopen must still observe the guard veto; removing the injected database fault alone does not authorize resolving an old guard.

## Consequences

One additive schema change is required; no HTTP or status vocabulary change. Existing origin, reason, diagnostic and creation history remain unchanged. A stale, missing or removed expected row alone is not an insert opportunity. New retained sinks remain allowed for genuinely fresh work and for a still-owned retry group making a valid retained handoff. Surviving owned siblings can proceed without reviving removed siblings.

Queue helpers distinguish removal, ownership loss, unavailable or invalid provenance, and actual storage failure. A recovered queue receipt never reconstructs a live playlist-send receipt. Live callbacks still validate current rows under the writer. No cryptographic capability or hostile in-process caller model is introduced.

## Confirmation and reconsideration

Require the real pipeline/queue/SQLite/auth/adapter race tests in authorization ARS-001, section Exact retry ownership receipt. A helper-only test or typed refusal injected instead of the real auth.Service composition is insufficient. Independently review the correction before enabling guarded production. This document did not execute runtime tests.

For ordinary provider-500 retry failure, require the no-removal control, one removed sibling, all removed, deferred-only and mixed-source cases through the full `processRetryVideo` return, plus rollback and reopen/reconciliation. Inspect source identities/history/owners, the exact active receiving-run sink, global/requested-run failure, actual action/tail facts, one POST/accounted attempt and persistent guard. Include a wrapped failed auth/quota handoff with error siblings to prove it is not converted into this ordinary handoff. Reconsider owner placement only if a demonstrated caller needs a complete generic failure disposition without the pipeline; do not add a committed-disposition protocol merely to preserve the defective inner write.

Reconsider a separate persisted generation only if multiple executions may concurrently own the same run/video, same-run reclaims become permitted, or a new writer can replace/reassign exact rows outside the accepted lifecycle. Do not add it merely to make the durable row set resemble the separate playlist-send receipt.

## Open questions

No product ambiguity blocks this correction. BR-014 permits removal; existing ARS contracts already distinguish explicit removal, still-owned siblings and fresh work, and do not promise cancellation of admitted requests. Implementation defect and assembled proof remain open with the auth-send-guard implementation/review owners. User-reserved date/recovery questions remain outside scope.
