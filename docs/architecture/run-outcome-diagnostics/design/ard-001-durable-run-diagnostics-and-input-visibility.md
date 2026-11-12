---
type: design
title: ARD-001 — Durable Run Diagnostics and Simulation Input Visibility
description: Reuse persisted run and decision evidence to show truthful run completion, selected and evaluated counts, action outcomes, and safe diagnostic detail across production and simulation modes.
tags: [architecture, design, run-history, diagnostics, simulation, safety]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — the bounded run-inspection realization has local composition and representative real-browser evidence below, but diagnostic safety is **not complete**. Source review on 2026-11-06 confirms arbitrary printable chains can pass vetting and earlier logs can emit raw detail. [ARS-001](../specifications/ars-001-safe-diagnostic-contract.md) decides the exact producer/write/read/log contract; implementation and fresh verification remain outstanding. This is not full-contract, container/deployment, live-Google, or accessibility certification. The Backtest/Replay date-range meaning was decided by the user on 2026-11-10 ([BR-027 AC8–13](../../../requirements/yuhaul/br-027-historical-backtesting.md)), and its realization is designed in [run-initiation ADR-002/ARS-001](../../run-initiation/index.md); no functional date filter is implemented or verified yet.

The UI reads stored run status to distinguish running, completed, failed, and interrupted work. Run inspection shows the captured selected-input count, the distinct count of videos with route decisions, the route/condition/action evidence that exists, and a vetted diagnostic when available. A start response is not completion. Simulations remain hypothetical: an action marked `not_attempted` is never presented as a playlist insertion.

For example, a backtest can show “14 selected, 13 with route decisions, failed during routing” and link to the 13 videos for which evidence exists. It must not turn the one missing decision into a rejection or call the run completed. A successful simulation says only that evaluation finished; it does not promise that a production run will add any video. A selected-versus-evaluated count is an aggregate, not a new per-video membership history for inputs that never acquired decision evidence.

## Derived from

- [BR-026 — Dry-Run Evaluation](../../../requirements/yuhaul/br-026-dry-run-evaluation.md)
- [BR-027 — Historical Backtesting](../../../requirements/yuhaul/br-027-historical-backtesting.md)
- [BR-028 — Historical Run Inspection](../../../requirements/yuhaul/br-028-historical-run-inspection.md)
- [BR-015 — Explainable Route Decisions](../../../requirements/yuhaul/br-015-explainable-route-decisions.md)
- [BR-012 — Idempotent Playlist Actions](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md)
- [BR-014 — Failed Work Retention and Retry](../../../requirements/yuhaul/br-014-failed-work-retention-and-retry.md)
- [BR-020 — Run-Level and Route-Level Statistics](../../../requirements/yuhaul/br-020-run-level-and-route-level-statistics.md)
- [BR-032 — Processing Outcome Distinction](../../../requirements/yuhaul/br-032-processing-outcome-distinction.md)
- [BR-011 — Processing State Persistence](../../../requirements/yuhaul/br-011-processing-state-persistence.md)
- [BR-017 — Manual Run Initiation](../../../requirements/yuhaul/br-017-manual-run-initiation.md)
- [BR-025 — Configuration History for Statistics Interpretation](../../../requirements/yuhaul/br-025-configuration-history-for-statistics-interpretation.md)
- [Run-Level and Route-Level Statistics](../../../requirements/yuhaul/run-route-statistics.md)
- [Error Queue](../../../requirements/yuhaul/error-queue.md)
- [OC-001 — YouTube API Quota and Failure Semantics](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md)
- [OC-002 — Route Evaluation Outcome Semantics](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md)
- [OC-003 — Configuration Snapshot for Historical Interpretation](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md)
- [YuHaul System Architecture](../../yuhaul-system-architecture.md), especially INV-8 and the three-tier outcome model
- [ADR-002 — Failed-Phase Runs Terminate as `failed`, Not `completed`](../../adr-002-run-status-failed-terminal-state.md) — **accepted**; records the terminal-`failed` versus resumable-`interrupted` rule this design applies
- [ARS-001 — Route, Playlist, and Run-Outcome API Contracts](../../ars-001-route-and-playlist-api-contracts.md)
- [ARD-001 — Run-Owned Production Action Authority](../../duplicate-continuation/design/ard-001-run-owned-production-action-authority.md)
- [ARS-005 — Duplicate Continuation Compatibility and Results Contract](../../duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md)
- [ARS-006 — Run-Attributed Production Outcomes](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md)
- [YuHaul Admin Interface Design Specification](../../../design/yuhaul-admin-ui-design.md), especially Run Detail, Simulation Inspection, and Backtest

## Satisfies

