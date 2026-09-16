---
type: decision
title: ADR-006 — Route List Match Count Is Derived from route_decision, Not Materialized on the Route Row
description: Expose the per-route "match count" column on the Route List by aggregating production-run route_decision rows (matched, run_type scheduled|manual) rather than adding a materialized counter to the route table, so the statistic cannot drift from the decision evidence that already defines it.
tags: [architecture, decision, yuhaul, routes, statistics, match-count, denormalization, route-decision]
---

**Status:** proposed

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [ADR-003 — Route Deletion Is a Soft Delete That Preserves Historical Decision Evidence](adr-003-route-soft-delete-preserve-history.md)
- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md)

## Satisfies

- [YuHaul Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route List — the Route row component includes a "match count" ("number of videos matched by this route — lifetime or period, depending on context").
- [BR-020 — Run-Level and Route-Level Statistics](../requirements/yuhaul/br-020-run-level-and-route-level-statistics.md) AC2 — "Each route has accumulated statistics: videos matched …". (BR-020 status: proposed.)
- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md) AC9 — the user can inspect the current route list at any time (the view this column enriches).

## Problem

The Route List view requires a "match count" per route, but no such surface exists:

- `router.Route` (`internal/router/models.go:64-78`) carries configuration only — name, conditions, actions, exclusions, ordering, flags — no match-count field.
- The `route` table (`internal/store/migrations/001_initial_schema.up.sql:90-100`) has no match-count column.
- `GET /api/routes` (`internal/server/server.go:652-660`) returns `router.Store.ListRoutes` (`internal/router/store.go:103-131`) verbatim — config rows with children, no statistics.
- The current view (`web/src/views/RoutesView.tsx:79-81`) renders a "Conditions" count and Yes/No flags, not the spec'd match count. Reviewer MAJOR-6 (`docs/ephemeral-reports/reviewer/yuhaul-route-crud/reverify-br-003.md`) flags the missing API surface.

The decision is: **where does the match count come from** — a materialized counter on the route row maintained at evaluation time, or an aggregation over the existing `route_decision` evidence — and what exactly "lifetime" counts.

## Decision drivers

- **Single source of truth.** `route_decision` already records every evaluation outcome (`decision IN ('matched','not_matched','deferred','skipped')`, `internal/store/migrations/001_initial_schema.up.sql:132-142`) and is preserved by ADR-003's soft-delete. Run-level counters are already *derived* from it (`state.Store.RecomputeRunCountersFromDecisions`, `internal/state/store.go:652-689`), and run-scoped per-route stats are already aggregated from it (`stats.ListRouteStats`, `internal/stats/store.go:131-166`). A match count is a *derived statistic* in this codebase, not a config attribute.
- **Production-only semantics.** `run.run_type` spans `scheduled | manual | dry_run | replay | backtest` (`001_initial_schema.up.sql:18`). Dry-run, backtest, and replay all record `route_decision` rows, but are explicitly non-production: dry-run "records route decisions … without mutating … production statistics" (`internal/pipeline/dryrun.go:59-62`), and replay/backtest are "labeled distinctly from production outcomes". "Lifetime matches" must therefore exclude non-production runs, or the column silently over-counts.
- **No drift.** A counter and its underlying evidence must never disagree. A write-time increment that must be suppressed for three of five run types is exactly the surface that produces silent wrong answers.
- **Admin-read scale.** The route list is a low-frequency, single-user view; an aggregate query cost is immaterial.
- **Config integrity.** Route rows and config snapshots are configuration, not statistics. Materializing a volatile statistic onto the config entity entangles two unrelated lifecycles.

## Options considered

### Option A — Materialize a `match_count` column on `route`, incremented during evaluation

Add `route.match_count INTEGER NOT NULL DEFAULT 0`; increment it in the evaluation write path whenever a route `matched`.

- **Benefits:** a scalar read with no join at list time.
- **Costs:** requires a migration; couples the evaluation hot path to a route-row write; must be transactional with the `route_decision` insert.
- **Failure modes:** the counter can drift from `route_decision` if any future path records a decision without incrementing, or increments without recording. The increment must be suppressed for `dry_run`/`backtest`/`replay` (three of five run types) — the same production-only filter, but now distributed across the *write* path rather than centralized in one read query. Any mistake is a silent wrong number with no reconciliation until someone audits against `route_decision`. This is the denormalization pattern the codebase has deliberately avoided: decision facts live once in `route_decision`, and every counter (`run.videos_*`, `stats.RouteStats`) is derived from it.

