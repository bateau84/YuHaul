---
type: decision
title: ADR-007 — Final Outcomes Are Never Reprocessed — Classify at the Claim and the Main Loop, Close Final Items, Continue Partly Done Videos Under Their Original Decision
description: Realize BR-039 with one shared classification (final, partly done, open) applied in the claim transaction and in one main-loop input filter. Queue items of final videos are closed with a recorded reason. Partly done videos are continued by sending only missing additions under the decisions of the single run that made their recorded successes, loaded without a status gate, shown in the sending run's inspection and attributed there in statistics. A later migration adds triggers that make final outcomes immutable.
tags: [architecture, decision, outcomes, lifecycle, retry, deferred-queue, error-queue, send-guard, idempotency, migration]
timestamp: 2026-11-12T00:00:00Z
---

**Status:** accepted — technical realization of [BR-039](../../../requirements/yuhaul/br-039-final-outcomes-are-never-reprocessed.md) AC1–AC12. Revised 2026-11-12 after critic review `ses_e2e6c22cfffeEhvwlTXJINOyx1` (CHANGES REQUIRED; F1–F4 and M1–M5 addressed, simplifications adopted: see "Revisions"). Not implemented. The exact contract is [ARS-006](../specifications/ars-006-final-outcomes-are-never-reprocessed.md). It amends:

- the claim path of [ARS-003](../specifications/ars-003-stop-run-and-deferred-handoff.md) §2.1 and its unfinished set (§2);
- the processing of retried videos in [ADR-006](adr-006-requeue-stranded-videos.md)/[ARS-005](../specifications/ars-005-requeue-stranded-videos.md) (Decision 10, §15);
- run video inspection and per-run route statistics;
- the authority check of `ClaimPlaylistSend` ([authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md)), by one optional field.

The send-guard owner reviews ARS-006 §7.3 before continuation is built.

## Summary

A production run can route a known video in only two ways:

- through a **queue claim**, which feeds `processRetryVideo`;
- through its **main loop**, which processes its own members (its discoveries and decisions). Resume and startup recovery re-enter this loop.

This design puts one classification at both entries:

- **Final:** `final_outcome` is accepted, rejected, excluded or `unavailable_on_youtube`.
  - The claim does not claim it. In the same transaction it closes the items the claim would have taken: deferred items become `completed`, Errors entries become `resolved`, and each gets `closed_reason='final_outcome'`.
  - No membership row is written, the video is not reset, and no outcome is recorded.
  - A user retry gets `409 video_already_final`.
  - The main loop drops the video from its input.
- **Partly done:** not final, and at least one production `executed_action` is `succeeded`.
  - Routes are never evaluated again.
  - Only the missing additions of the **original run** R0 are sent. R0 is the single production run whose route decisions the recorded successes reference.
  - R0's routes are read from its snapshot whatever R0's status is.
  - The send is recorded in the sending run with R0's decision id. It is shown in the sending run's inspection as "continues run R0's decision", and it is counted in the sending run's statistics.
  - The main loop drops a partly done video whose original run is not the current run.
- **Open:** everything else. This keeps today's behavior: reset and evaluate under the receiving run's configuration.

The main loop also drops any member video that has an active queue item the current run does not hold. That video belongs to the queue now (critic F2).

A later migration adds triggers that refuse any change to a final outcome. They ship only together with, or after, the claim classification. A down file is provided.

**Example (BR-039).** Run 40 matched V to "Lofi" and "Study". "Lofi" succeeded; "Study" was deferred. The user edits the routes. Run 41 claims V and classifies it partly done, with original run 40. Run 41 evaluates nothing. It loads run 40's routes from run 40's snapshot, even though run 40 has ended. The executor skips "Lofi" and sends "Study" under run 40's "Study" decision. V becomes `accepted` in run 41. Run 41's detail shows the "Study" addition, marked as continuing run 40's decision, and run 41's route statistics count that attempt.

Later, a stale item for the rejected video W is claimed by run 50. The item is closed with `closed_reason='final_outcome'`, W stays rejected, and run 50 neither counts W nor makes a call for it.

## Satisfies

