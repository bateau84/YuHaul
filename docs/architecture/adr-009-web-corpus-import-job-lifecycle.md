---
type: decision
title: ADR-009 — Web Corpus Import Uses an In-Process Async Job
description: Selects the web import lifecycle needed for preview, progress, and cancellation without adding durable job storage or changing append semantics.
tags: [architecture, decision, yuhaul, corpus-import, web-api, sqlite, privacy]
---

**Status:** accepted

In Settings, the user selects the prior application's SQLite corpus, reviews a preview, and confirms an append. The accepted input decision is direct SQLite import; the existing corpus contains 10,000+ titles, and no intermediate export is required. Imports can take long enough to need progress and cancellation. The API therefore accepts the upload, returns an import identifier, and lets the page read progress and request cancellation while reusing the existing migration schema discovery, fingerprinting, and destination data model. Import-job status is temporary process state; imported fingerprints remain in SQLite.

For example, after a valid SQLite file is accepted, the API returns an import ID. Settings reads its processed/total counts and ETA. If the user cancels, the importer stops at a transaction boundary and keeps all rows already committed. A restart may lose the job status, but it does not remove committed fingerprints; the user can submit the same SQLite file again using append semantics.

## Derived from

- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md) §Acceptance Criteria AC9
- [BR-009 — Historical Title Persistence and Corpus Import](../requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md)
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §US-002 and §SC-004
- [Corpus Import — Reconciliation to Direct SQLite Upload](../design/amendment-corpus-import-sqlite-reconciliation.md) (accepted amendment to US-002 and SC-004)
- [YuHaul System Architecture](yuhaul-system-architecture.md) §Migration and §API Surface
- `internal/migration/migration.go` and `cmd/migrate/main.go` (current SQLite import capability)

## Satisfies

- Supports the progress, completion-summary, append, and cancellation portions of the accepted SC-004 amendment, including direct SQLite input, no date-range preview, and no Export step.
- Supports BR-009 AC3–5 by reusing the existing SQLite source-schema discovery, fingerprint conversion, and append/idempotency data model. BR-009 AC6 recency meaning remains unresolved in [ARS-002](ars-002-web-corpus-import-api.md), but does not block the core import contract.
- Supports BR-008 and OC-004 by making newly inserted imported fingerprints exact/fuzzy matching candidates and preserving that eligibility if native discovery writes the same video ID before duplicate evaluation (ADR-013), while preserving BR-010's rule that imported source-channel association and matching-reference identity do not assign discovery credit.
- Preserves the system architecture's two-store rule: raw imported titles are transformed into `title_fingerprint`; raw titles are not inserted into the destination database.

## Decision question

How should the web API expose previewed corpus imports that need progress and cancellation, while preserving append behavior and avoiding unnecessary persistent infrastructure?

## Drivers and constraints

- BR-009 and its accepted reconciliation decision establish direct import of the prior SQLite corpus without a prerequisite export; 10,000+ titles is contextual scale, not a minimum-size threshold.
- The accepted SC-004 amendment requires progress and preserves already-committed rows on cancellation. Its source format is SQLite; it removes the former CSV/JSON, date-range, and Export details.
- `migration.DiscoverSchema` recognizes exactly one source table with `video_id` and `title` columns and an optional `channel_id`; `migration.Importer.Run` fingerprints titles and writes `source='user_import'` rows. The existing CLI importer currently wraps the complete scan in one destination transaction. Its unique destination `video_id` supports same-ID append skips.
- The accepted system architecture and OC-004 require migration-time title fingerprinting, imported-row eligibility in exact/fuzzy duplicate matching, and separation of matching eligibility from route/discovery credit.
- YuHaul is a single-user SQLite application. Import writes share the existing destination database and must not introduce a second durable job store absent a recovery requirement.
- The server closes its database during process shutdown. An import worker therefore needs an explicit shutdown-cancellation path before that close; startup cleanup alone is only crash recovery, not the normal privacy lifecycle.
- The existing server already accepts long-running work asynchronously for runs, but its run IDs and progress events are tied to durable `run` records; corpus import has no such record.
- A web import must accept the source SQLite database directly and preserve the discovered `video_id`/`title` mapping plus optional `channel_id`; no CSV/JSON adapter or intermediate export belongs in this path. The source contains no date field in the existing migration contract, so no date-range preview is promised. ARS-002 specifies a configurable 500 MiB default file cap; non-local exposure remains blocked on verification of the selected Traefik boundary.

## Options considered

### Option A — Single upload request with a streaming response

The import `POST` accepts the upload, then streams progress events on that same HTTP response. The browser can read the stream with `fetch`; aborting the request cancels the server context.

