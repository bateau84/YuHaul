---
type: decision
title: ADR-005 — `GET /api/playlists` Endpoint for the Action Playlist Picker
description: Add a GET /api/playlists endpoint backed by YouTube Data API playlists.list (mine=true, 1 unit/call) behind a PlaylistProvider seam, so the Route Editor's add_to_playlist action can populate a real playlist dropdown instead of a free-text playlist ID.
tags: [architecture, decision, yuhaul, playlists, api, route-editor]
---

**Status:** proposed

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md)
- [ADR-001 — YouTube Provider Seam via Base URL Override](adr-001-youtube-provider-seam.md)

## Satisfies

- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route Editor — Action configuration UI — the `add_to_playlist` playlist selector is "populated from user's YouTube playlists (fetched via `GET /api/playlists`)", with loading, empty ("No playlists found"), and error ("Retry") states.
- [Anchor](../anchors/youtube-discovery-routing.md) AC4 — routes contain configurable conditions and playlist actions; AC18 — external API consumption accounted for and visible.

## Problem

The action configuration does not match the accepted spec (reviewer MAJOR-4). `web/src/components/RouteEditor.tsx:326-343` renders the target playlist as a free-text ID input (`placeholder="Target playlist ID"`); there is no `listPlaylists` API client and **no `/api/playlists` endpoint exists anywhere in the repo** (grep across `internal/` + `web/src`: no matches). The spec requires an `add_to_playlist` action whose parameter is a playlist dropdown populated from `GET /api/playlists`.

The YouTube Data API exposes the necessary surface (`playlists.list`, `mine=true`, `part=snippet`, 1 unit/call), but the `youtube.Client` (`internal/youtube/client.go`) currently wraps only `subscriptions`, `channels`, `playlistItems`, `videos`, and `playlistItems.insert` — no playlist listing.

## Decision drivers

- **The picker needs a real playlist source.** The spec's loading/empty/error states presume a real async fetch, not a hardcoded list.
- **Quota accounted.** Every YouTube call must flow through the quota ledger (AC18 / BR-018); `playlists.list` costs 1 unit.
- **Preserve the provider seam.** The new call must honor `YUHAUL_YOUTUBE_BASE_URL` (ADR-001) so Product Acceptance can fake the playlist listing on the assembled binary.
- **Failure is not silent.** The spec distinguishes "failed to load" (error + Retry) from "no playlists" (empty). The endpoint must encode both.

## Options considered

### Option A — Real `GET /api/playlists` backed by `playlists.list` (chosen)

Add `ListPlaylists` to `youtube.Client`; expose a `PlaylistProvider` seam on the server; wire a provider in `cmd/server/main.go` using `auth.Service.ActiveToken` + `youtube.NewClient` (base-URL seam + quota recorder). Return `200 {"playlists":[{"playlist_id","playlist_name"}]}` on success.

- **Benefits:** satisfies the spec fully; reuses the existing `metadataFetcher` adapter pattern (`cmd/server/main.go:460-501`) and ADR-001's base-URL seam; quota cost is real and recorded.
- **Costs:** a new client method, a new server seam, and one more construction site (the ADR-001 consequence already anticipated "a fourth future construction site must be added deliberately").
- **Failure modes:** no active token → `ActiveToken` errors (surface as 502, not a fake empty list, so the UI shows "connect account"/retry rather than a misleading "no playlists").

### Option B — Stub endpoint returning an empty list (floor only)

`GET /api/playlists` returns `200 {"playlists":[]}` unconditionally.

- **Benefits:** unblocks the UI's empty-state rendering with zero API surface.
- **Costs:** the dropdown never populates; the spec's loading/error states are unreachable; the operator cannot actually select a playlist. Acceptable only as a temporary fallback, not a resolution. Rejected as the decision (retained as the degradation floor in the unauthenticated case).

## Decision

In the context of a pre-merge MAJOR where the action config has no playlist source, facing the choice between a real `playlists.list`-backed endpoint and a stubbed empty list, we decided **for the real endpoint** and **against** a permanent stub, to deliver the spec's playlist picker and honest quota accounting, accepting a new client method and server seam.

