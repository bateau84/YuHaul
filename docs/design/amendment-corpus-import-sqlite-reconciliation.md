---
type: design-amendment
title: "Corpus Import — Reconciliation to Direct SQLite Upload"
description: "Amends US-002 and SC-004 to reflect the authoritative product decision: the historical corpus is a SQLite database, imported directly without export or format conversion. Removes unsupported date-range preview and Export step. Corrects completion summary math."
tags: [design-amendment, corpus-import, sqlite, reconciliation]
status: accepted
timestamp: 2026-10-25T00:00:00Z
amends: yuhaul-admin-ui-design.md
---

## Amendment: Corpus Import Reconciliation to Direct SQLite

### Authority

The user has made an authoritative product decision: the historical corpus is a SQLite database containing 10,000+ titles, imported directly into YuHaul without requiring export to CSV/JSON or format conversion. This decision resolves the authority gap documented in `docs/ephemeral-reports/designer/sc-004-corpus-import-reconciliation.md`.

### Problem

The accepted design specified CSV/JSON input, a date-range preview (which the SQLite source schema cannot provide), and an Export step (not required by any accepted authority). The existing migration capability reads SQLite directly. The design could not be implemented as written.

### What the importer can actually read

Source: `internal/migration/migration.go`

- **Input:** SQLite database file
- **Schema discovery:** Requires exactly one table with `video_id` and `title` columns; optional `channel_id` column
- **Returns:** `ImportResult` with `Read` (rows processed), `Inserted` (new fingerprints), `Skipped` (video identifiers already present), `Errors` (rows not processable)
- **Source date field:** Not defined or interpreted by the accepted import contract; no source date range is displayed. This does not preclude separate YuHaul import-completion metadata.
- **Idempotency:** Appends only; re-importing the same video identifiers creates no duplicates

### Design changes

#### US-002: Import Historical Corpus (amended)

**Context (changed):**
- Before: "Existing corpus of 10,000+ titles must be preserved"
- After: "Existing corpus of 10,000+ titles in the prior application's SQLite database must be preserved"

**Trigger (changed):**
- Before: "uploads a CSV or JSON file"
- After: "uploads a SQLite database file"

**Observable success (clarified):**
- Before: "Import summary shows count of new titles added, duplicates skipped, and total corpus size"
- After: "Import summary shows, for this import's source rows, how many were newly inserted, how many video identifiers were already present in the destination (skipped), and how many failed; total corpus size reports the actual destination row count after import"
- Rationale: BR-009 AC5 requires every source record to be accounted for as imported, already present, or not imported for an explicit reason. The summary must report all three source-row reconciliation counts, plus the actual post-import destination total.

#### SC-004: Importing Historical Corpus (amended)

**Context (changed):**
- Before: "has a CSV file with 12,000 historical video titles"
- After: "has a SQLite database file containing 12,000 historical video titles with their video identifiers"

**Step 4 (changed):**
- Before: "User selects the CSV file"
- After: "User selects the SQLite database file"

**Step 5 — Preview (changed):**
- Before: "12,000 titles found. Date range: 2020-01-15 to 2026-09-01. First 10 titles shown for confirmation. 847 titles already exist in corpus (duplicates to skip)."
- After: "12,000 titles found in source database. 847 video identifiers already exist in corpus (will be skipped). First 10 sample titles shown for confirmation."
- Removed: Date range (source schema has no date column; cannot be displayed honestly)
- Clarified: "duplicates to skip" → "video identifiers already exist" (BR-009 AC8: fuzzy title similarity alone does not make a different video identifier an import duplicate)
- Preserved: Up to 10 sample titles (transient preview, not persisted per ADR-009)
- Added: Explicit statement that preview is for confirmation

**Step 6 (removed):**
- Before: "User selects 'Append' mode (default)"
- After: Removed. BR-009 AC8 states import is append-only; there is no mode choice. The user confirms import directly.

**Step 8 — Completion toast (corrected):**
- Before: "Imported 11,153 new historical titles (847 duplicates skipped). Total corpus: 11,153 titles."
- After: "Imported 11,153 new historical titles (847 already present, 0 errors). Total corpus: 12,000 titles."
- Rationale: The original math was inconsistent. If 847 video identifiers were already present and 11,153 were newly inserted, the total corpus must be 847 + 11,153 = 12,000, not 11,153. The amended version reports all three source-row reconciliation counts (inserted, already present, errors) per BR-009 AC5, and reports total corpus size as the actual destination row count after import. In this example the destination held only the 847 already-present rows before import, so total happens to equal inserted + already-present; when other destination rows exist, total may exceed that sum.

