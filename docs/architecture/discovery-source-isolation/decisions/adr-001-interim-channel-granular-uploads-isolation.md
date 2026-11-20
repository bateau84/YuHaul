---
type: decision
title: ADR-001 — Interim Channel-Granular Isolation of Unavailable Uploads Sources
description: Channel-granular interim that skips a channel whose uploads read returns 404 playlistNotFound, keeps completed channels' discoveries and floors, and reports incomplete coverage. Ships with ARS-002's migration 030 source-failure record, pending the full ARD-001/ARS-001 realization.
tags: [architecture, decision, discovery, interim, recovery, diagnostics]
timestamp: 2026-11-20T00:00:00Z
---

**Status:** accepted as an interim (architecture). User answers on 2026-11-20 (relayed by the dispatcher): G2 and G3 accepted; G1 declined ("there is no rush, implement the proper fix"). G1 is therefore closed by [ARS-002](../specifications/ars-002-durable-source-failure-record.md), which ships with this interim and adds migration `030`. That supersedes Option 1's "no migration" premise and withdraws D6. D1–D5 and D7 stand. It does not supersede [ARD-001](../design/ard-001-isolate-uploads-source-failures.md) or [ARS-001](../specifications/ars-001-durable-discovery-page-handoff.md). **Implementation:** D1–D5 and D7 are implemented in `5f849ea`. ARS-002 §1–§4.4 are implemented in `1a0bf19` (local candidate on `interim-uploads-isolation`); evidence: review record `833a06fb` ("with fixes", session `ses_e09c5a500ffepaBHsFYpJCNTXG`), not rerun by the architect. The explicit `isolateSources` flag and [ARS-002 §4.5](../specifications/ars-002-durable-source-failure-record.md#45-per-run-channel-scan-outcome-added-2026-11-20-review-833a06fb-major-1) are implemented in `f88ada9`; evidence coder-run on `f88ada9`: `go test -race` on store, state, discovery, pipeline, server, stats and diag, `go test ./...`, and the web suite (865 tests) all pass; browser, screen-reader and 320 px checks not run.

