---
type: specification
title: ARS-001 — Live-Status Read, Classification, Run-Scoped Clearance and Live Deferral
description: Exact parser rules, the FetchForEvaluation Live requirement, run_video_outcome clearance, gate placement on every pass and resume, native retention, live-deferral transactions, fence and backstop mapping, queue and diagnostic registrations, the AC7 live check limit (setting, check window, claim exclusion, Retry now), simulation behavior, migration content and tests for BR-042.
tags: [architecture, specification, live, upcoming, metadata, deferred-queue, migration, diagnostics, settings]
timestamp: 2026-11-22T00:00:00Z
---

**Status:** proposed exact contract for [ARD-001](../design/ard-001-fresh-live-status-gate.md). Revised 2026-11-19 after critic `ses_e0ca5c4cdffeU13eZuktRfYwvH`, which accepted it. Its precision fixes are applied in §4, §5.1 and §6.2. **Amended 2026-11-22 for BR-042 AC7** (the live check limit; Plan r30 node `b42-ac7-arch`): §1, §5.3, §6, §8.1–8.3 and §9 W1–W12. Revised the same day after critic `ses_dff71050fffeg7LIm7w3HonCUr` (CHANGES REQUIRED, six findings; see ARD-001 *Review disposition*): §6.1 step 4, §5.3, §8.2, §8.3.1–8.3.6 and the tests. Awaiting the critic's recheck. Not implemented.

