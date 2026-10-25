---
type: specification
title: ARS-002 — Web Corpus Import API Contract
description: Specifies direct SQLite import, durable last-successful-import metadata, duplicate-reference eligibility, process-local job status, append progress/cancellation, and configurable upload bounds.
tags: [architecture, specification, api, corpus-import, sqlite, privacy]
---

**Status:** accepted

Settings lets a user select the prior application's SQLite database, review what can be imported, and confirm an append. The accepted source decision is direct import of the existing SQLite corpus (10,000+ titles); no intermediate export is required. The API reports progress and final counts while preserving the existing migration's source schema and destination data model. Newly inserted imported fingerprints are eligible for exact and fuzzy duplicate matching; that eligibility is not evidence of YuHaul routing or discovery credit. For example, a preview reports source row count, same-video-ID matches already present, and up to ten sample titles; after confirmation, the browser receives an import ID and can read progress, recover the current in-process status after navigation, or ask the operation to stop. A process restart loses job status but not committed fingerprints; re-uploading the same source safely skips existing video IDs. A separate metadata read returns the durable completion time of the most recent successful import, including after restart; that YuHaul operation time is not a video publication date and is not part of process-local job status. The source has no date field in the import contract, so preview reports no date range. Raw titles are not retained in the YuHaul database; fingerprints, supported identifiers, and the single import-completion timestamp are.

The file contract is a SQLite database with exactly one discoverable non-internal table containing a video identifier and title; a channel identifier is optional. This matches `migration.DiscoverSchema` and `migration.Importer` rather than adding a CSV/JSON adapter. The accepted [design amendment](../design/amendment-corpus-import-sqlite-reconciliation.md) changes US-002/SC-004 to this direct-SQLite input, removes date-range preview and Export, and removes the optional Append-mode choice. The source is capped at 500 MiB by default, with a server configuration override. Non-local exposure remains conditional on deployment and verification of the selected access-control boundary.

**Implementation status:** implemented in `internal/corpusimport` (process-local `Manager` plus `Handler`) over `internal/migration`'s `Stager`, `Preflight`, `PreviewStaged`, and the bounded `Importer.RunChunked`, mounted on the real API by `internal/server.WithCorpusImportHandler` and wired in `cmd/server/main.go` (`newCorpusImport`, shutdown before the deferred database close). All five endpoints are live with this contract's status classes and exact response shapes: preview stages privately, revalidates read-only, and writes nothing; import enforces the file cap plus 1 MiB request envelope, re-preflights before acceptance, rejects a second start with `409`, and returns `202` with an opaque import ID; status/latest/cancel return the specified states, `404` messages, and `202`/`200` cancel classes; `total` comes from preflight only; terminal states carry the actual destination `COUNT(*)` as `total_corpus_size` (explicit `null` when a failed state cannot read it); error reason counts sum to `errors`. One active job is enforced under concurrent starts, committed chunks survive cancellation and are skipped on append retry, a fresh manager over the same database reports `404` while committed rows persist, startup and handled-terminal-path cleanup remove staged files, graceful shutdown cancels at a transaction boundary and removes the staged source before the database closes, and logs/responses carry no raw titles, upload filenames, or staging paths. Verification executed: `go build ./...`, `go vet ./...`, `gofmt`, `go test -count=1 ./...`, `go test -race -count=1 ./internal/corpusimport ./internal/migration ./internal/server ./cmd/server`, and `go test -tags=integration ./e2e/ -count=1` all pass, including a composed real-`httptest`-server proof at the contextual 12,000-title scale. Not claimed here: cluster deployment of the Traefik BasicAuth/TLS boundary (ADR-010/ARS-003 remains undeployed and unverified — application paths stay unauthenticated when reached directly), and BR-009 AC6's recency portion remains blocked by the open date-meaning question below.