| Requirement | How |
|---|---|
| BR-039 AC1 | **Claims and retries:** classified in the claim transaction (Decision 3). **Handoffs and retention:** exclude final videos (Decision 4). **Re-queue:** BR-037's scan excludes committed outcomes. **Resume, startup recovery and every run:** the main-loop input filter (Decision 5) drops final videos, and `executeDecision` refuses them as a backstop. No provider call is made for them. |
| AC2 | The classification keeps every writer away from final videos (Decisions 3–5). The triggers in migration 028 refuse any change to a final `video.final_outcome` and to a final `run_video_outcome.final_outcome` (Decision 6). |
| AC3 | A closed claim writes no membership row, and the filter keeps final videos out of the run's processing, so no later run records or counts them. |
| AC4 | Items are closed in the claim transaction with `closed_reason`, and their history is kept. A user retry gets `409 video_already_final`. Closing never fails a start. A run's own just-finalized video is acknowledged, not shown as "not processed" (M2). |
| AC5 | Unchanged. |
| AC6 | Open videos keep today's path. |
| AC7 | No evaluation. Only R0's matched decisions are passed to the executor, with R0's routes. The executor skips completed targets. Every send goes through `ClaimPlaylistSend` (guard, veto, success check). The main loop never sends under a later or earlier decision (F2). |
| AC8 | Nothing writes or downgrades a `succeeded` row (`ResolvePlaylistSendTx` already refuses). Test K16 audits this. |
| AC9 | All missing additions done → the existing `markVideoAccepted`. Deferred or failed → the existing retention, still partly done. |
| AC10 | R0 is not unique, or unreadable → no send and no evaluation. The video becomes `failed` (`original_decision_unavailable`) in the Errors queue, where the user can only dismiss it (Designer copy). |
| AC11 | The send guard is unchanged. The classification and the filter protect videos with nothing to guard. |
| AC12 | The main-loop filter applies only to production runs (`mutate=true`). Simulations neither claim, filter nor write outcomes (M1). |
| BR-035 AC8, AC14; BR-017 AC19 | Continuation actions appear in the sending run's inspection, including uncertain ones (Decision 8). |

## Current source (read 2026-11-12)

**Claims and resume**

- `internal/queue/retry.go:175–263` (`claimRetryGroupTx`): inserts membership (`:216`) and resets the video unconditionally when no live owner exists (`:229–233`).
- `internal/pipeline/retry.go:121–264` (`processRetryVideo`): it evaluates whenever the outcome is `not_yet_evaluated`. A matched-less video makes `executeVideoActions` return rejected with nothing written (`pipeline.go:856–858`), and the run fails with "no durable disposition" (`retry.go:245–246`).
- `internal/pipeline/pipeline.go:1150–1155` (`listRunVideos`): a run's members include its discoveries, its decisions and its live claims. `withoutRetryVideos` (`internal/pipeline/retry.go:361–373`) removes only videos whose queue rows name this run as `retry_run_id`. `StartRun` does not check for interrupted runs. So after a resume, the main loop can route a video that another run has claimed or continued (critic F2).

**Sending**

- `internal/pipeline/authority.go:20–56` (`productionAuthority`): refuses a run that is not `running` or `interrupted`, and validates and orders the snapshot routes.
- `internal/pipeline/executor.go:585–600` (`loadActions`): requires each decision's `run_id` to equal the `runID` passed in.
- `internal/pipeline/executor.go:288–303`: skips targets that already succeeded. `:707`: `ClaimPlaylistSend`.
- `internal/state/playlist_guard.go:101–109`: claim authority requires `rd.run_id = c.RunID` and the target in the sending run's snapshot.
- `internal/pipeline/executor.go:981` (`actionCompleted`) counts only production-run successes.

**Inspection and statistics**

- `internal/server/run_handlers.go:536–566` (`buildVideoDetail`): attaches actions only to the run's own decisions. `internal/state/store.go:844–850`: actions are filtered by `run_id`.
- `internal/stats/store.go:288–293`: per-run route action counts join actions to decisions by the **decision's** run.

**Outcomes and schema**

