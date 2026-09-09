---
type: requirement-index
title: YuHaul Behavioral Requirements
description: Cohesive view of all behavioral requirements for the YuHaul YouTube Discovery & Routing system.
tags: [requirement-index, yuhaul]
---

**Status:** accepted

## Overview

These requirements define the behavioral obligations for the YuHaul system — a personal, stateful discovery and routing system for content published by subscribed YouTube channels. Every requirement traces to one or more acceptance criteria in the [YuHaul anchor](../../anchors/youtube-discovery-routing.md). 32 acceptance criteria produce 36 behavioral requirement documents.

## Requirements

### Account & Authentication

- **Account Connection** — file `account-connection.md` — MUST: User can establish a connection to a YouTube account (AC 1)
- **Account Connection Security** — file `account-connection-security.md` — MUST: Credentials are protected and API access is restricted to authorized accounts (AC 1)

### Discovery

- **Subscription Discovery** — file `subscription-discovery.md` — MUST: System discovers newly published videos from subscribed channels without continuous monitoring (AC 2)

### Routes, Conditions, and Actions

- **Route Definition** — file `route-definition.md` — MUST: User can define multiple ordered routes (AC 3)
- **Route Conditions and Actions** — file `route-conditions-actions.md` — MUST: Each route has configurable conditions and actions (AC 4)
- **Condition Composition** — file `condition-composition.md` — MUST: Multiple conditions within a route compose with explicit AND/OR toggle semantics (AC 4)
- **Multi-Route Matching** — file `multi-route-matching.md` — MUST: A video can match multiple routes until a matching final route terminates evaluation (AC 5)
- **Final Route** — file `final-route.md` — MUST: A route can be marked final to terminate evaluation (AC 6)
- **Exclusion Behaviors** — file `exclusion-behaviors.md` — MUST: Route-scoped exclusion rules for channels, videos, titles, words, and duration (AC 7)

### Duplicate Detection

- **Duplicate Detection** — file `duplicate-detection.md` — MUST: Exact and configurable fuzzy duplicate detection against the corpus of routed titles (AC 8)
- **Historical Title Persistence** — file `historical-title-persistence.md` — MUST: Historical titles persisted; corpus importable (AC 9)
- **Duplicate Provenance** — file `duplicate-provenance.md` — MUST: Duplicate provenance retained to distinguish discovery credit (AC 10)

### Processing State and Idempotency

- **Processing State Persistence** — file `processing-state-persistence.md` — MUST: Processing state survives across runs and restarts, including three-tiered outcome model (AC 11)
- **Idempotent Execution** — file `idempotent-execution.md` — MUST: Repeated execution does not repeat completed actions (AC 12)

### Quota Management

- **Quota Deferral** — file `quota-deferral.md` — MUST: Quota-limited work deferred, not lost or falsely rejected (AC 13)
- **Quota Accounting** — file `quota-accounting.md` — MUST: Resource consumption accounted for by operation type, per run, cumulatively, with remaining capacity (AC 18)
- **Quota-Aware Processing** — file `quota-aware-processing.md` — MUST: Processing avoids unnecessary external expenditure (AC 19)

### Decision Evidence and Explainability

- **Decision Evidence** — file `decision-evidence.md` — MUST: Every route decision explainable from durable evidence (AC 15)

### Runs and Scheduling

- **Scheduled Runs** — file `scheduled-runs.md` — MUST: Runs configurable by user on a schedule (AC 16)
- **Manual Runs** — file `manual-runs.md` — MUST: User can manually initiate runs (AC 17)

### Error Handling and Recovery

- **Error Queue** — file `error-queue.md` — MUST: Failed work in inspectable queue with retry/dismissal; dismissal preserves historical evidence (AC 14)
- **Failure Retention** — file `failure-retention.md` — MUST: Temporary failures do not silently lose eligible work; quota exhaustion is not classified as failure (AC 30)

### Statistics and Analytics

- **Run-Level and Route-Level Statistics** — file `run-route-statistics.md` — MUST: Operational statistics at run and route level with rejection counts by reason (AC 20)
- **Source/Channel Statistics** — file `source-channel-statistics.md` — MUST: Channel yield and usefulness statistics including playlist retention counts (AC 21)
- **Retention/Value Signals** — file `retention-signals.md` — MUST: User-retention signals measured from designated retention playlists, excluding YuHaul-routed membership (AC 22)
- **Retention-Signal Configuration** — file `retention-signal-configuration.md` — MUST: User designates retention playlists; system distinguishes YuHaul-routed from user-driven membership (AC 22)
- **Temporal Statistics** — file `temporal-statistics.md` — MUST: Statistics viewable historically over time (AC 23)
- **Trend Visualization** — file `trend-visualization.md` — MUST: Useful trends visualized graphically (AC 24)