| Requirement / contract | Disposition in this design |
|---|---|
| BR-011 | Persisted Run, decision, action, and queue state remains the source of recovery and inspection. This design does not replace or weaken the existing recovery lifecycle. |
| BR-017 | Manual run initiation and the `202` start acknowledgement remain satisfied by the existing endpoint; this design changes the interpretation of that acknowledgement, not run initiation. |
| BR-026 AC1–7 | Evaluation, draft configuration, action suppression, and production-state isolation are provided by the existing pipeline and accepted INV-8 / ARD-001 / ARS-005 boundaries. This design adds truthful run completion and visible persisted hypothetical evidence; it does not change evaluation or matching. |
| BR-027 AC1–4, AC6–7 | Retained observations, proposed configuration, no-external-call behavior, distinguishable results, and isolation remain mode-owned by the existing pipeline. This design makes selected/evaluated counts and completion inspectable. Wiring the visible date controls to a filter is required. AC8–13 (2026-11-10) fix the meaning: YouTube published date, UTC days, both ends included, no range means no filter. [Run-initiation ADR-002/ARS-001](../../run-initiation/index.md) realize it with immutable run-row bounds and selection before `captureSelectedCount`; not implemented. |
| BR-027 AC5 | Accepted future same-application production-versus-simulation comparison is explicitly deferred from first-use launch. This artifact does not derive a baseline or show comparison-derived new/lost/net counts. |
| BR-028 AC1–6, AC9 | Existing run/video evidence and the requested-run inspection API remain authoritative. This design surfaces terminal status/reason and action outcomes, and requires the UI to reach all pages of the existing paginated video list. It does not reconstruct missing history or query live YouTube for inspection. |
| BR-028 AC7–8 | Failed-video retry-count display and cross-run video search are not changed by this bounded diagnostic design; they remain separate inspection obligations. |
| BR-015 | Route decisions and condition audits remain the durable explanation. No local outcome categories or new routing rules are introduced. |
| BR-012 AC1–5 | Existing exact-target production success remains the idempotency authority. A current-run skip is recorded as `not_attempted` with an `already_completed` reason, never as a fabricated success; the previous success remains the evidence for suppression. |
| BR-014 | Video action failures remain retained and retryable in the existing Error Queue. Run detail exposes the same run/video/route/target outcome without changing retry ownership or queue lifecycle. |
| BR-020 AC1, AC3, AC5–7 | Run inspection adds selected and decision-bearing counts beside existing outcome/duration/quota summaries and per-action detail. The existing route-statistics `ActionsExecuted` projection now counts only `succeeded` and attempted-`failed` action rows; new `deferred`/`not_attempted` evidence must not inflate attempts. This is bounded realization, not complete BR-020 certification. |
| BR-032 and OC-002 | Preserve the shared video-outcome vocabulary. Run status is not a video outcome; missing evidence is not rejection; a duplicate remains supporting information only. Run-level failure semantics are accepted: an unrecoverable processing-phase failure is terminal `failed`, resumable interruption remains `interrupted`, and normal completion is `completed` (System Architecture §Run Recovery, ADR-002). |
| OC-003 | The run's captured ConfigVersion remains the configuration authority. Input selection metadata belongs to the run, not in a mutable or repurposed ConfigVersion snapshot. |
| BR-025 | Run snapshots and historical configuration display remain with the existing ConfigVersion implementation and OC-003; the new selection fields do not alter snapshots. |
| Error Queue | Per-video failures continue through the existing Error Queue. Run-level failure status and safe diagnostic remain on Run Detail; no run-level queue entry or queue redesign is added. |
| OC-001 | This design prevents raw provider bodies from becoming diagnostic detail and records the relevant playlist-action disposition. It does not implement or claim complete response-category accounting for every read-only YouTube call; that existing contract remains a separate implementation concern. |

## Problem

The original gaps were at the joins between stored evidence and the operator's view: failed simulations could end as completed, Backtest announced completion from a start acknowledgement, selected counts and several action dispositions were missing, Run Detail could not reach later pages, the image build retained mock-default event behavior, and provider bodies could enter diagnostics. Those are pre-change findings, not descriptions of the current source.

### Current realization (source-inspected 2026-11-05)

| Boundary | Implemented realization | Important limit |
|---|---|---|
| Selection and run reads | Migration `016_run_selected_count_action_reason` adds nullable `run.selected_video_count` and `executed_action.outcome_reason`, with no backfill or date fields. `SetRunSelectedCount` is write-once; Run reads derive `videos_evaluated` from distinct run-scoped decision-bearing videos. | Capture failure is warn-only; null means unavailable, including selection not reached, capture failure, or legacy history. Equal counts are not membership/completeness proof. |
| Mode producers | Production captures run-owned discoveries/retries before exclusion; manual queue retry captures its claimed group; dry-run captures pending selection; replay/backtest capture all retained observations. Failure/panic paths use `FailRun` or `FailHistoricalRun`, not interrupt-then-complete. Production completion bookkeeping retries under a bounded cancellation-detached context only after all phases succeed. Authority-retention retries its atomic queue handoff under a bounded context; it marks failed only after all unfinished candidates are durably retained, otherwise persists interrupted under a separate bound so the run can resume. | If the store remains unavailable through both bounded retention and interruption attempts, status can remain `running`; persistence cannot be guaranteed while SQLite is unavailable. A stored status, not an event name, decides what the UI says. |
| Action evidence and route statistics | Executor records known success/failure, quota deferral, `already_completed`, and `prior_action_failed` using existing outcomes. No-attempt rows have no attempt time. Exact action identity deduplicates targets and protects recorded success from downgrade. Per-run `ActionsExecuted` counts `succeeded`/`failed`, not all rows. Cross-run operational route match rates include only completed `scheduled` and `manual` runs; simulation decisions remain in requested-run inspection. | No general attempt ledger or external reconciliation. A failed response/absent row does not prove the playlist was unchanged. |
| Run/Decision/Backtest UI | Runs and Run Detail poll persisted status while running; Backtest/Replay stores the acknowledged run ID then reads/polls its status. Read failures have retry states. Run Detail uses the real client, requests video pages, and resets pagination on filter change. Decision Detail carries reason/unavailable/attempt-time fields with mode-qualified wording. The unrelated-production-total comparison is removed. | Pagination behavior has component/API coverage, not a >100-video browser journey. Date controls still do not reach the selector. |
| Diagnostic and queue boundary | `youtube.APIError` no longer retains a body, but its reason regex and `internal/diag`'s printable-ASCII/marker test are insufficient. Current projections withhold only text failing that permissive test; pipeline/source logs can emit raw errors before vetting. Queue snake_case/identity/lifecycle work remains. | Confirmed safety gap, not an accepted private-log exception. The exact [ARS-001 contract](../specifications/ars-001-safe-diagnostic-contract.md) requires closed facts at source, writes, reads and logs, including typed SQLite/provider/timeout classification and honest legacy unavailability. Not yet implemented or verified. |
| Shipped build configuration | Dockerfile's SPA stage runs `VITE_USE_MOCK=false npm run build`. Browser proofs served a real-client production SPA through the real YuHaul server. | The current full Docker image/runtime was not built or tested in this diagnostic verification. Runtime environment alone cannot change Vite's compiled choice. |