**Implementation status for durable import metadata:** implemented. `internal/store/migrations/013_corpus_import_metadata.up.sql` adds the singleton `corpus_import_metadata` table (`singleton_id = 1` seeded with a NULL `last_successful_corpus_import_at`, no backfill) and applies through `store.Open` before requests are served. The shared importer (`migration.Importer.RunChunked`, which both the web manager and the CLI `Importer.Run` wrapper use) commits the YuHaul UTC RFC 3339 Nano marker in one destination transaction only after the whole source is reconciled, every chunk is committed, and the final destination corpus count is read; a marker write/commit failure is a job-level failure and a cancellation that wins before the marker commit yields `cancelled`, in both cases leaving the prior value unchanged. `GET /api/corpus/import/metadata` is served by `internal/corpusimport` (`Manager.ImportMetadata` over the destination database plus `Handler.handleMetadata`) with the exact status classes and shapes below, independent of the process-local job manager. Verification executed: `go build ./...`, `go vet ./...`, `gofmt`, `go test -count=1 ./...`, `go test -race -count=1 ./internal/corpusimport ./internal/migration ./internal/store ./internal/server ./cmd/server`, and `go test -tags=integration ./e2e/ -count=1` all pass; the `cmd/migrate` binary was also run against a scratch source/destination and recorded the marker. Tests cover the v12→v13 upgrade seeding NULL while preserving every corpus row, marker advancement on inserted/all-skipped/empty/row-error completions through both the chunked path and the CLI `Run` wrapper, no advancement on preview, mid-import cancellation, cancellation after the final committed chunk, fatal source failure, final-count failure, and marker-write failure, restart durability (job status 404 while metadata returns the prior value), the exact wire shape and explicit-null state, the reserved literal route, 500 on a missing/malformed/unreadable marker, and absence of raw titles and upload filenames in the stored value and response.

## Derived from

- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md) §Acceptance Criteria AC9
- [BR-009 — Historical Title Persistence and Corpus Import](../requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md)
- [BR-008 — Exact and Fuzzy Duplicate Detection](../requirements/yuhaul/br-008-exact-and-fuzzy-duplicate-detection.md)
- [BR-010 — Duplicate Provenance for Discovery Credit](../requirements/yuhaul/br-010-duplicate-provenance-for-discovery-credit.md)
- [OC-004 — Imported History as Duplicate References](../requirements/yuhaul/oc-004-imported-history-as-duplicate-reference.md)
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §US-002, §SC-004, and §View: Settings
- [Corpus Import — Reconciliation to Direct SQLite Upload](../design/amendment-corpus-import-sqlite-reconciliation.md)
- [YuHaul System Architecture](yuhaul-system-architecture.md) §Two-Store Data Model and §Migration
- [ADR-009 — Web Corpus Import Job Lifecycle](adr-009-web-corpus-import-job-lifecycle.md)
- [ADR-015 — Persist One Durable Corpus Import Completion Timestamp](adr-015-durable-corpus-import-completion-timestamp.md)
- [ADR-013 — Duplicate-Reference Eligibility Is Monotonic Across Fingerprint Upserts](adr-013-monotonic-duplicate-reference-eligibility.md)
- Current capability evidence: `internal/migration/migration.go`, `internal/migration/preview.go`, `internal/corpusimport/manager.go`, `internal/corpusimport/handler.go`, `cmd/migrate/main.go`, `internal/store/migrations/001_initial_schema.up.sql`, `internal/server/server.go`, and `cmd/server/main.go`

## Satisfies

| Authority | Classification | Notes |
|---|---|---|
| Anchor AC9 | Supported by this API direction | Reuses the existing migration's fingerprint destination and preserves imported historical titles. |
| BR-009 AC3 | Supported by this API direction | Direct bulk import from the prior SQLite database; no intermediate export is required. |
| BR-008 AC5–AC6 | Supported by this contract | Valid newly inserted import rows are eligible in the shared exact/fuzzy candidate set; matches identify the reference video. |
| BR-009 AC4–AC5, AC8 | Supported by this contract | Imported fingerprints are matching-eligible. Import is append-only; source rows reconcile as inserted, same-ID already present/skipped, or explicit reason-coded errors. Existing same-ID rows are not altered; the implemented `RunChunked`/web path exposes stable row-error reason counts whose sum equals `errors`. |
| BR-010 AC2–AC3, AC5–AC6 | Supported by this contract | Imported channel IDs remain associations only; import does not assign discovery credit or imply earliest discovery. |
| OC-004 | Satisfied by this specification | Import, native same-ID persistence, detector, and attribution share matching eligibility semantics. New `user_import` rows explicitly enter eligible; a later `api_discovery` upsert for the same video ID must preserve that eligibility, but the detector excludes that same ID from exact and fuzzy results. The retained reference remains available for distinct video IDs, including exact canonical-title matches. Neither eligibility nor a match assigns discovery credit. |
| BR-009 AC6 | Implemented and verified | Corpus size remains the actual destination count in import completion status. Corpus recency is the durable `last_successful_corpus_import_at` value from `GET /api/corpus/import/metadata`, not a publication date or source-row date. |
| BR-009 AC9 | Implemented and verified | The value survives restart and advances only on a normal successful completion. Row-level errors remain successful when reason-accounted; preview, rejected, cancelled, and failed attempts do not advance it. |
| Accepted US-002 / SC-004 amendment | Supported by this API contract direction | The accepted amendment specifies SQLite input, row count, existing-ID count, title samples, append-only import, progress, and cancellation; it removes date range and Export. |
| Adjacent web API security boundary | Satisfied by selected architecture; deployment verification remains external | Traefik BasicAuth/TLS is the selected ingress boundary (ADR-010/ARS-003). This specification does not claim the required cluster resources are provisioned or verified. |
| YouTube data-retention architecture | Satisfied elsewhere, preserved here | Raw imported title content is transformed and not stored in the destination; existing two-store/fingerprint policy remains authoritative. The compliance analysis document itself is marked proposed. |

