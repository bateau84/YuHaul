---
type: decision
title: ADR-001 — Interim Channel-Granular Isolation of Unavailable Uploads Sources
description: No-migration interim that skips a channel whose uploads read returns 404 playlistNotFound, keeps completed channels' discoveries and floors, and reports incomplete coverage, pending the full ARD-001/ARS-001 realization.
tags: [architecture, decision, discovery, interim, recovery, diagnostics]
timestamp: 2026-11-20T00:00:00Z
---

**Status:** accepted as an interim (architecture). Two user-owned gaps (G1, G2) need explicit acceptance before release; G3 gates one log attribute. It does not supersede [ARD-001](../design/ard-001-isolate-uploads-source-failures.md) or [ARS-001](../specifications/ars-001-durable-discovery-page-handoff.md). Nothing here is implemented yet.

Production builds up to `debad57` fail every run when one subscribed channel's uploads list returns `404 playlistNotFound`. Runs 52 and 53 each failed after about 13 minutes of reads. This interim keeps the run alive. If channels A and C list successfully and B returns `404 playlistNotFound`, the run saves and routes A's and C's new videos and advances only their floors. It keeps none of B's pages and does not move B's floor. It ends **Completed** with an explicit incomplete-discovery note, and the next fresh run polls B again. If every attempted channel fails this way and no run-wide stop happened, the run ends **Failed** with "No channels were scanned successfully." It needs no schema migration.

## Derived from

- [BR-002](../../../requirements/yuhaul/br-002-subscription-discovery-without-live-monitoring.md) AC6–10 and [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) “Permanent uploads-source error”.
- [ARD-001](../design/ard-001-isolate-uploads-source-failures.md) read-disposition table and the [safe diagnostic contract](../../run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md).
- The dispatcher's 2026-11-20 brief: Run 53 failed at discovery with `provider_error status=404 reason=playlistNotFound`, the same as Run 52, on deployed `debad57`. Plan nodes `run52-schema`, `run52-page-store`, `run52-handoff` and `run52-runner` are blocked on the receipt/receiving work and on migration ordering. This document did not independently query deployed evidence.
- Source read at `debad57`:
  - `internal/discovery/engine.go:300–369`: one run-wide buffer. The first non-refusal `ListPlaylistItems` error returns from discovery before metadata or persistence.
  - `engine.go:302–342`: the run-wide `listed` de-duplication map. Completion is tagged by snippet `ChannelID`, not by the scanned channel (`:362–366`).
  - `engine.go:492,592–607`: the floor (`channel.last_discovered_at`) advances inside each persisted video's transaction.
  - `internal/youtube/client.go:56–78,1142`: guarded reads return `*ReadError{Primary, Accounting, Renewal, Interruption, Status}`. `APIError` carries a vetted `Reason`/`Reasons`.
  - Migration `001`: `error_entry.video_id` is `NOT NULL REFERENCES video(id)`.
  - Migration `023`: `quota_ledger` has `run_id`, `operation_type`, `outcome_category` and `http_status`.
  - Only discovery calls `playlist_items_list`.
  - `internal/state/store.go:240–275`: `CompleteRun` does not write `interrupt_reason`. `FailRun` writes it through `diag.GuardErrorText`. `run_now.go:253` clears it on resume.
  - `web/src/views/RunDetailView.tsx:434`: the failure reason renders only for `failed`/`interrupted`.

## Satisfies

