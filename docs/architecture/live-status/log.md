---
type: Log
title: Live Status Change Log
description: Changes to the live-status architecture bundle.
tags: [changelog, architecture, live, upcoming]
timestamp: 2026-11-22T00:00:00Z
---

# Live Status Change Log

## 2026-11-22

**AC7 live check limit added (Plan r30 `b42-ac7-arch`).** [ARS-001](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §8.3 and [ARD-001](/docs/architecture/live-status/design/ard-001-fresh-live-status-gate.md) *Live check limit (AC7)* realize BR-042 AC7 as amended on 2026-11-19.
- **Setting.** `settings.live_check_limit_days`, seeded 30. `GET`/`PUT /api/settings/live-check-limit` accept only a JSON integer token in 1–365. A refusal is `400 invalid_live_check_limit`; a storage failure is `500 live_check_limit_save_failed`. Saving makes no configuration version and no YouTube call.
- **Window start.** `deferred_item.live_window_started_at` is set at the first live skip of a hold (the run's `live_checked_at`), reset by a still-held check in a `queue_retry` run, and cleared with the hold on confirmation. Nothing else writes it. A table CHECK ties it to `live_hold`.
- **Claim filter.** A per-video `LiveWindowClosed` predicate runs in the candidate list and in the claim writer's recheck, for automatic claims only. Claim kinds now separate automatic claims, Deferred Retry now and Errors retry. The claimed gate releases an unread claim whose window has closed, restoring the video's deferred state (`releaseClosedWindowTx`).
- **BR-035 AC14.** Window-closed videos are outside the eligible set. Creation order is unchanged, and a re-entering item keeps its place.
- **Read models.** `live_check` (`window_started_at`, `checks_stopped`, `checks_stopped_at`) on Deferred items. `live_retry.result` (`not_checked`, `not_live`, `live_or_upcoming`) on the Retry now response. These meet design `4703faa` capabilities (f)–(h).
- **Migration.** §8.2 replaces the stale "after 029 / no number" text: one migration takes the next free number on `main` (now ending at `031`) at merge, under Plan phase `migrations`. It adds the window column, the CHECK and the setting seed. Upgrade evidence starts from the then-production `030` or `031`.
- **Prerequisite recheck.** ARS-005 §5.1.1 provenance is built (`031`). §5.2 and §6.1–6.2 are not.
- **AC5** is marked resolved (requirements 33bea6f).
- Tests W1–W12 added; ARS-001 §8.3.9 maps every AC7 clause and the *Check window* example to a mechanism and a test.
- **Not yet applied.** The amendments to ARS-005, video-search ARS-001, capacity ARD-001 and safe-diagnostic ARS-001 are listed in ARS-001 §10 for their owners.
- **Open question for requirements.** The example's "new 10-day window" after X was lowered to 2 (ARD-001 open question 5).

Awaiting critique (`b42-ac7-critique`). Docs only.

**AC7 revised after critic `ses_dff71050fffeg7LIm7w3HonCUr` (CHANGES REQUIRED, six findings, all accepted or routed).**
1. A hold is never written without a start: `COALESCE(live_checked_at, transaction time)`. `:fresh` needs a recorded non-`none` check in a `queue_retry` run. L14 and W9 are extended.
2. `state.RestorePriorDeferralTx` sets `processing_state = 'deferred'` explicitly. It runs on every outcome-less release of a held video: the claimed-gate release, the Stop/Discard handoff and the run-failure sweep. Amendments for ARS-003 §2 and ARS-005 §5.1.1 are listed in ARS-001 §10. W5 and W6 are fixed.
3. New `run_video_outcome.claim_released_reason = 'live_window_closed'`. `unfinishedRunMembersTx` treats such a member as settled, so the handoff and sweep leave it alone.
4. An unreadable limit reads as "checks stopped" (no time) in inspection, and the claimed gate releases.
5. W11 depends on ARS-005 §5.2 (quota-bounded claiming, not built).
6. The BR-042 *Check window* example is corrected (the new window uses the current X). The Retry-now-without-check choice is open for the requirements owner (ARD-001 open question 5).

Awaiting the critic's recheck. Docs only.

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

**§10 amendments applied (Plan r29 `b42-amend`).**
- [ARS-005](/docs/architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md) §6.1.2 and §6.2 *Live gate*.
- [ARS-006](/docs/architecture/run-initiation/specifications/ars-006-final-outcomes-are-never-reprocessed.md) §7.2 *Live-status gate before continuation*.
- [Authorization ARS-001](/docs/architecture/youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) *Live-status clearance at the send*.
- [Safe-diagnostic ARS-001](/docs/architecture/run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md) live-status registrations.
- Capacity ARD-001 and Discovery ARS-001 working-tree copies, uncommitted because those documents are still untracked.

No new decision. No conflict with a target contract was found.
