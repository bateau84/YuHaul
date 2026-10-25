---
type: specification
title: ARS-001 — Route, Playlist, and Run-Outcome API Contracts
description: Exact public contracts for route action naming, playlist enumeration, distinct-video route match counts, run-video outcome filtering, and route/run list success and failure responses.
tags: [architecture, specification, api, routes, playlists, errors, pagination]
timestamp: 2026-10-24T00:00:00Z
---

**Status:** accepted

The API-level contracts in this artifact are settled. Route-list `match_count` counts distinct matched videos within its displayed time scope; because the Route List has no period selector, the scope is lifetime. The user-visible route/run list error and retry behavior is defined by the accepted design amendment, not by this API specification. Failed-attempt quota accounting remains explicitly outside this contract.

In everyday use, the Route Editor asks the API for the user's playlists and sends the selected playlist with the action name shown in the accepted design. The route list includes a lifetime count of distinct videos that matched each route in production decisions. If the same video matches a route in multiple production runs, it contributes once. On a run's detail page, the user can select an outcome category; the API filters the run's videos and reports totals for that filtered set. A successful empty route or run list is different from a server failure: an empty list is returned as `[]`; a failed read is an HTTP error and never masquerades as an empty result.

For example, a user with 75 playlists receives all 75 from one successful `GET /api/playlists` response, fetched through two YouTube pages. If the second page fails, the API returns an error rather than a plausible but incomplete list. If the same video matched one route in three production runs, that route's lifetime `match_count` includes it once. Selecting `failed` on a run's detail page returns only failed video outcomes, and its pagination total counts all failed outcomes in that run, not just the current page. If the route-count query fails, `GET /api/routes` fails as a whole instead of showing zero for an unknown count.

## Derived from

- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md)
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md)
- [List View Fetch Error Handling Amendment](../design/amendment-list-fetch-error-handling.md)
- [Route Identity design amendment](../design/amendment-route-uuid-identity-duplicate-names.md)
- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md)
- [BR-004 — Route Conditions and Playlist Actions](../requirements/yuhaul/br-004-route-conditions-and-playlist-actions.md) (proposed)
- [BR-020 — Run-Level and Route-Level Statistics](../requirements/yuhaul/br-020-run-level-and-route-level-statistics.md) (the distinct-video criterion is clarified for this contract; the requirement file's overall status is unchanged)
- [BR-026 — Dry-Run Evaluation](../requirements/yuhaul/br-026-dry-run-evaluation.md)
- [BR-027 — Historical Backtesting](../requirements/yuhaul/br-027-historical-backtesting.md)
- [BR-028 — Historical Run Inspection](../requirements/yuhaul/br-028-historical-run-inspection.md)
- [BR-032 — Processing Outcome Distinction](../requirements/yuhaul/br-032-processing-outcome-distinction.md)
- [OC-001 — YouTube API Quota and Failure Semantics](../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) (proposed)
- [OC-003 — Configuration Snapshot for Historical Interpretation](../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md)
- [ADR-005 — Complete `GET /api/playlists` Listing](adr-005-playlist-listing-endpoint.md)
- [ADR-006 — Route List Match Count](adr-006-route-list-match-count.md)
- [ADR-007 — Canonical Route Action Type](adr-007-canonical-route-action-type-api-boundary.md)
- [ADR-011 — Stable UUID Route Identity](adr-011-stable-uuid-route-identifiers.md)
- [ADR-012 — Proposal-Only Routes Use Inactive Identity Anchors](adr-012-proposal-route-identity-anchors.md)
- [ARS-004 — Route UUID Schema, API, and Migration Contract](ars-004-route-uuid-schema-api-migration.md)

## Satisfies

- Anchor AC3 and BR-003 AC1–9: route CRUD/listing remains available, ordered, and inspectable.
- BR-003 AC10/AC12/AC13: display names may repeat while route identity remains a stable, unambiguous public UUID independent of the name (with integer internal keys and immutable snapshots retained per ARS-004).
- BR-026, BR-027, and BR-028: route references exposed in dry-run/backtest proposals and results, run decisions, and historical inspection use UUIDs; this does not change evaluation or side-effect semantics.
- BR-015, BR-028, and OC-003: run-scoped route-stat order, when available, is projected from run-specific snapshot or decision evidence, never from mutable current route state.
- Anchor AC4 and the accepted design spec §Route Editor — Action configuration UI: route actions use the specified playlist action and real user playlists.
- Anchor AC18: this artifact specifies one quota unit per successful playlist-listing page. Comprehensive accounting of failed attempts is blocked by proposed OC-001 and is not claimed here.
- Anchor AC20 and the accepted design spec §Route List: the route list carries the required match-count field.
- BR-020 AC2 and its clarified AC7: the Route List match count counts distinct matched videos per route within the displayed scope. This API spec realizes that clarified meaning without making unrelated BR-020 status or statistics decisions.
- BR-021 AC1/AC6 and the accepted Route Identity design amendment: channel detail exposes per-route production match counts and keeps same-named routes distinct by UUID.
- BR-032 AC5: a user can filter the video outcomes within a historical run by outcome category. The accepted Run Detail design locates this filter on the run's detail view; this is not a filter on the Runs List.
- The accepted list-view error-handling amendment: route and run list clients distinguish a successful empty result from fetch failure and present retry recovery; this specification supplies the API envelopes those states depend on.

## Implements

- [ADR-005 — Complete `GET /api/playlists` Listing](adr-005-playlist-listing-endpoint.md)
- [ADR-006 — Route List Match Count](adr-006-route-list-match-count.md)
- [ADR-007 — Canonical Route Action Type](adr-007-canonical-route-action-type-api-boundary.md)
- [ADR-011 — Stable UUID Route Identity](adr-011-stable-uuid-route-identifiers.md)
- [ADR-012 — Proposal-Only Routes Use Inactive Identity Anchors](adr-012-proposal-route-identity-anchors.md)

## Scope

This specification settles the public seams for playlist enumeration, the action type field, route match counts, run-scoped route-stat identity and evaluation-order projection, run-video outcome filtering, and route/run list success/failure distinction. Route identity is a cross-cutting part of those seams: all public route references are UUID strings and route display names may repeat, as specified below and in ARS-004. Other route evaluation semantics, route deletion/history retention (ADR-003), condition operators (ADR-004), the meaning of run outcomes, failed YouTube attempt accounting, and the visual error/recovery behavior defined in the accepted list-view amendment remain unchanged.

## Current implementation and confirmation status (2026-10-24)

The following is confirmed against current source and targeted automated tests; it records realization and does not change the accepted contract above:

- **Playlist listing:** `cmd/server/main.go` follows every `nextPageToken`, preserves upstream order, and records one quota unit per successful page through the YouTube client's recorder. A later-page failure returns no partial list while prior successful-page entries remain recorded. The endpoint has no browser pagination; the Route Editor uses the one complete result. Go tests passed for multi-page listing, order, per-page quota, later-page failure, and empty success.
- **Action translation:** route CRUD, run decision detail, configuration-history version/diff/restore, dry-run drafts, and backtest proposals use `add_to_playlist` at public input/output boundaries and retain `playlist_insert` in persisted route/action/snapshot representations. Targeted Go tests passed for route round trips, legacy stored values, history serialization, and proposal-input cases.
- **Route List count:** `GET /api/routes` merges `COUNT(DISTINCT rd.video_id)` from matched decisions in scheduled/manual runs. Each list response carries integer `match_count`; route detail omits it. No `GET /api/routes/{route_uuid}/stats` handler is registered, and the server regression test observes 404 for that path. The `ListRouteMatchCounts` store method remains as the aggregate supporting this list projection; it is not a per-route endpoint. Targeted router/server tests passed.
- **Route identity (route-management CRUD/reorder, run-video decisions, statistics/analytics, and channel detail realized; other surfaces still integer):** `GET /api/routes` returns UUID `id` values with UUID child `route_id` values and the list-only `match_count`; `GET`/`PUT`/`DELETE /api/routes/{route_uuid}` parse the canonical hyphenated UUID (uppercase hex accepted on input, lowercase on output), return `400 {"error":"invalid route id"}` for malformed/numeric path IDs, and preserve the existing `404 route not found` for valid-but-unknown or soft-deleted UUIDs; `POST /api/routes` rejects any caller-supplied `id` and assigns a server-side UUID in the create transaction, accepting duplicate display names; `PUT` preserves the route UUID across rename and reconfiguration; `PUT /api/routes/reorder` accepts UUID `route_ids` and rejects malformed (`400`), duplicate (`400`), unknown/soft-deleted (`404`), and omitted-active (`400`) entries before persisting order without changing identities. Route detail omits `match_count`, and a serializer that cannot resolve a public identity fails the endpoint instead of emitting an integer. Run-video decision surfaces (`GET /api/runs/{run_id}/videos` and its decision detail) emit UUID `route_decisions[].route_id` resolved through the registry, including soft-deleted and snapshot-only route keys; `route_name` comes from that run's immutable ConfigVersion snapshot, with the current persisted label used only as legacy fallback when no usable snapshot exists (OC-003 incomplete context), and a decision whose route has no registry mapping fails the endpoint instead of emitting an integer or a name-only label. Run-scoped statistics and analytics have shipped: `GET /api/runs/{run_id}/route-stats` and `GET /api/analytics/route-match-rates` emit registry-resolved UUID `route_id` values while internal grouping and enrichment keep the integer route key, run-scoped `route_name` comes from that run's immutable ConfigVersion snapshot with the current persisted label used only as legacy fallback when no usable snapshot exists (OC-003 incomplete context), cross-run analytics keep the current persisted display label, and a missing mapping fails the endpoint instead of emitting an integer or a name. Channel detail has shipped: `GET /api/analytics/channels/{channel_id}` returns `matched_routes` as `{route_id, name, match_count}` objects — one entry per route identity with `COUNT(DISTINCT route_decision.video_id)` evidence from `scheduled`/`manual` runs only, soft-deleted routes included, ordered by name and then canonical UUID — and a missing identity mapping or route label fails the whole request with its existing `500` contract. Dry-run/backtest proposals have shipped: `POST /api/runs/dry-run` `draft_configuration` and `POST /api/backtest` `proposed_configuration` take UUID `routes[].id` values — canonical hyphenated UUIDv4 (uppercase hex accepted on input, lowercase on output), `400 {"error":"invalid route id"}` for missing, malformed, non-v4, numeric-alias, or duplicate-within-one-proposal IDs, and never name-based identity — resolved through the ADR-012 inactive identity-anchor ingest in one transaction with the immutable snapshot and run row, so results for those runs project UUID route references through the run-video, statistics, and configuration-history surfaces above. No route-identity field remains integer on the wire; migration 012 and the startup backfill already assign and validate UUIDv4 mappings for every route before serving. Targeted Go tests cover real store+handler duplicate-name independence, UUID-only responses, numeric-alias rejection, UUID stability across rename/reorder/soft-delete, reorder validation, detail-without-`match_count`, run-video evidence (snapshot labels across rename, soft delete, snapshot-only keys and duplicate names, legacy/corrupt-snapshot fallback, and fail-closed 500 on a missing identity mapping), and statistics/analytics evidence (UUID-only `route_id`, duplicate-name routes kept distinct, production-only distinct-video counts excluding dry-run/replay/backtest, run-snapshot labels surviving a later rename, `matched_routes` empty `[]`, and fail-closed 500s on a missing identity mapping).
- **Run outcome filter:** `GET /api/runs/{id}/videos` applies `outcome` to both the returned page and `pagination.total`/`total_pages`; accepted values are `accepted`, `rejected`, `excluded`, `deferred`, `failed`, and `not_yet_evaluated`. An unsupported value returns `400 {"error":"invalid outcome filter"}`. `RunDetailView` sends the selected category with `page_size=100` and displays the filtered total. It currently shows the first returned page and a “Showing x of N” note when more results exist; it has no control to request later pages. Targeted server and Run Detail tests passed.
- **Route/run list failures:** server list handlers return array-valued empty successes and JSON `500` errors on store failures. API adapters reject missing/non-array collection fields rather than converting them to empty arrays; `RoutesView` and `RunsView` implement the accepted retry/error states. This closes the previously documented UI implementation gap.
- **Run route-stat order:** conformant as of 2026-10-24. `internal/stats/store.go::ListRouteStats` no longer selects `route.evaluation_order`; `RouteStats.EvaluationOrder` is an optional field projected from the run's immutable ConfigVersion snapshot (`routes[].evaluation_order`, positive values only), falling back to persisted `route_decision.evaluation_order` for that route in the run only when the decisions agree on one positive integer, and omitted when neither source supplies one consistent historical order. The ADR-003/ADR-012 negative `-route_id` sentinel on current soft-deleted and proposal-anchor rows never reaches the wire. Statistics tests assert the run-scoped order projection for soft-deleted routes, proposal-only anchors, decision fallback, snapshot-over-decision precedence, unusable/inconsistent evidence omission, and the unchanged ordinary active-route case.

Verification run on 2026-10-24: `go test -count=1 ./internal/server ./internal/router ./internal/config ./cmd/server` passed; the targeted Vitest run listed in ADR-005 passed 43 tests; `npm run build` completed successfully. Route UUID CRUD/reorder verification: `go build ./...` clean and `go test -count=1 ./...` all packages passed after the route-management API conversion. Statistics/analytics/channel-detail UUID verification: `go build ./...` clean, `go vet ./internal/stats` clean, `go test -count=1 ./...` all packages passed, and `go test -tags integration ./e2e/... -count=1` passed; targeted mutation checks confirmed that dropping the production-run filter and the distinct-video count, and dropping run-snapshot labels, each fail the new statistics tests. Run route-stat order verification: `go build ./...` clean, `go vet ./internal/stats` clean, `go test -count=1 ./internal/stats ./internal/server ./internal/router ./internal/config` and `go test -count=1 ./...` all pass, `go test -tags integration ./e2e/... -count=1` passes; targeted mutation checks confirmed that reintroducing the current-row `route.evaluation_order` projection fails all six new order-projection tests, dropping the decision fallback fails the fallback and fall-through tests, and accepting non-positive orders from either source fails the omission tests.

## Structural constraints

- The bundled web application and API are part of the documented single-user product; no separately versioned third-party API is established.
- Route list order remains ascending `evaluation_order`; soft-deleted routes remain excluded per ADR-003.
- `route_decision` is the durable source for derived match counts. Non-production run types do not contribute.
- The Route List has no time-period selector; its `match_count` scope is lifetime. The count unit is distinct `route_decision.video_id` values per route, not matched decision rows.
- Run outcome filtering applies to the video list within one run. `GET /api/runs` Run List date/type/status filters remain separate and do not satisfy BR-032 AC5 by themselves.
- Public `action_type` is a domain/API value. Existing internal persistence uses `playlist_insert`; it is not rewritten by this specification.
- An API `200` collection response is complete and non-null. Failure is represented by a non-2xx status and JSON error object, not an empty collection.

## Route identity across these API contracts

The route identity type is a canonical UUIDv4 string on every public surface. The exact storage mapping, UUID validation, history projection, proposal-only identity anchors, and compatibility contract are specified by [ARS-004](ars-004-route-uuid-schema-api-migration.md) and [ADR-012](adr-012-proposal-route-identity-anchors.md). Run-specific decision labels come from the same immutable snapshot as the evaluated route configuration; names remain labels and do not replace identity. This section makes the route-bearing surfaces covered by this API specification explicit:

| Surface | Public route identity |
|---|---|
| `GET /api/routes`, `POST /api/routes` response, `GET`/`PUT`/`DELETE /api/routes/{route_uuid}` | Route object's `id` is a UUID string; route paths use `{route_uuid}`. A create request does not choose a persistent route ID. |
| `PUT /api/routes/reorder` | Every `route_ids` array entry is a UUID string; array order retains the existing reorder meaning. |
| `GET /api/runs/{run_id}/videos` and video decision detail | Every `route_decisions[].route_id` is a UUID string. `route_decisions[].route_name` is the route label from that run's immutable ConfigVersion snapshot, so proposal-only routes and historical renames retain the evaluated name. `run_id` and `video_id` keep their existing non-route identifier types. |
| `GET /api/runs/{run_id}/route-stats`, `GET /api/analytics/route-match-rates`, and route list projections | Every route-statistic or analytics `route_id` is a UUID string; route-list items use UUID `id`. Run-scoped `route_name` is from that run's immutable ConfigVersion snapshot; cross-run analytics and route lists use the current persisted display label. `match_count` remains an integer statistic, not an identity. |
| `GET /api/analytics/channels/{channel_id}` | The top-level `matched_routes` array contains objects with UUID `route_id`, display `name`, and integer production `match_count`; same-named routes remain separate entries. |
| Duplicate provenance | `source_route_id`, when present, is a UUID string; its existing `null` state remains `null`. |
| Configuration history, diff, restore, draft configuration | Public snapshot route `id` and route IDs nested in diff/restore representations are UUID strings. ConfigVersion IDs (`id`, `from_id`, `to_id`, and `compare_to`) remain their existing non-route integer identifiers. |
| Dry-run/backtest proposal and result | Route IDs in `draft_configuration` and `proposed_configuration`, and every route reference in results for those runs, are UUID strings. Run IDs remain their existing non-route identifiers. |

Names are display labels, not keys: they are required non-empty values under BR-003 AC10, but two or more routes may have the same name. No route lookup, update, reorder, history association, statistics grouping, or client-side identity may use a name as a key or merge same-named routes. Public API consumers MUST use UUIDs for route identity and MUST NOT accept or emit internal integer route keys as aliases. Internal integer route keys and foreign keys, and the integer IDs retained inside stored configuration snapshots, remain unchanged; public history serializers project them through the ARS-004 identity registry.

The path placeholders `{run_id}`, `{video_id}`, `{channel_id}`, and config-version `{id}` above continue to identify their respective non-route entities; they are not converted to UUIDs by the route identity decision. In particular, `POST /api/config/versions/{id}/restore` addresses a ConfigVersion with its existing identifier while returning UUIDs for route identities in its snapshot.

## Interface

Existing JSON fields not named below keep their current meanings and names. All response bodies use `Content-Type: application/json`.

### Route action type

For route actions in `POST /api/routes`, `PUT /api/routes/{route_uuid}`, `GET /api/routes`, and `GET /api/routes/{route_uuid}`:

```json
{
  "action_type": "add_to_playlist",
  "target_playlist_id": "PL..."
}
```

- `action_type` is required and MUST equal the exact string `add_to_playlist`.
- `target_playlist_id` is required and MUST be non-empty.
- An invalid/missing action type or empty target returns `400` with `{"error":"..."}`. The endpoint does not accept the internal spelling `playlist_insert` as an input alias.
- Responses MUST emit `add_to_playlist`, including when the stored route-action record uses `playlist_insert`.
- Public executed-action details and configuration-history route/action representations MUST also emit `add_to_playlist`. Existing stored configuration snapshots and executed-action rows remain unchanged; their public serializers normalize the legacy identifier.
- Internal route persistence, execution, and idempotency continue to use `playlist_insert`. The route action is realized through YouTube `playlistItems.insert`; these names are not interchangeable at the public boundary.

### `GET /api/playlists`

The endpoint returns every playlist belonging to the authenticated user. It does not expose page tokens to the browser.

| Result | Status | Response |
|---|---:|---|
| Complete list, one or more items | `200` | `{"playlists":[{"playlist_id":"PL...","playlist_name":"My Playlist"}, ...]}` |
| Complete list is empty | `200` | `{"playlists":[]}` |
| No active token / provider not configured | `502` | `{"error":"not authenticated"}` |
| Any playlist-page/API/network error | `502` | `{"error":"failed to list playlists"}` |

The server-side provider requests `playlists.list` with `mine=true`, `part=snippet`, and `maxResults=50`; it follows each `nextPageToken` until none remains and concatenates items in upstream page/item order. Each successful page is recorded once as one `playlists.list` quota unit. A failure at any page returns no partial `playlists` array; costs already recorded for prior successful pages are retained. No cache, browser pagination, or automatic retry is introduced by this contract.

The contract covers successful-page quota entries. Whether failed API attempts are also charged/logged with a response category is unresolved in proposed OC-001 and is not inferred here.

### `GET /api/routes`

Returns the active route list in ascending `evaluation_order`:

```json
{
  "routes": [
    {
      "id": "3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce",
      "name": "Ambient",
      "evaluation_order": 1,
      "actions": [{"action_type":"add_to_playlist","target_playlist_id":"PL..."}],
      "match_count": 12
    }
  ]
}
```

- `routes` is always an array. A successful empty result is `{"routes":[]}`.
- Each list item MUST include integer `match_count`; zero is explicit (`0`), not null or omitted.
- `match_count` is a lifetime projection derived by counting distinct `route_decision.video_id` values per route for rows with `decision = 'matched'`, joined to production runs `run.run_type IN ('scheduled','manual')`. Dry-run, replay, and backtest rows do not count. If a video matches the same route in more than one production run, it contributes once to the lifetime count (ADR-006, BR-020 AC7).
- The count covers currently listed active routes; soft-deleted routes are omitted from the list.
- If route loading or count aggregation fails, the whole response is `500 {"error":"failed to list routes"}`. The server MUST NOT return a partial list or substitute zero for an unavailable count.

### `GET /api/routes/{route_uuid}`

Returns the full active route configuration with canonical `action_type` values. It does not include `match_count`: this is a configuration response, not the Route List statistics projection. Exact resource errors are `400 {"error":"invalid route id"}`, `404 {"error":"route not found"}` for absent/soft-deleted routes, and `500 {"error":"failed to load route"}` on store failure.

`GET /api/routes/{route_uuid}/stats` is not part of the supported API surface. The route list is the one supported lifetime match-count surface. The server does not register a per-route lifetime stats handler; `ListRouteMatchCounts` is a list-level aggregate used to construct `GET /api/routes`, not a separate per-route API. Run-scoped route statistics remain available at `GET /api/runs/{run_id}/route-stats`; their route references use UUIDs.

The separately registered `GET /api/runs/{run_id}/stats` and `GET /api/runs/{run_id}/route-stats` endpoints are run-scoped statistics surfaces; neither is a per-route lifetime `match_count` endpoint or changes the list-only placement above. Any `route_id` in a route-statistics result is a UUID string.

For each `route_stats[]` item, `evaluation_order`, when present, is the order for that route in the requested run—not the route row's current order. Resolve it from that run's immutable ConfigVersion snapshot (`routes[].evaluation_order`). If that snapshot has no usable order for the route, use the persisted `route_decision.evaluation_order` for that route in the run only when the persisted decisions agree on one positive integer; if neither historical source supplies one usable order, omit `evaluation_order`. The current `route.evaluation_order` MUST NOT be used as a fallback: ADR-003 assigns deleted routes the negative `-route_id` sentinel, and ADR-012 uses the same sentinel for proposal-only identity anchors. Those internal values are storage bookkeeping, not evaluation evidence. Do not negate, absolute-value, clamp, or otherwise transform a sentinel into an apparent historical ordinal. This rule also applies when OC-003's snapshot is missing or undecodable; any existing legacy label fallback does not authorize a current-order fallback.

### `GET /api/analytics/channels/{channel_id}` — matched route breakdown

The existing channel-detail endpoint returns its `ChannelDetail` object directly at the top level. Its `matched_routes` field is an array of route-identity objects, not a string list and not a wrapper object:

```json
{
  "matched_routes": [
    {
      "route_id": "3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce",
      "name": "Ambient",
      "match_count": 12
    },
    {
      "route_id": "4c7c3b7e-f09e-4bf4-9b55-aec0cf479b51",
      "name": "Ambient",
      "match_count": 4
    }
  ]
}
```

The example shows only the `matched_routes` field; all other channel-detail fields and their existing top-level placement remain unchanged.

- Each entry represents one route identity with at least one matched video from this channel in a production run (`run_type` is `scheduled` or `manual`). Soft-deleted routes remain included when their production evidence exists. `route_id` is the canonical lowercase UUIDv4; `name` is the route's current persisted display label and may equal another entry's name; `match_count` is an integer count of distinct internal video records (`route_decision.video_id`) matched by that route for this channel across those production runs.
- Dry-run, replay, and backtest decisions do not contribute. This keeps the channel usefulness projection operational rather than allowing hypothetical runs to affect it (BR-027 AC7). A route matched in multiple production runs for the same channel video contributes that video once, consistent with the distinct-video Route List count.
- Entries are ordered by `name` ascending and then canonical `route_id` ascending. `matched_routes` is always an array; no matching production decisions returns `[]`.
- Grouping and client keys use `route_id`, never `name`; duplicate names remain separate entries. The UI displays the route name with the accepted short UUID suffix.
- A missing route-identity mapping fails the whole channel-detail request using its existing `500 {"error":"failed to load channel stats"}` response. The endpoint MUST NOT return a partial list, integer route ID, or name-only fallback. Missing channel ID remains `400`; unknown channel remains `404`.

The current implementation satisfies this target contract: `matched_routes` is an array of `{route_id, name, match_count}` objects resolved through the registry, counts distinct `route_decision.video_id` values from `scheduled`/`manual` runs only, keeps same-named routes as separate entries, orders by name and then canonical UUID, and returns `[]` when no production decision matches. A missing identity mapping or route label fails the request with the existing `500` response instead of degrading to integers or names.

### `GET /api/runs`

Returns the run collection in the current API order:

```json
{"runs":[...]}
```

- `runs` is always an array. A successful empty result is `{"runs":[]}`.
- A store failure returns `500 {"error":"failed to list runs"}`.
- A failed read MUST NOT be represented as an empty array.

### `GET /api/runs/{id}/videos?outcome=`

Returns the paginated video-outcome list for the specified run. The optional `outcome` query parameter filters by the video's `final_outcome`; an absent or empty value returns all outcome categories. Valid values are exactly `accepted`, `rejected`, `excluded`, `deferred`, `failed`, and `not_yet_evaluated` (BR-032). Filtering is applied before both page selection and count aggregation.

```json
{
  "videos": [{"video_id":"v1","final_outcome":"failed"}],
  "pagination": {"page":1,"page_size":100,"total":3,"total_pages":1}
}
```

- `pagination.total` is the number of videos in the run matching the selected outcome across all pages; `total_pages` is computed from that filtered total and the effective page size. A valid filter with no matches returns `videos: []`, `total: 0`, and `total_pages: 0`.
- For an existing run and otherwise valid request, the endpoint returns `400 {"error":"invalid outcome filter"}` for a non-empty unsupported outcome value; it MUST NOT silently ignore an invalid filter and return unfiltered results.
- Existing pagination uses `page` and `page_size`; defaults are page `1` and page size `20`, with a maximum page size of `100`. Videos are ordered newest-first by `discovered_at`.
- The Run Detail view sends the selected outcome to this endpoint. It currently requests page size `100`, displays the returned filtered total, and does not expose a control for subsequent pages. This is implementation status, not a promise that one response contains every video in a run.

### Error envelope and client behavior

API errors use `{"error":"message"}` with the status above. Clients MUST propagate non-2xx responses as errors. A missing, null, or otherwise invalid collection field in a successful response violates the contract and MUST NOT be normalized to a successful empty collection.

This establishes the API distinction consumed by the accepted list-view amendment. The UI presents the specified fetch-error and retry states; it must not treat a failed or malformed response as an empty list.

## Invariants and failure behavior

1. An empty successful collection is distinguishable from an unavailable collection.
2. Route counts are derived from evidence and only from production runs.
3. A route-list count failure fails the entire response; it cannot appear as zero.
4. Playlist listing never reports a partial page sequence as complete success.
5. API action fields always use `add_to_playlist`; internal `playlist_insert` remains a persistence/execution representation.
6. Previously persisted action rows and configuration snapshots are not rewritten by this contract.

## Compatibility and evolution

- This bundled, same-origin API has no documented external-client versioning guarantee. Requests using the old wire value `playlist_insert` are not accepted after this contract is implemented.
- Readers of existing internal records and configuration snapshots continue to recognize the stored value `playlist_insert`; API serializers emit `add_to_playlist` without mutating those historical records.
- A future separately deployed client or versioned API would require an explicit compatibility decision before changing accepted wire values.

## Verification

- API action round trip: send `add_to_playlist`, verify database/internal route value is `playlist_insert`, and verify all public route representations return `add_to_playlist`; reject `playlist_insert` and unknown values with 400.
- History compatibility: verify a prior config snapshot remains byte-for-byte unchanged in storage, is readable for replay/inspection, and is emitted publicly with canonical action values.
- Playlist pages: serve at least 51 playlists over two fake-provider pages; verify all entries, stable page order, and exactly one quota unit for each successful page.
- Playlist partial failure: fail the later page; verify 502, no partial list, and retained earlier successful-page ledger entries.
- Route-count API shape: verify the list-only integer field, explicit zero, soft-deleted-route exclusion, production-mode filtering, distinct-video behavior across repeated production runs, and count-query failure behavior.
- Run route-stat order projection: persist a run snapshot with a known route order, then soft-delete the route or use a proposal-only anchor whose current row has `evaluation_order = -id`; verify the response uses the snapshot order, falls back to one consistent positive `route_decision.evaluation_order` only when no usable snapshot order exists, and omits the field when neither source supplies one consistent historical order. Verify no sentinel, current-order fallback, or fabricated ordinal is emitted.
- Channel matched routes: verify the `matched_routes` object schema, canonical UUID identity, name+UUID ordering, separate same-named entries, distinct-video production count, exclusion of dry-run/replay/backtest decisions, `[]` empty result, and whole-endpoint failure if identity projection is incomplete.
- Proposal-only route identity: verify unknown and registry-only UUIDs map to durable internal keys/soft-deleted anchors, `route_decision` FKs remain valid, active routes/configuration remain unchanged, proposal snapshots retain their evaluated names, and failed transactions leave no partial proposal identity or run (ARS-004/ADR-012).
- Route/run list envelope: verify empty success is `[]`, store error is the specified 500 JSON body, and clients do not turn `null` into an empty result.
- Run outcome filter: for each accepted category, verify only matching `final_outcome` records are returned and both pagination totals describe the filtered set; verify no-match returns zero totals and unsupported outcome returns `400`.
- Run Detail composition: selecting/clearing categories issues `getRunVideos` with the corresponding query and displays the filtered result/total. The UI test uses a server-filter stand-in; the Go handler test independently verifies server filtering.
- Composition: exercise Route Editor → public route API → persistence → route-list response and playlist picker against the real server, with only YouTube replaced through ADR-001's base-URL seam.

## Open questions

- **Specifier / owner of OC-001 — Does not block the API contracts specified here:** decide whether failed YouTube API attempts must be included in the consumption log with a response category. The successful-page accounting contract remains as stated; this artifact makes no complete failed-call accounting claim.

The route-name/identity semantics are settled by accepted BR-003 AC10/AC12/AC13, ADR-011, and the accepted [Route Identity design amendment](../design/amendment-route-uuid-identity-duplicate-names.md); the previous requirements/design reconciliation is closed. UI implementation conformance remains an implementation gate. Proposal-only UUID mapping is settled by ADR-012. There are no remaining architecture-level questions about route identity representation, channel matched-route identity shape, Route List count unit/source/scope, BR-032 AC5 outcome filtering, or list-page fetch-error contract.