Grounding: `internal/store/migrations/016*`; `internal/state/{models,store}.go`; `internal/pipeline/{pipeline,dryrun,replay,retry,executor}.go`; `internal/stats/store.go::enrichRouteStats`; `internal/youtube/client.go`; `internal/diag/diag.go`; `internal/queue/handlers.go::errorEntryView`; `internal/server/run_handlers.go`; `web/src/api/{queue,run-videos}.ts`; `web/src/views/{RunsView,RunDetailView,DecisionDetailView,BacktestView,ErrorsView}.tsx`; and `Dockerfile`. Semantic navigation remained degraded after one repair (`gopls: Option is not a function`); bounded direct source reads supplied this grounding. Source presence is not test execution.

## Constraints and forces

- Preserve INV-8: dry-run, replay, and backtest never call YouTube mutation endpoints, authorize production actions, change production corpus/idempotency records, or affect production statistics. Backtest continues to make no external API calls.
- Preserve existing run types, video outcome categories, route matching/order/exclusion semantics, configuration snapshots, and queue lifecycle. Do not turn `not_yet_evaluated` into a new simulation outcome or infer it for inputs that lack persisted evidence.
- Keep the single-user SQLite architecture and existing run/detail APIs. There is no scale or concurrency evidence requiring an event store, a new service, or a distributed job protocol.
- Runs and route/action evidence are durable; SSE is best-effort and not authoritative. The public read after a start request must be able to distinguish a successful start from a completed evaluation.
- Run schema already accepts `failed`; ADR-002 and System Architecture §Run Recovery settle terminal-`failed` policy. Production and simulation paths must preserve that meaning: failure is not normal completion, and interrupted recovery is distinct.
- Diagnostic detail is persisted and served from the app API. It must not contain OAuth credentials, raw provider bodies, request authorization data, or expired/transient provider metadata.

## Options considered

### A. Enrich the existing run and evidence projections — chosen

Keep the SQLite Run row as the authoritative lifecycle record; project evaluated/action counts from existing run-scoped decision/action tables; add one nullable selected-input count and the run-owned selection bounds (`published_from`/`published_to`, specified in run-initiation ARS-001). Complete Decision Detail from the existing action table, including an explicit `not_attempted`/`already_completed` record for an idempotency suppression. Let the browser re-read `GET /api/runs/{id}` until the persisted run is no longer running, and use the existing paginated video API for detail. Sanitize diagnostics before persistence and in public projections.

- **Benefits:** Reuses the source of truth, preserves historical inspection after restart, gives explicit zero versus not-yet-known input counts, and requires no event replay or new service. Existing rows remain valid; older counts stay unknown rather than guessed.
- **Costs:** One additive SQLite migration for nullable selected count/action reason, plus Run/video projections and SPA types/views. The UI needs a small persisted-status polling loop because events may be absent or dropped. Date fields need their separate accepted contract before implementation.
- **Failure modes:** A worker can fail before selection is known, leaving the nullable count unavailable. Route decisions identify evaluated evidence, not all selected-but-unprocessed video IDs. A provider-success/local-record crash can still leave action result unknown; this design will not represent it as success or failure.
- **Compatibility/security:** Additive nullable fields and no backfill preserve old runs. Public diagnostic summaries must suppress unsafe legacy free text.
- **Obligation impact:** Meets run-level status/count/action visibility without weakening simulation isolation or changing the outcome vocabulary. Run-status policy is accepted (ADR-002 / System Architecture §Run Recovery) and is no longer gated; date semantics were settled on 2026-11-10 and are realized in run-initiation ADR-002.

### B. Add a general run-input/action-attempt event ledger

Write a durable event or membership row for every selected input, phase transition, API action attempt, and result, then reconstruct summaries and inspection from it.

- **Benefits:** Could identify individual selected inputs that never acquired route decisions and preserve multiple attempts across restarts.
- **Costs:** Adds a second history model, write volume, event ordering and projection logic, schema/API migration, and new failure semantics between external calls and event persistence. A pre-call intent event still cannot prove what YouTube did after a process crash; closing that window needs a separately accepted external reconciliation/idempotency contract.
- **Failure modes:** Partial event sequences and stale projections can disagree with existing route/action evidence. Every consumer must decide which history is authoritative.
- **Compatibility/security:** Larger retention surface for provider/user data; more paths must sanitize and restrict raw details.
- **Obligation impact:** Exceeds BR-028/BR-020 needs for the requested run summary and per-video evidence. ARS-005 explicitly does not establish a general zero-route/pre-decision membership contract. No accepted requirement currently justifies a second ledger.

### C. Rely on logs, toasts, or SSE completion events

Keep persistence unchanged and show process-local messages or infer completion from an event.