- **Benefits:** no import-job registry, status endpoint, or separate cancellation route. Progress and ETA can be emitted at each committed chunk; the last committed counts remain visible to the UI when it explicitly cancels.
- **Costs:** the upload request stays open for the full import and the browser needs a `fetch` stream reader because native `EventSource` does not submit a multipart `POST`. The stream cannot be reattached after a disconnect, and an aborted stream cannot carry a terminal cancellation summary.
- **Failure modes and risks:** network loss and explicit cancellation are indistinguishable to the server; if the operation is tied to the request, a disconnect implicitly cancels it. If it is not tied to the request, it needs independent operation state and becomes Option B. An intermediary timeout can also make the final state ambiguous.
- **Operational impact:** the current `http.Server` wiring has no explicit write timeout, but deployment intermediaries are not established by source.
- **Obligation impact:** viable for progress and cancellation on a continuously open page; it makes connection loss the operation-lifecycle policy and does not provide a queryable partial result after disconnect.

### Option B — In-process asynchronous job with status polling (chosen)

Accept and validate the upload, assign an ID, run the importer outside the request, keep current status in process memory, and expose status and cancel operations.

- **Benefits:** the upload request returns after acceptance; the UI can report progress, ETA, completion, and cancellation separately. No external queue or durable job schema is needed. The existing fingerprint table's uniqueness makes re-submission safe under append semantics.
- **Costs:** job status is lost on process restart and must not be represented as durable history. A private temporary copy is needed while background work uses the source file. One active import is sufficient for the single-user, single-writer SQLite deployment.
- **Failure modes and risks:** a process crash may leave temporary input behind and the in-memory operation status disappears. Startup cleanup of the dedicated staging area is required; committed fingerprint rows remain durable. A failed or cancelled operation may be partial by design and must report committed counts.
- **Security and privacy impact:** use a generated private temporary path and restrictive permissions; never derive a path from the uploaded filename; remove the source file after preview or job termination and clean abandoned staging files at startup. Do not log titles, file contents, or source paths. API access control remains an unresolved deployment precondition (ARS-002).
- **Obligation impact:** realizes the accepted progress/cancel behavior while keeping persistent data in the existing database.

### Option C — Persist import jobs and source files in SQLite or another durable queue

Store job lifecycle, progress, and recovery state durably, with a retained upload or a durable staged representation.

- **Benefits:** a page or process restart could inspect an operation's history or resume it without a new upload.
- **Costs:** requires new durable schema, recovery semantics, raw-file retention/cleanup policy, and migration/versioning. No accepted requirement asks import jobs to survive restarts or to resume automatically.
- **Failure modes and risks:** retained source titles broaden the sensitive-data lifecycle and backup surface; job recovery introduces additional states and partial-import rules.
- **Obligation impact:** unnecessary machinery for the stated single-user import path; no accepted guarantee is gained that justifies it.

## Comparison

Option A is viable if a dropped connection is intended to cancel the import and the UI may rely on its last committed progress event. Option C adds durable lifecycle and raw-file retention without an accepted recovery requirement. Option B adds a small process-local manager, but separates the accepted operation from its upload connection, supports explicit cancellation and queryable partial status, and avoids whole-job persistence. ARS-002 closes browser navigation recovery by exposing the active or most recent terminal state from the same process; process restart still clears that state.

## Decision

Use one in-process import manager with at most one active corpus import. The upload is a SQLite database file whose schema is discoverable by the existing `migration.DiscoverSchema` rules: exactly one non-internal table must contain `video_id` and `title`; `channel_id` is optional. A valid import request returns an ID and `202 Accepted`; the client polls that ID and may request cancellation. The exact routes and response fields are specified in ARS-002.

The manager keeps the active operation and most recent terminal result in memory; a new import replaces the prior terminal result. `GET /api/corpus/import` exposes the active operation, or otherwise the most recent terminal operation, so Settings can recover status after browser navigation/reload without persisting the import ID in the browser. The import continues after the browser leaves unless explicitly cancelled. The manager does not persist job history, schedule retries, or promise resume after process restart. After restart, job status is unavailable; committed rows remain and the user may select and re-upload the source, relying on the existing video-ID uniqueness rule to skip rows already present.

To preserve SC-004's cancellation behavior, the web import writer must commit in bounded transactions and publish counts only for committed rows. The current CLI `Importer.Run` uses one transaction for the whole source, so this web lifecycle requires a transaction/progress adaptation of the existing migration path. That adaptation MUST preserve the existing source schema discovery, `fingerprint.FromTitle` normalization, destination `title_fingerprint` fields, `source='user_import'`, optional `channel_id` association, and same-`video_id` append semantics; it MUST insert each new valid row with `is_duplicate_reference=true` so the shared exact/fuzzy detector can use it. On a later native-discovery upsert of that same video ID before duplicate evaluation, the shared fingerprint write MUST preserve the existing true candidate flag using the monotonic eligibility merge in ADR-013; source/channel fields do not override eligibility. The flag means matching-candidate eligibility, not route completion or discovery credit; import alone MUST NOT create route/action/discovery-credit evidence. A row already present by video ID on an import retry is skipped without changing its existing state. Cancellation takes effect at a transaction boundary: the current uncommitted transaction rolls back, while prior committed chunks remain. The batch size is an implementation tuning detail; whole-file atomicity is not promised. Database-level failures after earlier commits produce a failed/partial result, not a success response.

