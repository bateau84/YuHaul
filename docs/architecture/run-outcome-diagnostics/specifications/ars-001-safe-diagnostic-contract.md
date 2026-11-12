---
type: specification
title: ARS-001 — Safe Diagnostic Facts Across Producers, Storage, API and Logs
description: Closed diagnostic vocabulary, typed error classification, legacy admission and sink rules for run, video, action and queue failures without releasing arbitrary error text.
tags: [architecture, specification, diagnostics, safety, run-history, compatibility]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** decided technical contract; implementation and conformance verification outstanding. This realizes the accepted diagnostic boundary; it does not certify the current implementation.

Every layer must agree on which error facts may leave process memory. Keep the known operation, phase, HTTP status, registered provider reason and registered database code. Never publish an error's prose merely because it is short or printable. The same rule applies before the first log call, before a database write, and when reading old records.

The decided [authorization/call-safety ARS](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) extends this vocabulary for accepted BR-034/OC-006: auth operations/codes, authorization deferral and uncertain mutation. Those extensions are target contracts, not implemented by this documentation update. That ARS also covers standalone auth surfaces previously outside this run-only contract; no whole-application confidentiality certification follows.

For example, a real SQLite writer conflict while evaluating a video can become `diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_BUSY`. It must not retain `evaluate video <identifier>: router: record decision...`, SQL, parameter values or the driver's error message. An unrecognized historical chain is withheld, with the stored status/known phase and an unavailable-reason flag. No historical cause is reconstructed from a substring.

## Derived from

- [ARD-001 — Durable Run Diagnostics and Simulation Input Visibility](../design/ard-001-durable-run-diagnostics-and-input-visibility.md), especially Failure diagnostic boundary, Invariants and Security / trust boundaries.
- [BR-032 — Processing Outcome Distinction](../../../requirements/yuhaul/br-032-processing-outcome-distinction.md).
- [OC-002 — Route Evaluation Outcome Semantics](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md).
- [OC-001 — YouTube API Quota and Failure Semantics](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md).
- [ARS-001 — Route, Playlist, and Run-Outcome API Contracts](../../ars-001-route-and-playlist-api-contracts.md).
- [ARS-006 — Run-Attributed Production Outcomes](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md).

## Satisfies

| Authority | Disposition |
|---|---|
| ARD-001 safe source/write/projection/log invariant | Realized here by exact admission and ownership rules. Existing ASCII/marker checking is not conformant. |
| BR-032 AC1–4, AC6; OC-002 Guarantees 1–10 and Failure semantics | Preserved, not redefined: a diagnostic explains an existing outcome; unavailable detail does not change failed/deferred/rejected/accepted/excluded/not-yet-evaluated classification. OC-002's internal error reason is a safe error code and known operation, not an obligation to expose raw exception text. |
| BR-032 AC5; OC-002 duplicate/provenance and evaluation-path obligations | Satisfied elsewhere by run-scoped evidence and inspection contracts; unchanged by diagnostic release. This specification does not certify their implementation. |
| OC-001 provider/quota semantics | Preserve recognized quota/status/reason evidence without changing quota deferral, retry, accounting or external-state guarantees. Full all-call accounting remains outside this artifact and unresolved elsewhere. |
| ARD-001 historical compatibility | Preserve rows unchanged; project only admitted facts and explicit unavailable states. No redaction/backfill migration. |

## Implements

The existing SQLite Run/video/action/queue model and public endpoints remain in place. A shared diagnostic policy replaces `internal/diag`'s permissive text test. No service, event ledger, database column, log collector or new error queue is introduced.

## Scope and structural constraints

