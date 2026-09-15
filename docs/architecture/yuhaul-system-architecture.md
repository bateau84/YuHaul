---
type: design
title: YuHaul System Architecture
description: "System architecture for YuHaul — a personal, stateful discovery and routing system for YouTube subscription content with compliance-aware storage, quota management, and decision explainability."
tags: [architecture, design, yuhaul, youtube, routing]
---

**Status:** accepted

## Derived from

- [Anchor](../anchors/youtube-discovery-routing.md)
- [Requirement Index](../requirements/yuhaul/index.md) (32 BRs + 3 OCs)
- [Design Spec](../design/yuhaul-admin-ui-design.md)
- [Reconciliation Report](../requirements/yuhaul/reconciliation-report.md)
- [YouTube Compliance Analysis](yuhaul-youtube-compliance.md)

## Satisfies

| Requirement | Artifact | Notes |
|---|---|---|
| BR-001 Secure YouTube Account Connection | §OAuth and Credential Storage | OAuth2 flow, token persistence |
| BR-002 Subscription-Based Discovery | §Discovery Subsystem | Polling-based channel feed retrieval |
| BR-003 Multiple Ordered Route Definition | §Data Model, §Processing Pipeline | Route entity with evaluation_order |
| BR-004 Route Conditions and Playlist Actions | §Data Model, §Router Engine | Condition/Action config per route |
| BR-005 Multi-Route Video Matching | §Router Engine | Sequential evaluation, multi-match |
| BR-006 Final Route Termination | §Router Engine | is_final flag, subsequent skips |
| BR-007 Exclusion Behaviors | §Exclusion Gate, §Data Model | Route-scoped exclusions |
| BR-008 Exact and Fuzzy Duplicate Detection | §Duplicate Detection Engine | video_id exact + fingerprint Jaccard |
| BR-009 Historical Title Persistence and Import | §Two-Store Data Model, §Migration | Store B permanent corpus |
| BR-010 Duplicate Provenance for Discovery Credit | §Data Model — DuplicateMatch | source_route_id provenance |
| BR-011 Processing State Persistence | §State Management | Durable three-tier outcome records |
| BR-012 Idempotent Playlist Actions | §State Management | ExecutedAction unique constraint |
| BR-013 Quota-Limited Work Deferral | §Quota Management | Deferral queue, no-false-rejection |
| BR-014 Failed Work Retention and Retry | §State Management | ErrorEntry with retry/dismiss |
| BR-015 Explainable Route Decisions | §Decision Evidence | ConditionAudit per-route trail |
| BR-016 Configurable Scheduled Runs | §Scheduling and Recovery | Internal cron scheduler |
| BR-017 Manual Run Initiation | §API Surface | POST /runs endpoint |
| BR-018 API Consumption Accounting | §Quota Management | QuotaLedger per-operation tracking |
| BR-019 Quota-Aware Processing | §Quota Management | Pre-flight budget check |
| BR-020 Run-Level and Route-Level Statistics | §Statistics | Run/Route computed views |
| BR-021 Source and Channel Usefulness Statistics | §Statistics | ChannelStats entity |
| BR-022 User Retention and Value Signals | §Statistics, §Retention Signals | RetentionSignal entity (contingent on provider data) |
| BR-023 Historical Statistics Over Time | §Statistics | Time-bucketed materialization |
| BR-024 Trend Visualization | §Web Interface | Chart endpoints, time-series API |
| BR-025 Configuration History | §Configuration Versioning | ConfigVersion snapshots linked to runs |
| BR-026 Dry-Run Evaluation | §Processing Pipeline | Dry-run mode, action suppression |
| BR-027 Historical Backtesting | §Backtest Engine | Replay against retained observations |
| BR-028 Historical Run Inspection | §API Surface | Run detail + decision detail endpoints |
| BR-029 Responsive Administration | §Web Interface | SPA with responsive layout |
| BR-030 Temporary Failure Work Preservation | §Quota Management, §State Management | Deferred/error queues preserve work |
| BR-031 User Authority Over Policy Decisions | §Advisory System | Evidence-backed, no autonomous changes |
| BR-032 Processing Outcome Distinction | §Outcome Model | Five primary categories + not-yet-evaluated |
| OC-001 YouTube API Quota and Failure Semantics | §Quota Management | Quota exhaustion = operational state |
| OC-002 Route Evaluation Outcome Semantics | §Outcome Model | Shared vocabulary across all consumers |
| OC-003 Configuration Snapshot for Historical Interpretation | §Configuration Versioning | Immutable snapshots linked to runs |

## Problem

YuHaul is a personal tool that discovers videos from a user's YouTube subscriptions, evaluates them against configurable routing rules, adds qualifying videos to playlists, and measures channel/route effectiveness. It is a rebuild of an existing application with a corpus of 10,000+ historical titles that must be preserved. The new system must support high-volume fuzzy duplicate detection (primary use case: music discovery where the same song appears across many channels under slightly varying titles), manage YouTube's strict API quota (10,000 units/day), provide full decision explainability, comply with YouTube's 30-day metadata retention policy, and deliver temporal analytics — all within a single-user, self-hosted deployment.

Two architectural tensions dominate:

1. **Quota as first-class constraint.** YouTube's quota model makes the external API a scarce, metered resource. Every subsystem that touches the YouTube API must be aware of remaining budget and able to defer work without data loss.

2. **Metadata retention compliance.** YouTube's Developer Policies (§III.E.4) require most stored API metadata to be deleted or refreshed after 30 calendar days. YuHaul's core value depends on long-term title storage for duplicate detection. The storage model must cleanly separate API-sourced metadata (transient) from YuHaul-owned operational data (permanent).

## Constraints

- **Single-user personal tool.** No multi-tenancy, no shared state, no concurrent users.
- **YouTube Data API v3 quota.** Default 10,000 units/day. Varying costs per method.
- **YouTube Developer Policies §III.E.4.** API metadata subject to 30-day refresh/deletion. Statistics exempt with periodic reauthorization.
- **Historical data migration.** 10,000+ titles from prior installation. API-originated titles transformed to fingerprints during migration; raw titles discarded after successful transformation.
- **Polling, not streaming.** Scheduled/on-demand discovery, not continuous monitoring.
- **No autonomous policy changes.** System advises; user decides.
- **Responsive web interface.** Desktop and mobile. SPA with URL state.
- **Deployment.** Single-user self-hosted, Docker container, minimal infrastructure.

## Forces