## Implements

- [ADR-009 — Web Corpus Import Job Lifecycle](adr-009-web-corpus-import-job-lifecycle.md)
- [ADR-013 — Duplicate-Reference Eligibility Is Monotonic Across Fingerprint Upserts](adr-013-monotonic-duplicate-reference-eligibility.md)

## Scope

This specification defines the HTTP operation lifecycle, privacy boundary, migration behavior, and durable single-value completion metadata to preserve. Input is the prior application's existing SQLite database; no CSV/JSON adapter or intermediate export is included. It does not introduce another database, a new fingerprint algorithm, external API calls, corpus replacement, durable job-state recovery, automatic job resumption, import history, or a corpus export operation. The existing SC-004 Export step is outside this import contract and is not required by the accepted direct-import decision or BR-009. Status recovery after browser navigation is provided only while the same process-local import-manager state exists; only the most recent successful-completion timestamp is durable.

## Structural constraints

- The destination is the existing SQLite database and `title_fingerprint` table. Every successfully inserted import row uses `source='user_import'`, a generated import-batch identifier, the same normalization as native titles, the existing unique `video_id` identity, and `is_duplicate_reference=true`. For a later write conflicting on `video_id`, candidate eligibility is merged as `existing.is_duplicate_reference OR incoming.is_duplicate_reference`; the normal native-discovery incoming `false` cannot demote an existing imported reference. This is the monotonic merge selected by ADR-013.
- `is_duplicate_reference` means eligible as an exact/fuzzy historical matching candidate. It does not mean routed by YuHaul, discovered by an associated channel, or credited as the earliest discovery. Import does not create a `video`, route decision, playlist action, channel discovery counter, or discovery-credit attribution. Optional source `channel_id` is retained only as an association. `first_seen_at`, import order, and import time are not evidence of earliest discovery.
- The uploaded source MUST be a readable SQLite database opened read-only. It MUST contain exactly one non-internal table discoverable by the existing `migration.DiscoverSchema` semantics: a `video_id` column and a `title` column are required; `channel_id` is optional. No source date column is required or interpreted. A source with no matching table or more than one matching table is rejected before destination writes.
- Source row count and preview samples are read from the discovered source table. The preview's existing-ID count is a point-in-time count of source rows whose non-empty `video_id` already exists in the destination. It does not predict source-internal repeated IDs or later corpus changes; final import counts are authoritative.
- Import is append-only. There is no replace, truncate, reset, or destructive import mode in this contract.
- An already-present `video_id` is skipped, matching `migration.Importer`; fuzzy title similarity is not a reason to omit an otherwise new video ID during import.
- No YouTube API calls occur and no quota is charged.
- Import job state is process-local and not a durable import history. At most one import runs at a time. Settings can recover the active or most recent terminal in-process operation through `GET /api/corpus/import` after page navigation/reload. A process restart clears that state; already committed fingerprints remain durable and can be safely encountered again by append import.
- The writer commits bounded chunks so cancellation can preserve previously committed rows. Progress counts describe committed rows, not rows still inside an uncommitted chunk. The numeric chunk size is not part of the public API.
- The CLI `migration.Importer.Run` delegates to `RunChunked`, as does the web manager. Both entrypoints therefore share bounded destination commits, the same source-schema/fingerprint behavior, and same-ID append semantics; the timestamp extension MUST use this common path and must not fork a second data model.
- The web manager and `cmd/migrate` both use the shared `migration.Importer.RunChunked` path (`Importer.Run` delegates to it). Durable completion metadata MUST be updated at this shared successful-completion boundary so either import entrypoint reports the latest successful import. A manager-only update is non-conformant.
- Before import writes begin, the source MUST open read-only, pass schema discovery, and successfully return the source-table row count used as `total`. The import endpoint repeats this preflight rather than trusting a prior preview. Row-level invalid entries increment `errors`, are skipped, and do not cause other valid rows to be discarded. Each such row is accounted for by one explicit reason code in `error_counts_by_reason`. A source-read, transaction, or commit failure during import fails the operation; prior committed chunks remain.
- The server MUST impose a configurable hard maximum SQLite file size of 524,288,000 bytes (500 MiB) by default before retaining upload data. The setting is `YUHAUL_CORPUS_UPLOAD_MAX_BYTES`; an override must parse as a positive integer byte count, and invalid values fail server configuration rather than disabling or silently weakening the cap. The total HTTP request-body limit is the configured file limit plus at most 1 MiB for multipart framing and headers. Over-limit uploads return `413` before import writes. Cluster configuration/provisioning is external to this contract.

