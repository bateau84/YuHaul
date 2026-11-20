---
type: requirement
title: BR-011 — Processing State Persistence
description: Processing state survives across runs and application restarts
tags: [requirement, state, persistence, resilience, startup, priority:MUST]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL persist all processing state such that the current run position, discovered videos, evaluation progress, and action completion status survive across runs and application restarts.

## Acceptance criteria

1. A run interrupted mid-evaluation resumes from the last persisted state rather than restarting from scratch.
2. The system tracks which videos have been discovered, evaluated, and acted upon.
3. Processing state is durably stored and not dependent on in-memory state alone.
4. The system can reconstruct its current processing position from persisted state after restart.
5. Partially completed work is distinguishable from fully completed work.
6. State corruption or incompleteness is detectable and reported rather than silently ignored.
7. When startup recovery finds saved run state that is damaged or invalid, production work SHALL NOT start, as before: the corrupt state is reported, not skipped and not rewritten as resumable. The only exception is a time-limit-interrupted manual run that cannot be continued safely. Startup recovery leaves that run `interrupted` and unchanged, records a safe log entry for it and continues starting, as [BR-017](br-017-manual-run-initiation.md) AC11 specifies, so the user can discard it. That run is still reported, so AC6 still holds for it.

## Derived from

- Anchor: youtube-discovery-routing.md (AC11)
- AC7: the existing startup rule that corrupt saved state blocks production, which BR-017 relies on, plus its one exception from the user's explicit decision of 2026-11-10 (see BR-017, Derived from).

## Verified by

- Resilience test: interrupt a run mid-evaluation, restart, confirm resume from persisted state.
- Persistence test: all processing state queryable after restart.
- Corruption test: incomplete state is detected and reported.
- Startup with corrupt state (AC7): a corrupt run other than an unusable time-limit-interrupted manual run blocks production at startup and is reported. An unusable time-limit-interrupted manual run does not block startup; see BR-017 verification "Startup with an unusable run".

## Depends on

- None. AC7 refers to [BR-017](br-017-manual-run-initiation.md) AC11 for the exception.