| Force | Direction | Tension |
|---|---|---|
| **Compliance** | Two-store separation | More complex schema, metadata lifecycle management |
| **Quota economy** | Pre-flight budget checks, priority allocation | Deferred work must never be lost |
| **Duplicate detection quality** | Fingerprint-first with FTS5 search | Corpus must be permanent and complete |
| **Decision explainability** | ConditionAudit per-route | Storage cost per evaluation |
| **Operational simplicity** | Single container, SQLite | No horizontal scaling, single-writer |
| **Migration fidelity** | Identical schema for imported/native titles | Must not break existing corpus |
| **User authority** | Advisory system without autonomous action | More UI complexity for manual decision loop |

## Outcome Model

The outcome model satisfies OC-002's shared vocabulary and BR-032's distinction requirements. It operates at three tiers:

### Tier 1: Video Processing State

Tracks overall video lifecycle through the pipeline:

| State | Meaning |
|---|---|
| `discovered` | Video identified by discovery phase, not yet persisted to processing queue |
| `not_yet_evaluated` | Persisted to processing queue, awaiting evaluation |
| `evaluating` | Currently being processed through duplicate detection and routing |
| `evaluated` | Routing complete, awaiting action execution |
| `acted` | Action execution in progress |
| `completed` | All actions succeeded, video fully processed |
| `deferred` | Processing halted due to operational constraint (quota, auth) |
| `failed` | Processing halted due to unrecoverable error |

### Tier 2: Route Decision (per-route)

Each route evaluation produces one decision:

| Decision | Meaning |
|---|---|
| `matched` | Video satisfied this route's conditions |
| `not_matched` | Video evaluated but conditions not satisfied |
| `skipped` | Video skipped — see skip_reason |
| `deferred` | Route evaluation halted for this video (quota, metadata unavailable) |

Skip reasons: `exclusion` (route-scoped exclusion gate), `disabled` (route disabled), `final_route_matched` (earlier route with is_final matched).

### Tier 3: Action Execution (per-action)

Each action execution produces one outcome:

| Outcome | Meaning |
|---|---|
| `succeeded` | Action completed successfully |
| `failed` | Action attempted and failed |
| `not_attempted` | Action skipped due to dry-run mode or prior failure |
| `deferred` | Action deferred due to quota or temporary failure |

### Primary Outcome Classification (OC-002)

Every processed video receives exactly one primary outcome:

| Category | Definition |
|---|---|
| **accepted** | Matched one or more route conditions and action executed (or simulated in dry-run) |
| **rejected** | Evaluated against all applicable routes, matched no conditions, not excluded |
| **excluded** | Prevented from route evaluation by an exclusion rule (channel, video, title, word/phrase, duration) |
| **deferred** | Not evaluated due to operational constraint (quota exhaustion, auth pending) |
| **failed** | Evaluation or action execution attempted and encountered unrecoverable failure |
| **not_yet_evaluated** | Discovered but not yet reached evaluation — never classified as rejected |

**Critical invariant (OC-002, BR-032):** Absence of processing is never represented as a negative routing decision. A video in `not_yet_evaluated` state is not rejected, excluded, or deferred.

**Reconciliation note (C-1):** The three-tier model satisfies OC-002's requirement that "excluded" be a distinct category from "rejected." Exclusion occurs at the route-scoped exclusion gate before route condition evaluation, producing a `RouteDecision(skip_reason=exclusion)`. The primary outcome classification for an excluded video is `excluded`, not `rejected`. This distinction is surfaced in run statistics and the inspection UI.

## Invariants and Guarantees

### INV-1: No Silent Data Loss

**Guarantee:** No eligible video is silently dropped from processing. Every discovered video reaches one of six terminal states: accepted, rejected, excluded, deferred, failed, or not-yet-evaluated.

**Precondition:** Discovery persists discovered video identifiers before downstream processing.

**Component:** Discovery subsystem → Video entity (processing_state = not_yet_evaluated).

**Falsification test:** After a run, count(discovered) = count(accepted) + count(rejected) + count(excluded) + count(deferred) + count(failed) + count(not_yet_evaluated). Any gap violates the invariant.

### INV-2: Quota Exhaustion Is Operational State (OC-001)

**Guarantee:** Quota exhaustion never produces a false rejection. Videos not processed due to quota are deferred.

**Precondition:** Quota ledger accurately tracks remaining budget before each API call.

**Component:** QuotaLedger entity → pre-flight budget check.

**Falsification test:** Exhaust quota mid-run; verify deferred videos appear in next run's queue, not in rejected set.

### INV-3: Idempotent Playlist Actions (BR-012)

**Guarantee:** Re-executing a run does not duplicate playlist insertions.

**Precondition:** Action completion recorded with (run_id, video_id, playlist_id, action_type) identity.

**Component:** ExecutedAction entity with denormalized run_id/video_id, unique constraint on (run_id, video_id, target_playlist_id, action_type).

**Falsification test:** Execute a run, then re-execute. Playlist item count unchanged.

### INV-4: Decision Evidence Completeness (BR-015)

**Guarantee:** Every routing outcome is explainable from stored evidence without external log analysis.

**Precondition:** Router records condition evaluation results for every route evaluated against every video.

**Component:** ConditionAudit entity → per-route condition audit trail.

**Falsification test:** For any video outcome in any historical run, the user can trace the exact route evaluation sequence and condition results.

### INV-5: Configuration-Statistics Attribution (OC-003, BR-025)

**Guarantee:** Every run's statistics are interpretable in the context of the configuration version active during that run.

**Precondition:** Configuration snapshot created whenever routes change; each run records its active config version.

**Component:** ConfigVersion entity → Run.config_version_id foreign key.

**Falsification test:** Modify routes, run, inspect historical run — route conditions shown match configuration at run time, not current.

### INV-6: Historical Corpus Integrity (BR-008, BR-009)

**Guarantee:** Imported historical titles produce identical duplicate detection results as natively captured titles.

**Precondition:** Fingerprint corpus schema identical for imported and native entries.

**Component:** TitleFingerprint entity — no schema distinction between sources.

**Falsification test:** Import historical titles, run duplicate detection against known duplicate — result matches expected similarity.

### INV-7: Distinct Outcome States (BR-032, OC-002)

**Guarantee:** The system distinguishes six outcome categories and never represents absence of processing as a negative routing decision.

**Precondition:** Video processing state set to not_yet_evaluated at discovery; transitions only through explicit processing.

**Component:** Three-tier model: (a) VideoProcessingState, (b) RouteDecision, (c) ActionOutcome.

**Falsification test:** A never-evaluated video shows as not_yet_evaluated, not rejected. A video excluded by every route shows as excluded, not rejected. A skipped-by-final-route video shows as matched at its route level, not not_yet_evaluated.

