---
type: specification
title: ARS-002 — Causal Exclusion Capture and Inspection
description: Exact future exclusion-reference persistence, producer, recovery and list/detail contract without historical-input storage or invented legacy causes.
tags: [architecture, exclusions, evidence, snapshots, api, persistence]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted technical contract for the independent future causal-rule slice; unimplemented and runtime-unverified. Configured-value release requires established eligibility, not permission granted here. The generic-only legacy boundary is user-approved; full historical reproduction outside that boundary and provider retention remain unresolved.

New exclusion skips must answer **which rule matched and why this route stopped** in both the run's video list and Decision Detail. The matcher returns the first actual matched occurrence in the run's unchanged configuration; that reference commits with the decision. Inspection resolves the reference locally. It does not rerun matching or retain the compared provider value.

For example: “New Listens — skipped: exclusion #3 in configuration 42 matched; title contains configured phrase ‘live’; conditions were not evaluated. Compared input unavailable.” Show the phrase only if permitted. This explains the recorded branch, not what title was compared, independent reproduction, or the whole video's outcome. The user accepts existing generic-only history remaining explicitly incomplete and displaying “rule unavailable” in list/detail, never inferred or backfilled. Every new exclusion still requires actual causal capture, even for a video with old history.

## Derived from

- [BR-015 — Explainable Route Decisions](../../../requirements/yuhaul/br-015-explainable-route-decisions.md), especially AC10–13 and unchanged AC1–9.
- [OC-005 — Decision Evidence Attribution and Availability](../../../requirements/yuhaul/oc-005-decision-evidence-attribution-and-availability.md), Guarantees 4–12 and verification cases 6–10.
- [BR-007 — Exclusion Behaviors](../../../requirements/yuhaul/br-007-exclusion-behaviors.md), [OC-002 — Outcome Semantics](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md), and [OC-003 — Configuration Snapshot](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md).

## Implements

- [ADR-001 — Causal Exclusion Snapshot Reference](../decisions/adr-001-capture-causal-exclusion-snapshot-reference.md): snapshot-occurrence identity and version-1 comparisons are unchanged.
- [ARD-001 — Evidence and Metadata Lifecycles](../design/ard-001-run-attributed-evidence-and-metadata-lifecycles.md): independent rule identity, atomic capture, safe local inspection and retained blockers.
- Existing [run-video API](../../ars-001-route-and-playlist-api-contracts.md) and [route UUID contract](../../ars-004-route-uuid-schema-api-migration.md). [ARS-001 — Current-Local Metadata](ars-001-current-local-run-video-metadata.md) remains separate and unchanged.

## Satisfies

These allocations are not implementation-completion claims.

| Obligation | Realization / remaining boundary |
|---|---|
| BR-015 AC3/5/10; BR-007 AC8; OC-005 G4–5 | Actual first occurrence, run-snapshot binding and same-row durable capture through both producer paths. New generic-only skips fail conformance. |
| BR-015 AC11; OC-005 G8–10 | Same typed causal projection in list/detail, permitted configured value and explicit missing/withheld states. Positive value release still needs eligibility evidence. |
| BR-015 AC12; OC-005 G6–7/12 | Existing video outcome, individual route dispositions, conditions and actions stay distinct; simulation remains hypothetical. |
| BR-015 AC13; OC-005 G9, cases 9–10 | Realize the user-approved generic-only legacy boundary: preserve old rows and supported outcomes, show “rule unavailable” in list/detail, never guess/backfill. Reuse stays incomplete; new decisions capture their own cause. Not reproduction or implementation completion. |
| OC-003; BR-015 AC1/8/9; OC-005 G1–3/11 | Reuse snapshot, public UUID, membership and local-only inspection. Recognition/read recovery are satisfied elsewhere by ARS-001 and consumer repair; preserve them. |
| BR-015 AC7 outside AC13's accepted legacy boundary; BR-009 historical titles; OC-005 evaluated-input/reproduction | **Blocked upstream.** No raw-input capture, retention period or complete reproduction is specified. Accepted legacy incompleteness does not recover input. Existing condition-audit lifecycle is not repaired here. |
| BR-015 AC4/6; broader BR-028 inspection | Satisfied elsewhere by duplicate/history contracts or still outstanding there; not delivered here. Date selection/comparison features are outside scope. |