Production builds up to `debad57` fail every run when one subscribed channel's uploads list returns `404 playlistNotFound`. Runs 52 and 53 each failed after about 13 minutes of reads. This interim keeps the run alive. If channels A and C list successfully and B returns `404 playlistNotFound`, the run saves and routes A's and C's new videos and advances only their floors. It keeps none of B's pages and does not move B's floor. It records B's failure durably ([ARS-002](../specifications/ars-002-durable-source-failure-record.md)), ends **Completed** with an explicit incomplete-discovery note, and the next fresh run polls B again. If every attempted channel fails this way and no run-wide stop happened, the run ends **Failed** with "No channels were scanned successfully." This decision was first written to need no schema migration. After the user declined G1 on 2026-11-20, it ships with [ARS-002](../specifications/ars-002-durable-source-failure-record.md), which adds migration `030`. The decision rules below (D1–D5, D7) need no schema of their own.

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
| BR-002 AC7 | **Satisfied.** Scanning continues and the failed floor stays where it was. Run Detail reports incomplete coverage and names the channels from ARS-002's durable rows (this replaces the original ledger-based note). The next fresh run re-polls the channel. |
| BR-002 AC8 | **Satisfied.** The run ends terminal `failed` with an explicit no-successful-scan reason. That condition is defined below. |
| BR-002 AC9 | **Gap G2, accepted by the user on 2026-11-20.** A failed channel's earlier pages are dropped, not routed. They remain discoverable because B's floor does not move. |
| BR-002 AC10 | **Not applicable.** No page is saved before its channel completes. |
| OC-001 permanent uploads-source error | **Satisfied by [ARS-002](../specifications/ars-002-durable-source-failure-record.md),** which ships with this interim. G1 was declined on 2026-11-20. Consumption accounting still classifies the attempt as `permanent`. |
| OC-001 quota, transient and authorization semantics | **Unchanged.** These stay run-wide, exactly as today. |
| OC-002, BR-032, BR-039 | **Preserved.** No video or action outcome is invented. Final outcomes are untouched. |
| Live-status ([Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §4) | **Compatible.** The prefetch sees only merged videos from completed channels, so a skipped channel's videos get no `live_status`. |

## Decision question

What is the smallest sound change, implementable now and originally constrained to need no migration, that stops one unavailable uploads source from failing every production run, while satisfying as much of BR-002 AC7–10 and OC-001 as possible?

## Drivers and constraints

- Every production run fails today. Delivery time dominates.
- ~~No new migration.~~ *Superseded 2026-11-20:* the user declined G1, so [ARS-002](../specifications/ars-002-durable-source-failure-record.md) adds migration `030` under the central allocation in its §6. This driver no longer applies; it explains why Option 2 was deferred at first.
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
   - At first blocked on migration ordering. *Realized 2026-11-20 as [ARS-002](../specifications/ars-002-durable-source-failure-record.md) (migration `030`), which ships with Option 1.*
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

Only options 1 and 2 preserve every accepted guarantee except the named gaps. Option 1 can ship now. Option 2 needs a schema slot nobody can allocate today. Option 1 leaves nothing that the full design must migrate: no new rows, columns or codes beyond one closed diagnostic code. So it is the least complex complete interim, as long as the user accepts G1 and G2. *(2026-11-20: G2 accepted, G1 declined. Option 1 now ships together with Option 2, realized as [ARS-002](../specifications/ars-002-durable-source-failure-record.md).)*

## Decision

Implement Option 1 with exactly these rules.

### D1. Source-local predicate (the only error that is isolated)

During the uploads scan of channel `ch`, `ListPlaylistItems` returns `err`. Treat it as source-local only if all of the following hold:

- Isolation is enabled, which is true only on the `RunBound` (guarded) path. `runWithClient` takes its own explicit parameter, `isolateSources bool`: `RunBound` passes `true` and the legacy `Run` passes `false`, so nothing is isolated there. It must not reuse or be derived from `byID`, which controls metadata field presence only. *(Clarified 2026-11-20 after review `833a06fb`: `1a0bf19` gated D1 on `byID` at `engine.go:165,180`.)*
- `ctx.Err() == nil`.
- `errors.As(err, &re)` for `*youtube.ReadError` is **false**. The guarded reader returns a `*ReadError` only when accounting, renewal or interruption failed (`client.go:1141–1144`). Any `*ReadError` is therefore global, whatever it wraps. A clean, fully accounted 404 comes back as a bare `*APIError` wrapped by `ListPlaylistItems`.
- `errors.As(err, &api)` gives a `*youtube.APIError` with:
  - `api.Status == 404`;
  - `api.Complete == true`;
  - `len(api.Reasons) ≥ 1`;
  - every entry in `api.Reasons` equal to `"playlistNotFound"`.

  Don't check `api.Reason`. The adapter sets it only when there is exactly one reason, and clears `Reasons` when the response was incomplete, so `Reasons` plus `Complete` is the authoritative test.

Never classify by `err.Error()`. Every other error keeps today's behavior unchanged:

- Quota refusal and `ErrQuotaRefused` still break that channel's page loop, as today. Later channels are then refused by their own budget check before any request (D2 "Budget breaks").
- These still return a discovery error and fail the run:
  - any `*ReadError`, which covers accounting, renewal and interruption failures;
  - quota exceeded and authorization errors;
  - transient, 5xx and 429 errors;
  - cancellation;
  - an invalid response;
  - a 404 with an incomplete or empty reason list, or with any other reason;
  - SQLite errors.

Isolating transient errors or other permanent reasons is out of scope.

### D2. Per-channel buffer and merge

- Each channel scan collects its new items into a channel-local buffer with a channel-local seen set.
- The run-wide `listed` map and output list change only when that channel's pagination finishes, meaning `nextPageToken` is empty. When the buffer is merged, an item already in `listed` is dropped.
- Each buffered item is tagged with the scanning channel's ID (`ch.ID`) as `SourceChannelID`, alongside the snippet `ChannelID` it keeps today.
- A source-local failure discards the buffer and leaves `listed` untouched. So a video that both B and a later channel C list is still discovered through C.
- A quota break mid-channel keeps today's behavior: the partial buffer is merged with `EnumerationComplete=false`, so the floor does not move.
- **Budget breaks.** A failed `requireBudget` or an `ErrQuotaRefused` break exits only that channel's page loop and sets `ScanStopped`. The outer loop then reaches each later channel, and its first `requireBudget` check refuses it before any provider request is made. Such a channel is not attempted, made no request and has no ledger row. Its floor is unchanged. This interim does not add an early exit from the outer loop; it neither adds nor removes requests compared with today.

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
  - Discovery checks `ctx.Err()` first, as defense in depth. If the context is done, it returns that cancellation error, which keeps the existing interruption handling. Only otherwise does it return the typed error `discovery.ErrNoSourceScanned`. Cancellation never becomes this terminal failure.
  - The existing phase-failure path stores `diag:v1 phase=discovery operation=discover code=no_source_scanned` through `FailRun`.
  - Add `no_source_scanned` to `diag`'s closed `codes`. It is valid only with phase `discovery`, operation `discover` and no other facts. `Classify` maps the typed error to it.
  - The run is terminal `failed`, as today's discovery failure is. Retention and other side effects are unchanged.
- **Otherwise,** the existing flow continues and the run completes or defers as today. That includes a mixed run, and a run where both local failures and a `ScanStopped` happened.

### D6. Inspection without new storage (withdrawn 2026-11-20)

**Withdrawn.** Don't implement this section. [ARS-002 §4](../specifications/ars-002-durable-source-failure-record.md#4-authorized-readers-and-api) replaces the ledger-derived `discovery_incomplete` flag with the durable source-failure rows, which are exact, contain the channel, and don't over-report on resume. The original text is kept below for history.


- The Run read projection adds `discovery_incomplete` (boolean). It is true for a production run (`scheduled`/`manual`) when `quota_ledger` has a row with that `run_id`, `operation_type='playlist_items_list'`, `outcome_category='permanent'` and `http_status=404`.
  - The ledger is durable, append-only, never pruned in source, and survives resume.
  - **Premise, guarded path only.** For production runs on this build, a completed run has such a row only after an isolated D1 skip. On `RunBound`, every admitted read is finalized into the ledger. D1 applies only when that finalization succeeded, because a finalization failure yields a `*ReadError`. Any other permanent 404 fails the run. The legacy `Run` path isolates nothing, and its `RecordCalls` rows carry no category or status, so it cannot satisfy the predicate. If another caller of `playlist_items_list` or another isolation path is added, revisit this derivation.
  - It is a boolean, not a count. The row identifies no channel.
  - **Accepted over-report on resume.** The flag can stay true after an interruption and resume, even if the resumed execution re-scans the previously failed channel successfully. The ledger row from the first execution remains. Run Detail can then show "incomplete" for a run whose final scan was complete. This is accepted for the interim because the note is true of the run's history, and the next run polls that channel as usual. It never under-reports. ARD-001's source records replace this derivation.
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
- ~~The Run read query and projection, adding one derived field.~~ Withdrawn with D6; Run Detail reads ARS-002's run-scoped endpoint instead.
- `web/src/views/RunDetailView.tsx` and `web/src/lib/failure-reason*`: the note and the code's copy.

Explicitly unchanged:

- Existing migrations, plus `error_entry` and the video Errors list, API and commands. *Amended 2026-11-20:* ARS-002 §5 adds migration `030`, `internal/state/source_failure.go`, the source-error endpoints and the read-only Errors source section. Its write boundary governs those additions.
- The quota, authorization and `youtube` packages.
- Metadata prefetch, apart from receiving the smaller merged set.
- Persist transactions, apart from the D3 floor argument.
- Retry, deferral and stop paths.

## Consequences

- **Positive:** One unavailable channel no longer fails every run. Successful channels' discoveries and floors are kept. AC8 becomes explicit. The durable [ARS-002](../specifications/ars-002-durable-source-failure-record.md) record names the skipped channel in Errors and Run Detail, survives restart and resume, and is reused by the full design. *(The earlier ledger-derived note is withdrawn with D6.)*
- **Negative:**
  - G1: no Errors entry.
  - G2: a later-page failure re-fetches the channel's earlier pages next run. A video removed upstream in between is missed; in practice `playlistNotFound` occurs on page 1.
  - G3: identity comes only from logs, which are not durable product evidence.
  - A permanently missing playlist costs one unit per run until the user unsubscribes. This matches the accepted "retain ordinary polling".
- **Neutral:** Runs 52 and 53 are not recovered or reinterpreted. The full ARD-001 design replaces D2–D6. Runs from this build do have source-failure records ([ARS-002](../specifications/ars-002-durable-source-failure-record.md)), and the full design keeps reading them. Only runs from builds without migration `030` lack them, and the readers show that evidence as unavailable.
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
   - a 404 `playlistNotFound` with `Complete=false` or an empty `Reasons` list;
   - a 404 `playlistNotFound` returned inside a `*ReadError` with `Accounting`, `Renewal` or `Interruption` set;
   - the run context canceled, including when the provider returned a clean 404 `playlistNotFound`;
   - the legacy `Run` path returning a clean 404 `playlistNotFound`;
   - 500/`backendError`;
   - `quotaExceeded`;
   - 401/`authError`.

   A proactive refusal breaks that channel's page loop. Later channels are refused before any request: no provider request, no ledger row and no attempt count for them. `ScanStopped` is set, and the run does not take the AC8 failure.

   With the run context canceled after every channel returned 404, discovery returns the cancellation error, not `ErrNoSourceScanned`. The run is interrupted as today, not failed.
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
- ~~A migration slot is assigned.~~ *Done 2026-11-20:* `030` is assigned and [ARS-002](../specifications/ars-002-durable-source-failure-record.md) replaces D6.
- The user declines G1 or G2.

## Open questions

Answered by the user on 2026-11-20 (relayed by the dispatcher). Nothing user-owned remains open.

- **G1 (OC-001): declined.** "there is no rush, implement the proper fix." The durable read-only Errors entry ships with this interim, through [ARS-002](../specifications/ars-002-durable-source-failure-record.md) and migration `030`.
- **G2 (BR-002 AC9): accepted.** Until the full design, a channel that fails after earlier pages contributes none of its videos to that run. They are re-polled next run from the unchanged floor.
- **G3 (diagnostic log contract): accepted.** The validated channel ID is included as `source_channel_id` in the single D7 warning line. This is a bounded exception to the diagnostic log rule for that one record only.
