---
type: specification
title: ARS-003 — Stop Run and Deferred Handoff — Migration 025, Interfaces, HTTP and Tests
description: Exact migration 025 and schema step, run-control and stop interfaces, the shared deferred-queue handoff, claim-ownership changes, startup classification changes, run-read fields, HTTP contracts and the full conformance test list for Stop and the amended Discard.
tags: [architecture, specification, runs, stop, discard, deferred-queue, migration, api, concurrency]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — exact implementation contract. Implemented, review pending (2026-11-10; see §9). It realizes [ADR-004](../decisions/adr-004-stop-active-run-and-deferred-handoff.md) and amends [ARS-002](ars-002-discard-unusable-time-limit-run.md) §2.4 (Discard uses the handoff).

## What the two sides agree on

**Stop.** The browser sends `POST /api/runs/{id}/stop` with no body after the user confirms. Example: `POST /api/runs/41/stop` → `202 {"run_id":41,"status":"running","stop":"accepted"}`. Polling `GET /api/runs/41` shows `"status":"running","stop_requested_at":"2026-11-10T12:00:03Z"` (stopping), then `"status":"stopped"`. A second press while stopping answers `202 … "stop":"already_stopping"`. A press after settling answers `409 {"code":"run_ended","run_id":41,"status":"stopped"}`.

**Discardable.** `GET /api/runs/30` returns `"discardable":true,"discard_cause":"snapshot_unavailable"` for the one run that Discard would currently accept, and `"discardable":false` for every other run.

## Derived from

[BR-035](../../../requirements/yuhaul/br-035-stop-active-run.md); [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md) AC11 (startup rule), AC17 (amended), AC22; [BR-011](../../../requirements/yuhaul/br-011-processing-state-persistence.md) AC7; ADR-004; [authorization ADR-005](../../youtube-authorization/decisions/adr-005-startup-recovery-for-guarded-production-runs.md) and [ADR-007](../../youtube-authorization/decisions/adr-007-asynchronous-startup-resume-and-shutdown-interruption.md), both amended.

## Satisfies

See ADR-004's Satisfies table. No behavior beyond it.

## 1. Schema

### 1.1 Migration `025_run_stop.up.sql` (golang-migrate, one transaction)

```sql
ALTER TABLE run ADD COLUMN stop_requested_at TEXT;   -- RFC 3339 UTC | NULL

CREATE TRIGGER run_stop_requested_set
BEFORE UPDATE OF stop_requested_at ON run
WHEN OLD.stop_requested_at IS NOT NEW.stop_requested_at
  AND (OLD.stop_requested_at IS NOT NULL OR NEW.stop_requested_at IS NULL OR OLD.status <> 'running')
BEGIN SELECT RAISE(ABORT, 'invalid run stop request'); END;

CREATE TRIGGER run_stop_requested_insert
BEFORE INSERT ON run
WHEN NEW.stop_requested_at IS NOT NULL OR NEW.status = 'stopped'
BEGIN SELECT RAISE(ABORT, 'invalid run stop request'); END;

CREATE TRIGGER run_stop_requested_status
BEFORE UPDATE OF status ON run
WHEN NEW.status IS NOT OLD.status AND (
     (OLD.stop_requested_at IS NOT NULL AND NEW.status <> 'stopped')
  OR (NEW.status = 'stopped' AND (OLD.stop_requested_at IS NULL OR OLD.status NOT IN ('running','interrupted')))
  OR OLD.status = 'stopped')
BEGIN SELECT RAISE(ABORT, 'invalid stopped transition'); END;
```

The marker is set once, only on a `running` row. A marked row can only become `stopped`. `stopped` requires a marker and is final.

Rebuild `deferred_item` exactly as migration 020 does (same columns, indexes `idx_deferred_item_status` and `deferred_item_retry_owner`). The only change is:

```sql
deferral_reason TEXT NOT NULL CHECK(deferral_reason IN
  ('quota_exhausted','api_rate_limited','authorization_pending','stopped_by_user','discarded_by_user'))
```

