# Video search

The Videos page lets the user find a video YuHaul knows ([BR-038](../../requirements/yuhaul/br-038-video-search-by-title-or-id.md)). The server classifies the typed text. A video ID or a YouTube link opens the video. Other text of 3 to 200 characters searches the titles YuHaul still holds. Candidates come from an FTS5 trigram index over folded keys of held titles. Go then re-checks each candidate against the live title, with the held rule and the exact matcher. Search reads local data in one read-only transaction, never calls YouTube and never writes. The index entry for a title lives only as long as the title itself: triggers purge it, and secure-delete removes it physically. Example: `lofi beats` → `GET /api/videos/search?q=lofi%20beats` returns `total: 3` and those 3 results, newest discovery first. A pasted `https://youtu.be/dQw4w9WgXcQ?t=42` returns `kind: video`, and the SPA opens `/videos/dQw4w9WgXcQ`. That view shows title "expired", the channel ID, the latest production outcome, the queue state and every run that decided the video, with simulation runs labelled.

- [ADR-001 — Search Held Titles Through an FTS5 Trigram Prefilter With a Go Re-check](decisions/adr-001-held-title-search-by-request-time-scan.md) — revised 2026-11-12. The request-time scan (option B) failed its gate at 150,000 held titles: P2 name search p95 was about 2.8–3.0 s, and a typed not-found ID took about 1.5 s. So option C now applies:
  - a regular FTS5 trigram table over Go-folded keys, with `secure-delete`;
  - a stable integer document map, because `transient_metadata` has a text primary key;
  - purge triggers before every insert (covering `REPLACE`), after every update and after every delete;
  - Go adds in every writer, including discovery's raw insert;
  - a reconcile at startup and at the sweep;
  - a Go re-check, so that `total` counts only re-checked matches;
  - a key version, a drop-and-recreate rebuild, and batched, write-lock-first index writes.

  It was chosen over a contentless index (a deleted title's trigrams survive as tombstones), an external-content index (unstable rowid, simple folding), fold-in-trigger (breaks non-Go writers) and Go-only purges (a missed writer would leave a copy).
- [ARS-001 — Video Search API, Classification, Matcher, Paging, Queries and Tests](specifications/ars-001-video-search-api-and-queries.md) — `GET /api/videos/search` and `GET /api/videos/{videoId}`, the ordered input classifier and link rules, the matcher (`NFC(fold(NFC(s)))`, literal, no match that ends before a combining mark), the read queries, the opaque keyset cursor plus `page=n` jumps, and the match `total` with positions and page count (BR-038 AC18). §10 specifies the title search index: migration 029, the triggers, the writer contract, the reconcile, write-lock-first transactions, the tokenizer, query and key-version rules, path selection, the invariants, rebuild, damage and rollback, write cost and batching (with the spike and the alternatives S1–S4), and the retention proofs. It includes conformance tests, retention tests R1–R7, differential tests D1–D2, rollback test F-R, and the timing tests P1–P4 at QS-001's load (150,000 held titles), which are rerun as the gate.

**Status:** accepted 2026-11-11 and revised the same day after critique (CHANGES REQUIRED).

- **Implemented 2026-11-12 (option B):** classifier, matcher, cursor, metadata reads, `Service`, both HTTP handlers and the server conformance tests. The SPA routes were built earlier. The P1–P4 harness was added in commit `e4cc1f7`.
- **Revised 2026-11-12:** option B failed P2 and the typed-not-found part of P1. ADR-001 now selects option C. ARS-001 §10 is not implemented.
- **Revised again 2026-11-12** after critique (CHANGES REQUIRED, session `ses_e2e61920affeUTLBU835PmTm85`), findings F1–F8. The first implementation task is now a write-cost spike for secure-delete.
- **Gate:** the spike passes; then P1–P4 rerun on the indexed path, with P3's write-side criteria, plus R1–R7, D1–D2 and F-R.
- **Migrations:** search adds migration 029. BR-039 owns 027 and 028, and search releases only after BR-039's 028. Run-initiation migration 026 (ARS-005) owns the two `(video_id, status)` queue indexes and the stored `unavailable_on_youtube` outcome.
- **Reconciled 2026-11-11 (Plan revision 19):** `total` added; Q1, Q2 and S1 closed.

## Rules the design must preserve

1. A title exists only in three places:
   - `transient_metadata`;
   - memory, for one request;
   - as `Key(title)`, in `title_search`. An entry there is added only by the Go writers or the reconcile, and only for an indexable row. It is purged by trigger, physically through secure-delete, whenever the source row changes or is deleted, and at the next reconcile once the row stops being indexable.

   A channel name exists only in `transient_metadata` and in memory for one request. No cache, log line, cursor content or client storage holds a title or a channel name. Every response is `Cache-Control: no-store`.
2. Held means `metadata.ProjectLocal(row, cutoff)` gives `current_local`, at one cutoff per request. Search and Run Detail share this one rule.
3. No search code path can reach `internal/youtube`, `internal/quota`, or metadata refresh and fetch.
4. Latest outcome and queue state come only from production tables and production runs. Simulation appears only as labelled `RunEntry` rows or as `record: simulation_only`.
5. A fingerprint is never selected, returned, matched or shown.
6. The per-run outcome expression is the shared constant also used by Run Detail.
7. The index never decides. Results and `total` come only from rows that pass `metadata.Held` at the request cutoff and `titlekey.Contains`. The number of FTS hits is never shown or counted.
8. `title_search` stays a regular FTS5 table with `secure-delete = 1`. Its rowid is the stable `title_search_doc.doc_id`, never the implicit rowid of `transient_metadata`.
9. A rebuild is drop-and-recreate, never `DELETE` of every entry. Index-writing transactions are bounded to `IndexWriteBatch` rows, and the reconcile takes the write lock first.
10. Search uses the index only when `key_version` equals `titlekey.Version()`.

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
| F6 | Speed of option B. | Coder | **Closed 2026-11-12: failed.** P2 name search p95 was about 2.8–3.0 s (scan alone about 1.5 s), and P1 typed not-found p95 about 1.5 s, at 150,000 held titles on a 4-core i7-10510U (harness `e4cc1f7`, results reported by the user). The FTS5 fallback (option C) applies. | No |
| F7 | `discovery.Engine.upsertTransientMetadata` sets `is_active = 1` on conflict but leaves an earlier `expired_at`. So a re-discovered video whose metadata had expired stores a fresh title that is never held: Run Detail and search both hide it. `IndexTitleTx` reads the stored row, so it follows that behavior and does not index the title. | Metadata owner | Routed. This is existing behavior; search does not change it. | No |
| F8 | Option C's speed is an estimate `[unverified]`. | Coder | Gate: P1–P4 rerun on the indexed path (ARS-001 §9.2). | Release |
| F9 | Page-level remnants and the connection-wide `secure_delete` pragma (ADR-001 O2, now a compliance decision with options), and the sweep running only at run start (ADR-001 O3). | Compliance owner | Routed. Parity with `transient_metadata` today. | No |
| O4 | P3's run-duration budget (1.15×) and the 1 s sweep-transaction limit are architecture defaults. | Requirements owner | May change them. | No |
| O5 | If the spike fails, choose among the secure-delete alternatives S1–S4 (ARS-001 §10.10). | Compliance owner with the user | Open. The spike ran on 2026-11-12 and failed with secure-delete on (ARS-001 §10.10 "Spike result"). | Building |
| O1 | AC16 is read as allowing a copy whose lifetime is bounded by the source title (ADR-001 O1). | Requirements owner | The user agreed that option C applies. Record it in BR-038 if desired. | No |

Specification choices within BR-038, which the user may change: a link longer than 200 characters is `too_long`; a `watch` link with more than one `v` is unusable; ports and userinfo make a link unusable; character counts are Unicode code points; full case folding (`STRASSE` finds `Straße`).

The shared [architecture log](../log.md) records changes.