## Durable corpus import metadata

The timestamp is YuHaul operational metadata, stored separately from video records and from process-local import-job state. It does not represent a YouTube API field and does not change the API-data retention lifecycle.

### Schema and migration contract

The next schema migration is 013: `internal/store/migrations/013_corpus_import_metadata.up.sql`. It MUST add this table to the existing destination SQLite database and be applied by the current embedded migration runner through `store.Open` before requests are served:

| Column | Type / rule | Meaning |
|---|---|---|
| `corpus_import_metadata.singleton_id` | `INTEGER PRIMARY KEY CHECK (singleton_id = 1)` | The single metadata row; no additional rows are valid. |
| `corpus_import_metadata.last_successful_corpus_import_at` | nullable `TEXT` | Canonical RFC 3339 Nano timestamp in UTC for the most recent import whose success marker committed; `NULL` means no reliable completion timestamp is recorded. |

The migration inserts exactly one row with `singleton_id = 1` and a null timestamp. It is additive to the current version 012 schema and runs through the existing embedded SQLite migration path before API serving. Do not backfill from `published_at`, `title_fingerprint.first_seen_at`, source `videos.timestamp`, `pipeline_runs.finished_at`, file modification time, or import-batch IDs: none identifies when an import into the current destination completed. A pre-migration corpus therefore has a null timestamp until a successful post-migration append import records one. An append re-import may contain only same-ID skips and can establish the first reliable timestamp. Do not use an epoch/sentinel date such as `1971-01-01` to represent missing history; it would be presented as an import time rather than an unknown value.

No title, upload filename, source date, import ID, counts, or per-import record is stored in this table. No extra database, index, or job-history table is required.

### Update and completion boundary

The shared importer MUST update only `last_successful_corpus_import_at` in one transaction on the same destination database, and MUST commit that transaction before returning normal import success. It captures the YuHaul application wall-clock time at finalization as UTC and writes `time.Now().UTC().Format(time.RFC3339Nano)` (or an exactly equivalent canonical RFC 3339 Nano representation). The update MUST affect exactly the singleton row. The marker transaction commit is the durable success boundary; metadata write/commit failure rolls the update back, retains the prior timestamp, and makes the import fail at the job level even if previously committed corpus chunks remain.

The timestamp advances only when the source scan reached its end, each source row was reconciled, all destination chunks committed, the actual destination corpus count was successfully read, and the marker update committed. Row-level invalid/uninsertable records with explicit stable reason codes remain counted by `errors` and `error_counts_by_reason`; under BR-009 AC9 they do not by themselves prevent a normal successful completion or timestamp update. This does not say those rows were inserted. A terminal `cancelled` result or fatal source/destination/finalization failure leaves the old timestamp unchanged, even when earlier chunks remain committed. If cancellation races finalization, marker transaction commit is the outcome boundary: cancellation that takes effect before commit rolls the marker back and results in `cancelled`; if the marker commits first, the import is successful and a later cancellation is too late. Preview and pre-acceptance rejection never update it. A valid empty source and an all-same-ID-skipped source may succeed and update it if the importer reaches normal completion.

The same rule applies to web imports and the CLI migration command through their common importer boundary. The in-process job MUST transition to `completed` only after the marker transaction commits; if the marker write fails, the job is `failed`. A metadata read failure does not erase or replace the last committed value.

## Interface