1. Covers error-derived run reasons, video errors/operational deferral reasons, action errors/disposition reasons, queue messages/details, run events, and logs on those processing/inspection paths, including provider/metadata/discovery calls made by them. Include failure-finalization and retry-error paths, not only the main phase failure.
2. OAuth/token errors encountered on a run path are included. Standalone OAuth callback/import/settings behavior is not silently certified by this contract; adjacent raw-error findings must be routed to the integrator.
3. Do not include raw provider IDs, bodies, token/header material, URLs, hostnames, paths, SQL, table/constraint names, parameter values, provider message/description text or panic values in diagnostic summaries or diagnostic log attributes. Local numeric run/video/queue IDs may correlate logs. Existing authorized API identity fields such as `video_id`, `external_video_id` and `target_playlist_id` remain domain evidence, not diagnostic interpolation; this contract does not remove those fields.
4. Existing domain evidence and user-authored configuration text are not generic error diagnostics. Do not run route names, exclusion predicates, condition audit values or accepted decision explanations through a diagnostic allow-list and erase their meaning.
5. Classification must not alter run status, video/action outcome, attempt timestamp, retry ownership, counts, simulation isolation or recovery authority. A timeout diagnostic does not prove a provider mutation did not happen.
6. An unknown error cause can still supply a nonempty safe failure diagnostic, so classification itself does not prevent retaining work. Invalid nonempty failure prose is replaced by a safe unknown diagnostic. This does not waive ARS-006's required-reason validation: a missing required reason or an invalid required disposition enum remains a recording error handled by existing retention/recovery, not permission to invent a disposition.

## Decision and alternatives

**Choose typed classification plus a closed, canonical string in existing diagnostic columns, with independent write and read guards.** This preserves useful facts without a schema migration and lets legacy readers reject unknown text. Structured facts exist in memory; only their canonical rendering crosses existing text fields.

- Printable-ASCII/length/marker filtering loses: a plain token, external ID or SQL error chain can pass. Regex-shaped provider reasons have the same problem.
- A projection-only allow-list loses: new database rows and earlier source logs still leak, and typed SQLite/provider facts are lost before projection.
- One generic message everywhere is safe but loses known SQLite/status/reason facts that the accepted diagnostic design requires.
- New JSON columns or a general error envelope on every API lose: the existing short text fields and flags can carry this bounded contract. Revisit only for an accepted requirement for independently queryable/multiple causes, not for hypothetical future extensibility.

## Interface and vocabulary

### Shared operations

The policy owns four operations, whatever their final Go identifiers:

| Operation | Input | Output and obligation |
|---|---|---|
| Classify | One error cause and caller-supplied phase/operation enum values | Valid diagnostic facts. Never call `Error()`, `String()` or format the cause to discover or copy its text. Unknown values become `unknown`. |
| Encode | Validated facts | Exactly the v1 rendering below, at most 200 ASCII bytes. No caller-supplied fallback prose. |
| AdmitStored | Nullable/empty or nonempty stored string, plus field kind | Admitted canonical facts/registered reason, absent, or unavailable. Whole-input matching only; no searching or scraping chains. |
| GuardWrite | Proposed diagnostic text/facts and field kind | Valid canonical text/registered reason, unchanged absence, or a safe unknown diagnostic. Every supported write path applies it. |

`Reason(err, fallback string)` may not retain its current arbitrary-text semantics. Any compatibility helper must translate an exact registered constant to enum facts and then use this contract; an arbitrary fallback is not a release capability. `Safe` may remain only as an exact-admission predicate, never as a character-shape test. `Public` must validate its fallback too.

### Canonical failure diagnostic

Exact grammar (single ASCII spaces, case-sensitive, fixed order, no leading/trailing space):

`diag:v1 phase=<phase> operation=<operation> code=<code>[ status=<status>][ reason=<reason>][ sqlite=<sqlite>]`

Brackets describe optional fields; they are not emitted. Unknown fields, duplicate keys, reordered keys, wrong combinations, suffixes and unknown versions fail reader admission. All values come from these sets:

