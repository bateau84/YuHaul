---
type: design
title: YuHaul System Architecture
description: "System architecture for YuHaul — a personal, stateful discovery and routing system for YouTube subscription content with compliance-aware storage, quota management, and decision explainability."
tags: [architecture, design, yuhaul, youtube, routing]
supersedes: superseded/yuhaul-system-architecture-v1.md
---

**Status:** updated

## Log

- 2026-09-08: Resolved all 5 open questions. OQ-1 (fingerprint algorithm): normalized-token-set Jaccard, threshold 0.5, user-configurable 0.0–1.0 — based on researcher benchmark (F1=0.990, zero false positives; prior combined 0.6×Lev+0.4×Jacc at 0.97 threshold missed 11% of true duplicates). OQ-2 (discovery API): playlistItems.list on cached uploads playlists — 1 unit vs 100 units per call. OQ-3 (metadata refresh): hybrid — routed videos refreshed at run time, older on-demand. OQ-4 (SPA stack): implementation choice for coder — anchor imposes no framework requirement. OQ-5 (deployment): single container with mounted volume — SQLite file-based, minimal infrastructure.
- 2026-09-08: Architecture-design spec alignment check. Fixed 7 gaps: clarified RunStats/RouteStats as computed views (not missing entities), added quota remaining/reset info section, added replay config_version_id parameter, added config diff endpoint, added config restore endpoint, added account disconnect endpoint, added duplicate provenance endpoints. All 36 requirements now fully aligned with architecture and design spec.
- 2026-09-08: Addressed critic review findings (4 MAJOR, 5 MINOR). Added replay mode, fixed ExecutedAction constraint, added Advisory entity, extended dry-run for draft configs. See changelog entries below.

## Satisfies

All 36 behavioral requirements across 13 domains (see [requirement index](../requirements/yuhaul/index.md)).

- [Account Connection](../requirements/yuhaul/account-connection.md)
- [Account Connection Security](../requirements/yuhaul/account-connection-security.md)
- [Subscription Discovery](../requirements/yuhaul/subscription-discovery.md)
- [Route Definition](../requirements/yuhaul/route-definition.md)
- [Route Conditions and Actions](../requirements/yuhaul/route-conditions-actions.md)
- [Condition Composition](../requirements/yuhaul/condition-composition.md)
- [Multi-Route Matching](../requirements/yuhaul/multi-route-matching.md)
- [Final Route](../requirements/yuhaul/final-route.md)
- [Exclusion Behaviors](../requirements/yuhaul/exclusion-behaviors.md)
- [Duplicate Detection](../requirements/yuhaul/duplicate-detection.md)
- [Historical Title Persistence](../requirements/yuhaul/historical-title-persistence.md)
- [Duplicate Provenance](../requirements/yuhaul/duplicate-provenance.md)
- [Processing State Persistence](../requirements/yuhaul/processing-state-persistence.md)
- [Idempotent Execution](../requirements/yuhaul/idempotent-execution.md)
- [Quota Deferral](../requirements/yuhaul/quota-deferral.md)
- [Quota Accounting](../requirements/yuhaul/quota-accounting.md)
- [Quota-Aware Processing](../requirements/yuhaul/quota-aware-processing.md)
- [Decision Evidence](../requirements/yuhaul/decision-evidence.md)
- [Scheduled Runs](../requirements/yuhaul/scheduled-runs.md)
- [Manual Runs](../requirements/yuhaul/manual-runs.md)
- [Error Queue](../requirements/yuhaul/error-queue.md)
- [Failure Retention](../requirements/yuhaul/failure-retention.md)
- [Run-Level and Route-Level Statistics](../requirements/yuhaul/run-route-statistics.md)
- [Source/Channel Statistics](../requirements/yuhaul/source-channel-statistics.md)
- [Retention/Value Signals](../requirements/yuhaul/retention-signals.md)
- [Retention-Signal Configuration](../requirements/yuhaul/retention-signal-configuration.md)
- [Temporal Statistics](../requirements/yuhaul/temporal-statistics.md)
- [Trend Visualization](../requirements/yuhaul/trend-visualization.md)
- [Configuration History](../requirements/yuhaul/configuration-history.md)
- [Historical Re-evaluation](../requirements/yuhaul/historical-reevaluation.md)
- [Dry-Run Evaluation](../requirements/yuhaul/dry-run.md)
- [Historical Backtesting](../requirements/yuhaul/backtesting.md)
- [Run Inspection](../requirements/yuhaul/run-inspection.md)
- [Responsive Administration](../requirements/yuhaul/responsive-administration.md)
- [User Authority](../requirements/yuhaul/user-authority.md)
- [Outcome Distinction](../requirements/yuhaul/outcome-distinction.md)

## Traceability

| Requirement | Status | Notes |
|---|---|---|
| Account Connection | Satisfied | §OAuth and Credential Storage — OAuth2 flow, token persistence |
| Account Connection Security | Satisfied | §Security Threat Model — token encryption, access scoping |
| Subscription Discovery | Satisfied | §Discovery Subsystem — polling-based channel feed retrieval |
| Route Definition | Satisfied | §Data Model — Route entity, §Processing Pipeline — Router |
| Route Conditions and Actions | Satisfied | §Data Model — Condition/Action config, §Router Engine |
| Condition Composition | Satisfied | §Router Engine — AND/OR evaluation semantics |
| Multi-Route Matching | Satisfied | §Router Engine — sequential evaluation until final-route termination |
| Final Route | Satisfied | §Router Engine — final-route termination, subsequent routes marked skipped |
| Exclusion Behaviors | Satisfied | §Router Engine — route-scoped pre-evaluation exclusion gate |
| Duplicate Detection | Satisfied | §Duplicate Detection Engine — exact (video_id) + fuzzy (fingerprint similarity) |
| Historical Title Persistence | Satisfied | §Two-Store Data Model — Store B permanent corpus, §Migration |
| Duplicate Provenance | Satisfied | §Data Model — DuplicateMatch entity with source_route_id provenance |
| Processing State Persistence | Satisfied | §State Management — durable three-tier outcome records |
| Idempotent Execution | Satisfied | §State Management — ExecutedAction unique constraint |
| Quota Deferral | Satisfied | §Quota Management — deferral queue, no-false-rejection invariant |
| Quota Accounting | Satisfied | §Quota Management — QuotaLedger entity, per-operation cost tracking |
| Quota-Aware Processing | Satisfied | §Quota Management — pre-flight budget check, priority-aware allocation |
| Decision Evidence | Satisfied | §Decision Evidence — ConditionAudit records, per-route trail |
| Scheduled Runs | Satisfied | §Scheduling and Recovery — internal cron scheduler |
| Manual Runs | Satisfied | §API Surface — POST /runs endpoint |
| Error Queue | Satisfied | §State Management — ErrorEntry entity with retry/dismiss |
| Failure Retention | Satisfied | §State Management — error categorization, quota-exhaustion exclusion |
| Run/Route Statistics | Satisfied | §Statistics — RunStats, RouteStats entities with rejection-by-reason |
| Source/Channel Statistics | Satisfied | §Statistics — ChannelStats entity, yield + usefulness metrics |
| Retention/Value Signals | Satisfied | §Statistics — RetentionSignal entity, designated-playlist scoping |
| Retention-Signal Configuration | Satisfied | §Data Model — RetentionPlaylist entity, user designation |
| Temporal Statistics | Satisfied | §Statistics — time-bucketed materialization, per-run granularity |
| Trend Visualization | Satisfied | §Web Interface — chart endpoints, time-series data API |
| Configuration History | Satisfied | §Configuration Versioning — ConfigVersion snapshots linked to runs |
| Historical Re-evaluation | Satisfied | §Processing Pipeline — replay (current config) and backtest (proposed config), both user-triggered, no silent reprocessing |
| Dry-Run Evaluation | Satisfied | §Processing Pipeline — dry-run mode, action suppression, state isolation |
| Historical Backtesting | Satisfied | §Backtesting Engine — replay against retained observations, state isolation |
| Run Inspection | Satisfied | §API Surface — run detail + decision detail endpoints |
| Responsive Administration | Satisfied | §Web Interface — SPA with responsive layout per designer spec |
| User Authority | Satisfied | §Advisory System — Advisory entity with dismiss/ignore/act persistence, evidence-backed, no autonomous policy changes |
| Outcome Distinction | Satisfied | §Data Model — three-tier outcome model, not-yet-evaluated exclusion rule |

## Problem

YuHaul is a personal tool that discovers videos from a user's YouTube subscriptions, evaluates them against configurable routing rules, adds qualifying videos to playlists, and measures channel/route effectiveness. It is a rebuild of an existing application with a corpus of 10,000+ historical titles that must be preserved. The new system must support high-volume fuzzy duplicate detection (primary use case: music discovery where the same song appears across many channels under slightly varying titles), manage YouTube's strict API quota (10,000 units/day), provide full decision explainability, comply with YouTube's 30-day metadata retention policy, and deliver temporal analytics — all within a single-user, self-hosted deployment.

Two architectural tensions dominate:

1. **Quota as first-class constraint.** YouTube's quota model makes the external API a scarce, metered resource. Every subsystem that touches the YouTube API must be aware of remaining budget and able to defer work without data loss.

2. **Metadata retention compliance.** YouTube's Developer Policies (§III.E.4) require most stored API metadata to be deleted or refreshed after 30 calendar days. YuHaul's core value depends on long-term title storage for duplicate detection. The storage model must cleanly separate API-sourced metadata (transient) from YuHaul-owned operational data (permanent). See [YouTube Compliance Analysis](yuhaul-youtube-compliance.md) for the full policy analysis.

