---
type: decision
title: ADR-006 — Re-queue Stranded Videos with a Two-Phase Scan-and-Insert Under the Initiation Locks
description: Realize BR-037 as a preview read plus a confirm that scans the stranded set in a read snapshot and inserts every deferred item in one short write transaction, under initiationMu and the production lock. Blocked follows the send-time veto (branch 3 evaluated as at send time) through one shared SQL predicate. Runs claim deferred work in the existing created-at order, in quota-gated batches. Retried videos are never routed on missing or expired metadata, and a re-queued video whose id is absent from YouTube's raw response ends with the final outcome unavailable_on_youtube. A paged GET /api/deferred is a prerequisite.
tags: [architecture, decision, runs, deferred-queue, recovery, run-14, send-guard, stranded, metadata, quota, outcomes, paging, concurrency, migration]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted — technical realization of [BR-037](../../../requirements/yuhaul/br-037-requeue-stranded-videos.md) AC1–AC19, of [BR-035](../../../requirements/yuhaul/br-035-stop-active-run.md) AC14 as amended 2026-11-11, and of [BR-032](../../../requirements/yuhaul/br-032-processing-outcome-distinction.md) AC1 as amended 2026-11-11. Revised 2026-11-11 after those amendments, then corrected the same day after critic review `ses_e33a11010ffe4XP0Rmw0ZVtQBs` (CHANGES REQUIRED; F1, F2 and minors applied, F3 prepared in two variants): see "Revisions" below. Reconciled 2026-11-11 (Plan revision 19) with the user's decisions: F3 is decided as **variant K** (BR-035 AC14 unchanged); F4 does not apply to production data; the paged deferred read gains page jumps, positions and a reason filter (Decision 9). Not implemented. The exact contract is [ARS-005](../specifications/ars-005-requeue-stranded-videos.md). It reuses the Discard and Mark-as-ended locking ([ADR-003](adr-003-discard-unusable-time-limit-run.md), [ADR-005](adr-005-mark-legacy-run-as-ended.md)), the claim and processing path of the stop handoff ([ADR-004](adr-004-stop-active-run-and-deferred-handoff.md)), and ADR-004's guarded `writable_schema` procedure. Independent critique is recommended before implementation.

## Summary

**Prerequisite.** `GET /api/deferred` and the Deferred view first become paged. The read returns 50 items by default, with a total, counts by reason, id-based next and previous cursors, page jumps (`page=n` or `last`) with exact positions, and an optional server-side reason filter (Decision 9). The re-queue endpoints and their control ship only in a build that already has paging.

**What the user gets.** `GET /api/stranded-videos` shows how many videos the action would re-queue. It also shows why every other unfinished video would not be:

- it already has a queue item (with the Errors-queue ones counted separately);
- the user removed it;
- a playlist addition may already have been sent (blocked, listed one by one);
- one of its runs has not ended.

It also counts known videos with a committed `deferred` or `failed` outcome and no active queue item. Those are counted only, never queued. `POST /api/stranded-videos/requeue` repeats the whole check. In one transaction, it gives every stranded video one pending deferred item with the reason `requeued_by_user`, eligible at once. It makes no YouTube call and starts no run.

