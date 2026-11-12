---
type: obligation-contract
title: OC-001 — Controlled Orphaned-Work Recovery Handoff
description: Provisional semantic agreement for preserving run 14 leftovers, explicitly activating bounded production recovery, and reporting partial and uncertain outcomes truthfully.
tags: [obligation-contract, recovery, authority, quota, idempotency, declined]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** declined, 2026-11-10. The user decided to leave run 14 as it is: no recovery and no backfill; its saved unevaluated videos stay unprocessed. This proposal is not authorized, not planned and not normative; it is kept as history only. The open user choices below are closed by that decision. Original status: provisional proposal, not accepted new product scope or implementation acceptance. The SHALL clauses below describe the candidate flow if authorized. Existing accepted obligations cited below apply independently. No authorization to mutate a live database, change run status, restart, or issue provider requests is conveyed.
**Priority:** MUST for any authorized realization of this candidate flow.
**Identity:** OC-001 is local to the run14-recovery bundle, not a replacement for YuHaul's accepted quota OC-001.

## Participants

- **Authorizing user/operator:** chooses the recovery scope and expenditure, and separately initiates approved live operations.
- **Assessment reader:** supplies read-only, timestamped evidence and its limitations.
- **Work custodian:** preserves original evidence and transfers responsibility for unfinished work without losing or duplicating it.
- **Production worker:** evaluates and acts only under the authorized production run and configuration, within admitted expenditure.
- **Outcome reader:** reports original-run history and receiving-run results without conflating them.

These are responsibilities, not selected processes, endpoints, tables or interfaces.

## Preconditions and applicability

The incident report says run 14 is production, saved as `running` in discovery with no completion timestamp; 19,615 discovered videos remain `not_yet_evaluated`; no executed-action rows were reported; logs show a discovery deadline and failed failure-status write. These statements establish an investigation target, not current eligibility, provider nonexecution, or complete discovery.

The proposed flow applies only after fresh evidence establishes the intended database/environment, abandoned ownership and exact recoverable membership. Existing eligible error/deferred groups may use existing retry semantics; arbitrary unqueued discoveries require a newly authorized, tested handoff. Uncertainty blocks the affected mutation, not read-only investigation or preservation of existing records.

## Guarantees

### 1. Observation and activation authority

1. Assessment SHALL neither change durable work/run state nor issue provider requests. It SHALL distinguish reported incident facts, current observations, inferences and unknowns. An assessment result is not a recovery start.
2. Admitting or retaining work SHALL NOT itself execute playlist actions. Retention repair and production activation SHALL be distinguishable authorized operations. Before activation, the user SHALL know the intended cohort, configuration basis, batch limit, request budget, discovery policy and unresolved exclusions. Missing choices SHALL NOT default to processing all 19,615 videos. General scheduling approval is not incident activation.
3. This flow SHALL NOT add automatic startup resumption or silently submit recovery after observation failure. Existing startup behavior is not changed or assumed inert; exclusive-operation readiness must cover it. Restart is not a read-only precondition check.

### 2. Membership, ownership and evidence

4. Before changing an abandoned run or claiming a candidate, the custodian SHALL establish that no live production owner can concurrently execute that work. Conflicting or changed ownership/progress SHALL reject the affected handoff without overwriting the newer work. A stale saved `running` row, an elapsed deadline or an inactive observation interval alone is insufficient proof of abandonment.
5. The custodian SHALL preserve each video's discovery run, discovery time and source identity, original snapshots, decisions, audits, historical outcomes and real completion evidence. Receiving-run membership SHALL NOT be manufactured by moving discovery ownership. Simulation evidence SHALL neither authorize production nor count as production completion.
6. Each logical video with unfinished work SHALL have at most one active production recovery owner. Duplicate candidates, repeated admission and sibling queue entries SHALL NOT multiply evaluation or external effects. A repeated successful handoff SHALL identify already retained/owned work rather than append another executable copy. Unselected or conflicting work remains inspectable and retained, not silently consumed.
7. Before making the abandoned run terminal, the custodian SHALL durably retain every unfinished candidate requiring recovery, not just the approved processing slice. A crash between handoff and acknowledgement SHALL leave either the prior recoverable responsibility or the new recoverable responsibility, never neither. This does not require one specific transaction or persistence design.

### 3. Configuration and action authority

