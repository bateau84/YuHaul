---
type: specification
title: ARS-005 — Re-queue Stranded Videos — Migration 026, Outcome Schema Step, Shared Predicates, Scan and Insert SQL, Ordered Batch Claiming, Metadata Precondition, Unavailable Outcome, Paged Deferred Queue, HTTP and Tests
description: Exact contract for BR-037. Covers migration 026 and the final-outcome schema step; the shared send-veto, guard, active-item and removed-by-user predicates; the stranded scan and chunked insert; PreviewStranded and RequeueStranded with their locks and refusal codes; claiming in the existing created-at order while quota remains (with a ready alternative for provider failures); the metadata precondition and the unavailable_on_youtube outcome; the paged GET /api/deferred prerequisite; HTTP bodies; and the conformance tests.
tags: [architecture, specification, runs, deferred-queue, recovery, run-14, send-guard, stranded, metadata, quota, outcomes, paging, migration, api, concurrency]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted — exact implementation contract, not implemented. Revised 2026-11-11 for the amended BR-037, BR-035 AC14 and BR-032 AC1, then corrected after critic review `ses_e33a11010ffe4XP0Rmw0ZVtQBs` (see §13). It realizes [ADR-006](../decisions/adr-006-requeue-stranded-videos.md). Reconciled 2026-11-11 (Plan revision 19): F3 is decided as **variant K** (user decision; BR-035 AC14 unchanged), so the body is the contract and Appendix A is kept only as a rejected alternative. F4 does not apply to production data. §7 gains page jumps, positions and a reason filter.

It amends [ARS-003](ars-003-stop-run-and-deferred-handoff.md) in three places:

- §1.3: one more deferred reason;
- §2 `listUnfinishedRunVideosTx`: one more finished outcome (§6.4 here);
- §2.1 claim path: a limit and a same-run exclusion, in the existing order (§5 here).

The ARS-003 handoff itself (§2 step 3) and its test H1(b) are unchanged.

It refactors `ClaimPlaylistSend` without changing its results ([authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) guard semantics unchanged). It replaces the unpaged `GET /api/deferred` (§7).

## What the two sides agree on

1. The Deferred view reads `GET /api/deferred?limit=50` and pages with `after_id` and `before_id`.
2. The browser reads `GET /api/stranded-videos` and shows the counts.
3. After the user confirms, the browser sends `POST /api/stranded-videos/requeue` with `{"expected_requeue": <requeue from the preview>}`.
4. The server either inserts every stranded video's deferred item and answers `200`, or refuses with a closed `code` and changes nothing.
5. Run reads carry `videos_unavailable`. Run videos may carry `final_outcome: "unavailable_on_youtube"`, and the run-videos `outcome` filter accepts that value.

Example:

- Preview: `{"available":true,"requeue":70000,"already_queued":{"videos":1180,"in_errors_queue":2},"removed_by_user":3,"blocked":{"videos":41,…},"not_ended":{"videos":12,"runs":[{"run_id":52,"videos":12}]},"no_origin_run":0,"retained_without_queue_item":{"videos":150,"removed_by_user":40}}`.
- Confirm: `200 {"outcome":"requeued","requeued":70000,"expected_requeue":70000,"differs_from_preview":false,…}`.
- Deferred view: `GET /api/deferred` → `{"deferred":[…50 items…],"total":70000,"by_reason":{"requeued_by_user":70000},"page":{"limit":50,"next_after_id":91450,"prev_before_id":null}}`.

## Derived from

- [BR-037](../../../requirements/yuhaul/br-037-requeue-stranded-videos.md) AC1–AC19, including the user decisions of 2026-11-11;
- [BR-035](../../../requirements/yuhaul/br-035-stop-active-run.md) AC14, amended 2026-11-11;
- [BR-032](../../../requirements/yuhaul/br-032-processing-outcome-distinction.md) AC1, amended 2026-11-11, and AC4–5;
- ADR-006;
- [BR-036](../../../requirements/yuhaul/br-036-mark-legacy-run-as-ended.md);
- [authorization ADR-004](../../youtube-authorization/decisions/adr-004-atomic-quota-admission-and-single-attempt-transport.md).

## Satisfies

See ADR-006's Satisfies table. This specification adds no behavior beyond it.

## Scope

In scope:

- migration 026 and the schema step `widenFinalOutcomeChecks`;
- the shared predicates;
- the `ClaimPlaylistSend` refactor;
- `PreviewStranded` and `RequeueStranded`;
- `InsertRequeuedTx`;
- batch claiming in the existing order (variant K, decided); variant S is kept in Appendix A as a rejected alternative;
- `FetchForEvaluation` and the dispositions of retried videos;
- the `unavailable_on_youtube` outcome and its readers;
- the paged deferred read;
- the HTTP contracts;
- SPA obligations;
- tests.

Out of scope: copy and layout (Designer).

## 0. Delivery order

Each step builds on the ones before it. Steps may share a change set, but none may ship before its predecessors.

1. **Before any code:**
   - this specification corrected (done);
   - the user's F3 decision (done 2026-11-11: variant K);
   - the read-only production check for critic finding F4 (done 2026-11-11: runs 17 and 21, 0 open retry rows; F4 does not apply);
   - the one-off count of legacy rows with a future `retry_after` (ADR-006 Decision 5).
2. **Migration 026 (§1.1).** It is the next migration after 025 and lands before any other migration, including video search's. It owns both `(video_id, status)` indexes. No other change creates indexes on those columns.
3. **Schema step, vocabulary and readers.** `widenFinalOutcomeChecks` (§1.2), the vocabulary (§1.3), and **every reader** of `unavailable_on_youtube` (§6.4). Readers exist before any writer.
4. **Paged `GET /api/deferred` and the paged Deferred view (§7, BR-037 AC19).** This step needs no migration and may land in parallel with steps 2–3.
5. **Shared predicates (§2).** Gates: V1 and V2 on the real send path.
6. **Claim and processing changes (§5, §6).** They use variant K (§5.2), read `absent` from the raw response ids, and include `commitUnavailableTx`. They must ship before the action is exposed: today's code would claim every item at once and route on empty metadata.
7. **Scan, preview, confirm, HTTP and SPA (§3, §4, §8, §9).** Gates: P1, the A-series, and J1 on a database shaped like production.

The stranded-videos routes and control must not be registered or shown in any build without step 4. Test X3 enforces this.

## 1. Schema

### 1.1 Migration `026_requeue_stranded.up.sql` (golang-migrate, one transaction)

1. Rebuild `deferred_item` exactly as migration 025 does: the same columns, `INSERT … SELECT` of every row with its id, `DROP`, `RENAME`, and the indexes `idx_deferred_item_status` and `deferred_item_retry_owner`. The only column change:

   ```sql
   deferral_reason TEXT NOT NULL CHECK(deferral_reason IN
     ('quota_exhausted','api_rate_limited','authorization_pending',
      'stopped_by_user','discarded_by_user','requeued_by_user'))
   ```

2. Add:

   ```sql
   CREATE INDEX deferred_item_video_status ON deferred_item(video_id, status);
   CREATE INDEX error_entry_video_status   ON error_entry(video_id, status);
   ALTER TABLE run ADD COLUMN videos_unavailable INTEGER NOT NULL DEFAULT 0;
   ```

### 1.2 Schema step `widenFinalOutcomeChecks` (Go, end of `ApplyMigrations`, after `widenRunStatusCheck`)