| Field | Accepted values |
|---|---|
| `phase` | `unknown`, `discovery`, `exclusion`, `duplicate_detection`, `routing`, `action_execution` |
| `operation` | `unknown`, `start_run`, `load_run`, `load_token`, `select_inputs`, `capture_selection`, `discover`, `load_metadata`, `apply_exclusions`, `detect_duplicates`, `evaluate_video`, `record_decision`, `execute_action`, `record_action`, `retain_failure`, `retry`, `finalize_run`, `record_phase`, `inspect_run`, `inspect_queue`, `account_quota`, `generate_advisory`, `parse_response`, `subscriptions_list`, `channels_list`, `playlist_items_list`, `videos_list`, `playlist_items_insert`, `playlists_list`, `refresh_token`, `save_token`, `account_status`, `disconnect_account`, `oauth_callback`, `dispatch_request` |
| `code` | `unknown_error`, `sqlite_error`, `provider_error`, `oauth_error`, `timeout`, `canceled`, `transport_error`, `invalid_provider_response`, `panic`, `authorization_unavailable`, `action_authority_invalid`, `credential_persistence_failed`, `authorization_changed`, `mutation_application_uncertain` |
| `status` | Canonical decimal integer, without signs/leading zeroes: 300–599 for `provider_error`, 100–599 for `oauth_error` (an OAuth response can carry an error parameter with HTTP 200). No other code admits status. Use the actual numeric HTTP status, never `Response.Status` prose. An absent/invalid status is omitted, not guessed. |
| `reason` for `provider_error` | Exactly `quotaExceeded`, `dailyLimitExceeded`, `rateLimitExceeded`, `userRateLimitExceeded`, `authError`, `forbidden`, `insufficientPermissions`, `backendError`, `internalError`, `badRequest`, `invalidParameter`, `notFound`, `playlistNotFound`, `videoNotFound`, `playlistItemsNotAccessible`, `playlistContainsMaximumNumberOfVideos`, `videoAlreadyInPlaylist` |
| `reason` for `oauth_error` | Exactly `invalid_request`, `invalid_client`, `invalid_grant`, `unauthorized_client`, `unsupported_grant_type`, `invalid_scope`, `access_denied`, `temporarily_unavailable`, `server_error` |
| `sqlite` | Exactly `SQLITE_ERROR`, `SQLITE_BUSY`, `SQLITE_LOCKED`, `SQLITE_READONLY`, `SQLITE_IOERR`, `SQLITE_CORRUPT`, `SQLITE_FULL`, `SQLITE_CANTOPEN`, `SQLITE_CONSTRAINT`; allowed only for `sqlite_error` |

Optional facts not present in a known typed error are omitted. A matched nil typed pointer supplies no facts and must not be dereferenced. All other code/optional-field combinations are invalid. Unknown provider reasons are omitted even if identifier-shaped; never include an `unknownReason` plus the original token. Extensions require a deliberate policy/test update, not pattern-based automatic admission. These sets are disclosure choices, not a claim to enumerate the upstream provider's errors.

Known phase and operation are supplied where the error is handled, not parsed from wrappers. The operation is that boundary's operation, not necessarily the deepest failed function. `evaluate_video` plus `SQLITE_BUSY` is sufficient for a routing failure even if the internal chain names `record decision`. Use `record_decision` only where that operation is actually known. Do not invent or change persisted lifecycle phase values to accommodate finalization; use `operation=finalize_run` with the known phase or `unknown`.

### Typed classification and declassification ownership

Declassification means releasing only registered facts from a known error type. It is not permission to release that type's `Error()` output.

For one cause, apply the following precedence using typed inspection through `%w` wrappers. First matching row wins. Callers report a secondary retention/finalization error separately; do not concatenate it to the primary cause or overwrite the primary persisted reason with it.

