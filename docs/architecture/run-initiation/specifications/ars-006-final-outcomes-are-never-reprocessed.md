---
type: specification
title: ARS-006 — Final Outcomes Are Never Reprocessed — Migrations 027–028, Classification, Claim Closing, Main-Loop Filter, Continuation Send, Inspection, Statistics and Tests
description: Exact contract for BR-039. Covers migration 027 (closed_reason) and 028 (immutability triggers, with a down file); the shared final, partly-done and routable predicates; claim results and closing; the single-transaction already-final retry refusal; the production main-loop input filter; the original decision and the status-free decision authority loader; continuation with decisionRunID in the executor, loadActions and ClaimPlaylistSend; continued_actions inspection and run-attributed route statistics; handoff exclusions; the interim hold; delivery order; and the conformance tests.
tags: [architecture, specification, outcomes, lifecycle, retry, deferred-queue, error-queue, send-guard, migration, api, statistics]
timestamp: 2026-11-12T00:00:00Z
---

**Status:** accepted — exact implementation contract, not implemented. It realizes [ADR-007](../decisions/adr-007-final-outcomes-are-never-reprocessed.md) for [BR-039](../../../requirements/yuhaul/br-039-final-outcomes-are-never-reprocessed.md). Revised 2026-11-12 after critic review `ses_e2e6c22cfffeEhvwlTXJINOyx1` (see §11).

It amends:

- [ARS-003](ars-003-stop-run-and-deferred-handoff.md) §2 and §2.1;
- [ARS-005](ars-005-requeue-stranded-videos.md) §5–§6 (ARS-005 §15);
- run video detail and per-run route statistics;
- `ClaimPlaylistSend` in [authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) (§7.3). The send-guard owner reviews §7.3 before it is built.

## What the two sides agree on

- A retry of an entry whose video is final answers `409 {"code":"video_already_final","final_outcome":"<outcome>","entry":{…}}`. The entry is already closed.
- Queue item views carry `closed_reason` (`null` or `"final_outcome"`) and `video_final_outcome` (set when `closed_reason` is set).
- `GET /api/runs/{id}/videos/{videoId}` carries `continued_actions` (an empty array when there are none).
- Per-run route statistics count the actions the run itself attempted.

## Derived from

[BR-039](../../../requirements/yuhaul/br-039-final-outcomes-are-never-reprocessed.md) AC1–AC12; ADR-007; [BR-035](../../../requirements/yuhaul/br-035-stop-active-run.md) AC8, AC14–15; [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md) AC19; [BR-037](../../../requirements/yuhaul/br-037-requeue-stranded-videos.md) AC11, AC13, AC18.

## Satisfies

See ADR-007's Satisfies table. No behavior beyond it.

## 0. Delivery order

1. Predicates (§2), migration 027 (§1.1) and the vocabulary (§1.3).
2. Claim classification and closing (§3), the retry refusal (§4), the handoff exclusions (§6) and the main-loop filter (§5).
3. Continuation (§7) together with the inspection and statistics projection (§8). This ships in the **same release** as step 2. If it cannot, step 2 ships with the interim hold (§3.4), and step 3 follows.
4. Migration 028 (§1.2), in the same release as step 2 or later. **Never before step 2.**
5. ARS-005 step 6 (batched claiming and the metadata precondition), after step 3.

Before release, take three read-only counts:

- final videos with active queue items;
- partly done videos whose production successes reference more than one run;
- `processing_state='completed'` rows whose `final_outcome` is not final.

## 1. Schema

`ApplyMigrations` runs `m.Up()` first and then the Go schema steps (`internal/store/store.go:91–99`). Migrations 027 and 028 therefore run **before** `widenFinalOutcomeChecks` on a database being upgraded from 025. The triggers do not depend on the CHECK, so this order is harmless.

### 1.1 Migration `027_final_outcome_closing.up.sql`

```sql
ALTER TABLE deferred_item ADD COLUMN closed_reason TEXT
  CHECK (closed_reason IS NULL OR closed_reason = 'final_outcome');
ALTER TABLE error_entry ADD COLUMN closed_reason TEXT
  CHECK (closed_reason IS NULL OR closed_reason = 'final_outcome');
```

### 1.2 Migration `028_final_outcome_triggers.up.sql` and `.down.sql`

Up:

