---
type: specification
title: ARS-004 — Route UUID Schema, API, and Migration Contract
description: Exact database, public API, historical snapshot, duplicate-name, and rollout requirements for stable UUID route identity while preserving integer-keyed route history.
tags: [architecture, specification, yuhaul, routes, uuid, api, migration, history]
timestamp: 2026-10-24T00:00:00Z
---

**Status:** accepted

Route names are labels that a user can choose; route IDs are the stable identities used by the database, API, history, and UI. After this change, two routes may both be named `Music`, but each has a different UUID and can be edited, reordered, deleted, and found in historical evidence independently. A historical run decision continues to point to the same internal integer key, while public `route_id` values use that key's stable UUID rather than its current name or database sequence number.

This artifact specifies the target schema and public API contract. Implementation status: migration 012 creates the `route_identity` registry and a startup backfill assigns and validates UUIDv4 mappings before the scheduler or HTTP server can start (B1). Route-management API conversion has shipped: `GET /api/routes` and route `GET`/`POST`/`PUT`/`DELETE`/`PUT /api/routes/reorder` use UUID `id`/`route_id`/`route_ids` values with the rules below, resolved through the registry, and no integer or numeric-alias route identity is accepted or emitted on those surfaces. Run-video decision evidence has shipped: `GET /api/runs/{run_id}/videos` and its decision detail emit UUID `route_decisions[].route_id` resolved through the registry — including soft-deleted and snapshot-only route keys — take `route_name` from that run's immutable ConfigVersion snapshot with the current persisted label only as legacy fallback when no usable snapshot exists (OC-003 incomplete context), and fail the endpoint rather than emit an integer or a name-only label when a mapping is missing. Configuration-history projection has shipped: `GET /api/config/versions`, `GET /api/config/versions/{id}`, its `/diff`, and its `/restore` emit UUID snapshot route IDs resolved through the registry — including snapshot-only route keys whose `route` row no longer exists — fail closed with the endpoint's existing internal-error contract when a stored snapshot route key has no mapping, never rewrite stored snapshots, and keep ConfigVersion identifiers (`id`, `from_id`, `to_id`, `compare_to`) integer. Statistics/analytics/channel-detail projection has shipped: `GET /api/runs/{run_id}/route-stats` and `GET /api/analytics/route-match-rates` emit registry-resolved UUID `route_id` values while internal joins and grouping keep the integer route key, run-scoped `route_name` comes from that run's immutable ConfigVersion snapshot with the current persisted label as OC-003 legacy fallback, cross-run analytics keep the current persisted label, and `GET /api/analytics/channels/{channel_id}` returns `matched_routes` as `{route_id, name, match_count}` objects — production-only `COUNT(DISTINCT route_decision.video_id)` evidence, soft-deleted routes included, ordered by name then canonical UUID, same-named routes kept separate — with those endpoints failing closed when a mapping or route label is missing. Dry-run/backtest proposal ingest has shipped (B7, ADR-012): `POST /api/runs/dry-run` `draft_configuration` and `POST /api/backtest` `proposed_configuration` accept canonical UUIDv4 `routes[].id` values (uppercase hex input normalized to lowercase; missing, malformed, non-v4, numeric-alias, or duplicate UUIDs within one proposal are rejected with `400 {"error":"invalid route id"}` before any run starts), and the pipeline's single SQLite transaction resolves each UUID through the registry — reusing an existing mapping and route row unchanged, recreating an inactive identity-anchor row at exactly the mapped key when a snapshot-only identity has no `route` row, or allocating one internal key with the supplied UUID mapping plus a soft-deleted anchor (`is_enabled = false`, `evaluation_order = -id`, proposal `name`/`description`/`is_final`/`condition_composition` copied, no route child rows, no route-mutation ConfigVersion callback) for an unknown UUID — then commits the translated immutable ConfigVersion snapshot (internal integer route IDs) and the run row together; a failed transaction leaves none of them committed, and repeated or concurrent use of one previously unknown UUID converges on the single registry mapping. Anchor rows never appear in `GET /api/routes` or in production evaluation, and run-video/statistics/history projections emit the original UUIDs for proposal runs. No route-identity surface remains unconverted.

## Derived from

