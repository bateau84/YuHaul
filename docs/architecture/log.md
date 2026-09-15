---
type: Log
title: Architecture Change Log
description: Chronological record of changes to the docs/architecture/ bundle.
tags: [changelog, log, okf, architecture]
timestamp: 2026-09-08T00:00:00Z
---

# Architecture Update Log

## 2026-09-15

**Update**: `yuhaul-system-architecture.md` — Reflected the five-phase processing pipeline (previously documented as "four phases"). Split the combined "Exclusion Gate + Routing" phase into two distinct phases: Phase 3 (Exclusion — global exclusion gate marking videos excluded by every enabled route) and Phase 4 (Routing — per-route condition evaluation). Renumbered action execution to Phase 5, relocated the configuration snapshot to run initiation (no longer a numbered phase), and updated the mode-variations table and channel-counter phase references to match. Grounded in `internal/pipeline` `Phase` constants: `discovery` → `duplicate_detection` → `exclusion` → `routing` → `action_execution`.

## 2026-09-08

**Update**: `yuhaul-system-architecture.md` — Resolved all 5 open questions. OQ-1: fingerprint algorithm set to normalized-token-set Jaccard (threshold 0.5, user-configurable 0.0–1.0) based on researcher benchmark; added §Similarity Algorithm with benchmark table showing prior combined score missed 11% of true duplicates. OQ-2: discovery changed from search.list/activities.list to playlistItems.list on cached uploads playlists (1 unit/page). OQ-3: metadata refresh resolved as hybrid (routed videos at run time, older on-demand). OQ-4: SPA stack delegated to coder implementation choice. OQ-5: deployment resolved as single container with mounted volume. Updated quota model table, Phase 1 discovery steps, config version JSON example (threshold 0.95→0.5), and Pass 2 fuzzy match algorithm.

**Update**: `yuhaul-system-architecture.md` — Addressed critic review findings (4 MAJOR, 5 MINOR). MAJOR-1: Added replay mode to processing pipeline (distinct from backtest — uses current active config on retained observations). MAJOR-2: Fixed ExecutedAction unique constraint by denormalizing run_id and video_id columns for cross-route idempotency. MAJOR-3: Added Advisory entity with persistence for dismiss/ignore/act user actions and evidence snapshots. MAJOR-4: Extended dry-run endpoint to accept optional draft_configuration body parameter for unsaved route edits. MINOR-1: Extended Run.run_type enum to include 'backtest' and 'replay'. MINOR-2: Standardized video_id types (INTEGER for FK references to Video.id, TEXT for YouTube IDs). MINOR-3: Documented Channel counter update mechanism (atomic pipeline updates + reconcilable aggregates). MINOR-4: Added is_active and un_designated_at to RetentionPlaylist for soft deletion. MINOR-5: Added Phase 3 metadata re-fetch budget check. Added Advisory System section, design decisions D-7 through D-10, and advisory API endpoints.

**Creation**: `yuhaul-youtube-compliance.md` — YouTube API data-retention compliance analysis covering Developer Policies §III.E.4, derived metrics exception, two-store storage model, fingerprint defensibility, and metadata lifecycle design.

**Update**: `yuhaul-system-architecture.md` — Superseded v1 with compliance-informed v2. Key changes: two-store data model (transient API metadata vs permanent YuHaul data), metadata lifecycle manager component, fingerprint-at-discovery pattern, evidence display degradation for expired metadata, updated data model with 18 entities, INV-9 (API metadata compliance). All 36 behavioral requirements traced (v1 covered 31).

**Deletion**: `superseded/yuhaul-system-architecture-v1.md` — Original v1 architecture moved to superseded/ and frontmatter stripped. Superseded by yuhaul-system-architecture.md (v2) which incorporates YouTube compliance analysis.

**Creation**: `yuhaul-system-architecture.md` — Initial system architecture for YuHaul covering component decomposition, data model (17 entities), processing pipeline, quota management, duplicate detection, statistics, configuration versioning, API surface, and deployment model.