- **Per-table skip.** Read each table's `sql` from `sqlite_schema`. A table whose definition already contains `'unavailable_on_youtube'` is skipped. If both are skipped, the step does nothing.
- **Otherwise,** on one `db.Conn`:
  1. `BEGIN IMMEDIATE`;
  2. read `PRAGMA schema_version` as `v`;
  3. `PRAGMA writable_schema=ON`;
  4. run the rewrite below for each table that was not skipped, each required to change exactly one row with the old text present;
  5. `PRAGMA schema_version = v+1`;
  6. `PRAGMA writable_schema=OFF`;
  7. `COMMIT`;
  8. require `PRAGMA integrity_check` = `ok`.

  The two rewrites:

  ```sql
  UPDATE sqlite_schema SET sql = replace(sql,
    'CHECK(final_outcome IN (''accepted'', ''rejected'', ''excluded'', ''deferred'', ''failed'', ''not_yet_evaluated''))',
    'CHECK(final_outcome IN (''accepted'', ''rejected'', ''excluded'', ''deferred'', ''failed'', ''not_yet_evaluated'', ''unavailable_on_youtube''))')
  WHERE type='table' AND name='video' AND instr(sql, 'CHECK(final_outcome IN (''accepted'', ''rejected'', ''excluded'', ''deferred'', ''failed'', ''not_yet_evaluated''))') > 0;

  UPDATE sqlite_schema SET sql = replace(sql,
    'final_outcome IN (''accepted'',''rejected'',''excluded'',''deferred'',''failed'',''not_yet_evaluated'')',
    'final_outcome IN (''accepted'',''rejected'',''excluded'',''deferred'',''failed'',''not_yet_evaluated'',''unavailable_on_youtube'')')
  WHERE type='table' AND name='run_video_outcome' AND instr(sql, 'final_outcome IN (''accepted'',''rejected'',''excluded'',''deferred'',''failed'',''not_yet_evaluated'')') > 0;
  ```

- **Errors** fail `Open`. The step is idempotent, and completes a half-widened database.
- **Release note (no binary rollback).** Once any row holds `unavailable_on_youtube`, do not run an older binary. Its outcome validation rejects the value, and its per-run unfinished list would hand such a video to the deferred queue again on a stop or discard (BR-037 AC18). The widened CHECKs remain after a downgrade. Roll back only with a forward fix.

### 1.3 Vocabulary

- `queue.DeferredReasonRequeuedByUser = "requeued_by_user"`, added to `DeferredReason.Valid`. `HandOffUnfinishedTx` still accepts only its two reasons.
- `state.FinalOutcomeUnavailableOnYouTube = "unavailable_on_youtube"`, added to `VideoFinalOutcome.Valid`. `state.Run.VideosUnavailable int`, JSON `videos_unavailable`.
- `diag`:
  - add `requeued_by_user` to `GuardDisposition`'s `queue_reason` set;
  - add operation `requeue_stranded` (logs only);
  - add code `video_unavailable`. It is valid only with phase `routing` and operation `load_metadata`, and no other field. Stored text: `diag:v1 phase=routing operation=load_metadata code=video_unavailable`.
- `web/src/types.ts`: the deferred reason union gains `'requeued_by_user'`; the final-outcome union gains `'unavailable_on_youtube'`; `Run` gains `videos_unavailable`. `web/src/lib/outcome.ts` gets its own badge variant (label from the Designer).

## 2. Shared predicates

Each predicate is a Go `const` holding SQL text. It refers only to the aliases named here. Callers supply those aliases and never edit the text.

### 2.1 `state.LegacySendVeto`

Aliases:

- `old_run`: a `run` row;
- `subject`: columns `video_id`, `target_playlist_id` (may be NULL), `receiving_run_id` and `assume_acted` (0 or 1).

```sql
old_run.id <> subject.receiving_run_id
AND old_run.run_type IN ('manual','scheduled')
AND old_run.mutation_safety_version = 0
AND (
  EXISTS (
    SELECT 1 FROM route_decision old_decision
    LEFT JOIN config_version old_config ON old_config.id = old_run.config_version_id
    WHERE old_decision.run_id = old_run.id AND old_decision.video_id = subject.video_id
      AND old_decision.decision = 'matched'
      AND (
        old_config.id IS NULL
        OR NOT EXISTS (
          SELECT 1 FROM json_each(old_config.snapshot, '$.routes') route
          WHERE CAST(json_extract(route.value, '$.id') AS INTEGER) = old_decision.route_id)
        OR EXISTS (
          SELECT 1 FROM json_each(old_config.snapshot, '$.routes') route,
                        json_each(route.value, '$.actions') action
          WHERE CAST(json_extract(route.value, '$.id') AS INTEGER) = old_decision.route_id
            AND json_extract(action.value, '$.action_type') = 'playlist_insert'
            AND NOT EXISTS (
              SELECT 1 FROM executed_action resolved
              WHERE resolved.video_id = old_decision.video_id
                AND resolved.target_playlist_id = json_extract(action.value, '$.target_playlist_id')
                AND resolved.action_type = 'playlist_insert' AND resolved.outcome = 'succeeded'))))
  OR EXISTS (
    SELECT 1 FROM executed_action old_action
    WHERE old_action.run_id = old_run.id AND old_action.video_id = subject.video_id
      AND old_action.action_type = 'playlist_insert'
      AND (old_action.target_playlist_id IS NULL OR NOT EXISTS (
        SELECT 1 FROM executed_action resolved
        WHERE resolved.video_id = old_action.video_id
          AND resolved.target_playlist_id = old_action.target_playlist_id
          AND resolved.action_type = 'playlist_insert' AND resolved.outcome = 'succeeded')))
  OR EXISTS (
    SELECT 1 FROM video old_video
    WHERE old_video.id = subject.video_id
      AND (subject.assume_acted = 1 OR old_video.processing_state = 'acted')
      AND old_video.discovered_in_run_id = old_run.id
      AND old_run.current_phase = 'action_execution'
      AND NOT EXISTS (
        SELECT 1 FROM route_decision recorded
        WHERE recorded.run_id = old_run.id AND recorded.video_id = old_video.id
          AND recorded.decision = 'matched'))
  OR (
    old_run.current_phase = 'action_execution'
    AND EXISTS (
      SELECT 1 FROM run_video_outcome retry_outcome
      WHERE retry_outcome.run_id = old_run.id AND retry_outcome.video_id = subject.video_id
        AND (retry_outcome.final_outcome IS NULL
             OR retry_outcome.final_outcome IN ('not_yet_evaluated','failed')))
    AND (
      EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = subject.video_id AND d.retry_run_id = old_run.id)
      OR EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = subject.video_id AND e.retry_run_id = old_run.id))
    AND (subject.target_playlist_id IS NULL OR NOT EXISTS (
      SELECT 1 FROM executed_action resolved
      WHERE resolved.video_id = subject.video_id
        AND resolved.target_playlist_id = subject.target_playlist_id
        AND resolved.action_type = 'playlist_insert' AND resolved.outcome = 'succeeded')))
)
```

This is the current `playlist_guard.go:130–201` text with its positional parameters renamed to `subject` columns. There are only two other changes:

- `subject.target_playlist_id IS NULL OR` in branch 4;
- `subject.assume_acted = 1 OR` in branch 3.

**Why `assume_acted` (critic F1).** Branch 3 reads the video's live state. A claim resets the video to `not_yet_evaluated`, and the receiving run sets it to `acted` before it sends. So the scan must evaluate branch 3 as it will be at send time. With `assume_acted = 0`, the text is the old predicate exactly.

`ClaimPlaylistSend`'s legacy check becomes:

```sql
SELECT 1
FROM (SELECT ?1 AS video_id, ?2 AS target_playlist_id, ?3 AS receiving_run_id, 0 AS assume_acted) subject
JOIN run old_run ON <LegacySendVeto>
LIMIT 1
```

The parameters are `(c.VideoID, c.TargetPlaylistID, c.RunID)`. Nothing else in `ClaimPlaylistSend` changes.

**Open question for the send-guard owner** (non-blocking): is branch 3 meant to fire on the receiving run's own `acted` transition, or only on a video the version-0 run itself left `acted`? If the veto is narrowed, `assume_acted` keeps the scan aligned through the shared text. This specification does not change the veto.

### 2.2 `state.HeldSendGuard`

Alias: `subject.video_id`. This is a row source.

```sql
SELECT g.video_id, g.run_id, g.target_playlist_id,
  EXISTS (SELECT 1 FROM executed_action u
          WHERE u.run_id = g.run_id AND u.video_id = g.video_id
            AND u.target_playlist_id = g.target_playlist_id AND u.action_type = 'playlist_insert'
            AND u.outcome = 'failed' AND u.outcome_reason = 'mutation_application_uncertain') AS uncertain
FROM playlist_send_guard g
WHERE g.video_id = subject.video_id AND g.action_type = 'playlist_insert'
  AND NOT EXISTS (SELECT 1 FROM executed_action s
                  WHERE s.video_id = g.video_id AND s.target_playlist_id = g.target_playlist_id
                    AND s.action_type = 'playlist_insert' AND s.outcome = 'succeeded')
```