### INV-8: Dry-Run, Replay, and Backtest Isolation (BR-026, BR-027)

**Guarantee:** Non-production modes produce routing evaluations without mutating external state, production duplicate corpus, production idempotency records, or production statistics.

**Precondition:** Action execution gated by mode flag suppressing external API calls and production state writes.

**Component:** Run mode enum {production, dry_run, replay, backtest} → action executor gate, state write gate.

**Falsification test:** Run dry-run; verify: (a) no playlist changes on YouTube, (b) production corpus unchanged, (c) production statistics unchanged.

### INV-9: API Metadata Compliance

**Guarantee:** No raw YouTube API metadata retained beyond 30 calendar days without refresh, except statistics exemption. Migration-time raw titles transformed to fingerprints and discarded.

**Precondition:** Every API-sourced record carries fetched_at timestamp.

**Component:** TransientMetadata store → metadata lifecycle manager.

**Falsification test:** Query transient store for records with fetched_at > 30 days ago not refreshed. Count must be zero.

## Options Considered

### Option A: Compliance-Aware Two-Store Architecture (Recommended)

Separate storage into two logical stores within a single SQLite database:

- **Store A (Transient):** Raw API metadata with 30-day TTL. Refreshed on demand or at run time.
- **Store B (Permanent):** YuHaul-owned data — fingerprints, outcomes, evidence, statistics, configuration history.

Duplicate detection operates on fingerprints computed at discovery time. Raw titles cached transiently for display but not the matching substrate.

**Pros:** Compliant with YouTube Developer Policies; all anchor behaviors work on Store B data; clear audit boundary; migrated corpus fingerprints permanently available; quota-cheap metadata refresh.

**Cons:** Historical video titles degrade in UI after 30 days unless refreshed; added complexity from two-store coordination.

**Risks:** Fingerprint storage may be challenged under strict §III.E.4.h reading (low enforcement risk for single-user tool).

### Option B: Full Metadata Retention (Non-Compliant)

Store all API metadata permanently.

**Pros:** Simplest implementation; full title display always.

**Cons:** Violates YouTube Developer Policies §III.E.4.c; risk of credential revocation.

**Risks:** Disqualifying — policy violation with active enforcement.

### Option C: API-Free Discovery (Avoidance)

Use RSS feeds or scraping instead of YouTube Data API.

**Pros:** No API retention policies apply.

**Cons:** Disqualifying — violates Developer Policies §III.D.7 (scraping prohibition); loses authenticated API functionality.

**Risks:** ToS violation, unreliable data, no write operations.

### Single-Option Clause

Options B and C are disqualified by policy violations. Option A is the only viable approach.

## Decision

**Option A: Compliance-Aware Two-Store Architecture.**

Key design decisions:

1. **Fingerprint-at-discovery.** Compute normalized title fingerprint immediately upon API retrieval; store in Store B (permanent); allow raw title to follow Store A's 30-day lifecycle.
2. **Fingerprint as matching substrate.** All duplicate detection operates on fingerprints and video IDs, never on raw titles.
3. **Metadata refresh on demand.** Raw metadata refreshed when user views historical evidence or when retention signal monitoring requires current data. Routed videos refreshed at run time during processing.
4. **Migration-time fingerprint transformation.** Historical titles read from source SQLite, transformed to fingerprints, stored permanently in Store B. Raw API-originated titles discarded after successful transformation.
5. **Route-scoped exclusions.** RouteExclusion is scoped to individual routes, not global. Each route defines its own exclusion rules.
6. **ConfigVersion as JSON snapshot.** Full snapshot of effective configuration on each change. Simpler and more reliable than diff-based versioning.
7. **SSE for real-time progress.** Server-Sent Events via `/api/runs/:id/stream` for run progress. Chosen over WebSocket for simplicity in a single-user context where bidirectional communication is unnecessary.

## Structure and Flows

### Component Diagram

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
│  │  │ Discovery│→│Duplicate │→│ Exclusion│→│    Router    │ │ │
│  │  │ Phase    │ │Detection │ │   Gate   │ │    Engine    │ │ │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────┬───────┘ │ │
│  │                                                 │         │ │
│  │                                      ┌──────────▼───────┐ │ │
│  │                                      │   Action         │ │ │
│  │                                      │   Executor       │ │ │
│  │                                      └──────────────────┘ │ │
│  └────────────────────────────────────────────────────────────┘ │
│       │              │              │              │             │
│       ▼              ▼              ▼              ▼             │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │                    Storage Layer                            │ │
│  │  ┌─────────────┐  ┌──────────────┐  ┌──────────────────┐  │ │
│  │  │  Store A    │  │   Store B    │  │  Quota Ledger    │  │ │
│  │  │  (Transient │  │  (Permanent  │  │  (Daily Budget)  │  │ │
│  │  │   API Meta) │  │   YuHaul     │  │                  │  │ │
│  │  │             │  │   Data)      │  │                  │  │ │
│  │  └─────────────┘  └──────────────┘  └──────────────────┘  │ │
│  └────────────────────────────────────────────────────────────┘ │
│       │              │              │              │             │
│       ▼              ▼              ▼              ▼             │
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
| **Scheduler** | Internal cron; triggers scheduled runs; manages run lifecycle |
| **API Server** | REST endpoints + SSE for real-time run progress; serves web SPA; authentication middleware |
| **Discovery Engine** | Retrieves subscription list and recent video feeds; persists discovered videos |
| **Metadata Lifecycle** | Manages Store A TTL; refreshes active metadata; expires stale records |
| **Processing Pipeline** | Orchestrates: discovery → duplicate detection → exclusion gate → routing → action execution |
| **Duplicate Detection Engine** | Exact match (video_id) + fuzzy match (fingerprint similarity) against Store B corpus |
| **Exclusion Gate** | Route-scoped exclusion filtering before condition evaluation |
| **Router Engine** | Evaluates videos against ordered routes; records decision evidence; respects final-route termination |
| **Action Executor** | Executes playlist insertions via YouTube API; idempotency guards; handles failures and deferrals |
| **Statistics Engine** | Computes run/route/channel statistics; materializes time-bucketed aggregates |
| **Backtest Engine** | Offline evaluation against retained observations; state isolation from production |
| **Configuration Versioner** | Snapshots route configuration on change; links runs to active config version |
| **Quota Ledger** | Tracks per-operation API costs; pre-flight budget checks; remaining capacity reporting |
| **YouTube API Client** | Thin wrapper around YouTube Data API v3; handles auth, retries, rate limiting |
| **Storage Layer** | SQLite with FTS5 for fingerprint search; two-store separation enforced at schema level |
| **Web SPA** | Single-page application matching designer spec; responsive layout |

