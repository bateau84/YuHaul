---
type: decision
title: ADR-001 — Search Held Titles with a Request-Time Scan in Go, Not an FTS5 Index or SQL LIKE
description: Realize BR-038 name search as a per-request read-only scan of currently held transient_metadata titles, matched in Go with NFC-normalized full Unicode case folding and a literal substring test, sorted and keyset-paged in memory. No new table, index, trigger or cache holds a title. An FTS5 trigram prefilter is rejected for now because it adds a second title store, fold mismatches and purge obligations, and is kept as a measured fallback.
tags: [architecture, decision, videos, search, retention, sqlite, fts5, performance, unicode]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted — technical realization of [BR-038](../../../requirements/yuhaul/br-038-video-search-by-title-or-id.md) AC7, AC11, AC14–AC17 and [QS-001](../../../requirements/yuhaul/qs-001-video-search-response-time.md). Not implemented. The exact contract is [ARS-001](../specifications/ars-001-video-search-api-and-queries.md). The speed claim is an estimate until ARS-001 test P2 runs at 150,000 held titles; that test is the gate for this decision. Revised 2026-11-11 after critique: the gate load was raised from QS-001's 50,000 held titles to 150,000. Independent critique is recommended before implementation.

## Summary

**What it does.** For each name-search request the server opens one read-only SQLite transaction, reads every `transient_metadata` row that could hold a title (QS-001 assumes 50,000; production may hold about 85,000–155,000), keeps the rows that are held at that moment, and tests each title in Go: does its folded form contain the folded search text? Matches are sorted (newest production discovery first, then video ID) and the requested page of 50 is cut with a keyset cursor. Only those 50 are projected into result rows.

**Why.** It is the smallest design that meets every rule. Titles stay only where they already live, so expiry and deletion keep working unchanged (AC16). Matching uses one Go function, so case folding and accent rules are exact and testable (AC7). The cost is proportional to the number of held titles, which the 30-day hold bounds.

**Example.** The user types `lofi beats`. The server reads every candidate row (150,000 in the gate test), finds 3 whose folded title contains `lofi beats`, sorts them and returns all 3 with no next cursor. A title whose metadata expired yesterday is not a candidate: `Expire` cleared it and set `is_active = 0`.

**Limit.** Every page request scans all held titles again. If test P2 fails at 150,000 held titles, or production later holds clearly more than that, the FTS5 prefilter (option C) must be built.

## Derived from

- [BR-038](../../../requirements/yuhaul/br-038-video-search-by-title-or-id.md) and [QS-001](../../../requirements/yuhaul/qs-001-video-search-response-time.md) (uncommitted, accepted 2026-11-11).
- [Compliance analysis](../../yuhaul-youtube-compliance.md) and [system architecture](../../yuhaul-system-architecture.md): raw titles and channel names live only in transient metadata, cleared on expiry.
- Source read this session:
  - `internal/store/migrations/002_metadata_lifecycle.up.sql`: `transient_metadata(video_id TEXT PRIMARY KEY, title, channel_name, …, fetched_at, expires_at, expired_at, is_active)` with indexes on `is_active` and `(is_active, expires_at)`.
  - Title writers are `metadata.Store.Upsert`, `Refresh`, `Expire` (sets `title = NULL`, `is_active = 0`, `expired_at`), `CleanupExpired` (delete), and an `INSERT INTO transient_metadata` in `internal/discovery/engine.go`.
  - `metadata.Manager.ReadLocalDisplay` holds the current eligibility rule: `is_active`, no `expired_at`, `expires_at` after the cutoff, a valid `fetched_at` not after the cutoff and before `expires_at`. Timestamps are parsed in Go because several text layouts are stored.
  - `go.mod`: `modernc.org/sqlite v1.58.0` (bundles SQLite 3.53.4, so FTS5, the trigram tokenizer and FTS5 `secure-delete` exist) and `golang.org/x/text`.
  - `internal/store/store.go` opens the database with `_journal_mode=WAL` and `_busy_timeout=5000`; the driver's DSN parser accepts both keys (module source, `sqlite.go` lines 307–340). In WAL mode a read transaction does not block a run's writes.