- `internal/store/store.go:91–99`: `ApplyMigrations` runs `m.Up()` first, then the Go schema steps.
- Some migrations have down files (013, 015, 016). The application runs only up migrations.
- `internal/state/store.go:411–413`: `RecordRunVideoOutcomeTx` already refuses to change a terminal outcome.
- Migration 026 and ARS-005 steps 2–5 are implemented. ARS-005 step 6 is not.

## Decision 1 — one classification, defined once

Both definitions use production evidence only (critic M5). *Production* means `run_type IN ('manual','scheduled')`, matching `actionCompleted`.

- **final** ⇔ `v.final_outcome IN ('accepted','rejected','excluded','unavailable_on_youtube')`. This is BR-039's definition exactly. The first draft also counted `processing_state='completed'` as final, which went beyond the requirement (critic M4). A read-only count before release confirms that no `completed` row has a non-final outcome.
- **partly done** ⇔ not final AND `EXISTS` a `succeeded` `playlist_insert` `executed_action` for the video whose `run_id` is a production run.
- **open** ⇔ neither.

The definitions are `state.FinalVideo` and `state.PartlyDoneVideo` (SQL), and `state.ClassifyVideoTx` (Go). They read only durable state, so a resume reclassifies identically.

## Decision 2 — the original decision and its missing additions

- **R0:** `SELECT DISTINCT rd.run_id` over the video's production successes, joined to `route_decision` by `executed_action.route_decision_id`, which is mandatory.
  - Exactly one row → R0.
  - Zero or several rows → unidentifiable.
  - After F2's fix, "several" can only come from history before BR-039 ships. Continuation sends record R0's decision ids, and the main loop can no longer send under another decision.
- **Original decision:** R0's `matched` route decisions for the video, plus R0's snapshot.
- **Missing additions:** for each of those, each `playlist_insert` target with no production success for the video. The executor computes this already; continuation passes the whole decision.
- **Unreadable (AC10):**
  - R0 is not a production run;
  - its configuration is missing, or its snapshot fails `validateSnapshotRoutes`;
  - a matched route is absent from the snapshot;
  - an action has no target;
  - R0 has no matched decision.

  → no send, no evaluation; `failed` with `diag:v1 phase=action_execution operation=load_run code=original_decision_unavailable`, through the existing retry-failure transaction.

**Decision authority loader (critic F1).** `decisionAuthority(ctx, db, cs, decisionRunID) ([]router.Route, error)` is `productionAuthority`'s snapshot half without the run-status gate. It requires a production run type and a captured configuration, runs `validateSnapshotRoutes`, and applies the same route and order checks. Continuation uses it to read R0's routes whatever R0's status (ended runs are the normal case). Run-level gates stay on the **receiving** run: status, authority version, mutation safety version, the binding, and `admitLegacy`. `loadActions` gets `decisionRunID` for its persisted-run check, so it checks `persistedRun == decisionRunID`. With `decisionRunID == runID`, everything is exactly today's code.

| Option | Verdict |
|---|---|
| **Single production run referenced by the recorded successes; status-free snapshot loader (chosen)** | Uses mandatory data. The status gate is irrelevant to reading a decision. Run gates stay where they protect sending. |
| Reuse `productionAuthority(R0)` | Rejected (critic F1): it refuses ended runs, so every real continuation fails. |
| Latest matched decision, or the earliest success's run | Rejected: it can pick a decision the success was not made under. |
| Store an explicit original-decision pointer | Rejected: derivable from existing data, at the cost of a migration and upkeep. |

## Decision 3 — claims: closing final items, and the user retry

**Run claims** (`claimRetryGroupTx`, deferred family). After the owner check:

| Class | Owner | Result |
|---|---|---|
| final | another live run | lost (unchanged) |
| final | this run, and this run's own `run_video_outcome` holds that final outcome (the run made it final, then crashed or stopped before acknowledging; critic M2) | taken without reset. `processRetryVideo` acknowledges the receipt normally: items `completed`/`resolved`, with **no** `closed_reason`. |
| final | anyone else, or none | close the family with `closed_reason='final_outcome'`; result `ClaimClosedFinal`; no membership, no reset |
| partly done | — | taken. Reset to `processing_state='evaluated'`, `final_outcome='not_yet_evaluated'` (not routed). |
| open | — | taken. Today's reset. |

