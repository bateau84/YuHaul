---
type: decision
title: ADR-015 — Persist One Durable Corpus Import Completion Timestamp
description: Records the latest successfully completed corpus import as YuHaul operational metadata without retaining import history or source content.
tags: [architecture, decision, yuhaul, corpus-import, persistence, metadata]
---

**Status:** accepted

Settings must show when the current YuHaul corpus was most recently imported. This is the time an import into YuHaul completed, not a video's publication date or the date a source row was first observed. The source database may contain dates for videos and old processing runs, but those do not identify completion of an import into the current destination. Persist one timestamp in the existing SQLite database; do not retain job history or raw source content.

For example, a successful import that inserts only new rows, skips every row as already present, or accounts for row-level errors still updates the timestamp when it reaches the normal completed outcome. Preview, cancellation, and fatal failure leave the prior timestamp unchanged. The in-process job status remains temporary and may disappear on restart; the timestamp remains readable from SQLite.

## Derived from

- [BR-009 — Historical Title Persistence and Corpus Import](../requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md) AC6 and AC9.
- [ARS-002 — Web Corpus Import API Contract](ars-002-web-corpus-import-api.md).
- [ADR-009 — Web Corpus Import Uses an In-Process Async Job](adr-009-web-corpus-import-job-lifecycle.md).
- [YuHaul System Architecture](yuhaul-system-architecture.md) §Two-Store Data Model and §Migration.
- Current storage/import evidence: `internal/store/migrations/012_route_identity.up.sql`, `internal/store/migrations/006_settings.up.sql`, `internal/store/store.go`, `internal/migration/migration.go`, `internal/corpusimport/manager.go`, and `cmd/migrate/main.go`.

## Satisfies

- BR-009 AC6: provides a durable, queryable corpus-import recency value alongside the existing corpus-size reporting.
- BR-009 AC9: updates the value only when a normal successful completion is durably recorded; preserves it across application restarts; does not infer publication dates.
- BR-009 AC5: row-level errors count as a successful completion only when each source row is accounted for by the existing import counters/reason codes; an incomplete/fatal import does not update the value.

## Decision question

How should YuHaul retain the completion time of its most recent successful corpus import without persisting async-job state, import history, raw titles, filenames, or a source publication date?

## Drivers and constraints

- The accepted meaning is the completion time of the latest successful import into the YuHaul destination database.
- BR-009 AC9 explicitly allows an otherwise completed import to count as successful when it inserts no new rows or reports row-level errors, provided the source rows are accounted for.
- A cancellation or fatal source/destination failure may leave already-committed chunks, but it is not a successful completion and must not advance the timestamp.
- Import job state remains process-local under ADR-009. It is not necessary to retain job IDs, progress, source identity, or a job history to satisfy the timestamp requirement.
- The import paths share `migration.Importer.RunChunked`: the web manager invokes it directly and the CLI wrapper `Importer.Run` delegates to it. A completion update at this shared boundary covers both entrypoints.
- Existing `settings` values represent configurable effective configuration; a corpus import completion timestamp is operational metadata, not a user-editable setting.
- Source/video timestamps and `published_at` are not import-completion evidence. The supplied `~/my.db` has timestamp columns for video and prior pipeline-run records, but none identifies completion of an import into the YuHaul destination.

## Options considered

### A — Keep the timestamp only in the in-process import manager

- **Benefits:** no schema or persistent write.
- **Costs:** value disappears on restart, violating BR-009 AC9.
- **Failure mode:** a successful import cannot be inspected after process restart.
- **Disposition:** rejected because restart durability is explicit.

### B — Infer recency from video publication or row timestamps

- **Benefits:** uses date-like values already present in source or corpus rows; avoids a completion marker.
- **Costs:** those values describe videos, source-row observation, or a prior processing run, not import completion. They are absent for empty/all-skipped imports and cannot identify when a chunked import finished.
- **Failure mode:** reports a plausible but semantically wrong time, silently recreating the publication-date/source-date interpretation rejected by the accepted requirement.
- **Disposition:** rejected; no backfill or runtime inference from these dates is permitted.

### C — Persist an import-history/job table

- **Benefits:** could retain per-import identity, counts, outcomes, and progress across restarts.
- **Costs:** adds durable lifecycle state and history beyond the accepted requirement, needs retention/recovery policy, and broadens stored data without improving the single recency value.
- **Failure mode:** job history can be mistaken for automatic resume or success and expands the database's retained information.
- **Disposition:** rejected; ADR-009's no-durable-job-state decision remains intact.

### D — Store a completion key in the generic `settings` table

