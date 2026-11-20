---
type: specification
title: ARS-002 — Durable Uploads-Source Failure Record and Read-Only Errors Projection
description: Migration 030's immutable discovery_source_failure table, its write point in channel-granular discovery, same-run resume behavior, the authorized Errors and Run Detail readers, and the central migration allocation that places it ahead of the receipt migration.
tags: [architecture, specification, discovery, errors, migration, diagnostics, api]
timestamp: 2026-11-20T00:00:00Z
---

**Status:** accepted technical contract, amended 2026-11-20 for review `833a06fb` MAJOR-1 (§4.5, the per-run scan outcome). §1–§4.4 are implemented in `5f849ea` and `1a0bf19` (local candidate on `interim-uploads-isolation`); evidence: review record `833a06fb` ("with fixes", reviewer session `ses_e09c5a500ffepaBHsFYpJCNTXG`), not rerun by the architect. §4.5 and the explicit D1 flag are implemented in `f88ada9`; evidence coder-run on `f88ada9`: `go test -race` on store, state, discovery, pipeline, server, stats and diag, `go test ./...`, and the web suite (865 tests) all pass; browser, screen-reader and 320 px checks not run. It builds on the channel-granular interim ([ADR-001](../decisions/adr-001-interim-channel-granular-uploads-isolation.md)), and the two ship together. It realizes the source-failure part of [ARD-001](../design/ard-001-isolate-uploads-source-failures.md) and [ARS-001 §5](ars-001-durable-discovery-page-handoff.md#5-authorized-inspection-contract). The page handoff, sealed scope, assessment and floor rows remain with ARS-001 and its later migration.

Example: channels A and C list successfully, and B's uploads read returns `404 playlistNotFound`. Before discovery asks YouTube for anything else, it commits one row: run 54, channel `UC…B`, permanent, `playlist_items_list`, `404`, `playlistNotFound`, time. Run 54 continues and ends **Completed**. After a restart, Errors shows a read-only **Channel scan error** for B in run 54, with "YouTube reported that the uploads playlist was not found." Run Detail for run 54 says "Some channels could not be scanned. Results are incomplete." and lists B. Run 55 polls B again. If B still fails, run 55 adds its own row. If B succeeds, run 54's row stays as history; nothing removes or rewrites it.

## Implements and satisfies

| Authority | How |
|---|---|
| [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md), Permanent uploads-source error | Durable, read-only source entry in Errors that identifies the channel and its vetted reason after a reload or restart. No Retry, Dismiss or suppression. It does not affect the next poll. **Closes ADR-001 gap G1.** |
| [BR-002](../../../requirements/yuhaul/br-002-subscription-discovery-without-live-monitoring.md) AC7 | Run Detail reports partial coverage from the same durable rows. This replaces ADR-001 D6's ledger flag. |
| BR-002 AC8 | Unchanged: ADR-001 D5's `no_source_scanned`. The rows for that run also appear in Errors. |
| BR-002 AC6–7, re-poll | Rows are never read by floor or eligibility logic, so they cannot block or suppress a poll. |
| BR-002 AC9 | Accepted interim gap G2 (user, 2026-11-20). Partial pages of a failed channel are not routed. |
| [Admin UI design](../../../design/yuhaul-admin-ui-design.md), “Incomplete channel scans” | Supplies every field the Errors and Run Detail copy needs. Copy and layout stay with the design. |
| [Safe diagnostic contract](../../run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md) | Only closed vocabularies and a validated structured channel ID. No error text, playlist ID, token, URL or body. |
| [BR-028](../../../requirements/yuhaul/br-028-historical-run-inspection.md), BR-014 and [Error Queue](../../../requirements/yuhaul/error-queue.md) | The video Error Queue, its IDs, totals, filters and commands are unchanged. Source entries are a separate projection. |

User decisions recorded on 2026-11-20 (relayed by the dispatcher):

- G2 is accepted.
- G3 is accepted: the validated channel ID goes in ADR-001 D7's warning log line.
- G1 is declined: "there is no rush, implement the proper fix".

## 1. Schema: migration `030_discovery_source_failure.up.sql`

There is one forward migration and no down file, the same as `029`. This is one additive table with no rebuild and no change to any existing table.

```sql
CREATE TABLE discovery_source_failure (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id       INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    channel_id   TEXT    NOT NULL CHECK (length(channel_id) = 24 AND substr(channel_id, 1, 2) = 'UC'),
    occurred_at  TEXT    NOT NULL,
    category     TEXT    NOT NULL CHECK (category IN ('permanent', 'transient')),
    operation    TEXT    NOT NULL CHECK (operation = 'playlist_items_list'),
    http_status  INTEGER CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
    reason       TEXT    CHECK (reason IS NULL OR length(reason) BETWEEN 1 AND 64)
);
CREATE UNIQUE INDEX discovery_source_failure_permanent_once
    ON discovery_source_failure(run_id, channel_id) WHERE category = 'permanent';
CREATE INDEX discovery_source_failure_category_id ON discovery_source_failure(category, id);
CREATE INDEX discovery_source_failure_run_id      ON discovery_source_failure(run_id, id);
CREATE TRIGGER discovery_source_failure_immutable
    BEFORE UPDATE ON discovery_source_failure
    BEGIN SELECT RAISE(ABORT, 'discovery_source_failure is immutable'); END;

-- Added 2026-11-20 (§4.5): the run's channel-scan outcome.
ALTER TABLE run ADD COLUMN discovery_scan_outcome TEXT
    CHECK (discovery_scan_outcome IS NULL OR discovery_scan_outcome IN ('completed', 'cut_short', 'no_source_scanned'));
```

`030` is unreleased, so this column is added to the same file; no new number is taken. Any development or test database already at version 30 without the column must be recreated, the same as the `024` precedent. A database that has reached a deployed environment at version 30 without this column would need a new migration instead. The dispatcher confirmed none has.

Each choice, and why:

- **An immutable incident row, separate from source scope rows.** ARS-001 §5 needs newest-first lists keyed by an immutable record `id`, plus attributable transient failures in the run view. A transient failure can be followed by a successful re-scan in the same run, so incidents cannot share the mutable `(run_id, channel_id)` scope row that ARS-001 §1 later adds. That later row (`unsettled` → `complete`/`local_failure`, `has_committed_page`, cutoffs) goes in its own table in ARS-001's migration and references this one only by `(run_id, channel_id)`. This refines ARD-001's "one compact run/source record type": the record type is split into incidents (here) and coverage (later). The permanent row is still the Errors entry, with no dual write.
- **CHECKs are deliberately permissive** for `category`, `reason` and `http_status`. Then ARS-001's transient incidents and other vetted reasons need no SQLite table rebuild. The closed vocabulary lives in Go, applied at write and at read (§4). The interim writer only ever writes `permanent`, `playlist_items_list`, `404`, `playlistNotFound`.
- **The channel ID shape is fixed in SQL** as a backstop; Go validates `^UC[A-Za-z0-9_-]{22}$` exactly. The canonical channel ID is the approved structured source identity (ARD-001 "Inspection and trust"). No uploads playlist ID, page token, title, URL, body or error text is stored.
- **The partial unique index** allows at most one permanent incident per run and channel. It backs the same-run skip in §3.
- **The `UPDATE` trigger** enforces "a later healthy poll does not rewrite a historical failure". Rows leave only through `run` deletion (`ON DELETE CASCADE`, as for `error_entry`). There is no source delete path, and no `DELETE` trigger, because that would block the cascade.
- **`occurred_at`** uses the existing UTC run-time format (`formatRunTime`).
- **No backfill.** Runs 52 and 53, and any run on a build without this migration, have no rows, and the readers say so (§4.3). Older binaries refuse a version-30 database. Rollback means restoring a pre-migration backup with writers stopped. A binary-only downgrade is not supported.

## 2. Write point in discovery

The base is the channel-granular interim ([ADR-001](../decisions/adr-001-interim-channel-granular-uploads-isolation.md) D1–D5 and D7 as corrected). In `fetchNewVideos`, when channel `ch` meets the D1 predicate:

1. **Validate first.** `ch.ID` must match the channel-ID pattern, and `api.Reasons[0]` must be admitted by `diag`'s provider-reason set. Both always hold for a D1 match whose channel came from `channels.list`. If either fails, the error is **not isolated**. Return it as today: the run fails with its encoded diagnostic. A skip that cannot be recorded must not happen (ARD-001: "A store failure in the source-evidence write cannot be reported as a successfully isolated/inspectable skip").
2. **Commit the incident.** Write it in its own short transaction on `e.db`, before any further provider request and with no network I/O inside:

   ```sql
   INSERT INTO discovery_source_failure (run_id, channel_id, occurred_at, category, operation, http_status, reason)
   VALUES (?, ?, ?, 'permanent', 'playlist_items_list', 404, 'playlistNotFound')
   ON CONFLICT (run_id, channel_id) WHERE category = 'permanent' DO NOTHING;
   ```

   A conflict is a no-op, not an error. The earlier row and its time stand.
3. **On commit failure,** return the classified SQLite error (`phase=discovery operation=discover code=sqlite_error …`). The run fails as today, nothing counts as a local failure, and the D7 log line is **not** emitted.
4. **After commit,** discard the channel's buffer (D2), count it in `SourcesFailedLocal` (D4), emit the D7 warning (G3 accepted, so `source_channel_id` is included), and continue with the next channel.

**Why a separate transaction, not "the same transaction as the run's discovery outcome".** The interim has no single discovery-outcome transaction to join:

- videos commit one per transaction after all scans;
- the run status is written later by the pipeline;
- the AC8 `FailRun` happens after discovery returns.

Writing the incident at the moment of failure makes it durable before anything that could crash, stop or fail the run. That includes the AC8 terminal write, which then always has its incidents. It is also exactly ARS-001 §1's later rule: source settlement commits before the next provider request. When ARS-001's page and settlement transactions land, the incident insert moves into the same transaction that settles that source row as `local_failure`. The statement and its columns stay the same.

**Accepted window.** A crash after the guarded read finalizes its ledger row but before this insert commits leaves a ledger `permanent/404` row with no incident. The channel is not settled, and a resume re-polls it (§3). ARD-001 already accepts this: "the ledger can outlive source evidence".

## 3. Restart, resume and later runs

- **Same-run resume.** A run interrupted during discovery repeats discovery today. At the start of `fetchNewVideos`, load `SELECT channel_id FROM discovery_source_failure WHERE run_id = ? AND category = 'permanent'`. For each in-scope channel in that set:
  - make no uploads request;
  - count it as attempted and failed locally (D4);
  - do not log again.

  This saves the repeated unit, makes AC8 counts correct across resume, and matches ARS-001 §1 ("never poll a … `local_failure` source again in that run"). A channel with no row (crash before the insert) is scanned normally. A resume that skips past discovery does not read the table.
- **Fresh runs.** Floor and eligibility code never reads the table. Every eligible fresh run polls the channel again (OC-001 and BR-002 AC7). A new failure inserts a new row under the new `run_id`.
- **Clearing.** There is none, by accepted design. The rows are historical evidence of what happened in each run. A later successful scan does not delete, resolve, hide or rewrite them. This is the admin design's rule: "Do not promise … removal of this history after later success" and "a later successful fresh poll does not rewrite the historical reason". It is also ARS-001 §5's rule: "Neither a later healthy poll nor inspection rewrites these records". The Errors copy says the entry "records what happened in this run". A channel that stays broken adds one row per run, which is accurate history. The page bounds and cursors in §4 keep the list usable.
- **Process restart.** The rows are ordinary SQLite rows and are read the same way after a restart. Nothing is held in memory.

## 4. Authorized readers and API

Both endpoints sit behind the existing single-user ingress boundary ([ADR-010](../../adr-010-traefik-basic-auth-ingress-boundary.md)), like every other `/api` route. Both are read-only, make no provider I/O, and have no write method. The ARS-001 §5 contract is restated here with one addition, `channel_name`.

### 4.1 Endpoints

- `GET /api/errors/sources` lists `category='permanent'` rows across runs.
- `GET /api/runs/{id}/discovery-source-errors` lists that run's rows of every category. The interim writes only permanent rows.

Both endpoints:

- accept `limit` (integer `1..200`, default 50) and optional `before_id` (positive integer);
- order by `id DESC`;
- return `{ "items": [...], "next_before_id": <positive integer | null> }`. `next_before_id` is the last returned `id`, and only when another row exists; fetch `limit+1` rows to know.

Errors:

- invalid parameters return `400`;
- a missing run returns `404`, for the run-scoped endpoint only;
- a failed storage read returns the existing safe `500` envelope, never an empty list.

A run with no rows returns `200` with `items: []`, including non-production runs.

### 4.2 Item

| Field | Source and rule |
|---|---|
| `id` | Row `id`. It is not a video error ID and is not accepted by any video command. |
| `run_id` | Row `run_id`. |
| `channel_id` | Row `channel_id`, re-validated on read. Invalid → `null`; the UI then shows "Channel details unavailable." |
| `channel_name` | **Added to ARS-001 §5.** The current local `channel.channel_name`, found by `LEFT JOIN channel ON channel.channel_id = discovery_source_failure.channel_id`. It is nullable, not snapshotted, and follows the existing channel metadata lifecycle. It is `null` for a channel that never produced a discovered video. |
| `occurred_at` | Row value as UTC RFC 3339. |
| `category` | `permanent` or `transient`. Anything else → the row is withheld and counted in a server warning. |
| `operation` | `playlist_items_list`. |
| `http_status` | Integer or `null`. |
| `reason` | The row value only if `diag`'s provider-reason set admits it. Otherwise `null`. Never verbatim unvetted text. |

Never returned: the uploads playlist ID, page tokens, URLs, raw responses, error text, renewal causes, authorization IDs.

### 4.3 Routing and commands

- **Routing.** Register `GET /api/errors/sources` on the main mux as an exact pattern. It is more specific than the `/api/errors/` prefix that strips into the queue handler, so it wins. Register the run-scoped path with the existing run handlers.
- **Commands.** Because `/api/errors/` is a prefix, `POST /api/errors/sources/retry` and `…/dismiss` reach the queue handler's `POST /{id}/…` with `id="sources"`. They must be refused by the existing integer ID parsing (`400`) and never resolve to a row. A test pins this. Register no source write methods, and don't add source rows to video error totals, filters or pagination.

### 4.4 Run Detail and Errors UI

These obligations come from the existing design; copy and layout stay with the design.

- **Errors** gets a separate read-only **Channel scan error** section fed by `GET /api/errors/sources`, with its own loading, empty, failed-read and stale states. Each entry shows:
  - `channel_name`, or else `channel_id`, or else "Channel details unavailable.";
  - `View run {id}`;
  - `occurred_at`;
  - "Uploads could not be scanned.";
  - the reason sentence: for `404` plus `playlistNotFound`, "YouTube reported that the uploads playlist was not found."; for `null` or unknown, "The specific reason is unavailable.";
  - the fixed sentence "This channel will be checked again on the next eligible fresh poll if it is still eligible. This entry records what happened in this run."

  There are no Retry, Dismiss, Remove or suppress controls.
- **Run Detail** reads `GET /api/runs/{id}/discovery-source-errors` for the list. It chooses the sentence from the run read's `discovery_scan` (§4.5). A read failure uses the existing read-failure behavior, not "no failures".
- **D6 is withdrawn.** Don't ship ADR-001 D6's `discovery_incomplete` field or its ledger query. If the interim branch already added them, remove them. The incident rows are exact, contain the channel, and don't over-report on resume (§3).

**Remaining design gap, routed to the designer, not blocking.** This record cannot distinguish "a run on this build with no failed channel" from "an older run without scan evidence". Only ARS-001's later assessment can. Until then, Run Detail shows no coverage sentence for a run with zero rows. It does not show "Channel scan details are unavailable for this run.", and it never says that all channels were scanned or that no new videos were found. The designer confirms this or supplies alternative copy that is true in both cases.

### 4.5 Per-run channel-scan outcome (added 2026-11-20, review `833a06fb` MAJOR-1)

The design's "Scan cut short" variant needs a saved fact that channel scanning stopped early because of a run-wide cause. The in-memory `ScanStopped` and error paths are lost on restart. One nullable column on `run` holds the fact (§1).

| Stored value | Meaning |
|---|---|
| `NULL` | Unknown. The run never reached the uploads scan, it is a simulation, or it predates `030`. |
| `cut_short` | The uploads scan started and has not been recorded as finished. For a run that is no longer running, scanning stopped early because of a run-wide cause: a budget or quota stop (`ScanStopped`), a run-wide error during the scan (provider quota, authorization, accounting, non-isolated provider error, SQLite), cancellation (Stop, time limit, shutdown) or a crash. The run status carries the cause. |
| `completed` | The uploads loop visited every in-scope channel, and each one completed or failed locally under D1. No budget or refusal break happened. |
| `no_source_scanned` | The run ended terminal `failed` under BR-002 AC8 (ADR-001 D5). |

**Writes.** Each is a single `UPDATE run SET discovery_scan_outcome = ? WHERE id = ?`, with no provider I/O inside its transaction.

1. **`cut_short` at scan start.** The engine writes it when `fetchNewVideos` begins, after subscription and channel resolution and before the §3 same-run lookup and any `playlist_items_list` request. It is written even when no channel will need a request, such as when every channel lacks an uploads playlist or is skipped by §3. It is in its own statement. Writing it first covers every non-normal ending, including a crash, with no per-error write path. A failure returns the classified SQLite error and the run fails as today; no channel is scanned without the fact. Every incident row (§2) is written after this, so a run with rows always has a non-null value.
2. **`completed` at a normal scan end.** The engine writes it in its own statement when the uploads loop ends with `!ScanStopped` and no returned error. That is before metadata prefetch and before AC8 evaluation. A failure returns the classified SQLite error. If `ScanStopped` is set, it writes nothing, and `cut_short` stands. An all-local-failure scan also writes `completed`, because the scan did finish normally.
3. **`no_source_scanned` in the terminal failure write.** When the pipeline fails the run for an error with `errors.Is(err, discovery.ErrNoSourceScanned)`, it uses the store method that sets status `failed` and `interrupt_reason`. That method also sets `discovery_scan_outcome = 'no_source_scanned'` in **the same UPDATE**, chosen by an explicit parameter, never by inspecting reason text. If that write loses to Stop (`stop_requested_at` set), nothing changes, `completed` stands, and the run reads as stopped with a finished scan. That is true.

Prefetch, persist and later phases never write the column, so a later failure cannot turn a finished scan into `cut_short`.

**Resume.** A resume that repeats discovery overwrites the value through writes 1–2: `cut_short` at its scan start, then `completed` if it finishes. A resumed scan that completes therefore replaces an earlier `cut_short`. A resume that starts after discovery keeps the stored value from the execution that scanned. A `failed` run is never resumed, so `no_source_scanned` is final. Run Now's `interrupt_reason = NULL` reset and startup recovery do not touch the column. These updates are not guarded by `stop_requested_at`: they record what the scan did, not a lifecycle transition.

**Read API.** `GET /api/runs/{id}` adds `discovery_scan`, read in the same row as `status`:

- `null` when the stored value is `NULL`;
- `"in_progress"` when the stored value is `cut_short` and `status` is `running`;
- otherwise the stored value (`completed`, `cut_short` or `no_source_scanned`).

An unrecognized stored value is reported as `null` with a server warning. It is not a diagnostic and carries no text.

**UI rule.** A run with zero rows shows no coverage block, whatever the value. With one or more rows, apply the first matching rule:

| `status` and `discovery_scan` | Design variant |
|---|---|
| `failed` and `no_source_scanned` | "Run failed. No channels were scanned successfully." plus the list |
| `cut_short` | **Scan cut short:** "Channel scanning stopped before it finished. Results are incomplete." / "See the run status for the cause." / "Channels recorded as not scanned:" plus the list / "YuHaul has no record of which other channels were scanned." Add "Completed is the run's saved status; it does not mean every channel was scanned." when `status` is `completed`. |
| `completed`, or `no_source_scanned` with any status other than `failed` | **Neither:** "Some channels could not be scanned. Results are incomplete." plus the list |
| `in_progress` | The list under "Some channels could not be scanned. Results are incomplete." The run is still running; the designer confirms or replaces this copy (non-blocking). |
| `null` (rows exist) | Not reachable: write 1 precedes every row. Treat as an integrity anomaly and use **Run-wide cause, scan stage not established** ("This run did not complete normally, so results are incomplete. See the run status." plus the list) only when `status` is `failed`, `interrupted` or `stopped`. For `completed`, show only the list under "Channels recorded as not scanned:" with no coverage sentence. |

The **Run-wide cause, scan stage not established** variant otherwise has no reachable case on this build. It remains the design's rule for evidence that does not state the scan stage.

**Reuse.** ARS-001's later assessment refines, but does not replace, this column. When it lands, its writers keep setting `discovery_scan_outcome` as a coarse projection:

| ARS-001 assessment | `discovery_scan_outcome` |
|---|---|
| `blocked`, or an unsettled sealed scope at a terminal or interrupted end | `cut_short` |
| `complete`, `mixed` or `no_attempts` | `completed` |
| `all_local_failed` | `no_source_scanned` |
| `eligibility_changed_no_coverage` | `completed` |

Readers that understand `discovery_acquisition` prefer it. Runs from this build keep their coarse value.

**D1 isolation flag.** `runWithClient` takes an explicit `isolateSources bool`, separate from `byID`. `RunBound` passes `true`; the legacy `Run` passes `false`. `fetchNewVideos` applies D1 and §2/§3 only when `isolateSources` is true. Neither flag is derived from the other (ADR-001 D1).

## 5. Write boundary for the coder

Work on top of the interim branch (`interim-uploads-isolation`). Allowed changes:

- **New** `internal/store/migrations/030_discovery_source_failure.up.sql`, exactly §1, plus store migration tests.
- **New** `internal/state/source_failure.go`: the channel-ID validator, `RecordUploadsSourceFailureTx` (the §2 statement), `PermanentSourceFailureChannels(runID)` (§3), and the paged reader with `channel_name` join and read-side vetting (§4.2). Plus tests.
- `internal/discovery/engine.go`, only: the §2 write and validation at the D1 branch, the §3 same-run skip, and the D7 log moved after commit.
- New `internal/server/discovery_source_handlers.go`, plus registration in `server.go` and the run handlers (§4.1, §4.3), with tests.
- Remove D6: the `discovery_incomplete` projection in `internal/stats/store.go`, `internal/state/models.go` and the SPA, if present.
- Web: a new client function and types, the Errors source section, and the Run Detail coverage block, with tests. Reuse `failure-reason.ts`'s reason admission set.

**Amendment 2026-11-20 (§4.5 and the D1 flag), on top of `1a0bf19`:**

- `internal/store/migrations/030_discovery_source_failure.up.sql`: append the `ALTER TABLE run ADD COLUMN discovery_scan_outcome …` statement from §1. Extend the store migration tests to cover the column, its CHECK, `NULL` on existing rows, and a clean install plus the `029 → 030` upgrade.
- `internal/state`: a writer `SetDiscoveryScanOutcome(ctx, runID, value)` for `cut_short` and `completed`. Add a scan-outcome parameter to the terminal failure write: extend `Store.FailRun` and `Machine.FailRun`, or add a `…WithScanOutcome` variant, so `no_source_scanned` is set in the same UPDATE. Add a `DiscoveryScanOutcome` field to the run read and the public projection `discovery_scan` (`in_progress` mapping, unknown → `null`) in `models.go` and the run select in `internal/stats/store.go`. Plus tests.
- `internal/discovery/engine.go`: add the explicit `isolateSources` parameter (`RunBound` true, `Run` false), replacing the `byID` reuse at the D1 branch (`engine.go:165,180` in `1a0bf19`). Add write 1 before the first uploads request and write 2 at a normal scan end.
- `internal/pipeline/pipeline.go`: choose the terminal failure write with the `no_source_scanned` parameter when `errors.Is(err, discovery.ErrNoSourceScanned)`.
- Web: types and the Run Detail coverage-block selection rule from §4.5, using the design copy, with tests.

Unchanged:

- every existing migration;
- `error_entry`, `deferred_item`, `run_video_outcome`, Store A and the ledger;
- the queue handlers, other than the refusal test;
- quota, authorization and the `youtube` package;
- floor and eligibility logic;
- retry, Stop, Discard and resume semantics;
- the video Errors list and commands.

## 6. Central migration allocation (Plan node `rq-receipt-migration-order`)

**Decision.** `030` is allocated to this migration, ahead of the receipt-provenance migration.

**Why it is safe:**

- It is one new table. It reads or alters nothing that the receipt migration (ARS-005 §5.1.1, run-initiation), its §6.1.1 Store A clearing, capacity's `deferred_item` rebuild, or live-status's `run_video_outcome` and `deferred_item` changes touch.
- None of those depends on it, and it depends on none of them.
- The receipt migration's ordering obligation is relative to production `FetchForEvaluation` callers and receiving claims. This migration introduces neither.

**Rules for every later number**, so nothing is skipped or renumbered:

1. **Assign at integration, in order.** A migration gets its number only when it is integrated into the release candidate. The number is the next integer after the candidate's highest embedded migration. No number above `030` is reserved, and no placeholder file is created.
2. **No gaps.** A candidate may embed version *N* only if `1..N-1` are all embedded in that candidate or an earlier one. golang-migrate never applies a lower version added after a higher one has been applied, so a gap is a permanent skip.
3. **No renumbering after release.** A number that has reached any deployed or shared database is permanent. A migration still on a branch with a number that conflicts must be renumbered before integration, never after.
4. **Dependency order stays a plan rule, not a number rule:**
   - the receipt migration precedes ARS-001's Discovery migration (`run52-schema`);
   - receipt and Discovery precede live-status (`b42-migration`);
   - capacity's `deferred_item` rebuild and live-status's are combined, or the later one carries every earlier column and CHECK value.

   Expected sequence if they integrate in plan order: `031` receipt provenance (with the §6.1.1 Store A clearing), `032` Discovery page/scope/assessment, `033` live-status. That is an expectation, not a reservation.
5. **No overlap with this table.** ARS-001's later migration adds its scope and assessment tables and widens `deferred_item`. It must not alter `discovery_source_failure` except by additive indexes. Extending reasons and categories needs no DDL (§1).

[Run-initiation ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §5.1.1 and §6.1.1 are amended to match. The receipt migration is "the first receipt migration after `030`", not "the first migration after Search `029`". Its Store A clearing still goes in the earliest migration that must be applied before any production `FetchForEvaluation` caller.

## 7. Required tests (not executed)

1. **Migration.**
   - Clean database to version 30.
   - A real populated version-29 database (post-Search) to 30.
   - Verify:
     - every existing table, row, ID, trigger (including `028`'s) and the Search index is unchanged;
     - an `UPDATE` on the new table aborts;
     - a second permanent row for the same run and channel is ignored by the §2 statement;
     - rejected `channel_id`, `category` and `operation` values fail their CHECKs;
     - an older embedded chain ending at 29 refuses the version-30 database.
2. **Producer, through the guarded harness.**
   - A, B (404 `playlistNotFound`) and C: exactly one row for B with the exact fields, and the warning emitted after commit.
   - Inject an insert failure: the run fails with `sqlite_error`, there is no warning, B is not counted, and no row exists.
   - An invalid channel ID or an unadmitted reason: not isolated, and the run fails as before.
   - The run's `playlist_items_list` permanent ledger row is still present and separate.
3. **Same-run resume.**
   - Interrupt after B's row commits, then resume: no B request, B counted as failed, no second row.
   - The all-B case still yields `no_source_scanned`.
   - Crash before the insert: resume re-polls B.
4. **Fresh run.** The next run polls B. On failure it adds a new row under the new run, and the old row is unchanged. On success, there is no new row and the old row is unchanged.
5. **API through the real mux, with migrated SQLite reopened.**
   - Shapes, ordering, `limit` bounds, `before_id` and `next_before_id`, `400`, `404`, and the read-error `500`.
   - `channel_name` is present when a `channel` row exists and `null` otherwise.
   - An injected invalid `channel_id`, an unknown reason and an unknown category are withheld or nulled.
   - No playlist ID, token, URL or error text appears in the response.
   - `POST /api/errors/sources/retry` and `…/dismiss` return `400`, and the video Errors list, totals and commands are byte-for-byte unchanged.
6. **Browser.**
   - Errors shows a read-only Channel scan error after a restart, with the vetted sentence and no controls.
   - Run Detail shows the partial sentence and lists B.
   - A zero-row run shows no coverage claim.
   - The AC8 run shows "No channels were scanned successfully."
   - Keyboard, 320 px and screen reader checks are as in the design.

7. **Scan outcome (§4.5).** Run each case through the guarded harness and the real pipeline with migrated SQLite, then reopen.
   - A normal mixed scan reads `completed` and Run Detail shows "Some channels…".
   - A and B fail locally and a budget refusal then stops the scan; the run is `completed`. It reads `cut_short`, shows the scan-cut-short variant with the "Completed is…" sentence, and shows neither "Some channels…" nor "No channels were scanned successfully.".
   - A provider `quotaExceeded`, an authorization error and a cancellation during the scan each leave `cut_short` with the run's own status.
   - Kill the process mid-scan: `cut_short`, and the run is interrupted after startup recovery. Resume, and the scan completes: `completed`.
   - A resume that starts after discovery keeps its value.
   - All attempted channels fail locally: `failed`, `no_source_scanned`, set in the same UPDATE as the status. Inject a Stop that wins first: `stopped`, `completed`.
   - A later prefetch, persist or routing failure after a finished scan keeps `completed`.
   - A running run reports `in_progress`, simulations report `null`, and an injected unknown stored value reports `null`.
   - A zero-row run shows no block for every value.
   - The legacy `Run` path isolates nothing, even with `byID` forced true.

## Release notes

- **Rollback after `030`:** once migration `030` has been applied, any image built before `030` fails at startup, because its embedded migration chain ends at `029` and it refuses the version-30 database. Rolling back the image alone does not work. To roll back, stop the writers, restore a database backup taken before `030` was applied, then start the older image. Take that backup immediately before deploying the first `030` image. This applies to every later migration too. The operator-facing copy is [Upgrade notes](../../../user-docs/upgrade-notes.md); the PR body for the `030` release must repeat it.

## Open questions

- **Designer, non-blocking:** the zero-row Run Detail copy in §4.4.
- **No user-owned choice remains.** Clearing behavior, channel identity in Errors and logs, the reason text and read-only status are all set by accepted requirements, design and the 2026-11-20 answers.
