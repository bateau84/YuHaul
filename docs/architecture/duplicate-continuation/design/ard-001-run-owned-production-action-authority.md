---
type: design
title: ARD-001 — Run-Owned Production Action Authority
description: Bind production actions to the run snapshot, independently admit legacy evaluations, and preserve deferred video-group ownership across recovery.
tags: [architecture, design, production, isolation, snapshots, recovery]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted — correction implemented with local tests as detailed in ARS-005; independent acceptance remains outstanding.

A simulation may describe a playlist addition, but cannot give a later production run permission to make it. A production run may act only on its own matched decisions, using its own captured configuration. If a run captured `PLcaptured`, an edit to `PLchanged` affects a later run, not that run's remaining actions or restart recovery.

Deferred work is different from resuming an interrupted run. A later production run evaluates the deferred video again using the configuration captured when that later run started. It does not replay yesterday's matches. Already completed identical playlist actions remain completed; reevaluation does not undo them or repeat them. For example, if yesterday's first playlist addition succeeded and the second was deferred, today's evaluation can skip the completed target and execute only today's remaining eligible targets.

An interrupted run written by the old engine needs an extra check. Its own decision may have used edited live conditions rather than its snapshot. Recovery independently evaluates the snapshot without reading old decisions as answers, and admits the old evidence only if it agrees completely. A recorded `jazz` match against a captured `classical` condition fails closed: no playlist call, no rewritten decision, and unfinished work stays inspectable for retry.

The correction reuses ConfigVersion, explicit-route evaluation, per-run decisions, the queues, and executed-action evidence. Three small forward schema extensions are needed: nullable receiving-run references on deferred and error entries for pre-decision retry recovery, and a run producer-version marker separating new snapshot-owned evaluations from legacy evidence. The error owner is needed when an incompatible legacy evaluation is retained as a failure and manually retried; leaving that retry owner only in memory would lose work before its first new decision. It adds no action-intent service, separate simulation database, or general run-video ledger. Existing gaps in provider-side crash reconciliation and broad historical outcome accounting are identified below, not declared solved.

## Derived from

- [BR-003](../../../requirements/yuhaul/br-003-multiple-ordered-route-definition.md), especially AC11: edits take effect on the next evaluation run.
- [BR-004](../../../requirements/yuhaul/br-004-route-conditions-and-playlist-actions.md), [BR-005](../../../requirements/yuhaul/br-005-multi-route-video-matching.md), [BR-006](../../../requirements/yuhaul/br-006-final-route-termination.md), and [BR-007](../../../requirements/yuhaul/br-007-exclusion-behaviors.md).
- [BR-008](../../../requirements/yuhaul/br-008-exact-and-fuzzy-duplicate-detection.md), AC7 and AC9–AC11.
- [BR-011](../../../requirements/yuhaul/br-011-processing-state-persistence.md), [BR-012](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md), [BR-013](../../../requirements/yuhaul/br-013-quota-limited-work-deferral.md), [BR-014](../../../requirements/yuhaul/br-014-failed-work-retention-and-retry.md), [BR-015](../../../requirements/yuhaul/br-015-explainable-route-decisions.md), and [BR-030](../../../requirements/yuhaul/br-030-temporary-failure-work-preservation.md).
- [BR-025](../../../requirements/yuhaul/br-025-configuration-history-for-statistics-interpretation.md), [BR-026](../../../requirements/yuhaul/br-026-dry-run-evaluation.md), and [BR-027](../../../requirements/yuhaul/br-027-historical-backtesting.md).
- [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md), [OC-002](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md), and [OC-003](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md).
- [System architecture](../../yuhaul-system-architecture.md), especially Deferred Work Recovery's accepted current-at-next-run semantics.
- [ADR-012](../../adr-012-proposal-route-identity-anchors.md), exact saved-configuration capture and inactive proposal identities.
- [ADR-017](../decisions/adr-017-fixed-duplicate-continuation.md) and [ARS-005](../specifications/ars-005-duplicate-continuation-compatibility-results.md).