## Constraints

- **Single-user personal tool.** No multi-tenancy, no shared state, no concurrent users. The system serves exactly one YouTube account.
- **YouTube Data API v3 quota.** Default 10,000 units/day. Varying costs per method (e.g., `search.list` = 100 units, `videos.list` = 1 unit, `playlistItems.insert` = 50 units). Quota resets per provider-documented schedule.
- **YouTube Developer Policies §III.E.4.** API metadata (titles, descriptions, channel names) subject to 30-day refresh/deletion. Statistics (view/like/subscriber counts) may be retained longer with periodic reauthorization.
- **Historical data migration.** Existing application has a corpus of 10,000+ titles that must be importable. Old corpus provenance must be preserved: API-originated historical titles are not assumed to lose API-Data status merely because they are migrated. During migration, YuHaul transforms them into the user-approved non-reversible duplicate fingerprint representation and need not retain the raw title permanently unless another lawful provenance permits it.
- **Polling, not streaming.** Discovery operates on a scheduled/on-demand basis, not continuous monitoring.
- **No autonomous policy changes.** System advises; user decides. No auto-unsubscribe, no rule optimization, no ML classification.
- **Responsive web interface.** Desktop and mobile. SPA with URL state per designer spec.
- **Deployment.** Single-user self-hosted, likely Docker container. Minimal infrastructure dependencies.

## Invariants & Guarantees

### INV-1: No Silent Data Loss

**Guarantee:** No eligible video is silently dropped from processing. Every discovered video reaches one of five terminal states: accepted, rejected, deferred, failed, or not-yet-evaluated.

**Precondition:** The discovery subsystem persists discovered video identifiers before any downstream processing begins.

**Component:** Discovery subsystem → Video entity (discovered_at, processing_state=not_yet_evaluated).

**Falsification test:** After a run completes, count(discovered videos) = count(accepted) + count(rejected) + count(deferred) + count(failed) + count(not_yet_evaluated). Any gap is a violation.

### INV-2: Quota Exhaustion Is Operational State, Not Error

**Guarantee:** Quota exhaustion never produces a false rejection. Videos not processed due to quota are deferred, preserving their eligibility for the next run.

**Precondition:** The quota ledger accurately tracks remaining budget before each API call.

**Component:** QuotaLedger entity → pre-flight budget check in Processing Pipeline.

**Falsification test:** Exhaust quota mid-run; verify deferred videos appear in next run's processing queue, not in the rejected set.

### INV-3: Idempotent Playlist Actions

**Guarantee:** Re-executing a run or reprocessing a video does not duplicate playlist insertions.

**Precondition:** Action completion is recorded with (run_id, video_id, playlist_id, action_type) identity directly on the ExecutedAction entity.

**Component:** ExecutedAction entity with denormalized run_id and video_id columns, unique constraint on (run_id, video_id, target_playlist_id, action_type).

**Falsification test:** Execute a run, then re-execute the same run. Verify playlist item count is unchanged.

### INV-4: Decision Evidence Completeness

**Guarantee:** Every routing outcome is explainable from stored evidence without external log analysis.

**Precondition:** The router records condition evaluation results for every route evaluated against every video.

**Component:** ConditionAudit entity → per-route condition audit trail.

**Falsification test:** For any video outcome in any historical run, the user can trace the exact route evaluation sequence and condition results that produced the outcome.

### INV-5: Configuration-Statistics Attribution

**Guarantee:** Every run's statistics are interpretable in the context of the configuration version that was active during that run.

**Precondition:** A configuration snapshot is created whenever routes change, and each run records its active config version.

**Component:** ConfigVersion entity → Run.config_version_id foreign key.

**Falsification test:** Modify routes, run, then inspect the historical run — the route conditions shown must match the configuration at the time of the run, not the current configuration.

### INV-6: Historical Corpus Integrity

**Guarantee:** Imported historical titles produce identical duplicate detection results as natively captured titles. The corpus is a first-class data citizen.

**Precondition:** The fingerprint corpus schema is identical for imported and native entries.

**Component:** TitleFingerprint entity — no schema distinction between import sources.

**Falsification test:** Import historical titles, run duplicate detection against a known duplicate — result must match expected similarity score.

### INV-7: Distinct Outcome States (AC 32)

**Guarantee:** The system distinguishes three outcome tiers and never represents absence of processing as a negative routing decision.

**Precondition:** Video processing state is set to not-yet-evaluated at discovery time and only transitions through explicit processing.

**Component:** Three-tier model: (a) VideoProcessingState, (b) RouteDecision, (c) ActionOutcome.

**Falsification test:** A video that was never evaluated (discovered but quota exhausted before processing) shows as not-yet-evaluated, not rejected. A video skipped by a final-route match shows as skipped at the route level, not not-yet-evaluated at the video level.

### INV-8: Dry-Run, Replay, and Backtest Isolation

**Guarantee:** Dry-run, replay, and backtest modes produce routing evaluations without mutating external state (YouTube playlists, subscriptions), production duplicate corpus, production idempotency records, or production statistics.

**Precondition:** Action execution is gated by a mode flag that suppresses external API calls and state writes.

**Component:** Run mode enum {production, dry_run, replay, backtest} → action executor gate, state write gate.

**Falsification test:** Run a dry-run, then verify: (a) no playlist changes on YouTube, (b) production duplicate corpus unchanged, (c) production statistics unchanged, (d) no videos marked as processed.

### INV-9: API Metadata Compliance

**Guarantee:** No raw YouTube API metadata (titles, descriptions, channel names) is retained beyond 30 calendar days without refresh, except where the data falls within the statistics exemption. API-originated historical titles imported during migration are transformed to fingerprints (permanent, Store B) and raw titles are discarded after successful transformation.

**Precondition:** Every API-sourced metadata record carries a fetched_at timestamp and is subject to the metadata lifecycle process.

**Component:** TransientMetadata store → metadata lifecycle manager (refresh/expire).

**Falsification test:** Query the transient metadata store for records with fetched_at > 30 days ago that have not been refreshed. Count must be zero.

## Options Considered

### Option A: Compliance-Aware Two-Store Architecture (Recommended)

**Approach:** Separate storage into two logical stores:

- **Store A (Transient):** Raw API metadata with 30-day TTL. Refreshed on demand or on schedule.
- **Store B (Permanent):** YuHaul-owned data — fingerprints (including those computed from migrated API-originated titles), outcomes, evidence, statistics, configuration history.

Duplicate detection operates on fingerprints computed at discovery time. Raw titles are cached transiently for display but are not the matching substrate.

**Pros:**
- Compliant with YouTube Developer Policies under both strict and functional readings
- Preserves all anchor behaviors — duplicate detection, evidence, backtesting all work on Store B data
- Clear audit boundary — compliance review examines Store A only
- Migrated corpus fingerprints are permanently available (raw API titles transformed and discarded during migration)
- Quota-cheap metadata refresh (2–40 units per refresh cycle for typical corpus sizes)

**Cons:**
- Historical video titles degrade in the UI after 30 days unless refreshed
- Added system complexity from two-store coordination
- Evidence display degrades for old decisions (fingerprint-only unless metadata refreshed)

**Risks:**
- Fingerprint storage may be challenged under strict reading of §III.E.4.h (low enforcement risk for single-user tool)
- Metadata refresh adds quota expenditure (manageable at <0.5% of daily budget)

### Option B: Full Metadata Retention (Non-Compliant)

**Approach:** Store all API metadata permanently, ignoring the 30-day policy.

**Pros:**
- Simplest implementation — single store, no lifecycle management
- Full title display at all times

**Cons:**
- Violates YouTube Developer Policies §III.E.4.c
- Risk of API credential revocation or quota reduction
- No audit defense

**Risks:**
- **Disqualifying:** Policy violation. YouTube actively enforces via compliance audits and can revoke API access.

### Option C: API-Free Discovery (Avoidance)

**Approach:** Avoid the YouTube Data API entirely for metadata retrieval. Use RSS feeds, channel page scraping, or other non-API sources.

**Pros:**
- No API retention policies apply to non-API data

**Cons:**
- **Disqualifying:** Violates Developer Policies §III.D.7 (prohibited scraping) and §III.E.1.f.6 (scraping prohibition)
- Loses all authenticated API functionality (playlist management, subscription listing, retention signals)
- RSS feeds provide limited metadata and no write operations

**Risks:**
- Terms of service violation, unreliable data, no write operations

### Single-Option Clause

Options B and C are disqualified by policy violations. Option A is the only viable approach. The analysis above documents why the alternatives fail rather than presenting them as genuine choices.

## Recommendation

**Option A: Compliance-Aware Two-Store Architecture.**

The two-store model is the only approach that satisfies both the anchor's behavioral requirements and YouTube's Developer Policies. The key design decisions are:

1. **Fingerprint-at-discovery:** Compute a normalized title fingerprint immediately upon API retrieval, store in Store B (permanent), then allow the raw title to follow Store A's 30-day lifecycle.
2. **Fingerprint as matching substrate:** All duplicate detection (exact and fuzzy) operates on fingerprints and video IDs, never on raw titles.
3. **Metadata refresh on demand:** Raw metadata is refreshed when the user views historical evidence or when retention signal monitoring requires current data.
4. **Migration-time fingerprint transformation:** The 10,000+ historical titles imported from the prior installation retain their API-Data provenance. During migration, raw titles are read from the source SQLite database, transformed into fingerprints (the user-approved non-reversible representation), and stored permanently in Store B. Raw titles may be discarded after successful fingerprint generation, unless another lawful provenance permits their retention.