### 2.3 `queue.ActiveQueueItem`, `queue.ErrorsQueueItem` and `queue.RemovedByUser`

Alias: `subject.video_id`. Each is a boolean expression.

```sql
-- ErrorsQueueItem
EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = subject.video_id AND e.status IN ('active','retrying'))

-- ActiveQueueItem
(EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = subject.video_id AND d.status IN ('pending','retrying'))
 OR <ErrorsQueueItem>)

-- RemovedByUser: the most recent item over both queues was removed
(COALESCE((
  SELECT q.removed FROM (
    SELECT d.created_at AS at, 0 AS tbl, d.id AS id, (d.status = 'discarded') AS removed
      FROM deferred_item d WHERE d.video_id = subject.video_id
    UNION ALL
    SELECT e.created_at, 1, e.id, (e.status = 'dismissed')
      FROM error_entry e WHERE e.video_id = subject.video_id
  ) q ORDER BY julianday(q.at) DESC, q.tbl DESC, q.id DESC LIMIT 1), 0) = 1)
```

## 3. The stranded scan (package `pipeline`, file `stranded.go`)

### 3.1 Categories query (one read-only transaction)

```sql
WITH
prod AS MATERIALIZED (SELECT id, status FROM run WHERE run_type IN ('manual','scheduled')),
legacy_run AS MATERIALIZED (SELECT * FROM run WHERE run_type IN ('manual','scheduled') AND mutation_safety_version = 0),
unfinished AS MATERIALIZED (
  SELECT v.id AS video_id, v.video_id AS youtube_video_id,
         CAST(NULL AS TEXT) AS target_playlist_id, 0 AS receiving_run_id, 1 AS assume_acted
  FROM video v
  WHERE v.processing_state <> 'completed'
    AND (v.final_outcome IS NULL OR v.final_outcome = 'not_yet_evaluated')),
member(video_id, run_id) AS MATERIALIZED (
  SELECT u.video_id, v.discovered_in_run_id FROM unfinished u JOIN video v ON v.id = u.video_id WHERE v.discovered_in_run_id IS NOT NULL
  UNION SELECT rd.video_id, rd.run_id FROM route_decision rd JOIN unfinished u ON u.video_id = rd.video_id
  UNION SELECT d.video_id, d.retry_run_id FROM deferred_item d JOIN unfinished u ON u.video_id = d.video_id WHERE d.retry_run_id IS NOT NULL
  UNION SELECT e.video_id, e.retry_run_id FROM error_entry e JOIN unfinished u ON u.video_id = e.video_id WHERE e.retry_run_id IS NOT NULL
  UNION SELECT o.video_id, o.run_id FROM run_video_outcome o JOIN unfinished u ON u.video_id = o.video_id),
prod_member AS MATERIALIZED (SELECT m.video_id, m.run_id, p.status FROM member m JOIN prod p ON p.id = m.run_id)
SELECT subject.video_id, subject.youtube_video_id,
  <ActiveQueueItem>                                                       AS queued,
  <ErrorsQueueItem>                                                       AS in_errors,
  <RemovedByUser>                                                         AS removed,
  EXISTS (<HeldSendGuard>)
    OR EXISTS (SELECT 1 FROM legacy_run old_run WHERE <LegacySendVeto>)  AS blocked,
  EXISTS (SELECT 1 FROM prod_member pm WHERE pm.video_id = subject.video_id
          AND pm.status IN ('running','interrupted'))                    AS open_run,
  (SELECT MAX(pm.run_id) FROM prod_member pm WHERE pm.video_id = subject.video_id) AS origin_run_id
FROM unfinished subject
ORDER BY subject.video_id
```

Go assigns exactly one category per row, in this order:

1. `queued` → `already_queued`, and `in_errors` adds to its `in_errors_queue` sub-count;
2. `removed` → `removed_by_user`;
3. `blocked` → `blocked`;
4. `open_run` → `not_ended`;
5. `origin_run_id IS NULL` → `no_origin_run`;
6. otherwise stranded with that origin.

The implementer may prefilter or reorder joins for speed. The result must equal this query's result on the fixtures (test C1).

### 3.2 Detail queries (same transaction)

- **Blocked details:**
  - `HeldSendGuard` rows give reason `uncertain_addition` (when `uncertain`) or `unresolved_send_guard`, with `run_id = g.run_id`;
  - `SELECT subject.video_id, old_run.id FROM <blocked subjects> subject JOIN legacy_run old_run ON <LegacySendVeto>` gives reason `ambiguous_legacy_evidence`.

  `by_reason` counts each video once, under the first of `uncertain_addition`, `unresolved_send_guard`, `ambiguous_legacy_evidence`.
- **Not ended:** `SELECT run_id, COUNT(DISTINCT video_id) FROM prod_member WHERE status IN ('running','interrupted') AND video_id IN <not_ended videos> GROUP BY run_id ORDER BY run_id`. Per-run counts may overlap.
- **Retained without a queue item:**

  ```sql
  SELECT COUNT(*), COALESCE(SUM(<RemovedByUser>), 0)
  FROM (SELECT id AS video_id FROM video WHERE final_outcome IN ('deferred','failed')) subject
  WHERE NOT <ActiveQueueItem>
  ```

### 3.3 Types

```go
type StrandedVideo struct{ VideoID, OriginRunID int64 }
type BlockReason struct{ Reason string; RunID int64 } // uncertain_addition | unresolved_send_guard | ambiguous_legacy_evidence
type BlockedVideo struct{ VideoID int64; YouTubeVideoID string; Reasons []BlockReason }
type RunVideos struct{ RunID int64; Videos int }
type StrandedReport struct {
    Requeue, RemovedByUser, NoOriginRun int
    AlreadyQueued  struct{ Videos, InErrorsQueue int }
    Blocked        struct{ Videos int; ByReason map[string]int; Items []BlockedVideo }
    NotEnded       struct{ Videos int; Runs []RunVideos }
    RetainedWithoutQueueItem struct{ Videos, RemovedByUser int }
}
func scanStrandedTx(ctx context.Context, tx *sql.Tx) (StrandedReport, []StrandedVideo, error)
```

## 4. Preview and confirm (package `pipeline`)

```go
type StrandedPreview struct {
    StrandedReport
    Available         bool
    UnavailableReason string // "" | startup_recovery_active | production_run_active | shutting_down
}
func (p *Pipeline) PreviewStranded(ctx context.Context) (*StrandedPreview, error)

type RequeueResult struct {
    Report              StrandedReport
    Requeued            int
    SkippedConcurrently int // planned - inserted
    Expected            *int
}
var ErrRequeueNotApplied     = errors.New("re-queue did not take effect")
var ErrRequeueOutcomeUnknown = errors.New("re-queue outcome could not be confirmed")
func (p *Pipeline) RequeueStranded(ctx context.Context, expected *int) (*RequeueResult, error)
```

**`PreviewStranded`:** takes no lock. `UnavailableReason` is `startup_recovery_active` while the gate is closed. Otherwise it is `production_run_active` when `productionRegistered(…, registeredRunIDs())` is true. It then runs `scanStrandedTx` in a `ReadOnly` transaction. The pipeline does not know about shutdown. The handler sets `UnavailableReason = shutting_down` and `Available = false` when the server is closing, which overrides any pipeline value.

**`RequeueStranded`.** `ctx` = `context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)`, supplied by the handler.

1. `refuseWhileRecovering()` → `ErrStartupRecoveryActive`.
2. `initiationMu.Lock()`; deferred unlock.
3. `productionMu.TryLock()`, or `ErrProductionRunActive`; deferred unlock.
4. `productionRegistered(…)` → `ErrProductionRunActive`.
5. **Phase A:** a `ReadOnly` transaction runs `scanStrandedTx` and returns `report` and `plan`. A read error → `ErrRequeueNotApplied`.
6. If `plan` is empty, return `Requeued: 0` with nothing written.
7. **Phase B:** `qs.WithRetryTx(ctx, func(tx) error { n, err = qs.InsertRequeuedTx(ctx, tx, plan, now) })`. `WithRetryTx` begins a deferred transaction (`BeginTx(ctx, nil)`). Its first statement is the no-op write `UPDATE run SET id = id WHERE id = -1`, which takes SQLite's writer lock before `fn` runs. The wait for that lock is bounded by `busy_timeout`, and nothing retries the transaction. An error → `ErrRequeueNotApplied`. `queue.ErrCommit` → `ErrRequeueOutcomeUnknown`.
8. Log `stranded videos requeued` with `requeued`, `planned` and the counts (no video IDs).

