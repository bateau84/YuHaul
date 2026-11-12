---
type: decision
title: ADR-001 — Search Held Titles Through an FTS5 Trigram Prefilter With a Go Re-check (Request-Time Scan Failed Its Gate)
description: Realize BR-038 name search with a regular FTS5 trigram index over Go-folded keys of held titles, keyed by a stable integer document map, with FTS5 secure-delete and SQL purge triggers on transient_metadata. Go writers add entries in the same transaction as the title write; triggers remove them on every update or delete. A startup and sweep reconcile repairs gaps. Every candidate is re-checked in Go with the exact matcher and the held rule, and total counts only re-checked matches. The request-time scan (option B) was chosen first and failed its measured gate (P2) at 150,000 held titles; it remains the path for keys shorter than 3 code points and for an unbuilt index.
tags: [architecture, decision, videos, search, retention, sqlite, fts5, performance, unicode]
timestamp: 2026-11-12T12:00:00Z
---

**Status:** accepted 2026-11-12 — option C replaces option B. The decision realizes [BR-038](../../../requirements/yuhaul/br-038-video-search-by-title-or-id.md) AC6, AC7, AC11 and AC14–AC18, and [QS-001](../../../requirements/yuhaul/qs-001-video-search-response-time.md). Option C is not implemented yet. The exact contract is [ARS-001](../specifications/ars-001-video-search-api-and-queries.md) §10. The file path is unchanged so that existing links still work.

History:

- 2026-11-11: accepted with option B, the request-time scan.
- 2026-11-12: option B failed its gate, test P2, so option C applies, as this ADR required. The user confirmed the switch.
- 2026-11-12: revised after critique (CHANGES REQUIRED, session `ses_e2e61920affeUTLBU835PmTm85`):
  - migration renumbered to 029, after BR-039's 028;
  - an insert purge trigger;
  - the secure-delete write cost: a spike, batching, P3 criteria and recorded alternatives;
  - drop-and-recreate rebuild and damaged-index recovery;
  - write-lock-first reconcile;
  - the limits of the purge proof;
  - a key version.

Get an independent critique of §10 before implementation.

## Summary

**What it does.** Name search no longer reads every held title. A full-text index holds, for each held title, a folded search key: the title after the same Unicode normalization and case folding the matcher uses. The index answers "which titles contain these characters?" in time that depends on the number of hits, not on the number of held titles. Go then re-checks each hit against the live `transient_metadata` row:

- Is the title still held at this request's cutoff?
- Does it really contain the search text under BR-038's rules?

Only rows that pass both checks are results, sorted, paged and counted.

**Why.** Option B was the simplest design, but it cannot meet QS-001: at 150,000 held titles the scan alone took about 1.5 s. Option C is the smallest design that removes that cost and still meets every other rule:

- **No retention beyond the source title (AC16).** An index entry never outlives the title it came from. SQL triggers remove it when the title is cleared, changed, replaced or deleted, and FTS5 `secure-delete` removes it physically from the index structures.
- **Exact matching (AC7).** The index only proposes candidates. Matching stays in the one Go matcher.

**Example.** A user types `lofi beats`. The key `lofi beats` has 10 code points, so the request uses the index. It runs `title_search MATCH '"lofi beats"'` and gets 4 candidates. Go re-checks them:

- 1 title expired at 09:00, but the sweep has not run yet. `metadata.Held` rejects it.
- The other 3 contain `lofi beats` under the matcher.

The result is `total: 3` and those 3 rows, newest discovery first, the same answer option B gave.

**Limits.**

- A search key shorter than 3 code points after folding still uses the full scan. The trigram index cannot answer it. This happens only when normalization shortens a typed 3-character text, for example `e` + U+0301 + `x`.
- Between a title's `expires_at` and the next sweep, its index entry still exists, exactly as long as the title itself still exists in `transient_metadata`. It is never returned, because of the Go re-check.
- A row written by something other than the Go writers is not searchable until the next reconcile. This includes the `sqlite3` shell and a raw-SQL test fixture. Retention is unaffected, because purges are done by triggers.

## Derived from

