---
type: specification
title: ARS-004 — Mark Legacy Run as Ended — Endpoint, Interfaces, Saved Shape and Tests
description: Exact HTTP contract, pipeline and store interfaces, lock order, guarded status-and-reason write, end_legacy_run diagnostic, endable run-read field, refusal codes, idempotency and conformance tests for marking a stuck version-0 production run as ended.
tags: [architecture, specification, runs, legacy, run-14, end-run, api, concurrency, startup]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted — exact implementation contract. Implemented 2026-11-11, review pending (see §9). It realizes [ADR-005](../decisions/adr-005-mark-legacy-run-as-ended.md). It reuses [ARS-002](ars-002-discard-unusable-time-limit-run.md) §2.3 (`initiationMu`) and §2.4 step 6 (commit uncertainty), and [ARS-003](ars-003-stop-run-and-deferred-handoff.md) §3.1 (run control) and §4.2 (run-read projection). Nothing in those specifications changes, except that §4.2 gains one field.

## What the two sides agree on

After the user confirms for run `{id}`, the browser sends `POST /api/runs/{id}/end` with no body. The server either marks exactly that run `failed` with the end reason and answers `200`, or it refuses with a closed `code` and the `run_id`, and changes nothing. Run reads carry `endable`.

Example: `GET /api/runs/14` → `"status":"running","endable":true`. `POST /api/runs/14/end` → `200 {"run_id":14,"status":"failed","outcome":"ended"}`. `GET /api/runs/14` → `"status":"failed","interrupt_reason":"diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user","endable":false`. Any later `POST` → `409 {"code":"run_ended","run_id":14,"status":"failed",…}`.

## Derived from

[BR-036](../../../requirements/yuhaul/br-036-mark-legacy-run-as-ended.md) AC1–AC12; ADR-005; [Designer §Mark a Legacy Run as Ended](../../../design/yuhaul-admin-ui-design.md) (refusal cases and the machine reason it needs).

## Satisfies

See ADR-005's Satisfies table. This specification adds no behavior beyond it.

## Scope

In scope:

- `POST /api/runs/{id}/end`;
- `pipeline.EndLegacyRun`, `PrecheckEndLegacyRun` and `EndableRuns`;
- `state.Store.EndLegacyRunTx`;
- the `diag` additions;
- the `endable` read field;
- SPA request and response handling.

Out of scope: copy and layout (Designer); schema (no migration); startup recovery (unchanged, ADR-005 Decision 6).

## 1. Shared predicate and diagnostic

```go
// legacyEndableRow is the one SQL predicate for "a stuck legacy production
// row". Used verbatim by EndableRuns, the in-transaction recheck and EndLegacyRunTx.
const legacyEndableRow = `run_type IN ('manual','scheduled') AND mutation_safety_version = 0
  AND status IN ('running','interrupted') AND stop_requested_at IS NULL`
```

Define it once in `internal/state` and export it, or give `EndLegacyRunTx` and both pipeline reads one helper.

`internal/diag`:

- add operation `end_legacy_run` to `operations`;
- add code `ended_by_user` to `codes`;
- in `Facts.Valid`, extend the bound-operation branch so that `operation=end_legacy_run` ⇔ `code=ended_by_user`, with `Status`, `Reason` and `SQLite` empty.

Stored reason: `diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user`.

## 2. Interfaces

### 2.1 Store (package `state`)

```go
// EndLegacyRunTx writes the user's end of a stuck legacy run inside tx:
// status and reason only (ADR-005 Decision 1). Exactly one row must change.
func (s *Store) EndLegacyRunTx(ctx context.Context, tx *sql.Tx, id int64, reason string) error
```

```sql
UPDATE run SET status = 'failed', interrupt_reason = ?, interrupt_cause = NULL
WHERE id = ? AND <legacyEndableRow>
```