- **Benefits:** Little schema or API work.
- **Costs/failure modes:** Cannot inspect after restart, cannot recover missed/dropped SSE events, and does not make a successful start equivalent to terminal completion. It cannot provide truthful historical per-video action evidence. The original mock-default build compounded this risk; correcting the build still does not make events authoritative.
- **Obligation impact:** Fails BR-028's persisted-evidence requirement and can produce false completion claims. Rejected.

## Decision

Use the existing Run row and requested-run decision/action evidence as the only authoritative record. Add a nullable run-level `selected_video_count` set exactly once when that run's input set is established; for historical simulation modes also retain the submitted date bounds after their semantics are settled. Do not backfill those values on old runs. Project `videos_evaluated` as the count of distinct videos with persisted route-decision evidence for that run; retain the existing `videos_discovered` production meaning. The UI must label and qualify the count as decision-bearing inputs, not as proof that every selected video reached a complete evaluation.

Use the existing run API for lifecycle status and safe diagnostic summaries. A start response remains a start acknowledgement, never completion. While status is `running`, the client re-reads persisted run state; once it becomes `completed`, `failed`, or `interrupted`, it stops presenting active progress and shows that actual stored status. An SSE event may improve phase feedback but cannot override a persisted status or establish completion. A stored `interrupted` run is not shown as completed.

Keep per-video details run-scoped. Record a known action result in the existing `executed_action` row with the existing vocabulary; project failed and quota-deferred outcomes as well as existing successes and simulated `not_attempted` rows. For an exact-target production action already completed by an earlier production run, record `not_attempted` with a bounded `outcome_reason=already_completed`, and present “not attempted in this run — already completed.” Do not create a new successful action row or timestamp. Set `executed_at` only when a YouTube action was actually attempted; a quota-proactive deferral, simulation, or idempotency suppression has no attempt timestamp. If no durable outcome exists, show no confirmed result rather than inventing success or failure. Route nonmatches/exclusions remain explained by existing route decisions and audits.

Do not show a production comparison/new-catch/lost-catch result in Backtest until its accepted future baseline contract is implemented. Remove the current aggregate fallback: summing unrelated production-run totals is not a valid comparison. A completed backtest means only that the requested evaluation finished.

Build the shipped SPA with `VITE_USE_MOCK=false` at **build time**. Runtime container environment cannot change Vite's compiled `import.meta.env`. Local development may keep the mock default. This makes the existing SSE subscription real in the production image; persisted polling remains the lifecycle authority. The run/detail API functions already issue real fetches and are not all routed through mock mode.

### Persistence and API shape

- Migration 016 adds nullable `run.selected_video_count` and `executed_action.outcome_reason` only. Do not edit applied migrations or backfill historical runs. `NULL` selected count means the selection was not captured/unknown; `0` means selection completed and found no inputs. `outcome_reason` is explanatory evidence, not another action outcome category. Any future date metadata requires the accepted date contract and a later forward migration.
- Keep evaluated count derived from `COUNT(DISTINCT route_decision.video_id)` scoped to the requested run. Use the existing `run_id` index; do not count decisions, route actions, or duplicate matches as separate videos.
- Run reads expose nullable integer `videos_selected` and derived integer `videos_evaluated` (zero when there are no decision-bearing videos). The bundled client also tolerates omitted fields from older responses as unavailable. Existing `videos_discovered` remains unchanged. A missing/null selected count stays unavailable, not zero. Per-action detail adds optional `outcome_reason`; no action outcome category is added.
- Keep `GET /api/runs/{id}/videos` and detail route/action data scoped to the requested run. Extend the client to request the existing `page` parameter and expose navigation/load-more so every returned page can be inspected. Filtering and totals keep ARS-001 semantics.
- Keep start endpoints' existing accepted/running response. No new run endpoint or durable job/event subsystem is needed.
- No new action outcome enum is needed: the existing outcome constraint already permits the required values. One nullable reason column distinguishes an actual no-attempt reason without overloading `error_message`. No run-status schema migration is needed: the current SQLite constraint already includes `failed`. Date-bound fields are excluded from migration 016 and remain blocked.

### Failure diagnostic boundary

The exact realization is [ARS-001 — Safe Diagnostic Facts Across Producers, Storage, API and Logs](../specifications/ars-001-safe-diagnostic-contract.md). Printable ASCII and identifier-shaped provider reasons alone do not establish safety. Logs have the same release boundary as persistence/API/events: no raw source logging before vetting. The following accepted constraints remain in force; ARS-001 specifies their field vocabulary, legacy admission and enforcement seams without changing outcome or recovery policy.

Use short allow-listed diagnostic codes/categories and bounded safe summaries: phase/operation, HTTP status where known, and a vetted provider reason such as `quotaExceeded` or `authError`. Do not persist or serialize `APIError.Body`, full `err.Error()` chains, OAuth token material, raw request/response bodies, or transient metadata. `interrupt_reason` remains the internal run recovery reason; public Run projections expose only a sanitized diagnostic. Queue API projections must not serialize the existing raw `ErrorEntry.Detail`; new queue/action detail uses the same safe fields. For historical rows where a safe reason cannot be recovered, show status/phase and an explicit unavailable-reason state rather than raw text. Do not rewrite historical queue/run rows as a “redaction migration.”

This is additive at the public diagnostic level and does not create a second run-error queue. Existing ErrorQueue entries continue to explain failed videos and retain their current retry/resolution lifecycle.

## Structure and flows

```text
UI --POST start/config/selection--> API --> persisted Run(status=running, snapshot)
 |                                      |
 |                                      +--> async mode-specific input selection
 |                                           -> selected count persisted (if captured)
 |                                           -> pipeline records decisions/audits/actions
 |                                           -> existing video error/deferred queues
 |
 +--GET Run while running--------------+--> Run read projection
 +--GET run videos?page&outcome------------> requested-run decisions/actions
 +--GET video decision detail--------------> conditions + route/action evidence
```

