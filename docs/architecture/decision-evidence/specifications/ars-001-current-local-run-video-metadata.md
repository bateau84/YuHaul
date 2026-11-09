---
type: specification
title: ARS-001 — Current-Local Run-Video Metadata
description: Exact additive list/detail JSON contract for local title, source-channel name, duration, provenance, availability and optional-context read failure, without historical input capture.
tags: [architecture, specification, api, metadata, run-history, availability]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted — bounded wire contract for the implemented current/local read slice. Not acceptance of full historical evidence, retention permission, complete API safety or shipping.

The API and browser agree on a separate current/local metadata object beside the selected run's recorded evidence. For example, an eligible cached video can show its title and a known duration of `0` seconds. If the optional metadata query fails, the same HTTP `200` still contains the mandatory decisions, with `current_local_metadata: null` and `metadata_context_status: "context_read_failed"`. Neither response says what title the historical run evaluated.

This contract specifies existing emitted fields, not a new storage model. It makes missing values, known local-record expiry and current withholding distinguishable without exposing suppressed data. No field repairs generic historical exclusion evidence or authorizes indefinite retention.

## Derived from

- [BR-015 — Explainable Route Decisions](../../../requirements/yuhaul/br-015-explainable-route-decisions.md), AC8–9 and unchanged AC7 limits.
- [OC-005 — Decision Evidence Attribution and Availability](../../../requirements/yuhaul/oc-005-decision-evidence-attribution-and-availability.md), Guarantees 1–3 and 8–12 and failure semantics.
- [BR-028 — Historical Run Inspection](../../../requirements/yuhaul/br-028-historical-run-inspection.md), requested-run inspection and no live reconstruction; [BR-027 — Historical Backtesting](../../../requirements/yuhaul/br-027-historical-backtesting.md), no provider calls and simulation isolation.
- [Admin Interface Design](../../../design/yuhaul-admin-ui-design.md), §Run Video Recognition — Current Local Context, including conditional availability copy and local retry.

## Implements

- [ARD-001 — Run-Attributed Evidence and Metadata Lifecycles](../design/ard-001-run-attributed-evidence-and-metadata-lifecycles.md), independent safe read boundary, selected batch-reader realization and availability/copy reconciliation **only**.

## Satisfies

| Obligation | Allocation and limit |
|---|---|
| BR-015 AC8; OC-005 Guarantees 1–3 | Exact current/local title/name/duration, attribution, nullable source timestamp and field availability. No historical-input attribution. |
| OC-005 Guarantees 8–10 | Same requested-run membership; separate optional read failure; no invented absence cause or expired-value release. Existing raw-audit lifecycle is not certified by this projection. |
| BR-015 AC9; OC-005 failure semantics and Guarantee 11 | Same-endpoint local retry preserves identity; mandatory read errors remain errors; optional-context failure preserves mandatory evidence. No evaluation/provider repair. |
| OC-005 Guarantee 12; BR-027 AC4/6–7 | Enrichment neither changes mode/outcomes nor executes actions/provider calls or writes production state/statistics. Existing mode gates remain elsewhere. |
| BR-028 AC2–3, AC9 | Adds recognition to existing requested-run list/detail. Full causal explanation, cross-run search and retry-count obligations remain elsewhere. |
| BR-015 AC1–7; BR-009 durable-title promise; full causal capture | Not satisfied by this extension. Historical reproduction/retention remains blocked under the parent design; safe unavailable copy is not a waiver. |

## Scope and related contracts

This specification is authoritative for the two metadata fields on each run-video item and the nested object described below. It does not redefine the other video, route, condition, action or run fields.

- [Route, Playlist, and Run-Outcome ARS-001](../../ars-001-route-and-playlist-api-contracts.md) retains outcome-filtering, pagination, UUID and mandatory error-envelope meaning.
- [ARS-005 — Duplicate Continuation Compatibility and Results](../../duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md) and [ARS-006 — Run-Attributed Production Outcomes](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md) retain requested-run membership/outcome authority. A cached row alone does not establish membership.

No new endpoint, request parameter, migration, persisted evidence field, date-selection rule, exclusion predicate or metadata-refresh control is introduced.

## HTTP placement and producer emission

| Request | Successful envelope | Metadata placement |
|---|---|---|
| `GET /api/runs/{id}/videos` | `{"videos":[...],"pagination":{"page":1,"page_size":20,"total":N,"total_pages":P}}` | Each `videos[i]` has `current_local_metadata` and `metadata_context_status`. They are not page-level fields. |
| `GET /api/runs/{id}/videos/{videoId}` | `{"video":{...}}` | `video.current_local_metadata` and `video.metadata_context_status`. |

