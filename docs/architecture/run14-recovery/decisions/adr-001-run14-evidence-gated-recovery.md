---
type: decision
title: ADR-001 — Evidence-Gated Recovery of Run 14's Pending Videos
description: Read-only incident assessment distinguishing supported interruption and queue retries from a new tested repair for discovery-timeout leftovers, preserving quota, authority and truthful history.
tags: [architecture, decision, incident, recovery, quota, run-history]
timestamp: 2026-11-06T00:00:00Z
---

**Status:** proposed incident recommendation — source inspection only. No live endpoint, credential, production database, deployment operation, mutation or test execution was used. This document does not authorize repair or change accepted recovery policy.

## Recommendation

Do not restart, press Run Now, change run 14's phase/status, or run a large simulation as the first recovery action. Obtain a small, consistent read-only evidence set first. The reported 19,615 durable pending videos are recoverable data, but are not proof of an existing executable recovery job, 19,615 playlist additions, a complete discovery, or run 14's present status.

There is **no registered public API to resume an arbitrary run ID or directly process its unqueued pending discoveries without rediscovery**. There is a narrower supported mechanism: manual retry of an existing eligible error/deferred queue entry creates a new production receiving run, evaluates its video group and performs no discovery. It cannot accept arbitrary video IDs or a run ID in place of a queue entry.

For example, if the evidence finds an eligible retained error entry for video V, its normal Retry control can process V without crawling subscriptions, using the new run's snapshot. If V is only a pending discovery in run 14 and has no queue entry, that control cannot process it. Creating the retention handoff would be new repair work, not a supported operator command.

Subject to evidence confirming an abandoned discovery-phase failure, the smallest candidate realization is a **tested, operator-approved retention repair using existing error groups, followed by existing explicit queue retry**. Prefer this over a new general coordinator for the incident alone. It is not ready to execute. A bulk/bounded processing-only facility is separate code and behavioral scope if individual explicit retries are not a useful recovery experience.

## Derived from

- Incident assignment: reported live start at 22:21:22, discovery `begin transaction: context deadline exceeded` at 22:51:22, subsequent failure-status write reporting `loading run 14: context deadline exceeded`, and a supplied database aggregate of 19,615 videos with `discovered_in_run_id=14`, `processing_state='not_yet_evaluated'`, `final_outcome='not_yet_evaluated'`. The date/timezone, deployed revision and current live state were not independently verified.
- [ADR-002 — Failed terminal status](../../adr-002-run-status-failed-terminal-state.md), especially phase error versus resumable interruption.
- [Run-owned production authority](../../duplicate-continuation/design/ard-001-run-owned-production-action-authority.md) and [ARS-006 — Run-attributed outcomes](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md).
- [Proposed discovery/processing coordinator](../../discovery-processing-coordinator/design/ard-001-discovery-processing-budget-coordinator.md). Its independent processing controls are not implemented or approved by this assessment.

## Satisfies

This is a recovery decision boundary, not implemented conformance.

| Accepted obligation | Disposition in this assessment |
|---|---|
| [BR-011](../../../requirements/yuhaul/br-011-processing-state-persistence.md), [BR-030](../../../requirements/yuhaul/br-030-temporary-failure-work-preservation.md) | Preserve durable work and progress; identify the missing runnable handoff. Actual repair/recovery proof remains implementation/reviewer-owned. |
| [BR-012](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md) | Preserve recorded production completion vetoes; do not claim provider/local-record crash ambiguity solved. That existing technical gap blocks an unconditional duplicate-free replay recommendation where attempts are uncertain. |
| [BR-013](../../../requirements/yuhaul/br-013-quota-limited-work-deferral.md), [BR-018](../../../requirements/yuhaul/br-018-api-consumption-accounting-and-visibility.md), [BR-019](../../../requirements/yuhaul/br-019-quota-aware-processing.md), [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) | Require current budget/attempt evidence and bounded request expenditure; existing estimates do not establish strict quota safety. Accounting/admission corrections remain implementation-owned. |
| [BR-014](../../../requirements/yuhaul/br-014-failed-work-retention-and-retry.md) | Reuse eligible retained-error retry when present; do not fabricate eligibility or silently discard pending work. New retention repair must be verified. |
| [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md) | Preserve existing full-pipeline Run Now and nonconcurrent production execution. Do not relabel it processing-only. |
| [BR-026](../../../requirements/yuhaul/br-026-dry-run-evaluation.md) | Existing simulation remains hypothetical, cannot consume pending work or authorize production actions. No new preview guarantee. |
| [BR-031](../../../requirements/yuhaul/br-031-user-authority-over-policy-decisions.md), [BR-033](../../../requirements/yuhaul/br-033-honest-manual-run-progress.md), [OC-003](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md) | No automatic restart, policy change, new start after uncertain observation, snapshot substitution, or invented completion. User/operator authorizes recovery execution; requirements owner resolves only genuinely new policy. |
| ARS-006 | Preserve discovery ownership and requested-run results. Pending global state alone cannot reconstruct missing run-local historical evidence. |

