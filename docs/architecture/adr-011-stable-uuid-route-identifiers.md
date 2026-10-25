---
type: decision
title: ADR-011 — Stable UUID Route Identity Independent of Display Name
description: Add a stable UUID mapping for each current or historically referenced route identity while retaining integer database keys and foreign keys so immutable snapshots and route history survive migration and duplicate display names do not merge identity.
tags: [architecture, decision, yuhaul, routes, uuid, identity, migration]
timestamp: 2026-10-24T00:00:00Z
---

**Status:** accepted

## Derived from

- Explicit user direction: use a unique UUID for route ID so display names may duplicate.
- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md), particularly AC4, AC8, AC10, and AC11.
- [OC-003 — Configuration Snapshot for Historical Interpretation](../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md).
- [BR-010 — Duplicate Provenance for Discovery Credit](../requirements/yuhaul/br-010-duplicate-provenance-for-discovery-credit.md).
- [BR-015 — Explainable Route Decisions](../requirements/yuhaul/br-015-explainable-route-decisions.md).
- [BR-025 — Configuration History for Statistics Interpretation](../requirements/yuhaul/br-025-configuration-history-for-statistics-interpretation.md).
- [ADR-003 — Route Deletion Preserves Historical Decision Evidence](adr-003-route-soft-delete-preserve-history.md).
- [YuHaul System Architecture](yuhaul-system-architecture.md), current route/history data model.

## Satisfies

- The user's accepted route identity direction: route IDs are stable UUIDs and display names may duplicate.
- BR-003 AC2/AC4/AC8/AC11 and OC-003's immutable historical snapshot obligation by preserving the existing route rows, integer foreign keys, decisions, and stored snapshots while adding a public identity mapping.
- BR-010/BR-015/BR-025 by keeping provenance, durable route decision evidence, and history/snapshot references resolvable through a UUID registry that also covers snapshot-only legacy IDs.
- BR-003 AC10/AC12/AC13: names are non-empty display labels that may repeat; stable route identity is independent of a name and distinguishes same-named routes in history.

The accepted [Route Identity design amendment](../design/amendment-route-uuid-identity-duplicate-names.md) resolves the former unique-name and duplicate-label presentation conflict. The amendment defines the always-visible short UUID suffix; this ADR does not claim that the UI implementation already conforms.

## Decision question

How should route identity change from integer public IDs to stable UUID public IDs while preserving existing route rows, route decision history, and immutable configuration snapshots, with display names allowed to repeat?

## Drivers and constraints

- Current `route.id` is `INTEGER PRIMARY KEY AUTOINCREMENT`; `route_condition`, `route_action`, `route_exclusion`, `route_decision`, and `duplicate_match.source_route_id` use integer route references.
- The route HTTP model currently serializes integer `id`; route paths and reorder requests parse integer IDs. Run decision and statistics APIs also expose integer `route_id` values.
- Route soft deletion retains the route row so decision evidence and history can still resolve it (ADR-003).
- ConfigVersion snapshots persist route integer IDs and OC-003 requires captured snapshots to remain immutable.
- The user explicitly permits duplicate display names; the name remains a required display field under BR-003 and the accepted UI design.

## Options considered

### A. Retain integer keys; add an append-only route identity registry (chosen)

Keep `route.id` and all integer foreign keys unchanged. Add `route_identity(route_id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE)` with no foreign key to `route`. Generate UUIDv4 for new route IDs and backfill every current route plus every route ID found in existing history/config snapshots. The registry may therefore retain identity for a historical route ID whose route row was physically removed before ADR-003 was implemented. Translate at API boundaries between UUIDs and internal integers.

- **Benefits:** No route-table primary-key replacement or history-table rebuild; existing decisions and statistics keep their exact integer references; soft-deleted and historical-only route IDs retain UUIDs; immutable ConfigVersion JSON remains byte-for-byte unchanged.
- **Costs:** Adds one small identity table and requires mapping joins at API boundaries; migration must inspect existing ConfigVersion JSON as well as current route rows to include historical-only IDs.
- **Failure modes:** A missing mapping could otherwise expose or confuse an internal integer. Startup backfill must include every persisted route reference, and all serving paths must fail closed on a missing or duplicate mapping; no API may fall back to integer IDs or infer identity from name.
- **Compatibility:** The public route API changes from numeric IDs to UUID strings in one coordinated backend/SPA release. No documented third-party versioned API or numeric compatibility window exists.
- **Obligation impact:** Retains current FK-backed route decision evidence and maps historical-only IDs embedded in immutable snapshots without changing those snapshots; public identity is independent of route name.

### B. Add a UUID column directly to `route`

Add and backfill `route.public_id`, retaining the integer primary key and all foreign keys.

- **Benefits:** Avoids an identity lookup table for current route rows and keeps the UUID beside the route record.
- **Costs:** SQLite's additive column cannot express the final not-null invariant without extra trigger logic; more importantly, a route-table column cannot map a historical route ID that survives only inside an immutable ConfigVersion snapshot after an old hard delete.
- **Failure modes:** Public history would either fail for those snapshots or require a second fallback mapping mechanism, making the column-only option incomplete. Rebuilding the route table to add constraints still does not solve the historical-only ID case.
- **Obligation impact:** Preserves current route rows but cannot fully preserve public UUID identity for historical snapshots; rejected as incomplete for OC-003.

### C. Replace the integer route primary key and all references with UUIDs

Rebuild `route` and every table containing a route foreign key to use UUID text keys, then rewrite snapshots and all route ID representations.

