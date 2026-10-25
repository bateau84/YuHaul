---
type: design
title: YuHaul System Architecture
description: "System architecture for YuHaul — a personal, stateful discovery and routing system for YouTube subscription content with compliance-aware storage, quota management, and decision explainability. Imported duplicate-reference eligibility survives same-ID native fingerprint upserts."
tags: [architecture, design, yuhaul, youtube, routing]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted

YuHaul discovers subscription videos, flags historical title matches, and sends eligible videos through ordered routes to playlists. A distinct duplicate still continues: for example, another upload of the same song can match an earlier reference and also reach a matching playlist. Exclusions, conditions, final routes, idempotency, and operational constraints still apply. ADR-017/ARS-005 retire effective skip while preserving recorded history. The later ARD-001/ARS-005 production correction requires actual actions to use only their own run's matched decisions and captured targets; a simulation must not influence a later production mutation. Source/reviewer findings below distinguish implemented paths from outstanding corrections.

## Derived from

- [Anchor](../anchors/youtube-discovery-routing.md)
- [Requirement Index](../requirements/yuhaul/index.md) (32 BRs + 4 OCs)
- [Design Spec](../design/yuhaul-admin-ui-design.md)
- [Reconciliation Report](../requirements/yuhaul/reconciliation-report.md)
- [YouTube Compliance Analysis](yuhaul-youtube-compliance.md)
- [ADR-010 — Traefik BasicAuth Ingress Boundary](adr-010-traefik-basic-auth-ingress-boundary.md)
- [ADR-011 — Stable UUID Route Identity](adr-011-stable-uuid-route-identifiers.md)
- [ADR-012 — Proposal-Only Routes Use Inactive Identity Anchors](adr-012-proposal-route-identity-anchors.md)
- [ADR-013 — Duplicate-Reference Eligibility Is Monotonic Across Fingerprint Upserts](adr-013-monotonic-duplicate-reference-eligibility.md)
- [ADR-014 — Discovery Credit Requires Native Earliest-Discovery Evidence](adr-014-discovery-credit-requires-native-earliest-evidence.md)
- [ADR-015 — Persist One Durable Corpus Import Completion Timestamp](adr-015-durable-corpus-import-completion-timestamp.md)
- [ARS-002 — Web Corpus Import API Contract](ars-002-web-corpus-import-api.md)
- [ARS-003 — Traefik BasicAuth and TLS Kubernetes Contract](ars-003-traefik-basic-auth-kubernetes-contract.md)
- [ARS-004 — Route UUID Schema, API, and Migration Contract](ars-004-route-uuid-schema-api-migration.md)
- [ADR-017 — Fixed Duplicate Continuation with Inert Legacy Fields](duplicate-continuation/decisions/adr-017-fixed-duplicate-continuation.md)
- [ARS-005 — Duplicate Continuation Compatibility and Results Contract](duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md)

## Satisfies

| Requirement | Artifact | Notes |
|---|---|---|
| BR-001 Secure YouTube Account Connection | §OAuth and Credential Storage | OAuth2 flow, token persistence |
| BR-002 Subscription-Based Discovery | §Discovery Subsystem | Polling-based channel feed retrieval |
| BR-003 Multiple Ordered Route Definition | §Data Model, §Processing Pipeline, §API Surface | Stable public UUID over retained integer FK; BR-003 AC10 permits duplicate display names; evaluation order/history preserved (ADR-011, ARS-004). |
| BR-004 Route Conditions and Playlist Actions | §Data Model, §Router Engine | Condition/Action config per route |
| BR-005 Multi-Route Video Matching | §Router Engine | Sequential evaluation, multi-match |
| BR-006 Final Route Termination | §Router Engine | is_final flag, subsequent skips |
| [BR-007 Exclusion Behaviors](../requirements/yuhaul/br-007-exclusion-behaviors.md) | §Processing Pipeline — Phase 2, §Data Model | Satisfied by the design: route-scoped exclusions; a partial exclusion does not globally stop detection or other eligible routes. Configuration/input validation is specified elsewhere in the accepted Admin Interface Design and ARS-001. |
| [BR-008 Exact and Fuzzy Duplicate Detection](../requirements/yuhaul/br-008-exact-and-fuzzy-duplicate-detection.md) | §Processing Pipeline, §Duplicate Detection Engine; ADR-017/ARS-005 | Satisfied by the design: exclusion → distinct-ID detection → routing (AC7–AC9); fixed continuation, inert legacy input, truthful active output and immutable history (AC10–AC11). Ordering is implemented; replay/backtest continuation and compatibility changes remain pending. |
| BR-009 Historical Title Persistence and Import | §Two-Store Data Model, §Migration | Store B permanent corpus; durable last-successful-import completion time is separate from video publication time (ADR-015, ARS-002). |
| [BR-010 Duplicate Provenance for Discovery Credit](../requirements/yuhaul/br-010-duplicate-provenance-for-discovery-credit.md) | §Data Model — DuplicateMatch, §Statistics and Analytics | Satisfied by the design with ADR-014: selected distinct-pair identities/available channels persist; strictly ordered native evidence assigns earlier discovery and later relevance, otherwise both roles remain unknown (AC7). Attribution is independent per selected match, without transitive credit, all-candidate enumeration, or a global fuzzy-group origin (AC8). |
| BR-011 Processing State Persistence | §State Management | Durable three-tier outcome records |
| BR-012 Idempotent Playlist Actions | §State Management | ExecutedAction unique constraint |
| BR-013 Quota-Limited Work Deferral | §Quota Management | Deferral queue, no-false-rejection |
| BR-014 Failed Work Retention and Retry | §State Management | ErrorEntry with retry/dismiss |
| BR-015 Explainable Route Decisions | §Decision Evidence | ConditionAudit per-route trail |
| BR-016 Configurable Scheduled Runs | §Scheduling and Recovery | Internal cron scheduler |
| BR-017 Manual Run Initiation | §API Surface | POST /runs endpoint |
| BR-018 API Consumption Accounting | §Quota Management | QuotaLedger per-operation tracking |
| BR-019 Quota-Aware Processing | §Quota Management | Pre-flight budget check |
| BR-020 Run-Level and Route-Level Statistics | §Statistics | Run/Route computed views; Route List lifetime match count uses distinct production-matched videos per route (ADR-006) |
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
| BR-032 Processing Outcome Distinction | §Outcome Model, §API Surface | Five primary categories + not-yet-evaluated; run-detail video outcomes filter by category (ARS-001) |
| OC-001 YouTube API Quota and Failure Semantics | §Quota Management | Quota exhaustion = operational state |
| [OC-002 Route Evaluation Outcome Semantics](../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) | §Outcome Model, §Processing Pipeline; ARS-005 | Satisfied by the design: fully excluded inputs terminate before detection; survivors continue after a match. A duplicate is supporting evidence, not an outcome or exclusion. ARS-005 identifies the pending historical short-circuit and partial-exclusion counting corrections; shared vocabulary is unchanged. |
| OC-003 Configuration Snapshot for Historical Interpretation | §Configuration Versioning | Immutable snapshots linked to runs |
| [OC-004 Imported History as Duplicate References](../requirements/yuhaul/oc-004-imported-history-as-duplicate-reference.md) | §Duplicate Detection Engine, §Migration, §Statistics and Analytics; ADR-017/ARS-005 | Eligibility/no-self-match/credit realized elsewhere by ADR-013/014 and ARS-002; preserved here. Guarantee 5 uses the same fixed continuation for imported matches. Filtering evaluated inputs does not revoke imported reference eligibility or create credit. |
| User direction — authenticated ingress and automatic Let's Encrypt TLS | §Ingress Authentication and Network Exposure | HTTPS Traefik BasicAuth plus cert-manager-managed `yuhaul-tls`; HTTP redirects to an endpoint-free sink except for a narrowly scoped HTTP-01 solver route. Issuer/solver annotations and priority are present; explicit app router TLS activation and the Ingress-provider `allowEmptyServices: true` prerequisite require correction after real-provider review. Cluster issuance and exact corrected request-path verification remain open (ADR-010/ARS-003). |

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
- **Ingress boundary.** Non-local UI/API exposure uses Traefik BasicAuth and HTTPS; the application has no inbound user-auth middleware. Local loopback port-forward intentionally bypasses the Ingress boundary (ADR-010, ARS-003).

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

**Precondition:** Production completion evidence is checked for the exact video, target playlist and action type across runs; each action also records its authorizing run and decision. Hypothetical rows never count as completion.