- [ADR-011 — Stable UUID Route Identity Independent of Display Name](adr-011-stable-uuid-route-identifiers.md).
- [YuHaul System Architecture](yuhaul-system-architecture.md).
- [ADR-003 — Route Deletion Preserves Historical Decision Evidence](adr-003-route-soft-delete-preserve-history.md).
- [ARS-001 — Route, Playlist, and Run-Outcome API Contracts](ars-001-route-and-playlist-api-contracts.md).
- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md).
- [BR-010 — Duplicate Provenance for Discovery Credit](../requirements/yuhaul/br-010-duplicate-provenance-for-discovery-credit.md).
- [BR-015 — Explainable Route Decisions](../requirements/yuhaul/br-015-explainable-route-decisions.md).
- [BR-026 — Dry-Run Evaluation](../requirements/yuhaul/br-026-dry-run-evaluation.md).
- [BR-027 — Historical Backtesting](../requirements/yuhaul/br-027-historical-backtesting.md).
- [BR-028 — Historical Run Inspection](../requirements/yuhaul/br-028-historical-run-inspection.md).
- [BR-025 — Configuration History for Statistics Interpretation](../requirements/yuhaul/br-025-configuration-history-for-statistics-interpretation.md).
- [OC-003 — Configuration Snapshot for Historical Interpretation](../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md).

## Satisfies

- The explicit user direction for stable UUID route IDs and non-unique display names.
- BR-003 AC2/AC4/AC8/AC11: route inspection, deletion-history preservation, durable ordering/configuration identity, and run snapshot association.
- BR-010 AC4/AC5 and BR-015 AC1/AC4/AC5/AC6: historical provenance and route decision evidence retain a stable, independently resolvable route identity.
- BR-026, BR-027, and BR-028: route identity field types in dry-run/backtest proposals, their route-referencing results, and historical run inspection use UUIDs; these identity rules do not redefine run evaluation or side-effect semantics.
- BR-025 AC4/AC5/AC6/AC7: historical snapshots and their comparisons keep their route association across rename/deletion and restart.
- OC-003 guarantees 1–8: stored snapshots remain immutable and retain their historical route association.
- BR-003 AC10/AC12/AC13: route display names are non-empty labels that may repeat; stable identity is independent of the name and remains unambiguous in historical data.
- BR-021 AC6 and the accepted route-identity design amendment: channel detail keeps distinct same-named matched routes addressable by UUID and displays each route label with its production match count; the exact channel response shape is in ARS-001.

The accepted [Route Identity design amendment](../design/amendment-route-uuid-identity-duplicate-names.md) resolves the former unique-name and duplicate-label presentation conflict. The amendment is the authority for the short UUID suffix shown with route names; this architecture contract does not claim that the implementation already conforms to that design.

## Implements

- [ADR-011 — Stable UUID Route Identity Independent of Display Name](adr-011-stable-uuid-route-identifiers.md).
- [ADR-012 — Proposal-Only Routes Use Inactive Identity Anchors](adr-012-proposal-route-identity-anchors.md).
- This specification defines the storage mapping and full route-identity projection used by [ARS-001](ars-001-route-and-playlist-api-contracts.md). ARS-001 now states the UUID field types across its route CRUD, reorder, run-decision, statistics, provenance, history, dry-run/backtest, and channel matched-route surfaces; playlist enumeration, the existing Route List count unit/source/scope, outcome filtering, and list-error contracts remain unchanged.

## Scope

This contract changes persisted route public identity and every API field that refers to a route, including the matched-route projection in channel detail. It does not change run IDs, video IDs, child row primary keys, route evaluation order, route soft-delete semantics, action naming, or route configuration snapshot immutability. The channel per-route count is defined in ARS-001. A UUID is not an authorization credential.

## Structural constraints

- Current relational storage uses `route.id INTEGER PRIMARY KEY AUTOINCREMENT`; conditions, actions, exclusions, route decisions, and duplicate provenance reference this integer.
- The migration MUST retain `route.id` and all existing integer foreign keys. No route decision, condition audit, executed action, config version row, or stored ConfigVersion snapshot is rewritten or deleted.
- Route soft deletion retains the route row (ADR-003). Earlier application versions could hard-delete route rows while ConfigVersion snapshots retained their integer IDs; the migration must preserve identity for those snapshot-only IDs too.
- The bundled SPA and API are deployed together. No separately versioned external route API or numeric-ID compatibility contract is established.
- The route-name field remains required, non-empty after trimming whitespace, and limited to 100 characters. The accepted change is that names need not be unique. Names are not normalized or used as route keys.

