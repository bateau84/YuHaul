---
type: decision
title: ADR-003 — Route Deletion Is a Soft Delete That Preserves Historical Decision Evidence
description: Replace the hard-delete of routes with a soft-delete (deleted_at timestamp) so route_decision, condition_audit, and executed_action records — and the historical statistics derived from them — survive route deletion, satisfying BR-003 AC4 and Anchor AC15/25/28.
tags: [architecture, decision, yuhaul, routes, soft-delete, decision-evidence, history]
---

**Status:** proposed

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md)

## Satisfies

- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md) AC4 — "historical run data referencing the deleted route is preserved for interpretability."
- [Anchor](../anchors/youtube-discovery-routing.md) AC15 — every meaningful route decision explainable from durable evidence.
- [Anchor](../anchors/youtube-discovery-routing.md) AC25 — route/configuration history sufficient to interpret historical statistics.
- [Anchor](../anchors/youtube-discovery-routing.md) AC28 — user can inspect historical runs and understand why videos were accepted/rejected/deferred/failed.

## Problem

`router.Store.DeleteRoute` (`internal/router/store.go:206-217`) executes `DELETE FROM route WHERE id = ?;` — a hard delete. With foreign keys enforced (`internal/store/store.go:56` `_foreign_keys=1`) and the schema cascading (`internal/store/migrations/001_initial_schema.up.sql`), the deletion ripples outward:

- `route_decision.route_id ... ON DELETE CASCADE` (`:136`) → every per-video per-route decision for that route is erased.
- `condition_audit.route_decision_id ... ON DELETE CASCADE` (`:147`) → the per-condition evidence trail is erased.
- `executed_action.route_decision_id ... ON DELETE CASCADE` (`:160`) → executed-action history is erased.

Two further consequences follow from erasing `route_decision`:

1. `state.Store.RecomputeRunCountersFromDecisions` (`internal/state/store.go:652-689`) recomputes run counters from `route_decision`, so deleting a route retroactively rewrites the historical accepted/rejected/excluded counts of every run that evaluated it — violating AC25 (statistics interpretable in configuration context) and AC28.
2. The Decision Detail view loses the route's *name* (the `route_decision` → `route.name` join breaks), so the operator can no longer see *which route* produced a historical decision — violating AC15.

This is the CRITICAL-1 finding: the only implemented delete path destroys the durable evidence BR-003 AC4 mandates.

## Decision drivers

- **Historical evidence must be durable.** Deleting a route must remove it from the *evaluation sequence* (BR-003 AC4 first clause) without erasing *historical run data* (BR-003 AC4 second clause). The two requirements are distinct; a hard delete collapses them.
- **Interpretability requires a route identity.** The Decision Detail evidence trail must still resolve "Route 'Music Discovery' condition ... NOT MATCHED" after deletion. The evidence tables therefore need a retained, resolvable route reference.
- **Historical statistics must not drift.** Run counters already materialized must not change because a route was later deleted.
- **Single-user SQLite, minimal migration.** The fix should be a column add + query filters, not a table rebuild.

## Options considered

### Option A — Soft-delete via `deleted_at` (chosen)

Add a nullable `deleted_at TIMESTAMP` column to `route`. `DeleteRoute` becomes `UPDATE route SET deleted_at = now WHERE id = ? AND deleted_at IS NULL`. Every management and evaluation query filters `deleted_at IS NULL`. The `route` row — and therefore its `name` — is retained, so `route_decision.route_id` stays a valid `NOT NULL` FK and the evidence tables are never touched.

- **Benefits:** evidence, statistics, and route-name resolution all survive untouched; `route_decision.route_id` stays `NOT NULL` (no nullable-FK migration); a one-column `ALTER TABLE` migration; the config snapshot (`onChange` → `ConfigVersion`) already records the pre-delete configuration for AC11/AC25.
- **Costs:** soft-deleted rows accumulate (negligible for a single user); every future route query must remember to filter `deleted_at IS NULL`; a deleted route must be moved out of the positive `evaluation_order` space at delete time so create/reorder never collide with it (see Realization).
- **Failure modes:** a future code path that physically deletes a route would re-trigger the cascade — but no such path exists, and the FK remains a `NOT NULL` reference that makes physical deletion of a route with decisions semantically wrong.

