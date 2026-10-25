---
type: specification
title: ARS-006 — Run-Attributed Production Outcomes
description: Bounded M1 correction for immutable production run outcomes and reasons, receiving-run counting, consistent inspection queries and truthful legacy unavailability.
tags: [architecture, specification, production, run-history, outcomes, retry]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted — run-local production outcome capture/projection implemented with migration 015; full contract conformance and own-server deployment are not established by this documentation reconciliation.

A run's result describes what happened in that run, not the video's latest state. For example, production run A records deferred with a quota reason; receiving run B later records accepted. A's list, detail and summary remain deferred, B counts one accepted video even though it discovered none, and the global video can now be completed. Unrecorded old results remain unavailable; they are not guessed from that completed video.

## Derived from

- [BR-015 — Explainable Route Decisions](../../../requirements/yuhaul/br-015-explainable-route-decisions.md).
- [BR-028 — Historical Run Inspection](../../../requirements/yuhaul/br-028-historical-run-inspection.md).
- [BR-032 — Processing Outcome Distinction](../../../requirements/yuhaul/br-032-processing-outcome-distinction.md).
- [OC-002 — Route Evaluation Outcome Semantics](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md), especially one primary classification, cross-run changes and shared vocabulary.

## Implements

- [ARS-005](ars-005-duplicate-continuation-compatibility-results.md), production retry ownership, immutable prior summaries and requested-run inspection. This specification supplies its previously separate production outcome materialization correction, not its original-versus-simulation comparison.
- [ARD-001 — Run-Owned Production Action Authority](../design/ard-001-run-owned-production-action-authority.md), executing-run authority and existing single-process production gate.
- [ARS-001](../../ars-001-route-and-playlist-api-contracts.md), existing outcome filter, pagination and error envelopes.

## Satisfies

| Obligation | Realization / boundary |
|---|---|
| BR-015 AC1/5; BR-028 AC2/3/6/9 | Durable run-attributed primary disposition and operational reason, with existing run-owned route/audit evidence and no live reconstruction. Other explanation/provenance/search obligations remain with existing evidence/API paths; complete satisfaction is not claimed here. |
| BR-028 AC1; BR-032 AC1/4/6; OC-002 Guarantees 1/8/9 | New production summaries count that run's recorded per-video classifications, including receiving work; terminal results do not change with later runs. |
| BR-032 AC2/3/5 | Preserve true not-yet-evaluated separately from unavailable history; shared list/detail/filter/count projection never substitutes rejection or current global state. |
| BR-028 AC7 | Failure reason capture is covered; complete historical retry-count presentation is not solved by this bounded correction. |
| ARS-005 original-comparison seam / BR-027 AC5 | Accepted future same-app production-versus-hypothetical comparison, explicitly deferred from first-use launch. The nullable comparison contract remains unimplemented; this run-local relation neither selects a baseline nor satisfies that future comparison. |

## Scope and grounded finding

Confirmed M1 evidence: `/tmp/opencode/yuhaul-launch-3d852ae/followup-current/C2-upstream-quota-v2/assertions.json:112–139,188–275` shows A's deferred summary but accepted list/detail, while B has a successful action and accepted=0. These supplied observations were read, not rerun here.

The pre-correction source finding explained the inconsistency (not the current implementation):

- `video.final_outcome`, reasons and completion are mutable video-wide state (`internal/store/migrations/001_initial_schema.up.sql:74–87`; `internal/state/store.go:329–338`). Retry resets them without moving discovery ownership (`internal/queue/retry.go:98`).
- Production `runVideoInspection` reads that mutable result; `RecomputeRunCounters` counts only discovery-owned videos (`internal/state/store.go:442–470,680–703`).
- Decisions/audits/actions have run attribution, but do not store every video's authoritative primary disposition, especially pre-decision work and partial/failed/deferred actions (`001_initial_schema.up.sql:131–169`).
- `run.video.processed` is an in-memory, droppable SSE message, not a persisted event table (`internal/pipeline/events.go:79–133`). A broadcast cannot recover historical truth.
- Queue origin and mutable latest retry owner establish work context, not the run's historical primary result. Existing retry acknowledgement reads global outcome (`internal/pipeline/retry.go:103–138`); corrected acknowledgement must use captured receiving-run evidence.