**How runs then process them.** A new production run claims deferred work in the existing order (each video's oldest outstanding item, by `created_at` then id), 500 videos per claim transaction. It keeps claiming while quota remains, until it is stopped, interrupted or shut down, or no eligible item is left. It never skips an eligible item for a later one. A provider failure other than a quota refusal does not end claiming (variant K, decided 2026-11-11; Decision 5). Before a retried video is evaluated, the run makes sure its metadata is current. If it is not, the run fetches it, 50 videos per `videos.list` call, through the shared quota admission ([authorization ADR-004](../../youtube-authorization/decisions/adr-004-atomic-quota-admission-and-single-attempt-transport.md)). The result decides what happens to the video:

| Result | What happens |
|---|---|
| Metadata arrives | The video is evaluated normally. |
| Quota, rate-limit or authorization refusal | The video is deferred with that reason. |
| Any other failed request | The video is released unchanged. |
| YouTube returned the item but it cannot be used (for example an unparseable `publishedAt`) | The video is released unchanged. |
| A successful answer whose raw item ids lack the video, for a **re-queued** video | The final outcome `unavailable_on_youtube` (Decision 8). No route is evaluated. Its item is completed. No Errors entry is made. Nothing processes it again. |
| A successful answer without the video, for any **other** retried video | `failed` in the Errors queue, as before. |

A retried video is never routed on empty metadata.

**Example.** Run 14 has been marked as ended ([BR-036](../../../requirements/yuhaul/br-036-mark-legacy-run-as-ended.md)). The preview reports:

```
requeue: 70000
blocked: 41 (with the list)
not_ended: [{run_id: 52, videos: 12}]
already_queued: {videos: 1180, in_errors_queue: 2}
removed_by_user: 3
retained_without_queue_item: {videos: 150}
```

The user confirms with `{"expected_requeue": 70000}`. The server holds the initiation and production locks for the scan, a few seconds. It holds the writer lock for the insert, with a target of under 2 seconds. It answers `200` with `requeued: 70000`.

The Deferred view then opens on the first 50 of 70,000. The next scheduled run claims the 500 oldest eligible items and fetches their metadata in 10 calls. It evaluates them and adds the matching ones to playlists. It claims the next 500, and so on until quota runs out. It may use the whole day's quota, so new uploads wait (BR-037 AC17). Of the re-queued videos, 300 are deleted or private. Each of these ends as `unavailable_on_youtube`, has its own count in the run, and never reaches the Errors queue.

## Satisfies

| Requirement | How |
|---|---|
| BR-037 AC1 | Only the confirm handler calls `RequeueStranded`. It is registered next to Run Now (same access). No scheduler, startup, run, Stop, Discard or Mark-as-ended path calls it. |
| AC2 | Refusal order: `trackRun` (`shutting_down`), the producer gate (`startup_recovery_active`), `initiationMu`, then `productionMu.TryLock` and the registered-production-run check (both `production_run_active`). A simulation holds neither lock and is not registered as production, so it does not refuse. Refusal bodies carry closed codes only. |
| AC3, AC16 | The scan runs at confirm time, after the locks are taken. The insert transaction rechecks every predicate that a writer outside these locks can change (Decision 3). |
| AC4 | The preview runs the same scan read-only. It returns every count AC4 lists, including `already_queued.in_errors_queue` and `retained_without_queue_item`, with the blocked list and the not-ended runs. It takes no lock and writes nothing. Errors-queue entries are active queue items, so the action never touches them. |
| AC5 | The acknowledgement carries the actual inserted count, the same categories, `expected_requeue` echoed, and `differs_from_preview`. An empty stranded set commits nothing and answers `outcome: "nothing_to_requeue"`. |
| AC6 | One `INSERT … SELECT` per chunk with `status='pending'`, `retry_after=NULL`, `retry_count=0`, `deferral_reason='requeued_by_user'` and `run_id` = the origin run. Earlier rows are not touched. |
| AC7, AC8 | The confirm writes only `deferred_item` inserts. There is no provider client, auth binding, scheduler, `Execute` or `StartRun` on this path. |
| AC9 | All inserts are in one SQLite transaction. A failure rolls back. A crash leaves the WAL without the commit record, so none of the rows survive. |
| AC10 | A video with a pending item has an active queue item and is no longer stranded. The insert's `NOT EXISTS` recheck and `initiationMu` make a double submit insert nothing the second time. |
| AC11; BR-035 AC14 (amended) | Decision 5: claiming in the existing created-at order while quota remains. There is no batch limit of the action's own. |
| AC12 | `ClaimPlaylistSend` behaves exactly as before; it now reads its legacy veto from the shared predicate. |
| AC13 | Decision 6. The three cases are distinct statuses of one fetch. Only a successful answer whose raw item ids lack the video yields case 2. A returned but unusable item is case 3. |
| AC14 | A `running` or `interrupted` run in a video's candidate set puts the video under `not_ended`. Once Mark as ended sets that run to `failed`, the same videos become stranded. |
| AC15 | Each blocked video is listed with its reason and run, in the preview and in the acknowledgement. No request field can include a blocked video. |
| AC17 | Nothing reserves quota. The quota gate only asks whether one unit remains. Processing order is unchanged: deferred work runs before discovery. |
| AC18; BR-032 AC1, AC4–5 | Decision 8: the outcome value `unavailable_on_youtube` on `video`, `run_video_outcome`, counters and filters. The re-queued item becomes `completed`. There is no Errors entry and no new deferred item. The video is committed, so no scan, claim or handoff selects it again. |
| AC19 | Decision 9, shipped before the action. |

Satisfied elsewhere: control placement, confirmation, acknowledgement and refusal copy, the deferred-queue reason label, the outcome label and the Deferred view's paging controls (Designer); send-guard semantics ([BR-012](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md), [OC-006](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md)).

## Current source (read 2026-11-11)

**Send veto and guards**

- `internal/state/playlist_guard.go:79–225`: `ClaimPlaylistSend` runs these checks in order:
  1. exact-target success (`:116–123`);
  2. the legacy veto (`:129–208`);
  3. the guard insert with `ON CONFLICT(video_id,target_playlist_id,action_type) DO NOTHING` (`:210–220`).

  Veto branches 1–3 refuse whatever the target is. Branch 4 refuses only when the requested target has no success (`:195–199`).
- Veto branch 3 (`playlist_guard.go:172–182`) reads the video's *live* `processing_state = 'acted'`. A claim resets a claimed video to `not_yet_evaluated` (`internal/queue/retry.go:229–232`), and the receiving run moves it to `acted` before it sends (`internal/pipeline/pipeline.go:886–897`). So branch 3 can be false when the scan runs and true when the receiving run sends.
- `internal/pipeline/executor.go:956`: an admitted POST with an unknown result is recorded as `executed_action.outcome='failed'`, `outcome_reason='mutation_application_uncertain'`, and its guard is kept. `ResolvePlaylistSendTx` (`playlist_guard.go:248–287`) deletes a guard only with a conclusive outcome, so an uncertain row always has its guard.

**Claiming and retry processing**

- `internal/pipeline/pipeline.go:247–275`: `createClaimedRunLocked` claims every candidate from `ListDeferredRetryCandidates` in the run-creation transaction. `internal/queue/retry.go:470–482`: candidates are ordered by the video's oldest outstanding row (`created_at`, `id`), using correlated subqueries on `deferred_item.video_id`, which has no index. `claimRetryGroupTx` (`:175–263`) inserts a `run_video_outcome` membership row per claimed video and resets the video to `not_yet_evaluated`.
- `retry_after` is only ever cleared, never set to a time, by current code. The only non-test reference is the scan at `internal/queue/store.go:378`.
- `internal/pipeline/pipeline.go:413–418`: deferred work is processed before discovery. `internal/pipeline/retry.go:121–264` (`processRetryVideo`) runs the exclusion gate, duplicate detection and routing for a claimed video. `acknowledgeRetryReceiptTx` (`:328–357`) completes deferred rows and resolves error rows.
- `internal/pipeline/authority.go:371–382`: a run's unfinished set treats a video as finished only if it is `completed`, or has a `run_video_outcome` in `('accepted','rejected','excluded','deferred','failed')`. Stop and Discard hand every other member to the deferred queue.

**Metadata**

- `internal/pipeline/pipeline.go:747–751`, `exclusion.go:195–205` and `pipeline.go:683–687`: routing, the exclusion gate and duplicate detection replace unreadable or expired metadata with an empty record.
- `internal/metadata/manager.go:264–273`: `GetDisplayMetadata` returns `MetadataExpiredError` for an inactive or expired row.
- `cmd/server/main.go:656–697`: `metadataFetcher.FetchMetadata` calls `videos.list` through `DispatchWithQuota`. A response simply omits ids that YouTube does not return. `internal/youtube/client.go:638–643`: `GetVideos` also silently drops a returned item whose `publishedAt` does not parse, so the adapter's output is not the set of ids YouTube returned.
- `internal/auth/service.go:323`: `ErrQuotaRefused`. `internal/youtube/client.go:90`: `APIError.Reason`.

**Outcomes and their schema**

- `internal/store/migrations/001_initial_schema.up.sql:83`: `video.final_outcome CHECK(final_outcome IN ('accepted', 'rejected', 'excluded', 'deferred', 'failed', 'not_yet_evaluated'))`. Many tables reference `video`, and `deferred_item` cascades on delete, so `video` cannot be rebuilt inside a migration (same reason as ADR-004 Decision 1).
- `015_run_video_outcome.up.sql:4`: the same value list on `run_video_outcome`.
- `internal/store/store.go:111`: `widenRunStatusCheck` is the proven `writable_schema` step (ARS-003 §9, M0).
- Rejected and excluded videos stay `evaluated` with their outcome (`pipeline.go:792–803`).
- `internal/state/store.go:961–966`: counters are recomputed per outcome value. `internal/state/models.go:280–283`: `VideoFinalOutcome.Valid` gates the run-videos `outcome` filter (`internal/server/run_handlers.go:246–248`).
- `internal/stats/store.go:384–415` aggregates the counter columns.

**Concurrency and the existing deferred list**

- `internal/server/server.go:181–183, 661`: only Run Now has a time limit (30 minutes). `internal/scheduler/scheduler.go:173–182`: a scheduled tick blocks in `StartRun` while the production lock is held.
- `internal/store/store.go:64–69`: WAL with `busy_timeout=5000`. `internal/queue/retry.go:124–142`: `WithRetryTx` begins a deferred transaction (`BeginTx(ctx, nil)`). Its first statement is a no-op write, `UPDATE run SET id = id WHERE id = -1`, which acquires SQLite's writer lock before `fn` runs. Waiting for that lock is bounded by `busy_timeout`, and the transaction is not retried.
- `internal/queue/handlers.go:111–133`, `store.go:220–246`: `GET /api/deferred?include=pending|all` returns every row, ordered by `created_at`.
- `internal/pipeline/end_legacy.go:136–152`: `productionRegistered`.

## Decision 1 — where the stranded and blocked rules live

**Blocked follows the veto through one SQL text.** The legacy veto in `ClaimPlaylistSend` moves into one exported predicate, `state.LegacySendVeto`. It is written against two aliases:

- `old_run`: a `run` row;
- `subject`: a row with `video_id`, `target_playlist_id`, `receiving_run_id` and `assume_acted`.

`ClaimPlaylistSend` supplies one `subject` row from its parameters, with `assume_acted = 0`. The stranded scan supplies one `subject` row per candidate video, with `target_playlist_id = NULL`, `receiving_run_id = 0` and `assume_acted = 1`. The only textual change to the veto is in branch 4: `NOT EXISTS (success for the target)` becomes `(subject.target_playlist_id IS NULL OR NOT EXISTS (…))`. With a real target, this is exactly the old predicate. With NULL, it reads "some playlist target has no success". That is always true, because the set of possible playlists is open.

**Branch 3 is evaluated as it will be at send time** (critic F1). Branch 3 tests the video's *live* `processing_state = 'acted'`. A re-queued video is not `acted` when the scan runs. A run that later claims it resets it to `not_yet_evaluated`, and then moves it to `acted` itself just before sending (Current source). So at send time, branch 3 sees `acted` and refuses every target.

The predicate's branch 3 condition becomes `(subject.assume_acted = 1 OR old_video.processing_state = 'acted')`. The scan sets `assume_acted = 1`, so it blocks exactly the videos the send path will refuse. `ClaimPlaylistSend` sets `assume_acted = 0`, so its behavior is unchanged.

The other live inputs of the veto are stable between the scan and the send for a stranded video:

- Guards, version-0 decisions and actions change only under `productionMu`, and successes only remove blocks.
- Branch 4's `retry_run_id` link is on queue rows that are no longer active. A stranded video has no active queue item, and a claim rewrites only `pending` and `retrying` rows.

**Question for the send-guard owner** (authorization [ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md)). Branch 3 fires on the receiving run's own `acted` transition. Was it meant only for videos the version-0 run itself left `acted`? If it is narrowed (for example to "acted and not reset by a later claim"), the scan's `assume_acted` must follow it, because the shared text keeps them aligned. This design does not change the veto. Not blocking.

*Blocked*, exactly: a video is blocked when `ClaimPlaylistSend` would refuse at least one playlist addition for it, over every possible target. Following the claim's order:

1. The success check returns `already_completed` only for a target that already succeeded. So a refusal needs a target without success, and one always exists.
2. Legacy veto: branches 1–3 do not depend on the target, and branch 4 holds for any target without success. So the legacy part refuses some addition exactly when `LegacySendVeto` holds with a NULL target.
3. Guard insert: refused for target T when a guard for (video, T) exists and T has no recorded success. Otherwise step 1 would have returned first.

So `blocked(v) = EXISTS(guard (v,T) with no success for (v,T)) OR EXISTS(old_run satisfying LegacySendVeto for subject (v, NULL, 0, assume_acted = 1))`.

The reported reason is:

- *uncertain addition* when the guard's run has an uncertain `executed_action` row for the same video and target;
- *unresolved send guard* for any other guard;
- *ambiguous legacy evidence*, with `old_run.id`, for the legacy part.

A video may have several (reason, run) pairs. It is counted once, under the first of uncertain addition, unresolved send guard and ambiguous legacy evidence.

*Removed by the user* and *active queue item* are two more shared predicates, in package `queue` (`queue.ActiveQueueItem`, `queue.RemovedByUser`). The scan and the insert's recheck use the same text.

| Option | Verdict |
|---|---|
| **One predicate text with a nullable target (chosen)** | The send path and the report read the same SQL, and one test suite covers both. |
| Copy the veto into the scan | Rejected: two copies drift. |
| Call `ClaimPlaylistSend` per video and target in a rolled-back transaction | Rejected: it needs a receiving run and a target list, and it would take the writer lock 100,000 times. |
| A SQL `VIEW` | Rejected: views cannot take the receiving run and target parameters. |

## Decision 2 — the stranded scan and its categories

The scan reads in one transaction. It starts from known unfinished videos: `processing_state <> 'completed'` and `final_outcome` NULL or `not_yet_evaluated`. A video with any committed outcome, including `unavailable_on_youtube`, is never a candidate.

The *video's runs* are the production runs (`manual`, `scheduled`) that reach it through any of these:

- `video.discovered_in_run_id`;
- `route_decision.run_id`;
- `deferred_item.retry_run_id`;
- `error_entry.retry_run_id`;
- `run_video_outcome.run_id`.

Each unfinished video falls into exactly one category, in this precedence:

1. `already_queued`: it has an active queue item (a pending or claimed deferred item, or an active or claimed Errors entry). Sub-count `in_errors_queue`: those with an active or claimed Errors entry (AC4).
2. `removed_by_user`: its most recent queue item over both queues, by `created_at` then id, is a `discarded` deferred item or a `dismissed` Errors entry.
3. `blocked`: Decision 1.
4. `not_ended`: one of its runs is `running` or `interrupted`. Reported per run.
5. `no_origin_run`: none of its runs exists. This is an integrity count, expected to be 0.
6. Otherwise **stranded**. Its origin run is the highest id among its runs, which is the run that most recently owned it.

Blocked comes before not-ended, so every blocked video the user could otherwise reach is listed (AC15). The separate count `retained_without_queue_item` covers known videos with `final_outcome IN ('deferred','failed')` and no active queue item, with a sub-count of those the user removed (AC4).

## Decision 3 — the confirm: two phases under the locks, one write transaction

The order is:

1. `trackRun`;
2. the producer gate;
3. `initiationMu.Lock()`;
4. `productionMu.TryLock()`;
5. the registered-production-run check;
6. **phase A**;
7. **phase B**;
8. release.

The confirm runs under a context detached from the request (`context.WithoutCancel`), bounded to 2 minutes. A closed browser tab does not cut it short, and shutdown waits for it through `runWG`.

- **Phase A (read).** A read-only transaction runs the scan from Decision 2. It produces every stranded `(video_id, origin_run_id)` pair and the category counts.
- **Phase B (write).** `WithRetryTx` begins a deferred transaction whose first statement is a no-op write, so the writer lock is held before any recheck runs. It inserts in chunks of 2,000 pairs, one `INSERT … SELECT … FROM json_each(?)` per chunk. Each row is inserted only if the video still meets all of these:
  - it is still unfinished;
  - it has no active queue item;
  - it is not removed by the user;
  - it has no guard without a success.

  Target: under 2 seconds at 100,000 rows. Other writers wait at most 5 seconds (`busy_timeout`).

**Why phase B does not repeat the full scan.** With `initiationMu` and `productionMu` held, the gate open and no production run registered, nothing can change the inputs of the legacy veto or of the "runs ended" test:

- Run status changes come from executing production runs, Discard, Mark as ended, stop settling and startup. Every one of them is excluded.
- Version-0 evidence is never written again.
- A new success can only remove a block.
- Simulations write only rows of simulation runs.

The queue's Dismiss handlers take none of these locks, and phase B rechecks their effect. Rule 4 below records this assumption.

| Option | Pros | Cons / failure modes | Verdict |
|---|---|---|---|
| **Read-only scan, then a short insert transaction with a cheap recheck, under the locks (chosen)** | Short writer lock; atomic insert | Relies on the lock argument for the expensive predicates | **Chosen.** |
| Scan and insert in one `IMMEDIATE` transaction | Simplest proof | Writer lock held for the whole scan (estimated 5–30 s, [unverified]); concurrent writers fail with `SQLITE_BUSY` after 5 s | Rejected. |
| Several insert transactions | Short locks | Violates AC9 | Rejected. |
| A temporary table on a pinned connection | Set-based recheck | Needs a connection-scoped `WithRetryTx`, for no gain over a JSON parameter | Rejected. |

**Commit uncertainty.** If `Commit` fails (`queue.ErrCommit`), the server answers `500 requeue_outcome_unknown`. It makes no confirming read. Repeating the action is harmless (AC10), and the preview shows the truth.

**Preview.** `PreviewStranded` runs only phase A, takes no lock, and adds an advisory `available` flag with an `unavailable_reason`.

## Decision 4 — migration 026 and the outcome schema step

Migration 026 (SQL, one transaction) does three things:

1. Rebuild `deferred_item` exactly as migration 025 does, with `'requeued_by_user'` added to the `deferral_reason` CHECK.
2. Add the indexes `deferred_item_video_status ON deferred_item(video_id, status)` and `error_entry_video_status ON error_entry(video_id, status)`.
3. Add the column `run.videos_unavailable INTEGER NOT NULL DEFAULT 0`.

The origin run is stored in the existing `deferred_item.run_id`.

**Migration 026 owns both `(video_id, status)` indexes and lands first.** It must be the next migration after 025, before any other change that needs these indexes, including video search. The `deferred_item` rebuild drops every index on that table that the rebuild does not recreate. So no other change may create an index on `deferred_item` or `error_entry` for the same columns. Video search relies on 026's indexes and adds no migration for them.

A Go schema step, `widenFinalOutcomeChecks`, runs after `m.Up()` and after `widenRunStatusCheck`. It uses the same guarded `writable_schema` procedure (ARS-003 §1.2) to rewrite the exact CHECK texts of `video.final_outcome` and `run_video_outcome.final_outcome` so they admit `'unavailable_on_youtube'`.

- **Per-table skip.** Each table is skipped when its own definition already contains `'unavailable_on_youtube'`. The remaining rewrites run in one transaction, with `schema_version` bumped once, and `integrity_check = ok` is required afterwards. A database where only one table was widened therefore completes the other, and a fully widened database is untouched.
- **Why not a rebuild.** `video` cannot be rebuilt (Current source). `run_video_outcome` could be, but one mechanism for both tables is simpler and copies no data.

**Release note (no rollback).** Once any row holds `unavailable_on_youtube`, running an older binary is unsafe. An older binary rejects the value in its outcome validation. Worse, its per-run "unfinished" list does not count the value as finished, so a stop or discard would hand that video back to the deferred queue, breaking BR-037 AC18. The widened CHECKs stay after a downgrade. Rolling back past this release needs a forward fix, not a binary swap.

Rejected: a `requeue_action_id` column on `deferred_item`. Idempotency and `created_at` already cover confirmation and audit.

## Decision 5 — claiming in the existing order, in quota-gated batches

**Claim order (unchanged).** Candidates come in today's `ListDeferredRetryCandidates` order: each eligible video's oldest outstanding row, ordered by `created_at`, then id (`retry.go:470–476`). The query only gains a `LIMIT` and an exclusion of videos this run already claimed.

An item that a run claimed and then released, for example deferred for quota, keeps its `created_at`, so it keeps its place. The backlog therefore continues oldest first on the next quota day.

Current code never sets `retry_after` to a future time (Current source). So for every row it creates, an item is eligible from its creation onward, and creation order *is* eligibility order. That is why this order already satisfies "never pass over an earlier-eligible item".

A row written by older code with a future `retry_after` would sort by its `created_at` once it becomes eligible. It could then be claimed ahead of rows that became eligible before it. Before release, a one-off read-only check counts such rows: `SELECT COUNT(*) FROM deferred_item WHERE status='pending' AND retry_after IS NOT NULL AND julianday(retry_after) > julianday('now')`. A non-zero count goes to the revisit trigger below.

The first draft's "eligible-since" order and its change to the ARS-003 handoff are withdrawn (critic minor): they produced the same sequence for every reachable row, and added text and an amendment of implemented behavior.

**Batches.** `retryClaimBatch = 500`.

1. `createClaimedRunLocked` claims the first 500 in that order.
2. `processDeferredItems` processes every owned video. It then claims the next 500 in its own `WithRetryTx` while all of these hold:
   - the run's `initiated_by` is `run_now` or `schedule`;
   - its context is not done (stop, time limit or shutdown);
   - `ledger.CanAfford(ctx, OpVideosList cost)` is true;
   - eligible candidates remain;
3. A next batch excludes only videos that already have a `run_video_outcome` row in this run. The run has already claimed those, so excluding them passes nothing over. It only stops one run from claiming the same video twice.

**Provider failures during claiming — decided: variant K (critic F3; user decision 2026-11-11).** A provider failure other than a quota refusal is an authorization lapse, a rate limit, or a network, 5xx or malformed response. It always suspends metadata fetching for the rest of the run (Decision 6). The variants differ only in whether the run then keeps claiming.

| | **Variant K — keep claiming (decided)** | **Variant S — stop claiming (rejected)** |
|---|---|---|
| Requirement | Amended BR-035 AC14 as written: the run may stop claiming only on quota exhaustion, stop, interruption or shutdown. | Needs BR-035 AC14 to add: "MAY stop claiming after a provider failure that prevents processing". |
| After a provider failure | The run keeps claiming batches. Each claimed video without current metadata is released unchanged without a call. Videos with current metadata are evaluated. | The run finishes its current batch (same per-video handling) and then claims no more. Unclaimed items stay pending and untouched. |
| Cost of one transient failure | One pass over the whole remaining queue: about 70,000 claims, membership rows and release transactions, and about 140 candidate scans. A stop of that run hands off all of its members. | At most one batch (500) claimed and released. |
| Spec delta | None. | Would have needed an AC14 amendment and the changes kept in ARS-005 Appendix A as a rejected alternative. |

**Decided: variant K.** The user chose it on 2026-11-11 (BR-035 Derived from, AC14 confirmation), so BR-035 AC14 stays as amended and ARS-005 §5.2 is the contract. The critic and this ADR had recommended S. **Accepted trade-off (user):** after one non-quota provider failure, a run can claim and release the rest of a large backlog (about 70,000 items) in one pass without processing it, and a stop of that run hands all its claimed items back. This costs local work and saves only; it makes no YouTube call, and no item is lost or stranded. Variant S stays in ARS-005 Appendix A only as a rejected alternative. Revisit only if the requirement changes.

**BR-035 AC14 (amended) — check.**

- *Keep claiming while quota is left, not stopped or interrupted, and items remain:* the loop's conditions are exactly these (variant K). A queue-retry run is not a run that "starts as a new run".
- *May stop once quota is exhausted, stopped, interrupted or shut down:* yes. The quota test is "one more metadata call is affordable", checked between batches. Inside a batch, items claimed after quota runs out are deferred with `quota_exhausted` and keep their pending item.
- *Unclaimed items stay pending and eligible, unchanged:* the loop never writes to an unclaimed row.
- *Never pass over an earlier-eligible item for a later one:* claims follow creation order, which equals eligibility order for every row current code writes. The claim set is always a prefix of that order. It skips only three kinds of video, none of which is passed over:
  - videos with a live owner, which are not eligible;
  - videos this run already claimed;
  - videos whose claim was lost to a concurrent claimer.
- *Resumed runs:* a resumed run of these types continues the loop. AC14 allows a resume not to claim; continuing is the simpler rule.
- *Interrupted Run Now:* it holds at most one batch. Scheduled runs claim the rest in order, because the held videos are not eligible.

| Option | Verdict |
|---|---|
| **Existing created-at order, quota-gated batches of 500 (chosen)** | Bounded locks and churn; satisfies amended AC14 for every row current code writes; quota remains the only limit (BR-037 AC11, AC17). |
| Eligible-since order plus a handoff change | Withdrawn: same sequence today, more text, and an amendment of implemented ARS-003 behavior. Revisit if any code sets a future `retry_after`. |
| Claim everything at start (today) | Rejected at this scale: a long writer lock, 70,000 membership rows per run, and an interrupted Run Now run would hold the whole backlog. |
| Move a released item to the back of the line | Rejected: the backlog would cycle instead of draining oldest first. |
| A per-day item budget, or quota reserved for discovery | Rejected by BR-037 AC11 and AC17. |

## Decision 6 — no rejection or exclusion from missing metadata (AC13)

**The rule applies to every retried video** (`processRetryVideo`: every claimed group). Before a claimed batch is processed, the run collects the batch's videos that lack eligible metadata (`GetDisplayMetadata` fails). It fetches them in chunks of 50 with `metadata.Manager.FetchForEvaluation`, bound to the run's authorization binding. That function issues `videos.list` through `DispatchWithQuota` with `quota.OpVideosList` and stores each usable returned record with the normal 30-day TTL.

**`absent` comes from the raw response** (critic F2). The adapter's parsed list drops items it cannot parse (Current source), so it cannot show what YouTube returned. The read client therefore reports, alongside the parsed details, the `id` of every item in the raw `items` array of a successful response, parsed or not. Then, per requested id:

- in the raw ids and parsed → `fetched`;
- in the raw ids but not parsed → `unusable`;
- not in the raw ids → `absent`.

If any raw item in a chunk has no readable `id`, no id from that chunk can be shown to be missing. Every id of that chunk that is not parsed becomes `unusable`, never `absent`.

Each video gets one status:

| Status | Re-queued video | Other retried video |
|---|---|---|
| `current` or `fetched` | Evaluate normally. | Same. |
| `quota_refused` (`ErrQuotaRefused`, or `CanAfford` false before the call) | Deferred (`quota_exhausted`): `not_yet_evaluated → evaluating → deferred`, run outcome `deferred`, `AcknowledgeRetryDeferralReceiptTx`, all in one transaction. The pending item keeps its place. The run stops claiming (quota exhausted). | Same. |
| `authorization_pending` | Deferred (`authorization_pending`), as above. Fetching is suspended for the rest of the run. | Same. |
| `rate_limited` (`APIError.Reason` `rateLimitExceeded` or `userRateLimitExceeded`) | Deferred (`api_rate_limited`), as above. Fetching is suspended. | Same. |
| `failed` (network, 5xx, malformed body or any other error) | Released unchanged (`RequeueRetryReceiptTx`). The item is pending and the video stays `not_yet_evaluated`. The failure is logged with `diag`. Fetching is suspended. | Same. |
| `unusable` (YouTube returned the item, but it could not be parsed) | Released unchanged, as for `failed`, but fetching is **not** suspended: the call itself succeeded. Logged with the item's video id only. | Same. |
| `absent`: a successful response whose raw item ids lack it | **`unavailable_on_youtube`** (Decision 8). | `failed` with `diag:v1 phase=routing operation=load_metadata code=video_unavailable` through the existing retry-failure transaction (one active Errors entry). This is the existing retention, because routing would otherwise run on empty inputs. |

*Re-queued video* means the claim receipt holds at least one `deferred_item` with `deferral_reason = 'requeued_by_user'`. That stays true while the video's re-queued item is deferred again or released, because those keep the row and its reason.

**Fetching suspended** means that for the rest of the run, every claimed video without current metadata is released unchanged without a call, while videos with current metadata are processed. Claiming continues (variant K, decided): each video is claimed at most once per run, so a run with a broken provider makes one pass over the queue and makes no further failing calls.

An `unusable` item is released, not ended. That costs one metadata unit per affected chunk on every run that reaches the item. If a real video is never parseable, it is retried indefinitely and never lost. Revisit if logs show a persistently unusable video.

**Backstop at the point of use.** For a retried video, the exclusion gate, duplicate detection and routing never substitute an empty record. If `GetDisplayMetadata` fails at that moment, they return `errInputsUnavailable`, which `processRetryVideo` maps to the `failed` row (released unchanged). Freshly discovered videos keep today's behavior.

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| **Fetch within the run through ADR-004 admission, then evaluate, defer, release or end as unavailable (chosen)** | Real inputs. About 1 unit per 50 videos, around 2,000 units for 100,000. Uses the existing fetcher and ledger. | A new `videos.list` call site inside processing; the read client must expose raw item ids. | **Chosen.** |
| Decide `absent` from the parsed list | No client change | A returned but unparseable video gets a final, false outcome (critic F2). | Rejected. |
| Defer without fetching | No quota use | Expired metadata never returns, so the work never finishes. | Rejected. |
| Skip routing | No new path | The work never finishes, which equals stranding. | Rejected. |
| Fetch only the fields the routes need | Saves a few units | More logic for little gain | Rejected; revisit if metadata spend becomes material. |

## Decision 7 — interaction with Mark as ended (BR-036, AC14)

There is no change to BR-036.

- Before run 14 is ended, it is `running`. Each of its unfinished videos counts as `not_ended` with `run_id: 14`, or as `blocked`.
- Once `EndLegacyRun` sets the run to `failed`, the next preview counts those videos as stranded, except blocked or removed ones. Their origin is 14 unless a later production run also reached them.
- Queue items that run 14 held as `retrying` become reclaimable. They are active queue items, so the action does not touch them, and the next run claims them in order. The claim rewrites their `retry_run_id`, which removes veto branch 4's evidence for any version-0 run left in `action_execution` (critic F4, pre-existing). Run 14 is in phase `discovery`, so branch 4 does not apply to it. The production check (Findings, F4) found no other such run with open retry rows.
- The two actions serialize on `initiationMu`.

## Decision 8 — the `unavailable_on_youtube` outcome (BR-037 AC18, BR-032 AC1)

**Representation.**

| Place | Change |
|---|---|
| `video.final_outcome` and `run_video_outcome.final_outcome` | The value `unavailable_on_youtube` (Decision 4 schema step). |
| `video.processing_state` | `evaluated`, the same finished state that rejected and excluded videos keep. There is no new processing state and no new transition edge. |
| `run_video_outcome` | `deferred_reason` and `error_message` are NULL, as the existing CHECKs require for every outcome except deferred and failed. |
| `run.videos_unavailable` | Recomputed with the other counters. |
| `VideoFinalOutcome` | Gains `FinalOutcomeUnavailableOnYouTube`, so `Valid` and the run-videos `outcome` filter accept it. |

**Commit** (`commitUnavailableTx`, one `WithRetryTx`):

1. Validate the receipt.
2. Transition `not_yet_evaluated → evaluating → evaluated` with `FinalOutcomeUnavailableOnYouTube`, recorded as the receiving run's outcome.
3. `acknowledgeRetryReceiptTx`: the owned deferred rows become `completed`, which keeps their history; owned error rows, if any, become `resolved`.

No route decision, condition audit, executor call, guard, Errors entry or deferred item is written.

**Never touched again.**

- The video now has a committed outcome, so the stranded scan skips it.
- Its items are `completed`, so no claim selects it.
- Discovery skips known videos.
- The per-run unfinished set (`authority.go:380–381`) gains `'unavailable_on_youtube'` in its finished list. Without this, a stop or discard of the receiving run would hand the video to the deferred queue again. This amends ARS-003 §2's `listUnfinishedRunVideosTx`.

**Readers.** Each of these places must list the value as its own category, and never count it under rejected or failed:

- outcome lists in Go and SQL (`authority.go:381`; `processRetryVideo`'s disposition check);
- the counter recompute and every `SELECT` of the counter columns (`internal/state`, `internal/stats`, `internal/config`);
- the `run.completed` event payload;
- the SPA (`web/src/types.ts`, `web/src/lib/outcome.ts`).

Statistics that count only `completed` runs and `accepted` outcomes are unchanged.

| Option | Verdict |
|---|---|
| **New outcome value, `evaluated` processing state, items completed (chosen)** | Minimal schema change; the existing "finished, not accepted" state; no new edge. |
| New processing state `unavailable` | Rejected: it widens a second CHECK, the state machine and every state switch, for no behavior that an outcome value cannot express. |
| `completed` processing state | Rejected: `completed` maps to `accepted` (`machine.go:241–242`) and to the accepted reconciliation path. |
| Rebuild `video` | Rejected: cascading foreign keys (ADR-004 Decision 1). |

## Decision 9 — paged deferred queue (BR-037 AC19), a prerequisite

`GET /api/deferred` becomes a paged read. Moving one page forward or back uses id cursors (keyset). Jumping straight to a page uses its position. A reason filter applies on the server.

- **Parameters:**
  - `include=pending|all` (as today);
  - `reason` (optional, one stored `deferral_reason`);
  - `limit` (default 50, from 1 to 200);
  - at most one of `after_id`, `before_id` or `page` (a positive integer, or `last`).
- **Order:** rows come back by ascending `id`, in one read-only transaction.
- **Response:**
  - the page;
  - `total` for the filtered set (include and reason);
  - `queue_total` and `by_reason` for the include set without the reason filter;
  - `page`: `number`, `total_pages`, `first_position`, `last_position`, `next_after_id` and `prev_before_id`.

**Why both mechanisms (reconciled 2026-11-11).** This design first rejected offset paging because it skips or repeats rows when earlier rows leave the set while the user pages forward. That reason still holds for **sequential** paging, so `Next page` and `Previous page` keep using `next_after_id` and `prev_before_id`. Every item that stays in the set is reached exactly once moving forward, new items (higher ids) appear at the end, and BR-037 AC19's "reach every item by paging" rests on this path.

A **jump** (`First page`, `Last page`, `Go to page`, or reloading `?page=n`) has no earlier position to stay consistent with. It returns whatever sits at that position at request time, so the skip-or-repeat problem cannot arise inside one forward walk.

The page number shown after cursor moves comes from the first item's exact position, `1 + COUNT(*)` of earlier rows in the set, so `Page {p} of {pages}` and `{first}–{last} of {total}` are exact at request time. When rows leave the set, cursor pages may stop lining up with the jump-page boundaries. The number shown is then the page that contains the first item, and the positions remain exact.

Costs:

- a jump scans at most `total` entries of `idx_deferred_item_status`;
- the position is one count over the same index range;
- the reason filter is checked row by row (no index on `deferral_reason`).

At 70,000 rows these are expected well under the 200 ms page target `[unverified]`. Test P1 measures them. An index on `(status, deferral_reason, id)` is the revisit if they are slow.

**Reason filter.** The value must be one of the stored reasons: `quota_exhausted`, `api_rate_limited`, `authorization_pending`, `stopped_by_user`, `discarded_by_user` or `requeued_by_user`. There is no "Other" value. `total`, positions and pages describe the filtered set. `queue_total` and `by_reason` let the view say how many items are in the whole queue.

The response keeps the key `deferred` and the item shape. Dismiss (`POST /api/deferred/{id}/discard`) and retry are unchanged.

**Prerequisite enforcement.** The stranded-videos routes and the SPA control are added only in a change set that already contains the paged read and view. Composition test X3 fails a build where the action is registered and `GET /api/deferred` without parameters can return more than 200 items. No runtime flag is added.

## Rules the implementation must preserve

1. Only the confirm handler calls `RequeueStranded`. Only `InsertRequeuedTx` writes `deferral_reason='requeued_by_user'`.
2. `ClaimPlaylistSend` and the scan use the same `state.LegacySendVeto` text. `ClaimPlaylistSend` passes `assume_acted = 0`, the scan passes `1`, and `ClaimPlaylistSend`'s results do not change.
3. All re-queue rows are written in one transaction. Phase B writes nothing but `deferred_item` inserts.
4. Phase B runs while `initiationMu` and `productionMu` are held, the gate is open, and no production run is registered. Any new writer of run status, guards, actions, decisions or video state that does not hold `productionMu` invalidates Decision 3 and must be added to phase B's recheck.
5. The confirm and the preview make no provider call. The preview takes no lock and writes nothing.
6. A retried video is never evaluated on substituted empty metadata. Its outcome is one of:
   - evaluated;
   - deferred;
   - released;
   - `unavailable_on_youtube`, for re-queued videos only, and only when a successful response's raw item ids lack the video;
   - `failed`/`video_unavailable`, for other retried videos on the same evidence.

   A returned but unusable item is always released.
7. Claims follow the existing created-at order. A claim transaction claims at most 500 groups. A run never claims a video twice and stops claiming only under the Decision 5 conditions of the chosen variant.
8. `unavailable_on_youtube` is written only by `commitUnavailableTx`. Every outcome list treats it as a finished, separate category, and readers map stored outcomes by identity.
9. Migration 026 lands before any other migration and owns both `(video_id, status)` indexes. The action ships only with the paged deferred read and view.

## Findings

| Finding | Disposition | Owner | Blocks? |
|---|---|---|---|
| **F1** (critic): the blocked scan read veto branch 3 against the live `processing_state`. At send time the receiving run has made the video `acted`, so the veto refused videos the scan had re-queued. | **Fixed.** Branch 3 now uses `subject.assume_acted`, set to 1 by the scan and 0 by `ClaimPlaylistSend` (Decision 1). V2 now proves agreement through a real claim, evaluation and send (ARS-005 §11). | Architect | No |
| Does veto branch 3 deliberately fire on the receiving run's own `acted` transition? | **Open question** for the send-guard owner (Decision 1). Any narrowing must keep `assume_acted` aligned through the shared text. | Send-guard owner (youtube-authorization) | No |
| **F2** (critic): `absent` was read from the adapter's parsed list, which silently drops unparseable items. A returned video could get a final, false outcome. | **Fixed.** `absent` comes from the raw response item ids. A returned but unparseable item is `unusable` and released unchanged. A chunk with an item missing its id yields no `absent` (Decision 6). Test D7 added. | Architect, then Implementer | No |
| **F3** (critic): under amended AC14, one non-quota provider failure makes a run claim and release the whole backlog (about 70,000 membership rows and transactions), and a stop of that run hands them all off. | **Decided 2026-11-11: variant K.** BR-035 AC14 is unchanged and ARS-005 §5.2 stays as specified. The user accepted the churn as a trade-off (Decision 5). Variant S is kept only as a rejected alternative in ARS-005 Appendix A. | User (decided) | No |
| **F4** (critic, pre-existing): a claim rewrites `retry_run_id` on pending and retrying rows, which erases the evidence veto branch 4 reads. An ended version-0 run in `action_execution` whose retry items get reclaimed could then lose its block. | Not caused by this design. For stranded candidates, the branch-4 rows are inactive and are never rewritten (Decision 1). Run 14 is in phase `discovery`, so branch 4 does not apply to it. **Does not apply (2026-11-11).** The production check, relayed in the assignment and not re-run by the architect, found that the version-0 runs in `action_execution` are runs 17 and 21, with 0 open retry rows ([BR-037](../../../requirements/yuhaul/br-037-requeue-stranded-videos.md), Derived from). The *blocked* definition and the claim are unchanged. Revisit only if a version-0 run in `action_execution` gains an active retry row, which current code cannot create. | — | No |
| The video-search design (ARS-001 §1.2) planned to map `failed` + `code=video_unavailable` to `unavailable_on_youtube`. | **This design is current.** The category is the stored `final_outcome`, mapped by identity. `failed`/`video_unavailable` (other retried videos) must stay `failed`. | Search architect | No |
| The Deferred view shows `requeued_by_user` for an item that a later run deferred again for quota. `AcknowledgeRetryDeferralReceiptTx` keeps the item's original reason. Meanwhile the run's outcome says `deferred`/`quota_exhausted`. | **Flagged.** The item reason says why the item was created. The Designer decides whether the row also shows the latest deferral reason (from the video's latest run outcome). If the reason itself must change, that is a Requirements decision (BR-013 AC4) and a change to the acknowledgement. | Designer, Requirements | No |
| Legacy rows with a future `retry_after` could be claimed ahead of rows that became eligible earlier (Decision 5). | One-off read-only count before release. If it is non-zero, revisit the claim order. | Implementer | No |
| User questions on Errors-queue entries, removed videos, the count of committed deferred or failed videos, videos YouTube no longer returns, reserved quota, and paging. | **Closed 2026-11-11.** Realized in Decisions 2, 6, 8 and 9 and in the Summary. | — | No |
| Other retried videos that YouTube no longer returns, and playlist additions refused because the video is gone. | Still the requirements index's open, non-blocking question. They keep existing retention. | User | No |
| A video that becomes available again after `unavailable_on_youtube`. | Final by default; nothing reprocesses it. | User | No |
| Decision 6 applies to every retried video. It spends about 1 unit per 50 retried videos with expired metadata. | Decided: BR-032 AC2–3 forbid the false rejection for all of them. | — | No |
| Rolling back the binary after `unavailable_on_youtube` rows exist is unsafe (Decision 4 release note). | Release note. | Release owner | No |
| Freshly discovered videos still route on empty metadata when it cannot be read. | Outside BR-037; existing behavior. | Requirements / Architect | No |
| Timing targets (phase B under 2 s; preview under 30 s) are estimates. | Test P1 measures them. If they are missed, escalate before release; do not split the transaction. | Implementer | Blocks release if missed |
| An uncertain `executed_action` row without its guard cannot arise. | The report follows the veto, and a test seeds the invariant. | — | No |
| `selected_video_count` reflects membership at capture time. | Existing approximation (ARD-001). | Run outcome diagnostics | No |

## Revisions

**2026-11-11, requirement amendments.** The specifier amended BR-037 (AC4, AC11, AC13, AC17–19), BR-035 AC14 and BR-032 AC1. Changes:

- Decision 6 now ends a re-queued video that YouTube no longer returns as `unavailable_on_youtube`.
- Decisions 8 and 9 were added.
- Decision 2 gained the Errors sub-count.
- Decision 4 gained the outcome schema step and `run.videos_unavailable`.
- The user questions were closed.

**2026-11-11, critic review `ses_e33a11010ffe4XP0Rmw0ZVtQBs` (CHANGES REQUIRED).**

- **F1:** branch 3 is evaluated at send time with `assume_acted`. V2 runs a real claim, evaluation and send. The question for the send-guard owner is recorded.
- **F2:** `absent` is taken from the raw response ids, and a returned but unparseable item is `unusable` and released. Test D7 was added.
- **F3:** variants K and S were prepared. K stays specified until the user decides. *(Superseded: variant K decided 2026-11-11; see the reconciliation below.)*
- **Minor changes:**
  - The eligible-since order, the ARS-003 handoff amendment and the H1(b) change were withdrawn. The existing created-at order is kept, with a limit and the same-run exclusion, plus a revisit trigger.
  - Migration 026 owns both indexes and lands first.
  - The widening skip is per table, with a no-rollback release note.
  - The `WithRetryTx` wording was corrected.
  - `UnavailableReason` gained `shutting_down`.
  - The stale-reason display was flagged for the Designer.
- **F4** (pre-existing) and the search identity mapping were recorded as findings.

**2026-11-11, reconciliation (Plan revision 19, Loom Action b820f6ca).**

- **F3** is decided as variant K by the user; BR-035 AC14 is unchanged. Decision 5 and Findings are updated. Variant S is kept only as a rejected alternative.
- **F4** does not apply: the production check found 0 open retry rows for version-0 runs 17 and 21.
- **Decision 9** gains page jumps (`page=n`, `page=last`), exact positions and page counts, `queue_total`, and the server-side `reason` filter, as the Designer's paged Deferred view needs. Next and Previous stay keyset.

## Revisit triggers

- Any writer of `run.status`, `playlist_send_guard`, `executed_action`, `route_decision` or `video` processing state that does not hold `productionMu` (rule 4).
- Any code that sets `retry_after` to a future time, makes a row eligible later than its creation, or changes how released items keep their place. Claim order must then follow eligibility, not `created_at` (Decision 5).
- The legacy future-`retry_after` count is non-zero.
- The send-guard owner narrows or changes veto branch 3 (keep `assume_acted` aligned).
- More than one process or writer.
- Measured phase B over 2 seconds, or measured metadata spend that is material next to additions.
- A guard released by any path other than `ResolvePlaylistSendTx`.
- A persistently `unusable` video shows up in the logs.
- The requirements change BR-035 AC14 again (F3 was decided as variant K), or the user extends `unavailable_on_youtube` beyond re-queued videos.
- The deferred page jump or count (Decision 9) measures over 200 ms at 70,000 items: then add an index on `deferred_item(status, deferral_reason, id)` in a later migration.
