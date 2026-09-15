---
type: obligation-contract
title: OC-003 — Configuration Snapshot for Historical Interpretation
description: Stable configuration snapshots attached to runs so historical statistics and backtesting can be interpreted in the context of the active configuration
tags: [obligation-contract, configuration, snapshot, history, statistics, backtesting]
---

**Status:** proposed

## Participants
- **Configuration Manager**: the component that stores and manages route, exclusion, and duplicate detection configuration.
- **Run Orchestrator**: the component that initiates and records runs.
- **Statistics/Reporting**: the component that aggregates and presents historical statistics.
- **Backtesting Engine**: the component that evaluates proposed or historical route configurations against retained observations.

## Preconditions
- Route, exclusion, and duplicate detection configuration exists and is modifiable by the user (BR-003, BR-004, BR-007, BR-008).
- Configuration changes are recorded with timestamps and previous state (BR-025).
- Runs are recorded with timestamps (BR-011).

## Guarantees
1. When a run begins, the Configuration Manager produces a configuration snapshot that captures the active route definitions, exclusion rules, and duplicate detection settings.
2. The snapshot is associated with the run record and persists durably.
3. The snapshot captures configuration state at the point of run initiation, not at run completion.
4. Statistics/Reporting can link any historical run to its configuration snapshot for interpretation.
5. Backtesting can apply any historical configuration snapshot to a corpus of retained observations.
6. Configuration snapshots are immutable once captured; subsequent configuration changes do not alter a captured snapshot.
7. The user can view the configuration snapshot for any historical run through the inspection UI.
8. Configuration diffs between two snapshots can be computed and presented to the user.

## Semantic inputs and outputs
- **Input**: Current configuration state at run initiation.
- **Output**: Immutable configuration snapshot linked to run record.

## Failure semantics
- If snapshot production fails at run initiation, the run is deferred (not executed without a captured snapshot) to preserve interpretability.
- If a snapshot is corrupted or missing for a historical run, statistics for that run are flagged as having incomplete configuration context.

## Invariants
- Every run has an associated configuration snapshot.
- Captured snapshots are immutable.
- Historical statistics are interpretable only in the context of their associated snapshot.
- Backtesting uses snapshots, not current configuration, unless explicitly selected by the user.

## Derived from
- Anchor: youtube-discovery-routing.md (AC25, AC27)

## Verified by
- Unit test: run record includes configuration snapshot at initiation time.
- Unit test: snapshot is immutable; subsequent config changes do not alter it.
- Unit test: historical run inspection shows associated configuration snapshot.
- Backtesting test: historical snapshot applied to corpus produces expected outcomes.
- Diff test: configuration diff between two snapshots is correct.

## Open questions
- None