```sql
CREATE TRIGGER video_final_outcome_immutable
BEFORE UPDATE OF final_outcome ON video
WHEN OLD.final_outcome IN ('accepted','rejected','excluded','unavailable_on_youtube')
 AND NEW.final_outcome IS NOT OLD.final_outcome
BEGIN SELECT RAISE(ABORT, 'final outcome is immutable'); END;

CREATE TRIGGER run_video_outcome_final_immutable
BEFORE UPDATE OF final_outcome ON run_video_outcome
WHEN OLD.final_outcome IN ('accepted','rejected','excluded','unavailable_on_youtube')
 AND NEW.final_outcome IS NOT OLD.final_outcome
BEGIN SELECT RAISE(ABORT, 'final outcome is immutable'); END;
```

Down:

```sql
DROP TRIGGER IF EXISTS video_final_outcome_immutable;
DROP TRIGGER IF EXISTS run_video_outcome_final_immutable;
```

**Release note.** Before running a binary older than this release, apply the down file's two statements with `sqlite3`. Without them, the older binary's unconditional claim reset aborts on a final video, and every run start fails while any final video has an eligible item. Migration 027's columns need no rollback: older code names its insert columns, and the default is NULL.

### 1.3 Vocabulary

- **Closed items:** a closed deferred item has `status='completed'` and `closed_reason='final_outcome'`. A closed Errors entry has `status='resolved'`, `resolved_at` set, and `closed_reason='final_outcome'`. Nothing else writes `closed_reason`.
- **`queue` package:**
  - `ClosedReasonFinalOutcome = "final_outcome"`;
  - `DeferredItem.ClosedReason` and `ErrorEntry.ClosedReason` (`*string`);
  - `AlreadyFinalError{VideoID, EntryID int64; ErrorQueue bool; Outcome string}`, with `Is(ErrVideoAlreadyFinal)`.
- **`queue` views:** `closed_reason` and `video_final_outcome`, joined from `video` only when `closed_reason` is set.
- **`diag`:** the code `original_decision_unavailable`. It is valid only with phase `action_execution` and operation `load_run`, with no other field.
- **`web/src/types.ts`:** `closed_reason`, `video_final_outcome`, and `RunVideoDetail.continued_actions`.

## 2. Shared predicates (package `state`)

Alias `v` is a `video` row. "Production" means `run_type IN ('manual','scheduled')`.

```go
const FinalOutcomes = `'accepted','rejected','excluded','unavailable_on_youtube'`
const FinalVideo = `(v.final_outcome IN (` + FinalOutcomes + `))`

// productionSuccess(s) — s is an executed_action alias
const prodSuccess = `s.action_type = 'playlist_insert' AND s.outcome = 'succeeded'
  AND EXISTS (SELECT 1 FROM run sr WHERE sr.id = s.run_id AND sr.run_type IN ('manual','scheduled'))`

const PartlyDoneVideo = `(NOT ` + FinalVideo + ` AND EXISTS (SELECT 1 FROM executed_action s
  WHERE s.video_id = v.id AND ` + prodSuccess + `))`

// RoutableByRun: parameter ?run (bind as named or positional by the caller)
const RoutableByRun = `(NOT ` + FinalVideo + `
  AND NOT EXISTS (SELECT 1 FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id
      WHERE s.video_id = v.id AND ` + prodSuccess + ` AND srd.run_id <> :run)
  AND NOT EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = v.id
      AND (d.status = 'pending' OR (d.status = 'retrying' AND (d.retry_run_id IS NULL OR d.retry_run_id <> :run))))
  AND NOT EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = v.id
      AND (e.status = 'active' OR (e.status = 'retrying' AND (e.retry_run_id IS NULL OR e.retry_run_id <> :run)))))`

type VideoClass string // "final" | "partly_done" | "open"
func ClassifyVideoTx(ctx context.Context, q Querier, videoID int64) (VideoClass, VideoFinalOutcome, error)
```

**Original decision.**

```go
type OriginalDecision struct{ RunID int64; Matched []RouteDecisionRef } // {RouteDecisionID, RouteID}, by evaluation_order, id
var ErrOriginalDecisionUnavailable = errors.New("original decision unavailable")
func OriginalDecisionTx(ctx context.Context, q Querier, videoID int64) (OriginalDecision, error)
```