- [BR-038](../../../requirements/yuhaul/br-038-video-search-by-title-or-id.md) and [QS-001](../../../requirements/yuhaul/qs-001-video-search-response-time.md).
- [Compliance analysis](../../yuhaul-youtube-compliance.md) and [system architecture](../../yuhaul-system-architecture.md): raw titles live only in transient metadata and are cleared on expiry.
- **Gate evidence (option B).** The P1–P4 harness was added in commit `e4cc1f7` (`cmd/server/video_search_perf_integration_test.go`, gated by `YUHAUL_PERF=1`). It seeds 150,000 held titles, 100,000 expired rows and 150,000 imported references, and drives the real server over HTTP. The user reported these results on 2026-11-12, on a 4-core Intel i7-10510U:
  - P2 name search: p95 of about 2.8–3.0 s against a limit of 2 s. **Fail.** The candidate scan alone took about 1.5 s.
  - P1 typed ID that is not found: p95 of about 1.5 s against a limit of 1 s. **Fail.** BR-038 AC6 also name-searches that text, so it pays the same scan.

  The raw run output is not stored in the repository `[unverified by this session]`. The harness itself was read.
- Source read this session:
  - `internal/store/migrations/002_metadata_lifecycle.up.sql`: `transient_metadata(video_id TEXT PRIMARY KEY, …)`. This is a rowid table without an `INTEGER PRIMARY KEY`, so its implicit rowid may change on `VACUUM`.
  - Every production writer of `transient_metadata`:
    - `metadata.Store.Upsert`, which has no production caller;
    - `Refresh`;
    - `Expire`, which sets `title = NULL`, `is_active = 0` and `expired_at`;
    - `CleanupExpired`, which deletes and has no production caller;
    - `discovery.Engine.upsertTransientMetadata`, a raw `INSERT … ON CONFLICT DO UPDATE` inside the discovery transaction (`internal/discovery/engine.go` lines 484–508).
  - The sweep is `metadata.Manager.ExpireStale`. `RunLifecycle` calls it once at the start of every production run (`internal/pipeline/pipeline.go` line 408). If it fails, the run logs a warning and continues.
  - `store.Open` runs migrations and then a Go backfill (`BackfillRouteIdentities`) before any caller serves requests. This is the precedent for a Go-side index build.
  - Package imports: `metadata` imports only `quota` among internal packages; `store` imports none. So `store` → `metadata` and `discovery` → `metadata` create no cycle.
  - `go.mod`: `modernc.org/sqlite v1.58.0` bundles SQLite 3.53.4, which has FTS5, the trigram tokenizer (3.34+), `secure-delete` (3.42+) and contentless-delete (3.43+).
- **Experiment, this session.** SQLite 3.46.1 CLI, scratch files only.
  - A regular FTS5 table with `secure-delete = 1` loses a deleted row's trigram bytes from `_data` immediately. A contentless-delete table with `secure-delete = 1` keeps them in a segment behind a tombstone.
  - `trigram case_sensitive 1` matches a double-quoted phrase literally: quotes are escaped by doubling, `*`, `(`, `-`, `:` and `^` are literal, and a double space differs from a single space.
  - A phrase shorter than 3 characters matches no row.
  - The query plan drives from the FTS index (`VIRTUAL TABLE INDEX 0:M1`).

  ARS-001 §10.8 test R1 must repeat the purge check on the bundled modernc build.

## Satisfies

| Requirement | How |
|---|---|
| BR-038 AC7 | The index is only a prefilter. Every candidate is re-checked with the ARS-001 §5 matcher (`Key`, full case folding, literal, no match that ends before a combining mark). The index text and the query are both `Key` output, so the prefilter never misses a true match (§10.5 proof). |
| AC6 | A typed ID that is not found is name-searched through the same path. An 11-character key always uses the index. |
| AC11, AC18 | The match list is built from re-checked rows only. Sorting, cursors, `page=n` and `total` are unchanged (ARS-001 §7). `total` never uses an FTS count. |
| AC14, AC15 | Search still runs in one read-only transaction with no YouTube dependency. Only the existing metadata writers, the store opener and the sweep write the index. Search never writes it. |
| AC16 | Each index entry lives no longer than its source title. A purge trigger removes it in the same statement that clears, changes or deletes the title, and `secure-delete` removes the trigrams physically from the FTS5 structures. The Go re-check at the request cutoff stops a title that is no longer held from being found before the sweep runs. Retention tests R1–R7 (ARS-001 §10.11) check this, within the limits stated there. |
| AC17, QS-001 | Cost grows with the number of hits, at most about 20,000 per QS-001, not with the number of held titles. P1–P4 rerun at the 150,000 seed is the gate (ARS-001 §9.2). |