## Structural constraints and ownership

Use the existing process, state transaction, SQLite decision row, immutable configuration and two run-video endpoints. No new service, evidence ledger, outbox, cache, exclusion UUID, full snapshot response or provider-input archive. The ADR considered stable UUIDs and copied predicates; neither earns its added identity/retention machinery for a within-snapshot occurrence.

Configuration owns immutable array order. Router owns first-match selection and semantics. Pipeline binds evaluated routes to the run snapshot and preserves the match through the all-route gate. State owns atomic insert/reuse/readback. Server owns resolution and safe domain projection. Browser owns faithful mapping/rendering, not inference. These are component allocations, not a sequenced implementation plan.

## Persistence contract

Add these nullable scalar fields to `route_decision` in a new forward migration. No migration number is reserved while other changes are in flight; never edit an applied migration.

| SQLite column | Internal `state.RouteDecision` field | Contract |
|---|---|---|
| `exclusion_index` | `ExclusionIndex`, nullable integer | Zero-based nonnegative position across the **complete** snapshot route's `exclusions` array. Zero is valid, not absence. |
| `exclusion_semantics_version` | `ExclusionSemanticsVersion`, nullable integer | Positive interpretation version; corrected producers emit `1` only. |

Both fields are null or both present. Present fields require `decision = skipped` and `skip_reason = exclusion`. Enforce local shape at every insertion boundary and in database constraints; equivalent insert/update validation is sufficient if otherwise a table rebuild is needed. Cross-snapshot validation belongs to the producer, not JSON-reading triggers. Readers still handle malformed historical/external rows defensively.

Keep existing unique `(video_id, run_id, route_id)` identity. The run's `config_version_id` supplies configuration; do not duplicate it on the decision. Store no type, configured value, actual input, source timestamp, substring, hash, match score or result matrix in this extension. The skip plus valid reference is the recorded causal match; another Boolean is unnecessary.

Migration initializes every existing row null/null without reading metadata/rules; preserve IDs, outcomes, audits, actions, snapshots and uniqueness. Update both Store decision queries, shared scanner, direct insert and transaction insert/conflict-read column lists together. Schema upgrade alone proves no capture coverage.

## Producer and reuse contract

1. Prepare routes from the **selected run's linked immutable snapshot**, retaining every exclusion entry and its array order, including unknown/unparseable entries. Route sorting must not reorder exclusions. Validate unique route identity and binding before capture. Live child IDs, current rules or equal-value searches cannot establish occurrence identity. No new matching priority is authorized.
2. Router's exclusion operation returns no match or the actual `exclusion_index` and semantics version `1`, alongside the existing exclusion reason. Return at the first actual match using ADR-001's exact comparisons. This can enrich `Excluded` or use a shared match operation beneath it; keep one predicate implementation. No actual provider value belongs in this result.
3. Ordinary `evaluateRoute` attaches the match to its skipped decision before persistence. Disabled/final skips have no reference. Exclusions create no condition audits; their absence is not condition failure.
4. The gate carries route-plus-match results from `enabledRoutesThatExclude` (or replacement) into production `markVideoExcluded` and simulation inserts. Never drop the result and rerun matching or scan for equal text. Every newly skipped enabled route has its own cause. Partial-video survival checks publish no route decision; ordinary routing later captures its own actual comparison. Separate existing metadata reads do not become one historical-input snapshot.
5. Previously persisted exclusions used in gate recovery are **stored** results, not fresh matches. Preserve original reference/null even if today's input no longer matches. Never append a stored-only route to a fresh-match list and fabricate its index. Preserve downstream recovery and video classification.
6. Both gate paths use `RecordRouteEvaluation` with empty audits, or its same atomic insert/reuse implementation. Plain generic-only `RecordRouteDecision` cannot remain a bypass. All corrected paths, including `ExpectedEvaluations` when creating missing legacy-recovery decisions, supply valid references for **newly inserted** exclusions.
7. Decision, skip reason and fields commit together. Validate snapshot resolution before insert; invalid/missing snapshot, route, index or semantics uses existing processing-failure handling, never successful precise evidence. Missing initiation snapshot retains OC-003's deferred/not-executed rule; no new lifecycle policy. Remove mutable-route fallback for new simulation evaluation. Historical inspection can still show incomplete context. `Engine.Evaluate` cannot create a new run-bound reference from unrelated live routes.
8. On tuple conflict return the existing persisted row, never the losing candidate or an enrichment. All successful contenders observe the persisted winner. SQLite/cancellation errors remain errors under existing failure/retention handling; no new retry/coordinator. Reusing a legacy generic row is not a new captured decision and is not complete evidence.
9. `checkedLegacyEvaluation` compares whole decisions with `reflect.DeepEqual`. New expected pointers must not reject otherwise valid old rows. Preserve all preexisting admission/authority checks; compare their pre-extension fields unchanged, separately validate any stored new reference, and neither fill null fields nor use recomputed pointers as history. Missing decisions created from a fresh actual comparison receive capture. Do not widen production-action authority or skip existing input/audit checks.