`createClaimedRunLocked` and the batch claim treat `ClaimClosedFinal` like a lost claim.

**User retry (simplified, as the critic advised).** `RetryQueueEntry` uses one `WithRetryTx` under `productionMu`, before `auth.Bind`:

1. Look up the entry's video, whatever the entry's status, and its owner.
2. Classify.
3. If final, close the family (Errors and deferred rows), commit, and return `*queue.AlreadyFinalError`. No run is created.
4. Otherwise create the run and claim, as today.

There is no separate pre-check transaction and no backstop path. The answer is `409 {"code":"video_already_final","final_outcome":…,"entry":…}`.

**Concurrency.** Outcome writers and claims both hold `productionMu` and the SQLite writer, so they are serial.

**Schema.** Migration 027 adds `closed_reason TEXT CHECK (closed_reason IS NULL OR closed_reason='final_outcome')` to `deferred_item` and `error_entry`, by `ALTER TABLE`.

| Option | Verdict |
|---|---|
| **Close in the claim transaction, with a reason column (chosen)** | Atomic with the claim decision; history visible. |
| Pre-check transaction plus a backstop in the claim, for retries | Replaced by the single transaction (critic simplification). |
| Reuse `discarded`/`dismissed` | Rejected: those mean "removed by the user" (BR-037). |

## Decision 4 — handoffs and retention never re-queue a final video

- `unfinishedRunMembersTx` adds `AND NOT <FinalVideo>`.
- `retainAuthorityFailure` skips final videos.
- `HandOffUnfinishedTx` step 1 still releases every claim of the ended run. The next claim closes the items of final videos.

## Decision 5 — one main-loop input filter (critic F2, M1, simplification)

In `executeHeld`, for production runs only (`p.isProductionRun` and mutating execution), the member list from `listRunVideos` is filtered **once**, right after `withoutRetryVideos` and before `captureSelectedCount`, through `state.RoutableByRun` (alias `v`, parameter `?run`). A member is kept only if all three hold:

1. not `FinalVideo`;
2. not partly done under another run: `NOT PartlyDoneVideo`, or the video's successes reference only `?run`'s decisions;
3. no active queue item that `?run` does not hold: no deferred row `pending`, or `retrying` with `retry_run_id` other than `?run`, and no Errors row `active`, or `retrying` with `retry_run_id` other than `?run`.

Dropped members get no transition, no decision, no outcome, no event and no provider call. The run's resume checkpoint logic is unchanged. It simply sees fewer videos.

- Rule 3 covers the critic's sequence. After run R is interrupted, its deferred video V is claimed by R2. When R resumes, V is held by the queue or by R2, so R's main loop leaves V alone, whatever V's class is. V is not lost: it has an active item, or it is partly done under R2 and so continued by the next claim.
- R's **own** deferred or failed members whose sink R itself created also have an active item that R does not hold. Rule 3 drops those on resume too. They are already disposed (committed deferred or failed in R), and the queue owns them, so the next new run's claim processes them. Before this change, a resumed run re-attempted its own deferred videos. Now the next claim does. Both satisfy BR-013 and BR-035 AC14.
- Rule 2 covers a stranded member (no item) that is partly done under another run. The BR-037 re-queue then delivers it to continuation.

`runExclusionGate`, `detectVideos` and `routeVideos` get **no** per-function check. They receive the filtered list, and the shared simulation paths (`dryrun.go:152`, `replay.go:213`) are untouched. The only per-video backstop is in `executeDecision`: a final video returns its stored outcome without a write or send.

`processRetryVideo` dispatches on the class:

- final → acknowledge if this run's own outcome row holds that final outcome (M2), otherwise close with `closed_reason`;
- partly done → continuation;
- open → today's path, with ARS-005's precondition.

## Decision 6 — immutability triggers (migration 028), delivery and rollback

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

These are split into **migration 028**, separate from 027's columns (critic F3a).

