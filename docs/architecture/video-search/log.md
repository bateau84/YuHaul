---
type: Log
---

# Video Search architecture log

## 2026-11-19

- Fixed stale text found by review `4948ec4f` so it matches the 2026-11-19 §10.4 amendment (`89c0937`). [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) R4 and [ADR-001](/docs/architecture/video-search/decisions/adr-001-held-title-search-by-request-time-scan.md)'s key-change risk now name `metadata.PrepareSearchIndex`, called by `cmd/server`, as the rebuild point instead of `store.Open`. ARS-001 §10.3's discovery row no longer says `metadata` imports only `quota` and `titlekey`: it may import `store`, `auth`, `youtube`, `quota` and `titlekey`, and `discovery` → `metadata` stays acyclic. No contract change.
- Decided the startup index boundary for fts-lifecycle (coder result `943f7124`, session `ses_e0c6f319effekmpNk4DxdS6MkR`). [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) §10.4 and [ADR-001](/docs/architecture/video-search/decisions/adr-001-held-title-search-by-request-time-scan.md) constraint 12 now say this:
  - A new `metadata.PrepareSearchIndex` runs the startup steps in order: recreate on a key-version mismatch, then reconcile, then optimize only if the reconcile removed entries.
  - `cmd/server` calls it right after `store.Open`, before any service, worker or listener starts.
  - `store` never imports `metadata`.

  Rejected:
  - Moving three in-package tests to external packages. `quota_test.go` uses the unexported `bucketDate` and `nextReset`, and the `auth` tests share in-package helpers. It would also leave `store` depending on a package whose dependencies keep growing.
  - Moving only the index code below `store` and `metadata`. It needs the held rule (`LocalRow`, `ProjectLocal`, `Held`, `ParseLocalTime`), which would have to move with it.

  R5's startup clause now names `PrepareSearchIndex`. ADR-001's 2026-11-12 premise that `metadata` imports only `quota` is marked superseded.
- Added `metadata.Store.UpsertFetched` (rq-metadata-fetch) to [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) §10.3 and test R7, per review `bf15ec57`. It is the sixth `transient_metadata` title writer and indexes each row in its write transaction. R7 now says it detects the index call from parsed call expressions, not text, so a name in a comment or string does not count. The other writers and the trigger purge guarantee are unchanged.
- Dropped the `s` alias from the [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) §6.1 indexed query (`FROM title_search`, joined on `title_search.rowid`), per review `3b99d97c-c32f-40c2-9795-6132436f942d`. Test D2 expects the plan to start with `SCAN title_search VIRTUAL TABLE INDEX`, which SQLite prints only when the table is not aliased. The query's meaning is unchanged.
- Corrected [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) §10.5 after debugger session `ses_e0d2a7aa6ffeiSZrtfWc9ebUwX` confirmed the finding. With migration 029's triggers, a deferred writer's first trigger-firing statement connects the FTS5 table `title_search`. That opens a read transaction, so the write upgrade gets `SQLITE_BUSY` at once: SQLite does not call the busy handler for a read-then-write upgrade. The earlier claim that `Expire` and `Refresh` honour `_busy_timeout` was wrong.
- `BEGIN IMMEDIATE` (write lock first) now applies to every writer whose transaction can fire the `transient_metadata` triggers before it holds the write lock: today `Expire` and `Refresh`, and `Upsert` once it becomes a transaction. Discovery (writes `video` first) and the autocommit `CleanupExpired` are unaffected. Connection warm-up is allowed only as a secondary guard. Added test R9, cold-connection writer contention, and added it to the gate lists in ARS-001, [ADR-001](/docs/architecture/video-search/decisions/adr-001-held-title-search-by-request-time-scan.md) and the topic index.
- S1, migration 029, migration ordering and the other decisions are unchanged. The writer fix is implementation work in progress; R9 has not run yet.

## 2026-11-18

- Revised [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) and synchronized [ADR-001](/docs/architecture/video-search/decisions/adr-001-held-title-search-by-request-time-scan.md) in response to Loom QA `f3522231-9b8a-435e-91e6-1a0c2dfc760c`: purge the destination on an ID move, close or discard canceled write transactions safely, and measure actual 5,000-entry expiry against a working no-index control.
- S1, migration 029 ordering and rollback, and release thresholds are unchanged. This is a design correction, not independent recheck, implementation, or release evidence.