| Evidence | Facts released | Owner |
|---|---|---|
| `*modernc.org/sqlite.Error` | `code=sqlite_error`; map `Code() & 0xff`: 1→`SQLITE_ERROR`, 5→`SQLITE_BUSY`, 6→`SQLITE_LOCKED`, 8→`SQLITE_READONLY`, 10→`SQLITE_IOERR`, 11→`SQLITE_CORRUPT`, 13→`SQLITE_FULL`, 14→`SQLITE_CANTOPEN`, 19→`SQLITE_CONSTRAINT`. Unregistered primary codes omit `sqlite`; extended codes retain only their registered primary, including BUSY_SNAPSHOT→BUSY. Never use `ErrorCodeString` descriptions or driver message. | Shared `internal/diag` classifier; database adapters preserve typed errors with `%w`. No per-query text matching or mass wrapper rewrite. |
| `*youtube.APIError` | `code=provider_error`; independently validate status and exact reason membership above. | YouTube adapter admits reason at body parsing and again when rendering exported/constructible `APIError` values. Shared classifier revalidates fields; do not trust constructors alone. |
| `*oauth2.RetrieveError` | `code=oauth_error`; only `Response.StatusCode` when response exists and `ErrorCode` when registered. | Shared classifier at the run/token boundary. Never release `Body`, `ErrorDescription`, `ErrorURI`, `Response.Request`, headers or `RetrieveError.Error()`. |
| `errors.Is(err, context.DeadlineExceeded)` or typed `net.Error` with `Timeout()` true | `code=timeout` | Shared classifier; never the timeout object's message/address. |
| `errors.Is(err, context.Canceled)` | `code=canceled` | Shared classifier. A canceled execution context alone is not evidence that an unrelated error was a cancellation. |
| Typed `*net.OpError` or `*url.Error`, absent the above causes | `code=transport_error` | Shared classifier. Unwrapping a URL error alone is insufficient: its nested cause can still contain body, URL or credential text. |
| A source knows parsing failed, a panic occurred, no usable authorization exists, or action authority failed | Respectively `invalid_provider_response`, `panic`, `authorization_unavailable`, `action_authority_invalid` | Source supplies that registered code from the actual branch/type. Do not infer it from arbitrary prose. Panic handling never formats the recovered value, even an `error`. |
| Anything else, including an ordinary `errors.New("SQLITE_BUSY")` | `code=unknown_error` | Shared classifier. Caller phase/operation remain useful; cause detail is unavailable. |

The classifier may depend on the existing YouTube adapter, SQLite driver and OAuth/standard error types. The YouTube adapter must not import the classifier back and create an import cycle: it owns its exact reason-admission helper and body-free `APIError`; `diag` consumes/revalidates it. Its own source logs use fixed messages and registered facts only. This needs no generic plugin classifier registry or error-handler service.

YouTube `APIError` must continue not to retain a raw body. A provider parse/body-read/transport error can remain a wrapped internal cause in memory for control flow, but no caller may persist, serialize or log that chain. Safe `APIError.Error()` is defense in depth, not a substitute for classifying outer wrappers.

### Disposition fields are not arbitrary failure prose

- Action `outcome_reason`: only absent, `already_completed`, `prior_action_failed`, `quota_exhausted`, `authorization_pending`, `mutation_application_uncertain`. The authorization ARS supplies the two new reasons' lifecycle: auth failure before issue is deferred; an existing uncertain-send veto is not-attempted. Do not put a v1 error diagnostic in this field; action failure belongs in `error_message`.
- Video/global and run-video operational deferral reasons: only absent, `quota_exhausted`, `api_rate_limited`, `authorization_pending`, `metadata_unavailable`, `temporary_unavailable`. The first two describe actual quota/rate constraints. `authorization_pending` describes the authorization deferral already required by OC-001/OC-002; `metadata_unavailable` describes OC-002's missing-data deferral. `temporary_unavailable` describes a known other temporary operational constraint under OC-002 Guarantee 5, never an arbitrary internal failure. The existing classifier decides the outcome first; this vocabulary does not turn an otherwise failed video into deferred work.
- Deferred-queue `reason`: only `quota_exhausted`, `api_rate_limited`, `authorization_pending`. The first two are already admitted by the existing schema; the authorization ARS requires the forward CHECK/type/reader extension for the third before it can be emitted. The video and queue fields still have different registrations. Never label auth failure as quota/rate exhaustion. Registration does not declare retention/renewal implemented: the authorization ARS supplies that exact target lifecycle and verification; other missing non-quota retention remains an OC-001 obligation.
- Invalid disposition values are not converted into a different action/video outcome. Withhold them on read; keep the outcome and unavailable flag. An invalid optional action reason is omitted on write. A required deferred reason must be a registered actual cause: reject a missing/invalid required reason under the existing recording-error contract and retain recoverable work through existing recovery. Do not silently commit a deferred outcome without its required reason or invent `quota_exhausted` as fallback.

