---
type: design
title: ARD-001 — Separate Discovery and Processing with a Local Budget Coordinator
description: Proposed single-process discovery and processing jobs backed by existing SQLite work state, with shared quota admission and recoverable handoffs; product scheduling and first-use policies remain unresolved.
tags: [architecture, design, discovery, processing, queue, quota, scheduling, recovery]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** proposed — architecture assessment only. No implementation, live-run intervention, deployment operation, or runtime verification was performed. New controls, automatic dispatch policy, bootstrap selection limits and pause semantics are not accepted by this document.

**Reconciliation, 2026-11-10 (still proposed):** the user gave direction, recorded in the [requirements index](../../../requirements/yuhaul/index.md): both jobs run automatically on separate schedules, and exhausting the quota is acceptable because work continues after the quota resets. This answers the automation question below and lowers the weight of budget reserve/fairness for oversubscription. It does **not** accept this ARD, amend BR-016/BR-017 AC2/AC6 or the quota requirements, or define pause, first-use scope or caps. Since this assessment, accepted work has changed some of its source observations: startup recovery now classifies orphaned `running` rows (authorization ADR-005/007), and Run Now will resume a time-limit-interrupted Run Now run ([run-initiation ADR-001](../../run-initiation/decisions/adr-001-run-now-resumes-time-limit-interrupted-manual-run.md)). The source table below is kept as dated evidence from 2026-11-05. Any future coordinator must take over the production lock and the Run Now admission order defined there.

## Recommendation

Separate **discovery** from **production processing**, and let a **coordinator inside the existing YuHaul process** decide which authorized work can run next. Keep the durable queue in SQLite: the existing pending video lifecycle is the right starting point. Do not add Redis, a message broker, separate worker containers, or a distributed scheduler for the current single-user deployment.

Discovery retrieves subscription observations and makes them durable. Processing takes a durable selected set, evaluates the routes, and executes eligible actions. The coordinator reuses the scheduler and quota ledger, but must add shared request admission: today's independent budget checks are not reservations. Each stage must be able to yield at a committed boundary so the coordinator can react to backlog and budget instead of waiting for an entire subscriptions crawl.

For example, **if the user chooses discovery-only/manual-processing mode**, a bounded discovery leaves real videos pending. A dry-run can preview those rows without adding them to playlists. Only a separately authorized processing run can act, using that processing run's captured rules. Discovery completion means acquisition finished within its declared scope, not that routing or playlist additions finished. A preview does not itself approve a frozen production batch; that stronger meaning needs an explicit decision.

**Separation does not make discovery cheaper.** An unbounded first crawl still consumes quota. A coordinator that starts that entire crawl and waits for it is not a quota solution. Limits, resumable discovery and a shared API admission boundary are separate necessary changes. This proposal cannot change already-running manual run **#14**, refund its quota, or provide it a cancel endpoint. Its deployed state and binary were not inspected here; the reported empty pre-run video table and rising quota are supplied context, not fresh observations.

## Derived from