Both successful responses are `200`, `Content-Type: application/json`, with `Cache-Control: no-store`, including the optional-read-failure case. Empty list is `videos: []`; there is no synthetic metadata item or page-level context status. Existing video/channel identifiers remain their labeled reference identities, not replacement titles/names.

Every video item emitted by the current producer MUST include both keys below; neither uses omission to represent absence:

| Field | JSON type | Emission and meaning |
|---|---|---|
| `metadata_context_status` | string | Exactly `"available"` or `"context_read_failed"`. Not nullable. This is the optional lookup's status, not a video outcome or per-field availability. |
| `current_local_metadata` | object or `null` | Successful optional lookup emits the complete nested object, even when no metadata row exists. Failed optional lookup emits `null`; no partial object is released. |

`"available"` means the optional local lookup succeeded, **not** that a field has a displayable value. Missing metadata, a known-expired row, a withheld value, or all-null fields can accompany `"available"`. `"context_read_failed"` always accompanies `current_local_metadata: null` in current producer output.

### Nested `current_local_metadata`

All eight keys are required whenever this object is emitted. Nullable values are explicit JSON `null`, not omitted or encoded as an empty string or zero:

| Field | JSON type | Exact meaning |
|---|---|---|
| `attribution` | string | Constant `"current_local_not_necessarily_evaluated"`. Applies to the whole object, including unavailable fields. |
| `title` | string or `null` | Original locally cached title only when eligible and nonblank; otherwise `null`. |
| `title_availability` | string | One of the four emitted availability tokens below. |
| `channel_name` | string or `null` | Original locally cached source-channel display name only when eligible and nonblank; otherwise `null`. This is not a separate subscription name. |
| `channel_name_availability` | string | Availability of `channel_name`. |
| `duration_seconds` | integer or `null` | Locally cached duration, in seconds, only when eligible and present. `0` is a known value. This slice adds no new duration range/normalization rule. |
| `duration_availability` | string | Availability of `duration_seconds`; the key is **not** `duration_seconds_availability`. |
| `source_fetched_at` | RFC 3339 timestamp string or `null` | Eligible row's parsed `fetched_at`, serialized in UTC with `Z` and optional fractional seconds (Go RFC3339Nano JSON representation). Not run/discovery time, response time, expiry time, or an independently synthesized `refreshed_at` fallback. |

A string whose Unicode-whitespace-trimmed content is empty is unavailable. The trim test determines availability; a released nonblank string retains its original characters, rather than returning a normalized matching key. Strings are rendered as text, never interpreted as HTML.

`source_fetched_at` is present and non-null only for an eligible row. An eligible row may have a source timestamp even if all three fields are absent. For a missing or ineligible row it is `null`. A consumer may show a valid supplied timestamp as `Observed at {time}`, clearly attached to current/local context, and MUST NOT substitute run start/discovery time. An absent, null or malformed timestamp is not displayed or repaired. The timestamp does not turn these values into captured historical inputs.

### Per-field availability tokens

| Token | Field value | Meaning |
|---|---|---|
| `"current_local"` | Non-null | Field is present and released from an eligible cached row at the projection cutoff. It need not equal the input evaluated by this run. |
| `"known_expired"` | `null` | Local metadata **record** has supported expiry evidence. Says nothing about whether this particular field was previously populated. |
| `"withheld"` | `null` | Field is actually present but current release eligibility prevents disclosure, and no supported known-expired classification applies. This is withholding now, not an assertion about historical redaction or legal classification. |
| `"unknown"` | `null` | Field is unavailable without a more specific supported cause: for example no row, null field, or blank title/name. Does not mean never recorded. |

The current producer MUST NOT emit `not_recorded`, `known_not_captured`, `expired`, `available` or `context_read_failed` as per-field availability tokens. The design's conditional not-recorded copy has no supporting per-field capture-absence marker in the current Store. Read failure belongs to `metadata_context_status`, not three invented field statuses.

## Eligibility, precedence and provenance

Manager owns these rules; HTTP/UI consumers do not inspect raw lifecycle rows or diagnose from null fields. Let `C` be one UTC projection cutoff sampled **after** the complete batch result has been consumed, not the HTTP request-start time. A slow database read that crosses the deadline cannot release data using an earlier cutoff.