## Component Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                         YuHaul System                           │
│                                                                 │
│  ┌──────────┐  ┌───────────┐  ┌────────────┐  ┌─────────────┐ │
│  │ Scheduler│  │ API Server│  │ Discovery  │  │  Metadata   │ │
│  │ (cron)   │  │ (REST+SSE)│  │ Engine     │  │  Lifecycle  │ │
│  └────┬─────┘  └─────┬─────┘  └─────┬──────┘  └──────┬──────┘ │
│       │              │              │                  │         │
│       ▼              ▼              ▼                  ▼         │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │                  Processing Pipeline                        │ │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐ │ │
│  │  │ Discovery│→│Duplicate │→│  Router  │→│   Action     │ │ │
│  │  │ Phase    │ │ Detection│ │  Engine  │ │   Executor   │ │ │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────────┘ │ │
│  └────────────────────────────────────────────────────────────┘ │
│       │              │              │                  │         │
│       ▼              ▼              ▼                  ▼         │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │                    Storage Layer                            │ │
│  │  ┌─────────────┐  ┌──────────────┐  ┌──────────────────┐  │ │
│  │  │  Store A    │  │   Store B    │  │  Quota Ledger    │  │ │
│  │  │  (Transient │  │  (Permanent  │  │  (Daily Budget)  │  │ │
│  │  │   API Meta) │  │   YuHaul     │  │                  │  │ │
│  │  │             │  │   Data)      │  │                  │  │ │
│  │  └─────────────┘  └──────────────┘  └──────────────────┘  │ │
│  └────────────────────────────────────────────────────────────┘ │
│       │              │              │                  │         │
│       ▼              ▼              ▼                  ▼         │
│  ┌──────────┐  ┌───────────┐  ┌──────────┐  ┌───────────────┐ │
│  │ Statistics│  │ Backtest  │  │ Config   │  │   YouTube     │ │
│  │ Engine    │  │ Engine    │  │ Versioner│  │   API Client  │ │
│  └──────────┘  └───────────┘  └──────────┘  └───────────────┘ │
└─────────────────────────────────────────────────────────────────┘
          │                    │                    │
          ▼                    ▼                    ▼
    ┌──────────┐        ┌──────────┐        ┌──────────────┐
    │ SQLite   │        │ Web SPA  │        │ YouTube      │
    │ Database │        │ (Admin)  │        │ Data API v3  │
    └──────────┘        └──────────┘        └──────────────┘
```

### Component Responsibilities

| Component | Responsibility |
|---|---|
| **Scheduler** | Internal cron; triggers scheduled runs at user-configured intervals; manages run lifecycle |
| **API Server** | REST endpoints + SSE for real-time run progress; serves web SPA; authentication middleware |
| **Discovery Engine** | Retrieves subscription list and recent video feeds from YouTube API; persists discovered videos |
| **Metadata Lifecycle** | Manages Store A TTL; refreshes active metadata; expires stale records; provides metadata-on-demand for evidence display |
| **Processing Pipeline** | Orchestrates the four-phase processing flow: discovery → duplicate detection → routing → action execution |
| **Duplicate Detection Engine** | Exact match (video_id) + fuzzy match (fingerprint similarity); operates against Store B corpus |
| **Router Engine** | Evaluates videos against ordered routes with conditions/actions; records decision evidence; respects final-route termination |
| **Action Executor** | Executes playlist insertions via YouTube API; idempotency guards; handles failures and deferrals |
| **Statistics Engine** | Computes run/route/channel statistics; materializes time-bucketed aggregates; retention signal monitoring |
| **Backtest Engine** | Offline evaluation against retained observations; state isolation from production |
| **Configuration Versioner** | Snapshots route configuration on change; links runs to active config version |
| **Quota Ledger** | Tracks per-operation API costs; pre-flight budget checks; remaining capacity reporting |
| **YouTube API Client** | Thin wrapper around YouTube Data API v3; handles auth, retries, rate limiting, quota error detection |
| **Storage Layer** | SQLite with FTS5 for fingerprint search; two-store separation enforced at schema level |
| **Web SPA** | Single-page application matching designer spec; 9 views, 8 key flows |

## Two-Store Data Model

The data model enforces compliance at the schema level. Store A and Store B are separate tables with different retention semantics, linked by video_id.

### Store A: Transient API Metadata

```
┌──────────────────────────────────────┐
│ TransientMetadata                    │
│──────────────────────────────────────│
│ video_id TEXT PRIMARY KEY            │
│ title TEXT                          │  ← raw title from API
│ channel_name TEXT                   │  ← raw channel name
│ description TEXT                    │  ← raw description (truncated)
│ published_at TIMESTAMP              │
│ duration_seconds INTEGER            │
│ thumbnail_url TEXT                  │
│ fetched_at TIMESTAMP NOT NULL       │  ← TTL clock start
│ refreshed_at TIMESTAMP              │  ← last refresh time
│ expires_at TIMESTAMP NOT NULL       │  ← fetched_at + 30 days
│ is_active BOOLEAN DEFAULT true      │  ← eligible for refresh
└──────────────────────────────────────┘
```

**Lifecycle:** Records are created at discovery time. `expires_at` is set to `fetched_at + 30 days`. The metadata lifecycle process refreshes active records before expiry and expires inactive records. Expired records are either deleted or flagged as `metadata_expired` (title set to NULL, fingerprint reference preserved in Store B).

### Store B: Permanent YuHaul Data

```
┌──────────────────────────────────────┐
│ TitleFingerprint                     │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id TEXT NOT NULL UNIQUE        │  ← links to TransientMetadata
│ fingerprint TEXT NOT NULL            │  ← normalized, lossy hash
│ fingerprint_tokens TEXT             │  ← FTS5-indexed token set
│ channel_id TEXT                      │
│ source TEXT NOT NULL                 │  ← 'api_discovery' | 'user_import'
│ first_seen_at TIMESTAMP NOT NULL     │
│ is_duplicate_reference BOOLEAN       │  ← true if video was routed to playlist
│ import_batch_id TEXT                 │  ← null for native, batch ID for imports
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Video                                │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id TEXT NOT NULL UNIQUE        │  ← YouTube video ID
│ channel_id TEXT NOT NULL             │
│ discovered_at TIMESTAMP NOT NULL     │
│ discovered_in_run_id INTEGER        │  ← FK → Run
│ processing_state TEXT NOT NULL       │  ← enum: discovered, evaluating,
│                                      │     evaluated, acted, completed,
│                                      │     deferred, failed, not_yet_evaluated
│ final_outcome TEXT                   │  ← enum: accepted, rejected, deferred,
│                                      │     failed, not_yet_evaluated
│ deferred_reason TEXT                 │  ← quota, api_error, etc.
│ error_message TEXT                   │
│ completed_at TIMESTAMP              │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Run                                  │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ started_at TIMESTAMP NOT NULL        │
│ completed_at TIMESTAMP              │
│ run_type TEXT NOT NULL               │  ← 'scheduled' | 'manual' | 'dry_run' | 'replay' | 'backtest'
│ status TEXT NOT NULL                 │  ← 'running' | 'completed' | 'interrupted'
│ config_version_id INTEGER           │  ← FK → ConfigVersion
│ videos_discovered INTEGER DEFAULT 0  │
│ videos_accepted INTEGER DEFAULT 0    │
│ videos_rejected INTEGER DEFAULT 0    │
│ videos_deferred INTEGER DEFAULT 0    │
│ videos_failed INTEGER DEFAULT 0      │
│ videos_not_yet_evaluated INTEGER     │
│ quota_cost INTEGER DEFAULT 0         │
│ duration_seconds REAL               │
│ interrupt_reason TEXT               │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteDecision                        │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │  ← FK → Video
│ run_id INTEGER NOT NULL             │  ← FK → Run
│ route_id INTEGER NOT NULL           │  ← FK → Route
│ evaluation_order INTEGER NOT NULL   │  ← position in route evaluation sequence
│ decision TEXT NOT NULL               │  ← 'matched' | 'not_matched' | 'deferred' | 'skipped'
│ skip_reason TEXT                    │  ← 'final_route_matched' | 'exclusion' | 'disabled'
│ rejection_reason TEXT               │  ← specific reason for not_matched
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ConditionAudit                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_decision_id INTEGER NOT NULL  │  ← FK → RouteDecision
│ condition_index INTEGER NOT NULL    │  ← position in condition list
│ field TEXT NOT NULL                  │  ← 'title', 'duration', 'channel', etc.
│ operator TEXT NOT NULL              │  ← 'contains', 'equals', 'gt', etc.
│ expected_value TEXT NOT NULL        │  ← configured condition value
│ actual_value TEXT                   │  ← video's actual field value
│ matched BOOLEAN NOT NULL            │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ExecutedAction                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_decision_id INTEGER NOT NULL  │  ← FK → RouteDecision
│ run_id INTEGER NOT NULL             │  ← FK → Run (denormalized for constraint)
│ video_id INTEGER NOT NULL           │  ← FK → Video (denormalized for constraint)
│ action_type TEXT NOT NULL           │  ← 'playlist_insert' (v1)
│ target_playlist_id TEXT NOT NULL    │
│ outcome TEXT NOT NULL               │  ← 'succeeded' | 'failed' | 'not_attempted' | 'deferred'
│ error_message TEXT                  │
│ executed_at TIMESTAMP               │
│ UNIQUE(run_id, video_id, target_playlist_id, action_type)
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ DuplicateMatch                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │  ← FK → Video (the new discovery)
│ matched_fingerprint_id INTEGER      │  ← FK → TitleFingerprint (the historical match)
│ similarity_score REAL NOT NULL      │  ← 0.0–1.0
│ match_type TEXT NOT NULL            │  ← 'exact_video_id' | 'fuzzy_title'
│ source_route_id INTEGER             │  ← which route's acceptance created the reference
│ threshold_used REAL NOT NULL        │  ← the configured threshold at match time
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Route                                │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ name TEXT NOT NULL                   │
│ description TEXT                    │
│ evaluation_order INTEGER NOT NULL   │  ← unique, defines processing order
│ is_final BOOLEAN DEFAULT false      │
│ is_enabled BOOLEAN DEFAULT true     │
│ condition_composition TEXT NOT NULL │  ← 'AND' | 'OR'
│ created_at TIMESTAMP NOT NULL        │
│ updated_at TIMESTAMP NOT NULL        │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteCondition                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │  ← FK → Route
│ condition_index INTEGER NOT NULL    │  ← position within route
│ field TEXT NOT NULL                  │  ← 'title', 'duration', 'channel_id', etc.
│ operator TEXT NOT NULL              │  ← 'contains', 'not_contains', 'equals',
│                                      │     'gt', 'lt', 'in_list', etc.
│ value TEXT NOT NULL                  │  ← the comparison value
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteAction                          │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │  ← FK → Route
│ action_type TEXT NOT NULL           │  ← 'playlist_insert' (v1 only)
│ target_playlist_id TEXT NOT NULL    │
│ action_index INTEGER NOT NULL       │  ← execution order within route
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteExclusion                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │  ← FK → Route (route-scoped, not global)
│ exclusion_type TEXT NOT NULL        │  ← 'channel', 'video', 'title', 'word', 'duration_max'
│ exclusion_value TEXT NOT NULL       │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ConfigVersion                        │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ created_at TIMESTAMP NOT NULL        │
│ snapshot JSON NOT NULL              │  ← full effective route configuration
│ change_description TEXT             │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ QuotaLedger                          │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ date TEXT NOT NULL                   │  ← ISO date (quota bucket day)
│ operation_type TEXT NOT NULL        │  ← 'videos_list', 'search_list',
│                                      │     'playlist_items_insert', etc.
│ units_consumed INTEGER NOT NULL     │
│ call_count INTEGER NOT NULL         │
│ run_id INTEGER                      │  ← FK → Run (nullable for background ops)
│ recorded_at TIMESTAMP NOT NULL       │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ErrorEntry                           │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │  ← FK → Video (internal reference)
│ run_id INTEGER NOT NULL             │  ← FK → Run
│ error_category TEXT NOT NULL        │  ← 'api_error' | 'authorization' | 'runtime'
│ error_message TEXT NOT NULL          │
│ error_detail TEXT                   │  ← JSON with full context
│ status TEXT NOT NULL                │  ← 'active' | 'retrying' | 'dismissed' | 'resolved'
│ created_at TIMESTAMP NOT NULL        │
│ resolved_at TIMESTAMP               │
│ retry_count INTEGER DEFAULT 0       │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ DeferredItem                         │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │  ← FK → Video (internal reference)
│ run_id INTEGER NOT NULL             │  ← FK → Run (which run deferred it)
│ deferral_reason TEXT NOT NULL       │  ← 'quota_exhausted' | 'api_rate_limited'
│ retry_after TIMESTAMP               │  ← estimated next availability
│ retry_count INTEGER DEFAULT 0       │
│ status TEXT NOT NULL                │  ← 'pending' | 'retrying' | 'completed' | 'discarded'
│ created_at TIMESTAMP NOT NULL        │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RetentionPlaylist                    │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ playlist_id TEXT NOT NULL UNIQUE    │  ← YouTube playlist ID
│ playlist_name TEXT NOT NULL         │
│ designated_at TIMESTAMP NOT NULL    │
│ un_designated_at TIMESTAMP          │  ← null when active
│ is_active BOOLEAN DEFAULT true      │  ← false after user removes designation
│ last_checked_at TIMESTAMP           │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RetentionSignal                      │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id TEXT NOT NULL               │
│ playlist_id TEXT NOT NULL           │  ← FK → RetentionPlaylist
│ membership_source TEXT NOT NULL     │  ← 'yuhaul_routed' | 'user_driven' | 'unknown'
│ detected_at TIMESTAMP NOT NULL      │
│ is_retention_signal BOOLEAN NOT NULL│  ← true only when source != 'yuhaul_routed'
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Channel                              │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ channel_id TEXT NOT NULL UNIQUE     │  ← YouTube channel ID
│ channel_name TEXT                   │  ← cached, subject to Store A lifecycle
│ first_discovered_at TIMESTAMP       │
│ last_discovered_at TIMESTAMP        │
│ total_discovered INTEGER DEFAULT 0  │
│ total_routed INTEGER DEFAULT 0      │
│ total_retained INTEGER DEFAULT 0    │  ← in designated retention playlists
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Advisory                             │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ channel_id INTEGER NOT NULL         │  ← FK → Channel
│ advisory_type TEXT NOT NULL         │  ← 'low_usefulness' | 'declining_yield' | 'high_duplicate_rate'
│ generated_at TIMESTAMP NOT NULL     │
│ status TEXT NOT NULL                │  ← 'active' | 'dismissed' | 'ignored' | 'acted_upon'
│ ignored_until TIMESTAMP             │  ← for 30-day ignore, null otherwise
│ action_taken TEXT                   │  ← description of user action
│ dismissed_at TIMESTAMP              │
│ acted_upon_at TIMESTAMP             │
│ evidence_snapshot JSON              │  ← metrics at generation time
└──────────────────────────────────────┘
```

### Entity Relationships

```
Run 1──N Video
Run 1──1 ConfigVersion
Run 1──N QuotaLedger
Run 1──N ErrorEntry
Run 1──N DeferredItem