- User proposal supplied to this assessment: two independently controlled jobs, discovery into a queue and processing according to routes/rules; safe first-use preview; then a coordinator driven by API budget and queue size. This is a direction to evaluate, not approval of unspecified policies.
- [Anchor](../../../anchors/youtube-discovery-routing.md) and the requirement inventory at `docs/requirements/yuhaul/index.md`; applicable requirement concepts are linked below. The inventory is a reserved navigation file, not an indexed graph concept.
- [System Architecture](../../yuhaul-system-architecture.md), especially the single-process deployment, outcome vocabulary, quota constraints and recovery rules.
- [Run-Owned Production Action Authority](../../duplicate-continuation/design/ard-001-run-owned-production-action-authority.md), [ARS-006 — Run-Attributed Production Outcomes](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md), and [Run Diagnostics](../../run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md).
- [Google quota-cost documentation](https://developers.google.com/youtube/v3/determine_quota_cost), read 2026-11-05: requests including invalid requests cost at least one point, additional pages cost quota, the relevant list methods cost 1 and playlist insertion costs 50; the common daily quota resets at midnight Pacific Time. This does not prove actual allocation or remaining usage for the deployed project.

## Satisfies

This table maps the proposed realization, not implemented conformance. Existing accepted requirements remain authoritative until their owner explicitly amends them.

| Applicable authority | Disposition |
|---|---|
| [BR-002](../../../requirements/yuhaul/br-002-subscription-discovery-without-live-monitoring.md); [Subscription Discovery](../../../requirements/yuhaul/subscription-discovery.md) | Addressed structurally by durable page handoff, channel-level completion and recoverable polling. **Blocked policy:** first-discovery scope and whether capped discovery changes each-run completeness. Partial channel failures cannot discard successful observations. Subscription freshness remains required, not silently replaced by stale cached data. |
| [BR-011](../../../requirements/yuhaul/br-011-processing-state-persistence.md), [BR-030](../../../requirements/yuhaul/br-030-temporary-failure-work-preservation.md) | Addressed by durable processing admission before decisions and page checkpoints after persistence. Existing retry ownership is reused. Provider-action ambiguity and startup reconciliation remain technical gaps requiring proof, not deferred guarantees. |
| [BR-012](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md) | Existing production exact-target completion checks and serialized execution remain required. Separation does not close the provider-success/local-record crash window. Full conformance remains unestablished; architecture/implementation owns reconciliation. |
| [BR-013](../../../requirements/yuhaul/br-013-quota-limited-work-deferral.md), [BR-018](../../../requirements/yuhaul/br-018-api-consumption-accounting-and-visibility.md), [BR-019](../../../requirements/yuhaul/br-019-quota-aware-processing.md), [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) | Addressed by shared request admission, conservative durable budget holds, response-category accounting, explicit stop reasons and retained work. Existing ledger alone is insufficient. Allocation preference/limits remain user-owned; quota deferral and no immediate quota retry are already mandatory. |
| [BR-014](../../../requirements/yuhaul/br-014-failed-work-retention-and-retry.md) | Satisfied elsewhere by existing error/deferred queue semantics; the new pending selector must not bypass manual failure retry, dismissal or retry eligibility. |
| [BR-016](../../../requirements/yuhaul/br-016-configurable-scheduled-runs.md), [Scheduled Runs](../../../requirements/yuhaul/scheduled-runs.md), [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md), [Manual Runs](../../../requirements/yuhaul/manual-runs.md) | **Blocked amendment:** BR-017 AC2 explicitly defines manual full-pipeline execution; AC6 prevents concurrent manual/scheduled runs. Preserve existing full-run behavior unless amended. Independent stage controls and coordinator scheduling need an accepted interaction/schedule contract; the recommendation serializes production stages rather than inventing concurrent execution. |
| BR-003–008, BR-010; [OC-004](../../../requirements/yuhaul/oc-004-imported-history-as-duplicate-reference.md) | Satisfied elsewhere by existing route/exclusion/detection/snapshot/provenance contracts, preserved here. No duplicate policy, reference-eligibility, route-order or credit change is authorized. |
| [BR-009](../../../requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md) | Discovery preserves native observations/fingerprints; corpus imports remain references, not pending processing jobs or evidence of native discovery. |
| [BR-015](../../../requirements/yuhaul/br-015-explainable-route-decisions.md), [BR-020](../../../requirements/yuhaul/br-020-run-level-and-route-level-statistics.md), [BR-028](../../../requirements/yuhaul/br-028-historical-run-inspection.md), [BR-032](../../../requirements/yuhaul/br-032-processing-outcome-distinction.md), [OC-002](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) | Existing run diagnostics and run-owned disposition/decision/action records are reused. Discovery versus processing purpose and partial acquisition coverage must be inspectable. No new video outcome or inference from missing evidence. Exact new job API/presentation is blocked on behavioral clarification. |
| BR-003 AC11, BR-025, [OC-003](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md) | Existing run-initiation snapshot authority is preserved, including while waiting to execute. A fresh processing run captures its own configuration; same-run resume keeps it. Discovery does not grant later action authority. |
| [BR-026](../../../requirements/yuhaul/br-026-dry-run-evaluation.md), [BR-027](../../../requirements/yuhaul/br-027-historical-backtesting.md) | Existing isolated simulations remain separate from consuming work. BR-027 AC5 comparison remains **deferred by accepted first-use scope**. Date-selection meaning remains **blocked separately**, not solved by discovery lookback or coordinator scheduling. |
| [BR-031](../../../requirements/yuhaul/br-031-user-authority-over-policy-decisions.md) | Coordinator may execute only authorized policy. Queue pressure cannot change routes, thresholds, exclusions, eligible sources, or user-held processing permission. |

## Current mechanisms and material findings

Source grounding is the current working tree, including in-progress diagnostics changes, not a claim about run #14's deployed revision. Semantic navigation was attempted first; `code-intel.get_graph` returned degraded gopls (`Option is not a function`). Bounded direct source reads supplied the evidence below.

| Boundary | Verified current mechanism / limit |
|---|---|
| Discovery | `internal/discovery/engine.go:128–164` lists subscriptions, channels, all upload pages, fetches metadata, **then** persists. `238–299` filters by per-channel watermark but continues pagination to the end; no age/video/page/job-quota cap exists in that loop. A watermark is a filter, not an early pagination stop. |
| Partial acquisition | A later channel/API failure returns before the final persistence call (`152–163`, `258–260`), losing buffered observations from earlier successful channels. The no-loss/partial-channel obligations cannot be met by moving this whole method behind a new job name. |
| Watermark | `333–377` commits videos individually; `369`, `447–460` advance a fully enumerated channel's watermark in **each** video's commit. If a later video commit fails, remaining buffered IDs may be older than the committed watermark and skipped later. This is a source-level loss path, not a reproduced live incident. Empty channels also have no per-video persistence boundary to record completion. |
| Pending substrate | `state.Machine.DiscoverVideoTx` (`internal/state/machine.go:69–129`) writes native discovery and a run-attributed not-yet-evaluated row transactionally. `state.Store.ListPendingVideos` (`store.go:652–677`) reads `processing_state='not_yet_evaluated'`. This is durable backlog, **not yet an independently claimable production queue**. |
| Ordinary production | `pipeline.go:314–400,478–499,842–846` processes its own discoveries/decisions/owned retries, not all old pending videos. Changing only the discovery trigger leaves independently discovered rows outside a later ordinary run's selector. |
| Reusable retry ownership | `internal/queue/retry.go:14–110` acquires the SQLite writer, creates receiving-run membership and claims video sibling groups before evaluation. `113–180` distinguishes live/interrupted owners, failed owners and acknowledgement/requeue. This covers error/deferred work, not arbitrary native pending admission. |
| Run/action authority | `pipeline.go:172–236,243–245` captures configuration at initiation and serializes production through `productionMu`. `authority.go:18–53` validates production type/status/snapshot. `executor.go:177–239` checks exact-target completion and budget before actions. The gate is local, not multi-process protection. |
| Quota | `quota.go:125–151,188–255` stores cost and reads the daily sum; `CanAfford` does not reserve. `youtube/client.go:490–567` calls its recorder only after successful HTTP responses/body reads. Failed/ambiguous requests and a crash before recording can consume provider quota absent from the local sum. |
| Other callers | `cmd/server/main.go:528–559` lists all playlist pages with recording but no preflight admission; `569–604` creates a metadata client without a recorder, while `metadata/manager.go:150–180,238–290` accounts after fetch/storage. A coordinator governing only the two jobs would miss these callers or double-count refresh if both layers record. |
| Metadata readiness | Discovery may stop metadata enrichment for budget (`302–330`) yet persist missing duration as zero with fresh metadata (`345–367`). Production routing falls back to empty metadata (`pipeline.go:589–595`). Delayed processing makes missing/expired metadata a load-bearing correctness seam: unavailable conditions must defer, not silently evaluate fabricated zero/empty values. |
| Scheduling/restart | `scheduler.go:146–164,200–211` dispatches interrupted resumes and new full runs. `state/store.go:129–139` selects **only interrupted**, and inspected startup/shutdown (`cmd/server/main.go:150–249`) does not establish recovery of hard-crash `running` records. Safe restart cannot be assumed from scheduler existence. |
| Current API/simulation | `server.go:408–416,492–511` exposes full Run Now, asynchronous `202`, a 30-minute background execution context, and no registered run-cancel route. `dryrun.go:55–110` previews existing pending rows; it does not discover them. `replay.go:99–148` selects retained observations; it does not populate the native backlog. |

## Alternatives and decision

| Realization | Benefits | Costs / failure modes | Disposition |
|---|---|---|---|
| Keep one full pipeline; add bounded discovery and a discovery-only entry | Smallest change for acquisition without playlist mutation; preserves current full-run path | Still needs durable processing admission and shared request budget enforcement for the complete goal; a coordinator cannot fairly switch work inside an unyielding crawl | Viable for a narrower preview-only goal, insufficient alone for independent queued processing plus responsive coordination |
| Two logical jobs, existing SQLite work state, local coordinator and common API admission | Independent acquisition/processing, recoverable handoff, no additional deployed infrastructure; existing snapshots/queues/history remain useful | Requires genuine admission/checkpoint changes and quota accounting, not merely two goroutines. Exact scheduling/first-use behavior remains unresolved | **Recommend: least complex complete structural direction** |
| Independent worker services plus broker and distributed coordinator | Multiple hosts/parallel workers and independently scaled acquisition/action workers | Adds broker delivery, producer/database dual writes, distributed claims, quota arbitration, credentials in more processes, operations and reconciliation; still cannot make YouTube insertion atomic with SQLite | Not justified by current deployment/volume evidence. Revisit for an accepted multi-process or throughput requirement |

Even within the chosen option, a second general message table is unnecessary. Use existing `video` state for backlog and `run_video_outcome` for durable processing membership. Add only the ownership/job-purpose/checkpoint information the current model cannot represent. A general event log or action outbox is not selected, and would not solve external action ambiguity by itself.

## Structure and ownership

```text
Manual controls / existing schedule / startup recovery
                         |
                Local coordinator
        authorization + backlog + budget + due work
                 /                     \
        Discovery job              Processing job
       page -> SQLite       durable claim -> exclusions
             |               -> duplicates -> routes
             +--> pending             -> actions
                  videos                 |
                    +--------------------+

Discovery / processing / metadata / playlist-picker requests
                         |
            Shared YouTube admission boundary
          reserve in existing ledger -> request -> classify
                         |
                      YouTube
```

### Durable handoff, not a second queue

- Keep one native video identity and its original `discovered_in_run_id`, discovery time and channel. Discovery retries deduplicate on the native video ID and must not reset an evaluated/completed row, duplicate channel counts, revoke imported-reference eligibility or rewrite provenance. A pre-check alone is not a concurrency guard; the persistence transaction/unique identity is authoritative.
- Commit retrieved IDs, available observation data and fingerprints in bounded page/batch transactions **before** later channel calls or optional enrichment can fail. Never hold a SQLite write transaction over network I/O. Metadata readiness is explicit: a listing title is not proof of duration/description availability.
- Processing admission transactionally records its selected video membership **before the first decision**, and excludes work owned by another unfinished processing/full run. Reuse `run_video_outcome` admission rows and the retry writer-lock discipline. Derive active ownership from that membership joined to unfinished processing/full runs; terminal history and discovery-only membership do not confer ownership. No additional pending-owner table or field is selected. The exact owner query/constraints, including arbitration with existing queue owners and failed-run handoff, require the architecture specification before implementation.
- Pending selection must also honor deferred/error ownership: a retry resets global state to pending, so `processing_state='not_yet_evaluated'` alone is **not** production eligibility. Use existing group claims for those queues. An ordinary pending selector cannot steal retrying work, auto-retry active failures, or revive dismissed work.
- A processing run freezes its admitted set; arrivals after selection wait for another run. Reopening the database reconstructs its membership even if zero route decisions were written. Claim and membership cannot be lost when queue ownership later changes.
- Same-run interruption retains the processing owner, captured snapshot and progress. New-run retry of operationally deferred work uses the new run's snapshot under existing authority. Failed-run leftovers must have an explicit durable retained/reclaimable handoff; failed runs themselves are not auto-resumed. Ownership corruption fails visibly, not as permission for another worker.

### Run meaning and compatibility

Reuse existing Run/history storage, with a durable distinction between **job purpose** and trigger/mode. Manual/scheduled are triggers; discovery/processing/full-pipeline describe work. Do not simply add a production-like run type and accidentally admit it through action or statistics allowlists. Exact representation is a follow-up contract, not an enum prescribed here.

Discovery-only jobs cannot authorize playlist actions even if manually triggered. A processing run captures and uses its own snapshot at initiation, not discovery's snapshot or a simulation's decisions. Preserve OC-003's snapshot obligation for Run records unless requirements explicitly change it. Preserve the existing full-run endpoint/purpose until its replacement is approved (BR-017 AC2).

Historical discovery results remain not-yet-evaluated when processing later succeeds; only the later run records that acceptance. Existing `run_video_outcome`, route/action scope, null-versus-zero counts, attempt-only action statistics and vetted diagnostics remain authoritative. Do not turn acquisition completion into processing completion, or a budget stop into `failed`, `completed`, or a fabricated route result without its accepted lifecycle contract. Scoped discovery coverage and its stop reason must be visible independently of routing status. There is no new primary video outcome called queued/paused.

## Bounded discovery and safe watermarks

Two different limits must not be confused:

1. **Coverage policy:** which observations qualify on first use and afterward (all history, an approved lookback, specified channels, or another agreed source set). This changes accepted input scope and belongs to the user/requirements owner.
2. **Execution budget:** how much acquisition work may occur now before retaining a continuation. This changes when work happens, not which eligible work can be discarded.

A video cap alone does not bound the cost of enumerating subscriptions or empty/previously seen pages. A duration timeout is not a quota ceiling. Enforce approved per-job request/unit limits through common admission **before each page/batch call**, as well as the shared daily budget. Report partial scope and the reason work stopped. Do not choose numeric limits or silently interpret a cap as a new lookback window.

Persist scan progress separately from its completed coverage watermark. Advance a channel's completed watermark only after **all observations covered by that advancement are committed**. Partial enumeration or partial persistence cannot advance it past uncommitted IDs; channels with zero new videos still need explicit completion evidence. A durable per-channel work position allows later channels and continuations to receive turns instead of restarting the busiest channel forever.

Provider page-token validity and ordering assumptions need confirmation before the exact cursor protocol is specified. A token is a resumable hint, not a permanent truth. Invalidated cursors must fall back to a deduplicated rescan of still-uncompleted coverage, never mark it complete. Do not stop on the first old timestamp unless the API ordering contract actually makes that safe. Choosing to exclude older history deliberately is a product decision, not watermark recovery.

## Coordinator: scheduling is separate from admission

### Minimal local coordinator

Reuse the existing scheduler as the due-work timer and existing ledger for budget history. Put dispatch decisions in one process-local owner shared by manual controls, scheduler, queue retry and restart recovery. Start with **serialized production jobs/committed work quanta**, preserving current no-concurrent-production guarantees; independent control does not require simultaneous workers. Do not fork the coordinator into another service.

Before dispatch, consider authorization, due time, runnable distinct-video backlog, oldest waiting work, incomplete channels, metadata readiness, budget and provider block state. Queue size alone is not cost: rejected items need no insert, matches can target multiple playlists, and duplicate targets/completed actions reduce actual work. Existing error/deferred sibling rows must not multiply backlog size. Exclude not-yet-due and user-held failures from runnable counts.

Discovery yields after committed pages/batches; processing yields between admitted videos and safely recorded actions. No repeated retries in one run on insufficient quota. A quantum boundary is a coordinator checkpoint, not permission to replace its run snapshot or recapture its selected count. Bounded request deadlines prevent one stuck network operation from holding the dispatcher forever; exact timeout values are technical configuration to specify later.

### Shared budget admission and restart accounting

All YouTube Data API callers must use one budget authority, including metadata refresh and the playlist picker. The scheduler chooses work; the request boundary alone grants permission to spend. A later manual start cannot bypass a budget refusal. Existing successful-page-only accounting must not run in parallel with new debit accounting and charge twice.

For this deployment, serialize request admission and initially allow only one in-flight YouTube Data API request through that gate. Before sending, atomically check available local capacity and **commit a conservative cost hold** in the existing ledger. Release the SQLite writer before the network call. Completion records the attempt's safe response category; one hold is settled once, not counted both as reserved and consumed. This is a small ledger extension, not another budget database or job queue.

Durable holds earn their place because process death after sending but before accounting must not make possibly spent quota available again. A reservation that is provably never dispatched can be released. A crash/timeout with uncertain dispatch or outcome keeps a conservative debit until a valid quota-window rollover or authoritative reconciliation establishes otherwise. Do not claim that a held upper bound is measured provider usage. Failure to reserve prevents sending; inability to settle/account blocks further spending until recovery instead of silently resetting consumption. Cleanup/finalization needs a bounded context that still works after a work request is canceled.

Every retry is a separately admitted attempt. Quota-exhaustion responses close the relevant local admission gate even if the estimate says units remain, with no immediate quota retry (OC-001). Authorization errors defer affected account-dependent work and request reauthorization; transient/rate-limit responses retain work and respect their retry eligibility. Do not treat every HTTP 403 as quota exhaustion. Attempt cost/category rules must cover unsuccessful, malformed and ambiguous responses; successful-response-only recording is insufficient under the documented minimum cost of invalid requests.

Daily budget and request rate are separate constraints: daily headroom does not override a provider rate limit or its retry delay. The same gate must apply any established pacing/backoff without inventing another quota allocation or busy-looping a throttled job.

The supported claim is **YuHaul will not knowingly admit beyond its configured, conservatively accounted allowance**, not that it knows Google's exact remaining project quota. The inspected client has no authoritative remaining-quota feed. Google is the enforcement authority; its Cloud console reports usage/allocation, and other apps using that project may spend unseen units. Local remaining estimates must expose their source/uncertainty. A reliable future provider/Cloud metric can reconcile an estimate with stated freshness, but cannot be invented or inferred from an ordinary success response.

Use provider-aligned quota windows: the current fallback UTC calendar day (`quota.go:380–389`) is not the documented Pacific reset. The exact rollover contract must handle timezone daylight-saving transitions and requests spanning reset; an in-flight or unresolved request cannot gain a second free allowance by being relabeled into a new bucket. No numeric allocation beyond the actual configured/project allowance is assumed.

### Fairness, backpressure, and stop boundaries

Existing accepted architecture gives eligible deferred work FIFO priority before new discovery. Preserve it unless expressly amended. A recommendation to reserve processing capacity or alternate acquisition and processing is not permission to reverse that policy.

Within the approved policy, the coordinator needs bounded turns rather than an exhaustive discovery loop. Persist the next eligible channel/work position so the same busy channels do not win on every restart. Persistent queue pressure should reduce/hold **new acquisition**, not discard queued work. Persisted backlog, oldest wait, ready versus metadata-blocked counts, budget estimate/holds, next eligible time and stop reason make starvation and backpressure diagnosable without adding telemetry infrastructure.

There is a real policy conflict when queue capacity is exhausted but processing is intentionally disabled: the system cannot both keep discovering forever and keep a bounded backlog. The owner must choose whether acquisition waits, its scope changes explicitly, or the backlog is allowed to grow. No silent eviction, stale metadata use or automatic processing is authorized. Metadata expiry still applies while work waits; refresh under budget or defer required predicates, never extend retention just to preserve a preview.

Throttling controls **new dispatch**, not actions already applied on YouTube. A stop at a safe boundary commits page data, action evidence and retained ownership before yielding. Cancellation cannot guarantee that an in-flight insertion did not occur. Whether a user pause stops only new jobs or also requests an active job to yield, whether it persists across restart, and whether manual processing overrides an automatic hold are open observable policies. No cancel endpoint or new status is invented by this assessment.

### Safe restart

Before new dispatch, reconcile orphaned unfinished jobs, processing memberships, scan checkpoints and budget holds. The single supported writer must be established; memory mutexes do not prove an old process has stopped. A crashed `running` record is not evidence of a live worker, and an `interrupted` simulation cannot be handed to the production executor merely because the scheduler found it.

Resume the appropriate job/mode from its durable state and original snapshot where the accepted recovery contract allows it. Failed runs remain terminal; their retained work can enter a new receiving run only through permitted admission. Rebuild counts/eligibility from authoritative records, not stale in-memory queue sizes. Honor the accepted pause/automation choice before restarting spend; restart must not silently re-enable processing. Mixed-version or multiple active writers remain unsupported; no lease/leader-election service is justified here.

## Simulation and preview boundaries

- Discovery-only mode may write native observations/fingerprints and consume **read** quota; it performs no route actions. It is not a simulation. Empty-database preview requires such acquisition or another explicitly accepted source; importing fingerprint history does not create pending native videos.
- Existing dry-run reads pending rows and writes only its hypothetical run/decision/action evidence. It must not claim, acknowledge, consume or mark production queue work completed. If pending includes processing-owned retry resets, the exact preview selector needs an explicit consistent-view contract; the current bare selector is not a production claim API.
- Backtest/replay remain retained-observation simulations. Backtest execution makes zero YouTube calls and cannot be used by the coordinator as a way to refill backlog. No simulation result suppresses or authorizes production actions.
- A preview performed before later arrivals, metadata refresh or rule changes is not a guarantee of identical production outcomes. If the user wants “process exactly what I approved,” requirements must define the approved input set/configuration/metadata meaning before architecture adds a frozen approval contract. Otherwise label preview as hypothetical, not committed execution.
- Backtest/Replay date-picker semantics and same-app comparison deferral stay separate. A discovery lookback cannot silently wire or redefine those controls.

## Invariants and confirmation

These are required downstream proofs, **not tests executed in this assessment**. Use real selector/job/coordinator/SQLite/registered HTTP/UI composition, substituting only external YouTube via the existing provider seam.

| Guarantee / precondition | Owner and enforcement | Falsification / required evidence |
|---|---|---|
| Retrieved eligible work is durable before downstream failure | Discovery page transaction; coverage checkpoint follows commit | Fail a later channel, enrichment batch and second video/page commit; reopen and recover every earlier observed ID without a watermark skipping uncommitted work. |
| A pending video has at most one active production owner | Shared dispatcher and writer-locked admission with durable membership; all retry paths participate | Race scheduled/manual/queue retry/restart admission; crash before first decision; recover one owner, no orphan or double processing. |
| Discovery identity and terminal history never move | Existing video origin and run-local outcomes | Discover in D, preview H, process in P, retry in Q; D remains pending evidence, H hypothetical, P/Q retain their own outcomes and snapshots. |
| Only the processing run's own snapshot/decisions authorize actions | Existing action authority plus explicit job-purpose restriction | Manual discovery/simulation/foreign decisions cannot insert; edit routes while P waits/resumes and observe its captured targets. |
| Budget is not oversubscribed by local overlap or restart | One common API gate and durable pre-dispatch holds | Contend discovery, processing, refresh and playlist picker near allowance; crash before/after send/record; denied calls never reach provider and uncertain calls do not restore capacity. No duplicate accounting. |
| Budget estimate is truthful about authority | Ledger projection distinguishes known attempts, holds and provider block | Provider quotaExceeded while local estimate is positive closes admission; invalid/failed calls are represented; reset and cross-reset requests do not mint allowance; other-client usage is not claimed known. |
| Bounded turns and backpressure preserve work | Coordinator plus accepted priority/limits; persisted channel/queue position | One prolific channel and sustained backlog across restarts do not silently starve eligible channels/work. Repeat with processing held and full backlog under the user-approved policy. |
| Missing metadata never becomes false rejection | Metadata readiness check before required predicates; quota-aware refresh/defer | Discovery without duration/description, then delayed expiry: required predicates defer, not evaluate invented zero/empty values. |
| No double playlist insertion is inferred from local uniqueness | Existing exact-target completion and serialized executor; separate provider reconciliation seam | Complete target A, defer B, retry/restart with A suppressed and B executed. Separately crash after successful insertion before local action recording; unresolved result is not permission to claim exactly-once. |
| Simulations are non-consuming and isolated | Existing mode gates and requested-run evidence | Nonzero acquired backlog remains pending after dry-run; Backtest makes zero calls; production corpus/outcomes/idempotency/operational stats unchanged by simulations. |
| Stop and restart honor accepted user authority | Durable policy and owner/mode-aware recovery | Stop/timeout during a page and insertion, hard process death with `running` rows, restart while processing is held; no lost work, false completion or unauthorized mutation. |

## Material findings and unresolved decisions

| Finding / source | Disposition and reason | Owner / blocking scope |
|---|---|---|
| First-use scope, per-job caps and which subscriptions/channels/history qualify are unspecified; BR-002 each-run coverage remains accepted | **Route** for accepted behavioral meaning. Execution caps must preserve continuations; lookback cannot be invented as a quota fix | User + specifier; blocks acquisition selection/limit contract, not structural recommendation |
| BR-017 AC2 currently promises a full manual pipeline; new independent controls/coordinator authority are not specified | **Route**: retain full-run compatibility until an amendment explicitly defines replacement/additional commands, schedules and missed-trigger treatment. BR-017 AC7–12 (2026-11-10) add only Run Now resumption of a time-limit-interrupted Run Now run; they do not split the jobs | User + specifier/designer; blocks changed Run Now/schedule behavior |
| May processing auto-start, must each batch be explicitly initiated, or does the user choose a persistent mode? Does startup resume bypass a manual hold? | **Partly answered (user direction 2026-11-10, not yet a requirement):** both jobs run automatically on separate schedules. Pause/hold and startup-resume interaction remain open. The coordinator must still never equate queue size with authorization | User + specifier; acceptance and a BR-016/BR-017 amendment are needed before automatic dispatch is specified; pause/restart policy still blocked |
| Budget shares/reserve, priority changes, fairness objective, backlog ceiling and behavior when processing is disabled | **Partly answered (user direction 2026-11-10):** exhausting the daily quota is acceptable because work continues after the reset, so no reserve is required to prevent exhaustion. Fairness between the jobs, backlog ceiling and behavior when processing is disabled remain open. Existing deferred FIFO priority holds until amended. No numeric thresholds chosen | User; specifier defines observable policy, designer communicates it; blocks exact coordinator policy |
| Does preview promise only no mutations, or approval of an exact frozen batch/configuration? | **Route**: existing simulation satisfies hypothetical preview only; no implicit production approval or same-result promise | User + specifier/designer; blocks any approval/commit contract |
| Buffering/watermark, pending admission, quota race/under-accounting and orphan-running recovery paths in source table | **Accept** as demonstrated structural gaps. Mere job separation cannot satisfy preservation/budget/restart guarantees | Architecture/implementation; blocks relying on the proposed realization until corrected and verified |
| Provider-success/local-record crash window in accepted action authority | **Accept** as existing unresolved technical risk, not an accepted weakening of BR-012. Separate services/outbox alone do not solve it | Architecture/implementation; blocks full crash-idempotency claim; no new user preference required |
| API ordering/page-token recovery details and actual deployed quota configuration/reset/shared clients are unestablished | **Investigate** before exact cursor/budget specification. Report remaining quota as estimate meanwhile | Architecture/provider/deployment owners; blocks exact continuation/authoritative-budget claims |
| Run #14 state and ability to intervene | **Out of assessment**. No production DB/API/process observation or operation; current source lacks a run-cancel route | Dispatcher/operator handles any separate live diagnosis; no action authorized here |
| Existing date selection question and deferred comparison | **Date selection answered 2026-11-10** (BR-027 AC8–13; realized in [run-initiation ADR-002](../../run-initiation/decisions/adr-002-backtest-replay-published-date-range.md)); it is independent of acquisition controls. Comparison remains deferred | Comparison: accepted future scope |

## Consequences, dependencies, and limits

This direction makes first-use acquisition possible without accidentally performing playlist actions and lets processing progress independently of a fresh subscription crawl. It also exposes real costs: native-pending admission, page-level preservation, shared request accounting and purpose-aware recovery are structural work, not a scheduler toggle.

The dependency is semantic, not a task plan: product/requirements must settle scope, automation and observable stop behavior before exact selection/API/scheduling contracts can be specified; interaction design must make discovery, preview, processing, held work and partial completion distinguishable. Existing evidence/authority contracts constrain that specification. Implementation must not depend on unresolved policy. Independent architecture critique and the composition/failure proofs above are recommended before relying on this direction.

Critic session `ses_e5220e9c6ffe6WWPECT1QV53O4` reported **SOUND / no material blocker for the proposed structural recommendation only**, with no runtime tests or file changes. Its earlier automatic context exposed prior conclusions, so this is a qualified critique, not pristine independent rediscovery. Its two clarifications are retained explicitly: historical membership is not active ownership, and quota holds do not prove that a playlist insertion happened. Neither approves unresolved product policies or closes provider-side reconciliation. Fresh independent review remains appropriate before the exact specification is relied upon.

Assumptions retained without escalation: one supported app writer, one SQLite database, authenticated single-user deployment, existing provider seam and no need for horizontal workers. They come from accepted architecture, not a prediction of unlimited scale. Revisit distributed ownership only if deployment/throughput requirements change. No new service, telemetry stack, retention exception, source provider, exact numeric threshold, UI redesign, task breakdown, code change or intervention in run #14 is authorized.