- The reason passes through `diag.GuardErrorText`.
- `RowsAffected != 1` → an error wrapping `ErrRunStopNotApplied` (the existing sentinel), and the caller rolls back.
- It does not write `completed_at`, `duration_seconds`, `quota_cost`, counters, `current_phase`, `started_at`, `config_version_id`, `initiated_by`, `selected_video_count` or `stop_requested_at`.

### 2.2 Pipeline (package `pipeline`)

```go
type EndResult struct{ RunID int64 }

type EndRefusedError struct {
    RunID   int64
    Code    string // run_not_found | run_ended | run_not_endable
    Status  string // run_ended only: completed | failed | stopped
    Reason  string // run_not_endable only: not_production | not_legacy | executing
    RunType string // run_not_endable/not_production only: dry_run | replay | backtest
}
func (e *EndRefusedError) Error() string // fixed: "run %d cannot be marked as ended: %s"

// EndNotAppliedError: the transaction did not commit; the run is unchanged.
// Status is the unchanged saved status when known: running | interrupted | "".
type EndNotAppliedError struct{ RunID int64; Status string }
var ErrEndNotApplied = errors.New("end did not take effect") // EndNotAppliedError.Is matches it
var ErrEndOutcomeUnknown = errors.New("end outcome could not be confirmed")

func (p *Pipeline) PrecheckEndLegacyRun(ctx context.Context, runID int64) error
func (p *Pipeline) EndLegacyRun(ctx context.Context, runID int64) (*EndResult, error)
func (p *Pipeline) EndableRuns(ctx context.Context) (map[int64]bool, error)
```

`ErrStartupRecoveryActive` and `ErrProductionRunActive` are reused. Add an unexported test seam `endTx`, nil in production, with the same shape as `discardTx`. Add `registeredRunIDs() []int64`, which copies the keys of `p.runs` under `runsMu`.

**Row classification** (`classifyEndRow(runType, status string, safety int) *EndRefusedError`), applied in this order:

1. no row → `run_not_found`;
2. `status ∈ {completed, failed, stopped}` → `run_ended` with `Status`;
3. `run_type ∉ {manual, scheduled}` → `run_not_endable/not_production` with `RunType`;
4. `mutation_safety_version ≠ 0` → `run_not_endable/not_legacy`;
5. otherwise nil. The status is now `running` or `interrupted`.

### 2.3 `PrecheckEndLegacyRun`

It does one `SELECT run_type, status, mutation_safety_version FROM run WHERE id=?` on `p.db`, without locks or a transaction, and returns `classifyEndRow`'s refusal or nil. A read error returns nil and is logged with `diag.Encode(err, "unknown", "end_legacy_run")`; the locked path decides. It never returns success. It only refuses, and only with facts that are permanent (ADR-005 Decision 3).

### 2.4 `EndLegacyRun` algorithm

1. `refuseWhileRecovering()` → `ErrStartupRecoveryActive`.
2. `p.initiationMu.Lock(); defer p.initiationMu.Unlock()`.
3. `if !p.productionMu.TryLock() { return nil, ErrProductionRunActive }`; `defer p.productionMu.Unlock()`.
4. `stored := diag.Facts{Phase:"unknown", Operation:"end_legacy_run", Code:"ended_by_user"}.String()`.
5. `runTx(ctx, func(tx) error { … })` (`endTx` or `qs.WithRetryTx`). In order:
   1. `SELECT run_type, status, mutation_safety_version FROM run WHERE id=?`. Apply `classifyEndRow` and return any refusal. Remember `status` as `prior`.
   2. `p.control(runID) != nil` → `run_not_endable/executing`.
   3. `ids := p.registeredRunIDs()`. If it is non-empty: `SELECT EXISTS(SELECT 1 FROM run WHERE id IN (…) AND run_type IN ('manual','scheduled'))`. True → `ErrProductionRunActive`.
   4. `p.stateStore.EndLegacyRunTx(ctx, tx, runID, stored)`.

   Any returned error rolls back. Nothing else is written.
