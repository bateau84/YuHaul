---
type: specification
title: ARS-007 — Durable Last Retry Run on Queue Entries — Migration 032, the Claim Writer, the API Field and Tests
description: Exact contract for last_retry_run_id on deferred_item and error_entry. The fresh retry claim is its only writer; settlement, release, Stop/Discard handoff, failure sweep, final closing and removal keep it. GET /api/errors and GET /api/deferred return it as a nullable integer; NULL means no retry run is known and is never filled from retry_run_id or run_id. Covers migration 032, the legacy NULL case, the rejected alternatives and the conformance tests.
tags: [architecture, specification, retry, deferred-queue, error-queue, migration, api, run-link]
timestamp: 2026-11-22T00:00:00Z
---

**Status:** accepted — exact implementation contract, not implemented (2026-11-22). It answers the *API need* recorded in the admin UI design (commit `1ae11b0`, "Errors — Category and Reason Presentation", *Added codes*, *Which run the link targets*). Implementation belongs to PR-E (`feat/last-retry-run-link`).

## Summary

Each Errors entry and each Deferred item gets one new nullable column, `last_retry_run_id`. It names the most recent production run that took the entry as retry work. Only a fresh retry claim writes it, in the same statement that moves the row to `retrying`. Nothing else changes it: settlement, release, Stop or Discard handoff, the failure sweep, final closing and user removal all leave it as it is. The next fresh claim by another run replaces it. The Errors and Deferred APIs return it as `last_retry_run_id`, a positive integer or `null`. `null` means YuHaul knows no retry run for this entry. The client then shows the design's "a retry run" wording and no link.

**Why a new column.** The design's `View run {id}` link for `retry_claim_unverifiable` and `retry_work_removed` must name the retry run that stopped, never the entry's `run_id`. The existing `retry_run_id` cannot serve as that run's name. It is the live-claim owner, and `RequeueRetryReceiptTx` sets it to `NULL` when it releases rows (`internal/queue/retry.go:632`). Other paths keep it, and several ownership and send predicates read it (§6). A separate column with one writer gives the link a stable value without changing those predicates.

**Example.** Errors entry 12 (run 40) is claimed by manual retry run 61. Both `retry_run_id` and `last_retry_run_id` become 61. The person dismisses entry 12 while run 61 is working. Run 61's retry is refused and ends with `retry_work_removed`. The `409 retry_work_removed` body and `GET /api/errors` both show `last_retry_run_id: 61`, so the client links `View run 61`. Later, an unrelated Deferred item 7 of the same video is released by run 61's deferred outcome path. Its `retry_run_id` becomes `NULL`, but its `last_retry_run_id` stays 61. When scheduled run 70 later claims item 7, its value becomes 70.

**Limits.** This column does not say why a run ended, whether anything was sent, or that the entry's displayed reason belongs to that run. Showing the link only for the two retry-run codes is the client's job (design `1ae11b0`). Rows created after a run ended (sinks, handoff items, re-queue items) start `NULL` and get a value only when a later run claims them.

## Derived from