## Decision question

Can the reported pending discoveries be safely processed now through an existing mechanism, without rediscovery, quota overrun, duplicate mutation or false completion of run 14?

**Answer:** not established for the whole set. Existing eligible queue retry is a conditional narrow path; arbitrary unqueued pending admission is absent. The incident needs current evidence before choosing any mutation. An existing source method is not an operator API, and a locally fixed binary is not evidence of its deployment or historical repair.

## Evidence and important corrections

All source paths below were read in the current working tree. They do not establish the deployed binary. Semantic lookup and one repair attempt returned degraded gopls (`Option is not a function`); direct bounded source reads supplied grounding.

| Source | Finding |
|---|---|
| `internal/server/server.go:408–416,492–559` | Registered starts are new production run, dry-run, replay and backtest. No per-run resume/cancel or arbitrary-pending processing endpoint is registered. A manual full run gets a 30-minute background execution context. |
| `internal/pipeline/pipeline.go:294–400,862–883` | Resume skips discovery only for an already `interrupted` run whose saved phase is later than discovery. The run-video selector uses its discoveries, its decisions and its owned retrying groups, not every pending video in the database. Calling Execute on a `running` row starts at discovery regardless of its recorded phase. |
| `internal/discovery/engine.go:238–299,333–377` | Fresh discovery skips existing video IDs, but still traverses upload pages. Persistence commits individual videos; a timeout in a later transaction can leave an already committed prefix. A watermark may advance before later buffered videos commit. The supplied count therefore does not prove discovery completeness. |
| `internal/scheduler/scheduler.go:125–164`; `internal/state/store.go:129–139`; `cmd/server/main.go:202–249` | Startup dispatch selects only `interrupted`, not `running` or `failed`. Disabling the regular schedule is not a startup-resume fence: resume occurs before schedule ticks. Inspected shutdown does not establish conversion of abandoned manual `running` rows into `interrupted`. Restart is neither a repair nor necessarily a passive operation. |
| `internal/pipeline/retry.go:19–73`; `internal/queue/handlers.go:15–33`; `internal/queue/retry.go:35–110` | `POST /api/errors/{id}/retry` or `POST /api/deferred/{id}/retry` operates on an eligible existing entry. Claim, receiving run creation and durable membership share one writer transaction. A manual retry checkpoints the new run at actions, processes its owned group first and does not discover. New snapshot and new decisions govern; original discovery ownership stays intact. |
| `internal/pipeline/authority.go:18–53,69–185`; migration `014_action_authority_retry_ownership.up.sql` | Production requires manual/scheduled, unfinished status, valid linked snapshot and supported authority version. Legacy `0` needs independent snapshot/input/decision/audit admission; it must never be promoted to `1` as a repair. Unavailable legacy inputs fail closed. |
| `internal/pipeline/executor.go:177–239,393–451` | Durable production exact-video/action-type/target success suppresses another mutation. Simulation rows cannot authorize it. External insertion precedes local success recording; missing or failed evidence cannot exclude provider success. The gate is process-local, not multi-writer protection. |
| `internal/quota/quota.go:20–45,125–151,188–255`; `internal/youtube/client.go:490–567` | Default local budget is 10,000; an insertion is priced at 50 and relevant list requests at 1. `CanAfford` checks but does not reserve. Recording follows successful HTTP/body handling; failed/ambiguous requests or failed recording can consume quota absent from the sum. Run summary quota may be stale until finalization. |
| `internal/pipeline/pipeline.go:443–474`; `internal/pipeline/authority.go:208–268` | Local deadline fix detaches bounded terminal writes. Authority-retention failure retries handoff before terminal failure, otherwise attempts interruption. It does not repair old run 14, add arbitrary pending admission, or make startup recover stale `running` rows. Generic phase failure finalization is not a universal pending-to-queue handoff. |