All paths below are under `/api/corpus`. Multipart endpoints use `multipart/form-data` with exactly one file part named `file`; other parts are rejected. The upload filename is not a path, identity, or trusted format declaration.

### `POST /api/corpus/import/preview`

The browser sends the selected SQLite database file. The server stages it privately, opens it read-only, discovers the source schema, counts source rows, reads up to ten sample titles, counts source rows whose video IDs already exist in the destination as observed during preview, returns the preview, and removes the staged file. Preview performs no destination writes. Preview counts are point-in-time estimates; import revalidates the file and the final committed counts are authoritative if the corpus changes between preview and confirmation.

The response has this shape:

```json
{
  "source_row_count": 12000,
  "already_present_count": 847,
  "sample_titles": ["Example title"]
}
```

`source_row_count` is the number of rows in the single discovered source table, including rows that may later count as row-level errors. `already_present_count` counts source rows whose non-empty `video_id` matches a destination `title_fingerprint.video_id` at preview time; it is not a fuzzy-title duplicate count and does not predict same-ID repeats inside the source file. `sample_titles` contains zero to ten raw titles from the source table; sample order and membership are not guaranteed. The preview has no date/date-range field because the accepted source contract does not supply or define a date.

Preview status classes:

| Result | Status | Observable effect |
|---|---:|---|
| Valid preview | `200` | JSON preview; destination corpus unchanged. |
| Unsupported, malformed, or ambiguous source | `400` | JSON error; no destination writes. |
| Upload exceeds the chosen maximum | `413` | JSON error; no destination writes; temporary data removed. |
| Internal preview failure | `500` | Generic JSON error; no destination writes. |

### `POST /api/corpus/import`

The browser sends the same selected SQLite database again after the user confirms the preview. The server validates that it is readable and has one discoverable table with required columns before creating the operation, stages it in the private import directory, and begins one append-only import.

| Result | Status | Response |
|---|---:|---|
| Import accepted | `202` | `{"import_id":"<opaque-id>","status":"queued"}` |
| Another import is active | `409` | `{"error":"an import is already active"}`; no second import starts. |
| Unsupported, malformed, or ambiguous source | `400` | JSON error; no destination writes. |
| Upload exceeds the chosen maximum | `413` | JSON error; no destination writes; temporary data removed. |
| Internal pre-acceptance failure | `500` | Generic JSON error; no operation is accepted. |

Import is always append-only under BR-009 AC8 and the accepted design amendment. No `mode` parameter is accepted.

### `GET /api/corpus/import/metadata`

This endpoint reads the durable last-successful-import marker described above. It is independent of the process-local job manager and is available after process restart.

```json
{"last_successful_corpus_import_at":"2026-10-24T12:34:56.123456789Z"}
```

The field is a canonical RFC 3339 Nano UTC timestamp or `null` when no reliable completion time has been recorded. `200` is returned for either a valid timestamp or `null`; a missing singleton row, malformed stored timestamp, or database read failure returns `500` with `{"error":"failed to read corpus import metadata"}`. This literal route is reserved and is not interpreted as an import ID.

The endpoint does not expose import history or process-local job status. `GET /api/corpus/import` and `GET /api/corpus/import/{import_id}` remain unchanged and may return `404` after restart even when this metadata endpoint returns the prior successful completion timestamp.

The bundled Settings client MUST read this endpoint independently of the in-process job-status endpoint and present a non-null value as the corpus-import completion time, separately from corpus size. It MUST NOT substitute `published_at`, a source-row timestamp, or an import-job `started` value. `null` means no reliable timestamp is recorded, not proof that no prior import occurred.

### `GET /api/corpus/import/{import_id}`

Returns the in-memory state for a known operation. A new accepted import replaces the prior terminal result; job history is not retained. A process restart also loses this state, so an older ID returns `404` even though its committed fingerprints remain in the database.

```json
{
  "import_id": "<opaque-id>",
  "status": "running",
  "total": 12000,
  "processed": 3200,
  "inserted": 2980,
  "skipped": 190,
  "errors": 30,
  "error_counts_by_reason": {"row_read_failed": 30},
  "rate_per_second": 180.0,
  "estimated_seconds_remaining": 49
}
```