1. A successfully read row is **known expired** if `expired_at` parses as a nonzero timestamp, or `expires_at` parses as a nonzero timestamp at or before `C`. A parsable expired marker is a row-expiry marker, not a source observation timestamp.
2. The row is **eligible** only when `is_active` is true; `expired_at` is SQL NULL; it is not known expired; `fetched_at` and `expires_at` parse successfully; `fetched_at` is later than Go's zero time and no later than `C`; and `expires_at` is strictly later than `fetched_at`. Combined with rule 1, this requires `expires_at > C`.
3. **Marker presence and marker validity are separate.** Any non-NULL `expired_at`, including malformed or blank stored text, blocks eligibility. A malformed marker alone does not prove known expiry: present fields become `withheld`, absent fields remain `unknown`, unless another valid deadline establishes `known_expired`. A parse failure of a lifecycle value read into the raw lifecycle projection is ineligible provenance, not automatically a query failure. Actual query/scan/iteration failure follows the HTTP failure rules below.
4. For a known-expired row, all three field values and `source_fetched_at` are null, and all three availability tokens are `known_expired`, **even if text/duration were already cleared**. UI copy is “local metadata expired”, not “this title was previously recorded”.
5. Otherwise, absent duration or null/blank text yields `unknown`. A present field on an ineligible row yields `withheld`; a present field on an eligible row yields `current_local`. Each field is independent. `source_fetched_at` is emitted only for an eligible row.
6. A successful lookup with no row emits the attribution constant, three null values with three `unknown` tokens, and `source_fetched_at: null`. Cleanup may make a formerly known-expired row disappear; a later response may truthfully change to unknown-cause unavailability. This is not a historical-evidence change.

The current Store accepts lifecycle values as `time.Time`, text or bytes; its parser recognizes RFC3339Nano and the existing SQLite timestamp forms `2006-01-02 15:04:05.999999999-07:00`, `2006-01-02 15:04:05.999999999` and `2006-01-02 15:04:05`, then normalizes to UTC. Zero/unrecognized values fail that parse check. These are internal compatibility forms, not alternate public wire formats.

No value falls back to `channel.name`, a title fingerprint, a current rule/configuration value, or an arbitrary historical audit. No new expiry period is calculated during inspection, and lookup does not refresh/extend the cached source deadline. The eligibility rule is not a legal determination or proof that all other persisted copies obey retention policy.

## Membership, paging and local-reader boundary

- Mandatory run, requested-run video membership and route/decision/action evidence are resolved before optional enrichment. A globally cached video outside the requested run remains not found; its metadata is neither looked up as an inspection member nor disclosed.
- Existing `page`, `page_size` and `outcome` semantics remain unchanged. Defaults are page `1`, size `20`; a supplied positive size is capped at `100`. Invalid/nonpositive page or size falls back to its default. Outcome filtering and totals are resolved before enrichment. Metadata state does not filter, reorder, add or remove video outcomes.
- One local batch uses external video IDs from the selected page only, at most `100`; detail uses one member ID. Manager deduplicates IDs and Store uses parameter placeholders. More than `100` incoming IDs is a reader error, not silent truncation or an unbounded query. A nonempty page/detail requires one metadata SELECT, not a SELECT per video; the Store does not query for an empty ID set. Server may invoke the reader on an empty page, but emits no synthetic item or status.
- The existing manager is supplied through `server.WithMetadataReader` from production `cmd/server/main.go`; the read seam exposes only `ReadLocalDisplay(context, IDs)`, not refresh or YouTube methods. On success the production reader returns a result for every requested distinct ID, including default unknown results for missing rows. A test double omitting requested keys is not the production missing-row contract.
- Inspection and retry perform local reads only. No call to `GetDisplayTitle`, `RefreshOnDemand`, `RunLifecycle`, fetcher, YouTube or the quota ledger is part of enrichment. No metadata, decisions, outcomes, snapshots, production corpus/idempotency or statistics are mutated.
- `no-store` prevents this extension from establishing a reusable HTTP cache. It does not promise that already received browser data vanishes at the source deadline. Separate list/detail/retry reads need not be atomic and may return changed recognition information; recorded run evidence remains unchanged.

## Failure behavior and retry

