---
type: decision
title: ADR-007 — Canonical Route Action Type at the API Boundary
description: Use the accepted design's add_to_playlist action identifier in public API payloads while retaining playlist_insert as the existing internal persistence and execution key.
tags: [architecture, decision, yuhaul, routes, playlist, api, compatibility]
---

**Status:** accepted

## Derived from

- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md)
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md)
- [YuHaul System Architecture](yuhaul-system-architecture.md)

## Satisfies

- [Anchor](../anchors/youtube-discovery-routing.md) AC4 — routes contain configurable conditions and playlist actions.
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route Editor — Action configuration UI — v1 action type is `add_to_playlist`.
- [BR-004 — Route Conditions and Playlist Actions](../requirements/yuhaul/br-004-route-conditions-and-playlist-actions.md) AC4 — proposed requirement, consistent with the accepted design's playlist action.

## Decision question

Which `action_type` value is the public route API contract when the accepted UI design calls the action `add_to_playlist` but the current router, database constraints, execution records, and snapshots use `playlist_insert`?

## Drivers and constraints

- The accepted design spec is authoritative for the human-facing route action name: `add_to_playlist`.
- The current internal representation is persisted across route actions, executed-action idempotency keys, and configuration snapshots as `playlist_insert`.
- Configuration snapshots are durable historical records and must not be rewritten merely to normalize a name.
- The tool is documented as a single-user product with its bundled SPA as the API consumer; no separately versioned third-party API compatibility promise is documented.
- The external YouTube method remains `playlistItems.insert`; it is not the application's route action identifier.

## Options considered

### Option A — Canonical public name with a translation at the API boundary (chosen)

Public requests and responses use `add_to_playlist`. The server maps that value to the existing internal `playlist_insert` representation before persistence and maps stored values back when serializing public responses.

- **Benefits:** follows the accepted design; no route/action/snapshot migration; preserves historical records and existing idempotency identity; keeps provider method naming separate from product action naming.
- **Costs:** the API adapter must normalize writes and every response that exposes an action type, including route CRUD, run decision detail, and configuration-history route snapshots/diffs.
- **Failure modes:** a missed serializer leaks `playlist_insert`; a missed decoder could reject the design's canonical value. Both are bounded to the API boundary and testable.

### Option B — Rename the internal and persisted value to `add_to_playlist`

Change database constraints and code to use the public design name throughout.

- **Benefits:** one spelling in new internal records.
- **Costs:** requires schema/data migration for `route_action` and `executed_action`; changing the executed-action key can affect idempotency lookups; immutable configuration snapshots already contain the old identifier and still require a compatibility reader or rewriting historical records. This adds broader storage and history work for a single API naming seam.
- **Failure modes:** incomplete migration or idempotency normalization can cause old actions to be missed or repeated. Rejected as more complex than a boundary mapping.

### Option C — Change the accepted design/API to `playlist_insert`

Expose the internal identifier directly and amend the UI design spec.

- **Benefits:** no API translation.
- **Costs:** reverses accepted design authority to match an implementation detail. Rejected; only the design owner may change that accepted vocabulary.

## Comparison

Option A preserves the accepted public vocabulary and existing durable representations with one explicit adapter. Option B removes a translation but widens changes into schemas, evidence, snapshots, and idempotency. Option C is not an architecture-owned alternative because it would change accepted human-facing meaning.

## Decision

The public API action type is **`add_to_playlist`**. The request decoder accepts that value and maps it to internal **`playlist_insert`**. Route CRUD responses and public action-bearing inspection/configuration-history responses emit **`add_to_playlist`**, regardless of the stored legacy representation.

The public API does not accept `playlist_insert` as an input alias. Unknown, empty, or unsupported `action_type` values are rejected as `400 Bad Request`; an empty `target_playlist_id` is also rejected as `400`. The bundled SPA and API are deployed together, and no external compatibility contract has been established. Existing database rows and configuration snapshots are not rewritten.

## Realization

**Implemented:** `internal/router/action.go` owns the public-to-internal and internal-to-public mapping. Route request decoding in `internal/server/server.go` stores the internal value; route serializers, run decision details, and configuration-history serializers emit the public value. Dry-run draft and backtest proposal inputs are normalized before their snapshots are persisted or evaluated. Existing stored route rows, executed-action keys, and historical snapshots are not rewritten.

**Confirmed on 2026-10-24:** `go test ./internal/server ./internal/router ./internal/config` passed. The tests cover canonical route round trips and rejection of internal/unknown input, run decision detail, configuration history, and dry-run/backtest proposal normalization. This is implementation/test evidence, not a claim of an external-client compatibility guarantee.

## Consequences

- **Positive:** wire/API payloads match the accepted design; no migration alters durable action or snapshot data; the existing idempotency identity stays stable.
- **Negative:** every public serializer that exposes an action identifier must normalize the internal value. Contract tests must cover route, run-inspection, and configuration-history responses.
- **Neutral:** the database and internal execution path continue to use the legacy storage key; this is an internal representation, not a second public action type.
- **Revisit trigger:** a separately versioned public API, a second independently deployed client, or a planned storage-vocabulary migration would justify reconsidering the boundary.

## Confirmation and reconsideration

- **Automated confirmation passed on 2026-10-24:** see the tests and coverage summary in [Realization](#realization); the public `add_to_playlist` / internal `playlist_insert` boundary is exercised across the documented surfaces.
- API tests: `POST`/`PUT /api/routes` accept `add_to_playlist`, reject `playlist_insert` and unknown values with 400, and return canonical action types.
- Round-trip test: a route written through the public API persists internal `playlist_insert`, then `GET /api/routes` returns `add_to_playlist`.
- Proposal-input test: `POST /api/runs/dry-run` and `POST /api/backtest` accept `add_to_playlist` in draft/proposed configurations, persist and evaluate the internal representation, and reject the internal spelling and unknown values with `400`.
- Historical test: old configuration snapshots and executed-action rows remain unchanged in storage but public snapshot/detail responses emit `add_to_playlist`.
- Idempotency test: a retry for a route action stored as `playlist_insert` continues to match the existing executed-action key and does not duplicate playlist insertion.
- Composition test: the bundled Route Editor can create and reload a route through the real route API with `add_to_playlist` and a selected playlist.

## Open questions

- None for the API naming decision. User-visible list-fetch failure presentation is separately routed in ARS-001 to the Specifier.