The uploaded SQLite database is staged only for the lifetime of preview or the import job, in a private application-owned temporary directory created with mode `0700`; each generated file has mode `0600`. The original filename is display metadata only and MUST NOT influence a filesystem path. The source database is opened read-only. The temporary source is removed on every handled terminal path and abandoned staging files are removed at startup. No raw source contents are written to Store A, Store B, logs, or backups; only the non-reversible fingerprint representation and supported identifiers/metadata enter the destination schema.

On graceful application shutdown, the manager requests cancellation of an active import, waits for its current transaction boundary to finish, and removes its staged file before the destination database is closed. If the process terminates uncleanly, the dedicated staging directory is cleaned during the next startup before accepting imports.

## Consequences

- **Positive:** progress and cancellation are queryable without holding the UI request open for the import duration; append retry uses the existing unique video identity; no new durable queue or job table.
- **Negative:** operation status is process-local; one in-flight import at a time; chunked commits make a cancelled/failed append partial rather than whole-file atomic.
- **Neutral:** imports remain local SQLite work and incur no YouTube API calls or quota cost. The preview may return source row count, same-video-ID matches already present in the corpus, and up to ten raw title samples transiently to the importing UI; the source contract does not provide a date range. The terminal summary reports the actual destination `title_fingerprint` row count, not an arithmetic projection from source rows. Raw titles remain absent from destination persistence. No export operation is part of this import contract.
- **Required precondition:** do not expose the new upload endpoint to untrusted network clients until the selected Traefik BasicAuth boundary is deployed and verified, including TLS and HTTP-to-HTTPS redirect (ADR-010/ARS-003). The application server still has no inbound auth middleware, and direct Service/Pod/port-forward paths bypass the Ingress.

## Confirmation and reconsideration

- Integration proof MUST show a valid prior SQLite corpus produces monotonic committed progress and a terminal summary whose inserted/skipped/error counts reconcile with the processed rows. Include a corpus at the contextual 10,000+ title scale; this is not a minimum-size acceptance threshold.
- The assembled import → native same-ID fingerprint write → detector path MUST verify that new imported rows use `source='user_import'` and `is_duplicate_reference=true` and remain eligible after the ordinary native discovery write with incoming `false`. Evaluating that same video ID MUST return no exact or fuzzy duplicate; evaluating a different video ID with an equal normalized canonical title fingerprint MUST be able to select the imported row as an exact reference before route evaluation. Verify that this eligibility merge does not create route or discovery-credit evidence; same-ID import skips must preserve existing row state.
- Verify `GET /api/corpus/import` recovers the active or recent terminal state after browser navigation/reload within one process. Verify a process restart clears operation state, leaves committed rows intact, and permits an append retry by selecting the source again.
- Cancel after at least one committed chunk; verify prior committed fingerprints remain, the current uncommitted chunk does not, and a repeat append does not duplicate `video_id` rows.
- Force a database failure after an earlier committed chunk; verify the job reports failure and partial counts, retains committed fingerprints, and removes the staged raw file.
- Restart with an abandoned staged file; verify startup cleanup removes it. Verify job state is not represented as durable after restart.
- Trigger graceful server shutdown during an active import; verify the current chunk is resolved, committed rows remain, the staged file is removed, and the database closes only after the worker stops.
- Inspect the destination and logs after success, failure, cancellation, and preview: raw titles, upload content, and client filenames must not be retained or logged. Confirm source schema discovery, imported rows' shared fingerprint normalization, `source='user_import'`, optional channel handling, and existing destination schema.
- Reconsider durable job persistence only if an accepted requirement adds restart-resumable jobs or retained import history. Reconsider this decision if the deployment needs concurrent imports or the SQLite single-writer constraint changes.

## Open questions

- The selected Traefik BasicAuth access-control boundary is carried by the checked-in manifests (Traefik-class Ingress, `yuhaul-auth` Middleware, `yuhaul-tls` reference) but remains undeployed and unverified on a cluster. Do not expose the upload endpoint to untrusted network clients until the security/deployment owner provisions credentials and TLS and verifies the all-path Ingress middleware/redirect (ADR-010/ARS-003).
- BR-009 AC6's recency meaning remains unresolved because the accepted SQLite source contract has no date field. ARS-002 reports no date range; this blocks only a claim that the recency portion is satisfied, not the core import contract.