6. Error mapping:
   - `*EndRefusedError` and `ErrProductionRunActive` → return as they are.
   - An error before commit → `&EndNotAppliedError{runID, prior}`. `prior` is empty if step 5.1 did not complete.
   - `queue.ErrCommit` → re-read `SELECT status, interrupt_reason FROM run WHERE id=?` (the locks are still held). If the status is `failed` and the reason equals `stored`, it is a success. A `running` or `interrupted` status gives `EndNotAppliedError{runID, status}`. A read error, or any other state, gives `ErrEndOutcomeUnknown`.
7. Success: log `"legacy run marked as ended"` with `run_id` and `prior_status`, then return `{runID}`. No SSE event is required, and the SPA reloads.

`EndLegacyRun` never calls `executeHeld`, `Execute`, `StartRun`, `ResumeRun`, `InterruptRun`, `FailRun`, `RunQuotaCost`, `listUnfinishedRunVideosTx`, `HandOffUnfinishedTx`, `RecomputeRunCounters*`, the scheduler, any auth client or any provider client.

### 2.5 `EndableRuns`

1. `p.producerGate.Load() == producersGated` → return an empty map with a nil error.
2. `BeginTx(ctx, &sql.TxOptions{ReadOnly: true})`, then:
   - `ids := p.registeredRunIDs()`. If any of them is a `manual` or `scheduled` row (the same `EXISTS` as §2.4 step 5.3), return an empty map.
   - `SELECT id FROM run WHERE <legacyEndableRow>`. Add each `id` for which `p.control(id) == nil`.
3. Any error → `(nil, err)`.

It takes no lock other than `runsMu` and does not call `TryLock`.

## 3. HTTP contract — `POST /api/runs/{id}/end`

It is registered on the API mux next to `POST /api/runs/{id}/discard`, so it has the same access as Run Now. The body is ignored. The handler order is:

1. parse `{id}`;
2. `PrecheckEndLegacyRun`; a refusal is answered at once;
3. `trackRun()`;
4. `EndLegacyRun(r.Context(), id)`;
5. `runWG.Done()`;
6. respond.

The action runs synchronously in the request.

| Outcome | Status | Body |
|---|---|---|
| Ended | `200` | `{"run_id":<id>,"status":"failed","outcome":"ended"}` |
| `{id}` not a positive integer | `400` | `{"error":"invalid run id","code":"invalid_run_id"}` |
| Not found | `404` | `{"error":"run <id> does not exist","code":"run_not_found","run_id":<id>}` |
| Already ended (incl. repeats) | `409` | `{"error":"run <id> has already ended","code":"run_ended","run_id":<id>,"status":"completed\|failed\|stopped"}` |
| Not a production run | `409` | `{"error":"run <id> cannot be marked as ended","code":"run_not_endable","run_id":<id>,"reason":"not_production","run_type":"dry_run\|replay\|backtest"}` |
| Not a legacy run | `409` | `{…,"code":"run_not_endable","run_id":<id>,"reason":"not_legacy"}` |
| Executing now | `409` | `{…,"code":"run_not_endable","run_id":<id>,"reason":"executing"}` |
| Shutting down | `503` | `{"error":"server is shutting down","code":"shutting_down","run_id":<id>}` |
| Startup recovery | `503` | `{"error":"<ErrStartupRecoveryActive text>","code":"startup_recovery_active","run_id":<id>}` |
| Production run active | `409` | `{"error":"a production run is already active","code":"production_run_active","run_id":<id>}` |
| Not applied | `500` | `{"error":"the end did not take effect; run <id> is unchanged","code":"end_failed","run_id":<id>,"status":"running\|interrupted"}` (`status` omitted when unknown) |
| Outcome unknown | `500` | `{"error":"could not confirm whether run <id> was marked as ended; reload the run","code":"end_outcome_unknown","run_id":<id>}` |

The refusal precedence follows the check order:

1. the permanent row refusals (not found, ended, not production, not legacy);
2. shutting down;
3. startup recovery;
4. production run active (lock);
5. the same row refusals again, under the lock;
6. executing;
7. production run active (registry).

Each reported refusal is true. Responses and logs carry only these closed values and the run ID.