## Satisfies

| Obligation | Disposition and owner |
|---|---|
| BR-003 AC11, BR-004 AC6; OC-003 Guarantees 1–6; BR-025 | Realized here: all production exclusions, conditions, final/order/enabled rules, threshold and action targets use the run-linked snapshot. Existing history/diff/UI contracts remain elsewhere in ARS-001/004/005. |
| BR-008 AC9; BR-026/027 isolation | Realized here: simulation evidence cannot authorize later production execution, directly or through queue recovery. Fixed continuation and read-only simulation detection remain ADR-017/ARS-005. |
| BR-011 AC1–6; BR-013; BR-030 | Realized here for deferred admission and same-run restart: durable receiving-run queue owner, resumable current-run evaluation, no loss before the first decision. External API failure categories remain OC-001 and the existing queue contract. |
| BR-003 AC11; BR-011 AC6; BR-014/015/030; OC-003 | Realized here for upgrade admission: new-run producer marker; independent snapshot reevaluation of legacy evidence before reuse; contradictions retained as inspectable runtime failures, not rewritten history or false routing rejections. |
| BR-012; BR-005 AC4 | Preserve and extend durable completion lookup across production runs for the exact action target. Full crash-between-provider-success-and-local-record conformance is not established; see Failure and recovery. |
| BR-015; OC-002 | New decisions/actions reference the actual receiving run, video and snapshot route. Old evidence is not moved or rewritten. Primary operational outcome rules remain OC-002; broader mixed-result historical accounting is not decided here. |
| BR-003–007 route semantics | Satisfied elsewhere by the existing explicit-route evaluator and ARS-004 identities; this artifact changes the authority source, not predicates or ordering semantics. |
| BR-008 matching/provenance and reference eligibility | Satisfied elsewhere by ADR-013/014 and ARS-002/005; neither simulation nor changed retry configuration rewrites recorded pairwise evidence. |

No accepted isolation or snapshot obligation is intentionally deferred. Local evaluation/action and retry composition is recorded in ARS-005; independent acceptance and the stated remaining inspection/crash proofs are outstanding. The existing incomplete provider-crash idempotency guarantee must not be represented as satisfied by the database uniqueness constraint.

## Problem and evidence

The independent reviewer log `/tmp/opencode/reviewer-duplicate/probes-final.log` records repeated failures: the captured snapshot names `PLcaptured` but insertion goes to `PLchanged`; a backtest match causes a later real insertion into `PLforbidden`.

Source reads in this session confirm the structural paths:

- `pipeline.executeVideoActions` and `videosWithMatchedDecisions` use `GetRouteDecisionsForVideo`, ignoring their run ID. `processDeferredItem` invokes action execution without reevaluation.
- Production exclusion uses `router.Routes`; routing uses `router.Evaluate`; `Executor.loadActions` reads mutable `route_action`. Only the detector currently selects the linked snapshot's threshold.
- `dryRunRoutes`, `routesFromSnapshot`, `EvaluateWithRoutes`, and `GetRouteDecisionsForVideoInRun` already provide the necessary explicit-route and scoped-read seams. Simulations write decisions/actions under their own run.
- `listRunVideos` and the existing run-video lookup restrict to `discovered_in_run_id`. A retry video was discovered in an earlier run; discovery identity is not evaluation membership.
- `deferred_item` retains an originating run and status, but no receiving-run owner. A crash after `retrying` and before the first new decision cannot be attributed to a receiving run from decisions alone.
- The executor's completion check is same-run only. Its successful external insertion occurs before the local action insert. No provider-membership reconciliation is present in the inspected executor; the existing YouTube playlist-list method is an uploads-oriented metadata parser, not an established mutation-reconciliation contract.