- **Why later.** Under today's unconditional claim reset, the trigger would abort a claim of a final video and roll back the whole run start, which breaks BR-035 AC15. So 028 ships in the same release as the claim classification, or later. It never ships before.
- **Why only `final_outcome`.** The `video` trigger watches only `final_outcome`, following M4's strict definition. Changes to `processing_state` are left to the classification.
- **Rollback.**
  - `028_final_outcome_triggers.down.sql` drops both triggers. The application never runs down migrations, so an operator rolling the binary back past this release first applies that file's two `DROP TRIGGER` statements with `sqlite3`. Without that, the older binary's reset hits the trigger and every run start fails while a final video has an eligible item.
  - 027's columns are harmless to an older binary: every insert names its columns, and the default is NULL.
  - Items closed by the new binary stay closed.

## Decision 7 — continuation send under the original run's decision

`continueOriginalDecision(ctx, runID, v, receipt)`:

1. `OriginalDecisionTx` → R0 and its matched decisions, or the AC10 path.
2. `executeDecision(ctx, runID, v, R0, matched)`. Every use of routes for actions in `executeDecision` and the executor comes from `decisionAuthority(R0)`: `loadActions` (with `decisionRunID`) and `matchedActionsComplete`. Run-level checks use the receiving run.
3. Disposition is today's: accepted, guarded deferral, or retry failure.

**`ClaimPlaylistSend` with `DecisionRunID ≠ RunID`** (amends authorization ARS-001). Authority is taken from R0's decision and snapshot, with no status gate on R0, and three more conditions must hold:

- the video has a production success under an R0 decision;
- **no** production success under a decision of any other run;
- the receiving run currently holds the video's retry claim: a `retrying` deferred or Errors row with `retry_run_id = RunID`.

These follow the critic's advisory. The second makes the database itself enforce the uniqueness Decision 2 relies on. The third ties continuation to the claim path. The success check, the legacy veto (receiving run) and the guard insert are unchanged.

| Option | Verdict |
|---|---|
| **Send under R0's decision rows with `DecisionRunID` (chosen)** | Success records stay attributable; guard and veto unchanged. |
| Copy R0's decisions into the receiving run | Rejected: authority checks the receiving run's snapshot, which may lack the route (the BR-039 example). |

## Decision 8 — inspection and statistics for continuation sends (critic F4)