### Dry-run is not a repair, but API-read cost must not be invented

The current source's dry-run is **local-only during evaluation**, not a metadata-fetching job. `dryrun.go:83–110,127–189` selects all globally pending rows, reads cached metadata, runs local exclusion/detection/routing, and records hypothetical decisions/actions. `metadata.Manager.GetDisplayMetadata` (`manager.go:183–195`) only reads SQLite and reports missing/expired data; it does **not** call `RefreshOnDemand`. Exclusion (`exclusion.go:168–178`), detector configuration (`duplicate_config.go:14–38`), detector (`internal/duplicate/detector.go:108–227`) and explicit-route evaluation (`internal/router/engine.go:93–132`) are local paths. Historical completion does local counters/ledger reads.

Accordingly, do not claim this inspected dry-run would spend 19,615 API read units. BR-026 permits reads, but this realization does not perform them. Deployment differences and unrelated browser calls, especially the provider-backed playlist picker, remain separate unknowns. No live zero-call proof is claimed.

A 19,615-input simulation still creates potentially many SQLite decision/audit/action rows, performs corpus comparisons, uses CPU/memory, competes for the writer, and has the same 30-minute manual execution bound. Its source has no per-run subset limit. Missing/expired metadata falls back to empty input; discovery may have stored default duration `0` when enrichment was unavailable (`engine.go:302–367`). A completed hypothetical result therefore is neither a trustworthy metadata-completeness check nor a production admission, a frozen approved batch, or an exact count of needed insertions. Do not run it merely to determine whether the rows survived; small read-only aggregates answer that.

## Smallest additional read-only evidence, in triage order

Obtain this through the operator/parent session, without credentials in reports. These are observation priorities, not an implementation task plan. Use a consistent database read snapshot where possible; separately timestamp process/log and provider evidence.

1. **Identity and activity:** fresh `GET /api/runs/14`, deployed image/revision and migration level; run type, saved status/phase/reason, start/completion, linked configuration and authority version if present. Inventory other unfinished runs and actual active app processes/replicas. Recent run-scoped phase/action logs and an observation interval showing whether state/ledger is changing are useful; a static `running` row alone does not prove a live worker, and a static interval alone does not prove abandonment.
2. **Work and attempt census:** all run-14 discoveries grouped by `(processing_state,final_outcome)`, not only the known pending group. Count distinct videos and total decisions for run 14, separating production from simulations. Count actions by run type/run ID/outcome, with attempt timestamp presence, both for run 14 and for the pending cohort across other runs. Summarize deferred/error rows for that cohort by status, origin and retry owner, joining owner status/type; include unowned counts and distinct-video counts so siblings do not inflate workload. If available, count `run_video_outcome` membership/classifications including null and missing rows. Any action/ownership conflict leads to targeted inspection, not blanket replay.
3. **Budget:** direct quota-ledger sums by bucket and operation for run 14 and all callers in the current provider window, including null-run entries and recent recording times; configured limit/reset settings; operator-supplied provider allocation/usage/reset and other clients sharing that project. Compare rather than treating local remaining as provider truth. Do not increase/reset/delete local quota accounting to make recovery fit. If actual availability or uncertain consumed requests cannot be bounded, wait for a confirmed usable provider window before mutation.
4. **Authority and readiness:** verify linked snapshot availability and inspect enabled routes, conditions and distinct targets; compare with intended current configuration. Aggregate cached metadata presence/active expiry and required-field nulls for the cohort. Fresh non-null values do not alone prove enrichment, because fallback zero/empty values can be persisted as fresh. Inspect discovery warnings and available fetch evidence if those fields affect routing. Small samples can explain an anomaly but cannot prove all 19,615 ready. Preserve the SQLite database and WAL consistently before any later approved repair; do not copy only a live main database file as a purported complete backup.