Lock order: `initiationMu` → `productionMu` → `runsMu`.

**`queue.Store.InsertRequeuedTx(ctx, tx, plan, now) (int, error)`.** For each chunk of at most 2,000 pairs, marshalled as a JSON array of `[video_id, origin_run_id]`:

```sql
INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at)
SELECT subject.video_id, subject.origin_run_id, 'requeued_by_user', NULL, 0, 'pending', ?2
FROM (SELECT CAST(json_extract(j.value, '$[0]') AS INTEGER) AS video_id,
             CAST(json_extract(j.value, '$[1]') AS INTEGER) AS origin_run_id
      FROM json_each(?1) j) subject
JOIN video v ON v.id = subject.video_id
WHERE v.processing_state <> 'completed'
  AND (v.final_outcome IS NULL OR v.final_outcome = 'not_yet_evaluated')
  AND NOT <ActiveQueueItem>
  AND NOT <RemovedByUser>
  AND NOT EXISTS (<HeldSendGuard>)
```

`?2` = `now.UTC().Format(time.RFC3339)`. It returns the sum of `RowsAffected` and writes nothing else.

## 5. Claiming in the existing order while quota remains (packages `queue`, `pipeline`; BR-035 AC14 amended)

### 5.1 Candidate list

The signature changes to `ListDeferredRetryCandidates(ctx, now time.Time, receivingRunID int64, limit int) ([]DeferredItem, error)`. The query keeps today's text (`retry.go:471–476`), including its selection, eligibility and `ORDER BY d.created_at, d.id`. It gains only two things:

```sql
-- appended to the existing WHERE clause
AND NOT EXISTS (SELECT 1 FROM run_video_outcome o WHERE o.run_id = ?receivingRunID AND o.video_id = d.video_id)
-- appended after the existing ORDER BY
LIMIT ?limit
```

`receivingRunID = 0` excludes nothing. With migration 026's `deferred_item_video_status` index, each correlated subquery is an index range.

**Order.** The order is unchanged: each eligible video's oldest outstanding row, ordered by `created_at`, then `id`. Current code never sets `retry_after` to a future time, so every row that current code writes becomes eligible when it is created. Creation order is therefore eligibility order, which is what amended AC14 requires.

- A claimed and released item keeps its `created_at`, and so its place.
- The ARS-003 handoff (§2 step 3) and its test H1(b) are **not** changed.
- The first draft's eligible-since order was withdrawn (ADR-006 Decision 5).
- Rows from older code that still have a future `retry_after` are counted once before release (§0 step 1). A non-zero count is a revisit trigger.

### 5.2 Run loop (variant K — decided 2026-11-11)

- `const retryClaimBatch = 500` (package `pipeline`).
- `createClaimedRunLocked`: `ListDeferredRetryCandidates(ctx, now, 0, retryClaimBatch)`, then claims as today.
- `processDeferredItems(ctx, runID)`:
  1. `ids := ListRetryVideoIDs(runID)`. Run the metadata precondition for `ids` (§6.2), then each video's disposition.
  2. Leave the loop if any of these holds:
     - the run's `initiated_by` is not `run_now` or `schedule`;
     - `ctx.Err() != nil`;
     - `ledger.CanAfford(ctx, quota.OpVideosList.UnitCost())` is false or fails;
     - §6.2 reported a quota refusal in this pass.

     Nothing else leaves the loop. In particular, fetch suspension does not.
  3. `items := ListDeferredRetryCandidates(ctx, now, runID, retryClaimBatch)`. If it is empty, leave.
  4. In `qs.WithRetryTx`, recheck the run row (`running`, `stop_requested_at` NULL); if it fails, claim nothing and leave. Otherwise call `ClaimDeferredGroupTx` per item in list order. A lost claim (`claimed=false`) is skipped: the item was taken by a live owner at that moment.
  5. Go to 1.
- `RetryQueueEntry` uses the same `processDeferredItems`. Its `initiated_by` is `queue_retry`, so it never claims more.

Variant S, which would have changed step 2 and test B7, was rejected (Appendix A).

### 5.3 AC14 conformance argument

The run's claimed set is always a prefix of the created-at order, which equals the eligibility order (§5.1), less only these videos:

- videos owned live by another run, which are not eligible;
- videos this run already claimed;
- claims lost to a concurrent claimer.

The run leaves the loop only on quota exhaustion, stop, interruption or shutdown, or when no candidates remain. Unclaimed rows are not written. Tests B6 and B7 check this.

## 6. Metadata precondition, dispositions and the unavailable outcome

### 6.1 Metadata manager and read client (packages `youtube`, `metadata`, `cmd/server`)

**Raw item ids (critic F2).** `youtube.Client` gains `GetVideosWithReturnedIDs(ctx, ids) (details []VideoDetail, returned VideoIDs, err error)`, with `VideoIDs struct{ IDs []string; Complete bool }`.

- `returned.IDs` lists the `id` of every element of the raw `items` array of the successful response, including items that `GetVideos` skips because `publishedAt` does not parse (`client.go:638–643`).
- `Complete` is false if any raw item has an empty or unreadable `id`.
- `GetVideos` keeps its signature and behavior.

```go
type FetchResult struct {
    Details  []TransientMetadata
    Returned []string // raw item ids of the successful response
    Complete bool     // every raw item had a readable id
}
type BoundFetcher interface {
    FetchMetadataBound(ctx context.Context, binding auth.AuthorizationBinding, videoIDs []string) (FetchResult, error)
}
type FetchStatus string // current | fetched | unusable | absent | quota_refused | authorization_pending | rate_limited | failed
func (m *Manager) FetchForEvaluation(ctx context.Context, binding auth.AuthorizationBinding, videoIDs []string) (map[string]FetchStatus, error)
```

`cmd/server`'s `metadataFetcher` implements `FetchMetadataBound` with `boundReadOptions(f.auth, binding, f.ledger)`, the given binding, and `GetVideosWithReturnedIDs`.

`FetchForEvaluation`:

1. Mark `current` every id whose `GetDisplayMetadata` succeeds.
2. For the rest, take chunks of 50:
   - If `CanAfford(OpVideosList cost)` is false, mark this chunk and every later chunk `quota_refused`.
   - Otherwise call `FetchMetadataBound`.
     - **On success:** store `Details` with `store.Refresh(ctx, details, m.ttl, now)`. Then, for each requested id:
       - in `Details` → `fetched`;
       - in `Returned` but not in `Details` → `unusable`;
       - not in `Returned` and `Complete` true → `absent`;
       - not in `Returned` and `Complete` false → `unusable`.
     - **On error,** mark the chunk with the matching status:
       - `quota_refused` for `errors.Is(err, auth.ErrQuotaRefused)`;
       - `authorization_pending` for authorization unavailable, using the executor's existing test;
       - `rate_limited` for `*youtube.APIError` with `Reason` `rateLimitExceeded` or `userRateLimitExceeded`;
       - `failed` for anything else, including a body that does not decode.

       Mark every later chunk with the same status, without calling.

`absent` is produced only when a successful response, with every item id readable, does not include the id. This is BR-037's definition of *not returned by YouTube*. An `unusable` id is logged at warning with only the video id and the count of the chunk's unusable items.

### 6.2 Pipeline precondition (per claimed pass)

- Collect the YouTube ids of `ids` and call `FetchForEvaluation(ctx, *p.executionBinding, …)`. If `p.executionBinding` is nil, or fetching is *suspended* for this run, give every non-`current` id the status `failed` without a call.
- Once any chunk returns `authorization_pending`, `rate_limited` or `failed`, fetching is suspended for the rest of the run (an in-memory flag on the run control). `unusable` does not suspend fetching, because the call succeeded. Suspension does not end claiming (variant K, decided).
- `requeued(v)` := `EXISTS (SELECT 1 FROM deferred_item WHERE id IN <receipt.Deferred ids> AND deferral_reason = 'requeued_by_user')`.

