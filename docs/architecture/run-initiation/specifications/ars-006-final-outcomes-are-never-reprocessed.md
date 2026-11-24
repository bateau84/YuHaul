---
type: specification
title: ARS-006 — Final Outcomes Are Never Reprocessed — Migrations 027–028, Classification, Claim Closing, Main-Loop Filter, Continuation Send, Inspection, Statistics and Tests
description: Exact contract for BR-039. Covers migration 027 (closed_reason) and 028 (immutability triggers, with a down file); the shared final, partly-done and routable predicates; claim results and closing; the single-transaction already-final retry refusal; the production main-loop input filter; the original decision and the status-free decision authority loader; continuation with decisionRunID in the executor, loadActions and ClaimPlaylistSend; continued_actions inspection and run-attributed route statistics; handoff exclusions; the interim hold; delivery order; and the conformance tests.
tags: [architecture, specification, outcomes, lifecycle, retry, deferred-queue, error-queue, send-guard, migration, api, statistics]
timestamp: 2026-11-18T00:00:00Z
---

**Status:** accepted — exact implementation contract; steps 1, 2a, 2b, 3 and 4 (§1, §2–§7) implemented; K6 implemented by `rq-confirm` (§12); K18 implemented by `TestK18_PartlyDoneVideoFinishesAndFinalVideoStaysFinal` (§12); the K-series tests K20 and K21 not yet. It realizes [ADR-007](../decisions/adr-007-final-outcomes-are-never-reprocessed.md) for [BR-039](../../../requirements/yuhaul/br-039-final-outcomes-are-never-reprocessed.md). Revised 2026-11-12 after critic review `ses_e2e6c22cfffeEhvwlTXJINOyx1` (see §11).

It amends:

- [ARS-003](ars-003-stop-run-and-deferred-handoff.md) §2 and §2.1;
- [ARS-005](ars-005-requeue-stranded-videos.md) §5–§6 (ARS-005 §15);
- run video detail and per-run route statistics;
- `ClaimPlaylistSend` in [authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) (§7.3). The send-guard owner reviewed §7.3 on 2026-11-12 and approved it with the conditions in §7.4.

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

**Release note: rollback.** golang-migrate records the database's version in `schema_migrations`. A binary refuses to start when that version is higher than its own newest migration file. It fails with `no migration found for version 28: read down for version 28 .: file does not exist`. Dropping the triggers is therefore not enough. The recorded version must also go down to the target binary's newest migration. The application never runs down migrations itself.

*Roll back to a binary whose newest migration is 027.* Stop the server, back up the database, and run the following in one `sqlite3` transaction:

```sql
BEGIN;
DROP TRIGGER IF EXISTS video_final_outcome_immutable;
DROP TRIGGER IF EXISTS run_video_outcome_final_immutable;
UPDATE schema_migrations SET version = 27, dirty = 0;
COMMIT;
```

Then start the older binary. Without the `DROP TRIGGER` statements, the older binary's unconditional claim reset aborts on a final video, and every run start fails while any final video has an eligible item. To roll forward again, start the new binary. It applies 028 again and recreates both triggers.

*Roll back to a binary older than 027.* Keep 027's two `closed_reason` columns. They are harmless to the older code, which names its insert columns, and the default is NULL. Run the same transaction, but set `version` to that binary's newest migration (for example `26`). Before rolling forward, stop the server and run `UPDATE schema_migrations SET version = 27, dirty = 0;`, because 027's columns are already there. If you skip this, the new binary fails at start with `duplicate column name: closed_reason` and leaves the version at `27` with `dirty = 1`. The same `UPDATE` then repairs it. The new binary applies 028 on its next start.

> **Warning: the triggers can disappear silently.** If you drop the triggers but leave `version = 28`, the older binary refuses to start. If you then deploy the new binary again, it starts without error, because golang-migrate sees nothing to apply. The database then has **no** immutability triggers, and nothing reports it. Always change the version in the same transaction as the `DROP TRIGGER` statements. After any rollback or roll-forward, check that `SELECT name FROM sqlite_master WHERE type='trigger' AND name IN ('video_final_outcome_immutable','run_video_outcome_final_immutable')` returns both names whenever the version is 28 or higher.