### Option B — Derive the count from `route_decision` at read time (chosen)

Aggregate `route_decision` filtered to production runs when building the list response.

- **Benefits:** `route_decision` is already the durable source of truth; no migration; `idx_route_decision_route_id` (`001_initial_schema.up.sql:267`) already serves the lookup; the evaluation engine is untouched; the production-only filter lives in exactly one query; matches the existing `stats.ListRouteStats` pattern.
- **Costs:** the list endpoint gains one aggregate query (immaterial at admin-read scale); the semantics ("lifetime = production runs only") must be pinned explicitly.
- **Failure modes:** none new — it inherits the pre-existing durability of `route_decision` (which any derived statistic already depends on).

### Option C — Defer match count to v2

- **Costs:** contradicts the *accepted* design spec's Route row component; dropping a spec'd component is Specifier/Designer authority, not Architect's. Rejected on authority grounds before technical merit.

## Decision

In the context of an accepted Route List design that requires a per-route match count, facing the choice between a materialized counter on the route row, a read-time aggregation over `route_decision`, or deferral, we decided **for deriving the count from `route_decision`** and **against** materializing a counter and **against** deferral, to achieve a statistic that cannot drift from the evidence that defines it, accepting one aggregate query on a low-frequency admin read.

"Lifetime" is pinned as **all `matched` route decisions recorded under production runs** — `run.run_type IN ('scheduled','manual')` — across all history. Dry-run, backtest, and replay decisions are excluded because they are non-production ("would have" / labeled non-production). A period-filtered variant is already the responsibility of the Analytics surface (BR-020 AC4, served by `stats.ListRouteStats` + temporal stats); the Route List has no period context, so lifetime is the only well-defined default and needs no user decision.

## Realization

- **New aggregate query** (single statement, no migration):
  ```sql
  SELECT rd.route_id, COUNT(*)
  FROM route_decision rd
  JOIN run ru ON ru.id = rd.run_id
  WHERE rd.decision = 'matched' AND ru.run_type IN ('scheduled','manual')
  GROUP BY rd.route_id;
  ```
  Routes with zero production matches simply have no row and default to 0. The `JOIN run` is the load-bearing production-only filter; omitting it over-counts.
- **Field placement.** Add a read-only display field `MatchCount int64 \`json:"match_count"\`` to `router.Route` (`internal/router/models.go`), populated only by the list path. It is never written by create/update, and it cannot leak into config history because `config_version.snapshot` serializes `config.SnapshotRoute` (`internal/config/models.go:21`), not `router.Route`.
- **Merge point.** `GET /api/routes` (`internal/server/server.go:652-660`) obtains the map and merges `match_count` into each route before responding. `GET /api/routes/:id` (editor) may leave it zero — the editor does not display it. Alternatively, a `router.Store.MatchCounts(ctx)` helper returns `map[int64]int64`; the merge stays in the server handler so the config entity remains pure.
- **Web.** `web/src/types.ts` `Route` gains `match_count: number`; `RoutesView.tsx` adds a "Matches" column rendering `r.match_count` (replacing the current placeholder conditions-count-only row per spec §Route List).

## Consequences

- **Positive:** no schema migration; no write-path change to the evaluation engine; the count is always consistent with `route_decision` by construction; the production-only semantics are enforced in one place; matches the codebase's established derive-from-decision pattern.
- **Negative:** the route list read is now two queries (routes + one aggregate) instead of one; the list response carries a statistic alongside config, which the DTO-vs-entity boundary must keep out of snapshots (already safe via `SnapshotRoute`).
- **Neutral / inherited:** the count's durability is exactly `route_decision`'s durability. A future hard-delete of `video`/`run` (which `route_decision` cascades from, `001_initial_schema.up.sql:134-135`) would reduce historical counts — but that equally affects `run.videos_*` counters and `stats.ListRouteStats`, so this decision introduces no new exposure. No such hard-delete path exists today; routes are soft-deleted per ADR-003.

## Confirmation

- Unit: seed `route_decision` rows across run types — matched under `scheduled`/`manual`, matched under `dry_run`/`backtest`/`replay`, and `not_matched` under `manual` — and assert `GET /api/routes` reports `match_count` equal to the production-matched count only; a route with zero production matches reports 0.
- Integration: run a manual run that matches a route N times, then a dry-run that would match it M more; assert the list count advances by N and not N+M.
- Regression: confirm `config_version.snapshot.routes` still serializes no `match_count` field (snapshot uses `SnapshotRoute`).

## Open questions

- None.