## Write and log boundaries

The producer classifies the actual error before any database call, event publication or log call. State/queue stores additionally guard diagnostic values immediately before SQL binding, including transaction variants and direct executor SQL. This catches callers bypassing the pipeline helper. An invalid nonempty failure string becomes `diag:v1 phase=unknown operation=unknown code=unknown_error` unless that sink already has trusted phase/operation facts. Absence stays absent; no reason is invented for a successful action.

Logs are an output boundary at every level, including debug. **No source may first log raw errors and sanitize later.** Remove the contrary “full chain stays in the server log” comments in `failRun` and `failHistoricalRun`. Log a fixed source message and the encoded diagnostic, or fixed registered fact attributes. Do not attach the original error, format it into the message, or append raw detail under another key. Do not log provider IDs, timestamps/values from invalid provider payloads, request/response objects or URLs. Counts, booleans, numeric local correlation IDs, registered phase/operation/status/code/reason and fixed HTTP methods are sufficient here. This applies to injected loggers and package-default `slog`, not just the server's final handler. A global redacting logger is not required and would not repair already-formatted unsafe messages.

SSE error/quota messages are guarded just like persisted reasons. Domain video-event identity stays under its existing event contract; it must not become error-message interpolation. If a sink fails, log that failure's independently classified facts; no recursive attempt to persist a diagnostic about the diagnostic guard is required.

## Reader compatibility and public projection

Reader admission and producer emission are different. New failure producers emit only v1. Readers accept strict v1 and only these exact legacy forms:

1. Whole string `youtube API returned <status>` or `youtube API returned <status>: <registered-provider-reason>`, with the same numeric/reason constraints. Normalize to v1 with unknown phase/operation; do not echo original text.
2. The exact whole legacy literals in the table below, normalized to v1. Generic failure literals carry `unknown_error`; they do not establish a specific root cause.
3. Exact registered disposition reasons, only in their matching fields.

| Exact legacy literal | Phase | Operation | Code |
|---|---|---|---|
| `discovery phase failed` | `discovery` | `discover` | `unknown_error` |
| `action execution phase failed`, `action execution failed`, `playlist insertion failed`, `playlist action failed` | `action_execution` | `execute_action` | `unknown_error` |
| `retry failed` | `unknown` | `retry` | `unknown_error` |
| `snapshot action authority failure` | `unknown` | `execute_action` | `action_authority_invalid` |
| `run completion failed` | `unknown` | `finalize_run` | `unknown_error` |
| `panic: unrecoverable error` | `unknown` | `unknown` | `panic` |

No wrapper prefixes are admitted, including `insert playlist item: ...`; no SQLite substring is recovered from legacy prose. Do not map arbitrary legacy `no active token` or a bare identifier to an authorization fact. Their status/phase/evidence remain available; the specific reason does not. Reader admission of known literal values is not provenance proof, but those whole values carry only the released vocabulary even if written by an older producer.

For each projected failure reason, return only normalized v1 text or absence. Nonempty rejected legacy text is absent and unavailable; `unknown_error` likewise says the cause detail is unavailable, while its safe phase/operation may be retained. Empty/null input means no recorded reason, not a hidden reason. Do not convert absence to rejection or successful completion.