No table references `deferred_item`, so the rebuild is safe inside the transaction.

### 1.2 Schema step `widenRunStatusCheck` (Go, end of `ApplyMigrations`)

- **Skip** when the `run` row in `sqlite_schema` already contains `'stopped'`.
- **Otherwise,** on one connection (`db.Conn`), in this order:
  1. `BEGIN IMMEDIATE`.
  2. `v := PRAGMA schema_version`.
  3. `PRAGMA writable_schema=ON`.
  4. `UPDATE sqlite_schema SET sql = replace(sql, 'CHECK(status IN (''running'', ''completed'', ''interrupted'', ''failed''))', 'CHECK(status IN (''running'', ''completed'', ''interrupted'', ''failed'', ''stopped''))') WHERE type='table' AND name='run' AND instr(sql, 'CHECK(status IN (''running'', ''completed'', ''interrupted'', ''failed''))') > 0`.
  5. Require `RowsAffected == 1`; otherwise roll back and return an error.
  6. `PRAGMA schema_version = v+1`.
  7. `PRAGMA writable_schema=OFF`.
  8. `COMMIT`.
  9. `PRAGMA integrity_check` must return exactly `ok`.
- **Errors** fail `Open`. The step is idempotent.
- **Fallback** if test M0 fails: the 12-step rebuild of `run` on the same dedicated connection with `PRAGMA foreign_keys=OFF` outside the transaction. It recreates the `run` indexes and all six 024 triggers plus the three 025 triggers, and requires `PRAGMA foreign_key_check` to be empty before commit.

### 1.3 Model and vocabulary

- `state.RunStatusStopped = "stopped"`. `state.Run.StopRequestedAt *time.Time`, JSON `stop_requested_at` (always present; `null` when unset).
- `queue.DeferredReasonStoppedByUser = "stopped_by_user"`, `queue.DeferredReasonDiscardedByUser = "discarded_by_user"`. Add both to `DeferredReason.Valid` and to `diag.GuardDisposition`'s `queue_reason` set.
- `diag`: add operation `stop_run` and code `stopped_by_user`. `Facts.Valid` requires `operation=stop_run` ⇔ `code=stopped_by_user`, with no status, reason or sqlite field. The stored stop reason is `diag:v1 phase=unknown operation=stop_run code=stopped_by_user`.
- `pipeline.ErrRunStoppedByUser = errors.New("run stopped by user")` (context cause only; never stored as text).
- `web/src/types.ts`: `RunStatus` adds `'stopped'`; `Run` adds `stop_requested_at`, `discardable`, `discard_cause`.

## 2. Shared handoff (package `queue`, called by `pipeline`)

```go
// HandOffUnfinishedTx hands every unfinished video of ended run runID to the
// deferred queue and releases all of its claims. Caller's transaction; no
// network; no guard or action writes.
func (s *Store) HandOffUnfinishedTx(ctx context.Context, tx *sql.Tx, runID int64, reason DeferredReason, unfinished []int64) (HandoffResult, error)

type HandoffResult struct{ Requeued, Created, Resolved, Removed int } // for logs and tests
```

`reason` must be `DeferredReasonStoppedByUser` or `DeferredReasonDiscardedByUser`; anything else is an error. Steps, in order:

1. `UPDATE deferred_item SET status='pending', retry_after=NULL WHERE status='retrying' AND retry_run_id=?` → `Requeued`.
2. `UPDATE error_entry SET status='resolved', resolved_at=? WHERE status='retrying' AND retry_run_id=?` → `Resolved`.
3. For each V in `unfinished`:
   - `UPDATE deferred_item SET retry_after=NULL WHERE video_id=V AND status='pending'`.
   - If `EXISTS(deferred_item WHERE video_id=V AND retry_run_id=R AND status='discarded') OR EXISTS(error_entry WHERE video_id=V AND retry_run_id=R AND status='dismissed')`: `Removed++` and continue.
   - If no `deferred_item` of V has `status='pending'`: `INSERT INTO deferred_item(video_id, run_id, deferral_reason, retry_after, retry_count, status) VALUES(V, R, reason, NULL, 0, 'pending')` → `Created++`.