### Option B — Hard-delete + `route_decision.route_id` `ON DELETE SET NULL` + route-name snapshot

Physically delete the route; relax the FK to `SET NULL`; add a denormalized `route_name` (or identity) snapshot column to `route_decision` (and propagate to the audit/action display).

- **Benefits:** no retained route rows; the deleted route's position in `evaluation_order` is freed.
- **Costs:** `route_id` becomes nullable, requiring schema changes and nullable-scan handling across every decision query; the identity snapshot must be backfilled for pre-existing rows and maintained on every future write; the `SET NULL` FK still loses the direct join, so interpretability now depends on a manually-maintained snapshot rather than the FK — more surface to drift. Rejected as heavier than the finding requires.

### Option C — Hard-delete + `ON DELETE NO ACTION`/`RESTRICT` (fail loudly)

Change the FK so a hard delete of a route with decisions errors out.

- **Costs:** SQLite requires a full table rebuild to alter an FK clause; and it does *not* satisfy BR-003 AC4's first clause — the user could no longer delete a route at all once it has any decision history, which contradicts "deleting a route removes it from the evaluation sequence." Rejected.

## Decision

In the context of a pre-merge CRITICAL where route deletion cascades away historical decision evidence, facing the choice between soft-delete, a nullable-FK + name-snapshot hard-delete, or a restrictive FK, we decided **for soft-delete via a `deleted_at` timestamp** and **against** hard-delete variants, to achieve durable evidence with a retained route identity and stable historical statistics, accepting a one-column migration and a standing "filter `deleted_at`" discipline.

The `route_decision.route_id` FK is deliberately left as `NOT NULL ... ON DELETE CASCADE`. Under this decision no code path physically deletes a route row, so the cascade is dormant; the retention guarantee comes from *not deleting*, not from relaxing the FK. Changing the FK to `SET NULL` would be the wrong direction (it would discard the very join this decision preserves); changing it to `RESTRICT` is a table-rebuild not required by the fix and would block legitimate deletion semantics.

## Realization

The order space is partitioned by sign: **active routes occupy positive `evaluation_order` values; soft-deleted routes occupy negative sentinel values.** This is what lets create and reorder proceed without contending with a deleted route's old position.

- `internal/store/migrations/010_route_soft_delete.up.sql` (next free version — `002`–`009` already exist)
  - `ALTER TABLE route ADD COLUMN deleted_at TIMESTAMP;` (a matching `010_route_soft_delete.down.sql` may drop the column for symmetry).
- `internal/router/models.go`
  - Add `DeletedAt string \`json:"deleted_at,omitempty"\`` to `Route` (internal bookkeeping; deleted routes are filtered from CRUD responses, so it never surfaces in `list`/`get`).
