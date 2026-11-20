---
type: Log
title: Discovery Source Isolation Change Log
description: Changes to the discovery source isolation architecture bundle.
tags: [changelog, architecture, discovery]
timestamp: 2026-11-18T00:00:00Z
---

# Discovery Source Isolation Change Log

## 2026-11-20

**Review `833a06fb` MAJOR-1 realized in the specification:** [ARS-002 §4.5](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md#45-per-run-channel-scan-outcome-added-2026-11-20-review-833a06fb-major-1) extends unreleased `030` with the nullable `run.discovery_scan_outcome` (`completed`, `cut_short`, `no_source_scanned`, or `NULL` for unknown or legacy). Writes:
- `cut_short` when the uploads scan begins;
- `completed` when the loop ends normally;
- `no_source_scanned` in the same UPDATE as the AC8 terminal failure.

A resumed scan overwrites the value. The run read exposes `discovery_scan` (adds `in_progress`) with a variant table for the design's three coverage wordings. [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) D1 now requires an explicit `isolateSources` flag separate from `byID`. Status lines in ADR-001, ARS-002 and the admin design cite `b8934d6`/`04aee26` with the reviewer's evidence, and leave the SHA for the pending fix as a placeholder. The `030` rollback note is also in the operator [Upgrade notes](/docs/user-docs/upgrade-notes.md).

**ARS-002 accepted by critic; follow-ups applied:** [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md)'s body no longer claims "no migration", "schema unchanged", a ledger-derived note or "no source records". Each point is amended or struck and points to [ARS-002](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md). Budget breaks now read as exiting the channel's page loop, with later channels refused before any request. Release note added: after `030` is applied, a pre-`030` image fails at startup until a pre-`030` backup is restored.

**Source-failure record specified (G1 declined by the user):** [ARS-002](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md) adds migration `030`, an immutable `discovery_source_failure` table. The row is committed at the isolated `playlistNotFound` failure before the next request. A failed insert means the source is not isolated. Same-run resume skips a channel already recorded, and later successes never clear or rewrite rows. Read-only `GET /api/errors/sources` and run-scoped endpoints carry the validated channel ID, current local name and vetted reason. [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) records the user answers (G2 and G3 accepted, G1 declined) and withdraws D6. [ARD-001](/docs/architecture/discovery-source-isolation/design/ard-001-isolate-uploads-source-failures.md) and [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §4–5 now reuse the table. Central migration allocation: `030` goes ahead of receipt provenance; later numbers are assigned at integration, contiguously, with no reservation above `030`. [Run-initiation ARS-005](/docs/architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md) §5.1.1 and §6.1.1 are amended to match. No implementation or runtime proof.

**Interim decided:** [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) records a no-migration slice for the Run 52/53 failure on `debad57`. It isolates only exactly classified `404 playlistNotFound` uploads reads per channel, merges a channel's buffer only on completion, leaves a skipped channel's floor unchanged, derives Run Detail's incomplete-coverage note from the durable ledger, and adds closed code `no_source_scanned` for BR-002 AC8. User-owned gaps G1 (OC-001 Errors entry), G2 (AC9 partial pages) and G3 (channel ID in one log record) are open. ARD-001 and ARS-001 are unchanged and remain the full design. No implementation or runtime proof.

**Corrected after critique (CHANGES REQUIRED):** [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) D1 now isolates only a bare `*APIError` (never any `*ReadError`) with status 404, `Complete`, at least one reason and every reason `playlistNotFound`, with `ctx.Err() == nil`, and only on `RunBound`. D6 states that its ledger premise holds only on the guarded path and accepts the resume over-report. D2 records that budget breaks exit the page loop and later channels are refused before any request. D5 checks `ctx.Err()` before `ErrNoSourceScanned`.

## 2026-11-18

**Proposed:** [ARD-001](/docs/architecture/discovery-source-isolation/design/ard-001-isolate-uploads-source-failures.md) describes the Run 52 correction and downstream proof, including the accepted Errors and all-local-failure behavior. No production operation or implementation.

**Specified:** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) defines the durable page and receiving-queue handoff, the shared metadata precondition and the required restart evidence. No runtime proof was performed.

**Corrected after independent review:** ARD-001 and ARS-001 seal acquisition scope and make terminal retention atomic, align readiness and eligibility fences across receiver/recovery entry points, and specify authorized inspection with vetted reasons. The shared [receiving ARS-005](/docs/architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md) now distinguishes complete responses, typed dispatch stops and local-only claiming. Parent architecture navigation, shared log reconciliation and migration numbering are coordinated by the integrator.

**Targeted re-review:** The shared receiver now preserves authorization deferral for unsent, locally claimed and restored receipts. The user approved Completed with explicit incomplete coverage when the only source becomes ineligible on restart after saving observations. Discovery ARD-001 and ARS-001 specify atomic handoff, no origin routing, and the restart proof. No runtime proof was performed.

**Live-status amendment (BR-042, 2026-11-19).** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §3: readiness adopts `liveGatePass` and `retainNativeTx`, and the prefetch records `live_status` ([Live ARS-001 §10](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md)).
