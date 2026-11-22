---
type: specification
title: ARS-001 — Playlist Capacity Schema, Waiting Queue, Send Path, Evicted-Add Outcome, APIs and Tests
description: Exact contract for capacity ARD-001. Covers the deferred_item rebuild and its storage guards, enrollment with oldest-first eviction, the derived decision pin, the count read and local room bounds, the lifecycle gate and fences, dispositions, the BR-041 AC7 evicted-add aggregate on every entry point, queue and run inspection, registrations, amendments other owners apply, and the conformance tests.
tags: [architecture, specification, playlist, capacity, deferred-queue, eviction, send-guard, quota, migration, api]
timestamp: 2026-11-22T00:00:00Z
---

**Status:** draft, revision 2 (2026-11-22). Revision 1 was written under Plan r30 node `cap-ard-update`, ahead of the planned order. Critic `ses_dff707369ffe3UAV2Ns6K4abw0` returned CHANGES REQUIRED; this revision addresses its findings 1–3 and the minor findings, and writes finding 4 as two selectable options (§4.2), because the choice belongs to the user. Do not rely on this ARS, or re-slice implementation from it, until the user selects the §4.2 option and `cap-ars-critique` passes on this revision. Not implemented. Section numbers here are the references used by ARD-001.

**Changes in revision 2.** §3.3 and §4.7: permission is checked per target, with a typed per-target eviction refusal and a first-match release table. §3.5: every writer that assumed one active row per video. §3.6: every `ErrWorkRemoved` call site and what `ErrWorkEvicted` does there. §4.2: room bounds count each possible add once and exclude the invocation's own guard; finding 4 is offered as **Option A** and **Option B**. §4.6: the insufficient-evidence outcome and retention. §2: two lookup indexes. §7.3: no production read in simulations. §12: items for the requirements owner.

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
| Local spend, bounds, freshness, restart; count each possible add once | §4.2 (Option A or B, user decision) | K5–K13 |
| Insufficient evidence keeps work under existing rules (BR-040 AC1) | §4.6 *Insufficient evidence* | N1–N4 |
| Lifecycle gate; both GETs and the POST fenced | §4.3 | F1–F5 |
| Live check before capacity (BR-042 AC13) | §4.4 | L1–L3 |
| Admitted full refusal recognized narrowly | §4.5 | R1–R3 |
| Disposition and accounting table | §4.6 | D1–D6 |
| Target-scoped row, cap, oldest-first eviction, non-resurrection | §2, §3.1 | Q1–Q8 |
| Claim versus authority; evicted member terminal | §3.2–3.3 | Q9–Q11 |
| One-active-row writers ignore capacity rows; settlement releases capacity members (ARD-001 *Mixed work*, *Video-scoped uniqueness*) | §3.5 | W1–W8 |
| Per-target send permission; eviction refusal and guard release | §4.7, §3.6 | T1–T9 |
| Derived decision pin; B under the original decision | §3.4 | P1–P5 |
| Evicted-add aggregate (BR-041 AC7) on every entry point | §6 | A1–A9 |
| Stop, Discard, failure sweep, startup | §6.3 | A6–A7 |
| BR-037 scan and confirm | §6.4 (no predicate change) | S1–S2 |
| Queue reads, eviction history, Discard and Retry on capacity rows | §5 | H1–H6 |
| Run inspection and statistics | §7 | I1–I4 |
| Migration and compatibility | §2 | M1–M5 |
| Simulations isolated | §7.3 | I5 |

## 1. Vocabulary and registrations

| Name | Value | Where |
|---|---|---|
| Queue reason | `playlist_full` | `deferred_item.deferral_reason`; only on capacity rows |
| Queue status | `evicted` | `deferred_item.status`; terminal; only on capacity rows |
| Action reason | `playlist_full` | `executed_action.outcome_reason`, with outcome `deferred` (§4.6) |
| Action reason | `capacity_evicted` | `executed_action.outcome_reason`, with outcome `not_attempted` only (§6.2, §4.7) |
| Action reason | `capacity_unconfirmed` | `executed_action.outcome_reason`, with outcome `not_attempted` only: a usable read that could not establish room (§4.6) |
| Video and run-video reason | `playlist_full` | `video.deferred_reason`, `run_video_outcome.deferred_reason` |
| Video and run-video reason | `missing_add_evicted` | the same two columns; only from §6.2 |
| Diagnostic code | `capacity_evidence_unavailable` | phase `action_execution`, operation `check_playlist_capacity`; no status, reason or SQLite fact |
| Operation | `check_playlist_capacity` | safe-diagnostic operation set |

- **Capacity row** means `deferred_item.target_playlist_id IS NOT NULL`. **Video-scoped row** means `target_playlist_id IS NULL`. This is the `<video-scoped>` predicate that Live ARS-001 §6.1 step 5 leaves to this specification.
- **Ordinary target** of a pinned decision: a required target with no capacity row in any status. **Capacity target**: a required target with a capacity row in some status.
- **Ordinary remaining work** of a pinned video: its ordinary targets without a production success. `ordinaryRemainingTx(tx, videoID) ([]string, error)` returns them; for a video that is not decision-pinned it returns `nil, errNotPinned` and callers keep today's behavior.
- **Typed errors** (package `queue`): `ErrWorkEvicted` (sentinel) and `*EvictedTargetError{VideoID, TargetPlaylistID, RowID}`, whose `Is(ErrWorkEvicted)` is true. Neither satisfies `errors.Is(err, ErrWorkRemoved)` or `ErrWorkNotOwned`.
- `internal/queue/models.go`: add `DeferredReasonPlaylistFull` and `DeferredStatusEvicted`, and accept them in `Valid`. `HandOffUnfinishedTx` and `InsertRequeuedTx` never write either.
- `internal/diag/diag.go` `GuardDisposition`: `action_reason` gains `playlist_full`, `capacity_evicted` and `capacity_unconfirmed`; `video_reason` gains `playlist_full` and `missing_add_evicted`; `queue_reason` gains `playlist_full`. The operation and code are registered as above.
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