The mode-specific selector remains in the current pipeline:

| Mode | Selected inputs | Evaluation/result evidence | External effects |
|---|---|---|---|
| `manual` / `scheduled` | Inputs admitted to this run, including run-owned retry work and discoveries, established before route evaluation | Existing production run outcome, route decision, condition audit, action and queue records | Existing production behavior only |
| `dry_run` | Pending videos selected by the current dry-run selector | This run's hypothetical route decisions/audits and `not_attempted` simulated actions | No mutation API calls; no production corpus, idempotency, or statistics writes |
| `replay` / `backtest` | Retained observations selected by the replay/backtest selector; currently all retained observations | This run's hypothetical decisions/audits/actions | No external YouTube API calls for backtest; replay remains simulation and must not execute actions |

The client obtains terminal status from the persisted Run API. It may display phase events, but every terminal screen is confirmed by a run read. Errors from the status read leave completion unknown and require an explicit status-read error/retry affordance; they are not converted into a success toast.

Dry-run, replay, and backtest finalization uses a bounded context detached from request cancellation. An unrecoverable evaluation or initial-load failure is persisted as terminal `failed`; if that write also fails, the existing resumable `interrupted` state is the fallback. A successful evaluation whose first completion write fails retries completion with the bounded finalization context. These simulation paths must preserve their decision-derived counters and never invoke production counter recomputation or external playlist actions.

## Input and operation coverage

- **Selection complete with zero rows:** persist `selected_video_count = 0`; show `0 selected`, the decision-bearing count, and the real run status. A completed empty evaluation is not a claim that any video matched or was added.
- **Selection not reached or unavailable:** keep the selected count null. Do not substitute the discovered count, a count of decisions, or zero.
- **Evaluation in progress/failure:** evaluated count reflects persisted route-decision evidence at read time. A gap from selected count indicates not all selected inputs have decision evidence; it does not assign a per-video outcome.
- **Route mismatch/exclusion:** show the existing ordered route decisions, skip reason, condition audits, and final hypothetical/production outcome. No routing predicate or duplicate behavior changes.
- **Action success/failure/deferral/simulation:** expose the corresponding existing action outcome and safe detail when persisted. A quota deferral has reason `quota_exhausted` and no external attempt; simulation `not_attempted` is explicitly hypothetical; later actions skipped after an earlier failure use `prior_action_failed`. Do not count `not_attempted` as an external action execution.
- **Prior idempotent completion:** persist a current-run `not_attempted` action with `outcome_reason=already_completed`, tied to its matched route decision and exact target. The durable earlier production success remains the evidence for suppression; never fabricate a current-run success.
- **Per-video evidence absent:** do not synthesize a route result. This design deliberately does not add a general selected-video membership ledger for zero-route or pre-decision simulation inputs; ARS-005 explicitly leaves that broader contract outside its successful-result inspection path.
- **Backtest comparison:** not shown. BR-027 AC5 remains deferred from first-use launch; no baseline is computed from sums of production counters or from current video state.

## Invariants and guarantees

| Guarantee | Precondition / owner | Enforcement | Falsification point |
|---|---|---|---|
| Start is not completion | A run record exists; API server owns Run lifecycle | `202` continues to mean accepted/running; UI refreshes stored status and never announces completion from the start response | A delayed/failing run produces a completed toast before persisted terminal status |
| Selected and evaluated counts are not conflated | Input selection boundary is reached; pipeline records selected count; query layer projects decisions | Nullable selected count; decision-bearing count is distinct and run-scoped; discovered count semantics unchanged | Empty selection appears as unknown/positive, or selected inputs are described as evaluated without decision evidence |
| Non-production isolation remains intact | Run type is dry-run/replay/backtest | Existing mode/action/state gates; simulated action rows remain `not_attempted`; no production writes are authorized | Fake YouTube observes a mutation call or production corpus/statistics/idempotency changes |
| Route/action evidence belongs to the requested run | Persisted run ID and snapshot are available | Existing requested-run decision/detail queries; no live reconstruction | A video detail contains another run's action/decision or current mutable route state |
| No false action success | A durable action outcome or prior completion row exists | Only a persisted same-run success is shown as succeeded; idempotent prior success is recorded as an explicit no-attempt reason; absent evidence stays unconfirmed | Any missing action row is shown as failed/succeeded, or a prior run's success is attributed to this run |
| No sensitive provider detail leaks | Diagnostic crosses SQLite/API/browser boundary | Allow-listed structured summary; raw provider body and auth/request data suppressed in run and queue serializers | Sentinel body/token appears in database detail, API JSON, browser output, or error log |
| Run-level failure stays separate from video queue | Phase/run failure can occur without one failed video item | Stored run status and sanitized run diagnostic render on Run Detail; Error Queue contract unchanged | A run failure is invisible when the video Error Queue is empty, or a synthetic queue item is created for it |

## Failure and recovery