Video 1──N RouteDecision
Video 1──1 TitleFingerprint (via video_id)
Video 0──1 TransientMetadata (via video_id, may expire)
Video 0──N DuplicateMatch
Video 0──N RetentionSignal

RouteDecision N──1 Route
RouteDecision 1──N ConditionAudit
RouteDecision 1──N ExecutedAction

Route 1──N RouteCondition
Route 1──N RouteAction
Route 1──N RouteExclusion

ConfigVersion 1──N Run

TitleFingerprint 1──0..1 TransientMetadata
TitleFingerprint 0──N DuplicateMatch (as matched reference)

Channel 1──N Advisory
```

### Key Design Decisions in the Data Model

**D-1: Fingerprint-first duplicate detection.** The `TitleFingerprint` table is the canonical duplicate reference corpus, not `TransientMetadata`. Fingerprints are computed at discovery time and stored permanently. Raw titles are cached transiently for display. This ensures duplicate detection works regardless of metadata freshness.

**D-2: Three-tier outcome model.** Outcomes are stored at three levels: `Video.processing_state` (overall), `RouteDecision.decision` (per-route), `ExecutedAction.outcome` (per-action). This separation satisfies AC 32's requirement that absence of processing is never represented as rejection.

**D-3: Route-scoped exclusions.** `RouteExclusion` is scoped to individual routes, not global. Each route defines its own exclusion rules as conditions. This matches the specifier's correction that exclusions are route-level conditions, not global gates.

**D-4: ConfigVersion as JSON snapshot.** Each config change creates a full snapshot of the effective route configuration (all routes, conditions, actions, exclusions). This is simpler and more reliable than diff-based versioning and directly satisfies the requirement to reconstruct configuration at any point in time.

**D-5: Retention playlist provenance.** `RetentionSignal.membership_source` distinguishes YuHaul-routed membership from user-driven membership. Only `user_driven` membership counts as a retention signal, preventing the system from crediting its own routing actions.

**D-6: Separate Error and Deferred tables.** Quota exhaustion (deferral) and runtime failures (errors) are distinct operational states with different recovery semantics. Quota exhaustion is not classified as failure.

**D-7: ExecutedAction denormalization for idempotency.** The `ExecutedAction` entity carries `run_id` and `video_id` as direct columns (denormalized from `RouteDecision`) to enable a SQLite-enforceable `UNIQUE(run_id, video_id, target_playlist_id, action_type)` constraint. This guarantees cross-route idempotency: a video inserted into the same playlist by two different routes in the same run is prevented at the database level. The alternative — a constraint on `(route_decision_id, target_playlist_id, action_type)` — would not prevent duplicate insertions across route decisions.

**D-8: Advisory entity for persistent user authority state.** The `Advisory` entity persists advisory generation, user dismiss/ignore/act actions, and evidence snapshots. Advisory generation is computed from `Channel` statistics (yield, usefulness, duplicate rate), but user responses are durable state. The `ignored_until` timestamp enables the 30-day ignore-then-reappear behavior from the design spec. Advisory status transitions: `active` → `dismissed` | `ignored` | `acted_upon`. Ignored advisories regenerate when `ignored_until` expires and the underlying condition persists.

**D-9: RetentionPlaylist soft deletion.** The `RetentionPlaylist` entity uses `is_active` and `un_designated_at` to model playlist un-designation without losing historical retention signal data. When a user removes a playlist designation, `is_active` is set to `false` and `un_designated_at` is recorded. Historical `RetentionSignal` records from that playlist are preserved for audit but stop counting toward active usefulness metrics. This enables the user to re-designate the playlist later without losing historical signal context.

**D-10: Video ID type consistency.** All entities use `video_id INTEGER` as a foreign key reference to `Video.id` for internal references (RouteDecision, DuplicateMatch, ExecutedAction, ErrorEntry, DeferredItem). The YouTube video ID is stored as `video_id TEXT` on the `Video` entity itself and on `TitleFingerprint`, `TransientMetadata`, and `RetentionSignal` where it serves as the external identifier. This separation ensures referential integrity on internal relationships while preserving the YouTube ID where needed for API operations.

## Migration

### Historical Corpus Import

The prior YuHaul installation has a corpus of 10,000+ titles obtained from the YouTube API. These titles retain their API-Data provenance after migration — exporting from one API client version and importing into another does not cause data to cease being API Data.

**Migration flow:**

1. **Read** raw titles and video IDs from the source SQLite database
2. **Transform** each title into a fingerprint using the same normalization algorithm applied to natively discovered titles
3. **Store** fingerprints permanently in `TitleFingerprint` (Store B) with `source = 'user_import'` and an `import_batch_id`
4. **Discard** raw titles after successful fingerprint generation — raw API-originated titles are not retained permanently

**Compliance rationale:** The fingerprint is the user-approved non-reversible representation (per the functional reading accepted in the [compliance analysis](yuhaul-youtube-compliance.md)). It is an internal index structure, not replacement metadata. The raw title follows the standard 30-day policy for API Data; since migration is a one-time event and the raw title serves no ongoing purpose after fingerprint generation, it may be discarded immediately rather than retained for 30 days.

**Schema handling:** Migrated fingerprints use `source = 'user_import'` and `import_batch_id` to distinguish them from natively discovered entries. No `TransientMetadata` record is created for migrated entries — the fingerprint exists only in Store B.

**INV-6 compliance:** Migrated fingerprints use the identical schema and fingerprint algorithm as natively captured entries. Duplicate detection produces identical results regardless of provenance.

## Processing Pipeline

### Run Lifecycle

A run proceeds through four phases. Each phase is atomic with respect to state transitions — if the run is interrupted, state reflects the last completed phase.

```
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│ Phase 1  │────▶│ Phase 2  │────▶│ Phase 3  │────▶│ Phase 4  │
│ Discovery│     │ Duplicate│     │ Routing  │     │ Action   │
│          │     │ Detection│     │          │     │ Execution│
└──────────┘     └──────────┘     └──────────┘     └──────────┘
     │                │                │                │
     ▼                ▼                ▼                ▼
  Persist new     Check against     Evaluate routes   Execute playlist
  video records   fingerprint       in order, record   insertions,
  as not-yet-     corpus. Flag      evidence. Apply    record outcomes.
  evaluated.      duplicates.       final-route       Handle failures
                                    termination.       and deferrals.