**Step 9 (removed):**
- Before: "User clicks 'Export' to verify the corpus can be backed up"
- After: Removed. No accepted authority (BR-009, Anchor AC9, ADR-009) requires export. The corpus is durably persisted in SQLite and can be backed up at the database level. Export behavior (format, scope, privacy) was never specified.

**Edge condition (changed):**
- Before: "Expected CSV with title column, or JSON array of objects with 'title' field"
- After: "Expected SQLite database with exactly one table containing video_id and title columns"
- Rationale: Matches the schema discovery logic in `internal/migration/migration.go` (`ErrSchemaNotDiscoverable`).

**Append/cancellation behavior (preserved):**
- Unchanged: If the user cancels mid-import, already-committed titles are preserved. This matches BR-009 AC8 and ADR-009's transaction-boundary cancellation.

#### Settings view (amended)

**Data region description (changed):**
- Before: "historical corpus import/export, data management"
- After: "historical corpus import, data management"

**Components (changed):**
- Before: "import/export controls"
- After: "import controls"

**Recency (subsequently resolved):**
- BR-009 AC6 and AC9 define recency as the completion time of the most recent successful corpus import, displayed separately from corpus size and retained across restart. It is not a source-video publication date, source-row date, file modification time, or job start time.
- The [Settings recency amendment](amendment-settings-corpus-import-recency-display.md) specifies "Last corpus import" with a local-time timestamp, or "Not recorded" and explanatory helper text when no reliable completion timestamp exists. Null does not imply an empty corpus or prove no earlier import occurred.
- [ADR-015](../architecture/adr-015-durable-corpus-import-completion-timestamp.md) and accepted [ARS-002](../architecture/ars-002-web-corpus-import-api.md) supply the durable value independently of process-local job status. Normal completion may include same-ID skips, zero inserts, and explicitly accounted row-level errors; preview, rejection, in-progress, cancelled, or failed attempts do not replace the last successful value. The completion timestamp does not imply that every source row was inserted or that the corpus is error-free.

### What this amendment does not change

- **BR-009:** Already reconciled; format-neutral; does not require date or export.
- **ADR-009:** Already describes the in-process async job lifecycle for SQLite upload.
- **Append/idempotency behavior:** Preserved. Import remains append-only per BR-009 AC8.
- **Progress and summary:** Preserved. Progress indicator and completion summary remain as specified.
- **Cancellation semantics:** Preserved. Already-committed rows survive cancellation per ADR-009.

### Subsequent decisions and remaining external prerequisite

The original open items below have since been resolved by accepted authority; they are not new design choices in this maintenance update:

1. **API contract:** [ARS-002](../architecture/ars-002-web-corpus-import-api.md) is accepted and specifies direct SQLite multipart upload, preview, start, status, cancellation, and durable metadata reads.
2. **Access control:** Traefik BasicAuth/TLS is the selected ingress boundary under ARS-002 and ADR-010/ARS-003. Deployment-owner verification remains a prerequisite for untrusted-network exposure; direct application access remains unauthenticated. This amendment does not claim cluster deployment or verification.
3. **Maximum upload size:** ARS-002 specifies a 500 MiB default file cap with the `YUHAUL_CORPUS_UPLOAD_MAX_BYTES` server override and bounded multipart overhead.
4. **Browser disconnect behavior:** ARS-002 specifies that accepted imports continue independently of the browser connection. Settings recovers active/recent job status after navigation or reload within the same server process. Restart loses job status, not committed rows or the durable last-successful-import timestamp; unknown prior outcomes are not presented as success or automatic resume, and append retry requires reselecting/re-uploading the source.
5. **Recency meaning and presentation:** Resolved by BR-009 AC6/AC9, ADR-015, ARS-002, and the Settings recency amendment described above. The missing source-date field still rules out a source-date preview; it is not a gap in import-completion recency.

**Source status checked on 2026-10-25:** `internal/migration/migration.go` records the successful-completion marker transactionally; `web/src/views/SettingsView.tsx` independently reads metadata, refreshes it on completed imports, and renders the timestamp/"Not recorded" states adjacent to import controls. It distinguishes a metadata fetch error from null and offers Retry rather than substituting another date. This is source inspection and accepted-authority reconciliation only, not fresh browser, screen-reader, cluster, or Product Acceptance evidence.

