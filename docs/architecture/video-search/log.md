---
type: Log
---

# Video Search architecture log

## 2026-11-19

- Dropped the `s` alias from the [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) §6.1 indexed query (`FROM title_search`, joined on `title_search.rowid`), per review `3b99d97c-c32f-40c2-9795-6132436f942d`. Test D2 expects the plan to start with `SCAN title_search VIRTUAL TABLE INDEX`, which SQLite prints only when the table is not aliased. The query's meaning is unchanged.
- Corrected [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) §10.5 after debugger session `ses_e0d2a7aa6ffeiSZrtfWc9ebUwX` confirmed the finding. With migration 029's triggers, a deferred writer's first trigger-firing statement connects the FTS5 table `title_search`. That opens a read transaction, so the write upgrade gets `SQLITE_BUSY` at once: SQLite does not call the busy handler for a read-then-write upgrade. The earlier claim that `Expire` and `Refresh` honour `_busy_timeout` was wrong.
- `BEGIN IMMEDIATE` (write lock first) now applies to every writer whose transaction can fire the `transient_metadata` triggers before it holds the write lock: today `Expire` and `Refresh`, and `Upsert` once it becomes a transaction. Discovery (writes `video` first) and the autocommit `CleanupExpired` are unaffected. Connection warm-up is allowed only as a secondary guard. Added test R9, cold-connection writer contention, and added it to the gate lists in ARS-001, [ADR-001](/docs/architecture/video-search/decisions/adr-001-held-title-search-by-request-time-scan.md) and the topic index.
- S1, migration 029, migration ordering and the other decisions are unchanged. The writer fix is implementation work in progress; R9 has not run yet.

## 2026-11-18

- Revised [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) and synchronized [ADR-001](/docs/architecture/video-search/decisions/adr-001-held-title-search-by-request-time-scan.md) in response to Loom QA `f3522231-9b8a-435e-91e6-1a0c2dfc760c`: purge the destination on an ID move, close or discard canceled write transactions safely, and measure actual 5,000-entry expiry against a working no-index control.
- S1, migration 029 ordering and rollback, and release thresholds are unchanged. This is a design correction, not independent recheck, implementation, or release evidence.