8. A legitimate same-run continuation SHALL use run 14's original captured configuration and persisted progress. A fresh receiving production run SHALL capture its own initiation configuration, evaluate its own decisions, and keep results distinct from run 14. Neither path may substitute today's configuration into run 14's snapshot. Missing/corrupt authority or unverifiable legacy decisions SHALL prevent actions and retain the affected work with a safe, inspectable reason.
9. A production worker SHALL suppress an already completed identical video/action/target mutation using durable production completion evidence, including completion across routes/runs. It SHALL NOT repeat a confirmed insertion to obtain a new local success record. It SHALL retain completed partial work while recovering the remaining targets.
10. Missing action rows SHALL NOT establish that no provider effect occurred. Where an attempted insertion may have succeeded without durable local completion, another insertion SHALL wait until evidence resolves whether it occurred. If that cannot be established, the affected action remains explicitly unresolved and is not reissued or reported complete. User approval alone cannot establish duplicate-free execution. Architect owns the reconciliation realization; no new provider exactly-once capability is assumed.

### 4. Bounded expenditure and stopping

11. For the proposed controlled slice, the worker SHALL admit no more distinct videos than the authorized batch limit and no request whose conservatively bounded quota cost would exceed its remaining authorized request budget or known provider availability. Count every actual request attempt, including retries, failed/ambiguous requests, reconciliation reads and metadata acquisition, not only successful calls or playlist additions. Multiple targets and shared targets mean videos and actions are not interchangeable budget units.
12. The worker SHALL include relevant concurrent expenditure and uncertain prior consumption when establishing usable provider availability. If availability or a required request's cost cannot be bounded, it SHALL defer that API-dependent work rather than guess. It SHALL NOT raise limits, reset accounting, use a new quota window as a fresh authorization, or automatically enlarge the cohort/budget. A strict total bound is scoped to the authorized slice, not a promise to control unrelated provider clients; their spend affects available capacity.
13. On batch/budget exhaustion, quota deferral, authorization lapse or failure, all remaining eligible work SHALL stay durably recoverable with an accurate reason. No immediate quota retry loop is permitted. Existing quota-deferred work remains eligible under accepted next-run policy, but this incident-specific bounded authorization does not silently authorize spending beyond its slice. Continuation beyond the slice needs the selected ongoing policy or another explicit activation.
14. A requested stop SHALL prevent further request admission after it is observed; already issued requests may still have effects and SHALL be accounted for and reconciled. A stop or timeout SHALL NOT erase completed work or imply rollback. A budget cap is checked before issue, not repaired afterward by reclassifying excess calls. This defines stop meaning if the authorized flow offers a stop; it does not independently require a new cancellation UI.

### 5. Truthful status and recovery

15. Confirmed abandoned discovery-phase failure SHALL be recorded as terminal `failed` under accepted failure policy only after recoverable responsibility is preserved. A genuine resumable interruption remains `interrupted`; lack of evidence SHALL not be resolved by fabricating either classification. Later receiving-run success SHALL NOT mark run 14 `completed`, advance its historical discovery checkpoint, or assert that discovery was complete. Do not backdate a recovery write as an observed original completion time.
16. Untouched videos SHALL remain not-yet-evaluated unless an actual operational deferral/failure is recorded; they SHALL NOT become rejected or failed route evaluations merely because run 14 failed. Evaluation, external success, already-applied, deferred, failure and unresolved external effect SHALL remain distinguishable. Outcome readers SHALL identify the responsible run and configuration, processed subset and remaining work; a successful retention repair is not successful playlist processing.
17. A failed durable retention/finalization write SHALL be reported as unconfirmed, not acknowledged as successful handoff or terminal status. Subsequent recovery SHALL reconcile saved ownership and effects before continuing. Lost contact alone SHALL not trigger a second start, establish completion, or prove that no actions occurred.

## Minimal read-only precondition check

Return these bounded summaries with environment identity and observation time, using a consistent database view where available; separately label process/provider observations taken at other times. No full cohort export, simulation, provider playlist enumeration, credential export or live mutation is needed for this check.