```

### Phase 1: Discovery

1. Retrieve user's subscription list via `subscriptions.list` (cost: 1 unit per page)
2. For each subscribed channel, retrieve the uploads playlist ID via `channels.list` (1 unit per call, cached after first retrieval), then fetch recent uploads via `playlistItems.list` on the uploads playlist (cost: 1 unit per page)
3. Filter to videos published since last discovery watermark
4. For each new video: create `Video` record with `processing_state = not_yet_evaluated`
5. Pre-fetch video metadata via `videos.list` (cost: 1 unit per 50 IDs)
6. Store raw metadata in `TransientMetadata` (Store A)
7. Compute title fingerprint → store in `TitleFingerprint` (Store B)
8. Record discovery quota cost in `QuotaLedger`

**Pre-flight budget check:** Before Phase 1, estimate discovery cost based on subscription count. If insufficient budget for even minimal discovery, defer the entire run.

### Phase 2: Duplicate Detection

1. For each discovered video, check exact match: `TitleFingerprint` with same `video_id` where `is_duplicate_reference = true`
2. For non-exact matches, run fuzzy detection: FTS5 query against `fingerprint_tokens`, then compute similarity score against candidates
3. If similarity exceeds configured threshold → flag as `DuplicateMatch`
4. Apply user-configured duplicate behavior (skip, flag, allow)
5. No external API calls in this phase — all data is local

### Phase 3: Routing

For each non-duplicate, non-excluded video:

1. **Pre-flight metadata budget check:** Count videos requiring condition evaluation with expired metadata. Estimate re-fetch cost (1 unit per 50 videos). If `consumed + re_fetch_estimate + action_estimate > daily_limit`, defer videos with expired metadata to preserve Phase 4 budget.
2. Load active route configuration (ordered routes with conditions/actions) — or draft configuration if provided via dry-run endpoint
3. For each route in order:
   a. Check route-scoped exclusions → if excluded, record `RouteDecision(decision=skipped, skip_reason=exclusion)`
   b. Check if route is enabled → if disabled, record `RouteDecision(decision=skipped, skip_reason=disabled)`
   c. Evaluate conditions (AND/OR composition per route)
   d. Record `ConditionAudit` for each condition (matched/not matched with actual values)
   e. If conditions match → record `RouteDecision(decision=matched)`
   f. If route is marked `is_final` → skip remaining routes with `RouteDecision(decision=skipped, skip_reason=final_route_matched)`
4. Determine `Video.processing_state = evaluated` (or `deferred` if quota prevents action execution)

**Condition evaluation:** Conditions are evaluated against video metadata. For fields like `title`, the value comes from `TransientMetadata` if fresh, or from the fingerprint's reconstructable properties if expired. Conditions on `duration`, `channel_id`, etc. use cached metadata or re-fetch on demand (budgeted in step 1).

### Phase 4: Action Execution

For each video with at least one matched route:

1. Pre-flight budget check: estimate action cost (50 units per playlist insert)
2. If insufficient budget → defer video and all remaining videos; mark as `processing_state = deferred`
3. For each matched route's actions:
   a. Check idempotency: has this (video_id, playlist_id) already been inserted? → skip if yes
   b. Execute `playlistItems.insert` (cost: 50 units)
   c. Record `ExecutedAction` with outcome (succeeded/failed)
   d. On failure: categorize error, add to `ErrorEntry`, mark `Video.processing_state = failed`
4. If all actions succeeded → `Video.processing_state = completed`, `final_outcome = accepted`
5. Update duplicate reference corpus: if video was routed to at least one playlist, set `TitleFingerprint.is_duplicate_reference = true`

### Mode Variations

The processing pipeline supports four execution modes, each with different isolation guarantees:

**Production mode:** Full pipeline execution with external state mutations (YouTube playlist insertions) and production statistics updates. Scheduled and manual runs use this mode.

**Dry-run mode:** Evaluates current pending workload against either the active configuration or a draft configuration (passed via `POST /api/runs/dry-run` request body). Actions are simulated — no playlist changes, no production statistics updates, no duplicate corpus mutations. Results are stored as a run record marked `run_type = 'dry_run'` for inspection. This enables the workflow: edit routes → dry-run with unsaved changes → save if satisfied.

**Replay mode:** Re-evaluates retained historical observations against the *current active* configuration. Uses retained observations (like backtest) but applies the live configuration snapshot, not a proposed one. Useful for answering "what would happen if I re-ran old data through my current routes?" Results are stored with `run_type = 'replay'`, isolated from production statistics.

**Backtest mode:** Re-evaluates retained historical observations against a *proposed* configuration (passed via `POST /api/backtest` request body). Useful for comparing routing changes before committing them. Results are stored with `run_type = 'backtest'`, isolated from production statistics.

| Mode | Phase 1 | Phase 2 | Phase 3 | Phase 4 | State Writes |
|---|---|---|---|---|---|
| **Production** | Full | Full | Full | Full (external mutations) | All production state |
| **Dry-run** | Full | Full | Full (active or draft config) | Simulated (no external mutations) | Run/decision records marked as dry_run; NO production state changes |
| **Replay** | Uses retained observations (no API) | Full (against retained corpus) | Full (against current active config) | Simulated | Replay result records only; NO production state changes |
| **Backtest** | Uses retained observations (no API) | Full (against retained corpus) | Full (against proposed config) | Simulated | Backtest result records only; NO production state changes |

**Isolation guarantee:** Dry-run, replay, and backtest modes never mutate external state (YouTube playlists, subscriptions), production duplicate corpus, production idempotency records, or production statistics (INV-8).

## Quota Management

### Quota Model

YouTube Data API v3 uses a unit-based quota with a daily bucket (default 10,000 units, resets per provider-documented schedule). Each API method has a documented unit cost.

| Operation | Method | Unit Cost |
|---|---|---|
| List subscriptions | `subscriptions.list` | 1 per page |
| Search videos | `search.list` | 100 per call (not used in standard discovery) |
| List channel uploads | `playlistItems.list` (uploads playlist) | 1 per call |
| Get video metadata | `videos.list` | 1 per call (up to 50 IDs) |
| Get channel info | `channels.list` | 1 per call (cached for uploads playlist ID) |
| Insert playlist item | `playlistItems.insert` | 50 per call |
| List playlist items | `playlistItems.list` | 1 per call |

### Pre-Flight Budget Check

Before each quota-consuming phase, the pipeline:

1. Queries `QuotaLedger` for today's consumed units
2. Estimates cost for the phase (e.g., N playlist inserts × 50 units)
3. If `consumed + estimated > daily_limit` → defer remaining work
4. If `consumed + estimated > configurable_reserve_threshold` → defer and warn

### Priority-Aware Allocation

When quota is limited, processing priority is:

1. **Discovery** (lowest cost, highest information value) — always attempt
2. **Metadata fetch** (1 unit per 50 videos) — always attempt after discovery
3. **Routing evaluation** (no API cost) — always complete
4. **Action execution** (50 units per insert) — deferred first when quota is tight
5. **Metadata refresh** (1 unit per 50 videos) — lowest priority, deferred to next run

### Remaining Capacity and Reset Information

The quota endpoint (`GET /api/analytics/quota`) returns derived values alongside raw consumption:

- **Remaining capacity:** Computed as `daily_limit - SUM(units_consumed WHERE date = today)`. When the daily limit is configurable, the current configured value is used. If the YouTube API provides explicit remaining quota information (via response headers or a quota endpoint), that value takes precedence over the local computation.
- **Reset timing:** The next quota bucket reset time, determined by the Quota Reset Detection mechanism below. Returned as an ISO 8601 timestamp.
- **Bucket info:** The provider-documented bucket size and reset schedule.

These derived values satisfy the quota-accounting requirement (AC 4: "reports remaining quota capacity and quota bucket reset timing where the API provides this information").

### Quota Reset Detection

The system does NOT assume midnight Pacific reset. Instead:

1. On first API call of a run, check if the API reports quota reset (via response headers or `quota` endpoint if available)
2. If the API provides reset timing, use it
3. If not, reset the daily ledger at the provider-documented reset time
4. Track actual vs. expected reset to detect provider-side changes

## Decision Evidence

### Evidence Model

Every route decision produces a complete evidence trail:

```
Video (video_id, channel_id, processing_state, final_outcome)
  └── RouteDecision (route_id, decision, evaluation_order)
       ├── ConditionAudit (field, operator, expected, actual, matched)
       ├── ConditionAudit ...
       └── ExecutedAction (action_type, target, outcome, error_message)