| Situation | HTTP/body behavior | Safe inference |
|---|---|---|
| Optional lookup succeeds, including no row or all fields unavailable | `200`; `metadata_context_status: "available"`; complete metadata object | Local lookup worked; use field tokens, not the outer status, to determine display availability. |
| Optional query, scan or iteration fails; reader unconfigured; reader returns any error alongside partial data | `200`; retain mandatory evidence; `metadata_context_status: "context_read_failed"`; `current_local_metadata: null` on every affected item | Context read failed, not expired/not-recorded/rejected. Partial lookup data is discarded, not presented for earlier rows while later rows look missing. |
| Mandatory lookup/identity/evidence failure | Existing non-2xx `{"error":"message"}`; no successful inspection envelope or optional enrichment | No routing success/failure can be inferred from the failed inspection. |

Existing mandatory errors remain: `400` for unparseable run ID (`invalid run id`), empty video ID at the handler (`invalid video id`), or unsupported nonempty outcome filter (`invalid outcome filter`); `404` for unknown run (`run not found`) or absent requested-run video (`video not found`); `500` for mandatory run, video list/member, route identity, decisions/audits/actions or detail assembly read failure. Safe existing error messages and route-identity failure behavior are unchanged. This extension does not add an optional metadata-error payload containing raw database errors, SQL, provider bodies, request data, credentials or arbitrary error chains. It adds no new status endpoint or error queue entry.

`Retry local information` rereads the **same** GET endpoint: list preserves run ID, page/size and outcome filter; detail preserves run/video identity. Mandatory evidence may be read again because there is no separate metadata endpoint, but evaluation is not rerun. The consumer keeps usable decisions and a distinct context error/retry indication, not an endless loading state or a new routing outcome. A late response for a different selection cannot replace the selected run/video. Missing run mode still cannot default to production language. UI copy and focus behavior remain design-owned.

## Compatibility and evolution

- **Producer emission:** current producer always emits both outer keys and every nested key when the object exists, with exactly the strings/types/null rules above. Only the four field tokens and two outer statuses are supported for emission; no future token is authorized by this contract.
- **Reader acceptance:** older API responses may omit either metadata key, and the bundled TypeScript reader tolerates optional/null metadata and optional `source_fetched_at`/value fields. Missing extensions mean unsupported/unavailable context, not a successful metadata lookup, known expiry or never-recorded history. Do not promote legacy top-level `title`, `channel_name` or duration fields to this attributed object or fall back to them for current-local recognition.
- Unknown optional members may be ignored. Unknown availability tokens must not release their accompanying values: only the explicit `current_local` token plus a usable value permits display. Unrecognized/absent outer status supplies no positive lookup-status meaning. Unrecognized attribution cannot be interpreted as historical evidence; this extension's data must never be promoted to evaluated-input context. Supported fields can remain independently recognizable under their known field tokens; do not invent a failure/expiry cause for unknown strings.
- A malformed/omitted `source_fetched_at` is hidden, not replaced with current time or run time. It does not alter the stored routing outcome. Legacy absent metadata does not require a migration or history backfill. Older consumers may ignore this additive object; that tolerance does not make them satisfy the newly accepted recognition design.
- This is a bundled same-origin API, not a new separately versioned third-party protocol. A new required meaning, attribution or status requires its own agreed evolution before producers emit it. No storage rollback, purge or retention exemption is granted by this additive wire contract.

## Examples

These are valid JSON **projections of the envelopes**, omitting unrelated existing video/route fields. They are not complete seed records or evidence that a particular run/video exists. Source eligibility is assumed at the response cutoff.

### Successful list lookup, including known zero duration

```json
{
  "videos": [{
    "video_id": "v1",
    "final_outcome": "accepted",
    "current_local_metadata": {
      "attribution": "current_local_not_necessarily_evaluated",
      "title": "Example title",
      "title_availability": "current_local",
      "channel_name": "Example channel",
      "channel_name_availability": "current_local",
      "duration_seconds": 0,
      "duration_availability": "current_local",
      "source_fetched_at": "2026-11-07T10:15:30Z"
    },
    "metadata_context_status": "available"
  }],
  "pagination": {"page": 1, "page_size": 20, "total": 1, "total_pages": 1}
}
```

The example supplies no run mode. It therefore does not establish whether the accepted result represents production or a hypothetical run. The existing run read supplies that mode.

### Successful detail lookup of a known-expired local record