## 4. Run reads (amends ARS-003 §4.2)

`GET /api/runs`, `GET /api/runs/{id}` and the overview's `last_run` add:

- `executing`: `true` while this process holds an in-process run control for the run (`Pipeline.ExecutingRuns`), `false` otherwise. It is always present, because it needs no database read and cannot fail. A saved status of `running` does not imply it: a stuck legacy run is saved as running and is not executing.
- `endable`: `true` for a run in `EndableRuns`' set, `false` otherwise. It is `false` for every run once `trackRun` would refuse; add a read-only `Server.shuttingDown()` that checks `closing || runBase.Err() != nil` under `runMu`. It is `null` on every item when `EndableRuns` errors, logged with `diag.Encode(err, "unknown", "inspect_run")`.

The server computes `endable` and `executing` once per request, together with `DiscardableRun`, in one `runReadView` that `projectRun` reads. `web/src/types.ts` `Run` also gains `executing?: boolean`. `discardable` and `discard_cause` are unchanged. `web/src/types.ts` `Run` gains `endable: boolean | null`.

## 5. SPA obligations (behavior only; the Designer owns copy and layout)

- Offer Stop only when `canOfferStop(run)` is true: the saved status is `running` and `executing === true`. One shared predicate serves Run List, Run Detail, Overview and `StopRunControl`. An absent `executing` (older server) offers no Stop. Run 14 therefore never shows Stop, whether or not another production run is active.
- A run with `endable === true` is not shown as "Run in progress", is not polled as progressing, and does not hold back Run Now.
- Offer the action only on Run Detail when `endable === true`.
- Send the request only after confirmation. Cancelling sends nothing.
- On `200`, reload the run.
- On a refusal, branch on `code`, `reason`, `status` and `run_type`, never on `error` text, and reload the run.
- On `end_outcome_unknown` or a network failure, reload the run before offering anything again.
- A run whose `interrupt_reason` parses with `operation=end_legacy_run` displays as "ended by user (legacy run)". One that parses with `operation=discard_run` displays as "discarded by user".

## 6. Invariants

1. A successful end changes exactly `status` (to `failed`), `interrupt_reason` (to the end reason) and `interrupt_cause` (NULL to NULL) of one row that matches `legacyEndableRow`.
2. A refused or failed end changes no row.
3. No provider request, auth read, guard, action, decision, outcome, video, queue or counter write.
4. An ended row is outside every resume, recovery and stop-settlement selector, and never makes startup or a start fail.
5. Repeats are refused as `run_ended` before any gate or lock.
6. The action and `EndableRuns` agree, apart from the millisecond window documented in ADR-005 Decision 5.

## 7. Verification — tests the implementation must pass

Tests use real migrated SQLite unless marked *unit*. The "controlled provider" is the counting `httptest` YouTube seam. A "snapshot" is a byte-for-byte copy of every row of these tables:

- `run`;
- `video`;
- `run_video_outcome`;
- `route_decision`;
- `executed_action`;
- `playlist_send_guard`;
- `deferred_item`;
- `error_entry`;
- the quota ledger.

**Seeds** (S):

- L14, run-14-shaped: version 0, `manual`, `initiated_by` NULL, `running`, `current_phase='discovery'`, `completed_at` NULL, nonzero counts, unevaluated videos.
- L2: version 0, `scheduled`, `interrupted`, with a prose `interrupt_reason`.
- A pending deferred item for an L14 video.
- An error entry and a deferred item that L14 holds as `retrying`.
- A retained guard and an uncertain action of another, version-1 run on an L14 video.
- Version-1 rows: `running` with a control, `interrupted` (time limit, usable), `interrupted` (scheduled).
- Version-0 `interrupted` simulations: a dry run, a replay and a backtest.
- `completed`, `failed`, discarded and `stopped` rows.

### 7.1 Vocabulary and store