### Recording preflight: exact snapshot binding

An index is meaningful only in the array actually matched. Checking that index zero exists in the snapshot is insufficient: a caller could have matched a different rule at index zero. Before either recording entry point matches or writes anything, validate its complete evaluation route set against the selected run's persisted snapshot. For example, an explicit route with the correct ID but reordered exclusions must fail before it records any decision, even if both arrays happen to match the video.

**Selected interface:** a synchronous, read-only `ValidateRunRoutes` dependency taking context, run ID and the proposed ordered `[]router.Route`, returning success or error. Pipeline supplies this dependency to the recording Engine and uses the same validator directly for its exclusion gate and missing-legacy-decision producer. Router declares the dependency without importing pipeline/configuration. No validation token, new store, per-route provenance field or second matcher is needed.

- `Engine.EvaluateWithRoutes` invokes validation once at entry, before its first predicate, decision or audit. A missing validator is an error, not permission to record unbound decisions. `Engine.Evaluate` retains its live-route convenience API but delegates through this guarded entry; live routes succeed only when their effective definition equals the run's snapshot. It must not silently relink the run or reinterpret an index against current configuration. `Routes`, `Excluded`, `ExclusionMatch` and non-recording `ExpectedEvaluations` remain useful without this dependency; their outputs alone do not authorize persistence.
- Pipeline's validator rereads the persisted run by ID; it does not trust a caller-supplied run/configuration ID. For production it retains `productionAuthority` and its existing run-mode/status/version checks. For simulation it loads that persisted run's linked configuration using the snapshot-only loading/validation path. Missing run/link/configuration, decoding failure, malformed required fields or ambiguous route identity returns an error. Existing mode-specific preparation/order stays unchanged; this is not permission to introduce another route priority.
- Compare the **complete ordered effective route definition**, not just route IDs or exclusion counts: each route's internal identity, evaluation order, enabled/final flags, condition composition, ordered conditions (index, field, operator, value), ordered actions (index, type, target), and complete ordered exclusion type/value pairs. Include every other field actually used by evaluation or action selection if the model gains one. Ignore storage-only child IDs, timestamps, current labels and public-ID decoration; they are not evaluation identity. Nil versus empty child lists are equivalent. Unknown types and unparseable thresholds retain their original positions and spelling; existing matcher semantics still treat them as nonmatches. Missing/null required type/value is not equivalent to a present empty string. Snapshot validation must occur before conversion loses that distinction. Normalize only as already required by the relevant mode's route preparation; never sort exclusions or use equal-value lookup.
- Compare and evaluate the same route data. The recording call owns its route slice and nested slices for its duration; callers must not mutate them concurrently or between preflight and use. No shared mutable route cache is introduced. The accepted immutable snapshot/run link is the authority, so no long-held database transaction or lock across matching is needed. This does not defend against hostile code or direct database tampering inside the process.
- `runExclusionGate` validates once at entry in **both** mutate modes, before metadata matching, decisions, video transitions or broadcasts, even if the input video slice is empty or stored decisions may be reused. Its production legacy-admission check remains; merely calling `productionAuthority` and discarding its routes is not validation. `enabledRoutesThatExclude` and the ordinary evaluator use the same `ExclusionMatch` operation on the validated arrays. Carry the returned first occurrence unchanged to insertion; do not rematch during validation or insertion.
- `completeLegacyEvaluation` validates its route set before `ExpectedEvaluations` can produce any missing decision for insertion. The non-recording helper itself need not load a snapshot. Existing stored rows, including null legacy references, retain the original reuse/readback contract. Validate any present recovered reference structurally against the snapshot; do not demand that it equal a new first match against changed metadata, enrich a null row, or bypass existing authority/audit/input checks.
- State/Store continue to own local reference shape, atomicity and conflict-winner readback. They do not load configuration or reconstruct the evaluated video to prove a first match. A valid reference pair is not proof of snapshot binding. Preflight failure propagates through existing processing-failure/retention handling; no successful decision, condition audit, action or whole-video exclusion may be attributed to the rejected call. Existing initiation deferral and historical incomplete-read rules remain distinct and unchanged.