- **Run video detail** (`GET /api/runs/{id}/videos/{videoId}`) gains `continued_actions`: every `executed_action` of this run and video whose decision belongs to another run. Each carries:
  - `decision_run_id`, `route_decision_id`, `route_id`, and the route name from that run's snapshot;
  - the target, outcome, reason and attempt time, through the same safe projection as other actions.

  Unresolved mutations of this run (`ListUnresolvedMutations`, already scoped by the guard's `run_id`) include continuation guards, so an uncertain continuation stays visible (BR-035 AC8, BR-017 AC19). The run's video list already includes the video through its membership (BR-035 AC14).
- **R0's detail** is unchanged. It shows its own actions only.
- **Per-run route statistics** (`internal/stats/store.go:288–293`) count actions where `ea.run_id = runID`, joined to their decision for the route id, instead of by the decision's run. A continuation attempt therefore counts for the run that made it, under R0's route id, and never adds to R0 after R0 has ended.

  Every existing row has `ea.run_id = rd.run_id`, because `loadActions` enforces it, so today's numbers do not change. The decision-count columns (matched, rejected and so on) still count decisions, so the sending run's matched count does not include a continued video. Its actions count does.
- **Designer:** the label "Continues run R0's decision" in Run Detail, the route-statistics note that attempts can belong to continued decisions, and the Errors copy for `original_decision_unavailable` (it can only be dismissed).

## Delivery order (critic build order, adopted)

1. **Design:** this revision. The send-guard owner reviews ARS-006 §7.3.
2. **Implementation:**
   1. Predicates, migration 027 (`closed_reason` columns only) and the vocabulary.
   2. Claim classification and closing, the retry 409, the handoff exclusions, and the main-loop input filter.
   3. Continuation: `decisionAuthority`, `decisionRunID` in the executor and `loadActions`, the `ClaimPlaylistSend` clause, and the inspection and statistics projection. It ships in the **same release** as ii. If it cannot (for example, the send-guard review is pending), ii ships with the **interim hold** (ARS-006 §3.4): a partly done video is not claimed and not processed, its item stays pending and untouched, and the hold is logged. It never reaches the old evaluate path.
   4. Migration 028 (the triggers), in the same release as ii or later.
   5. ARS-005 step 6 (batched claiming and the metadata precondition), after iii.
3. **Before release, three read-only counts:**
   - final videos with active items;
   - partly done videos whose successes reference more than one run;
   - `processing_state='completed'` rows with a non-final outcome (M4).

## Rules the implementation must preserve

1. Every classification uses `state.FinalVideo`, `state.PartlyDoneVideo` and `state.RoutableByRun`.
2. A claim of a final video, other than the M2 case, writes no membership, does not reset, and closes exactly its family with `closed_reason='final_outcome'`.
3. The claim reset runs only for open videos.
4. A production main loop processes only `RoutableByRun` members. Simulations are untouched.
5. A partly done video is never evaluated in production. It is sent only under R0's decisions, with R0's routes from `decisionAuthority`, and only by the run that holds its claim.
6. Nothing deletes or downgrades a `succeeded` `executed_action`.
7. Migration 028 never ships before item 2.ii of the delivery order. Removing the triggers needs a new decision.

## Findings

| Finding | Disposition | Owner | Blocks? |
|---|---|---|---|
| Critic F1: `productionAuthority` and `loadActions` refuse ended original runs. | Fixed: `decisionAuthority` loader; `decisionRunID` in `loadActions`; run gates stay on the receiving run (Decisions 2 and 7). | Architect | No |
| Critic F2: resume routes a video that another run took over. | Fixed: the main-loop filter `RoutableByRun` (Decision 5). Test K19 has the exact sequence. | Architect | No |
| Critic F3: unsafe delivery order (triggers before classification; classification before continuation); rollback. | Fixed: triggers split into 028, shipped with or after ii; continuation in the same release as ii, or the interim hold; ARS-005 step 6 last; down file and operator note (Decision 6, Delivery order). | Architect, Implementer | Ordering |
| Critic F4: continuation sends invisible; statistics credited to R0. | Fixed: `continued_actions` and run-attributed route statistics (Decision 8). The label goes to the Designer. | Architect, Designer | No |
| M1–M5 | M1 filter for production runs only; M2 acknowledgement; M3 ordering sentence (ARS-006 §1); M4 strict final definition plus count; M5 production filters. | Architect, Implementer | No |
| A video on the AC10 path can only be dismissed. | Designer copy. | Designer | No |
| Resumed runs no longer re-attempt their own deferred members; the next claim does (Decision 5, rule 3). | Accepted: the queue owns those videos, and BR-013 and BR-035 AC14 are met. | — | No |

## Revisions

**2026-11-12, critic review `ses_e2e6c22cfffeEhvwlTXJINOyx1`.**

- **F1:** decision authority loader, and `decisionRunID` in `loadActions`.
- **F2:** main-loop input filter `RoutableByRun`, with three rules.
- **F3:** triggers moved to migration 028 with a down file; continuation in the same release as the classification, or the interim hold; ARS-005 step 6 last.
- **F4:** `continued_actions` and run-attributed route statistics.
- **M1:** production-only filter, replacing the per-function checks.
- **M2:** a run's own final outcome is acknowledged.
- **M3:** ordering sentence corrected.
- **M4:** strict final definition; the `completed` count is added.
- **M5:** production-run filters.
- **Simplifications adopted:** one retry transaction; one input filter. The critic's advisory claim-clause conditions were adopted.

## Revisit triggers

- Any new writer of `video.final_outcome` or `run_video_outcome.final_outcome`.
- Any new way for a known video to enter a production run other than a queue claim or the filtered main loop.
- A requirement that makes `deferred` or `failed` final, or that lets a final outcome change.
- A send path that does not go through `ClaimPlaylistSend`.
- The M4 count is non-zero.