**Component:** ExecutedAction entity with denormalized run_id/video_id and current-run decision linkage; the unique constraint on (run_id, video_id, target_playlist_id, action_type) suppresses duplicate local rows, not duplicate external requests by itself. ARD-001/ARS-005 require snapshot-authorized actions, production-only exact-target completion reuse and serialized in-process production execution. The existing provider-success/local-record crash window remains unproven and must not be described as solved by this constraint.

**Falsification test:** Execute and repeat/resume a run; then quota-defer a second target and process it in a new run. Completed targets do not repeat, pending eligible targets complete from the new run's snapshot, and simulation rows suppress nothing. Separately crash after external insertion before local recording to test the still-unproven provider reconciliation seam.

### INV-4: Decision Evidence Completeness (BR-015)

**Guarantee:** Every routing outcome is explainable from stored evidence without external log analysis.

**Precondition:** Router records condition evaluation results for every route evaluated against every video.

**Component:** ConditionAudit entity → per-route condition audit trail.

**Falsification test:** For any video outcome in any historical run, the user can trace the exact route evaluation sequence and condition results.

### INV-5: Configuration-Statistics Attribution (OC-003, BR-025)

**Guarantee:** Every run's statistics are interpretable in the context of the configuration version active during that run.

**Precondition:** Configuration snapshot created whenever routes change; each run records its active config version.

**Component:** ConfigVersion entity → Run.config_version_id foreign key, consumed by every configuration-dependent phase and action target lookup. Capturing a correct snapshot without using it is not conformance. BR-003 AC11 and ARD-001/ARS-005 forbid live configuration fallback during execution/recovery.

**Falsification test:** Modify routes, run, inspect historical run — route conditions shown match configuration at run time, not current.

### INV-6: Historical Reference Eligibility and Provenance Separation (BR-008, BR-009, BR-010, OC-004)

**Guarantee:** Every valid newly imported historical fingerprint is eligible for exact and fuzzy duplicate matching, using the same fingerprint normalization and detector as native observations. Eligibility does not assert that YuHaul routed the imported video or that any associated channel discovered it first.

**Precondition:** The importer validates the source row, fingerprints its title through the shared normalization path, and inserts the new row with `source='user_import'` and `is_duplicate_reference=true`. Import retries skip an existing same-video-ID row without changing its eligibility or provenance. A later native discovery upsert for that ID MUST preserve a stored true candidate flag using `existing OR incoming` before duplicate evaluation.

**Component:** Corpus importer → `TitleFingerprint.is_duplicate_reference` → exact/fuzzy Duplicate Detection Engine; channel association and discovery-credit attribution remain separate.

**Pairwise scope:** Each observed selected distinct match retains both video identities and available channel associations even when native chronology is unknown. Ordered native evidence assigns discovery/relevance only within that pair. Fuzzy matches are not assumed transitive; A–B plus B–C establishes neither A–C nor inherited A credit for B–C. No all-candidate enumeration or canonical group origin is required (BR-010 AC7–AC8, ADR-014).

**Falsification test:** Import a valid record whose source provides no route or discovery evidence, then persist native discovery for that same video ID before duplicate evaluation using the normal false initial flag. Verify there remains one eligible fingerprint row, but evaluating that video ID returns no exact or fuzzy duplicate. Evaluate a different video ID whose title normalizes to the same canonical fingerprint and verify the imported row can be returned as an exact match regardless of the fuzzy threshold; with another distinct ID whose title shares some known fingerprint tokens, verify the row remains available to the fuzzy candidate query under the configured threshold. Verify the pipeline creates no same-ID `duplicate_match` row. Verify the imported channel association earns no credit; a distinct later duplicate credits the reference only when its native `video.discovered_at`/`video.channel_id` evidence is present and strictly earlier. Verify missing or ambiguous order earns no credit, legacy same-ID match rows are excluded from both provenance projections, import creates no route/action evidence, retries leave an existing ID unchanged, and invalid source rows are not candidates.

### INV-7: Distinct Outcome States (BR-032, OC-002)

**Guarantee:** The system distinguishes six outcome categories and never represents absence of processing as a negative routing decision.

**Precondition:** Video processing state set to not_yet_evaluated at discovery; transitions only through explicit processing.

**Component:** Three-tier model: (a) VideoProcessingState, (b) RouteDecision, (c) ActionOutcome.

**Falsification test:** A never-evaluated video shows as not_yet_evaluated, not rejected. A video excluded by every route shows as excluded, not rejected. A skipped-by-final-route video shows as matched at its route level, not not_yet_evaluated.

### INV-8: Dry-Run, Replay, and Backtest Isolation (BR-026, BR-027)

**Guarantee:** Non-production modes produce routing evaluations without mutating external state, production duplicate corpus, production idempotency records, or production statistics.

**Precondition:** Action execution gated by mode flag suppressing external API calls and production state writes.

**Component:** Persisted run types (`scheduled`/`manual` production, `dry_run`, `replay`, `backtest`) → action executor gate, state write gate. Both production pipeline and real executor reject simulation invocation; production match selection binds to its own run and validates persisted decision ownership. Simulation evidence cannot authorize a later real action (ARD-001/ARS-005).

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
│  │  │ Discovery│→│ Exclusion│→│Duplicate │→│    Router    │ │ │
│  │  │ Phase    │ │   Gate   │ │Detection │ │    Engine    │ │ │
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
| **API Server** | REST endpoints + SSE for real-time run progress; serves web SPA. Inbound BasicAuth is enforced by Traefik Ingress, not by application middleware (ADR-010). |
| **Discovery Engine** | Retrieves subscription list and recent video feeds; persists discovered videos |
| **Metadata Lifecycle** | Manages Store A TTL; refreshes active metadata; expires stale records |
| **Processing Pipeline** | Orchestrates: discovery → exclusion gate → duplicate detection → routing → action execution |
| **Duplicate Detection Engine** | Exact canonical-title fingerprint match across distinct video IDs plus fuzzy FTS5/Jaccard matching against eligible Store B references; same-ID candidates are excluded from both passes; evaluates only videos that survive the all-route exclusion gate |
| **Exclusion Gate** | Route-scoped exclusion filtering before duplicate detection and route-condition evaluation; fully excluded videos do not enter duplicate detection |
| **Router Engine** | Evaluates videos against ordered routes; records decision evidence; respects final-route termination |
| **Action Executor** | Executes playlist insertions via YouTube API; idempotency guards; handles failures and deferrals |
| **Statistics Engine** | Computes run/route/channel statistics; materializes time-bucketed aggregates |
| **Backtest Engine** | Offline evaluation against retained observations; state isolation from production |
| **Configuration Versioner** | Snapshots route configuration on change; links runs to active config version |
| **Quota Ledger** | Tracks per-operation API costs; pre-flight budget checks; remaining capacity reporting |
| **YouTube API Client** | Thin wrapper around YouTube Data API v3; handles auth, retries, rate limiting. Exposes a base-URL seam (`YUHAUL_YOUTUBE_BASE_URL`) so the assembled artifact can run against a fake/sandbox endpoint for Product Acceptance without modifying the binary (see ADR-001) |
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
│ is_duplicate_reference BOOLEAN       │  ← matching-candidate eligibility, not route/credit evidence
│ import_batch_id TEXT                 │
└──────────────────────────────────────┘

┌──────────────────────────────────────────────┐
│ CorpusImportMetadata                         │
│──────────────────────────────────────────────│
│ singleton_id INTEGER PRIMARY KEY             │
│ last_successful_corpus_import_at TEXT NULL   │
└──────────────────────────────────────────────┘

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
│ status TEXT NOT NULL                 │  ← 'running' | 'completed' | 'interrupted' |
│                                      │     'failed'
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
│ route_id INTEGER NOT NULL           │  ← internal FK → Route.id
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
│ action_type TEXT NOT NULL           │  ← internal key: 'playlist_insert'
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
│ video_id INTEGER NOT NULL           │  ← currently evaluated native video
│ matched_fingerprint_id INTEGER      │  ← matched TitleFingerprint reference
│ similarity_score REAL NOT NULL      │
│ match_type TEXT NOT NULL            │  ← 'exact_video_id' (legacy label for exact title) | 'fuzzy_title'
│ source_route_id INTEGER             │  ← internal FK → Route.id (nullable)
│ threshold_used REAL NOT NULL        │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ Route                                │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │  ← internal key; existing FKs remain integer
│ name TEXT NOT NULL                   │  ← required display label; duplicates allowed by user direction
│ description TEXT                    │
│ evaluation_order INTEGER NOT NULL   │
│ is_final BOOLEAN DEFAULT false      │
│ is_enabled BOOLEAN DEFAULT true     │
│ condition_composition TEXT NOT NULL │  ← 'AND' | 'OR'
│ created_at TIMESTAMP NOT NULL        │
│ updated_at TIMESTAMP NOT NULL        │
│ deleted_at TIMESTAMP                │  ← soft delete (ADR-003); null = active
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteIdentity                        │
│──────────────────────────────────────│
│ route_id INTEGER PRIMARY KEY         │  ← maps internal route identity; intentionally no FK
│ public_id TEXT NOT NULL UNIQUE        │  ← stable public UUIDv4; append-only (ADR-011)
└──────────────────────────────────────┘