**Why this boundary:** pipeline-only checks leave the public recording Engine bypassable, while state-only index validation lacks the evaluated array and input needed to establish causality. A callback at the existing recording entry and the gate closes those demonstrated seams without moving configuration into router/state, changing predicate semantics, or introducing a capability/lifecycle framework. A snapshot-owning evaluation object would also work but adds a second representation and migration burden without a demonstrated benefit here. This is an exact realization of the existing pipeline-binding/router-matching allocation, not a new product guarantee.

**Required focused proof (not executed here):**

1. Direct recording Engine calls reject absent validation, missing run snapshot and same-ID mismatched routes before any decision/audit write. Mutate exclusion order, insert/remove an unknown entry, substitute a same-index value, omit/duplicate a route, and change enabled/final/order/conditions/actions. Equivalent effective definitions with different storage-only child IDs and nil/empty lists succeed. Live configuration edits cannot change a historical reference or authorize a mismatching `Evaluate` call.
2. Run the real validator with persisted run/configuration and migrated SQLite through ordinary manual/scheduled routing, production all-route gate, dry-run gate/routing, replay/backtest gate/routing and retry gate. Inject mismatched arrays into each recording seam; assert no new decisions/audits/video-excluded transition/action from the rejected call. Test malformed snapshot fields as well as missing link; do not replace the real validator with an always-success fake in composition tests.
3. On the same valid snapshot, exercise two matching occurrences (including identical entries), mixed types and unknown/unparseable entries before the match. Both ordinary and all-route producers persist the first full-array occurrence. Partial exclusion survival publishes no premature decision; ordinary routing later captures its actual match. Reopen SQLite and resolve the persisted reference against that run, not mutable routes.
4. Resume/reuse unchanged generic-only legacy rows and supported captured rows after current metadata changes. Keep nulls and stored references unchanged; create any missing decisions only from freshly matched validated snapshot routes. Existing legacy admission, condition audits, outcomes, final-route behavior and simulation isolation must retain their established tests.

## HTTP schema and shared projection

Keep `GET /api/runs/{id}/videos` and `GET /api/runs/{id}/videos/{videoId}`, envelopes, membership, outcome filters, paging and `Cache-Control: no-store`. Add these fields to **every** `route_decisions[]` item in both responses, using one shared causal resolver. Detail retains existing audits/actions.

| Field | JSON type / values | Meaning |
|---|---|---|
| `route_name_source` | `run_snapshot`, `current_local`, `unavailable` | Attribution of existing `route_name`; snapshot only for an unambiguous selected-snapshot route, unavailable for absent/empty label. Permitted current fallback stays current/local. UUID remains authoritative. |
| `causal_exclusion` | object or null | Null for non-exclusion decisions, including disabled/final skips. Required object for `skipped` + `exclusion`, even if capture absent. |

Every non-null causal object has **all** keys below; null keys are not omitted:

| Key | JSON type / values |
|---|---|
| `status` | `available`, `not_recorded`, `definition_unavailable`, `unsupported`, `invalid_reference` |
| `config_version_id` | positive integer or null; selected run's linked version only |
| `exclusion_index` | nonnegative integer or null |
| `semantics_version` | positive integer or null |
| `exclusion_type` | `channel`, `video`, `title`, `word`, `duration_min`, `duration_max`, or null |
| `field` | `channel_id`, `video_id`, `title`, `duration_seconds`, or null |
| `comparison` | `exact`, `trimmed_equal_fold`, `lowercase_substring`, `strict_less_than`, `strict_greater_than`, or null |
| `configured_value` | original configured string or null; never evaluated input |
| `configured_value_status` | `available`, `withheld`, `unavailable` |
| `matched` | true for a resolved supported causal match; otherwise null, never false |
| `compared_input_status` | `unavailable` for this reference-only contract |

