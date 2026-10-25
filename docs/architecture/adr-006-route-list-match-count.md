---
type: decision
title: ADR-006 — Route List Match Count Is a Production-Evidence Projection
description: Expose the Route List match count as a required list-only projection counting distinct matched videos from production-run route_decision evidence; do not materialize a counter or retain a redundant per-route stats endpoint.
tags: [architecture, decision, yuhaul, routes, statistics, match-count, denormalization, route-decision]
---

**Status:** accepted

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [ADR-003 — Route Deletion Is a Soft Delete That Preserves Historical Decision Evidence](adr-003-route-soft-delete-preserve-history.md)
- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md)

## Satisfies

- [YuHaul Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route List — the Route row component includes a "match count" ("number of videos matched by this route — lifetime or period, depending on context").
- [BR-020 — Run-Level and Route-Level Statistics](../requirements/yuhaul/br-020-run-level-and-route-level-statistics.md) AC2 and clarified AC7 — the Route List count is distinct videos matched by a route within its displayed time scope; one video contributes at most once per route in that scope.
- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md) AC9 — the user can inspect the current route list at any time (the view this column enriches).

## Problem

The accepted design's Route List row includes a match count, and the clarified BR-020 contract defines its unit: distinct videos matched by a route in the displayed time scope. The Route List has no time-period selector, so this is a lifetime count. The list-time aggregate over production `route_decision` evidence remains the simplest source of truth. The implementation now uses `ListRouteMatchCounts` to merge distinct-video counts into `GET /api/routes`; it does not register a per-route `GET /api/routes/{route_uuid}/stats` handler. The aggregate store method remains because it supplies the canonical route-list projection, not a second API surface.

## Decision drivers

- **Single source of truth.** `route_decision` records every evaluation outcome and is preserved by ADR-003's soft-delete. A match count is a derived statistic, not a route configuration attribute.
- **Distinct-video semantics.** The counted entity is the video, not each evaluation or matched decision. If one video matches the same route in multiple production runs, it contributes one to this lifetime count.
- **Production-only semantics.** `run.run_type` spans `scheduled | manual | dry_run | replay | backtest` (`001_initial_schema.up.sql:18`). Dry-run, backtest, and replay all record `route_decision` rows, but are explicitly non-production: dry-run "records route decisions … without mutating … production statistics" (`internal/pipeline/dryrun.go:59-62`), and replay/backtest are "labeled distinctly from production outcomes". Non-production runs must not change this production statistic.
- **No drift.** A counter and its underlying evidence must never disagree. A write-time increment that must be suppressed for three of five run types is exactly the surface that produces silent wrong answers.
- **Admin-read scale.** The route list is a low-frequency, single-user view; an aggregate query cost is immaterial.
- **Config integrity.** Route rows and config snapshots are configuration, not statistics. Materializing a volatile statistic onto the config entity entangles two unrelated lifecycles.
- **Error honesty.** A failed aggregate must not be presented as a real zero or a partially populated route list.

## Options considered

### Option A — One list endpoint with a list-time aggregate (chosen)

Return `match_count` on every `GET /api/routes` list item, derived from production `route_decision` evidence by one aggregate query using distinct `video_id` values per route. The field is a list projection, not a field on route configuration. A zero-match route carries `match_count: 0`.

- **Benefits:** one aggregate rather than an N+1 request pattern; no migration or counter-maintenance path; errors are visible at the same API boundary as route listing; the aggregate directly implements the accepted distinct-video unit.
- **Costs:** one aggregate query is added to a low-frequency admin read.
- **Failure modes:** if route listing or aggregation fails, the whole list request fails; it never returns counts that look like genuine zeros.

### Option B — Dedicated `GET /api/routes/{route_uuid}/stats` endpoint

Have the browser fetch each route's match count separately, or retain a direct per-route stats endpoint as the canonical interface.

- **Benefits:** a caller can request one route's count without loading the full list.
- **Costs:** the accepted design asks for the count in the route row; an N+1 browser implementation creates redundant calls; retaining both endpoints duplicates API and store surface that can drift.
- **Failure modes:** if clients convert a failed per-route stats request to `0`, the result is indistinguishable from a genuine zero. Rejected; this path is not part of the supported contract.

### Option C — Materialized `route.match_count` counter

- **Benefits:** scalar read without an aggregate.
- **Costs:** requires a migration and evaluation write-path coupling; production-only updates must be correct for every run mode; counter can diverge from durable evidence. Rejected as more complex and less reliable.

### Option D — Defer match count

- **Costs:** contradicts the accepted Route List row design. Rejected on authority grounds.

## Decision

