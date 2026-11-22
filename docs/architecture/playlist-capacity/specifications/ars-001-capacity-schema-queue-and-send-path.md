---
type: specification
title: ARS-001 — Playlist Capacity Schema, Waiting Queue, Send Path, Evicted-Add Outcome, APIs and Tests
description: Exact contract for capacity ARD-001. Covers the deferred_item rebuild and its storage guards, enrollment with oldest-first eviction, the derived decision pin, the count read and local room bounds, the lifecycle gate and fences, dispositions, the BR-041 AC7 evicted-add aggregate on every entry point, queue and run inspection, registrations, amendments other owners apply, and the conformance tests.
tags: [architecture, specification, playlist, capacity, deferred-queue, eviction, send-guard, quota, migration, api]
timestamp: 2026-11-22T00:00:00Z
---

**Status:** draft. Written 2026-11-22 under Plan r30 node `cap-ard-update`, ahead of the planned order: the plan puts this ARS after `cap-ard-critique`. Do not rely on it, or re-slice implementation from it, until the [ARD-001](../design/ard-001-exact-target-capacity-and-waiting-work.md) critique and this ARS's own critique (`cap-ars-critique`) pass. Not implemented. Section numbers here are the references used by ARD-001.

## What this specifies, with one example

Video V was evaluated in run 40. Its decision adds V to playlists A and B. In run 40, A was full and B was refused for quota. So V has a capacity row for A (`playlist_full`, target A, decision run 40) and an ordinary quota row. Later, A's waiting list reaches 5,000, and the next distinct enrollment for A evicts V's row. Run 47 claims V's quota row. V is decision-pinned to run 40, so run 47 does not re-evaluate it. The live gate records `none`. Run 47's planner drops A (evicted) and sends only B, which succeeds. Run 47 then writes `not_attempted / capacity_evicted` for A, sets V to `deferred / missing_add_evicted`, and completes the quota row without creating a new one. Run Detail shows "Added to B" and "The add to A was removed from the queue and won't be retried automatically" (designer copy). Run 47 never reads A's count. Nothing ever sends A again.

## Derived from