RouteIdentity is kept separate so an immutable historical ConfigVersion snapshot can retain a public UUID mapping even if an older pre-ADR-003 route row was physically deleted. Current and soft-deleted Route rows each have one registry row; historical-only mappings may have no Route row until an evaluation needs a `route_decision` FK target. Dry-run/backtest may create an inactive, soft-deleted Route identity anchor for a proposal-only UUID, without adding it to the active configuration or creating Route child rows (ADR-012).

┌──────────────────────────────────────┐
│ RouteCondition                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │  ← internal FK → Route.id
│ condition_index INTEGER NOT NULL    │
│ field TEXT NOT NULL                  │
│ operator TEXT NOT NULL              │
│ value TEXT NOT NULL                  │
└──────────────────────────────────────┘

┌──────────────────────────────────────┐
│ RouteAction                          │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │  ← internal FK → Route.id
│ action_type TEXT NOT NULL           │
│ target_playlist_id TEXT NOT NULL    │
│ action_index INTEGER NOT NULL       │
└──────────────────────────────────────┘

The public API serializes this action as `add_to_playlist` per ADR-007; the internal `playlist_insert` representation remains in durable rows and snapshots.

┌──────────────────────────────────────┐
│ RouteExclusion                       │
│──────────────────────────────────────│
│ id INTEGER PRIMARY KEY               │
│ route_id INTEGER NOT NULL           │  ← internal FK → Route.id
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