`available` means **resolved rule identity and supported interpretation**, not complete historical evidence or automatic value release. Value status is separate. Valid recorded index/version remain visible when definition is missing; do not expose malformed scalars. Requested run/video plus route UUID supply the rest of identity. Human ordinal is `index + 1`, labeled as that run's configuration occurrence, not a current editor position.

### Resolution and nullability

| Stored/read condition | Projection |
|---|---|
| Both fields null | `not_recorded`; index/version/type/field/comparison/value/matched null; value unavailable. Run config ID may remain known. Says absence of this capture, not why absent. |
| Malformed pair, negative/noninteger index, nonpositive version, impossible decision/reference shape | `invalid_reference`; valid identity scalars only, no predicate/value/matched. No coercion to index zero. |
| Positive unsupported semantics version | `unsupported`; valid identity scalars only, no predicate/value/matched. Never apply version 1. |
| Snapshot absent/undecodable, referenced route absent/duplicate | `definition_unavailable`; valid reference scalars remain, no predicate/value/matched. No mutable-rule fallback. |
| Snapshot route available but index out of range | `invalid_reference`; no predicate/value/matched. |
| Referenced type unsupported | `unsupported`; reference scalars only, no echo of arbitrary type/value. |
| Supported version/type and in-range occurrence | `available`; type/field/comparison and `matched: true`; value follows separate release status. |

Validate pair shape, then version, snapshot/route/index/type in that order. Non-exclusion decisions emit null causal object even if invalid stored fields exist; flag integrity safely without inventing an exclusion. Unresolved objects use value status `unavailable`, not `withheld`. A resolved existing value deliberately suppressed for unsafe/unknown eligibility is `withheld`. Snapshot missing a usable value is `unavailable`. A captured duration with a threshold unparseable under version-1 integer semantics is `invalid_reference`, not another comparator. Channel/video map to `exact`; title to `trimmed_equal_fold`; word to `lowercase_substring`; duration minimum/maximum to `strict_less_than`/`strict_greater_than`. Units are seconds; a permitted configured string retains its spelling.

Absent/undecodable snapshot is incomplete history, not read failure. Actual database/query/scan/iteration failure uses existing mandatory-evidence HTTP 500 and safe error, ending loading with retry; never return `not_recorded`/empty success. Missing route UUID mapping retains ARS-004 endpoint failure. Existing 400/404 are unchanged. Optional local metadata failure remains ARS-001's successful evidence response with context-read failure. No new HTTP status/refresh endpoint.

Load/parse the snapshot once per request and share its validated route map for labels/causes. No per-video/route snapshot reads or browser configuration fetch. Do not scan other runs, adjacent condition actuals or Store A for a cause. No persistent projection cache.

## Configured-value release boundary

Only the selected predicate crosses this new boundary. Structural type/comparison/reference carries no compared input; configured text can. Server owns a shared list/detail domain-evidence decision from `(selected snapshot, route occurrence, typed value)` to permitted original value or explicit no-value status. Generic diagnostic normalization is not a value transformer. Do not silently truncate/mask into a different rule or declare printable text safe. Browser renders released text as text, never HTML, and does not recover withheld values elsewhere.

Positive eligibility must establish privacy/provider-lifecycle obligations for the value's actual origin. Being user-configured or not matching a secret pattern is not proof. `SnapshotExclusion` currently has type/value only, no origin/deadline/eligibility. **This contract invents neither that missing authority nor a text-classification algorithm.** If existing authorized policy cannot prove eligibility, withhold value and preserve structural cause. This safely implements identity/availability, but does not prove permitted-value display fulfilled. Integration must obtain a bounded compliance/safety decision for positive release before complete configured-value-display acceptance; source-content-free capture and withholding-capable consumers need not wait. No new provenance schema, retention permission, cleanup or blanket snapshot exemption is selected here.

Do not add provider copies, snapshots, predicates or secret-bearing values to events/logs. Existing diagnostics carry only safe category/phase/operation. This extension does not certify or repair preexisting raw condition-audit release/storage; its separate confirmed lifecycle risk remains unresolved in the ARD.

## Browser compatibility and presentation

