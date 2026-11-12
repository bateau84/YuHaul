# Video search

The Videos page lets the user find a video YuHaul knows ([BR-038](../../requirements/yuhaul/br-038-video-search-by-title-or-id.md)). The server classifies the typed text. A video ID or a YouTube link opens the video. Other text of 3 to 200 characters searches the titles YuHaul still holds. Search reads local data in one read-only transaction, never calls YouTube and never copies a title. Example: `lofi beats` → `GET /api/videos/search?q=lofi%20beats` returns `total: 3` and those 3 results, newest discovery first. A pasted `https://youtu.be/dQw4w9WgXcQ?t=42` returns `kind: video`, and the SPA opens `/videos/dQw4w9WgXcQ`. That view shows title "expired", the channel ID, the latest production outcome, the queue state and every run that decided the video, with simulation runs labelled.

- [ADR-001 — Search Held Titles with a Request-Time Scan in Go](decisions/adr-001-held-title-search-by-request-time-scan.md) — chosen over SQL `LIKE` (ASCII-only folding fails AC7), an FTS5 trigram index (a second, retention-sensitive title store with triggers, rowid drift and a folding mismatch) and an in-memory index (a title copy outside the database). The FTS5 trigram index is the measured fallback. The gate is test P2 at 150,000 held titles. If it fails, the fallback applies.
- [ARS-001 — Video Search API, Classification, Matcher, Paging, Queries and Tests](specifications/ars-001-video-search-api-and-queries.md) — `GET /api/videos/search` and `GET /api/videos/{videoId}`, the ordered input classifier and link rules, the matcher (`NFC(fold(NFC(s)))`, literal, no match that ends before a combining mark), the read queries, the opaque keyset cursor plus `page=n` jumps, the match `total` with positions and page count (BR-038 AC18), and 26 conformance tests plus the timing tests P1–P4 at QS-001's load (150,000 held titles).

**Status:** accepted 2026-11-11, revised the same day after critique (CHANGES REQUIRED). Implemented 2026-11-12: classifier, matcher, cursor, metadata reads, `Service`, both HTTP handlers and the server conformance tests. The SPA routes were built earlier. The speed claim is unverified until P1–P4 run. Search adds no migration. Run-initiation migration 026 (ARS-005) owns the two `(video_id, status)` queue indexes and the stored `unavailable_on_youtube` outcome, and search is built on top of it. Reconciled 2026-11-11 (Plan revision 19): `total` added; Q1, Q2 and S1 closed.

## Rules the design must preserve

1. A title or channel name exists only in `transient_metadata`, and in memory for one request. There is no index, cache, log line, cursor content or client storage holding one. Every response is `Cache-Control: no-store`.
2. Held means `metadata.ProjectLocal(row, cutoff)` gives `current_local`, at one cutoff per request. Search and Run Detail share this one rule.
3. No search code path can reach `internal/youtube`, `internal/quota`, or metadata refresh and fetch.
4. Latest outcome and queue state come only from production tables and production runs. Simulation appears only as labelled `RunEntry` rows or as `record: simulation_only`.
5. A fingerprint is never selected, returned, matched or shown.
6. The per-run outcome expression is the shared constant also used by Run Detail.

## Findings and questions

| # | Finding or question | Owner | Disposition | Blocks |
|---|---|---|---|---|
| Q1 | Links pasted without a scheme. | User | **Closed 2026-11-11.** Not recognized (BR-038 Definitions); they are name-searched. ARS-001 already does this; test T1 covers it. | No |
| Q2 | Total match count. | User | **Closed 2026-11-11.** Shown (BR-038 AC18). ARS-001 §4.1 and §7.3 add `total`, positions and `total_pages`, computed in the same scan. It does not conflict with ADR-001. | No |
| Q3 | "Every run that recorded a decision" is read as: a `route_decision` row, or a committed per-run outcome. A production run that only discovered the video, with outcome `not_yet_evaluated`, is not listed. | Requirements | Assumed; test T25 pins it. | No |
| F3 | *Unavailable on YouTube* versus `failed`. | Run-initiation (ARS-005) | **Closed 2026-11-11.** Revised ARS-005 stores `unavailable_on_youtube` natively. Search shows the stored `final_outcome` unchanged; `failed` with code `video_unavailable` stays `failed`. Test T22 covers both. | No |
| S1 | QS-001's load (50,000 held titles) is below likely production: about 85,000–155,000 titles held within 30 days, given the 2026-11-06 runs (22,067 and 62,866 discovered). | Requirements owner | **Closed 2026-11-11.** QS-001 now states 150,000 known videos that all hold a title. The P1–P4 seed matches it; if P2 fails, the FTS5 fallback applies. | No |
| A1 | `q` is in the URL, so a proxy access log (Traefik) may record search text. It is user-typed text, not provider data, so it is not a retention breach under AC16. | Operator | Advisory: drop query strings from access logs if search text should not be kept there. The application logs never record `q`. | No |
| F4 | Table `channel` stores `channel_name` durably (migration 001), while the compliance analysis keeps channel names only in transient metadata. Search never reads it. | Architecture and compliance owner | Routed. Outside this scope. | No |
| F5 | `state.Store.updateVideoState` writes `video.final_outcome` without checking the run type. The design relies on only production paths calling it `[unverified]`. Test T23 guards the search side. | Coder / reviewer | Accepted risk, with a test. | No |
| F6 | Speed is an estimate: up to about 150,000 short rows scanned and folded per page in pure-Go SQLite `[unverified]`. | Coder | Gate: P1–P4 at 150,000 held titles; if P2 fails, the FTS5 fallback. | No |

Specification choices within BR-038, which the user may change: a link longer than 200 characters is `too_long`; a `watch` link with more than one `v` is unusable; ports and userinfo make a link unusable; character counts are Unicode code points; full case folding (`STRASSE` finds `Straße`).

The shared [architecture log](../log.md) records changes.