The example is illustrative, not a promised processing rate or a fixed error-reason distribution. `status` is one of `queued`, `running`, `cancel_requested`, `cancelled`, `completed`, or `failed`. Counts are non-negative integers and refer to rows reconciled at committed transaction boundaries. `total` is the number of source rows established during complete preflight. `processed` is the number of source rows whose result is committed/reconciled; `inserted + skipped + errors` MUST equal `processed`. `error_counts_by_reason` is an object from stable reason codes to non-negative integer counts, and the sum of its values MUST equal `errors`; it is `{}` when `errors` is zero. Its allowed keys are `row_read_failed`, `empty_video_id`, `empty_fingerprint`, `destination_lookup_failed`, and `fingerprint_insert_failed`. `rate_per_second` and `estimated_seconds_remaining` are approximate and may be `null` before a rate is available. `completed` and `cancelled` states include `total_corpus_size`, which is the actual `COUNT(*)` of all rows in the destination `title_fingerprint` table after the final committed transaction, not a sum derived from source rows. A `failed` state includes the same actual destination count if it can still be read; otherwise the count is `null`.

| Result | Status | Response |
|---|---:|---|
| Known operation | `200` | JSON state as above. |
| Unknown ID, including a job lost on restart | `404` | `{"error":"import not found"}`. |

### `GET /api/corpus/import`

Returns the active operation if one exists; otherwise returns the most recent terminal operation retained by the process. The Settings view MUST call this endpoint when entered or re-entered so page navigation/reload can recover the operation identifier and current state without browser-stored job state. Recovery does not resume or restart work: an accepted import continues in the process until completion or explicit cancellation, independent of the browser connection.

| Result | Status | Response |
|---|---:|---|
| Active or most recent terminal operation available | `200` | The same JSON state shape as `GET /api/corpus/import/{import_id}`. |
| No operation state in this process, including after restart | `404` | `{"error":"no import state available"}`. |

This endpoint exposes only the current process-local state (one active operation and its most recent terminal result); it does not add durable history. After restart, the UI treats missing state as an unknown prior outcome; if an import may have been interrupted, it explains that some committed rows may remain and asks the user to select and re-upload the same SQLite source to retry. Existing `video_id` rows are skipped on that append retry. It does not claim automatic resume or whole-file rollback.

### `POST /api/corpus/import/{import_id}/cancel`

Requests cancellation of an active operation. It does not delete the operation or any committed fingerprints.

- Active job: `202` and current status `cancel_requested`; the importer stops after the active transaction boundary.
- Repeated cancellation while cancellation is pending: `202` with the current status.
- Terminal job: `200` with its unchanged terminal status and result counters.
- Unknown ID: `404`.
- `cancelled` is terminal. All rows committed before the final cancellation boundary remain; any current uncommitted chunk rolls back. The final status exposes committed `processed`, `inserted`, `skipped`, and `errors` counts.

### Import result semantics

- `inserted` is new `title_fingerprint` rows.
- `skipped` is an existing fingerprint with the same source `video_id`; it is not a fuzzy title match.
- `errors` follows the current importer’s row-level skip count (for example, empty IDs or titles that normalize to no tokens, or a per-row lookup/insert failure). Such rows do not stop other valid rows. The terminal result must show this count and must not present a count with errors as an error-free complete corpus.
- Under BR-009 AC9, these explicitly reason-accounted row errors do not by themselves make a fully scanned import unsuccessful. A `completed` outcome with `errors > 0` advances the completion timestamp, but the timestamp does not claim that those rows were inserted or that the corpus is error-free.
- Every row counted in `errors` MUST have an explicit reason in the result, or be represented by a reason-count breakdown whose sum equals `errors`.
- On database-level failure, status becomes `failed`; the operation reports counts for committed work, the staged source is removed, and no claim of whole-file rollback is made.

## Invariants and lifecycle