4. Postcondition, checked in the transaction: no row with `status='retrying' AND retry_run_id=R` remains in either table. Otherwise return `ErrRetryOwnership`, and the caller rolls back.

`retry_run_id` is not cleared (most-recent-owner marker).

The unfinished set comes from `listUnfinishedRunVideosTx(ctx, tx, runID) ([]int64, error)` in package `pipeline`. It is the exact `SELECT` now inline in `sweepStrandedWork` (`authority.go:314–326`), extracted. `sweepStrandedWork` calls it and is otherwise unchanged. For a non-production run type it returns an empty set.

### 2.1 Claim ownership (package `queue`)

- `retryOwner`: `if status == "failed" || status == "stopped" { continue }`.
- `claimRetryGroupTx` eligibility and `ListDeferredRetryCandidates`: `r.status IN ('failed','stopped')` replaces `r.status = 'failed'`.
- `ValidateRetryReceiptTx`: unchanged (`stopped` is not `running`/`interrupted`, so not owned).

## 3. Pipeline (package `pipeline`)

### 3.1 Run control

```go
type runControl struct {
    mu            sync.Mutex
    cancel        context.CancelCauseFunc // nil until attached
    stopRequested bool
    finalizing    bool
}
func (p *Pipeline) registerRun(id int64) *runControl          // idempotent
func (p *Pipeline) unregisterRun(id int64)
func (c *runControl) attach(ctx context.Context) (context.Context, bool /*stopRequested*/)
func (c *runControl) beginFinalize() bool                     // false ⇒ a stop owns the ending
```

`attach` derives `context.WithCancelCause(ctx)` and stores the cancel func. If `stopRequested` is already set, it cancels immediately with `ErrRunStoppedByUser` and returns `true`.

Registration points:

- `createClaimedRunLocked` after commit;
- `admitLocked` resume branch after commit;
- `RetryQueueEntry` after its creation commit;
- `DryRun`, `Replay`, `Backtest` after insert;
- `executeHeld` at entry, if not registered.

`RunNowAdmission.Release` unregisters.

Attach points:

- `executeHeld`: after loading the run; if `attach` reports a stop, go to the finalizer;
- `RetryQueueEntry`: before `processDeferredItems`;
- `executeHistorical` and `ExecuteDryRun`: at entry.

### 3.2 Terminal gate

Each terminal writer begins with `if ctl := p.control(runID); ctl != nil && !ctl.beginFinalize() { return }`:

- `retainThenFail`;
- `interruptRunWithCause`;
- `completeRun`;
- `finalizeFailedRun`;
- `completeHistoricalRun`;
- `failHistoricalRun`;
- the dry-run complete and fail paths.

Store backstop: `CompleteRun`, `FailRun`, `InterruptRun`, `InterruptRunWithCause` and `DiscardRunTx` add `AND stop_requested_at IS NULL` to their `WHERE`. `interruptCauseFor` returns `""` when `errors.Is(context.Cause(ctx), ErrRunStoppedByUser)`, checked first.

### 3.3 Finalizer and settling

`executeHeld`, `RetryQueueEntry`, `executeHistorical` and `ExecuteDryRun` each `defer p.finishRun(runID)`. It runs before the function returns, so production runs still hold `productionMu`. `finishRun`: if `ctl.stopRequested` → `settleStop(runID)`; then `unregisterRun`.

```go
func (p *Pipeline) settleStop(ctx context.Context, runID int64) error
func settleStopTx(ctx context.Context, qs *queue.Store, ss *state.Store, tx *sql.Tx, runID int64, quotaCost int, now time.Time) error
```

`settleStop` reads `quotaCost` (`machine.RunQuotaCost`, exported as in ARS-002) before the transaction. It runs `settleStopTx` in `WithRetryTx` under `runFinalizationContext`, retries once, and logs `run stop settled` (`run_id`, `HandoffResult`) or `run stop settle failed` (`run_id`, `diag.Encode`). `settleStopTx`:

1. `SELECT run_type, status, stop_requested_at`. Require a marker and status `running`/`interrupted`. `stopped` → return nil (idempotent). Anything else → error.
2. Production (`manual`/`scheduled`): `ids := listUnfinishedRunVideosTx`; `qs.HandOffUnfinishedTx(…, DeferredReasonStoppedByUser, ids)`.
3. `ss.StopRunTx(ctx, tx, runID, stopReason, quotaCost, now)`:

   ```sql
   UPDATE run SET status='stopped', interrupt_reason=?, interrupt_cause=NULL,
          completed_at=?, duration_seconds=(julianday(?) - julianday(started_at))*86400, quota_cost=?
   WHERE id=? AND stop_requested_at IS NOT NULL AND status IN ('running','interrupted')
   ```

   It must affect exactly one row.
4. `RecomputeRunCountersTx` for production; `RecomputeRunCountersFromDecisionsTx` for simulations. This is a new tx variant over the existing statement.
5. Broadcast `EventRunStopped` (new event constant) after commit.

### 3.4 `StopRun`

```go
type StopResult struct { RunID int64; Outcome string } // "accepted" | "already_stopping"
type StopRefusedError struct { RunID int64; Code, Status, Reason string }
// Code: run_not_found | run_ended (Status completed|failed|stopped) | run_not_active (Reason not_executing|ending)
var ErrStopNotRecorded = errors.New("stop could not be recorded")
func (p *Pipeline) StopRun(ctx context.Context, runID int64) (*StopResult, error)
```

Algorithm:

1. `SELECT status, stop_requested_at FROM run WHERE id=?`. No row → `run_not_found`. `completed`/`failed`/`stopped` → `run_ended`.
2. `ctl := p.control(runID)`. If nil: marker set → `already_stopping`; else `run_not_active/not_executing`.
3. `ctl.mu.Lock()` (deferred unlock).
   - `finalizing` → `run_not_active/ending`.
   - `stopRequested` → `already_stopping`.
4. `res := UPDATE run SET stop_requested_at=? WHERE id=? AND status='running' AND stop_requested_at IS NULL`.
   - Error → `ErrStopNotRecorded`.
   - `RowsAffected=0` → re-read and map per step 1, or `run_not_active/not_executing`.
5. `ctl.stopRequested = true`; if `ctl.cancel != nil` → `ctl.cancel(ErrRunStoppedByUser)`. Log `run stop accepted` (`run_id`).
6. Return `accepted`.

`StopRun` takes neither `initiationMu` nor `productionMu`, and it is not gated by `refuseWhileRecovering`.

### 3.5 Discard amendment (ARS-002 §2.4 step 5.5)

Replace `sweepStrandedWork(ctx, qs, tx, runID, stored)` with `ids := listUnfinishedRunVideosTx(ctx, tx, runID)`; `qs.HandOffUnfinishedTx(ctx, tx, runID, queue.DeferredReasonDiscardedByUser, ids)`. Everything else in ARS-002 is unchanged.

### 3.6 Discardable read

```go
func (p *Pipeline) DiscardableRun(ctx context.Context) (runID int64, cause string, ok bool, err error)
```

It runs in a read-only `BeginTx(ctx, &sql.TxOptions{ReadOnly: true})`: `selectRunNowCandidateTx` → `validateResumableRow` → `discardCauseFor`. No locks.

### 3.7 Startup classification (amends authorization ADR-005; see its amendment)

Inside `classifyStartupRuns`' transaction:

1. **Settle marked rows first.** `SELECT id FROM run WHERE stop_requested_at IS NOT NULL AND status IN ('running','interrupted') ORDER BY started_at, id`. For each, `settleStopTx` with `quotaCost` from the ledger, read before the transaction begins. A settle error returns `startupCheckError{id, "stop_settlement"}`, which blocks startup: a marked row cannot be resumed and must not be skipped.
2. **The recovery query** adds `r.initiated_by, r.interrupt_cause` and `AND r.stop_requested_at IS NULL`.
3. **Skip rule.** `timeLimitRunNow := runType=='manual' && initiatedBy=='run_now' && status=='interrupted' && cause=='time_limit' && safety==1 && authority==1`. When a row fails the terminal-timestamp check or `validateResumableRow` and `timeLimitRunNow` holds: log at warning `startup recovery left unusable run` with `run_id`, `cause` (`discardCauseFor`) and `reason`, then `continue`. Otherwise return `startupCheckError` as today.

## 4. HTTP

### 4.1 `POST /api/runs/{id}/stop`

Handler order: parse `{id}` → `trackRun()` → `StopRun(r.Context(), id)` → `runWG.Done()` → respond.

| Outcome | Status | Body |
|---|---|---|
| Accepted | `202` | `{"run_id":<id>,"status":"running","stop":"accepted"}` |
| Repeat while stopping | `202` | `{"run_id":<id>,"status":"running","stop":"already_stopping"}` |
| Bad id | `400` | `{"error":"invalid run id","code":"invalid_run_id"}` |
| Shutting down | `503` | `{"error":"server is shutting down","code":"shutting_down","run_id":<id>}` |
| Not found | `404` | `{"error":"run <id> does not exist","code":"run_not_found","run_id":<id>}` |
| Ended | `409` | `{"error":"run <id> has already ended","code":"run_ended","run_id":<id>,"status":"completed\|failed\|stopped"}` |
| Not active | `409` | `{"error":"run <id> is not running","code":"run_not_active","run_id":<id>,"reason":"not_executing\|ending"}` |
| Not recorded | `500` | `{"error":"the stop was not recorded; run <id> continues","code":"stop_failed","run_id":<id>}` |

### 4.2 Run reads (`GET /api/runs`, `GET /api/runs/{id}`, `last_run` in `GET /api/overview`)