| Public surface | Reason and flag contract |
|---|---|
| Run list/detail and any `Run.MarshalJSON` use | Keep `interrupt_reason` key, null when withheld; `failure_reason_unavailable=true` for rejected nonempty text or known-generic `unknown_error`. Retain actual status. Admit `current_phase` through the phase enum, otherwise null. |
| Run video list/detail and executed actions | Existing `error_message` is absent/null when withheld; `error_message_unavailable=true` on rejected nonempty error or `unknown_error`. Apply identical policy in both list and detail projections. |
| Error queue list/retry/dismiss responses | `error_message` remains a string (empty if withheld); `detail` remains optional. Admit both fields; `error_message_unavailable` is true if either nonempty field is withheld or carries `unknown_error`. Keep IDs, category/status, counts, origin/retry run and resolution lifecycle. |
| Action disposition reason | Invalid `outcome_reason` is omitted; additive optional `outcome_reason_unavailable=true`. Does not set a different outcome. |
| Video/deferred queue operational reason | Invalid `deferred_reason`/queue `reason` is omitted; additive optional `deferred_reason_unavailable=true`. Maintain the existing valid enum wire spelling and lifecycle; do not serialize an invalid typed-string value directly. |
| SSE/API error response caused by covered processing/inspection failures | Only encoded admitted facts or a fixed registered operation failure response. Never return `err.Error()` as a shortcut. SSE cannot establish terminal status. |

Optional unavailable booleans may be omitted when false. Browser DTOs must preserve them; existing unavailable presentation is reused. This contract does not change the user's optional wording, redesign notices or require rendering the v1 prefix verbatim. Presentation may format admitted facts; it may not treat an unknown value as displayable or recover hidden text from another field. API guards remain authoritative even when a browser ignores optional fields.

Old database rows remain unchanged. New readers support old rows as above. Older API consumers tolerate safe text/optional flags; no current text string is a machine-readable retry/status policy. **Do not roll back to permissive readers and claim diagnostic safety:** without a schema migration they could expose still-present unsafe legacy text. A rollback requiring that weaker reader needs a separate operational risk decision. Do not implement speculative mixed-writer coordination: current deployment remains one application process/SQLite owner.

## Code seam map and ownership

Source locations below were read/searched on 2026-11-06; they are current gap evidence, not a claim that fixes exist. Semantic navigation returned building/loading; bounded direct reads supplied the evidence.

| Owner / seam | Required realization |
|---|---|
| Shared diagnostics: `internal/diag/diag.go` | Replace shape-based `Safe`, full-chain-first `Reason` and unchecked fallback with classification, canonical codec, whole-input legacy admission and field-specific disposition checks. One policy serves storage/projection/log callers. |
| Provider: `internal/youtube/client.go`, `APIError.Error`, `youtubeAPIError`, request/body/metadata handling | Replace identifier regex with exact reason membership and validate exported fields at rendering. Preserve status; discard bodies. The invalid `publishedAt` debug logs currently release external ID/value/error; replace with fixed parse-failure facts. Request logs need only fixed method/operation, not request URL/query. |
| Pipeline: `pipeline.go` failure/panic/completion/phase/selection and video-outcome paths; `replay.go`, `dryrun.go`, `retry.go`, `authority.go`, `exclusion.go`; `executor.go` failure retention, action write and direct evidence SQL | Supply known phase/operation; classify before log/event/write; remove raw/panic formatting and external identifiers in diagnostic logs. Preserve typed causes for classification. Independently diagnose finalization/retention failures; guard direct SQL paths. |
| State: `internal/state/store.go` `FailRun`, `InterruptRun`, `RecordRunVideoOutcomeTx`, `CreateVideo`, `UpdateVideoState`/Tx, `CreateExecutedAction`; `machine.go` callers | Guard every stored failure/disposition field, including initial creates and transaction paths. This is not just `Run.MarshalJSON`. |
| Queue: `internal/queue/store.go`, `retry.go::RetainRuntimeErrorTx`, `service.go`, `handlers.go` | Guard new/retried messages/details and deferred reasons. Classify service/handler failures before logs; preserve retry/resolution behavior. Replace permissive `unsafeText`; inspect all response paths. |
| Public reads: `state/models.go::Run.MarshalJSON`, `server/run_handlers.go::safeErrorMessage`, action/video builders, `queue/handlers.go::publicErrorEntry`, deferred response projections | Apply whole-input admission everywhere. Current action `OutcomeReason` and video `DeferredReason` assignments are direct projections and need their own checks, not only `error_message` vetting. |
| Processing dependencies and entry/read handlers: `discovery/engine.go`, `metadata/manager.go`, run/metadata adapter wiring in `cmd/server/main.go`, `scheduler/scheduler.go`, `server/server.go`, run-related `stats/handlers.go` | Audit actual call paths for earlier raw logs; fix the reachable provider/DB diagnostic logs and external-ID diagnostic attributes too. No closure claim based only on a grep of `diag.Safe` callers. |
| Browser: API DTO adapters and Run/Decision/Errors views | Preserve unavailable flags and distinguish a safe code from outcome/status. Do not require provider IDs or raw chains to explain the failure. Reuse accepted copy. |