Follow-up evidence from Critic session `ses_e8cdba982ffexUtFVqtPEvdLxu` was read in full through its stored findings (concerns `24a37d8a-8625-4519-9e99-f512eafbbe4f`, `b2ca48dd-54c2-407e-be35-14e8494d1284`, final QA `ffc4b33b-090d-475f-91d8-b4de39c79d6d`) and `/tmp/opencode/critic-authority-probe_test.go` / `critic-authority-probes.log`. Its real-phase fixture captured `classical`, edited live conditions to `jazz`, persisted a same-run `matched` decision with a `jazz` audit, then recovered a `PLjazz` insertion. All previously specified run/video/route/matched/enabled checks passed; independent snapshot evaluation returned no matches. This is accepted evidence of a material legacy-admission gap, not a stronger hypothetical threat model. The schema/probe also permits two pending rows for one video.

Source inspection confirms why a normal evaluator call is not validation: `internal/router/engine.go:79–85` uses the result of `RecordRouteEvaluation`, and `internal/state/machine.go:275–279` returns an existing decision without comparing it to the proposed evaluation/audits. The schema's unique `(video_id,run_id,route_id)` prevents inserting a replacement without moving or deleting history. `condition_audit` records condition inputs/results, but no complete exclusion input/configuration provenance. `deferred_item` has no video uniqueness constraint (`001_initial_schema.up.sql:131–168,208–217`); `Executor.deferVideo` inserts another row. These facts justify independent, non-recording admission and video-group queue operations, not history repair or a new uniqueness constraint.

These are current-source findings, not new runtime reproductions. Semantic navigation returned degraded gopls (`Option is not a function`); direct source reads were used.

## Constraints and forces

- Preserve continuation, quota deferral, immutable historical snapshots/actions and stable route identity. Never fix contamination by disabling simulation matches or dropping deferred work.
- Do not load a live route as a fallback when a captured route or snapshot is missing. Missing authority is a detectable integrity failure, not permission to guess a target.
- Production is `manual` or `scheduled`; replay, backtest and dry-run are never production authorization sources. A proposal-only identity anchor is an FK target, not an executable route definition.
- Keep the existing SQLite/single-process deployment. Scheduler recovery and manual/scheduled calls can overlap even with one user; serialize production execution through one pipeline-owned execution gate. Capture remains at run initiation, not when a queued execution eventually obtains that gate. This does not serialize read-only simulations or configuration edits.
- The earlier ADR-012 correction did not make simultaneous saved edits and capture atomic. This correction does not claim to resolve that separate capture-consistency issue; once captured, the stored snapshot is the sole execution authority.

## Options considered

| Option | Benefits | Cost and failure modes | Decision |
|---|---|---|---|
| Scope matches to the run but retain live route/action loading | Smallest textual patch; blocks the demonstrated cross-run match leak | Still inserts into an edited target, and simply breaks deferred work because the next run has no matches yet; violates BR-003 AC11 and preservation | Reject: incomplete |
| Run-owned snapshot evaluation and execution, existing evidence, one durable retry-owner field | Reuses current router/config/state seams; restart can identify pre-decision deferred work; no duplicate action-intent representation | Requires reevaluation/state-reset support and queue ownership integration; completion lookup must not use hypothetical rows | Choose: least complete realization |
| Persist a separate action-intent/outbox and general run-video lifecycle ledger | Can represent every selected workload and action attempt independently | New schema families, recovery protocol, reporting joins, and dual configuration/action authorities; external insertion still is not atomic with SQLite | Viable larger redesign, but not needed for the demonstrated authority defects; revisit for a separately scoped general recovery/accounting design |
| Replay old production matches and targets in a new run | Could resume old action intent without reevaluation | Contradicts accepted current-at-next-run deferred semantics; misattributes old rules to the new run's snapshot | Reject |

For legacy admission, identity checks alone lose to the demonstrated `classical`/`jazz` case. Rewriting/deleting old decisions to rerun in place loses immutable evidence and action links. A new evaluation-generation schema could preserve both versions, but expands uniqueness, action, history and statistics contracts unnecessarily. Blanket rejection of all legacy runs is safe but blocks even snapshot-compatible unfinished work. **Choose an immutable run producer marker plus read-only, full snapshot reevaluation for legacy runs:** compatible evidence resumes without a new history model; contradictory or unverifiable evidence is quarantined through existing error handling. The marker never retroactively certifies what the old engine did.