### Validation criteria

The implementation passes this amendment when:

1. Settings > Data accepts a SQLite database file upload (not CSV/JSON).
2. Schema validation checks for exactly one table with `video_id` and `title` columns.
3. Preview shows: source row count, count of video identifiers already present, and up to 10 sample titles. Preview does not show a date range.
4. Import proceeds without requiring the user to select an "Append mode" (import is append-only by design).
5. Progress indicator shows processed/total counts and estimated time remaining.
6. Completion summary reports: inserted count, already-present count, error count, and total corpus size. Inserted, already-present, and error counts reconcile the rows in this import's source. Total corpus size is the actual destination row count after import. When no other destination rows existed before import, total equals inserted + already-present; when other destination rows exist, total may exceed inserted + already-present. Total must never be less than inserted + already-present.
7. Cancellation mid-import preserves already-committed titles.
8. Re-importing the same video identifiers creates no duplicates (idempotency).
9. Invalid schema shows the error message: "Expected SQLite database with exactly one table containing video_id and title columns."
10. No Export button or action is present in the import flow.

### Design decisions

**DD-amendment-1: Remove date-range preview rather than fabricate it**
- Decision: The preview does not show a date range because the source schema has no date column.
- Rationale: The accepted source contract establishes no source-date field. Showing a source date range would require fabricating data (e.g., file modification time, inferred from content) or demanding a schema change. Honest preview shows what the importer can read: row count, existing identifiers, sample titles. BR-009 AC6/AC9 separately resolve recency as the most recent successful YuHaul import's completion time; ADR-015 and the Settings recency amendment provide its durable value and display without inventing source dates.
- Alternatives considered: Show file modification time — rejected because it is not user-controlled and may be misleading. Require source to include a date column — rejected because the existing migration capability does not read dates, and the product decision does not require it.

**DD-amendment-2: Remove Export step rather than specify it**
- Decision: The import flow does not include an Export action.
- Rationale: No accepted authority (BR-009, Anchor AC9, ADR-009) requires export. The corpus is durably persisted in SQLite and can be backed up at the database level. Export behavior (format, scope, privacy) was never specified. Adding it now would require product decisions about what to export (all titles? only imported titles? raw titles or fingerprints only?) and how to handle privacy (raw titles are sensitive; fingerprints are non-reversible). The product decision for this reconciliation explicitly states "no export needed."
- Alternatives considered: Specify export as CSV/JSON/SQLite with defined scope and privacy behavior — rejected because it is not required by any accepted authority and the product decision does not authorize it.

**DD-amendment-3: Correct completion summary math**
- Decision: The completion summary reports inserted count, already-present count, error count, and total corpus size. Inserted, already-present, and error counts reconcile the rows in this import's source. Total corpus size is the actual destination row count after import.
- Rationale: BR-009 AC5 requires every source record to be accounted for. The original example was mathematically inconsistent (showed total = inserted, ignoring already-present). The corrected version reconciles source rows and reports the true post-import destination total. In the canonical example (destination held only the 847 already-present rows), total equals inserted + already-present; when other rows exist, total may exceed that sum.
- Alternatives considered: Compute total as (inserted + already-present) always — rejected because that would report a derived figure rather than the actual destination count, hiding any rows that reached the destination by other means. Report total as (prior corpus + inserted) — rejected because "prior corpus" is ambiguous and requires state the importer does not directly observe.

**DD-amendment-4: Clarify example assumption for total corpus size**
- Decision: The SC-004 example (total corpus 12,000 = 11,153 inserted + 847 already present) assumes no other destination rows existed before import. Documentation must state this assumption and clarify that total corpus size is the actual post-import destination row count, which may exceed inserted + already-present when other rows exist.
- Rationale: Without this clarification, implementers and reviewers could read the example as a general formula (total = inserted + already-present) and build incorrect UI math. The destination is append-only and may accumulate rows from prior imports, programmatic insertions, or other sources; the total must reflect what the destination actually contains.
- Scope: This clarification does not re-open the SQLite source decision, the no-export decision, or the BR-009 reconciliation semantics. It only clarifies how to read and produce the total corpus size figure.

### Open questions

None for design. ARS-002's deployment-verification prerequisite remains with the security/deployment owner before untrusted-network exposure; it does not reopen the accepted API, upload cap, browser-disconnect behavior, or recency decisions.