- **Benefits:** avoids creating a new table and reuses existing key/value storage.
- **Costs:** that table is for effective configuration values. Operational import metadata would be mixed with user/configuration settings and would share generic setting access semantics.
- **Failure mode:** metadata can be mistaken for a configurable setting or leak through future settings projections.
- **Disposition:** rejected as a weaker domain boundary than one small dedicated row.

### E — Dedicated singleton corpus-import metadata row (chosen)

- **Benefits:** represents exactly one current value, is durable in the existing SQLite database, supports a clear read API, and stores no job history or source data.
- **Costs:** requires one additive migration and one transactional update after an import completes.
- **Failure mode:** if the marker write fails, the import cannot be reported as successfully completed even though previously committed corpus chunks remain; the old timestamp remains intact and append retry is safe.
- **Disposition:** chosen as the smallest realization that preserves semantic separation and restart durability.

## Decision

Migration 013 (`internal/store/migrations/013_corpus_import_metadata.up.sql`) adds a one-row `corpus_import_metadata` table in the existing YuHaul SQLite database. The row has the singleton key `singleton_id = 1` and a nullable `last_successful_corpus_import_at TEXT` column. The migration inserts the singleton row with a null value. No historical import time is backfilled: dates in source/video rows do not establish destination-import completion. A null value means that no reliable completion timestamp is recorded; it does not prove that no earlier import occurred. A sentinel epoch date is not used because it would look like a real completion time.

The shared importer updates the single timestamp in a destination-database transaction only after it has scanned and reconciled the entire source, committed all import chunks, obtained the final destination corpus count, and reached the normal successful completion path. It captures the YuHaul wall-clock time in UTC immediately before the marker update and stores canonical RFC 3339 Nano text. The transaction commit is the durable success-marker boundary. The importer reports success, and the web manager reports `completed`, only after this update commits. The CLI path uses the same importer boundary.

The accepted row/error semantics are preserved exactly:

- Any number of inserted rows and/or same-video-ID skips can complete successfully.
- An import with zero inserted rows, including an all-skipped or empty valid source, can complete successfully.
- Explicitly accounted row-level errors do not by themselves prevent successful completion; they remain visible in `errors` and `error_counts_by_reason` and do not imply that all source rows were inserted.
- Preview, pre-acceptance rejection, a cancellation that wins before the marker commits (including graceful shutdown), fatal source/destination errors, or failure to commit the metadata update do not update the timestamp. If cancellation races the marker commit, the commit boundary decides: cancellation before commit rolls back the marker; a completed marker commit makes the cancellation too late and the import successful.
- Previously committed corpus chunks are not rolled back if cancellation/failure occurs. If the marker transaction fails, its update rolls back and the previous value remains; the import fails at the job level and can be retried using append semantics.

`GET /api/corpus/import/metadata` reads this durable value regardless of whether the current process has an active or retained job. It returns the same value after restart. Existing `GET /api/corpus/import` and ID-specific job-status endpoints continue to represent process-local job state and keep their existing post-restart `404` behavior; no durable timestamp is inserted into a job-status record.

## Consequences

- The timestamp describes the current YuHaul database's latest durably marked successful import completion, not a source-file modification time, video publication date, per-video `first_seen_at`, or old source-application run time.
- No raw title, filename, upload, source date, job ID, count history, or import history is stored in the new metadata row.
- Existing databases with prior imported corpus start with a null value after migration. A subsequent successful append import, including a re-import whose rows are all skipped, establishes a reliable timestamp. This is honest rather than fabricating a historical completion date.
- Migration 013 is additive and uses the current database/migration runner; it does not alter `title_fingerprint`, `transient_metadata`, YouTube metadata expiry, or API-data retention policy.
- The new read path is independent of the import manager's in-memory lifecycle. A restart can yield no job status while still returning the last successful-import timestamp.

## Confirmation and reconsideration

- Test a newly completed web import and a CLI import; both must persist the timestamp through the shared importer path and expose it through the API.
- Test that the field is null before any post-migration marker has been recorded and remains so after migration of a database that already contains historical fingerprint rows.
- Test successful imports with inserts, all same-ID skips, zero rows, and explicitly reason-coded row errors; each normal completed import updates the timestamp.
- Test preview, rejected source, cancellation after a committed chunk, fatal source iteration/transaction/commit failure, final corpus-count failure, and metadata-update/commit failure; each leaves the prior timestamp unchanged.
- Test restart with an existing marker: job status is absent under the existing process-local API contract while metadata returns the prior timestamp.
- Inspect persisted metadata and logs to confirm no title, source filename, source date, or job history is added. Confirm the existing API-data retention lifecycle remains unchanged.
- Request an independent architecture/specification critique before treating this extension as implementation-ready.

## Open questions

- None for the storage/API realization. Deployment exposure continues to be governed by ARS-002's separate Traefik/TLS verification prerequisite.