## Decision and structure

The pipeline resolves the persisted run and linked ConfigVersion once per execution/resume into a run-local configuration value. Reuse the snapshot conversion and explicit-route evaluator, rather than changing the shared router's mutable configuration or caching a process-global active snapshot. All production phases and deferred evaluation receive that value. The executor independently validates the persisted run/decision linkage and resolves targets from the same linked ConfigVersion; caller-supplied route IDs are references to validate, not capabilities.

```text
saved capture -> Run R -> immutable ConfigVersion S
                         | exclusions / detection / conditions
deferred claim(R) ------> | -> decisions(video, R, route in S)
                                      |
                              production executor gate
                                      |
                          validate R + decisions + S
                                      |
                  exact-target production completion check
                                      |
                          actions from S -> YouTube

simulation Run H -> its snapshot -> decisions(video, H, ...)
                                     X no path authorizing R
```

### Invariants and enforcement

| Guarantee and precondition | Responsible boundary / mechanism | Falsification |
|---|---|---|
| Only a production run's own snapshot-admissible matched decisions authorize its actions | Pipeline uses `GetRouteDecisionsForVideoInRun` in both selectors; executor validates persisted references and legacy admission before quota/auth/provider work | Foreign/simulation match or legacy live-condition mismatch; no calls or action rows |
| Decisions and action target describe one captured configuration | Run loader supplies snapshot routes to exclusion and `EvaluateWithRoutes`; executor resolves snapshot actions, never `route_action` | Edit/delete/disable/reorder/change final/conditions/target after start and before restart; the original run remains on its captured rules |
| Restart is not a new evaluation policy | Same run ID and ConfigVersion; native-version evidence retains its prefix; legacy evidence must pass independent snapshot reevaluation before reuse/completing missing work | Legacy matched/nonmatched/final/exclusion drift and native partial-route restart after edits |
| A deferred retry is new-run evaluation, not old decision replay | Receiving-run queue claim precedes metadata/evaluation; reset only current work state and evaluate with receiving snapshot; record new decisions | Old match/new nonmatch or new exclusion produces no old target mutation |
| A retry cannot disappear before its first decision | `deferred_item.retry_run_id` persists receiving owner with `retrying` in one transaction; recovery includes its owned items regardless of run phase/discovery origin | Crash immediately after claim and before any decision; reopen and resume same run |
| Completion evidence can suppress, never grant, an action | Exact video/action-type/target success joined to production run; simulation `not_attempted` or matched rows never count as completion | Simulated success-shaped contamination and changed target fixtures |

### Deferred ownership and lifecycle

Add only nullable `deferred_item.retry_run_id`, referencing `run.id`, leaving `run_id` as immutable origin. Existing rows start null; do not infer historical retry owners. Exact field and transaction rules are in ARS-005. No new queue statuses are needed.

At a fresh production execution, pending work is considered FIFO before new discoveries. The claim transaction verifies a production receiving run, no other live owner for that video's outstanding deferred work, and records `retrying` plus the receiving run ID before resetting the deferred video's current work state for evaluation. Preserve its video ID, discovery run/time/channel, old decisions/actions and completed identical actions. Merely adding the `deferred -> evaluating` transition without clearing the old primary deferred result is insufficient.

The ownership unit is a **video's outstanding queue group**, not one physical row. Claim all its pending/reclaimable retrying siblings together; use their oldest `(created_at,id)` for FIFO placement. Keep the rows and their origins, perform one evaluation/reset/finalization per video, and acknowledge or requeue the group together. The executor reuses the owned group on another deferral instead of inserting another active sibling. Stale FIFO entries for siblings cannot trigger another attempt in the same run. Exact transaction and contender rules are in ARS-005; no uniqueness cleanup migration is authorized.