1. `SELECT DISTINCT srd.run_id FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id WHERE s.video_id = ? AND <prodSuccess>`. It must return exactly one row; otherwise return unavailable.
2. R0 must be a production run.
3. `Matched` = R0's `decision='matched'` rows for the video. It must not be empty.

Snapshot readability is checked by `decisionAuthority` (§7.1).

## 3. Claims (package `queue`)

### 3.1 Result

```go
type ClaimResult int // ClaimTaken | ClaimLost | ClaimClosedFinal | ClaimHeld (interim only, §3.4)
```

`claimRetryGroupTx` returns `(RetryGroupReceipt, int64, ClaimResult, error)`. The exported wrappers keep `claimed bool` (`== ClaimTaken`). `ClaimDeferredGroupResultTx` exposes the result.

After the run checks, the entry lookup and `retryOwner`, and before eligibility, membership and updates:

```text
class, outcome := state.ClassifyVideoTx(tx, videoID)
switch class:
case final:
    if owner != 0 && owner != runID: return ClaimLost
    if owner == runID && runOutcome(tx, runID, videoID) == outcome:   // M2
        return ClaimTaken with receipt, no reset, no new membership
    closeFinalFamilyTx(tx, videoID, includeErrors=manual, now); log; return ClaimClosedFinal
case partly_done:
    proceed as today, but the owner==0 reset is:
    UPDATE video SET processing_state='evaluated', final_outcome='not_yet_evaluated',
                     deferred_reason=NULL, error_message=NULL WHERE id=?
case open:
    proceed exactly as today
```

`runOutcome` = `SELECT final_outcome FROM run_video_outcome WHERE run_id=? AND video_id=?`.

### 3.2 `closeFinalFamilyTx(ctx, tx, videoID, includeErrors bool, now) (int, error)`

```sql
UPDATE deferred_item SET status='completed', closed_reason='final_outcome'
WHERE video_id=? AND (status='pending'
   OR (status='retrying' AND (retry_run_id IS NULL OR retry_run_id IN (SELECT id FROM run WHERE status IN ('failed','stopped')))));
-- includeErrors:
UPDATE error_entry SET status='resolved', resolved_at=?, closed_reason='final_outcome'
WHERE video_id=? AND (status='active'
   OR (status='retrying' AND (retry_run_id IS NULL OR retry_run_id IN (SELECT id FROM run WHERE status IN ('failed','stopped')))));
```

`retry_run_id`, `retry_count` and `created_at` are kept. The log line is `queue item closed: video final` (video id, entry id, outcome, row count).

### 3.3 Callers

`createClaimedRunLocked` and the ARS-005 batch claim treat `ClaimClosedFinal` and `ClaimHeld` like `ClaimLost`. `ListDeferredRetryCandidates` may add `AND NOT <FinalVideo>` as an optimization only.

### 3.4 Interim hold (only if §7 is not in the same release)

`partly_done` → `ClaimHeld`. The item is untouched: no membership, no reset, no status change. A warning is logged: `partly done video held until continuation ships` (video id, entry id). A user retry of such an item answers `409 {"code":"continuation_unavailable"}` and changes nothing. The main-loop filter (§5) already drops such videos.

When §7 ships, `ClaimHeld` and `continuation_unavailable` are removed.

## 4. User retry refusal (`pipeline.RetryQueueEntry`) — one transaction

Under `productionMu`, before `auth.Bind`, one `qs.WithRetryTx` runs:

1. Look up the entry (Errors or deferred) and its video, whatever the entry's status (a missing entry → `ErrNotFound`).
2. `retryOwner`. A live other owner → return as today (lost claim, no run).
3. `ClassifyVideoTx`:
   - `final` → `closeFinalFamilyTx(includeErrors=errorQueue)`; record `alreadyFinal`; return nil (commit). No run is created.
   - `partly_done` under the interim hold → record `held`; return nil (commit). Nothing is written.
   - otherwise → create the receiving run and claim, as today.
4. After the commit: `alreadyFinal` → return `&queue.AlreadyFinalError{…}`; `held` → return `queue.ErrContinuationUnavailable`.

`auth.Bind` moves after this transaction, so a refused retry makes no authorization or provider call.

Handlers (`handleRetryError`, `handleRetryDeferred`):