1. **V1** *(unit)*: the end reason round-trips through `AdmitStored` and `GuardErrorText`. These are rejected: `end_legacy_run` with any other code, `ended_by_user` with any other operation, and either with `status`, `reason` or `sqlite`. The existing `discard_run` and `stop_run` cases still pass.
2. **T1 store guard:** `EndLegacyRunTx` on L14 changes only `status` and `interrupt_reason`, and `completed_at` stays NULL. It returns an error with `RowsAffected=0`, and the row unchanged, on a version-1 row, a simulation, a terminal row, and a row with a stop marker (seeded by direct SQL on a `running` row).

### 7.2 Pipeline

3. **P1 ending (AC1, AC4–7):** end L14 and L2. In the snapshot diff, only those two rows change, in `status`, `interrupt_reason`, and `interrupt_cause` (NULL→NULL). Every other table is identical. This includes the retrying claims, the guard and the uncertain action. The controlled provider records zero requests, and no auth binding is made. `GET /api/runs/{id}` shows the end reason, distinct from a discarded row, a stopped row and a self-failed row.
4. **P2 refusal matrix (AC9):** check every seed, plus a missing ID, through both `PrecheckEndLegacyRun` and `EndLegacyRun`. Each gives its code, reason, status or `run_type`, and the snapshot is unchanged with zero provider requests.
5. **P3 executing:** register a control for L14 through the test seam → `run_not_endable/executing`, unchanged.
6. **P4 production active:**
   - (a) `Execute` of a version-1 run holds `productionMu` → `ErrProductionRunActive` within a bounded wait.
   - (b) A registered version-1 control with the lock free (the gap after `StartRun`) → `ErrProductionRunActive`.
   - (c) A registered simulation control alone does not refuse.

   Each leaves the snapshot unchanged.
7. **P5 gates:** with the startup gate closed → `ErrStartupRecoveryActive`, with no lock attempt (`initiationMu` stays free), unchanged.
8. **P6 atomicity (AC4):** a `BEFORE UPDATE ON run` abort trigger → `EndNotAppliedError{Status:"running"}`, with the row unchanged and still endable.
9. **P7 commit uncertainty:**
   - a commit seam that fails after committing → success;
   - one that fails without committing → `EndNotAppliedError`;
   - with a failing re-read → `ErrEndOutcomeUnknown`.
10. **P8 idempotency (AC10):** two sequential ends → the second gives `run_ended/failed`, and the row is identical after both. Then hold `productionMu` with a running version-1 `Execute` and end L14 again: the answer is `run_ended`, not `production_run_active`. Repeat with the startup gate closed: still `run_ended`.
11. **P9 races (AC10–11, `-race`, ≥100 iterations each):**
    - (a) Two concurrent ends of L14: exactly one success and one `run_ended`.
    - (b) End of L14 vs. `AdmitRunNow`, with no version-1 candidate. Each iteration is either {end succeeds, press starts a new run} or {press starts and holds the lock, end refused `production_run_active` with L14 unchanged}.
    - (c) End vs. a scheduler `StartRun`: the same two orders.
    - (d) End of L14 vs. Discard of a discardable version-1 row: both succeed, in either order.

    Never a mixture.

### 7.3 Startup and later starts (AC8)

12. **U1 startup after ending** (`cmd/server` child process): seed S plus a recoverable version-1 `interrupted` scheduled row. End L14 and L2 over HTTP. Restart twice, once after SIGTERM and once after SIGKILL. Each time:
    - the process starts and serves, and the gate opens;
    - `classifyStartupRuns` returns only the version-1 recoverable rows, and they resume;
    - no `startup recovery blocked` or `left unusable run` log names L14 or L2;
    - L14 and L2 are identical to their post-end snapshot (`completed_at` still NULL);
    - the scheduler starts.

    Also *(pipeline)*: classification with an ended row whose `completed_at` is NULL and another whose `completed_at` is set (seeded) ignores both.
13. **U2 never resumed, never blocking:** after ending, each of these succeeds:
    - a Run Now press, which never selects L14 or L2;
    - a scheduled `StartRun`;
    - `RetryQueueEntry` for a pending item.

    The `retrying` claims that L14 held do not fail any start: they are reclaimable as a `failed` owner (ADR-005 Findings). L14 and L2 stay `failed`.

