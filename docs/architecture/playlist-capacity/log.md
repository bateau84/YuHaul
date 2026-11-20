---
type: Log
title: Playlist Capacity Change Log
description: Changes to the playlist capacity architecture bundle.
tags: [changelog, architecture, playlist, capacity]
timestamp: 2026-11-18T00:00:00Z
---

# Playlist Capacity Change Log

## 2026-11-19

**Live-status amendment (BR-042).** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) now runs the live gate before the count GET and checks clearance at POST admission. Its video-scoped uniqueness ignores capacity rows ([Live ARS-001 §10](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md)).

## 2026-11-18

**Architecture proposed:** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) defines the provider evidence, guarded admission, target-scoped waiting and eviction, and reconciliation with production retry and stranded work. No implementation, live provider test, or deployment.

**Critique reconciliation:** ARD-001 now distinguishes a refreshed count from proof it covers prior successful adds, establishes ordinary sibling authority at the state writer, and fences both capacity GET attempts against Stop. The receiving-run primary outcome when a sibling succeeds after eviction remains a product decision.