### Data Model

#### Store A: Transient API Metadata

```
┌──────────────────────────────────────┐
│ TransientMetadata                    │
│──────────────────────────────────────│
│ video_id TEXT PRIMARY KEY            │
│ title TEXT                          │
│ channel_name TEXT                   │
│ description TEXT                    │
│ published_at TIMESTAMP              │
│ duration_seconds INTEGER            │
│ thumbnail_url TEXT                  │
│ fetched_at TIMESTAMP NOT NULL       │
│ refreshed_at TIMESTAMP              │
│ expires_at TIMESTAMP NOT NULL       │
│ is_active BOOLEAN DEFAULT true      │
└──────────────────────────────────────┘
```

Lifecycle: Records created at discovery. `expires_at = fetched_at + 30 days`. Metadata lifecycle refreshes active records before expiry. Expired records deleted or flagged (title set to NULL, fingerprint reference preserved in Store B).

#### Store B: Permanent YuHaul Data

```
┌──────────────────────────────────────┐
│ TitleFingerprint                     │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id TEXT NOT NULL UNIQUE        │
│ fingerprint TEXT NOT NULL            │
│ fingerprint_tokens TEXT             │  ← FTS5-indexed token set
│ channel_id TEXT                      │
│ source TEXT NOT NULL                 │  ← 'api_discovery' | 'user_import'
│ first_seen_at TIMESTAMP NOT NULL     │
│ is_duplicate_reference BOOLEAN       │
│ import_batch_id TEXT                 │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Video                                │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id TEXT NOT NULL UNIQUE        │
│ channel_id TEXT NOT NULL             │
│ discovered_at TIMESTAMP NOT NULL     │
│ discovered_in_run_id INTEGER        │
│ processing_state TEXT NOT NULL       │  ← enum: discovered, not_yet_evaluated,
│                                      │     evaluating, evaluated, acted,
│                                      │     completed, deferred, failed
│ final_outcome TEXT                   │  ← enum: accepted, rejected, excluded,
│                                      │     deferred, failed, not_yet_evaluated
│ deferred_reason TEXT                 │
│ error_message TEXT                   │
│ completed_at TIMESTAMP              │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Run                                  │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ started_at TIMESTAMP NOT NULL        │
│ completed_at TIMESTAMP              │
│ run_type TEXT NOT NULL               │  ← 'scheduled' | 'manual' | 'dry_run' |
│                                      │     'replay' | 'backtest'
│ status TEXT NOT NULL                 │  ← 'running' | 'completed' | 'interrupted'
│ config_version_id INTEGER           │
│ videos_discovered INTEGER DEFAULT 0  │
│ videos_accepted INTEGER DEFAULT 0    │
│ videos_rejected INTEGER DEFAULT 0    │
│ videos_excluded INTEGER DEFAULT 0    │
│ videos_deferred INTEGER DEFAULT 0    │
│ videos_failed INTEGER DEFAULT 0      │
│ videos_not_yet_evaluated INTEGER     │
│ quota_cost INTEGER DEFAULT 0         │
│ duration_seconds REAL               │
│ interrupt_reason TEXT               │
│ current_phase TEXT                  │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteDecision                        │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │  ← FK → Video
│ run_id INTEGER NOT NULL             │  ← FK → Run
│ route_id INTEGER NOT NULL           │  ← FK → Route
│ evaluation_order INTEGER NOT NULL   │
│ decision TEXT NOT NULL               │  ← 'matched' | 'not_matched' |
│                                      │     'deferred' | 'skipped'
│ skip_reason TEXT                    │  ← 'exclusion' | 'disabled' |
│                                      │     'final_route_matched'
│ rejection_reason TEXT               │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ConditionAudit                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_decision_id INTEGER NOT NULL  │
│ condition_index INTEGER NOT NULL    │
│ field TEXT NOT NULL                  │
│ operator TEXT NOT NULL              │
│ expected_value TEXT NOT NULL        │
│ actual_value TEXT                   │
│ matched BOOLEAN NOT NULL            │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ExecutedAction                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_decision_id INTEGER NOT NULL  │
│ run_id INTEGER NOT NULL             │  ← denormalized for constraint
│ video_id INTEGER NOT NULL           │  ← denormalized for constraint
│ action_type TEXT NOT NULL           │
│ target_playlist_id TEXT NOT NULL    │
│ outcome TEXT NOT NULL               │  ← 'succeeded' | 'failed' |
│                                      │     'not_attempted' | 'deferred'
│ error_message TEXT                  │
│ executed_at TIMESTAMP               │
│ UNIQUE(run_id, video_id, target_playlist_id, action_type)
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ DuplicateMatch                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │
│ matched_fingerprint_id INTEGER      │
│ similarity_score REAL NOT NULL      │
│ match_type TEXT NOT NULL            │  ← 'exact_video_id' | 'fuzzy_title'
│ source_route_id INTEGER             │
│ threshold_used REAL NOT NULL        │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Route                                │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ name TEXT NOT NULL                   │
│ description TEXT                    │
│ evaluation_order INTEGER NOT NULL   │
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
│ route_id INTEGER NOT NULL           │
│ condition_index INTEGER NOT NULL    │
│ field TEXT NOT NULL                  │
│ operator TEXT NOT NULL              │
│ value TEXT NOT NULL                  │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteAction                          │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │
│ action_type TEXT NOT NULL           │
│ target_playlist_id TEXT NOT NULL    │
│ action_index INTEGER NOT NULL       │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteExclusion                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │
│ exclusion_type TEXT NOT NULL        │  ← 'channel' | 'video' | 'title' |
│                                      │     'word' | 'duration_min' | 'duration_max'
│ exclusion_value TEXT NOT NULL       │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ConfigVersion                        │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ created_at TIMESTAMP NOT NULL        │
│ snapshot JSON NOT NULL              │
│ change_description TEXT             │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ QuotaLedger                          │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ date TEXT NOT NULL                   │
│ operation_type TEXT NOT NULL        │
│ units_consumed INTEGER NOT NULL     │
│ call_count INTEGER NOT NULL         │
│ run_id INTEGER                      │
│ recorded_at TIMESTAMP NOT NULL       │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ ErrorEntry                           │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │
│ run_id INTEGER NOT NULL             │
│ error_category TEXT NOT NULL        │
│ error_message TEXT NOT NULL          │
│ error_detail TEXT                   │
│ status TEXT NOT NULL                │
│ created_at TIMESTAMP NOT NULL        │
│ resolved_at TIMESTAMP               │
│ retry_count INTEGER DEFAULT 0       │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ DeferredItem                         │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id INTEGER NOT NULL           │
│ run_id INTEGER NOT NULL             │
│ deferral_reason TEXT NOT NULL       │
│ retry_after TIMESTAMP               │
│ retry_count INTEGER DEFAULT 0       │
│ status TEXT NOT NULL                │
│ created_at TIMESTAMP NOT NULL        │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RetentionPlaylist                    │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ playlist_id TEXT NOT NULL UNIQUE    │
│ playlist_name TEXT NOT NULL         │
│ designated_at TIMESTAMP NOT NULL    │
│ un_designated_at TIMESTAMP          │
│ is_active BOOLEAN DEFAULT true      │
│ last_checked_at TIMESTAMP           │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RetentionSignal                      │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ video_id TEXT NOT NULL               │
│ playlist_id TEXT NOT NULL           │
│ membership_source TEXT NOT NULL     │  ← 'yuhaul_routed' | 'user_driven' | 'unknown'
│ detected_at TIMESTAMP NOT NULL      │
│ is_retention_signal BOOLEAN NOT NULL│
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Channel                              │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ channel_id TEXT NOT NULL UNIQUE     │
│ channel_name TEXT                   │
│ first_discovered_at TIMESTAMP       │
│ last_discovered_at TIMESTAMP        │
│ total_discovered INTEGER DEFAULT 0  │
│ total_routed INTEGER DEFAULT 0      │
│ total_retained INTEGER DEFAULT 0    │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Advisory                             │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ channel_id INTEGER NOT NULL         │
│ advisory_type TEXT NOT NULL         │
│ generated_at TIMESTAMP NOT NULL     │
│ status TEXT NOT NULL                │
│ ignored_until TIMESTAMP             │
│ action_taken TEXT                   │
│ dismissed_at TIMESTAMP              │
│ acted_upon_at TIMESTAMP             │
│ evidence_snapshot JSON              │
└──────────────────────────────────────┘
```