-- capacity: room bounds look up history by playlist (§4.2); without these, each check scans
CREATE INDEX executed_action_target_success ON executed_action(target_playlist_id, video_id)
    WHERE action_type = 'playlist_insert' AND outcome = 'succeeded';
CREATE INDEX playlist_send_guard_target ON playlist_send_guard(target_playlist_id, video_id);
```

Notes:

- **Lookup indexes.** `executed_action` has no index on `target_playlist_id`, and `playlist_send_guard`'s primary key starts with `video_id`. §4.2 reads both by playlist on every room check, so the migration adds the two partial and plain indexes above. They change no data. Keep room-check reads short so they stay inside the QS-001 shared-SQLite boundary.
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

### 3.3 Receipt validation (receipt level)

`ValidateRetryReceiptTx` (`internal/queue/retry.go:46`) re-reads each member by ID, as today. Changes:

1. **`evicted` is terminal.** Today the member loop treats only `discarded`/`completed` (Deferred) and `dismissed`/`resolved` (Errors) as terminal; any other non-`retrying` status lands in `NotOwned`, so an evicted member would make the whole receipt `ErrWorkNotOwned`. Add `evicted` to the Deferred terminal set, recorded in a new `result.Evicted` list (not `result.Removed`).
2. **Per-member scope.** `RetryReceiptValidation` gains, for each live Deferred member, `TargetPlaylistID` and `RouteDecisionID` (both empty for a video-scoped row). The receipt value and its `031` provenance are unchanged.
3. **No live member left.** Replace the final `ErrWorkRemoved` (`retry.go:151`) with: if `result.Removed` contains any `discarded` or `dismissed` member, or a member row is missing, return `ErrWorkRemoved` (user removal wins); otherwise, if `result.Evicted` is non-empty, return `ErrWorkEvicted`; otherwise (only `completed`/`resolved`) return `ErrWorkRemoved` as today.
4. `NotOwned`, unexpected live siblings and cross-queue ownership keep their existing `ErrWorkNotOwned` results and take precedence over (3).

Receipt-level validation answers "may this run still settle this video". Whether a **target** may be sent is the per-target check of §4.7.

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

### 3.5 Writers that assumed one active row per video (critic finding 1)

Until now every active Deferred row was video-scoped, so several writers read "any active row for the video" as "this video's retained work". Capacity rows break that. Two rules apply to every function below:

- **Sink checks ignore capacity rows.** Anything that decides whether a video already has a video-scoped retained row, or that creates one, considers only `target_playlist_id IS NULL`. A capacity row is never a sink for ordinary work and never suppresses one.
- **Settlement releases live capacity members unchanged.** Whenever a settlement completes, resolves or requeues the receipt's members, a live capacity member goes back to `pending` with ID, `created_at`, reason, target and decision unchanged (`retry_run_id` may be cleared or kept as the function already does). The exceptions: its own target succeeded (§4.6: completed in the success transaction), or §6.2 runs (no live capacity member can remain then). Completing or resolving a capacity member for any other reason is a defect.

| Function | Today | Change |
|---|---|---|
| `queue.Store.CreateDeferredItemTx` (`internal/queue/store.go:185`) | `INSERT ... WHERE NOT EXISTS (active row for video)`; returns `LastInsertId` even when zero rows were inserted | Refuse `deferral_reason = playlist_full` (only `EnrollCapacityTx` creates capacity rows). The `NOT EXISTS` checks only video-scoped rows (`AND target_playlist_id IS NULL`). Return the inserted ID, or the existing active video-scoped row's ID when the insert was suppressed; never a stale `LastInsertId`. |
| Guarded quota/auth handoff closure (`internal/pipeline/pipeline.go:997–1013`) | Creates a sink only when `len(owned.Deferred) == 0` | Create the sink when the receipt has no live **video-scoped** Deferred member and the video has ordinary remaining work (`ordinaryRemainingTx`) or is not pinned. A receipt that owns only capacity rows plus Errors rows therefore still gets B's sink. When the deferred target is itself a capacity target, the capacity row is its retention; no sink. Treat `ErrWorkEvicted` per §3.6. |
| `retainGuardedDeferralTx` (`internal/pipeline/executor.go:565`) | Returns `ErrWorkRemoved` when no live member; retains every owned Deferred member | Apply only to video-scoped members; capacity members are released by the handoff above. `ErrWorkEvicted` per §3.6. |
| Retained-work proof (`internal/pipeline/retry.go:390`) | `EXISTS (active deferred row for video)` | Retained means: an active video-scoped row exists, **or** the video has no ordinary remaining work and every unsettled target has an active capacity row. Any other state is "deferred without retained work" (existing error). |
| `queue.HandOffUnfinishedTx` step 2 (`internal/queue/handoff.go:92`) | Skips a video with any `pending` row | Skip only when a pending **video-scoped** row exists, or when the video has no ordinary remaining work (its live capacity rows, released in step 1, are its retention). A video whose targets satisfy §6.1 gets §6.2 instead (§6.3). Step 1's release of capacity members to `pending` is already correct. |
| `queue.Store.FinishRetryFailureTx` (`internal/queue/retry.go:864`) | Completes every owned Deferred member | Complete only video-scoped members; release live capacity members to `pending` unchanged. The runtime Errors sink is still created. `ErrWorkEvicted` per §3.6. |
| `acknowledgeRetryReceiptTx` (`internal/pipeline/retry.go:577`) | Completes every owned Deferred member | Complete video-scoped members; release live capacity members unchanged. (Under §6.2 there is none.) |
| `queue.Store.AcknowledgeRetryGroupTx` / `finishRetryGroupTx` (`internal/queue/retry.go:678`), used by `retainAuthorityFailure` (`internal/pipeline/authority.go:294`) | Completes all owner-matched Deferred siblings | Same as `acknowledgeRetryReceiptTx`: capacity siblings back to `pending` unchanged. |
| `queue.Store.CloseRetryReceiptFinalTx` (`internal/queue/retry.go:570`) | Closes owned rows with `closed_reason='final_outcome'` | A final video closes capacity rows too (`completed`, `final_outcome`); allowed by the trigger because `completed` is the target state. A pinned video with an evicted target cannot become accepted, so this path never meets an evicted member in practice; if it does, `ErrWorkEvicted` per §3.6. |
| `AcknowledgeRetryDeferralReceiptTx`, `RequeueRetryReceiptTx` (`internal/queue/retry.go:618, 653`) | Return members to `pending` | Correct for capacity members; no change. |
| `deferRetryForInputsTx` and the authorization-stop settlement (run-initiation ARS-005 §6.2) | Create a sink "when the receipt has no owned Deferred row" | Read as "no owned **video-scoped** Deferred row". Capacity members are released unchanged. |
| Live `liveSkip` (Live ARS-001 §6.1) | Already uses the video-scoped predicate | No change; `<video-scoped>` is `target_playlist_id IS NULL`. |

### 3.6 `ErrWorkEvicted` at every `ErrWorkRemoved` site (critic finding 2)

`ErrWorkEvicted` means "no live source remains for this work, and the reason is eviction, not the user". Three rules:

- It never ends a run and never maps to `retry_work_removed` or a 409.
- If the video satisfies §6.1, run §6.2.
- Otherwise a retention path may create the **video-scoped** sink it would create for fresh work, because nothing the user removed is being recreated. It never revives, replaces or re-enrolls the evicted capacity row; the triggers refuse that anyway.

| Site (non-test, `8d7e009`) | Handling of `ErrWorkEvicted` |
|---|---|
| `queue/retry.go:54` receiving run missing | Not eviction; unchanged. |
| `queue/retry.go:151` end of `ValidateRetryReceiptTx` | Produces it (§3.3 rule 3). |
| `queue/retry.go:570` `CloseRetryReceiptFinalTx` | Nothing live to close: return it; callers treat it as "nothing to close", like `ErrWorkRemoved`. |
| `queue/retry.go:791` `RetryGroupReceiptForRunVideo` | Unchanged (no live owned row means no receipt). Startup recovery then settles the member by §6.3 using §6.1 without a receipt. |
| `queue/retry.go:933, 939` `RetainDeferredForRetryTx` | Add `evicted` → return `&EvictedTargetError{...}` (today it falls through to `ErrWorkNotOwned`). Only video-scoped members reach this function after §3.5, so this is a guard. |
| `queue/retry.go:959, 965` `retainOwnedErrorTx` | Errors rows only; unchanged. |
| `queue/handlers.go:103, 280` manual retry handlers | `ErrWorkEvicted` must not reach them. If it does, it is a defect and answers the existing `500`; it is never the `retry_work_removed` 409 (test H6). |
| `pipeline/retry.go:222` `withIntegrityCode` | No code: eviction is not run-ending. |
| `pipeline/retry.go:251` `isRetryAdmissionRefusal` | Unchanged; `ErrWorkEvicted` is deliberately absent. The executor handles `*EvictedTargetError` before this check (§4.7). |
| `pipeline/retry.go:358` `processRetryVideo` failure disposition | Before `committedRetryFailureTx`: on `ErrWorkEvicted`, run §6.2 if §6.1 holds; otherwise commit the failure disposition with a video-scoped runtime Errors sink and no source writes. |
| `pipeline/retry.go:500, 508` `settleFinalRetryVideo` | Treat like `ErrWorkRemoved`: nothing to acknowledge, return nil. |
| `pipeline/pipeline.go:1003` guarded handoff | §3.5 row 2: create the video-scoped sink if ordinary remaining work exists; if §6.1 holds, return a typed result that routes to §6.2 instead. |
| `pipeline/pipeline.go:1348` pre-processing receipt validation | Today any non-refusal error is fatal (`validate retry video %d`). On `ErrWorkEvicted`, skip dispatch for the video and settle it by §6.3. |
| `pipeline/executor.go:573` `retainGuardedDeferralTx` | Same as the guarded handoff. |
| `pipeline/executor.go:900` authorization Errors lookup | Errors rows only; unchanged. |
| `queue/handoff.go` `claimedWorkRemoved` | Checks only `discarded`/`dismissed`; eviction is correctly not removal. No change. |

## 4. Send path (packages `youtube`, `pipeline`, `state`, `auth`)

### 4.1 `CountPlaylistItems(ctx, binding, playlistID) (CountResult, error)`

One `GET /youtube/v3/playlists?id=<playlistID>&part=contentDetails&fields=items(id,contentDetails(itemCount)),nextPageToken` through the existing read adapter, which owns `playlists_list` accounting (1 unit, [authorization ADR-003](../../youtube-authorization/decisions/adr-003-read-adapter-owns-actual-call-accounting.md)). `CountResult{Count uint32, ObservedAt time.Time, Binding}` is returned only when: HTTP 200; exactly one item; `items[0].id` equals `playlistID` byte for byte; `contentDetails.itemCount` present, a JSON integer, `>= 0`; no `nextPageToken`; read accounting confirmed. Every other shape returns `ErrCapacityEvidence` with no count. HTTP and quota errors keep their existing typed meanings. A missing item is `ErrCapacityEvidence`, not zero and not a missing video. `ObservedAt` is sampled when the response is received. One credential-recovery retry after an admitted 401 is allowed and is a separate admitted call (§4.3).

### 4.2 Room bounds (critic findings 3 and 4)

`RoomObservation{PlaylistID, Binding, RunID, Pass, Count, ObservedAt, CoveredVideos}` lives in the executor for one live execution pass. It is never persisted or reused across passes, runs, restarts or bindings. `CoveredVideos` is used only by Option B.

**Count each possible add once.** For the candidate add `(V0, P)` by invocation I, classify every other video `v ≠ V0` that has, for playlist P, a production success, an unresolved `playlist_send_guard`, or an admission in this pass. Each `v` takes the **first** matching class:

| Class | Which `v` | Lower bound | Upper bound |
|---|---|---|---|
| `confirmed_post` | admitted in this pass **after** `ObservedAt`, success recorded | +1 | +1 |
| `in_flight` | admitted in this pass after `ObservedAt`, no result yet, or application unknown | 0 | +1 |
| `covered` (**Option B only**) | `v ∈ CoveredVideos` | 0 | 0 |
| `uncovered` | any other production success | 0 | +1 |
| `guarded_other` | any other unresolved guard | 0 | +1 |

- `lower = Count + |confirmed_post|`; `upper = lower + |in_flight| + |uncovered| + |guarded_other|`, arithmetic capped at 5,000.
- **The invocation's own guard is excluded.** `(V0, P)` is the add being decided. Its guard is committed before POST admission (authorization ARS-001), so it must not count against itself. `V0` has no production success for P (the success veto runs first).
- An unsettled admission counts **only** in the upper bound, never in the lower. It moves to `confirmed_post` when its success commits. It leaves the classes when its non-admission or non-application is positively known.
- **Full:** `lower >= 5000`. **Room:** `upper <= 4999`. **Otherwise:** insufficient evidence (§4.6).
- An observation authorizes a send only while `now - ObservedAt <= 60s` (technical maximum, not a setting) and only in its pass. After that, the next eligible add needs a new count.
- A definite full refusal (§4.5) marks P full for the rest of the pass.
- Queries: one indexed read of successes and one of guards by `target_playlist_id` (§2 indexes), inside the admission writer. Pass-local admissions come from executor memory.

Examples: a usable 4,999 with no history gives `lower = upper = 4999`: room, and the last slot can be used. Admit it and record success: `lower = 5000`: full for the rest of that observation. A raw 5,000 is full with no allowances. 4,998 plus one other unresolved guard gives `upper = 4999`: room for one.

**Finding 4: which successes a later count covers — USER DECISION PENDING.** The two options differ only in the `covered` class. Implementers must not choose; the dispatcher records the user's selection here, and `cap-ars-critique` checks whichever is selected.

- **Option A — no success is ever covered (current ARD-001 text).** `covered` is empty. Every production success for P that is not `confirmed_post` stays `uncovered` forever, across passes, runs and restarts, including successes recorded before the migration.
  - *Effect.* YuHaul's own adds are normally already inside `Count`, so they are counted twice in `upper`. With `S` YuHaul successes on P and `E` other items, `Count ≈ S + E`, and room needs `2S + E <= 4999`. With `E = 0`, YuHaul stops adding to a playlist after about **2,499** of its own adds, although YouTube allows 5,000. A playlist already past that at upgrade gets no further adds from the first run onward. Every later add to it takes the insufficient-evidence branch.
  - *What it protects.* YuHaul never relies on YouTube's count being consistent with an add it confirmed.
- **Option B — a count received after a recorded success covers it.** When the count GET for P is admitted (under the gate and the SQLite writer, §4.3), snapshot `CoveredVideos` = the videos with a production success for P committed at that moment. That success's response was received before the GET was sent, so the count was received after it. Those videos are `covered`. A success recorded later is `confirmed_post` or `uncovered`. Unresolved guards are never covered.
  - *Effect.* Room needs `Count + |in_flight| + |uncovered| + |guarded_other| <= 4999`, so the full 5,000 is usable except for adds with unknown application.
  - *Backstop.* If YouTube's count lags an add it already confirmed, YuHaul can send while the playlist is actually full. YouTube refuses it with `403 playlistContainsMaximumNumberOfVideos`. §4.5 recognizes that refusal: one attempted add, 50 units, a truthful `deferred / playlist_full` with its admission time, guard released, add enrolled to wait. YouTube enforces the limit, so the playlist never exceeds 5,000. The cost is one 50-unit attempt per lagging count. How often YouTube's count lags is not known; there has been no live provider test.
  - *Requirement impact.* This reads BR-040 AC1's "account for YuHaul's own adds" as satisfied by a count received after the add's confirmation. That is a reinterpretation of a MUST, so it needs the user.

### 4.3 Lifecycle gate

`capacityGate` is one process-local mutex owned by the pipeline. Order: **capacityGate → auth authority mutex → quota admission mutex → SQLite writer**. Under it, for each count GET (initial and the one recovery retry) and for each POST:

1. production receiving run `running`, no Stop marker (`stop_requested_at IS NULL`);
2. the per-target permission check of §4.7 passes for this exact target (for a count GET, for the capacity target being checked);
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
| Insufficient evidence (a usable count, but §4.2 cannot establish room or full) | No POST, no enrollment on this basis. Same under Option A and Option B; the options change only how often it happens. See *Insufficient evidence* below. |
| `ErrCapacityEvidence` (bad response shape) or a typed read error | No POST, no enrollment. HTTP/quota/auth/rate errors keep their existing typed handling (for example quota → `deferred / quota_exhausted`). A bad shape is handled like insufficient evidence, with the same diagnostic. A capacity row already waiting is released unchanged in every case (BR-041 AC6). |
| Application unknown | Existing uncertainty: guard kept, `mutation_application_uncertain`. No enrollment as a substitute. An existing capacity row for the target stays as it is. |
| Success | Existing success plus owned guard deletion. If the target's capacity row is live and owned, complete it in the same transaction. If it was evicted after admission, leave it evicted; the success stands. |

**Insufficient evidence: outcome and retention.** BR-040 AC1 forbids treating the count as empty or full and requires "the applicable existing failure or deferral rules". Applied per target:

| Target | Action row in this run | Retention | Video outcome when this is the only incompleteness |
|---|---|---|---|
| Has a live capacity row in this receipt (already waiting) | `not_attempted / capacity_unconfirmed`, no `executed_at` | The capacity row returns to `pending` unchanged: same ID, age and cap count (BR-041 AC6). | `deferred / playlist_full`. The add is still waiting on capacity, and the row is its retention. The next ordinary pass reconsiders it: one count GET per playlist observation, not per video. |
| No capacity row (first time this add is checked) | `not_attempted / capacity_unconfirmed`, no `executed_at` | No capacity row. The existing failure retention writes one video-scoped runtime Errors entry, diagnostic `diag:v1 phase=action_execution operation=check_playlist_capacity code=capacity_evidence_unavailable`: `FinishRetryFailureTx` for retry-origin work, the existing native failure retention otherwise. | `failed`, with the same diagnostic. Errors entries are retried only by the user, so the read is not repeated automatically for this video. |

- Other targets of the same video proceed independently (BR-040 AC2). If several causes apply, the existing precedence decides, with failure over deferral.
- *Option A:* on a playlist past about 2,499 YuHaul adds, every new matching video takes the second row. Each costs one 1-unit read and creates one Errors entry. A user retry fails the same way.
- *Option B:* the second row arises only while other adds have unknown application or the count is unusable. It clears once those guards resolve.

`ActionsExecuted` keeps its existing rule and adds one case: a `deferred / playlist_full` row with a non-NULL `executed_at` (an admitted refusal) counts once, and not in `ActionsFailed`. A `playlist_full` row with NULL `executed_at` and every `capacity_evicted` row never count.

### 4.7 Per-target permission and the eviction refusal (critic finding 2)

Receipt-level validity (§3.3) is necessary but not enough: `ValidateRetryReceiptTx` passes while any one member is live, so a live capacity row for A would otherwise authorize B. Permission is therefore checked **per target**, by `queue.Store.ValidateRetryTargetTx(ctx, tx, receipt, targetPlaylistID)`.

**Signature change.** `state.PlaylistRetryAuthority` (`internal/state/playlist_guard.go:29`) becomes `func(ctx, tx, runID, videoID int64, targetPlaylistID string) error`. `ClaimPlaylistSend` passes `c.TargetPlaylistID`. The executor's `validateReceipt` closure (`internal/pipeline/executor.go:767`) calls `ValidateRetryTargetTx` with that target. The admission scope (`executor.go:1035`) and `resolveAfterWriterUnavailable` (`executor.go:1110`) pass `guard.TargetPlaylistID`. One copied receipt still serves both fences.

**Rules, under the writer, first match wins:**

1. Receiving run missing, not unfinished production, malformed provenance, conflicting owner or unexpected live sibling → the existing typed error (`ErrWorkRemoved` for a missing run, `ErrWorkNotOwned`, `ErrRetryOwnership`, `ErrRetryProvenance`).
2. The pair `(video, target)` has an `evicted` capacity row → `&EvictedTargetError{VideoID, TargetPlaylistID, RowID}`. This holds whether or not that row is in the receipt, and whatever happened to the receipt's other members.
3. The pair has a `discarded` capacity row → `ErrWorkRemoved`. (Not reachable while §5.3 refuses Discard on capacity rows.)
4. **Capacity target:** the receipt must contain a live, owned capacity member for exactly this target → pass. Otherwise → `ErrWorkNotOwned`. The row is pending and owned elsewhere, or completed; a completed row's success is caught by the success veto first.
5. **Ordinary target:** the receipt must contain at least one live, owned **non-capacity** member (a video-scoped Deferred row or any Errors row) → pass. Otherwise, if a non-capacity member was `discarded` or `dismissed` → `ErrWorkRemoved`; otherwise → `ErrWorkNotOwned`. A capacity-only receipt therefore never authorizes an ordinary target.

Native (non-retry) sends have no receipt. `ClaimPlaylistSend` still applies rules 2 and 3 to them, before the success check, so no origin can send an evicted pair.

**Where each result goes, first match wins.** This table holds at guard preparation (`ClaimPlaylistSend`), at final POST admission (`admissionScope`), and in the writer-not-acquired release (`resolveAfterWriterUnavailable`). Those last two get here through `refuseAtAdmission`.

| # | Observed at the fence | Guard | Evidence and continuation |
|---|---|---|---|
| 1 | Stop marker (`ErrPlaylistSendStopped`) | Existing Stop path: own guard released through `releaseUnadmittedClaim` | Existing typed Stop. Stop owns run ending. |
| 2 | `*EvictedTargetError` | At preparation: no guard was created. At admission: release **only this invocation's own guard**, in one detached bounded writer transaction (`runFinalizationContext`). That transaction (a) re-reads the guard and requires the full tuple to equal the invocation's receipt, and (b) re-reads the pair's `evicted` row. It does **not** re-run the fence (that would refuse again), the Stop check or sibling checks: the fence refusal already proves non-admission, and the evicted pair can never be sent again (trigger), so releasing cannot cause a duplicate POST. If both hold: `ResolvePlaylistSendTx(not_attempted, reason capacity_evicted, executed_at NULL)`. If the tuple changed: keep the guard and report a local integrity failure. If the transaction cannot begin or commit: keep the guard (residual, as in authorization ARS-001). | Write `not_attempted / capacity_evicted` (in the release transaction, or on its own at preparation). Not a run-ending refusal: the executor continues with the video's other targets, and §6.3 decides the video at the end of the pass. Not `isRetryAdmissionRefusal`, not `retry_work_removed`. |
| 3 | `ErrWorkRemoved`, `ErrWorkNotOwned`, `ErrRetryOwnership`, `ErrRetryProvenance` | Kept (existing rule: no release on the basis of loss) | Existing `retryAdmissionRefusal` routing and run ending, unchanged. |
| 4 | Writer not acquired (`ErrAdmissionWriterUnavailable`) | Existing release in `resolveAfterWriterUnavailable`. Its recheck now calls the per-target validator; a recheck result of row 2 goes to row 2, rows 1 and 3 to their own paths. | Existing typed local storage fault. |
| 5 | Changed guard, other storage or integrity failure | Kept | Existing local failure. |

Ordering examples:

- B's row discarded **and** A evicted. A's fence matches row 2: A's own guard is released with `capacity_evicted`. B's fence matches row 3: B's guard is kept and the run ends through the user-removal path. AC7 does not apply, because the user removed B.
- A evicted while B is live (test A3). A matches row 2 and B passes rule 5. B is sent, and §6.2 runs at the end of the pass.
- Stop marked and A evicted at the same check. `CheckPlaylistAdmissionTx` checks Stop before the receipt validator runs, so row 1 applies. The guard is released either way.

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
| `*EvictedTargetError` at a fence | §4.7 row 2 (own guard released, `not_attempted / capacity_evicted`). The pass continues with the remaining targets, and §6.2 runs at the end if §6.1 holds. |
| `ErrWorkEvicted` at disposition or pre-processing validation (no live member left) | Run §6.2 if §6.1 holds; otherwise §3.6 (a video-scoped sink where ordinary work remains, never a revived capacity row). |
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

Dry run, Replay and Backtest make no count read, enrollment, eviction, guard change or capacity disposition. They do not read or display production capacity rows, eviction history or room bounds either. Revision 1 let them show production capacity state as current local context; the design asks for no such read, so it is removed. Their existing hypothetical evaluation and isolation rules are unchanged (BR-040 AC4).

## 8. Amendments other owners apply

This specification cannot edit these documents. Each owner records the cross-reference:

| Document | Amendment |
|---|---|
| Authorization ARS-001, writer-validation table and fence outcomes | `evicted` is a terminal member state (§3.3). Permission is per target (§4.7 rules), and `PlaylistRetryAuthority` takes the target. `*EvictedTargetError` is a distinct per-target refusal: its own unissued guard is released (§4.7 row 2), unlike removal. The writer-not-acquired recheck uses the per-target validator. Settlement and failure transactions release live capacity members unchanged (§3.5). |
| Run-initiation ARS-005 §6.2 | `deferRetryForInputsTx` and the authorization-stop settlement create a sink only when there is no owned **video-scoped** Deferred row (§3.5). |
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
5a. No ordinary target is sent on the authority of a capacity member alone (§4.7 rule 5).
5b. No settlement completes or resolves a live capacity member except through its own target's success (§3.5).
5c. In §4.2 each other video counts at most once, an unsettled admission only in the upper bound, and the invocation's own guard never.
6. A decision-pinned video has exactly one decision run, or nothing is sent.
7. §6.2 never writes accepted or any final outcome and never creates a queue row.

## 10. Tests

Use the real pipeline, shared auth and ledger, migrated SQLite, registered HTTP and the shipped client, with a controlled YouTube endpoint that counts requests. No sleeps; pause with channels. Fixture-only classification is not proof of provider behavior.

**Count and bounds.**

- K1: count shapes — 0 versus missing, null, negative, float, string, two items, wrong ID, `nextPageToken`, empty items, 404, 403 quota, 401 then recovery. Only valid shapes give a count; none becomes 0 or 5,000.
- K2: the count GET is one `playlists_list` row of 1 unit; the recovery GET is a second row.
- K3: `ListPlaylists` (picker) is never used for counts.
- K4: no `playlistItems.list` call on any capacity path.
- K5: usable 4,999, no history, own guard prepared: room (the own guard does not count); one POST. Then one success: the same observation cannot authorize a second add (two routes, same playlist).
- K6: raw 5,000 is full with no allowances; 4,998 plus one unresolved guard allows one add.
- K7 (option-dependent): 4,999 observation, one recorded success, then a new count of 4,999 after expiry, in a new run and after reopen. **Option A:** neither send nor enrollment; insufficient evidence (§4.6). **Option B:** the success was committed before the new count GET's admission, so it is covered: room, one POST.
- K8: observation older than 60 s refused at admission.
- K9: guard replaced by durable success: no double count.
- K10: two admissions to P after `ObservedAt`, both held before their responses: each counts once, in the upper bound only; with `Count = 4997` a third add is still room (`upper = 4999`), and `lower` stays 4,997 (no false full).
- K11: one held admission's response is lost (application unknown): it stays `in_flight` (upper only) and is never `confirmed_post`.
- K12: a video with both an old success and an unresolved guard on P counts once.
- K13 (Option B only): a success committed after the count GET was admitted is not covered; a guard is never covered even when older than the count. **Option A:** pre-migration successes count in `upper` on the first run after upgrade (playlist with 2,500 recorded YuHaul successes and `Count = 2500`: insufficient evidence).

**Fences.** F1–F2: pause before final quota admission of the initial GET and of the recovery GET; Stop first gives zero GETs (resp. only the first); admission first gives exactly one accounted GET and no later admission. F3: Stop marker before cancellation is decisive. F4: Discard of a video-scoped sibling, eviction of the capacity row, and Stop each win before POST admission: zero POSTs. F5: admission wins, then eviction: exactly one POST; the result is recorded; the row stays evicted.

**Live.** L1: live video against a full playlist: zero count GETs, no row, cap unchanged. L2: capacity-only receipt for an unconfirmed video: zero GETs and POSTs; row keeps ID and age; one active video-scoped item. L3: evict that capacity row: the video still has its active video-scoped item.

**Refusal.** R1: exact 403 `playlistContainsMaximumNumberOfVideos`: one attempt, 50 units `permanent`, guard deleted, enrolled, no immediate repeat. R2: 403 with another reason, two reasons, malformed body, body-read failure, redirect: uncertain; guard kept. R3: refusal after the row was evicted: composition rolls back; guard kept; refusal recorded. *This keeps a guard on an add YouTube definitely refused, which leaves permanent uncertainty and blocks §6.2 for the video. OC-007 G2 requires it today; see §12 item 3.*

**Dispositions.** D1: Study full, Lofi room: Study `deferred/playlist_full` no time, Lofi success, video `deferred/playlist_full`. D2: admitted refusal has `executed_at`; no-send has NULL; `ActionsExecuted` counts only the first. D3: quota refusal of the count: existing quota deferral, capacity row age unchanged. D4: application unknown: no enrollment. D5: success completes the owned capacity row. D6: accounting failure after a real call: no new room, no erased success.

**Insufficient evidence (§4.6).** N1: an already-waiting capacity row meets an insufficient read: `not_attempted / capacity_unconfirmed`, row back to `pending` with the same ID and age, video `deferred / playlist_full`, one count GET for the playlist per observation. N2: a first-time target meets an insufficient read: no POST, no capacity row, `not_attempted / capacity_unconfirmed`, one video-scoped runtime Errors entry with `capacity_evidence_unavailable`, video `failed`; no automatic re-read in later runs. N3: a sibling target with room in the same video is still sent (BR-040 AC2). N4: a bad count shape behaves as N1/N2; a quota-refused count keeps `deferred / quota_exhausted` and the capacity row's age.

**Writers (§3.5).**

- W1: A full, then B quota-refused, same video and run, no prior success. Exactly one active video-scoped `quota_exhausted` row and A's capacity row; the next run claims both, sends B under the original decision and rechecks A. Run it both as native work and as a receipt that owns only A's capacity row plus an Errors row for B.
- W2: B fails with provider 500 while A is still full. `FinishRetryFailureTx` creates the runtime Errors sink and completes only video-scoped members. A's capacity row is `pending` with the same ID, `created_at` and cap count.
- W3: crash after A's enrollment commit and before B's disposition. Then reopen, startup recovery, a marked Stop, and the failure sweep: each leaves B a video-scoped row (or B's original row released) and A's capacity row unchanged. Because B keeps its own row, BR-037's `already_queued` for the video is true for B's work, not only for A's; there is nothing for Re-queue to rescue.
- W4: `CreateDeferredItemTx` with an existing active capacity row inserts the video-scoped row; with an existing active video-scoped row returns that row's ID, never a stale `LastInsertId`; refuses `playlist_full`.
- W5: `HandOffUnfinishedTx` on a member whose only remaining work is A's waiting add creates no video-scoped row; with B's ordinary work remaining, it creates exactly one.
- W6: `retainAuthorityFailure` and `AcknowledgeRetryGroupTx` release A's capacity sibling unchanged.
- W7: the retained-work proof (`retry.go:390`) accepts a capacity-only deferral and refuses a deferred outcome whose only active row is a capacity row for a different target.
- W8: authorization-stop settlement and `deferRetryForInputsTx` with a receipt owning only a capacity row create one video-scoped `authorization_pending` sink and release the capacity row.

**Per-target permission (§4.7).**

- T1: B's video-scoped row discarded, A's capacity row live: B's guard preparation and B's final admission each refuse with `ErrWorkRemoved`; zero B POSTs. Repeat at final admission after the guard is prepared: the guard is kept.
- T2: capacity-only receipt for A tries an ordinary target B: `ErrWorkNotOwned`, zero POSTs.
- T3: A evicted while B is live (also A3): A refused with `*EvictedTargetError` at preparation (no guard) and, separately, at final admission (own guard released with `not_attempted / capacity_evicted`); B sent; the run continues.
- T4: A evicted **and** B discarded: A's guard released, B's guard kept, run ends by the user-removal path; no `retry_work_removed` for A.
- T5: Stop marked and A evicted at the same check: Stop path.
- T6: eviction release transaction finds a changed guard tuple: guard kept, local integrity failure. Release transaction blocked: guard kept.
- T7: writer-not-acquired at A's admission with A evicted meanwhile: the recheck routes to §4.7 row 2.
- T8: native send (no receipt) of an evicted pair: refused at `ClaimPlaylistSend` before the success check.
- T9: `ErrWorkEvicted` at each §3.6 site: pre-processing validation, failure disposition, guarded handoff and `settleFinalRetryVideo` each settle without ending the run; none produces a 409.

**Queue.** Q1: 4,999 + 1 = 5,000, no eviction. Q2: 5,001st distinct evicts the lowest ID, including when tied `created_at`. Q3: repeat of an active pair: no eviction, same ID and age. Q4: claimed oldest is evicted; the claim cannot send. Q5: two concurrent admissions: one order of effects, never 5,001 active. Q6: failure injected between eviction and insert: neither survives reopen. Q7: trigger refusals — re-enroll an evicted pair, update an evicted row's status, change a capacity row's target or decision, insert a capacity row as `retrying`. Q8: other-reason rows are never counted or evicted. Q9: receipt with a live B and an evicted A validates B only. Q10: receipt with only evicted members returns `ErrWorkEvicted`, not `ErrWorkRemoved` and not `ErrWorkNotOwned`; with one evicted and one discarded member it returns `ErrWorkRemoved`. Q11: provenance stays version 1 and byte-identical across release and eviction.

**Pin.** P1: no success, capacity row under run 40, current routes edited: continuation uses run 40; zero route evaluations. P2: capacity rows naming two decision runs: `original_decision_unavailable`, no send. P3: substituted decision ID at `ClaimPlaylistSend`: refused. P4: rediscovery of a pinned video by another run: dropped by `RoutableByRun`. P5: A's capacity row cannot authorize B; B's ordinary row cannot authorize A.

**Aggregate (BR-041 AC7).**

- A1: the example above: one POST to B; zero requests for A; `not_attempted/capacity_evicted` for A; run video and video `deferred/missing_add_evicted`; source completed; no new Deferred or Errors row; A's row unchanged; never accepted or final.
- A2: reopen: unchanged. Later scheduled and Run Now runs and Re-queue stranded videos send nothing and create nothing.
- A3: A evicted while claimed by this run: typed refusal, own guard released with `not_attempted/capacity_evicted`, same aggregate, manual retry returns its success response.
- A4: B quota-refused in the same run: existing quota deferral, not the aggregate.
- A5: unresolved guard on A: uncertainty, not the aggregate.
- A6: Stop committed after B's success and before the aggregate: aggregate committed locally; no `stopped_by_user` row.
- A7: run-failure sweep and startup recovery at the same point: same result.
- A8: all targets evicted, none succeeded, video claimed through a video-scoped row: aggregate with zero successes. *Derived beyond AC7's literal trigger; see §12 item 2.*
- A10: B succeeded in an earlier run; this run claims A's waiting row, and A is evicted before admission: §4.7 row 2, then the aggregate, with no success row in this run. *Same §12 item 2.*
- A9: a live report for V: `liveSkip`, not the aggregate; A unchanged.

**BR-037.** S1: preview with a live capacity row (`already_queued`), an evicted-only partly done video after the aggregate (`retained_without_queue_item` only), and an evicted row as the most recent item (not `removed_by_user`). S2: confirm inserts no capacity row and never revives an evicted pair.

**HTTP.** H1: `include=evicted` pages with its own totals and cursors. H2: capacity fields on capacity items only; name fallback. H3: Discard on a capacity row: `409 capacity_discard_unavailable`, nothing changed. H4: Retry on an evicted row refused. H5: zero YouTube calls on every queue and run read. H6: `ErrWorkEvicted` injected into a manual retry handler answers 500, never the `retry_work_removed` 409.

**Inspection.** I1: Run Detail and Decision Detail show the AC7 explanation from run rows only, for a historical run, after the queue rows changed. I2: unsafe values injected at write and read are withheld. I3: `missing_add_evicted` is deferred in counts and filters. I4: `ActionsExecuted` and `ActionsFailed` per §4.6. I5: dry run, Replay, Backtest: zero count reads, zero capacity writes, and no read of production capacity rows or eviction history.

**Migration.** M1: clean install. M2: upgrade a production-shaped database from the then-production schema (at least `030`) through every new migration: IDs, `sqlite_sequence`, reasons, statuses, `closed_reason`, `retry_run_id`, Live's columns, indexes and foreign keys intact; `integrity_check` ok. M3: `028` triggers, `029` search index, `030` rows and `031` provenance unchanged. M4: legacy rows are video-scoped with NULL target. M5: record what an older binary does against the new schema; the upgrade notes say older writers must not run after the migration.

## 11. Limits

- No live provider probe; YouTube's count consistency is not proven, and the design does not depend on it beyond §4.2.
- Under Option A, uncovered successes make room impossible to establish after about 2,499 YuHaul adds to a playlist (§4.2). Under Option B, the 403 refusal is the backstop when YouTube's count lags.
- Queue Discard on capacity rows is refused until ARD-001 open question 1 is decided.
- Migration content is written against `031`; the rebase adds intervening columns by the rule in §2.
- The §4.2 option is not selected. Implementation of §4.2 and §4.6 waits for the user's choice.

## 12. Items for other owners

1. **§4.2 Option A or B (user, through the dispatcher; blocks §4.2 and §4.6 implementation).** The numbers to put to the user are in §4.2: Option A stops adds after about 2,499 YuHaul adds per playlist, including for playlists already past that at upgrade. Option B uses the full 5,000 and relies on YouTube's 403 refusal, at 50 units per attempt, when the count lags.
2. **Derived AC7 cases (requirements owner; not blocking).** Two cases go beyond BR-041 AC7's literal trigger ("records success for every other missing add"): **A8**, every add evicted and none succeeded; and **A10**, the other add succeeded in an earlier run, so this run records no success. This ARS gives both `deferred / missing_add_evicted` (OC-002 G1: exactly one outcome; G5: deferred is the only non-final fit). Please record them in BR-041 as derived consequences. The designer needs copy without "Added to B" for both. A related case gets no new outcome: if A's row is evicted while **pending** (no run holds it) and B already succeeded, no run processes the video again. Its last committed outcome (`deferred / playlist_full`) stays, because BR-041 AC3 says eviction writes no new video outcome; the queue's eviction history is the only new fact. Please confirm that is intended.
3. **R3: admitted refusal after eviction (requirements owner; not blocking).** OC-007 G2 lets a definite refusal release the guard only together with a valid retained-work handoff. After eviction there is no sink, so the guard stays and the video shows permanent "application unknown" for an add YouTube definitely refused. That also blocks §6.2. A possible amendment: a definite refusal of an evicted pair may release the guard with `deferred / playlist_full` evidence and no sink, because the pair is permanently suppressed. Until the owner decides, R3 is kept as specified.
4. **Discard on capacity rows** (ARD-001 open question 1) and **Retry copy** (open question 2) are unchanged.
