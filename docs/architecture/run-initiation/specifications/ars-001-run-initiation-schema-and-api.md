---
type: specification
title: ARS-001 — Run Initiation Schema and API — Run Now Resume and Published-Date Range
description: Exact migration 024, pipeline interfaces, HTTP contracts, failure responses and conformance tests for resuming a time-limit-interrupted Run Now run and for filtering Backtest/Replay by YouTube published date.
tags: [architecture, specification, runs, manual, recovery, backtest, replay, date-range, migration, api]
timestamp: 2026-11-10T00:00:00Z
---

**Status:** accepted — exact implementation contract. Implemented 2026-11-10; independent review pending. Deviations from the text below are listed in [§10](#10-implementation-record-and-deviations). It realizes [ADR-001](../decisions/adr-001-run-now-resumes-time-limit-interrupted-manual-run.md) and [ADR-002](../decisions/adr-002-backtest-replay-published-date-range.md).

## What the two sides agree on

**Run Now.** The browser sends `POST /api/runs` with no body, as it does today. The server either resumes the oldest Run Now run stopped by its time limit, starts a new run, or refuses with a reason. The response says which of the three happened. Example: run 30 stopped at its time limit, so the press returns `202 {"run_id":30,"status":"running","admission":"resumed"}`. If run 30's snapshot is missing, the press instead returns `409 {"code":"resume_unavailable","run_id":30,"reason":"snapshot_unavailable",…}`; no run is started and run 30 is unchanged.

**Date range.** The Backtest view sends `published_from`/`published_to` as `"YYYY-MM-DD"` or `null`, or omits them, on both `POST /api/backtest` and `POST /api/runs/replay`. The server rejects a bad range with `400` before anything exists. Every run read returns the saved range. Example: `{"published_from":"2026-11-02","published_to":"2026-11-01"}` returns `400 {"code":"invalid_date_range","error":"invalid date range: the start day must not be after the end day"}`, and the run list is unchanged.

## Derived from

- [BR-017 AC7–12](../../../requirements/yuhaul/br-017-manual-run-initiation.md) and [BR-027 AC8–13](../../../requirements/yuhaul/br-027-historical-backtesting.md), both accepted 2026-11-10.
- ADR-001 and ADR-002 in this topic; [authorization ADR-005/ADR-007](../../youtube-authorization/index.md) for startup classification and the producer gate; [run-diagnostics ARD-001](../../run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md) for the write-once selected count; [decision-evidence ARS-001](../../decision-evidence/specifications/ars-001-current-local-run-video-metadata.md) for the local-metadata release rule.

## Satisfies

See the Satisfies tables in ADR-001 (BR-017 AC5–12, BR-033) and ADR-002 (BR-027 AC4, AC7–13, BR-015 AC7). This specification adds no behavior beyond them.

## Scope

In scope: migration 024; the `state` and `pipeline` interfaces named below; `POST /api/runs`, `POST /api/backtest`, `POST /api/runs/replay`, `GET /api/runs` and `GET /api/runs/{id}`; the SPA request/response handling these contracts require. Out of scope: copy and layout (Designer), the scheduler path, queue retry semantics, dry-run, startup recovery behavior, and the time-limit duration.

## 1. Schema — migration `024_run_initiation_and_published_range.up.sql`

Forward-only, like 019–023. Existing rows get `NULL` in every new column and are not backfilled.

```sql
ALTER TABLE run ADD COLUMN initiated_by TEXT;      -- 'run_now' | 'schedule' | 'queue_retry' | NULL
ALTER TABLE run ADD COLUMN interrupt_cause TEXT;   -- 'time_limit' | 'shutdown' | NULL
ALTER TABLE run ADD COLUMN published_from TEXT;    -- 'YYYY-MM-DD' | NULL
ALTER TABLE run ADD COLUMN published_to TEXT;      -- 'YYYY-MM-DD' | NULL
```

Triggers, following the repository's trigger-guard pattern (migrations 021/022). Each `RAISE(ABORT, …)` message is fixed text.

| Trigger | Fires | Rejects when |
|---|---|---|
| `run_initiated_by_insert` | `BEFORE INSERT ON run` | `NEW.initiated_by IS NOT NULL AND NOT ((NEW.initiated_by IN ('run_now','queue_retry') AND NEW.run_type = 'manual') OR (NEW.initiated_by = 'schedule' AND NEW.run_type = 'scheduled'))` |
| `run_initiated_by_immutable` | `BEFORE UPDATE OF initiated_by ON run` | `OLD.initiated_by IS NOT NEW.initiated_by` |
| `run_interrupt_cause_check` | `BEFORE INSERT ON run` and `BEFORE UPDATE OF interrupt_cause ON run` (two triggers) | `NEW.interrupt_cause IS NOT NULL AND (NEW.interrupt_cause NOT IN ('time_limit','shutdown') OR NEW.status <> 'interrupted')` |
| `run_published_range_insert` | `BEFORE INSERT ON run` | either bound is non-NULL and any of: `NEW.run_type NOT IN ('backtest','replay')`; a non-NULL bound fails `typeof(x)='text' AND length(x)=10 AND x GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]' AND IFNULL(date(x) = x, 0)`; both non-NULL and `NEW.published_from > NEW.published_to` |
| `run_published_range_immutable` | `BEFORE UPDATE OF published_from, published_to ON run` | `OLD.published_from IS NOT NEW.published_from OR OLD.published_to IS NOT NEW.published_to` |

`date(x) = x` is a secondary guard. The Go parser (§3.1) is authoritative for calendar validity. SQLite's `date()` rejects `2026-02-30` (verified at implementation; the trigger aborts). The migration test records that behavior, and the application still never depends on it. Note that `run_interrupt_cause_check` fires only when `interrupt_cause` is written. A later status change (for example `FailRun` on an interrupted row) may leave a stale cause on a non-interrupted row; it is harmless because eligibility also requires `status='interrupted'` (§2.2).

### 1.1 `state.Run` fields

```go
InitiatedBy    *string `json:"-"`                // internal; closed values above
InterruptCause *string `json:"-"`                // internal; closed values above
PublishedFrom  *string `json:"-"`                // projected through PublishedRange below
PublishedTo    *string `json:"-"`
```

`createRunWithGuard`'s `INSERT` writes all four columns. `ListRuns`, `GetRun` and `scanRun` read them. `MarshalJSON` adds `published_range` (§4.4). `initiated_by` and `interrupt_cause` are not part of the public JSON.

### 1.2 Writers

| Writer | Change |
|---|---|
| `StartRun(ctx, manual)` and the new-run branch of `AdmitRunNow` | `initiated_by='run_now'` |
| `StartRun(ctx, scheduled)` | `initiated_by='schedule'` |
| `RetryQueueEntry` | `initiated_by='queue_retry'` |
| `Replay`, `Backtest`, `DryRun` | `initiated_by=NULL`; Replay/Backtest write the validated range |
| New `Store.InterruptRunWithCause(ctx, id, reason string, cause string)` | `UPDATE run SET status='interrupted', interrupt_reason=?, interrupt_cause=? WHERE id=?`, with `diag.GuardErrorText` on the reason, exactly like `InterruptRun` |
| `Store.InterruptRun` (existing) | Also sets `interrupt_cause = NULL` |
| `Store.ResumeRun` (existing) | Also sets `interrupt_cause = NULL` |
| Startup reclassification `UPDATE` (`startup_recovery.go:148`) | Also sets `interrupt_cause = NULL`; otherwise unchanged |
| `failRun` ended-context branch (`pipeline.go:531–537`) | Computes `cause := interruptCauseFor(ctx)` and calls `InterruptRunWithCause` when the cause is non-empty, otherwise `InterruptRun`. `interruptCauseFor` returns `"time_limit"` iff `errors.Is(context.Cause(ctx), ErrRunTimeLimit)`, `"shutdown"` iff `errors.Is(ctx.Err(), context.Canceled)`, else `""`. Both writes use `runFinalizationContext`, as `interruptAuthorityRun` does today. |

No other path writes `interrupt_cause`.

## 2. Pipeline interfaces

### 2.1 Sentinels and errors (package `pipeline`)

```go
var ErrRunTimeLimit = errors.New("run execution time limit reached")   // context cause only; never stored as text
var ErrProductionRunActive = errors.New("a production run is already active")
var ErrInvalidDateRange = errors.New("invalid date range")

type ResumeUnavailableError struct {
    RunID  int64
    Reason string // closed: snapshot_unavailable | snapshot_unreadable | snapshot_inadmissible |
                  //         checkpoint_unrecognized | terminal_timestamp_contradiction | recheck_at_write
}
func (e *ResumeUnavailableError) Error() string // fixed: "run %d cannot be resumed: %s"
```

### 2.2 Run Now admission

```go
type RunNowAdmission struct { /* unexported: runID, resumed bool, held lock, pipeline */ }
func (a *RunNowAdmission) RunID() int64
func (a *RunNowAdmission) Resumed() bool
func (a *RunNowAdmission) Execute(ctx context.Context) // runs executeHeld, then releases the lock exactly once
func (a *RunNowAdmission) Release()                    // releases without executing; no-op after Execute/Release

func (p *Pipeline) AdmitRunNow(ctx context.Context) (*RunNowAdmission, error)
```

`AdmitRunNow` algorithm, in order:

1. `refuseWhileRecovering()` → `ErrStartupRecoveryActive`.
2. `if !p.productionMu.TryLock() { return nil, ErrProductionRunActive }`.
3. Begin one SQLite transaction (`queue.Store.WithRetryTx` or equivalent single writer transaction). Read the candidate:

   ```sql
   SELECT r.id, r.completed_at, r.current_phase, r.config_version_id, c.snapshot
   FROM run r LEFT JOIN config_version c ON c.id = r.config_version_id
   WHERE r.run_type = 'manual' AND r.initiated_by = 'run_now'
     AND r.status = 'interrupted' AND r.interrupt_cause = 'time_limit'
     AND r.mutation_safety_version = 1 AND r.action_authority_version = 1
   ORDER BY r.started_at ASC, r.id ASC
   LIMIT 1
   ```

4. **Candidate found:** validate it with `validateResumableRow(completedAt, phase, configVersionID, snapshot) (reason string)`. This function is extracted from `classifyStartupRuns` and used by both. Mapping, checked in this order:
   - `completed_at` not NULL → `terminal_timestamp_contradiction`;
   - `config_version_id` NULL or snapshot NULL → `snapshot_unavailable`;
   - snapshot is not a JSON object, or fails to decode → `snapshot_unreadable`;
   - `validateSnapshotRoutes` fails → `snapshot_inadmissible`;
   - `current_phase` is non-NULL and not one of `discovery`, `exclusion`, `duplicate_detection`, `routing`, `action_execution` → `checkpoint_unrecognized`. A NULL phase is a recognized "not yet started" checkpoint, as at startup.

   Startup maps these to its existing check names (`terminal_timestamp_contradiction`, `authority`, `snapshot_readable`, `snapshot_admissible`, `checkpoint`), so its observable behavior is unchanged.

   On a non-empty reason: roll back, unlock, and return `&ResumeUnavailableError{id, reason}`. Otherwise run `UPDATE run SET status='running', interrupt_reason=NULL, interrupt_cause=NULL WHERE id=? AND status='interrupted' AND interrupt_cause='time_limit' AND initiated_by='run_now' AND completed_at IS NULL`. If `RowsAffected != 1`: roll back, unlock, and return reason `recheck_at_write`. Commit. A commit error unlocks and is returned as an ordinary error (HTTP 500). Return `{runID, resumed:true}`.

5. **No candidate:** commit or roll back the read-only transaction. Then run the existing `StartRun` body (`prepareProductionRun(manual)`, `CreateGuardedMutationRunTx` with `initiated_by='run_now'`, deferred-group claims) **without** taking `productionMu` again. Broadcast `EventRunStarted` as today. Return `{runID, resumed:false}`. On error, unlock and return the error.

`executeHeld(ctx, runID, resumeAdmitted bool)` is the current `Execute` body without `productionMu.Lock/Unlock`. `Execute(ctx, id)` becomes `Lock(); defer Unlock(); executeHeld(ctx, id, false)`. In `executeHeld`, `isResume := run.Status == interrupted || resumeAdmitted`. The `ResumeRun` call runs only when the status is `interrupted`. When `isResume` is true, `startPhase` comes from `current_phase` (NULL → `discovery`). No other change to `Execute`.

`StartRun` keeps its signature and blocking behavior for the scheduler. The server no longer calls it for Run Now.

### 2.3 Date range (package `state`)

```go
type PublishedRange struct {
    From *string // canonical "YYYY-MM-DD" or nil
    To   *string
}
func ParsePublishedRange(from, to *string) (PublishedRange, *RangeError)
type RangeError struct { Field string; Kind string } // Field: "published_from"|"published_to"|""; Kind: "invalid_date"|"reversed"
func (r PublishedRange) IsSet() bool
func (r PublishedRange) Bounds() (lower, upper *time.Time) // lower = From 00:00Z; upper = To+1day 00:00Z (exclusive)
func (r PublishedRange) Contains(t time.Time) bool         // uses t.UTC(); only present bounds apply
```

`ParsePublishedRange` uses `time.Parse("2006-01-02", s)` in UTC. It then requires `parsed.Format("2006-01-02") == s`, which rejects non-canonical forms such as `2026-1-05`. It checks `From` before `To`, and the order only when both are valid.

Pipeline signatures change:

```go
Replay(ctx context.Context, configVersionID *int64, rng state.PublishedRange) (*state.Run, error)
Backtest(ctx context.Context, proposed *config.ProposalSnapshot, rng state.PublishedRange) (*state.Run, error)
```

Each re-validates `rng` first and returns an error wrapping `ErrInvalidDateRange` before calling `resolveReplayConfig`, `ingestProposalRun` or `CreateRun`. The run struct carries the range into the same `INSERT`.

### 2.4 Selection

New store read: `ListRetainedVideosWithPublishedLocal(ctx) ([]RetainedVideo, error)`, where `RetainedVideo` is a `Video` plus raw `PublishedAt`, `ExpiresAt` and `ExpiredAt` as `sql.NullString` and `IsActive sql.NullInt64`:

```sql
SELECT v.id, v.video_id, v.channel_id, v.discovered_at, v.discovered_in_run_id,
       v.processing_state, v.final_outcome, v.deferred_reason, v.error_message, v.completed_at,
       CAST(tm.published_at AS TEXT), tm.is_active, CAST(tm.expires_at AS TEXT), CAST(tm.expired_at AS TEXT)
FROM video v LEFT JOIN transient_metadata tm ON tm.video_id = v.video_id
ORDER BY v.discovered_at
```

In `executeHistorical`, replace the `ListRetainedVideos` call:

- If `!rng.IsSet()`: keep `ListRetainedVideos` exactly as today.
- Else: read the rows above; `cutoff := time.Now().UTC()`, taken once; keep a row iff `IsActive=1 AND ExpiredAt IS NULL AND decode(ExpiresAt) ok AND decoded.After(cutoff) AND decode(PublishedAt) ok AND rng.Contains(decoded)`. `decode` tries, in order: `time.RFC3339Nano`; `"2006-01-02 15:04:05.999999999 -0700 MST"`; `"2006-01-02 15:04:05.999999999 -0700 -0700"` (added at implementation, see §10); `"2006-01-02 15:04:05.999999999-07:00"`; `"2006-01-02 15:04:05"` (as UTC). A failure means "no date"; it is never an error.
- In both branches, `captureSelectedCount(ctx, runID, len(selected))` runs before evaluation. A read error calls `failHistoricalRun` before capture, as today.

No call to `p.meta`, `metadata.Manager`, any refresh or any provider client may be made by selection.

## 3. HTTP contracts

All error bodies keep the existing `{"error": "<message>"}` field and add `"code"` where shown. Add a `writeErrorCode(w, status, code, message, extra map[string]any)` helper; other handlers are unchanged.

### 3.1 `POST /api/runs` (Run Now)

The request body is ignored, as today. The handler order is: `trackRun()` → `AdmitRunNow(r.Context())` → goroutine `{ defer runWG.Done(); ctx, cancel := context.WithTimeoutCause(s.runBase, 30*time.Minute, pipeline.ErrRunTimeLimit); defer cancel(); admission.Execute(ctx) }`. On any admission error, call `runWG.Done()` before responding.

| Outcome | Status | Body |
|---|---|---|
| New run | `202` | `{"run_id": <new>, "status": "running", "admission": "started"}` |
| Resumed | `202` | `{"run_id": <resumed>, "status": "running", "admission": "resumed"}` |
| Shutting down | `503` | `{"error": "server is shutting down"}` (unchanged) |
| Startup recovery | `503` | `{"error": "<ErrStartupRecoveryActive text>"}` (unchanged) |
| Production active | `409` | `{"error": "a production run is already active", "code": "production_run_active"}` |
| Candidate unusable | `409` | `{"error": "run <id> stopped at its time limit but cannot be resumed safely; no new run was started", "code": "resume_unavailable", "run_id": <id>, "reason": "<closed reason>"}` |
| Other | `500` | `{"error": "failed to start run"}` (unchanged) |

The `admission` field is new; older clients that ignore it keep working. For a refusal, the server logs only the closed code, run ID and reason, never raw errors (run-diagnostics ARS-001).

### 3.2 `POST /api/backtest`

The body adds two optional members next to `proposed_configuration`:

```json
{ "proposed_configuration": { … }, "published_from": "2026-10-01", "published_to": null }
```

Each member may be absent, `null` or a string. Any other JSON type, or a string that fails `ParsePublishedRange`, is invalid. An empty string is invalid; the SPA omits an empty control. Validation runs right after body decode and before `req.snapshot()`, threshold and action checks.

| Outcome | Status | Body |
|---|---|---|
| Accepted | `202` | existing body (`run_id`, `run_type`, `status`) |
| Reversed range | `400` | `{"error": "invalid date range: the start day must not be after the end day", "code": "invalid_date_range", "field": null}` |
| Invalid day | `400` | `{"error": "invalid date range: <field> must be a valid calendar date (YYYY-MM-DD)", "code": "invalid_date_range", "field": "<field>"}` |

Existing errors are unchanged.

### 3.3 `POST /api/runs/replay`

The range is read **only** from the JSON body (`published_from`, `published_to`), with the same rules as §3.2. The handler now decodes the body even when `config_version_id` is given as a query parameter; an empty body (`EOF`) means no range. If `published_from` or `published_to` appears as a **query** parameter, the handler returns `400 invalid_date_range` with `"error": "invalid date range: send published_from/published_to in the request body"`, so a range is never silently ignored. Range validation runs before `Replay`, so no config lookup or run creation happens for an invalid range.

### 3.4 Run projection (`GET /api/runs`, `GET /api/runs/{id}`, `last_run` in `GET /api/overview`)

`Run.MarshalJSON` adds:

- For `run_type` `backtest` or `replay`: `"published_range": {"from": "<YYYY-MM-DD>|null", "to": "<YYYY-MM-DD>|null"}`. Both `null` means no date filter was applied; this is also true for every pre-024 Backtest/Replay run, which selected all retained videos.
- For every other run type: `"published_range": null`.

A stored value that is not canonical (impossible past the triggers) is projected as `null` for that bound and logged as a closed integrity code, never echoed raw.

### 3.5 SPA obligations (behavior only; Designer owns copy and layout)

- `startRun()` returns `{run_id, status, admission}`. A `409` exposes `code`, `run_id` and `reason` to the view. Progress follows `run_id` in both `202` cases.
- `BacktestView` sends `published_from`/`published_to` from the date inputs' `value` strings unchanged (they are already `YYYY-MM-DD`). It omits empty inputs and never builds a `Date`. In both modes it shows the `400` message and starts nothing.
- Run inspection shows `published_range` for Backtest/Replay, including the "no date filter" state when both bounds are `null`.

## 4. Invariants

1. **One press, one admission.** An accepted press either resumes exactly one eligible run or creates exactly one new run, never both. A refused press changes no row.
2. **No overlap.** The admission holds `productionMu` from `TryLock` until `admission.Execute` returns or `Release` is called. Every production execution (`Execute`, `RetryQueueEntry`, `StartRun`'s creation) takes the same mutex.
3. **Resume is the same run.** A resumed run keeps `id`, `config_version_id`, `current_phase`, `selected_video_count`, its decisions, actions, send guards and queue ownership. Nothing is inserted into `run` or `config_version` by a resume.
4. **Cause integrity.** `interrupt_cause` is non-NULL only if it was written together with `status='interrupted'`. It is cleared by every write that clears `interrupt_reason`.
5. **Range immutability.** `published_from`/`published_to` are fixed at insert and are only present on Backtest/Replay rows.
6. **Selection locality.** Ranged selection reads only `video` and `transient_metadata`. With no range, selection is byte-for-byte today's behavior.

## 5. Failure behavior

- A crash after the admission commits leaves `status='running'` with the checkpoint kept. Startup classification reclassifies it (`interrupt_cause=NULL`) and resumes it before producers open (unchanged ADR-005/007 behavior).
- If `admission.Execute`'s auth binding or authority check fails, the existing `failRun`/`retainAuthorityFailure` paths apply unchanged.
- If the time limit expires again, `failRun` writes `interrupt_cause='time_limit'` again. The run becomes the eligible candidate again, ordered by its original `started_at`.
- Shutdown during a resumed run → `interrupt_cause='shutdown'` → not eligible for Run Now; startup resumes it.
- A selection read failure in a ranged Backtest/Replay → `failed` through `failHistoricalRun`; `selected_video_count` stays NULL.

## 6. Compatibility and evolution

- Readers must accept NULL in all four new columns. Pre-024 rows never become eligible for resume and project `published_range` as both bounds `null` for Backtest/Replay.
- Rolling back the binary to one without migration 024 is not supported once 024 has run. This matches the existing forward-only 017–023 policy. Old binaries would not write `initiated_by`, so their new manual runs would be ineligible, which fails closed.
- New `initiated_by` or `interrupt_cause` values require a new migration that replaces the triggers, plus an update to this specification.

## 7. Security surface

Nothing new crosses a trust boundary. The range strings are validated before persistence and are echoed only in canonical form. Refusal bodies contain only a run ID and a closed reason. No provider data or error text is added to any response or log.

## 8. Verification — tests the implementation must pass

Each test uses real migrated SQLite (`store.ApplyMigrations`) unless marked *unit*. "Controlled provider" means the existing local `httptest` YouTube seam with request counting (see `e2e/invariants_test.go`). Package names are suggestions.

### 8.1 Migration and store (`internal/store`, `internal/state`)

1. **M1 upgrade:** a database at 023 with manual, scheduled, retry-shaped, backtest and replay rows migrates to 024. All four columns are NULL, no row changes otherwise, and close/reopen succeeds.
2. **M2 initiator guard:** an insert with `initiated_by='run_now'` on a scheduled run, `'schedule'` on a manual run, or an unknown value aborts. Any update of `initiated_by` aborts.
3. **M3 cause guard:** inserting or updating `interrupt_cause` to a value outside the set, or together with a status other than `interrupted`, aborts. `InterruptRunWithCause` succeeds. `ResumeRun` and `InterruptRun` leave the cause NULL.
4. **M4 range guard:** a range on manual/scheduled/dry_run aborts. `2026-1-05` and `2026-11-02 > 2026-11-01` abort. Assert and record what `2026-02-30` does under the trigger ([unverified] SQLite `date()` behavior). Any update of either bound aborts, including NULL→value.
5. **M5 projection:** `GetRun`/`ListRuns` round-trip all four fields. Backtest/replay JSON carries `published_range`; other types carry `null`.

### 8.2 Interruption cause (`internal/pipeline`)

6. **C1 time limit:** `Execute` under `WithTimeoutCause(…, ErrRunTimeLimit)` with a controlled provider that blocks in action execution past a short deadline. The row is `interrupted`, `interrupt_cause='time_limit'`, the phase is kept and the reason is `code=timeout`.
7. **C2 shutdown:** cancel the parent context instead → `interrupt_cause='shutdown'`.
8. **C3 non-limit timeout:** a provider `net.Error` timeout whose retention then fails, ending in the `interrupted` fallback → `interrupt_cause` NULL even though the reason says `code=timeout`.
9. **C4 plain deadline:** a `context.WithTimeout` without the cause (for example the queue-retry wrapper) → cause NULL.

### 8.3 Admission (`internal/pipeline`)

10. **A1 resume:** seed one eligible run with `current_phase='action_execution'`, a recorded success for target X and a retained send guard for target Y. `AdmitRunNow` returns `Resumed()==true` with that ID, and status is `running` before `Execute`. The `run` and `config_version` row counts are unchanged, the versioner/config-store capture is not called, and no deferred group is claimed. After `Execute`, the provider received zero inserts for X and Y, and Y's guard still exists and is unresolved.
11. **A2 original rules:** edit the active configuration after the interruption; the resumed run evaluates with its original snapshot (decision route IDs and labels come from the original).
12. **A3 ordering:** three eligible runs, two with an identical `started_at`. Successive admissions (each executed to completion) resume oldest-first, with the lower ID first on the tie, one per admission. The next admission after the last one creates a new run.
13. **A4 exclusions:** seed version-0, `action_authority_version=0`, NULL-initiator (pre-024), `queue_retry`, `schedule`, `shutdown`-cause, NULL-cause, `failed`, `completed`, `dry_run`, `replay` and `backtest` rows. The admission creates a new run and every seeded row is byte-for-byte unchanged.
14. **A5 unusable candidate:** for each reason (snapshot row deleted → `snapshot_unavailable`, non-JSON snapshot → `snapshot_unreadable`, inadmissible routes → `snapshot_inadmissible`, `current_phase='bogus'` → `checkpoint_unrecognized`, `completed_at` set → `terminal_timestamp_contradiction`): the result is `ResumeUnavailableError` with that ID and reason, no run is inserted, the row is unchanged, the lock is free afterwards, and the provider received zero requests. A younger valid candidate is **not** chosen.
15. **A6 busy:** while `Execute` of another run holds the lock, `AdmitRunNow` returns `ErrProductionRunActive` immediately (bounded wait in test) and changes nothing.
16. **A7 gate:** with the startup gate closed → `ErrStartupRecoveryActive`, with no lock attempt and no change.
17. **A8 concurrent presses:** two goroutines call `AdmitRunNow` at once with one eligible run. Exactly one succeeds; the other gets `ErrProductionRunActive`. The run is resumed once.
18. **A9 re-limit:** the resumed run hits the limit again → eligible again, with prior decisions and actions kept and `selected_video_count` unchanged.
19. **A10 crash after admission:** commit the admission, then simulate process loss (do not execute). `classifyStartupRuns` reclassifies the row (`interrupt_cause` NULL) and `ResumeRecovered` resumes it from the saved phase.
20. **A11 startup unchanged:** the existing `startup_recovery_test.go` and `startup_*_integration_test.go` suites pass unmodified against the shared validator.

### 8.4 Run Now HTTP (`internal/server`)

21. **H1 contract:** registered `POST /api/runs` returns `202 admission=started` with no candidate, `202 admission=resumed` with the candidate's ID, `409 production_run_active`, `409 resume_unavailable` (with `run_id` and `reason`), and the unchanged 503 bodies for shutdown and recovery. `runWG` is balanced in every branch (WaitRuns returns).
22. **H2 time-limit context:** the handler's execution context carries `ErrRunTimeLimit` as its cause (inject a short limit through a test seam). The schedule's next fire time is unchanged after a resume (AC5).

### 8.5 Date range (`internal/state`, `internal/pipeline`, `internal/server`)

23. **D1 parse (*unit*):** valid days; leap day `2028-02-29` ok, `2026-02-29` invalid; `2026-1-05` invalid; empty string invalid; reversed reported with an empty field; S=E valid.
24. **D2 boundaries:** real `transient_metadata` rows written through the **real discovery upsert path** (so the driver's stored format is exercised) for S−1 23:59:59Z, S 00:00:00Z, E 23:59:59Z and E+1 00:00:00Z, plus a `+02:00` offset value near midnight. The backtest selects exactly the middle two (and the offset value by its UTC instant). `selected_video_count` is 2 and only those two have decisions.
25. **D3 no range and one bound:** no range selects every retained video, including one with no metadata row and one with an expired row (the same as today's set). Start-only and end-only apply only their bound.
26. **D4 ineligible dates:** with a range, videos with no row, `is_active=0`, `expired_at` set, `expires_at` ≤ cutoff, NULL `published_at` or undecodable `published_at` are not selected, have no decision and are in no count or outcome filter. The controlled provider records zero requests, and `metadata.Manager` methods are not called (spy).
27. **D5 invalid range HTTP:** for Backtest and Replay, a reversed range, `2026-02-30`, a non-string type, an empty string and (Replay) a query-parameter range each return `400 invalid_date_range`. Run, config_version and identity-anchor row counts are unchanged, and the proposal ingest/config store are not called.
28. **D6 zero match:** a valid range matching nothing completes with `selected_video_count=0`, status `completed`.
29. **D7 persistence:** start a ranged backtest, close/reopen SQLite (restart), and `GET /api/runs/{id}` shows the same `published_range`. A direct `UPDATE` of a bound aborts. A no-range Backtest shows both bounds `null`; a production run shows `published_range: null`.
30. **D8 replay parity:** the D2 boundary set through `POST /api/runs/replay` with a `config_version_id` both in the query and in the body.

### 8.6 Composition and browser

31. **E1 resume journey (`-tags=integration ./e2e`):** real binary path (registered handler, real pipeline/executor/auth store, migrated SQLite, controlled provider). Shorten the limit through a test-only seam that keeps `WithTimeoutCause(…, ErrRunTimeLimit)`. The first Run Now times out mid-actions; the second Run Now returns the same `run_id` with `admission=resumed`; the run completes; the provider insert count equals the remaining unsent targets only; the run count increased by exactly one in total.
32. **E2 date journey:** real-client SPA build against the real binary, in a browser with `TZ` set to a non-UTC zone (for example `Pacific/Kiritimati`). Set the range, run in both Backtest modes, confirm the selected/evaluated counts match D2, restart the server, reopen run detail and confirm the saved range is shown; an invalid range shows the message and no run appears in the list.
33. **E3 SPA unit (`web/`):** `startRun` maps the `admission`/`409` fields; `BacktestView` sends the exact input strings, omits empty ones and never constructs a `Date` for them; run detail renders all three `published_range` states.

## 9. Open questions and findings

| Finding | Disposition | Owner | Blocks? |
|---|---|---|---|
| Ranged selection excludes videos whose local metadata has expired (30-day lifecycle unless refreshed), so ranges over older periods may select few or none. | Follows accepted INV-9 and the decision-evidence release rule; reported, not escalated. Revisit if compliance establishes that `published_at` may be kept and used after expiry. | Product owner (awareness); compliance for any widening | No |
| Run Now during an active production run changes from "request waits" to `409 production_run_active`. | Decided in ADR-001 within BR-017 AC6 ("prevented"). Designer to supply the refusal copy. | Designer | No |
| Pre-024 time-limit-interrupted manual runs cannot be proven Run Now/time-limit runs. | Fail closed: not eligible for Run Now; still resumed by startup recovery. | — | No |
| An unusable oldest candidate blocks every Run Now press until it is repaired. The next process start also blocks on it in startup classification (existing ADR-005 behavior). | Required by BR-017 AC11 (fail closed, no skip). No automatic repair is designed or authorized. | Product owner if a repair path is wanted | No |
| Replay applying the range (BR-027 index open question). | Realized for both modes, per the accepted design (US-012). Narrow the trigger and handler if the owner decides otherwise. | Product owner | No |
| SQLite `date()` normalization for impossible days. | Verified: the trigger rejects `2026-02-30`. The Go parser stays authoritative. | — | No |

## 10. Implementation record and deviations

Run Now: commit `2476375`. Date range: the commit that follows it on PR #4. Both are unreviewed; this section is the record of where the code differs from §1–§9.

| # | Deviation | Owner | Effect |
|---|---|---|---|
| 1 | **Migration split.** `024_run_initiation.up.sql` (not `024_run_initiation_and_published_range.up.sql`) holds `initiated_by` and `interrupt_cause` as pushed in `2476375`. The `published_from`/`published_to` columns and their two triggers are appended to the same file in the date-range commit. The interrupt-cause guard is two triggers, `run_interrupt_cause_insert` and `run_interrupt_cause_update`, not `run_interrupt_cause_check`. | Run Now / date range | One migration file overall. A database already migrated to 024 from `2476375` alone does not gain the range columns (the migration will not re-run). Such databases must be recreated; 024 has not shipped. |
| 2 | **Validator order.** `validateResumableRow(completedAt, phase, snapshot)` checks terminal timestamp, missing snapshot, checkpoint, readable snapshot, then admissible routes. §2.2 listed checkpoint last, and passed `config_version_id`. The code keeps startup classification's existing order so its behavior is unchanged (test A11). A NULL `config_version_id` yields a NULL joined snapshot, so it is reported as `snapshot_unavailable`. | Run Now | A row failing two checks can report a different reason than §2.2 predicts. Which rows are refused is the same. |
| 3 | **Quota legacy stub.** `internal/quota/quota_test.go` creates a minimal `run` table (`id, run_type, status`) for its fixture, because migration 024 alters `run` and the old fixture had no such table. | Run Now | Test fixture only; no production effect. |
| 4 | **Extra timestamp layout.** The ranged selector also decodes `2006-01-02 15:04:05.999999999 -0700 -0700`, the driver's form for a numeric-only zone (`+0200 +0200`). Without it, videos stored with a non-UTC offset were silently left out. | Date range | §2.4's list is amended above. |
| 5 | **`date()` behavior.** SQLite `date()` rejects `2026-02-30`, so the range trigger aborts for it. Previously [unverified]. | Date range | None; recorded in the M4 test log. |
| 6 | **Selector location.** The ranged read and filter are one method, `state.Store.ListRetainedVideosPublishedIn(ctx, rng, cutoff)`, instead of a separate read plus a pipeline-side filter. The pipeline reads the range from the run row. | Date range | Same behavior; the filter cannot be bypassed by a caller. |

Tests not run as ARS-001 describes: E2 (real browser in a non-UTC zone), the `metadata.Manager` spy in D4 (the selector lives in `state` and cannot reach the manager), a real process restart in D7 (a new store over the same database stands in), and D8 through a real pipeline (handler-level with a fake, plus `Replay` directly). Live provider behavior and the real 30-minute limit were not exercised.
