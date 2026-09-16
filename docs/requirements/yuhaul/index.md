---
type: requirement-index
title: YuHaul — Behavioral Requirements & Obligation Contracts
description: Obligation layer for the YouTube subscription discovery and routing system, derived from the accepted Anchor
tags: [requirement-index, youtube, discovery, routing, analytics]
---

**Status:** accepted

## Source

- Anchor: ../../anchors/youtube-discovery-routing.md

## Behavioral Requirements

| ID | Title | File | Priority | Summary |
|----|-------|------|----------|---------|
| BR-001 | Secure YouTube Account Connection | br-001-secure-youtube-account-connection.md | MUST | User can securely connect and manage YouTube account authorization |
| BR-002 | Subscription-Based Discovery Without Live Monitoring | br-002-subscription-discovery-without-live-monitoring.md | MUST | System discovers new videos from subscriptions via polling, not continuous monitoring |
| BR-003 | Multiple Ordered Route Definition | br-003-multiple-ordered-route-definition.md | MUST | User can create, read, update, delete, and reorder routes evaluated in declared order |
| BR-004 | Route Conditions and Playlist Actions | br-004-route-conditions-and-playlist-actions.md | MUST | Routes contain configurable conditions that trigger playlist actions |
| BR-005 | Multi-Route Video Matching | br-005-multi-route-video-matching.md | MUST | A video may satisfy conditions of multiple routes |
| BR-006 | Final Route Termination | br-006-final-route-termination.md | MUST | A route marked final terminates further evaluation after successful match |
| BR-007 | Exclusion Behaviors | br-007-exclusion-behaviors.md | MUST | Exclusion filters for channels, videos, titles, words, and duration |
| BR-008 | Exact and Fuzzy Duplicate Detection | br-008-exact-and-fuzzy-duplicate-detection.md | MUST | Historical duplicate detection via exact and configurable fuzzy matching |
| BR-009 | Historical Title Persistence and Corpus Import | br-009-historical-title-persistence-and-corpus-import.md | MUST | Historical titles preserved; existing corpus importable |
| BR-010 | Duplicate Provenance for Discovery Credit | br-010-duplicate-provenance-for-discovery-credit.md | MUST | Duplicate provenance distinguishes discovery credit from other source relevance |
| BR-011 | Processing State Persistence | br-011-processing-state-persistence.md | MUST | Processing state survives across runs and restarts |
| BR-012 | Idempotent Playlist Actions | br-012-idempotent-playlist-actions.md | MUST | Repeated execution does not repeat completed playlist mutations |
| BR-013 | Quota-Limited Work Deferral | br-013-quota-limited-work-deferral.md | MUST | Quota-limited work deferred rather than lost or falsely rejected |
| BR-014 | Failed Work Retention and Retry | br-014-failed-work-retention-and-retry.md | MUST | Failed work retained in inspectable queue with retry and removal |
| BR-015 | Explainable Route Decisions | br-015-explainable-route-decisions.md | MUST | Every meaningful route decision explainable from durable evidence |
| BR-016 | Configurable Scheduled Runs | br-016-configurable-scheduled-runs.md | MUST | User configures when processing runs execute |
| BR-017 | Manual Run Initiation | br-017-manual-run-initiation.md | MUST | User can trigger a processing run on demand |
| BR-018 | API Consumption Accounting and Visibility | br-018-api-consumption-accounting-and-visibility.md | MUST | External API consumption tracked and visible |
| BR-019 | Quota-Aware Processing | br-019-quota-aware-processing.md | MUST | Processing avoids unnecessary external API expenditure |
| BR-020 | Run-Level and Route-Level Statistics | br-020-run-level-and-route-level-statistics.md | MUST | Operational statistics available at run and route level |
| BR-021 | Source and Channel Usefulness Statistics | br-021-source-and-channel-usefulness-statistics.md | MUST | Source and channel contribution to value measured |
| BR-022 | User Retention and Value Signals | br-022-user-retention-and-value-signals.md | SHOULD | User retention signals measured where reliable provider evidence permits |
| BR-023 | Historical Statistics Over Time | br-023-historical-statistics-over-time.md | MUST | Statistics viewable over time, not only lifetime counters |
| BR-024 | Trend Visualization | br-024-trend-visualization.md | SHOULD | Useful trends rendered graphically |
| BR-025 | Configuration History for Statistics Interpretation | br-025-configuration-history-for-statistics-interpretation.md | MUST | Route/configuration history sufficient to interpret historical statistics |
| BR-026 | Dry-Run Evaluation | br-026-dry-run-evaluation.md | MUST | Dry-run evaluates routes without mutating external state |
| BR-027 | Historical Backtesting | br-027-historical-backtesting.md | MUST | Backtesting compares routing changes against retained observations without external mutation |
| BR-028 | Historical Run Inspection | br-028-historical-run-inspection.md | MUST | User inspects historical runs and understands accepted/rejected/deferred/failed decisions |
| BR-029 | Responsive Administration Interface | br-029-responsive-administration-interface.md | MUST | Administration usable on desktop and mobile |
| BR-030 | Temporary Failure Work Preservation | br-030-temporary-failure-work-preservation.md | MUST | Temporary failures do not silently lose eligible work |
| BR-031 | User Authority Over Policy Decisions | br-031-user-authority-over-policy-decisions.md | MUST | System advises with evidence but does not autonomously change policy |
| BR-032 | Processing Outcome Distinction | br-032-processing-outcome-distinction.md | MUST | Outcomes distinguish rejected, deferred, failed, and not-yet-evaluated |

## Obligation Contracts

| ID | Title | File | Seam | Summary |
|----|-------|------|------|---------|
| OC-001 | YouTube API Quota and Failure Semantics | oc-001-youtube-api-quota-and-failure-semantics.md | System ↔ YouTube Data API v3 | Quota exhaustion treated as operational state; failures deferred not lost; consumption accounted |
| OC-002 | Route Evaluation Outcome Semantics | oc-002-route-evaluation-outcome-semantics.md | Evaluation Engine ↔ State/History/Statistics | Accepted vocabulary for routing outcomes shared across evaluation, persistence, and reporting |
| OC-003 | Configuration Snapshot for Historical Interpretation | oc-003-configuration-snapshot-for-historical-interpretation.md | Configuration Manager ↔ Statistics/Backtesting | Stable configuration snapshots attached to runs for historical statistics interpretation |

## Dependencies

- BR-015 (Explainable Decisions) depends on BR-003–BR-006 (Route Configuration) for decision logic and BR-032 (Outcome Distinction) for classification vocabulary.
- BR-023 (Historical Statistics) depends on BR-025 (Configuration History) for meaningful interpretation.
- BR-027 (Backtesting) depends on BR-008–BR-010 (Duplicate Detection) and BR-015 (Explainable Decisions) for retained observations.
- OC-002 provides shared vocabulary consumed by BR-015, BR-020, BR-028, and BR-032.
- OC-003 is consumed by BR-023, BR-025, and BR-027.

## Open Questions

- None — all 32 ACs are fully covered by the obligation baseline above.