1. Run 14 type/status/phase/times, linked snapshot availability and authority provenance; deployed revision/migration level and active production workers/other unfinished owners. Report whether abandonment is established or still unknown.
2. All run-14 discoveries by processing state/outcome and distinct video count; run-local decisions/actions by mode/outcome; attempts/completions across other runs for the pending cohort; outstanding queue groups by distinct video, origin, owner and owner status. Zero local action rows do not exclude unrecorded external success. Report discrepancies from 19,615 rather than forcing that count.
3. Current-window quota ledger totals across all callers, configured allocation/reset and operator-supplied provider/shared-client consumption where available. State omitted/ambiguous requests and whether usable capacity is actually bounded. Do not infer available budget from run-summary counters alone.
4. Required snapshot/configuration and retained-input readiness, including missing/expired metadata and known default/unenriched values. Unknown duration and recorded zero must not be conflated. Show aggregate incomplete counts; a sample cannot establish readiness of the whole cohort.

Output is a branch classification: existing eligible retry groups; abandoned unqueued work requiring new handoff; genuine interrupted continuation; active/conflicting owner; or insufficient evidence. It SHALL state which choices/evidence block each proposed mutation. This is not executable approval or a newly selected API shape.

## Semantic inputs and outputs

- **Inputs:** original-run identity and evidence; exact candidate identity/progress/ownership; authorized continuation mode, configuration basis, batch and budget; request costs/availability; provider-effect evidence; durable handoff/disposition results.
- **Outputs:** read-only readiness and gaps; attributable retained/claimed membership; unchanged historical origin; receiving-run evaluation/effect evidence; conservative spend and remaining authorization; truthful partial, terminal or unconfirmed status.

## Failure semantics and invariants

Stale ownership or authority denies the affected mutation. Quota/auth conditions defer API-dependent work under accepted semantics. Transient failure retains retryable work; permanent failure remains inspectable. Ambiguous external success blocks reissue, not the recording of uncertainty. No failed write is success. One live owner, immutable origin/snapshot evidence, preserved unfinished work, and no duplicate confirmed mutation hold across interruption, restart and repeated handoff.

## Derived from