Semantic navigation returned degraded gopls (`Option is not a function`); direct source reads ground these claims. No history reconstruction, new classification precedence, simulation baseline, external idempotency mechanism or broad analytics redesign is in scope.

### Implementation status and evidence limits

Source inspection on 2026-10-25 confirms migration `015_run_video_outcome.up.sql` creates the empty run/video relation without backfill. `internal/state/store.go` records production dispositions and projects production list/detail/filter/count from that relation; absent legacy capture stays null rather than using current global outcome. Production counters use the executing run's recorded categories, independently of discovery. `internal/state/machine.go` combines lifecycle/disposition writes, and `internal/pipeline/pipeline.go` captures accepted outcome in its local finalization transaction. `internal/server/run_handlers.go` emits nullable `final_outcome`; this is distinct from ARS-005's unimplemented `original_production_outcome` comparison field.

`TestProductionRunVideoOutcomesAreRunLocal` in `internal/state/inspection_test.go` covers separate A-deferred/B-accepted projections, reason/completion separation, uncaptured legacy unavailability and receiving-run acceptance counting. These tests and source were read, **not executed in this reconciliation**; the verification matrix below remains a conformance target, not a blanket completion claim.

**Observed implementation gap, owned by implementation:** retry admission currently inserts `not_yet_evaluated` (`internal/queue/retry.go:92–94`), rather than invariant 2's required null; the writer also retains an existing provisional classification when asked to clear it to null (`internal/state/store.go:297–301`). This status update does not weaken those invariants or certify full conformance.

First-use focus is subscription → playlist. [BR-027 AC5](../../../requirements/yuhaul/br-027-historical-backtesting.md) defers same-app production-versus-simulation comparison while retaining it as accepted future scope; simulation isolation remains mandatory. No selected comparison baseline is currently available from this implementation. Own-server deployment is postponed/unverified; no live-provider, deployment or new assembled-product acceptance proof is claimed here.

## Decision and alternatives

Persist one small run/video disposition relation in existing Store B. Reuse the production classifier, execution gate, SQLite transactions and requested-run inspection query.

| Option | Benefit | Decision / failure mode |
|---|---|---|
| Keep global projection; fix B's counter only | No schema change | Reject: A still changes after B, violating durable run inspection. |
| Reconstruct from decisions/actions/queues/SSE | Reuses current evidence | Reject: no complete primary-disposition source; a route match, partial action set or overwritten retry owner cannot establish it. SSE is not durable. |
| One run/video disposition row beside existing evidence | Explicit attribution, ordinary local transactions, one row per video | **Choose:** smallest complete M1 capture/projection. |
| Persist/replay all events or add generations/outbox | Richer history | Reject: M1 needs a recorded run disposition, not an event protocol, replay engine or new external transaction guarantee. |

The cost is one forward table migration, explicit run context at disposition writes, nullable-aware inspection clients and transaction integration. No new run-version marker is needed: each row directly attests capture, not engine authority or complete legacy history. Existing `action_authority_version` remains exclusively execution-admission evidence.

## Interface / schema

### Persistence

Forward migration **`015_run_video_outcome.up.sql`** creates **`run_video_outcome`**. Never edit an applied migration or backfill guessed records.