| Authority | Disposition in this interim |
|---|---|
| BR-002 AC6 | **Satisfied.** A failed channel is skipped for this run and retried next run. Successful channels' discoveries are kept. |
| BR-002 AC7 | **Satisfied, except for identity.** Scanning continues and the failed floor stays where it was. Run Detail shows incomplete coverage from durable ledger facts, without naming the channel. The next fresh run re-polls the channel. |
| BR-002 AC8 | **Satisfied.** The run ends terminal `failed` with an explicit no-successful-scan reason. That condition is defined below. |
| BR-002 AC9 | **Gap G2.** A failed channel's earlier pages are dropped, not routed. They remain discoverable because B's floor does not move. |
| BR-002 AC10 | **Not applicable.** No page is saved before its channel completes. |
| OC-001 permanent uploads-source error | **Gap G1.** There is no durable read-only source entry in Errors. Consumption accounting still classifies the attempt as `permanent`. |
| OC-001 quota, transient and authorization semantics | **Unchanged.** These stay run-wide, exactly as today. |
| OC-002, BR-032, BR-039 | **Preserved.** No video or action outcome is invented. Final outcomes are untouched. |
| Live-status ([Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §4) | **Compatible.** The prefetch sees only merged videos from completed channels, so a skipped channel's videos get no `live_status`. |

## Decision question

What is the smallest sound change, implementable now without a migration, that stops one unavailable uploads source from failing every production run, while satisfying as much of BR-002 AC7–10 and OC-001 as possible?

## Drivers and constraints

- Every production run fails today. Delivery time dominates.
- No new migration. Migration numbering is unassigned, and `029` is taken by search.
- The safe diagnostic contract: no provider IDs or prose in diagnostics or logs; only closed vocabularies.
- Floor safety: never advance a channel's floor past observations that were not durably saved.
- Don't fabricate videos or error entries. Don't change quota, authorization, accounting or cancellation dispositions.
- Stay forward-compatible with ARD-001/ARS-001. The interim must not create state they would need to migrate.

## Options considered

1. **Channel-granular isolation, as dispatched (chosen).** Buffer each channel's eligible items separately. Merge the buffer into the run set only when that channel's pagination completes. On an exactly classified `404 playlistNotFound`, discard that channel's buffer and continue. Report coverage from ledger facts and one new closed failure code.
   - Benefits: small, contained in the discovery engine, the pipeline's discovery-result handling, `diag`, and the run read and Run Detail. No schema change.
   - Costs: G1 and G2. B's earlier pages are re-fetched next run.
   - Failure mode: a misclassified error could hide a run-wide problem. The exact predicate below prevents this.
2. **Option 1 plus a small additive `discovery_source_failure` table.** One row per isolated failure, keyed by run and channel, projected read-only in Errors.
   - Benefits: closes G1 and gives durable identity on Run Detail.
   - Costs: a migration number, an Errors projection and API, and contract alignment with ARD-001's run/source record. That record also holds completion and floor cutoffs, so an early partial table either duplicates it or pre-empts its shape.
   - Blocked today on migration ordering. Recommended as the first full-design slice once a number is assigned.
3. **Store the note in `run.interrupt_reason` on a completed run.**
   - Rejected. `run_now.go:253` clears the column on resume, so the note is lost if a run is interrupted after discovery. It also overloads a recovery column for completed runs.
4. **Reuse `error_entry`, `advisory` or `settings`.**
   - Rejected:
     - `error_entry` requires a real video. Using it would fabricate a video and put source items into the video Retry/Dismiss command namespace, which OC-001 forbids.
     - `advisory` has a closed `CHECK` on its type, so a new type needs a migration. It also offers dismiss and act-upon commands.
     - `settings` is effective configuration, and source failures would pollute configuration history.
5. **Persist every page now, as ARD-001 does.**
   - Rejected for the interim. It needs the receiving and readiness handoff (ARS-001), which is the blocked work.

## Comparison

Only options 1 and 2 preserve every accepted guarantee except the named gaps. Option 1 can ship now. Option 2 needs a schema slot nobody can allocate today. Option 1 leaves nothing that the full design must migrate: no new rows, columns or codes beyond one closed diagnostic code. So it is the least complex complete interim, as long as the user accepts G1 and G2.

## Decision

Implement Option 1 with exactly these rules.

### D1. Source-local predicate (the only error that is isolated)

During the uploads scan of channel `ch`, `ListPlaylistItems` returns `err`. Treat it as source-local only if all of the following hold:

- `ctx.Err() == nil`.
- `errors.As(err, &re)` gives a `*youtube.ReadError`, and `re.Accounting == nil`, `re.Renewal == nil` and `re.Interruption == nil`.
- `errors.As(re.Primary, &api)` gives a `*youtube.APIError`, and `api.Status == 404`.
- `api.Reason == "playlistNotFound"`, and every entry in `api.Reasons` is `"playlistNotFound"`.

Never classify by `err.Error()`. Every other error keeps today's behavior unchanged:

- Quota refusal and `ErrQuotaRefused` still break the channel loop.
- Quota exceeded, authorization errors, accounting or renewal failure, transient/5xx/429, cancellation, invalid response, other 404 reasons and SQLite errors still return a discovery error and fail the run.
- The unguarded legacy `Run` path cannot produce a `ReadError`, so nothing is isolated there.

Isolating transient errors or other permanent reasons is out of scope.

### D2. Per-channel buffer and merge

- Each channel scan collects its new items into a channel-local buffer with a channel-local seen set.
- The run-wide `listed` map and output list change only when that channel's pagination finishes, meaning `nextPageToken` is empty. When the buffer is merged, an item already in `listed` is dropped.
- Each buffered item is tagged with the scanning channel's ID (`ch.ID`) as `SourceChannelID`, alongside the snippet `ChannelID` it keeps today.
- A source-local failure discards the buffer and leaves `listed` untouched. So a video that both B and a later channel C list is still discovered through C.
- A quota break mid-channel keeps today's behavior: the partial buffer is merged with `EnumerationComplete=false`, so the floor does not move.

### D3. Floor safety

- The floor is written only through the existing `upsertChannel` in a persisted video's transaction. A skipped channel persists nothing, so its `last_discovered_at`, `total_discovered` and `channel` row stay unchanged.
- `EnumerationComplete` is true only for items from a buffer whose channel completed.
- Add one tightening: the floor value is written only when the item's snippet `ChannelID` equals its `SourceChannelID`. Otherwise the item is persisted with a NULL floor argument. This removes cross-channel floor writes; it never advances more than today.
- Unchanged pre-existing risk, recorded here and not fixed: a persist failure partway through a completed channel can leave that channel's floor already advanced past its unsaved later items. ARD-001 §3 fixes this.

### D4. Discovery result facts

`discovery.Result` gains three in-memory fields:

- `SourcesAttempted`: channels for which at least one `ListPlaylistItems` request was made.
- `SourcesCompleted`: channels whose pagination finished, including those with zero new uploads.
- `SourcesFailedLocal`: channels that ended under D1.

It also gains one flag, `ScanStopped`, set when any uploads-loop `break` happens because of a budget or refusal condition.

Channels with no uploads playlist are not attempted.

### D5. Run ending

After discovery, the pipeline decides as follows:

- **All attempted channels failed locally (AC8).** This applies when `SourcesAttempted ≥ 1`, `SourcesCompleted == 0`, `SourcesFailedLocal == SourcesAttempted` and `!ScanStopped`.
  - Discovery returns a typed error, `discovery.ErrNoSourceScanned`.
  - The existing phase-failure path stores `diag:v1 phase=discovery operation=discover code=no_source_scanned` through `FailRun`.
  - Add `no_source_scanned` to `diag`'s closed `codes`. It is valid only with phase `discovery`, operation `discover` and no other facts. `Classify` maps the typed error to it.
  - The run is terminal `failed`, as today's discovery failure is. Retention and other side effects are unchanged.
- **Otherwise,** the existing flow continues and the run completes or defers as today. That includes a mixed run, and a run where both local failures and a `ScanStopped` happened.

### D6. Inspection without new storage

- The Run read projection adds `discovery_incomplete` (boolean). It is true for a production run (`scheduled`/`manual`) when `quota_ledger` has a row with that `run_id`, `operation_type='playlist_items_list'`, `outcome_category='permanent'` and `http_status=404`.
  - The ledger is durable, append-only, never pruned in source, and survives resume.
  - For runs on this build, such a row exists only for an isolated D1 skip: any other permanent 404 fails the run, and the guarded path finalizes a ledger row before D1 can apply.
  - It is a boolean, not a count: a same-run resume may re-scan, and the row identifies no channel.
- Run Detail shows a non-error note on a **Completed** run when `discovery_incomplete` is true. The note says that discovery was incomplete because at least one subscribed channel's uploads could not be read (YouTube: playlist not found). Those channels were skipped and will be polled again on the next run. The server log for this run identifies them. It must not say "No new uploads" or present the run as failed.
- A **Failed** run renders `no_source_scanned` as "No channels were scanned successfully." The design owner owns the exact copy.
- No Errors change: no fake entries and no source commands.
- Legacy failed runs, including 52 and 53, keep their stored reason. The note is shown only for `completed` runs.

### D7. Identifying the skipped channel (logs)

At each D1 skip, emit exactly one `WARN` record:

- Fixed message: `discovery: uploads source skipped`.
- Attributes:
  - `run_id`.
  - `diagnostic`: `diag.Encode(err, "discovery", "playlist_items_list")`, which yields `code=provider_error status=404 reason=playlistNotFound`.
  - `pages_discarded` (count).
  - `source_channel_id`: `ch.ID`, only if it matches `^UC[A-Za-z0-9_-]{22}$`. Otherwise omit it and set `source_channel_id_valid=false`. **This attribute is gated on G3.**
  - If the `channel` row exists, its local numeric `channel.id` as `source_channel_ref`. The diagnostic contract permits this attribute.
- Never log:
  - the uploads playlist ID, page token or channel title;
  - `err` or its text, because `ListPlaylistItems` wraps the playlist ID into the message;
  - the request URL.

The channel ID never enters `diag` text, the database, SSE or the API. If G3 is declined, only `source_channel_ref` remains, and a channel that never produced a video cannot be identified until Option 2 lands.

### Write boundary

The only writes and changes this interim makes:

- `internal/discovery/engine.go`: buffer, merge, predicate, result facts, floor tightening and the log.
- The discovery handling in `internal/pipeline/pipeline.go` (`phaseDiscovery` and its failure classification).
- `internal/diag/diag.go`: one code.
- The Run read query and projection (`internal/stats/store.go` run select and `internal/state/models.go` public run), adding one derived field.
- `web/src/views/RunDetailView.tsx` and `web/src/lib/failure-reason*`: the note and the code's copy.

Explicitly unchanged:

- Schema and migrations.
- `error_entry` and the Errors view and API.
- The quota, authorization and `youtube` packages.
- Metadata prefetch, apart from receiving the smaller merged set.
- Persist transactions, apart from the D3 floor argument.
- Retry, deferral and stop paths.

## Consequences

- **Positive:** One unavailable channel no longer fails every run. Successful channels' discoveries and floors are kept. AC8 becomes explicit. The ledger-derived note survives resume. No schema debt for the full design.
- **Negative:**
  - G1: no Errors entry.
  - G2: a later-page failure re-fetches the channel's earlier pages next run. A video removed upstream in between is missed; in practice `playlistNotFound` occurs on page 1.
  - G3: identity comes only from logs, which are not durable product evidence.
  - A permanently missing playlist costs one unit per run until the user unsubscribes. This matches the accepted "retain ordinary polling".
- **Neutral:** Runs 52 and 53 are not recovered or reinterpreted. The full ARD-001 design replaces D2–D6. Its Run Detail must keep rendering `discovery_incomplete` (or "source evidence unavailable") for runs from this build, which have no source records.
- **Concurrent work:**
  - `debad57` has no live-status recording in discovery: `prefetchMetadata` still discards `VideoIDs`. If [Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §4 lands in the same file, it must attach `Live` only to merged items, and `RecordLiveStatusTx` stays in the per-video persist transaction. A skipped channel produces no `run_video_outcome` row, so no `live_status` and no gate input.
  - The capacity and retry-receipt work in the uncommitted `internal/pipeline` files is disjoint, except for `pipeline.go`. Merge carefully there.

## Confirmation and reconsideration

These are required tests. They are not executed.

1. **Engine, guarded path** (`newBoundHarness`). Channels A, B and C, where B returns `404` with `reason=playlistNotFound`.
   - Put B first, middle and last.
   - Assert that A's and C's videos are persisted and their floors are set, and that B's `channel` row, floor and `total_discovered` are unchanged (absent or prior value).
   - Assert there are no more `playlist_items_list` requests for B after the 404, and exactly one `permanent`/`404` ledger row.
   - Assert `SourcesAttempted=3`, `SourcesCompleted=2` and `SourcesFailedLocal=1`.
2. **Later-page failure.** B returns two pages, then a 404 on page 3. None of B's items are persisted and B's floor is unchanged. A later run where B succeeds discovers those items, including one older than A's floor.
3. **Cross-channel duplicate.**
   - V is listed by B, which fails, and by C, which succeeds. V is persisted once, through C.
   - Reverse the order: C lists V first and B fails afterwards. V is still persisted once.
   - V listed twice on one page of A is persisted once.
4. **Predicate negatives.** Each of the following fails the run exactly as today, with no isolation:
   - `404 notFound`;
   - `403 playlistItemsNotAccessible`;
   - mixed `Reasons` (`playlistNotFound` plus `quotaExceeded`);
   - a 404 wrapped with `Accounting` or `Renewal` errors;
   - `Interruption` set, or the run context canceled;
   - 500/`backendError`;
   - `quotaExceeded`;
   - 401/`authError`.

   A proactive refusal breaks the loop as today.
5. **AC8.**
   - Every attempted channel returns `playlistNotFound`: the run is `failed` with `diag:v1 phase=discovery operation=discover code=no_source_scanned`, and Run Detail shows "No channels were scanned successfully".
   - The run stays `completed` with no `no_source_scanned` in each of these cases:
     - one channel succeeds with zero uploads, so the run is mixed and the note shows;
     - no subscriptions;
     - no channel has an uploads playlist;
     - B fails and then a budget refusal stops the scan, so `ScanStopped` is set.
6. **Run Detail and the API, assembled.** Use the real runner, migrated SQLite and the registered run endpoint.
   - A mixed run is `completed` with `discovery_incomplete=true`.
   - After an interruption after discovery and a resume, it is still true.
   - A clean run reads false. Dry-run and backtest read false.
   - Legacy Run 52-style rows (failed, with a ledger 404) show their stored reason and not the completed note.
7. **Diagnostics and logs.** Capture the `slog` output. There is one `discovery: uploads source skipped` record with a valid `source_channel_id`. The uploads playlist ID, page token, channel title, URL and error text appear in no log, diagnostic column, SSE event or API field. An invalid channel ID gives `source_channel_id_valid=false`. `diag` tests admit `no_source_scanned` only in its exact form.
8. **Downstream composition.** In the mixed run, A's and C's new videos reach exclusion, duplicate detection, routing and actions through the ordinary pipeline. Existing Errors and Retry tests stay green.

Reconsider the interim when any of these happens:

- Production shows a source-local error other than `404 playlistNotFound` repeatedly failing runs. That would widen D1, with evidence.
- A migration slot is assigned. Then implement Option 2 or ARD-001's source record and retire D6's ledger derivation for new runs.
- The user declines G1 or G2.

## Open questions

User-owned. These block release, not implementation.

- **G1 (OC-001):** Ship without the durable read-only source entry in Errors until a migration slot exists. The stand-ins are Run Detail's incomplete-coverage note and the log identity. The alternative is to wait for Option 2.
- **G2 (BR-002 AC9):** Until the full design, a channel that fails after earlier pages contributes none of its videos to that run. They are re-polled next run from the unchanged floor.
- **G3 (diagnostic log contract):** Allow the validated YouTube channel ID of a skipped source in one server-log record (D7). This shows one of your subscriptions in your own server logs; nothing is stored or published. Without it, a channel that never produced a video cannot be identified until Option 2.