| Status | `requeued(v)` | Otherwise |
|---|---|---|
| `current`, `fetched` | `processRetryVideo` | same |
| `quota_refused` | `deferRetryForInputsTx(…, quota_exhausted)`; report a quota refusal | same |
| `authorization_pending` | `deferRetryForInputsTx(…, authorization_pending)` | same |
| `rate_limited` | `deferRetryForInputsTx(…, api_rate_limited)` | same |
| `failed` | `RequeueRetryReceiptTx` (released unchanged); log `diag.Encode(err, "routing", "load_metadata")` | same |
| `unusable` | `RequeueRetryReceiptTx` (released unchanged); no outcome; fetching not suspended | same |
| `absent` | `commitUnavailableTx` (§6.3) | retry-failure transaction (`FinishRetryFailureTx` + `recordRetryVideoFailureTx`) with message `diag:v1 phase=routing operation=load_metadata code=video_unavailable` |

**`deferRetryForInputsTx(ctx, tx, runID, videoID, receipt, reason)`** runs in one `WithRetryTx`:

1. `ValidateRetryReceiptTx`;
2. `TransitionRunVideoStateTx(not_yet_evaluated → evaluating)`;
3. `TransitionRunVideoStateTx(evaluating → deferred, FinalOutcomeDeferred, &reason)`;
4. resolve owned error rows, first creating a pending deferred sink when the receipt has no deferred row (as `processRetryVideo`'s deferred branch does);
5. `AcknowledgeRetryDeferralReceiptTx(receipt)`.

A same-owner resumed claim that is already past `not_yet_evaluated` uses its current state on an allowed edge.

The item keeps its original `deferral_reason` (for example `requeued_by_user`) while the run outcome records the deferral reason. How the Deferred view shows this is for the Designer to decide (ADR-006 Findings). Nothing is changed here.

**Backstop.** `processRetryVideo` sets `requireInputsKey{}`. With it set, `videoContextForExclusion`, `detectVideos` and `routeVideos` return `errInputsUnavailable` instead of substituting an empty `TransientMetadata`. `processRetryVideo` maps that error to the `failed` row (released unchanged, no outcome). Without the key, behavior is unchanged.

### 6.3 `commitUnavailableTx(ctx, tx, runID, videoID, receipt)` — one `WithRetryTx`

1. `owned := ValidateRetryReceiptTx(receipt)`. An error rolls back; a removed group writes nothing.
2. `TransitionRunVideoStateTx(not_yet_evaluated → evaluating, FinalOutcomeNotYetEvaluated)`, then `TransitionRunVideoStateTx(evaluating → evaluated, FinalOutcomeUnavailableOnYouTube, nil, nil)`. Afterwards, `video` reads `processing_state='evaluated'` and `final_outcome='unavailable_on_youtube'`, and the run outcome row reads `final_outcome='unavailable_on_youtube'` with NULL reason and message.
3. `acknowledgeRetryReceiptTx(ctx, tx, receipt, owned)`: owned deferred rows become `completed`, and owned error rows `resolved`.

It writes no `route_decision`, `condition_audit`, `executed_action`, `playlist_send_guard`, `error_entry` or new `deferred_item`. It makes no executor or provider call. Afterwards it broadcasts `EventRunVideo` with outcome `unavailable_on_youtube` and `matched=false`.

### 6.4 Readers of the new outcome

- `unfinishedRunMembersTx` (`authority.go:380–381`): the finished list becomes `('accepted','rejected','excluded','deferred','failed','unavailable_on_youtube')`. This amends ARS-003 §2. A stop, discard or failure of the receiving run therefore never re-queues the video.
- `processRetryVideo`'s disposition check (`retry.go:245`) accepts `unavailable_on_youtube` as acknowledged.
- `RecomputeRunCountersTx` adds `videos_unavailable = (SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome='unavailable_on_youtube')`. Every `SELECT` of the counter columns, `scan` and JSON projection in `internal/state`, `internal/stats` (including the aggregation at `store.go:384–415`) and `internal/config` adds `videos_unavailable`. So do the `run.completed` payload (`events.go`) and the reset at `state/store.go:1045`.
- `GET /api/runs/{id}/videos?outcome=unavailable_on_youtube` filters by it, through `VideoFinalOutcome.Valid`.
- No reader may fold `unavailable_on_youtube` into rejected, excluded or failed. Test O2 audits this.

## 7. Paged deferred queue (BR-037 AC19) — `GET /api/deferred`

Next and Previous move with id cursors (keyset). A jump goes straight to a page by its position. An optional reason filter applies on the server. ADR-006 Decision 9 explains why sequential paging stays keyset while a jump may use a position.

### 7.1 Store

```go
type DeferredPageQuery struct {
    Include  DeferredStatus // "" (all) or DeferredStatusPending
    Reason   DeferredReason // "" = no filter; otherwise must satisfy DeferredReason.Valid
    Limit    int            // 1..200
    AfterID  int64          // 0 = unset
    BeforeID int64          // 0 = unset
    Page     int            // 0 = unset; >= 1 = page number
    Last     bool           // page=last
    // At most one of AfterID, BeforeID, Page, Last is set.
}
type DeferredPage struct {
    Items         []DeferredItem
    Total         int                    // include + reason filter
    QueueTotal    int                    // include only
    ByReason      map[DeferredReason]int // include only
    Number        int                    // page number (see below)
    TotalPages    int                    // max(1, ceil(Total / Limit))
    FirstPosition, LastPosition *int     // 1-based positions in the filtered set; nil when Items is empty
    NextAfterID, PrevBeforeID   *int64
}
func (s *Store) ListDeferredPage(ctx context.Context, q DeferredPageQuery) (DeferredPage, error)
```

It runs in one read-only transaction. `<filter>` is `status = 'pending'` for `pending` and `1=1` for `all`. When `Reason` is set, ` AND d.deferral_reason = :reason` is appended (a bound parameter).

- **Forward or first page:**

  ```sql
  SELECT … FROM deferred_item d JOIN video v ON v.id = d.video_id
  WHERE <filter> AND d.id > :after ORDER BY d.id ASC LIMIT :limit + 1
  ```

  With no cursor, `:after` = 0.
- **Backward:**

  ```sql
  … WHERE <filter> AND d.id < :before ORDER BY d.id DESC LIMIT :limit + 1
  ```

  The rows are then reversed to ascending order.
- **Jump:**
  - `Last` sets `Page = TotalPages`.
  - `offset = (Page − 1) × Limit`, then `… WHERE <filter> ORDER BY d.id ASC LIMIT :limit + 1 OFFSET :offset`.
  - A `Page` greater than `TotalPages` returns no items, with `Number = Page`.
- **Positions:**
  - `FirstPosition = 1 + (SELECT COUNT(*) FROM deferred_item d WHERE <filter> AND d.id < :first_id)`.
  - `LastPosition = FirstPosition + len(Items) − 1`.
  - `Number = (FirstPosition − 1) / Limit + 1` (integer division). For a jump it equals the requested page.
  - When `Items` is empty: for a jump, `Number` is the requested page; otherwise it is 1. Both positions are nil.
- `NextAfterID` is the last returned id when a further row exists past it, otherwise nil. `PrevBeforeID` is the first returned id when an earlier row exists, otherwise nil. Each is checked with the extra fetched row or one `EXISTS` query.
- **Totals:**
  - `Total` = `SELECT COUNT(*) FROM deferred_item d WHERE <filter>`, with the reason filter;
  - `QueueTotal` = the same count without the reason filter;
  - `ByReason` = `SELECT deferral_reason, COUNT(*) … GROUP BY deferral_reason`, without the reason filter.

`ListPendingDeferred` and `ListAllDeferred` stay for internal callers. No HTTP path returns an unbounded list.

### 7.2 HTTP

`GET /api/deferred?include=pending|all&reason=<deferral_reason>&limit=<1..200>&after_id=<id>|before_id=<id>|page=<n>|page=last`

| Outcome | Status | Body |
|---|---|---|
| Page | `200` | `{"deferred":[<unchanged item shape>…],"total":N,"queue_total":Q,"by_reason":{"<reason>":n,…},"page":{"limit":L,"number":p,"total_pages":P,"first_position":f\|null,"last_position":l\|null,"next_after_id":id\|null,"prev_before_id":id\|null}}` |
| Bad `include` | `400` | unchanged |
| Bad `limit`; a non-positive id; `page` not a positive integer or `last`; more than one of `after_id`, `before_id` and `page` | `400` | `{"error":"invalid page","code":"invalid_page"}` |
| `reason` not one of the six stored values | `400` | `{"error":"invalid reason","code":"invalid_reason"}` |
| Read failed | `500` | unchanged |

- With no parameters, the server returns the first 50 pending items in ascending id order, with every reason.
- A page past the end answers `200` with `deferred: []`, so the SPA can say the queue changed and offer the last page.
- Retry and Dismiss are unchanged.

### 7.3 SPA

- The Deferred view requests one page (`limit=50`) and shows `{first_position}–{last_position} of {total}`, and `queue_total` when a reason filter is applied.
- `Next page` and `Previous page` use the cursors.
- `First page` is `page=1`, `Last page` is `page=last`, and `Go to page` is `page=n`, valid for 1 ≤ n ≤ `total_pages`.
- The URL holds `page={number}` and `reason`. Reloading a URL performs a jump, so it is position-based.
- Removing an item reloads the current page with the same `after_id`, or the same `page` when it was reached by a jump.
- The reason select offers exactly the six stored reasons and `All reasons`; there is no `Other`. It resets to page 1 when the filter changes.
- The item rows, reason labels and Dismiss control are unchanged.

## 8. HTTP — stranded videos

Both routes are registered next to `POST /api/runs` (same access), and only together with §7 (§0).

### 8.1 `GET /api/stranded-videos`

| Outcome | Status | Body |
|---|---|---|
| Report | `200` | below |
| Read failed | `500` | `{"error":"the stranded videos could not be counted","code":"preview_failed"}` |

```json
{
  "available": true,
  "unavailable_reason": null,
  "requeue": 70000,
  "already_queued": {"videos": 1180, "in_errors_queue": 2},
  "removed_by_user": 3,
  "blocked": {
    "videos": 41,
    "by_reason": {"uncertain_addition": 2, "unresolved_send_guard": 4, "ambiguous_legacy_evidence": 35},
    "items": [{"video_id": 812, "youtube_video_id": "dQw4w9WgXcQ",
               "reasons": [{"reason": "ambiguous_legacy_evidence", "run_id": 14}]}]
  },
  "not_ended": {"videos": 12, "runs": [{"run_id": 52, "videos": 12}]},
  "no_origin_run": 0,
  "retained_without_queue_item": {"videos": 150, "removed_by_user": 40}
}
```

`unavailable_reason` is one of `startup_recovery_active`, `production_run_active`, `shutting_down`, or `null`. `items` is complete and ordered by `video_id`.

### 8.2 `POST /api/stranded-videos/requeue`

Body: `{"expected_requeue": <int ≥ 0>}`, or empty. Handler order:

1. parse;
2. `trackRun()`;
3. `RequeueStranded(detachedCtx, expected)`;
4. `runWG.Done()`;
5. respond.

| Outcome | Status | Body |
|---|---|---|
| Inserted | `200` | `{"outcome":"requeued","requeued":N,"skipped_concurrently":K,"expected_requeue":E\|null,"differs_from_preview":bool, <all §8.1 category fields except available>}` |
| Nothing to do | `200` | the same shape, with `"outcome":"nothing_to_requeue","requeued":0` |
| Bad body | `400` | `{"error":"invalid request body","code":"invalid_body"}` |
| Shutting down | `503` | `{"error":"server is shutting down","code":"shutting_down"}` |
| Startup recovery | `503` | `{"error":"<ErrStartupRecoveryActive text>","code":"startup_recovery_active"}` |
| Production run active | `409` | `{"error":"a production run is already active","code":"production_run_active"}` |
| Not applied | `500` | `{"error":"the re-queue did not take effect; nothing changed","code":"requeue_failed"}` |
| Outcome unknown | `500` | `{"error":"could not confirm whether the videos were re-queued; reload the preview; repeating is safe","code":"requeue_outcome_unknown"}` |

`differs_from_preview` = `expected_requeue != null && expected_requeue != requeued`. Bodies never carry raw error, SQL, credential or provider text.

## 9. SPA obligations (behavior only; the Designer owns copy and layout)

- The preview shows every count, including the Errors sub-count and the retained-without-item count, plus the blocked list (video, reason, run) and the not-ended runs. It offers confirm only when `available` and `requeue > 0`.
- Confirm sends the preview's `expected_requeue`. Cancelling sends nothing.
- The acknowledgement shows `requeued` and says when `differs_from_preview` is true. Each refusal `code` has its own message.
- `requeued_by_user` has its own deferred-queue label.
- `unavailable_on_youtube` has its own badge in Run Detail, its own count on run summaries (`videos_unavailable`), and its own option in the outcome filter.
- The Errors queue shows `video_unavailable` (other retried videos only).
- The Deferred view is paged (§7.3).

## 10. Invariants

1. After a successful confirm, every planned video that passed phase B has exactly one new pending `requeued_by_user` item with `retry_after` NULL, and no other row changed.
2. For every video and every target T without recorded success: `ClaimPlaylistSend` would refuse T ⇔ the video is `blocked`.
3. No retried video receives `rejected` or `excluded` in a run where its metadata was not current at evaluation.
4. `unavailable_on_youtube` exists only for videos whose claim receipt held a `requeued_by_user` row and whose id was missing from the raw item ids of a successful response in which every item id was readable. A returned but unparseable item never yields it. Such a video has no route decision, action, guard or Errors entry from that run, and no active queue item afterwards.
5. A claim transaction claims at most 500 groups. A run claims a prefix of the existing created-at order (§5.3) and never claims a video twice.
6. Every claimed video ends its run in one of these states:
   - a committed outcome with its item acknowledged;
   - a pending item;
   - still claimed by an `interrupted` run.
7. No HTTP response lists more than 200 deferred items.

## 11. Verification — the test list

Real migrated SQLite unless marked *unit*. "Controlled provider" is the counting `httptest` YouTube seam. "Snapshot" means every row of `video`, `run`, `deferred_item`, `error_entry`, `playlist_send_guard`, `executed_action`, `route_decision`, `run_video_outcome` and `transient_metadata`.

### 11.1 Schema and vocabulary

1. **M1 upgrade:** a 025 database with deferred rows of every reason and status → 026:
   - rows identical, ids kept;
   - the new reason inserts and an unknown one aborts;
   - indexes present;
   - `sqlite_sequence` continues above the max id;
   - `run.videos_unavailable` is 0 everywhere.
2. **M2 outcome step:**
   - both CHECKs admit `unavailable_on_youtube` and still reject `bogus`;
   - `integrity_check` = `ok`;
   - running `ApplyMigrations` twice bumps `schema_version` once;
   - reopening keeps the CHECKs;
   - `foreign_key_check` is empty.
3. **M3 vocabulary** (*unit*): `DeferredReason.Valid("requeued_by_user")`; `HandOffUnfinishedTx` rejects it; `VideoFinalOutcome.Valid("unavailable_on_youtube")`; `diag` accepts only `routing/load_metadata/video_unavailable`.

### 11.2 Shared predicates

4. **V1 veto refactor:** every existing `playlist_guard_test.go` case passes unchanged.
5. **V2 veto agreement on the real send path (AC12, AC15; critic F1).**

    **Seeds.** One video per veto condition, each stranded apart from its veto evidence:
    - branch 1 with a matched decision, with a missing config, and with a missing route;
    - branch 2;
    - branch 3: a version-0 run in `action_execution` discovered the video, no matched decision exists, and the video is **not** currently `acted` (for example `not_yet_evaluated`);
    - branch 4 on inactive retry rows, with success recorded for target A only;
    - a held guard;
    - an uncertain addition with its guard;
    - a guard whose target already succeeded (not blocked);
    - a control video with no evidence.

    The configuration routes every seed to target B, which has no success. Branch 4 also routes to target A.

    **Run.** Take the preview, then confirm. For each seed the scan reports as blocked, also insert a pending deferred item directly, standing in for a hypothetical unblocked re-queue. Start a new scheduled run against the controlled provider. It performs a real claim, metadata precondition, evaluation and send.

    **Assert.**
    - Every seed the scan reported as blocked, with its reason and run, gets `uncertain_blocked` from `ClaimPlaylistSend` for target B, and the provider counts zero `playlistItems.insert` for it.
    - Every seed the scan did not block, including the target-A-succeeded guard and the control, is re-queued by the confirm and is inserted to B exactly once.
    - The branch 3 seed is reported as blocked and is refused at send time, although it was not `acted` at scan time.
    - Branch 4: target A → `already_completed`, target B → refused.
    - With `assume_acted = 0`, the shared text returns the pre-refactor result for every seed (unit).
6. **V3 queue predicates** (*SQL*):
   - most-recent ordering across both queues with mixed `created_at` formats and same-second ties;
   - pending, retrying, active and retrying items count as active;
   - completed and resolved items do not;
   - `ErrorsQueueItem` covers only `error_entry`.

### 11.3 Scan and categories

7. **C1 categories (AC4):** the BR-037 "Verified by" seed set, plus:
   - a video in an `interrupted` version-1 run;
   - a video in an unended version-0 `running` run;
   - a video in two open runs;
   - a video whose only run was deleted;
   - committed `deferred`/`failed` videos with and without queue items, some of them removed;
   - two active Errors entries;
   - a committed `unavailable_on_youtube` video.

   Each category count is exact and the categories partition the unfinished videos. `in_errors_queue` = 2. `retained_without_queue_item` and its sub-count are exact. The unavailable video is in no category and gets no item. `blocked.items` names every reason and run.
8. **C2 origin:** a video reached by runs 14 (version 0, ended) and 30 (version 1, failed) gets origin 30. A video reached only by run 14 gets 14.
9. **C3 preview writes nothing:** the snapshot is unchanged and the provider counts zero requests. The `available` and `unavailable_reason` values match the gate and registration state.

### 11.4 Confirm

10. **A1 effect (AC6–8):** only stranded videos get exactly one new item, with:
    - `pending`, `retry_after` NULL, `retry_count` 0;
    - `requeued_by_user`;
    - `run_id` = the origin;
    - one shared `created_at`.

    Every other snapshot row is identical, including both Errors entries. Zero provider requests; no run row.
11. **A2 refusals (AC2–3):** confirm while each of these is active → `409 production_run_active`:
    - a Run Now run;
    - a scheduled run (registered, before `Execute`);
    - a queue-retry run;
    - a resumed run.

    Gated → `503 startup_recovery_active`. After shutdown begins → `503 shutting_down`. The snapshot is unchanged each time. A running dry run → success. Preview, start a production run, then confirm → refused.
12. **A3 atomicity (AC9):**
    - a trigger that aborts the 1,501st insert → `500 requeue_failed` and zero new rows;
    - SIGKILL of the child process while phase B holds the writer → on restart, zero or all of the planned rows (100 iterations).
13. **A4 commit uncertainty:** a seam that returns `queue.ErrCommit` → `500 requeue_outcome_unknown`. A following confirm is correct.
14. **A5 repeats (AC5, AC10):** two concurrent confirms → one `requeued` and one `nothing_to_requeue`. A third → `nothing_to_requeue`. One item per video.
15. **A6 concurrent dismissal (AC5, AC16):** a seam pauses between phase A and phase B. Dismiss an Errors entry and a deferred item of two planned videos. Result: `requeued = planned − 2`, `skipped_concurrently = 2`, and `differs_from_preview` is true.
16. **A7 races (AC16),** at least 100 iterations each, with `-race`: confirm against Run Now, scheduled `StartRun`, `RetryQueueEntry`, Discard, Mark as ended, Dismiss (both queues) and shutdown. Each outcome matches one serial order.

### 11.5 BR-036 order (AC14)

17. **E1:** a run-14-shaped version-0 `running` run (phase `discovery`) with 200 unevaluated videos: 3 with a held guard, and 1 whose deferred item it holds as `retrying`.
    - Preview: 196 `not_ended` for run 14, 3 blocked, 1 already queued. Confirm inserts none of them.
    - `EndLegacyRun(14)`. Preview: 196 stranded. Confirm inserts 196 items with origin 14.
    - The `retrying` item is claimed by the next run.

### 11.6 Claiming (AC11; BR-035 AC14 amended)

18. **B1 batch bound:** with 1,200 eligible items, a scheduled run claims 500 at creation, then 500, then 200. Each claim transaction has at most 500 groups, and no video is claimed twice.
19. **B2 quota stop and rollover:** quota covers the metadata for every item plus 3 additions, and 10 videos match.
    - The run adds 3. The other 7 are deferred with `quota_exhausted` and keep their pending items in place.
    - Below one unit of quota, the run stops claiming, and the unclaimed items are byte-identical.
    - Next quota day: the next run claims those 7 before any younger item.
    - No video is ever left without an active item or a committed outcome.
20. **B3 time limit:** a Run Now run hits its limit during batch 2 and is `interrupted`, holding at most batch 2. A scheduled run claims the unclaimed remainder in order. Run Now resume finishes batch 2.
21. **B4 stop and failure:**
    - Stop during batch 2 → claims released (ARS-003 H1).
    - Run-stopping failure → groups requeued.
    - An `unavailable_on_youtube` video committed earlier in the run is not handed off again.
22. **B5 queue retry does not widen.**
23. **B6 order (AC14, never pass over):**
    - Seed items A (oldest) to Z, created in that order, with quota for half of them.
    - Seed item Q, claimed and released by an earlier run.
    - Seed a stop handoff that creates item H last.

    Across consecutive runs, the claims are exactly the created-at order, then id. Q keeps its original place, and H comes after every older item. The claimed set after each run is a prefix of that order. Unclaimed rows are byte-identical between runs.
24. **B7 keep claiming (variant K, decided):** a provider 5xx on the first metadata chunk.
    - Fetching is suspended. The run keeps claiming batches until no candidates remain.
    - Each claimed video without current metadata is released unchanged.
    - Videos with current metadata are evaluated.
    - After the failure: zero further `videos.list` requests, and each video is claimed once.
25. **B8 ARS-003 regression:** ARS-003 Q2 (handoffs smaller than one batch) and H1 pass unchanged, including H1(b)'s `retry_after` NULL.

### 11.7 Metadata and the unavailable outcome (AC13, AC18; BR-032 AC1, AC4–5)

26. **D1 fetched:** 120 retried videos with expired metadata and title routes → 3 `videos.list` requests (50, 50, 20), with one ledger row each. Videos are evaluated on the fetched titles.
27. **D2 refusals:** quota refusal → `deferred`/`quota_exhausted`. Rate limit (`rateLimitExceeded`) → `deferred`/`api_rate_limited`. Authorization unavailable → `deferred`/`authorization_pending`. 5xx, a network error, or a body that does not decode → released unchanged. None is committed `unavailable_on_youtube`, `rejected` or `excluded`, and none gets an Errors entry.
28. **D3 unavailable (re-queued):** the provider answers successfully, and the raw `items` array omits 2 of 50 re-queued ids. For those 2:
    - `video` reads `evaluated`/`unavailable_on_youtube`;
    - the run outcome is `unavailable_on_youtube`;
    - the item is `completed`;
    - there is no Errors entry, no new deferred item, and no route decision, action or guard row;
    - no `playlistItems.insert`.

    `videos_unavailable` = 2. `GET /api/runs/{id}/videos?outcome=unavailable_on_youtube` returns exactly those 2. The other 48 are evaluated. A later run, a later preview and confirm, and a stop of a later run leave the 2 unchanged.
29. **D4 absent (other retried videos):** the same response for quota-deferred and stop-handoff items → `failed` with `video_unavailable` and one active Errors entry each. Never `unavailable_on_youtube`.
30. **D5 backstop:** `GetDisplayMetadata` fails after the precondition passed → the claim is released; no `rejected` or `excluded`.
31. **D6 discovery unchanged:** a freshly discovered video with unreadable metadata routes as today.
31a. **D7 returned but unusable (critic F2):** a successful response for 50 re-queued ids.
    - Two items have an unparseable `snippet.publishedAt`: those 2 → `unusable`, released unchanged (pending item, `not_yet_evaluated`, no run outcome, no Errors entry).
    - In a second chunk, one item has an empty `id` and 2 requested ids are missing: those 2 → `unusable`, not `absent`.
    - Fetching is not suspended: the next chunk is still requested.
    - The other ids are evaluated.
    - A later run, with the items parseable, evaluates all of them.
    - `GetVideos` (unit) still drops the unparseable item, and `GetVideosWithReturnedIDs` lists its id in `returned.IDs`.
32. **O1 counts and filter consistency (BR-032 AC4–5):** run history, run detail, statistics aggregation, `run.completed` events and the SPA show `unavailable_on_youtube` as its own category with equal counts.
33. **O2 reader audit:** a test enumerates every SQL `IN (…)` outcome list and Go outcome `switch` in `internal/`, through a maintained list checked against a `grep`. Each one handles the value explicitly.

### 11.8 Paging (AC19)

34. **G1 paging:** with 70,000 pending items, `GET /api/deferred` returns 50 items, `total` = `queue_total` = 70,000, `by_reason`, `number` 1, `total_pages` 1,400 and positions 1–50. Following `next_after_id` reaches every id exactly once, in ascending order, with exact positions. `prev_before_id` walks back. `page=last` returns positions 69,951–70,000 and `number` 1,400. `page=700` returns positions 34,951–35,000. `page=1401` returns `deferred: []` with `number` 1,401. With `reason=quota_exhausted` (51 seeded), `total` = 51, `queue_total` = 70,000, `total_pages` = 2, and paging reaches exactly those 51.
35. **G2 paging under change:** while paging, a run claims 1,000 items and the action inserts 500. Each item present throughout is returned exactly once. Claimed items disappear. New items appear only after the original last id.
36. **G3 bounds:** `limit=0`, `limit=201`, both cursors, a cursor with `page`, `page=0`, `page=x`, or a negative id → `400 invalid_page`. `reason=other` → `400 invalid_reason`. No response holds more than 200 items. Dismiss and retry behave as before.
37. **G4 SPA:** the Deferred view loads one page, shows the total and positions, pages both ways, and jumps with `First page`, `Last page` and `Go to page`. Reloading `?page=n&reason=…` restores the page. The reason filter is applied by the server, and the select has no `Other`. Reason labels and Dismiss are kept.

### 11.9 Performance, HTTP and composition

38. **P1 scale:** 100,000 stranded videos, 30 version-0 runs with evidence on 1,000 videos, and 10,000 other queue rows. Measure phase A, phase B's writer-lock duration, the preview and a `GET /api/deferred` page. Targets:
    - phase B ≤ 2 s;
    - preview ≤ 30 s;
    - a deferred page ≤ 200 ms, measured for the first page, `page=last` and `page=last&reason=quota_exhausted`;
    - a concurrent writer succeeds within `busy_timeout` during phase B.

    Record the numbers in §12.
39. **X1 contract:** every row of §7.2, §8.1 and §8.2 through the registered handlers. `runWG` stays balanced. No raw text in bodies.
40. **X2 SPA** (`web/`): every preview field, the confirm body, every refusal code, the difference message, and the new reason and outcome labels.
41. **X3 prerequisite (§0):** in the composed server, if `/api/stranded-videos` is registered, then `GET /api/deferred` without parameters returns at most 200 items and carries `total`.
42. **J1 journey** (`-tags=integration ./e2e`):
    1. seed run-14 shape;
    2. end it;
    3. preview;
    4. confirm;
    5. page the Deferred view;
    6. run a scheduled run with partial quota, where some videos are absent from the provider;
    7. restart;
    8. the next run continues oldest first.

    Expected: provider inserts = matched videos admitted by quota, and the absent videos are `unavailable_on_youtube` with no Errors entries.

## 12. Implementation record and deviations

Not implemented.

## 13. Revision of 2026-11-11

Revised to match the amended BR-037 (AC4, AC11, AC13, AC17–19), BR-035 AC14 and BR-032 AC1:

- §0 delivery order.
- §1: the outcome schema step and `run.videos_unavailable`.
- §2.3: `ErrorsQueueItem`.
- §3: the Errors sub-count.
- §5: eligible-since order, the ARS-003 handoff amendment, and the conformance argument; claiming continues after a non-quota provider failure. The first two were withdrawn later the same day (see below).
- §6: the `absent` split between re-queued and other retried videos, fetch suspension, `commitUnavailableTx`, and the reader changes.
- §7: the paged `GET /api/deferred`.
- Tests: M2, B6–B8, D3–D4, O1–O2, G1–G4 and X3 added.

**Critic review `ses_e33a11010ffe4XP0Rmw0ZVtQBs` (CHANGES REQUIRED), corrections of the same day:**

- **F1:**
  - §2.1 adds `subject.assume_acted`. The scan passes 1, so veto branch 3 is evaluated as at send time. `ClaimPlaylistSend` passes 0 and is unchanged.
  - V2 now runs a real claim, evaluation and send.
  - The open question for the send-guard owner is recorded in §2.1.
- **F2:**
  - §6.1 decides `absent` from the raw response item ids, through `GetVideosWithReturnedIDs`.
  - A returned but unparseable item, or a chunk with an unreadable id, is `unusable` and released unchanged, without suspending fetching.
  - Test D7 is added. Invariant 4 is tightened.
- **F3:** variant K stays in §5.2 until the user decides. Variant S is ready in Appendix A. *(Superseded by the reconciliation below: variant K decided.)*
- **Minor changes:**
  - §5 drops the eligible-since order, the §5.2 handoff amendment and the H1(b) change. The existing created-at order is kept, with a `LIMIT` and the same-run exclusion. B6 and B8 are adjusted.
  - §0 is restated as the build order: migration 026 lands first and owns both indexes.
  - §1.2 skips per table and gains the no-rollback release note.
  - §4 corrects the `WithRetryTx` wording and adds `shutting_down` to `UnavailableReason`.
  - §6.2 notes that the item keeps its reason after a quota re-defer (flagged for the Designer in ADR-006).
  - E1's blocked seeds use guards, because a run-14-shaped run is in phase `discovery`, where branch 3 does not apply.

**Reconciliation of 2026-11-11 (Plan revision 19, Loom Action b820f6ca):**

- **F3 decided: variant K.** The user kept BR-035 AC14 as amended and accepted the claim-and-release churn after a non-quota provider failure. §5.2, §5.3, §6.2 and B7 are the contract. Appendix A is a rejected alternative and must not be applied.
- **F4 does not apply.** The production check found the version-0 runs in `action_execution` to be runs 17 and 21, with 0 open retry rows. §0 step 1 is done.
- **§7:** added page jumps (`page=n|last`), exact positions, `total_pages`, `queue_total` and the `reason` filter; tests G1, G3, G4 and P1 extended. Next and Previous stay keyset.

## Appendix A — Variant S (stop claiming after a provider failure) — rejected alternative, not applied

**Rejected 2026-11-11:** the user chose variant K, and BR-035 AC14 stays unchanged. This appendix is kept only as a record of the alternative the critic recommended. It would have required BR-035 AC14 to allow "MAY stop claiming after a provider failure that prevents processing", and these three changes.

1. **§5.2 step 2** gains one condition: "fetching is suspended for this run (§6.2)". The sentence "In particular, fetch suspension does not" is deleted.
2. **§6.2** second bullet: "Under variant K, suspension does not end claiming. Under variant S it does (Appendix A)" becomes "Suspension also ends claiming after the current pass (§5.2)."
3. **Test B7** is replaced by **B7-S stop claiming:** a provider 5xx on the first metadata chunk of batch 1.
   - Fetching is suspended.
   - The run finishes batch 1: videos with current metadata are evaluated, and the others are released unchanged.
   - It claims no further batch. Every unclaimed item is byte-identical and pending.
   - The run holds no claim afterwards.
   - Zero further `videos.list` requests.
   - The next run claims, in created-at order, from the oldest released or unclaimed item.

It would also have extended §5.3's loop-exit sentence with "or after fetching is suspended". ADR-006's Findings row F3 reads "Decided: variant K".