#### Entity Relationships

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

### Processing Pipeline

#### Run Lifecycle

Five processing phases, each atomic with respect to state transitions. A configuration snapshot is captured when the run is created, before Phase 1; it is not itself a processing phase:

```
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│ Phase 1  │────▶│ Phase 2  │────▶│ Phase 3  │────▶│ Phase 4  │────▶│ Phase 5  │
│ Discovery│     │ Duplicate│     │ Exclusion│     │ Routing  │     │  Action  │
│          │     │ Detection│     │   Gate   │     │          │     │Execution │
└──────────┘     └──────────┘     └──────────┘     └──────────┘     └──────────┘
```

**Run Initiation: Configuration Snapshot**

Captured when the run is created, before the processing phases begin:

1. Capture current route configuration as immutable snapshot (OC-003)
2. Create ConfigVersion record with full JSON snapshot
3. Link Run to config_version_id
4. If snapshot fails, defer the run (OC-003 guarantee 6)

**Phase 1: Discovery**

1. Retrieve subscription list via `subscriptions.list` (1 unit/page)
2. For each channel, retrieve uploads playlist ID via `channels.list` (1 unit, cached), fetch recent uploads via `playlistItems.list` (1 unit/page)
3. Filter to videos published since last discovery watermark
4. For each new video: create Video record with processing_state = not_yet_evaluated
5. Pre-fetch metadata via `videos.list` (1 unit per 50 IDs)
6. Store raw metadata in TransientMetadata (Store A)
7. Compute title fingerprint → store in TitleFingerprint (Store B)
8. Record quota cost in QuotaLedger

**Phase 2: Duplicate Detection**

1. Exact match: TitleFingerprint with same video_id where is_duplicate_reference = true
2. Fuzzy match: FTS5 query against fingerprint_tokens, compute Jaccard similarity
3. If similarity exceeds threshold → flag as DuplicateMatch
4. No external API calls — all data local

**Phase 3: Exclusion**

Global exclusion gate, applied across all discovered videos after duplicate detection:

1. Load all enabled routes and their route-scoped exclusion rules.
2. For each video, evaluate it against every enabled route's exclusions (channel, video, title, word/phrase, duration).
3. If excluded by every enabled route → record RouteDecision(skip_reason=exclusion) for each route, transition to evaluated with final_outcome = `excluded`, and remove from further processing. This produces the distinct "excluded" category required by OC-002.
4. Videos excluded by some-but-not-all routes remain eligible and proceed to routing.

**Phase 4: Routing**

For each video that passed the exclusion gate:

1. **Metadata budget check:** Count videos requiring condition evaluation with expired metadata. Estimate re-fetch cost. If insufficient, defer videos with expired metadata.
2. **Route evaluation:** Load active route configuration (or draft config for dry-run). For each route in order:
   a. Check exclusions → if excluded by this route, record RouteDecision(skip_reason=exclusion)
   b. Check enabled → if disabled, record RouteDecision(skip_reason=disabled)
   c. Evaluate conditions (AND/OR composition)
   d. Record ConditionAudit for each condition
   e. If conditions match → record RouteDecision(decision=matched)
   f. If is_final → skip remaining routes with RouteDecision(skip_reason=final_route_matched)
3. Set Video.processing_state = evaluated (or deferred if quota prevents action execution)

**Phase 5: Action Execution**

For each video with at least one matched route:

1. Pre-flight budget check: estimate action cost (50 units per insert)
2. If insufficient → defer video and all remaining
3. For each matched route's actions:
   a. Check idempotency: has this (video_id, playlist_id) already been inserted? → skip
   b. Execute playlistItems.insert (50 units)
   c. Record ExecutedAction with outcome
   d. On failure: categorize, add to ErrorEntry
4. If all actions succeeded → Video.processing_state = completed, final_outcome = accepted
5. Update corpus: set TitleFingerprint.is_duplicate_reference = true if routed to playlist

#### Mode Variations

| Mode | Snapshot | Discovery | Duplicate | Exclusion | Routing | Action | State Writes |
|---|---|---|---|---|---|---|---|
| **Production** | Full | Full | Full | Full | Full | Full (external mutations) | All production state |
| **Dry-run** | Full (or draft config) | Full | Full | Full | Full | Simulated | Run/decision records only |
| **Replay** | Full (current or historical config) | Uses retained observations | Full | Full | Full | Simulated | Replay records only |
| **Backtest** | Full (proposed config) | Uses retained observations | Full | Full | Full | Simulated | Backtest records only |

**Isolation guarantee (INV-8):** Non-production modes never mutate external state, production corpus, production idempotency records, or production statistics.

Replay and backtest modes query retained observations for duplicate detection, not the production corpus.