`web/src/api/run-videos.ts` must retain list `route_decisions` rather than drop them; detail-only repair fails BR-015 AC11/OC-005 case 6. Extend types/mapping with the same causal object and label attribution. List summary need not pretend it has audits/actions: preserve absence, never invent action rows. Share interpretation/copy across surfaces.

Whenever list/detail presents an exclusion, show affected route UUID/attributed label, exclusion reason, supported snapshot occurrence, comparison meaning, permitted value and explicit compared-input absence. List can abbreviate but must identify the available cause without opening detail. Do not replace it with “Skipped—excluded”, hide other presented routes' available causes behind the first, or call all configured rules matches. Detail states the recorded match prevented this route's conditions. Preserve overall outcome separately, including rejected with a non-match and another exclusion, or accepted with another matched route. Infer no target/action for an excluded route. Simulations stay hypothetical using persisted mode; unknown mode never defaults to production.

For `not_recorded`, say exclusion recorded and “rule unavailable”, and identify the explanation as incomplete in list/detail. This realizes BR-015 AC13's accepted old generic-only boundary; it is not permission for a new producer to omit capture. For missing definition/unsupported/invalid reference, distinguish supported captured identity from unavailable interpretation; do not say nothing was captured. Withholding leaves supported type/comparison/occurrence visible. Input status says this projection supplies no historical input, not empty, zero or known expired input. Current/local title stays separate under ARS-001. A later authorized historical-input contract must provide separate capture/release attribution, not borrow another route's audit or reinterpret this status.

New servers emit fields as above. New consumers of old responses treat missing causal field as unavailable, not captured. Unknown status/version/comparison is incomplete/unsupported, never today's interpretation or a crash; retain known disposition/valid identity, not unknown raw text/value. Ignore unrelated optional keys. Old clients can ignore additive fields but **do not** satisfy the new visibility requirement; acceptance requires producer/server/SPA composition. Old-writer redeployment can create null skips and is not a conformant operating state. Do not automatically remove captured columns/evidence for rollback; retain schema/readability or use a forward fix. No destructive rollback/legacy rewrite is authorized.

## Verified source baseline and change surfaces

Read 2026-11-07 in the shared dirty tree, not a frozen revision. Semantic lookup failed with degraded gopls (`Option is not a function` in corpus import); bounded reads supplied evidence. No code, migration, tests, browser or live operations were performed.

| Boundary | Source facts / implementation scope |
|---|---|
| Snapshot/run binding | `config/models.go:28–61`, `config/service.go:210–239`, `pipeline/dryrun.go:240–275` preserve complete arrays with no child identity/provenance. `pipeline/authority.go:40–50` sorts routes/conditions/actions, not exclusions. `dryRunRoutes:115–127` has an unsuitable mutable fallback. Paths are under `internal/`. |
| Predicate/gate | `internal/router/engine.go:135–222` loses the matched occurrence; `ExpectedEvaluations:26–55` has an all-excluded shortcut. `internal/pipeline/exclusion.go:35–110,124–166` drops matches and uses plain inserts in both modes. `dryrun.go:129–163` and `replay.go:152–190` invoke gate then router. |
| State/recovery | `internal/state/{models.go:173–183,machine.go:289–372,store.go:488–559,1021–1039}` and unique tuple in `internal/store/migrations/001_initial_schema.up.sql`. `internal/pipeline/authority.go:162–195` compares whole structs and creates missing legacy evaluations; preserve admission authority. |
| HTTP/SPA | `internal/server/run_handlers.go:83–90,136–145,373–429,483–567` lacks causal fields/label-source attribution, already distinguishes snapshot decode absence from DB failure. `web/src/api/run-videos.ts:14–30,70–87,115–126` drops list decisions. `DecisionDetailView.tsx:32–55,138–200` shows generic exclusion. Run Detail consumes mapped `VideoOutcome` rows; update both views, types/mappers, not the diagnostic classifier. |

These are narrow ownership surfaces for the integrating task-manager, linked by one contract; not assigned tasks or implementation authorization.