```

The user can trace from any video outcome back through:
- Which routes were evaluated and in what order
- Which conditions matched and which didn't (with actual values)
- Which actions were executed and their outcomes
- Why subsequent routes were skipped (final-route termination)

### Evidence Display with Expired Metadata

When `TransientMetadata` has expired for a video referenced in evidence:
- The evidence structure (which conditions matched, what actions executed) is always available from Store B
- The displayable values (video title, channel name) degrade to fingerprint or "metadata expired — refresh available"
- A one-click refresh re-fetches metadata from YouTube API (1 unit per 50 videos) and restores full display

## Duplicate Detection Engine

### Two-Pass Algorithm

**Pass 1: Exact match.** Query `TitleFingerprint` for `video_id` match where `is_duplicate_reference = true`. O(1) via index.

**Pass 2: Fuzzy match.** If no exact match:
1. Tokenize the new video's fingerprint into tokens
2. FTS5 query: find candidates sharing ≥N tokens with the new fingerprint (N configurable)
3. For each candidate, compute normalized-token-set Jaccard similarity: |A ∩ B| / |A ∪ B| where A and B are the token sets of the two fingerprints
4. If any candidate exceeds the configured threshold (default: 0.5) → flag as duplicate

### Fingerprint Design

The fingerprint is a normalized, lossy transformation designed for music-title matching:

1. **Lowercase** the title
2. **Strip** punctuation, special characters, parenthetical annotations (e.g., "(Official Video)", "[Lyrics]")
3. **Remove** common music stop words (e.g., "official", "video", "lyrics", "ft.", "feat.")
4. **Tokenize** into sorted word set
5. **Store** both the canonical fingerprint string and the token set (for FTS5 indexing)

This is a one-way, lossy transformation. The original title cannot be reconstructed from the fingerprint. Different titles may produce the same fingerprint (acceptable false-positive rate for music discovery).

### Similarity Algorithm

**Decision:** Normalized-token-set Jaccard similarity as the sole fuzzy-matching metric. No combined score, no weighted pipeline.

**Algorithm:** Given two fingerprints A and B, each represented as a set of normalized tokens:

```
similarity(A, B) = |A ∩ B| / |A ∪ B|
```

**Threshold:** Default 0.5, user-configurable in range 0.0–1.0.

**Rationale (from researcher benchmark):**

The prior architecture recommended a combined score (0.6 × normalized Levenshtein + 0.4 × Jaccard) at a 0.97 threshold, carried over from the prior system's pure-Levenshtein implementation. Empirical benchmarking against 10 duplicate groups (147 positive pairs, 181 negative pairs) from the historical corpus revealed:

| Algorithm | Best F1 | Best Threshold | False Positives | Recall at 0.97 |
|---|---|---|---|---|
| Token-set Jaccard | 0.990 | 0.572 | 0 | n/a (different scale) |
| Char-trigram cosine | 0.993 | 0.566 | 2 | n/a |
| Combined 0.6×Lev + 0.4×Jacc | 0.983 | 0.409 | — | **0.891 (16 missed)** |
| Pure Levenshtein | 0.969 | 0.630 | — | n/a |

The combined score at the architecture's stated 0.97 threshold missed 11% of true duplicates — artist/track reorders ("Get Lucky - Daft Punk" vs "Get Lucky (Daft Punk)") and artist-name shortening ("Pharrell Williams" vs "Pharrell"). Combined scores do not live on the same scale as pure Levenshtein; the 0.97 number was invalid for the combined score space.

Token-set Jaccard was selected over char-trigram cosine despite cosine's marginally higher F1 (0.993 vs 0.990) because Jaccard produced **zero false positives** (vs 2 for cosine). For a music-discovery tool where a false positive means silently skipping a genuinely distinct video, zero false positives is the safer operating point. The simpler algorithm also reduces implementation and tuning complexity.

**Configuration:** The threshold is exposed via `PUT /api/settings/duplicate-threshold` and is part of the effective configuration (creates a `ConfigVersion` on change). The prior weight-based configuration is removed — there are no weights to configure.

**Derived metrics exception:** Not applied for v1. The 36-month statistical retention extension (YouTube Developer Policies, June 2026) requires a compliance audit application and is deferred to v2.

### Corpus Scope

Per the behavioral requirement, the duplicate-reference corpus (`is_duplicate_reference = true`) includes only titles from videos that were **routed to at least one playlist.** Videos that were rejected, excluded, failed, or never evaluated are in the corpus but not eligible as duplicate references. They remain available for audit and inspection.

## Statistics and Analytics

### Three-Level Granularity

| Level | Entity | Scope | Retention |
|---|---|---|---|
| **Per-run raw** | `Run`, `RouteDecision`, `ExecutedAction` | Individual run data | Permanent |
| **Materialized aggregates** | `ChannelStats` (materialized as `Channel` entity counters); `RunStats` and `RouteStats` are computed views derived from raw `Run`, `RouteDecision`, and `ExecutedAction` records | Period summaries | Permanent |
| **Temporal series** | Time-bucketed queries on raw data | Trend visualization | Derived from permanent raw data |

### Run Statistics

Run statistics are computed views derived from the `Run` entity's denormalized counters and the `RouteDecision`/`ExecutedAction` records. No separate `RunStats` materialized table is needed — the `Run` entity itself serves as the run-level aggregate.

Each run records:
- Counts by outcome: accepted, rejected, deferred, failed, not_yet_evaluated (stored directly on `Run` entity)
- Rejection counts by reason (no route matched, exclusion, duplicate, duration) — computed from `RouteDecision.rejection_reason` and `RouteDecision.skip_reason`
- Quota cost by operation type — computed from `QuotaLedger` filtered by `run_id`
- Duration (stored directly on `Run` entity)
- Configuration version (FK on `Run` entity)

### Route Statistics

Route statistics are computed views — aggregated by querying `RouteDecision` records filtered by `route_id` and `run_id`. No separate `RouteStats` materialized table is needed for v1 data volumes.

Per-route, per-run:
- Videos evaluated
- Videos matched (with condition match patterns)
- Videos not-matched (with rejection reason breakdown)
- Videos skipped (final-route or exclusion)

### Channel Statistics

Per-channel, cumulative and per-period:
- Videos discovered
- Videos routed (accepted by at least one route)
- Videos rejected (with reason breakdown)
- Videos retained in designated retention playlists
- Usefulness rate: retained / discovered
- Yield: routed / discovered
- Time since last useful discovery

**Channel counter maintenance:** The `Channel` entity's denormalized counters (`total_discovered`, `total_routed`, `total_retained`) are updated atomically within the processing pipeline:
- `total_discovered` incremented during Phase 1 (Discovery) when a new video from the channel is persisted
- `total_routed` incremented during Phase 4 (Action Execution) when at least one action for a video from the channel succeeds
- `total_retained` updated during retention signal monitoring when playlist membership is observed or lost

These counters are materialized aggregates for fast advisory generation and dashboard display. They are reconcilable against authoritative source data (COUNT queries on Video, RouteDecision/ExecutedAction, RetentionSignal) and may be recomputed on demand if drift is suspected.

### Retention Signals

Retention signals are measured from designated retention playlists:
- Periodically check playlist membership for previously routed videos
- Distinguish `yuhaul_routed` (system added it) from `user_driven` (user added it independently)
- Only `user_driven` membership counts as a retention signal
- Supplementary provider signals (watch time, engagement) captured where available; graceful degradation when unavailable

### Yield vs Usefulness

- **Yield** = how often a channel's content matches routing rules (system-level metric)
- **Usefulness** = how often a channel's content ends up in the user's actual rotation (human-level metric)
- A channel can have high yield but low usefulness — the system routes its content, but the user doesn't keep it

## Advisory System

### Advisory Generation

Advisories are evidence-backed recommendations generated from channel statistics. The system monitors channel performance metrics and generates advisories when thresholds are crossed:

**Advisory types:**
- **Low usefulness:** Channel's usefulness rate (retained / discovered) falls below configurable threshold over a configurable time window. Evidence: "Channel X: 5% usefulness (3 retained of 60 discovered) over 90 days."
- **Declining yield:** Channel's yield rate has decreased significantly compared to historical baseline. Evidence: "Channel X: yield dropped from 40% to 10% over 60 days."
- **High duplicate rate:** Channel's videos frequently match duplicates from other sources. Evidence: "Channel X: 80% duplicate rate (24 of 30 videos matched existing corpus)."

**Generation trigger:** Advisories are computed at the end of each production run, after statistics are updated. The computation scans `Channel` statistics and generates new advisories when conditions are met and no active advisory of the same type already exists for the channel.

**Evidence snapshot:** Each advisory captures the metrics at generation time in `evidence_snapshot` (JSON), enabling the user to see what data triggered the advisory even if statistics change later.

### User Actions on Advisories

Per the design spec, the user can take three actions on each advisory:

1. **Take action:** User manually acts (e.g., unsubscribes, adjusts route) and logs the action. Advisory status → `acted_upon`, `acted_upon_at` recorded. The system never auto-acts.
2. **Dismiss:** User permanently dismisses this specific advisory. Status → `dismissed`, `dismissed_at` recorded. The advisory never reappears even if conditions persist.
3. **Ignore for 30 days:** User temporarily hides the advisory. Status → `ignored`, `ignored_until` set to `now + 30 days`. When `ignored_until` expires, if the underlying condition still holds, a new advisory is generated (new `id`, new `generated_at`).

### Advisory Persistence

The `Advisory` entity stores both generated advisories and user action state. This separation enables:
- Audit trail: all advisories ever generated, with evidence snapshots
- User authority: dismiss/ignore decisions persist across runs
- Temporal behavior: 30-day ignore with automatic re-generation
- UI state: active advisories shown as badges, dismissed/ignored hidden

**Advisory lifecycle:** `generated` → `active` (visible) → user action → `dismissed` | `ignored` | `acted_upon`. Ignored advisories with expired `ignored_until` trigger re-evaluation: if condition persists, new advisory generated; if condition resolved, no new advisory.

## Configuration Versioning

### Snapshot Model

Every route configuration change creates a `ConfigVersion` with a full JSON snapshot of the effective configuration:

```json
{
  "routes": [
    {
      "id": 1,
      "name": "Music Discovery",
      "evaluation_order": 1,
      "is_final": true,
      "is_enabled": true,
      "condition_composition": "AND",
      "conditions": [
        {"field": "title", "operator": "contains", "value": "jazz"},
        {"field": "duration_seconds", "operator": "gt", "value": "180"}
      ],
      "actions": [
        {"type": "playlist_insert", "target": "PL..."}
      ],
      "exclusions": [
        {"type": "channel", "value": "UC..."}
      ]
    }
  ],
  "duplicate_threshold": 0.5,
  "duplicate_behavior": "skip"
}
```

Each `Run` records its `config_version_id`, enabling reconstruction of the exact configuration active during any historical run.

## API Surface

### REST Endpoints

The API server provides REST endpoints matching the designer's UI specification:

**Key endpoints with request body parameters:**
- `POST /api/runs/dry-run` — optional `draft_configuration` body parameter (same JSON structure as `ConfigVersion.snapshot`). When provided, evaluates against the draft config instead of the active config. Enables the "edit → preview → save" workflow.
- `POST /api/runs/replay` — optional `config_version_id` query parameter. When provided, replays retained observations against the specified historical configuration snapshot instead of the current active configuration. Enables the "re-evaluate history with different settings" workflow from the design spec.
- `POST /api/backtest` — required `proposed_configuration` body parameter. Evaluates retained observations against the proposed config.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/overview` | Dashboard summary: last run, quota state, error count |
| `POST` | `/api/runs` | Initiate a manual run |
| `POST` | `/api/runs/dry-run` | Initiate a dry-run (optional `draft_configuration` body param for unsaved route edits) |
| `POST` | `/api/runs/replay` | Initiate a replay — re-evaluate retained observations against current active config (optional `config_version_id` query param for historical config) |
| `GET` | `/api/runs` | List runs (filterable by type, date, status) |
| `GET` | `/api/runs/:id` | Run detail with outcome summary |
| `GET` | `/api/runs/:id/videos` | Video outcomes for a run |
| `GET` | `/api/runs/:id/videos/:videoId` | Decision detail (three-tier evidence) |
| `GET` | `/api/routes` | List routes (ordered) |
| `POST` | `/api/routes` | Create route |
| `GET` | `/api/routes/:id` | Route detail |
| `PUT` | `/api/routes/:id` | Update route (creates ConfigVersion) |
| `DELETE` | `/api/routes/:id` | Delete route |
| `PUT` | `/api/routes/reorder` | Update evaluation order |
| `GET` | `/api/analytics/trends` | Temporal trend data (time-series) |
| `GET` | `/api/analytics/channels` | Channel statistics list |
| `GET` | `/api/analytics/channels/:id` | Channel detail with advisories, duplicate provenance breakdown (discovery credit vs. relevance credit), and retention signal provenance (user-driven vs. YuHaul-routed) |
| `GET` | `/api/analytics/channels/:id/duplicate-provenance` | Detailed duplicate provenance breakdown: per-video discovery credit (first source) and relevance credit (also-published-by) for the specified channel |
| `GET` | `/api/analytics/channels/:id/advisories` | Advisory list for a specific channel |
| `GET` | `/api/analytics/advisories` | All active advisories across channels |
| `POST` | `/api/analytics/advisories/:id/dismiss` | Permanently dismiss an advisory |
| `POST` | `/api/analytics/advisories/:id/ignore` | Ignore advisory for 30 days |
| `POST` | `/api/analytics/advisories/:id/act` | Log user action taken on advisory |
| `GET` | `/api/analytics/quota` | Quota ledger breakdown |
| `GET` | `/api/errors` | Error queue (active entries) |
| `POST` | `/api/errors/:id/retry` | Retry a failed item |
| `POST` | `/api/errors/:id/dismiss` | Dismiss from active queue (preserves history) |
| `GET` | `/api/deferred` | Deferred work queue |
| `POST` | `/api/deferred/:id/retry` | Manually retry a deferred item |
| `POST` | `/api/deferred/:id/discard` | Discard from active queue (preserves history) |
| `POST` | `/api/backtest` | Run backtest with proposed configuration (request body contains draft config JSON) |
| `GET` | `/api/backtest/:id` | Backtest results |
| `GET` | `/api/config/versions` | Configuration history |
| `GET` | `/api/config/versions/:id` | Specific config snapshot with list of runs that executed under this version |
| `GET` | `/api/config/versions/:id/diff` | Diff between this version and its predecessor (or optional `compare_to` query param for arbitrary version comparison) |
| `POST` | `/api/config/versions/:id/restore` | Restore a historical configuration version as the active config (creates new ConfigVersion with snapshot from the restored version) |
| `GET` | `/api/settings` | Current settings (schedule, quota limits, duplicate threshold) |
| `PUT` | `/api/settings` | Update settings (schedule, quota limits) |
| `GET` | `/api/settings/duplicate-threshold` | Current duplicate detection threshold (value + explanation) |
| `PUT` | `/api/settings/duplicate-threshold` | Update duplicate detection threshold (creates new ConfigVersion since threshold is part of effective configuration) |
| `GET` | `/api/settings/retention-playlists` | List designated retention playlists |
| `PUT` | `/api/settings/retention-playlists` | Update retention playlist designations |
| `POST` | `/api/corpus/import` | Import historical corpus (CSV/JSON) |
| `GET` | `/api/corpus/export` | Export current corpus |
| `GET` | `/api/account/status` | Account connection status |
| `POST` | `/api/auth/initiate` | Begin OAuth2 flow |
| `GET` | `/api/auth/callback` | OAuth2 callback |
| `DELETE` | `/api/account` | Disconnect account — revokes stored OAuth tokens, removes credentials (preserves all routing data, statistics, and configuration) |
| `GET` | `/api/runs/:id/stream` | SSE stream for real-time run progress |