## Satisfies

| Requirement | How |
|---|---|
| BR-038 AC7 | Literal substring test on `key(title)` against `key(search text)`, where `key` = NFC → full Unicode case fold → NFC. No wildcard syntax exists, so `%`, `_`, `*`, quotes and repeated spaces are ordinary characters. A match may not end just before a combining mark, so accents are never dropped (`cafe` ≠ `Café` in NFC or NFD). |
| AC11, AC18 | All matches are sorted by one total order. The keyset cursor returns each match exactly once over unchanged data. `total` is that list's length, from the same scan, so it equals the union of the pages. |
| AC14–AC15 | One read-only transaction; the search package may not import the YouTube client, quota ledger or metadata refresh code (enforced by a dependency test). |
| AC16 | No new copy of any title: no index, table, trigger, cache or log holds one. Held is decided per request at one cutoff, using the same rule as `ReadLocalDisplay`. |
| AC17, QS-001 | Estimated cost is one indexed range read of up to about 150,000 short rows plus an in-memory sort of at most 20,000 keys per page `[unverified]`. ARS-001 test P2 measures it at 150,000 held titles. |

## Decision question

How should name search find videos whose held title contains the search text, at QS-001 scale, without creating new title retention?

## Drivers and constraints

1. **No new retention (AC16).** Any second copy of a title must disappear when the source does, in the same transaction, including physically inside index structures.
2. **Exact matching rules (AC7).** Unicode case folding, no accent removal, every character literal, contiguous substring.
3. **Speed (QS-001).** First page and page 100+ under 2 s at the 95th percentile with up to 20,000 matches, also during a run. QS-001 states 50,000 held titles. The 2026-11-06 discovery volumes (22,067 and 62,866 videos per run) mean production may hold about 85,000–155,000 titles within 30 days. So this decision is gated at 150,000. QS-001's load needs updating by the requirements owner.
4. **Correctness under load (QS-001 AC4).** A faster answer that misses a match fails.
5. **Read only (AC15)** and **no YouTube call (AC14).**

## Options considered

### A. SQL `LIKE` over `transient_metadata.title`

- *Mechanism:* `WHERE title LIKE '%' || :escaped || '%' ESCAPE '\'`, with the eligibility filter in SQL.
- *Benefits:* no schema change; very little code.
- *Costs and risks:* SQLite `LIKE` folds only ASCII letters, so `ÉTÉ` would not find `été` (violates AC7). `%` and `_` need escaping. A leading `%` cannot use an index, so it scans anyway. Eligibility in SQL must compare timestamp text, but several layouts are stored, so a string comparison can be wrong (risks AC16).
- *Verdict:* fails AC7 as written. With a Go case-fold function it becomes option B.

### B. Request-time scan with matching in Go (chosen)

- *Mechanism:* read candidate rows (`is_active = 1 AND expired_at IS NULL AND title` not empty) joined to `video` for the sort key; apply the shared Go eligibility rule at one cutoff; test `key(title)` contains `key(needle)`; sort; cut a keyset page.
- *Benefits:* no schema, trigger or migration; titles stay in one place; folding, accents and literals are one pure function, unit-testable; eligibility identical to `ReadLocalDisplay`.
- *Costs:* every page rescans all held titles. Linear in held titles.
- *Failure modes:* slow if held titles grow a lot; mitigated by the measured gate and option C.
- *Operations:* nothing new to run, repair or migrate.

### C. FTS5 trigram index kept in sync with `transient_metadata`