**Reading of AC16.** AC16 forbids keeping, copying or extending a title "beyond the existing retention rules". The index stores a folded copy whose lifetime is tied to the source title. It never outlives it, and the triggers enforce that even for writers outside Go. The user agreed that option C applies under the gate. This decision relies on that reading. If the requirements owner reads AC16 as banning every second copy, regardless of lifetime, option C is not allowed and only option D's cost class (also rejected) remains. See Open questions.

## Decision question

How should name search find videos whose held title contains the search text within QS-001's limits at 150,000 held titles, given that the full scan failed its gate, without creating title retention beyond the source?

## Drivers and constraints

1. **No retention beyond the source (AC16).** A second copy must disappear when the source title does: in the same transaction and physically inside the index structures, whichever writer changes the source.
2. **Exact matching (AC7).** Full Unicode case folding (`ß` → `ss`), no accent removal, literal characters, contiguous substring, and no match that ends before a combining mark.
3. **Speed (QS-001).** p95 ≤ 2 s for name search and ≤ 1 s for ID requests at 150,000 held titles, also during a run. Measured: the scan alone is about 1.5 s.
4. **No missed match (QS-001 AC4).** The prefilter must return every true match.
5. **Read-only search (AC15).** Only existing writers and maintenance code write the index.
6. **Stable keys.** `transient_metadata` has a text primary key and a rowid that `VACUUM` may change.

## Options considered

### A. SQL `LIKE` over `transient_metadata.title`

Rejected, as before. It folds only ASCII letters (fails AC7), and a leading `%` scans anyway, so it has no speed benefit over B.

### B. Request-time scan with matching in Go (chosen 2026-11-11; failed its gate 2026-11-12)

- *Mechanism:* read every candidate held row, then filter, fold, match, sort and page in Go.
- *Result:* P2 p95 of about 2.8–3.0 s and typed not-found p95 of about 1.5 s at 150,000 held titles. Both fail QS-001.
- *Kept for:* keys shorter than 3 code points, and an index that has not been built yet (§10.6). Both are rare, and the scan is correct in both cases. In the second case it is only slow.

### C. FTS5 trigram index over Go-folded keys, purged by triggers (chosen)

- *Mechanism:*
  - a **regular** (content-storing) FTS5 table `title_search(k)` with `tokenize='trigram case_sensitive 1'` and `secure-delete = 1`;
  - a document map `title_search_doc(doc_id INTEGER PRIMARY KEY, video_id TEXT UNIQUE)` that supplies stable integer rowids;
  - **adds** are explicit Go writes in the same transaction as the title write, because only Go can compute `Key`;
  - **Removals** are SQL triggers on `transient_metadata`: before insert (which covers `REPLACE`, whose implicit delete fires no delete trigger), after update and after delete. So they cannot be bypassed;
  - a Go **reconcile** at `store.Open` and at the sweep removes entries that are not indexable and adds missing ones;
  - search sends `Key(text)` as a quoted FTS5 phrase and re-checks every hit in Go.
- *Benefits:* cost grows with the number of hits. AC7 semantics stay in one Go function. Retention is enforced in SQL for every writer.
- *Costs:* migration 029, two tables, three triggers, one Go helper called by 3 writers, a reconcile pass, a second candidate query, and extra write cost on discovery, refresh and expiry (secure-delete is slower).
- *Failure modes:*
  - A non-Go writer adds a held row: it is not found until the next reconcile. This is a recall gap, not a retention breach.
  - A future migration rebuilds `transient_metadata` and drops the triggers. Schema test R6 catches it.
  - A text shorter than 3 code points after folding falls back to the scan.
  - **Secure-delete write cost.** The critic measured about 3 ms per purge in a dense test index. Expiry is batched (`IndexWriteBatch`, 200 rows per transaction), and a spike on realistic titles is the first implementation task. If the spike shows the cost is too high, the alternatives in ARS-001 §10.10 (S1–S4) go to a decision (O5).
  - **A damaged index blocks metadata writes.** The triggers make every write to `transient_metadata` also write the index. Recovery is drop-and-recreate (ARS-001 §10.9). This dependency is accepted, because writes that bypass the index would leave title copies behind.
  - **Unicode or `Key` change.** Entries built with an older `Key` could miss matches. `title_search_state.key_version` records the version; search uses the scan path until a rebuild finishes, and `store.Open` rebuilds.