**Prerequisite state on `origin/main` 8d7e009 (rechecked 2026-11-22).** Claimed paths still require [ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §6.1–6.2 (ARD-001 P1). Of that contract, only the §5.1.1 claim provenance is built (migration `031`, `internal/queue/retry_provenance.go`). Not built: the §6.2 precondition (`FetchForEvaluation`, `internal/metadata/manager.go:406`, still has no production caller, and no `deferRetryForInputsTx` exists), and the §5.2 batched claim loop (`createClaimedRunLocked` lists every candidate unbounded, `internal/pipeline/pipeline.go:252`). The §5.3 claimed gate below is an addition to §6.2 and ships with or after it.

## Satisfies

[BR-042](../../../requirements/yuhaul/br-042-skip-live-and-upcoming-videos-until-ended.md) AC1–AC16, including AC7 as amended on 2026-11-19 and AC5 as clarified on 2026-11-22 (only the video-scoped item counts), and the 2026-11-19 live case of [OC-002](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) Guarantee 5. For AC7 it also preserves the 2026-11-19 *Live check limit* note in [BR-035](../../../requirements/yuhaul/br-035-stop-active-run.md) AC14. ARD-001 *Satisfies* gives the mapping. The tests are in §9; §8.3.8 maps every AC7 clause and the BR-042 *Check window* example to a mechanism and a test.

## 1. Vocabulary

| Name | Values | Where |
|---|---|---|
| `LiveStatus` | `none`, `live`, `upcoming`, `unconfirmed` | Parser result; `run_video_outcome.live_status` |
| Run deferred reason | `live_now`, `live_upcoming`, `live_unconfirmed` | `run_video_outcome.deferred_reason`, `video` deferred reason |
| Action outcome reason | `live_or_upcoming` | Deferred action tail at the fence (§6) |
| Queue origin reason | `live_or_upcoming` | `deferred_item.deferral_reason`, on items that a live deferral creates |
| Queue live hold | `live_now`, `upcoming`, `unconfirmed` | `deferred_item.live_hold`, on video-scoped rows |
| Native retention reasons | Run reason `metadata_unavailable` with queue reason `discovery_processing_pending`, or the existing `quota_exhausted`, `api_rate_limited`, `authorization_pending` | §5.2, matching [Discovery ARS-001](../../discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) |
| Live check limit `X` | Whole days, 1–365, default 30 | `settings` row `live_check_limit_days` (§8.3.1) |
| Window start | UTC timestamp | `deferred_item.live_window_started_at`, set exactly when `live_hold` is set (§8.3.2) |
| Window closed | `now − window start ≥ X × 24 h` on an active held video-scoped row | `queue.LiveWindowClosed` (§8.3.3) |
| Claim released | `live_window_closed` | `run_video_outcome.claim_released_reason`: a member the run released without an outcome (§8.3.5) |

**Terms.** A *held row* is a `deferred_item` row with `status IN ('pending','retrying')`, `live_hold IS NOT NULL`, and that is video-scoped (not a capacity row, as capacity ARD-001 defines one). The *current hold* of a video starts when a live skip sets `live_hold` on a row whose `live_hold` is `NULL`, and ends when §6.3 clears it on confirmation. A *user queue retry run* is a production run with `initiated_by = 'queue_retry'` (`internal/pipeline/retry.go:47–48`).

The mapping is: `live` → run reason `live_now`, hold `live_now`; `upcoming` → `live_upcoming`, hold `upcoming`; `unconfirmed` (including any fence or backstop refusal) → `live_unconfirmed`, hold `unconfirmed`.

## 2. Parser (`youtube.Client.GetVideosWithReturnedIDs`)

- Send `part=snippet,contentDetails,liveStreamingDetails`. Envelope validation is unchanged.
- `VideoIDs` gains `Live map[string]LiveStatus`, with one entry for each ID in `IDs`. Compute it before the `publishedAt` `continue`.
- `GetVideos` (legacy) is unchanged.
- Classify each returned item in this order; the first rule that applies wins:
  1. If the item has a duplicate `snippet` member, or `snippet` is not a duplicate-free object, it is `unconfirmed`.
  2. Let `lbc` be `jsonString(snippet.liveBroadcastContent)`. If that member is duplicated, missing, not a string, or not exactly `live`, `upcoming` or `none`, the item is `unconfirmed`.
  3. If `lbc` is `live`, the item is `live`. If it is `upcoming`, the item is `upcoming`.
  4. If `lbc` is `none` and the item has no `liveStreamingDetails` member, it is `none`.
  5. If `lbc` is `none` and `liveStreamingDetails` is a duplicate member or not a duplicate-free object, the item is `unconfirmed`.
  6. If `lbc` is `none` and `liveStreamingDetails` is an object, the item is `none` only when all of these hold. Otherwise it is `unconfirmed`.
     - `actualEndTime` is present and parses with `parseTime`.
     - `activeLiveChatId` is absent.
     - `concurrentViewers` is absent.
     - `actualStartTime` is either absent, or it parses and is not after `actualEndTime`.
- The parser never reads `contentDetails.duration`. Raw values are never stored, logged or returned.

## 3. Metadata manager

```go
type InputRequirement struct {
    VideoID                                   string
    Title, ChannelName, Description, Duration bool
    Live                                      bool // never satisfied by a stored row
}
type FetchResult struct {
    Details  []TransientMetadata
    Returned []string
    Complete bool
    Live     map[string]LiveStatus // one per Returned id
}
type FetchOutcome struct {
    Statuses        map[string]FetchStatus
    Live            map[string]LiveStatus // only IDs returned by a read in this call
    Read            *ReadFacts
    Attempts, Saved int
}
```

- The `FetchForEvaluation` signature is unchanged. A requirement with `Live` set is never `current`: the ID is always read, even if its other required inputs are held.
- `Live` is copied from each successful chunk. Per-ID `Statuses` keep their ARS-005 §6.1 meanings, and `Live` is reported alongside them, not merged into them.
- No Store A schema or writer changes. `UpsertFetched` keeps its behavior.
- `FetchMetadataBound` copies `returned.Live` into `FetchResult.Live`.
- The caller requests `Live` only for IDs whose current run row has `live_status IS NULL` (§5). The manager has no knowledge of runs.

## 4. Run-scoped record and clearance (package `state`)

- **Columns** on `run_video_outcome`:
  - `live_status TEXT CHECK (live_status IS NULL OR live_status IN ('none','live','upcoming','unconfirmed'))`
  - `live_checked_at TIMESTAMP`
- **Recorder.** `RecordLiveStatusTx(ctx, tx, runID, videoID, status, at)` runs `UPDATE run_video_outcome SET live_status=?, live_checked_at=? WHERE run_id=? AND video_id=? AND live_status IS NULL`.
  - Zero affected rows with an existing non-`NULL` value is a no-op. The caller then uses the stored value.
  - Zero affected rows because the row is missing is a local error (`ErrRunMembershipMissing`).
  - There are no other writers. `RecordRunVideoOutcomeTx`'s upsert, the claim insert (`ON CONFLICT DO NOTHING`) and every transition must not list these columns. The `028` trigger fires only on `UPDATE OF final_outcome`, so these writes are unaffected.
- **Discovery.** When the by-ID prefetch returned `Live` for a video, `discovery.persist` calls `RecordLiveStatusTx` for the discovering run in the same transaction as `DiscoverVideoTx`, which wrote the row. Otherwise the value stays `NULL`.
  - *Implementation note:* `prefetchMetadata` currently discards the returned IDs (`batch, _, err = client.GetVideosWithReturnedIDs(…)`, `internal/discovery/engine.go:402`). It must keep the returned `VideoIDs` and attach `Live[id]` to each `discoveredVideo`. That includes returned items without a usable detail, which today get no `Metadata`.
  - The legacy `GetVideos` branch and refused or failed batches attach nothing. Those videos stay `NULL` and are read by `liveGatePass`.
- **Clearance.** `LiveClearedTx(ctx, q, runID, videoID) (bool, error)` returns true exactly when `SELECT live_status FROM run_video_outcome WHERE run_id=? AND video_id=?` is `'none'`. It is the only predicate for I1 and I2.

## 5. Gate placement and precedence (package `pipeline`)

### 5.1 `liveGatePass(ctx, runID, videos)`: every main-loop pass, fresh or resumed

- **Where it runs.** In `Execute`, after `processDeferredItems` and after discovery has produced or reloaded the pass's videos. It runs before the work of whatever `startPhase` is: exclusion, duplicate detection, routing or action execution. The resume branches that reload videos (`filterExcludedVideos`, `videosWithMatchedDecisions`) apply it only to videos that remain in the filtered set below.
- **Which videos.** Only the set left after `withoutRetryVideos` and, on production runs, `withoutUnroutableVideos` (`pipeline.go` ≈ 454–470). The gate never runs on the raw reloaded set. Claimed retry members are therefore left to `liveGateClaimed` (§5.3), and final or other-run-held videos never reach the gate. From that set, the gate skips any video whose run row already has a committed `deferred` or terminal disposition.
- **Requirement.** `Live` only. Before [Discovery ARS-001](../../discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) readiness lands, no route-input requirement is added, so ordinary routing behavior and its `NULL` handling are unchanged. When that readiness lands, its readiness step becomes this gate's caller and adds its own route inputs.
- **Videos whose row is already set** use the stored value, with no read. All others are read in chunks of 50, and the result is recorded with `RecordLiveStatusTx` in one short transaction per chunk before any disposition.
- A dispatch stop that is already recorded or restored (ARS-005 §6.1) is checked before reading. Unread videos then take AC3 retention.

### 5.2 Precedence for one video (native pass)

| `Statuses[id]` | Live (recorded) | Disposition |
|---|---|---|
| (row already set) | stored value | As in the `Live` rows below |
| `absent` | — | Existing absent handling. For a new discovery this means native retention with run reason `metadata_unavailable` and queue reason `discovery_processing_pending`. BR-037's `unavailable_on_youtube` applies only to `requeued_by_user` receipts (AC8). |
| `quota_refused` / `authorization_pending` / `rate_limited` | — | Native retention with run and queue reason `quota_exhausted` / `authorization_pending` / `api_rate_limited` |
| `failed`; `unusable` with no `Live` entry | — | Native retention with `metadata_unavailable` / `discovery_processing_pending` |
| any returned status | `live`, `upcoming`, `unconfirmed` | `liveSkip` if the video has no recorded decision in this run; otherwise `liveDeferAfterEvaluation` |
| any returned status | `none` | Continue the pass |

**Native retention** is `retainNativeTx(runID, video, runReason, queueReason)`, one `WithRetryTx`:

1. Run `ClassifyVideoTx`; a final video is skipped.
2. Transition the video to `deferred` with `runReason`, using the allowed edge from its current state.
3. Reuse the active video-scoped item, if one exists, without changing it. Otherwise create one (`pending`, `retry_after NULL`, `deferral_reason = queueReason`, origin run = `runID`).

It never writes "no outcome". A video evaluated in this run whose add was blocked by quota, auth or rate limiting at the resume read uses the existing post-evaluation guarded deferral instead.

### 5.3 `liveGateClaimed`: within the ARS-005 §6.2 precondition (prerequisite P1)

- **Window check first (AC7).** Before any read, for each claimed `open` or `partly_done` receipt whose receiving-run row has no `live_status`: if the receiving run is not a user queue retry run and either `LiveWindowClosedTx(video, now, X)` (§8.3.3) is true or X is unreadable (§8.3.1) while the video has a held row, make no read and run `releaseClosedWindowTx` (§8.3.5): the receipt is released unchanged, the video row gets back its deferred state, the receiving run records no outcome for it, and the hold and window are untouched. This covers a run that claimed the item while its window was open and reaches the gate after it closed, such as a resume days later or a limit lowered mid-run. A receipt whose row already has `live_status` uses it, as below; no second read is made.
- For each other claimed `open` or `partly_done` receipt whose receiving-run row has no `live_status`, add `Live` to the receipt's requirement. Partly-done, capacity-only and continuation receipts request `Live` only.
- Record the result with `RecordLiveStatusTx` for the receiving run.
- Then apply the §6.2 status table, with one row inserted ahead of the `current`/`fetched` and `unusable` rows: a recorded `live ≠ none` goes to `liveSkip` with the receipt. `absent`, `quota_refused`, `authorization_pending`, `rate_limited`, `failed`, and `unusable` without `Live` keep their existing §6.2 rows unchanged.
- Resumed receipts use the stored value.

## 6. Live deferral writers

### 6.1 `liveSkip(ctx, runID, video, status, receipt *RetryGroupReceipt)`: before evaluation, one `WithRetryTx`

1. `ValidateRetryReceiptTx` when a receipt is given. A user-removed group writes nothing.
2. `ClassifyVideoTx`. A final video is a no-op.
3. Transition `not_yet_evaluated → evaluating → deferred`, with `FinalOutcomeDeferred` and the reason from §1. A same-owner resumed receipt uses its current state on an allowed edge.
4. Set `live_hold = :hold` and `retry_after = NULL` on every owned (or, without a receipt, active) **video-scoped** row. Keep `id`, `deferral_reason`, `run_id`, `created_at`, `retry_count` and the owner history. In the same statement set the window start (AC7, §8.3.2):
   `live_window_started_at = CASE WHEN live_hold IS NULL OR :fresh THEN :start ELSE live_window_started_at END`, where:
   - `:start` is `COALESCE(<this run row's live_checked_at>, <this transaction's time>)`. A fence or backstop refusal (§7) can reach this writer with no recorded check (`live_status` and `live_checked_at` are `NULL`, mapped to `unconfirmed`). The hold is still written, so the window start must never be `NULL` there; the table CHECK (§8.2) refuses a hold without a start.
   - `:fresh` is true exactly when `runID` is a user queue retry run **and** this run row has a recorded check with `live_status IN ('live','upcoming','unconfirmed')`. A refusal with no recorded check in a retry run is not a check, so it never restarts the window.

   SQLite evaluates the `CASE` against the row's values before the update, so `live_hold IS NULL` means "this skip starts the current hold".
5. If there is no such row, create exactly one with `deferral_reason = 'live_or_upcoming'`, `live_hold = :hold`, `live_window_started_at = :start`, `status = 'pending'`, `retry_after = NULL` and `run_id = runID`. Create it even when the receipt owns only capacity rows. The uniqueness guard is `NOT EXISTS (… video_id = ? AND status IN ('pending','retrying') AND <video-scoped>)`, so capacity rows never suppress it. `<video-scoped>` means not a capacity row, as capacity ARD-001 defines one; before capacity ships, every row is video-scoped.
6. With a receipt, run `AcknowledgeRetryDeferralReceiptTx`. Owned video-scoped Deferred rows return to `pending`. Owned Error rows become `resolved` with their history kept (BR-035 AC11). Owned capacity rows are released to `pending` unchanged: same ID, enrollment, age and cap count.

It never writes a route decision, condition audit, duplicate evidence, action success, guard, Errors entry, `failed` or a final outcome. It never reads the live check limit: whether the window is open matters only to claiming (§8.3.3) and to the claimed gate (§5.3), never to the skip itself.

### 6.2 `liveDeferAfterEvaluation(ctx, runID, video, status)`: a decision is recorded but the add is not sent

This reuses the executor's guarded post-evaluation deferral machinery: `retainGuardedDeferralTx` and the context handoff (`executor.go` ≈ 119, 511–561, 843). It does **not** reuse the quota branch's `deferActions`.

- **A separate handoff disposition.** Today the handoff is `func(*sql.Tx, queue.DeferredReason) error`, which carries one reason that serves as both the queue reason and the run reason. Add a live variant that takes `LiveDeferral{QueueReason: "live_or_upcoming", Hold: live_now|upcoming|unconfirmed, RunReason: live_now|live_upcoming|live_unconfirmed}`. The handoff writes:
  - the queue sink, using `QueueReason` and `Hold` under the video-scoped reuse-or-create rule of §6.1 steps 4–6, including the window-start rule of step 4;
  - the run-video `deferred` outcome, using `RunReason`.

  Neither value is derived from the other. The existing quota, auth and rate handoffs keep the single-reason signature.
- **One transaction.** `liveDeferAfterEvaluation` runs one `WithRetryTx` that:
  - validates the receipt where there is one;
  - runs the live handoff;
  - records every remaining action as `not_attempted` with action outcome reason `live_or_upcoming` and no `executed_at`;
  - acknowledges the receipt (`AcknowledgeRetryDeferralReceiptTx`).

  The recorded decision is kept. No guard is prepared. If one was prepared, it is the invocation's own positively unissued guard, and it is cleared in the same transaction, as the existing guarded deferral does. Any other guard stays.
- **Return value.** After that transaction commits, the action path returns `(state.FinalOutcomeDeferred, nil)`. It never enters `deferActions`, which would write `deferred / quota_exhausted` action rows and the quota queue reason. It never returns an error that the caller could turn into `guardedRetryHandoffError`, `videoActionFailure` or `FinalOutcomeFailed`. If the transaction fails, the existing guarded-deferral error contract applies: retain-before-fail recovery, with the receipt recoverable. No live outcome is partially committed.

### 6.3 Clearing the hold

When the receiving run's gate records `none` for a claimed receipt, set `live_hold = NULL` and `live_window_started_at = NULL` on its owned video-scoped rows. Do this in the transaction that moves the video to `evaluating`, or that starts `continueOriginalDecision`. If that transaction rolls back, the hold and the window remain. No other writer clears either: Stop/Discard handoff, quota, auth or rate deferral, the closed-window release (§8.3.5), Dismiss and final close all leave `live_hold` and `live_window_started_at` unchanged. A later live skip of the same video therefore starts a new hold and a new window (AC7: "since it was last confirmed not live").

## 7. Backstops and fences

- **Evaluation backstops.** On production runs only, `videoContextForExclusion`, `detectVideos` and `routeVideos` check `LiveClearedTx(runID, v)`. On failure they return `ErrLiveNotCleared` for that video and do nothing for it. The caller maps that to `liveSkip(…, unconfirmed, receipt)`.
- **Send fence.** `ClaimPlaylistSend` checks `LiveClearedTx(sendingRunID, video)` at the authority step, before exact-target success, and again at final POST admission (under the capacity lifecycle gate where that applies). On failure it returns `state.ErrLiveNotCleared` before any guard write. The executor checks for this error **before** `isRetryAdmissionRefusal` and before the generic `ActionOutcomeFailed` branch (`executor.go` ≈ 749–757), and maps it to `liveDeferAfterEvaluation(…, unconfirmed)`, which commits inside the guarded path and returns `(Deferred, nil)` (§6.2). It is never authority failure, `retainAuthorityFailure`, `ActionOutcomeFailed`, `FinalOutcomeFailed` or an Errors entry.
- **Capacity.** The planner calls `liveGateClaimed` (Live only) before the first count GET for a claimed capacity row's video. If the result is not `none`, there is no GET and no POST, and the video goes through `liveSkip` with the receipt.
- Every refusal logs `diag:v1 phase=routing operation=check_live_status code=live_status_not_current`.

## 8. Queue, inspection, statistics, registrations and migration

### 8.1 Reads and registrations

- **`GET /api/deferred`.**
  - Each item gains `live_status` (`live_now`, `upcoming`, `unconfirmed` or `null`, from `live_hold`) and, on a held row, `live_check` (AC7, §8.3.6).
  - `reason=live_or_upcoming` selects `status IN ('pending','retrying') AND live_hold IS NOT NULL`, which is active rows only. It returns held rows whether or not their checks have stopped. There is no separate "checks stopped" filter; the design needs none.
  - `reason` still returns the stored `deferral_reason`. How a reused item that was held and is now cleared is labelled is a Designer question (ARD-001 open question 3).
  - A stale hold on a completed or discarded row is ignored by every reader.
- **Run inspection and statistics.** The three run reasons are deferred outcomes. They are never rejected, excluded or failed, and never counted in `ActionsExecuted` or `ActionsFailed`. The `not_attempted / live_or_upcoming` action is not an attempt.
- **Re-queue.** BR-037 predicates are unchanged. A live-held item is active.
- **Registrations** ([safe-diagnostic ARS-001](../../run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md) §118–120 and `internal/diag`):
  - the video and run-video reasons `live_now`, `live_upcoming`, `live_unconfirmed`;
  - the action outcome reason `live_or_upcoming`;
  - the queue reasons `live_or_upcoming`, plus `discovery_processing_pending` if Discovery has not already registered it;
  - the closed enum `live_status`;
  - the operation `check_live_status` with the code `live_status_not_current`. That code is valid only with phase `routing` and that operation, and with no status, reason or SQLite fact, following the `video_unavailable` pattern.

  Update `queue.DeferredReason.Valid`, `web/src/types.ts` and `web/src/lib/outcome.ts` together with these.
- **Simulations.** Dry run, Replay and Backtest read the local status as the `live_status` of the video's most recent **production** run row with a non-`NULL` value, ordered by run start, then run ID.
  - `live`, `upcoming` or `unconfirmed`: shown as skipped for live status, labelled as current/local.
  - `none`: evaluated, labelled as local not-live.
  - No row: evaluated, labelled as live status unknown.

  Simulations make no provider call and change nothing in `run_video_outcome.live_status`, the queue, production outcomes, guards or successes.

### 8.2 Migration (number from the central allocation)

One forward migration, owned by Plan node `b42-migration`.

- **Number.** `origin/main` ends at `031` (`internal/store/migrations/031_retry_claim_provenance.up.sql`). The migration takes the next free number on `main` (max + 1) when PR-L rebases for merge, under the central allocation (Plan phase `migrations`): contiguous, no reservation or placeholder, no edit to `001`–`031`, and no renumbering once a deployed or shared database has the number. The expected order puts it after the last-retry-run migration (PR-E), the run dispatch-stop migration only if PR-R needs one, and the Discovery page/handoff migration (PR-D), and before capacity (PR-C). The file name and version are therefore decided at merge, not here.
- **Combining with capacity.** If capacity's `deferred_item` rebuild is in the same release, the two are combined into one rebuild, or the later rebuild carries every column, CHECK value and index defined here.
- **`run_video_outcome`.** `ALTER TABLE … ADD COLUMN live_status …`, `ADD COLUMN live_checked_at TIMESTAMP`, and `ADD COLUMN claim_released_reason TEXT CHECK (claim_released_reason IS NULL OR claim_released_reason = 'live_window_closed')` (§8.3.5). This is not a table rebuild, and the `028` final-outcome triggers remain. There is no backfill: rows in flight read as `NULL`, and the resume gate reads them once (§5.1).
- **`deferred_item` rebuild** (like `026`), because the `deferral_reason` CHECK widens to add `live_or_upcoming` (and `discovery_processing_pending` if an earlier migration has not added it). New columns:
  - `live_hold TEXT CHECK (live_hold IS NULL OR live_hold IN ('live_now','upcoming','unconfirmed'))`;
  - `live_window_started_at TIMESTAMP` (AC7, §8.3.2);
  - the table CHECK `((live_hold IS NULL) = (live_window_started_at IS NULL))`, so a hold never exists without a window start, or the reverse.

  The rebuild preserves IDs and the `sqlite_sequence` high-water mark, `closed_reason` (`027`) and any capacity columns, every index (`idx_deferred_item_status`, `deferred_item_retry_owner`, `deferred_item_video_status`) and foreign key, and every existing CHECK value. All existing rows get `NULL` in both new columns, so no existing item is held or has a window. No new index: the window predicate runs per video through `deferred_item_video_status`.
- **`settings` seed.** `INSERT OR IGNORE INTO settings (key, value) VALUES ('live_check_limit_days', '30');` (the `006` key/value table). `OR IGNORE` keeps a value that is already there.
- **Store A** is unchanged.
- **Down migration.** It refuses while any row has a live hold, a window start, a `claim_released_reason` or a `live_or_upcoming` reason. Otherwise it rebuilds `deferred_item` without the new columns and CHECK values, drops the three `run_video_outcome` columns, and deletes the `live_check_limit_days` setting. Older writers stop before the migration.
- **Upgrade evidence** (release check, Plan phase `conventions`): upgrade a copy of a production-shaped database at the then-production schema (`030` or `031`; record which) through every new migration. `PRAGMA integrity_check` is `ok`; reopen; old rows, IDs, the AUTOINCREMENT high-water mark, `closed_reason`, indexes, the `028` triggers, the `029` title-search rows and the `031` receipts are unchanged; the new columns are `NULL`; the setting reads 30.
- **Release.** The parser, recorder, gate, writers, fences, readers, the live check limit (§8.3) and SPA ship together. On claimed paths they ship only together with or after ARS-005 §6.1–6.2.

### 8.3 Live check limit and check window (BR-042 AC7)

**What it does.** The user sets a whole number of days, X (default 30, 1–365). Each live-held item has a window start: the check time of the first live skip of its current hold. While less than X days have passed, runs claim and check it as before. From X days on, no automatic claim picks it up and no automatic check reads it. It stays active, deferred, visible and non-final, and Deferred inspection says checks stopped and when. Retry now still checks it. If it is still not confirmed, that check starts a fresh window. A changed X applies at once to every item, from each item's own start.

**Example.** X = 3. Run 70 on day 0 skips P (upcoming); its row gets `live_hold = upcoming` and `live_window_started_at` = run 70's check time `t0`. Runs on days 1 and 2 claim and check P; each skip finds `live_hold` already set, so the start stays `t0`. On day 3, `now − t0 ≥ 3 × 24 h`: the next run's candidate list and claim recheck exclude P, so P is neither claimed nor read, and that run has no row for P. `GET /api/deferred` shows P with `live_check.checks_stopped = true` and `checks_stopped_at = t0 + 72 h`. On day 12 the user presses Retry now. The retry run claims P regardless of the window, reads it, and P is still upcoming: the skip writes `live_window_started_at` = the retry run's check time, and the response says `live_or_upcoming`.

#### 8.3.1 The setting

- **Storage.** `settings` row `live_check_limit_days`, value in canonical decimal form (`"1"`–`"365"`, no sign, no leading zero), seeded `"30"` by the migration (§8.2). It survives a restart because it is a SQLite row. It is not part of `config_version` or any run snapshot. Saving it creates no configuration version, because it must apply to every item at the next claim, not to runs created under a given version.
- **Reader.** `queue.LiveCheckLimitDaysTx(ctx, q) (int, error)` reads the row in the caller's transaction. A missing row, or a value that is not canonical decimal in 1–365, returns `ErrLiveCheckLimitUnreadable`. The only writer is the PUT handler below, so this happens only after outside edits to the database.
- **Unreadable value, fail safe, applied the same way everywhere.** Every reader treats every held row as window-closed:
  - automatic claiming excludes held videos and logs `diag:v1 phase=routing operation=check_live_status code=live_check_limit_unreadable` once per claim transaction;
  - the claimed gate (§5.3) releases an unread held receipt with `releaseClosedWindowTx`;
  - Deferred inspection reports `checks_stopped: true` with no `checks_stopped_at` (§8.3.6), so the UI shows "Automatic checks have stopped." without a date, matching what claims actually do.

  Non-held items are claimed as usual, so a run start never fails because of it (BR-035 AC15). Retry now is unaffected. `GET` answers `500 live_check_limit_unreadable`, and saving a valid value repairs it. Rejected alternative: falling back to 30, which would silently change the user's setting.
- **No YouTube call.** Reading or saving the setting never calls the provider and changes no `deferred_item` or run row (AC7, *Changing the setting*).

#### 8.3.2 The window start

`deferred_item.live_window_started_at` is the window start of the held row. Writers:

| Event | Effect on `live_window_started_at` |
|---|---|
| Live skip, row not held (`live_hold IS NULL`) or new row (§6.1 steps 4–5, §6.2) | Set to `:start` = `COALESCE(live_checked_at, transaction time)`: the first skip of the current hold. The transaction time applies only to a fence or backstop refusal with no recorded check. |
| Live skip, row already held, no recorded non-`none` check by a user queue retry run | Unchanged. This includes every fence or backstop refusal of an already-held row. |
| Live skip in a user queue retry run whose row records `live`, `upcoming` or `unconfirmed` (Retry now checked it and it is still not confirmed) | Set to that `live_checked_at`: a fresh window. |
| Confirmation `none` (§6.3) | `NULL`, together with `live_hold`. |
| Claim, release, closed-window release (§8.3.5), Stop/Discard handoff, quota, auth or rate deferral (AC3), startup recovery, resume, restart, Dismiss, final close, settings change | Unchanged. No such writer lists the column. |

- **Never a hold without a start.** Every writer that sets `live_hold` also sets the start in the same statement, with `COALESCE`, so the table CHECK `(live_hold IS NULL) = (live_window_started_at IS NULL)` holds on every path, including refusals with no recorded check.
- **Why the check time.** The first skip's run row already holds the check time (`live_checked_at`, written once, §4). Using it makes the start the moment YouTube last said "live or upcoming", and a crash between the read and the skip cannot move it: on resume the stored `live_checked_at` is reused (§5.1).
- **One start per video.** Every writer sets the same value on all of the video's held rows in one statement, so the video has one window start. Readers still use `MIN(live_window_started_at)` over the video's held rows, which can only make the window end earlier, never later.
- **Format.** Written as UTC RFC 3339 text, the format `retry_after` already uses with `julianday()` (`internal/queue/retry.go:466`).
- **Errors-queue retry.** It also creates a user queue retry run, so a still-held result there also starts a fresh window. In practice this does not arise: a live skip resolves the video's owned Errors rows (§6.1 step 6, BR-042 AC5), and a later Errors entry for the video comes only from paths that complete its Deferred rows, so a held video has no active Errors entry to retry.
- **Retry now and "fresh".** A recorded non-`none` check in a user queue retry run is the "check again" of AC7's last bullet, so it restarts the window. If the retry run records no check (quota, authorization, rate limit, a failed read, a Stop before the read, or a fence or backstop refusal with no recorded check), the window is unchanged. A stopped item stays stopped and the response says `not_checked` (§8.3.7). That no-check case is a specification choice awaiting the requirements owner's confirmation (ARD-001 open question 5).

#### 8.3.3 Claim exclusion (the claim filter)

One predicate, `LiveWindowClosed(video, now, X)`, defined as a shared SQL fragment and as `queue.LiveWindowClosedTx(ctx, q, videoID, now, days) (bool, error)`:

```sql
EXISTS (SELECT 1 FROM deferred_item h
        WHERE h.video_id = :video
          AND h.status IN ('pending','retrying')
          AND h.live_hold IS NOT NULL
          AND <video-scoped h>
          AND julianday(:now) - julianday(h.live_window_started_at) >= :days)
```

`<video-scoped h>` is the capacity ARD-001 test; before capacity ships every row is video-scoped. Elapsed time is exact: X days means X × 24 hours between two UTC instants, not calendar days. `:days` is the X read in the same transaction.

It applies to **automatic** claims only, in two places that must agree:

1. **Candidate list.** `ListDeferredRetryCandidatesForRun` (`internal/queue/retry.go:805`) gains `AND NOT <LiveWindowClosed(d.video_id)>` in its `WHERE`. The caller reads X first. The list is not authority (its own comment), so a stale X only changes which items are offered.
2. **Writer recheck (authority).** In `claimRetryGroupTx` (`internal/queue/retry.go:381`), the existing eligibility check for an unowned deferred claim (`retry.go:463–470`) gains the same clause, with X read in the claim transaction. A closed window returns `ClaimLost` and writes nothing, like any ineligible item.

The exclusion is per **video**: when any held row of the video is window-closed, no row of that video is claimed automatically. That includes capacity rows, because a claim takes every outstanding sibling (`claimRowRefs`), and a claimed capacity row would trigger a live check (§7 *Capacity*). Their adds cannot be sent anyway while the video is unconfirmed (AC13), and they keep their identity and age.

**Claim kinds.** `claimRetryGroupTx`'s `manual bool` today means "Errors-queue claim". It becomes a three-value kind:

| Kind | Caller | Window clause |
|---|---|---|
| `claimAutomatic` | `createClaimedRunLocked`; the ARS-005 §5.2 batch loop once built | Applied |
| `claimUserDeferred` | `RetryQueueEntry(…, errorQueue=false)` (Deferred Retry now) | Not applied. The existing `retry_after` eligibility rule is unchanged |
| `claimUserErrors` | `RetryQueueEntry(…, errorQueue=true)` | Not applied (unchanged: Errors claims check no deferred eligibility) |

Only `claimAutomatic` reads X. The kind is not stored; the run's `initiated_by` records the user retry durably for §8.3.2.

**Stop and recovery.** Stop/Discard handoff and the run-failure sweep put claimed rows back to `pending` without touching the hold or window (§6.3). For a held video they also restore its deferred state (§8.3.5). An item whose window has closed therefore stays excluded and deferred (AC7: a handoff does not make it eligible). Startup recovery and resume reuse their own receipts; they make no new claim, and §5.3 refuses a check after the window has closed.

#### 8.3.4 Fit with BR-035 AC14

BR-035 AC14 says a run keeps claiming eligible items while it has quota, never passes over an eligible item in favor of one that became eligible later, and (note of 2026-11-19) a window-closed live item is neither eligible nor passed over.

- **Eligible set.** AC14's eligible set is now ARS-005 §5.1's set minus window-closed videos. Every AC14 statement is made over this set. "Eligible items remain" (§5.2 step 3) is false when only window-closed items are left, so the claim loop ends normally.
- **Order.** Unchanged: oldest outstanding row per video by `created_at`, then `id`. A window-closed item keeps its `created_at`.
- **Re-entry.** An item comes back into the set when X is raised above its window age, or after Retry now restarts its window. It comes back in its old place, ahead of items created after it. This is the rule ARS-005 §5.1 already applies to a claimed and released item ("keeps its `created_at`, and so its place"). It is consistent with AC14: the item first became eligible when it was created, before every item it now precedes. While it was out of the set it was not "passed over", because AC7 says so. AC14's guarantee that every eligible item is eventually claimed still holds, because the order is a fixed total order over creation.
- **ARS-005 text.** ARS-005 §5.1 states that "creation order is therefore eligibility order" because no row becomes eligible after it is created. That premise no longer holds for window-closed items, so ARS-005 §5.1 needs the amendment listed in §10 (owner: run-initiation). The claiming algorithm does not change.
- **Quota.** A window-closed item costs no quota and takes no claim slot, so it cannot delay newer items.
- **Dependency.** The pass-over property only becomes observable once claiming is bounded by quota, which is the ARS-005 §5.2 batch loop ("claim while quota remains"). That loop is **not built**: `createClaimedRunLocked` (`internal/pipeline/pipeline.go:252`) claims every eligible candidate at run creation. Until §5.2 ships, the exclusion and order still hold, but no run passes anything over. Test W11 therefore depends on ARS-005 §5.2 and runs once it lands. Before then, W4 and W7 cover exclusion and re-entry.

#### 8.3.5 Releasing a claimed held video without an outcome

A claim resets the video row (`internal/queue/retry.go`, the `resetSQL` after the claim): `processing_state = 'not_yet_evaluated'` (or `'evaluated'` for a partly done video) and `final_outcome = 'not_yet_evaluated'`, with `deferred_reason = NULL`. Any path that returns the claimed rows of a **held** video to its queue without committing an outcome in the receiving run must undo that reset. Otherwise the video reads `not_yet_evaluated`, which the Videos page latest-outcome join (`internal/videosearch/service.go:719`) shows, and AC7 ("the video SHALL stay deferred") fails. A video is held here when any of its claimed Deferred rows has `live_hold IS NOT NULL`.

**`state.RestorePriorDeferralTx(ctx, tx, receivingRunID, videoID)`** writes only the `video` row, never a run row:

- **Precondition:** `video.final_outcome = 'not_yet_evaluated'` (the claim reset is still in place) and the receiving run has no outcome for the video. Otherwise it is a no-op.
- **Effect:** `processing_state = 'deferred'`, `final_outcome = 'deferred'`, `error_message = NULL`, and `deferred_reason` = the `deferred_reason` of the video's most recent `run_video_outcome` row from another production run with `final_outcome = 'deferred'`, ordered by run start, then run ID. `processing_state` is always set to `'deferred'` explicitly, including for a partly done video, because the last committed state of a held video is a live deferral. BR-039 partly-done classification comes from recorded successes, not from `processing_state`, so it is unchanged (W6).
- **No prior deferred row:** an integrity fault. A held row exists only after a live deferral, which always writes such a run row. Roll back and use the caller's existing integrity handling: `retryAdmissionRefusal` for the gate, and a failed Stop settlement for the handoff, as ARS-005 §5.1.1 already requires for a handoff that cannot be proved.

It is called in the same transaction by every such path:

| Path | Where today | Change |
|---|---|---|
| Closed-window release at the claimed gate | `releaseClosedWindowTx` (below) | New |
| Stop/Discard handoff | `queue.HandOffUnfinishedExceptTx` (`internal/queue/handoff.go`); [ARS-003](../../run-initiation/specifications/ars-003-stop-run-and-deferred-handoff.md) §2 | For each held unfinished video whose owned rows it returned to `pending`, call `RestorePriorDeferralTx`. Today it does not touch `video`. |
| Run-failure sweep | `sweepStrandedWorkExcept` → `RequeueRetryGroupTx` (`internal/pipeline/authority.go:365`); [ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §5.1.1 *failure sweep* | Same, for each held `retryOwned` member it requeues |

The 2026-11-19 live writers (`liveSkip`, `liveDeferAfterEvaluation`, native retention) need no restore, because they commit `deferred` themselves. Non-held videos keep today's behavior; this amendment does not change them.

**`releaseClosedWindowTx(ctx, runID, receipt)`: one `WithRetryTx`.** This runs only from §5.3 when a receipt reaches the claimed gate unread, in a run that is not a user queue retry run, after its window has closed (or while X is unreadable).

1. `RequeueRetryReceiptTx(receipt)` (`internal/queue/retry.go:618`): owned Deferred rows go to `pending`, owned Error rows to `active`, `retry_run_id = NULL`, with identity, reason, hold and window unchanged.
2. `RestorePriorDeferralTx(runID, videoID)`.
3. Set `run_video_outcome.claim_released_reason = 'live_window_closed'` on the receiving run's row (§8.2). Leave `final_outcome` and `live_status` `NULL`. The row stays as the same-run claim fence (ARS-005 §5.1).

**A released member is settled.** After step 3 the run has nothing left to do for the video. Every "unfinished member" reader must treat it as settled:

- `unfinishedRunMembersTx` (`internal/pipeline/authority.go:423`) gains `AND NOT EXISTS (SELECT 1 FROM run_video_outcome r WHERE r.run_id = ?1 AND r.video_id = v.id AND r.claim_released_reason IS NOT NULL)`. Without it, the row's stored provenance and missing outcome select the video, so a later Stop/Discard handoff (`listUnfinishedRunVideosTx`) would create a second `stopped_by_user` item next to the held one, or the failure sweep would try to requeue rows the run no longer owns.
- `HandOffUnfinishedExceptTx` therefore gets no such video in `unfinished`. Its stored-receipt loop changes no row for it, because none is still `retrying` for this run, and its final ownership check still passes.
- Startup recovery lists work from owned rows (`ListRetryVideoIDs`), and none remain, so it does not resume the video.
- `claim_released_reason` is written only by step 3 and never cleared. It is not an outcome, and no count or inspection view lists it.

**Why not record an outcome.** Recording a deferred outcome in this run would add the video to the run's outcomes and counts without a check, which AC7 forbids.

**Readers.** Run inspection, statistics and `ActionsExecuted` already exclude a membership row with no outcome, as for ARS-005's `unusable` release *[unverified for every reader; test W10 proves it]*.

#### 8.3.6 Inspection: `live_check` on a Deferred item

Every deferred item view (`deferredItemView`, `internal/queue/handlers.go`) that is a held row gains:

```json
"live_check": {
  "window_started_at": "2026-11-19T09:00:00Z",
  "checks_stopped": true,
  "checks_stopped_at": "2026-12-19T09:00:00Z"
}
```

- `window_started_at`: the video's window start (`MIN` over its held rows).
- `checks_stopped`: `LiveWindowClosed(video, request time, X)`.
- `checks_stopped_at`: `window_started_at + X × 24 h`, present only when `checks_stopped` is true. It is when automatic checks stopped under the current X. After X changes, it is recomputed and is not a stored event.
- If X is unreadable, `checks_stopped` is `true` and `checks_stopped_at` is omitted, because claims and the claimed gate then treat the row as closed (§8.3.1). The design shows "Automatic checks have stopped." with no date.
- `live_check` is omitted on rows that are not held, including a completed or discarded row with a stale hold.
- All values are computed in the list's read-only transaction. They are no new stored state.

The Videos page `queue_state` (`deferred_pending` / `deferred_claimed`, [video-search ARS-001](../../video-search/specifications/ars-001-video-search-api-and-queries.md) §6.6) gains the same optional `live_check` object for a held row. That is an amendment to video-search ARS-001 (§10), for its owner.

#### 8.3.7 Retry now result

`POST /api/deferred/{id}/retry` still runs the retry synchronously and answers `200` with the item view, now including `live_status` and `live_check`. When the retry created a receiving run, the body gains:

```json
"live_retry": { "run_id": 412, "result": "live_or_upcoming" }
```

`result` comes from the receiving run's row for the video, `run_video_outcome(run_id, video_id).live_status`:

| `live_status` | `result` | Meaning for the UI |
|---|---|---|
| `NULL` | `not_checked` | No current report in that run (AC3) or a Stop before the read. Hold and window unchanged. |
| `none` | `not_live` | Confirmed. The video was processed normally; the hold and window are cleared. |
| `live`, `upcoming`, `unconfirmed` | `live_or_upcoming` | Still held, with a fresh window from this check. `live_check.checks_stopped` is false. |

- The handler learns `run_id` from the coordinator: `queue.RetryCoordinator.RetryQueueEntry` returns the receiving run ID with its error (0 when no run was created). It does not use `retry_run_id`, because a later automatic run could claim the item before the response is read.
- `live_retry` is omitted when no run was created: refusals such as the final-video refusal, `409 retry_work_removed` and `503`. Existing statuses and codes are unchanged.
- The UI branches on `result` and the item's `status`, never on message text.

#### 8.3.8 Settings API

`GET /api/settings/live-check-limit` and `PUT /api/settings/live-check-limit`, registered on the config handler, which `server.go:593–595` already mounts for `/api/settings/`. They are separate from `PUT /api/settings`, so this card saves on its own (design: a refused value never blocks another card). `PUT /api/settings` keeps ignoring an unknown `live_check_limit_days` field.

| Request | Status | Body |
|---|---|---|
| `GET` | `200` | `{"days": 30}` |
| `GET`, stored value unreadable | `500` | `{"error":"live check limit unreadable","code":"live_check_limit_unreadable"}` |
| `PUT {"days": n}`, valid | `200` | `{"days": n}`, read back from the same transaction that wrote it |
| `PUT`, invalid value or body | `400` | `{"error":"invalid live check limit","code":"invalid_live_check_limit","saved_days": m}`. `saved_days` is the unchanged saved value, or `null` if that is unreadable |
| `PUT`, storage failure | `500` | `{"error":"failed to save live check limit","code":"live_check_limit_save_failed"}`. The UI must treat the saved value as unknown |

Every response sets `Cache-Control: no-store`.

**Validation, server-side and authoritative.** The body is at most 1 KiB and is a JSON object with exactly one member, `days`, with no duplicate or unknown members. The raw JSON token of `days` must match `^(0|[1-9][0-9]*)$`. That refuses strings (`"30"`), fractions (`2.5`, `3.0`), exponents (`1e2`), signs (`-4`), `null`, booleans and arrays. Its value must be 1–365, so `0` and `366` are refused. A refused PUT writes nothing. A valid PUT is one `INSERT … ON CONFLICT(key) DO UPDATE` on `settings`, storing the canonical decimal. `007` can only arrive as the number 7: the UI trims it and sends a number, because JSON forbids leading zeros.

The "refusal versus failure" distinction the design needs is the pair `400 invalid_live_check_limit` versus `500 live_check_limit_save_failed`.

#### 8.3.9 AC7 mapping

| AC7 clause / *Check window* example step | Mechanism | Test |
|---|---|---|
| Setting: whole days, default 30, 1–365, refused with a visible reason, saved value unchanged | §8.3.1 seed and reader; §8.3.8 validation and codes | W1, W2 |
| Saved value survives a restart | `settings` row | W2 |
| Window starts at the first live skip of the current hold | §8.3.2 `CASE` on `live_hold IS NULL` with a `COALESCE` start; cleared only on confirmation | W3, W8, L14 |
| A further skip, claim, Stop/Discard handoff, restart, startup recovery do not move the start | No writer except §8.3.2's lists the column | W3, W5, W6 |
| Open window: checked automatically under AC5 | Unchanged claim path while the predicate is false | W3 |
| After X days: no automatic claim or check; not counted as passed over | §8.3.3 candidate and writer clause; §5.3 gate refusal; §8.3.4 | W4, W6, W11 |
| Stays active, deferred, non-final, with identity, history and reason; in no later run's outcomes or counts | Nothing writes the item; §8.3.5 restores the video's deferred state on every outcome-less release (gate, Stop/Discard, failure sweep) | W4, W5, W6, W10 |
| Inspection shows checks stopped and when | §8.3.6 `live_check` | W4, W7 |
| Handoff does not make it eligible | Handoff and the failure sweep leave the hold and window alone and restore the deferred state; per-video predicate; a released member is settled | W5, W6 |
| Changing X applies to every item from its own start; raise resumes, lower stops at the next run; no start, reason, history or outcome change; no YouTube call | X read per claim transaction; the predicate uses each row's start; §8.3.1 | W7 |
| Retry now checks whether or not the window has ended; fresh window if still held | `claimUserDeferred` skips the clause; §8.3.2 fresh rule (only a recorded non-`none` check); §8.3.7 result | W9, L14 |
| Re-queue does not apply; Dismiss removes and it stays removed | BR-037 predicates unchanged (a held item is active); Dismiss discards the row, and nothing recreates it | W9, L8 |
| Example: X = 3, days 0–2 checked, day 3 not, restart, raise to 10, lower to 2, Retry now on day 12, Dismiss, refusals 0, 366, 2.5 and text | W12 runs the whole example in order | W12 |

## 9. Tests (real server path, migrated SQLite, request-counting controlled provider)

- **L1. Skip, restart, confirm.** Run 60 records `live` for L and `upcoming` for P from the prefetch, with zero gate reads. It skips both, leaving one item each with a hold and no decision or send. After a restart, run 61 reads each once. L is `none`: it is routed and added under run 61's snapshot, and its hold is cleared. P is skipped again on the same item ID, with `created_at` and the origin unchanged.
- **L2. Outcome rows.** The run reasons are `live_now` and `live_upcoming`. There is no `route_decision`, audit, duplicate row, action, guard or final outcome.
- **L3. Ambiguity.** Every `unconfirmed` row of §2, plus `P0D` or a missing duration with no `liveBroadcastContent`, gives `live_unconfirmed` and nothing is evaluated.
- **L4. No report, claimed work.** Quota, auth, rate, transport and invalid-envelope failures keep the existing §6.2 rows. No live reason, final outcome or `failed` comes from live status.
- **L5. Exclusions and duplicates.** A live video that an exclusion matches and that has a duplicate title is deferred, with no exclusion and no duplicate evidence. Once confirmed, the normal behavior applies.
- **L6. Another run's status.** Run 59's row says `none` while the provider reports `upcoming`. Run 60 reads the video and skips it. A Store A `live`-free refresh changes nothing.
- **L7. One item, once per run.** The video is not re-claimed in the same run, and there is no second active video-scoped row. A claimed Errors entry becomes `resolved`, and the sink is created. `RecordLiveStatusTx` called twice leaves the first value.
- **L8. Long wait.** With X = 30 and one run a day on days 0–29, P is read once per run, keeps one item with an unchanged window start, and is never final. Dismiss stops further reads, and re-queue does not restore it.
- **L9. Absent.** A complete response omits the video. A `requeued_by_user` receipt gets `unavailable_on_youtube`; another claimed receipt gets ordinary retention. A new discovery gets native retention with `metadata_unavailable` and a pending item. An incomplete response releases or retains as in §5.
- **L10. BR-039.** A final video now reported live: zero reads and only the existing close. A partly-done video reported `upcoming`: a live-only read, no guard or POST, and its sources unchanged. After confirmation only the missing add is sent, under the original decision.
- **L11. Capacity.** A live video that matches a full playlist: no count GET, no waiting row, and the cap count is unchanged. A capacity-only receipt for a video that is not confirmed: no GET or POST, the capacity row keeps its ID and age, and a video-scoped live item exists. Evict that capacity row: the video still has an active item.
- **L12. Stop and re-queue.** Stop with a live-held claim: the item is `pending` and keeps its hold. The BR-037 preview does not count the video.
- **L13. Registration.** API and SPA show the reasons and `live_status`. The filter returns active held rows only, and a completed row with a stale hold is excluded. Statistics count the video as deferred. An unknown value is withheld.
- **L14. Fences.** Call `routeVideos` and `ClaimPlaylistSend` directly with no row value, with `live`, and with another run's `none`. Each ends deferred with `live_unconfirmed` and an active item, with no `ActionOutcomeFailed`, Errors entry or failed run, plus the fixed diagnostic. AC7 window (§6.1 step 4): with no row value, the hold is written with `live_window_started_at` = the refusal transaction's time, never `NULL`, and the table CHECK holds; on an already-held row the start is unchanged. Repeat both refusals inside a user queue retry run whose row has no recorded check: the start is unchanged (no fresh window), and `live_retry.result` is `not_checked`.
- **L15. Simulations.** Dry run, Replay and Backtest over the latest production values `live`, `upcoming`, `unconfirmed`, `none` and none at all give the §8.1 labels. They make zero requests and write nothing.
- **L16. Resume after expiry.** A Run Now run stops at its time limit after routing, then expire every Store A row. Resume it at the action phase: zero live reads, adds are sent, no `failed`. Repeat with a migration-era run whose rows are `NULL` and whose provider now reports `upcoming` for one routed video: one live-only read, then `liveDeferAfterEvaluation`, no send, and the decision kept. Repeat at the duplicate-detection and routing phases.
- **L17. New discovery without a report.** The prefetch and the gate read are quota-refused, failed or returned an invalid envelope. The video ends `deferred` with an existing reason or `metadata_unavailable`, with exactly one pending item, and a later run claims it. It is never `not_yet_evaluated` without an item.
- **Upgrade.** As §8.2 *Upgrade evidence*: from the then-production schema (`030` or `031`) through every new migration, with IDs, the AUTOINCREMENT high-water mark, `closed_reason`, indexes, the `028` triggers, the `029` search rows and the `031` receipts unchanged, the new columns `NULL`, the table CHECK holding, and the setting reading 30. A clean install gives the same schema.

**AC7 tests (W1–W12).** Real server path, migrated SQLite, request-counting controlled provider, and an injected clock. Day *n* means the day-0 run's check time plus *n* × 24 h.

- **W1. Setting validation.** On a fresh install `GET` gives 30. `PUT` with `1`, `365` and `7` saves each and reads it back. `0`, `366`, `2.5`, `3.0`, `-4`, `1e2`, `"30"`, `"abc"`, `null`, `{}`, `{"days":5,"x":1}`, a duplicate `days` member and a non-JSON body each give `400 invalid_live_check_limit` with the unchanged `saved_days`, and the row is unchanged. A forced write failure gives `500 live_check_limit_save_failed`. No request reaches the provider, no configuration version is created, and no `deferred_item` row changes.
- **W2. Restart.** Save 12, restart the binary, `GET` gives 12. Corrupt the row by hand (`"abc"`), then: `GET` gives `500 live_check_limit_unreadable`; a run start succeeds; non-held items are claimed; held items are not, and the diagnostic is logged once per claim transaction; Retry now on a held item still checks it; `GET /api/deferred` shows every held row with `checks_stopped: true` and no `checks_stopped_at`; a run holding an unread claim of a held video releases it at the claimed gate with zero reads (§5.3, §8.3.5); a valid `PUT` repairs it.
- **W3. Start fixed by the first skip.** X = 3. Day 0: P is skipped, `live_window_started_at` = that run's `live_checked_at`. Days 1 and 2: each run claims and reads P once, and the start is unchanged. A quota-refused claim on day 2 (no report) also leaves it unchanged.
- **W4. Window closed.** Day 3: the run neither claims nor reads P, has no row for P, and its counts and outcomes omit it. P stays `pending` with the same ID, reason, `created_at` and hold. The video reads deferred with no final outcome. `GET /api/deferred` shows `checks_stopped: true` and `checks_stopped_at` = day 3. The re-queue preview does not count P.
- **W5. Handoff, sweep and restart.** On day 2, a run claims P and is stopped before its read: P is `pending` with its hold and start unchanged, no second item exists, and the video row reads `processing_state = 'deferred'`, `final_outcome = 'deferred'` and the prior run's live `deferred_reason`, exactly as before the claim (Videos page latest outcome unchanged). Restart the process; on day 3 a new run does not claim P. Repeat with Discard, and with a run that fails after claiming P (the run-failure sweep): the same video state and one item. Contrast a non-held claimed video under Stop: its behavior is unchanged from today.
- **W6. Resume across the boundary.** A run claims P on day 2.9 and is interrupted before the read. It is resumed (Run Now resume, and separately startup recovery) on day 3.1: zero reads for P. `releaseClosedWindowTx` returns P to `pending` with its hold and window. The video row reads `processing_state = 'deferred'`, `final_outcome = 'deferred'` and the prior live `deferred_reason`, as before the claim; `ClassifyVideoTx` and the Videos page latest outcome are unchanged. The receiving row has `claim_released_reason = 'live_window_closed'` and no outcome. The resumed run's outcomes and counts omit P. Then Stop the resumed run, and separately let it fail: `unfinishedRunMembersTx` does not select P, the handoff creates no `stopped_by_user` item and the sweep requeues nothing, and Stop settlement succeeds. Repeat with a partly done P: its recorded successes and BR-039 classification are unchanged, and `processing_state` reads `'deferred'`.
- **W7. Changing X.** With P closed on day 4: raise X to 10, then the next run claims and reads P, and the start is unchanged. Lower X to 2: the next run does not claim P, and `checks_stopped_at` reads day 2. Saving X calls no provider and changes no row's start, reason, history or outcome. Lower X while a run holds an unread claim of P: that run's gate releases P (§5.3).
- **W8. A new hold after confirmation.** P reports `none` on day 5 under X = 10: hold and window are `NULL`. On day 6 a later skip (the video became live again) starts a new window at day 6.
- **W9. Retry now.** On a closed P: Retry now claims P whatever the window, reads it once, and on still-upcoming answers `200` with `live_retry.result = live_or_upcoming`, `live_check.checks_stopped = false` and `window_started_at` = the retry run's check time. With the provider reporting `none`: `result = not_live`, and P is processed normally. With quota refused: `result = not_checked`, and the start and the stopped state are unchanged. With a fence or backstop refusal in the retry run and no recorded check: the same `not_checked`, an unchanged start, and no `NULL` start. Dismiss P: no later run claims it, Retry now gives the existing not-found or removed answer, and re-queue does not restore it.
- **W10. Readers.** After W4 and W6, run inspection, Run List counts, statistics, `ActionsExecuted` and `ActionsFailed` contain no entry for P in any run after the window closed, including the resumed membership row with a `NULL` outcome.
- **W11. AC14 order** (depends on ARS-005 §5.2, the quota-bounded claim loop, which is not built; run it when §5.2 lands, §8.3.4). Seed items A (oldest, held, closed), B and C (newer, ordinary), with quota for one claim: the run claims B, not A, and A is not "passed over". Raise X so A reopens: the next run with quota for one claims A before C.
- **W12. The BR-042 *Check window* example end to end**, in order on one database, through the HTTP API and the SPA: X = 3; days 0, 1, 2 checked; day 3 not; Stop and restart; raise to 10; lower to 2; Retry now on day 12 checks P and, still upcoming, starts a new window under the current X, here 2 days (BR-042 example corrected 2026-11-22); Dismiss; refusals of 0, 366, 2.5 and text; the saved X survives a restart.

## 10. Amendments to other contracts (applied 2026-11-19)

Each target section now states its amendment and links back here. The capacity ARD-001 and Discovery ARS-001 amendments are in those documents' working-tree copies. Those documents are not yet committed, so their owners commit them.

- [ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md):
  - §6.1: the `Live` requirement, `liveStreamingDetails`, and `Live` in `VideoIDs`, `FetchResult` and `FetchOutcome`.
  - §6.2: `liveGateClaimed`, the live row in the status table, and clearing the hold on confirmation.
- [ARS-006](../../run-initiation/specifications/ars-006-final-outcomes-are-never-reprocessed.md) §7.2: the live-only gate before `continueOriginalDecision`.
- [Authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md): `LiveClearedTx` in the guard authority step and at POST admission; `ErrLiveNotCleared` and its executor mapping.
- [Capacity ARD-001](../../playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md):
  - the gate before the count GET;
  - the clearance check at admission;
  - video-scoped uniqueness that does not count capacity rows.
- [Discovery ARS-001](../../discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md):
  - the readiness step adopts `liveGatePass` and `retainNativeTx`;
  - the prefetch records `live_status`.
- [Safe-diagnostic ARS-001](../../run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md) §118–120: the registrations in §8.1.

**AC7 amendments (2026-11-22), not yet applied.** This task may write only `docs/architecture/live-status/**`, so these go to their owners. Until they land, this section and §8.3 are the authority for the AC7 behavior.

- [ARS-003](../../run-initiation/specifications/ars-003-stop-run-and-deferred-handoff.md) §2 (run-initiation): the Stop/Discard handoff calls `RestorePriorDeferralTx` for each held unfinished video it returns to `pending` (`processing_state = 'deferred'` set explicitly), and its unfinished set excludes members with `claim_released_reason` (§8.3.5).
- [ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) (run-initiation):
  - §5.1.1 *failure sweep*: the run-failure sweep does the same restore for each held member it requeues, and `unfinishedRunMembersTx` treats a member with `claim_released_reason` as settled (§8.3.5);
  - §5.1: the candidate list gains the window clause, and the sentence "creation order is therefore eligibility order" gains the window-closed exception and the re-entry rule of §8.3.4;
  - §5.2: the batch loop reads X once per claim transaction, and "eligible items remain" excludes window-closed videos;
  - §6.2 *Live gate*: the window check before the read and `releaseClosedWindowTx` (§5.3, §8.3.5);
  - §7.2: the item wire shape gains `live_status` and `live_check`; `POST /api/deferred/{id}/retry` gains `live_retry`.
- [Video-search ARS-001](../../video-search/specifications/ars-001-video-search-api-and-queries.md) §6.6: `deferred_pending` and `deferred_claimed` gain the optional `live_check` object (§8.3.6).
- [Capacity ARD-001](../../playlist-capacity/design/ard-001-exact-target-capacity-and-waiting-work.md): any capacity claim path applies the per-video window exclusion (§8.3.3), and a capacity rebuild carries `live_window_started_at` and its table CHECK.
- Safe-diagnostic ARS-001: register the code `live_check_limit_unreadable` (phase `routing`, operation `check_live_status`, no other facts) and the API codes `invalid_live_check_limit`, `live_check_limit_unreadable` and `live_check_limit_save_failed`.