- [BR-011](../yuhaul/br-011-processing-state-persistence.md), [BR-014](../yuhaul/br-014-failed-work-retention-and-retry.md), [BR-030](../yuhaul/br-030-temporary-failure-work-preservation.md): preservation and recoverable partial work; clauses 4–7 and 13/17 prevent lost or doubly owned work.
- [BR-012](../yuhaul/br-012-idempotent-playlist-actions.md): clauses 9–10 are required because absent local success cannot safely justify repeating an insertion.
- [BR-013](../yuhaul/br-013-quota-limited-work-deferral.md), [BR-018](../yuhaul/br-018-api-consumption-accounting-and-visibility.md), [BR-019](../yuhaul/br-019-quota-aware-processing.md), [quota OC-001](../yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md): accounting and deferral remain accepted. A new strict per-slice authorization cap in clauses 11–12 is **proposed incident scope**, not an already implemented guarantee derived from these BRs.
- [BR-017](../yuhaul/br-017-manual-run-initiation.md), [BR-031](../yuhaul/br-031-user-authority-over-policy-decisions.md), [BR-032](../yuhaul/br-032-processing-outcome-distinction.md), [BR-033](../yuhaul/br-033-honest-manual-run-progress.md), [OC-003](../yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md): nonconcurrent production, policy authority, honest outcomes/progress and immutable snapshots.
- Delegated incident specification assignment, 2026-11-07, Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`, Plan revision 12 `run14-assessment`: explicitly requests provisional controlled recovery meaning, read-only checks, no live approval, and user-owned scope/configuration/budget/discovery decisions. Specification choices: separate retention acknowledgement from processing; fail closed on changed evidence; count distinct videos; stop means no further admission, not rollback. These do not choose implementation structure.

## Reconciliation

Baseline was extracted from the assignment and accepted BRs before reading downstream architecture/source. The assignment had already relayed the architect's retention recommendation; no particular queue/API realization is made normative here.

- **Aligned:** [run-owned authority](../../architecture/duplicate-continuation/design/ard-001-run-owned-production-action-authority.md) fixes original-snapshot same-run continuation versus fresh-snapshot receiving-run evaluation. The user choice is which legitimate recovery path to authorize, not permission to mix these semantics. Choosing original continuation for a confirmed failed run conflicts with [ADR-002](../../architecture/adr-002-run-status-failed-terminal-state.md); changing that policy requires an explicit amendment, not relabeling `failed` as `interrupted`.
- **Aligned, proposed realization only:** [run14 ADR](../../architecture/run14-recovery/decisions/adr-001-run14-evidence-gated-recovery.md) identifies absent arbitrary-pending admission, distinct retention/explicit retry, preservation, ownership and quota gaps. Existing eligible retry is not approval for bulk retention or dispatch. No direct SQL rewrite or new startup default is authorized here.
- **Missing implementation proof:** provider-success/local-record uncertainty and success-only quota accounting cannot establish clauses 10–12. These are architect/engineering/reviewer obligations, not user risk waivers. The strict slice cap requires authorized new scope; its numerical values remain open.
- **Bounded source check:** `internal/pipeline/retry.go:19–73` creates/claims an existing queue group and a fresh production run, with no discovery workload; it does not accept arbitrary pending video membership. This was read, not executed. Semantic navigation was unavailable due to degraded gopls (`Option is not a function`); direct source read was used. Other implementation findings above rely on the attributed architect report, not independent full-code verification. No live DB/log/provider access or tests occurred.

## Verified by

Required proof if this proposal is authorized; none is claimed executed here. Exercise the real assessment → handoff → production → durable outcome/readback path using a synthetic or consistently copied database and controlled provider, without live credentials.

1. Reproduce partially persisted discovery plus deadline/failure-write loss. Read-only assessment makes no writes/calls. Retain the full unfinished cohort before terminal failure while selecting only an approved small processing slice. Original discovery/snapshot/history stays unchanged; run 14 never becomes completed from receiving-run success.
2. Repeat admission and race a changed owner/progress; one logical video is claimed once and conflicts fail without overwriting. Interrupt before/after handoff and disposition acknowledgement, reopen, and establish no loss or duplicated ownership/evaluation.
3. Edit live rules after original capture. Same-run interruption uses original rules; fresh receiving run uses its own snapshot. Missing authority and conflicting legacy evidence deny actions. Simulations cannot authorize or suppress production actions.
4. Complete target A and interrupt target B. Retry preserves A and executes only eligible remaining targets. Inject provider success followed by local-write failure; another insertion must not issue until the effect is reconciled. If reconciliation is unavailable, readback shows unresolved work rather than success or blind replay.
5. Set a small explicit batch and request budget. Exercise multiple targets, shared targets, metadata/reconciliation reads, failed requests, retry, uncertain consumption and concurrent expenditure. Attempt totals stay within admitted bounds, all attempts are accounted for, no discovery request occurs under backlog-only authorization, and exhaustion defers remaining work without same-window looping or automatic budget reset.
6. Stop during an in-flight request; no further request is admitted after stop observation, the in-flight effect remains accounted/reconciled, and remaining work survives reopen. Fail retention/finalization writes and observation reads; no false acknowledgement/status or duplicate start follows.

## Open user choices and next owners

All choices below block dependent implementation/activation, not read-only assessment. Parent session must obtain the product owner's answers; an unanswered question is not acceptance.

1. **Scope and activation:** authorize only a tested retention repair plus existing explicit retries, or a new controlled batch flow as well? Live retention writes and live action activation each need separate explicit authorization. Do not infer either from this artifact or general schedule approval.
2. **Configuration basis/path:** prefer processing under current captured configuration in fresh receiving runs, or investigate legitimate original-snapshot continuation? Established phase failure requires terminal failure and fresh retry under current policy. An original-snapshot option after such failure needs a separately specified policy decision; never rewrite run 14's captured configuration.
3. **Size and expenditure:** maximum distinct videos and total request quota units for the first slice; usable provider window/reserve and whether later slices need new manual activation. A video cap alone cannot bound spend. Architect must establish enforceability before execution.
4. **Discovery order:** process saved backlog without new discovery first, or also authorize further discovery? Additional discovery needs its own bounded allocation/ordering and evidence of incomplete prior discovery. No silent full-pipeline Run Now substitute.

Architect next: reconcile these answers with accepted lifecycle/snapshot policy, define a realization for ownership, strict request admission and ambiguous external effects, and expose any infeasible promise. Operator next: supply current read-only evidence. Reviewer next, only after implementation: prove the failure/reopen composition above. No automatic startup resume, date-filter decision or two-job coordinator is added.