```json
{
  "video": {
    "video_id": "v1",
    "final_outcome": "excluded",
    "current_local_metadata": {
      "attribution": "current_local_not_necessarily_evaluated",
      "title": null,
      "title_availability": "known_expired",
      "channel_name": null,
      "channel_name_availability": "known_expired",
      "duration_seconds": null,
      "duration_availability": "known_expired",
      "source_fetched_at": null
    },
    "metadata_context_status": "available"
  }
}
```

Expired context did not produce the recorded `excluded` outcome. Neither that category nor generic historical route evidence becomes more specific because the current record expired. For a successfully missing row, the same nested null values and attribution are emitted with all three availability tokens `unknown` instead.

### Optional context read failure with mandatory detail preserved

```json
{
  "video": {
    "video_id": "v1",
    "final_outcome": "accepted",
    "route_decisions": [],
    "current_local_metadata": null,
    "metadata_context_status": "context_read_failed"
  }
}
```

This is HTTP `200`. The route array is illustrative; an existing nonempty array is preserved just as an empty one is. No context read error rewrites it or proves acceptance complete. The equivalent list failure keeps its original videos/pagination and sets the two metadata fields on each item; it does not return an empty list.

## Verification and evidence limits

### Grounding read in this authoring session

Inspected current shared working-tree source on 2026-11-07: `internal/metadata/models.go` (`LocalDisplay`), `manager.go` (`ReadLocalDisplay`, `parseLocalTime`), `store.go` (`readLocalDisplayRows`); `internal/server/{server,run_handlers}.go`; `cmd/server/main.go`; `web/src/types.ts`, `api/run-videos.ts`, `lib/current-local-metadata.ts`, `views/{RunDetailView,DecisionDetailView}.tsx`; and relevant metadata/server tests. Semantic lookup was attempted first but gopls analysis remained degraded; bounded source reads ground the field and lifecycle claims. No application source changed and no runtime/browser tests were rerun for this documentation task.

### Existing attributed proof, not new execution

Reviewer assessment `ca1cf9a7-9fa7-4794-be89-47e72ebcd65a`, session `ses_e4ce6f4deffeuFYX83oOtyAXib`, is a **targeted follow-up**, not independent rediscovery or full historical acceptance. Its executable reader/API/current SPA checks passed; its remaining documentation finding requested this exact authoritative spec, not a mandatory rewrite of the unrelated portions of the parent run-video ARS. It records known-expired cleared fields, malformed-marker withholding including source timestamp, post-query expiry, zero duration, trustworthy supplied observation copy, context-read failure/recovery, same-run retry, scoped pages/membership, zero observed provider requests during inspection and unchanged checked tables. Current-source identities and temporary artifacts are in `/tmp/opencode/local-context-final-followup-20261107/`; these are local evidence pointers, not shipped fixtures or a durability promise.

That assessment attributes passing `go test ./internal/metadata ./internal/server ./cmd/server -count=1`, tagged diagnostic composition tests, 296 frontend tests and the real-client build. These are reviewer-reported executions, not independently repeated here. Reused real-discovery/Backtest fixture history proves inspection/restart of that history, not a newly run current-build discovery/browser Backtest. Full screen-reader announcements, contrast and 200% text zoom remain unproved. Minor secondary-label readability feedback is separate from this wire contract and remains frontend/design-owned.

### Conformance requirements

Maintain producer → real SQLite → registered HTTP → bundled adapter/view composition, not only response-shaped fixtures. Required cases are successful active/partial/null/blank/zero and missing-row context; exact/past expiry; cleared fields on a known-expired row; malformed non-null expired marker; inactive and unusable/future source timing; blocked lookup crossing expiry; query/scan/iteration failure with no partial release; unconfigured reader; membership rejection and mandatory failure before optional access; one bounded page/detail metadata query and no empty-set query; mandatory evidence unchanged across metadata edits, failure and retry; no-store, no provider calls, no product writes, safe diagnostics and correct simulation wording. Consumer checks include omitted older fields, unknown tokens, valid/null/malformed source timestamps and identity-safe local retry. Tests for the design's conditional not-recorded copy cannot substitute for absent Store provenance.

## Limits and open questions

There is no open user-owned decision for these exact current/local wire fields. The parent design still owns the unresolved historical input/title-retention conflict, causal exclusion capture and old raw-audit lifecycle risk. Current metadata can assist recognition of a legacy Run15-shaped record but cannot identify which exclusion actually matched or reconstruct its compared input. This specification neither backfills those facts nor certifies existing audit storage/release as safe. Dates, production comparison, retention permission, full accessibility and whole-product acceptance remain unchanged and unproved here.