## Database schema and migration

### Logical schema

The existing `route` table keeps its current columns. Add this registry:

| Column | Type / rule | Meaning |
|---|---|---|
| `route_identity.route_id` | `INTEGER PRIMARY KEY`; deliberately **no FK** to `route` | Internal route identity key; can also represent a historical ID whose route row was removed before ADR-003 |
| `route_identity.public_id` | Canonical UUIDv4 text; `NOT NULL UNIQUE`; immutable | Stable public route identity |

Every currently extant route row must have exactly one `route_identity` row before the API serves. Registry rows are append-only and may outlive their corresponding `route` row, so the registry does not cascade or delete identities. No unique index or validation rule on `route.name` is added. If an older deployment has a route-name uniqueness index outside the checked-in schema, the migration must remove it before duplicate names are accepted; the current checked-in schema does not declare one.

All existing child/history columns that refer to `route.id` remain integer FKs. Internal joins continue to use those integers.

### Versioned migration and startup backfill

The checked-in SQL migrations currently reach version 011. The route identity migration is the next schema version and MUST perform these operations in order:

1. Create `route_identity(route_id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE)`. Do not add a foreign key from this table to `route`.
2. Install triggers that reject `UPDATE` and `DELETE` of an existing identity row. This makes established UUID mappings immutable and append-only.
3. Immediately after the existing `golang-migrate` SQL migrations complete, and before serving requests, run one SQLite transaction that gathers the union of route IDs from:
   - every row in `route` (including soft-deleted rows);
   - every surviving route-reference column (`route_condition.route_id`, `route_action.route_id`, `route_exclusion.route_id`, `route_decision.route_id`, and non-null `duplicate_match.source_route_id`); and
   - valid numeric `routes[].id` fields from stored ConfigVersion snapshot JSON.
4. For each distinct ID without a registry row, generate a cryptographically random UUIDv4 and insert the mapping. Use the already-declared UUID library; no new service or dependency is required.
5. Before serving, verify that every extant route/FK reference has a mapping and that every public ID is canonical UUIDv4 and distinct. A valid snapshot-only ID must also have a mapping. Backfill/validation failure prevents startup; no route API may fall back to an integer ID.

The backfill is idempotent: it inserts only missing `route_id` mappings and never replaces assigned UUIDs. Because the existing migration runner applies embedded SQL, schema creation and the application-data backfill are separate. If a process stops after the SQL migration is recorded but before backfill completes, the next startup repeats the missing-only backfill before opening the API. One application writer performs the migration, consistent with the single-replica SQLite deployment.

For the release that first ships this migration/backfill, `Deployment/yuhaul` MUST use `replicas: 1` and `spec.strategy.type: Recreate`. `Recreate` terminates all old ReplicaSet Pods before creating the new-version Pod, preventing old and new binaries from overlapping against the same SQLite PVC while the migration/backfill runs. The deployment contract is:

```yaml
spec:
  replicas: 1
  strategy:
    type: Recreate
```

The previous/default `RollingUpdate` strategy is not acceptable for this release. This stop-before-start rollout may cause downtime; no zero-downtime migration guarantee is made. No second Deployment, Job, or process may run the migration or write the database concurrently. The current deployment manifest is not evidence that this future release constraint has been applied; see [ARS-003](ars-003-traefik-basic-auth-kubernetes-contract.md) for the deployment-level verification gate.

Malformed legacy ConfigVersion JSON retains OC-003's existing incomplete-context behavior. Migration does not rewrite or repair the snapshot. IDs that can be decoded from valid snapshots are registered even when their old route rows no longer exist.

The UUID migration is forward-only for assigned identities: no down migration may delete or regenerate registry rows. The backend and bundled SPA switch to UUID route APIs together. A pre-change binary does not implement UUID lookup or UUID generation for new routes, so writes through that old API after migration are not a supported rollback. If release rollback is required after UUIDs have been exposed or route writes have occurred, an explicit compatibility/restore decision is required; do not silently drop the registry or restore an old database in a way that loses post-migration history.

### New route creation