**Dry-run scope (BR-026):** Dry-run protects external YouTube state (no playlist mutations). Read-only API calls (video listing, channel enumeration) may execute to obtain metadata. Internal state changes (corpus, statistics) are also suppressed as a conservative design decision beyond the minimum anchor requirement — this prevents analytics pollution from hypothetical evaluations.

### Quota Management

#### Quota Model

| Operation | Method | Unit Cost |
|---|---|---|
| List subscriptions | `subscriptions.list` | 1 per page |
| List channel uploads | `playlistItems.list` | 1 per call |
| Get video metadata | `videos.list` | 1 per call (up to 50 IDs) |
| Get channel info | `channels.list` | 1 per call |
| Insert playlist item | `playlistItems.insert` | 50 per call |
| List playlist items | `playlistItems.list` | 1 per call |

#### Pre-Flight Budget Check

Before each quota-consuming phase:

1. Query QuotaLedger for today's consumed units
2. Estimate cost for the phase
3. If consumed + estimated > daily_limit → defer remaining work
4. If consumed + estimated > configurable_reserve_threshold → defer and warn

#### Priority-Aware Allocation

When quota is limited:

1. **Discovery** (lowest cost, highest information) — always attempt
2. **Metadata fetch** (1 unit per 50 videos) — always attempt after discovery
3. **Routing evaluation** (no API cost) — always complete
4. **Action execution** (50 units per insert) — deferred first
5. **Metadata refresh** (1 unit per 50 videos) — lowest priority

#### Quota Reset Detection

System does NOT assume midnight Pacific reset:

1. On first API call of a run, check if API reports quota reset via response headers
2. If API provides reset timing, use it
3. If not, reset daily ledger at provider-documented reset time
4. Track actual vs expected reset to detect provider-side changes

### Decision Evidence

Every route decision produces a complete evidence trail:

```
Video (video_id, channel_id, processing_state, final_outcome)
  └── RouteDecision (route_id, decision, evaluation_order)
       ├── ConditionAudit (field, operator, expected, actual, matched)
       ├── ConditionAudit ...
       └── ExecutedAction (action_type, target, outcome, error_message)
```

**Evidence display with expired metadata:** When TransientMetadata has expired, the evidence structure is always available from Store B. Displayable values degrade to fingerprint or "metadata expired — refresh available." One-click refresh re-fetches metadata (1 unit per 50 videos).

### Duplicate Detection Engine

#### Two-Pass Algorithm

**Pass 1: Exact match.** Query TitleFingerprint for video_id match where is_duplicate_reference = true. O(1) via index.

**Pass 2: Fuzzy match.** If no exact match:

1. Tokenize new video's fingerprint into tokens
2. FTS5 query: find candidates sharing ≥N tokens
3. Compute normalized-token-set Jaccard similarity: |A ∩ B| / |A ∪ B|
4. If any candidate exceeds threshold (default 0.5) → flag as duplicate

#### Fingerprint Design

One-way, lossy transformation for music-title matching:

1. Lowercase the title
2. Strip punctuation, special characters, parenthetical annotations
3. Remove common music stop words
4. Tokenize into sorted word set
5. Store both canonical fingerprint string and token set (for FTS5 indexing)

#### Similarity Algorithm

**Decision:** Normalized-token-set Jaccard similarity as sole fuzzy-matching metric.

**Threshold:** Default 0.5, user-configurable 0.0–1.0.

**Rationale:** Researcher benchmarked against 10 duplicate groups (147 positive, 181 negative pairs). Token-set Jaccard achieved F1=0.990 with zero false positives at threshold 0.572. Selected over char-trigram cosine (F1=0.993, 2 false positives) because zero false positives is safer for music discovery where false positive silently skips a distinct video.

**Corpus scope:** Only titles from videos routed to at least one playlist have is_duplicate_reference = true. Rejected, excluded, failed, or unevaluated videos are in corpus but not eligible as references.

### Statistics and Analytics

#### Three-Level Granularity

| Level | Entity | Scope | Retention |
|---|---|---|---|
| **Per-run raw** | Run, RouteDecision, ExecutedAction | Individual run data | Permanent |
| **Materialized aggregates** | Channel (counters); Run/Route computed views | Period summaries | Permanent |
| **Temporal series** | Time-bucketed queries on raw data | Trend visualization | Derived from permanent raw |

#### Run Statistics

Computed from Run entity's denormalized counters:

- Counts by outcome: accepted, rejected, excluded, deferred, failed, not_yet_evaluated
- Rejection counts by reason (from RouteDecision.rejection_reason/skip_reason)
- Quota cost by operation type (from QuotaLedger filtered by run_id)
- Duration and configuration version

#### Route Statistics

Computed views aggregated from RouteDecision records filtered by route_id and run_id.

Per-route, per-run: videos evaluated, matched (with condition patterns), not-matched (with rejection breakdown), skipped (final-route or exclusion).

#### Channel Statistics

Per-channel, cumulative and per-period:

- Videos discovered, routed, rejected (with reason breakdown), retained
- Usefulness rate: retained / discovered
- Yield: routed / discovered
- Time since last useful discovery

**Channel counter maintenance:** Denormalized counters updated atomically within processing pipeline:
- `total_discovered` incremented during Phase 1 (Discovery)
- `total_routed` incremented during Phase 5 (Action Execution) when action succeeds
- `total_retained` updated during retention signal monitoring

#### Retention Signals (BR-022)

Retention signals measured from designated retention playlists:

- Periodically check playlist membership for previously routed videos
- Distinguish `yuhaul_routed` from `user_driven` membership
- Only `user_driven` counts as retention signal
- Supplementary provider signals (watch time, engagement) captured where available; graceful degradation when unavailable

De-designating a playlist stops future membership checks but preserves historical RetentionSignal records for statistical integrity.

**Data availability contingency:** BR-022 specifies retention signals are measured "where reliable provider evidence permits." The retention signal model is designed to degrade gracefully: if YouTube does not provide retention-quality signals beyond playlist membership, the system falls back to membership-only provenance tracking. The `membership_source` field distinguishes YuHaul-routed from user-driven membership regardless of what supplementary signals are available.

### Advisory System

#### Advisory Generation

Evidence-backed recommendations computed from channel statistics at end of each production run:

- **Low usefulness:** Usefulness rate below configurable threshold over configurable window
- **Declining yield:** Yield rate decreased significantly from historical baseline
- **High duplicate rate:** Channel's videos frequently match existing corpus

Each advisory captures metrics at generation time in `evidence_snapshot` (JSON).

#### User Actions (Design Decisions D-1, D-2)

Per the design spec, three actions per advisory:

1. **Take action:** User manually acts, logs action. Status → `acted_upon`.
2. **Dismiss:** Permanent acknowledge. Status → `dismissed`. Never reappears.
3. **Ignore for 30 days:** Temporary hide. Status → `ignored`, `ignored_until` set. Reappears when expired and condition persists.

These are design-level behaviors supporting BR-031's user authority requirement. They are not Anchor-entailed obligations.

### Configuration Versioning

Every route configuration change creates a ConfigVersion with full JSON snapshot of effective configuration. Each Run records config_version_id, enabling reconstruction at any historical point.

Diffs are computed as structural JSON comparison of the snapshot objects, highlighting added, removed, and modified routes and their conditions.

**Config diff mechanism:** The diff endpoint (`GET /api/config/versions/:id/diff`) computes differences between two ConfigVersion snapshots using **JSON structural comparison** (field-level diff of the serialized snapshot). This is a mechanical comparison of the stored JSON — it highlights added, removed, and modified fields at every level of the route configuration object. It is *not* a semantic route diff that interprets routing meaning (e.g., two routes with different names but identical conditions would still show as distinct). The diff is a display and audit tool for configuration history, not an analytical tool for route equivalence.

### API Surface

#### REST Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/overview` | Dashboard summary |
| `POST` | `/api/runs` | Initiate manual run |
| `POST` | `/api/runs/dry-run` | Dry-run (optional draft_configuration body) |
| `POST` | `/api/runs/replay` | Replay (optional config_version_id) |
| `GET` | `/api/runs` | List runs (filterable) |
| `GET` | `/api/runs/:id` | Run detail |
| `GET` | `/api/runs/:id/videos` | Video outcomes for run |
| `GET` | `/api/runs/:id/videos/:videoId` | Decision detail (three-tier evidence) |
| `GET` | `/api/routes` | List routes (ordered) |
| `POST` | `/api/routes` | Create route |
| `GET` | `/api/routes/:id` | Route detail |
| `PUT` | `/api/routes/:id` | Update route (creates ConfigVersion) |
| `DELETE` | `/api/routes/:id` | Delete route |
| `PUT` | `/api/routes/reorder` | Update evaluation order |
| `GET` | `/api/analytics/trends` | Temporal trend data |
| `GET` | `/api/analytics/channels` | Channel statistics |
| `GET` | `/api/analytics/channels/:id` | Channel detail with advisories |
| `GET` | `/api/analytics/channels/:id/duplicate-provenance` | Duplicate provenance breakdown |
| `GET` | `/api/analytics/channels/:id/advisories` | Advisory list for channel |
| `GET` | `/api/analytics/advisories` | All active advisories |
| `POST` | `/api/analytics/advisories/:id/dismiss` | Dismiss advisory |
| `POST` | `/api/analytics/advisories/:id/ignore` | Ignore 30 days |
| `POST` | `/api/analytics/advisories/:id/act` | Log user action |
| `GET` | `/api/analytics/quota` | Quota ledger |
| `GET` | `/api/errors` | Error queue |
| `POST` | `/api/errors/:id/retry` | Retry failed item |
| `POST` | `/api/errors/:id/dismiss` | Dismiss (preserves history) |
| `GET` | `/api/deferred` | Deferred work queue |
| `POST` | `/api/deferred/:id/retry` | Retry deferred |
| `POST` | `/api/deferred/:id/discard` | Discard (preserves history) |
| `POST` | `/api/backtest` | Backtest with proposed config |
| `GET` | `/api/backtest/:id` | Backtest results |
| `GET` | `/api/config/versions` | Configuration history |
| `GET` | `/api/config/versions/:id` | Specific snapshot |
| `GET` | `/api/config/versions/:id/diff` | Diff between versions |
| `POST` | `/api/config/versions/:id/restore` | Restore historical config |
| `GET` | `/api/settings` | Current settings |
| `PUT` | `/api/settings` | Update settings |
| `GET` | `/api/settings/duplicate-threshold` | Current threshold |
| `PUT` | `/api/settings/duplicate-threshold` | Update threshold (creates ConfigVersion) |
| `GET` | `/api/settings/retention-playlists` | Designated playlists |
| `PUT` | `/api/settings/retention-playlists` | Update designations |
| `POST` | `/api/corpus/import` | Import historical corpus |
| `GET` | `/api/corpus/export` | Export current corpus |
| `GET` | `/api/account/status` | Account status |
| `POST` | `/api/auth/initiate` | Begin OAuth2 |
| `GET` | `/api/auth/callback` | OAuth2 callback |
| `DELETE` | `/api/account` | Disconnect account |
| `GET` | `/api/runs/:id/stream` | SSE stream for run progress |

#### Server-Sent Events (SSE)

Real-time progress via SSE at `/api/runs/:id/stream`. Chosen over WebSocket for architectural simplicity: the communication is unidirectional (server → client), the single-user context eliminates connection multiplexing concerns, and SSE works over standard HTTP without protocol upgrade negotiation.

Events: `run.started`, `run.phase`, `run.video.processed`, `run.quota.warning`, `run.completed`, `run.error`.

### Scheduling and Recovery

#### Internal Scheduler

- User configures schedule via Settings (cron expression or simplified interval)
- Scheduler triggers production runs at configured times
- Manual, dry-run, and backtest runs bypass scheduler

#### Run Recovery

If a run is interrupted:

1. Run status set to `interrupted` with `interrupt_reason`
2. Videos in `evaluating` or `acted` state are re-processing candidates
3. Next run checks for interrupted runs and resumes from last known state
4. Idempotency guards prevent duplicate insertions on resume
5. Videos in `completed` state are never reprocessed

#### Deferred Work Recovery

1. At start of each production run, check deferred queue
2. If quota available, process deferred items before new discoveries (FIFO)
3. If deferred item fails again, moves to error queue

**Deferred video configuration semantics:** Deferred videos are reprocessed under the *current* route configuration in the next run, not the configuration that was active when they were deferred. This is consistent with the run lifecycle: a fresh configuration snapshot is captured at run initiation (before the processing phases), and deferred items are evaluated against that snapshot. If route conditions changed between deferral and reprocessing, the deferred video is subject to the updated rules — it may match a new route, no longer match a previously matching route, or be excluded by a new exclusion rule. This behavior is intentional: reprocessing under the current config ensures deferred work reflects the user's latest intent.

## Security Threat Model

### Trust Boundaries