1. Preview never mutates the destination.
2. An accepted import is append-only; no existing corpus entry is removed or rewritten.
3. `video_id` uniqueness prevents a repeated append of the same source identifiers from creating duplicate rows.
4. Every newly inserted imported row has `source='user_import'` and `is_duplicate_reference=true`; invalid rows are not inserted, and same-ID import skips do not alter existing rows. A subsequent native same-ID upsert reuses the unique fingerprint row and preserves its true candidate-eligibility flag.
5. Reference eligibility is not route completion or discovery-credit evidence. Import does not create routing outcomes, discovery counters, or credit attribution; an optional source channel ID is an association only.
6. Imported raw titles are transformed with the shared fingerprint implementation and are not persisted in `transient_metadata`, `title_fingerprint`, or another application table.
7. The raw upload is accessible only in the private staging area during preview/import, removed on every handled completion/failure/cancellation path, and cleaned after an unclean process exit on next startup.
8. The import path does not log the file contents, raw title sample, uploaded filename, or local staging path.
9. Status is process-local. `GET /api/corpus/import` recovers the active/most-recent terminal status after navigation only within that process; restart clears job status. Durable import data is the committed fingerprint corpus plus the one latest-success timestamp; there is no durable job state or import history.
10. Graceful process shutdown cancels an active import at a transaction boundary and removes the staged source before the destination database is closed; startup cleanup handles only files left by an unclean exit.
11. The single durable `last_successful_corpus_import_at` marker is written only after normal full-source completion, row accounting, committed chunks, final corpus count, and marker transaction commit. Reason-accounted row errors do not prevent it; preview, rejection, a cancellation that wins before marker commit, fatal failure, or marker-write failure leaves the prior marker unchanged. Marker commit wins a concurrent cancellation that arrives too late; that operation is completed, not cancelled. The metadata endpoint survives restart, while import IDs/progress/terminal job state remain process-local.

## Failure behavior

- Invalid or ambiguous source content is rejected before destination writes.
- Per-row failures are represented by the existing aggregate `errors` count while other valid rows continue, matching current `migration.Importer` behavior. Fatal source iteration or destination transaction/commit errors fail the operation; prior committed chunks remain.
- Cancellation and database-level failure can leave a partial append because committed chunks are intentionally preserved; status counts expose the committed boundary.
- An import can be safely resubmitted in append mode after cancellation, failure, or lost job status. Existing video IDs are skipped; previously skipped/error rows follow their input data and existing importer rules on the new attempt.
- A file exceeding the configured byte limit or a request exceeding the bounded multipart envelope is `413` and must occur before any import write. The default file limit is 524,288,000 bytes (500 MiB), overridden by `YUHAUL_CORPUS_UPLOAD_MAX_BYTES`.
- Error responses must not echo raw file contents, titles, local filesystem paths, SQL statements, or stack traces.
- A failure to read/update/commit the completion marker is a job-level failure, not a row-level import error. Roll back the marker transaction and leave its previous value unchanged; any already-committed corpus chunks remain subject to the existing append-retry behavior.

## Compatibility and evolution

The interface is part of the bundled single-user application and is not a separately versioned third-party API. The input is the existing prior-application SQLite corpus; there is no CSV/JSON compatibility adapter and no inferred `video_id`, title, or publication date. Source schema discovery and destination identity/fingerprint semantics remain those of the existing migration model. Migration 013 adds the one-row metadata table but does not rewrite corpus rows or backfill a completion time. No source-date preview or export behavior may be inferred from this contract.

## Security surface

- Trust boundary: for non-local access, browser → Traefik BasicAuth/TLS Ingress → same-origin HTTP API; upload contents remain untrusted data. Format parsing must be data-only and must not execute uploaded content. Loopback port-forward bypasses the Ingress and is an operator-only trusted path.
- File handling: use generated names under an application-owned directory with mode `0700`; create files with mode `0600`. Ignore the client filename for storage paths; do not trust MIME type or extension; validate actual content using only the owner-approved parser. For a SQLite source, use a read-only connection.
- Privacy: imported API-originated titles retain API-data provenance. Raw source files and the ten-item preview are transient only. Persist only fingerprints and the exact identifiers/optional channel data already supported by the chosen source contract. No quota or external provider request is involved.
- Import-completion metadata: `last_successful_corpus_import_at` is a YuHaul operation timestamp, not API-provided video metadata. The dedicated row stores no raw title, filename, source date, or historical job record; this addition does not extend, shorten, or otherwise alter YouTube data-retention handling.
- Resource controls: enforce the configured 500 MiB-default maximum SQLite file size and a total request-body limit no greater than that setting plus 1 MiB of multipart framing/headers; clean up partial files. Cluster-level resource provisioning remains external.
- Access control: the selected deployment boundary is Traefik BasicAuth on every YuHaul Ingress path (ADR-010/ARS-003), not application middleware. Current `cmd/server/main.go` mounts `/api/` directly and defaults `YUHAUL_HTTP_ADDR` to `:8080`; direct Service/Pod access is unauthenticated. The checked-in Ingress now identifies the Traefik class, attaches the BasicAuth Middleware annotation, and references the `yuhaul-tls` Secret, but the boundary is not yet deployed or verified against a cluster. Do not expose the import endpoint to untrusted networks until the Middleware, external credentials, valid TLS, and HTTPS redirect are deployed and verified. This specification does not claim internet reachability.

