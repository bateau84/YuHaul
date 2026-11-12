---
type: specification
title: ARS-002 — Discard Unusable Time-Limit Run — Endpoint, Interfaces and Tests
description: Exact HTTP contract, pipeline and store interfaces, transaction steps, diag vocabulary, refusal codes, concurrency rules and conformance tests for discarding the run that Run Now refuses as resume_unavailable.
tags: [architecture, specification, runs, manual, recovery, discard, api, concurrency, retention]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — exact implementation contract. Implemented, review pending (2026-11-10; see [ARS-003 §9](ars-003-stop-run-and-deferred-handoff.md#9-implementation-record-and-deviations)). It realizes [ADR-003](../decisions/adr-003-discard-unusable-time-limit-run.md) and extends [ARS-001](ars-001-run-initiation-schema-and-api.md) (§2.2 gains the initiation mutex; nothing else in ARS-001 changes).

**Amended 2026-11-10 by [ARS-003](ars-003-stop-run-and-deferred-handoff.md):**

- §2.4 step 5.5 uses the deferred-queue handoff instead of the ADR-006 sweep.
- Run reads carry `discardable`/`discard_cause` (ARS-003 §4.2).
- Startup no longer exits on a discardable run (ARS-003 §3.7).
- Tests P5, P8, P12 and E1 are amended below; ARS-003 test D1 runs them.

## What the two sides agree on

The browser sends `POST /api/runs/{id}/discard` with no body after the user confirms Discard for run `{id}`. The server either discards exactly that run and answers `200`, or refuses with a closed `code`, the `run_id` and, where useful, a `reason`. A refusal or a failed discard changes nothing. Example: run 30 is the oldest time-limit Run Now run and its snapshot is missing. `POST /api/runs/30/discard` → `200 {"run_id":30,"status":"failed","outcome":"discarded","cause":"snapshot_unavailable"}`. Sending it again → `409 {"code":"run_ended","run_id":30,"status":"failed",…}`, with no further change.

## Derived from

- [BR-017 AC13–21](../../../requirements/yuhaul/br-017-manual-run-initiation.md) (user choice B, 2026-11-10).
- ADR-003 and ADR-001 in this topic; [authorization ADR-006](../../youtube-authorization/decisions/adr-006-per-video-action-failure-isolation-and-stranded-work-retention.md) §3 (sweep), [ADR-007](../../youtube-authorization/decisions/adr-007-asynchronous-startup-resume-and-shutdown-interruption.md) (gate, drain); [run-diagnostics ARD-001](../../run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) (closed diagnostic vocabulary).

## Satisfies

See ADR-003's Satisfies table (BR-017 AC13–21). This specification adds no behavior beyond it.

## Scope

In scope: `POST /api/runs/{id}/discard`; `pipeline.DiscardRun` and its errors; the shared candidate selector; `initiationMu`; `state.Store.DiscardRunTx` and `RecomputeRunCountersTx`; the `diag` vocabulary additions; SPA request/response handling. Out of scope: confirmation and acknowledgement copy and layout (Designer); startup recovery and schema (both in ARS-003, migration 025); the handoff body (ARS-003 §2).

## 1. Diagnostic vocabulary (`internal/diag`)

- Add operation `discard_run` to `operations`.
- Add codes `snapshot_unavailable`, `snapshot_unreadable`, `checkpoint_unrecognized` to `codes`.
- `Facts.Valid` additionally returns false when: `Operation == "discard_run"` and `Code` is not one of the three; or `Code` is one of the three and `Operation != "discard_run"`; or `Code` is one of the three and any of `Status`, `Reason`, `SQLite` is non-empty.
- Stored discard reason: `diag:v1 phase=unknown operation=discard_run code=<cause>`. It round-trips through `AdmitStored` and `GuardErrorText` unchanged.

## 2. Pipeline interfaces (package `pipeline`)

### 2.1 Types

```go
type DiscardResult struct {
    RunID int64
    Cause string // closed: snapshot_unavailable | snapshot_unreadable | checkpoint_unrecognized
}

type DiscardRefusedError struct {
    RunID  int64
    Code   string // closed: run_not_found | run_ended | run_not_discardable
    Status string // run_ended only: "failed" | "completed"
    Reason string // run_not_discardable only: resumable | not_oldest | not_eligible
}
func (e *DiscardRefusedError) Error() string // fixed: "run %d cannot be discarded: %s"

// Returned when the transaction did not commit; the run is unchanged.
var ErrDiscardNotApplied = errors.New("discard did not take effect")
// Returned when Commit failed and the follow-up read also failed.
var ErrDiscardOutcomeUnknown = errors.New("discard outcome could not be confirmed")

func (p *Pipeline) DiscardRun(ctx context.Context, runID int64) (*DiscardResult, error)
```

Existing `ErrStartupRecoveryActive` and `ErrProductionRunActive` are reused.

### 2.2 Shared candidate selector

Extract from `resumeOldestRunNowTx` (no SQL change):

```go
type runNowCandidate struct {
    ID                        int64
    CompletedAt, Phase, Snapshot sql.NullString
}
// selectRunNowCandidateTx returns the oldest eligible row (ARS-001 §2.2 step 3 SQL), found=false if none.
func selectRunNowCandidateTx(ctx context.Context, tx *sql.Tx) (c runNowCandidate, found bool, err error)
```

`resumeOldestRunNowTx` = `selectRunNowCandidateTx` → `validateResumableRow` → existing resume `UPDATE`. `DiscardRun` = `selectRunNowCandidateTx` → `validateResumableRow` → discard steps. No other function may decide discard eligibility.

```go
// discardCauseFor maps a non-empty validateResumableRow reason to the AC16 cause.
func discardCauseFor(reason string) string
```

| reason | cause |
|---|---|
| `snapshot_unavailable` | `snapshot_unavailable` |
| `snapshot_unreadable`, `snapshot_inadmissible` | `snapshot_unreadable` |
| `checkpoint_unrecognized`, `terminal_timestamp_contradiction` | `checkpoint_unrecognized` |

Any other value is a programming error: `DiscardRun` rolls back and returns `ErrDiscardNotApplied`.

### 2.3 Initiation mutex

Add `initiationMu sync.Mutex` to `Pipeline`. Lock order: `initiationMu` then `productionMu`. `Execute`, `StartRun`, `RetryQueueEntry` and startup never take `initiationMu`.

`AdmitRunNow` (amends ARS-001 §2.2): after step 1 (`refuseWhileRecovering`), `p.initiationMu.Lock()`; `defer p.initiationMu.Unlock()`; then the existing steps 2–5 unchanged. The deferred unlock runs before `AdmitRunNow` returns in every branch; `productionMu` stays held by a successful admission as before.

### 2.4 `DiscardRun` algorithm

1. `refuseWhileRecovering()` → `ErrStartupRecoveryActive`.
2. `p.initiationMu.Lock(); defer p.initiationMu.Unlock()`.
3. `if !p.productionMu.TryLock() { return nil, ErrProductionRunActive }`; `defer p.productionMu.Unlock()`.
4. `quotaCost := p.machine.RunQuotaCost(ctx, runID)` — export the existing `state.Machine.runQuotaCost` (`machine.go:547`) unchanged. This ledger read happens before the transaction because `WithRetryTx` forbids a second connection inside it; no execution can add ledger rows for this run while the lock is held. An error → `ErrDiscardNotApplied`.
5. `qs.WithRetryTx(ctx, func(tx) error { … })`, in order:
   1. `SELECT status FROM run WHERE id=?`. No row → `&DiscardRefusedError{runID, "run_not_found"}`. `failed`/`completed` → `run_ended` with `Status`.
   2. `c, found := selectRunNowCandidateTx`. If `!found || c.ID != runID`: if the requested row satisfies the eligibility predicate (`run_type='manual' AND initiated_by='run_now' AND status='interrupted' AND interrupt_cause='time_limit' AND mutation_safety_version=1 AND action_authority_version=1`) → `run_not_discardable/not_oldest`; else `run_not_discardable/not_eligible`.
   3. `reason := validateResumableRow(c.CompletedAt, c.Phase, c.Snapshot)`. `""` → `run_not_discardable/resumable`.
   4. `cause := discardCauseFor(reason)`; `stored := diag.Facts{Phase:"unknown", Operation:"discard_run", Code:cause}.String()`.
   5. `ids := listUnfinishedRunVideosTx(ctx, tx, runID)`; `qs.HandOffUnfinishedTx(ctx, tx, runID, queue.DeferredReasonDiscardedByUser, ids)` (ARS-003 §2). *Amended 2026-11-10; originally `sweepStrandedWork`.*
   6. `stateStore.DiscardRunTx(ctx, tx, runID, stored, quotaCost)` (§2.5). It must affect exactly one row.
   7. `stateStore.RecomputeRunCountersTx(ctx, tx, runID)`.
   
   Any returned error rolls back.
6. Error mapping after `WithRetryTx`:
   - `*DiscardRefusedError` → return it.
   - Error before commit (fn returned an error, or begin/writer-lock failed) → `ErrDiscardNotApplied` (wrapped; the raw chain is logged only via `diag.Encode`).
   - `Commit` error → re-read `SELECT status, interrupt_reason FROM run WHERE id=?` (lock still held). `failed` and reason == `stored` → success. `interrupted` → `ErrDiscardNotApplied`. Read error or any other state → `ErrDiscardOutcomeUnknown`. `WithRetryTx` must let the caller tell a commit error apart (wrap it with a `queue.ErrCommit` sentinel, or `DiscardRun` uses its own `BeginTx` with the same writer-lock statement).
7. Success: log `"run discarded"` with `run_id`, `cause` and the exact validator `reason` (closed values only). Return `{runID, cause}`. No SSE event is required; the SPA reloads.

`DiscardRun` never calls `executeHeld`, `StartRun`, `ResumeRun`, `InterruptRun`, `FailRun`, the scheduler, `p.meta`, any auth client or any provider client.

### 2.5 Store (package `state`)

```go
// DiscardRunTx writes the terminal failed state of a user discard inside tx.
func (s *Store) DiscardRunTx(ctx context.Context, tx *sql.Tx, id int64, reason string, quotaCost int) error
```

```sql
UPDATE run
SET status='failed', interrupt_reason=?, interrupt_cause=NULL,
    completed_at=COALESCE(completed_at, ?), quota_cost=?
WHERE id=? AND status='interrupted' AND run_type='manual'
  AND initiated_by='run_now' AND interrupt_cause='time_limit'
  AND mutation_safety_version=1 AND action_authority_version=1
```

The reason goes through `diag.GuardErrorText`. `RowsAffected != 1` → error (rolls back). `duration_seconds`, `started_at`, `current_phase`, `config_version_id`, `selected_video_count`, `initiated_by` are not written. `COALESCE` keeps an existing `completed_at` (the `terminal_timestamp_contradiction` case).

```go
func (s *Store) RecomputeRunCountersTx(ctx context.Context, tx *sql.Tx, runID int64) error
```

Same statement as `RecomputeRunCounters`; implement both over one helper taking an `ExecContext` interface.

## 3. HTTP contract — `POST /api/runs/{id}/discard`

Registered on the API mux next to `POST /api/runs`, so it has the same access as Run Now. The request body is ignored. Handler order: parse `{id}` → `trackRun()` → `DiscardRun(r.Context(), id)` → `runWG.Done()` → respond. The discard runs synchronously in the request.

| Outcome | Status | Body |
|---|---|---|
| Discarded | `200` | `{"run_id":<id>,"status":"failed","outcome":"discarded","cause":"<cause>"}` |
| `{id}` not a positive integer | `400` | `{"error":"invalid run id","code":"invalid_run_id"}` |
| Shutting down | `503` | `{"error":"server is shutting down","code":"shutting_down","run_id":<id>}` |
| Startup recovery | `503` | `{"error":"<ErrStartupRecoveryActive text>","code":"startup_recovery_active","run_id":<id>}` |
| Production active | `409` | `{"error":"a production run is already active","code":"production_run_active","run_id":<id>}` |
| Not found | `404` | `{"error":"run <id> does not exist","code":"run_not_found","run_id":<id>}` |
| Already ended | `409` | `{"error":"run <id> has already ended","code":"run_ended","run_id":<id>,"status":"failed\|completed"}` |
| Not discardable | `409` | `{"error":"run <id> is not the run that Run Now cannot resume","code":"run_not_discardable","run_id":<id>,"reason":"resumable\|not_oldest\|not_eligible"}` |
| Not applied | `500` | `{"error":"the discard did not take effect; run <id> is unchanged","code":"discard_failed","run_id":<id>}` |
| Outcome unknown | `500` | `{"error":"could not confirm whether run <id> was discarded; reload the run","code":"discard_outcome_unknown","run_id":<id>}` |

Refusal precedence is the order of the checks: shutdown, startup recovery, production active, not found, ended, not discardable. When several apply, the first is reported; each is true. Responses and logs carry only these closed values and the run ID.

Path note: `POST /{id}/discard` under `/api/deferred` is a different, existing action (deferred-item discard). The run endpoint lives only under `/api/runs`.

## 4. Invariants

1. **Same question as Run Now.** `DiscardRun(id)` succeeds only if, in the same transaction, `selectRunNowCandidateTx` returns `id` and `validateResumableRow` rejects it.
2. **All or nothing.** The run row change, every queue change and the counter recompute commit together or not at all.
3. **No side channel.** No provider request, no auth or token read, no change to `playlist_send_guard`, `action` or attempt rows.
4. **Terminal and final.** After success the row is `failed`, `interrupt_cause` is `NULL`, and `interrupt_reason` is a `discard_run` diagnostic. No code path moves a `failed` row back.
5. **Serial equivalence.** Every `DiscardRun` and every `AdmitRunNow` runs its decision section under `initiationMu` and `productionMu`; every other production start holds `productionMu`.
6. **Idempotent by state.** A second discard of the same run finds `failed` and is refused `run_ended`; the handoff is not run twice.

## 5. SPA obligations (behavior only; Designer owns copy and layout)

- Offer Discard only for the run named by the latest `409 resume_unavailable` from Run Now, or by run inspection when that run is shown. The server rechecks, so showing it is advisory.
- Send the request only after the user confirms for that named run; cancelling sends nothing.
- Offer Discard on Run Detail when the run reads `discardable=true` (ARS-003 §4.2).
- On `200`, reload the run and the deferred queue. On any refusal, show the code's message and reload the run. On `discard_outcome_unknown` or a network failure, reload the run before offering anything else.
- Run history and inspection render `interrupt_reason` with `operation=discard_run` as a user discard with its cause.

## 6. Verification — tests the implementation must pass

Real migrated SQLite unless marked *unit*. "Controlled provider" = the existing counting `httptest` YouTube seam. "Snapshot" of a run means its `run` row, its `deferred_item`/`error_entry` rows, `playlist_send_guard`, `action` and `run_video_outcome` rows, compared byte for byte.

### 6.1 Vocabulary (`internal/diag`, *unit*)

1. **V1:** each of the three discard reasons round-trips through `AdmitStored`/`GuardErrorText`. `operation=discard_run code=timeout`, `operation=finalize_run code=snapshot_unavailable`, and a discard code with `status=500` are rejected.

### 6.2 Pipeline (`internal/pipeline`)

2. **P1 eligibility matrix (AC13, AC20):** seed an unusable oldest candidate (missing snapshot), a younger usable candidate, a younger unusable candidate, and every ARS-001 test A4 kind (version 0, authority 0, NULL initiator, `queue_retry`, `schedule`, `shutdown` cause, NULL cause, `failed`, `completed`, `dry_run`, `replay`, `backtest`), plus a missing ID. Only the oldest is discarded. Each other request returns the expected code/reason (`not_oldest`, `not_eligible`, `run_ended`, `run_not_found`) and the snapshot is unchanged.
3. **P2 resumable refused:** a usable oldest candidate → `run_not_discardable/resumable`; unchanged; the next `AdmitRunNow` resumes it.
4. **P3 stale control:** obtain `resume_unavailable` for run R, then repair R's snapshot (re-insert `config_version`) → discard returns `resumable`. Separately, interrupt an older run with `time_limit` → discard of R returns `not_oldest`.
5. **P4 causes (AC16):** one case per validator reason; the stored reason equals the mapped `discard_run` diagnostic and `GET /api/runs/{id}` shows it with `failure_reason_unavailable` absent.
6. **P5 result (AC15, AC17):** a run with accepted, rejected, failed (with active entry), unfinished, retry-claimed (deferred group `retrying`, `retry_run_id`=R), user-removed retry-claimed, and unfinished-with-existing-active-entry videos. After discard: status `failed`, `completed_at` set, `duration_seconds` unchanged, `interrupt_cause` NULL. *(Amended)* Each unfinished video has the ARS-003 test H1 shapes: one pending deferred item with `retry_after` NULL (reused if present, new with `discarded_by_user` otherwise). A claimed error entry is `resolved`. The discard creates no `error_entry`. No `retrying` row with `retry_run_id=R` remains. The retry-claimed group is `pending` with its original IDs, `run_id` and reasons; the removed group stays removed; committed outcomes, `action` and `started_at`/`current_phase`/`config_version_id`/`initiated_by` unchanged; counters equal a fresh recompute.
7. **P6 atomicity:** inject a failure (a test seam, or a temporary `BEFORE UPDATE ON run` abort trigger) at (a) the handoff's deferred insert, (b) the claim release, (c) `DiscardRunTx`, (d) the counter recompute. Each returns `ErrDiscardNotApplied` and the snapshot is unchanged; the run is still the Run Now candidate.
8. **P7 commit error:** a commit seam that fails after a successful commit → `200`-equivalent success; one that fails without committing → `ErrDiscardNotApplied`; plus a failing follow-up read → `ErrDiscardOutcomeUnknown`.
9. **P8 no provider, guards intact (AC18–19):** seed an uncertain action with a retained guard on an unfinished video and on a video with a committed `failed` outcome. Controlled provider: zero requests during discard. Guard rows unchanged. The committed-`failed` video keeps its active Errors-queue entry; the unfinished one has a pending deferred item *(amended)*. Inspection still shows the action as uncertain. Then start a new run: it claims the deferred item and sends zero inserts to the guarded target, which ADR-006 §1 records as a handled failure. A retry of the error entry also sends zero inserts.
10. **P9 idempotency:** discard twice sequentially → second `run_ended`; queue rows identical after the first and second call.
11. **P10 races (AC21):** with `-race`, (a) two goroutines discard R at once: exactly one success, the other `run_ended`; (b) discard R and `AdmitRunNow` at once, repeated ≥100 times with a younger usable candidate Y: every iteration is either {press → `resume_unavailable` R, discard → success} or {discard → success, press → resumed Y}; R is never resumed; (c) while `Execute` of another run holds `productionMu`, discard returns `ErrProductionRunActive` within a bounded wait and changes nothing; (d) a discard holding the locks (seam pause) delays a scheduler `StartRun`, which then runs and leaves R failed.
12. **P11 gates:** startup gate closed → `ErrStartupRecoveryActive`, no lock attempt, unchanged.
13. **P12 never replayed:** after discard, `classifyStartupRuns` does not return R and does not fail on it; `AdmitRunNow` never selects R; R's handed-over deferred items are claimed by the next new run (ARS-003 Q2), and a `failed` R never makes a start fail.
14. **P13 ARS-001 unchanged:** ARS-001 tests A1–A11 pass unmodified with the extracted selector and `initiationMu`.

### 6.3 HTTP (`internal/server`)

15. **H1 contract:** registered `POST /api/runs/{id}/discard` returns every row of the §3 table; `runWG` is balanced in every branch (`WaitRuns` returns); the shutdown case is driven by `WaitRuns` having begun.
16. **H2 schedule:** the scheduler's next fire time is unchanged after a discard.

### 6.4 Composition and browser

17. **E1 discard journey (`-tags=integration ./e2e`):** real binary path with migrated SQLite and the controlled provider. Run Now hits the shortened time limit mid-actions; delete the run's `config_version` row; Run Now → `409 resume_unavailable`; discard → `200`. The deferred queue lists the unfinished videos *(amended)*. Restart (new store on the same database) and Run Now → `202 admission=started` (or `resumed` for a seeded next candidate); a started run claims the handed-over videos. Provider request count during the discard is zero. Also restart **before** the discard: the process starts, and the discard then succeeds (ARS-003 U3).
18. **E2 SPA unit (`web/`):** the discard call maps `200` and every refusal `code`; nothing is sent when the confirmation is cancelled; the run reloads after any response or a network error.

## 7. Open questions and findings

See ADR-003 Findings. The startup-classification finding was answered on 2026-11-10 (BR-017 AC11 at process start) and is realized in ARS-003 §3.7.