The server generates a UUIDv4 before creating a route. It inserts the route, obtains its internal integer key, and writes `(route_id, public_id)` to `route_identity` in the same transaction as the route's child rows. Any failure rolls back route and identity together. A primary-key or unique-ID collision fails the transaction and never overwrites or reassigns an existing ID. Update, reorder, rename, enable/disable, and soft delete do not modify the registry.

### Proposal-only route identities

Dry-run drafts and backtest proposals can contain a route UUID that has not previously been saved. Because `route_decision.route_id` remains an integer FK to `route.id`, the proposal-ingest transaction MUST resolve each route UUID before storing its ConfigVersion or evaluating videos. The precise realization is in [ADR-012](adr-012-proposal-route-identity-anchors.md): reuse an existing mapping; if a mapped historical-only ID has no `route` row, recreate an inactive identity-anchor row at that same integer key; for a valid UUID absent from the registry, allocate one internal key and persist the supplied canonical UUID mapping with an inactive anchor row.

An anchor row is committed with `deleted_at IS NOT NULL`, `is_enabled = false`, and `evaluation_order = -id`; it has no condition/action/exclusion child rows and is not loaded by active-route queries. Its required route-table metadata copies the proposal route's `name`, `description`, `is_final`, and `condition_composition`; creation/update/deletion timestamps are set in the same transaction. Proposal evaluation uses the full route definition in the proposal's immutable ConfigVersion snapshot, not the anchor row. The transaction covers required anchors/mappings, translated ConfigVersion snapshot, and run row; failed validation or persistence leaves none of these new records committed. Repeated/concurrent use of one UUID resolves to one mapping. Proposal-only UUIDs are not implicitly promoted to active routes; `POST /api/routes` continues to assign a new server-generated UUID.

Run-specific route names for proposal evaluations are projected from that run's immutable ConfigVersion snapshot. The anchor's route name is not an authority for historical decision display. Public IDs are UUID strings; stored ConfigVersion and decision IDs remain integer keys.

### Historical preservation

- Migration does not modify `route.id`, `route_decision.route_id`, child-table `route_id` values, `duplicate_match.source_route_id`, evaluation-order evidence, or any other existing route FK.
- Soft-deleted rows and snapshot-only legacy IDs are assigned one persistent registry UUID. The no-FK registry preserves historical identity even if a route row is absent.
- ConfigVersion snapshots remain unchanged in storage. Their route `id` values remain internal integers; public serializers resolve them through `route_identity.route_id → route_identity.public_id`.
- Migration preserves every route decision and evidence row that exists at migration time. It cannot reconstruct route decisions already cascade-deleted by an older hard-delete implementation; it does not synthesize replacement evidence.
- If an API serializer cannot resolve a referenced route key, it MUST fail according to that endpoint's existing error contract. It MUST NOT emit an integer, omit identity silently, or substitute route name.

## Public API contract

### Identity representation

The public route ID is a canonical, hyphenated UUIDv4 string (lowercase on output). Request parsers may accept uppercase hexadecimal in an otherwise canonical UUIDv4; output is always lowercase. Decimal integer route IDs are invalid and are not accepted as aliases.

Every public JSON field whose semantic meaning is a route reference MUST use the UUID string:

| Public representation | Required type |
|---|---|
| Route entity `id` in `GET /api/routes`, `GET /api/routes/{route_uuid}`, and `POST /api/routes` response | UUID string |
| Path `{route_uuid}` in route `GET`, `PUT`, and `DELETE` | UUID string |
| Route-condition/action/exclusion `route_id` | UUID string |
| `route_ids` entries in `PUT /api/routes/reorder` | UUID strings, preserving request order |
| Run-video `route_decisions[].route_id` and decision-detail `route_decisions[].route_id` | UUID string |
| `route_stats[].route_id` in run-scoped route statistics and `routes[].route_id` in route-match analytics | UUID string |
| Duplicate-provenance `source_route_id` | UUID string or existing `null` state |
| ConfigVersion snapshot `routes[].id`, route-diff route `id`/nested route IDs, and public restore representation | UUID string |
| Route `id` values in `draft_configuration` for `POST /api/runs/dry-run` and `proposed_configuration` for `POST /api/backtest` | UUID string; all route references in their result/output surfaces are UUID strings as well |
| `matched_routes[].route_id` in `GET /api/analytics/channels/{channel_id}` | UUID string; each entry also carries its display `name` and production `match_count` as specified in ARS-001 |