| Column | Type and exact constraint / meaning |
|---|---|
| `run_id` | INTEGER NOT NULL; FK `run(id)` with default NO ACTION deletion behavior. |
| `video_id` | INTEGER NOT NULL; FK `video(id)` with default NO ACTION deletion behavior. Internal key, not YouTube identifier. |
| `final_outcome` | Nullable TEXT. Non-null values constrained to `accepted`, `rejected`, `excluded`, `deferred`, `failed`, `not_yet_evaluated`. NULL is unavailable/uncaptured classification, not a seventh outcome. |
| `deferred_reason` | Nullable TEXT; non-null only for deferred. Deferred requires a nonempty known reason. |
| `error_message` | Nullable TEXT; non-null only for failed. Failed requires a nonempty diagnostic. |
| `completed_at` | Nullable TIMESTAMP; actual video completion captured by this run when applicable, never a later global completion or zero-time placeholder. |
| `updated_at` | TIMESTAMP NOT NULL; explicit UTC writer value for the last actual capture/change, not an inferred historical timestamp. |

Primary key is **`(run_id,video_id)`**. Enforce vocabulary and conditional reason constraints in schema and writer; missing required reason is a recording failure, not invented text. The primary key supports run reads/counts; no event ID, generation or additional index is justified yet. Route/condition/exclusion explanations remain in existing evidence, not duplicated JSON. Do not copy transient titles, descriptions or other metadata into this table.

### Writer operation

The state/pipeline owner records with explicit executing run ID, internal video ID, primary outcome (or explicit uncaptured admission), reason and applicable completion time, inside a caller-owned SQLite transaction. It returns success only after validation/write success; the transaction owner controls commit. Do not infer executing run from discovery ownership, latest run or queue origin. Require an unfinished manual/scheduled run; simulations cannot write this relation.

The operation is idempotent for an identical repeated disposition, preserving timestamps. Fresh admission is insert-if-absent, never an upsert that clears a recorded result. Valid lifecycle progress in an unfinished run may replace its provisional/operational disposition. Terminal-run inserts/updates and conflicting rewrites without actual authorized lifecycle progress fail as integrity errors. This is an application invariant under the existing supported single corrected writer, not protection against hostile direct SQL.

## Invariants and lifecycle

1. **Fresh discovery:** create its outcome row in the same transaction as the new video, recording genuinely observed not-yet-evaluated. On entering evaluation, clear that provisional classification to NULL in the same transaction as the lifecycle transition; a started but unfinished evaluation is not rejection. Resume does not reset existing captured progress.
2. **Fresh retry claim:** insert one receiving-run row with NULL outcome/reasons/completion in the existing group-claim transaction, before metadata/evaluation. A reset of previously processed global work is not evidence it was never evaluated. Sibling rows produce one membership record. A losing claim creates no record and changes no global state. Same-owner resume does not recreate/reset the row. Durable membership survives later replacement of queue retry ownership.
3. **Actual disposition:** rejection, full exclusion, operational deferral, retained failure and acceptance record the existing classifier's primary result and reason in the **same local transaction as the corresponding global lifecycle/disposition change**. The global state machine remains authoritative for current processing; this is a run-attributed record, not a second classifier. A match, default executor return or successful action row alone is insufficient proof of acceptance. Existing mixed-action precedence is unchanged.
4. **Accepted finalization:** extend `markVideoAccepted`'s local state/channel-counter transaction with the row write. Reconciliation of an already durable current-run disposition does not repeat global transitions/counters. Prior exact-target production completion remains an action veto under ARS-005, not a fabricated new `succeeded` action/timestamp; record the receiving run's actual authorized classifier result rather than copying the earlier primary result.
5. **Queue handoff:** capture the receiving disposition before acknowledging/releasing its group. Where acknowledgement/requeue or error retention also establishes disposition, combine them in the existing queue transaction. An earlier committed current-run disposition can be reused by the acknowledgement transaction. Global video outcome alone is no longer acknowledgement evidence. Further quota deferral records B's reason before requeue and leaves A's row untouched.
6. **Terminal immutability:** completed/failed runs and their outcome rows are read-only to retries, callbacks, inspection and repeated finalization. A new receiving run never updates an earlier run's row, summary, decisions, audits or actions. The global lifecycle may continue changing normally; discovery ownership remains original.
7. **Publication:** processed SSE follows committed capture and uses the same run/outcome. Before publishing completed or failed status/summary, persist that run's dispositions/retained-work handoffs and final known counters; do not expose a terminal run and then mutate its history. SSE stays best-effort and is not a store. Counter/capture failure cannot publish completed or successful acknowledgement.
8. **Local rollback:** capture failure rolls back its state/counter/queue changes and preserves existing interrupted/retained-work recovery. Provider insertion cannot share SQLite atomicity: this contract does not close the existing provider-success/local-action-record crash window.