### C′ variants rejected inside option C

| Variant | Why it loses |
|---|---|
| Contentless or contentless-delete FTS5 table (no stored text) | `secure-delete` needs the old tokens to remove entries. A contentless-delete table only writes a tombstone and keeps the trigrams in its segments. This was observed this session. The deleted title can then be rebuilt from the file, which breaks driver 1. |
| External-content table on `transient_metadata` | It must key on the implicit rowid, which `VACUUM` may change (driver 6). Its content would be the raw title, not `Key`, so the case-insensitive trigram tokenizer would fold simply (one code point at a time) and miss `STRASSE` → `Straße` (driver 4). |
| Index raw titles with `case_sensitive 0` | The same simple-folding miss (driver 4). |
| Insert triggers that call a Go `fold()` SQL function | Every connection without that function fails on insert: the `sqlite3` shell and golang-migrate's SQL. A failing writer is worse than a recall gap that the reconcile repairs. The function would also exist only in Go anyway. |
| All index writes explicit in Go, no triggers | A missed or new writer would leave a title copy behind after the source was cleared. That is the retention failure driver 1 forbids. Purges must not depend on writer discipline. |
| Rebuild the whole index at every startup instead of reconciling | It deletes 150,000 entries with secure-delete and refolds every title at every start, for no gain over a reconcile that does both kinds of repair. |

### D. Process-local in-memory index

Rejected, as before. It would be a title copy outside the database, with no transaction tying it to `Expire` or delete.

## Comparison

| Driver | A LIKE | B Go scan | C FTS5 trigram + Go re-check | D memory |
|---|---|---|---|---|
| AC16 bounded to source | yes | yes | yes: purge triggers, secure-delete, R1–R7 | no |
| AC7 exact | **no** | yes | yes: prefilter is a superset; Go decides | yes |
| QS-001 at 150k held | no | **failed (measured)** | expected `[unverified]`; gate is P1–P4 rerun | yes |
| Never misses a match | — | yes | yes when all writers are Go writers; otherwise until the next reconcile | — |
| New schema or ops | none | none | migration 029, triggers, reconcile | sync logic |

## Decision

**Option C.** Name search proposes candidates from the FTS5 trigram index `title_search` and decides each one in Go with the ARS-001 §5 matcher and `metadata.Held` at the request cutoff. ARS-001 §10 gives the schema, triggers, writer contract, reconcile, query and tests.

The following constraints are part of the design:

1. **Title copies.** A title or channel name may exist outside `transient_metadata` only in two places:
   - request-scoped memory;
   - the `title_search` index. This holds only `Key(title)`, never a channel name, and only for rows that were indexable when last written (ARS-001 §10.3).

   No other component may store, cache, log or copy one. Logs may record counts, timings and the search path, but never search text, keys or titles.
2. **Purges in SQL.** These purge that video's index entry through a trigger in the same statement:
   - every `INSERT` into `transient_metadata`, in every conflict form, before the row is written, by `NEW.video_id`;
   - every `UPDATE` of `video_id`, `title`, `is_active`, `expired_at`, `fetched_at` or `expires_at`;
   - every `DELETE`.

   No Go code is needed for a purge to happen.
3. **Secure delete.** `title_search` is a regular FTS5 table with the persistent `secure-delete` option set to 1. It is never contentless or contentless-delete.
4. **Stable key.** The FTS5 rowid is `title_search_doc.doc_id`, an `INTEGER PRIMARY KEY`. The implicit rowid of `transient_metadata` is never used.
5. **The index never decides.** Held is decided by `metadata.Held` at one cutoff per request, and the match by `titlekey.Contains`. `total` counts only rows that pass both checks.
6. **Adds only through Go writers.** An index entry is added only by `metadata.IndexTitleTx`, in the same transaction as the title write, or by `metadata.ReconcileSearchIndex`. Search code never writes.
7. **Isolation stays.** The search package must not depend on `internal/youtube`, `internal/quota` or any metadata refresh or fetch method.
8. **Ordering.** BR-039 owns migrations 027 and 028. Search's migration is 029, and search releases only after BR-039's 028 (ARS-001 §10.2).
9. **Rebuild only by drop-and-recreate.** Never by `DELETE` of every entry, which under secure-delete costs about one purge per entry, and never by FTS5 `'rebuild'`.
10. **Reconcile and recreate take the write lock first** (`BEGIN IMMEDIATE` on a dedicated connection). Every index-writing transaction is bounded to `IndexWriteBatch` rows.
11. **Key version.** Search uses the index only when `title_search_state.key_version` equals `titlekey.Version()`.