On same-run restart, recover owned retrying work before ordinary phase dispatch, even if the checkpoint has reached routing or actions. Run decisions determine evaluation progress **only after legacy admission below**; the old global video's `evaluated`, `acted` or `deferred` value is not proof that this receiving run has finished evaluating. Complete admissible current-run work without duplicate decisions/audits. Execute only current-run matches. Recovery candidates include current-run decisions and owned retries as well as original discoveries; never change `discovered_in_run_id` to make them appear.

If quota is still insufficient, retain pending work for a later run; do not loop immediately in the same run. A terminal retry result (including a real new rejection/exclusion) completes the queue item only after its disposition is durable. Failure retains inspectable work under the existing OC-001/error-queue rules. Queue completion and disposition must not be separated by a crash window that loses unfinished work. Interrupted owners retain their claim; terminal failed owners cannot strand it permanently. A later run may reclaim unfinished work after the prior owner is no longer resumable, but never execute that owner's decisions as its own.

This is not a promise to preserve obsolete pending targets when the next run's configuration no longer selects them; that policy was already settled by the system architecture's Deferred Work Recovery section. Successfully completed old mutations are not reversed.

### Legacy evaluation admission

Add `run.action_authority_version` with default `0` for unknown/legacy production, and explicit `1` only on production runs newly created by the completely corrected snapshot-owned producer. It is internal producer provenance, not a ConfigVersion version or a timestamp cutoff. Never backfill, promote a resumed run, or infer it from audits, snapshot contents, IDs, descriptions or deployment dates. Legacy runs stay `0`, including decisions newly appended while resuming them. Simulations cannot acquire production authority through this marker.

For version `0`, pipeline recovery and the real executor independently calculate the complete expected decision/audit vector from the linked snapshot and available metadata, without invoking recording/recovery helpers or consulting old decisions to choose the result. Compare **every** existing decision/audit, including nonmatches, exclusions, disabled and final skips, not just supplied matches. Every existing row must match that expected vector exactly; missing rows may be recorded only after validation. Before actions, the entire vector must be durably complete and the executor repeats this independent check. No persisted admission bit or caller attestation bypasses it. Version `1` retains normal prefix reuse, not metadata reevaluation of already recorded native evidence.

Mismatch or inability to establish the required inputs is not a fresh no-match disposition. Fail closed before playlist work, preserve snapshot/decisions/audits/actions and unfinished candidates, and report an existing runtime integrity failure with durable error-queue retention. Quota/temporary missing-input handling retains OC-001/002 meaning. Before a run abort becomes terminal, retain every unfinished candidate, not just the first conflicting video; otherwise an apparently safe fence would strand later work. A contradictory legacy tuple cannot be overwritten because its unique key is occupied: the failed item's normal manual retry must enter a **fresh production receiving run** and record new evidence, never repair the legacy tuple or silently switch the interrupted run to today's snapshot. Retaining failed work without a functional fresh-evaluation retry path is not conformance with BR-014/030. ARS-005 defines that boundary without new endpoint/status semantics. Completed historical runs are inspection-only and are not reopened or reclassified.

This admission is deliberately conservative: metadata drift or expiration can make old evidence unverifiable, and an audit alone cannot prove exclusions were applied. Such work remains retained rather than guessed. An admitted legacy decision is independently snapshot-compatible now; admission does not assert its original producer used that snapshot. Old completed production successes may still veto duplicate mutations; they never grant new mutations.

### Membership, inspection and accounting

Run-local evidence, not discovery ownership, identifies a video evaluated by a replay/backtest or receiving production run. Reuse decisions for inspection membership and recovery, supplemented by owned queue work for pre-decision recovery. The real list/detail queries must bind to the requested run, filter by that run's evidence before pagination, and load only its decisions/actions. Another run's decisions never create membership or authorization in this run.

Do not reuse the production video's current primary outcome as a historical/simulation outcome. Existing decision-derived successful simulation projections remain ARS-005's bounded contract. Original production run summaries must not be recomputed from a video's later retry state merely because it was originally discovered there. A general per-run outcome ledger, zero-route/untouched-work accounting and mixed operational-result precedence remain separate requirements/implementation reconciliation; this correction does not certify them.