- `ErrVideoAlreadyFinal` → `409 {"error":"this video already has a final outcome; nothing was retried","code":"video_already_final","final_outcome":"<outcome>","entry":<closed row view>}`;
- `ErrContinuationUnavailable` → `409 {"error":"this video is waiting for a later update; nothing was retried","code":"continuation_unavailable"}`.

Everything else is unchanged.

## 5. Main-loop input filter and `processRetryVideo` dispatch (package `pipeline`)

**Filter.** In `executeHeld`, when `p.isProductionRun(run.RunType)`, the `videos` list is filtered once, immediately after `withoutRetryVideos` and before `captureSelectedCount`:

```sql
SELECT v.id FROM video v WHERE v.id IN (<member ids>) AND <state.RoutableByRun>  -- :run = runID
```

Only the kept ids go on to `phaseExclusion`, `filterExcludedVideos`, `phaseDuplicateDetection`, `phaseRouting`, `completeLegacyEvaluation`, `videosWithMatchedDecisions` and `phaseActionExecution`. For a resume entering at action execution, the filter is applied before `videosWithMatchedDecisions`. Dropped ids are logged at debug, with counts by reason at info.

`runExclusionGate`, `detectVideos`, `routeVideos`, `dryrun.go` and `replay.go` are **unchanged**.

**Backstop.** `executeDecision` reads the class. For a `final` video it returns the stored outcome, with no write and no send.

**`processRetryVideo` dispatch** (first, after the receipt):

- `final`:
  - if `runOutcome(runID, videoID)` equals the video's final outcome → `acknowledgeRetryReceiptTx` (items `completed`/`resolved`, `closed_reason` NULL);
  - otherwise, `ValidateRetryReceiptTx` and close the owned rows with `closed_reason='final_outcome'`.

  Return nil in both cases.
- `partly_done` → §7.2.
- `open` → today's path. ARS-005 §6.2's precondition fetches metadata only for `open` videos.

## 6. Handoffs and retention (amends ARS-003 §2)

- `unfinishedRunMembersTx`: add `AND NOT <state.FinalVideo>` (alias `v`) to the outer `WHERE`.
- `retainAuthorityFailure`: skip `final` videos (classify inside the retain transaction), replacing the `completed`-only skip.

## 7. Continuation

### 7.1 `decisionAuthority(ctx, db, cs, decisionRunID) ([]router.Route, error)`

This is `productionAuthority` without the run-status gate:

- the run exists and `run_type IN ('manual','scheduled')`;
- `config_version_id` is non-NULL and `cs.Get` succeeds;
- `validateSnapshotRoutes` passes;
- the same route, condition and action ordering, and the same id-uniqueness checks.

Any failure → `ErrOriginalDecisionUnavailable`. `productionAuthority` itself is unchanged.

### 7.2 `continueOriginalDecision(ctx, runID, v, receipt) error`

1. `od, err := state.OriginalDecisionTx(p.db, v.ID)`, then `routes, err := decisionAuthority(…, od.RunID)`. Also check that every `od.Matched` route id is present in `routes` with at least one `playlist_insert` action with a target.
   - On any `ErrOriginalDecisionUnavailable`, in one `WithRetryTx`: `ValidateRetryReceiptTx`, `FinishRetryFailureTx`, and `recordRetryVideoFailureTx` with `diag:v1 phase=action_execution operation=load_run code=original_decision_unavailable`. No send. Return nil.
2. `_, workErr := p.executeDecision(ctx, runID, v, od.RunID, od.Matched)`. `processRetryVideo`'s existing disposition and failure handling apply.

### 7.3 Executor and `ClaimPlaylistSend` (amends authorization ARS-001)

- **`executeDecision(ctx, runID, v, decisionRunID, matched)`:** `executeVideoActions(ctx, runID, v)` becomes the case `decisionRunID == runID` with matched = this run's decisions, unchanged. When `decisionRunID != runID`, routes for `matchedActionsComplete` come from `decisionAuthority(decisionRunID)`. The `failed` and `completed` reconciliation and the state transitions use the receiving run, as today.
- **`Executor.execute(ctx, runID, decisionRunID, videoID, matches, binding)`:**
  - `run` and the run-level gates (`productionAuthority(runID)`, mutation safety version, binding, `admitLegacy`) use `runID`;
  - `routes` for `loadActions` = `decisionAuthority(decisionRunID)` when `decisionRunID != runID`, else today's routes;
  - `loadActions(ctx, decisionRunID, videoID, matches, routes)`, so its persisted-run check compares with `decisionRunID`;
  - every `PlaylistSendClaim` gets `DecisionRunID: decisionRunID`.

  `ExecuteBound` and `Execute` pass `runID` for both run arguments. `ExecuteBoundDecision` exposes the parameter.
