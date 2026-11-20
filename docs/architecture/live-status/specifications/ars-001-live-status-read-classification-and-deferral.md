---
type: specification
title: ARS-001 — Live-Status Read, Classification, Run-Scoped Clearance and Live Deferral
description: Exact parser rules, the FetchForEvaluation Live requirement, run_video_outcome clearance, gate placement on every pass and resume, native retention, live-deferral transactions, fence and backstop mapping, queue and diagnostic registrations, simulation behavior, migration placement and tests for BR-042.
tags: [architecture, specification, live, upcoming, metadata, deferred-queue, migration, diagnostics]
timestamp: 2026-11-19T00:00:00Z
---

**Status:** proposed exact contract for [ARD-001](../design/ard-001-fresh-live-status-gate.md). Revised 2026-11-19 after critic `ses_e0ca5c4cdffeU13eZuktRfYwvH`, which accepted it. Its precision fixes are applied in §4, §5.1 and §6.2. Not implemented. Claimed paths require [ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §6.1–6.2, which is not built (ARD-001 P1).

## Satisfies

[BR-042](../../../requirements/yuhaul/br-042-skip-live-and-upcoming-videos-until-ended.md) AC1–AC16 and the 2026-11-19 live case of [OC-002](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) Guarantee 5. ARD-001 *Satisfies* gives the mapping. The tests are in §9.

## 1. Vocabulary

| Name | Values | Where |
|---|---|---|
| `LiveStatus` | `none`, `live`, `upcoming`, `unconfirmed` | Parser result; `run_video_outcome.live_status` |
| Run deferred reason | `live_now`, `live_upcoming`, `live_unconfirmed` | `run_video_outcome.deferred_reason`, `video` deferred reason |
| Action outcome reason | `live_or_upcoming` | Deferred action tail at the fence (§6) |
| Queue origin reason | `live_or_upcoming` | `deferred_item.deferral_reason`, on items that a live deferral creates |
| Queue live hold | `live_now`, `upcoming`, `unconfirmed` | `deferred_item.live_hold`, on video-scoped rows |
| Native retention reasons | Run reason `metadata_unavailable` with queue reason `discovery_processing_pending`, or the existing `quota_exhausted`, `api_rate_limited`, `authorization_pending` | §5.2, matching [Discovery ARS-001](../../discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) |

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

- For each claimed `open` or `partly_done` receipt whose receiving-run row has no `live_status`, add `Live` to the receipt's requirement. Partly-done, capacity-only and continuation receipts request `Live` only.
- Record the result with `RecordLiveStatusTx` for the receiving run.
- Then apply the §6.2 status table, with one row inserted ahead of the `current`/`fetched` and `unusable` rows: a recorded `live ≠ none` goes to `liveSkip` with the receipt. `absent`, `quota_refused`, `authorization_pending`, `rate_limited`, `failed`, and `unusable` without `Live` keep their existing §6.2 rows unchanged.
- Resumed receipts use the stored value.

## 6. Live deferral writers

### 6.1 `liveSkip(ctx, runID, video, status, receipt *RetryGroupReceipt)`: before evaluation, one `WithRetryTx`

1. `ValidateRetryReceiptTx` when a receipt is given. A user-removed group writes nothing.
2. `ClassifyVideoTx`. A final video is a no-op.
3. Transition `not_yet_evaluated → evaluating → deferred`, with `FinalOutcomeDeferred` and the reason from §1. A same-owner resumed receipt uses its current state on an allowed edge.
4. Set `live_hold = :hold` and `retry_after = NULL` on every owned (or, without a receipt, active) **video-scoped** row. Keep `id`, `deferral_reason`, `run_id`, `created_at`, `retry_count` and the owner history.
5. If there is no such row, create exactly one with `deferral_reason = 'live_or_upcoming'`, `live_hold = :hold`, `status = 'pending'`, `retry_after = NULL` and `run_id = runID`. Create it even when the receipt owns only capacity rows. The uniqueness guard is `NOT EXISTS (… video_id = ? AND status IN ('pending','retrying') AND <video-scoped>)`, so capacity rows never suppress it. `<video-scoped>` means not a capacity row, as capacity ARD-001 defines one; before capacity ships, every row is video-scoped.
6. With a receipt, run `AcknowledgeRetryDeferralReceiptTx`. Owned video-scoped Deferred rows return to `pending`. Owned Error rows become `resolved` with their history kept (BR-035 AC11). Owned capacity rows are released to `pending` unchanged: same ID, enrollment, age and cap count.

It never writes a route decision, condition audit, duplicate evidence, action success, guard, Errors entry, `failed` or a final outcome. A future AC7 cap would be the only addition here.

### 6.2 `liveDeferAfterEvaluation(ctx, runID, video, status)`: a decision is recorded but the add is not sent

This reuses the executor's guarded post-evaluation deferral machinery: `retainGuardedDeferralTx` and the context handoff (`executor.go` ≈ 119, 511–561, 843). It does **not** reuse the quota branch's `deferActions`.

- **A separate handoff disposition.** Today the handoff is `func(*sql.Tx, queue.DeferredReason) error`, which carries one reason that serves as both the queue reason and the run reason. Add a live variant that takes `LiveDeferral{QueueReason: "live_or_upcoming", Hold: live_now|upcoming|unconfirmed, RunReason: live_now|live_upcoming|live_unconfirmed}`. The handoff writes:
  - the queue sink, using `QueueReason` and `Hold` under the video-scoped reuse-or-create rule of §6.1 steps 4–6;
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

When the receiving run's gate records `none` for a claimed receipt, set `live_hold = NULL` on its owned video-scoped rows. Do this in the transaction that moves the video to `evaluating`, or that starts `continueOriginalDecision`. If that transaction rolls back, the hold remains. No other writer clears it: Stop/Discard handoff, quota, auth or rate deferral, Dismiss and final close all leave `live_hold` unchanged.

## 7. Backstops and fences

- **Evaluation backstops.** On production runs only, `videoContextForExclusion`, `detectVideos` and `routeVideos` check `LiveClearedTx(runID, v)`. On failure they return `ErrLiveNotCleared` for that video and do nothing for it. The caller maps that to `liveSkip(…, unconfirmed, receipt)`.
- **Send fence.** `ClaimPlaylistSend` checks `LiveClearedTx(sendingRunID, video)` at the authority step, before exact-target success, and again at final POST admission (under the capacity lifecycle gate where that applies). On failure it returns `state.ErrLiveNotCleared` before any guard write. The executor checks for this error **before** `isRetryAdmissionRefusal` and before the generic `ActionOutcomeFailed` branch (`executor.go` ≈ 749–757), and maps it to `liveDeferAfterEvaluation(…, unconfirmed)`, which commits inside the guarded path and returns `(Deferred, nil)` (§6.2). It is never authority failure, `retainAuthorityFailure`, `ActionOutcomeFailed`, `FinalOutcomeFailed` or an Errors entry.
- **Capacity.** The planner calls `liveGateClaimed` (Live only) before the first count GET for a claimed capacity row's video. If the result is not `none`, there is no GET and no POST, and the video goes through `liveSkip` with the receipt.
- Every refusal logs `diag:v1 phase=routing operation=check_live_status code=live_status_not_current`.

## 8. Queue, inspection, statistics, registrations and migration

### 8.1 Reads and registrations

- **`GET /api/deferred`.**
  - Each item gains `live_status` (`live_now`, `upcoming`, `unconfirmed` or `null`, from `live_hold`).
  - `reason=live_or_upcoming` selects `status IN ('pending','retrying') AND live_hold IS NOT NULL`, which is active rows only.
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

### 8.2 Migration (no number assigned)

One forward migration:
- **Order.** Strictly after Search `029`, the receipt-provenance migration and the Discovery migration (ARS-005 §5.1.1 order). If capacity's `deferred_item` rebuild is in the same release, the two are combined into one rebuild, or the later rebuild carries every earlier column and CHECK value. The integration owner assigns the number.
- **`run_video_outcome`.** `ALTER TABLE … ADD COLUMN live_status …` and `ADD COLUMN live_checked_at TIMESTAMP`. This is not a table rebuild, and the `028` triggers remain. There is no backfill: rows in flight read as `NULL`, and the resume gate reads them once (§5.1).
- **`deferred_item`.** `live_hold` can be added with `ADD COLUMN … CHECK (live_hold IS NULL OR live_hold IN ('live_now','upcoming','unconfirmed'))`. The wider `deferral_reason` CHECK (adding `live_or_upcoming`, and `discovery_processing_pending` if it is absent) needs a rebuild like `026`. The rebuild preserves:
  - IDs and the `sqlite_sequence` high-water mark;
  - `closed_reason` (`027`) and any capacity columns;
  - all indexes and foreign keys;
  - every existing CHECK value.
- **Store A** is unchanged.
- **Down migration.** It refuses while any row has a live hold or a `live_or_upcoming` reason. Older writers stop before the migration.
- **Release.** The parser, recorder, gate, writers, fences, readers and SPA ship together. On claimed paths they ship only together with or after ARS-005 §6.1–6.2.

## 9. Tests (real server path, migrated SQLite, request-counting controlled provider)

- **L1. Skip, restart, confirm.** Run 60 records `live` for L and `upcoming` for P from the prefetch, with zero gate reads. It skips both, leaving one item each with a hold and no decision or send. After a restart, run 61 reads each once. L is `none`: it is routed and added under run 61's snapshot, and its hold is cleared. P is skipped again on the same item ID, with `created_at` and the origin unchanged.
- **L2. Outcome rows.** The run reasons are `live_now` and `live_upcoming`. There is no `route_decision`, audit, duplicate row, action, guard or final outcome.
- **L3. Ambiguity.** Every `unconfirmed` row of §2, plus `P0D` or a missing duration with no `liveBroadcastContent`, gives `live_unconfirmed` and nothing is evaluated.
- **L4. No report, claimed work.** Quota, auth, rate, transport and invalid-envelope failures keep the existing §6.2 rows. No live reason, final outcome or `failed` comes from live status.
- **L5. Exclusions and duplicates.** A live video that an exclusion matches and that has a duplicate title is deferred, with no exclusion and no duplicate evidence. Once confirmed, the normal behavior applies.
- **L6. Another run's status.** Run 59's row says `none` while the provider reports `upcoming`. Run 60 reads the video and skips it. A Store A `live`-free refresh changes nothing.
- **L7. One item, once per run.** The video is not re-claimed in the same run, and there is no second active video-scoped row. A claimed Errors entry becomes `resolved`, and the sink is created. `RecordLiveStatusTx` called twice leaves the first value.
- **L8. Long wait.** Over 30 runs P is read once per run, keeps one item and is never final. Dismiss stops further reads, and re-queue does not restore it.
- **L9. Absent.** A complete response omits the video. A `requeued_by_user` receipt gets `unavailable_on_youtube`; another claimed receipt gets ordinary retention. A new discovery gets native retention with `metadata_unavailable` and a pending item. An incomplete response releases or retains as in §5.
- **L10. BR-039.** A final video now reported live: zero reads and only the existing close. A partly-done video reported `upcoming`: a live-only read, no guard or POST, and its sources unchanged. After confirmation only the missing add is sent, under the original decision.
- **L11. Capacity.** A live video that matches a full playlist: no count GET, no waiting row, and the cap count is unchanged. A capacity-only receipt for a video that is not confirmed: no GET or POST, the capacity row keeps its ID and age, and a video-scoped live item exists. Evict that capacity row: the video still has an active item.
- **L12. Stop and re-queue.** Stop with a live-held claim: the item is `pending` and keeps its hold. The BR-037 preview does not count the video.
- **L13. Registration.** API and SPA show the reasons and `live_status`. The filter returns active held rows only, and a completed row with a stale hold is excluded. Statistics count the video as deferred. An unknown value is withheld.
- **L14. Fences.** Call `routeVideos` and `ClaimPlaylistSend` directly with no row value, with `live`, and with another run's `none`. Each ends deferred with `live_unconfirmed` and an active item, with no `ActionOutcomeFailed`, Errors entry or failed run, plus the fixed diagnostic.
- **L15. Simulations.** Dry run, Replay and Backtest over the latest production values `live`, `upcoming`, `unconfirmed`, `none` and none at all give the §8.1 labels. They make zero requests and write nothing.
- **L16. Resume after expiry.** A Run Now run stops at its time limit after routing, then expire every Store A row. Resume it at the action phase: zero live reads, adds are sent, no `failed`. Repeat with a migration-era run whose rows are `NULL` and whose provider now reports `upcoming` for one routed video: one live-only read, then `liveDeferAfterEvaluation`, no send, and the decision kept. Repeat at the duplicate-detection and routing phases.
- **L17. New discovery without a report.** The prefetch and the gate read are quota-refused, failed or returned an invalid envelope. The video ends `deferred` with an existing reason or `metadata_unavailable`, with exactly one pending item, and a later run claims it. It is never `not_yet_evaluated` without an item.
- **Upgrade.** Seeded rows: IDs, the AUTOINCREMENT high-water mark, `closed_reason`, indexes, the `028` and `029` triggers and the search rows are unchanged, and the new columns are `NULL`.

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