An initial status read plus aggregate decision/action/queue and budget summaries is sufficient to classify the next branch; no 19,615-row dump, full dry-run, playlist enumeration or raw metadata/credential export is required.

## Options considered

| Option | Benefits | Obligations and risks | Disposition |
|---|---|---|---|
| Start a fresh Run Now | Already exposed; supports newly discovered work and eligible deferred claims | Full discovery costs quota, skips existing IDs and does not generally select run 14's pending rows. Cannot establish the requested recovery. | Reject as incident recovery shortcut. |
| Restart and rely on resume | Existing startup mechanism for genuinely interrupted work | Only `interrupted` rows dispatch. A discovery checkpoint repeats discovery; a fabricated later checkpoint falsely asserts discovery finished. Startup may also resume other runs and issue calls. | Conditional supported behavior, not recommended for unknown run 14. |
| Retry already eligible error/deferred entries | Existing no-discovery receiving-run processing with ownership/snapshot/idempotency checks | Covers only present eligible groups. Requires verified budget, corrected deployed authority and safe single-writer activity. Does not close ambiguous provider attempts. | Prefer if census proves the relevant groups already exist. |
| Tested incident retention repair, then existing explicit queue retries | Reuses queue, group claims, receiving-run snapshots, action checks and inspection; no general coordinator needed | New repair code/verification and explicit operator approval. Must retain exactly the proven abandoned work without overwriting progress, queue ownership or history. Large per-video retry overhead; bulk automation is not supplied by existing API. Existing quota/accounting and provider ambiguity gaps must be addressed or excluded by evidence, not waived silently. | Smallest candidate when rows are unqueued and abandoned, not an available operator recipe. |
| New processing-only admission/batch facility | Useful bounded batches and durable run-owned selection without rediscovery | New behavior/API/ownership and budget contract; must coexist with queue claims and explain partial discovery separately. More scope than narrowly retaining incident leftovers. | Consider only if useful bulk recovery is requested; architect/specifier own contract, coder implements. |
| Full two-job coordinator | Addresses ongoing acquisition/processing control | Proposed only; wider scheduling, admission and policy changes. No present incident operation exists. | Do not make it a prerequisite for a narrow repair. |
| Direct SQL rewrites/deleting rows/watermarks | Superficially fast | Can lose discovery/provenance, steal ownership, repeat actions, promote legacy authority, fabricate completion or force expensive rediscovery. An unconditional state-store method is not a checked repair protocol. | Reject as routine remediation. |

## Decision and constraints

Choose **evidence-gated recovery**, with no live write now. Use existing manual queue retry only for groups already proven eligible. If the supplied pending rows are unqueued leftovers of an abandoned discovery failure, choose a separately tested retention handoff before reusing retry; do not pretend an unsupported resume endpoint exists.

A retention repair must satisfy all of these architectural constraints:

- Establish exclusive operation against the intended database/revision and an actual abandoned owner. Fencing/pausing/restarting is operator work requiring authorization, not performed here. One disabled schedule does not fence startup resumes, manual runs, browser provider calls or a second writer.
- Validate expected run status/phase, snapshot/version, exact candidate membership and absence of conflicting progress/owners inside the repair's writer transaction. Repeated repair must reconcile identically, not append duplicate live queue groups. Never hold a database write transaction over network work.
- Preserve original video discovery identity/time/channel, snapshots, decisions, audits, production success evidence and captured historical outcomes. Do not clear state, promote authority, copy hypothetical evidence, change watermarks or mark each untouched video rejected/failed merely because the run failed.
- Retain every affected unfinished candidate durably before making its abandoned run terminal. The reported phase error calls for truthful terminal `failed` under ADR-002 once established, not `completed`. Existing terminal history is never reopened/recomputed to accommodate a retry. If evidence instead establishes a genuine interrupted later-phase run, normal same-run recovery retains its old snapshot; this document does not reclassify it.
- A new receiving run captures its own initiation snapshot and evaluates its own evidence. Recorded identical production actions suppress duplicates, but any ambiguous provider attempt requires reconciliation before reissue. No user permission can turn an unproved no-duplicate guarantee into a proved one.
- Bound all actual request attempts in the authorized recovery slice, account conservatively for uncertainty, preserve quota deferral and prevent immediate quota retry. Existing `CanAfford` plus success-only accounting is not a strict spend cap. Provider availability, unrelated callers and any metadata refresh must fit the bound. Pending videos are not action units: exclusions/nonmatches, multiple routes and shared targets change the number of insertions.
- New admission/bulk controls require a precise observable contract before implementation: scope, snapshot, budget and stop/retention meaning. Reusing an existing error-retention transaction is not permission to invent an automatic dispatcher or to replace ordinary full-pipeline Run Now.