- [ARD-001](../design/ard-001-exact-target-capacity-and-waiting-work.md), revision of 2026-11-22.
- [BR-040](../../../requirements/yuhaul/br-040-respect-youtube-playlist-capacity.md), [BR-041](../../../requirements/yuhaul/br-041-bound-playlist-full-waiting-work.md) AC1–AC7, [OC-007](../../../requirements/yuhaul/oc-007-playlist-capacity-deferral-handoff.md), [OC-002](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) Guarantees 1 and 5 (amended 2026-11-19), [BR-042](../../../requirements/yuhaul/br-042-skip-live-and-upcoming-videos-until-ended.md) AC13.
- Contracts on `main` at `8d7e009`: [authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md), [run-initiation ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §2, §5.1.1 and §6–7, [ARS-006](../../run-initiation/specifications/ars-006-final-outcomes-are-never-reprocessed.md) §2 and §7, [ARS-003](../../run-initiation/specifications/ars-003-stop-run-and-deferred-handoff.md) §2.1, [Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §6–8, [safe-diagnostic ARS-001](../../run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md).
- Source read at `8d7e009`: migrations `001`–`031`, `internal/queue/models.go`, `internal/queue/retry.go`, `internal/queue/handlers.go`, `internal/state/final_outcome.go`, `internal/diag/diag.go`. Names below that do not exist yet are required shapes; Go spelling is free unless stated.

## Satisfies

Every ARD-001 obligation, with its mechanism and test. Tests are in §10.

| ARD-001 obligation | Mechanism | Tests |
|---|---|---|
| Exact count evidence; no fallback count | §4.1 `CountPlaylistItems` | K1–K4 |
| Local spend, bounds, freshness, restart | §4.2 `RoomObservation`, `roomBounds` | K5–K9 |
| Lifecycle gate; both GETs and the POST fenced | §4.3 | F1–F5 |
| Live check before capacity (BR-042 AC13) | §4.4 | L1–L3 |
| Admitted full refusal recognized narrowly | §4.5 | R1–R3 |
| Disposition and accounting table | §4.6 | D1–D6 |
| Target-scoped row, cap, oldest-first eviction, non-resurrection | §2, §3.1 | Q1–Q8 |
| Claim versus authority; evicted member terminal | §3.2–3.3 | Q9–Q11 |
| Derived decision pin; B under the original decision | §3.4 | P1–P5 |
| Evicted-add aggregate (BR-041 AC7) on every entry point | §6 | A1–A9 |
| Stop, Discard, failure sweep, startup | §6.3 | A6–A7 |
| BR-037 scan and confirm | §6.4 (no predicate change) | S1–S2 |
| Queue reads, eviction history, Discard and Retry on capacity rows | §5 | H1–H5 |
| Run inspection and statistics | §7 | I1–I4 |
| Migration and compatibility | §2 | M1–M5 |
| Simulations isolated | §7.3 | I5 |

## 1. Vocabulary and registrations

| Name | Value | Where |
|---|---|---|
| Queue reason | `playlist_full` | `deferred_item.deferral_reason`; only on capacity rows |
| Queue status | `evicted` | `deferred_item.status`; terminal; only on capacity rows |
| Action reason | `playlist_full` | `executed_action.outcome_reason`, with outcome `deferred` (§4.6) |
| Action reason | `capacity_evicted` | `executed_action.outcome_reason`, with outcome `not_attempted` only (§6.2) |
| Video and run-video reason | `playlist_full` | `video.deferred_reason`, `run_video_outcome.deferred_reason` |
| Video and run-video reason | `missing_add_evicted` | the same two columns; only from §6.2 |
| Diagnostic code | `capacity_evidence_unavailable` | phase `action_execution`, operation `check_playlist_capacity`; no status, reason or SQLite fact |
| Operation | `check_playlist_capacity` | safe-diagnostic operation set |

- **Capacity row** means `deferred_item.target_playlist_id IS NOT NULL`. **Video-scoped row** means `target_playlist_id IS NULL`. This is the `<video-scoped>` predicate that Live ARS-001 §6.1 step 5 leaves to this specification.
- `internal/queue/models.go`: add `DeferredReasonPlaylistFull` and `DeferredStatusEvicted`, and accept them in `Valid`. `HandOffUnfinishedTx` and `InsertRequeuedTx` never write either.
- `internal/diag/diag.go` `GuardDisposition`: `action_reason` gains `playlist_full` and `capacity_evicted`; `video_reason` gains `playlist_full` and `missing_add_evicted`; `queue_reason` gains `playlist_full`. The operation and code are registered as above.
- `web/src/types.ts` and `web/src/lib/outcome.ts` gain the same values in the same change. The designer owns the labels.
- Producers and readers change together. Unknown stored values are withheld under the safe-diagnostic contract.

## 2. Migration

**Number.** Assigned by Plan phase `migrations`: the PR takes max+1 on `main` when it rebases for merge, after PR-L's live-status migration. Never edit `001`–`031`. No down file, like `029`–`031`; rollback is backup restore (`docs/user-docs/upgrade-notes.md`).

**Content against `main` at `031`.** One golang-migrate transaction. At rebase, the rebuilt table is the then-current `deferred_item` plus exactly the additions marked `-- capacity`. Columns, CHECK values, indexes and foreign keys that intervening migrations add (expected: Live's `live_hold` column and `live_or_upcoming` reason; Discovery's `discovery_processing_pending` reason; any last-retry-run column) are carried unchanged. A rebased migration that drops any of them fails M2.

```sql
CREATE TABLE deferred_item_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    deferral_reason TEXT NOT NULL CHECK(deferral_reason IN ('quota_exhausted', 'api_rate_limited', 'authorization_pending', 'stopped_by_user', 'discarded_by_user', 'requeued_by_user', 'playlist_full')), -- capacity: playlist_full
    retry_after TIMESTAMP,
    retry_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK(status IN ('pending', 'retrying', 'completed', 'discarded', 'evicted')), -- capacity: evicted
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    retry_run_id INTEGER REFERENCES run(id) ON DELETE SET NULL,
    closed_reason TEXT CHECK (closed_reason IS NULL OR closed_reason = 'final_outcome'),
    target_playlist_id TEXT CHECK (target_playlist_id IS NULL OR length(target_playlist_id) BETWEEN 1 AND 64),   -- capacity
    route_decision_id INTEGER REFERENCES route_decision(id) ON DELETE NO ACTION,                                  -- capacity
    evicted_at TIMESTAMP,                                                                                         -- capacity
    CHECK ((deferral_reason = 'playlist_full') = (target_playlist_id IS NOT NULL)),                               -- capacity
    CHECK ((target_playlist_id IS NULL) = (route_decision_id IS NULL)),                                           -- capacity
    CHECK ((status = 'evicted') = (evicted_at IS NOT NULL)),                                                      -- capacity
    CHECK (status <> 'evicted' OR (target_playlist_id IS NOT NULL AND closed_reason IS NULL))                     -- capacity
);
INSERT INTO deferred_item_new (id, video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at, retry_run_id, closed_reason)
SELECT id, video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at, retry_run_id, closed_reason FROM deferred_item;
DELETE FROM sqlite_sequence WHERE name = 'deferred_item_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'deferred_item_new', seq FROM sqlite_sequence WHERE name = 'deferred_item';
DROP TABLE deferred_item;
ALTER TABLE deferred_item_new RENAME TO deferred_item;
CREATE INDEX IF NOT EXISTS idx_deferred_item_status ON deferred_item(status);
CREATE INDEX deferred_item_retry_owner ON deferred_item(retry_run_id, status, video_id);
CREATE INDEX deferred_item_video_status ON deferred_item(video_id, status);

-- capacity: one active add per actual playlist and video (BR-041 AC1)
CREATE UNIQUE INDEX deferred_item_capacity_active ON deferred_item(target_playlist_id, video_id)
    WHERE target_playlist_id IS NOT NULL AND status IN ('pending', 'retrying');
-- capacity: count and oldest lookup per playlist; id is the settled enrollment order (AC2)
CREATE INDEX deferred_item_capacity_order ON deferred_item(target_playlist_id, id)
    WHERE target_playlist_id IS NOT NULL AND status IN ('pending', 'retrying');
-- capacity: exact-pair history and suppression
CREATE INDEX deferred_item_capacity_pair ON deferred_item(video_id, target_playlist_id, status)
    WHERE target_playlist_id IS NOT NULL;

-- capacity: a pair with eviction or discard history is never enrolled again; enrollment starts pending
CREATE TRIGGER deferred_item_capacity_insert BEFORE INSERT ON deferred_item
WHEN NEW.target_playlist_id IS NOT NULL AND (NEW.status IS NOT 'pending' OR EXISTS (
    SELECT 1 FROM deferred_item h WHERE h.video_id = NEW.video_id
      AND h.target_playlist_id = NEW.target_playlist_id AND h.status IN ('evicted', 'discarded')))
BEGIN SELECT RAISE(ABORT, 'capacity pair cannot be enrolled'); END;

-- capacity: identity is immutable; completed, discarded and evicted are terminal
CREATE TRIGGER deferred_item_capacity_immutable
BEFORE UPDATE OF video_id, run_id, deferral_reason, created_at, target_playlist_id, route_decision_id, status, evicted_at ON deferred_item
WHEN (OLD.target_playlist_id IS NOT NULL OR NEW.target_playlist_id IS NOT NULL) AND (
     NEW.video_id IS NOT OLD.video_id OR NEW.run_id IS NOT OLD.run_id
  OR NEW.deferral_reason IS NOT OLD.deferral_reason OR NEW.created_at IS NOT OLD.created_at
  OR NEW.target_playlist_id IS NOT OLD.target_playlist_id OR NEW.route_decision_id IS NOT OLD.route_decision_id
  OR (OLD.status = 'evicted' AND NEW.evicted_at IS NOT OLD.evicted_at)
  OR (OLD.status IN ('completed', 'discarded', 'evicted') AND NEW.status IS NOT OLD.status))
BEGIN SELECT RAISE(ABORT, 'capacity row identity is immutable'); END;
```

Notes:

- No other table changes. `run_video_outcome.deferred_reason`, `video.deferred_reason` and `executed_action.outcome_reason` are free text vetted in Go, and `executed_action.outcome` already admits `deferred` and `not_attempted`. So `028`'s triggers, `031`'s `retry_claim_provenance`, `030`'s table and `029`'s search triggers are untouched.
- `route_decision_id ... ON DELETE NO ACTION` follows `playlist_send_guard`: deleting the decision's run while a capacity row of another origin run names it fails. That protects the authority the row carries. If run deletion must succeed in that case, that is a revisit trigger, not a silent cascade.
- Legacy rows get `NULL` target, decision and `evicted_at`: all are video-scoped. No backfill.
- The `deferred_item_capacity_immutable` trigger does not list `retry_run_id`, so `ON DELETE SET NULL` from run deletion still works.

## 3. Queue store and claims (package `queue`)

### 3.1 `EnrollCapacityTx(ctx, tx, e CapacityEnrollment) (EnrollResult, error)`

`CapacityEnrollment{VideoID, OriginRunID, TargetPlaylistID, RouteDecisionID, Now}`. The caller holds the `WithRetryTx` writer and the lifecycle gate (§4.3). It runs inside the same transaction as the run/action evidence and any owned guard release of §4.6.

1. Require `RouteDecisionID` to be a `matched` row of this video in a production run whose snapshot has a `playlist_insert` action with exactly `TargetPlaylistID` (the ARS-006 §7.3 snapshot query). Require the decision's run to equal the video's pinned decision run if the video is already pinned (§3.4). Otherwise `ErrCapacityAuthority`.
2. If an active capacity row exists for `(TargetPlaylistID, VideoID)`: return `{Row: id, Existing: true}`. Do not touch it (same ID, age and owner). No eviction (AC1, AC2).
3. If the pair has `evicted` or `discarded` history: return `ErrCapacityPairSuppressed`. Nothing is written. The storage trigger refuses the insert too.
4. `n := SELECT COUNT(*) FROM deferred_item WHERE target_playlist_id = ? AND status IN ('pending','retrying')`.
5. If `n >= 5000`: `victim := SELECT id FROM deferred_item WHERE target_playlist_id = ? AND status IN ('pending','retrying') ORDER BY id LIMIT 1`; `UPDATE deferred_item SET status='evicted', evicted_at=:now WHERE id=:victim AND status IN ('pending','retrying')`; require one row. A claimed victim (`retrying`) is evicted too (AC1: a claim still counts). `retry_run_id` is kept as owner history.
6. `INSERT` the new row: `deferral_reason='playlist_full'`, `status='pending'`, `run_id=OriginRunID`, target, decision, `created_at=:now`, `retry_count=0`, `retry_after=NULL`.
7. Return `{Row, Evicted: victim-or-0}`.

Only this function inserts capacity rows. Repeated evidence for an existing active pair takes step 2. A failure anywhere rolls the whole transaction back, so no partial admission or eviction survives (AC2).

### 3.2 Claim

`ClaimDeferredGroupTx` and `ListDeferredRetryCandidatesForRun` are unchanged: a capacity row is a Deferred row, claimed with its video's group in the existing `created_at, id` order, and written into `retry_claim_provenance` version 1 as `{id, origin_run_id}`. A claimed capacity row is `retrying` and still counts toward the cap. Release (`RequeueRetryReceiptTx`, `AcknowledgeRetryDeferralReceiptTx` returning to pending) keeps ID, `created_at`, reason, target and decision (the trigger enforces it).

### 3.3 Receipt validation and the eviction refusal

`ValidateRetryReceiptTx` re-reads each member by ID, as today. Additions:

- It returns, per live Deferred member, whether it is a capacity row and its `TargetPlaylistID` and `RouteDecisionID` (`RetryReceiptValidation` gains these; no receipt field changes).
- An `evicted` member is terminal, like `completed`. It is never a live retention path, a sink or user removal.
- If no live member remains and every non-live member is `evicted` or `completed` with at least one `evicted`, return **`ErrWorkEvicted`** (`errors.Is(err, ErrWorkRemoved)` is false). If any non-live member is `discarded` (Deferred) or `dismissed` (Errors), the existing `ErrWorkRemoved` rule applies.
- `ErrWorkEvicted` carries the evicted members' IDs and targets.

Pipeline handling of `ErrWorkEvicted` is §6. It never maps to `retry_work_removed`.

### 3.4 Decision pin (package `state`)

```go
// capacityDecision(c): c is a deferred_item alias.
const capacityRow = `c.target_playlist_id IS NOT NULL`

const DecisionPinnedVideo = `(NOT ` + FinalVideo + ` AND (
  EXISTS (SELECT 1 FROM executed_action s WHERE s.video_id = v.id AND ` + prodSuccess + `)
  OR EXISTS (SELECT 1 FROM deferred_item c WHERE c.video_id = v.id AND ` + capacityRow + `)))`
```

- `ClassifyVideoTx` returns `partly_done` for `DecisionPinnedVideo` (today: `PartlyDoneVideo`). The class name stays; its meaning becomes "continues under its original decision".
- `OriginalDecisionTx` step 1 becomes: the distinct runs of `route_decision` rows referenced by production successes of the video **union** those referenced by its capacity rows (`deferred_item.route_decision_id`). Exactly one run, else `ErrOriginalDecisionUnavailable`. Steps 2–3 are unchanged.
- `RoutableByRun` gains `AND NOT EXISTS (SELECT 1 FROM deferred_item c JOIN route_decision crd ON crd.id = c.route_decision_id WHERE c.video_id = v.id AND crd.run_id <> :run)`. A pinned video is never fresh main-loop input of another run. This is what stops rediscovery from re-routing an evicted-only video.
- `ClaimPlaylistSend`'s cross-run decision query (ARS-006 §7.3) replaces its `EXISTS (success under :decisionRun)` and `NOT EXISTS (success under another run)` with the same union: some success or capacity row of the video names `:decisionRun`, and none names another run.
- `continueOriginalDecision` is unchanged in structure. Its target set is §6.1's partition.

## 4. Send path (packages `youtube`, `pipeline`, `state`, `auth`)

### 4.1 `CountPlaylistItems(ctx, binding, playlistID) (CountResult, error)`

One `GET /youtube/v3/playlists?id=<playlistID>&part=contentDetails&fields=items(id,contentDetails(itemCount)),nextPageToken` through the existing read adapter, which owns `playlists_list` accounting (1 unit, [authorization ADR-003](../../youtube-authorization/decisions/adr-003-read-adapter-owns-actual-call-accounting.md)). `CountResult{Count uint32, ObservedAt time.Time, Binding}` is returned only when: HTTP 200; exactly one item; `items[0].id` equals `playlistID` byte for byte; `contentDetails.itemCount` present, a JSON integer, `>= 0`; no `nextPageToken`; read accounting confirmed. Every other shape returns `ErrCapacityEvidence` with no count. HTTP and quota errors keep their existing typed meanings. A missing item is `ErrCapacityEvidence`, not zero and not a missing video. `ObservedAt` is sampled when the response is received. One credential-recovery retry after an admitted 401 is allowed and is a separate admitted call (§4.3).

### 4.2 Room bounds

`RoomObservation{PlaylistID, Binding, RunID, Pass, Count, ObservedAt, LocalAdmits int}` lives in the executor for one live execution pass. It is never persisted or reused across passes, runs, restarts or bindings.

`roomBounds(tx, obs) (lower, upper int)`, with arithmetic capped at 5,000:

- `lower = Count + obs.LocalAdmits` where `LocalAdmits` counts adds to this playlist admitted **after** `ObservedAt` in this pass whose success is recorded or whose application is unknown.
- `upper = lower + U`, where `U` is the number of distinct `(video, playlist)` pairs for this playlist with a production success recorded before this pass or an unresolved `playlist_send_guard`, excluding pairs already counted in `LocalAdmits`, plus unsettled live admissions of this pass.
- Full: `lower >= 5000`. Room: `upper < 5000`. Otherwise insufficient evidence.
- An observation authorizes a send only while `now - ObservedAt <= 60s` (technical maximum, not a setting) and only in its pass. After that, the next eligible add needs a new count.
- Admission debits `LocalAdmits` by one. Positively known non-admission or non-application restores it. A definite full refusal (§4.5) sets this playlist full for the rest of the pass.

### 4.3 Lifecycle gate

`capacityGate` is one process-local mutex owned by the pipeline. Order: **capacityGate → auth authority mutex → quota admission mutex → SQLite writer**. Under it, for each count GET (initial and the one recovery retry) and for each POST:

1. production receiving run `running`, no Stop marker (`stop_requested_at IS NULL`);
2. the live receipt is still valid (§3.3) and, for a capacity target, its row is live and owned;
3. `LiveClearedTx(receiving run, video)` (§4.4);
4. for a POST: the existing guard tuple, exact-target success veto, legacy veto, and room (`roomBounds` says room, observation fresh);
5. convert the provisional quota hold to admitted.

Release the gate before transport. `StopRun` acquires `capacityGate` before `runControl.mu` and its marker write. Every write that can invalidate the checks (queue Discard, eviction in §3.1, ownership transfer, terminal and final writes, the Stop marker) either takes the gate or runs under the production gate that already serializes it with the executor. No caller holding `runControl.mu` or a SQLite writer acquires the gate. No lock spans OAuth exchange, `RoundTrip`, response parsing or outcome accounting. The final POST admission is the authorization ARS-001 scoped admission, run while holding the gate; the writer-not-acquired rule of that ARS applies unchanged.

### 4.4 Live gate (BR-042 AC13)

The planner calls `liveGateClaimed` (Live-only requirement) before the first count GET for any target of the video. A result other than `none` takes `liveSkip` with the receipt (Live ARS-001 §6.1): no GET, no POST, no enrollment, no eviction, capacity rows released unchanged, one active video-scoped item. A video not confirmed `none` is never passed to `EnrollCapacityTx`. The POST fence checks `LiveClearedTx` (§4.3 step 3); its refusal is `liveDeferAfterEvaluation`, not a capacity disposition.

### 4.5 Admitted full refusal

The guarded insert adapter returns the private fact `capacityNotApplied` only for the single admitted POST whose guard and receipt match the sent video and playlist, when: final HTTP status 403 with no redirect; the body is read completely and parses as the documented error envelope; `error.errors` has exactly one element and its `reason` is exactly `playlistContainsMaximumNumberOfVideos`; no competing or duplicate reasons; no transport or body-read error. Anything else is the existing uncertain or failed result. Neither an exported error value, a diagnostic code, a substring nor a later count can produce `capacityNotApplied`.

### 4.6 Dispositions (one writer transaction each)

| Fact | Writes |
|---|---|
| Room proved full (`lower >= 5000`), no POST | `executed_action(outcome='deferred', outcome_reason='playlist_full', executed_at=NULL)` for this run and target; `EnrollCapacityTx`. If the target's own claimed capacity row exists, step 2 finds it and the receipt handoff returns it to `pending` unchanged. Clear only this invocation's positively unissued guard, if one was prepared. Continue other targets. |
| `capacityNotApplied` | `executed_action(outcome='deferred', outcome_reason='playlist_full', executed_at=<admission time>)`; `EnrollCapacityTx`; owned guard deletion; all in one transaction or none. The insert's 50-unit consumption is finalized by the insert owner with category `permanent`. Mark the playlist full for the pass. If enrollment returns `ErrCapacityPairSuppressed` or `ErrWorkEvicted` (no live sink), roll back the composition, keep the guard (OC-007), and record the admitted refusal through the existing failure retention. |
| Insufficient evidence or `ErrCapacityEvidence` | No POST. No enrollment on this basis. Existing quota, auth, rate or failure policy; a capacity row already waiting is released unchanged. Log `capacity_evidence_unavailable`. |
| Application unknown | Existing uncertainty: guard kept, `mutation_application_uncertain`. No enrollment as a substitute. An existing capacity row for the target stays as it is. |
| Success | Existing success plus owned guard deletion. If the target's capacity row is live and owned, complete it in the same transaction. If it was evicted after admission, leave it evicted; the success stands. |

`ActionsExecuted` keeps its existing rule and adds one case: a `deferred / playlist_full` row with a non-NULL `executed_at` (an admitted refusal) counts once, and not in `ActionsFailed`. A `playlist_full` row with NULL `executed_at` and every `capacity_evicted` row never count.

## 5. HTTP queue contract

### 5.1 `GET /api/deferred`

- `include` gains `evicted`: `<filter>` = `status = 'evicted'`. Paging, cursors, positions and jumps are those of ARS-005 §7 over that filter. `queue_total` and `by_reason` use the same filter.
- `reason=playlist_full` is a valid filter for `pending`, `all` and `evicted`.
- Each item gains, only on capacity rows: `target_playlist_id`, `target_playlist_name` (local display data from the decision's snapshot, else omitted so the SPA falls back to the ID), `route_decision_id`, `decision_run_id`, and `evicted_at` when evicted. Video-scoped items are unchanged.
- No count, name or status is read from YouTube.

### 5.2 Retry on a capacity row

`POST /api/deferred/{id}/retry` is unchanged: it claims the video's group. The capacity target in it is rechecked under §4. An `evicted` row answers the existing not-retryable response, because it is not `pending`. The designer owns the copy (ARD-001 open question 2).

### 5.3 Discard on a capacity row — interim

`POST /api/deferred/{id}/discard` on a capacity row answers `409 {"error": <designer copy>, "code": "capacity_discard_unavailable", "item": <current item>}` and changes nothing. Video-scoped rows are unchanged. This is the interim of ARD-001 open question 1. If the requirements owner instead extends AC7 to a discarded add, this refusal is removed and §6.1 gains that state.

### 5.4 Errors

Read failure is `500`, never an empty page. Bad `include` stays `400`.

## 6. Evicted-add aggregate (BR-041 AC7)

### 6.1 Target partition

For video V with pinned decision run D, the required targets are the `playlist_insert` targets of D's `matched` route decisions in D's production snapshot (`decisionAuthority`). For each target t:

```sql
-- :v, :t
SELECT
  EXISTS (SELECT 1 FROM executed_action s WHERE s.video_id = :v AND s.target_playlist_id = :t AND <prodSuccess>) AS succeeded,
  EXISTS (SELECT 1 FROM deferred_item c WHERE c.video_id = :v AND c.target_playlist_id = :t AND c.status = 'evicted') AS evicted,
  EXISTS (SELECT 1 FROM playlist_send_guard g WHERE g.video_id = :v AND g.target_playlist_id = :t AND g.action_type = 'playlist_insert') AS guarded
```

State: `succeeded` if `succeeded`; else `evicted` if `evicted AND NOT guarded`; else `other`. The planner removes `succeeded` and `evicted` targets before any count GET or guard preparation. So an `evicted` target is never read, enrolled or sent.

### 6.2 `commitEvictedAddTx(ctx, tx, runID, v, receipt)`

Precondition, re-checked inside the transaction: every target is `succeeded` or `evicted`, at least one is `evicted`, V is not final. Then, in one `WithRetryTx`:

1. `ValidateRetryReceiptTx(receipt)` if a receipt exists. `ErrWorkEvicted` is acceptable here; `ErrWorkRemoved`, `ErrWorkNotOwned` and integrity errors roll back.
2. For each `evicted` target with no `executed_action` row for `(runID, V, t)`: insert `outcome='not_attempted'`, `outcome_reason='capacity_evicted'`, `executed_at=NULL`, `route_decision_id` = D's matched decision for t.
3. `TransitionRunVideoStateTx` to `deferred`, `FinalOutcomeDeferred`, reason `missing_add_evicted`, on an allowed edge from the current state. The global video becomes `processing_state='deferred'`, `final_outcome='deferred'`, `deferred_reason='missing_add_evicted'`.
4. `acknowledgeRetryReceiptTx(receipt, owned)`: owned Deferred rows `completed`, owned Errors rows `resolved`, `closed_reason` NULL. Evicted rows untouched. **No sink is created.**
5. Recompute run counters.

After commit, broadcast `EventRunVideo` with outcome `deferred`. It writes no route decision, guard, Errors entry or new Deferred row, and makes no provider call.

### 6.3 Entry points

| Entry | When it calls §6.2 |
|---|---|
| `continueOriginalDecision` end of pass | After every target has settled in this pass and §6.1 holds. Otherwise the existing classification applies. |
| `ErrWorkEvicted` at a fence (§3.3) | The invocation releases its own unissued guard by the detached owned resolution used for Stop (`ResolvePlaylistSendTx(not_attempted, reason capacity_evicted, no executed_at)` after re-running the fence checks with the same guard and receipt). Then the pass continues with the remaining targets, and §6.2 runs at the end if §6.1 holds. If the release transaction cannot commit, the guard stays (existing residual). |
| `ErrWorkEvicted` at disposition (no live member left) | Run §6.2 if §6.1 holds; else leave the existing classification with no sink. |
| Stop handoff, run Discard, run-failure sweep, startup recovery | Before handing off an unfinished member, compute §6.1 for it. If it holds, run §6.2 instead of the handoff. This is local and needs no provider call. |

Manual retry: when the run ends normally after §6.2, `POST /api/deferred/{id}/retry` and `POST /api/errors/{id}/retry` return their existing success response. Eviction never produces `retry_work_removed`.

### 6.4 BR-037 (no predicate change)

ARS-005 §2–4 are unchanged. Consequences, checked by S1–S2: a live capacity row makes its video `already_queued`; an evicted row is not `RemovedByUser`; a video after §6.2 is outside `unfinished` (committed `deferred`) and is counted only in `retained_without_queue_item` (BR-037 AC4); `InsertRequeuedTx` writes only video-scoped rows.

## 7. Inspection, statistics, simulations

### 7.1 Run videos

`GET /api/runs/{id}/videos` and the detail read return the existing action rows, now including `playlist_full` deferrals (with or without `executed_at`) and `capacity_evicted` no-attempt rows, each with its exact target. The video's `deferred_reason` `missing_add_evicted` is filterable through the existing outcome filter (`outcome=deferred`). The SPA explains AC7 from these rows only. The designer owns the copy; BR-041 AC7's sentence is the meaning to express, not required text.

### 7.2 Statistics

`ActionsExecuted` per §4.6. Reads, evictions, `capacity_evicted` rows and simulations count nowhere. Route statistics treat `missing_add_evicted` as deferred, never accepted, rejected, excluded or failed (OC-002 G1).

### 7.3 Simulations

Dry run, Replay and Backtest make no count read, enrollment, eviction, guard change or capacity disposition. They show the production capacity state only as current local context, under the designer's labels.

## 8. Amendments other owners apply

This specification cannot edit these documents. Each owner records the cross-reference:

| Document | Amendment |
|---|---|
| Authorization ARS-001, writer-validation table and fence outcomes | `evicted` is a terminal member state; `ErrWorkEvicted` is a distinct refusal whose owned unissued guard is released as in §6.3, unlike removal. |
| ARS-006 §2 and §7.3 | `DecisionPinnedVideo`, the `OriginalDecisionTx` union, the `RoutableByRun` addition and the cross-run decision query change of §3.4. |
| ARS-003 §2 | Stop and Discard call §6.2 for members that satisfy §6.1. |
| Safe-diagnostic ARS-001 | Registrations of §1. |
| Live ARS-001 §6.1 step 5 | `<video-scoped>` is `target_playlist_id IS NULL` (§1). |

## 9. Invariants

1. At most one active capacity row per `(target_playlist_id, video_id)` (unique index).
2. Active capacity rows per playlist never exceed 5,000 after any committed transaction (§3.1 is the only inserter).
3. A pair with `evicted` or `discarded` history is never active again (trigger).
4. A capacity row's identity never changes; `completed`, `discarded` and `evicted` are terminal (trigger).
5. No POST for a target with a live capacity row that is not owned by the sending invocation's receipt, or with eviction history.
6. A decision-pinned video has exactly one decision run, or nothing is sent.
7. §6.2 never writes accepted or any final outcome and never creates a queue row.

## 10. Tests

Use the real pipeline, shared auth and ledger, migrated SQLite, registered HTTP and the shipped client, with a controlled YouTube endpoint that counts requests. No sleeps; pause with channels. Fixture-only classification is not proof of provider behavior.

**Count and bounds.**

- K1: count shapes — 0 versus missing, null, negative, float, string, two items, wrong ID, `nextPageToken`, empty items, 404, 403 quota, 401 then recovery. Only valid shapes give a count; none becomes 0 or 5,000.
- K2: the count GET is one `playlists_list` row of 1 unit; the recovery GET is a second row.
- K3: `ListPlaylists` (picker) is never used for counts.
- K4: no `playlistItems.list` call on any capacity path.
- K5: 4,999 then one success: the same observation cannot authorize a second add (two routes, same playlist).
- K6: raw 5,000 is full with no allowances; 4,998 plus one unresolved guard allows one add.
- K7: 4,999 observation, one recorded success, new count 4,999 after expiry, in a new run and after reopen: neither send nor enrollment; `capacity_evidence_unavailable`.
- K8: observation older than 60 s refused at admission.
- K9: guard replaced by durable success: no double count.

**Fences.** F1–F2: pause before final quota admission of the initial GET and of the recovery GET; Stop first gives zero GETs (resp. only the first); admission first gives exactly one accounted GET and no later admission. F3: Stop marker before cancellation is decisive. F4: Discard of a video-scoped sibling, eviction of the capacity row, and Stop each win before POST admission: zero POSTs. F5: admission wins, then eviction: exactly one POST; the result is recorded; the row stays evicted.

**Live.** L1: live video against a full playlist: zero count GETs, no row, cap unchanged. L2: capacity-only receipt for an unconfirmed video: zero GETs and POSTs; row keeps ID and age; one active video-scoped item. L3: evict that capacity row: the video still has its active video-scoped item.

**Refusal.** R1: exact 403 `playlistContainsMaximumNumberOfVideos`: one attempt, 50 units `permanent`, guard deleted, enrolled, no immediate repeat. R2: 403 with another reason, two reasons, malformed body, body-read failure, redirect: uncertain; guard kept. R3: refusal after the row was evicted: composition rolls back; guard kept; refusal recorded.

**Dispositions.** D1: Study full, Lofi room: Study `deferred/playlist_full` no time, Lofi success, video `deferred/playlist_full`. D2: admitted refusal has `executed_at`; no-send has NULL; `ActionsExecuted` counts only the first. D3: quota refusal of the count: existing quota deferral, capacity row age unchanged. D4: application unknown: no enrollment. D5: success completes the owned capacity row. D6: accounting failure after a real call: no new room, no erased success.

**Queue.** Q1: 4,999 + 1 = 5,000, no eviction. Q2: 5,001st distinct evicts the lowest ID, including when tied `created_at`. Q3: repeat of an active pair: no eviction, same ID and age. Q4: claimed oldest is evicted; the claim cannot send. Q5: two concurrent admissions: one order of effects, never 5,001 active. Q6: failure injected between eviction and insert: neither survives reopen. Q7: trigger refusals — re-enroll an evicted pair, update an evicted row's status, change a capacity row's target or decision, insert a capacity row as `retrying`. Q8: other-reason rows are never counted or evicted. Q9: receipt with a live B and an evicted A validates B only. Q10: receipt with only evicted members returns `ErrWorkEvicted`, not `ErrWorkRemoved`. Q11: provenance stays version 1 and byte-identical across release and eviction.

**Pin.** P1: no success, capacity row under run 40, current routes edited: continuation uses run 40; zero route evaluations. P2: capacity rows naming two decision runs: `original_decision_unavailable`, no send. P3: substituted decision ID at `ClaimPlaylistSend`: refused. P4: rediscovery of a pinned video by another run: dropped by `RoutableByRun`. P5: A's capacity row cannot authorize B; B's ordinary row cannot authorize A.

**Aggregate (BR-041 AC7).**

- A1: the example above: one POST to B; zero requests for A; `not_attempted/capacity_evicted` for A; run video and video `deferred/missing_add_evicted`; source completed; no new Deferred or Errors row; A's row unchanged; never accepted or final.
- A2: reopen: unchanged. Later scheduled and Run Now runs and Re-queue stranded videos send nothing and create nothing.
- A3: A evicted while claimed by this run: typed refusal, own guard released with `not_attempted/capacity_evicted`, same aggregate, manual retry returns its success response.
- A4: B quota-refused in the same run: existing quota deferral, not the aggregate.
- A5: unresolved guard on A: uncertainty, not the aggregate.
- A6: Stop committed after B's success and before the aggregate: aggregate committed locally; no `stopped_by_user` row.
- A7: run-failure sweep and startup recovery at the same point: same result.
- A8: all targets evicted, none succeeded, video claimed through a video-scoped row: aggregate with zero successes.
- A9: a live report for V: `liveSkip`, not the aggregate; A unchanged.

**BR-037.** S1: preview with a live capacity row (`already_queued`), an evicted-only partly done video after the aggregate (`retained_without_queue_item` only), and an evicted row as the most recent item (not `removed_by_user`). S2: confirm inserts no capacity row and never revives an evicted pair.

**HTTP.** H1: `include=evicted` pages with its own totals and cursors. H2: capacity fields on capacity items only; name fallback. H3: Discard on a capacity row: `409 capacity_discard_unavailable`, nothing changed. H4: Retry on an evicted row refused. H5: zero YouTube calls on every queue and run read.

**Inspection.** I1: Run Detail and Decision Detail show the AC7 explanation from run rows only, for a historical run, after the queue rows changed. I2: unsafe values injected at write and read are withheld. I3: `missing_add_evicted` is deferred in counts and filters. I4: `ActionsExecuted` and `ActionsFailed` per §4.6. I5: dry run, Replay, Backtest: zero count reads and zero capacity writes.

**Migration.** M1: clean install. M2: upgrade a production-shaped database from the then-production schema (at least `030`) through every new migration: IDs, `sqlite_sequence`, reasons, statuses, `closed_reason`, `retry_run_id`, Live's columns, indexes and foreign keys intact; `integrity_check` ok. M3: `028` triggers, `029` search index, `030` rows and `031` provenance unchanged. M4: legacy rows are video-scoped with NULL target. M5: record what an older binary does against the new schema; the upgrade notes say older writers must not run after the migration.

## 11. Limits

- No live provider probe; YouTube's count consistency is not proven, and the design does not depend on it beyond §4.2.
- Uncovered successes can make room impossible to establish (ARD-001 *Availability limit*).
- Queue Discard on capacity rows is refused until ARD-001 open question 1 is decided.
- Migration content is written against `031`; the rebase adds intervening columns by the rule in §2.
