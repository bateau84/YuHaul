---
type: Log
title: Live Status Change Log
description: Changes to the live-status architecture bundle.
tags: [changelog, architecture, live, upcoming]
timestamp: 2026-11-19T00:00:00Z
---

# Live Status Change Log

## 2026-11-19

**Architecture proposed:** [ARD-001](/docs/architecture/live-status/design/ard-001-fresh-live-status-gate.md) and [ARS-001](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) realize BR-042. The gate is on the reused `FetchForEvaluation` read, with a same-run Store A stamp and a fail-closed classifier. Queue items carry a `live_hold` overlay. Backstops guard evaluation and the send guard. The migration follows the receipt and Discovery migrations, with no number. AC7 is designed for its default of no limit, with a single cap seam. No implementation and no critique yet.

**Revised after critic `ses_e0ca5c4cdffeU13eZuktRfYwvH` (CHANGES REQUIRED).**
- The clearance moved from a Store A stamp to `run_video_outcome.live_status`, so expiry can no longer lose it.
- The gate runs at every pass entry, whatever phase the pass resumes at.
- Fence and backstop refusals end in a live deferral, never in `failed`.
- ARS-005 §6.1–6.2 is a hard prerequisite for claimed paths.
- New discoveries get native retention: an atomic deferral plus a pending item.
- The main-loop gate requests `Live` only.
- A live skip always leaves one video-scoped item, so a capacity eviction cannot strand the video.
- The extra quota for claimed items is stated.
- The queue filter covers active rows only.
- Version-0 continuation is recorded as unreachable.

**Accepted by critic `ses_e0ca5c4cdffeU13eZuktRfYwvH`; precision fixes in ARS-001.**
- §5.1: the main-loop gate runs only on the set left after `withoutRetryVideos` and `withoutUnroutableVideos`.
- §6.2: the live post-evaluation deferral uses a separate handoff that carries a queue reason with its hold and a separate run reason. It commits inside the guarded path and returns `(Deferred, nil)`, never through `deferActions`.
- §4: the discovery prefetch must keep the returned IDs (`engine.go:402`).

The §10 cross-document amendments are not yet applied.