In the context of the accepted Route List design and clarified BR-020 distinct-video contract, facing a list-time aggregate, a per-route stats endpoint, a materialized counter, or deferral, we decided **for deriving the list projection from `route_decision`** and **against** the other options. `GET /api/routes` is the canonical surface; the unused per-route stats endpoint is not part of the supported API because it adds no required capability and risks duplicating the meaning. The count unit is one distinct `video_id` per route within the selected time scope.

The Route List has no period selector, so the time span is lifetime. The accepted architecture's non-production isolation (INV-8) excludes dry-run, replay, and backtest data; only `scheduled` and `manual` production runs contribute. If one video has multiple matched decisions for the same route across those runs, it contributes once.

## Count definition and API placement

- **Source/filter/count:** use matched `route_decision` evidence joined to `run`, over all history, restricted to `run_type IN ('scheduled','manual')`, and count `COUNT(DISTINCT route_decision.video_id)` grouped by route. Each video therefore contributes at most once per route in the lifetime scope. Routes with no contributing evidence return an explicit zero.
- **List placement.** `GET /api/routes` list items MUST contain `match_count` as an integer, including zero. It is a response projection, not route configuration; `GET /api/routes/{route_uuid}` does not expose a match count. In the target route API, each list item's `id` is a UUID; route names may repeat and are labels, not keys (ADR-011, ARS-004). Implementers should use a list-response DTO or equivalent so an uncomputed zero is not emitted as fact.
- **Failure contract.** If either the route list or aggregate query fails, `GET /api/routes` returns `500 {"error":"failed to list routes"}` and no route list. It MUST NOT return a missing/partial count or convert a query error to zero.
- **Dedicated endpoint.** `GET /api/routes/{route_uuid}/stats` and a per-route `CountRouteMatches` API are not part of the supported contract. The implementation retains `ListRouteMatchCounts` only as the aggregate backing the route list; it is not a per-route endpoint.
- **Web.** The Route List renders the returned count directly; a count-fetch failure cannot be caught per row and shown as zero because no per-row request exists.

## Realization (2026-10-24)

**Implemented:** `internal/router/store.go` computes the distinct-video production aggregate; `internal/server/server.go` joins it into `GET /api/routes` through the list response projection. `GET /api/routes/{route_uuid}` does not expose `match_count` in the target contract. No per-route stats route is registered; the former path returns 404. `ListRouteMatchCounts` remains the list-scoped store method and is not a duplicate per-route endpoint. **Identity status:** current source still serializes the route list `id` as an integer; UUID route identity is target behavior specified by ADR-011/ARS-004 and is not implemented by this match-count decision.

## Consequences

- **Positive:** no schema migration; no write-path change; one source of truth; production-only and distinct-video semantics are enforced in one place; the response cannot silently display a failed count as zero.
- **Negative:** the route list read is two queries rather than one; the server needs a clear response projection that keeps statistics out of configuration snapshots.
- **Revisit trigger:** if a separately deployed or documented external API client is established, assess compatibility before removing a per-route stats endpoint.
- **Neutral / inherited:** the count's durability is exactly `route_decision`'s durability. A future hard-delete of `video`/`run` (which `route_decision` cascades from, `001_initial_schema.up.sql:134-135`) would reduce historical counts — but that equally affects `run.videos_*` counters and `stats.ListRouteStats`, so this decision introduces no new exposure. No such hard-delete path exists today; routes are soft-deleted per ADR-003.

## Confirmation

**Confirmed on 2026-10-24:** `go test -count=1 ./internal/server ./internal/router ./internal/config ./cmd/server` passed. The router store test seeds repeat matches for the same video across production runs plus non-production matches and verifies the distinct production count. Server tests verify explicit list counts/zero behavior, omission of `match_count` from route detail, failure behavior, and 404 for `/api/routes/{route_uuid}/stats`. The store query is `COUNT(DISTINCT rd.video_id)` grouped by route and restricted to matched decisions in scheduled/manual runs. This confirmation covers the match-count behavior, not UUID route serialization.

- Unit: seed the same video as matched for one route in multiple production runs, another matched video for that route, and matches in dry-run/replay/backtest runs; assert the lifetime count is two, production-only, and zero is explicit for a route with no production matches.
- Integration: run a manual run that matches a route, then a dry-run that would match it; assert the dry-run does not affect the production count.
- Failure: force the aggregate query to fail and assert the route list returns 500 with the error body and no route array.
- Projection: assert `GET /api/routes/{route_uuid}` does not emit `match_count`, and config snapshots never contain it.
- Regression: assert there is no web/API call path to a per-route match-count endpoint.

## Open questions

- None for the Route List match-count meaning, source, scope, or API placement. Duplicate display names and UUID identity are settled by BR-003/ADR-011/ARS-004 and do not change this count contract. Failed YouTube attempt accounting remains outside this decision and unresolved under OC-001.