- **`ClaimPlaylistSend`:** with `DecisionRunID` 0 or equal to `RunID`, it is unchanged. Otherwise the authority query is:

```sql
SELECT 1 FROM route_decision rd
JOIN run dr ON dr.id = rd.run_id
JOIN config_version cv ON cv.id = dr.config_version_id
WHERE rd.id = ? AND rd.run_id = :decisionRun AND rd.video_id = :video AND rd.decision = 'matched'
  AND dr.run_type IN ('manual','scheduled')
  AND EXISTS (SELECT 1 FROM json_each(cv.snapshot,'$.routes') route, json_each(route.value,'$.actions') action
      WHERE CAST(json_extract(route.value,'$.id') AS INTEGER) = rd.route_id
        AND json_extract(action.value,'$.action_type') = 'playlist_insert'
        AND json_extract(action.value,'$.target_playlist_id') = :target)
  AND EXISTS (SELECT 1 FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id
      WHERE s.video_id = :video AND <prodSuccess> AND srd.run_id = :decisionRun)
  AND NOT EXISTS (SELECT 1 FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id
      WHERE s.video_id = :video AND <prodSuccess> AND srd.run_id <> :decisionRun)
  AND (EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = :video AND d.status = 'retrying' AND d.retry_run_id = :run)
    OR EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = :video AND e.status = 'retrying' AND e.retry_run_id = :run))
```

The receiving-run gate, the success check, `LegacySendVeto` (`receiving_run_id = RunID`, `assume_acted = 0`) and the guard insert are unchanged. The guard and the `executed_action` row get `run_id = RunID` and `route_decision_id` = the R0 decision.

## 8. Inspection and statistics

### 8.1 Run video detail (`GET /api/runs/{id}/videos/{videoId}`, `buildVideoDetail`)

It adds `continued_actions`:

```sql
SELECT ea.id, ea.route_decision_id, rd.run_id AS decision_run_id, rd.route_id,
       ea.target_playlist_id, ea.outcome, ea.outcome_reason, ea.error_message, ea.executed_at
FROM executed_action ea JOIN route_decision rd ON rd.id = ea.route_decision_id
WHERE ea.run_id = :run AND ea.video_id = :video AND rd.run_id <> :run
ORDER BY ea.id
```

Each item is `{action fields as in the existing action projection, "decision_run_id", "route_decision_id", "route_id", "route_name"}`. `route_name` comes from the decision run's snapshot, and is null if it cannot be read. Reasons use the existing `safeDispositionReason`. The existing `unresolved_mutations` already lists this run's continuation guards. No other field changes.

### 8.2 Per-run route statistics (`internal/stats/store.go:288–293`)

The action-outcome query becomes:

```sql
SELECT … FROM executed_action ea JOIN route_decision rd ON rd.id = ea.route_decision_id
WHERE ea.run_id = ? AND rd.route_id = ? AND rd.decision = 'matched'
```

The sums are unchanged. Attempts count for the run that made them, under the decision's route. Decision counts are unchanged. Existing data has `ea.run_id = rd.run_id` throughout, so stored numbers do not change (test K21).

### 8.3 Designer items

- The label "Continues run N's decision" on `continued_actions`.
- A route-statistics note that attempts may continue an earlier run's decision.
- Copy for `video_already_final`, for `continuation_unavailable` (interim), for closed rows, and for `original_decision_unavailable` (the user can only dismiss it).

## 9. Invariants

1. A final `final_outcome` never changes, on `video` or on `run_video_outcome` (after 028: also enforced by the database).
2. A claim of a final video, other than the M2 case, writes no membership and closes exactly its family with `closed_reason`.
3. A production main loop processes only `RoutableByRun` members.
4. A partly done video gets no production `route_decision` after its first success. Every send for it uses an R0 decision, is made by the run holding its claim, and is shown in that run's `continued_actions`.
5. The count of production `succeeded` rows for a video never decreases.
6. A refused user retry creates no run and makes no authorization or provider call.
7. Migration 028 is never applied while the claim reset is unconditional.