| Boundary | Assets Crossing | Threats | Mitigation |
|---|---|---|---|
| **User ↔ Web UI** | Credentials, routing config | Session hijacking, CSRF, XSS | HTTPS-only, CSRF tokens, CSP, input sanitization |
| **Web UI ↔ API Server** | Auth tokens, config, run data | Unauthorized access, injection | Token-based auth, parameterized queries |
| **API Server ↔ YouTube API** | OAuth tokens, API requests | Token theft, API abuse | Encrypted token storage, minimal scopes, rate limiting |
| **API Server ↔ SQLite DB** | All persistent data | SQL injection, corruption | Parameterized queries only |
| **Storage ↔ Disk** | Database file | Unauthorized file access | File permissions, Docker volume isolation |

### STRIDE Analysis

| Threat | Category | Mitigation |
|---|---|---|
| OAuth token theft | Spoofing | AES-256 at rest, never logged, minimal scopes |
| Unauthorized API access | Tampering | Token auth, valid session required |
| Route config tampering | Tampering | Auth required, config versioning audit trail |
| Run history manipulation | Repudiation | Append-only records, no hard deletes of evidence |
| API data exposure | Info Disclosure | Single-user, no external API, HTTPS |
| Quota manipulation | Elevation of Privilege | Server-side tracking only |

### Accepted Risks

- **Single-user assumption:** No multi-user auth model. Redesign needed if ever exposed to multiple users.
- **SQLite concurrency:** Single-writer. Acceptable for single-user tool.
- **Local deployment security:** Physical access to host = access to all data.

## Migration

### Historical Corpus Import

Prior installation has 10,000+ titles. API-originated titles retain API-Data provenance after migration.

**Migration flow:**

1. Read raw titles and video IDs from source SQLite
2. Transform each title into fingerprint using same normalization algorithm
3. Store permanently in TitleFingerprint with source = 'user_import' and import_batch_id
4. Discard raw titles after successful fingerprint generation

**INV-6 compliance:** Migrated fingerprints use identical schema and algorithm as native entries. Duplicate detection produces identical results regardless of provenance.

## Verification and Composition Obligations

### Cross-Component Contracts Requiring Vertical Composition Proof

1. **Discovery → Duplicate Detection:** Fingerprint computed during discovery must produce identical similarity scores when queried during duplicate detection. Both phases use the same TitleFingerprint schema and algorithm.

2. **Quota Ledger → Pre-flight Budget Check:** QuotaLedger must accurately reflect consumed units before each phase's budget check. Lag between API call recording and budget check must be negligible.

3. **ConfigVersion → Run → Statistics:** Run.config_version_id must reference a valid ConfigVersion created before the run started. Statistics must link to the correct config version.

4. **Exclusion Gate → RouteDecision → Outcome Classification:** Excluded videos must produce RouteDecision(skip_reason=exclusion) and primary outcome `excluded`. This is the OC-002 guarantee that excluded ≠ rejected.

5. **ExecutedAction Unique Constraint → Idempotency:** The UNIQUE constraint on (run_id, video_id, target_playlist_id, action_type) must prevent duplicate insertions across routes in the same run.

### Production-Owned Components (Cannot Be Mocked in Product Acceptance)

- YouTube Data API v3 interaction (OAuth, quota, playlist management)
- SQLite database with FTS5
- SSE connection for real-time progress
- OAuth2 flow end-to-end

### Failure/Recovery Scenarios

1. Process crash mid-run: run status = interrupted, resumes on next run
2. Quota exhaustion mid-run: remaining work deferred, processed in next run
3. Auth token expiry mid-run: remaining work deferred, user prompted to re-authorize
4. Metadata expiry: evidence degrades gracefully, one-click refresh restores
5. Database corruption: SQLite backup (file copy) restores to last consistent state

## Consequences

### What This Enables

- Full YouTube API compliance with clean audit boundary
- Complete decision explainability from stored evidence
- Historical backtesting and replay with isolated state
- Temporal analytics with configuration attribution
- Graceful degradation under quota constraints

### What This Requires

- Two-store coordination adds implementation complexity
- Metadata lifecycle management (refresh, expiry)
- Fingerprint algorithm tuning and corpus management
- SSE implementation for real-time progress
- SQLite WAL mode for concurrent reads during writes

### What This Forecloses

- Horizontal scaling (single SQLite, single-user)
- Real-time monitoring (polling only)
- Autonomous policy changes (user authority invariant)
- Multi-provider support (YouTube-only for v1)

## Anchor Check

### Minimal Version

This architecture delivers all v1 elements defined in the Anchor:

- ✅ Single-user — no multi-tenancy
- ✅ Web admin interface — SPA with responsive layout
- ✅ Scheduled processing — internal cron
- ✅ Stateful routing — three-tier outcome model
- ✅ Conditions/actions — configurable per route, AND/OR composition
- ✅ Historical duplicate detection — fingerprint corpus with FTS5
- ✅ Quota awareness — QuotaLedger, pre-flight checks, deferral
- ✅ Decision evidence — ConditionAudit trail
- ✅ Statistics with temporal analytics — three-level granularity
- ✅ Dry-run — mode flag, state isolation, draft config support
- ✅ Replay — re-evaluation against current config
- ✅ Backtesting — offline evaluation with proposed config
- ✅ Source/channel analysis — per-channel yield and usefulness
- ✅ Advisory system — evidence-backed recommendations, user decides

### Not This

- ✅ No continuous monitoring — polling only
- ✅ No auto-unsubscribe — advisory system, user decides
- ✅ No autonomous rule optimization — system advises, user acts
- ✅ No ML classification — threshold-based fuzzy matching
- ✅ No multi-provider — YouTube Data API v3 only
- ✅ No social/community — single-user
- ✅ No sophisticated recommendation AI — rule-based routing

## Open Questions

None — all 32 BRs and 3 OCs are satisfied by this architecture. All prior OQs (fingerprint algorithm, discovery method, metadata refresh, SPA stack, deployment) were resolved in the previous architecture iteration and remain valid.

## Deferred / Excluded

| Item | Guarantee Impact | Acceptable? |
|---|---|---|
| Multi-provider support | Provider abstraction layer | Yes — v1 YouTube-only per Anchor |
| Continuous monitoring | WebSocket/push architecture | Yes — Anchor excludes this |
| ML classification | Training data pipeline | Yes — Anchor excludes this |
| Autonomous rule optimization | Violates user authority | Yes — Anchor excludes this |
| Multi-tenancy | Tenant isolation | Yes — single-user per Anchor |
| Real-time quota from YouTube | YouTube doesn't expose via API | Yes — local tracking |
| 36-month statistical retention | Compliance audit application | Deferred to v2 |
| Auto-refresh all historical metadata | Significant quota cost | Yes — refresh-on-demand sufficient |