### Server-Sent Events (SSE)

Real-time progress for active runs:
- `run.started` — run begins
- `run.phase` — phase transition (discovery, duplicate detection, routing, action execution)
- `run.video.processed` — individual video outcome
- `run.quota.warning` — approaching quota limit
- `run.completed` — run finished with summary
- `run.error` — run-level error

## Scheduling and Recovery

### Internal Scheduler

The system includes an internal cron-style scheduler:
- User configures schedule via Settings (cron expression or simplified interval)
- Scheduler triggers production runs at configured times
- Manual runs bypass the scheduler (immediate execution)
- Dry-runs and backtests bypass the scheduler (on-demand)

### Run Recovery

If a run is interrupted (process crash, network failure, user cancellation):
1. The run's `status` is set to `interrupted` with `interrupt_reason`
2. Videos in `evaluating` or `acted` state are candidates for re-processing
3. On next run start, the pipeline checks for interrupted runs and resumes from the last known state
4. Idempotency guards prevent duplicate playlist insertions on resume
5. Videos in `completed` state are never reprocessed

### Deferred Work Recovery

Deferred items are automatically retried:
1. At the start of each production run, check deferred queue
2. If quota is available, process deferred items before new discoveries
3. Deferred items from the oldest runs are processed first (FIFO)
4. If a deferred item fails again, it moves to the error queue