The adjacent auth callback handler logs and serializes raw errors; its service can wrap `oauth2.Exchange` errors. The integrator owns routing this confirmed finding outside the bounded run path. It remains unresolved until addressed or explicitly dispositioned; this specification is not a whole-application confidentiality certificate. Similarly, unrelated configuration/import/advisory logging must not be declared safe merely because this diagnostic task is complete.

Grounding for SQLite and OAuth type contracts: installed `modernc.org/sqlite@v1.58.0/error.go` exposes `Error.Code()` and an unsafe message-bearing `Error()`; installed `golang.org/x/oauth2@v0.37.0/token.go` exposes `RetrieveError.Response`, `Body`, `ErrorCode`, `ErrorDescription`, `ErrorURI`, and message-bearing `Error()`. These are the versions in `go.mod`. Recheck the adapters if those dependencies change.

## Verification required before claiming closure

These are conformance obligations, not a task sequence or executed test report.

1. **Policy/codec:** exact positive/negative tables for all enums, optional combinations, status bounds, canonical order, 200-byte limit, unknown versions/keys, invalid fallbacks and disposition values. Prove marker-free `TOKEN_SENTINEL`, `VIDEO_SENTINEL`, `SQLITE_BUSY`, and harmless-looking arbitrary prose are not admitted. Prove an error whose `Error()` panics can be classified as unknown without that method being called. Round-trip every emitted summary through the stored reader.
2. **Typed causes:** real wrapped SQLite driver error (including BUSY/BUSY_SNAPSHOT primary mapping); wrapped APIError with registered reason; exported APIError containing a marker-free secret as reason; unknown reason; invalid status; OAuth RetrieveError with/without response and sentinel body/description/URI; context deadline/cancellation; typed network timeout with sentinel address. Assert retained safe facts and absence of every raw sentinel. A plain error containing `SQLITE_BUSY` must remain unknown, not masquerade as driver evidence.
3. **Source logs:** capture injected and default `slog` at debug. Exercise malformed provider timestamp with marker-free sentinel ID/value, body parse/read/transport errors, metadata degradation, finalization persistence failure and retry failure. No raw error object/message/value may leak before a later guard. Exercise at least one ordinary unknown error with a marker-free sentinel; provider-body-only sentinels cannot catch the old defect.
4. **Write bypass:** directly exercise state/queue store entry points, transaction variants and executor SQL with unsafe supplied reason/error/disposition values. Inspect persisted columns: safe canonical fallback or absence only. Work retention, outcomes, ownership and immutable recorded success must be unchanged. Legacy unsafe rows for reader tests must be deliberately seeded below these write guards, never through the production API.
5. **Legacy compatibility:** seed unsafe and marker-free historical run/video/action/queue texts, invalid disposition strings, known provider literal, known generic literal, empty/null values and a full Run15-shaped raw SQLite chain. After DB reopen, registered real API list/detail/queue responses contain no raw sentinel or chain; unavailable flags agree across paths; status/phase/outcome/counts survive. No SQL update rewrites the historical source row. Unknown cause is not inferred as rejected or as a new video outcome.
6. **Real composition:** controlled provider through the real adapter/executor → migrated SQLite → close/reopen → registered HTTP reads → real-client browser, with logs captured. Verify known `500/backendError` remains useful, unknown marker-free reason/body/token/URL/ID sentinels are absent from diagnostic columns, JSON diagnostic fields, SSE and logs, and the browser shows existing failure/unavailable states. Resource identity assertions are field-aware: unchanged domain identity fields are not evidence of a diagnostic leak. Include a real typed SQLite writer conflict through evaluation and assert `SQLITE_BUSY` without SQL/IDs/chain. Verify simulations retain their existing no-mutation guarantees.
7. **Regression scope:** update tests that assert raw internal chains or arbitrary queue prose to assert the contract instead; do not weaken status/retention/isolation assertions merely to make a new allow-list green. Run the affected diag/provider/state/queue/server/pipeline suites and the assembled diagnostic composition. Broader suites remain the integrating coder/reviewer's responsibility.