### 7.4 Reads and HTTP

14. **R1 `endable` (AC12):** with S, `endable` is true only for L14 and L2, on `GET /api/runs`, `GET /api/runs/{id}` and `last_run`. It becomes false for all runs in each of these cases:
    - while a version-1 run is registered;
    - while the gate is closed;
    - after `WaitRuns` begins;
    - for L14, after L14 is ended.

    It is `null` on every item when the read fails. `discardable` gives the same values as before this change.
15. **R2 stale true:** read `endable:true` for L14, start a production run, then `POST /end` → `production_run_active`, unchanged.
16. **H1 contract:** every row of the §3 table through the registered handler. `runWG` is balanced in every branch, and `WaitRuns` returns. During shutdown, an ended run answers `run_ended` and an endable run answers `shutting_down`.
17. **H2 no side effects:** the scheduler's next fire time is unchanged, and no SSE run event is emitted.

### 7.5 Composition and browser

18. **E1 journey (`-tags=integration ./e2e`):** use the real binary with a database seeded with L14 and the controlled provider.
    - `GET /api/runs/14` shows `endable:true`.
    - `POST /api/runs/14/end` → `200`, with zero provider requests.
    - A repeat → `409 run_ended`.
    - Restart, then Run Now → `202 admission=started`.
    - L14 is still `failed` with the end reason.
19. **E2 SPA unit (`web/`):**
    - the end call maps `200` and every code, reason and `run_type`;
    - cancelling sends nothing;
    - the run reloads after any response or a network error;
    - the control renders only for `endable === true`;
    - `operation=end_legacy_run` renders as a user end, distinct from a discard.

## 8. Findings

See ADR-005 Findings. None blocks implementation.

## 9. Implementation record and deviations

Implemented 2026-11-11 on branch `yutube-connect-problems-debug` (PR #4). Review pending. The queue-claim finding was confirmed by the user the same day: items the legacy run had already claimed become reclaimable once it is ended.

- **Code:** `internal/diag` (operation and code), `internal/state/store.go` (`LegacyEndableRow`, `EndLegacyRunTx`), `internal/pipeline/end_legacy.go`, `internal/server/run_control.go` (`handleEndRun`, `runReadView`), `web/src/components/EndLegacyRunControl.tsx`, `web/src/lib/run-ending.ts`, `canOfferStop` in `web/src/types.ts`.
- **Additions beyond this specification:** the run read's `executing` field and the shared `canOfferStop` predicate (§4, §5). They fix Stop showing for run 14 because its saved status is `running`.
- **Names:** `LegacyEndableRow` is exported from `state` so both packages share one SQL text. The refusal constants are `EndRunNotFound`, `EndRunEnded`, `EndRunNotEndable`, `EndReasonNotProduction`, `EndReasonNotLegacy`, `EndReasonExecuting`. `ExecutingRuns() map[int64]bool` is the exported read of the registry.
- **Tests:** V1 `internal/diag`; T1 `internal/state/run_end_test.go`; P1–P9, U1 classification, U2, R1/R2, H2 `internal/pipeline/end_legacy_integration_test.go` (`integration` tag); H1 and the read projection `internal/server/run_control_test.go`; U1 and E1 as a real process with restarts after SIGTERM and SIGKILL `cmd/server/end_legacy_run_integration_test.go`; E2 under `web/src`.
- **Not covered:** U2's queue retry (`RetryQueueEntry`) is not exercised after an end; Run Now and a scheduled start are. U1 restarts with a completed recoverable row, not a still-recoverable one, so classification with a live recoverable row is covered at pipeline level only. No browser, screen-reader or 320 px pass.
- **Stop for a marked run that no control holds:** a run saved as running with a stop marker but no control (a crash between accepting a stop and settling it) no longer renders the "Stopping run {id}…" text in the control, because `canOfferStop` needs `executing`. The status text elsewhere still says Stopping.
