---
type: specification
title: ARS-005 — Re-queue Stranded Videos — Migration 026, Outcome Schema Step, Shared Predicates, Scan and Insert SQL, Ordered Batch Claiming, Metadata Precondition, Unavailable Outcome, Paged Deferred Queue, HTTP and Tests
description: Exact contract for BR-037. Covers migration 026 and the final-outcome schema step; the shared send-veto, guard, active-item and removed-by-user predicates; the stranded scan and chunked insert; PreviewStranded and RequeueStranded with their locks and refusal codes; claiming in the existing created-at order while quota remains (with a ready alternative for provider failures); the run dispatch stop (migration 032; quota derived from the ledger); the metadata precondition and the unavailable_on_youtube outcome; the paged GET /api/deferred prerequisite; HTTP bodies; and the conformance tests.
tags: [architecture, specification, runs, deferred-queue, recovery, run-14, send-guard, stranded, metadata, quota, outcomes, paging, migration, api, concurrency, dispatch-stop]
timestamp: 2026-11-23T00:00:00Z
---

**Status:** accepted — exact implementation contract, partly implemented: the run dispatch stop (§6.1.3) and the receiving precondition (§6.2, without §6.3/§6.4) are built (§12); the rest is not. Revised 2026-11-11 for the amended BR-037, BR-035 AC14 and BR-032 AC1, then corrected after critic review `ses_e33a11010ffe4XP0Rmw0ZVtQBs` (see §13). It realizes [ADR-006](../decisions/adr-006-requeue-stranded-videos.md). Reconciled 2026-11-11 (Plan revision 19): F3 is decided as **variant K** (user decision; BR-035 AC14 unchanged), so the body is the contract and Appendix A is kept only as a rejected alternative. F4 does not apply to production data. §7 gains page jumps, positions and a reason filter. **Amended 2026-11-12 for BR-039:** see §15. **Corrected 2026-11-23 (F7):** only the creating execution claims, and a resumed execution settles only its held claims (§5.2, §5.3, B3, B8; see §16).

**2026-11-22 dispatch stop (`rq-stop-arch`):** §6.1.3 pins the run dispatch stop. The `quota` level is derived from the run's `quota` ledger rows (PR #9, [quota-stop ARS-001](../../quota-stop/specifications/ars-001-provider-quota-stop-pacific-reset.md) §1) and is never stored. The `authorization` and `accounting_or_integrity` levels are stored in the raise-only column `run.dispatch_stop`, added by migration `032`. One read function and an admission choke point before token renewal enforce it. Implemented (migration `032` by `rq-stop-schema`; the read function, choke point, writers and readers by `rq-receiving-readiness`, §12).

**2026-11-18 receiving clarification:** §§5–6 also constrain the shared receiver used by [Discovery ARS-001](../../discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md). This does not specify new BR-037 admin controls or change the guarded send/result contract.

It amends [ARS-003](ars-003-stop-run-and-deferred-handoff.md) in three places:

- §1.3: one more deferred reason;
- §2 `listUnfinishedRunVideosTx`: one more finished outcome (§6.4 here);
- §2.1 claim path: a limit and a same-run exclusion, in the existing order (§5 here).

The ARS-003 handoff's reuse and removal outcomes (§2 step 3 and test H1(b)) are unchanged; its writes require [exact provenance](#511-durable-retry-claim-provenance).

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
- `run_video_outcome(run_id, video_id)` is the durable admission fence, not `retry_run_id` alone. Recheck it under the writer before any fresh claim, including direct/manual retry entry. An existing marker bars a new claim and reset; only an exact live receipt under §5.1.1 permits recovery, with no retry-count increment. A released previously claimed video is not an ordinary main-loop candidate of that receiving run, even if release cleared `retry_run_id`. On the integrated discovery path identify fresh native inputs by origin, and use durable receiving membership plus existing routability checks to exclude released foreign inputs from dispatch. Capacity-owned aggregate settlement without a live dispatch receipt is not a fresh claim or ordinary dispatch.
- The first draft's eligible-since order was withdrawn (ADR-006 Decision 5).
- Rows from older code that still have a future `retry_after` are counted once before release (§0 step 1). A non-zero count is a revisit trigger.

#### 5.1.1 Durable retry claim provenance