## Security Threat Model

### Trust Boundaries

| Boundary | Assets Crossing | Threats | Mitigation |
|---|---|---|---|
| **User ↔ Web UI** | Credentials, routing config, video data | Session hijacking, CSRF, XSS | HTTPS-only, CSRF tokens, CSP headers, input sanitization |
| **Web UI ↔ API Server** | Auth tokens, configuration, run data | Unauthorized access, injection | Token-based auth, parameterized queries, input validation |
| **API Server ↔ YouTube API** | OAuth tokens, API requests/responses | Token theft, API abuse | Encrypted token storage, minimal scopes, rate limiting |
| **API Server ↔ SQLite DB** | All persistent data | SQL injection, data corruption | Parameterized queries only, no raw SQL construction |
| **Storage ↔ Disk** | Database file | Unauthorized file access | File system permissions, Docker volume isolation |

### STRIDE Analysis

| Threat | Category | Mitigation |
|---|---|---|
| OAuth token theft | Spoofing | Encrypted at rest (AES-256), never logged, scoped to minimum required permissions |
| Unauthorized API access | Tampering | Token-based authentication, all mutations require valid session |
| Route config tampering | Tampering | Auth required for all config mutations, config versioning provides audit trail |
| Run history manipulation | Repudiation | Append-only run/decision records, config versioning, no hard deletes of evidence |
| API data exposure | Information Disclosure | Single-user system, no external API exposure, HTTPS for all transport |
| Quota manipulation | Elevation of Privilege | Quota tracked server-side only, user cannot override consumed quota |

### Accepted Risks

- **Single-user assumption:** No multi-user auth model. If the system is ever exposed to multiple users, auth must be redesigned.
- **SQLite concurrency:** Single-writer limitation. Acceptable for single-user tool with no concurrent processing.
- **Local deployment security:** The system trusts the host environment. Physical access to the host = access to all data.

## Anchor Check

### Minimal Version

The anchor defines v1 as: *"single-user personal tool with web administration interface, scheduled processing, stateful routing with conditions/actions, historical duplicate detection, quota awareness, decision evidence, statistics with temporal analytics, dry-run, backtesting, and source/channel analysis."*

This architecture delivers all elements:

- ✅ Single-user — no multi-tenancy in data model or deployment
- ✅ Web admin interface — SPA matching designer spec, 9 views, REST API
- ✅ Scheduled processing — internal cron scheduler
- ✅ Stateful routing — three-tier outcome model, persistent state
- ✅ Conditions/actions — configurable per route, AND/OR composition
- ✅ Historical duplicate detection — fingerprint corpus with FTS5 search
- ✅ Quota awareness — QuotaLedger, pre-flight budget checks, deferral
- ✅ Decision evidence — ConditionAudit trail per route decision
- ✅ Statistics with temporal analytics — three-level granularity, time-bucketed
- ✅ Dry-run — mode flag, state isolation, no external mutations, supports draft configurations
- ✅ Replay — user-triggered re-evaluation against current config, isolated from production
- ✅ Backtesting — offline evaluation against retained observations with proposed config
- ✅ Source/channel analysis — per-channel yield and usefulness metrics
- ✅ Advisory system — evidence-backed recommendations with persistent user action state

### Not This

- ✅ No continuous monitoring — polling-based discovery only
- ✅ No auto-unsubscribe — advisory system only, user decides
- ✅ No autonomous rule optimization — system advises, user acts
- ✅ No ML classification — configurable threshold-based fuzzy matching
- ✅ No multi-provider — YouTube Data API v3 only
- ✅ No social/community — single-user personal tool
- ✅ No sophisticated recommendation AI — rule-based routing

## Open Questions

### OQ-1 — Fingerprint algorithm selection [RESOLVED]

- **Question:** Which specific fingerprint normalization and similarity algorithm provides the best match quality for music titles?
- **Resolution:** Resolved — normalized-token-set Jaccard similarity, default threshold 0.5, user-configurable range 0.0–1.0. No combined score, no weights.
- **Routing:** Architect (resolved)
- **Blocking:** no
- **Depends on:** none
- **Rationale:** Researcher benchmarked four algorithms against 10 duplicate groups (147 positive pairs, 181 negative pairs) from the historical corpus. Token-set Jaccard achieved F1=0.990 with zero false positives at threshold 0.572. The architecture's prior recommendation (combined 0.6×Lev + 0.4×Jacc at threshold 0.97) was shown to miss 11% of true duplicates at the stated threshold — the 0.97 value was inherited from the prior pure-Levenshtein implementation and is invalid in the combined score space. Jaccard was selected over char-trigram cosine (F1=0.993, 2 false positives) because zero false positives is the safer operating point for music discovery where a false positive silently skips a distinct video. See §Similarity Algorithm for full benchmark table.

### OQ-2 — Discovery method selection [RESOLVED]

- **Question:** Should discovery use `search.list` (100 units/call, broad) or `activities.list` + `playlistItems.list` (1 unit/call, narrower) for finding new uploads?
- **Resolution:** Resolved — `playlistItems.list` on each channel's cached uploads playlist. 1 unit per page, reliable, complete for new uploads.
- **Routing:** Architect (resolved)
- **Blocking:** no
- **Depends on:** none
- **Rationale:** `playlistItems.list` on the uploads playlist costs 1 unit per page (vs 100 for `search.list`), returns all uploads in chronological order, and is the canonical YouTube API method for "what has this channel published." The uploads playlist ID is obtained once via `channels.list` (1 unit) and cached. For 100 subscriptions with 2 pages each, discovery costs ~200 units vs ~10,000 units with `search.list` — a 50× reduction that preserves quota for action execution. `search.list` is retained in the quota model as a reference but is not used in standard discovery.

### OQ-3 — Metadata refresh strategy [RESOLVED]

- **Question:** Should metadata refresh be proactive (scheduled background refresh of active records) or reactive (refresh on demand when user views expired metadata)?
- **Resolution:** Resolved — hybrid: routed videos refreshed at run time during processing, older/unviewed records refreshed on demand when the user accesses evidence or UI display.
- **Routing:** Architect (resolved)
- **Blocking:** no
- **Depends on:** none
- **Rationale:** A purely proactive approach wastes quota refreshing records the user may never view. A purely reactive approach introduces latency when viewing historical evidence and risks stale metadata during route condition evaluation. The hybrid approach refreshes metadata for recently routed videos during Phase 3 (needed for condition evaluation and evidence completeness) and defers older records to on-demand refresh (triggered by UI access or evidence inspection). This aligns with the existing priority-aware quota allocation (§Quota Management) where metadata refresh has lowest priority. Refresh cost is 1 unit per 50 videos — negligible at typical corpus sizes (<0.5% of daily budget).

### OQ-4 — Web SPA technology selection [RESOLVED]

- **Question:** Which frontend framework/stack should the web SPA use?
- **Resolution:** Resolved — implementation choice for coder. The anchor and architecture impose no framework requirement.
- **Routing:** Architect (resolved — delegated to implementation)
- **Blocking:** no
- **Depends on:** none
- **Rationale:** The anchor's "Not this" section explicitly states "Particular charting framework requirement" is out of scope. The architecture specifies the SPA as a component with 9 views and 8 key flows per the design spec, with REST API and SSE for real-time updates — all framework-agnostic. The coder selects the framework based on implementation ergonomics. Any modern framework (React, Vue, Svelte, or vanilla web components) satisfies the responsive administration requirement.

### OQ-5 — Deployment packaging [RESOLVED]

- **Question:** Should the system be distributed as a single Docker image (application + SQLite) or as separate containers?
- **Resolution:** Resolved — single container with mounted volume for SQLite database persistence.
- **Routing:** Architect (resolved)
- **Blocking:** no
- **Depends on:** none
- **Rationale:** SQLite is a file-based embedded database — it does not run as a separate service. A multi-container deployment (app + DB) would require a network-accessible database server, which contradicts the SQLite choice. A single container with a mounted volume provides data persistence across container restarts, simple backup (copy the file), and minimal infrastructure — matching the anchor's "minimal infrastructure dependencies" constraint and the single-user deployment model.

## Deferred / Excluded

| Item | Guarantee Impact | Acceptable? |
|---|---|---|
| Multi-provider support | Would require provider abstraction layer | Yes — v1 is YouTube-only per anchor |
| Continuous monitoring | Would require WebSocket/push architecture | Yes — anchor explicitly excludes this |
| ML-based classification | Would require training data pipeline | Yes — anchor explicitly excludes this |
| Autonomous rule optimization | Would violate user authority invariant | Yes — anchor explicitly excludes this |
| Multi-tenancy | Would require tenant isolation in data model | Yes — single-user per anchor |
| Real-time quota from YouTube | YouTube does not expose real-time remaining quota via API | Yes — system tracks consumption locally and infers remaining from daily limit |
| 36-month statistical retention via derived metrics exception | Requires API compliance audit application | Deferred to v2 — local statistics are permanent regardless |
| Auto-refresh of all historical metadata | Would consume significant quota for large corpuses | Yes — refresh-on-demand is sufficient; active records refresh at run time |