- Admin UI design `docs/design/yuhaul-admin-ui-design.md`, commit `1ae11b0`, *Added codes (2026-11-20)*: "The entry needs a durable id of the last retry run that touched it, or the reason is shown without a link."
- [ARS-005 §5.1.1](ars-005-requeue-stranded-videos.md#511-durable-retry-claim-provenance): the single claim writer and migration `031`'s preservation rules. This spec follows them and does not amend them.
- [ARS-003 §2](ars-003-stop-run-and-deferred-handoff.md#2-shared-handoff-package-queue-called-by-pipeline): the Stop/Discard handoff and its release.
- [Authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md): the claim and admission fences. This spec adds no fence and changes no refusal.
- Plan migration allocation (Loom Action `b820f6ca`, phase `migrations`): main ends at `031`; this PR takes the next number, expected `032`.

## Satisfies

| Source | Item | How |
|---|---|---|
| Design `1ae11b0` | Durable id of the last retry run that touched an Errors or Deferred entry | §1 column, §2 writer, §3 preservation |
| Design `1ae11b0` | Link targets the retry run that stopped, never the entry's `run_id` | §4 API field; §4.2 no fallback |
| Design `1ae11b0` | No run id: "a retry run" wording, no link | §4.2 `null` meaning |
| Plan `lr-arch` | Legacy rows stay `NULL`, no backfill | §1.2, §5 |

There are no Behavioral Requirements for this field. It adds no product behavior beyond the design's stated need.

## 1. Schema

### 1.1 Migration `032_last_retry_run.up.sql`

One golang-migrate file, no down file (like `029`–`031`). Number: the next after the highest migration on `main` when PR-E rebases for merge. On today's `main` that is `032`. If another PR merges a migration first, PR-E renumbers before merge (phase `migrations`).

```sql
-- Durable last retry run (run-initiation ARS-007). last_retry_run_id names
-- the most recent production run that took this row as retry work. Only the
-- fresh retry claim writes it; release, settlement, handoff, sweep, final
-- closing and removal keep it. NULL means no retry run is known: rows from
-- before this migration and rows no run has claimed since. Old rows are not
-- backfilled from retry_run_id, run_id or provenance.
ALTER TABLE deferred_item ADD COLUMN last_retry_run_id INTEGER REFERENCES run(id) ON DELETE SET NULL;
ALTER TABLE error_entry   ADD COLUMN last_retry_run_id INTEGER REFERENCES run(id) ON DELETE SET NULL;
```

- No index. No query filters or joins on this column; it is only projected.
- No `CHECK` and no trigger. Value rules are enforced by the single writer (§2).
- `ADD COLUMN` with a `NULL` default does not rebuild the tables. Existing rows, row IDs, the `028` final-outcome triggers, the `029` search index and `031` receipts are untouched.
- `ON DELETE SET NULL` matches `deferred_item.retry_run_id`. YuHaul does not delete runs today. If one were deleted, the entry would read `null` and show no link, which is the safe result.

### 1.2 Legacy rows

Every row that exists before `032` reads `NULL`, including rows whose `retry_run_id` is set. Do not backfill from `retry_run_id`, `run_id`, `run_video_outcome.retry_claim_provenance`, counts or timestamps. Reason: `retry_run_id` is cleared on some releases and kept on others, so on an old row it may name a different run than the one that last claimed it, or none. A wrong link is worse than no link.

### 1.3 Model

`queue.ErrorEntry` and `queue.DeferredItem` gain `LastRetryRunID *int64`. Every scan that fills these structs selects the column. A reader that builds a public view must have it.

## 2. The writer

**One writer: the fresh claim.** In `claimRetryGroupTx` (`internal/queue/retry.go`), the guarded `UPDATE` that moves each receipt row to `retrying` also sets `last_retry_run_id = <receiving run id>`. It is the same statement as `retry_run_id = ?`, so it commits or rolls back with membership, provenance, owner moves and the video reset. This covers every claim entry point: `ClaimDeferredGroup*Tx` (scheduled and Run Now batches, `RetryQueueEntry`) and `ClaimErrorGroup*Tx` (manual Errors retry). It covers both kinds of row in the receipt, so a manual claim's Deferred siblings get the value too.

| Claim result | `last_retry_run_id` |
|---|---|
| `ClaimTaken`, fresh claim (new membership row) | set to the receiving run on every receipt row |
| `ClaimTaken`, same-owner recovery (owner already this run; also the M2 "own final" return) | not written; it already equals this run from the fresh claim |
| `ClaimLost` (stale, ineligible, other live owner, already admitted) | not written |
| `ClaimClosedFinal` (`closeFinalFamilyTx`) | not written; closing is not a claim |
| `RefuseFinalRetryTx` closing, `409 video_already_final` | not written |
| Rolled back or unconfirmed commit | not written (same transaction) |

A row that a run did not claim never gets that run's id. No other function writes the column.

## 3. Points that keep the value

These points change queue rows of a retry run. Each one leaves `last_retry_run_id` exactly as it was. That includes `RequeueRetryReceiptTx`, which clears `retry_run_id`.

| Point | Function | Effect on the row | `last_retry_run_id` |
|---|---|---|---|
| Settlement: success | `acknowledgeRetryReceiptTx`, `AcknowledgeRetryGroupTx` | completed / resolved | kept |
| Settlement: deferred outcome | `AcknowledgeRetryDeferralReceiptTx`; `processRetryVideo` deferred branch with `RequeueRetryReceiptTx` | pending (owner kept, or cleared) / resolved | kept |
| Settlement: final after claim | `CloseRetryReceiptFinalTx`, `settleFinalRetryVideo` | completed / resolved, `closed_reason='final_outcome'` | kept |
| Settlement: retry failure | `FinishRetryFailureTx` | source rows completed / resolved | kept |
| Stop or Discard handoff | `HandOffUnfinishedTx`, `HandOffUnfinishedExceptTx` (ARS-003 §2) | claimed Deferred back to pending; claimed Errors resolved | kept |
| Failure sweep | `sweepStrandedWork*` → `RequeueRetryGroupTx` (in `retainThenFail`) | pending / active | kept |
| Refused retry (`retry_work_removed`, lost ownership) | `settleRefusedRetry`; the protected video gets no write | unchanged | kept |
| Unusable claim identity (`retry_claim_unverifiable`) | run interrupted, rows stay live | unchanged | kept |
| User removal | `DismissErrorEntry`, `DiscardDeferredItem` | dismissed / discarded | kept |
| Final closing without a claim | `closeFinalFamilyTx` | completed / resolved | kept |

**Rows that start `NULL`.** A row inserted by any path starts `NULL`. This includes the sink of `FinishRetryFailureTx` / `RetainRuntimeErrorTx` / `RetainRunSinkTx`, the synthetic `stopped_by_user` / `discarded_by_user` items of the handoff, `requeued_by_user` items (ARS-005), and items and entries that ordinary runs retain. These rows were made by a run, not claimed by it. Their `run_id` already names that run. A later fresh claim sets the value.

**Does it ever change later?** Only when a different run makes a fresh claim of the row (§2). The value then moves to that run. There is no other change, no clearing and no reset.

## 4. HTTP

### 4.1 Field

| Field | Type | Present on |
|---|---|---|
| `last_retry_run_id` | integer (positive `run.id`) or `null` | every Errors entry view (`errorEntryView`) and Deferred item view (`deferredItemView`) |

The field is always present: the value is an integer or JSON `null`. It is never omitted (unlike `retry_run_id`, which stays `omitempty` and keeps its current meaning). Because both views are shared, the field appears on:

- `GET /api/errors` and `GET /api/deferred` (all pages, filters and cursors; the field does not affect paging, `total` or `by_reason`), plus their `/api/queue/...` aliases;
- the single-entry reads and the `entry` object of the `409 video_already_final` and `409 retry_work_removed` bodies.

### 4.2 Meaning

- **Integer `n`:** run `n` is the most recent production run that claimed this entry as retry work. A client that shows `retry_claim_unverifiable` or `retry_work_removed` beside this entry links `View run {n}`.
- **`null`:** YuHaul knows no retry run for this entry. Either the row predates migration `032` and has not been claimed since, or no run has claimed it. The client uses the design's no-run wording ("a retry run" / "A retry stopped because…") and no link.
- **No fallback.** Neither the server nor the client fills a `null` from `retry_run_id`, `run_id` or anything else. A client that reads a response without the field (an older server) treats it as `null`.
- `retry_run_id` keeps its meaning: the current or most recent claim owner, as the queue code uses it. It is not a link target for these reasons.

In the `409 retry_work_removed` answer to Retry, the refused run made a fresh claim before the removal won. So `entry.last_retry_run_id` names that run. A client may use it for the design 3a notice link.

## 5. Rollout and rollback

- **Upgrade.** From `030` or `031` through every migration up to `032`. All existing rows read `NULL`. No data step and no Go startup step.
- **Writers.** The binary that carries `032` also carries the claim writer and the projection. There is no intermediate state in which the column exists but is not written.
- **Rollback.** Restore the pre-upgrade backup (`docs/user-docs/upgrade-notes.md`, written by `lr-docs`). Do not run an older binary on a `032` database; the release check rehearses the restore.

## 6. Rejected alternatives

1. **Keep `retry_run_id` after every release, and link from it.** This means removing the clear in `RequeueRetryReceiptTx`. `retry_run_id` without a status check is read as "this run held this video" by `withoutRetryVideos`, `unfinishedRunMembersTx`, `send_predicates.go`, `state/store.go` and `final_outcome.go`. Changing when it is cleared would change ownership, sweep and send-predicate answers. That is a far wider change than a link needs, and it still gives legacy rows a possibly wrong value.
2. **Derive the run on read from `run_video_outcome.retry_claim_provenance`.** It needs a JSON search over all receipts per entry on every list page. It is `NULL` for claims before `031`. A new column is simpler and cheaper.
3. **Write the value at settlement or handoff instead of the claim.** Paths that never settle (interrupted runs, refused retries, `retry_claim_unverifiable`) would get no value. Those are exactly the cases the link is for. The claim is the one point every retry run passes.
4. **Also stamp sinks and handoff items with the run that created them.** That run is already the row's `run_id`. On main today no writer puts these two codes into a row's `error_message`, so nothing needs it. Revisit trigger: if a path starts storing `retry_claim_unverifiable` or `retry_work_removed` as the reason of a row it creates, it must set `last_retry_run_id` to that run in the same insert, and this spec is amended.

## 7. Invariants

1. `last_retry_run_id` is non-`NULL` only if that run made a committed fresh claim of the row.
2. Only `claimRetryGroupTx` writes it, in the same statement as `retry_run_id`.
3. No release, settlement, handoff, sweep, closing or removal changes it.
4. The API never substitutes another id for `NULL`.
5. Migration `032` changes no existing value.

## 8. Tests

`internal/store`:

- **S1 Clean install.** Both columns exist, nullable, with the `run` foreign key.
- **S2 Upgrade from `030` and from `031`.** Seed rows with `retry_run_id` set and unset, statuses across all values, `closed_reason` rows, `031` receipts and indexed titles. After `032`: every `last_retry_run_id` is `NULL`. Row counts, ids, other columns, `028` triggers, `title_search` and receipts are unchanged. `integrity_check` is `ok` and the foreign-key check is clean. Reopening the database does not run `032` again.

`internal/queue` (real SQLite):

- **Q1 Fresh claim, both kinds.** A manual Errors claim with a Deferred sibling sets the value to the receiving run on every receipt row and on no other row (a non-member sibling, another video).
- **Q2 Rollback.** A claim transaction that fails after the `UPDATE` leaves the prior value (`NULL` or an older run) on reopen.
- **Q3 Same-owner recovery.** Recovery and the M2 return do not write. The value is unchanged, and so is `retry_count`.
- **Q4 Lost and closed.** `ClaimLost`, `ClaimClosedFinal` and `RefuseFinalRetryTx` leave the value unchanged.
- **Q5 Kept.** For each row of the §3 table, run the point after a claim by run R. Afterwards the value is still R. For `RequeueRetryReceiptTx` assert also that `retry_run_id` is `NULL` while `last_retry_run_id` is R.
- **Q6 Restart.** Claim by R and commit. Close and reopen the database, then settle or hand off. The value is R before and after.
- **Q7 Later retry replaces.** After R released the row (failure sweep, or Stop), a fresh claim by R2 sets R2. Then R2's settlement keeps R2.
- **Q8 New rows.** Sinks, handoff items and `requeued_by_user` items start `NULL`.

`internal/pipeline` (real binary path where available):

- **P1 `retry_work_removed`.** Manual retry run R claims entry E; the person dismisses E; R ends with `retry_work_removed`. E reads `last_retry_run_id = R`. Run P2 the same way with a Deferred item and discard.
- **P2 `retry_claim_unverifiable`.** A run whose stored claim identity is unusable is interrupted. Its rows keep the value of the claim that set it, or `NULL` for a pre-`032` claim. No other id is substituted.
- **P3 Stop.** After a Stop handoff of R, the released Deferred item keeps R, and the synthetic `stopped_by_user` item is `NULL`.

`internal/queue` handlers / `internal/server`:

- **H1 Lists.** `GET /api/errors` and `GET /api/deferred` return `"last_retry_run_id": <n>` for a claimed row and `"last_retry_run_id": null` for a legacy row whose `retry_run_id` is set (this proves there is no fallback). The key is present in both cases.
- **H2 409 bodies.** The `entry` of `409 retry_work_removed` carries the refused run's id. The `entry` of `409 video_already_final` carries the stored value unchanged.
- **H3 Paging.** Paging, `total`, `by_reason` and cursors are unchanged by the field.

Web tests belong to `lr-fe` (link from `last_retry_run_id` only; `null` keeps the no-run wording).