A claim of Deferred row 1 still names only row 1 after restart, even if row 2 now has the same owner. Membership prevents a second claim; the stored exact set identifies the first claim. Neither grants dispatch on its own. This contract realizes [authorization ADR-002](../../youtube-authorization/decisions/adr-002-exact-retry-group-receipts.md) and preserves BR-011 restart and integrity handling, BR-014 removal, BR-035 Stop/handoff, BR-037 receiving and BR-039 finality. Implemented in migration `031` (claim, recovery, validation, failure sweep, Stop/Discard handoff) and the final POST admission fence (see [authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md#authority-at-guard-preparation-and-post-admission)). Legacy-cohort evidence and a real-binary upgrade proof on production-shaped data are separate release evidence.

**Storage.** Add nullable `retry_claim_provenance` (`TEXT`, default SQL `NULL`) to `run_video_outcome`. The `(run_id, video_id)` key supplies receiving run and video. Version 1 is a JSON object with exactly `version` (integer `1`), `deferred_rows` and `error_rows` (arrays of objects with exactly `id` and `origin_run_id`, positive signed 64-bit integers). Each array is sorted by strictly increasing row ID; at least one row is required across the two arrays. Queue kinds have separate ID spaces. Writers emit both arrays even if one is empty. Reject duplicate fields, unknown fields or versions, and invalid or noncanonical member sets. Compare decoded identities, not JSON whitespace or object key order. Do not store credentials, provider data, outcomes or mutable owner/status in this value.

**One writer.** The queue claim reserves the SQLite writer before checking eligibility, ownership, finality and receiving membership. For a fresh winner, take the exact eligible rows by ID and immutable origin, verify the same rows make the guarded owner/status transitions, insert the new membership row with its complete value, and apply the existing reset rules in the same transaction (and run creation where already composed). No receipt on a losing, rolled-back or unconfirmed claim. No receipt on final-close without a claim. Manual and automatic claims keep their distinct sibling rules. Initialize the value only on insertion by the fresh claim; never overwrite or fill it later. Outcome upserts, handoff, release and removal preserve membership and provenance, even when they change or clear `retry_run_id`. They do not update the claim set or add a sink to it.

**Recovery and ending.** Only an explicitly admitted recovery loads a receipt for processing; a same-owner claim lookup uses the same stored value, not a second claim. Live owner rows can nominate work or reveal an integrity problem, but cannot supply receipt members. Keep existing recovery order and include original members in the descriptor even if they are now terminal or missing. Disposed-only history is not a work candidate. Reconcile a committed handoff only from its existing proven disposition and exact source/sink history. [Authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md#exact-retry-ownership-receipt) defines writer-time checks and typed refusals. Failure retention and the shared [Stop/Discard handoff](ars-003-stop-run-and-deferred-handoff.md#21-claim-ownership) use the same provenance, including for released members that are no longer visible by owner. Membership plus current routing evidence must not turn a released retry into native work.

**Compatibility.** The additive migration leaves old values `NULL`. Do not backfill from current or historical owner pointers, counts or timestamps. Terminal history needs no receipt; a genuinely native member requires independent native authority, not just `NULL` or absence of a current owner. An unfinished member that needs exact retry authority but has no trustworthy receipt is an integrity failure, distinct from proved removal or storage I/O failure. No dispatch, claim reset, synthetic handoff or guard change for that work. Apply existing startup integrity rules without widening the time-limit exception; a marked run whose required handoff cannot be proved fails Stop settlement, is not resumed and is not skipped. A compatible rollout requires evidence that no such ambiguous unfinished cohort needs automatic recovery or settlement, or separately authorized remediation based on independent evidence. This is not authority to inspect or change live data.

**Migration order.** `028` remains the deployed baseline for this decision; Search `029` remains reserved for the real Search migration. Apply the receipt migration strictly after actual `029` and any intervening allocations. *(Amended 2026-11-20: `030` is allocated to the discovery source-failure table, [Discovery ARS-002](../../discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md) §6, which has no dependency in either direction on this migration. The receipt migration takes the next number at integration under ARS-002 §6's rules, expected `031`.)* Allocate its number with the integration owner coordinating Search, Discovery and Capacity. No placeholder, renumbering, release-order change or migration over an omitted lower version. Stop older writers before migration; all writers that claim or settle must understand the new contract before production admission. Do not treat an older binary as a safe rollback writer or discard nonempty provenance to enable it.

**Required evidence.** Use the actual embedded migration chain on a clean database and on an upgrade from `028` through real `029`. Check old membership/outcomes, queue identities and history, guards, final-outcome triggers and Search state remain intact. Prove native and terminal legacy controls, and block dispatch and handoff of ambiguous unfinished legacy work after upgrade; include marked Stop and a current-format valid Stop as controls. Contrast SQL `NULL`, malformed JSON, duplicate members and unsupported versions; none can authorize work or be silently replaced. Inject a failure after claim writes but before commit: membership, provenance, owner transitions and reset are all absent on reopen. Reopen after confirmed commit: the exact mixed-kind set survives, an outcome upsert preserves it, and removing or releasing rows cannot shrink it or permit a same-receiver reclaim. Contrast a later eligible receiver and unchanged FIFO/due behavior. See the assembled counterexample and ordering proofs in [authorization ARS-001](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md#exact-receipt-and-admission-verification).

### 5.2 Run loop (variant K — decided 2026-11-11)

- `const retryClaimBatch = 500` (package `pipeline`).
- `createClaimedRunLocked`: if minimum-unit quota is known exhausted, create the run without claiming; otherwise `ListDeferredRetryCandidates(ctx, now, 0, retryClaimBatch)` then recheck claim eligibility under the writer. A preflight storage fault is not a quota result. A supported resume never calls it and is not a second initial claim.
- **Claim authority (corrected 2026-11-23, F7; ADR-006 Decision 5).** `mayClaim := !resumed && initiated_by ∈ {run_now, schedule}` is set once, when the execution starts. `resumed` is true for a Run Now resume admission (`admitLocked` resume branch) and for startup recovery (the row was `interrupted` when execution began). It is the same fact that selects the start checkpoint (`isResume`). It is not derived from `initiated_by`, membership, receipts, queue history or the run's past executions. `createClaimedRunLocked` and step 4 below, run only by an execution with `mayClaim`, are the only callers of a fresh deferred claim on the run path.
- `processDeferredItems(ctx, runID)`:
  1. `ids := ListRetryVideoIDs(runID)`. Run the metadata precondition for `ids` (§6.2), then each video's disposition. In a resumed execution, `ids` are the run's exact held receipts, restored under §5.1.1 recovery.
  2. Leave the loop if any of these holds:
     - `mayClaim` is false: the run's `initiated_by` is not `run_now` or `schedule`, or this execution is resumed;
     - `ctx.Err() != nil`;
     - the run's effective dispatch stop (§6.1.3) is `quota` or `accounting_or_integrity`, or the minimum-unit preflight `CanAfford(ctx, 1)` under the run scope returns a known refusal (reader R1);
     - an integrity/accounting fault blocks reliable admission or settlement, including a `RunDispatchStop` or `CanAfford` read error (propagate it as writer W3, do not treat it as quota).

     A non-quota provider failure does not otherwise leave the loop. In particular, fetch suspension and unrecovered authorization do not prohibit **local-only claim and settlement** while AC14's conditions hold. Section 6 governs what may run for those claims; claiming does not authorize a provider call.
  3. `items := ListDeferredRetryCandidates(ctx, now, runID, retryClaimBatch)`. If it is empty, leave.
  4. In `qs.WithRetryTx`, recheck the run row (`running`, `stop_requested_at` NULL, `dispatch_stop` not `accounting_or_integrity`; reader R2 in §6.1.3); if it fails, claim nothing and leave. Otherwise call `ClaimDeferredGroupTx` per item in list order. A lost claim (`claimed=false`) is skipped: the item was taken by a live owner at that moment.
  5. Go to 1.
- **A resumed execution** therefore runs step 1 only for its exact held receipts. It settles each once under its original ownership, with no new claim and no `retry_count` increment. It then leaves at step 2 before reader R1, and never reaches step 3 or 4. That holds after its held pass settles and while time, quota and a working provider remain. It writes nothing to any row it did not hold: no membership, receipt, reset, decision, outcome or provider work. An eligible Stop or Discard handoff or `requeued_by_user` item that the run never held stays pending and unchanged, first in order for the next new run (BR-035 AC14, BR-037 AC11). Native discovery in a resumed execution is unchanged (§5.1; ARS-006 §5).
- `RetryQueueEntry` uses the same `processDeferredItems`. Its `initiated_by` is `queue_retry`, so it never claims more.

Variant S, which would have changed step 2 and test B7, was rejected (Appendix A).

*(2026-11-22 hotfix: [quota-stop ARS-001](../../quota-stop/specifications/ars-001-provider-quota-stop-pacific-reset.md) §1 realizes the **quota** part of the run dispatch stop in step 2 and in §6.1. It is a ledger predicate over the run's `quota` rows in the current bucket. Until this batch loop is built, a run claims only at creation, and §1.4 there applies the minimum-unit preflight to that claim. The authorization stop and §6.2 are unchanged and still not built.)* *(2026-11-22, `rq-stop-arch`: §6.1.3 keeps that ledger predicate as the only source of the `quota` level and stores only the `authorization` and `accounting_or_integrity` levels, in `run.dispatch_stop`, migration `032`.)*

### 5.3 AC14 conformance argument

The run's claimed set is always a prefix of the created-at order, which equals the eligibility order (§5.1), less only these videos:

- videos owned live by another run, which are not eligible;
- videos this run already claimed;
- claims lost to a concurrent claimer.

The run leaves the loop on quota exhaustion, stop, interruption or shutdown, no remaining candidates, or a local integrity/accounting fault that prevents safe admission or settlement. Unclaimed rows are not written. Provider authorization refusal without an accounting/integrity fault is not that last case. Tests B6 and B7 check the claim order and the non-quota cases.

**Resumed executions (corrected 2026-11-23, F7).** Only the creating execution claims (§5.2 `mayClaim`). AC14 says that when Run Now resumes a run, the handed-over items do not have to be claimed "and they wait for the next new run". BR-037 AC11 gives re-queued items the same boundary ("runs that start as new runs"). A resumed execution, including startup recovery, settles only its exact held claims. It leaves every other eligible item unwritten and in place, so nothing is passed over, and the next new run's claim set is still a prefix of the order. Variant K is unchanged: it decides whether the creating execution keeps claiming after a non-quota provider failure, not who may claim. Test B3 and [ARS-003](ars-003-stop-run-and-deferred-handoff.md) Q3 check this boundary.

## 6. Metadata precondition, dispositions and the unavailable outcome

### 6.1 Metadata manager and read client (packages `youtube`, `metadata`, `cmd/server`)

**Raw item ids (critic F2).** `youtube.Client` gains `GetVideosWithReturnedIDs(ctx, ids) (details []VideoDetail, returned VideoIDs, err error)`, with `VideoIDs struct{ IDs []string; Complete bool }`.

- `returned.IDs` lists the `id` of every element of the raw `items` array of the successful response, including items that `GetVideos` skips because `publishedAt` does not parse (`client.go:638–643`).
- `Complete` is true only for an admitted, successfully accounted, successful response with a decoded top-level object and an explicitly present non-null `items` array, after every element has been structurally read and its nonempty, valid, unique ID matched to the requested set. An empty array is valid. A missing or null array, `{}`, `null`, non-array `items`, invalid item identity, duplicate/unrequested ID, truncated body, or nonempty/unusable `nextPageToken` is not complete for this by-ID request. Do not follow a token or infer omissions. An invalid envelope yields a typed invalid-response error; an incomplete identity set must not yield `absent` for any request. Already returned individually valid details may only be used when the envelope is valid and identities unambiguous; they cannot prove absence.
- `GetVideos` keeps its signature and behavior.

```go
type FetchResult struct {
    Details  []TransientMetadata
    Returned []string // raw item ids of the successful response
    Complete bool     // validated complete response; safe to infer omission
}
type BoundFetcher interface {
    FetchMetadataBound(ctx context.Context, binding auth.AuthorizationBinding, videoIDs []string) (FetchResult, error)
}
type FetchStatus string // current | fetched | unusable | absent | quota_refused | authorization_pending | rate_limited | failed
```

`cmd/server`'s `metadataFetcher` implements `FetchMetadataBound` with `boundReadOptions(f.auth, binding, f.ledger)`, the given binding, and `GetVideosWithReturnedIDs`.

`FetchForEvaluation` takes the invocation binding and one input requirement per unique requested ID (fields required by the captured configuration and evaluation path). It returns a status keyed by ID and the run-scoped disposition described below, plus a separate typed error if local classification/persistence failed. A dispatch stop is not an input status.

`FetchForEvaluation` applies this order:

1. Mark `current` only for an unexpired local row supplying the actual required fields of this evaluation, not merely because `GetDisplayMetadata` returns a row. The caller supplies relevant input requirements from its snapshot; presence/validity is separate from value, including valid zero duration or empty description. Classify `final` and `partly_done` first (§6.2).
2. For the rest, take chunks of 50:
   - If the minimum-unit preflight reports known insufficiency, return `quota_refused` with zero attempts. A preflight/store error is a local integrity/accounting failure, not refusal. A positive estimate never overrides final admission or a previously known provider stop.
   - Otherwise call `FetchMetadataBound`.
     - **On success:** persist usable `Details` with Store A's TTL- and index-aware upsert, preserving true field presence and accepting an evicted/missing prior row. Do not count an update of zero rows as saved. Then, for each requested id:
       - in `Details` → `fetched`;
       - in `Returned` but not in `Details` → `unusable`;
       - not in `Returned` and `Complete` true → `absent`;
       - not in `Returned` and `Complete` false → `unusable`.
     - **On error,** carry the primary read fact, accounting error and renewal error separately. Apply the guarded read disposition below before per-ID status; never flatten a wrapped provider error into `failed` or `absent` when it carried a run-wide stop or auxiliary fault.

       Preserve earlier chunks' durably established inputs and settled outcomes. If the run enters an authorization stop, mark the rejected chunk's unresolved inputs and every unsent input that still requires a fetch `authorization_pending`, not `failed`, `rate_limited`, or `absent`. No debit for the unsent suffix. A previously available local input is not invalidated by the stop, but it does not permit a subsequent network operation. An invalid 2xx envelope or unusable item is not evidence of absence.

`absent` is produced only when the validated, complete successful response defined above does not include the ID. This is BR-037's definition of *not returned by YouTube*. Log a fixed `unusable` diagnostic and counts, not provider payload or identifiers.

Per-ID fetch statuses describe inputs, not permission to dispatch. The boundary also returns a typed run dispatch-stop result and separate primary/accounting/renewal facts whenever present. Propagate a typed auxiliary fault through the error result, not a `failed` input status that loses the enclosing disposition. Stop evidence and the fault survive all wrappers through the run owner. In a compound error, a known quota reason still forbids further claims and remains a deferral reason; an accounting fault controls fail-closed propagation. A renewal observation can preserve account status even when another retry is forbidden.

**One guarded Data API disposition for production phases.** Apply this on `videos.list`, uploads and discovery reads, and propagate the corresponding dispatch stop from guarded action results without changing mutation classification. The selected run dispatch scope controls all subsequent production Data API paths, including discovery continuation, enrichment and executor calls. A narrower fetch suspension is not a dispatch stop.

| Evidence | Processing effect and accounting |
|---|---|
| Durable Stop/selection cancellation | Stop/interrupt under the existing lifecycle. Preserve separate facts for any already admitted attempt. It is not a source-local failure or an admission to try another call. |
| Outer `ReadError.Accounting`, or local quota/admission/store integrity error | No further dispatch; keep conservative held cost, and propagate the fault through existing retain-before-fail/recovery. Preserve the primary category/disposition and a separate renewal fact if present. Never turn a known quota/auth reason into a fabricated video failure; never pretend a durable debit or usable response after failed accounting. |
| Authorization refusal, identity changed, unresolved rejection or unavailable renewal | Follow OC-006. Classify the current binding/revision; preserve typed reconnect-required versus temporary/unavailable cause. After acknowledged accounting, allow at most one identical read retry if the authorization boundary proves accepted current access for the **same** identity; account it independently. If no safe authorized continuation, close the run's dispatch scope, preserve authorization deferral, and let variant K continue local-only as applicable. Do not retry a mutation. A first rejection is not an authorization stop if the permitted read recovers. |
| Definitive quota refusal | Typed pre-admission refusal costs zero. A proved admitted quota rejection (`quotaExceeded` or `dailyLimitExceeded`, or equivalent registered classifier evidence) has one `quota` ledger attempt even with positive local headroom. Close the dispatch scope and stop further claims under AC14. Defer remaining API-dependent work under existing quota semantics; no immediate retry. |
| Other bounded provider transport/5xx/rate/invalid-response failures with usable authority and known accounting | Preserve existing fetch-chunk suspension and local variant K. A rate limit does not imply quota exhaustion; no call contrary to applicable pacing. An independently ready video may continue only if the actual operation can pass the same authorization, pacing and quota guards; local claiming itself does not grant dispatch. |

Attribute each admitted send to its actual operation under the bound ledger owner. Persist the run-level known stop before proceeding beyond the boundary; on compatible same-run recovery reuse it. A provider quota stop is persisted by its own `quota` ledger row and is read back from it; there is no separate copy to reconstruct (§6.1.3). Check the stop before any run-owned provider work, including metadata lifecycle refresh and renewal for a new protected request, not just at the next batch. Retain any already-known accounting fault and separately known quota/auth facts; if the local store cannot record or reconcile them, fail closed in the current process using existing retention/recovery. Do not treat unknown accounting as a positive bank of quota or successful result. These rules do not claim crash-complete accounting for a request whose send and result were never durably captured (authorization ADR-004). A fresh eligible run performs ordinary authorized admission again.

### 6.1.1 Legacy Store A defaults (decided 2026-11-19, review `516b34d2` F1; clarified after critic `ses_e0cc26e27ffeKGNU9EOMRb4itV`)

Before the corrected writers, `Store.Refresh` (through the `FetchMetadata` adapter) and discovery's `upsertTransientMetadata` stored `''` for a missing description or channel name and `0` for a missing or unparsable duration. Discovery does this whenever its metadata prefetch is quota-refused or fails, which is the same population that becomes quota-deferred receiving work. A stored row does not record which writer produced it, so in an old row a real empty description or real zero duration cannot be told apart from a fabricated one. Step 1's presence rule (non-`NULL` is present) would otherwise accept those defaults as real inputs.

**Decision.** Clear the legacy defaults once, as SQL statements in a numbered migration:

```sql
UPDATE transient_metadata SET description      = NULL WHERE description      = '';
UPDATE transient_metadata SET channel_name     = NULL WHERE channel_name     = '';
UPDATE transient_metadata SET duration_seconds = NULL WHERE duration_seconds = 0;
```

- **Which migration.** Put the statements in the receipt-provenance migration of §5.1.1, the first receipt migration after the discovery source-failure migration `030` (amended 2026-11-20; `030` creates one unrelated table and has no `FetchForEvaluation` caller). The receiving claim path, the first production caller of `FetchForEvaluation`, needs that migration before it may claim (§5.1.1 *Migration order*). It takes no new number and keeps the fixed order (`029`, `030` source failures, then receipt, then the Discovery page/scope migration). If integration splits or reorders those migrations, the statements go in the earliest migration that must be applied before any production `FetchForEvaluation` caller can run. They never go in a later one.
- **Fallback home.** Suppose the release that enables the first production `FetchForEvaluation` caller contains no receipt-provenance migration, for example because that work was dropped or deferred. Then the statements go in a dedicated data-only migration, numbered by the integration owner after the actual `029` and any numbers already allocated. It must be applied before that caller can run. It has no down step that restores values. Do not skip the clearing because the receipt migration is absent, and do not move it into a Go startup step.
- **Writer boundary.** The binary that carries the migration must also contain the corrected writers. Discovery `persist`, the `FetchMetadata` adapter with `Store.Refresh`, and `Store.Upsert` callers keep the true field presence reported by the by-ID read (`VideoDetail.Present`). They store every supplied value as it is, including a real `''` description and a real zero duration such as `P0D`, and write `NULL` only for a field that was not supplied or was invalid. The rule is "no default for an unsupplied field", not "never store `''` or `0`". §5.1.1 already requires older writers to be stopped before migration. So no legacy writer runs after the clearing.
- **Discovery prefetch failure.** A typed invalid-response result for a discovery `videos.list` batch means the batch supplied no details. Those videos keep their listing title, published date and thumbnail, and their description and duration are `NULL`. Discovery continues with later batches and with persisting the page. It does not return an error for the whole discovery pass and does not substitute defaults. Quota, authorization, accounting and interruption results keep their existing dispositions (§6.1 table and Discovery ARS-001 §3).
- **Exact scope.** Only those three columns change, and only where they equal the default. Do not change `title`, `fetched_at`, `refreshed_at`, `expires_at`, `expired_at`, `is_active`, row count or TTL. None of the three columns is in the `029` title-search update trigger (`UPDATE OF video_id, title, is_active, expired_at, fetched_at, expires_at`), so the search index is untouched. Every reader already scans these columns as nullable.
- **Why titles are not cleared.** The defaulting paths above do not fabricate a title. When the prefetch fails, discovery falls back to the listing title, not `''`, so there is no identified population of fabricated empty titles. A YouTube title is never legitimately empty, so the ambiguity this section resolves does not arise for titles. Clearing `title` would also fire the `029` search purge trigger and change which rows are indexable, for no demonstrated benefit. Revisit this if an upgraded database is found with `title = ''` rows.
- **How a cleared field behaves.** The effect depends on the path that reads it:
  - *Readiness-gated paths* (`FetchForEvaluation` callers): a `NULL` field that the evaluation requires is not `current`. The bound fetch reads it again and `UpsertFetched` stores the real value.
  - *Ordinary routing* (not gated by readiness): a `NULL` duration or description means **no match**, not "unavailable". A duration minimum or maximum exclusion does not exclude the video, and a condition on that field does not match (`router/engine.go`). This is the existing `NULL` behavior, applied to rows that previously held a fabricated `0` or `''`. Dry-run, Replay and Backtest over those old rows behave the same way. Display shows no value instead of an empty one or `0:00`.
  - *Resumed legacy runs (`action_authority_version = 0`)*: `admitLegacy` (`pipeline/authority.go`) is read-only. When an enabled route's snapshot has a duration minimum or maximum exclusion, it requires a non-`NULL` duration. A cleared duration therefore raises an authority error, and the run takes the existing `retainAuthorityFailure` path. Its unfinished videos are retained, not evaluated against `0`, and no re-fetch happens. This is the retention cost of the clearing for any path that continues a version-0 run. It is the same outcome as the row expiring under TTL, and it fails closed. It is accepted, because evaluating against a value that may be fabricated would violate the presence rule. Startup recovery and Run Now resume do not continue version-0 runs automatically (authorization ADR-005), so the cost arises only on a path that continues one explicitly.
- **Cost of the ambiguity.** A real `''` or `0` in an old row also becomes `NULL`. On readiness-gated paths that costs one accounted `videos.list` unit per 50 such IDs, only when a rule actually needs the field. No decision ever uses a fabricated value.
- **One time only.** The migration version is the marker. Do not add a startup step, a recurring sweep or a read-side rule that treats `''` or `0` as missing. Any of those would erase real values that the corrected writers store later.

**Rejected.** *TTL expiry alone:* no caller of `FetchForEvaluation` exists yet, but the receiving consumer is being built now and can ship well within the 30-day TTL. Rows that discovery wrote with defaults after a quota-refused prefetch are exactly the queued work it reads, and nothing enforces a 30-day wait. *Treat rows written before a cutoff as not ready:* this needs a durable epoch, which is itself a migration or setting. It also needs a permanent read-side rule. And `fetched_at` does not reliably identify the writer, because discovery stamps a resumed run's original start time. *Provenance column:* this needs a migration, a value from every writer and a check in every reader, all kept forever to handle a problem that lasts one TTL. It also still cannot classify old rows any better than clearing them.

**Required evidence.**

1. **Upgrade.** Upgrade from `028` through the real `029` to this migration, using seeded rows: legacy `''`/`0`/`''`, real non-empty values, already `NULL`, an expired row and an indexed title. Only the defaulted cells become `NULL`. All other columns, the row count, the `title_search` rows and their rowids are unchanged.
2. **Re-fetch.** On a cleared row, `FetchForEvaluation` with a description or duration requirement makes exactly one bound call and returns `fetched`. A returned real `''` and `P0D` are stored as non-`NULL`, and the next call returns `current` with zero attempts.
3. **One time.** After the next startup, a real `''` written by `UpsertFetched` survives, because the migration does not run again.
4. **Missing fields.** The corrected writers' tests show that a quota-refused discovery prefetch, and a refresh with missing fields, store `NULL`.
5. **Real values.** A successful discovery prefetch whose by-ID response supplies a real `''` description and `P0D` duration stores both as non-`NULL` (`''` and `0`). A later `Store.Refresh` through the `FetchMetadata` adapter, with the same real values, keeps them non-`NULL`.
6. **Invalid response.** A discovery prefetch batch that receives a typed invalid by-ID response persists those videos with `NULL` description and duration and their listing title, then continues with the next batch and the page commit. Contrast quota refusal and authorization refusal, which keep their existing dispositions.
7. **Ordinary routing.** With `NULL` duration and description, a duration minimum or maximum exclusion does not exclude the video and a condition on those fields does not match. A version-0 run with a duration exclusion and a cleared duration takes `retainAuthorityFailure` with no provider call.
8. **Release chain.** The embedded migration chain of the binary that enables the first production `FetchForEvaluation` caller contains these statements, in the receipt migration or the fallback migration.

### 6.1.2 Live-status additions (BR-042, amended 2026-11-19)

[Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §2–3 extends this read. Nothing above changes for a requirement without `Live`.

- `GetVideosWithReturnedIDs` sends `part=snippet,contentDetails,liveStreamingDetails`. `VideoIDs` gains `Live map[string]LiveStatus` (`none`, `live`, `upcoming`, `unconfirmed`), with one entry per returned ID. It is computed before the `publishedAt` skip, by the fail-closed rules of Live ARS-001 §2. Envelope validation, `Complete` and `absent` are unchanged, and so is `GetVideos`.
- `InputRequirement` gains `Live bool`. A stored row never makes a `Live` requirement `current`, so the ID is always read, even when its other inputs are held.
- Two fields carry the classification alongside `Statuses`:
  - `FetchResult.Live`, which `FetchMetadataBound` copies from `returned.Live`;
  - `FetchOutcome.Live`, which holds only IDs returned by a read in this call.

  Per-ID statuses keep the meanings above. The Store A schema and its writers are unchanged. The caller records the result on the run's own row (Live ARS-001 §4).
- Cost: a claimed open or partly-done item is read on every run that claims it, even when its route inputs are current ([Live ARD-001](../../live-status/design/ard-001-fresh-live-status-gate.md) *Consequences*).

### 6.1.3 Run dispatch stop: storage, writers and readers (decided 2026-11-22, Plan node `rq-stop-arch`)

**What it decides.** §5.2 step 2, §6.1 and §6.2 need a run-scoped stop with three levels: `authorization` < `quota` < `accounting_or_integrity`. This section says where each level is stored, who writes it and who reads it. The `quota` level already exists on `main`: [quota-stop ARS-001](../../quota-stop/specifications/ars-001-provider-quota-stop-pacific-reset.md) §1 (PR #9) derives it from the run's own `quota` ledger rows. The other two levels have no storage today. So:

- **`quota` is derived from the ledger and never stored.** The ledger is the only source of truth for "this run is quota stopped".
- **`authorization` and `accounting_or_integrity` are stored** in a new nullable column, `run.dispatch_stop`. They can only be raised.
- **The effective stop** is the higher-ranked of the stored value and the derived quota level.

**Example.**
- Run 70 claims 500 videos. Renewal fails during a metadata chunk, and the permitted same-identity retry does not recover. The pipeline raises `run.dispatch_stop` to `authorization` in the transaction of the first `authorization_pending` settlement. Run 70 keeps claiming and settling locally (variant K) but makes no further YouTube request. The stored value stays for the rest of run 70's life, including after a restart.
- Run 71 has no authorization problem. An insert is answered `403 quotaExceeded`. `Hold.Finalize` commits the `quota` ledger row (PR #9), and from that commit run 71's effective stop is `quota`. Nothing is written to `run.dispatch_stop`. After the next Pacific midnight, a same-run resume of run 71 can dispatch again, as PR #9 already allows.

**Why quota is not stored (alternatives).**

| Option | Result |
|---|---|
| **A. Derive quota from the ledger; store only the other two levels (chosen)** | One source of truth. It reuses PR #9's tested predicate, its restart behavior and its Pacific bucket unchanged. There is no copy that can disagree with admission and no recovery writer that rebuilds the copy after a crash. |
| B. Write `quota` to the column alongside the ledger row | Two facts for one meaning. The column is raise-only for the run's life, but the ledger stop ends at the reset. After a reset, the claim loop (reading the column) and admission (reading the ledger) would disagree. It also needs the recovery writer that Discovery ARS-001 had to add for the crash between the ledger row and the column write. It loses. |
| C. Make PR #9's predicate lifetime-wide to match a sticky column | It changes shipped behavior: a same-run resume after the reset could no longer dispatch. No requirement asks for that, and Discovery's reason for a sticky value ("no clearing writer, no reset-time evidence") does not apply to a derived value, which has neither. It loses. |
| D. Store nothing; derive every level | Not possible. An authorization stop depends on OC-006's retry outcome, and an accounting fault means the ledger itself may be unreliable. Neither can be read back from the ledger. §6.2 needs both to survive a restart ("restored authorization stop"). |

A **known local-budget refusal** (the minimum-unit preflight, with no ledger row) is not a run stop and is not stored. `CanAfford` evaluates it fresh at every check, so it holds for as long as it is true and ends when the budget allows again. This is PR #9's existing behavior.

#### Schema: migration `032_run_dispatch_stop.up.sql` (Plan node `rq-stop-schema`)

`032` is the next free number on `main` (latest `031`; PR #9 added none). Under the central allocation rule the number is final when PR-R rebases for merge: if another migration lands first, this file takes `max + 1` and keeps the same content. It is one forward file with no down file, like `029`–`031`.

```sql
-- Run dispatch stop (run-initiation ARS-005 section 6.1.3). The run has proved
-- it must not start another run-owned YouTube request. Only the two levels that
-- cannot be read from the quota ledger are stored; the quota level is derived
-- from the run's own quota_ledger rows (quota-stop ARS-001 section 1) and is
-- never written here. NULL means no stored stop; old runs stay NULL.
ALTER TABLE run ADD COLUMN dispatch_stop TEXT
    CHECK (dispatch_stop IS NULL OR dispatch_stop IN ('authorization', 'accounting_or_integrity'));

-- Raise-only: authorization (rank 1) may become accounting_or_integrity
-- (rank 3); nothing lowers or clears a stored value.
CREATE TRIGGER run_dispatch_stop_raise_only
BEFORE UPDATE OF dispatch_stop ON run
WHEN OLD.dispatch_stop IS NOT NULL AND (
    NEW.dispatch_stop IS NULL OR
    (CASE NEW.dispatch_stop WHEN 'authorization' THEN 1 ELSE 3 END) <
    (CASE OLD.dispatch_stop WHEN 'authorization' THEN 1 ELSE 3 END))
BEGIN
    SELECT RAISE(ABORT, 'run dispatch_stop cannot be lowered or cleared');
END;
```

- **No backfill.** Every existing run reads `NULL`. No `quota` value is ever valid in the column. The CHECK refuses it, so a writer cannot create a second quota fact by mistake.
- **Schema steps.** `widenRunStatusCheck` edits the `run` definition in place by replacing the status CHECK substring. An added column does not touch that substring, so the step still applies on a fresh install (where it runs after `032`) and is skipped on an upgraded database. The rq-stop-schema test checks both.
- **Revisit trigger.** If a requirement later needs a quota stop that outlives the reset, or a stored "stopped on the local budget" fact, add it as its own fact. Do not add `quota` to this CHECK: a CHECK change on `run` needs another `writable_schema` step.

#### Ranks and the effective stop

| Level | Source | Rank | Claims afterwards (§5.2) | Provider requests afterwards |
|---|---|---|---|---|
| none | — | 0 | allowed | allowed under ordinary admission |
| `authorization` | `run.dispatch_stop` | 1 | allowed: local-only claiming and settlement (variant K) | none |
| `quota` | `EXISTS (SELECT 1 FROM quota_ledger WHERE run_id = ?R AND date = ?D AND outcome_category = 'quota')`, D = current bucket | 2 | none | none in bucket D |
| `accounting_or_integrity` | `run.dispatch_stop`, or the in-memory latch below | 3 | none | none |

**Effective stop** = the highest rank among the stored value, the in-memory latch and the derived quota level. A compound error raises to its highest level. A settlement already committed under a lower level (for example `authorization_pending`) stays as it is.

**One read function (package `quota`).**

```go
type DispatchStop int // StopNone, StopAuthorization, StopQuota, StopAccountingOrIntegrity (rank order)

// RunDispatchStop returns the effective stop of the run in ctx's run scope
// (quota.WithRun). Without a run scope it returns StopNone. Any read error is
// returned as an error; the caller treats it as accounting_or_integrity.
func (l *Ledger) RunDispatchStop(ctx context.Context) (DispatchStop, error)

// LatchRunStop raises the in-memory stop of ctx's run scope. It never lowers it.
func LatchRunStop(ctx context.Context, s DispatchStop)
```

- It reads `run.dispatch_stop` by primary key and PR #9's `runStopped` predicate for the current bucket (the same `bucketCalculator` as `CanAfford`), and combines them with the latch.
- **The latch** is a raise-only value in the run scope that `quota.WithRun` already creates (it becomes a small struct instead of a bare run ID). It survives `context.WithoutCancel`. A new execution (resume, restart) starts with a fresh latch and reads the stored value again. The latch exists only for a stored write that failed.
- PR #9's checks inside `Acquire` and `CanAfford` stay unchanged. `Acquire` remains the atomic quota check under `l.mu`.

#### Writers

Only the run's own production execution writes, under the SQLite writer, in a transaction with no provider I/O. Simulations (dry run, Replay, Backtest) never write and always read `NULL`. The statement is:

```sql
UPDATE run SET dispatch_stop = :v
WHERE id = :run
  AND (dispatch_stop IS NULL OR
       (CASE dispatch_stop WHEN 'authorization' THEN 1 ELSE 3 END) <
       (CASE :v WHEN 'authorization' THEN 1 ELSE 3 END));
```

Zero affected rows is not an error: an equal or higher stop is already stored. It is `state.Store.RaiseRunDispatchStopTx(ctx, tx, runID, level)`. Passing `StopNone` or `StopQuota` is a programming error and returns an error without writing.

**Order of every write:** first `quota.LatchRunStop(ctx, level)`, then the stored write. The stored write commits before the execution starts another run-owned provider request. If it fails, the latch still holds the stop, the execution makes no further provider request, and the classified SQLite error propagates under existing retention (fail closed).

| # | Writer | Level | Transaction |
|---|---|---|---|
| W1 | §6.2 precondition, when `FetchForEvaluation` returns a typed authorization disposition (OC-006: no safe authorized continuation) | `authorization` | The `WithRetryTx` of the first `authorization_pending` settlement it causes; its own `WithRetryTx` if it causes none |
| W2 | Executor, when an action result is `MutationNotIssued` with `DispatchRefusalAuthorization` (a mutation is never retried, so there is no safe authorized continuation). Raising is idempotent, so a refusal that R0 produced from an existing stored stop writes zero rows | `authorization` | The transaction that defers the action as `authorization_pending` (`executor.go` handoff) |
| W3 | Any run-owned path that sees an outer `ReadError.Accounting`, `auth.ErrQuotaAccounting`, a `CanAfford` or `RunDispatchStop` error, or another fault that prevents reliable admission or settlement | `accounting_or_integrity` | Its own `WithRetryTx`, best effort (the store may be what failed); the latch is authoritative for this execution |
| W4 | Discovery reads (PR-D, [Discovery ARS-001](../../discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §1.2) | as W1 and W3 | as W1 and W3 |
| — | Provider quota refusal | `quota` | **No write.** The `quota` ledger row committed by `Hold.Finalize` or `RecordAttempt` (PR #9) is the stop. If that row cannot be written, the result is an accounting fault, which is W3. |

There is **no recovery writer.** A same-run resume reads the stored levels and derives quota from the ledger. The crash window between a provider quota response and its ledger row is the one PR #9 already accepts (authorization ADR-004): at most one more request, which is refused and then stops the run.

#### Readers

| # | Point | Reads | Effect |
|---|---|---|---|
| R0 | **Admission choke point.** `auth.Service.dispatchScoped`, at the top of each loop iteration, **before `Resolve`**, when `ledger != nil` | `ledger.RunDispatchStop(ctx)` | `StopQuota` → `ErrQuotaRefused` (zero cost, as PR #9's `Acquire` refusal). `StopAuthorization` → new `auth.ErrRunAuthorizationStopped`, which `auth.IsPreAdmissionAuthorizationRefusal` accepts, so existing callers map it to `DispatchRefusalAuthorization` and `authorization_pending`. `StopAccountingOrIntegrity` or a read error → joined with `ErrQuotaAccounting` (fail closed). No renewal, no hold, no request. |
| R1 | §5.2 step 2, before each batch, in the creating execution only (a resumed execution has already left at `mayClaim`) | `RunDispatchStop(ctx)`, then `CanAfford(ctx, 1)` with the run scope | Leave the loop for `quota` or `accounting_or_integrity`, or when `CanAfford` refuses (the local budget, which also includes PR #9's run stop). A read error leaves the loop and propagates as W3. `authorization` does not leave the loop. |
| R2 | §5.2 step 4, the claim's `WithRetryTx` | The run row recheck also reads `dispatch_stop` | Claim nothing and leave if it is `accounting_or_integrity`. `authorization` still claims. The quota level is not re-read here. R1 evaluated it from the ledger just before, with no provider I/O in between, and only this run's own sequential execution writes this run's `quota` rows. |
| R3 | `createClaimedRunLocked` (initial claim) | Unchanged: PR #9's unscoped `CanAfford(ctx, 1)` | A fresh run has no stop. A supported resume is not a claim and claims nothing later either (§5.2). |
| R4 | §6.2 precondition, at the start of each claimed pass and before each `FetchForEvaluation` call | `RunDispatchStop(ctx)` | Classify with the effective stop before fetch suspension, a nil binding or the readiness backstop (§6.2). `authorization` → no fetch; inputs that need one are `authorization_pending`. `quota` → `quota_refused`. `accounting_or_integrity` → propagate. |
| R5 | `FetchForEvaluation`, before each chunk's minimum-unit preflight | `m.ledger.RunDispatchStop(ctx)` | Return the typed run disposition with zero attempts for this and every later chunk. The manager never writes the stop; its caller does (W1, W3). |
| R6 | Executor, before guard preparation for each action, next to the existing `CanAfford` pre-checks (`executor.go:344`, `:370`) | `e.ledger.RunDispatchStop(ctx)` | No guard preparation. `authorization` → `authorization_pending` deferral. `quota` → existing `deferActions` with `quota_exhausted`. `accounting_or_integrity` → fail closed. |
| R7 | `state.Store.ClaimPlaylistSend`, in its writer transaction | Its existing `SELECT … FROM run` also reads `dispatch_stop` | Any non-`NULL` stored value → new `state.ErrPlaylistSendDispatchStopped`, before any guard write. The executor then reads the effective stop (R6) to choose the disposition. The quota level is enforced by R0 and by `Acquire`. |
| R8 | Discovery engine and prefetch (PR-D) | `RunDispatchStop(ctx)` | Discovery ARS-001 §1.2. |

R0 is the backstop that makes a missed call site safe: every guarded run-scoped request, including metadata lifecycle refresh, live-status and capacity reads, and the token renewal that `Resolve` would start, passes it. R1–R8 are still required, because they choose the correct disposition (for example `authorization_pending` rather than a generic release) and avoid local work that would only be refused. Run 58 is the evidence for a choke point: a stop that depended on each call site let 199 requests through.

**Cost.** One primary-key read of `run` and PR #9's indexed `EXISTS` per check. Both are local SQLite reads, small beside a YouTube request.

**Not readers:** floors and eligibility, fresh runs, Errors, analytics and simulations. Run Detail reads the stop only through Discovery ARS-001's derived acquisition state.

#### `NULL`, legacy and lifetime

- `NULL` means no stored stop. It does not grant dispatch: admission still applies the derived quota level, `CanAfford` and authorization.
- Runs from before `032` read `NULL`, with no backfill. A run that a `031` build stopped for authorization, then resumed on a `032` build, has no stored stop. It meets the authorization boundary again, and OC-006 decides again.
- A stored level lasts for the run's life. Run Now resume, startup recovery, Stop, Discard and terminal writes never lower or clear it (the trigger refuses).
- The derived `quota` level lasts for the bucket in which its ledger row was written, as PR #9 specifies.
- A fresh run starts with no stop and performs ordinary authorized admission.

#### Required evidence (added to §11.7 as S1–S8)

- **S1 schema:** clean install and upgrade from `031` with rows. Old runs read `NULL`. `'quota'` and any unknown value are refused by the CHECK. Lowering or clearing is refused by the trigger; `authorization` → `accounting_or_integrity` is allowed. `028` triggers, the `029` search index and `031` receipts are intact. `widenRunStatusCheck` is idempotent on both paths.
- **S2 effective stop:** for each combination of stored value, latch and a `quota` row in the current bucket, `RunDispatchStop` returns the highest rank. A `quota` row from the previous bucket gives `StopNone` after the reset. An unscoped context gives `StopNone`.
- **S3 choke point:** with each level set, a guarded read and an insert through the real `dispatchScoped` make zero requests, zero renewals and zero ledger rows, and return the mapped error (`ErrQuotaRefused`, `ErrRunAuthorizationStopped`, `ErrQuotaAccounting`). Another run and an unscoped call in the same bucket are admitted.
- **S4 authorization, variant K:** the receiving proof of §6.2, plus: `dispatch_stop='authorization'` is committed with the first `authorization_pending` settlement; later batches are still claimed and settled locally in the creating execution; zero further provider requests. Reopen the database and resume: the stop is read back, the held live receipts settle `authorization_pending` with no request, and the resumed execution claims no further batch (§5.2).
- **S5 quota without a stored write:** an admitted `quota` attempt during a metadata chunk. The column stays `NULL`. R1 leaves the claim loop, unsent inputs are `quota_refused`, and no second request is made. On a same-run resume in the same bucket there is still no request; after the reset the run is admitted again for its own held work, and it claims nothing new (§5.2).
- **S6 accounting:** inject a ledger read error and, separately, a failure of the stored write itself. In both cases there are zero further provider requests in that execution and the error propagates. With the stored write working, a later resume reads `accounting_or_integrity` and makes no request.
- **S7 per-call readers:** with a stale in-process view, R2 refuses a claim under a stored `accounting_or_integrity` and R7 refuses `ClaimPlaylistSend` under any stored value, each inside its writer transaction with no guard or membership write.
- **S8 simulations:** a dry run, Replay and Backtest never write the column; no stored level. A dry run can still derive a quota stop from the ledger.

#### Write boundary for `rq-receiving-readiness`

This section adds two files to that node's write surface: `internal/quota/quota.go` (`DispatchStop`, `RunDispatchStop`, `LatchRunStop`, the run-scope struct) and `internal/auth/service.go` (R0 and `ErrRunAuthorizationStopped`), with their tests. The persistence (`RaiseRunDispatchStopTx`, R7) stays in `internal/state`. The migration and its test are `rq-stop-schema`'s.

### 6.2 Pipeline precondition (per claimed pass)

- **BR-039 first (§15):** classify each video of `ids` with `state.ClassifyVideoTx`. Only `open` videos enter this precondition and its dispositions. `final` videos are closed and `partly_done` videos are continued, as ARS-006 §5 and §7 say.
- Before handling open or partly-done receipts, resolve any current or restored run dispatch stop as in §6.1, with `RunDispatchStop` (§6.1.3 reader R4; writers W1 and W3). Classify incomplete work with that fact **before** consulting generic fetch suspension, a nil binding or a readiness-backstop result. In particular an authorization stop wins over `failed` or unchanged release for missing inputs, even if a prior batch had already suspended fetching. If there is no recorded stop and the binding is absent, obtain the typed authorization disposition at the existing admission boundary; a malformed per-call setup is a local fault, not proof that the user must reconnect.
- Without an effective run-wide stop, check current inputs and fetch the rest for `open` videos. A non-quota `rate_limited` or `failed` metadata result suspends further metadata fetches for the rest of the run, across batches (B7, §11.6), according to §6.1; `unusable` alone does not. The suspension is in memory, keyed by the run in the context's quota scope; a dispatch stop is resolved before it. This generic fallback assigns `failed` only to otherwise unclassified inputs. Quota and accounting faults follow §5.2; a pure authorization stop permits further local claims under variant K, in the creating execution only (§5.2), but no further provider work.
- Under an authorization stop, apply the same `authorization_pending` settlement below to receipts for the unsent suffix, to later locally claimed receipts, and to unfinished exact live receipts restored after restart. Already committed final results reconcile or acknowledge under BR-039; do not rewrite settled receipt history as authorization deferral. An otherwise-ready video may settle only under existing local rules that require no provider work. If an open video still needs metadata or an otherwise authorized network operation cannot start, defer the real unfinished work. For a partly-done video retain only its outstanding work with the original decision and existing exact target/guard authority; do not hydrate it, reroute it or issue a new send. A previously issued or uncertain mutation keeps its established disposition and guard; this table is not a means of reopening, releasing or reinterpreting it. A concurrent accounting/integrity fault still closes dispatch and further claims; preserve established authorization deferral facts without forcing unsafe settlement or swallowing the auxiliary fault.
- `requeued(v)` := `EXISTS (SELECT 1 FROM deferred_item WHERE id IN <receipt.Deferred ids> AND deferral_reason = 'requeued_by_user')`.

| Status | `requeued(v)` | Otherwise |
|---|---|---|
| `current`, `fetched` | `processRetryVideo` | same |
| `quota_refused` | `deferRetryForInputsTx(…, quota_exhausted)`; report a quota refusal | same |
| `authorization_pending` | Exact `authorization_pending` deferral under the protocol below; no generic release or unavailable result | same |
| `rate_limited` | `deferRetryForInputsTx(…, api_rate_limited)` | same |
| `failed` | `RequeueRetryReceiptTx` (released unchanged); log `diag.Encode(err, "routing", "load_metadata")` | same |
| `unusable` | `RequeueRetryReceiptTx` (released unchanged); no outcome; fetching not suspended | same |
| `absent` | `commitUnavailableTx` (§6.3) | retry-failure transaction (`FinishRetryFailureTx` + `recordRetryVideoFailureTx`) with message `diag:v1 phase=routing operation=load_metadata code=video_unavailable` |

**`deferRetryForInputsTx(ctx, tx, runID, videoID, receipt, reason)`** runs in one `WithRetryTx`:

1. `ValidateRetryReceiptTx`;
2. `TransitionRunVideoStateTx(not_yet_evaluated → evaluating)`;
3. `TransitionRunVideoStateTx(evaluating → deferred, FinalOutcomeDeferred, &reason)`;
4. when the receipt has no owned Deferred row, establish the appropriate pending Deferred sink without changing the owned Error rows or adding the new row to the receipt;
5. call `AcknowledgeRetryDeferralReceiptTx(receipt)` to return owned Deferred rows to pending and resolve the exact owned Error rows. Do not resolve or release them before acknowledgement revalidates the receipt.

A same-owner resumed claim that is already past `not_yet_evaluated` uses its current state on an allowed edge.

The item keeps its original `deferral_reason` (for example `requeued_by_user`) while the run outcome records the deferral reason. How the Deferred view shows this is for the Designer to decide (ADR-006 Findings). Nothing is changed here.

**Authorization-stop receipt settlement.** In `WithRetryTx`, revalidate the exact live receipt, finality, and the run's authority to settle locally. For open work blocked before evaluation, use `deferRetryForInputsTx`. For other independently authorized unfinished work prevented from sending, use the existing disposition/receipt handoff at the current state without re-running evaluation or writing an attempted action. The receiving-run video outcome is `deferred` with `deferred_reason=authorization_pending`; the run retains the separate vetted reconnect-required or temporary/unavailable cause. Restore the exact owned Deferred rows to pending with their original IDs, age, reason and most-recent owner. Resolve owned Errors rows only with a durable appropriate Deferred sink in the same transaction; if there was no owned Deferred row, create the needed sink with `deferral_reason=authorization_pending`. Do not substitute an unrelated ordinary or capacity row for an exact obligation. Preserve the run/video membership fence, existing decisions, successes, final outcomes, and all unresolved guards; an unowned or user-removed item is not recreated. Failed settlement rolls back and leaves the receipt recoverable. Recovery after a committed settlement makes no second claim or deferral.

**Backstop.** `processRetryVideo` sets `requireInputsKey{}`. With it set, `videoContextForExclusion`, `detectVideos` and `routeVideos` return `errInputsUnavailable` instead of substituting an empty `TransientMetadata`. Recheck the effective current or restored stop first: authorization uses the exact `authorization_pending` settlement; only a generic input loss without a more specific disposition uses unchanged release. Without the key, this backstop is unchanged; the dispatch stop still applies.

**Receiving proof:** Fail renewal in an admitted metadata batch, then exercise its unsent suffix, a later eligible local batch, and a receiver reopened with the stop and live receipts. Include a pre-existing generic suspension, local-ready inputs, partly-done work and an unresolved guard. Verify zero subsequent run-owned provider calls, no debit for unsent or local-only work, exact authorization deferrals and cause, unchanged queue age/reason and guard history, and no fabricated absence/failure or same-run re-claim. Contrast accounting failure and quota refusal, and recovery after a committed receipt settlement.

**Live gate (BR-042, amended 2026-11-19).** `liveGateClaimed` ([Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §5.3) runs inside this precondition, after BR-039 classification and the dispatch-stop resolution above. Live ARD-001 P1 makes this precondition a hard prerequisite of the BR-042 claimed paths.

- For each claimed `open` or `partly_done` receipt whose receiving-run `run_video_outcome.live_status` is `NULL`, add `Live` to its requirement. Partly-done, capacity-only and continuation receipts request `Live` only. Record each returned classification with `RecordLiveStatusTx` for the receiving run before any disposition. A resumed receipt uses its stored value, with no read.
- **The live row.** Insert one row ahead of the `current`/`fetched` and `unusable` rows of the table. A recorded `live ≠ none` → `liveSkip` with the receipt (Live ARS-001 §6.1), which:
  1. records the run outcome `deferred` with `live_now`, `live_upcoming` or `live_unconfirmed`;
  2. sets `live_hold` on the owned video-scoped row, or creates exactly one sink with `deferral_reason = live_or_upcoming`, even when the receipt owns only capacity rows;
  3. releases owned capacity rows unchanged;
  4. calls `AcknowledgeRetryDeferralReceiptTx`.

  The rows for `absent`, `quota_refused`, `authorization_pending`, `rate_limited`, `failed`, and `unusable` without a `Live` entry are unchanged.
- **Hold clearing.** When the receiving run records `none`, set `live_hold = NULL` on the receipt's owned video-scoped rows in the transaction that moves the video to `evaluating` or starts `continueOriginalDecision`. No other writer clears it.
- Evaluation and sends in `processRetryVideo` also require `LiveClearedTx(receiving run, video)` (Live ARS-001 §7). A refusal is a live deferral, never a release, an authority failure or a failure.

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
| Page | `200` | `{"deferred":[<item, below>…],"total":N,"queue_total":Q,"by_reason":{"<reason>":n,…},"page":{"limit":L,"number":p,"total_pages":P,"first_position":f\|null,"last_position":l\|null,"next_after_id":id\|null,"prev_before_id":id\|null}}` |
| Bad `include` | `400` | unchanged |
| Bad `limit`; a non-positive id; `page` not a positive integer or `last`; more than one of `after_id`, `before_id` and `page` | `400` | `{"error":"invalid page","code":"invalid_page"}` |
| `reason` not one of the six stored values | `400` | `{"error":"invalid reason","code":"invalid_reason"}` |
| Read failed | `500` | unchanged |

Each item uses the snake_case wire shape of `deferredItemView` (`internal/queue/handlers.go`):

```json
{
  "id": 51,
  "video_id": 7,
  "external_video_id": "dQw4w9WgXcQ",
  "run_id": 3,
  "retry_run_id": 4,
  "deferral_reason": "requeued_by_user",
  "retry_after": "2026-11-12T06:00:00Z",
  "retry_count": 0,
  "status": "pending",
  "created_at": "2026-11-11T00:00:00Z"
}
```

- `video_id` is the internal row id. `external_video_id` is the provider's video ID, and it is the "Video ID" the SPA shows.
- `retry_run_id`, `retry_after`, `deferral_reason` and `deferred_reason_unavailable` are omitted when empty.

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
20. **B3 time limit and resume (corrected 2026-11-23, F7):** use the registered Run Now path, real migrated SQLite, the controlled provider and a real time-limit expiry, not an injected `interrupted` row or injected receipts.
    - Seed more than two batches of eligible items. A new Run Now run R claims batch 1, settles it and claims batch 2. Its time limit expires during batch 2, so R is `interrupted` with a usable checkpoint and holds at most batch 2, by exact receipt (§5.1.1). At least one batch 2 item X is still held and unsettled. X is the positive recovery control.
    - A new scheduled run S claims the unclaimed remainder in order, starting after batch 2. It never touches a row R holds. Leave at least one eligible item W unclaimed when S ends: S's quota runs out, or W is the handoff from a real Stop of another production run P after S ends. Repeat W as a Discard handoff (BR-017 AC17) and as a `requeued_by_user` item.
    - Before the resume, show that W is eligible and that R has no tie to it. W is `pending`, has no owner and is due now. R has no `run_video_outcome` row, receipt, native origin or decision for W. No guard, final outcome, quota refusal or time limit would prevent W's claim. Discovery does not return W. R has enough time and quota and a working provider.
    - Press Run Now. R resumes with the same id, snapshot and checkpoint. Wait until the resumed execution has returned, not only for the `202`. Expected: every held batch 2 item, including X, is settled once under R's original ownership, with no new claim and no `retry_count` increment. R runs no claim transaction. W's row is byte-identical: id, video, origin `run_id`, reason, `retry_after`, `status`, owner and `retry_count`. R has no membership, receipt, decision or outcome for W, and makes no provider request for W.
    - A later new run, scheduled and separately Run Now with no resume candidate, claims W once and processes it without rediscovery (ARS-003 Q2).
    - Test power: the test must fail when `mayClaim` ignores `resumed` (a targeted mutation of §5.2). A fixture with no eligible unclaimed item at resume time cannot prove this, nor can one whose witness was already a member of R.
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
25. **B8 ARS-003 regression:** ARS-003 Q2 (handoffs smaller than one batch), Q3 (with its unrelated-handoff fixture, ARS-003 §7.3) and H1 pass unchanged, including H1(b)'s `retry_after` NULL. Add a re-queued contrast to Q3: an eligible `requeued_by_user` item that the resumed run never held also stays pending and unchanged through a Run Now resume, and the next new run claims it (BR-037 AC11).

### 11.7 Metadata and the unavailable outcome (AC13, AC18; BR-032 AC1, AC4–5)

The run dispatch stop tests S1–S8 are listed in §6.1.3 *Required evidence*. S1 belongs to `rq-stop-schema`; S2–S8 belong to `rq-receiving-readiness`.

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

**Built (2026-11-22, `rq-stop-schema`, `rq-receiving-readiness`, branch `feat/requeue-stranded`):** §6.1.3 in full except R3 (unchanged) and R8 (Discovery, PR-D), and the §6.2 precondition for the shared receiving path.

- **Stop mechanism.** `quota.DispatchStop`, `Ledger.RunDispatchStop` and `quota.LatchRunStop` (`internal/quota/quota.go`; the run scope is now a struct holding the latch, and a nested `WithRun` for the same run keeps it). `state.Store.RaiseRunDispatchStopTx` (`internal/state/playlist_guard.go`).
- **Readers.** R0 in `auth.Service.dispatchScoped` before `Resolve`, with `auth.ErrRunAuthorizationStopped` accepted by `IsPreAdmissionAuthorizationRefusal`; R2 as `queue.ErrRunDispatchStopped` from the claim transaction; R4 and R5 (`prepareReceiving` and `FetchForEvaluation`, `FetchOutcome.Stop`); R6 (`Executor.dispatchStopDisposition`); R7 (`state.ErrPlaylistSendDispatchStopped` from `ClaimPlaylistSend`).
- **Writers.** W1 (`settleAuthorizationPending`), W2 (the executor's authorization refusal) and W3 (`failClosed` in the pipeline and the executor).
- **§6.2.** `prepareReceiving` makes one `FetchForEvaluation` call per pass for the pass's open videos; `applyReadiness` settles each status as the §6.2 table says; `deferForInputsTx` is `deferRetryForInputsTx`; the readiness backstop is `requireInputsKey` with `errInputsUnavailable` in the three evaluation readers.

**Deviations and limits.**

- **`absent` splits on `requeued(v)` (`rq-processing`, §6.3).** `settleAbsent` runs one `WithRetryTx`: `requeued(v)` is read from the receipt's deferred ids, then `commitUnavailableTx` (re-queued) or the ordinary `video_unavailable` retry failure (other retried videos) is written in that same transaction. `commitUnavailableTx` writes no decision, audit, action, guard, Errors row or new deferred row and makes no provider call. `EventRunVideo` (`unavailable_on_youtube`, `matched=false`) is broadcast after the commit. A same-owner resumed claim that is already `evaluating` goes `evaluating → evaluated` directly. The §6.4 readers (`unfinishedRunMembersTx`, the disposition check, the `videos_unavailable` counter and its projections, the `outcome` filter) were already in place from the vocabulary step; `rq-processing` added tests only (D3, D4, D8, D9, O1).
- **The readiness gate is active only when `Pipeline.meta` provides `FetchForEvaluation`.** `cmd/server` passes a `*metadata.Manager`, so production is gated. A test double without it keeps the old degraded behavior.
- **`Title` is always a required input** when a pass is gated, because duplicate detection reads it. Other fields follow the enabled routes' conditions and duration exclusions.
- **`RaiseRunDispatchStopTx` writes only for production runs** (`run_type IN ('manual','scheduled')`). The spec's statement did not have this guard; it makes "simulations never write" true at the persistence seam as well as at the call sites.
- **`RunDispatchStop` treats a missing run row as no stored stop** (the derived quota level is independent of it). A read error, or a stored value outside the CHECK, is `accounting_or_integrity`.
- **R0 latches `accounting_or_integrity` on a stop read error** (not on a cancelled context), so a later call in the same execution cannot pass on a lucky read.
- **R1 and the §5.2 batch loop are not built.** `processDeferredItems` still receives its receipts from the creation-time claim; `rq-claim` owns the loop. R2 is enforced in the claim transaction it will call.
- **Run-long fetch suspension (`rq-metadata-fetch`).** `Manager.FetchForEvaluation` keeps a generic provider failure (`failed` or `rate_limited`, not quota, authorization, accounting or interruption) per run, so a later batch of the same run sends no `videos.list` request and its inputs take the same status (B7: one request over three batches). The map is in memory and bounded to 32 runs. A restart or a call without a run scope starts unsuspended. Earlier text said "in the pass", which B7 contradicts; B7 was decided, so the spec text was corrected here.
- **The first accounting fault latches at the ledger and the admission seam (2026-11-23, `rq-gate-claim` critical finding).** A run must send no provider request after its first quota-accounting fault, whichever caller sees the error (S6). `quota.Hold.Finalize`, `Ledger.RecordAttempt` and `Ledger.RecordCalls` latch `accounting_or_integrity` on the run scope when their ledger insert fails; a hold remembers the scope it was acquired under, so the latch does not depend on the context the caller finalizes with. `auth.dispatchScoped` latches when admission accounting fails. R0 then refuses every later guarded request of that execution. The run owner still classifies and stores the stop (W3): `metadata.IsAccountingFault` recognises a lifecycle sweep error that carries an accounting fact, a failed preflight, or `ErrQuotaAccounting`, and `executeHeld` ends the run through `failClosedAtLifecycle` and the ordinary retain-then-fail path with the closed diagnostic `operation=account_quota`, instead of logging the sweep error and continuing. A `CanAfford` read error in the metadata sweep, in `FetchForEvaluation` and in the executor's two pre-action checks is an accounting fault (it joins `auth.ErrQuotaAccounting`; the executor fails closed through `failClosed`). `processRetryVideo` returns a `runStoppingError` instead of finishing the video as failed or writing an Errors entry. A lifecycle error without an accounting fact stays a warning. Not changed here: discovery's own accounting handling (PR-D, R8/W4) and the other open findings of that assessment.
- **No Live gate.** `liveGateClaimed` is not in the receiving path yet.
- **Unguarded authorization paths.** `ActiveToken`, the ledger-less `Dispatch` and the legacy `discovery.Run` bypass R0. A test (`TestUnguardedAuthorizationPathsHaveNoProductionCaller`) pins the exact set of non-test callers, none of which a production run reaches: `cmd/server` wires an auth service, so `Pipeline.Execute` binds the run and uses `RunBound`, and the executor calls `ActiveToken` only without a binding or with a test factory.

**Evidence.** S2 (`quota/dispatch_stop_test.go`), S3 (`auth/dispatch_stop_test.go`), R5 (`metadata/fetch_test.go`), S4–S8 and the §6.2 table (`pipeline/receiving_stop_test.go`, `pipeline/receiving_stop_integration_test.go`). S6 at the product seam: `TestS6_LifecycleAccountingFaultSendsNoFurtherRequest` (`cmd/server/accounting_fault_composition_integration_test.go`: the real server, SQLite and a loopback endpoint; a failed ledger write during a lifecycle refresh leaves that refresh as the run's only provider request), with unit coverage in `quota/dispatch_stop_test.go`, `auth/dispatch_stop_test.go`, `metadata/manager_test.go`, `metadata/fetch_test.go`, `pipeline/executor_test.go` and `pipeline/receiving_stop_test.go`. Fixtures that relied on degraded empty metadata now seed a current Store A row (`seedCurrentMetadata`), which is the state production guarantees.

**Not implemented:** everything else in this document.

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

## 14. Foundation gate correction of 2026-11-12

- **§7.2:** the item shape is now the snake_case wire shape of `deferredItemView`, including `external_video_id` and `retry_run_id`. The earlier text said "unchanged item shape", which did not match the code. The SPA shows `external_video_id` as the "Video ID" in the Deferred tab, not the internal `video_id`.

## 15. BR-039 amendment of 2026-11-12 (partly done exception)

Source: [BR-039](../../../requirements/yuhaul/br-039-final-outcomes-are-never-reprocessed.md), realized by [ADR-007](../decisions/adr-007-final-outcomes-are-never-reprocessed.md) and [ARS-006](ars-006-final-outcomes-are-never-reprocessed.md), with ADR-006 Decision 10.

- **§5 claims.** `claimRetryGroupTx` returns `ClaimClosedFinal` for a final video, after closing its claim family with `closed_reason='final_outcome'` (ARS-006 §3). `createClaimedRunLocked` and the §5.2 batch claim treat it like a lost claim. It is not a member, and it counts against nothing. The open-video reset is unchanged. A partly done video is reset to `evaluated`/`not_yet_evaluated` instead, so that it is not routed.
- **§6.2 precondition.** Only `open` videos are fetched and given the dispositions in the §6.2 table. A partly done video is never fetched, never evaluated, and never committed `unavailable_on_youtube`. It goes to ARS-006 §7 (continuation under its original decision). A final video is closed (ARS-006 §5).
- **§6.3 `commitUnavailableTx`** is reachable only for an `open` re-queued video.
- **Invariant 4** gains: "and the video was open (not partly done) when the fetch ran".
- **Tests:**
  - D3 and D7 seed only open videos.
  - New **D8:** a re-queued partly done video whose metadata is expired and whose id is absent from a fetch. It is not fetched, not committed unavailable, and continues only its missing addition (ARS-006 K9).
  - New **D9:** a re-queued item whose video became final after the confirm. It is closed at the claim with `closed_reason='final_outcome'`, with no fetch and no outcome.
- **Delivery order (§0 step 6).** This step ships after ARS-006 §0 step 3 (continuation), as ARS-006 §0 step 5 requires. It is never shipped with the claim reset still unconditional, nor with only the interim hold.
- **Revised 2026-11-12 after critic review `ses_e2e6c22cfffeEhvwlTXJINOyx1` of ARS-006:**
  - *final* now means the four final outcomes only, with no `completed` clause;
  - the main loop of a resumed run drops videos held by the queue or partly done under another run (ARS-006 §5). This step's batch claims are unaffected.

## 16. Resumed-execution claim boundary — correction of 2026-11-23 (F7)

Sources: reviewer assessment `771112e4` and concern `ef7815e7`, and the specifier's semantic handoff `2d76fcfc` (Loom Action b820f6ca, Plan node `rq-gate-claim`), all read at `feat/requeue-stranded` `df91a0c`. Authority: [BR-035](../../../requirements/yuhaul/br-035-stop-active-run.md) AC10, AC13 and AC14, including "they wait for the next new run" and the variant K confirmation. Also [BR-017](../../../requirements/yuhaul/br-017-manual-run-initiation.md) AC7–9, AC12, AC17 and AC21; [BR-037](../../../requirements/yuhaul/br-037-requeue-stranded-videos.md) AC11; and ADR-001 rule 2. No requirement changed, and this correction makes no new behavior decision.

- **§5.2:** `mayClaim` replaces the `initiated_by` test, and a resumed execution settles only its exact held receipts. **§5.3:** the boundary is stated. **§6.1.3:** R1, R3, S4 and S5, and **§6.2:** the authorization-stop sentence, now say "creating execution only". **B3** is rewritten with a real interruption, the scheduled remainder, an unrelated eligible witness and a check of test power. **B8** adds Q3 and a re-queued contrast. ARS-003 Q3's fixture is strengthened, and its expected result is unchanged.
- Native discovery, the variant K decision, explicit "Retry now" (`RetryQueueEntry`), startup selection and validation, finality, guards and the quota reset are unchanged. A startup-recovered execution claims nothing new, which matches `main` (ADR-006 Decision 5, *Startup recovery*).

**Implementation status at `df91a0c` (read-only source check, not a test run).** The branch does not conform yet:

- `internal/pipeline/pipeline.go:416` computes `isResume`, and `:438` passes it to `processRetryBatches`.
- `:1339–1352` there gates further claiming on `initiated_by` only, so a resumed Run Now execution continues to `:1376–1386` and claims.
- `internal/pipeline/claim_batches_test.go:315–332` (`TestBatchLoopResumedRunFinishesItsBatchThenContinuesB3`) injects three held claims and expects seven members. That is the widening this correction forbids, and the test has no scheduled control.
- `internal/pipeline/stop_integration_test.go:409–425` (ARS-003 Q3) queues V3, which is already a member of the resumed run. The candidate query's membership exclusion (`internal/queue/retry.go:821`) drops V3 whatever the resume rule is, so the test cannot detect the widening.

The coder and reviewer own the code change and the proof. This correction is documentation only.