Non-route entity identifiers remain unchanged. IDs for condition/action/exclusion rows, route decisions, runs, config versions, and videos are not changed merely because they occur in route-related objects. The internal integer route key is never emitted in a route-identity field.

`POST /api/config/versions/{id}/restore` continues to identify the ConfigVersion by its existing integer config-version ID; it does not accept route IDs in its request body. The returned version's snapshot route IDs use UUIDs. ConfigVersion identifiers such as `id`, `from_id`, `to_id`, and `compare_to` remain non-route identifiers and keep their existing types. Route IDs in dry-run and backtest draft/proposed configurations are UUID strings; result payloads, including any run-video decisions, route-statistics, or route references exposed by a backtest-specific result surface, use UUID strings too. The immediate run-start response's `run_id` remains a run identifier, not a route identifier.

### CRUD and reorder behavior

- `POST /api/routes` assigns the UUID server-side. Request bodies MUST omit `id`; a caller-supplied route ID is rejected as invalid input. The response contains the assigned UUID in `id`.
- `GET /api/routes` returns every active route in ascending `evaluation_order`, including routes with the same `name`, and retains ARS-001's `match_count` contract. Each route is independently keyed by UUID.
- `GET /api/routes/{route_uuid}`, `PUT /api/routes/{route_uuid}`, and `DELETE /api/routes/{route_uuid}` resolve UUID through `route_identity` to the internal integer key. Malformed UUID returns the existing route-ID `400` error; a valid but unknown or soft-deleted UUID returns the existing `404 route not found` response.
- `PUT /api/routes/reorder` accepts `{"route_ids":["<uuid>", ...]}`. Each entry resolves to the existing internal route key, and array order remains the requested evaluation order. Existing reorder semantics are unchanged.
- Name validation continues to reject missing, blank-after-trim, and over-100-character values. Duplicate names are accepted for create and rename; name is never used to identify an update/delete/reorder target.
- Every response and every request field that semantically references a route uses UUIDs; no endpoint-specific integer fallback is permitted.

### Configuration history, decision history, and analytics

ConfigVersion storage keeps internal integer route IDs to preserve OC-003's immutable snapshots. On public read, `snapshot.routes[].id` and route identities in diff/restore representations project through the append-only registry, including IDs whose `route` row no longer exists. The same projection applies to route references in run-video decisions, run-scoped route statistics, route analytics, duplicate provenance, and dry-run/backtest request/result representations. In draft/proposed configurations, public route IDs are UUID strings; the API boundary translates them to the internal identity representation without changing the public type. Immediate run-start `run_id` values and ConfigVersion IDs remain their existing non-route identifier types.

Internal joins and grouping continue to use integer route keys. Public aggregation and UI state MUST be keyed by route UUID, not route name. Where duplicate names are displayed, distinct rows, controls, and analytics series MUST not merge because their labels match. The accepted design amendment requires a short UUID suffix with route names where route identity matters. No name-based identity or fallback is allowed.

### Compatibility and evolution

- This is a coordinated breaking change to the bundled route API: numeric route IDs become UUID strings in the same backend/SPA release.
- No integer alias or dual-reader compatibility mode is required or supported. Numeric route paths fail with `400`; old clients must be upgraded with the bundled application.
- Existing relational rows and historical ConfigVersion JSON remain unchanged. Only the public route identity projection changes.
- Assigned IDs are not rotated or rederived from names. A UUID-generation or encoding change requires a new architecture decision.

## Invariants and lifecycle

1. Every current route and every extant history reference has one registry mapping before serving.
2. Each registry internal key maps to exactly one canonical UUIDv4, and each UUIDv4 maps to at most one internal key.
3. A route's UUID does not change across rename, reorder, enable/disable, or soft delete; historical-only mapping rows are retained.
4. A display name is not unique and never determines persistent/API/history identity.
5. Persisted ConfigVersion snapshots are immutable; public UUID projection does not mutate them.
6. Missing identity mapping is an integrity error, not a reason to guess by name or expose an integer.

## Failure behavior