**Focused source recheck, 2026-11-07:** `internal/router/engine.go:85–122` now captures match indices but still records live/caller arrays without binding validation. `internal/pipeline/authority.go:34–55` and `dryrun.go:115–126,240–264` now reject missing snapshots/malformed fields before conversion; those fixes do not protect caller-array recording entry points. `pipeline/exclusion.go:35–47` discards the routes reread by production authority, and simulation has no equivalent preflight. `authority.go:135,166–221` uses non-recording expected results for recovery/missing inserts. The preflight section closes this exact contract gap; implementation and runtime proof remain outstanding. Semantic lookup was degraded (`gopls` reported a test transport as not a function), so bounded source reads supplied these observations. The earlier table is the historical planning baseline, not a claim that its already changed omissions still exist.

## Verification and composition

Required proof, **not executed tests**. Distinguish identity, positive value-release permission and full reproduction in verdicts.

| OC-005 case / seam | Falsifiable proof |
|---|---|
| 6 — modes/semantics | Real manual/scheduled and every dry-run/replay/backtest producer → migrated SQLite → reopen → registered HTTP list/detail → real-client SPA. Channel/video/title/word/min/max causes retain actual index/config/route/version/comparison and permitted value. Test trim/Unicode fold, substring not whole-word, zero/absent duration, strict equality, invalid thresholds and unknown entries. New generic-only skips fail. |
| 7 — occurrence/order/both paths | Two matches, identical type/value occurrences, mixed types, unknown entries before match; fully/partially excluded inputs. Actual full-array first index, never type/equal-value guess. Edit/reorder/delete current rules and rename/delete route afterward: old cause unchanged. Disabled/final skips have null causal object. |
| 8 — scope/outcome | Overall rejected with A condition non-match and B exclusion; repeat with C matched/simulated action. Both surfaces explain A/B independently, preserve video outcome, never infer all-route rejection/target or failed unevaluated conditions. |
| 9 — honest limits | Null old rows survive upgrade/reopen/resume unchanged despite one plausible rule; list/detail display “rule unavailable”, identify incomplete explanation and preserve supported skip/overall outcome. This passes the accepted legacy boundary, not reproduction. Missing/corrupt snapshot, duplicate/missing route, index errors and unsupported version/type differ from DB read failure; legacy acceptance does not excuse losing a new rule. Withheld/unknown-eligibility value retains identity, current title never becomes historical input. Exercise null/discriminator combinations and old-server/unknown extensions. |
| 10 — atomicity/reuse | Fail/cancel before commit; race different indices for one tuple. Successful callers/readers see only persisted winner, never generic-first/mismatched cause. Legacy reuse stays null without authority regression; newly inserted missing decisions capture their own cause. A new decision for a video with generic-only old history still captures actual first occurrence/comparison/permitted value. Failed writes use existing handling. |
| Safety/isolation | Separate actual-input and credential/body sentinels from permitted ordinary Unicode configuration. New columns/causal responses/events/logs contain no actual input or withheld value; safe permitted rules remain understandable. Zero provider calls in Backtest/inspection/retry; unchanged production corpus/idempotency/statistics from simulation/inspection. Does not prove old audit lifecycle repaired. |
| End-to-end consumer | Real list mapping/page/filter retains all presented route causes, including multiple exclusion routes. Direct detail/failure/not-found/retry, optional context failure and late responses preserve identity and terminal feedback. Shipped real-client build plus actual persisted producer output, not only shaped UI fixtures. |

## Remaining owner decisions and limits

- **Resolved legacy scope:** BR-015 AC13 and OC-005 Guarantee 9 record the user's “Yes, that is ok” to old exclusion records saying “rule unavailable”. Existing generic-only rows remain explicitly incomplete and unchanged in list/detail, never reconstructed, including on reuse. No specific historical cause or recovery ability is established. Every new exclusion still requires actual causal capture. No further legacy-scope decision blocks this contract; the acceptance neither reproduces input nor authorizes retention.
- **Compliance/safety owner and integrating architect:** positive permitted configured-value release basis. Current snapshot model does not prove origin; unknown eligibility is withheld, not approved. Blocks complete positive-value-display acceptance, not reference capture. No provenance/retention expansion is authorized here.
- **Existing compliance/provider and product owners:** full historical-input/BR-009 retention and old audit lifecycle remain unresolved. A pointer/unavailable message is not AC7 reproduction; no requirement is weakened.
- **Designer:** final layout/copy/accessibility within these meanings. Independent critique of this exact contract is recommended before reliance; documentation conformance proves neither runtime behavior nor policy permission.