Items closed by the new binary stay closed after a rollback: their `status` is `completed` or `resolved`.

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
const FinalVideo = `(COALESCE(v.final_outcome, '') IN (` + FinalOutcomes + `))` // COALESCE: a NULL outcome is not final, and NOT FinalVideo is never NULL

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

**Live-status gate before continuation (BR-042, amended 2026-11-19).** Before `continueOriginalDecision` sends any missing addition, the receiving run applies `liveGateClaimed` with a `Live`-only requirement ([Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §5.3). Route inputs are not hydrated, and the gate runs before step 1.

- A final video is still closed before any read (§5).
- Only a recorded `none` for the receiving run continues to step 1.
- `live`, `upcoming` or `unconfirmed` → `liveSkip` (Live ARS-001 §6.1). The video is `deferred` with a live reason, and no guard is prepared. The missing additions stay retained under the original decision, with their exact sources unchanged. No `ErrOriginalDecisionUnavailable` failure is recorded on account of live status.
- After confirmation, only the missing additions are sent, under the original decision. `ClaimPlaylistSend` checks the clearance again (authorization ARS-001, *Live-status clearance at the send*).

### 7.3 Executor and `ClaimPlaylistSend` (amends authorization ARS-001)

**Receipt-at-send clarification (2026-11-18; implementation proof outstanding):** The §7.4 review records the earlier decision-query contract. The sender-ownership clause is now the exact-receipt check in [authorization ARS-001, “Authority at guard preparation and POST admission”](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md), not the existence of any same-owner row. Check it in the same guard writer, at the authority step before exact-target success, and again at final POST admission. This applies equally when `DecisionRunID` normalizes to 0 and the work is retry-origin. `DecisionRunID` still selects only decision authority; genuine native work needs no queue receipt. This clarification supersedes only the earlier broad sender clause and the claim that same-run **retry** authority is unchanged. C1–C5 otherwise keep their decision, evidence, order and compatibility rules; the typed Stop/removal/ownership outcomes are separate from a missing captured decision. No persisted shape changes.

- **`executeDecision(ctx, runID, v, decisionRunID, matched)`:** `executeVideoActions(ctx, runID, v)` becomes the case `decisionRunID == runID` with matched = this run's decisions, unchanged. When `decisionRunID != runID`, routes for `matchedActionsComplete` come from `decisionAuthority(decisionRunID)`. The `failed` and `completed` reconciliation and the state transitions use the receiving run, as today.
- **`Executor.execute(ctx, runID, decisionRunID, videoID, matches, binding)`:**
  - `run` and the run-level gates (`productionAuthority(runID)`, mutation safety version, binding, `admitLegacy`) use `runID`;
  - `routes` for `loadActions` = `decisionAuthority(decisionRunID)` when `decisionRunID != runID`, else today's routes;
  - `loadActions(ctx, decisionRunID, videoID, matches, routes)`, so its persisted-run check compares with `decisionRunID`;
  - every `PlaylistSendClaim` gets `DecisionRunID: decisionRunID`.

  `ExecuteBound` and `Execute` pass `runID` for both run arguments. `ExecuteBoundDecision` exposes the parameter.
- **`ClaimPlaylistSend`:** with `DecisionRunID` 0 or equal to `RunID`, use the existing same-run decision query plus the receipt rule above. Otherwise use this decision query plus the same receipt rule:

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
```

The receiving-run gate includes the Stop check from authorization ARS-001. The success check, `LegacySendVeto` (`receiving_run_id = RunID`, `assume_acted = 0`) and the guard insert are unchanged. The guard and the `executed_action` row get `run_id = RunID` and `route_decision_id` = the R0 decision.

### 7.4 Send-guard owner review (2026-11-12, task b39-sendguard-review, Plan revision 20)

**Verdict: APPROVED with conditions C1–C5.** The conditions are part of this contract. Checked against [authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) §"Claim request" and the code at `a83ab51`: `internal/state/playlist_guard.go:79–146, 189–215` and `internal/state/send_predicates.go`.

| Point | Finding |
|---|---|
| **Equivalence at `DecisionRunID = 0`** | Safe. With 0, or a value equal to `RunID`, the run gate, the authority query (`rd.run_id = RunID`, the target in the sending run's snapshot), the exact-target success check, `legacyClaimVetoQuery` and the guard insert are byte-for-byte today's. C1 makes this a closed rule, not an implementation choice. |
| **Decision-run authority** | Sound. It requires: `rd` is matched, belongs to `DecisionRunID` and the video; `dr` is a production run; and the target is a `playlist_insert` action of `rd.route_id` in the decision run's snapshot. There is deliberately no status gate on `dr`. Run-level gates (`running`, authority 1, mutation safety 1) stay on `RunID`. A version-0 decision run is still refused by the veto (below), so no authority or mutation-safety check is needed on `dr`. |
| **No success under any other run** | Sound and necessary: it makes the uniqueness of `OriginalDecisionTx` a database-checked fact at send time. It reads production successes over **all** targets (`prodSuccess`), including the receiving run's own decisions. That is conservative, and it can hold only for continuation. It runs inside the claim transaction, so the claim and the success it reads are serialized like today's checks. |
| **Sender holds the retry claim** | Sound. A `retrying` deferred or Errors row with `retry_run_id = RunID` for the video ties a cross-run send to the queue path. Only one live owner can exist (`retryOwner`). A resumed or other run without the claim cannot use `DecisionRunID`. |
| **Veto interplay** | Unchanged and still effective. `legacyClaimVetoQuery` runs with `receiving_run_id = RunID` and `assume_acted = 0`. `old_run.id <> RunID` therefore still includes the decision run: a version-0 decision run with an unresolved matched target (branch 1) or action (branch 2) refuses, as today. A version-1 decision run is outside the veto by design: its uncertainty lives in guards, which the key protects. |
| **Guard key uniqueness** | Unchanged. The key is `(video_id, target_playlist_id, action_type)` and does not include the run. A guard left by the decision run, or by any run, for the same target makes the `ON CONFLICT DO NOTHING` insert affect 0 rows, so the claim returns `uncertain_blocked`. Two runs can never hold a send for one target. |
| **Receipt fields** | Acceptable with C3. `PlaylistSendGuard` embeds `PlaylistSendClaim`, so the in-process receipt carries `DecisionRunID`. The persisted guard row is not extended. It stores `run_id = RunID` (the sender) and `route_decision_id` (a decision-run decision), and the decision run is derivable as `route_decision.run_id`. `ResolvePlaylistSendTx` deletes by the full row tuple and gates on `g.RunID` being `running`, so it works unchanged. Its `executed_action` upsert writes `run_id = RunID` and the decision-run `route_decision_id`, which is what `OriginalDecisionTx`, `continued_actions` and the run-attributed statistics read. `ListUnresolvedMutations` is scoped by guard `run_id`, so a continuation guard appears in the **sending** run's inspection (BR-035 AC8). |

**Conditions (exact changes).**

- **C1 Validation.** The claim validation (`playlist_guard.go:80`) also rejects `DecisionRunID < 0` as `state: invalid playlist send claim`. `DecisionRunID == RunID` is normalized to 0 before any query, so only one code path exists for same-run sends.
- **C2 One authority statement, one error.** When `DecisionRunID ≠ 0`, the §7.3 query **replaces** the same-run authority query; it is not run in addition. If it returns no row, the claim fails with the existing error `state: playlist claim lacks captured run-owned action authority`. There is no new `PlaylistClaimResult` value. The caller treats it like today's authority failure. That fails closed: nothing is sent, and the executor's existing error path applies.
- **C3 Persisted shape unchanged.** No column is added to `playlist_send_guard` or `executed_action`. `ResolvePlaylistSendTx` and its callers must not read `DecisionRunID` for any decision. It is carried only so callers can log it. Any reader that needs the decision run joins `route_decision`.
- **C4 Order inside the transaction.** The order stays: run gate → authority (same-run or §7.3) → exact-target success → legacy veto → guard insert. The §7.3 conditions "no success under another run" and "sender holds the claim" are part of the authority statement, so they are evaluated before the success check, and refuse even when the target is already complete.
- **C5 Tests**, added to K12:
  - `DecisionRunID = -1` → invalid claim, no write;
  - `DecisionRunID = RunID` gives results identical to `0` on the full V1 suite;
  - a guard already held by the decision run for the missing target → `uncertain_blocked`, zero sends;
  - a version-0 decision run with an unresolved matched target → `uncertain_blocked` through branch 1;
  - after a successful continuation send, `ResolvePlaylistSendTx` removes exactly that guard, and the `executed_action` row has `run_id = RunID` and the decision-run `route_decision_id`;
  - a run without the retry claim, or with a success under a third run → authority error, no guard row.

**Not changed by this review:** the exact-target success check (it still counts every run's success, which is conservative), quota and authorization dispatch, and the uncertainty rules of ARS-001.

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
16. **K10 still partly done:**
    - **Non-quota rejection:** "Study" is rejected for a reason other than quota → V is `failed` with an Errors entry, and "Lofi" stays `succeeded`. The "Study" send guard stays as possibly applied ([authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md): only a success or a quota `not_applied` releases it), listed under the original sending run's `unresolved_mutations`. A user retry sends nothing, records "Study" as `not_attempted` with `mutation_application_uncertain`, and keeps V in the Errors queue (one active entry).
    - **Quota deferral:** "Study" deferred for quota → the next run, and separately a user retry, each send only "Study".
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
27. **K22 rollback:** apply 028, then its down file, then run the pre-BR-039 claim code against a final video. The start succeeds (the reset is the old behavior). The full §1.2 procedure goes through golang-migrate. A source that ends at 027 fails on a database at version 28. After the rollback transaction, that source starts. The full source then recreates both triggers. Dropping the triggers without the version change leaves a database at version 28 with no triggers, and the full source starts on it without error. A source that ends at 026, with the version set to 26, starts too. Rolling forward without first setting version 27 fails with `duplicate column name` and leaves the database dirty. With version 27 set, the roll-forward succeeds.
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

**2026-11-12, send-guard owner review (task b39-sendguard-review):** §7.3 approved with conditions C1–C5 (§7.4). C5 adds six cases to test K12. No other section changes.

**2026-11-18, gate b39-gate-iii MAJOR-1 (reviewer `ses_e108def55ffel6JlGSIwaKar9U`, HEAD `b7e4a75`):** the §1.2 rollback note was wrong. Dropping the triggers left `schema_migrations` at 28, so the older binary refused to start. Deploying the new binary again then started with no triggers. §1.2 now sets the recorded version in the same transaction, warns about the silent loss of the triggers, and gives the extra version step needed to roll back past 027 and forward again. The old claim that 027 "needs no rollback" ignored the version. K22 is extended to match. The architect checked each step with golang-migrate on 2026-11-18, in a throwaway copy of the repository, with migration sources capped at 027 and 026.

## 12. Implementation record and deviations

**Step 1 (foundation) — implemented 2026-11-12**, with tests F1, F3, F5 and the OriginalDecisionTx parts of F4:

- §1.1 migration 027 (`internal/store/migrations/027_final_outcome_closing.up.sql`, no triggers);
- §1.3 vocabulary: `queue.ClosedReasonFinalOutcome`, `ErrVideoAlreadyFinal`, `AlreadyFinalError`, `ClosedReason` and `VideoFinalOutcome` on `ErrorEntry` and `DeferredItem` (read by every queue select, `video_final_outcome` only when `closed_reason` is set), the queue JSON views, the `diag` code `original_decision_unavailable`, and `ClosedReason`, `VideoFinalOutcome`, `RunVideoDetail.continued_actions` in `web/src/types.ts`;
- §2 `state.FinalVideo`, `PartlyDoneVideo`, `RoutableByRun`, `ClassifyVideoTx`, `OriginalDecisionTx` (`internal/state/final_outcome.go`).

**Steps 2a and 2b — implemented 2026-11-16** (commits 07c6429 and the main-loop filter commit):

- 2a, §3–§4: claim classification and closing, the `ClaimResult`, `closeFinalFamilyTx`, the one-transaction user-retry refusal and the `409 video_already_final` answer (tests K1, K2, K8a parts).
- 2b, §5–§6:
  - `Pipeline.withoutUnroutableVideos` (`internal/pipeline/routable_filter.go`) applies `state.RoutableByRun` once, right after `withoutRetryVideos`, only when `isProductionRun`. It runs for every start phase. Dropped ids are logged at debug, and counts by reason (`final`, `held_elsewhere`) at info. No per-function final check exists in exclusion, duplicate detection or routing, so none was removed.
  - `processRetryVideo` classifies first: a `final` video is settled by `settleFinalRetryVideo`. When the run's own `run_video_outcome` equals the video's outcome, the receipt is acknowledged (items `completed`, `closed_reason` NULL). Otherwise `queue.Store.CloseRetryReceiptFinalTx` closes the owned rows with `closed_reason='final_outcome'`. Rows an operator removed leave nothing to settle.
  - `unfinishedRunMembersTx` adds `AND NOT state.FinalVideo`. It is the single source for the stop, discard and failure-sweep handoffs. `retainAuthorityFailure` classifies inside its retention transaction and skips `final` videos.
  - Tests: K3, K4, K5, K17, K19 and the final retry dispatch (`final_filter_test.go`, `final_filter_integration_test.go`).

**Step 3 (continuation) — implemented 2026-11-16**, fixing gate b39-gate-ii D1 (a claimed partly done video committed no outcome, so every production run failed and a user retry answered 500) and D4:

- §7.1 `decisionAuthority` (`internal/pipeline/authority.go`) shares `snapshotRoutes` with `productionAuthority` and has no run-status gate. Every failure wraps `state.ErrOriginalDecisionUnavailable`.
- §7.2 `Pipeline.continueOriginalDecision` (`retry.go`): `processRetryVideo` dispatches a `partly_done` video to it before any evaluation. It reads `state.OriginalDecisionTx` and `decisionAuthority`, and checks that every matched route still has a `playlist_insert` target. If any step is unavailable, nothing is sent and the error carries `original_decision_unavailable` (`diag.WithCode` now accepts that code), so the existing failure transaction retains the video in Errors. Other read errors take the same failure path without that code.
- §7.3 `Pipeline.executeDecision(ctx, runID, v, decisionRunID, matched)`; `executeVideoActions` is its `decisionRunID == runID` case. A continuation uses `Executor.ExecuteBoundDecision`; it fails closed if the executor lacks it or no execution binding exists. `Executor.execute` keeps the run-level gates on the receiving run, takes routes for `loadActions` and `matchedActionsComplete` from `decisionAuthority`, and `loadActions` compares persisted decisions with `decisionRunID`. Each `action` carries `DecisionRunID` into `PlaylistSendClaim`. The send-claim clause itself shipped in e159c72.
- §5 backstop: `executeDecision` classifies first and returns the stored outcome for a `final` video, with no write or send.
- D4: in `executeHeld`, `withoutRetryVideos` runs first, then `withoutUnroutableVideos`, then `captureSelectedCount`. The captured count is the kept members plus the claimed retry members, so unroutable videos are not counted and claimed work still is. `listRunVideos` now treats any queue row with `retry_run_id = run` as claimed work, whatever its status, as `withoutRetryVideos` already did. Without that, a continued video (which gets no decision of its own) left the run's input set once acknowledged.
- Tests: K9, K10, K11, K13, K14, K15, K16, the D1 regression (`continuation_integration_test.go`, `executed_action_writers_test.go`), and K19's final clause (`final_filter_integration_test.go`).

**Provider failures other than quota leave the send guard** (authorization ARS-001: the application state is unknown). K10's "permanent failure" case therefore asserts that V stays partly done and that a retry is refused at the claim, with nothing resent. The retry that sends only the missing target is asserted after a quota deferral, by the next run and by a user retry.

**Fixtures changed because BR-039 forbids their old expectation (a re-evaluation of a partly done video):**

- `TestV2_ScanAndSendTimeVetoAgree`: the seeds `branch4_target_a_succeeded` and `guard_on_succeeded_target` hold a production success under a decision that matched nothing. They have no original decision to continue, so the run now sends nothing for them and retains them with `original_decision_unavailable`. The scan-side assertions are unchanged.
- `TestComposition_ActionOutcomesThroughHTTP`: the retry run has no decision for V1 and its sends are read as `continued_actions` of run 1's decision.

**Deviation (K19).** Run Now resumes only a manual `run_now` run interrupted by `time_limit`. The Run Now variant of K19 therefore uses a manual `run_now` run for R; the startup-recovery variant uses a scheduled run. R's two targets are deferred through a quota limit with no room, because the pre-flight prices a video's whole action batch. R2 sends A and defers B through a provider `quotaExceeded` on B. The final clause (the next new run continues V under R2 and sends only B) is asserted in the two pending-item variants. In the dismissed variants no queue row claims V; the BR-037 re-queue (ARS-005 step 6) is what lists it as stranded, and it is not implemented.

**Continuation-pending tests.** The six integration tests that failed at 07c6429 (`TestV2_ScanAndSendTimeVetoAgree`, `TestRetryRecovery_CompletedTargetSurvivesFreshEvaluation`, `TestEndLegacyRunChangesOnlyStatusAndReason`, `TestEndedRunsAreOutsideStartupClassification`, `TestEndedRunNeverResumedAndNeverBlocks`, `TestEndableReadAndStaleTrue`) pass after step 3, with the `TestV2` fixture change above.

**Step 4 (migration 028) — implemented 2026-11-18**, after the claim reset became conditional on the video class (invariant 7, step 2a):

- §1.2 `internal/store/migrations/028_final_outcome_triggers.up.sql` and `.down.sql`, as specified: `video_final_outcome_immutable` and `run_video_outcome_final_immutable`. They fire on `UPDATE OF final_outcome`, so an `ON CONFLICT DO UPDATE` upsert is covered too. Writing the same value passes. The existing suites ran with the triggers on and found no production writer that changes a final outcome.
- Tests (`internal/store/final_outcome_triggers_migration_test.go`):
  - **F2:** every final outcome refuses every other value (including NULL) on `video`, and `not_yet_evaluated` on `run_video_outcome`; the same value and `processing_state` alone pass; deferred → accepted, NULL → rejected, not_yet_evaluated → excluded, and (on `run_video_outcome`) NULL → accepted and deferred → accepted succeed; accepted → deferred aborts; an upsert over a final run outcome aborts; the down file removes both triggers.
  - **K22:** with 028 applied, the pre-BR-039 unconditional reset aborts on a final video; after the down file it succeeds.
  - Two more: `Steps(-1)` and `Steps(1)` through golang-migrate use the embedded down file, and an upgrade from 027 with a final video keeps its row and gains the triggers.
- Schema version assertions moved from 27 to 28 (`store_test.go`, `exclusion_reference_migration_test.go`, `route_identity_test.go`). The outcome-site inventory (`internal/state/outcome_inventory_test.go`) lists 028's outcome list as handled.

**Existing tests changed by 028:**

- `TestRunStatusCheckWidenedByWritableSchemaStep`: the trigger count is 10, because `run_video_outcome_final_immutable` also matches `LIKE 'run_%'`.
- `TestRunStatusCheckStepIsIdempotent`: it now migrates to 28 before it measures the widening steps, since 028 bumps `schema_version` too.
- `TestWidenFinalOutcomeChecksAcceptsAndRejects`: the "bogus outcome" CHECK probe uses a non-final video, because the BEFORE trigger fires ahead of the CHECK on a final one.

**Deviation (K22).** K22 runs the pre-BR-039 claim reset statement, copied verbatim, against the migrated schema. It does not check out and run the old binary. The statement is the only part of the old claim code that touches the trigger.

K6 (the BR-037 preview counts and the confirm inserts no final video) is implemented by `TestRequeueFinalVideosGetNoItem` and `TestRequeueConfirmWritesOnlyTheStrandedItems` (`internal/pipeline/requeue_confirm_integration_test.go`; ARS-005 §12).

K18 (the BR-039 example through restart, run, retry and re-queue preview) is implemented by `TestK18_PartlyDoneVideoFinishesAndFinalVideoStaysFinal` (`cmd/server/final_outcomes_journey_integration_test.go`, `-tags=integration`, Plan node `b39-journey`). **Placement:** §10.4 names `./e2e`; the real-process harness lives in package `main`, which `./e2e` cannot import, so the test sits beside J1 and G2 (`go test -tags=integration -count=1 -run '^TestK18' ./cmd/server`). It re-executes the test binary into the real `main()`, so migrations, HTTP routes, scheduler, pipeline, send guard and YouTube adapter are the shipped ones; only YouTube is a loopback fake behind a small proxy that refuses inserts into "Study" for quota while a flag is set. **What the product produces:** Run Now matches V to "Lofi" and "Study", sends "Lofi", the provider refuses "Study" (V is partly done, its "Study" addition pending), and the same run rejects W. **Prerequisites the test writes:** the user's route edit (every route disabled over `PUT /api/routes/{id}`, so a fresh evaluation of V would match nothing), the stale pending deferred item for the already rejected W, a schedule, and one unfinished video S in a legacy run so that the preview counts something. **Asserted:** the restart changes none of V's or W's decisions, actions, outcomes or queue rows and run 40 still shows its decisions; the next scheduled run, on the first scheduler check after the restart, sends exactly one insertion, to "Study", with no metadata request for V or W and no route evaluation for either; the "Study" send carries run 40's "Study" `route_decision_id`; "Lofi" has one success and an `already_completed` row in run 41; V and run 41's outcome for V are `accepted`; W stays `rejected`, run 41 holds no outcome for W and does not count it, and W's item is `completed` with `closed_reason='final_outcome'` and kept; the Errors queue stays empty; run 41's video detail for V lists the "Study" and "Lofi" actions under `continued_actions` with `decision_run_id` = run 40 and no decision of its own, and run 40's detail lists none; a user retry of W's item answers `409 video_already_final` with `final_outcome: "rejected"` and starts no run, makes no provider request and changes no row; after S's run is ended, the preview is available, counts exactly one video to re-queue (S), lists no blocked or already queued video and names neither V nor W. **Limits.** (1) The preview is not followed by a confirm: that is K6. (2) One scheduled-run wait of about one minute; the test depends on the real scheduler's first check after a start. (3) Quota-day rollover is not exercised; the refusal is a provider `quotaExceeded` on one playlist and the second process keeps the default daily budget. (4) Mutants that failed the test: partly done classified as open (no "Study" insertion), `continued_actions` always empty. Two others (final classified as open, the final-closing update disabled) were stopped by timeouts, which are not assertion kills. (5) Not independently reviewed. **Not implemented:** K20, which applies only if the interim hold ships, and it did not. K21 (statistics regression) is also not written.

**Deviation.** `FinalVideo` wraps the column in `COALESCE(v.final_outcome, '')`. `video.final_outcome` is nullable, and without it a NULL outcome makes `FinalVideo`, `NOT FinalVideo` and `PartlyDoneVideo` NULL, so `RoutableByRun` would drop every video with a NULL outcome. Classification is otherwise as specified.

`ClassifyVideoTx` returns the stored `final_outcome` as its second value for every class (`""` when NULL), not only for `final`.