## 10. Verification — the test list

Real migrated SQLite and the counting controlled provider unless marked *unit*. "Snapshot" means every row of `video`, `run_video_outcome`, `route_decision`, `executed_action`, `playlist_send_guard`, `deferred_item` and `error_entry`.

### 10.1 Schema and predicates

1. **F1 migration 027:** both columns exist, NULL on every existing row; an unknown `closed_reason` aborts.
2. **F2 migration 028:**
   - for each final outcome, changing `video.final_outcome` aborts; changing `processing_state` alone does not;
   - deferred → accepted, NULL → rejected and not_yet_evaluated → excluded succeed;
   - on `run_video_outcome`, NULL → accepted and deferred → accepted succeed, and accepted → deferred aborts;
   - the down file removes both triggers, and the old reset statement then succeeds on a final video.
3. **F3 classification (SQL):**
   - each final outcome → final;
   - `completed` with `not_yet_evaluated` → not final;
   - deferred, failed or evaluated with a production success → partly done;
   - a success in a simulation run only → open;
   - no success → open.
4. **F4 original decision and authority:**
   - one production run → its matched decisions;
   - two runs → unavailable;
   - R0 not production → unavailable;
   - **R0 `completed`, `failed` or `stopped` → `decisionAuthority` returns routes (critic F1)**;
   - config row missing, snapshot invalid, matched route absent, or action without a target → unavailable.
5. **F5 `RoutableByRun` (SQL):** one seed per rule, plus a video held only by the run's own `retrying` row (kept).

### 10.2 Final videos (AC1–4)

6. **K1 run claims:**
   - **Seeds:** one video per final outcome, each with a pending deferred item; accepted and rejected videos with active Errors entries. Routes are changed so every seed would get a different outcome.
   - **Run:** a new scheduled run and a new Run Now run.
   - **Assert:**
     - zero requests for the seeds;
     - each deferred item is `completed` with `closed_reason='final_outcome'`, and its history fields are unchanged;
     - no membership row and no counts for the seeds;
     - outcomes are unchanged everywhere;
     - Errors entries are untouched;
     - the runs start and claim the other items.
7. **K2 user retries:**
   - `POST /api/errors/{id}/retry` and `/api/deferred/{id}/retry` → `409 video_already_final`. The family is closed, no run row exists, the provider and the authorization `Bind` are not called, and `runWG` is balanced.
   - A second retry → the same 409, with nothing changed.
8. **K3 counts (AC3):** run counters and `GET /api/runs/{id}/videos` totals equal those of a run without the seeds.
9. **K4 handoffs and retention:** stop, discard, a run-stopping failure and an authority failure of a run whose members include a video another run finalized. No new item or entry for that video.
10. **K5 resume and startup recovery:** an interrupted run whose members include a final video, resumed by Run Now and by startup recovery. No transition, decision, outcome or request for it.
11. **K6 re-queue:** the BR-037 preview counts and the confirm inserts no final video.
12. **K7 both directions (AC2):** an accepted video under routes that would reject it, and a rejected video under routes that would accept it. Unchanged through K1–K5.
13. **K8 races:** 100 iterations under `-race`. A scheduled run commits `rejected` for X while a user retry of X's Errors entry and a Run Now press arrive. Every result is one serial order, and no trigger aborts.
14. **K8a own final (M2):** a run commits `accepted` for a claimed video and is killed before acknowledging. On resume, its claim is acknowledged (`completed`, `closed_reason` NULL), and the outcome and counts are unchanged.

### 10.3 Partly done (AC6–10)

15. **K9 example V:**
    - **Seed:** run 40 (then `completed`) matched V to "Lofi" (`succeeded`) and "Study" (deferred, pending item). Routes are then edited so V matches nothing.
    - **Run:** run 41.
    - **Assert:**
      - zero run-41 decisions for V and no metadata fetch;
      - exactly one insert, to "Study", with `route_decision_id` = run 40's "Study" decision and `run_id` = 41;
      - "Lofi" has an `already_completed` row in run 41, and its success is unchanged;
      - V and run 41's outcome are `accepted`;
      - run 41's video detail lists the "Study" action under `continued_actions` with `decision_run_id=40`;
      - run 41's route statistics count one attempt, and run 40's do not change.
