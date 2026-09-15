---
type: requirement
title: BR-011 — Processing State Persistence
description: Processing state survives across runs and application restarts
tags: [requirement, state, persistence, resilience, priority:MUST]
---

**Status:** proposed
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

## Derived from

- Anchor: youtube-discovery-routing.md (AC11)

## Verified by

- Resilience test: interrupt a run mid-evaluation, restart, confirm resume from persisted state.
- Persistence test: all processing state queryable after restart.
- Corruption test: incomplete state is detected and reported.

## Depends on

- None