### Endpoint contract

`GET /api/playlists`

| Case | Status | Body |
|---|---|---|
| Success, ≥1 playlist | `200` | `{"playlists":[{"playlist_id":"PL...","playlist_name":"My Playlist"}, …]}` |
| Success, 0 playlists | `200` | `{"playlists":[]}` |
| No active token / auth not configured | `502` | `{"error":"not authenticated"}` |
| YouTube API/network error | `502` | `{"error":"failed to list playlists"}` |

The 502 path drives the spec's "Failed to load playlists" + Retry state; the `200 []` path drives the "No playlists found" empty state. The instruction's "at minimum returns an empty list" floor is satisfied by the `200 []` empty-success case; the unauthenticated case is deliberately a 502 (not a fake empty list) so the UI does not mislead the operator.

## Realization

- `internal/youtube/client.go`
  - Add `OpPlaylistsList Operation = "playlists_list"`.
  - Add `Playlist struct { ID, Title string }`.
  - Add `ListPlaylists(ctx, nextPageToken string) ([]Playlist, string, error)` → `GET /playlists?part=snippet&mine=true&maxResults=50`, paged, 1 unit/page (mirrors `ListSubscriptions`).
- `internal/quota/quota.go`
  - Add `OpPlaylistsList Operation = "playlists_list"` and `unitCosts[OpPlaylistsList] = 1`.
- `internal/server/server.go`
  - Add a `PlaylistProvider` interface (`ListPlaylists(ctx) ([]Playlist, error)`) with a no-op default that **returns an error** (distinguishable from a real YouTube failure, e.g. `"playlist provider not configured"`), so an unwired server surfaces `502` instead of a fake `200 []`; add `WithPlaylistProvider`; register `GET /api/playlists` in `Handler()`.
  - Handler maps provider error → `502`; otherwise `200` with the list. The no-op default's error is what makes a missing `WithPlaylistProvider` wire loud rather than "No playlists found".
- `cmd/server/main.go`
  - Add a `playlistProvider` (mirroring `metadataFetcher`): `auth.ActiveToken(ctx)` → `youtube.NewClient(ctx, token, youtube.WithBaseURL(cfg.YouTubeBaseURL), youtube.WithRecorder(...))` → `ListPlaylists`. Wire via `server.WithPlaylistProvider(...)`. The recorder routes the 1-unit cost to the quota ledger (AC18).
- `web/src/api/` (coder)
  - Add `listPlaylists()` client calling `GET /api/playlists`.
- `web/src/components/RouteEditor.tsx` (coder)
  - Replace the free-text playlist ID input with a dropdown populated from `listPlaylists()`, with the spec's loading/empty/error states.
- `docs/architecture/yuhaul-system-architecture.md`
  - Add `playlists.list | 1` to the quota model table (§Quota Management) and a note on the new endpoint in §API Surface, per the documentation update obligation.

## Consequences

- **Positive:** the action config matches the spec; the operator selects a real playlist; quota cost for the listing is visible in the ledger; the ADR-001 seam keeps the endpoint fakeable for Product Acceptance.
- **Negative:** one more client-construction site carries the base URL and recorder (the known cost from ADR-001); the playlist list is fetched live on editor open (1 unit), not cached.
- **Neutral:** the `add_to_playlist` vs `playlist_insert` action-type naming is out of scope for this endpoint decision (see concern).
- **Neutral:** the no-op provider default returns an error → `502`, so a missing `WithPlaylistProvider` wire fails loud rather than faking the spec's "No playlists found" empty state.

## Confirmation

- Unit test: a fake YouTube serving `GET /playlists` returns the playlist list through `youtube.Client.ListPlaylists`; the server handler returns `200` with the shaped payload, `502` on provider error.
- Product Acceptance: with `YUHAUL_YOUTUBE_BASE_URL` pointed at a fake provider, opening the Route Editor populates the playlist dropdown from `GET /api/playlists` and the quota ledger records the 1-unit cost.

## Open questions

- None.