16. **K10 still partly done:** "Study" fails permanently → `failed` with an Errors entry; a user retry sends only "Study". "Study" deferred for quota → the next run sends only "Study".
17. **K11 send safety:** a held guard on "Study" → `uncertain_blocked`, zero inserts, and the guard is listed in run 41's `unresolved_mutations`. A version-0 R0 → the legacy veto refuses, and nothing is sent.
18. **K12 authority clause:**
    - `DecisionRunID=40` without a run-40 success → refused;
    - with run-40 and run-55 successes → refused;
    - with the receiving run not holding the video's claim → refused;
    - with every condition met → claimed;
    - `DecisionRunID=0` → V1 regression.
19. **K13 unreadable (AC10):** run 40's config row is removed; separately, V has successes under two runs. Zero sends and zero decisions. V is `failed` with `original_decision_unavailable` and one Errors entry. A user retry gives the same result.
20. **K14 crash mid-continuation:** kill the process after the "Study" guard claim. On restart (recovery and resume), V is continued under run 40 and nothing is resent (uncertain).
21. **K15 open (AC6):** a deferred video with decisions and no success is evaluated under the receiving run (ARS-005 regression).
22. **K16 no reversal audit:** every SQL statement that writes `executed_action` (a grep-maintained list) cannot change a `succeeded` row.
23. **K19 resume after another run took over (critic F2), exact sequence:**
    1. Run R (scheduled) matches V to targets A and B. Both are deferred for quota (pending item, `run_id=R`). R is then interrupted (`time_limit` or a kill).
    2. A new scheduled run R2 claims V. V is open, so R2 evaluates it, inserts A (success) and defers B. V is partly done with R0=R2.
    3. R is resumed: (a) by Run Now; (b) separately, by startup recovery.

    **Assert:** R's main loop drops V (rule 3: V has a pending item that R does not hold; rule 2 also applies). R sends nothing for V and writes no transition or outcome for it. V keeps one production success, under R2's decision. The next new run continues V under R2 and sends only B.

    Repeat with R2's pending item dismissed by the user (no active item): rule 2 alone drops V in R. The BR-037 preview later lists V as stranded, and continuation sends only B.

### 10.4 Simulations, statistics and composition

24. **K17 simulations (AC12, M1):** a dry run and a backtest over the K1 and K9 seeds report simulated results. `runExclusionGate` evaluates final videos as before, and the snapshot is unchanged.
25. **K20 interim hold (only if shipped):** with §7 absent, a partly done video's item is not claimed, membership and state are unchanged, and a user retry → `409 continuation_unavailable`. The run does not fail.
26. **K21 statistics regression:** on a fixture of existing runs, per-run route statistics are identical before and after §8.2.
27. **K22 rollback:** apply 028, then its down file, then run the pre-BR-039 claim code against a final video. The start succeeds (the reset is the old behavior).
28. **K18 journey** (`-tags=integration ./e2e`): seed the BR-039 example's V and W; restart; run a scheduled run; retry W as the user; take the re-queue preview. Provider inserts = 1 ("Study"). W stays rejected, its item is closed with the reason, and run detail shows the continued action.

## 11. Revisions

**2026-11-12, critic review `ses_e2e6c22cfffeEhvwlTXJINOyx1` (CHANGES REQUIRED):**

- **F1:** §7.1 `decisionAuthority`; `loadActions` gets `decisionRunID`; run-level gates stay on the receiving run; F4 tests ended runs.
- **F2:** §2 `RoutableByRun` and the §5 single main-loop filter; test K19.
- **F3:** triggers split into migration 028 with a down file and release note; §0 order (continuation with the classification, or the §3.4 interim hold; ARS-005 step 6 last); K20 and K22.
- **F4:** §8 `continued_actions` and run-attributed route statistics; K21; Designer label.
- **M1:** the filter is production-only, and the per-function checks are removed.
- **M2:** the own-outcome acknowledgement (§3.1, §5, K8a).
- **M3:** the §1 ordering sentence corrected.
- **M4:** `completed` dropped from `FinalVideo`; third count added.
- **M5:** production-only successes.
- **Simplifications:** one retry transaction (§4); one input filter (§5); the advisory clause conditions adopted (§7.3).
- **Test numbering:** the K-series is no longer contiguous.

## 12. Implementation record and deviations

Not implemented.
