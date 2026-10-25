---
type: decision
title: ADR-005 — Complete `GET /api/playlists` Listing for the Action Playlist Picker
description: Provide the Route Editor's playlist picker with the authenticated user's complete playlist list by following YouTube Data API playlists.list pages behind a PlaylistProvider seam and accounting for each successful page.
tags: [architecture, decision, yuhaul, playlists, api, route-editor]
---

**Status:** accepted

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md)
- [ADR-001 — YouTube Provider Seam via Base URL Override](adr-001-youtube-provider-seam.md)
- [ADR-007 — Canonical Route Action Type at the API Boundary](adr-007-canonical-route-action-type-api-boundary.md)

## Satisfies

- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route Editor — Action configuration UI — the `add_to_playlist` playlist selector is "populated from user's YouTube playlists (fetched via `GET /api/playlists`)", with loading, empty ("No playlists found"), and error ("Retry") states.
- [Anchor](../anchors/youtube-discovery-routing.md) AC4 — routes contain configurable conditions and playlist actions; AC18 — external API consumption accounted for and visible.

## Problem

The accepted design spec requires the Route Editor's `add_to_playlist` action to select from the user's YouTube playlists through `GET /api/playlists`, distinguishing a successful empty list from a loading error. At the time of this decision, the provider called only the first `playlists.list` page and recorded quota after the overall operation, so a multi-page list could be incomplete and successful page costs could be lost on a later-page failure. The picker has no page controls; the endpoint therefore supplies the complete list or fails rather than returning a partial list. The implementation below now closes those gaps.

## Decision drivers

- **The picker needs a real playlist source.** The spec's loading/empty/error states presume a real async fetch, not a hardcoded list.
- **The picker must be complete.** The accepted UI has one selector and no pagination controls; a `200` response must therefore contain all pages, not a silent first-page subset.
- **Successful-page quota accounting.** Each successful `playlists.list` page costs and records 1 unit (AC18 / BR-018); failed-attempt logging remains outside this decision under proposed OC-001.
- **Preserve the provider seam.** The new call must honor `YUHAUL_YOUTUBE_BASE_URL` (ADR-001) so Product Acceptance can fake the playlist listing on the assembled binary.
- **Failure is not silent.** The spec distinguishes "failed to load" (error + Retry) from "no playlists" (empty). The endpoint must encode both.

## Options considered

### Option A — Aggregate every `playlists.list` page in the provider (chosen)

Keep a simple `GET /api/playlists` response and let the provider follow each `nextPageToken` until exhausted. The provider concatenates pages in upstream order and returns the complete collection only after every page succeeds. Wire the client's quota recorder so every successful page is recorded once at 1 unit.

- **Benefits:** matches the accepted one-selector experience; no partial collection is mistaken for the full playlist set; uses the existing page-token client contract and provider seam; each successful page is quota-accounted.
- **Costs:** an account with many playlists incurs one quota unit and one request per page and waits for all pages before receiving a result.
- **Failure modes:** failure on any page returns a provider error and no partial collection. The handler returns 502, driving the existing error/retry state rather than a misleading incomplete list.

### Option B — Expose page tokens through `GET /api/playlists` and paginate in the UI

Return one page plus a continuation token and require the browser to request additional pages.

- **Benefits:** bounds each response and allows incremental presentation.
- **Costs:** requires cursor-aware API state and a paginated selector, neither of which exists in the accepted design; users could fail to discover playlists omitted from an uncompleted page sequence. Rejected absent a UI requirement for pagination.

### Option C — Return only the first page

Keep the current single-page provider call and return a successful response.

- **Costs:** truncates accounts with more than 50 playlists while reporting success; rejected because the accepted selector has no indication that results are incomplete.

## Decision

In the context of an accepted playlist-selector design with an API that currently exposes only the first page, facing the choice between provider-side page aggregation, browser-driven pagination, or retaining a silent first-page result, we decided **for provider-side aggregation of every page**. This keeps the existing simple picker contract, avoids partial success, and reuses the existing page-token seam; the quota cost grows by one unit per successful API page.