## Exact query and API contract

Use a single shared requested-run inspection relation for list, detail, filter and total. The following relational rules are normative, not optional query heuristics:

1. For production R, member IDs are the **distinct union** of original discoveries in R, videos with decisions in R, and `run_video_outcome.video_id` where `run_id=R`. For still-owned legacy pre-decision work also admit deferred/error IDs with `retry_run_id=R`; that proves membership only, not classification or earlier overwritten owners. New claims always have a durable outcome-relation row. Do not multiply members by route/action/queue joins.
2. Join the outcome relation on **both `(R,video_id)`**. Production `final_outcome`, `deferred_reason`, `error_message`, `completed_at` come **only** from that row. No row or NULL classification emits explicit JSON **`final_outcome: null`**. Missing reason/completion evidence uses existing optional-field null/omission, never global fallback. No `unknown` string or empty string is an outcome.
3. `processing_state` remains the **current global lifecycle** field, not a historical snapshot or source of classification. Clients cannot fill/explain historical results from it; if displayed alongside history its current-state meaning must be clear. Discovery identity/time remain original. Detail decisions/audits/actions remain strictly requested-run scoped.
4. Apply the existing six-value equality filter to projected `final_outcome` **before count and LIMIT/OFFSET**. NULL matches no named category, including not-yet-evaluated. Absent/empty filter includes unknown rows. Preserve unsupported-value 400; no unknown filter is added. Count and page selection use one SQLite read transaction/snapshot, ordered by **`discovered_at DESC,id DESC`**, one member once. Detail uses identical membership/projection and existing nonmember 404.
5. List video objects and detail's `video` object expose the same six-value-string-or-null `final_outcome`. Update the bundled adapter/domain model with the producer; preserve null as unavailable, without category inference. An older server's outcome string is not newly certified historical evidence. No second independently derived `run_outcome` field is added. Real store failures stay non-2xx, never successful unknown/empty fallback.
6. Simulation projection remains ARS-005's requested-run hypothetical projection. Simulations do not write this relation or mutate production state. The separate `original_production_outcome` field **continues to emit only null**: having a new run-specific record does not choose which actual is the original simulation baseline.

### Summary attribution

For a production run being finalized, each of its six outcome counters is the count of relation rows with **that run ID and that non-null category**, once per video. Do not restrict by discovery ownership or count routes/actions/siblings. `videos_discovered` retains the count of actual original discoveries (`video.discovered_in_run_id=R`), not processed workload. Thus B may correctly report discovered=0 and accepted=1; outcomes need not sum to discoveries.

Unknown rows count in no category, never rejected/not-yet-evaluated. A new successful production run cannot complete with uncaptured admitted work; genuinely untouched fresh discoveries explicitly recorded not-yet-evaluated remain distinct. Do not fabricate classifications to pass that check. Failed/interrupted runs retain known records and unfinished work under existing failure rules. Classification gaps, including zero-enabled-route policy if existing behavior cannot supply an authorized result, remain with their owner rather than being solved by this storage contract.

Never recompute or rewrite a preexisting terminal run's stored summary. A resumed pre-upgrade run may capture genuinely new dispositions and aggregate those known rows, but missing earlier classifications remain unknown; its distribution must not be presented as a recovered full history. This specification does not revise unrelated lifetime-statistics queries or simulation accounting.

## Compatibility and evolution

