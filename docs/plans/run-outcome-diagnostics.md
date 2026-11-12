---
type: plan
title: Run Outcome Diagnostics and Simulation Completion
description: Plan durable, safe run diagnostics, truthful simulation completion, and scoped commit/push/PR delivery after all work is verified.
tags: [active, plan:run-outcome-diagnostics, diagnostics, run-history, simulation]
---

## Goal

Give the operator enough persisted, safe evidence to distinguish whether a production playlist action was attempted and what happened, and whether a dry-run/backtest selected inputs, recorded evaluation evidence, and reached its actual terminal state. Preserve production action authority and accepted simulation-isolation semantics. Prove composition through the real shipped SPA and API. After accepted implementation, verification, and documentation are complete, the scoped changes are committed, the current branch is pushed, and a PR is created. This planning update does not perform delivery. It does not decide historical date-range semantics or claim unrecorded outcomes can be reconstructed.

**2026-11-07 correction:** Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`, saved Loom Plan **16**, supersedes Plan 14's delivery dependencies. The correction below governs the older task map, schedule, open-question inventory and checks where they differ. Existing task contracts and completed reports are preserved. This is scope correction, not a new approval gate or architecture restart.

## Release-scope correction: Plan 16

### Decision and sources

Keep guarded **new production mutations**, shared quota, automatic renewal, safe new causal exclusion explanations, diagnostics and simulation isolation in the scoped PR. Do not mistake a fail-closed production mutation outage for feature completion. Optional coordinator expansion and live run14 work are separate; historical reproduction remains unfulfilled, not waived.

Authority read for this correction:

- Existing Action goal, Plan 14's complete 37-node list and progress ledger; critic assessment `0a5cc37b-ae23-45fd-ad49-e87be9860b07` read in full. Its source findings are agent-reported, not independently revalidated code facts here.
- [BR-034](../requirements/yuhaul/br-034-automatic-youtube-token-refresh.md): accepted mandatory automatic renewal, safe status and mutation replay limits.
- [Authorization ARS-001](../architecture/youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) and [ADR-004](../architecture/youtube-authorization/decisions/adr-004-atomic-quota-admission-and-single-attempt-transport.md): decided realization; implementation/composition remain to be proved.
- [Decision-evidence ARS-002](../architecture/decision-evidence/specifications/ars-002-causal-exclusion-capture-and-inspection.md): accepted new causal capture and list/detail contract; positive configured-value release still needs eligibility authority.
- [Evidence lifecycle ARD-001](../architecture/decision-evidence/design/ard-001-run-attributed-evidence-and-metadata-lifecycles.md): full historical reproduction/retention remains blocked; old generic-only “rule unavailable” is accepted only within its stated boundary.
- This assignment authorizes dependency correction, preserves user-owned decisions and excludes code changes/live operations. No product or architecture decision is made here.

### Current plain-language task map

> Reader aid only. Saved Plan 16 contracts and dependencies govern. Earlier numbered map remains the historical base, not the current complete inventory.

#### Existing diagnostic foundations and UI

| Task ID | What this actually does |
|---|---|
| plan | Align status records with accepted failure and interruption meaning without choosing new behavior. |
| t1.1 | Expose durable selected counts and requested-run action reasons without rewriting historical rows. |
| t1.2 | Capture actual selector counts and truthful production and simulation terminal states. |
| t1.3 | Record action dispositions while keeping attempt statistics limited to actual attempts. |
| t1.4 | Keep unsafe details out of diagnostic persistence, responses, events and logs. |
| t1.5 | Build the real producer-to-SQLite-to-registered-HTTP test using producer-created state. |
| t1.6 | Independently execute that composition proof before relying on its API evidence. |
| t2.1 | Show stored lifecycle, distinct counts and every page of requested-run outcomes. |
| t2.2 | Distinguish real action outcomes from no-attempt and hypothetical simulation evidence. |
| t2.3 | Report stored simulation completion without unsupported comparisons or date-filter claims. |
| t2.4 | Compile shipped browser assets with real clients rather than mock defaults. |

#### Remaining repairs and accepted integration

| Task ID | What this actually does |
|---|---|
| sqlite-contention | Preserve durable route evidence under the reported SQLite contention condition. |
| diagnostic-safety | Close remaining raw diagnostic leakage across old and new paths. |
| simulation-timeout | Preserve truthful simulation terminal state after request cancellation. |
| decision-load | Recover from failed or missing Decision Detail reads instead of staying in Loading. |
| empty-simulation-copy | Distinguish completed empty selection from unknown counts and loading. |
| decision-evidence-contract | Resolve positive configured-value eligibility while preserving separate historical retention blockers. |
| decision-context-impl | Complete actual new causal capture and faithful safe explanations in list and detail. |
| auth-owner | Own current authorization, encrypted durable renewal and truthful safe account status. |
| auth-dispatch | Fence each production request and integrate all read spenders with shared admission. |
| auth-send-guard | Preserve exact-target mutation uncertainty and retained work across retries and restart. |
| guarded-mutation-integration | Connect corrected new-run mutations to shared quota without promoting old uncertain history. |
| auth-composition | Prove renewal, shared capacity and lost-response replay protection through real components. |

#### Final proof and scoped delivery

| Task ID | What this actually does |
|---|---|
| design-check | Preserve prior design evidence and its findings as input to final changed-state validation. |
| t3.1 | Prove the final integrated browser journeys after the relevant implementation and repairs. |
| t3.2 | Preserve recorded realization evidence without treating earlier results as proof of later changes. |
| delivery | Synchronize final evidence documentation, then commit, push and open only the reviewed scoped PR. |

#### Separate decisions and conditional work

| Task ID | What this actually does |
|---|---|
| auth-restart-research | Retain bounded diagnosis work without substituting research for mandatory renewal implementation. |
| coordinator-decision | Preserve the product-owned coordinator decision without making expansion a PR prerequisite. |
| run14-assessment | Retain separately authorized read-only recovery assessment without blocking independent code delivery. |
| run14-authorization | Require operational authority before choosing or carrying out live recovery. |
| run14-operation | Perform recovery only if separately authorized, never from this plan update. |
| date-meaning | Preserve the user's date-selection and interim-control decisions without choosing defaults. |
| date-contract | Specify date selection only after the user settles its meaning. |
| date-backend | Apply accepted date bounds after their architecture contract exists. |
| date-ui | Present and submit only the accepted date selection the backend actually supports. |
| date-e2e | Prove the conditional date journey without substituting unfiltered results. |

**Important relationships:** Source-content-free causal implementation does not wait for positive value-release or historical-input retention decisions. Complete positive-value acceptance does require eligibility evidence or an explicit authorized product disposition. Final browser proof follows all material repairs, not just the older UI build.

**Whole plan:** Preserve valid work → finish guarded production and causal inspection → prove the integrated product → document limits → deliver a scoped PR.

### Obligation map and risk boundaries

| Obligation | Satisfier and owner | Verification and disposition |
|---|---|---|
| Working guarded new production mutations; old uncertainty stays vetoed | Authorization ARS-001; `guarded-mutation-integration`, coder | Local refusal/retention/accounting tests → `auth-composition` HTTP production/restart proof → `t3.1` browser action result. Implement. |
| One shared quota owner, once-only accounting, no hidden transport replay | ADR-004; `auth-dispatch` and mutation integrator, coder | Real ledger/SQLite/provider counters; mutation/read contention in both admission orders; non-admission versus admitted failure. `auth-composition`. Implement. In-memory holds do not establish crash-complete accounting. |
| Automatic renewal, commit-before-use, authority precedence and safe status | BR-034/authorization ARS-001; `auth-owner`/`auth-dispatch`, coder | `auth-composition` scheduled/manual expiry, restart, replacement races; final browser status meaning. Implement, not optional research. |
| Actual new causal exclusions in all modes and both surfaces | Decision-evidence ARS-002; `decision-context-impl`, coder | Snapshot-binding and both-producer tests → reopened SQLite/registered list/detail → final SPA proof. Implement; no generic-only new skips. |
| Positive permitted configured-value display | ARS-002 release boundary; `decision-evidence-contract`, architect coordinating compliance/safety and product owners | Record bounded eligibility basis or explicit product disposition; `t3.1` checks permitted display. Blocked only for this acceptance claim; withholding is not fulfillment. |
| Old generic-only evidence remains explicitly incomplete | ARS-002 accepted legacy boundary; causal implementer | Upgrade/reuse/read tests and list/detail “rule unavailable”; no backfill. Implement, not full historical reproduction. |
| Safe diagnostics, truthful lifecycle and simulation isolation | Existing diagnostic contracts and repairs; implementation owners | Sentinel/local checks, producer/HTTP proof and final failure/nonzero simulation browser scenarios. Implement. |
| Full historical reproduction, BR-009 retention and old audit lifecycle | Evidence ARD-001; existing compliance/provider and product owners, tracked in `decision-evidence-contract` | Blocked upstream, not waived or silently deferred by agreement. No complete historical conformance claim is available. Independent source-content-free work and scoped PR can proceed with explicit disclosure. |
| Dates, coordinator/first-run scope, live run14 recovery | Existing separate nodes and their user/product/operations owners | Decisions remain unresolved; only their dependent work is blocked. No new filtering, bounded-first-run, recovery or deployment guarantee is available. |
| Final assembled proof and scoped PR | `t3.1`, reviewer; `delivery`, general | Frozen final artifact, real producer state, exact diff inventory, final doc synchronization and verified PR result. Implement. No live operations here. |

Material boundaries are mutation admission/accounting, durable guard/retry ownership, actual producer-to-inspection causality, safe release eligibility, and final shipping-artifact fidelity. The technical contracts settle the first three; current code readiness is not independently verified by this planning task. Positive release and full historical reproduction are distinct upstream unknowns. Neither justifies an archive, coordinator or architecture restart.

### Task guarded-mutation-integration: Finish guarded production quota integration [M] → owner: coder

Target/change surface: `internal/pipeline/{executor,pipeline,retry}.go`, `internal/youtube/guarded_insert.go`, state run creation and server wiring. These paths come from the read architecture and full critic report; current source contents are **[unverified]** by this planner. Reread before editing.

Type: Backend. Nature: Enhancement. Applicable skills: software-engineering, golang-how-to, golang-database, golang-testing.

Implements: [Authorization ARS-001](../architecture/youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) and [ADR-004](../architecture/youtube-authorization/decisions/adr-004-atomic-quota-admission-and-single-attempt-transport.md).

Outcome: Corrected new production/retry runs can actually mutate through the shared final admission path, while old uncertainty remains protected.

Execution fit: One remaining executor/creator integration loop after existing auth/quota/guard foundations. Claim resolution, final accounting and safe new-run activation share one mutation lifecycle and verification boundary. Do not reimplement those foundations or absorb unrelated discovery/coordinator work. If current foundations are missing, return that bounded gap to their existing owner rather than expanding this task.

Acceptance criteria:

- [ ] Every production mutation uses the same final fenced shared quota owner as production reads; no legacy duplicate charge remains.
- [ ] Executor-owned once-only finalization records admitted calls; positive quota non-admission makes zero calls/rows and preserves its applicable disposition/retained handoff atomically before owned guard release.
- [ ] Only genuinely corrected new StartRun/retry creators enable mutation safety version 1. Never promote historical rows or bypass legacy/success/uncertainty vetoes.
- [ ] Real SQLite and controlled-provider local tests prove allowed new action, refusal, accounting, retained-work atomicity and unchanged legacy fail-closed behavior.
- [ ] Existing network lock limits, automatic renewal, single-attempt transport and simulation isolation remain intact.

### Existing task corrections and exact graph delta

No IDs were renamed or removed. One task was added. Unlisted dependencies remain unchanged.

| Node | Dependency change from Plan 14 | Remaining contract correction |
|---|---|---|
| guarded-mutation-integration | New; depends on `auth-dispatch`, `auth-send-guard` | Bounded task above. |
| auth-composition | Add `guarded-mutation-integration` | Real HTTP StartRun/discovery creates the corrected run; independent provider counts prove mutation/read quota competition and lost-response/reopen/retry veto. No injected corrected run/final evidence. |
| decision-context-impl | Remove `decision-evidence-contract`; retain `t1.6`, `t2.2` | Accepted ARS already exists. Preserve valid work; finish snapshot-bound causal producers and both list/detail consumers without waiting for positive release permission. One coder owns remaining contract conformance; re-slice only if current discovery exposes independent unfinished reasoning threads. |
| decision-evidence-contract | No dependency change | Do not restart accepted technical/legacy decisions. Own remaining positive release eligibility/disposition and visible historical blockers; architect coordinates the decision, not coder. |
| t3.1 | Add `auth-composition`, `decision-context-impl`, `sqlite-contention`, `diagnostic-safety`, `simulation-timeout`, `decision-load`, `empty-simulation-copy`, `decision-evidence-contract` | Final frozen-artifact browser proof after repairs; recheck changed UI design states. Withheld values do not pass positive-display acceptance. |
| delivery | Remove `auth-restart-research`, `run14-assessment`, `coordinator-decision`; add `auth-composition`, `decision-context-impl`, `t3.1` | Final doc/index/log synchronization follows new proof. Scoped PR discloses unresolved historical/date guarantees; no deployment/live-operation or whole-product conformance claim. |

Preserved completed contracts/reports: `plan`, `t1.2`, `t1.3`, `t1.5`, `t1.6`, `t2.4`, `t3.2`, `design-check`. They remain byte-for-byte unchanged in the saved node definitions. Reported completion is not fresh acceptance of later changes. `t3.1` owns affected final-build/design revalidation; `delivery` owns final evidence-document synchronization without deleting earlier reports.

### Ownership and remaining execution waves

These are continuation waves, not a restart of completed work. Historical open state in Loom does not prove implementation is missing; consume existing results and verify the relevant current handoff.

1. Existing foundation owners finish only unmet `auth-owner`/`auth-dispatch`/`auth-send-guard` obligations in dependency order. The architect can independently resolve bounded configured-value eligibility. Causal implementation can proceed against accepted ARS without that decision.
2. `guarded-mutation-integration`, coder, after its foundations. Serialize its pipeline/state/server writes with causal producer or safety repairs; hand over after rereading the integrated tree. Disjoint frontend work may proceed with stable API contracts and isolated test state. No blanket safe parallelism is claimed.
3. `auth-composition`, reviewer, after integrated mutations. Causal implementation owns its real producer/SQLite/HTTP vertical checks. Finish any remaining safety, contention, lifecycle and read-recovery repairs before final browser proof.
4. `t3.1`, reviewer: rebuild/freeze the integrated real-client artifact, revalidate changed human states, then execute the representative journeys. Positive-value acceptance waits only for its named eligibility/disposition decision.
5. `delivery`, general: synchronize final observed realization and required indexes/logs; inventory reviewed Action-owned diffs; commit/push/open PR only after prerequisites. Preserve unrelated Settings picker edits. No dispatch, commit, push, live operation or test execution is performed by this correction.

First bounded integration task: `guarded-mutation-integration`. Its two prerequisites require a current bounded handoff; partial discovery integration or old green tests alone do not establish they are ready. If a prerequisite remains unfinished, finish that existing bounded remainder first rather than inventing another investigation gate.

### Final proof allocation

Keep the existing three browser scenarios, extending their final observations rather than creating one scenario per requirement:

- **E2E-001 / `t3.1`:** real-client browser → registered API → new production discovery/pipeline → real auth/guard/shared quota → controlled YouTube → SQLite → list/detail. Inputs/configuration come from real upstream paths, not final-row injection. Assert actual provider/action evidence, truthful counts/status, safe new causal explanations on both surfaces and accurate account state. Use the existing isolated token prerequisite; it does not prove OAuth renewal.
- **E2E-002 / `t3.1`:** retain the separate no-token phase-failure journey and safe durable failure outside an empty Error Queue. No start acknowledgement is completion.
- **E2E-003 / `t3.1`:** retain genuinely discovered pending input and nonzero retained corpus; dry-run has zero mutation calls and Backtest zero YouTube calls, with unchanged production corpus/idempotency/statistics. Causes remain hypothetical and dates blank under current all-retained meaning.

**Authorization/recovery composition / `auth-composition`:** real manual/scheduled entry, auth service/encrypted store, adapters, shared ledger, executor, queues and registered API; substitute only controlled external OAuth and YouTube endpoints. Prove expired-start/restart/between-call renewal, authority races, 50-unit mutation versus 1-unit read contention in both orders, and applied/lost-response → reopen → real retry with zero second POST. Static prerequisites may use the approved auth store; exercised authorization/run/action/guard states must come from the real path. Human-facing uncertainty/status interpretation is checked in final browser proof. These are composition discriminators, not live-account compatibility or full OAuth initiation coverage.

No source test, browser journey or code finding is newly validated by this document. PASS requires executed evidence for its exact claim and artifact.

### Correction checks and unresolved boundaries

- Saved Plan 16 has **38 nodes: 37 tasks and one grouping phase**. All 37 prior IDs remain; the one new ID is `guarded-mutation-integration`. Eight reported-complete contracts are unchanged. There are five changed existing contracts and one new contract.
- Final saved Plan 16 readback was compared programmatically with revision 14: no removed IDs, exactly the five changed contracts listed above, one added task, eight unchanged completed contracts, no cycles and no dangling task dependencies. Plan storage is not product proof.
- Ten obligation groups above have owners, satisfiers/proof paths or explicit blocked dispositions. No historical guarantee is labeled deferred by agreement without authority. No new technical realization is selected.
- Five continuation waves respect dependency and overlapping-file ownership. No optional coordinator, diagnostic investigation or live run14 operation is required for the scoped PR.
- Three final browser scenarios plus the existing authorization composition task cover materially different outcomes with real product components and upstream state. Local/vertical evidence remains distinct from final browser acceptance.
- Four unresolved owner-boundary groups remain: date meaning (user/design), positive configured-value release (compliance/safety plus architect, or explicit product disposition), full historical-input retention/reproduction/old audit lifecycle (compliance/provider plus product), and optional coordinator/live run14 decisions (product/architecture and operations respectively). Only dependent work or claims wait.
- Current implementation readiness is **[unverified]** by this planner. Critic findings inform task scope but are not independently reproduced. This correction is ready for bounded continuation, **not** evidence the release is ready.
- No plans index/log existed at the inspected paths. This update stays in the existing indexed plan file rather than inventing new bundle artifacts. Earlier “only date remains open,” task counts, schedules and validation results below are historical and must not be reused as current checks.
- Current `validate_doc` result: one file, zero errors, one W3 warning for unknown type `plan`. Required plan frontmatter is retained. Structural validation does not establish content correctness or runtime readiness.

## Plain-language task map

> Reader aid only. The detailed tasks, acceptance criteria, and dependencies govern if this ever differs.

### Phase 0: Reconcile accepted authority

| Task | What this actually does |
| ---- | ------------------------ |
| 0.1 | Align architecture records with the already accepted rule that unrecoverable phase failures are failed, not completed. |

### Phase 1: Make run and action evidence durable and safe

| Task | What this actually does |
| ---- | ------------------------ |
| 1.1 | Add durable selected-input and action-reason fields, with distinct run-scoped evidence in the API. |
| 1.2 | Record each run's selected inputs and truthful terminal status across production and simulation modes. |
| 1.3 | Record each action disposition and keep `ActionsExecuted` limited to real attempts. |
| 1.4 | Keep unsafe provider details and legacy free text out of diagnostics, API responses, events, and logs. |
| 1.5 | Build a test that runs the real pipeline and reads its SQLite-produced result through the registered HTTP API. |
| 1.6 | Have an independent reviewer prove the new producer-to-API test is real, correctly sourced, and executed. |

### Phase 2: Expose the evidence in the shipped experience

| Task | What this actually does |
| ---- | ------------------------ |
| 2.1 | Show stored status, selected/evaluated counts, safe run failure detail, and every page of run outcomes. |
| 2.2 | Show the requested run's per-action result and reason without implying an unattempted action succeeded. |
| 2.3 | Make Backtest/Replay report the requested run's real state and remove the unsupported production-total comparison. |
| 2.4 | Build the shipped SPA with its real API/event clients rather than the mock-default bundle. |

### Phase 3: Prove the product and record realization

| Task | What this actually does |
| ---- | ------------------------ |
| design-check | Have a designer validate the implemented Run, Decision Detail, and Backtest/Replay views against the accepted UI design before assembled-product acceptance. |
| 3.1 | Verify production action, run-failure, dry-run, and backtest journeys through the real shipped UI and API. |
| 3.2 | Record what implementation and end-to-end evidence actually established, including remaining limits. |

### Phase 4: Deliver after accepted work is complete

| Task | What this actually does |
| ---- | ------------------------ |
| delivery | Select only this Action's reviewed changes, preserve unrelated working-tree edits, then commit, push the current branch, and open a PR. |

### Follow-up phase: close remaining defects and gate decisions

| Task | What this actually does |
| ---- | ------------------------ |
| F1 | Repair the confirmed route-evaluation contention boundary without touching live data. |
| F2 | Prevent raw error chains and unsafe historical text from escaping through diagnostics or logs. |
| F3 | Keep canceled dry-run and replay execution from leaving misleading terminal state. |
| F4 | Give Decision Detail a recoverable state when a read fails or finds no record. |
| F5 | Explain a completed simulation that selected no inputs distinctly from unknown or loading. |
| F6 | Settle the evidence meaning for video/channel context and exclusion explanations before changing code. |
| F7 | Show only the decision context that the settled contract says is durably supported. |
| F8 | Trace the post-restart auth disconnect and report cause or bounded unknowns without changing behavior. |
| F9 | Assess run14 using fresh read-only evidence; do not infer safety from historical facts. |
| F10 | Require explicit operational authorization before any live run14 recovery. |
| F11 | Decide whether coordinator/first-run bounds are accepted scope; do not invent spend limits. |

### Blocked dependent branch: date picker and filtering

| Task | What this actually does |
| ---- | ------------------------ |
| date-meaning | Wait for the user-owned date semantics and interim control-presentation decision; choose no defaults. |
| date-contract | Have the architect record the accepted date-selection/API realization after that decision. |
| date-backend | Apply the accepted range to real retained-observation selection and run evidence. |
| date-ui | Present the accepted picker behavior and send only the range the backend applies. |
| date-e2e | Prove range selection from the real browser through the API and persisted run. |

> These five date nodes are explicitly blocked and are not prerequisites for independent diagnostic work. If the user resolves and accepts this branch, date-e2e becomes a delivery prerequisite; otherwise delivery reports the unresolved date request without implying it was implemented.

### Important task relationships

- 1.1 provides durable fields/projections; 1.2 and 1.3 make real producers write them; 1.4 secures the resulting diagnostic boundary; 1.5 builds the vertical test and 1.6 independently verifies it before UI work depends on the proof.
- 1.2 precedes 1.3 because both touch the pipeline action path; 1.3 also owns the coupled `ActionsExecuted` projection so newly stored no-attempt rows do not alter existing metric meaning. 1.3 precedes 1.4 so security coverage includes complete run/action paths.
- 2.1 owns shared response types before 2.2 adds action presentation. 2.3 is separate because Backtest/Replay's false completion and comparison are a distinct user path.
- `design-check` validates the Task 2.1–2.4 implemented UI against the accepted design before 3.1 records assembled-product acceptance; 3.1 consumes its findings, and design conformance alone is not end-to-end proof.
- Date filtering is not implemented here. Existing date controls are not evidence a filter was applied; the filter remains blocked until its meaning is decided.
- 3.1 uses real product-owned components; only YouTube is replaced by a controlled external provider endpoint.
- `delivery` follows 3.2 and all transitive core implementation/verification. `date-e2e` is additionally required before delivery only if date semantics and implementation are resolved and accepted. The delivery task must not stage the existing Settings picker changes.

### Whole plan in one sentence

Reconcile accepted status authority → persist and safely expose run/action evidence → render it, validate it against the accepted design, and prove it through the real product → synchronize docs → commit the scoped changes, push the current branch, and create a PR.

## Assumptions

- **Accepted status authority:** System Architecture §Run Recovery and the supplied specifier follow-up say an unrecoverable processing-phase failure is terminal `failed`; `interrupted` remains a resumable interruption; `completed` means successful completion. Task 0.1 reconciles the status records but does not choose this meaning.
- The accepted realization is the existing Run row plus run-scoped decision/action evidence. No general event ledger, per-video selected-but-unevaluated membership ledger, or provider-side reconciliation is introduced.
- Null selected count means selection was not captured/known; zero means selection completed and found no inputs. Old runs remain unknown rather than reconstructed or backfilled.
- Existing Backtest/Replay date fields do not define date semantics; no timestamp, timezone, or endpoint inclusion rule is assumed.
- **Working-tree preservation:** at plan creation, `web/src/views/SettingsView.tsx`, `web/src/views/SettingsView.test.tsx`, `docs/architecture/index.md`, and `docs/architecture/log.md` are modified, and the ARD-001 directory/artifact is untracked. This plan writes only this plan file. Downstream work must reread and preserve the in-progress Settings changes and coordinate architecture index/log updates rather than reverting or replacing them.

## Out of scope

- **Date-filter implementation is blocked, not silently deferred.** Do not choose `discovered_at`, UTC/local interpretation, inclusivity, or omitted/reversed-bound behavior. No date-bound schema fields, filter behavior, or date-selection claims are part of executable tasks before the owner resolves those meanings. Existing controls are not changed here; selection by date remains unavailable and must not be represented as implemented.
- **BR-027 AC5 same-application production comparison is deferred by agreement** for first-use launch. No baseline, new/lost/net catch calculation, or comparison UI is implemented. The unavailable guarantee is comparison-derived impact against an earlier same-app production run.
- No general event ledger or per-video selected-input membership history. The result is aggregate selected/evaluated counts; an input without decision evidence cannot be identified or assigned a negative outcome by this work.
- No provider-success/local-record crash reconciliation, outbox, or new idempotency protocol. If the provider may have applied a request but local evidence is absent, the outcome remains unknown.
- No OAuth refresh redesign, Error Queue schema/lifecycle redesign, historical re-evaluation, or repair of the separate Settings corpus-import-state 404 observed in supplied logs.
- BR-028 AC7 retry-count display and AC8 cross-run search remain separate inspection obligations, unchanged by this bounded scope.
- OC-001 comprehensive response-category accounting for every read-only YouTube call is not implemented or claimed; ARD-001 treats it as a separate concern. Safe action/run diagnostics do not close that broader quota-accounting obligation.

## Sources

| Source | Status and authority used here |
|---|---|
| User request and specifier follow-up supplied with this planning task | Accepted goal: diagnose missing production playlist actions and distinguish simulation selection/evaluation/completion. Supplied clarification settles `failed` as the unrecoverable run-phase terminal status, says to reconcile ADR-002, and leaves Backtest/Replay date semantics undecided. |
| User delivery instruction in this continuation | After implementation, required verification, and documentation are complete, commit only scoped changes, push the current branch, and create a PR. Do not deliver during this plan update. |
| Independent reviewer concern `984e5ea6-23ba-4f0f-a28a-a58a88060612` | Reviewer-reported composition gap: existing pipeline tests read state directly and API tests seed rows; no test was found that runs the real pipeline and then reads its output through registered HTTP. The report is retained as reported evidence; Task 1.5 constructs and Task 1.6 independently verifies the missing seam. |
| [ARD-001 — Durable Run Diagnostics and Simulation Input Visibility](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) | Chosen structural realization: reuse Run/evidence; selected/evaluated counts; action outcome/reason; safe diagnostics; persisted-status reads; pagination; no fabricated comparison; real SPA build mode. **Repository mismatch:** file currently says `Status: proposed` and retains the run-status question, while caller says this realization is accepted. Task 0.1 reconciles the record without changing the accepted design. |
| [ADR-002 — Failed-Phase Runs Terminate as `failed`, Not `completed`](../architecture/adr-002-run-status-failed-terminal-state.md) | Body specifies the agreed terminal-vs-resumable distinction, but file says `Status: proposed`. Supplied follow-up confirms behavior; Task 0.1 records its accepted status. |
| [BR-026 — Dry-Run Evaluation](../requirements/yuhaul/br-026-dry-run-evaluation.md) | Accepted MUST: evaluate and record hypothetical actions without YouTube mutations or production idempotency changes. |
| [BR-027 — Historical Backtesting](../requirements/yuhaul/br-027-historical-backtesting.md) | Accepted MUST: evaluate retained observations without YouTube calls or production-state/statistics mutation. AC5 is deferred from first-use. Date selection is required, but its meaning is unresolved. |
| [BR-028 — Historical Run Inspection](../requirements/yuhaul/br-028-historical-run-inspection.md) | Accepted MUST: inspect requested-run persisted evidence; run diagnostics and pagination are in scope. AC7/8 are explicitly unchanged in ARD-001 and out of this plan. |
| [BR-032 — Processing Outcome Distinction](../requirements/yuhaul/br-032-processing-outcome-distinction.md) and [OC-002 — Route Evaluation Outcome Semantics](../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) | Accepted shared vocabulary: absence of processing is not rejection; run status is not a video-outcome category; no local outcome variant is added. |
| [BR-011](../requirements/yuhaul/br-011-processing-state-persistence.md), [BR-013](../requirements/yuhaul/br-013-quota-limited-work-deferral.md), [BR-017](../requirements/yuhaul/br-017-manual-run-initiation.md), [BR-025](../requirements/yuhaul/br-025-configuration-history-for-statistics-interpretation.md), [BR-030](../requirements/yuhaul/br-030-temporary-failure-work-preservation.md), [Error Queue](../requirements/yuhaul/error-queue.md), and [OC-001](../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) | Accepted MUST contracts: durable recovery and manual initiation remain intact; quota deferral and per-video failure remain in existing queues; diagnostic scope does not claim complete OC-001 API-call category accounting. |
| [BR-012](../requirements/yuhaul/br-012-idempotent-playlist-actions.md), [BR-014](../requirements/yuhaul/br-014-failed-work-retention-and-retry.md), [BR-015](../requirements/yuhaul/br-015-explainable-route-decisions.md), [BR-020](../requirements/yuhaul/br-020-run-level-and-route-level-statistics.md), [Run-Route Statistics](../requirements/yuhaul/run-route-statistics.md), [OC-003](../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md) | Accepted preservation obligations: no fabricated current-run success; existing video-error queue ownership; durable route evidence and immutable run configuration; truthful summaries. |
| [ARS-001 — Route, Playlist, and Run-Outcome API Contracts](../architecture/ars-001-route-and-playlist-api-contracts.md) | Accepted API contract: run-scoped filtering/totals and existing `page`/`page_size` pagination. Endpoint caps at 100 videos; the UI must expose later pages. |
| [YuHaul System Architecture](../architecture/yuhaul-system-architecture.md) | Accepted architecture, including §Run Recovery (`failed` terminal; `interrupted` resumable), the three-tier outcome model, INV-8 simulation isolation, and deferred-work constraints. This accepted status rule resolves the proposed marker conflict in ADR-002/ARD-001. |
| [ARD-001 — Run-Owned Production Action Authority](../architecture/duplicate-continuation/design/ard-001-run-owned-production-action-authority.md) | Accepted production-action authority: only a production run's own snapshot-admissible matches authorize its actions; simulation evidence cannot authorize production; provider-success/local-record crash reconciliation remains unproven. |
| [ARS-005 — Duplicate Continuation Compatibility and Results Contract](../architecture/duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md) | Accepted implementation contract for run-owned actions, simulation isolation, run-scoped inspection, no general selected-input ledger, and explicit first-use deferral of same-app comparison. |
| [ARS-006 — Run-Attributed Production Outcomes](../architecture/duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md) | Accepted bounded run-local outcome capture and null legacy projection. This plan adds selected/evaluated counts and action evidence without duplicating its `run_video_outcome` relation or inferring legacy outcomes. |
| [YuHaul Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) | Accepted human-facing constraints: Run Detail, mode-qualified simulation evidence, actual vs simulated action wording, and no inferred production outcome from hypothetical results. Comparison design is future-target/deferred. |
| Repository `AGENTS.md` | OKF documentation/index/log updates are required when authoritative docs change; existing shared edits must be reread before editing. |
| Direct source inspection in this session | Grounded implementation surfaces: `internal/pipeline/{pipeline,dryrun,replay,executor}.go`, `internal/state/{models,store}.go`, `internal/server/{server,run_handlers}.go`, `web/src/views/{RunsView,RunDetailView,DecisionDetailView,BacktestView}.tsx`, `web/src/api/runs.ts`, `web/src/types.ts`, `Dockerfile`, and `web/package.json`. These observations are not acceptance authority or test results. |

### Current implementation evidence read for this plan

- `internal/pipeline/dryrun.go` and `replay.go` show interrupt-then-complete failure/panic paths; `pipeline.go` already uses `FailRun` on its regular production failure path. `internal/state/models.go` includes `failed`; `web/src/types.ts` does not.
- `dryrun.go` selects pending videos; `replay.go` selects retained videos. `internal/state/store.go` derives simulated `videos_discovered` from decision rows. New counts must not reuse these fields ambiguously.
- `internal/pipeline/executor.go` has a YouTube base-URL seam and persists success, while error and proactive quota/idempotency paths do not all yet create the required run-scoped action outcomes. `internal/stats/store.go` separately sums succeeded/failed outcomes but currently computes `ActionsExecuted` as `COUNT(ea.id)` over all action rows, so newly persisted deferred/not-attempted rows would inflate the metric; this is source inspection, not runtime test evidence.
- `internal/server/run_handlers.go` returns requested-run detail; `RunDetailView.tsx` requests only the first 100 items and lacks authoritative terminal-status polling. `BacktestView.tsx` fetches immediately after start, toasts completion, and has an aggregate-production-total fallback comparison.
- `Dockerfile` builds without `VITE_USE_MOCK=false`; `web/package.json` declares Vitest component tests but no browser-E2E script. The browser proof must still exercise the actual built SPA.

## Obligation map

| Obligation | Owner / satisfier | Verification path | Disposition |
|---|---|---|---|
| BR-011/BR-017: start acknowledgement is not completion; manual start remains supported, and durable `running`, `completed`, `failed`, and `interrupted` states are distinguishable. | Lifecycle Task 1.2; UI Task 2.1; authority record Task 0.1. | Pipeline/state tests; vertical construction/review Tasks 1.5–1.6; E2E-002/003. | implement |
| BR-032/OC-002 and accepted Run Recovery rule: unrecoverable phase failure is terminal `failed`, not completed or auto-resumed; resumable interruption remains `interrupted`, and run status is not a video outcome. | Accepted status rule: Task 0.1; implementation Task 1.2. | Persisted state/recovery tests; E2E-002. | implement |
| BR-020: selected count distinguishes known zero from unknown; evaluated count is distinct requested-run decision evidence; old values are not guessed and `videos_discovered` remains unchanged. | Schema/API Task 1.1; selectors Task 1.2; UI Task 2.1. | Migration/store tests; mode-selector tests; Task 1.6; E2E-003. | implement |
| BR-020 / Run-Route Statistics: `ActionsExecuted` counts actual attempts only; successful inserts and actual attempted failures count, while deferred and not_attempted rows do not. | Task 1.3 owns action outcomes and the existing `internal/stats/store.go` projection. | Focused route-statistics tests cover success, an actually attempted provider failure, quota deferral, and not_attempted, while preserving simulation isolation. | implement |
| BR-025/OC-003: production route/action evaluation continues to use the run-linked immutable configuration snapshot; route semantics and shared video-outcome vocabulary remain unchanged. | Tasks 1.2–1.3; UI interpretation Tasks 2.1–2.3. | Regression tests; SQLite/provider tests Tasks 1.2–1.6; E2E-001/003. | implement |
| BR-015/BR-028: requested-run route, condition, action, and video evidence remains persisted and inspectable without live reconstruction; action results use only existing outcomes. | Persistence/API Task 1.1; producer Task 1.3; UI Task 2.2. | Executor/state tests; Task 1.6; E2E-001. | implement |
| Prior exact-target success is explicit `not_attempted`/`already_completed`, never a current-run success/timestamp. | Tasks 1.3 and 2.2. | Idempotency regression, API/UI evidence, Task 1.5. | implement |
| Quota deferral, failure, and simulation are not counted as external attempts; unknown provider/local-record state remains unconfirmed. | Task 1.3. | Outcome/timestamp tests; Task 1.6; E2E-001. | implement |
| BR-014/Error Queue: run-level failure is visible outside the Errors queue; video failures remain inspectable/retryable under the existing queue lifecycle. | Tasks 1.2, 1.4, 2.1. | Queue-empty run failure E2E-002; video action failure E2E-001. | implement |
| Credentials, raw provider bodies/chains, authorization data, and unsafe legacy text do not cross diagnostic or log boundaries. | Task 1.4; UI Tasks 2.1–2.2. | Sentinel persistence/API/event/log tests; legacy projection tests; E2E-001/002. | implement |
| Dry-run remains hypothetical and does not mutate YouTube or production corpus/idempotency/statistics. | Tasks 1.2–1.3 and 2.1–2.3. | Mode tests; E2E-003 with mutation-counting provider fake. | implement |
| Replay/backtest make no YouTube calls and do not mutate production state/idempotency/statistics. | Tasks 1.2–1.3 and 2.3. | Mode tests; E2E-003 asserts zero calls during backtest. | implement |
| Backtest completion means evaluation finished; no comparison is inferred from unrelated production totals. | Tasks 1.2 and 2.3. | Backtest view test and E2E-003. | implement |
| BR-026 AC6: dry-run and actual-run evidence remain separately inspectable for comparison; this bounded plan adds no comparison/baseline feature. | Already supplied by the current run-scoped evaluation/evidence path per ARD-001; preserve in Tasks 1.2/2.1. | Existing UI/pipeline regressions and Task 3.1 verify actual vs hypothetical evidence is not conflated; no comparison test was run during planning. | already satisfied |
| BR-028 AC1–6/9: requested-run summaries, per-video/route evidence, deferral/exclusion explanation, and persisted-only inspection remain authoritative. | Existing ARD-001 run-scoped evidence path; Tasks 1.5–1.6, 2.1–2.2, and 3.1 guard against leakage/regression. | Run-scoped API composition and browser inspection; current live-provider reconstruction is not allowed. | implement |
| ARD-001 producer→SQLite→HTTP composition proof: a real pipeline-produced run is read through registered HTTP endpoints, not only tested in separate producer/API suites. | Test implementation: Task 1.5; independent verification: Task 1.6; frontend gate: Task 2.1. | Real selector/pipeline → migrated SQLite → registered HTTP list/detail read, with no final-row seeding and only YouTube substituted. | implement |
| BR-027 AC1 date-range selection and the current visible-but-unwired date controls. | Product/requirements owner must decide timestamp, timezone, endpoint, and partial/reversed-range meaning; design owner decides how controls are represented until wired. | No authoritative filter test or date-control claim before those decisions; blank-bound all-corpus remains testable. | blocked |
| BR-027 AC5 same-app historical production comparison. | No task; first-use comparison is explicitly deferred. | No comparison proof is claimed. | deferred by agreement |
| BR-028 AC7 retry-count display and AC8 cross-run search. | Separate inspection work; ARD-001 says unchanged. | Not expanded or claimed here. | out of scope by agreement |
| Identify every selected-but-unevaluated input or reconstruct missing evidence. | No ledger/reconstruction in accepted architecture; only aggregate counts and persisted per-video evidence. | Verify missing evidence is not shown as rejection; count gap stays aggregate. | out of scope by agreement |
| Reconcile provider success after process loss before local action persistence. | Separate architecture/authority work. | Tests must not infer failure/no mutation from an absent row or ambiguous transport error. | out of scope by agreement |
| OC-001 complete response-category accounting for all read-only YouTube calls. | Separate quota-accounting obligation explicitly not solved by ARD-001. | Action diagnostics do not prove all-operation accounting. | out of scope by agreement |
| Authorized delivery: after all accepted implementation, verification, and documentation, commit the scoped changes, push the current branch, and create a PR. | Task `delivery`; only action-owned paths/diffs are eligible. Existing Settings picker edits are explicitly excluded and preserved. | Exact-path inventory and diff classification, commit identity, confirmed pushed branch, and PR URL; inability is reported as blocked, not complete. | implement |

## Risk boundaries

1. **Authority-record conflict — implementation risk, not an open behavior choice.** Caller says run-status policy is accepted, but ARD-001/ADR-002 currently say proposed. Status-changing work depends on Task 0.1; no task may choose another status meaning.
2. **Date selection — blocked only for date-filter scope.** Timestamp, timezone, and endpoint rules affect filter/query semantics, date metadata, UI claims, and boundary tests. They do not block all-corpus backtests, diagnostics, counts, or status. Do not use `discovered_at`/UTC/inclusive as authority.
3. **Producer/API false-green risk — confirmed.** Existing producer tests inspect stores directly and API tests seed rows; neither proves real pipeline→SQLite→registered HTTP composition. Task 1.5 constructs that boundary and independent Task 1.6 verifies it before UI depends on the evidence.
4. **Composition false-green — confirmed verification gap.** The reviewer found pipeline tests reading state directly and API tests seeding rows, with no pipeline-produced state read through registered HTTP. Test construction and independent review are separate tasks and block frontend reliance.
5. **Action-statistics classification — confirmed correctness risk.** New deferred and not-attempted action rows enter the same table used by route statistics; the existing `ActionsExecuted` projection must continue to count actual attempts only. Task 1.3 owns the focused correction and regression tests.
6. **Sensitive diagnostics — high security consequence.** Provider error text and legacy free-text fields cross storage/API/UI/log boundaries. Task 1.4 owns the complete boundary; sentinel tests cover persistence, JSON, events, logs, and legacy rows. UI-only hiding is insufficient.
7. **False completion/count — high product consequence.** Start is an acknowledgement; missing decision evidence is not rejection; discovered count is not selected count; simulation success is not production insertion. Task 1.5 constructs the producer→SQLite→HTTP test; independent Task 1.6 proves that seam before frontend work; Task 3.1 is assembled browser proof.
8. **Simulation isolation — mandatory safety boundary.** Only YouTube is substituted in final proof. The SPA, API, selectors, pipeline, and DB remain real; the provider fake records requests.
9. **Shipping fidelity — Vite configuration is compiled into the SPA.** Production artifact must be built with `VITE_USE_MOCK=false`; runtime env alone is not evidence. Task 2.4 precedes browser proof.
10. **Evidence limits remain visible.** Old selected counts remain null; unsafe historical reasons become unavailable, not leaked/guessed; aggregate gaps do not identify individual untouched inputs; ambiguous provider success without local evidence remains unknown.

## End-to-end proof

All scenarios run after Tasks 1.1–1.6 and 2.1–2.4 against the shipped SPA and actual browser/API path, not component tests alone. Real product-owned stages: SPA, server/router, pipeline, stores, SQLite migration, and requested-run read APIs. The only external substitution is a controlled YouTube Data API endpoint through the existing provider base-URL seam. Setup/configuration and input state come through real product entry points; directly inserted final run/action/decision rows are not evidence.

### E2E-001: Diagnose a production playlist-action result

- Outcome: The operator distinguishes a real action attempt/result from no-attempt or unknown state, without unsafe provider detail.
- Entry point: Browser; configure a route and start a manual run in the production-built SPA.
- Auth state precondition (persisted state, not an external substitution): the isolated test database is given one non-secret, unexpired test token written through the production `auth.SQLiteTokenStore`. The shipped server still constructs the production `auth.Service` and reads the token through the real encrypted store/`ActiveToken` path; no auth component is mocked or bypassed. No Google credentials, real Google account, or live Google endpoint is used, and this scenario makes no claim about OAuth initiation, callback/state validation, token exchange, refresh, or revocation. E2E-002 remains the separate no-active-token failure path.
- Product-owned path: SPA config → real API/config snapshot → run start → production discovery/routing → action executor → SQLite Run/route/action/error evidence → run/detail/decision APIs → SPA.
- External substitutions: Controlled YouTube Data API endpoint only; it supplies discovery inputs and successful/definitive failing playlist-insert responses while recording reads/mutations. The app adapter/executor are real. The auth token above is persisted test state, not a substituted endpoint: no Google/OAuth HTTP is redirected or simulated.
- State provenance: Route is created through real UI/API; video and decisions come from real production discovery/routing; action result comes from real executor response handling and persistence. The only seeded precondition is the auth credential row; no final run, terminal status, selected/evaluated count, route/decision, action, or outcome row is directly inserted.
- Externally meaningful assertion: Success appears only with same-run durable success evidence; definitive provider rejection appears as failed with its safe reason and existing video-error evidence; fake call log agrees with displayed attempt. Idempotency suppression is not attempted, never current-run success.
- Failure/recovery: Per-video failure stays in the Error Queue and requested-run detail. No stronger claim for ambiguous timeout/crash. Sentinel response-body/token text is absent from SQLite diagnostics, API/events/UI, and logs.
- Human design check: Required—wording, order, keyboard/screen-reader interpretation, responsive Run/Decision Detail, and production/simulation distinction against accepted design.
- Executes in: Task 3.1.

### E2E-002: See an unrecoverable run failure outside the Errors queue

- Outcome: A pre-action phase failure has truthful persisted terminal status and safe run-level explanation; it is neither completed nor hidden by an empty video-error queue.
- Entry point: Browser; initiate a manual run and open Run Detail.
- Product-owned path: SPA → real run API → real production pipeline/auth-phase failure handling → SQLite Run → persisted run read → Run List/Run Detail.
- External substitutions: None required for a no-active-token stimulus. The real app/auth/pipeline failure path runs; final status is not directly edited.
- State provenance: UI/API creates the run; legitimate unavailable auth makes the real pipeline fail; real Error Queue API confirms no synthetic video queue item.
- Externally meaningful assertion: UI begins in running/unknown state after start, then shows persisted `failed` and safe phase/operation detail; it never treats start acknowledgement as completion.
- Failure/recovery: Failed is not shown as resumable interruption. Status-read failure keeps completion unknown and gives retry/recovery affordance.
- Human design check: Required—failure text/status are perceivable without color alone, readable at narrow widths, and contain no raw internal error.
- Executes in: Task 3.1.

### E2E-003: Confirm dry-run/backtest evaluation and completion without production effects

- Outcome: The operator distinguishes selected inputs, decision-bearing inputs, and terminal simulation state for dry-run and backtest, and can inspect hypothetical run evidence separately from the actual production run/action evidence established by E2E-001, without a simulation mutation claim.
- Entry point: Browser; start a dry-run on a genuinely pending observation produced by real discovery and a controlled pre-evaluation failure, then a Backtest over the retained corpus, and inspect the requested runs. Actual production action evidence comes from E2E-001; this scenario does not start a production run on that older pending observation, because a later Start Run cannot re-admit it.
- Auth state precondition (persisted state, not an external substitution): the isolated test database is given one non-secret, unexpired test token written through the production `auth.SQLiteTokenStore`, because the scenario's real upstream discovery requires an active token. The shipped server still constructs the production `auth.Service` and reads through the real encrypted store/`ActiveToken` path; no auth component is mocked or bypassed. No Google credentials, real Google account, or live Google endpoint is used, and this scenario makes no claim about OAuth initiation, callback/state validation, token exchange, refresh, or revocation. E2E-002 remains the separate no-active-token failure path.
- Product-owned path: Real upstream discovery creates retained video state; a controlled pre-evaluation failure at the lifecycle transition leaves a legitimately pending observation; real SPA → dry-run endpoint/selector/evaluator with a nonzero pending input set → SQLite Run/decision/action evidence → real Backtest endpoint/retained selector with date bounds empty under current all-retained semantics and a nonzero retained input set → persisted-status reads → Run/Decision Detail and Backtest.
- External substitutions: Controlled YouTube Data API endpoint only for upstream discovery; its call log is baselined before each simulation and production state is compared immediately before and after each simulation. Product-owned selection/evaluation/persistence/UI stay real. The auth token is persisted test state, not a substituted endpoint: no Google/OAuth HTTP is redirected or simulated. No final video, run, status, count, decision, action, or outcome row is directly seeded; scenario inputs still come from fake-provider responses through real discovery and selection.
- State provenance: Provider response travels through real discovery to create the video, and the controlled pre-evaluation failure leaves it pending without direct row insertion. The real dry-run selector consumes that pending row without changing production state; the Backtest selector consumes the retained corpus produced by real discovery with date bounds empty. Each simulation writes only its own requested-run evidence, inspected separately from the actual production run/action evidence produced by E2E-001; no later Start Run re-admits or re-processes the older pending observation, and no re-admission behavior is added.
- Externally meaningful assertion: Dry-run and backtest each select nonzero real inputs; nonzero selected/evaluated counts reflect separate sources, and a gap is not rejection. Each run remains active until stored terminal status. Dry-run records simulated `not_attempted` actions with zero YouTube mutation calls and leaves production corpus/idempotency/statistics unchanged from the post-setup baseline. Backtest makes zero YouTube calls and leaves production state unchanged. Actual production evidence from E2E-001 and hypothetical evidence remain distinct and inspectable; no same-item re-admission, no baseline/new/lost/net comparison, and no playlist-insertion claim appears.
- Failure/recovery: Local mode tests also cover phase error, panic, interruption, empty/unknown selection, and old null counts; simulation failure is not shown as successful completion.
- Human design check: Required—persistent simulation explanation, hypothetical outcomes/actions, completion/failure states, accessible reading order, and mobile presentation against accepted design.
- Executes in: Task 3.1.

## Phases

### Follow-up phase: confirmed repairs and separately gated work

The 2026-11-06 reviewer reconciliation (Loom result `6039ec58-68ee-4483-b190-117616bfce1d`) reports several earlier fixes and tests, but explicitly denies blanket delivery approval. The findings below remain reported until fresh task-level evidence is recorded. Preserve completed evidence; do not rerun unrelated work unless a change invalidates it.

#### F1: Repair route-evaluation SQLite contention [M] → owner: Backend state owner

Target/change surface: `internal/state/machine.go` and state/pipeline regression tests [verified from reviewer source inspection; recheck before edit].
Type: Backend  
Nature: Bug  
Execution fit: One persistence boundary and regression loop; the supplied Run15 reproduction must be read before asserting incident equivalence. Do not touch live data.
Applicable skill: `software-engineering`, `sqlite`, `golang-database`, `golang-testing`
Implements: [Run outcome architecture](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:
- [ ] Read the full supplied Run15 contention reproduction and verify the current failure mode.
- [ ] Regression evidence shows route evaluation survives the accepted SQLite contention case without lost or falsely successful evidence.
- [ ] Focused state/pipeline verification passes; no live DB operation is performed.

#### F2: Close diagnostic reason and log leakage [L] → owner: Security/backend owner

Target/change surface: `internal/diag/`, run serialization and pipeline/replay error logging [verified by reviewer report; recheck current source].
Type: Security  
Nature: Bug  
Execution fit: One confidentiality invariant across durable reason, API and logs. Keep recovery internals intact; do not add bulk redaction or alter queue lifecycle.
Applicable skill: `software-engineering`, `golang-security`, `golang-testing`
Implements: [Run outcome architecture](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:
- [ ] Public and persisted diagnostic reasons use only the architecture-approved safe summaries; raw `err.Error()` chains and unsafe legacy text are not re-emitted.
- [ ] Sentinel tests cover stored reason, JSON/API/event output and failure logs while preserving safe diagnostics.
- [ ] Focused tests pass; historical unsafe text is unavailable rather than rewritten by an unapproved migration.

#### F3: Finalize simulation state after request cancellation [M] → owner: Backend pipeline owner

Target/change surface: `internal/pipeline/dryrun.go`, `replay.go`, and lifecycle tests [verified by reviewer report; recheck].
Type: Backend  
Nature: Bug  
Execution fit: One lifecycle boundary and one cancellation regression; production cancellation evidence is not a substitute for simulation coverage.
Applicable skill: `software-engineering`, `golang-context`, `golang-testing`
Implements: [Run outcome architecture](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:
- [ ] Cancellation/timeout tests establish truthful, durable terminal behavior for dry-run and replay under the accepted lifecycle contract.
- [ ] Canceled request context cannot leave a terminal simulation reported as active or turn successful completion into failure.
- [ ] Focused state/pipeline tests pass.

#### F4: Recover from Decision Detail read failures [S] → owner: Frontend owner

Target/change surface: `web/src/views/DecisionDetailView.tsx` and its tests [reported, verify before change].
Type: Frontend  
Nature: Bug  
Execution fit: One view state and retry boundary; no API/evidence contract change is implied.
Applicable skill: `typescript-async`, `typescript-testing`, `design-implementation`
Implements: [Accepted UI design](../design/yuhaul-admin-ui-design.md)
Acceptance criteria:
- [ ] Failed and missing detail reads leave Loading and present a comprehensible recovery/navigation state.
- [ ] Tests cover initial failure and recovery without changing accepted design meaning.

#### F5: Clarify completed-empty simulation presentation [S] → owner: Frontend owner

Target/change surface: `web/src/views/RunDetailView.tsx` and tests [reported, verify before change].
Type: Frontend  
Nature: Enhancement  
Execution fit: One narrow copy/state distinction; preserve the distinction between completed-empty, unknown, and loading.
Applicable skill: `typescript-testing`, `design-implementation`
Implements: [Accepted UI design](../design/yuhaul-admin-ui-design.md)
Acceptance criteria:
- [ ] A completed run with zero selected inputs is not presented as ongoing or as an unexplained missing-evidence state.
- [ ] Tests distinguish completed-empty from unknown/loading; no new outcome meaning is introduced.

#### F6: Settle decision-context evidence contract [M] → owner: Requirements/specification owner, then architect

Target/change surface: Decision detail API/UI, ARS-001/ARD-001 and decision evidence sources [reported; exact durable fields require inspection].
Type: Documentation  
Nature: Research  
Execution fit: Separate decision from implementation because the designer's suggested amendment is not accepted authority and the answer may change the API/UI scope.
Applicable skill: `behavioral-spec`, `architectural-spec`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:
- [ ] Requirements owner specifies which video/channel context and exclusion evidence are promised and supported by durable records.
- [ ] Architect records the API/evidence realization before any structurally dependent code change.
- [ ] Historical matched-rule explanations are not inferred where evidence was not persisted.

#### F7: Implement accepted decision-context evidence [M] → owner: Backend/frontend owners

Target/change surface: Run-video API and Decision Detail [unverified until F6 settles exact change surface].
Type: Frontend  
Nature: Enhancement  
Execution fit: Depends on the settled evidence contract; one vertical read/presentation outcome, verified through the real API and UI.
Applicable skill: `software-engineering`, `golang-how-to`, `typescript-testing`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:
- [ ] Only agreed, persisted metadata/reasons are displayed; absent historical evidence remains unavailable.
- [ ] API and UI tests prove run scope and do not invent a matched-rule explanation.

#### F8: Diagnose auth status after restart [S] → owner: Research owner

Target/change surface: Auth status and active-token/refresh path [unverified until source review].
Type: Research  
Nature: Research  
Execution fit: Read-only investigation; no credentials or live account required. Do not assume the reported reauthentication workaround is a root cause or fix.
Applicable skill: `code-analysis`
Implements: [System architecture](../architecture/yuhaul-system-architecture.md)
Acceptance criteria:
- [ ] Trace restart status and refresh-token availability from source/tests and sanitized evidence.
- [ ] Report confirmed cause or explicit unknowns and route any behavior decision; no implementation is bundled into discovery.

#### F9: Read-only run14 recovery assessment [M] → owner: Operations/research owner

Target/change surface: Authorized read-only live run/cohort/worker evidence [not inspected in this planning session].
Type: Research  
Nature: Research  
Execution fit: Safety boundary separates observation from any irreversible or quota-consuming operation.
Applicable skill: `product-lifecycle`, `code-analysis`
Implements: [Run14 recovery decision records](../architecture/run14-recovery/decisions/) [directory link; exact record to verify before execution]
Acceptance criteria:
- [ ] Obtain fresh authorized read-only evidence for run state, worker quiescence, cohort ownership, and outstanding work before recommending recovery.
- [ ] Distinguish confirmed facts from unknown provider side effects; do not infer safety from zero action rows.
- [ ] No live mutation, retry, cancellation, deployment, or scheduler change.

#### F10: Decide and, only if authorized, execute run14 recovery [L] → owner: Operations authority

Target/change surface: Live operation [not verified].
Type: Infrastructure  
Nature: Maintenance  
Execution fit: Independent approval and operation boundary; the user's general request to fix follow-ups does not authorize live DB mutation or acceptance of operational risk.
Applicable skill: `product-lifecycle`
Implements: [Run14 recovery decision records](../architecture/run14-recovery/decisions/) [directory link; exact record to verify before execution]
Acceptance criteria:
- [ ] Authorized owner records an explicit recovery policy after F9; no policy is inferred from admission or a proposed ADR.
- [ ] Until authorization, live recovery remains blocked. If authorized, document before/after evidence and verify resulting run/cohort state and side effects.

#### F11: Decide discovery-coordinator expansion and first-run bounds [M] → owner: Product and architecture owners

Target/change surface: Proposed [discovery coordinator design](../architecture/discovery-processing-coordinator/design/ard-001-discovery-processing-budget-coordinator.md) [repository path observed; acceptance status proposed per reviewer].
Type: Documentation  
Nature: Research  
Execution fit: Decision before implementation; do not invent spend thresholds, quotas, scheduling behavior, or enablement.
Applicable skill: `behavioral-spec`, `architectural-design`, `risk-driven-planning`
Implements: Proposed coordinator design above.
Acceptance criteria:
- [ ] Product owner decides whether bounded first-run discovery/coordinator is in scope and states acceptable risk/behavior.
- [ ] Architect realizes accepted behavior before implementation; proposal/critic approval alone is not authorization.
- [ ] If not accepted, keep coordinator and unbounded-first-run remedy outside this diagnostics delivery and disclose the unavailable guarantee.

#### Follow-up dependency order and delivery gate

F1–F5 and F8 can proceed independently of date semantics, subject to their shared source rereads. F2 must finish before final safety composition proof; F3 and F1 must finish before integrated lifecycle/persistence verification. F6 precedes F7. F9 precedes F10, and F10 is blocked absent explicit operational authorization. F11 is a decision/disposition, not an implementation ticket. The earlier E2E scenarios remain required after changes that can invalidate them; rerun only affected scenarios plus a representative final set after integration. `delivery` depends on F1–F8 outcomes and the existing design/E2E/documentation checks; blocked live recovery and unaccepted coordinator/date expansion are not silently marked complete or required for code delivery, but must be disclosed. Preserve and exclude unrelated Settings picker edits.

### Phase 0: Reconcile accepted authority

#### Task 0.1: Align the accepted run-failure authority documents [S] → owner: Architecture documentation owner

Target/change surface: `docs/architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md`, `docs/architecture/adr-002-run-status-failed-terminal-state.md`, `docs/architecture/index.md`, `docs/architecture/log.md` (latter two already edited; reread and preserve).
Type: Documentation
Nature: Documentation
Read surfaces: ARD-001, ADR-002, [System Architecture §Run Recovery](../architecture/yuhaul-system-architecture.md), the supplied specifier follow-up, and current architecture index/log.
Write surfaces: The two status-bearing records and required index/log entries only.
Depends on: None; accepted status meaning is supplied upstream and is not a task decision.
Execution fit: One bounded doc loop over two contradictory status markers and navigation records. It records accepted meaning, not architecture discovery; date meaning is independent/open.
Applicable skill: `okf-authoring`, `documentation`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) and [ADR-002](../architecture/adr-002-run-status-failed-terminal-state.md)
Acceptance criteria:

- [ ] ARD-001 and ADR-002 record accepted status consistently: unrecoverable processing-phase failure is terminal `failed`, resumable interruption is `interrupted`, and normal completion is `completed`.
- [ ] ARD-001 no longer lists run-failure status as unresolved; date semantics remain explicitly open and unchanged.
- [ ] Index/log references and relationships resolve; existing in-progress edits are preserved/reconciled, not overwritten.
- [ ] No date, recovery, or action semantics beyond supplied accepted decisions are introduced.
- [ ] Applicable OKF validation completes or its limitation is reported.

### Phase 1: Make run and action evidence durable and safe

#### Task 1.1: Persist selected/evaluated and action-reason fields [M] → owner: Backend state/API owner

Target/change surface: `internal/store/migrations/`, `internal/state/models.go`, `internal/state/store.go`, `internal/server/server.go`, `internal/server/run_handlers.go`, corresponding Go state/server tests.
Type: Backend
Nature: Feature
Read surfaces: ARD-001 persistence/API sections; ARS-001; current run, executed-action, and requested-run inspection projections.
Write surfaces: One additive migration, state/API projections, focused migration/state/server tests; do not edit applied migrations.
Depends on: None for fields/projections. Date-bound fields are excluded. Tasks 1.2–1.3 depend on these fields.
Execution fit: One storage/API contract contribution: add nullable fields and distinct requested-run read meanings. Selector behavior and action production stay downstream, avoiding all-mode pipeline context here.
Applicable skill: `software-engineering`, `golang-how-to`, `golang-database`, `sqlite`, `sql`, `golang-testing`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] A forward-only additive migration supports nullable selected count and action `outcome_reason`; historical rows remain readable without backfill, and no date fields are added.
- [ ] Run reads expose nullable `videos_selected` and requested-run distinct-video `videos_evaluated`; `videos_discovered` meaning is unchanged. Production `run_video_outcome` capture/projection remains the ARS-006 source for recorded production outcomes; this change adds no duplicate relation and never falls back to mutable global video outcome.
- [ ] Action detail exposes optional `outcome_reason` without a new/renamed outcome category.
- [ ] Zero and unknown selection remain distinct; evaluated count counts distinct videos with decisions for that run, not decision/action rows.
- [ ] Migration/store/API tests cover fresh and pre-change databases, null/zero, multiple decisions per video, requested-run scope, and nullable reason.

#### Task 1.2: Capture selector counts and truthful terminal status [M] → owner: Backend pipeline owner

Target/change surface: `internal/pipeline/pipeline.go`, `internal/pipeline/dryrun.go`, `internal/pipeline/replay.go`, relevant `internal/state/machine.go` calls, pipeline/state tests.
Type: Backend
Nature: Enhancement
Read surfaces: ARD-001 mode/failure rules; Task 0.1 record; Task 1.1 state APIs; production/dry-run/replay/backtest selectors.
Write surfaces: Pipeline selection/failure handling and targeted tests.
Depends on: Tasks 0.1 and 1.1.
Execution fit: One pipeline reasoning horizon: capture each existing selector's actual set and prevent failure from flowing into completion. Mode-local tests prove boundaries without changing routing/UI.
Applicable skill: `software-engineering`, `golang-how-to`, `golang-context`, `golang-testing`, `test-driven-development`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) and [ADR-002](../architecture/adr-002-run-status-failed-terminal-state.md)
Acceptance criteria:

- [ ] Count is set once when each real selector establishes its inputs: production run-owned work (including retries/discoveries), pending dry-run inputs, and retained replay/backtest inputs. Backtest/Replay use all retained observations until date meaning is settled.
- [ ] Completed empty selection is zero; failure before selection remains unknown; a known selection count survives mid-run failure; historical counts stay null.
- [ ] Failed phase/panic paths for production, dry-run, replay, and backtest end `failed`; resumable interruption remains `interrupted`; successful evaluation alone ends `completed`; failed runs are not auto-resumed.
- [ ] Run status is not a video outcome; absent decision evidence is not rejection or a fabricated per-video result.
- [ ] Existing production `videos_discovered`, route order/match/exclusion, ConfigVersion, and simulation isolation remain unchanged; tests cover empty, partial, and complete selections.

#### Task 1.3: Persist per-action outcomes and no-attempt reasons [M] → owner: Backend action-execution owner

Target/change surface: `internal/pipeline/executor.go`, action portion of `internal/pipeline/pipeline.go`, `internal/state/store.go` action writes, `internal/stats/store.go` route-stat projection, and executor/pipeline/state tests plus `internal/stats/stats_test.go`. Reread Task 1.2 changes before editing shared pipeline code.
Type: Backend
Nature: Feature
Read surfaces: ARD-001 action rules; BR-012/014/020; [Run-Route Statistics](../requirements/yuhaul/run-route-statistics.md); current `executed_action` writes/reads, `ActionsExecuted` projection, and Error Queue ownership.
Write surfaces: Action-result persistence, route-stat attempt projection, and focused action/stats tests; no queue lifecycle redesign.
Depends on: Tasks 1.1 and 1.2. Sequential after 1.2 also prevents competing pipeline edits.
Execution fit: One action-outcome boundary from executor response through durable requested-run evidence and its existing route-stat consumer. The stats correction is tightly coupled: writing deferred/not-attempted rows must not redefine actual `ActionsExecuted` attempts.
Applicable skill: `software-engineering`, `golang-how-to`, `golang-database`, `golang-testing`, `test-driven-development`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] Known success, provider failure, quota deferral (`quota_exhausted`), later action skipped after prior failure (`prior_action_failed`), simulation `not_attempted`, and prior exact-target production completion (`already_completed`) have requested-run evidence using existing action outcomes only.
- [ ] Prior completed target is `not_attempted` with `already_completed`, never current-run success/timestamp; proactive deferral/simulation have no external-attempt timestamp; `executed_at` exists only for an actual attempted action.
- [ ] Failed/deferred video work retains existing Error Queue lifecycle; run failure creates no synthetic queue item.
- [ ] Duplicate configured targets do not create repeated action evidence/counts; absent durable outcome remains unconfirmed.
- [ ] `ActionsExecuted` counts successful action attempts and failed actions only when an external action was actually attempted; deferred and not_attempted rows never count.
- [ ] Focused `internal/stats` route-statistics tests prove succeeded and actual attempted failed actions count as `ActionsExecuted`, deferred and not_attempted rows do not, and simulation isolation remains true.
- [ ] Tests through the existing provider seam prove observed mutation attempts agree with persisted action outcomes.

#### Task 1.4: Enforce the safe diagnostic boundary [L] → owner: Security/backend owner

Target/change surface: `internal/youtube/client.go`, `internal/pipeline/{pipeline,dryrun,replay,executor}.go`, `internal/queue/`, `internal/server/{server,run_handlers}.go`, associated tests. Reread Tasks 1.2–1.3 before touching shared paths.
Type: Security
Nature: Enhancement
Read surfaces: ARD-001 safety sections; provider `APIError`, queue detail, run/action reasons, JSON, SSE, and logger paths.
Write surfaces: Safe diagnostic production/projections and security tests; no historical redaction migration or queue redesign.
Depends on: Tasks 1.2 and 1.3.
Execution fit: One security boundary spans provider errors through persistence, API/events, and logs. Splitting by file would leave unsafe neighboring paths; the broad surface is justified by one confidentiality invariant and one sentinel proof boundary.
Applicable skill: `software-engineering`, `golang-security`, `golang-how-to`, `golang-testing`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] New run/action/queue diagnostics contain only bounded safe fields (phase/operation, known HTTP status, vetted provider reason); raw bodies/chains, credentials, authorization data, URL/query data, and transient provider metadata do not enter diagnostics or logs.
- [ ] Public Run/action/queue/event projections suppress unsafe legacy free text; unrecoverable legacy reason is unavailable, not raw.
- [ ] Internal recovery reason and video queue lifecycle are preserved; no run-error queue item or bulk redaction is added.
- [ ] Sentinel tests prove absence from new persistence, API JSON, event output, browser-visible data, and logs while safe fields remain.

#### Task 1.5: Build a real pipeline-to-HTTP composition test [L] → owner: Backend integration-test implementer

Target/change surface: Existing Go integration-test surfaces under `e2e/` and/or `internal/server/` [exact new test file chosen by owner]; real pipeline, migrated SQLite, registered HTTP routes, and response projections.
Type: Testing
Nature: Feature
Read surfaces: ARD-001 verification matrix; Tasks 1.1–1.4; current pipeline selectors, SQLite migration/store, registered run/video HTTP handlers, and fake-YouTube base-URL seam.
Write surfaces: Bounded integration-test suite and test setup only. Do not alter product implementation in this task; failures return to the responsible implementation owner.
Depends on: Tasks 1.1, 1.2, 1.3, and 1.4.
Execution fit: The missing proof is one vertical boundary, not a broader API rewrite: a real selector/pipeline writes durable state and registered HTTP routes read that same state. One test suite may have multiple cases for zero/partial/action outcomes while sharing the same composition harness. Independent verification is a separate, smaller task.
Applicable skill: `golang-testing`, `product-acceptance`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] Execute the real pipeline through its real selector and persist through the real migrated SQLite store. Do not seed final run status, selected count, route decisions, action rows, or outcomes.
- [ ] Read pipeline-produced records through registered HTTP routes for run summary, run-video list, and video detail; direct store reads/direct handler calls are not the claimed API proof.
- [ ] Verify selected, evaluated, and discovered fields against their distinct producer/query sources, plus stored status and action projection. Cover zero, unknown, partial-evaluation failure, and legacy-null where the existing real selectors/migration permit them; use a non-equal count case when a real failure boundary permits it.
- [ ] Cover representative real action outcomes through API readback, including successful/attempted failure, deferred, simulated not_attempted, and prior-success suppression without false success.
- [ ] Reopen the same migrated SQLite database before HTTP readback if the existing harness supports it; if infeasible, record why while still exercising the real registered HTTP endpoint.
- [ ] Unsafe sentinel content is absent from API output. Only YouTube is substituted; no product-owned selector, pipeline, store, serializer, or HTTP handler is mocked. Simulation isolation remains intact.

#### Task 1.6: Independently verify producer-to-HTTP composition [S] → owner: Independent reviewer

Target/change surface: Task 1.5 integration test, real pipeline/SQLite/HTTP path, and its execution evidence.
Type: Testing
Nature: Maintenance
Read surfaces: ARD-001 composition obligations; reviewer concern `984e5ea6-23ba-4f28-97da-9fe8440e6f38`; Task 1.5 test and supporting source.
Write surfaces: Independent verification findings/evidence only; fixes return to Task 1.5 or the relevant implementation owner.
Depends on: Task 1.5.
Execution fit: Separate construction from independent proof because prior green tests exercised producers and APIs independently but did not combine them. The reviewer verifies and executes this one composition boundary before UI work relies on it.
Applicable skill: `product-acceptance`, `golang-testing`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] Independently execute and inspect the suite: actual selector/pipeline writes through the real migration/store, and registered HTTP routes read that same producer-created state; direct store reads or seeded API rows do not substitute for the HTTP boundary.
- [ ] Verify the composed selected/evaluated/discovered values and failed status in HTTP JSON, plus action/detail serialization and safe diagnostics; confirm database reopen/readback or record its bounded limitation.
- [ ] Confirm no final run status/count/decision/action/outcome rows are hand-seeded; only YouTube is substituted, and simulation isolation assertions remain real.
- [ ] Record PASS only with executed composition evidence; otherwise mark UNPROVEN and return the gap to its owner. Frontend work remains dependent on this review result.

### Phase 2: Expose the evidence in the shipped experience

#### Task 2.1: Show truthful status, counts, and complete run inspection [M] → owner: Frontend run-inspection owner

Target/change surface: `web/src/views/RunsView.tsx` and test; `web/src/views/RunDetailView.tsx` and test; `web/src/api/runs.ts`, `web/src/api/run-videos.ts`, `web/src/types.ts`.
Type: Frontend
Nature: Enhancement
Read surfaces: Accepted Run Detail/Simulation Inspection design; ARD-001 client rules; ARS-001 pagination; Task 1.6 independently verified API composition evidence.
Write surfaces: Run list/detail types, refresh/pagination behavior, and view tests.
Depends on: Task 1.6.
Execution fit: One run-inspection screen family owns lifecycle, run summary, and requested-run video-list completeness through the same Run response/read boundary. Decision Detail presentation is separate.
Applicable skill: `software-engineering`, `typescript-common-practice`, `typescript-async`, `typescript-testing`, `accessibility-implementation`, `design-implementation`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) and [ARS-001](../architecture/ars-001-route-and-playlist-api-contracts.md)
Acceptance criteria:

- [ ] Start acknowledgement is not completion. While stored status is running, UI refreshes requested-run state; terminal presentation follows stored `completed`/`failed`/`interrupted`. Failed status read leaves completion unknown and gives a visible retry state.
- [ ] Run List/Detail recognizes `failed`; Run Detail shows safe run-level failure independently of the Errors queue.
- [ ] Counts use the new fields; null selected count is unavailable, not zero; simulation does not reuse production `videos_discovered` as selected count.
- [ ] Run Detail exposes later pages and preserves outcome filters/filtered totals; more than 100 videos are inspectable.
- [ ] Existing run type, decision, outcome, and simulation interpretation remain mode-correct; missing evidence is never rejection.
- [ ] UI tests cover running/terminal/failure/interruption, status-read failure, legacy null, zero selection, filters, and paging; accepted design/accessibility constraints remain satisfied.

#### Task 2.2: Present run-scoped action outcomes without false success [M] → owner: Frontend decision-detail owner

Target/change surface: `web/src/views/DecisionDetailView.tsx` and test; `web/src/types.ts`; existing run-video detail client/adapter.
Type: Frontend
Nature: Enhancement
Read surfaces: ARD-001 action rules; accepted Run/Decision Detail and Simulation Inspection design; Task 1.6 verified API responses.
Write surfaces: Action detail types and view/tests only; reread Task 2.1's shared `types.ts` changes first.
Depends on: Tasks 2.1 and 1.6.
Execution fit: One video-detail outcome layer displays durable requested-run action evidence. Separate from run lifecycle/pagination because it proves action interpretation, including hypothetical labels.
Applicable skill: `software-engineering`, `typescript-common-practice`, `typescript-type-safety`, `typescript-testing`, `accessibility-implementation`, `design-implementation`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) and [YuHaul Admin Interface Design](../design/yuhaul-admin-ui-design.md)
Acceptance criteria:

- [ ] Production rows show requested-run succeeded/failed/deferred/not-attempted and safe reason; `already_completed` means not attempted in this run, never current-run success.
- [ ] Simulation rows remain hypothetical/not attempted and never imply playlist mutation or execution success.
- [ ] Empty evidence and unavailable legacy reason are neutral/unavailable, not inferred as rejection/failure/success.
- [ ] Tests cover existing outcomes, prior-completion reason, safe/unavailable reason, empty action set, and production/simulation copy.

#### Task 2.3: Make Backtest/Replay results reflect the requested run [M] → owner: Frontend simulation-flow owner

Target/change surface: `web/src/views/BacktestView.tsx` and `web/src/views/BacktestView.test.tsx`.
Type: Frontend
Nature: Enhancement
Read surfaces: ARD-001 Backtest/completion decision; BR-027 first-use scope; accepted Backtest/Simulation Inspection design; Task 2.1 Run Detail state.
Write surfaces: Backtest/Replay start/result flow and view tests; existing date controls are not wired or redefined.
Depends on: Tasks 2.1 and 1.6.
Execution fit: One distinct user path with two modes sharing a start/results surface. It removes unsupported comparison and false start-completion; detailed lifecycle/evidence stay owned by Run Detail.
Applicable skill: `software-engineering`, `typescript-common-practice`, `typescript-async`, `typescript-testing`, `accessibility-implementation`, `design-implementation`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) and [BR-027](../requirements/yuhaul/br-027-historical-backtesting.md)
Acceptance criteria:

- [ ] `202`/start is not presented as completion; requested persisted status determines terminal result, and the run ID remains available for inspection.
- [ ] Aggregate comparison against unrelated production totals is removed; no first-use baseline/new/lost/net claim is shown.
- [ ] Completion means evaluation finished, not playlist action success.
- [ ] User can reach requested Run Detail for status, counts, filtering, and action detail.
- [ ] Tests cover Backtest and Replay start, active/terminal/failure state, status-read failure, and no fabricated comparison.
- [ ] No timestamp/timezone/bound rule is introduced; date filter remains blocked and is not claimed complete.

#### Task 2.4: Build the shipped SPA in real-client mode [S] → owner: Container/build owner

Target/change surface: `Dockerfile` and directly necessary build-stage configuration.
Type: Infrastructure
Nature: Enhancement
Read surfaces: ARD-001 shipped SPA decision; current Dockerfile build stage and Vite mock default.
Write surfaces: Production image SPA build configuration and its build verification.
Depends on: Tasks 2.1–2.3 so the artifact includes final UI changes.
Execution fit: One isolated shipping-fidelity change at the static SPA build boundary; no runtime-only variable imitates Vite's build-time setting.
Applicable skill: `software-engineering`, `documentation`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] Production image SPA bundle is compiled with `VITE_USE_MOCK=false` at build time.
- [ ] Built artifact uses the real application API/event path for run/status/detail/progress, not mock SSE.
- [ ] Verification distinguishes build-time bundle configuration from runtime container environment; runtime env alone is not accepted.
- [ ] Local-development mock defaults are unchanged unless directly required by the accepted build contract.

### Phase 3: Prove the product and record realization

#### Task design-check: Validate run-diagnostics UI against accepted design [M] → owner: Designer / design-validation owner

Target/change surface: Implemented UI under inspection: `web/src/views/RunsView.tsx`, `web/src/views/RunDetailView.tsx`, `web/src/views/DecisionDetailView.tsx`, `web/src/views/BacktestView.tsx`; design-validation findings/evidence records only.
Type: Design validation
Nature: Maintenance
Read surfaces: Accepted [YuHaul Admin Interface Design](../design/yuhaul-admin-ui-design.md) (especially Run Detail and Simulation Inspection); [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md); the real implemented Task 2.1–2.4 UI and its observed build/environment and viewport/input conditions.
Write surfaces: Evidence-backed design-validation findings only; no product source, design, or requirement edits. Clear implementation drift is returned to the owning Task 2.x.
Depends on: Tasks 2.1, 2.2, 2.3, and 2.4 so the implemented surface is final. Date-range controls and their semantics stay outside this review.
Execution fit: One designer-owned validation loop over the four implemented run-diagnostics views against the accepted design, before assembled-product acceptance in Task 3.1. Validation only: no redesign, no product edit, and no new accepted requirement from heuristic findings; it is separate from Task 3.1's real assembled-product proof.
Applicable skill: `design-validation`, `accessibility-implementation`
Implements: [YuHaul Admin Interface Design](../design/yuhaul-admin-ui-design.md) and [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] Observe the actual implemented UI (not a prototype/mock substitute) for Run List/Detail, Decision Detail, and Backtest/Replay; compare each load-bearing state against the accepted design and ARD-001, recording the observed build/environment and relevant viewport/input conditions.
- [ ] Verify that running, persisted `completed`/`failed`/`interrupted`, and unavailable/unknown status are distinguishable; selected/evaluated/discovered and null-versus-zero counts are not conflated; missing evidence is not presented as rejection.
- [ ] Verify that actual production attempts, no-attempt/idempotency suppression, and simulated `not_attempted` actions are clearly distinguished; simulation completion does not imply playlist mutation; unsafe provider or legacy detail is not exposed; no deferred comparison is implied.
- [ ] Check accepted keyboard/focus behavior, semantic reading order, screen-reader/status announcements, text-not-color-only distinctions, and responsive constraints including 320 CSS px and 200% zoom where the accepted design specifies them.
- [ ] Record evidence-backed findings with accepted-intent reference, observed behavior, human consequence, severity, and owner; return clear implementation drift to the relevant Task 2.x. Do not change the design or product behavior; raise a design question only when the accepted design genuinely cannot settle a material issue.
- [ ] Keep date-range controls and their meaning outside this review. Record unobserved conditions and limitations; this validation is not a substitute for Task 3.1 end-to-end evidence.

#### Task 3.1: Prove run-diagnostics journeys through the shipped product [L] → owner: Product-acceptance owner

Target/change surface: Built container/SPA, real server/SQLite/run APIs/pipeline, browser evidence [exact browser harness is not established by `web/package.json`; owner confirms available harness without substituting component tests].
Type: Testing
Nature: Feature
Read surfaces: Three E2E scenario contracts above; Task 1.6, Tasks 2.1–2.4, and the Task `design-check` findings; accepted UI design and run/config/API contracts.
Write surfaces: Acceptance evidence/browser artifacts and tests only where an existing suite owns them. Product fixes return to implementation owner.
Depends on: Tasks 1.6, 2.1, 2.2, 2.3, and 2.4, plus Task `design-check`.
Execution fit: One final acceptance loop reuses the shipped artifact, app/API, database, and external provider setup to prove production-action, run-failure, and simulation journeys. The broad path is justified by one assembled-product outcome.
Applicable skill: `product-acceptance`, `design-validation`, `accessibility-implementation`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] E2E-001–003 execute against the real production-built SPA, API/pipeline/SQLite, with only the stated external YouTube substitution.
- [ ] Route/configuration and input state have upstream provenance through real UI/API/product behavior; no directly seeded final decision/action/status row stands in for a producer.
- [ ] Browser visibly distinguishes production action result, run-level failure with empty queue, and completed dry-run/backtest with selected/evaluated evidence.
- [ ] Provider logs prove zero dry-run mutation calls and zero backtest YouTube calls; production calls agree with displayed evidence.
- [ ] Sentinel body/token is absent from persistence/API/events/browser/logs; no unsupported comparison is shown.
- [ ] Human status/action/simulation wording, keyboard/screen-reader interpretation, and responsive presentation are checked against accepted design; evidence and gaps are recorded.
- [ ] Each claim is PASS only with execution evidence and provenance; otherwise FAIL or UNPROVEN with its gap. No local test is promoted to product acceptance.
- [ ] The designer-owned Task `design-check` is completed and its findings are dispositioned before product-acceptance claims; design conformance alone does not substitute for real browser/API/pipeline/SQLite proof.

#### Task 3.2: Record implementation evidence and update architecture navigation [S] → owner: Documentation integration owner

Target/change surface: ARD-001, `docs/architecture/index.md`, `docs/architecture/log.md`, and directly affected relationships.
Type: Documentation
Nature: Documentation
Read surfaces: Accepted ARD-001/ADR-002; results from Tasks 1.1–3.1; current architecture index/log edits.
Write surfaces: Realization/verification status and necessary OKF index/log/relationship entries only; do not rewrite requirements to fit implementation.
Depends on: Task 3.1 so documentation reports executed evidence, not plans or local tests.
Execution fit: One final documentation integration loop consolidates implementation status after all owners finish, preventing concurrent edits and keeping architecture claims honest.
Applicable skill: `okf-authoring`, `documentation`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] ARD-001 distinguishes implemented, verified, unverified, and blocked obligations; only executed product proof is reported as pass.
- [ ] Date semantics, comparison deferral, count/membership limit, and provider-success/local-record uncertainty remain explicit.
- [ ] Index/log/relationships preserve existing work and resolve; applicable OKF validation completes or reports its limitation.
- [ ] No requirement is rewritten to hide a missing guarantee or failed/unproven scenario.

### Phase 4: Deliver the scoped change

#### Task delivery: Commit, push, and open the scoped PR [M] → owner: Delivery owner (native role: general/coordination)

Target/change surface: The final Action-owned diff on the current branch, including implementation, tests, accepted documentation, and this plan only where those paths are in scope. `web/src/views/SettingsView.tsx` and `web/src/views/SettingsView.test.tsx` are pre-existing picker changes and must remain untouched/uncommitted unless the user separately authorizes them.
Type: Repository Management
Nature: Maintenance
Read surfaces: Accepted task results and evidence; current branch, worktree diff, and repository PR conventions.
Write surfaces: Exact scoped file/diff selection, one scoped commit, current-branch push, and created PR. No implementation changes are authorized by this task.
Depends on: Task 3.2 (and transitively Tasks 1.1–3.1). If the date meaning and implementation are resolved and accepted within this Action, Task `date-e2e` is also a required prerequisite; if not, the date branch stays explicitly blocked and is disclosed in the PR.
Execution fit: One final delivery boundary after the integrated code, verification, and docs are complete. File ownership/diff review is essential because the worktree already contains unrelated Settings edits and shared architecture documentation changes.
Applicable skill: `git-commit-discipline`, `pr-educational-body`
Implements: [ARD-001](../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md)
Acceptance criteria:

- [ ] Required implementation, local/vertical/product verification, and documentation tasks are complete with evidence; date E2E is required only if the user resolved and accepted date work.
- [ ] An exact list of paths/diffs to include is recorded and each is classified as Action-scoped. Existing Settings picker edits and any unrelated/pre-existing changes are excluded and preserved. If a shared-file diff cannot be scoped safely, stop and report the blocker.
- [ ] One commit contains only the reviewed Action-scoped changes; its identity and included paths are recorded.
- [ ] The current branch is pushed and the remote branch reflects the commit.
- [ ] A PR is created and its URL recorded; its description states motivation, implementation scope, executed verification evidence/limits, and any still-blocked date-picker request without claiming it was implemented.
- [ ] If branch/remote/auth/PR capability prevents delivery, report the exact blocked step and do not claim completion.

### Blocked dependent branch: date-picker request

These nodes preserve the separately authorized picker/filter request; they are blocked, not removed, and are not on the core diagnostics delivery path while the user decision is unresolved.

#### Task date-meaning: Set date-range and interim control semantics [S] → owner: User / product requirements owner, with design owner for control presentation

Outcome: Accepted meaning for the retained-observation date range and for the currently visible-but-unwired controls. No agent or plan task chooses a default.
Depends on: User-owned decision; no diagnostics implementation dependency.
Criteria:

- [ ] Define timestamp source, date/timezone interpretation, endpoint inclusion, and omitted/partial/reversed range behavior.
- [ ] Decide what the existing date controls communicate until filtering is functional; separate UI affordance from actual filtering.
- [ ] Do not infer `discovered_at`, UTC, or inclusive endpoints absent authorization.

#### Task date-contract: Realize accepted date selection [M] → owner: Architect

Outcome: Architecture/API contract for the user-approved date selection and persisted run-owned bounds.
Depends on: Task `date-meaning`.
Criteria:

- [ ] ARD/API contract records the accepted bounds and selector provenance without changing Run/config ownership.
- [ ] Contract distinguishes control presentation from actual backend filtering and specifies boundary tests before implementation.

#### Task date-backend: Apply accepted date bounds [M] → owner: Backend implementation owner

Outcome: Backtest/Replay selection and persisted run evidence follow the approved contract.
Depends on: Tasks `date-contract`, 1.1, and 1.2.
Criteria:

- [ ] Only approved in-range observations are selected; selected count/bounds match the actual selector input.
- [ ] Empty/partial/reversed ranges and endpoints match the approved contract; Backtest remains no-YouTube-call and production-isolated.

#### Task date-ui: Implement date-picker presentation and request flow [M] → owner: Frontend implementation owner

Outcome: Controls communicate approved meaning and send only the range the backend applies.
Depends on: Tasks `date-contract`, `date-backend`, 2.3, and 1.6.
Criteria:

- [ ] Control presentation and copy match accepted date semantics and interim/final state.
- [ ] No silent range default, UI/API mismatch, or implication that unfiltered results are filtered.

#### Task date-e2e: Prove date selection through real UI and API [M] → owner: Product-acceptance owner

Outcome: Real browser selection creates the expected retained-input set and durable run evidence.
Depends on: Tasks `date-backend`, `date-ui`, 2.4, and 1.6.
Criteria:

- [ ] Boundary/empty/partial/reversed cases execute with upstream-produced retained observations through the real SPA/API/pipeline/SQLite path.
- [ ] Displayed range, request values, persisted bounds, selected count, and evaluated inputs agree.
- [ ] This proof is required before delivery only if the user resolves and accepts this branch; otherwise report it as blocked.

## Dependency and ownership graph

Each task appears once. `1.2 → 1.3` is an ordered write-surface handoff because both touch the pipeline action path; it is not a semantic dependency of action outcomes on selection counts. Task 1.5 constructs the real vertical test; Task 1.6 independently proves it. Frontend and final product-proof work depend on the reviewer result.

```text
0.1, 1.1 -> 1.2 -> 1.3 -> 1.4
1.1, 1.2, 1.3, 1.4 -> 1.5 -> 1.6 -> 2.1
2.1, 1.6 -> 2.2
2.1, 1.6 -> 2.3
2.1, 2.2, 2.3 -> 2.4 -> design-check -> 3.1 -> 3.2 -> delivery

