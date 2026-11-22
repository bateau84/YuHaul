---
type: Log
title: Playlist Capacity Change Log
description: Changes to the playlist capacity architecture bundle.
tags: [changelog, architecture, playlist, capacity]
timestamp: 2026-11-22T00:00:00Z
---

# Playlist Capacity Change Log

## 2026-11-22

**BR-041 AC7 realized and reconciled with `main` (Plan r30 `cap-ard-update`).** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) replaces the blocked receiving-run case with the evicted-add aggregate. When every other add succeeded and an evicted add remains, the run records `deferred / missing_add_evicted` and a `not_attempted / capacity_evicted` row, creates no queue item, and makes no capacity read for the evicted add. Stop, Discard, the failure sweep and startup recovery apply it locally. B's decision is now derived from successes plus capacity rows, not stored on B's row. Eviction at a fence is a typed refusal that releases the invocation's own unissued guard. The receipt stays at `031` provenance version 1. The stale `028`/`029` migration text is replaced by the Plan `migrations` rule. Interactions with BR-037 and BR-042 are stated. Open question: the outcome after a user discards a capacity row.

**Specification drafted.** [ARS-001](/docs/architecture/playlist-capacity/specifications/ars-001-capacity-schema-queue-and-send-path.md) gives the migration (checked against a `031` schema), queue writers, send path, aggregate, APIs and tests. It was written before the ARD critique and needs its own critique. No implementation.

## 2026-11-19

**Live-status amendment (BR-042).** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) now runs the live gate before the count GET and checks clearance at POST admission. Its video-scoped uniqueness ignores capacity rows ([Live ARS-001 §10](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md)).

## 2026-11-18

**Architecture proposed:** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) defines the provider evidence, guarded admission, target-scoped waiting and eviction, and reconciliation with production retry and stranded work. No implementation, live provider test, or deployment.

**Critique reconciliation:** ARD-001 now distinguishes a refreshed count from proof it covers prior successful adds, establishes ordinary sibling authority at the state writer, and fences both capacity GET attempts against Stop. The receiving-run primary outcome when a sibling succeeds after eviction remains a product decision.