Migration creates an empty relation. Existing terminal runs, snapshots, discoveries, decisions, audits, actions, queue origins and aggregate counts remain unchanged. Old per-video primary outcomes/reasons are unavailable without an actual corrected capture. No backfill from matched decisions, successful actions, queue status, current global outcome, authority version or aggregate totals is authorized. Old A can truthfully retain aggregate deferred=1 while V's classification is null: that aggregate cannot identify V's result. This acknowledges lost evidence instead of labeling B's later acceptance as A's outcome.

Stop old writers before migration/updated workers, as already required by ARS-005. Deploy the nullable-aware bundled client with the API. No separately versioned external-client guarantee exists in ARS-001. Old binaries cannot write outcome-bearing runs: they could change global state without capture. Existing pre-upgrade-database restore restrictions remain the downgrade boundary. No simulation-baseline/history-reconstruction storage is authorized.

## Verification

Required downstream proofs, **not tests executed here**; use real SQLite/pipeline/HTTP composition, replacing only external YouTube through the existing provider seam:

1. Repeat M1: A matches then defers at insertion; B receives and inserts. A list/detail/filter stay deferred with quota reason and unchanged summary/evidence. B list/detail show accepted with its own action; summary accepted=1/discovered=0. Global video completes with original discovery ownership. Repeat reads after DB close/reopen and a third unrelated run.
2. B instead rejects, fully excludes, permanently fails or defers again. Each run retains its own disposition/reason and one count. Include pre-decision quota failure with no decision rows and sibling queues yielding one member/result/count. Later claims/reset cannot alter earlier rows.
3. Completed legacy A has matched decisions, B's later successful action and global accepted, but no outcome row. A list/detail emit null with no leaked reasons/completion; named filters exclude unknown while unfiltered includes it. Original summary/SQL history are byte-identical. Authority marker=1 or seemingly matching aggregates cannot permit reconstruction.
4. All six recorded categories plus NULL, tied discovery times, multiple routes/actions/siblings, more than two pages and an unrelated video. List/count/detail agree with deterministic ordering and filtered totals. A count/page interleaving proves one read snapshot. No duplicate, missing member or cross-run evidence leaks.
5. Interrupt/reopen after claim before evaluation, after disposition before acknowledgement, and before completed-status publication. Membership survives; recorded disposition reconciles acknowledgement without repeating actions/counters. Capture failure inside the global-state transaction rolls back, emits no processed SSE, does not acknowledge lost work and leaves recoverable work. Counter failure cannot publish completed.
6. Identical disposition writes preserve timestamps; repeated finalization does not double-count; terminal row/summary rewrite fails. Migration adds no guessed rows. Simulations write no production outcomes. Bundled list/detail models preserve null, distinguish real not-yet-evaluated and do not infer from current processing state. Original-comparison field remains independently null.

## Open questions and limits

- **Requirements owner, nonblocking for confirmed single-action quota/retry M1:** mixed-action primary precedence and zero-enabled-route classification if existing behavior cannot supply an authorized primary result. This specification records classifications; it cannot invent them.
- **Requirements/design owner, blocks only future positive original comparison, not first-use launch:** ARS-005's discovery-run versus pre-simulation/selected-baseline and temporal comparison meaning remains unresolved. BR-027 AC5 explicitly defers that same-app comparison; run-local production capture does not supply its baseline.
- **Implementation/verification owners:** complete failure retry-count presentation, already-applied inspection and provider-success/local-record reconciliation remain separate conformance surfaces, not satisfied by these fields.
- **Independent review:** Critic session `ses_e8c0f9f14ffe1FskD05v9ImVOV` approved the bounded contract after source/document inspection, with no material contradiction or simpler complete alternative found. This does not establish implementation conformance. Migration/capture/projection source now exists as recorded above; full runtime/assembled-product proof and the noted admission correction remain outstanding here. No runtime test, new independent implementation review or Product Acceptance occurred in this reconciliation.