- **Benefits:** One identity representation across storage and API; no boundary translation from public UUID to an integer key.
- **Costs:** Requires rebuilding multiple SQLite tables with foreign keys and uniqueness constraints, rewriting every stored decision/reference, and transforming immutable snapshots. The migration has many more partial-failure and corruption opportunities.
- **Failure modes:** A missed reference or failed backfill can orphan or misattribute historical decisions; modifying ConfigVersion snapshots violates OC-003 immutability; SQLite table-rebuild ordering is coupled to the full history schema.
- **Compatibility:** A wider storage and API break with no added behavior required by the user request.
- **Obligation impact:** Can preserve history only with a large, carefully ordered rewrite; rejected as materially riskier than the append-only identity registry.

### D. Keep integer public IDs and only remove route-name uniqueness

Allow duplicate names while continuing to expose integer IDs.

- **Benefits:** No schema or API migration and no key translation.
- **Costs:** Does not realize the user's explicit UUID identity direction; public IDs remain sequence-derived implementation keys.
- **Failure modes:** If a future storage migration changes integer keys, historical public identity would not be independent of that migration.
- **Obligation impact:** Rejected because it does not meet the explicit user decision.

## Comparison

Option A adds one append-only mapping table and resolves both current routes and historical-only IDs without rewriting the history graph or ConfigVersion bytes. Option B is superficially smaller for current routes but fails when an old immutable snapshot references a route row that was hard-deleted before ADR-003; adding a fallback would recreate Option A with more machinery. Option C introduces a risky multi-table history rewrite, and Option D fails the explicit UUID requirement. Therefore Option A is the least complex complete option that preserves all recoverable historical route identity.

## Decision

Use an immutable, lowercase, canonical UUIDv4 as each route identity, stored in an append-only `route_identity` registry keyed by the existing integer route ID. Retain `route.id` as the internal database key and leave every integer foreign key and every stored ConfigVersion snapshot unchanged. The registry's `route_id` intentionally has no FK to `route`: it must retain identities for historical IDs that exist only inside old snapshots after a pre-ADR-003 hard delete. Generate IDs server-side; they are identifiers, not secrets or authorization tokens.

The versioned migration creates `route_identity`; a startup backfill transaction assigns UUIDv4 values to the union of IDs in current route rows (active and soft-deleted), surviving route foreign-key references, and valid route entries in stored ConfigVersion snapshots. It does not alter any source row or snapshot. Never regenerate an assigned UUID. New route creation inserts the route and its identity registry row in the same transaction. The registry's primary/unique constraints prevent duplicate internal/public identities; database triggers make registry rows append-only and public IDs immutable. Before serving, validate that every extant route/reference has a mapping.

At every HTTP boundary, the route entity's `id` and every field that semantically references a route (`route_id`, `source_route_id`, and `route_ids`) is a UUID string, never the internal integer. The full contract, including historical snapshot translation and rollout requirements, is in [ARS-004](ars-004-route-uuid-schema-api-migration.md). Proposal-only UUIDs that need an integer FK target use inactive identity anchors as specified by [ADR-012](adr-012-proposal-route-identity-anchors.md).

Route names remain non-empty required display values (and retain the accepted 100-character limit); they are not identifiers and may duplicate. Name is never used to resolve, join, order, or group route identity. APIs and UI state key routes by UUID; route statistics remain distinct by UUID even when display labels match.

## Consequences

- **Positive:** Existing route decisions, route-specific child rows, source-route provenance, and soft-delete history retain their integer FKs. Current and historical-only route IDs receive durable public UUIDs, and same-name routes remain independently addressable.
- **Negative:** Every API serializer/deserializer and front-end route identity type must switch to UUID strings. Historical snapshots need an API-only integer-to-UUID projection through the retained route registry.
- **Neutral:** Existing ConfigVersion JSON and internal evaluation order are unchanged. Route names remain required and bounded; only uniqueness ceases to be a validation rule.
- **Compatibility:** Numeric route IDs are not accepted as public aliases after the coordinated API/SPA change. The bundled application has no established third-party API compatibility guarantee.
- **Requirements/design reconciliation:** Accepted BR-003 AC10/AC12/AC13 permit duplicate display names and require stable identity independent of name. The accepted Route Identity design amendment supplies the presentation treatment; implementation conformance remains an implementation gate.

## Confirmation and reconsideration

Migration and integration evidence must prove:

1. migration of a database containing active, soft-deleted, and snapshot-only historical route IDs assigns one unique UUIDv4 to each distinct referenced integer identity, preserves every existing integer primary key/FK and historical decision row, and leaves stored ConfigVersion snapshot JSON unchanged;
2. rerunning startup migration/backfill neither replaces existing UUIDs nor changes history, and failure before completion prevents the API from serving route data;
3. public route CRUD, reorder, run-decision, statistics, provenance, configuration history/diff/restore, dry-run, and backtest surfaces use UUID strings consistently and never leak integer route keys;
4. identical route names can be created and independently updated/deleted/reordered/inspected by UUID, while blank names remain rejected and evaluation ordering/count semantics are unchanged;
5. renaming, reordering, and soft deletion never change a UUID; historical run decisions and config snapshots still resolve to the same route UUID, including a fixture whose snapshot references a route row absent from `route`.

Reconsider replacing the internal key only if a future requirement removes SQLite or changes the persistence boundary enough that FK translation is no longer useful. Reconsider the public API compatibility choice only if a separately versioned external client or rolling old/new server topology is introduced.

## Open questions

- None for the registry and duplicate-name identity decision. Proposal-only route identities and their retained FK realization are specified by ADR-012; proposal-to-active promotion is outside that decision and would require a separate contract.