- *Mechanism:* an FTS5 table with `tokenize='trigram'`, kept current by triggers on insert, update and delete of `transient_metadata`, used as a prefilter; Go re-checks each candidate.
- *Benefits:* cost proportional to matches, not to held titles; rare strings answer in milliseconds.
- *Costs and risks:*
  - It is a **second store of title text**: the trigram index can rebuild a title from overlapping trigrams. Plain FTS5 deletes write tombstones and leave the old entries in segment b-trees until a merge, so a cleared title stays readable in the file. It needs the `secure-delete` option, a migration backfill, and triggers on every writer, including the raw `INSERT` in `discovery/engine.go`.
  - **Rowid instability:** `transient_metadata` has a text primary key, so its implicit rowid can change on `VACUUM`. An external-content table keyed by that rowid can drift. It would need a contentless table with its own key map, or a stored copy of the title.
  - **Fold mismatch:** the trigram tokenizer folds one code point to one (simple folding). AC7 full folding (`ß` → `ss`) would miss matches unless the index stores Go-folded text, which needs a Go function inside SQLite triggers. Then any write from a process without that function, such as the `sqlite3` shell or a migration run, fails.
  - Common needles (`the`, up to 20,000 matches) still cost a 20,000-row sort, the same as B.
- *Verdict:* buildable, but it adds a retention-sensitive structure, triggers, a migration and a second matching implementation to save time that B is expected to have spare `[unverified]`. Kept as the fallback.

### D. Process-local in-memory index

- *Mechanism:* load held titles into a Go structure and keep it in sync.
- *Verdict:* rejected. It is a second title copy outside the database, so no transaction ties it to `Expire` or `CleanupExpired`. It would need its own expiry logic, which is exactly the retention risk AC16 forbids.

## Comparison

| Driver | A LIKE | B Go scan | C FTS5 trigram | D memory |
|---|---|---|---|---|
| AC16 no new copy | yes | yes | needs secure-delete, triggers, key map | no |
| AC7 exact folding | **no** | yes | prefilter only; needs Go re-check, and full folding needs a stored folded copy | yes |
| QS-001 at 150k held | likely | to be measured (P2) `[unverified]` | yes | yes |
| Scales well past 150k held | no | degrades linearly | yes | yes |
| New schema or ops | none | none | migration, triggers, backfill | sync logic |

## Decision

**Option B.** Name search is a request-time scan of held titles in one read-only transaction, matched in Go. ARS-001 §5 gives the exact matcher and §6 the queries.

The following constraints are part of the design:

1. No component may store, cache, index, log or copy a title or channel name outside `transient_metadata`, beyond the lifetime of one HTTP request. Logs may record counts and timings, never search text or titles.
2. Held is decided by one shared pure function, extracted from `ReadLocalDisplay`, at one cutoff per request taken after the candidate rows are read.
3. The search package must not depend on `internal/youtube`, `internal/quota` or any metadata refresh or fetch method.

## Consequences

- **Positive:** no migration; expiry, refresh and cleanup need no change; the matching rules live in one function; search cannot drift from Run Detail's held-title rule.
- **Negative:** per-page CPU and I/O grow with held titles, and paging does not get cheaper on later pages. A run's write load shares the disk.
- **Neutral:** the result is a page plus cursors, the match `total` (BR-038 AC18) and the page positions. The total is the length of the match list this request already sorted, so it adds no query and no stored data. It is the same on every page while data is unchanged, because each page request rescans the same held titles at the same rule. A page jump (`page=n`) costs the same as a cursor page, because the full sorted list exists on every request. Adding the total does not conflict with any constraint of this decision: it is a count, not a title. (Reconciled 2026-11-11, Plan revision 19.)

## Confirmation and reconsideration

- **Confirmation (gate):** ARS-001 tests P1–P4 pass on the deployment host, or one of equal capacity, with the **150,000-held-title** seed of ARS-001 §9.2.
- **Fallback trigger:** if P2 fails at that seed, or production later holds clearly more than 150,000 held titles, build option C before relying on search. Its mandatory constraints:
  - FTS5 `secure-delete=1`;
  - synchronous triggers on every `transient_metadata` writer, including `UPDATE` that clears the title;
  - a stable key, not the implicit rowid;
  - Go re-check of every candidate with the same matcher; `total` counts the re-checked matches, never an FTS estimate;
  - a purge test that reads the FTS shadow tables after `Expire` and `CleanupExpired`.
- **Changed premise:** if the requirements owner widens searchable titles (for example to fingerprints), revisit this decision.

## Open questions

None block this decision. See the topic index for questions routed to the user and to other owners.