- `internal/router/store.go`
  - `DeleteRoute`: replace the `DELETE FROM route` with `UPDATE route SET deleted_at = ?, evaluation_order = -id WHERE id = ? AND deleted_at IS NULL`. The `evaluation_order = -id` sentinel moves the route out of the positive order space *at delete time*, freeing its position; decision-time order is already snapshotted per decision in `route_decision.evaluation_order` (`migrations/001:137`), so the sentinel carries no evidence. Return `ErrRouteNotFound` on zero rows affected; capture the route name in the snapshot description (`snapshot("deleted route: " + name)`), fixing the name-less `"deleted route"` attribution (MINOR-5).
  - `GetRoute`, `ListRoutes`, `LoadActiveRoutes`: add `AND deleted_at IS NULL` (or `WHERE deleted_at IS NULL`) to the `SELECT`s. `LoadActiveRoutes` continues to include *disabled* routes (for skip decisions) but excludes *deleted* routes, because a deleted route is removed from the evaluation sequence (BR-003 AC4), not skipped.
  - `UpdateRoute`: add `AND deleted_at IS NULL` to the `UPDATE ... WHERE id = ?` so a deleted route cannot be resurrected/edited.
  - `ReorderRoutes`: deleted rows already sit at negative `-id` sentinels, so the old `-evaluation_order - 1` negative-shift (which can collide with a `-id` sentinel) is replaced by a shift that lands *below* the sentinel band: `UPDATE route SET evaluation_order = -evaluation_order - 1 - ? WHERE deleted_at IS NULL`, where `?` is `MAX(id)` over **all** routes (computed once at the start of the transaction). Then assign each active route `i+1` in slice order. The temp values are distinct, negative, and strictly below every `-id` sentinel, so neither the shift nor the reassignment collides.
  - `CreateRoute`'s callers: compute `evaluation_order = max(active evaluation_order) + 1` over non-deleted routes (`web/src/views/RouteEditorView.tsx` already does `max(...) + 1` over `listRoutes()`, which filters deleted). Because deleted routes sit at negative sentinels, `max(active) + 1` is always a free positive position — no reserved-position collision.
  - Add `ListRouteNamesIncludingDeleted(ctx)` — an unfiltered `SELECT id, name FROM route` with no `deleted_at` predicate — so historical name resolution still reaches soft-deleted routes.
- `internal/server/run_handlers.go`
  - `routeNameMap` (currently built from `routeStore.ListRoutes`, which now filters `deleted_at IS NULL`) must switch to `ListRouteNamesIncludingDeleted`. `routeNameMap` feeds both `handleListRunVideos` (via `toVideoListItem`) and `handleGetRunVideo` (via `buildVideoDetail` → `toRouteDecisionDetail`); without this switch a soft-deleted route's name vanishes from the run video list and Decision Detail — the exact AC15 regression this decision protects against.
  - `ListRoutes` remains filtered for management (route list/editor), and the server's `RouteStore` interface (`internal/server/server.go`) gains the new method.
- Historical evidence resolution must **not** filter `deleted_at` — the retained soft-deleted row is what preserves the route name in the evidence trail, realized through the unfiltered name map above (not a SQL join on `route_decision`).
- No change to `route_decision`, `condition_audit`, or `executed_action` — they are untouched by a soft delete, and `RecomputeRunCountersFromDecisions` continues to see their rows.

## Consequences

- **Positive:** deleting a route leaves `route_decision`/`condition_audit`/`executed_action` intact; historical run counters do not change retroactively; the Decision Detail still resolves the route name; BR-003 AC4 and Anchor AC15/25/28 are satisfied.
- **Negative:** soft-deleted rows accumulate (single-user, immaterial); a deleted route's `evaluation_order` is rewritten to the `-id` sentinel, so its original position value is no longer a meaningful ordinal — but decision-time order remains snapshotted in `route_decision.evaluation_order`, so no evidence is lost. The design spec's "No re-indexing occurs — the gap in positions is transparent to the user" (§Route Editor — Delete behavior) constrains only the *remaining* routes' relative order, which this sentinel scheme preserves (remaining routes keep their positions; no re-indexing). It does **not** mandate that the deleted row reserve its position.
- **Neutral:** the `ON DELETE CASCADE` FK remains in the schema but is never exercised; a future "purge" feature that wants to physically remove long-deleted routes would need its own explicit migration and would knowingly accept evidence loss.

## Confirmation

- Persistence test: create a route, run an evaluation (produce a `route_decision` + `condition_audit` + `executed_action`), delete the route, then assert the decision/audit/action rows still exist, `LoadActiveRoutes` no longer returns the route, `GetRoute` returns `ErrRouteNotFound`, and the run's counters are unchanged.
- Collision regression: delete the highest-order route, then create a new route — succeeds (no `evaluation_order` UNIQUE violation); delete a middle route, then reorder the remaining routes — succeeds (no reassignment collision with the `-id` sentinel). These are the two flows the earlier reservation scheme broke.
- Product Acceptance: after deleting a route, the Decision Detail for a video historically matched by that route still shows the route name and its condition trail (exercises `ListRouteNamesIncludingDeleted` through `routeNameMap`).

## Open questions

- None.