### Endpoint contract

`GET /api/playlists`

| Case | Status | Body |
|---|---|---|
| Success, ≥1 playlist across all pages | `200` | `{"playlists":[{"playlist_id":"PL...","playlist_name":"My Playlist"}, …]}` |
| Success, 0 playlists | `200` | `{"playlists":[]}` |
| No active token / auth not configured | `502` | `{"error":"not authenticated"}` |
| Any page/API/network error | `502` | `{"error":"failed to list playlists"}`; no partial playlist array |

The provider follows `nextPageToken` until it is empty and preserves upstream page/item order. Each successful page is recorded once as one `playlists.list` unit. If a later page fails, prior successful-page quota entries remain recorded and the HTTP response is an error, not a partial success. The 502 path drives the spec's "Failed to load playlists" + Retry state; the `200 []` path drives the "No playlists found" empty state. The unauthenticated case is deliberately a 502, not a fake empty list.

This decision defines accounting for successful listing pages. Failure-category accounting for unsuccessful YouTube attempts remains subject to OC-001, which is currently proposed; this ADR does not claim to settle that broader contract.

## Realization

- **Implemented:** `internal/youtube/client.go` requests one page and returns its continuation token; `cmd/server/main.go` loops through every token, preserves page/item order, and returns no partial list on error. The configured base URL and per-successful-page quota recorder are passed to the client. `internal/quota/quota.go` assigns one unit to each successful `playlists.list` page.
- **Implemented:** `internal/server/server.go` exposes `GET /api/playlists` through `PlaylistProvider`; provider failure maps to `502`, with no authenticated token distinguished as `not authenticated`.
- **Implemented:** `web/src/api/playlists.ts` calls the endpoint, and `web/src/components/RouteEditor.tsx` loads the playlist selector through `listPlaylists()`. Browser-side pagination is not exposed; the endpoint supplies one complete collection.
- **Documented:** [YuHaul System Architecture](yuhaul-system-architecture.md) and [ARS-001](ars-001-route-and-playlist-api-contracts.md) state complete server-side pagination and per-successful-page accounting.

## Consequences

- **Positive:** the action config selects from the complete real playlist set; successful page costs are visible in the ledger; ADR-001's base-URL seam keeps the endpoint fakeable for Product Acceptance.
- **Negative:** a multi-page playlist collection waits for every page and incurs one quota unit per successful page; the list is still fetched live on editor open and is not cached.
- **Neutral:** API action-type spelling is settled by ADR-007; this ADR supplies the endpoint used by that action.
- **Neutral:** the no-op provider default returns an error → `502`, so a missing `WithPlaylistProvider` wire fails loud rather than faking the spec's "No playlists found" empty state.

## Confirmation

**Confirmed on 2026-10-24:** `go test -count=1 ./internal/server ./internal/router ./internal/config ./cmd/server` passed. The production-provider tests exercise multi-page listing and item order, one quota entry per successful page, an empty complete list, and a later-page error that returns no partial response while retaining earlier successful-page entries. The server tests cover response/error mapping. The targeted frontend run (`npm test -- --run src/views/RunDetailView.test.tsx src/views/RouteReorder.test.tsx src/components/RouteEditor.test.tsx src/views/RouteEditorView.test.tsx`) passed all 43 tests; the `RouteEditor` and `RouteEditorView` tests cover playlist loading/empty/error picker behavior.

**Not claimed:** these automated tests do not constitute a live YouTube Product Acceptance run. They use the configured base-URL seam and a fake provider.

## Open questions

- **Specifier / owner of OC-001:** decide and reconcile whether unsuccessful YouTube page attempts must also be included in quota consumption logging with response category. This does not block the successful multi-page API contract; it blocks claiming complete failed-call accounting.