## Failure and recovery

- Snapshot capture failure prevents execution without a snapshot, as OC-003 requires. Missing/corrupt linked snapshot or unmatched route identity at execution prevents provider calls and is reported through existing operational failure handling. Never repair an old run by linking today's configuration. Inspection's documented incomplete-context fallback is not an execution fallback.
- Preserve exact-target completed-action checks across runs and restarts. Two routes selecting the same mutation still produce at most one external insertion; a changed playlist target is a different action, not suppressed just because its route ID is unchanged. A skip is not a newly successful external call and must remain inspectable from existing completion evidence.
- **Existing residual risk, implementation owner:** `Executor.executeAction` performs the external insertion before inserting `executed_action`. A crash in between can repeat the insertion even with correct snapshot authority; SQLite uniqueness is not a cross-provider transaction. This correction does not claim full BR-012/BR-030 crash-window conformance. Establish a provider-side reconciliation contract and test the real crash window before claiming that guarantee. Do not casually reuse the uploads parser or record a hypothetical action as completion. This is a technical verification/design gap, not a new user-policy choice or an excuse to weaken BR-012.
- The single-process production execution gate prevents two in-process check-then-insert paths from racing. It is not a distributed lock or a provider exactly-once guarantee; multiple app writers remain unsupported by the existing deployment model.
- Old incorrect external insertions are not automatically removed, and old evidence is not rewritten. Any compensating playlist mutation requires separate product/operator authority.

## Implementation boundary

The affected boundaries are the pipeline's run-configuration resolution, production run creation and entry guard; non-recording legacy admission; production exclusion/routing and both action selectors; executor validation/action loading/completion lookup; deferred group claim/recovery and state transitions; forward queue-owner and producer-marker migration/store support; error-queue fresh-evaluation retry with durable receiving owner; and run-scoped membership inspection where evidence belongs to an earlier-discovered video. Exact rules are in ARS-005. Scheduler/manual/recovery dispatch must share the same production execution gate.

Do not change proposal activation, immutable history, duplicate sensitivity/reference/provenance policy, action naming, public UUID identity, final-route semantics, or failure categories. Do not suppress hypothetical actions to hide the leak. Repairing partial-exclusion/nonmatch classification is a separate confirmed reviewer finding under existing OC-002, not an authorization mechanism.

## Verification and composition

Required downstream regressions use real SQLite, config capture, detector, router, queue, executor and HTTP readback; fake only external YouTube. These are required evidence, not executed tests:

1. Reproduce `PLcaptured` versus `PLchanged` through real `StartRun`/`Execute`; also change exclusions, conditions, enabled/final/order and delete the route after capture. All phases use the captured configuration. A later fresh run uses the edit.
2. Quota-defer a production video; evaluate a backtest/dry-run/replay proposal matching an otherwise forbidden route for that video; retry in a new production run. Only that new run's actual matches and snapshot actions can execute. Assert no external `PLforbidden` insertion and all action decision/run/video FKs agree.
3. Directly invoke the real executor with a simulation run, an old production decision, wrong video, wrong route, nonmatched decision and route absent from the snapshot. Each rejects before any token/provider use or action write. No valid match plus a malformed extra match may partially execute.
4. Retry old-match/new-nonmatch, newly excluded, disabled/deleted route, changed playlist and unchanged matching route cases. Retain old decisions/actions and origin discovery identity; inspect new decisions via the receiving run's real HTTP list/detail, filters and pagination. No empty-match shortcut may silently complete an unevaluated queue item.
5. Crash after queue claim before first decision, after a persisted route prefix, during action execution and after terminal disposition before queue acknowledgement. Reopen the database and resume the same run after live configuration edits; every claimed video remains discoverable under the original receiving snapshot, no duplicate audits/decisions/counters/acknowledgements, and no quota busy-loop.
6. Complete target A, defer target B, start a new run with the same rules and sufficient quota. A is not inserted twice; B executes once with the new run's decision. Repeat across restart, two matched routes sharing a target, and a changed target. Simulated `not_attempted` actions never suppress real pending work; completion checks do not require insertion quota for an already completed action.
7. Run production entrypoints concurrently under one process, including scheduler recovery and manual execution, and show serialized ownership/execution with no duplicate mutation. Simulations can run between production phases without changing production authority.
8. Retain confirmed distinct imported/native exact/fuzzy duplicate continuation, all/partial exclusions, condition nonmatch, final route and quota/failure regressions. SQL snapshots prove simulations themselves create no production mutation, corpus/provenance, idempotency or operational counters. Do not infer safety solely from zero mutations during simulation; assert the subsequent production run too.
9. Construct legacy state through the pre-fix real phases: snapshot condition `classical`, live edit `jazz`, same-run matched/audit `jazz`, interrupted at actions. Upgrade/reopen and call both production recovery and executor directly. Both refuse any insertion/action row, retain byte-identical historical evidence and the linked snapshot, and expose durable unfinished/error work. Repeat with wrong legacy nonmatch, order/final/disabled/exclusion drift, missing/partial audits, missing metadata and missing snapshot; no valid-match subset may execute. A snapshot-compatible legacy fixture admits, fills only missing decisions, and resumes without duplicate counters/audits/actions; its marker remains `0`. A native `1` fixture reuses its prefix despite metadata/live edits. Migration/default/direct-store/unknown-version coverage must show the marker cannot be inferred or upgraded on resume; verify the rollout excludes old writers rather than claiming an old binary understands the fence. Demonstrate the quarantined item's real manual retry through a fresh production receiving run, including a crash after durable error-owner claim before the first decision, and new evidence, with old completed actions still suppressing duplicates.
10. Seed two pending sibling rows and a pending plus legacy-unowned-retrying pair for one video, including both siblings in one FIFO batch. Claim/reset/evaluate/finalize once, requeue or acknowledge all siblings atomically, preserve each origin, and do not add another sibling on repeated quota deferral. Crash after group claim and before group acknowledgement, reopen and resume; also contend with another receiving run. One group has one live owner and a losing/stale sibling never reexecutes or acknowledges work.

Independent critique and assembled verification are required before relying on this correction. Full provider-side crash-window idempotency needs its separate positive and failure proof; it is not covered by regression 6's locally recorded-success fixture.

## Consequences and open questions

- **No user decision blocks these corrections.** Deferred current-at-next-run policy is already explicit; BR-003 AC11 and OC-003 forbid live edits changing an in-progress run. Simulation cannot authorize real actions under BR-008 AC9.
- **Implementation/review owner, blocking full recovery acceptance:** durable retry ownership/state integration now has local pre-decision and terminal-before-ack reopen tests; obtain independent review and complete the remaining crash-prefix/failure-injection proofs. An in-memory list or discovery-only query is insufficient.
- **Architecture/implementation owner, blocking full idempotency conformance, not the authority correction:** provider-success/local-record crash reconciliation remains unproven. It is reported rather than expanded into an ungrounded external exactly-once claim.
- **Requirements/reporting owners, outside this correction:** broader historical per-run operational outcomes and incomplete-context presentation remain as recorded in ARS-005. No new outcome precedence or general selected-workload count is invented.
- The nullable queue-owner extensions recover retries before any decision exists without repurposing their immutable originating `run_id`; the producer marker discharges the demonstrated legacy trust gap. They do not encode a new outcome ledger. Revisit evaluation generations only if accepted recovery requirements demand replacing contradictory evidence inside the same run rather than retained-failure retry.
- **Deployment/implementation owners:** stop the old writer before applying the forward extension and enabling the corrected producer. Downgrade writes to this database are unsupported: an old binary could append live-route decisions to a version-`1` run. Read-only historical inspection remains available; neither a new marker nor this architecture makes mixed-version writers safe.