`CorpusImportMetadata` is one row of YuHaul operational state: the latest successful corpus-import completion timestamp. It is not a video publication date or YouTube API metadata. The timestamp survives process restarts; import-job status does not. Its migration and exact update/read rules are specified in ARS-002 and ADR-015.

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
Route 1──1 RouteIdentity (public UUID; registry can also retain historical-only IDs)
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
│ Discovery│     │ Exclusion│     │ Duplicate│     │ Routing  │     │  Action  │
│          │     │   Gate   │     │ Detection│     │          │     │Execution │
└──────────┘     └──────────┘     └──────────┘     └──────────┘     └──────────┘
```

This order is required by BR-008 AC7: exclusion filtering happens before duplicate detection, and duplicate detection happens before route evaluation. For example, if two enabled routes both exclude a channel, its video receives exclusion evidence without a duplicate lookup. If only one excludes it, the video still reaches duplicate detection and the other route remains eligible to evaluate it. BR-008 AC9 separately fixes continuation after a distinct match: duplicate status itself never suppresses those routes/actions. Both rules apply to production/recovery, dry-run, replay, and backtest; mode-specific write isolation remains unchanged (ADR-017/ARS-005).

**Realization choice:** Reuse the existing pipeline exclusion gate and router exclusion predicate before the existing detector. Keeping duplicate detection first and merely suppressing its persisted result would still violate AC7 and could hide detector errors behind an exclusion. A second detector-specific exclusion engine or durable candidate-set table adds configuration/state synchronization with no demonstrated need; neither is selected. The pipeline owns the survivor set, and the router continues to own per-route condition/exclusion evidence. No new store, phase name, outcome, schema migration, or duplicate policy is required.

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

**Phase 2: Exclusion Gate**

This is a route-scoped gate used to decide whether any enabled route remains eligible for the video. It does not turn a route-specific exclusion into a global exclusion.

1. Load the run's applicable route configuration and each enabled route's exclusion rules.
2. For each discovered video, determine which enabled routes exclude it (channel, video, title, word/phrase, duration).
3. If every enabled route excludes the video, record the route-level `RouteDecision(skip_reason=exclusion)` evidence, set the video outcome to `excluded`, and remove it from all later processing. An all-route-excluded video MUST NOT enter duplicate detection; no new `DuplicateMatch` row or duplicate-derived provenance is written for it.
4. If no enabled route excludes it, or only some enabled routes exclude it, keep it in the candidate set for Phase 3. Partial exclusions remain route-scoped: they do not prevent duplicate detection or evaluation by other routes.
5. If there are no enabled routes, the gate does not treat the video as excluded by every route; the existing later route/outcome handling remains in force.

Per-route exclusion evidence for a partially excluded video is recorded during route evaluation, where excluded routes receive a skipped decision and eligible routes are still evaluated. The gate does not add duplicate or route-condition evidence.

The filter restricts the **videos being evaluated**, not the historical reference corpus. Discovery persistence still precedes the gate; exclusion does not undo a stored fingerprint or revoke an imported reference's existing eligibility (OC-004, ADR-013). An all-route-excluded observation can therefore retain an eligible same-ID imported reference for comparisons with other videos, while receiving no new duplicate evidence for its own evaluation. Existing reference-eligibility and provenance policies are otherwise unchanged.

**Phase 3: Duplicate Detection**

1. Exact match: eligible TitleFingerprint with the same normalized canonical title fingerprint and a different video_id
2. Fuzzy match: FTS5 query against fingerprint_tokens, compute Jaccard similarity
3. Evaluate only the videos retained by Phase 2, including videos excluded from some but not all enabled routes. If exact or fuzzy matching returns a distinct reference → record a DuplicateMatch; the detector never returns a same-ID candidate, so no self-match evidence can be persisted (BR-008 AC8, OC-004).
4. No external API calls — all data local
5. Continue every surviving input to routing after a successful lookup, whether it matched or not. A match is not an exclusion or primary outcome and does not imply an action succeeded (BR-008 AC9, OC-002 Guarantee 11). Production retains its normal match/provenance writes; simulations retain their read-only detection and do not create production match/provenance evidence.

**Phase 4: Routing**

For each video that passed the exclusion gate:

1. **Metadata budget check:** Count videos requiring condition evaluation with expired metadata. Estimate re-fetch cost. If insufficient, defer videos with expired metadata.
2. **Route evaluation:** Load the immutable configuration linked to this run (saved capture, selected historical version or proposal, according to mode). Production, including exclusion recovery, uses explicit snapshot routes rather than live active-route loading. For each route in snapshot order:
   a. Check exclusions → if excluded by this route, record RouteDecision(skip_reason=exclusion)
   b. Check enabled → if disabled, record RouteDecision(skip_reason=disabled)
   c. Evaluate conditions (AND/OR composition)
   d. Record ConditionAudit for each condition
   e. If conditions match → record RouteDecision(decision=matched)
   f. If is_final → skip remaining routes with RouteDecision(skip_reason=final_route_matched)
3. Set Video.processing_state = evaluated (or deferred if quota prevents action execution)

**Phase 5: Action Execution**

For each video with at least one matched route **in this production run** (ARD-001/ARS-005):

1. Pre-flight budget check: estimate action cost (50 units per insert)
2. If insufficient → defer video and all remaining
3. Validate all matched decision IDs against this persisted production run/video and its snapshot routes before any external action. Resolve each action from that snapshot, never mutable `route_action`. For each authorized action:
   a. Check exact-target production completion across runs before requiring insertion quota; skip a completed identical action without claiming a new external success
   b. Execute playlistItems.insert (50 units)
   c. Record ExecutedAction with outcome
   d. On failure: categorize, add to ErrorEntry
4. If all actions succeeded → Video.processing_state = completed, final_outcome = accepted
5. Update corpus: set TitleFingerprint.is_duplicate_reference = true when the native video is routed to a playlist. Successful imports also set this flag when inserting a new historical reference (see INV-6 and §Migration); the flag records matching eligibility, not route completion or discovery credit.

#### Mode Variations

| Mode | Snapshot | Discovery | Exclusion | Duplicate | Routing | Action | State Writes |
|---|---|---|---|---|---|---|---|
| **Production** | Full | Full | Full | Full | Full | Full (external mutations) | All production state |
| **Dry-run** | Full (or draft config) | Full | Full | Full | Full | Simulated | Run/decision records only |
| **Replay** | Full (current or historical config) | Uses retained observations | Full | Full | Full | Simulated | Replay records only |
| **Backtest** | Full (proposed config) | Uses retained observations | Full | Full | Full | Simulated | Backtest records only |

**Isolation guarantee (INV-8):** Non-production modes never mutate external state, production corpus, production idempotency records, or production statistics.

Replay and backtest evaluate retained observations and use the existing read-only detector/candidate substrate with their selected snapshot threshold. They apply the same all-route exclusion filter first; partially excluded videos remain in detection and route evaluation even when a match is found. No separate historical candidate store is introduced or production corpus mutated. The current `evaluateHistoricalVideo` match early return violates this target and must be removed. Successful result counters derive from this simulation's distinct-video route evidence, not duplicate status or unchanged production outcomes; ARS-005 specifies the bounded counting correction and isolation proof.

**Resume invariant:** Whenever a run resumes at Phase 3 (Duplicate Detection), the video set supplied to that phase MUST already have passed the Phase 2 all-route exclusion filter. Reloading all run videos without reconstructing that filtered set would violate BR-008 AC7 and could create duplicate-match evidence for a fully excluded video. This invariant applies whether the duplicate phase was reached during the current process or restored after interruption. The order correction does not retroactively delete `DuplicateMatch` rows already persisted by an older implementation; history cleanup is not implied.

The same survivor restriction holds on entry to routing. An empty survivor set is a completed filter result, not a signal to reload all discovered videos. On recovery, existing route decisions alone do not prove that a video survived exclusions: all-exclusion decisions must keep it out of duplicate detection and condition evaluation. Reuse durable exclusion evidence without duplicating decisions or counting the video again; preserve later-phase/action idempotency. Reconstruction uses the applicable run configuration, not a new independent exclusion policy.

#### Phase-Order Implementation Status and Confirmation

The reordered flow is implemented for production, recovery, replay, backtest, and dry-run as of 2026-10-25. Dry-run uses a read-only detector configured from its selected immutable snapshot (including proposal thresholds) and continues routing/action simulation on a match. The subsequent user decision now requires that continuation in every mode; source inspection confirms replay/backtest still return early on a match and settings still expose ineffective `skip`/arbitrary strings. ADR-017/ARS-005 specify the pending corrections, including truthful compatibility and successful-result counting. Earlier ordering evidence does not prove conformance to this new amendment or Product Acceptance. Successful dry-run counters already derive from its own decisions rather than production video state, but partial-exclusion/nonmatch disjointness remains a demonstrated gap.

| Surface | Verified source fact / required delta |
|---|---|
| `internal/pipeline/pipeline.go` — `Execute` | Discovery advances to exclusion, then duplicate detection, then routing using the same survivor slice. Resumed duplicate/routing entry reconstructs survivors from reloaded videos without resetting the durable phase checkpoint. An empty survivor slice is not reloaded. Action-execution recovery is unchanged. |
| `internal/pipeline/exclusion.go` — `runExclusionGate` | Every-enabled-route filtering preserves partial/no exclusions and zero-enabled-route pass-through. Durable exclusion decisions are reused, missing exclusion decisions are completed after an interrupted write, and finalized excluded videos are not re-admitted or re-counted. Existing non-skipped condition decisions retain downstream recovery behavior. **Outstanding correction:** production currently uses active routes; ARD-001/ARS-005 require its linked snapshot, also for exclusion recovery and deferred reevaluation. |
| `internal/pipeline/events.go` | Package summary reflects the corrected order; serialized phase names are unchanged. |
| `internal/pipeline/replay.go` — `evaluateHistoricalVideo` | Gate precedes lookup, but match still returns before routes/actions. Remove that return under BR-008 AC9 while retaining read-only detection, failure handling, snapshot configuration and simulation isolation. Replace the old empty-decision/zero-count duplicate regression expectation with positive continuation evidence (ARS-005). |
| `internal/pipeline/dryrun.go` — `dryRunVideo` | Gates exclusions before read-only duplicate detection and route conditions. Fully excluded videos bypass lookup; survivors use `detectorForRun` with the selected snapshot threshold. Detection errors fail the run before conditions/actions. Matches do not change dry-run's existing routing/action simulation or create production duplicate-match evidence. Successful completion reuses decision-based historical counters. |

Required regression evidence uses the real SQLite-backed gate, detector, state, and router through assembled execution, not only direct phase-method tests:

| Case | Required falsification / observation |
|---|---|
| Every enabled route excludes an input whose title would match a distinct eligible reference | No detector lookup, no new `duplicate_match`, no condition audits or actions; per-route exclusion evidence and exactly one excluded-video count. A detector error injected at the lookup boundary must not affect that input. Zero persisted matches alone is insufficient proof that no lookup occurred. |
| Only some routes exclude an input | With a confirmed distinct duplicate, detection is reached and the unexcluded route still evaluates/acts while excluded routes retain exclusion-only evidence. A successful eligible nonmatch is rejected once, not also globally excluded; a match is hypothetically accepted once in simulations. |
| No exclusions / zero enabled routes / disabled routes | Preserve pass-through behavior and ordinary detection; disabled routes do not turn an otherwise partial exclusion into an all-route exclusion. Zero enabled routes is not an exclusion. |
| Interrupted at exclusion or duplicate detection, including already-recorded all-exclusion decisions and an empty survivor set | No fully excluded input re-enters detection or conditions; no repeated exclusion decisions/counts. Preserve the existing action-resume idempotency proof. |
| Replay/backtest and dry-run | Same input ordering and fixed continuation with the applicable snapshot, including legacy `skip`; no external mutation or production duplicate/provenance, corpus, idempotency, or statistics mutation. Confirm a real match reaches route decisions/simulated `not_attempted` actions and decision-derived distinct-video counters. Fully excluded inputs still bypass detection. Backtest makes no external calls. |
| Imported same-ID reference plus a different surviving video | Gate filtering leaves imported eligibility intact; same-ID evaluation cannot self-match; a different surviving video can still match the reference under configured rules (OC-004). |

`internal/pipeline/exclusion_order_test.go` now exercises fresh production execution through real discovery (only YouTube HTTP is faked), SQLite-backed filtering/detection/routing/actions, exclusion/duplicate/routing recovery with empty survivors and durable decisions, and replay/backtest with explicit snapshots. It checks distinct matching references, partial-exclusion matching/action behavior, zero enabled routes, disabled routes, imported same-ID eligibility preservation, no new duplicate rows for fully excluded inputs, per-route exclusion evidence without condition audits/actions, and one excluded-video count. A real detector backed by a closed database proves lookup bypass for fully excluded inputs and lookup failure for survivors; zero persisted matches is not the sole evidence. An injected second-route decision-write failure proves recovery completes missing exclusion evidence without duplicating it. Existing action-resume idempotency and duplicate no-self-match tests remain in the package suite. `internal/pipeline/dryrun_duplicate_test.go` adds dry-run proposal-threshold, all/partial exclusion, lookup-boundary failure, continuation/action simulation, and decision-derived counter checks with a real detector. Its existing debug diagnostics distinguish fuzzy matches from no-match without persisting production duplicate rows. These are automated composition/regression checks, not independent review or deployed Product Acceptance.

**Dry-run scope (BR-026):** Dry-run protects external YouTube state (no playlist mutations). Read-only API calls (video listing, channel enumeration) may execute to obtain metadata. Internal state changes (corpus, statistics) are also suppressed as a conservative design decision beyond the minimum anchor requirement — this prevents analytics pollution from hypothetical evaluations.

### Quota Management

#### Quota Model

| Operation | Method | Unit Cost |
|---|---|---|
| List subscriptions | `subscriptions.list` | 1 per page |
| List channel uploads | `playlistItems.list` | 1 per call |
| Get video metadata | `videos.list` | 1 per call (up to 50 IDs) |
| Get channel info | `channels.list` | 1 per call |
| List user playlists | `playlists.list` | 1 per successful page |
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

**Pass 1: Exact canonical-title match.** Normalize the evaluated title with the shared fingerprint normalizer, then query eligible `TitleFingerprint` rows where `fingerprint` equals that normalized canonical fingerprint and `video_id` differs from the evaluated video ID. The query orders by fingerprint-row `id` and returns the oldest eligible distinct reference, with exact score `1.0`. Thus two different video IDs with differently formatted titles that normalize identically can match; the evaluated video's own fingerprint row is not returned. The persisted match-type value `exact_video_id` is retained by the current schema as a legacy label for this exact-title comparison; it does not mean that exact matching compares the video IDs.

**Pass 2: Fuzzy title match.** If no exact match:

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

**Rationale:** The earlier architecture recorded a researcher benchmark against 10 duplicate groups (147 positive, 181 negative pairs): token-set Jaccard F1=0.990 with zero false positives at threshold 0.572, versus char-trigram cosine F1=0.993 with two false positives. Those historical measurements are not rerun here. The selected method remains unchanged; fewer false positives improve duplicate flags and provenance quality. The old rationale that a false positive silently skips a distinct video is superseded by BR-008 AC9: duplicate status cannot suppress routing/actions. This policy change does not retune the algorithm or threshold.

**Corpus scope and marker meaning:** `is_duplicate_reference` means that a row is eligible to serve as an exact/fuzzy historical reference. Native `api_discovery` rows become eligible when routed to at least one playlist; valid newly inserted `user_import` rows are eligible on import because the prior-system corpus is authoritative as historical matching data even when YuHaul routing is unknown. Eligibility is monotonic across same-`video_id` fingerprint upserts: merge the stored and incoming flags with logical OR, so a native observation's ordinary incoming `false` cannot demote an imported reference before duplicate evaluation (ADR-013, ARS-002). One unique fingerprint row remains authoritative for each video ID. The detector's exact pass compares normalized canonical title fingerprints across different video IDs, and both exact and fuzzy passes exclude the evaluated video's own ID; eligibility of a same-ID row is retained for comparisons with other videos. A matching reference is not itself route-completion evidence or discovery credit. A source `channel_id` remains an association only; import time/order and that association do not identify the earliest-discovered instance. Native rejected, excluded, failed, and unevaluated observations remain ineligible unless their own accepted lifecycle later makes them references. A same-ID candidate never reaches the pipeline as a match, so no new self-duplicate event is persisted. For a distinct match, discovery/relevance roles are derived from the current and reference native `video.channel_id` / decoded `video.discovered_at` pair: the earlier observation receives discovery credit and the later relevance credit, regardless of match direction. Missing or ambiguous evidence earns no chronology-based credit. Fingerprint source/channel and channel-level `first_discovered_at` are not substitutes (ADR-014).

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

Outcome counters classify videos, not route decisions. A video excluded by every enabled route receives `final_outcome=excluded` and contributes exactly one to `videos_excluded` when run counters are computed; its multiple per-route exclusion decisions do not multiply that video count. A video excluded by only some routes is not globally finalized by Phase 2 and proceeds through duplicate detection and routing. Phase 2 itself contributes no terminal-outcome counter for a partial exclusion; its later primary outcome follows the existing routing/action outcome model. A fully excluded video creates no `DuplicateMatch` row, so it contributes no duplicate-derived discovery/relevance provenance.

#### Route Statistics

Computed views aggregated from RouteDecision records filtered by route_id and run_id.

Per-route, per-run: videos evaluated, matched (with condition patterns), not-matched (with rejection breakdown), skipped (final-route or exclusion).

The Route List's lifetime `match_count` is a separate cross-run projection: it counts distinct video IDs with at least one matched decision for that route in `scheduled` or `manual` runs. A video matched by the same route in multiple production runs contributes once. Dry-run, replay, and backtest decisions are excluded (ADR-006, ARS-001).

#### Channel Statistics

Per-channel, cumulative and per-period:

- Videos discovered, routed, rejected (with reason breakdown), retained
- Usefulness rate: retained / discovered
- Yield: routed / discovered
- Time since last useful discovery

#### Duplicate Provenance Credit (BR-010, OC-004)

For each observed selected distinct match, retain the evaluated video, selected reference, and their available channel associations in durable provenance available to history and statistics. The selected reference is not necessarily the earlier observation or a global original. The statistics projections distinguish discovery credit for the earlier native observation within that pair from relevance credit for its later member. A same-ID self-reference is excluded from both credits; legacy persisted self-match rows are filtered as well. Compare the current native video's `video.discovered_at` with the selected reference's native `video.discovered_at`. The earlier video's native `video.channel_id` receives discovery credit and the later video's channel receives relevance credit for that match, regardless of reference direction. If either member lacks a native `video` row or times are equal, unavailable, or unorderable, both chronology-based roles remain unknown without discarding the match identities or available associations; the imported fingerprint association is never a fallback. Compare decoded time values rather than raw SQLite timestamp text (ADR-014, BR-010 AC7).

**Nontransitive attribution:** Attribute each selected pair independently. For native times A < B < C and observed A–B and B–C matches, A receives discovery and B relevance for A–B; B receives discovery and C relevance for B–C. These events establish no A–C match and do not transfer A's discovery credit to B–C. Neither the detector nor provenance must enumerate or credit every eligible candidate, construct a canonical fuzzy-equivalence group, or assign one group-global origin (BR-010 AC8). The user's 2026-10-25 approval recorded in BR-010 settles this scope; duplicate routing and native reference eligibility are separate questions.

**Confirmation:** ADR-014 requires independent selected-pair readback for the A–B/B–C chain, retained identities/available channels when native chronology is unknown, no inferred A–C event or inherited credit, and no all-candidate completeness promise. This documentation reconciliation ran no runtime tests; existing pairwise implementation/composition reports do not by themselves establish that new chain confirmation or Product Acceptance.

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

Stored ConfigVersion JSON retains the internal integer route key and is never rewritten. Public configuration-history, diff, draft, and restore representations project each route identity to its stable UUID through the append-only `route_identity` registry, including historical-only IDs whose route row no longer exists (ADR-011, ARS-004).

**Retired duplicate handling:** ADR-017/ARS-005 retain `duplicate_behavior` for compatibility, not selection. Active settings/threshold responses and new effective saved/proposal captures emit `continue`; old persisted setting strings are inert and legacy valid request strings are accepted/ignored. No schema migration or behavior-only configuration version is needed. Historical snapshots/diffs and the existing restore-copy path retain recorded values without altering old rows or outcomes; clients label them as recorded legacy data. Evaluating an old snapshot today still continues after a match. Current source has not implemented these compatibility corrections. Restore currently appends a snapshot copy rather than installing active routes/settings; this bounded amendment does not silently change or certify that broader behavior.

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
| `GET` | `/api/runs` | List runs (filterable; `runs` is always an array; 500 JSON error on store failure) |
| `GET` | `/api/runs/:id` | Run detail |
| `GET` | `/api/runs/:id/videos` | Paginated video outcomes for run; optional outcome-category filter, with filtered pagination totals (ARS-001) |
| `GET` | `/api/runs/:id/videos/:videoId` | Decision detail (three-tier evidence) |
| `GET` | `/api/routes` | List routes (ordered; excludes soft-deleted; includes production lifetime `match_count`) |
| `POST` | `/api/routes` | Create route |
| `GET` | `/api/routes/:route_uuid` | Route detail by UUID (404 if unknown/soft-deleted) |
| `PUT` | `/api/routes/:route_uuid` | Update route by UUID (creates ConfigVersion) |
| `DELETE` | `/api/routes/:route_uuid` | Soft-delete route by UUID (preserves history; ADR-003/011) |
| `PUT` | `/api/routes/reorder` | Update evaluation order using UUID `route_ids` (active routes only) |
| `GET` | `/api/playlists` | List all user's YouTube playlists across API pages (ADR-005) |
| `GET` | `/api/analytics/trends` | Temporal trend data |
| `GET` | `/api/analytics/channels` | Channel statistics |
| `GET` | `/api/analytics/channels/:id` | Channel detail with advisories and per-route UUID/name/production match-count objects |
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
| `POST` | `/api/corpus/import/preview` | Preview a prior-application SQLite corpus |
| `POST` | `/api/corpus/import` | Start append-only historical corpus import |
| `GET` | `/api/corpus/import` | Recover active or most recent in-process import status after navigation |
| `GET` | `/api/corpus/import/metadata` | Read the durable last-successful-import completion timestamp |
| `GET` | `/api/corpus/import/{import_id}` | Read in-process import progress/result |
| `POST` | `/api/corpus/import/{import_id}/cancel` | Request import cancellation |
| `GET` | `/api/account/status` | Account status |
| `POST` | `/api/auth/initiate` | Begin OAuth2 |
| `GET` | `/api/auth/callback` | OAuth2 callback |
| `DELETE` | `/api/account` | Disconnect account |
| `GET` | `/api/runs/:id/stream` | SSE stream for run progress |

#### Route, playlist, and list-response contracts

The public route-action identifier is `add_to_playlist`, matching the accepted Route Editor design. The existing internal/persisted identifier remains `playlist_insert`; the HTTP boundary translates between them without rewriting route rows, executed-action keys, or historical configuration snapshots (ADR-007).

Route `id` values, route path identifiers, and every public route-reference field are canonical UUID strings. This rule covers route CRUD/reorder, run-video decisions, run-scoped statistics and route analytics, channel matched-route entries, duplicate provenance, configuration history/diff/restore, and route IDs in dry-run/backtest proposals and result payloads (ARS-001, ARS-004). Unknown valid proposal UUIDs are mapped to durable internal integer keys and inactive FK identity anchors; they do not enter the active route set (ADR-012). Internal route primary/foreign keys and stored snapshot route IDs remain integers and never appear as public route identities; serializers project them through `route_identity`. Non-route IDs such as run IDs and ConfigVersion IDs keep their existing types. Names are required display values but may repeat; they are never identity keys or grouping keys, and clients/history/statistics distinguish routes by UUID. Numeric route IDs are not public aliases.

`GET /api/analytics/channels/:id` returns `matched_routes` as objects containing `route_id` (UUID), `name` (display label), and `match_count` (distinct channel videos matched in scheduled/manual runs). It keeps same-named routes separate and excludes dry-run, replay, and backtest decisions from this operational statistic (ARS-001, BR-027 AC7).

`GET /api/playlists` follows every YouTube `playlists.list` continuation token and returns the complete list in one `{"playlists":[...]}` response. It returns `200` with `[]` only when the complete list is empty, and `502` on authentication or page/API failure; a later-page failure does not return a partial list. Each successful page costs and records one quota unit. The endpoint does not expose pagination to the browser (ADR-005, ARS-001).

`GET /api/routes` returns active routes ordered by `evaluation_order`, with each route identified by a UUID and independently addressable when names repeat. Each list item has an integer `match_count`, including explicit zero. The count is the number of distinct `route_decision.video_id` values matched by that route over its lifetime, joined to `scheduled` or `manual` production runs; repeated matches of the same video/route pair count once, and dry-run, replay, and backtest decisions are excluded. It is a list-only projection; `GET /api/routes/:route_uuid` returns configuration without `match_count`. Route loading or count aggregation failure returns `500 {"error":"failed to list routes"}` for the whole response. The unused per-route `/stats` endpoint is not part of the supported API contract (ADR-006, ARS-001, ARS-004).

BR-032 AC5 is realized on the Run Detail page, not by the Runs List filters. `GET /api/runs/:id/videos` accepts an optional `outcome` from the six final-outcome values; the returned videos and pagination totals are both filtered, and an unsupported non-empty value returns `400`. The Run Detail view requests up to 100 videos for the selected category and shows the matching total; it currently has no control for later pages (ARS-001).

The accepted Route List design requires drag-to-reorder as the primary interaction and keeps explicit Move Up/Down buttons as the single-pointer and keyboard-accessible alternative. `RoutesView` now implements the ADR-008 choice with `@dnd-kit/react`: the grip starts dragging, Escape/canceled drops do not persist, and successful drops share the existing move-and-save path with the buttons. The targeted frontend tests and production build pass; touch/pen and mobile-scrolling behavior have not been verified in a real browser.

`GET /api/runs` returns `{"runs":[]}` for a successful empty result and `500 {"error":"failed to list runs"}` on store failure. A successful collection field is never null; clients reject a failed or malformed response instead of translating it to an empty collection. The accepted [list-view error-handling amendment](../design/amendment-list-fetch-error-handling.md) defines the visible fetch-error and retry states; `RunsView` and `RoutesView` implement those states with retry, rather than treating a failed request as an empty list.

#### Server-Sent Events (SSE)

Real-time progress via SSE at `/api/runs/:id/stream`. Chosen over WebSocket for architectural simplicity: the communication is unidirectional (server → client), the single-user context eliminates connection multiplexing concerns, and SSE works over standard HTTP without protocol upgrade negotiation.

Events: `run.started`, `run.phase`, `run.video.processed`, `run.quota.warning`, `run.completed`, `run.error`.

### Scheduling and Recovery

#### Internal Scheduler

- User configures schedule via Settings (cron expression or simplified interval)
- Scheduler triggers production runs at configured times
- Manual, dry-run, and backtest runs bypass scheduler

#### Run Recovery

Run `status` has four states: `running`, `completed`, `interrupted`, and `failed`. `interrupted` and `failed` are distinct terminal-adjacent states with different recovery semantics; both carry a reason in `interrupt_reason` (redefined as "the reason the run is not in a normal terminal state").

**Interrupted (resumable):** a run whose process was cleanly stopped at a phase boundary (e.g. crash mid-run):

1. Run status set to `interrupted` with `interrupt_reason`
2. Videos in `evaluating` or `acted` state are re-processing candidates
3. Next run checks for interrupted runs and resumes from last known state
4. Idempotency guards prevent duplicate insertions on resume
5. Videos in `completed` state are never reprocessed

**Failed (terminal):** a run whose processing phase returned an unrecoverable error, or which panicked mid-flight:

1. Run status set to `failed` with `interrupt_reason`
2. Run records `completed_at`, `duration_seconds`, and `quota_cost` (terminal accounting), and its outcome counters are recomputed
3. The run is NOT auto-resumed — it is excluded from the interrupted-run resume queue
4. The operator initiates a fresh run after remedying the cause (e.g. re-connecting the account)

#### Deferred Work Recovery

1. At start of each production run, check deferred queue
2. If quota available, process deferred items before new discoveries (FIFO)
3. If deferred item fails again, moves to error queue

**Deferred video configuration semantics:** Deferred videos are reprocessed under the *current* route configuration in the next run, not the configuration that was active when they were deferred. This is consistent with the run lifecycle: a fresh configuration snapshot is captured at run initiation (before the processing phases), and deferred items are evaluated against that snapshot. If route conditions changed between deferral and reprocessing, the deferred video is subject to the updated rules — it may match a new route, no longer match a previously matching route, or be excluded by a new exclusion rule. This behavior is intentional: reprocessing under the current config ensures deferred work reflects the user's latest intent.

**Production authority correction (accepted target, not implemented by this documentation):** [ARD-001](duplicate-continuation/design/ard-001-run-owned-production-action-authority.md) and [ARS-005](duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md) make “current” mean the receiving run's initiation snapshot, never a live lookup halfway through execution. Reevaluate deferred videos before action selection and record new run-owned decisions. Same-run interrupted recovery retains its original snapshot and only an admissible evaluation prefix. A same-run legacy match may itself have used edited live conditions: add internal `run.action_authority_version` (existing/unknown `0`, explicit corrected new production `1`, never promoted/backfilled). Version `0` requires independent non-recording snapshot evaluation and full persisted decision/audit comparison before reuse and inside the executor; disagreement or missing proof fails closed with retained work, not rewritten history or a false rejection. Add nullable `deferred_item.retry_run_id` and `error_entry.retry_run_id` for durable receiving ownership before first decisions, preserving originating `run_id`; deferred sibling rows are one owned video group, claimed/requeued/acknowledged together. A quarantined legacy failure's manual retry creates fresh production evidence in a new receiving run rather than changing the old run's snapshot. Recover owned work even at later phase checkpoints and never transfer discovery ownership. Reuse run evidence/owned queue membership rather than discovery-only queries. Exact-target production successes may suppress repeated actions, but other runs' matches and simulation rows cannot authorize them. One shared in-process production execution gate covers scheduler/manual/resume paths; old-writer coexistence/downgrade writes are unsupported. Confirmed reviewer probes show unscoped decisions/live actions violating isolation and captured-target truth; Critic's captured-`classical`/recorded-`jazz` fixture additionally defeats ownership-only legacy validation. Provider-success/local-record crash reconciliation and broad historical per-run operational outcome accounting remain explicitly unproven; no old evidence is rewritten and incorrect external insertions are not automatically undone.

## Ingress Authentication and Network Exposure

The selected ingress boundary is Traefik BasicAuth, not application login/session middleware. The HTTPS YuHaul Ingress uses the Traefik class, attaches `default-yuhaul-auth@kubernetescrd` to its `/` Prefix route, and routes all SPA/API/OAuth/SSE paths through that middleware. The operator-provisioned bcrypt htpasswd credential Secret is not committed. cert-manager ingress-shim owns the Let's Encrypt Certificate and TLS Secret `default/yuhaul-tls`; it is configured through a deployment-owned ClusterIssuer, not installed or credentialed by this repository. A separate HTTP Ingress redirects through an endpoint-free sink and cannot select YuHaul. During an active HTTP-01 challenge, a temporary solver Ingress is the sole exception and serves only the challenge URI to the cert-manager solver. The full Middleware, Secret, TLS lifecycle, challenge routing, entrypoint, and redirect contract is in ADR-010 and ARS-003.

Non-local application use requires a valid, trusted `yuhaul-tls` Secret for `yuhaul.bateau.cloud`, non-TLS `web` (`:80`) and TLS app routing on `websecure` (`:443`), TLS pass-through from the load balancer to Traefik, and the ARS-003 route pattern. The app Ingress MUST explicitly activate TLS with `traefik.ingress.kubernetes.io/router.tls: "true"`; neither the listener name nor `spec.tls` suffices. The controller MUST set static `providers.kubernetesIngress.allowEmptyServices: true` so the sink-backed HTTP router exists and its Middleware runs. This provider-wide option also retains unrelated empty-service routers; the operator must assess shared-controller precedence/error behavior. A controller-wide redirect is optional defense in depth, not the required HTTP boundary.

The manifests contain the two-route topology, both Middlewares, cert-manager issuer/solver annotations, HTTP redirect priority `1`, the endpoint-free sink, and the single-replica `Recreate` UUID-migration rollout. The corrected app Ingress now explicitly sets `router.tls: "true"`; README requires Ingress-provider `allowEmptyServices: true` and explains shared-controller impact and 404 diagnosis. ARS-003 records the narrow local Traefik v3.7.16 real-provider proof: HTTPS 401, HTTP 301, plaintext TLS-port no challenge, zero backend hits, and HTTP route omission when the provider option is disabled, without entrypoint-TLS/global-redirect defaults. Remaining auth/failure-injection checks, independent confirmation, live cluster behavior, and external cert-manager issuance/renewal are not established.

cert-manager, the production ClusterIssuer, the ACME account key, and optional DNS-provider credentials remain deployment-owner work; `letsencrypt-prod` is a configurable example name, not an existing cluster resource. If HTTP-01 is used, public port 80 must be reachable and the active solver route must outrank the sink without reaching YuHaul; DNS-01 is the alternative otherwise. Until a trusted certificate is issued and the routes are verified, do not send credentials or treat the application as ready for non-local use. A client that proactively sends BasicAuth credentials over HTTP can expose them before a server redirect; clients must use HTTPS and not send credentials over HTTP.

The current Service is `ClusterIP`. Direct in-cluster Service/Pod requests and a local `kubectl port-forward` bypass Traefik BasicAuth and TLS by design. Port-forward is an operator-only trusted path and must bind to loopback; it is not a substitute for authentication on a network-exposed Ingress. Repository evidence does not verify whether `yuhaul.bateau.cloud` is publicly reachable.

### Security Threat Model

### Trust Boundaries

| Boundary | Assets Crossing | Threats | Mitigation |
|---|---|---|---|
| **Remote user ↔ Traefik Ingress** | BasicAuth credentials, routing config, run data, OAuth callback traffic | Credential interception, unauthorized access | HTTPS BasicAuth; valid TLS Secret; HTTP redirect-only route with no YuHaul backend |
| **Traefik ↔ API Server** | Authorized HTTP request and application data | Header leakage, alternate route bypass | BasicAuth `removeHeader`; same-origin backend; every external route must attach the Middleware |
| **Cluster user / port-forward ↔ API Server** | Full admin and account-management surface | Bypass of Ingress authentication | Kubernetes RBAC and trusted operator host access; no app auth is provided |
| **API Server ↔ YouTube API** | OAuth tokens, API requests | Token theft, API abuse | Encrypted token storage, minimal scopes, rate limiting |
| **API Server ↔ SQLite DB** | All persistent data | SQL injection, corruption | Parameterized queries only |
| **Storage ↔ Disk** | Database file | Unauthorized file access | File permissions, Docker volume isolation |

### STRIDE Analysis

| Threat | Category | Mitigation |
|---|---|---|
| OAuth token theft | Spoofing | AES-256 at rest, never logged, minimal scopes |
| Unauthorized Ingress API access | Tampering | Traefik BasicAuth on every Ingress path; HTTPS required outside local host |
| Route config tampering | Tampering | Ingress BasicAuth for ingress requests, config versioning audit trail |
| Run history manipulation | Repudiation | Append-only records, no hard deletes of evidence |
| API data exposure | Info Disclosure | Single-user, no external API, HTTPS |
| Quota manipulation | Elevation of Privilege | Server-side tracking only |

### Accepted Risks

- **Single-user assumption:** BasicAuth is a shared ingress credential, not per-user identity or authorization. Redesign needed if ever exposed to multiple users.
- **SQLite concurrency:** Single-writer. Acceptable for single-user tool.
- **Local deployment security:** Physical access to host = access to all data.
- **Ingress residuals:** BasicAuth is not CSRF/XSS protection and this design adds no rate limiting. A server redirect cannot prevent a client from sending an Authorization header preemptively on its initial HTTP request; clients must use HTTPS and not send credentials over HTTP. Public exposure requires an explicit security/deployment review of these risks; no claim of internet reachability or complete public-deployment safety is made.
- **Trusted bypass paths:** Kubernetes users and host operators able to access the ClusterIP through port-forward are trusted; those paths bypass Traefik authentication.

## Migration

### Historical Corpus Import

Prior installation has 10,000+ titles. The prior application's existing SQLite database is imported directly; no intermediate export is required. The source must have exactly one discoverable non-internal table containing `video_id` and `title`; `channel_id` is optional. API-originated titles retain API-Data provenance after migration. The import contract does not interpret source dates as completion time or video publication date; the separate YuHaul import-completion timestamp is recorded as described below.

**Migration flow:**

1. Read raw titles and video IDs from the discovered source SQLite table (optional channel IDs are retained when present)
2. Transform each title into fingerprint using same normalization algorithm
3. Store permanently in TitleFingerprint with source = 'user_import', import_batch_id, and is_duplicate_reference = true
4. Discard raw titles after successful fingerprint generation

**INV-6 compliance and provenance boundary:** Imported rows use the same TitleFingerprint schema and fingerprint normalization as native entries and remain eligible in the shared exact/fuzzy candidate set. The `is_duplicate_reference` field is a candidate-eligibility flag: for native rows, routing may enable it; for newly imported rows, import itself enables it. Candidate eligibility is monotonic on same-video-ID writes: native discovery uses the same unique fingerprint row and MUST preserve an existing true value before duplicate detection. The write merge does not create Video, RouteDecision, ExecutedAction, Channel discovery counters, or a discovery-credit attribution; ordinary native discovery may independently persist its own observation using the existing pipeline. A supplied `channel_id` is an association only; absent independent evidence, discovery credit remains unknown. `first_seen_at`/import order must not be interpreted as discovery time. For a distinct matched pair, the native `video` rows' channels and decoded discovery times provide pairwise discovery/relevance evidence; the earlier observation is the discovery source regardless of match direction. The duplicate detector excludes the evaluated video's own ID from both exact and fuzzy matching, so a same-ID row creates neither a match result nor a new duplicate event; see BR-008 AC8, OC-004, ADR-013, and ADR-014.

### Corpus Import Completion Metadata

**Implemented:** Migration 013 (`internal/store/migrations/013_corpus_import_metadata.up.sql`) adds one `corpus_import_metadata` row to Store B's existing SQLite database. Its nullable `last_successful_corpus_import_at` records the UTC completion time of the latest import whose success-marker transaction committed. The shared importer (`migration.Importer.RunChunked`; `Importer.Run` and the web job manager both delegate to it) applies the update only after full source scanning, row accounting, committed destination chunks, and a successful final corpus count; explicit row-level errors remain successful when accounted under BR-009 AC5/AC9. Preview, rejected input, cancellation that wins before marker commit, fatal failure, and marker-write failure leave the previous value unchanged; marker commit wins a cancellation that arrives too late. The web API (`GET /api/corpus/import/metadata`, served by `internal/corpusimport`) and `cmd/migrate` use the shared importer boundary, and a metadata-write failure does not erase already committed corpus chunks.

The migration seeds the singleton row with `NULL` and does not backfill from video publication dates, per-video timestamps, prior source-application pipeline-run timestamps, or file metadata. Inspection of the supplied prior-app database found video and pipeline timestamps but no timestamp representing completion of an import into the current destination. Existing imported corpora therefore report no reliable timestamp until a successful append import after migration (all-skipped retries may establish it). This is YuHaul operational metadata and does not alter API-data provenance, `transient_metadata` expiry, or any YouTube retention rule. Durable job IDs/progress/history are not added; process-local status remains governed by ADR-009.

### Route Identity Migration

Schema migration 012 creates the append-only `route_identity` registry while retaining `route.id` and all integer route foreign keys; UPDATE and DELETE triggers make registry rows immutable and append-only. Before serving, a startup transaction (invoked from `store.Open`, which `cmd/server` calls before the scheduler and HTTP listener) assigns UUIDv4 values to every current route, surviving route reference, and route ID recoverable from stored ConfigVersion snapshots, then validates coverage and canonical UUIDv4 form; validation failure aborts startup. The snapshots and their source rows are not rewritten. The registry has no foreign key to `route` so a snapshot-only historical ID still resolves. Proposal ingestion may create a deleted identity-anchor Route row for a UUID that needs to back a route decision; the proposal ConfigVersion snapshot remains the route definition and active route queries do not load the anchor (ADR-012) — the proposal-ingest anchor path is not yet implemented, and of the public UUID API conversion route-management CRUD/reorder, run-video decisions, configuration history/diff/restore, run-scoped statistics and route analytics, and channel matched-route projection are implemented; dry-run/backtest route-ID conversion remains outstanding. The migration release must retain one replica and use Deployment `strategy.type: Recreate`, so the old binary is terminated before the new binary runs migration/backfill against the SQLite PVC; this may cause downtime and must not overlap another database writer. Exact backfill, immutability, public API conversion, rollout, and compatibility rules are specified in ADR-011/ADR-012/ARS-004.

## Verification and Composition Obligations

### Cross-Component Contracts Requiring Vertical Composition Proof

1. **Discovery → Duplicate Detection:** Fingerprint computed during discovery must produce identical similarity scores when queried during duplicate detection. Both phases use the same TitleFingerprint schema and algorithm.

2. **Quota Ledger → Pre-flight Budget Check:** QuotaLedger must accurately reflect consumed units before each phase's budget check. Lag between API call recording and budget check must be negligible.

3. **ConfigVersion → Run → Statistics:** Run.config_version_id must reference a valid ConfigVersion created before the run started. Statistics must link to the correct config version.

4. **Exclusion Gate → RouteDecision → Outcome Classification:** Excluded videos must produce RouteDecision(skip_reason=exclusion) and primary outcome `excluded`. This is the OC-002 guarantee that excluded ≠ rejected.

5. **ExecutedAction Unique Constraint → Idempotency:** The UNIQUE constraint on (run_id, video_id, target_playlist_id, action_type) must prevent duplicate insertions across routes in the same run.
6. **Route UUID → Historical identity:** Public UUIDs must map one-to-one to internal integer route identities; route decisions, stored ConfigVersion snapshots (including snapshot-only legacy IDs and proposal-only routes), same-name routes, and all public route references must resolve to the same identity. Proposal-only route anchors must remain inactive and satisfy the existing route-decision FK. No API may fall back to the integer key or display name (ARS-004, ADR-012).
7. **Traefik Ingress → Authentication/TLS:** Every HTTPS request to a user-facing path must be challenged before backend dispatch; no plaintext HTTP route may dispatch to YuHaul or issue a BasicAuth challenge; clients must not send BasicAuth credentials over HTTP; and no alternate non-local route may bypass the Middleware (ARS-003).

8. **Corpus Import → Native Discovery → Duplicate Detection → Provenance:** A valid new SQLite row is transformed by the shared fingerprint function and stored as `user_import` with matching eligibility enabled, then a native write for the same video ID occurs before duplicate matching. The same unique row remains eligible after the native write's incoming false flag, but evaluation of that ID returns no match. A distinct video ID with an equal normalized canonical title fingerprint can return the imported row as its exact reference; a distinct ID may also use it in the fuzzy candidate path. The detector excludes same-ID rows from both passes and the pipeline therefore creates no self `duplicate_match` event. For a later distinct match with native observations on both sides, the earlier observed video's channel receives discovery credit and the later video's channel receives relevance credit, independent of detector match direction. An import association, missing/equal/unparseable time, or legacy self-match row earns no chronology-based credit. The flag merge does not create route/action/credit evidence. Same-ID import retries preserve existing state; invalid rows do not enter the candidate set.
9. **Corpus Import → Durable Completion Metadata:** A successful full import through either the web path or `cmd/migrate` commits the one-row UTC timestamp only after every source row is reconciled and the actual destination count is available. Accounted row-level errors and all-skipped imports still record success; cancellation, fatal errors, or metadata transaction failure do not. After restart the metadata endpoint returns the marker while the job-status endpoints retain their existing no-state behavior. Migration of a preexisting corpus leaves the timestamp null rather than inferring a completion date from video or source-run timestamps.

### Production-Owned Components (Cannot Be Mocked in Product Acceptance)

- YouTube Data API v3 interaction (OAuth, quota, playlist management)
- SQLite database with FTS5
- SSE connection for real-time progress
- OAuth2 flow end-to-end

### Failure/Recovery Scenarios

1. Process crash mid-run: run status = interrupted, resumes on next run
2. Quota exhaustion mid-run: remaining work deferred, processed in next run
3. Auth token expiry mid-run: remaining work deferred, user prompted to re-authorize
4. Processing phase failure or panic (e.g. no active token): run status = failed, terminal, not auto-resumed
5. Metadata expiry: evidence degrades gracefully, one-click refresh restores
6. Database corruption: SQLite backup (file copy) restores to last consistent state

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

1. **Implementation / deployment owners — blocks automatic TLS and non-local application use:** implementation must add explicit app router TLS activation, reconcile README's Ingress-provider `allowEmptyServices: true` prerequisite, and prove the corrected real-provider path (ARS-003). Deployment must confirm Traefik v3, `IngressClass/traefik`, Middleware CRD/provider and naming mode, Ingress-provider empty-service handling and shared-controller effects, non-TLS `web` plus `websecure` listeners, TLS pass-through, and load-balancer exposure; install/confirm cert-manager ingress-shim and provide a production ClusterIssuer referencing its cluster-held ACME account-key Secret (created/managed by cert-manager). Choose the actual issuer name (`letsencrypt-prod` is only the documented default), confirm public DNS points `yuhaul.bateau.cloud` at the intended controller, and determine whether public port 80 is reachable: use HTTP-01 with solver class `traefik` when it is, otherwise configure DNS-01 with external DNS-provider credentials. Confirm HTTPS application access can remain unavailable until the certificate is trusted. Verify certificate issuance/renewal, active HTTP-01 route priority above the sink, and the assembled cluster request path. No cluster behavior is verified here (ADR-010, ARS-003).
2. **Deployment owner — blocks non-local Ingress exposure:** provision the `yuhaul-basic-auth` credentials Secret outside source control; cert-manager creates and renews `yuhaul-tls` after the ClusterIssuer is configured.
3. **Security owner / user — blocks a claim of fully safe public exposure:** assess residual CSRF, XSS, rate-limit, and credential-sharing risks under the intended browser/network threat model. BasicAuth provides the selected Ingress authentication boundary but does not claim these controls.
4. **No duplicate-handling choice remains:** BR-008 AC9–AC11 and the user's “Continue, yes” fix continuation, superseding only legacy AC6's selectable handling. ADR-017/ARS-005 settle compatibility with no skip activation or history rewrite; implementation and independent confirmation remain pending. Pairwise attribution, unknown chronology, and nontransitive credit remain settled by ADR-014. The all-route-excluded evidence decision is unchanged: no lookup/new match evidence under AC7. Native-reference eligibility beyond the current routed-only native realization and other legacy-document conflicts remain with the product / requirements owner; they do not block this amendment. Complete restore activation and broader historical failure/zero-route accounting are separate existing concerns, not new behavior silently supplied here.

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