## Verification

Tests/proofs must include:

1. API preview accepts a valid SQLite source with exactly one discoverable table; rejects unreadable, ambiguous, or missing-column databases before writes; returns accurate row count, same-ID count, and at most ten sample titles; and leaves `title_fingerprint` unchanged. It returns no date-range field.
2. A full import over the assembled HTTP → migration → SQLite path produces the same fingerprints as `fingerprint.FromTitle`, correct `source='user_import'`/batch/channel fields, `is_duplicate_reference=true`, accurate inserted/skipped/error/reason counts, and `total_corpus_size` equal to the actual destination-table count.
3. The same composed import → native-discovery persistence → detector path first inserts a valid imported `v1` reference, then persists native discovery of `v1` with its normal incoming false value before matching. Verify there is still exactly one eligible `v1` fingerprint row, but evaluating `v1` returns no exact or fuzzy duplicate. Evaluate a different video ID whose title normalizes to the same canonical fingerprint and verify exact detection can select `v1` as its reference, regardless of the fuzzy threshold. With another distinct ID whose title shares known fingerprint tokens, verify the row remains available to the fuzzy candidate query. The eligibility merge itself does not create route/action/credit evidence, and an imported `channel_id` does not by itself establish earliest discovery. An import retry with the same ID leaves the existing row and eligibility unchanged; invalid source rows are excluded from matching.
4. Cancellation after a committed chunk preserves that chunk, rolls back the in-flight chunk, returns terminal partial counters and the actual destination count, and a repeated append creates no duplicate `video_id` rows.
5. Malformed/ambiguous input and an over-limit body are rejected before writes; temporary upload data is removed.
6. Preview, success, invalid input, cancellation, and database failure leave no raw upload/title in destination tables or logs; crash cleanup removes abandoned staging files.
7. Concurrent start is rejected while an import is active. `GET /api/corpus/import` recovers the active/recent state after navigation in the same process; after process restart, status is absent, committed rows remain, and selecting/re-uploading the source safely retries append. Status/cancel behavior for stale and terminal IDs matches the tables above.
8. Composition proof uses a real file selected by the Settings UI and the real HTTP server and destination database, not only mocked API calls.
9. Security verification confirms that deployment exposure is restricted to the intended single-user trust boundary before the endpoint is enabled.
10. Migration 013 on an existing version-012 database creates exactly the singleton metadata row with null timestamp and leaves every corpus row unchanged; no video/source dates are copied into it. A later successful import writes canonical UTC RFC 3339 Nano text, and the value remains readable after closing and reopening the database.
11. The real web HTTP → shared importer → SQLite composition returns the durable value from `GET /api/corpus/import/metadata` after success, including after a process restart that makes job status return `404`. Exercise success with inserts, all rows already present, zero inserted rows, and accounted row errors; exercise preview, rejected source, cancellation before finalization, cancellation racing marker commit, fatal import error, final corpus-count failure, and marker write/commit failure to prove the terminal outcome determines whether the marker advances. Include the CLI `cmd/migrate` wrapper to prove it uses the same successful-completion rule.
12. Validate API wire shapes for timestamp and null; prove the literal `/metadata` route is handled as metadata rather than an import ID; fail the metadata read when the singleton row is absent or malformed; and verify no raw title, filename, source date, import ID, or history was persisted or logged. Confirm `transient_metadata` expiry and all other API-data retention paths are unchanged.

## Open questions

1. **Blocking for untrusted-network exposure — deployment verification:** The selected boundary is Traefik BasicAuth (ADR-010/ARS-003), but required cluster-side Traefik controller/CRD/redirect configuration, credential Secret, and TLS Secret remain deployment-owner prerequisites and are not verified here. Application paths remain unauthenticated when reached directly. **Who:** security/deployment owner. **Blocks:** any deployment where untrusted clients can reach the API until credentials, valid TLS, redirect, and all-path Middleware are installed and verified. This contract does not claim public reachability or authorize unprotected public exposure.
2. ~~**Design owner — null/legacy display wording:** the metadata contract returns `null` when no reliable timestamp is recorded, including on an upgraded database whose old imports have no completion marker. Decide the exact Settings message/visual state without implying that no import ever occurred. **Blocks:** final UI copy/conformance only; does not block schema, API, or data-flow realization.~~ **Resolved.** See [amendment-settings-corpus-import-recency-display.md](../design/amendment-settings-corpus-import-recency-display.md).
