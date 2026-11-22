---
type: specification
title: ARS-001 — Run Quota Stop, Refused-Call Charging, Pacific Reset and Pre-Scan Quota Outcome
description: Hotfix contract for run 58. A provider quota refusal stops all later provider calls in the run, durably, through the existing ledger admission. A quota refusal costs 1 unit locally, not the operation's full cost. The quota day defaults to midnight Pacific. A quota refusal before the channel scan ends discovery as cut_short instead of failing the run.
tags: [architecture, specification, quota, hotfix, run-58, dispatch-stop, discovery, ledger, reset, deferred-queue]
timestamp: 2026-11-22T00:00:00Z
---

**Status:** proposed hotfix contract on `origin/main` at `8d7e009` (includes PR #5's admission fence). Revised 2026-11-22 after critic `ses_dfd2e7bdeffeRof29fhoIoWn65` (CHANGES REQUIRED): §1.2 now orders the stop before any daily-limit logic, with new test Q1b; §3.3 corrects how long the deployment-day overcount lasts and adds the optional relabel (§3.3.1, Q-4). Implemented on branch `hotfix/quota-stop` (see the implementation record at the end). §5's terminal status is the dispatcher's recommendation and is **pending the user's decision** (Q-1). Independent critique is recommended before release.

## Summary

Run 58 started at 00:00:44Z. The local ledger counts a quota day from UTC midnight, so it showed a fresh 10,000 units. YouTube counts from Pacific midnight and was still exhausted. YouTube accepted one insert, then refused 199 with `403 quotaExceeded`. YuHaul kept sending, because nothing treats a provider refusal as a stop. It charged each refusal 50 units, so the local ledger reached 10,000 and refused the remaining ~330 videos. Discovery then hit a plain "insufficient quota" error before the channel scan and failed the run as `unknown_error`. Debugger session `ses_dfd3cf500ffeOrmsSiJ7cIV7ui` has the diagnosis and reproduction.

This hotfix changes four things:

1. **Run quota stop.** After an admitted call in run R gets a provider quota refusal, the ledger refuses every later admission for R in the same quota day. Every caller already handles that refusal as a quota deferral. The stop is read from R's own ledger rows, so it survives a restart without a new column.
2. **Charging.** A call whose outcome is `quota` is recorded as 1 unit, YouTube's documented minimum, not 50.
3. **Reset.** With no stored schedule, the quota day runs from 00:00 `America/Los_Angeles`. A stored `SetResetSchedule` still wins.
4. **Pre-scan quota outcome.** A quota refusal before the channel loop ends discovery with `discovery_scan_outcome='cut_short'`, no error, and a fixed WARN line. The run completes (pending Q-1).

**Example.** Same start as run 58, with 530 claimed videos. Insert 1 succeeds (50 units). Insert 2 gets `403 quotaExceeded`. It is recorded as 1 unit with `outcome_category='quota'`, and its video is deferred as `quota_exhausted`. Each of the other 528 videos is evaluated locally. Its send is refused by the ledger before any request is made, and it is handed back to the queue as pending `quota_exhausted`. Discovery's first budget check is refused, so the scan outcome is `cut_short`. The run completes. YouTube received 2 requests, not 200.

**Limits.**
- The stop is per run, as [ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §6.1 specifies ("a fresh eligible run performs ordinary authorized admission again"). A later run in the same Pacific day may send one request, which will be refused, and then stops.
- Ledger rows written before deployment keep their UTC-day labels. Until the next Pacific midnight after the deployment's UTC day, up to about 24 hours, the local count can be too high (§3.3). A deployment after 08:00Z can therefore block runs until 08:00Z the next day. An optional one-off relabel fixes this (Q-4).
- Run Detail cannot truthfully say "skipped" from the stored facts (§5.3, Q-2).

## Satisfies

| Requirement | How |
|---|---|
| [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) G1, G2, failure semantics "Quota exhaustion" | §1. A provider quota refusal defers this and all later API-dependent work with `quota_exhausted`, through existing deferral paths. §4: pre-scan exhaustion is not an error. |
| OC-001 G3 | §1. No retry in the run. The next run or a user-started run resumes. |
| OC-001 G7 and invariant "no API call when quota is known to be insufficient" | §1 (the stop is known evidence) and §3 (the provider-aligned day). |
| OC-001 G6 and invariant "the log reflects every attempt" | §2. Every admitted attempt still gets exactly one row with its category. Only the unit amount changes. |
| OC-001 invariant "quota exhaustion is never classified as an error" | §4 removes the `unknown_error` path. |
| [BR-013](../../../requirements/yuhaul/br-013-quota-limited-work-deferral.md), [BR-019](../../../requirements/yuhaul/br-019-quota-aware-processing.md) | Deferral and proactive short-circuit, as above. |
| [BR-018](../../../requirements/yuhaul/br-018-api-consumption-accounting-and-visibility.md) | §2. The ledger amount follows the documented rule. §1.3: run-scoped bound reads that are recorded with no run today get their run ID. |
| [ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §5.2 step 2 and §6.1 "Definitive quota refusal" | §1 is the first realization of the run **dispatch stop** for quota: one `quota` attempt, close dispatch for the run, defer the rest, survive same-run recovery from typed ledger evidence. The authorization stop and §6.2 are **not** realized here. |
| [Discovery-source-isolation ADR-001](../../discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) (an in-loop quota stop completes the run as `cut_short`) | §4 applies the same rule before the loop. |
| Pre-scan terminal status | **Unspecified upstream.** §5 assumes the recommendation (Q-1). |

Adopted in part from the shelved [coordinator ARD-001](../../discovery-processing-coordinator/design/ard-001-discovery-processing-budget-coordinator.md): "a provider quotaExceeded closes admission even when the estimate shows headroom" (scoped to the run here) and "provider-aligned quota windows". Its job split, durable holds, shares and pacing are not adopted.

## 1. Run quota stop (packages `quota`, `pipeline`)

### 1.1 Run scope on the context

```go
// package quota
func WithRun(ctx context.Context, runID int64) context.Context // runID > 0
func RunFromContext(ctx context.Context) (int64, bool)
```

- `executeHeld` wraps `ctx` with `quota.WithRun(ctx, runID)` right after `ctl.attach`, before binding, metadata lifecycle, retries and discovery. The queue-retry entry (`retry.go`, before `processDeferredItems`) does the same with its run ID.
- `context.WithoutCancel` keeps values, so finalization contexts keep the scope.
- Calls outside a run carry no scope and behave exactly as today. Examples: title search, the playlist picker, on-demand refresh outside a run.

### 1.2 Stop predicate and admission

**Run R is stopped in bucket D** when this holds:

```sql
EXISTS (SELECT 1 FROM quota_ledger WHERE run_id = ?R AND date = ?D AND outcome_category = 'quota')
```

Here D is the bucket of the current time (§3). `idx_quota_ledger_run_id` covers the lookup. There is no in-memory latch. The row that `Finalize` commits is the stop, so it holds after a restart or resume. A run that crosses the reset is no longer stopped in the new bucket, because YouTube has reset too.

**Order and the limit.** The stop applies whatever the daily limit is, including `daily_limit=0` (unlimited). Both methods evaluate it before any daily-limit logic: before `Acquire`'s `limit > 0 && …` refusal, and before `CanAfford`'s early `DailyLimit <= 0` success. An unlimited local budget says nothing about YouTube's quota.

- `Ledger.Acquire(ctx, op)`: when the context carries R, evaluate the predicate under `l.mu`, using the same `date` as the capacity check. If it holds, return `(nil, nil)`. `auth.dispatchScoped` already turns this into `ErrQuotaRefused`, a typed pre-admission refusal that costs nothing. A query error is returned as an error, which `auth` already joins with `ErrQuotaAccounting` (fail closed).
- `Ledger.CanAfford(ctx, estimate)`: same predicate. If it holds, return `(false, 0, nil)`.
- `Budget` and `ConsumedToday` stay unscoped and unchanged.

The ledger is the one admission point every guarded production call goes through: `auth.dispatchScoped` → `Acquire`. Every caller's pre-check is `CanAfford`. So no executor, metadata or discovery branch is added for the stop, and none can be missed. The existing handling then gives the required outcomes:

| Path | Existing handling of a refusal (unchanged code) |
|---|---|
| Executor pre-check `CanAfford` (`executor.go:344`, `:370`) | `deferActions`: no-attempt evidence `quota_exhausted`, deferred item pending |
| Executor final admission | `DispatchRefusalQuota` → `MutationNotIssued` → deferred |
| Metadata reads (`manager.go:207/362/480`) | `quota_refused` / deferred refresh |
| Discovery | §4 |

### 1.3 Run attribution of bound reads

`Hold` keeps the run ID from `RunFromContext` at `Acquire`. Some callers pass no run to `Finalize(ctx, runID, …)`; today that is the bound-read adapter (`cmd/server/main.go:801`). For those, `Finalize` writes the hold's run ID. An explicit valid `runID` wins. Without this, a `videos.list` quota refusal inside a run would not stop the run. Side effect: those reads now count toward the run's `quota_cost`, which is correct under BR-018.

### 1.4 Claiming and the rest of the run

- **At creation** (`createClaimedRunLocked`, ARS-005 §5.2 bullet 2): call `CanAfford(ctx, 1)` with no run scope before listing candidates. If it refuses, create the run and claim nothing. A ledger error fails `StartRun`, as a storage fault, not a quota result. When quota is known to be used up, this leaves the deferred rows exactly as they were: same status, owner and retry count.
- **After a stop:** `processDeferredItems` keeps going through the receipts it already holds. Each one is still settled by its existing path. Local-only outcomes (exclusion, final, rejected) settle as today. Any send or read is refused at the ledger, and the video goes back to the queue as pending `quota_exhausted` through `RequeueRetryReceiptTx`. Do not break out of the loop: an unsettled claimed receipt would need a handoff that does not exist on this path.
- **No further claims:** `main` claims only at creation, so there is no later claim to stop. When ARS-005's batch loop is built, its step 2 test is this same predicate.
- **Discovery** runs after retries and meets the stop at its first budget check (§4).

**Guarantee:** after the first admitted quota refusal is finalized, run R sends no more YouTube requests in that bucket. The only exception is a request already admitted at that moment. Production dispatch is sequential under `productionMu`, so today that number is zero.

## 2. Charging a provider quota refusal

`Hold.Finalize` and `Ledger.RecordAttempt` write `units_consumed = 1` when the category is `quota`. Every other category keeps `op.UnitCost()`. `call_count`, `http_status` and the row-per-attempt rule do not change. While a request is in flight, its reservation still holds the full cost, so admission stays conservative.

**Why 1 unit.** Google's quota page says "All API requests, including invalid requests, incur a quota cost of at least one point" and that the daily quotas "reset at midnight Pacific Time (PT)" ([determine_quota_cost](https://developers.google.com/youtube/v3/determine_quota_cost); also [getting-started § Quota usage](https://developers.google.com/youtube/v3/getting-started), read 2026-11-22). The 50-unit cost is the cost of a write. A request refused for quota performs no write. The documented floor is therefore the smallest amount that is never below Google's own rule.

- Charging 0 would break that rule.
- Charging 50 made the local ledger "spend" quota that YouTube never granted. Run 58 recorded 9,950 units for 199 refusals.

Now that §1 stops the run, the charge no longer acts as the brake. It only needs to be accurate.

**Not changed:**
- Historic rows, including run 58's 199 rows at 50 units, are not rewritten.
- Other failed inserts (`permanent`, `transient`) still cost 50. Whether Google charges a failed write its full cost is `[unverified]`, and outside this fix.

## 3. Quota day aligned with YouTube's reset

### 3.1 Rule

`bucketCalculator` and `nextReset` use the stored schedule when there is one. Otherwise they use a built-in default: `Timezone="America/Los_Angeles"`, `TimeOfDay="00:00"`. `ResetSchedule()` still reports only the stored schedule. `SetResetSchedule` is unchanged and stays the override. No production caller or new configuration is added.

- `bucketDateFor` already handles daylight saving: midnight Los Angeles is 08:00Z in winter and 07:00Z in summer.
- The package imports `_ "time/tzdata"`, so `LoadLocation` cannot fail in the image or in tests. If it does fail, `Acquire` and `CanAfford` error and admission fails closed.
- `Budget.ResetKnown` becomes true and `reset_at` appears in `/api/quota`. This is the documented reset, not a guess.
- `AdmitIf` and `AdmitScoped` already refuse a hold whose bucket changed before admission. A request that spans the reset is not relabeled into the new day.

### 3.2 Labels

The `quota_ledger.date` label means the Pacific day from now on. Statistics that group by `date` show UTC days before the deployment and Pacific days after it. There is no automatic relabeling migration. Only the deployment day matters, and the optional operator step in §3.3 covers it.

### 3.3 Day of deployment

`ConsumedOn` sums rows by label. Pacific day P runs from P 08:00Z to P+1 08:00Z (07:00Z while daylight saving is in effect). Rows written before deployment carry the UTC day as their label. Let T be the deployment time and D its UTC day.

- **T at or after D 08:00Z.** The current bucket is D. It includes the pre-deployment rows labeled D that were recorded before D 08:00Z. Those rows belong to Pacific D−1, so the count is too high. This lasts until D+1 08:00Z, up to about 24 hours. Example: on run 58's day, suppose 10,000 units were recorded between 00:00Z and 00:07Z and the deployment is at 12:00Z. Bucket `2026-11-22` starts full, so every run is refused locally until 2026-11-23T08:00Z, although YouTube reset at 2026-11-22T08:00Z.
- **T before D 08:00Z.** Until D 08:00Z, the current bucket is D−1. It counts D−1's rows from before D−1 08:00Z, which is too high, and leaves out D's rows from 00:00Z to T, which is too low. From D 08:00Z the first case applies.

The too-high count fails safe: work is deferred, not sent. The §1 stop limits the effect of the too-low count to one refused request per run. The optional relabel in Q-4 removes both.

**Revisit trigger:** a refused request soon after deployment that is not followed by a stop.

#### 3.3.1 Optional one-off relabel (operator; the user decides; Q-4)

This moves pre-deployment rows to their Pacific label. It is not part of the code change.

**Format.** `recorded_at` is UTC RFC 3339 text in every writer. Go writes `2026-11-22T00:00:44.000000123Z`, and the SQL default writes `2026-11-22T00:00:44Z`. Both were checked on a scratch database at this branch. Compare against `'<L>T<HH>:00:00'` with no trailing `Z`. With the `Z`, a row at `…T08:00:00.5Z` would sort before the boundary, because `.` sorts before `Z`.

**Labels to relabel.** L = D always. Also L = D−1 when T was before D 08:00Z. `<HH>` is `08` when L's Pacific midnight is in standard time and `07` when it is in daylight time. Rows written after deployment are never matched: a post-deployment row labeled L has `recorded_at` at or after L's reset.

1. **Back up first.** With no run in progress, copy the database with WAL-safe `sqlite3 /data/yuhaul.db ".backup '/data/yuhaul-pre-relabel.db'"`. Or stop the pod and copy `/data/yuhaul.db*`. The image has no `sqlite3`, so run the client somewhere that can reach the volume.
2. **Preview (read-only).** For each L:
   ```sql
   -- rows that would move; non_utc_text must be 0, or stop
   SELECT date, COUNT(*) AS rows_to_move, SUM(units_consumed) AS units_to_move,
          MIN(recorded_at), MAX(recorded_at),
          SUM(recorded_at NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z') AS non_utc_text
   FROM quota_ledger
   WHERE date = '<L>' AND recorded_at < '<L>T<HH>:00:00'
   GROUP BY date;

   -- bucket totals before the change
   SELECT date, SUM(units_consumed) FROM quota_ledger
   WHERE date IN ('<L-1>', '<L>') GROUP BY date;

   -- keep this id list to undo the change without the backup
   SELECT id FROM quota_ledger WHERE date = '<L>' AND recorded_at < '<L>T<HH>:00:00' ORDER BY id;
   ```
3. **Apply,** one transaction for each L:
   ```sql
   BEGIN IMMEDIATE;
   UPDATE quota_ledger SET date = '<L-1>'
   WHERE date = '<L>' AND recorded_at < '<L>T<HH>:00:00';
   -- changes() must equal rows_to_move from step 2; otherwise ROLLBACK
   SELECT changes();
   COMMIT;
   ```
   Example, deployment at 2026-11-22T12:00Z, with L = `2026-11-22` and HH = `08`: `UPDATE quota_ledger SET date='2026-11-21' WHERE date='2026-11-22' AND recorded_at < '2026-11-22T08:00:00';`.
4. **Check:** run the preview again; it returns no rows. The bucket totals moved by `units_to_move`.

The preview, update and boundary were run on a scratch SQLite database with rows at `00:00:44.000000123Z`, `07:59:59Z`, `08:00:00Z`, `08:00:00.5Z` and `12:00:01.1Z`. Only the first two moved.

Effect on reporting: per-day statistics for the moved rows show their Pacific day. To undo, restore the backup, or set `date='<L>'` back for the saved ids.

## 4. Typed pre-scan quota outcome (package `discovery`)

### 4.1 Typed refusal

- `requireBudget` returns `fmt.Errorf("discovery: insufficient quota for %s x%d: %w", op, calls, auth.ErrQuotaRefused)` when the budget refuses.
- A `CanAfford` error keeps its current form (`budget check failed`) and does **not** wrap `ErrQuotaRefused`.
- Add a helper, `quotaStop(err) bool`. It is true when the error is `errors.Is(err, auth.ErrQuotaRefused)`, or when an admitted provider error carries the reason `quotaExceeded` or `dailyLimitExceeded`. That second case is the same reason set that `readCategory` maps to `quota`, matched through `ReadError` wrapping by `errors.As(*youtube.APIError)`.

### 4.2 Before the channel loop

In `runWithClient`, a `quotaStop` error from `fetchSubscriptions` or `fetchChannels` is handled here, on any page or batch. Subscription paging also returns it instead of `break` with a partial list: once quota is refused, the next call would be refused too.

1. `SetDiscoveryScanOutcome(ctx, runID, cut_short)`. A write error is returned as an error, as today.
2. Set `ScanStopped = true` and `Deferred = true`, and set `DeferredReason` to a fixed text. Discover no videos and send nothing more.
3. Log the fixed WARN in §6, then return `(result, nil)`.

The remaining phases run on zero discovered videos, and the run completes (§5).

### 4.3 Inside the channel loop

- An error from `requireBudget` sets `ScanStopped` only when `quotaStop` is true. A budget-check storage fault is returned as an error. Today any error is treated as a quota stop, which is a latent misclassification.
- A `ListPlaylistItems` error that satisfies `quotaStop` (including the provider's 403 `quotaExceeded`, not only `ErrQuotaRefused`) sets `ScanStopped`.

The rule stays that a run whose status is `completed` and whose outcome is `cut_short` was always cut short by quota. `ScanStopped` has no other cause.

## 5. Terminal status (pending user decision Q-1)

### 5.1 Server

Assumed outcome:
- status `completed`;
- `discovery_scan_outcome='cut_short'`;
- `interrupt_reason` NULL;
- `videos_discovered=0`;
- `selected_video_count` captured as usual.

Nothing reaches `failRun`. If the user decides on a different status, only §4.2's return value and §5.2's condition change.

### 5.2 Run Detail

`RunDetailView` shows one sentence when `status==='completed' && discovery_scan==='cut_short'`. It sits next to the terminal status line, separate from `ChannelCoverage`, which renders only when source failures exist. The designer owns the copy.

### 5.3 "Skipped" wording (Q-2)

The requested wording, "discovery was skipped because the daily quota was used up", is true only when no channel scan began. The stored facts cannot tell that apart from a quota stop partway through the channels: both are `completed` + `cut_short`. This ARS therefore specifies a sentence that is true in both cases. Example: "Discovery stopped early because the daily YouTube quota was used up. It continues after the quota resets."

A distinct "skipped" sentence needs a new durable fact. That means a new scan-outcome value, which requires rebuilding the CHECK constraint, or a new run column. It is not in the hotfix.

## 6. Log lines

All are JSON on stdout. All carry `"run_id"` when a run scope exists. Payloads and video IDs are never logged.

| Level | Message | Fields | When |
|---|---|---|---|
| WARN | `youtube quota refused; run dispatch stopped` | `run_id`, `operation`, `http_status`, `bucket` | `Finalize`/`RecordAttempt` commits a `quota` row with a run. At most one per in-flight admission. |
| WARN | `quota pre-flight check failed` (existing) | add `run_id`, `cause` = `local_budget` or `run_dispatch_stop` | `CanAfford` refuses |
| DEBUG | `quota admission refused` | `run_id`, `operation`, `cause` | `Acquire` refuses |
| WARN | `discovery skipped: daily quota used up` | `run_id`, `operation` (`subscriptions_list`/`channels_list`), `refusal` = `admission` or `provider` | §4.2 |
| INFO | `discovery complete` (existing) | add `scan_outcome` | end of discovery |

The diag vocabulary does not change.

## 7. Write boundary

The coder may change only these files:

- `internal/quota/quota.go`, plus tests in `internal/quota/`: §1.1–1.3, §2, §3, §6 ledger lines.
- `internal/pipeline/pipeline.go`: the scope in `executeHeld`, and the claim skip in `createClaimedRunLocked`. `internal/pipeline/retry.go`: the scope at queue-retry entry. Tests in `internal/pipeline/`.
- `internal/discovery/engine.go`, plus tests in `internal/discovery/`: §4, §6 discovery lines.
- `web/src/views/RunDetailView.tsx` and its test: §5.2, using the designer's copy (a placeholder until then).
- `cmd/server/*_test.go`, test files only, for Q5. No production change in `cmd/server`.
- Docs: this bundle, `docs/architecture/index.md`, `docs/architecture/log.md`, and the implementation record here.

**Out of bounds:** migrations, `internal/diag`, `internal/youtube` classification, `internal/auth`, `internal/metadata`, `internal/pipeline/executor.go`, and `cmd/server` production code. If a change seems to need one of these, stop and return to the architect.

Existing tests to update:
- `TestGuardedFinalQuotaRefusalAndProviderQuota` now expects 1 unit.
- `TestDiscoveryRespectsQuotaBudget` now expects a completed run with `cut_short`.
- Any test that expects a partial subscription list after a quota refusal on page 2.

## 8. Verification

Use a fake YouTube and a scratch SQLite database, with the fake clock set as noted. Base the tests on the debugger's run 58 reproduction.

| # | Test | Expected |
|---|---|---|
| Q1 | 8 claimed videos, limit 10,000. The fake returns 200, then 403 `quotaExceeded`. | The provider receives exactly 2 inserts and no discovery calls. Ledger: `success` 50, `quota` 1. 7 videos pending `quota_exhausted` (8 claimed, 1 sent). Run `completed`, `cut_short`. |
| Q1b | Q1 with `daily_limit=0` (unlimited) | Exactly 2 inserts reach the provider. The same ledger rows and deferrals as Q1. Also at ledger level: with `daily_limit=0` and a `quota` row for R, `Acquire` returns `(nil, nil)` and `CanAfford` returns `false` for R. |
| Q2 | Q1, then stop the process after the `quota` row and resume R | R sends nothing. Work is deferred as in Q1. |
| Q3 | A `quota` row for R | Run S and an unscoped call in the same bucket are admitted. R is admitted after the next reset. |
| Q4 | Charging | `quota` → 1 unit and 403 recorded. `permanent` insert → 50. Reads → 1. |
| Q5 | A bound `videos.list` 403 quota inside R, through `cmd/server`'s adapter | The row has `run_id=R`. R's next insert is refused before sending. |
| Q6 | No stored schedule | 2026-11-22T00:00:44Z → `2026-11-21`. 07:59:59Z → `2026-11-21`. 08:00Z → `2026-11-22`. 2026-07-01T06:59:59Z → `2026-06-30`. 07:00Z → `2026-07-01`. `ResetKnown` true. `SetResetSchedule("UTC","00:00")` restores UTC days. |
| Q7 | Local budget exhausted before discovery | `completed`, `cut_short`, `interrupt_reason` NULL, 0 discovery calls, the §4.2 WARN line. |
| Q8 | First `subscriptions.list` returns 403 `quotaExceeded` | As Q7, with one `quota` row. |
| Q9 | `channels.list` batch 2 refused | As Q7. No `playlistItems` call. |
| Q10 | In-loop budget check gets a storage error | Run `failed`. Not `ScanStopped`. |
| Q11 | `StartRun` when `CanAfford(1)` refuses | The run exists with zero claims. Deferred rows are unchanged byte for byte. |
| Q12 | End to end: start at 00:00:44Z, local ledger empty, provider refusing every call | At most 1 request reaches the provider. Run `completed`. |
| Q13 | Web | `completed` + `cut_short` shows the sentence. `completed` + `completed`, and `failed` + `cut_short`, do not. |

## 9. Open questions

- **Q-1 (user, blocks release of §5 only):** the terminal status when quota runs out before discovery. Assumed: `completed` + `cut_short`.
- **Q-2 (user or designer, does not block):** accept the §5.3 wording that is true in both cases, or ask for a new durable "skipped" fact later.
- **Q-3 (requirements owner, does not block):** record the pre-scan behavior in BR-013 or OC-001, and decide whether a provider quota refusal should also block *later runs* until the reset. That would be a bucket-wide stop. It is rejected here because ARS-005 §6.1 makes the stop per run, and because YouTube now has separate pools (`search.list` has its own 100-call allowance), so one pool's refusal must not stop the others.
- **Q-4 (user decides, operator runs; does not block the code):** whether to run the §3.3.1 relabel once after deployment. Without it, local refusals can continue until the next Pacific midnight after the deployment's UTC day ends, up to about 24 hours.
- **Q-5 (operator):** the run 58 ledger, settings and log queries from the debugger session. They confirm the reset mismatch as the first trigger.

## 10. Implementation record

Implemented on `hotfix/quota-stop` from `c1c1977`. Commits: `111d244` (quota), `2aa0506` (discovery), `f02a69e` (pipeline and `cmd/server` test), `0eb154a` (Run Detail), `6827e05` (e2e test expectation).

**Where the assumptions live.** Q-1's terminal status (`completed` + `cut_short`) is `Engine.preScanOutcome` in `internal/discovery/engine.go` (return value) and `stoppedEarlyForQuota` in `web/src/views/RunDetailView.tsx` (condition). The Run Detail copy is the constant `QUOTA_STOPPED_EARLY_COPY` in the same file. It is the §5.3 sentence and is a placeholder pending the designer.

**Evidence** (run on the branch head, Go with `-race` where stated):
- `go build ./...`, `go vet ./...` and `go vet -tags=integration ./...` pass.
- `go test -race` passes for `internal/quota`, `internal/discovery` and `internal/pipeline` (`-timeout 40m`). The pipeline run had one failure, fixed below, and the affected and new tests were re-run under `-race`.
- `go test -tags=integration ./e2e/... ./cmd/server/... ./internal/pipeline/...`, `go test ./...` and the web suite (`vitest`, 901 tests, `tsc -b`) pass.
- Q6 maps to `TestDefaultResetIsPacific`; Q3 to `TestRunStopIsScopedToRunAndBucket`; Q4 to `TestQuotaRefusalChargedOneUnit`; Q1, Q1b, Q11 and Q12 to `internal/pipeline/quota_stop_test.go`; Q7–Q10 to `internal/discovery/quota_stop_test.go`; Q5 to `cmd/server/quota_stop_bound_read_test.go`; Q13 to `RunDetailView.test.tsx`.

**Deviations and gaps, for the architect.**
1. **Q1 count.** The table said 6 pending videos. Eight claimed videos with one sent leave 7, so the row now says 7.
2. **Q2** is proved at the executor boundary: a new ledger and executor over the same database send nothing for the run. A resumed `Execute` on a terminal run is rejected before any send, so it cannot show this.
3. **Q5** proves the refusal row carries the run ID and that the run's next read and its next insert admission are refused. It does not drive an executor insert through `cmd/server`.
4. **Q12** proves the "empty ledger, provider refuses everything" half. The clock half (00:00:44Z gives the previous Pacific day) is Q6, because the ledger clock is not reachable from the pipeline tests.
5. **Outside the write boundary:** `e2e/retry_authority_test.go` asserted that a quota-limited run claims and requeues deferred rows (retry count 1, then 2). §1.4 and Q11 require it to claim nothing, so the test now expects retry count 0 for the limited run and 1 after the final retry. It is its own commit (`6827e05`) so it can be replaced.
6. **Existing tests changed:** `TestGuardedFinalQuotaRefusalAndProviderQuota` (1 unit), `TestExecutor_GuardedQuotaTailEvidenceFailureRollsBackHandoff` (1 unit, not named in §7), `TestDiscoveryRespectsQuotaBudget` (completed `cut_short`), the provider-403 cases in `source_isolation_test.go` (a provider `quotaExceeded` in the channel loop now stops the scan, as §4.3 says), and `TestReservationRetainsBucketAfterRollover` (pinned to a UTC schedule, because the default day is now Pacific).
7. **Not demonstrated against the old code:** the new tests use the new API, so they do not compile at `c1c1977`. The debugger's reproduction of run 58 (200 inserts sent) is the evidence for the old behavior.