- Malformed/non-UUID route IDs fail request validation with `400` and the existing `invalid route id` error body.
- Syntactically valid but absent or soft-deleted route UUIDs preserve the existing route `404` behavior.
- A valid UUID absent from `route_identity` in a dry-run draft or backtest proposal is not treated as a route-CRUD `404`: it is registered with an inactive identity anchor as specified by ADR-012. Missing, malformed, non-v4, or duplicate route UUIDs within one proposal return `400 {"error":"invalid route id"}` before any ConfigVersion/run/anchor is committed.
- A backfill/create uniqueness collision aborts its transaction and never replaces another mapping.
- Incomplete backfill prevents API startup. Startup retries only missing IDs on the next attempt; established UUIDs remain unchanged.
- Proposal-ingest transaction failure returns the endpoint's existing `500` start-run error; it leaves no new mapping, anchor, proposal ConfigVersion, or run committed.
- History projection without an identity mapping fails the endpoint using its existing internal-error contract. It never silently substitutes an integer or route name.

## Verification

Required evidence:

1. **Migration preservation:** migrate a database containing active and soft-deleted routes, plus a valid ConfigVersion snapshot whose route ID has no current `route` row, along with route children, decisions, audits, executed actions, provenance, and ConfigVersions. Assert all existing integer keys/FKs and decision counts remain unchanged; every distinct valid current/history ID has one unique UUIDv4; stored snapshot JSON is byte-for-byte unchanged.
2. **Idempotent startup:** interrupt after schema migration but before backfill, restart, and verify missing mappings are populated; restart again and verify no UUID changes. Incomplete/duplicate mapping data prevents server startup.
3. **UUID immutability:** direct update/delete of an assigned registry row is rejected; route rename/order/soft-delete leaves its mapping unchanged.
4. **Name behavior:** create two routes with the same valid display name and distinct UUIDs; independently read, update, reorder, delete, and inspect their history. Blank and overlength names remain rejected.
5. **API sweep:** all public route-reference fields are UUID strings (or nullable where already specified), no numeric route key is emitted, malformed UUID returns `400`, and unknown/deleted UUID preserves `404`.
6. **History composition:** execute a run, capture a snapshot, rename/soft-delete a route, and inspect run decisions, stats, configuration history/diff/restore, and duplicate provenance. Assert all use the same UUID and stored snapshots remain unchanged.
7. **Historical-only projection:** a snapshot-only integer ID has a public UUID projection despite the missing `route` row; it is not inferred from name or omitted.
8. **Statistics/client identity:** duplicate names produce separate route rows/analytics series and UI controls keyed by UUID; order and match-count semantics are unchanged.
9. **Dry-run/backtest identity:** UUID route IDs in draft/proposed configuration requests remain UUIDs in decision, statistics, and result projections; no internal integer route key or route-name-based match is emitted. Run and ConfigVersion IDs retain their existing types. Include a UUID absent from the registry and a registry-only historical UUID; prove FK-backed decisions, stable mappings, unchanged active route set, snapshot-based route names, and rollback on transaction failure.
10. **Channel route identity:** `GET /api/analytics/channels/{channel_id}` returns one `matched_routes` object per route UUID, including distinct same-named routes, display name, and production distinct-video match count; route UUID is never replaced by name or integer. Dry-run, replay, and backtest decisions do not affect the count.
11. **Bundled composition:** exercise Route Editor → UUID route API → SQLite → run/history/API with a same-name route pair.
12. **Exclusive UUID rollout:** verify the deployed migration release uses one replica and `Recreate`; observe that every old-version Pod is terminated before the new Pod is created, no second process writes the SQLite file, and the new Pod becomes ready only after migration/backfill validation succeeds.

## Open questions

- **Resolved — proposal-only route identity:** [ADR-012](adr-012-proposal-route-identity-anchors.md) defines inactive FK identity anchors, transaction scope, unknown UUID handling, historical-only identity recovery, and snapshot-based route names. It preserves active-route isolation and the accepted integer FK target.
- **Resolved — duplicate-label presentation:** the accepted [Route Identity design amendment](../design/amendment-route-uuid-identity-duplicate-names.md) defines the always-visible short UUID suffix. UI implementation conformance remains an implementation gate, not an architecture question.
- **Deployment owner — does not block forward migration; blocks a rollback plan after UUIDs are exposed or route writes occur:** confirm whether rollback to a pre-UUID binary is required. Such rollback is unsupported without a separate compatibility/restore decision.