## Consequences

- **Positive:**
  - search cost grows with hits, so a typed not-found ID and rare texts answer in milliseconds `[unverified until P1–P2]`;
  - matching semantics and the held rule still live in one place each;
  - removing a title cannot leave a copy behind, whatever the writer.
- **Negative:**
  - a new migration and two triggers;
  - discovery, refresh and expiry each do extra index work. Secure-delete costs about 3 ms per purge in the critic's dense test, which is why expiry is batched and the spike comes first;
  - metadata writes now fail when the index is damaged, until it is recreated;
  - `store.Open` runs a reconcile, which at first start after 029 folds and inserts every held title;
  - the sweep gains a reconcile pass of about one scan of the index map;
  - there are now two candidate queries (indexed and scan), and they must agree (test D1).
- **Neutral:** paging, cursors, `total`, the HTTP contract and the SPA are unchanged.

## Confirmation and reconsideration

- **Before building:** the write-cost spike (ARS-001 §10.10) passes with secure-delete on. If it does not, decide among S1–S4 (O5) before continuing.
- **Confirmation (gate):** ARS-001 P1–P4 pass at the 150,000-held-title seed on the i7-10510U host or the deployment host. P3 now also requires no `SQLITE_BUSY`, the run-duration budget, and sweep transactions of at most 1 s. The seed is reconciled after it is inserted (ARS-001 §9.2). R1–R7, D1, D2 and F-R pass on the bundled modernc SQLite.
- **If P2 still fails with option C:** look at the per-stage split that P2 reports (`begin`, `candidates`, `recheck`, `sort`, `project`, `encode`). Do not drop the Go re-check or count FTS hits as `total`. A remaining cost of the 20,000-match sort is a QS-001 matter for the requirements owner, not a reason to weaken AC7 or AC4.
- **Revisit** if the requirements owner reads AC16 as forbidding any second copy, or widens searchable text (for example to fingerprints or channel names).

## Open questions

| # | Question | Owner | Blocks |
|---|---|---|---|
| O1 | AC16 reading: a lifetime-bounded index copy is "within the existing retention rules". The user agreed to option C on that basis; the requirements text does not say it explicitly. | Requirements owner | No (user-agreed); record in BR-038 if desired |
| O2 | **Compliance decision: connection-wide `PRAGMA secure_delete`.** FTS5 `secure-delete` removes entries from the index structures, as far as SQL can read them (ARS-001 §10.11 states exactly what R1 proves). It does not remove bytes from freed pages, in-page free space, WAL frames before checkpoint, or backups. The same is true of `transient_metadata` today when `Expire` sets `title = NULL`. The options are: (a) keep the current state, parity with the source table; (b) enable `PRAGMA secure_delete = ON` on every connection, through the DSN, which zeroes freed content for every table; it costs extra writes on every delete and overwrite, and it does not clear WAL frames before checkpoint; (c) option (b) plus a checkpoint after each sweep. The architect has no recommendation that would replace this decision; (a) meets AC16 as written. | Compliance owner | No: parity with the source table |
| O4 | P3's run-duration budget (index on ≤ 1.15 × index off) and the 1 s sweep-transaction limit are architecture defaults. QS-001 sets no write-side budget. | Requirements owner | No |
| O5 | If the spike shows secure-delete is too costly: choose among ARS-001 §10.10 S1–S4. Each one changes when removed trigrams leave the index structures. | Compliance owner with the user | Blocks building only if the spike fails |
| O3 | The sweep (`ExpireStale`) runs only at the start of a production run, and `CleanupExpired` has no production caller. So a title past `expires_at` stays in `transient_metadata`, and with it in the index, until the next run. Search never shows it (Go re-check), but the source retention is a separate question. | Compliance and metadata owner | No: existing behavior, outside search |