- [Authorization ADR-005](../../youtube-authorization/decisions/adr-005-startup-recovery-for-guarded-production-runs.md) closes the decided startup-discovery seam for new guarded production runs stranded as `running`: exclusive pre-producer startup classification, original-checkpoint continuation and durable uncertainty vetoes. Implementation and real kill/restart proof remain outstanding. No automatic historical version-0 recovery, terminal-run reopening or live Run 14 operation is authorized; a null checkpoint is not proof of never-started execution.
- Production failure/panic handling uses `FailRun`; dry-run/replay/backtest failure/panic handling uses `FailHistoricalRun`. Neither path completes after recording failure. After successful execution phases, completion bookkeeping alone may be retried with a bounded detached context. For production authority failures, unfinished candidates are handed to the queue atomically before `failed` is written; if that handoff cannot complete, the run remains owned by its run row and a separately bounded `interrupted` write permits a later resume. If SQLite remains unavailable through both retention and interruption attempts, status may remain `running`; the implementation logs this failure and cannot promise persistence during store unavailability. Preserve the accepted rule: unrecoverable phase failure/panic is terminal `failed`, resumable interruption stays `interrupted`, and only normal completion is `completed`.
- Run status finalization and known outcome counters must be persisted before any terminal success is shown. A `run.completed` SSE event is a hint; if `CompleteRun` failed and persisted status is `interrupted`, the UI shows `interrupted`.
- If action response is known but persisting its outcome row fails, report that result was not durably recorded; do not claim the YouTube state is unchanged. If the process dies after provider success but before local recording, the current architecture still cannot distinguish that from no request. This design exposes that limit and does not add an outbox, retry generation, or provider reconciliation protocol.
- A persisted `failed` action means the request returned an error; for timeout/connection-loss cases it does not prove that YouTube did not apply the mutation. Do not display “not added” as a confirmed external-state fact without a separate authorized provider reconciliation path.
- Per-video action failures remain in the existing Error Queue with the same retry/resolution lifecycle. Run-level failures do not create queue entries merely to make them visible.
- Historical selected counts remain null; date bounds do not exist in this migration. No production result, input set, or outcome is reconstructed from today's mutable video state or route snapshot.

## Security / trust boundaries

The existing application API is served behind the selected ingress boundary; local port-forward access remains the documented bypass. This design adds no new trust boundary and does not assume multiple tenants. It does reduce the data released across the current API boundary:

- `youtube.APIError` now retains status and a vetted identifier-like reason, not a body. Transport handling removes request URLs before error propagation. New diagnostics must continue to exclude raw provider bodies from `error_detail`, `interrupt_reason`, action `error_message`, events, and logs.
- Only allow-listed HTTP status/provider reason and stable internal phase/action codes may cross into persisted diagnostic summaries. No OAuth token, authorization header, raw body, URL query, or transient metadata is included.
- Public queue and Run serializers suppress unsafe historical free text. Existing raw detail rows are not bulk-rewritten; when they cannot be safely projected, the UI states that the reason is unavailable.
- The detailed run and video evidence remains available through the existing app API; this design does not create public telemetry or a new export surface.

## Consequences

- **Positive:** Run start, active work, completion, interruption, and failure are distinguishable; empty and partially processed selections are visible without false zero/complete claims; per-video action outcomes are inspectable; diagnostic release has explicit source/write/projection checks rather than UI-only hiding.
- **Negative:** One additive migration and API/client changes are required. Old runs cannot acquire trustworthy selected counts or failure detail retroactively. Polling adds a small read load to the single-user SQLite service.
- **Neutral:** Existing route conditions, configuration snapshot authority, production idempotency, error queue lifecycle, and simulation isolation remain unchanged. Simulation comparison remains deferred.
- **Known limit:** A count mismatch does not identify every selected video lacking decisions. Full membership and provider-side crash reconciliation require separate accepted contracts; they are not hidden by this design.

## Verification and composition

### Executed evidence and its scope (reconciled 2026-11-05)

Local test executions below were repeated during the initial t3.2 documentation update. After the diagnostic harness correction, the documentation author also reran the five composition cases and the entire integration-tagged `./e2e` package: PASS. The independent bearer-transport trace and browser results are attributed to reviewers' full stored reports in Loom Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`, not rerun by the documentation author. A reported pass applies only to its observed path. Temporary artifacts are local evidence pointers, not shipped acceptance fixtures or permanent links.

