---
type: decision
title: ADR-002 — Backtest and Replay Select by Locally Eligible YouTube Published Date
description: Persist the requested UTC day range on the run row, validate it before any run or snapshot exists, and filter retained videos in Go by the published date of an eligible local metadata row, without calling YouTube.
tags: [architecture, decision, backtest, replay, date-range, metadata, retention]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — technical realization of BR-027 AC8–13. Implemented 2026-11-10 (date-range commit on PR #4); independent review pending. Two realization findings are recorded below and in [ARS-001 §10](../specifications/ars-001-run-initiation-schema-and-api.md#10-implementation-record-and-deviations). The exact contract is [ARS-001](../specifications/ars-001-run-initiation-schema-and-api.md).

## Summary

A Backtest or Replay request may carry `published_from` and `published_to` as `YYYY-MM-DD` strings. The server validates them before it creates anything. The run row stores them as two immutable text columns. At execution, the selector reads every retained video together with its local `transient_metadata` row and keeps a video only when that row is *currently eligible* (the same release rule as current/local display) and its `published_at` falls in `[from 00:00Z, to+1 day 00:00Z)`. The selected count is the size of that filtered set. With no range, selection is unchanged.

Example: range `2026-10-01`..`2026-10-31`. A video published `2026-10-31T23:59:59Z` is selected; one published `2026-11-01T00:00:00Z` is not. A video whose metadata row is missing or expired is not selected and does not appear in any count. No provider request is made. Run inspection later shows `published_range: {"from":"2026-10-01","to":"2026-10-31"}`, even after restart and after the controls change.

## Satisfies

| Requirement | How |
|---|---|
| [BR-027](../../../requirements/yuhaul/br-027-historical-backtesting.md) AC8 | Half-open UTC interval computed from calendar days; comparison of decoded instants, not text. |
| AC9 | Each absent bound means unbounded on that side. Both absent means the filter is skipped entirely, including undated videos. |
| AC10 | With any bound set, a video without an eligible local published date is dropped before evaluation. Selection is one local SQL read with no metadata manager, refresh or provider call. No other timestamp substitutes. |
| AC11 | Validation in the handler before proposal ingest or config resolution, and again in the pipeline before `ingestProposalRun`/`CreateRun`. A SQLite trigger rejects an invalid stored range as a last guard. |
| AC12 | `captureSelectedCount(len(filtered))`. Only filtered videos are evaluated, so decision-derived counts, outcomes and filters cover only them. An empty selection completes with `0`. |
| AC13 | Two columns written in the run's `INSERT`; an update trigger rejects any change; the range is projected on every run read. |
| BR-027 AC4/AC7, [BR-026](../../../requirements/yuhaul/br-026-dry-run-evaluation.md) isolation | Unchanged: selection is a read-only local query; no provider seam is reachable from it. |
| [BR-015 AC7](../../../requirements/yuhaul/br-015-explainable-route-decisions.md) | The published date is used only as current/local information. No historical provider snapshot is created. |

Satisfied elsewhere: date-control layout, copy, the invalid-range message presentation and saved-range display (Designer). Dry-run (`POST /api/runs/dry-run`) has no date controls and is out of scope.

## Current source (read 2026-11-10)

- `internal/pipeline/replay.go:99–150`: `executeHistorical` calls `stateStore.ListRetainedVideos` (all `video` rows, `internal/state/store.go:605–627`), then `captureSelectedCount(len(videos))`, then evaluates each video. The comment at `:128` records that the date filter was deferred.
- `internal/store/migrations/002_metadata_lifecycle.up.sql`: `transient_metadata.published_at TIMESTAMP`, written by discovery (`internal/discovery/engine.go:485–505`, a bound `time.Time`) and metadata refresh. Expiry (`internal/metadata/store.go:263–271`) nulls text fields and sets `is_active=0`/`expired_at`, but keeps `published_at`.
- Migration 011 records that the SQLite driver stores a bound `time.Time` in Go's `String()` form, which does not compare correctly as text against RFC3339.
- `internal/server/server.go:678–780`: Replay reads `config_version_id` from the query or the body. Backtest reads `proposed_configuration` from the body. Neither reads a date.
- `web/src/views/BacktestView.tsx:162–163, 519–531`: `dateFrom`/`dateTo` state and inputs exist, but are not sent.
- [Decision-evidence ARD-001](../../decision-evidence/design/ard-001-run-attributed-evidence-and-metadata-lifecycles.md) §Local reader: "Do not resurrect retained duration/publication values from an inactive/expired row." [ARS-001](../../decision-evidence/specifications/ars-001-current-local-run-video-metadata.md) fixes that release rule.

## Decision 1 — which published date is "available locally"

Use `transient_metadata.published_at` only from a row that passes the current/local release rule at the selection cutoff (the instant the selector runs): `is_active = 1`, `expired_at IS NULL`, `expires_at` decodes, and `expires_at` is strictly after the cutoff. Any other case (no row, ineligible row, `NULL` or undecodable `published_at`) means "no published date available locally".

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **Eligible-row `published_at` only (chosen)** | Same boundary as accepted current/local display and as backtest evaluation itself (`GetDisplayMetadata` rejects expired rows). No new use of expired provider data. | Videos whose metadata has expired are left out of a ranged backtest (see consequence). | **Chosen**: the only option consistent with accepted INV-9 and the decision-evidence release rule. |
| `published_at` from any row, including expired | Selects more old videos | Reuses provider data past its lifecycle, which the accepted release rule forbids. Expiry keeping `published_at` is a retention gap, not a permission. | Rejected without a compliance basis. |
| `video.discovered_at` or import time as a fallback | Always present | BR-027 AC10 forbids substitution explicitly. | Rejected. |
| Fetch missing dates from YouTube | Complete | BR-027 AC4/AC10 forbid calls. | Rejected. |

**Consequence the product owner should know:** metadata expires on the accepted 30-day lifecycle unless it is refreshed. A ranged Backtest or Replay over a period whose videos' metadata has expired will select few or none of them, and complete with a correspondingly small or zero count. This follows from the accepted retention model, not from this design. It is reported as a finding below, not escalated as a blocker. AC10 already accepts that undated videos are left out.

## Decision 2 — where the range lives

Two nullable `TEXT` columns on `run`, `published_from` and `published_to`, in canonical `YYYY-MM-DD` form. They are written in the same `INSERT` that creates the run: inside `ingestProposalRun`'s transaction for Backtest, and through `CreateRun` for Replay. They are guarded by triggers: allowed only for `backtest`/`replay` run types, well formed, from ≤ to, and immutable after insert.

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| **Run-row columns (chosen)** | Atomic with run creation. Read with the run. Fits the existing write-once run evidence (`selected_video_count`, authority versions). | Two more columns on `run`. | **Chosen.** |
| Store the range inside the ConfigVersion snapshot | No `run` columns | Replay reuses an existing snapshot, so the range would either mutate shared configuration or need a new snapshot (AC11 forbids side effects; OC-003 snapshots are configuration, not selection). | Rejected. |
| Separate `run_selection` table | Extensible | A second write and join for two values; no other selector parameter exists. | Rejected: no demonstrated need. |

## Decision 3 — filter in Go over a single local read

Add one store read that returns, for every retained `video` row, its identity plus the raw `published_at`, `is_active`, `expired_at` and `expires_at` text from a `LEFT JOIN transient_metadata`. Decode and filter in Go. SQL text comparison is not used, because the stored timestamp format is the driver's `String()` form (migration 011). Decoding accepts a fixed list of layouts, including the form the real driver writes. A value that cannot be decoded is treated as "no date", never as a query failure. A query, scan or iteration error fails the run through the existing `failHistoricalRun`, before `captureSelectedCount`.

## Rules the implementation must preserve

1. No range → the selector result is exactly today's `ListRetainedVideos` set, including undated videos.
2. Bound computation: `lower = from 00:00:00Z`; `upper = (to + 1 day) 00:00:00Z`; select when `lower ≤ published_at_utc < upper`, using only the bounds that are present.
3. The selector never calls `metadata.Manager`, `RefreshOnDemand`, `RunLifecycle` or any provider client.
4. An invalid range never reaches `ingestProposalRun`, `resolveReplayConfig`, `CreateRun` or the config store.
5. The stored range is never changed after insert, and every run projection exposes it.
6. The browser sends calendar-day strings exactly as entered. It never converts them through a local `Date`, so a non-UTC browser selects the same set.

## Realization findings (2026-11-10)

- **The stored timestamp has a fourth form.** For a value bound with a numeric-only zone (for example `+02:00`), the SQLite driver writes `2026-11-01 01:00:00 +0200 +0200`. None of the layouts in Decision 3 parses it, so such a video was silently dropped from every range. The decoder now also accepts `2006-01-02 15:04:05.999999999 -0700 -0700`. A value that still cannot be decoded remains "no date", never an error.
- **SQLite `date()` rejects `2026-02-30`.** The insert trigger aborts with `invalid run published range`, so the secondary guard agrees with the Go parser for that day. The Go parser stays authoritative and the application does not rely on the trigger.
- **Not proven:** a real browser in a non-UTC time zone against the running binary (ARS-001 test E2); the Replay HTTP path with a real pipeline (D8 covers the handler with a fake and the boundary set through `Replay` directly).

## Revisit triggers

- Compliance establishes that `published_at` may be kept and used after metadata expiry. Decision 1 could then widen, without a schema change.
- Dry-run gains date controls.
- The owner decides Replay should ignore the range (BR-027 index, open question). The column guard would then narrow to `backtest`.
