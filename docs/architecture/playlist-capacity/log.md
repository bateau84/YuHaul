---
type: Log
title: Playlist Capacity Change Log
description: Changes to the playlist capacity architecture bundle.
tags: [changelog, architecture, playlist, capacity]
timestamp: 2026-11-22T00:00:00Z
---

# Playlist Capacity Change Log

## 2026-11-22

**User decisions realized; aligned with PR #9 (ARS-001 revision 3).** The requirements on this branch record the user's decisions (`5f5e717` for BR-040 and BR-041, `9e24dac` for OC-007 G2 and the BR-040 AC3 exception). [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) and [ARS-001](/docs/architecture/playlist-capacity/specifications/ars-001-capacity-schema-queue-and-send-path.md) now realize them:

- **Trust the count.** It is the room-bound contract (ARS-001 §4.2). The never-covered alternative is removed.
- **Dismiss on capacity rows.** It is allowed, removes only that add, and is treated like an eviction everywhere: per-target refusal, aggregate, Stop handoff, and re-queue's `RemovedByUser`. The 409 is removed (§5.3).
- **R3.** A definite full refusal after eviction or Dismiss records the add as attempted and not added (`failed / playlist_full`), and releases the guard with no waiting entry (§4.6).
- **Derived AC7 cases.** The aggregate covers them (§6.2). The video reason is renamed `missing_add_removed`, and `capacity_dismissed` is added.
- **PR #9 on `main` (quota stop).** Capacity relies on the per-run quota stop, the 1-unit quota refusal and the Pacific quota day (§4.8). A `quotaExceeded` insert is never a capacity fact.

Not yet rechecked by the critic.

**BR-041 AC7 realized and reconciled with `main` (Plan r30 `cap-ard-update`).** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) replaces the blocked receiving-run case with the evicted-add aggregate. When every other add succeeded and an evicted add remains, the run records `deferred / missing_add_evicted` and a `not_attempted / capacity_evicted` row, creates no queue item, and makes no capacity read for the evicted add. Stop, Discard, the failure sweep and startup recovery apply it locally. B's decision is now derived from successes plus capacity rows, not stored on B's row. Eviction at a fence is a typed refusal that releases the invocation's own unissued guard. The receipt stays at `031` provenance version 1. The stale `028`/`029` migration text is replaced by the Plan `migrations` rule. Interactions with BR-037 and BR-042 are stated. Open question: the outcome after a user discards a capacity row.

**ARS-001 revision 2 after critique `ses_dff707369ffe3UAV2Ns6K4abw0` (CHANGES REQUIRED).** [ARS-001](/docs/architecture/playlist-capacity/specifications/ars-001-capacity-schema-queue-and-send-path.md) now:

- lists every writer that assumed one active row per video. Their sink checks ignore capacity rows, and settlement releases live capacity members unchanged (§3.5);
- checks send permission per target, with a typed per-target eviction refusal, a first-match guard-release table and an `ErrWorkEvicted` rule for every `ErrWorkRemoved` call site (§3.6, §4.7);
- counts each possible add once in the room bounds, excluding the invocation's own guard (§4.2);
- writes the critic's finding 4 as Option A and Option B for the user to select, and specifies the insufficient-evidence outcome under both (§4.2, §4.6);
- removes the simulation read (§7.3) and adds two lookup indexes (§2).

The derived AC7 cases and the R3 guard are flagged for the requirements owner (§12). ARD-001 marks its coverage text as Option A and adds open questions 3 and 4.

**Specification drafted.** [ARS-001](/docs/architecture/playlist-capacity/specifications/ars-001-capacity-schema-queue-and-send-path.md) gives the migration (checked against a `031` schema), queue writers, send path, aggregate, APIs and tests. It was written before the ARD critique and needs its own critique. No implementation.

## 2026-11-19

**Live-status amendment (BR-042).** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) now runs the live gate before the count GET and checks clearance at POST admission. Its video-scoped uniqueness ignores capacity rows ([Live ARS-001 §10](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md)).

## 2026-11-18

**Architecture proposed:** [ARD-001](/docs/architecture/playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md) defines the provider evidence, guarded admission, target-scoped waiting and eviction, and reconciliation with production retry and stranded work. No implementation, live provider test, or deployment.

**Critique reconciliation:** ARD-001 now distinguishes a refreshed count from proof it covers prior successful adds, establishes ordinary sibling authority at the state writer, and fences both capacity GET attempts against Stop. The receiving-run primary outcome when a sibling succeeds after eviction remains a product decision.