Critic session `ses_e4d3951c2ffeIG2bR2vT1fzXIf` reviewed the contract/source/authority without edits. It found the video-versus-queue deferral-vocabulary contradiction recorded below; its scoped follow-up approved the correction only. This is a bounded challenge, not blind rediscovery: automatic/authority context can expose earlier conclusions. Independent implementation/composition review is still required before relying on safety closure. Documentation validation proves metadata/links only, not runtime confidentiality.

This author's three changed topic documents pass OKF validation with zero errors/warnings/notifications. All six authority links from this specification resolve. Graph integrity reports 14 unrelated dangling references, no superseded dependencies and no loaded profile; typed graph conformance is therefore not established. Parent log integration remains outstanding below. No runtime test, browser, live-provider, database intervention or implementation edit was performed for this specification.

## Material findings, open questions and limits

| Source | Disposition / owner | Blocking effect |
|---|---|---|
| Current `diag.Reason` accepts `err.Error()` before inspecting types; `Safe` accepts marker-free ASCII | Confirmed violation of the existing ARD-001 invariant. Closed by this realization decision, **not yet fixed in code**; coder owns implementation. | Blocks diagnostic-safety completion. |
| Pipeline phase failure logs precede vetting; YouTube timestamp debug logs carry provider values | Confirmed violation, not an authorized private-log exception. Coder owns source log fixes. | Blocks diagnostic-safety completion. |
| Reported Run15 raw SQLite chain and reverted Safe-only trial (Loom concern `69d1b7f4-daa7-4ca9-b306-c600f82f0800`) | Trial report read; Run15 remains user/reviewer-supplied evidence, not a locally reproduced incident. This contract supplies typed DB classification and legacy withholding; no inference about Run15's recovery or side effects. | Incident recovery remains separate; no user choice blocks this contract. |
| Standalone OAuth callback raw error log/API paths | Confirmed adjacent finding; routed to integration owner, not silently accepted or declared fixed. | Does not block deciding this bounded contract; blocks any blanket application-wide safety claim. |
| Shared architecture log has concurrent authors | Integrator owns the same-wave log entry/index reconciliation; this author owns topic spec/index and narrow ARD status correction. | Documentation integration is outstanding until the log entry is applied. |
| Critic `ses_e4d3951c2ffeIG2bR2vT1fzXIf`, concern `b2690083-52c3-46d0-a0e8-92dc31dd71d6`: quota/rate-only video vocabulary would reject accepted authorization deferral | Accepted. Split video and deferred-queue registrations and register truthful authorization/missing-data/known-temporary video reasons under OC-001/OC-002. Existing `deferred_item` enum is not expanded; no new routing/recovery is invented. | Contract contradiction corrected. Existing broader authorization-retention implementation remains outside this safety-closure proof, not waived. |

No unresolved product decision is needed for the bounded diagnostic contract. Arbitrary-text confidentiality, private debug logging of raw errors and retroactive cause inference are not promises it makes. Raw errors may exist in trusted process memory for control flow; no crash-dump or hostile-plugin threat model is introduced. This assumption is not escalated because the accepted boundary concerns persisted/public/event/log release in the existing single-user application. Existing date semantics, optional copy, retention/compliance questions, SQLite contention recovery and simulation timeout-finalization policy remain with their existing owners and are not settled here.