- `status` may be `stopped`.
- `stop_requested_at`: string or `null`.
- `discardable`: `true` only for the run `DiscardableRun` returns, `false` otherwise; `null` on every item when `DiscardableRun` errors (logged with `diag.Encode`).
- `discard_cause`: present only when `discardable` is `true`.
- `executing`: added by [ARS-004 §4](ars-004-mark-legacy-run-as-ended.md#4-run-reads-amends-ars-003-42) (2026-11-11): `true` only while this process holds the run's control. Stop is offered only when the status is `running` and `executing` is `true`.
- `endable`: added by [ARS-004 §4](ars-004-mark-legacy-run-as-ended.md#4-run-reads-amends-ars-003-42) (2026-11-11), and computed in the same view.

Any existing `status` query filter on `GET /api/runs` accepts `stopped` [unverified whether such a filter exists].

### 4.3 Discard (ARS-002 §3)

The response is unchanged. Its acknowledgement copy now says "deferred queue for the next run" (Designer).

## 5. UI implications (behavior only; Designer owns copy and layout)

- A Stop control on any run with `status='running'` and `stop_requested_at=null`, with confirmation (BR-035 AC2).
- Progress shows **Stopping** while `status='running'` and `stop_requested_at≠null`, and **Stopped by user** for `stopped`. Never "completed", "failed" or "running normally".
- Run history and filters gain `stopped`. Statistics are unchanged; they count only `completed`.
- A stopped simulation's results are labelled incomplete.
- Run Detail shows Discard when `discardable=true`, using `discard_cause` in the confirmation.
- Deferred-queue rows with reason `stopped_by_user` or `discarded_by_user` need labels.

## 6. Invariants

1. A marked row ends only as `stopped`; `stopped` is final (triggers).
2. Exactly one of {own terminal write, stop settle} happens for an executing run (`runControl`).
3. After a settled stop or discard of a production run R: no queue row has `status='retrying' AND retry_run_id=R`. Every unfinished video of R has at least one `pending` deferred item with `retry_after` NULL, unless removed (AC12). No new item exists beside an already pending one.
4. A handoff writes no `playlist_send_guard`, `executed_action` or quota row and makes no provider call.
5. Startup never resumes a marked row, never blocks on an unusable time-limit Run Now row, and still blocks on every other invalid row.
6. At most one run reads `discardable=true`.

## 7. Verification — the full test list

Real migrated SQLite unless marked *unit*. "Controlled provider" = the counting `httptest` YouTube seam with per-request pause and release hooks. "Unchanged" = byte-for-byte equality of the run row and of every `deferred_item`, `error_entry`, `playlist_send_guard`, `executed_action` and `run_video_outcome` row.

### 7.1 Migration and schema (`internal/store`)

1. **M0 spike:** on a modernc database at 024, the schema step succeeds; `integrity_check` = `ok`; a `stopped` insert succeeds only through the allowed path; `'bogus'` still fails; reopening keeps the CHECK. If this fails, switch to the fallback and run the same assertions plus `foreign_key_check` empty and every 024 trigger present.
2. **M1 upgrade:** 024 database with queue rows in every status → 025: `deferred_item` rows identical; `stop_requested_at` NULL everywhere; indexes present.
3. **M2 triggers:** setting the marker on non-`running` rows aborts; changing it aborts; a marked row moving to `completed`/`failed`/`interrupted` aborts; `stopped` without a marker aborts; leaving `stopped` aborts; inserting `stopped` aborts.
4. **M3 reasons:** both new `deferral_reason` values insert; an unknown value aborts. `diag` *unit*: the stop reason round-trips; `stop_run` with any other code and `stopped_by_user` with any other operation are rejected.
5. **M4 idempotent step:** `ApplyMigrations` twice → second step no-op; `schema_version` incremented once.

### 7.2 Handoff (`internal/queue`, `internal/pipeline`)

6. **H1 cases:** ended run R with unfinished videos:
   - (a) no queue item;
   - (b) a pending deferred item from another run with future `retry_after`;
   - (c) a deferred item R claimed;
   - (d) an Errors-queue entry R claimed;
   - (e) a deferred item the user discarded while R held the claim;
   - (f) an error entry the user dismissed while R held the claim;
   - plus committed accepted, rejected and failed videos.

   After the handoff:
   - (a) one new pending item with R's reason and `retry_after` NULL;
   - (b) the same row, `retry_after` NULL, nothing new;
   - (c) the same row `pending`, original id, `run_id`, reason and `retry_count`;
   - (d) entry `resolved` with `resolved_at` set and history kept, plus a new pending item;
   - (e) and (f) nothing new;
   - committed videos untouched;
   - no `retrying` row with `retry_run_id=R`;
   - guards and actions unchanged.
7. **H2 idempotency:** a second handoff for R changes nothing.
8. **H3 leftover claim:** a retrying row of R for a video with a committed outcome is released too; the postcondition holds.
9. **H4 atomicity:** a failure injected after step 2 rolls back steps 1–2.

### 7.3 Claims and next run (`internal/queue`, `internal/pipeline`)

10. **Q1 stopped owner reclaimable:** a retrying row owned by a `stopped` run does not fail `StartRun`, `AdmitRunNow` (new) or `RetryQueueEntry`, and is claimed by the new run.
11. **Q2 next run claims (AC14):** after a stop with handed-over videos, a scheduled `StartRun` whose discovery returns none of them claims every item, evaluates and acts on each, lists them in `GET /api/runs/{id}/videos`, adds the formerly unsent target, and sends zero POSTs to the uncertain target. Repeat with a Run Now new run, and with a discard instead of a stop.
12. **Q3 resume does not claim:** with a usable time-limit candidate, Run Now resumes it and the handed-over items stay `pending`.

### 7.4 Stop (`internal/pipeline`, `-race`)

13. **S1 each type (AC1, AC5, AC17):** stop a Run Now run, a scheduled run, a queue-retry run, a Run Now resume, a startup-resumed run, a dry run, a replay and a backtest. Each ends `stopped` with the stop reason. Production runs hand off; simulations write no queue, guard or action row, and their counters come from decisions. Statistics totals are unchanged; `executeHeld` re-entry on a stopped run is rejected.
14. **S2 no calls after accept (AC6):** pause the provider inside a discovery page, a metadata read and a playlist POST; stop; release. Zero requests start after the cancel timestamp.
15. **S3 guards (AC7–8):** one addition paused before dispatch and one after admission. The unsent guard row is gone with a `not_attempted` action (no attempt time); the sent one is `failed`/`mutation_application_uncertain` with its guard kept. A response released before settling records its real result (AC8 last sentence).
16. **S4 pre-attach stop:** stop a scheduled run that is registered but waiting on `productionMu`. When it acquires the lock it settles without any provider call.
17. **S5 settle failure:** inject failure into both settle attempts. The row stays `running` with a marker; `StopRun` answers `already_stopping`; restart settles it.
18. **S6 completing leaves no claims:** a normally completed run has no `retrying` row (supports the Findings row on completed owners).

### 7.5 Races and repeats (`-race`, ≥100 iterations each)

19. **R1 double press (AC20):** two concurrent `StopRun` → one `accepted`, one `already_stopping`, one handoff (H1 counts).
20. **R2 vs. completion, failure, time limit and shutdown (AC21):** release the run's last step concurrently with `StopRun`. Each iteration is either {own outcome saved, stop refused `run_ended` or `run_not_active/ending`, nothing changed} or {`stopped` with handoff}. Never `failed` + deferred items, never Errors-queue sinks + `stopped`.
21. **R3 vs. Run Now/StartRun/discard:** a stop settling under `productionMu` while Run Now presses and a scheduled start arrive: starts see the handed-over items only after the settle commit; a discard attempt during the settle is refused `production_run_active`.

### 7.6 Startup (`internal/pipeline`, `cmd/server` child process)

22. **U1 crash after ack (AC9):** kill the child process after `202` and before settling. Restart: the run is `stopped`, not resumed, with its handoff; a guard from an admitted in-flight POST is kept and the action shows as uncertain.
23. **U2 stop during recovery (AC19):** two recovered runs; stop the first while it resumes; it ends `stopped` and the worker resumes the second. A stop of the second before the worker reaches it → `run_not_active/not_executing`.
24. **U3 unusable time-limit rows (BR-017 AC11, BR-011 AC7):** seed an unusable oldest and an unusable younger time-limit Run Now row (each of the five validator reasons), a recoverable scheduled row and a usable time-limit row. The process starts and serves; the unusable rows are unchanged; the log names each run and cause with no raw text; the recoverable rows resume; the gate opens and the scheduler starts. Run Now is refused for the oldest unusable row, which then discards successfully.
25. **U4 other corruption still blocks:** a corrupt scheduled row (or an unusable row that is not time-limit Run Now) → startup exits non-zero, unchanged.
26. **U5 settle failure at startup:** a marked row whose settle fails → `stop_settlement` blocks startup.

### 7.7 Discard amendment and reads

27. **D1:** ARS-002 tests P1–P13 pass with the handoff. P5 and P8 now assert deferred items (H1 shapes) and zero Errors-queue entries created by the discard. P8 additionally asserts that the next run is blocked by the uncertain guard.
28. **V1 discardable (AC22):** with the U3 seeds, only the oldest unusable row reads `discardable=true` with its cause, on both `GET /api/runs/{id}` and the list. After its discard, or after an older time-limit row appears, the next read changes. A read failure yields `null`.

### 7.8 HTTP and composition

29. **X1 stop contract:** every row of §4.1 through the registered handler; `runWG` balanced; the shutdown refusal is driven by `WaitRuns`.
30. **X2 projection:** `stopped`, `stop_requested_at`, `discardable` and `discard_cause` round-trip in `GET /api/runs`, `GET /api/runs/{id}` and `GET /api/overview`.
31. **E1 stop journey (`-tags=integration ./e2e`):** real binary path. Start a scheduled-equivalent run, pause the provider mid-actions, stop it, release; then restart and run Now. The new run claims the handed-over videos and the provider insert count equals only the unsent targets.
32. **E2 SPA unit (`web/`):** stop and discard calls map every code; cancelling a confirmation sends nothing; Stopping and Stopped states render; `discardable` shows the control.

## 8. Findings

See ADR-004 Findings. Test M0 must run first; it gates the migration choice.

## 9. Implementation record and deviations

Implemented 2026-11-10 on PR #4 (backend only; the frontend, including `web/src/types.ts` §1.3, is not). Review is pending. Spec numbers in brackets.

**Result of the first task (test M0).** `writable_schema` works on modernc. The §1.2 schema step is the shipped mechanism; the 12-step fallback was not needed and is not implemented. `Open` and `ApplyMigrations` (including the in-memory `storetest` databases) run it.

**Deviations from the contract.**

- `settleStopTx` returns `(queue.HandoffResult, error)`, not `error`, so `settleStop` can log the counts [§3.3].
- `finishRun(ctx, runID)` takes the context, for logging and the detached finalization context [§3.3].
- `listUnfinishedRunVideosTx` wraps `unfinishedRunMembersTx`, which keeps the `retry-owned` flag that `sweepStrandedWork` needs. The `SELECT` is the extracted original [§2].
- `finishRun` also marks the control `finalizing` when the entry point returns without a stop. This closes a window where a stop could be accepted for a run that was already returning without writing a terminal state [§3.3].
- A user-stopped `RetryQueueEntry` returns `nil`: the stop is the user's own act, and `finishRun` settles it [§3.1].
- `queue.ErrCommit` wraps a failed `Commit` in `WithRetryTx` (ARS-002 §2.4 step 6). `Pipeline.discardTx` is an unexported test seam over that call; it is nil in production.
- `Machine.RunQuotaCost` is exported (ARS-002 §2.4 step 4). `RecomputeRunCountersFromDecisionsTx` was added next to `RecomputeRunCountersTx`.
- The run stream (`GET /api/runs/{id}/stream`) closes on the new `run.stopped` event, as it does on `run.completed` and `run.error`.
- `GET` run reads project `discardable`/`discard_cause` in the server (`internal/server/run_control.go`), by adding keys to the marshalled run, because `state.Run` has its own `MarshalJSON`. `Server`'s `Pipeline` interface gained `StopRun`, `DiscardRun` and `DiscardableRun`.
- The `status` filter on `GET /api/runs` (§4.2, marked unverified) does not exist; there is nothing to extend.
- Test R1 accepts `run_ended`/`stopped` as the second response when the second press arrives after settling; the spec's own table gives that answer to a late press.
- Existing tests changed only for the new schema version (24 → 25) and the quota migration fixture, which now has the `deferred_item` table and the `run.status` CHECK that migration 025 rewrites.

**What the tests prove, and what they do not.**

- Run at Go level against real SQLite: M0–M4 (`internal/store`), H1–H4, Q1 (`internal/queue`), the S, Q, R, U and D tests (`internal/pipeline`), X1/X2 (`internal/server`), and U1, U3 and E1 as child processes (`cmd/server`). Race tests R1, R2 (completion, time limit, shutdown), R3 and discard-versus-press run 100 iterations each under `-race`.
- U1 holds the "after the acknowledgement, before settling" window open with a trigger that fails the settle, then kills the child with SIGKILL. It does not time a kill against a settle that is in progress.
- S3 proves the admitted-POST branch (guard kept, action uncertain). The unsent-guard branch (released with `not_attempted`) rests on the existing ADR-007 §3.2 cancellation tests; no stop-level test pauses between the guard claim and the admission, because no seam exists there.
- Simulations (dry run, replay, backtest) are stopped before they start executing (the pre-attach case). A stop in the middle of a simulation's evaluation loop is not exercised.
- The scheduler-unchanged test (ARS-002 H2) was not written: `DiscardRun` and `StopRun` have no reference to the scheduler.
- The SPA tests (E2) belong to the frontend task.
- Live-provider, real-browser and long-run behavior are unproven.

