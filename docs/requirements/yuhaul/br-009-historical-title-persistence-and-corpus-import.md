---
type: requirement
title: BR-009 — Historical Title Persistence and Corpus Import
description: Historical titles persisted durably; existing corpus from prior system importable and preservable
tags: [requirement, persistence, titles, corpus, import, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL durably preserve historical title records and support direct import of the prior application's existing SQLite corpus so that duplicate detection can use the full historical record. Importing that corpus does not depend on first exporting it to another file format.

## Acceptance criteria

1. Every video title encountered during discovery is durably persisted with its associated video identifier and channel.
2. Historical titles survive application restarts and are not dependent on transient state.
3. The system supports bulk import directly from the prior application's existing SQLite corpus. The accepted product decision for this reconciliation is that no export from the prior application is needed.
4. Imported titles are treated identically to newly discovered historical records for duplicate detection, under the shared import/reference semantics in [OC-004](oc-004-imported-history-as-duplicate-reference.md).
5. Import does not silently truncate, replace, or discard existing corpus entries. Every source record is accounted for as imported, already present, or not imported for an explicit reason; an unaccounted source record cannot be reported as a fully preserved import.
6. The user can inspect the historical corpus's size and recency as distinct values. Recency is the time the most recent successful corpus import completed; it is not the publication date of videos in the corpus. The direct SQLite source has no publication-date field, so the system does not infer or fabricate a publication date as corpus recency. Where no trustworthy successful-import completion timestamp is available, recency is unavailable rather than an invented timestamp.
7. Each imported record supplies both its title and the source video's identifier; the system does not fabricate a missing title or identifier.
8. Import is append-only. A source record whose video identifier is already present is skipped rather than duplicated or used to alter/remove the existing entry; fuzzy title similarity alone does not make a different video identifier an import duplicate.
9. The timestamp for the most recent successful import remains available after application restart and advances only when a subsequent import completes successfully. A preview, rejected, in-progress, cancelled, or failed attempt does not replace the prior successful-import timestamp. An import counts as successful when it reaches its normal completion outcome with every source row accounted for under AC5; same-ID skips and explicitly reported row-level errors do not by themselves make an otherwise completed import unsuccessful, and an import that inserts no new rows may still be successful.

## Derived from

- Anchor: youtube-discovery-routing.md (AC9, Context: rebuild of existing application with historical data that should be preserved)
- Accepted [ADR-009](../../architecture/adr-009-web-corpus-import-job-lifecycle.md) and the [SC-004 amendment](../../design/amendment-corpus-import-sqlite-reconciliation.md) support append semantics and preservation of already-committed rows on cancellation. The accepted amendment records the user's direct-SQLite/no-export decision; it does not make preview, progress, or export an Anchor AC9 obligation.
- User-approved clarification for this amendment (2026-10-24): corpus recency means the completion time of the most recent successful corpus import, not source-video publication date. This semantic clarification does not specify a storage or API realization.
- Specification choice for "successful": an import qualifies when it reaches normal completion with all source rows accounted for under AC5, rather than requiring every row to be newly inserted or error-free. Thus an all-already-present import and an otherwise completed import with explicitly reported row-level errors qualify; preview, rejection, cancellation, or operation-level failure do not.

## Verified by

- Persistence test: titles survive restart and are queryable.
- Import/detection test: import directly from the prior SQLite corpus and verify the shared behavior in [OC-004](oc-004-imported-history-as-duplicate-reference.md), including exact and fuzzy matching. Confirm a same-video-ID skip leaves the existing entry unchanged (AC8).
- Data integrity test: every source record is reconciled as imported, already present by video identifier, or explicitly not imported; re-importing the same identifiers creates no additional entries and does not remove or alter existing entries.
- With controlled import completion times, verify that the user-visible recency is the completion time of the latest successful import and is presented separately from corpus size. Verify that an all-already-present import can still establish recency, a preview or rejected/failed/cancelled attempt does not advance it, and the value survives application restart. Verify that missing trustworthy timestamp evidence yields an unavailable value, and that no source-video publication date is inferred or fabricated from a SQLite file that has no such field.

## Depends on

- None

## Reconciliation notes

- Anchor AC9 requires historical title persistence and an importable/preservable existing corpus. It does not require a browser file picker, a preview, CSV/JSON, a date-range display, progress/ETA, cancellation controls, or export. Those are design/lifecycle details, not additional Anchor obligations.
- The user-supplied accepted decision for this reconciliation fixes the prior corpus as an existing SQLite database containing 10,000+ titles and settles direct import without a prerequisite export. The count is context, not a minimum-size threshold. The accepted SC-004 amendment is the durable record of that decision, and accepted ARS-002 specifies the corresponding import interface; neither makes the input format an Anchor statement. ARS-002's remaining deployment-verification precondition does not reopen the input-format decision.
- The accepted system-architecture migration description and existing migration capability identify title and video identifier as necessary source data. The existing migration capability also handles a channel identifier when present, but channel data is not required by this BR. Exact table discovery, column aliases, and browser-upload parsing remain architectural/interface details.
- No source-date field is present in the accepted SQLite input contract. SC-004's date-range preview therefore cannot be promised from this source. Corpus recency is separately defined as the completion timestamp of the most recent successful import; this does not turn file metadata or inferred video dates into publication dates. A source-row count and a count of already-present video identifiers can be reported honestly; any duplicate count must mean same-video-ID rows to skip, not fuzzy title matches. A sample of up to ten raw titles is a design-only preview choice, not an Anchor/BR obligation; accepted ADR-009 describes such preview data as transient, not destination persistence.
- The accepted SC-004 amendment supersedes its earlier CSV/JSON, date-range, and Export steps with direct SQLite input and an append-only import. These details remain design choices rather than additional Anchor obligations. Its 12,000-row example totals 847 already-present IDs plus 11,153 inserted rows; ARS-002 separately notes that an actual destination-wide corpus count may be larger if unrelated rows were already present.
- BR-009 AC6's size-and-recency clause is an existing accepted requirement-baseline claim but is not entailed by Anchor AC9. The accepted SC-004 amendment exposes a source-row count in preview and the actual destination corpus count in the completion summary; this is the accepted design's user-visible means to inspect size, not an additional BR-009 interface requirement. The user-approved clarification above defines recency as the most recent successful import's completion time. The direct SQLite source still supplies no video publication date; none is fabricated. Accepted [ADR-015](../../architecture/adr-015-durable-corpus-import-completion-timestamp.md) and [ARS-002](../../architecture/ars-002-web-corpus-import-api.md) now realize the durable value independently of transient job status. The [Settings recency amendment](../../design/amendment-settings-corpus-import-recency-display.md) specifies its timestamp and “Not recorded” presentation; current importer, metadata-read, and Settings source implement these paths. Focused backend timestamp/metadata tests pass, including reopen durability and unsuccessful-attempt preservation (see the 2026-10-25 status-only entry in this bundle's log); this status reconciliation is not a fresh browser, cluster, or Product Acceptance proof. The older corpus-import amendment's stale status wording remains Designer-owned maintenance, not an unresolved recency decision.
- AC4 clarifies an already accepted outcome, not an import feature expansion: valid newly added imported historical records must be usable as duplicate references even though the prior SQLite contract does not establish whether they were routed by YuHaul. The shared importer/detector obligation is detailed in OC-004; imported reference eligibility does not establish discovery-credit provenance (BR-010). A same-video-ID record is still skipped without altering the existing row under AC8.