### Configuration History

- **Configuration History** — file `configuration-history.md` — MUST: Full effective route configuration preserved for reconstruction at any point in time (AC 25)
- **Historical Re-evaluation** — file `historical-reevaluation.md` — MUST: Configuration changes never silently reprocess historical videos; re-evaluation is explicit user-triggered operation (AC 25)

### Dry-Run and Backtesting

- **Dry-Run Evaluation** — file `dry-run.md` — MUST: Routing evaluation of current pending workload without mutating external state (AC 26)
- **Historical Backtesting** — file `backtesting.md` — MUST: Backtesting replays historical observations against proposed configurations without altering any state (AC 27)
- **Run Inspection** — file `run-inspection.md` — MUST: User can inspect historical runs and understand outcomes (AC 28)

### User Authority and Administration

- **Responsive Administration Interface** — file `responsive-administration.md` — MUST: Admin interface usable on desktop and mobile (AC 29)
- **User Authority** — file `user-authority.md` — MUST: System advises but does not autonomously subscribe, unsubscribe, or change policy (AC 31)
- **Outcome Distinction** — file `outcome-distinction.md` — MUST: Three-tiered outcome model distinguishing processing state, per-route decisions, and per-action outcomes (AC 32)

## AC Coverage Matrix

| AC | Title | Requirement(s) |
|---|---|---|
| 1 | Account connection | account-connection, account-connection-security |
| 2 | Subscription discovery | subscription-discovery |
| 3 | Ordered routes | route-definition |
| 4 | Conditions/actions | route-conditions-actions, condition-composition |
| 5 | Multi-route matching | multi-route-matching |
| 6 | Final route | final-route |
| 7 | Exclusion behaviors | exclusion-behaviors |
| 8 | Duplicate detection | duplicate-detection |
| 9 | Historical titles | historical-title-persistence |
| 10 | Duplicate provenance | duplicate-provenance |
| 11 | Processing state | processing-state-persistence |
| 12 | Idempotent execution | idempotent-execution |
| 13 | Quota deferral | quota-deferral |
| 14 | Error queue | error-queue |
| 15 | Decision evidence | decision-evidence |
| 16 | Scheduled runs | scheduled-runs |
| 17 | Manual runs | manual-runs |
| 18 | Quota accounting | quota-accounting |
| 19 | Quota-aware processing | quota-aware-processing |
| 20 | Run/route statistics | run-route-statistics |
| 21 | Source/channel statistics | source-channel-statistics |
| 22 | Retention signals | retention-signals, retention-signal-configuration |
| 23 | Temporal statistics | temporal-statistics |
| 24 | Trend visualization | trend-visualization |
| 25 | Configuration history | configuration-history, historical-reevaluation |
| 26 | Dry-run | dry-run |
| 27 | Backtesting | backtesting |
| 28 | Run inspection | run-inspection |
| 29 | Responsive admin | responsive-administration |
| 30 | Failure retention | failure-retention |
| 31 | User authority | user-authority |
| 32 | Outcome distinction | outcome-distinction |

## Cross-requirements

Dependencies between requirements (predecessor → successor):

- **account-connection** → subscription-discovery, route-conditions-actions, quota-aware-processing, retention-signals, retention-signal-configuration
- **route-definition** → route-conditions-actions, condition-composition, multi-route-matching, final-route, exclusion-behaviors, run-route-statistics, source-channel-statistics, configuration-history, dry-run, historical-reevaluation, retention-signal-configuration
- **route-conditions-actions** → condition-composition, multi-route-matching, final-route, idempotent-execution, decision-evidence, user-authority, historical-reevaluation
- **condition-composition** → multi-route-matching, final-route
- **processing-state-persistence** → idempotent-execution, quota-deferral, error-queue, decision-evidence, run-route-statistics, source-channel-statistics, manual-runs, configuration-history, outcome-distinction
- **duplicate-detection** → historical-title-persistence, duplicate-provenance
- **quota-accounting** → quota-aware-processing
- **quota-deferral** → failure-retention, quota-aware-processing
- **decision-evidence** → run-inspection, user-authority
- **run-route-statistics** → temporal-statistics, trend-visualization, outcome-distinction, run-inspection
- **source-channel-statistics** → temporal-statistics
- **retention-signal-configuration** → retention-signals
- **retention-signals** → source-channel-statistics
- **temporal-statistics** → trend-visualization
- **dry-run** → backtesting
- **historical-title-persistence** → backtesting
- **configuration-history** → backtesting, historical-reevaluation
- **error-queue** → failure-retention

## Source

All requirements in this domain derive from YuHaul — YouTube Subscription Discovery & Routing at `../../anchors/youtube-discovery-routing.md`.

## Open questions

None at this time. The anchor's 32 acceptance criteria are fully covered by the 36 requirement documents above.