| Evidence | What it establishes | Limits / disposition |
|---|---|---|
| `go test -count=1 ./internal/state ./internal/queue ./internal/server ./internal/stats ./internal/diag ./internal/youtube` and `go test -count=1 ./internal/pipeline` — PASS | Current state/API/queue/statistics/sanitizer/provider/pipeline suites pass, including null/zero and distinct counts, action dispositions, simulation phase-error/panic handling, safe legacy projections, and attempt-only statistics. | Package/component evidence, not every possible crash, response category, browser state, or full-contract proof. |
| Corrected diagnostic composition: `go test -tags=integration -count=1 -v ./e2e -run '^TestComposition_'` — PASS, five top-level cases; `go test -tags=integration -count=1 ./e2e` — PASS | Real pipeline/selectors/executor → migrated SQLite → close/reopen → registered HTTP reads. Covers partial evaluation (selected differs from decision-bearing), action outcomes and prior-success suppression, dry-run/replay/**actual Backtest** selection, unknown/sentinel and zero selection. All five cases now use `setupDiagnosticCompositionEnv`: production `auth.SQLiteTokenStore` saves the synthetic prerequisite; real `auth.Service.ActiveToken` loads/decrypts it; client factories preserve `oauth2.Transport` bearer handling around the controlled provider's base transport (`e2e/invariants_test.go:375–468`). No final result rows are seeded; controlled SQLite faults exercise failures. | Reviewer `ac7a8a40-5111-4632-8a42-7e7a5c394886` independently passed the exercised Plan10 t1.5/t1.6 only-controlled-YouTube fidelity condition, focused/whole-e2e suites and socket-write trace: 27 provider requests, all 27 with expected bearer, including four playlist POSTs. All five cases assert positive request totals and zero unauthorized requests. Backtest's live counter is separately baselined immediately before creation/execution and unchanged afterward; setup discovery accounts for the simulation case's four authenticated requests. Metadata refresh/error execution is **not exercised**: the configured test-local adapter is not the exact production adapter, and unused `Pipeline.auth` remains nil. This proves neither full metadata/auth lifecycle nor browser/exhaustive recovery behavior. |
| `npm test -- --run src/views/RunsView.test.tsx src/views/RunDetailView.test.tsx src/views/DecisionDetailView.test.tsx src/views/BacktestView.test.tsx src/views/ErrorsView.test.tsx src/api/run-videos.test.ts src/api/queue.test.ts` — PASS, 99 tests | Local UI/adapter status, polling/read-error, pagination/filter, action wording/reason, and queue wire-consumer checks pass. | Shaped API doubles are appropriate local tests but cannot prove producer-to-browser composition. |
| E2E-001 reviewer `27bae524-fb64-41ab-b0fd-a26e8508a6be`, test-only follow-up `5347d563-6e78-4a72-8617-39445d2b8e3b` — PASS for bounded production/queue journey | Real-client SPA, real binary/API/auth store/discovery/executor/SQLite, controlled YouTube only. Browser creates route/starts run; real provider rejection produces run/video/action/error evidence. Keyboard Retry resolves the original queue row in a fresh production run, skips prior completed target as `not_attempted/already_completed`, and inserts only remaining targets. Separate keyboard Remove dismisses another real error without provider call/retry run; history survives restart. Sentinel/body and plaintext synthetic token are absent from checked storage/API/UI/log surfaces. | `/tmp/opencode/e2e001-queue-fixed/` holds script, manifests, logs, screenshots and responses. Queue cells show generic safe `playlist insertion failed`; provider-specific detail is in API/Decision Detail. The earlier PascalCase safety-test decoder failure is closed by independently executed queue/server tests and byte-identical production binary, not by weakening assertions. No SSE event was captured in this follow-up. |
| E2E-002 reviewers `ca0af90c-7d85-4f52-a10b-39dd60e84984` (API) and `b3713cd8-d4af-4fec-bd5f-ba3bb4c55a3c` (browser) — PASS for no-token failure | Start returns `202`/running; stored run becomes `failed` with safe discovery/auth reason. Browser Run Detail shows failure while Errors is empty; no synthetic run-error queue entry. | `/tmp/e2e-browser-final.js` and its screenshots record this representative path. This is not a delayed-run, every-status-read-fault, or OAuth acquisition/refresh proof. |
| E2E-003 nonzero dry-run in reviewer `6ceec0e9-5542-4860-8236-6f025724501e`; actual Backtest in `ecf02883-cba9-4da5-968a-e2954b95e849` — PASS for those bounded browser paths | Controlled pre-evaluation failure after real discovery leaves genuine pending inputs. Dry-run selects/evaluates two and records hypothetical `not_attempted` actions with zero mutations, keeping videos pending. Separate corrected browser journey POSTs **`/api/backtest`**, persists **`run_type=backtest`**, selects/evaluates two retained observations, completes, and makes zero provider calls during execution. | Earlier empty dry-run and mislabeled Replay runs are not nonzero-dry-run/Backtest proof. The earlier “Backtest made a read” finding was a counter/endpoint harness error; actual Backtest proof replaces it. The pre-click `/playlists` dropdown request is UI setup, not Backtest execution. Do not repeat the older report's same-item production re-admission claim. Date controls were empty; no date filtering or production baseline is proved. |
| Designer `0554f046-f9fb-48c3-a2fe-a9dc4331b02c` with `94b27bf6-059c-4dc4-ab05-e30405f14679` — source-level PASS | Mode-qualified labels, persistent hypothetical explanation, distinct counts/status, unavailable reasons, and acknowledgement-versus-completion match accepted design in inspected source. | Rendered visual/assistive-technology certification was not performed by that review. Empty route-decision copy, mode-qualified Run Detail toast and retry-heading focus are advisory, not resolved by recording them. E2E-001 later checks representative desktop/320px screenshots and keyboard queue controls only. |

E2E-001 and E2E-003 use an isolated, unexpired synthetic token written through production `auth.SQLiteTokenStore`; `auth.Service`/`ActiveToken` stay real. E2E-002 deliberately has no active token. The corrected local diagnostic composition also uses the real encrypted token store/service, but remains separate evidence from the browser journeys. No final run/video/decision/action/outcome/queue rows are inserted as evidence. These fixtures do **not** prove Google OAuth initiation, callback/state validation, exchange, refresh, revocation, live YouTube compatibility, or a real user's account authorization. Configured metadata transport is not proof of executed metadata refresh/error behavior.

### Confirmation obligations and remaining gaps

The original confirmation targets remain constraints. The table above establishes bounded executed coverage; it does not turn every target below into a pass.

1. **State/status:** Start each mode through HTTP and verify the response is only a start acknowledgement. Exercise successful completion, phase error, panic, interruption, and failure-finalization error; reopen the database and verify status/phase/reason. Failed historical and dry-run paths must not become completed. Check that `failed` is accepted by frontend types/filters without adding a schema enum migration.
2. **Counts:** Use real SQLite and the real selector for production discoveries/retry work, pending dry-run inputs, and retained backtest/replay observations. Cover selection of zero, selection known before a mid-run failure, selection unavailable before selector completion, more selected than decision-bearing inputs, and a legacy run with null selected count. Verify distinct-video counts and that `videos_discovered` retains production meaning.
3. **Action outcomes:** Through the executor with the existing fake YouTube base-URL seam, cover insert success, provider error, quota deferral, later action skipped after failure, simulation `not_attempted`, and prior exact-target success reuse. Verify no simulation mutation call, no false current-run success for reuse, and no action result counted more than once for duplicate configured targets.
4. **Inspection UI:** Exercise a run with more than 100 video outcomes, apply/clear each outcome filter, page through all results, open each requested-run detail, and inspect conditions and action evidence. Verify failures are visible on Run Detail with an empty Error Queue and video failures still remain in the existing queue.
5. **No false comparison:** Run a backtest while production runs have unrelated accepted totals. Verify only requested-run hypothetical outcomes are shown and no new/lost/net comparison is inferred. Verify the completion copy does not promise playlist additions.
6. **Date filter:** The semantics are accepted (BR-027 AC8–13). The required proofs are run-initiation ARS-001 §8.5–8.6 (D1–D8, E2): boundaries, omitted and single bounds, reversed and invalid ranges, non-UTC browser, eligible-local published-date source, selection count and saved-range display, against real retained records.
7. **Diagnostic safety:** Use a fake provider response containing sentinel secret/body text plus an allow-listed status/reason. Assert the sentinel is absent from persisted newly created run/action/queue diagnostics, all API JSON, and emitted logs; safe status/reason remains. Seed a legacy unsafe `interrupt_reason`/`error_detail` and verify the API does not return it verbatim.
8. **Real SPA build mode:** Build the shipped Docker image with `VITE_USE_MOCK=false` in the SPA build stage; serve that artifact against the real YuHaul API and its fake YouTube provider. Verify the browser sends run/status/detail requests to the API, the SSE handler is real (not the mock timer), the persisted run ID/status matches SQLite, and the fake provider observes the expected production mutation but zero dry-run/backtest mutations. Record the image/build inputs; a runtime container env var alone is not proof because Vite values are compiled into static assets.

**Still unverified or unavailable:** the current full container build/runtime and deployment; exhaustive failure-finalization/restart/crash interleavings; a real-browser >100-video/every-filter journey; complete production corpus/idempotency/statistics isolation snapshots for every simulation browser case; full contrast/200%-zoom/actual-screen-reader verification; and universal safety of arbitrary historical free text. SSE is not promoted to lifecycle authority by any evidence here. No new browser, container or live-provider execution was performed during this documentation update.

**No longer blocked:** date semantics were decided on 2026-11-10 and their realization is designed in run-initiation ADR-002/ARS-001. Implementation and boundary proof remain outstanding, and the Designer owns the control and saved-range presentation. **Deferred by agreement:** BR-027 AC5 same-app production comparison. **Not solved:** selected-input membership without decisions, provider-success/local-record reconciliation, BR-028 AC7–8's broader inspection obligations, and complete OC-001 all-operation accounting.

Independent implementation/browser review is recorded above; it is not an independent architecture/security critique of every guarantee. Critic session `ses_e5282497cffeRlP53qcH2KgrU2` correctly caught auth/metadata substitutions in the earlier local harness. The subsequent diagnostic-only harness correction and reviewer `ac7a8a40-5111-4632-8a42-7e7a5c394886` supersede that current-fidelity limitation for the exercised path. Reviewer follow-up `4b0477b4-3c8b-44d0-907f-99643f4e7bd6` resolves bearer-transport blocker `5f2d12db-ac62-49d4-94c1-a2d79cee956b`; the earlier concern remains historical evidence, not a current blocker. No product change was needed. Metadata refresh/error execution, exact production metadata-adapter execution and live OAuth remain unproved; unused nil `Pipeline.auth` grants no lifecycle proof. Browser evidence was not rerun or replaced by this local follow-up. A separate critique is still needed before relying on the date sub-contract in run-initiation ARS-001.

## Open questions

| Question | Owner | Blocks |
|---|---|---|
| ~~What do Backtest/Replay date inputs select, in which time zone, and are both ends inclusive?~~ **Answered 2026-11-10** by the user ([BR-027 AC8–13](../../../requirements/yuhaul/br-027-historical-backtesting.md)): YouTube published date, UTC days, both ends included, no range means no filter. Realized in [run-initiation ADR-002/ARS-001](../../run-initiation/index.md). Whether Replay should apply the range remains a non-blocking product question (BR-027 index). | Product owner (Replay scope only) | Nothing. |

The former run-status question is resolved and no longer open: the accepted System Architecture §Run Recovery rule and [ADR-002](../../adr-002-run-status-failed-terminal-state.md) settle unrecoverable processing-phase failure as terminal `failed`, resumable interruption as `interrupted`, and normal completion as `completed`.

No question is opened for production-vs-simulation comparison: its accepted first-use deferral is already explicit in BR-027 AC5 and the design spec. Revisit only when the owner accepts a baseline/capture contract.

## Concerns and assumptions

- **Assumption:** One nullable run-level selected count is enough for this request. It adds no per-video selection ledger and respects ARS-005's explicit exclusion of a general zero-route/pre-decision reporting contract. If a future accepted requirement demands identifying every selected-but-untouched video, this design must be revisited with that obligation in hand.
- **Concern, intentionally not expanded:** The provider-success/local-record crash window remains an unproven idempotency risk documented by ARD-001/ARS-005. A new event ledger alone cannot reconcile an external mutation after process death; no such stronger guarantee is introduced here.
- **Concern, advisory:** OC-001 requires response-category accounting for every API call. This task covers safe, useful action diagnostics, not comprehensive read-call accounting; the separate obligation remains in force and must not be described as fulfilled by these action rows.
- **Assumption:** Existing single-user deployment and ingress authority are unchanged. No additional authentication service, log aggregation service, or event-processing system is justified by this diagnostic goal.
