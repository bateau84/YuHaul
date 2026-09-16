---
type: Log
title: Architecture Change Log
description: Chronological record of changes to the docs/architecture/ bundle.
tags: [changelog, log, okf, architecture]
timestamp: 2026-09-08T00:00:00Z
---

# Architecture Update Log

## 2026-09-16

**Update**: `adr-003-route-soft-delete-preserve-history.md` — Amended Realization/Consequences per the architecture-gate critique (CRITICAL-1, MAJOR-1, MINOR-1): replaced the "reserved position" order-space strategy with a sentinel-negative scheme (`DeleteRoute` sets `evaluation_order = -id`), switched `ReorderRoutes` to a shift below the sentinel band, fixed the migration filename to `010_route_soft_delete`, and added an unfiltered `ListRouteNamesIncludingDeleted` for the `routeNameMap` historical-name path.

**Update**: `adr-004-condition-operator-vocabulary.md` — Amended Realization/Consequences per the architecture-gate critique (MAJOR-2): added write-boundary operator validation in `decodeRouteRequest` (`400` on an operator outside the field kind's canonical set, and on a malformed `between`), an engine `default` case that logs a warning for stored drifted operators instead of silently returning `false`, and a lazy (no-migration) policy for existing drifted rows.

**Update**: `adr-005-playlist-listing-endpoint.md` — Amended Realization/Consequences per the architecture-gate critique (MINOR-2): specified the `PlaylistProvider` no-op default returns an error → `502`, so an unwired server fails loud instead of faking the spec's empty state.

## 2026-09-15

**Update**: `yuhaul-system-architecture.md` — Reconciled the Run entity `status` enum to add `failed` as a fourth state (`running | completed | interrupted | failed`), redefined `interrupt_reason` as "the reason the run is not in a normal terminal state" (populated for both `interrupted` and `failed`), split §Run Recovery into resumable `interrupted` vs terminal `failed` recovery, added a failure/panic recovery scenario, and documented the YouTube base-URL seam in the YouTube API Client responsibility.

**Creation**: `adr-001-youtube-provider-seam.md` — Resolves pre-merge MAJOR-1: thread the existing `youtube.WithBaseURL` through the three production client-construction sites behind `YUHAUL_YOUTUBE_BASE_URL` (empty = production default) so Product Acceptance can run the assembled artifact against a fake YouTube. Chose the narrow base-URL seam (Option A) over a full client-factory override to preserve the real OAuth token source and quota recorder on the proof path.

**Creation**: `adr-002-run-status-failed-terminal-state.md` — Resolves pre-merge MAJOR-2: add `RunStatusFailed = "failed"` and route `Pipeline.failRun` and the `Execute` panic-recovery path through a new `Machine.FailRun` so a phase failure is reported `failed`, not `completed`. `failed` is terminal and excluded from interrupted-run resume.

**Update**: `yuhaul-system-architecture.md` — Reflected the five-phase processing pipeline (previously documented as "four phases"). Split the combined "Exclusion Gate + Routing" phase into two distinct phases: Phase 3 (Exclusion — global exclusion gate marking videos excluded by every enabled route) and Phase 4 (Routing — per-route condition evaluation). Renumbered action execution to Phase 5, relocated the configuration snapshot to run initiation (no longer a numbered phase), and updated the mode-variations table and channel-counter phase references to match. Grounded in `internal/pipeline` `Phase` constants: `discovery` → `duplicate_detection` → `exclusion` → `routing` → `action_execution`.

**Creation**: `adr-003-route-soft-delete-preserve-history.md` — Resolves CRITICAL-1: replace the hard `DELETE FROM route` with a soft delete (`route.deleted_at`) and filter soft-deleted routes from management/evaluation queries, so `route_decision`/`condition_audit`/`executed_action` and the historical run counters derived from them survive route deletion (BR-003 AC4; Anchor AC15/25/28). Retains the `route_decision.route_id NOT NULL` FK join to the soft-deleted route name.

**Creation**: `adr-004-condition-operator-vocabulary.md` — Resolves MAJOR-5: adopt the accepted design spec's operator vocabulary as the engine's single canonical set (`contains, equals, starts_with, ends_with, matches_regex` for text; `equals, greater_than, less_than, between` for numerics), retiring the drifted `not_contains/not_equals/in_list/gt/gte/lt/lte`. `between` is encoded `"min,max"` in the single value field; `matches_regex` uses Go RE2 (case-sensitive, `(?i)` available).

**Creation**: `adr-005-playlist-listing-endpoint.md` — Resolves MAJOR-4: add `GET /api/playlists` backed by YouTube `playlists.list` (`mine=true`, 1 unit) behind a `PlaylistProvider` seam, honoring the ADR-001 base-URL seam and quota ledger, so the Route Editor action config gets a real playlist dropdown instead of a free-text ID.

**Update**: `yuhaul-system-architecture.md` — Added `route.deleted_at` (soft-delete) to the Route entity and noted the soft-delete semantics in the API surface (`DELETE /api/routes/:id` soft-deletes; `GET /api/routes`/`/api/routes/:id` exclude deleted; reorder scoped to active routes); added `playlists.list | 1 per call` to the quota model table; added `GET /api/playlists` to the REST endpoint table.

## 2026-09-08

**Update**: `yuhaul-system-architecture.md` — Resolved all 5 open questions. OQ-1: fingerprint algorithm set to normalized-token-set Jaccard (threshold 0.5, user-configurable 0.0–1.0) based on researcher benchmark; added §Similarity Algorithm with benchmark table showing prior combined score missed 11% of true duplicates. OQ-2: discovery changed from search.list/activities.list to playlistItems.list on cached uploads playlists (1 unit/page). OQ-3: metadata refresh resolved as hybrid (routed videos at run time, older on-demand). OQ-4: SPA stack delegated to coder implementation choice. OQ-5: deployment resolved as single container with mounted volume. Updated quota model table, Phase 1 discovery steps, config version JSON example (threshold 0.95→0.5), and Pass 2 fuzzy match algorithm.

**Update**: `yuhaul-system-architecture.md` — Addressed critic review findings (4 MAJOR, 5 MINOR). MAJOR-1: Added replay mode to processing pipeline (distinct from backtest — uses current active config on retained observations). MAJOR-2: Fixed ExecutedAction unique constraint by denormalizing run_id and video_id columns for cross-route idempotency. MAJOR-3: Added Advisory entity with persistence for dismiss/ignore/act user actions and evidence snapshots. MAJOR-4: Extended dry-run endpoint to accept optional draft_configuration body parameter for unsaved route edits. MINOR-1: Extended Run.run_type enum to include 'backtest' and 'replay'. MINOR-2: Standardized video_id types (INTEGER for FK references to Video.id, TEXT for YouTube IDs). MINOR-3: Documented Channel counter update mechanism (atomic pipeline updates + reconcilable aggregates). MINOR-4: Added is_active and un_designated_at to RetentionPlaylist for soft deletion. MINOR-5: Added Phase 3 metadata re-fetch budget check. Added Advisory System section, design decisions D-7 through D-10, and advisory API endpoints.

**Creation**: `yuhaul-youtube-compliance.md` — YouTube API data-retention compliance analysis covering Developer Policies §III.E.4, derived metrics exception, two-store storage model, fingerprint defensibility, and metadata lifecycle design.

**Update**: `yuhaul-system-architecture.md` — Superseded v1 with compliance-informed v2. Key changes: two-store data model (transient API metadata vs permanent YuHaul data), metadata lifecycle manager component, fingerprint-at-discovery pattern, evidence display degradation for expired metadata, updated data model with 18 entities, INV-9 (API metadata compliance). All 36 behavioral requirements traced (v1 covered 31).

**Deletion**: `superseded/yuhaul-system-architecture-v1.md` — Original v1 architecture moved to superseded/ and frontmatter stripped. Superseded by yuhaul-system-architecture.md (v2) which incorporates YouTube compliance analysis.

**Creation**: `yuhaul-system-architecture.md` — Initial system architecture for YuHaul covering component decomposition, data model (17 entities), processing pipeline, quota management, duplicate detection, statistics, configuration versioning, API surface, and deployment model.