## Consequences, owners and open questions

| Finding / source | Disposition, owner and blocking effect |
|---|---|
| Supplied incident logs and pending aggregate | Accepted as reported, not fresh observation. Operator supplies current status/activity/census/budget. Blocks choosing any live write. |
| No arbitrary pending API / inspected selector and routes | Confirmed local structural gap. Coder can implement a narrowly authorized repair; architect validates ownership/transaction boundaries. Blocks a claim that the whole backlog is processable today without new code. |
| Eligible queue retry / retry pipeline | Supported only conditionally. Operator verifies group eligibility/deployed revision/budget and chooses explicit execution. No user-owned policy question is needed to identify its current semantics. |
| Generic failed-phase leftovers and abandoned `running` startup gap | Confirmed local gap under BR-011/030, not solved by terminal-context fix. Route to coder/architect for durable retention and reconciliation; do not silently label it resolved because documented. |
| Success-only ledger and provider/local-record crash window | Confirmed existing technical risks, not accepted risk waivers. Coder/architect/reviewer must provide accounting/admission and reconciliation proof or incident evidence excluding uncertain work. Blocks unconditional no-overrun/no-duplicate claims. |
| Dry-run cost warning in incident framing | Corrected: current source does local cached evaluation, not API reads. Deployed parity, local performance and metadata fidelity remain unknown; no large simulation recommended as triage. |
| Scope/configuration/spend preference | User/operator decides whether and when to execute actual recovered actions, acceptable authorized budget and whether bulk processing-only control is desired. Do not auto-change routes/limits or assume all historical discoveries should be acted on immediately. Requirements owner resolves any requested departure from accepted snapshots/failure/retention meaning. |

The cost of waiting for evidence is delayed processing, not lost durable discoveries. Preserving the rows is useful but does not meet runnable recovery by itself. A metadata expiry delay can reduce evaluation fidelity and must remain visible, not become a false rejection.

No provider calls, tests or recovery were executed. The local tests `TestDryRun_EvaluatesPendingVideoWithoutMutations` and `TestProductionAuthority_RetentionDeadlineInterruptsThenResumes` were read only; the latter begins with a real interrupted state and its saved discovery checkpoint, not the reported live stale-running incident. Neither establishes run 14 recovery.

Documentation verification: the new decision/topic index and updated parent index/log passed OKF validation with zero errors/warnings; the shared log retains its existing relative-link notification. Graph checks found the 12 previously recorded unrelated dangling references and no new dangling link from this artifact. No profile is loaded, so typed relationship conformance is unproved. Topic inventory and parent navigation/log are reconciled. Independent content review remains recommended, not completed here.

## Confirmation and reconsideration

Before relying on a repair, obtain independent critique/review and a credential-free, controlled-provider reproduction using a consistent copy or synthetic database with the deployed schema: partial discovery persistence, expired-context terminal-write failure, restart and the proposed admission through actual registered retry paths. Prove exact membership, original ownership/history immutability, no discovery requests, current receiving snapshot authority, preexisting-success suppression, ambiguous-attempt handling, bounded request expenditure, exhausted-budget deferral, rollback on contention/stale owner, repeat-repair safety and reopen recovery. No completed status may be produced for run 14 merely because later receiving work succeeds.

Reconsider the preferred narrow retention repair if the read-only census finds valid later-phase interruption, existing eligible queue groups, another active owner, conflicting action evidence, missing required snapshot/metadata, or a requested bulk workflow. Those materially change applicability; a growing row count alone does not justify a distributed coordinator.