date-meaning -> date-contract -> date-backend -> date-ui -> date-e2e
                              ^ 1.1, 1.2       ^ 2.3, 1.6      ^ 2.4, 1.6
date-e2e -> delivery only if date work is resolved and accepted
```

Task 1.4 also depends on 1.2/1.3; Task 1.5 on 1.1–1.4; Task 1.6 on 1.5; Task 2.1 on 1.6; Tasks 2.2/2.3 on 2.1 and 1.6; Task 2.4 on 2.1–2.3; Task `design-check` on 2.1–2.4; Task 3.1 on 1.6, 2.1–2.4, and `design-check`; Task 3.2 on 3.1; `delivery` on 3.2 and conditionally date-e2e if date work is accepted. The date branch does not delay core diagnostics while undecided.

### Ownership and write-surface boundaries

| Task | Primary owner | Main write surface | Parallelism constraint |
|---|---|---|---|
| 0.1 | Architecture documentation owner | ARD-001, ADR-002, architecture index/log | Preserve in-progress edits; no parallel architecture-doc edits. |
| 1.1 | Backend state/API owner | Migration, state models/store, run/action API | Foundation; finish before producer work. |
| 1.2 | Backend pipeline owner | Production/dry-run/replay pipeline and status | Finish before action task touches shared pipeline path. |
| 1.3 | Backend action-execution owner | Executor/action persistence/stats projection | Sequential after 1.2; owns attempt-only route stats. |
| 1.4 | Security/backend owner | Diagnostic persistence, API/event/log projections | After 1.2–1.3; secures their complete outputs. |
| 1.5 | Backend integration-test implementer | Real pipeline→SQLite→HTTP test | Test construction only; no product implementation. |
| 1.6 | Independent reviewer | Review/execution of Task 1.5 proof | Must independently verify real provenance and registered HTTP path before UI. |
| 2.1 | Frontend run-inspection owner | RunsView/RunDetail, run API/types/tests | Depends on Task 1.6; owns shared types before 2.2. |
| 2.2 | Frontend decision-detail owner | DecisionDetail/action types/tests | After 2.1 due shared `types.ts`. |
| 2.3 | Frontend simulation-flow owner | BacktestView/tests | Disjoint from 2.2; may run alongside it. |
| 2.4 | Container/build owner | Dockerfile build stage | After UI tasks to include their final source. |
| design-check | Designer / design-validation owner | Design-validation findings/evidence only | After Task 2.4; no product/design edits; clear drift returns to the owning Task 2.x. |
| 3.1 | Product-acceptance owner | Browser proof/evidence | Depends on Task 1.6 and design-check findings; no product-owned mocks. |
| 3.2 | Documentation integration owner | ARD-001, architecture index/log | After evidence; sole final realization-status editor. |
| delivery | Delivery owner | Scoped Git commit, current-branch push, PR | After implementation/verification/docs; excludes existing Settings edits. |
| date-meaning → date-e2e | User/requirements/design → architect/backend/frontend/acceptance owners | Date semantics, contract, selector/UI, proof | Entire branch blocked until user meaning is accepted; does not block core diagnostics. |

## Execution waves

| Wave | Tasks | Why safe |
|---|---|---|
| 1 | 0.1, 1.1 | Doc authority alignment and additive backend schema/API work are disjoint; 1.1 chooses neither status nor date meaning. |
| 2 | 1.2 | Requires durable fields and reconciled status record. |
| 3 | 1.3 | Ordered to avoid concurrent pipeline action-path edits. |
| 4 | 1.4 | Secures complete producer/output paths after status/action evidence exists. |
| 5 | 1.5 | Build the real producer→SQLite→HTTP integration test after backend contracts are real. |
| 6 | 1.6 | Independent reviewer proves the test before any frontend reliance. |
| 7 | 2.1 | UI begins from independently verified persisted API behavior. |
| 8 | 2.2, 2.3 | Decision Detail and Backtest/Replay files/tests are disjoint; shared types are stable. |
| 9 | 2.4 | Build final UI source into shipped real-client artifact. |
| 10 | design-check | Designer validates the implemented UI against the accepted design before assembled-product acceptance. |
| 11 | 3.1 | Final browser acceptance after components, build settings, and design validation are real. |
| 12 | 3.2 | Record observed evidence and synchronize docs before delivery. |
| 13 | delivery | Commit/push/create PR only after core verification and docs; include date-e2e if date work is accepted. |

No wave contains graph-ordered tasks or tasks with overlapping mutable source/test surfaces. The date branch is not scheduled in the core waves while blocked; if accepted, its dependency chain must finish before delivery.

## Open questions

| Question | Owner | Blocks | Safe independent work |
|---|---|---|---|
| For Backtest/Replay date inputs, which retained observation timestamp is selected; UTC or local date interpretation; endpoint inclusion; omitted/reversed-bound meaning; and what should the existing visible controls communicate until the filter is implemented? | Product/requirements owner for observable selection, with design owner for control meaning; user resolves if ownership is not delegated. | Filter/query contract, date-bound persistence, date UI claims, and exact range tests. Do not adopt `discovered_at`/UTC/inclusive by default. | Durable diagnostics, blank-bound all-retained evaluation, selected/evaluated counts, status UI, simulation-isolation proof. |

Run-failure meaning is not an open question: accepted System Architecture §Run Recovery and supplied follow-up resolve it. The repository status mismatch is documentation reconciliation in Task 0.1, not an approval gate.

## Plan checks

1. **Authority:** 17 source groups are listed. The only conflicting repository markers concern already accepted status authority; Task 0.1 reconciles them. Date semantics remain unresolved and block only dependent scope.
2. **Obligations:** 21 rows have explicit dispositions, owners/satisfiers, and verification paths. Date selection is blocked; comparison, membership ledger, retry/search, provider reconciliation, and broad OC-001 accounting have named dispositions and weakened guarantees.
3. **Open decisions:** Only date-range semantics remain open. No behavior decision is hidden in an implementation task; Task 0.1 records, but does not make, the accepted status rule.
4. **Order:** Schema/projection precedes producers; producers precede security/vertical proof; verified API precedes UI; final build precedes design validation and browser proof; docs report evidence before the final delivery task.
5. **Graph/waves:** 20 tasks appear once: 15 core tasks including delivery and 5 date-branch tasks explicitly blocked. The core graph is acyclic with no dangling dependencies; 13 core waves preserve test-construction/review, pipeline/type/build/documentation boundaries. The date branch is conditional and does not gate core delivery while undecided.
6. **Mechanisms:** Nullable counts/reasons, status vocabulary, safe diagnostics, persisted reads, pagination, build-time mock setting, and attempt-only `ActionsExecuted` projection trace to ARD-001/ADR-002/ARS-001 or accepted requirements/design. No date meaning, ledger, new outcome, or provider-reconciliation mechanism is invented.
7. **Grounding/artifact validation:** Current paths/observations were read directly; working-tree Settings and architecture changes are protected. Relative document links resolve. `validate_doc` reports zero errors and one W3 warning (`unknown type "plan"`); the required plan frontmatter is retained, and no `docs/plans` bundle/index exists in the current corpus, so no extra index/profile artifact was invented. No software tests are claimed as run.
8. **Execution fit:** Each task has one outcome, owner, read/write surface, verification boundary, and fit rationale. Shared pipeline/types/docs writes are ordered; frontend work groups by evidence layer.
9. **Vertical proof:** Task 1.5 constructs the real producer→SQLite→HTTP test; independent Task 1.6 reviews/executes it before Task 2.1 or Task 3.1 depends on that evidence.
10. **End-to-end:** Three scenarios cover production action result, run-level failure with empty queue, and dry-run/backtest completion/isolation with real product-owned components, provenance, bounded external substitution, assertions, and human design checks. Run/Decision Detail checks preserve the existing requested-run route/condition evidence and actual-vs-hypothetical distinction.
11. **No unsupported passes:** This is a plan only; commit, push, and PR are future delivery obligations, not actions performed here. Future proofs are pending; architecture text, local tests, and this plan are not product acceptance evidence.
