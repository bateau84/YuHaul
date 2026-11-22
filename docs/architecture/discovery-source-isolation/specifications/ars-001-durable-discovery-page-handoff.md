---
type: specification
title: ARS-001 — Durable Discovery Page Handoff
description: The commit, ownership, readiness and queue contract for processing observations recovered from incomplete uploads scans.
tags: [architecture, specification, discovery, readiness, queue, recovery]
timestamp: 2026-11-22T12:00:00Z
---

**Status:** proposed technical contract for accepted behavior. No implementation or runtime proof. Reconciled 2026-11-22 (Plan r30 node `run52-arch-reconcile`) with what is on `main` at `8d7e009`:
- migration `030` and [ARS-002](ars-002-durable-source-failure-record.md): the immutable `discovery_source_failure` table, `run.discovery_scan_outcome` and the source Errors projection;
- migration `031` and [receiving ARS-005 §5.1.1](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md#511-durable-retry-claim-provenance): `run_video_outcome.retry_claim_provenance`, the Store A default clearing, and the receipt functions;
- the receiving contracts in ARS-005 §5–6.

This spec now reuses those facts instead of restating them. It no longer stores a separate run assessment; §1.3 derives it. §1.2 defines the run dispatch stop. §4 gives the migration content against the `031` baseline. Revised the same day for critique `ses_dff73847effengFMSzBkXiCHwF` (CHANGES REQUIRED, F1–F6 and advisories). Re-critique is required before anyone relies on it.

A committed uploads page leaves real video work that survives the source, process and run. In a mixed run, a video from B's first page can be processed even if B's third page fails. If the server crashes before loading that video's metadata, the originating run can recover it. If all sources fail and the run must end, a fresh receiving run can claim the same video through Deferred even if YouTube never lists that ID again. Re-polling B and processing that known video are separate obligations.

## Implements

- [Discovery source isolation ARD-001](../design/ard-001-isolate-uploads-source-failures.md).
- User decision, relayed in Loom answer `f190b26f-3e88-49b0-a1c2-5d62992c2479` to `d69a1aba-447b-4dc0-bbf8-517b913219e7`: in the narrow only-source restart case, complete with explicit incomplete coverage and hand off saved work without routing it in the origin. The specifier records the authoritative requirements separately.

## Satisfies

- [BR-002](../../../requirements/yuhaul/br-002-subscription-discovery-without-live-monitoring.md) AC3–4, AC6–11: page-level ownership with completed-only coverage; a source-local failure skips only that source in this run; ordinary mixed-run admission; retention before terminal all-local failure; the only-source eligibility change ends Completed after a durable handoff. The accepted user decisions behind AC7–11 are preserved. AC11 (multi-source recovery with a now-ineligible source, commit `44f3ba4`) resolved the former open decision 1.
- [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md), [BR-011](../../../requirements/yuhaul/br-011-processing-state-persistence.md), [BR-013](../../../requirements/yuhaul/br-013-quota-limited-work-deferral.md) and [BR-030](../../../requirements/yuhaul/br-030-temporary-failure-work-preservation.md): real work survives, with exact reason and guarded read accounting. The permanent-source Errors projection remains in ARD-001; it does not receive real-video work.
- [Production authority and receiving ARS-005](../../duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md), [BR-039 and continuation ARS-006](../../run-initiation/specifications/ars-006-final-outcomes-are-never-reprocessed.md), [guarded dispatch](../../youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md), and [Stop/Discard handoff](../../run-initiation/specifications/ars-003-stop-run-and-deferred-handoff.md): existing receipt, snapshot, terminal-outcome and action authority remain authoritative.
- [BR-037 receiving ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §5–6 supplies a shared receiving pattern; its historical scan/confirm flow and special final outcome are not part of this contract. [BR-026](../../../requirements/yuhaul/br-026-dry-run-evaluation.md) and [BR-027](../../../requirements/yuhaul/br-027-historical-backtesting.md) retain their original selectors, local metadata/range meaning, and isolation.

## 1. Commit boundary and records

Use the existing SQLite writer and production lock. `DiscoverVideoTx` currently creates the native video and its original `run_video_outcome` together; production's selector and terminal/stop sweep can find this ownership without any route decision. Extend that boundary, not the API call, to accept a page. No SQLite transaction spans provider I/O.

| Fact | Exact meaning |
|---|---|
| `video.discovery_page_observation` | Integer boolean, default `0`, only `0` or `1`, immutable after creation. Emit `1` only for new native IDs admitted by the new page transaction. `0` is existing or unrelated producer behavior, not a claim about inputs. This identifies exactly which videos can be exposed before enrichment, even after Store A eviction or in a receiving run. Do not promote older videos/observations on a re-poll. |
| Existing native identity and origin | Uniqueness is the YouTube `video.video_id`; engine-local existence checks are advisory. Winner records the scanning run as `discovered_in_run_id`, original source/video attribution, discovery time, run-local `not_yet_evaluated` membership, a native fingerprint when the title is available, and one increment to channel discovery statistics. Imported fingerprint for that ID is not a native video and retains existing monotonic reference eligibility. |
| Listing metadata | Store A only: when a real listing title is available, keep it, the published date and other allowed listing values under existing expiry/write rules. Description and duration are `NULL` until reliably provided. If no title is available and the Store A title constraint prevents insertion, retain the video and membership without a Store A row or a fabricated fingerprint; the readiness step can create them from a usable detail. Never materialize an absent/invalid field as `""` or `0`. Valid empty description and valid zero duration from a decoded detail are non-null values. TTL applies equally to listing and enriched values. |
| `deferred_item.deferral_reason = discovery_processing_pending` | New registered reason for a real, released observation awaiting a later processing pass where there is no more specific existing reason. Not a phase, new outcome, source Errors entry, or `requeued_by_user`. `run_id` is the releasing origin run, `video_id` the existing native row, `status=pending`, `retry_run_id=NULL`, `retry_after=NULL`, `retry_count=0` on first creation. `created_at` is release time in existing queue timestamp format. If already retained, keep the original queue row, reason, age and receipt lineage; do not insert a sibling to restate a reason. |

The **obligation at page commit is durable original ownership, not premature queue eligibility**. A committed marked video is either (1) owned for processing by its originating unfinished run, (2) settled under existing outcome/retained-removal contracts, or (3) released to the existing real-video queue with a receivable row. Page commit creates (1), not a claimable queue item. This reuses ordinary mixed-run routing without adding a held-queue state or letting a sibling claim hide the video from `RoutableByRun`.

Take the writer before native admission and related read-check-write arbitration. Within one transaction persist all newly admitted page observations and the source row's `has_committed_page`, and, only for the last successfully traversed page, the row's `complete` settlement and cutoff (§1.3). Duplicate IDs within the page, on a repeated page, or already known from another run are no-op for video/metadata origin, fingerprint, discovery count and processing entitlement. They still count as observed for traversal, and do not invalidate valid end-of-scan completion. A replay of an uncertain commit reconciles from durable native identity and source records before another provider call; it never retries side effects by guessing. Rejected/rolled-back page work advances neither ownership nor cutoff.

### 1.1 Stored facts and their owners

Every durable fact this contract uses has exactly one owner. Facts already on `main` are reused, not copied.

| Fact | Storage | Owner and status |
|---|---|---|
| Source failure incidents | `discovery_source_failure` (migration `030`) | [ARS-002](ars-002-durable-source-failure-record.md) §1–§3; built. This spec adds a writer for `category='transient'` (§1.3) and moves the permanent insert into the settlement transaction. No DDL change. |
| Did the uploads scan finish | `run.discovery_scan_outcome` (`030`): `NULL`, `cut_short`, `completed`, `no_source_scanned` | ARS-002 §4.5; built. On the page path its three writes move into this spec's transactions (§1.3). It is the only stored "scan finished" fact. There is no second assessment column. |
| Receiving claim identity | `run_video_outcome.retry_claim_provenance` (`031`) | Receiving ARS-005 §5.1.1; built. This spec reads it and never writes it. |
| Run dispatch stop | `run.dispatch_stop` | §1.2; new. |
| Sealed scope and per-source coverage | `run.discovery_scope_cutoff`, table `discovery_source_scope` | §1.3; new, in the discovery migration (§4). |
| Page-admitted marker | `video.discovery_page_observation` | §1 table above; new, in the discovery migration. |
| Frozen legacy floor | `channel.legacy_discovery_floor` | §1.3; new, copied once by the discovery migration. |
| Saved-work queue reason | `deferred_item.deferral_reason = 'discovery_processing_pending'` | §1 table above; new CHECK value in the discovery migration. The Deferred label is "Saved discovery awaiting processing" ([admin design](../../../design/yuhaul-admin-ui-design.md), designer answer `68a9007e`). |

### 1.2 Run dispatch stop

**What it means.** `run.dispatch_stop` records that this production run has proved it must not start another run-owned YouTube request for the rest of its life. A run-owned request is any Data API read (subscriptions, channels, uploads, `videos.list`), any playlist mutation, and any token renewal started for a new protected request of this run. It is the run-scoped veto of receiving ARS-005 §6.1 and §5.2 step 2.

It is not:
- a user control (Stop uses `stop_requested_at`);
- an account-wide or provider-wide lock (a fresh run performs ordinary authorized admission again);
- a source failure, a run status or a phase.

**Storage.** One nullable column on `run`. No timestamp and no detail column. The detail already has an owner: the authorization cause (reconnect required or temporarily unavailable) is the account authorization state; quota evidence is `quota_ledger`; an accounting or integrity fault is the run's safe terminal diagnostic.

```sql
ALTER TABLE run ADD COLUMN dispatch_stop TEXT
    CHECK (dispatch_stop IS NULL OR dispatch_stop IN ('authorization', 'quota', 'accounting_or_integrity'));

CREATE TRIGGER run_dispatch_stop_monotonic
BEFORE UPDATE OF dispatch_stop ON run
WHEN OLD.dispatch_stop IS NOT NULL AND (
    NEW.dispatch_stop IS NULL OR
    (CASE NEW.dispatch_stop WHEN 'authorization' THEN 1 WHEN 'quota' THEN 2 ELSE 3 END) <
    (CASE OLD.dispatch_stop WHEN 'authorization' THEN 1 WHEN 'quota' THEN 2 ELSE 3 END))
BEGIN
    SELECT RAISE(ABORT, 'run dispatch_stop cannot be lowered or cleared');
END;
```

**Values.** Each value forbids every further run-owned provider request. They differ only in what local work may continue.

| Value | Set when | Local work afterwards |
|---|---|---|
| `authorization` | Under OC-006 there is no safe authorized continuation: reconnect required, renewal unavailable, identity changed, or a rejection the one permitted same-identity read retry did not recover. A first rejection that the permitted retry recovers sets nothing. | Variant K local-only claiming and settlement continue. Unfinished work settles as `authorization_pending` (ARS-005 §6.2). |
| `quota` | A known minimum-unit preflight refusal (no ledger row), or an admitted provider `quotaExceeded`/`dailyLimitExceeded` or equivalent registered classifier evidence (one `quota` ledger attempt), even with positive local headroom. | No further claims (ARS-005 §5.2 step 2). Unfinished API-dependent work is deferred `quota_exhausted`. |
| `accounting_or_integrity` | An outer `ReadError.Accounting`, a local quota/admission store failure, or another fault that prevents reliable admission or settlement. | No further claims. The fault propagates fail-closed through the existing retain-before-fail path. |

**Rank and precedence.** `authorization` < `quota` < `accounting_or_integrity`. A write may only raise the rank. Nothing lowers or clears the value: not Run Now resume, startup recovery, Stop, Discard, or a terminal write. A compound error writes its highest-ranked value. Settlements already committed under a lower value (for example `authorization_pending` deferrals) stay as they are.

**Scope.** One production run. Simulations (dry run, Replay, Backtest) never write it and always read `NULL`. A same-run resume inherits the stored value. A fresh run starts at `NULL`.

**Writers.** Only the run's own owner process, under the SQLite writer, in a transaction with no provider I/O:

1. the guarded read disposition for discovery reads (subscriptions, channels, uploads);
2. the run-scoped disposition returned by the bound metadata read (`FetchForEvaluation`) and by the discovery prefetch;
3. the guarded executor, when an action result carries a dispatch stop (ARS-005 §6.1), without changing mutation classification;
4. the minimum-unit preflight refusal at claim, discovery or fetch, which writes `quota`. In discovery this is the budget break: today's `requireBudget` refusal that sets `ScanStopped`. The stop commits before the loop breaks, so a budget break always leaves a stored `quota` stop. A preflight *store* error (`CanAfford` fails) is not a refusal and not a budget break: it writes `accounting_or_integrity` and propagates. Today's code treats it as a budget break; that changes with the page path;
5. same-run recovery, before any run-owned provider work: if the run's typed ledger evidence shows an admitted provider quota attempt and the stored value ranks lower, write `quota`.

The statement is `UPDATE run SET dispatch_stop = :v WHERE id = :run AND (dispatch_stop IS NULL OR rank(dispatch_stop) < rank(:v))`, with `rank` written as the trigger's `CASE`. Zero affected rows is not an error: an equal or higher stop already exists. The write commits before the next run-owned provider request. It may share the transaction of the settlement it causes, such as an `authorization_pending` deferral. If the write fails, the process keeps the stop in memory, makes no further provider request in this execution, and propagates the classified SQLite error under existing retention.

A dispatch stop does not settle any source row and does not write `discovery_scan_outcome`. A stop during the uploads scan leaves `cut_short` standing (§1.3).

**Readers.** Two kinds of read:
- **Per call, inside the writer transaction:** the receiving claim and `ClaimPlaylistSend` re-read `run.dispatch_stop` in the same writer transaction that admits the claim or the send. A claim is refused for `quota` or `accounting_or_integrity`; it is still allowed for `authorization`, because variant K local-only claiming continues. `ClaimPlaylistSend` is refused for any non-`NULL` value. These are the points where a stale copy could otherwise let a send or a claim through.
- **Per execution, for every other reader:** read the stored value when the execution starts, and again after a resume or restart. After that, use the execution's own copy, updated by every write this execution makes. This is safe because only the run's owner writes the value, and the production lock gives one execution per run. The copy may only add a stop, never remove one.

The readers are:
- The discovery engine, at pass start and before each subscriptions, channels or uploads request.
- The metadata read (`FetchForEvaluation`) and the discovery prefetch.
- The claim loop (ARS-005 §5.2 step 2), the claim itself (per call), and the claimed-batch precondition (ARS-005 §6.2).
- The executor, before guard preparation, and `ClaimPlaylistSend` (per call).
- Token renewal requested for a new protected request of this run.
- Run Detail, only through `discovery_acquisition.blocked_reason` (§5).

Floor and eligibility logic, fresh runs, Errors, analytics and simulations do not read it.

**Accepted trade-off.** Because nothing clears the value, a same-run resume after the daily quota resets still cannot dispatch. That covers Run Now resume and startup recovery of the same run. Its unfinished API-dependent work stays deferred, and the next fresh run claims it through ordinary admission. The simpler rule wins: no clearing writer, no reset-time evidence, and no risk of a stale stop being lifted mid-run. Revisit only if quota-stopped runs are routinely resumed and that delays work by a whole extra run.

**`NULL` and legacy.** `NULL` means no stop was recorded. Every row from before the migration is `NULL`, with no backfill. `NULL` alone does not authorize dispatch on a same-run resume: recovery still applies writer 5 and current admission.

**Which migration.** The column and trigger go in exactly one migration. The Plan's phase `migrations` gives it to PR-R (`rq-stop-schema`, expected `033`) and [receiving ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §6 (`rq-stop-arch`) records the storage. If PR-R merges without it, the discovery migration (§4) carries it as its first statement. The DDL above is the shape either way.

### 1.3 Sealed scope, settlement and the derived acquisition state

**Stored scope.** `discovery_source_scope` has one row per `(run_id, channel_id)` (DDL in §4). Each row holds:
- the source's input floor and its kind (`modern` or `legacy`);
- `settlement`: `unsettled`, `complete`, `local_failure` or `no_longer_eligible`;
- `has_committed_page`;
- the completed cutoff;
- the settlement time.

The row is mutable only along the allowed transitions. Incidents stay in ARS-002's immutable table; the two are joined only by `(run_id, channel_id)`.

**Seal transaction.** Seal only after a successfully traversed subscription enumeration and channel resolution. Partial subscription pagination, a refused channel batch, or an empty in-memory list is not a ready scope. For both scope and successful source completion, require accounted, structurally valid results: an explicit valid `items` array, a valid continuation when supplied, and all pages traversed. A malformed response never becomes empty or terminal by default.

Before the first uploads request, one writer transaction:
- inserts the full source inventory as `unsettled` rows, each with its input floor;
- sets `run.discovery_scope_cutoff` to the seal time (no later than the first source request);
- replaces ARS-002 §4.5 write 1 by setting `discovery_scan_outcome = 'cut_short'` in the same transaction.

A source that already has a permanent `discovery_source_failure` row for this run is inserted directly as `local_failure`. That is the ARS-002 §3 same-run skip, now carried by the scope. It covers a run started on a `030`/`031` build and resumed after the upgrade. If no row is `unsettled` (an empty scope, or every source pre-settled), the same transaction writes `completed` instead.

`discovery_scope_cutoff IS NOT NULL` is the only proof of a sealed scope. A genuinely empty scope is representable: the cutoff is set and there are no rows. Do not store uploads playlist IDs, URLs, credentials or page tokens. A source row starting `unsettled` is not evidence of an attempt.

**Floors.** A source's input floor is, in order:
1. the maximum `completed_cutoff` of its `complete` rows (kind `modern`);
2. else `channel.legacy_discovery_floor` (kind `legacy`);
3. else `NULL` (existing first-use coverage).

`channel.last_discovered_at` stays a statistics field and is no longer read as a floor. Use the same floor on every page and on every compatible same-run rescan, which begins at page one. Against a `modern` floor, reconsider an exact tie and deduplicate by native ID. Against a `legacy` floor, keep the old compatibility filter. Published coverage is the maximum of completed cutoffs, so it never regresses if an older run resumes after a newer one.

The following do not move coverage:
- a refused or failed source;
- a successful intermediate page;
- a receiver action;
- a metadata result.

Fresh polling uses its newly selected, still-eligible scope, whether or not earlier video work has been processed. Empty successful scans count as completion. A crash cannot turn an untraversed list into an empty success.

**Settlement transactions.** Each settles one row, under the writer, with no provider I/O:
- **Page commit.** Set `has_committed_page = 1` with the first successfully committed page, even if it adds no new native rows. The final page's transaction also sets `complete` and `completed_cutoff = run.discovery_scope_cutoff`. The completed cutoff is always the run's seal cutoff, never a returned item's time or the commit time. A trigger enforces this (§4.1).
- **Local failure.** For a typed source-local failure (ARD-001's table), set `local_failure` and insert the incident in the same transaction. A permanent failure uses ARS-002 §2's statement unchanged.

  An *attributable transient* failure is exactly an admitted uploads read of that source that returns a 5xx, or a per-request timeout, while the run's context is still live (`ctx.Err() == nil`). It inserts a `category='transient'` row with `http_status` (5xx, or `NULL` for a timeout) and a vetted reason or `NULL`.

  Either way, the source is skipped for the rest of this run (BR-002 AC6) and polled again by the next eligible fresh run (AC7). No same-run retry of a settled source.

  **Not a local failure:** a rate limit (429, or `rateLimitExceeded`/`userRateLimitExceeded`) follows ARS-005 §6.1's pacing. The row stays `unsettled`, with no incident and no dispatch stop. Run cancellation or deadline, account-side failures and other 4xx variants are not local failures either.

  **Rate-limit suspension (scan).** On a rate limit from any uploads read, the execution suspends uploads reads for the rest of that execution. It makes no further `playlist_items_list` request for that source or any other `unsettled` source, and it does not wait and retry in place. The suspension is held in memory only: it is not a dispatch stop, writes nothing, and does not affect other phases. Those continue under their own pacing, quota and authorization guards. The scan then ends with `unsettled` rows, so `cut_short` stands and no `completed` is written.
  - *Why suspend rather than wait and retry:* it needs no in-discovery timer or back-off policy, and it matches ARS-005 §6.1's existing fetch-chunk suspension.
  - *What it costs:* the unsettled sources are retried only by a resume (which restarts them from page one) or by the next eligible fresh run.
  - *End state:* because the pass is not settled, no marked observation is routed in this execution, including those of completed sources. The end state is §2's run-wide deferral row.
- **No longer eligible.** Recovery may settle a source `no_longer_eligible` only from complete, current eligibility evidence. A partial list or a transient resolution failure is not proof. This settlement does not erase `has_committed_page`. It is its own writer transaction on recovery, with no provider I/O; if it settles the last `unsettled` row it also writes `completed` (below). With `has_committed_page = 1` the source is an *ineligible-after-page source* (BR-002 AC10–11). It counts as neither a completed scan nor a local failure. Its saved observations are never routed in the origin and are handed off by §2's terminal handoff rule. With `has_committed_page = 0` it is unexamined, and the pass settles from the other rows.
- **Last row.** The transaction that settles the last `unsettled` row also writes `discovery_scan_outcome = 'completed'`. That replaces ARS-002 §4.5 write 2. A budget break or provider quota leaves rows `unsettled` with a stored `quota` stop (writers 1 and 4), so `cut_short` stands and the derived state is `blocked`. A rate-limit suspension, Stop, cancellation or crash also leaves rows `unsettled` and `cut_short`, but with no stop, so the derived state is `scope_ready`. A run that reaches a terminal result after a rate-limit suspension releases its unfinished marked work under §2's run-wide deferral row.

**Resume.**
- *No sealed scope:* rerun enumeration and seal.
- *Sealed scope with `unsettled` rows:* first reconcile the dispatch stop (§1.2 writer 5). Then restart only still-eligible `unsettled` sources, from page one, after current authority and source-resolution checks. Never poll a `complete` or `local_failure` source again in that run. Never treat a persisted source ID as authorization to poll an ineligible source. No new source is added to the sealed pass.
- *No `unsettled` row:* derive the result with no further uploads request.

`current_phase`, in-memory loop position and zero counts are not evidence of pass settlement.

**Derived acquisition state.** There is no stored assessment. Readers derive it in one SQLite read view from:
- the run's `status`, `discovery_scope_cutoff`, `discovery_scan_outcome` and `dispatch_stop`;
- the run's scope rows.

Apply the first matching rule:

| Condition | State |
|---|---|
| `discovery_scope_cutoff IS NULL`, `dispatch_stop` set, `initiated_by IN ('run_now','schedule')` and `discovery_scan_outcome IS NULL` (a discovery run stopped before sealing) | `blocked` |
| `discovery_scope_cutoff IS NULL` (every other case, including `queue_retry` runs, simulations, legacy runs and runs from `030`/`031` builds with a stored scan outcome) | none (Run Detail `discovery_acquisition: null`) |
| `dispatch_stop` set and some row `unsettled` | `blocked` |
| Some row `unsettled` | `scope_ready` |
| `status = 'failed'` and `discovery_scan_outcome = 'no_source_scanned'` | `all_local_failed` |
| `status = 'completed'`, every row settled `no_longer_eligible`, at least one with `has_committed_page = 1` (AC10, and AC11's all-ineligible case) | `eligibility_changed_no_coverage` |
| No row is `complete`, `local_failure`, or has `has_committed_page = 1` | `no_attempts` |
| At least one `complete` row and every row `complete` | `complete` |
| At least one `complete` row | `mixed` |
| Otherwise (all settled, no completed source, no terminal transaction committed yet, or Stop won first) | `scope_ready` |

`scope_ready` therefore means "scope known, no final acquisition result". `all_local_failed` and `eligibility_changed_no_coverage` appear only after their terminal transactions commit, because each needs the terminal status. A row settled `no_longer_eligible` with no committed page counts as unexamined, not attempted.

**Derived values for runs from `030`/`031` builds.** Those runs have no cutoff, so the state is none. Readers fall back to ARS-002's `discovery_scan` and incident rows, which keep their meaning. Readers that understand the derived state prefer it.

**Routing permission.** Routing of marked observations is allowed only for `mixed` or `complete`, and only after the last-row transaction committed `completed`. Even then, a marked observation routes only if its `video.channel_id` scope row is `complete` or `local_failure` (AC9). Observations of an ineligible-after-page source are never routed in the origin (AC11). All-local failure and the ineligible-after-page cases follow §2.

## 2. Origin-run ownership and release

Use the existing single production lock, run snapshot, `withoutRetryVideos`, `RoutableByRun`, final classification and guarded executor. A future run cannot discover a known ID as its own work. A run may not route a marked observation until the acquisition path durably establishes a disposition allowing routing, and the readiness check in §3 succeeds. Resume at any phase must honor that disposition and existing queue ownership, not just `current_phase`.

| Condition | Transactional disposition and subsequent eligibility |
|---|---|
| Live origin, acquisition incomplete or genuine interruption | Original membership remains the owner; no pending item merely because the run crashed. Continue only through existing compatible startup/Run Now rules, keeping the original snapshot. The last-row settlement commits `discovery_scan_outcome = 'completed'` (§1.3) before the run moves to its next operation. Re-entry must honor that settled pass without running a new discovery pass. Stop/Discard remains available only under its own authority. |
| Derived `mixed` or `complete` (§1.3) | Route eligible ready native admissions from every observed successful page, including pages from an incomplete source. For a new observation whose required inputs cannot be used this run, atomically retain a Deferred row and an origin `deferred` outcome with actual operational reason (`metadata_unavailable`, or existing quota/rate/auth reason). Use `discovery_processing_pending` when no existing queue reason fits. The origin's committed `deferred` outcome excludes it from further origin processing, including resume after the queue row is claimed, completed or removed. Do not pass an empty filtered slice back through a fallback that reloads released inputs. |
| All-local failure | Under the existing initiation/production ownership and SQLite writer, recheck the sealed scope (no `unsettled` row, none `complete`, at least one `local_failure`, and every other attempted source an ineligible-after-page source per AC11), `run.dispatch_stop IS NULL`, run nonterminal ownership, and no accepted Stop marker/Discard winner. One transaction applies the terminal handoff rule below, creates or preserves one pending row for every newly observed unfinished unreleased video, writes safe summary/counters, and marks terminal `failed` with `discovery_scan_outcome = 'no_source_scanned'` in the same `UPDATE` (ARS-002 §4.5 write 3, explicit parameter, never reason text). Leave those videos `not_yet_evaluated`, with no new route/action evidence, and preserve earlier settled retries. Do not call the generic runtime-error sweep for these observations. Failed transaction: no terminal status, new foreign eligibility or event; recover from the still-owned observations plus sealed scope. Lost commit result: read the committed state and reconcile, never publish a contradictory status. A committed Stop/Discard first wins; if terminal failure commits first, those controls apply only their existing refusal. |
| Source ineligible after a saved page (AC10, AC11) | Do not re-poll the source, advance its floor, write a source Errors entry, or enrich, route or send for its observations in the origin. Its row settles `no_longer_eligible` with `has_committed_page = 1` (§1.3), and its saved observations are handed off by the terminal handoff rule below. The lifecycle comes from the other rows:<br>• **At least one `complete` (AC11 case 1):** the derived state is `mixed`. The run routes the other sources' eligible observations normally and ends `completed` through the existing path. Run Detail shows explicitly incomplete coverage.<br>• **None `complete`, at least one `local_failure` (AC11 case 2):** the all-local-failure row above applies. The run ends `failed` with `no_source_scanned` and routes nothing.<br>• **Every row `no_longer_eligible` (AC10, AC11 case 3):** recheck the sealed scope, prior page evidence, stored eligibility result, effective dispatch stop, nonterminal ownership and Stop/Discard, under the same writer rules as all-local failure. One transaction applies the terminal handoff rule, writes `discovery_scan_outcome = 'completed'` if it is not already written, and publishes status `completed` with terminal time and counters. The derived state is then `eligibility_changed_no_coverage`.<br><br>In every case, keep the original membership and the `not_yet_evaluated` outcome. There is no fabricated deferral outcome, video or source failure, or action. The next eligible fresh receiving run may claim the saved work regardless of current subscription or whether the provider repeats the ID. Before commit, original ownership holds; a rollback creates no terminal result and no foreign eligibility. After commit, recover by reading the terminal record. Do not repeat the handoff, poll, or reopen the origin. Existing final, removed and separately retained work is not recreated. |
| Global quota/auth, rate-limit suspension, or terminal local failure | Use existing lifecycle and reason precedence, never all-local-failure derivation. The run's `dispatch_stop` (§1.2) carries the run-wide stop; `cut_short` stands for an unsettled scan. Before publishing a terminal result, unfinished open discovery work not itself evaluated/failed must have a durable receiving row or existing retained owner; a real failed evaluation/integrity result still follows its existing retained-error path. In global quota/auth use the actual existing deferred reason, no raw error and no continued provider dispatch. Genuine interruption retains original ownership for unreleased work.<br><br>**Rate-limit suspension at the terminal result.** This case is a run ending with a terminal result other than Stop or Discard, with `unsettled` rows, `dispatch_stop IS NULL`, and an uploads suspension recorded in this execution (§1.3). It is a run-wide deferral under the existing `api_rate_limited` reason.<br><br>The terminal transaction does the following, under the writer and the same rechecks as the all-local row (sealed scope, nonterminal ownership, no accepted Stop marker or Discard winner):<br>• it applies the terminal handoff rule above; then, for every other unfinished, unreleased marked observation of the origin, with no route decision and not final, removed or already retained, it writes the `retainNativeTx` pairing: origin `run_video_outcome` `deferred` with `deferred_reason = api_rate_limited`, and exactly one pending `deferred_item` with `deferral_reason = api_rate_limited`, due on commit;<br>• it then publishes the terminal status and counters.<br><br>`discovery_processing_pending` is not used, because an existing reason fits. The scan outcome stays `cut_short` and the derived state is `scope_ready`. No source incident, coverage advance or dispatch stop is written.<br><br>If the transaction rolls back, original ownership holds and there is no terminal result. If the commit result is lost, read the committed state and reconcile. Stop/Discard committing first wins, with its own handoff. A crash before the terminal transaction is an interruption: the suspension is lost with the execution, and a resume restarts the unsettled sources from page one. |
| Confirmed Stop or Discard | Existing handoff takes precedence under the writer and its status/stop checks. Reuse pending rows, don't duplicate them or undo user removal. If either terminal transaction above wins first, do not stop, discard or reopen that terminal run. No new provider work is authorized by either disposition. |

**Terminal handoff rule (derived from AC3, AC10–11).** The handoff of an ineligible-after-page source's saved observations is durable before *any* final run outcome is saved: `completed`, `failed` (including AC8), a run-wide deferral (quota, authorization, or the rate-limit suspension), or a terminal local failure. Every transaction that publishes a terminal status, other than Stop and Discard, first writes, in the same transaction, a due `discovery_processing_pending` row for every unfinished, unreleased marked observation whose source row is `no_longer_eligible` with `has_committed_page = 1`. That excludes work that is final, removed or already retained. Its origin outcome stays `not_yet_evaluated`.

This takes precedence over the run-wide deferral pairing for those observations, so the rate-limit row's `api_rate_limited` pairing covers only the other marked work. Stop and Discard keep their existing handoff, which already gives unfinished work a pending row. A run with an ineligible-after-page row can never commit a terminal status without these rows. Without this rule, a restart after a Failed outcome could lose the saved observations.

For release, under the writer verify the originating or current receipt owner, no incompatible claim, and no final or user-removed work; do not manufacture work for a video already owned or settled. An already committed identical release is a no-op, preserving item ID and age. Unexpected missing/contradictory membership fails visibly instead of creating a substitute. Check ownership, origin status and stop request again inside finalization. Final status, counters and queue evidence are published only after the corresponding durable write. A later release must not rewrite an origin's already committed historical outcome.

Use set-based local writes and no provider calls for the all-local transaction. Do not publish `failed` between batches. A separately committed series of queue batches would require an additional durable admission fence to prevent partially released work from being taken before finalization. Prefer the single transaction unless the assembled active-run search/write checks demonstrate it cannot meet the applicable limits. Test terminal-failure writes with a realistic large acquired set, not just an empty or single-page set.

Neither the new completion path nor the derived acquisition state is a new run status. BR-002 AC7–11 still govern fresh eligible polling, mixed runs and all-local failures. Accepted Stop/Discard, genuine interruptions and global quota/auth/accounting outcomes retain their own precedence and behavior.

For first foreign claim of a released item, normal queue due checks, production admission and snapshot capture apply. A mixed-run release is due upon release, but only the existing production gate permits foreign admission. Both special terminal handoffs become due with their terminal commits. Use the same persisted `(receiving run, video)` fence in automatic enumeration, writer-side fresh claim (including direct/manual entry), and ordinary phase/recovery selection. A receiving membership with foreign native origin is not an ordinary native input even after clearing `retry_run_id`; original native ownership may proceed only if the origin did not release it and its settled pass permits routing (§1.3). For the marked-observation routing gate only (`discovery_page_observation = 1`), native authority is the origin's own membership with `retry_claim_provenance IS NULL` and `video.discovered_in_run_id` equal to the run. Unmarked native rows (`0`) keep ARS-005 §5.1.1's existing rule unchanged. In both cases `NULL` provenance alone is not native authority (ARS-005 §5.1.1 *Compatibility*). A receiving claim of a released row writes its provenance once, through the existing `031` claim (`ClaimDeferredGroupReceiptTx`); release (`RequeueRetryReceiptTx`), deferral acknowledgement (`AcknowledgeRetryDeferralReceiptTx`), removal and outcome upserts preserve it. Inspect membership without giving it dispatch authority: only an exact still-owned receipt restores receiving dispatch; a released/removed/completed item is not claimed again in that run. Do not use phase fallback, a timer, `MAX(id)` or retry counts to infer ownership. Keep FIFO `(created_at,id)` among eligible groups and existing capacity-specific settlement without dispatch.

## 3. Metadata contract before evaluation

- **Who checks:** New marked native admissions before exclusion/detection/routing, and marked **open** queue recipients after `ClaimDeferredGroupReceiptTx` and BR-039 classification. A `GetDisplayMetadata` success alone is not proof of readiness. Use the locally held, unexpired inputs required by the run snapshot and normal duplicate evaluation (title), including channel name, description and duration where an applicable rule can read them. IDs come from native evidence. Readiness checks include exclusion inputs. Using real empty/zero fields is allowed; substituting for missing data is not. An ID/channel-only exclusion may use the actual identity; no fabricated metadata is needed or permitted for a predicate. If an unfinished evaluation has a persisted prefix and its inputs must be reloaded, use the existing non-recording snapshot evaluation and persisted-prefix comparison from production-authority ARS-005 before appending to it. Missing inputs yield retention; contradiction uses the existing authority-failure path. Do not overwrite an audit or append with incompatible inputs. A completed, otherwise authorized decision does not need refetching only because a display field expired.
- **How to load:** Reuse the bound, quota-accounted BR-037 §6 read seam, up to 50 distinct IDs per call. Current locally usable IDs need no fetch. Upsert a valid detail in Store A through the existing TTL/index-aware writer even if the prior metadata row was evicted; do not infer success from an `UPDATE` affecting zero rows. For the new marked population the parser and refresh writers must keep field presence/validity: use `NULL` for missing/invalid route inputs, including duration parse failures; do not let another refresh writer silently substitute defaults. A successfully returned ID without usable required fields is `unusable`, never `absent`. Establish `absent` only from a complete, successful returned-ID response. No raw payload or extra snapshot is retained. If a row expires/disappears between preflight and evaluation, the per-predicate read backstop fails closed rather than invoking existing degraded-metadata fallback.
- **Origin vs receiver:** Apply receiving ARS-005 §6's typed admission/renewal/accounting precedence and run-scope veto (the §1.2 dispatch stop) at origin, receiver and later phases. Expiry or loss after preflight is a **per-video** readiness result, not a fatal phase exception: stop evaluation of that marked origin video, atomically defer it under §2 with `run_video_outcome.deferred_reason=metadata_unavailable` and queue `discovery_processing_pending`, and continue independently ready work where dispatch remains allowed. Compare any persisted unfinished decision/audit prefix against reloaded inputs before appending; a contradiction is authority failure, not ordinary expiry. An upload-list failure or first enrichment pass cannot finalize a video as unavailable. For a claimed group follow receiving ARS-005 §6: ready → normal execution; quota/auth/rate → exact deferred disposition and `AcknowledgeRetryDeferralReceiptTx`; failed/unusable or late expiry → release unchanged with `RequeueRetryReceiptTx`; established absence from a validated complete envelope → ordinary **non-`requeued_by_user`** retry-failure retention. Source handoff alone never qualifies for BR-037's final absence outcome. Validate the exact receipt again before any settlement or send; losing ownership cannot recreate or acknowledge the removed item.
- **Modes and safety:** BR-039 `final` closes or acknowledges existing ownership without executing, including a final disposition committed immediately before crash; `partly_done` follows its original decision for missing actions and is not hydrated/rerouted by this contract. The normal executor checks snapshot authority, exact-target success, dispatch scope and unresolved send guards. No claim or readiness flag is permission to send. Dry-run and Replay/Backtest make no fetch through this new seam and do not claim or mutate production work. This does not repeal BR-026 AC4's allowance for dry-run read calls elsewhere; Backtest retains BR-027's no-external-calls requirement. Keep corpus/date selection and the existing selected versus decision-bearing distinction. For a marked selected input that cannot be evaluated from local data, don't synthesize a decision/action; retain the count and safe run-local unavailability evidence using existing diagnostic semantics. This does not imply a new per-selected-video history, outcome class or ability to reconstruct inputs absent from the inspection query.

- **Live status (BR-042, amended 2026-11-19):** [Live ARS-001](../../live-status/specifications/ars-001-live-status-read-classification-and-deferral.md) §4–5.
  - **Readiness gate.** The readiness step for native admissions becomes the caller of `liveGatePass`. It runs on the pass's videos left after `withoutRetryVideos` and `withoutUnroutableVideos`, before the start phase of every fresh or resumed pass. Its requirement is `Live` plus the route inputs above.
  - **Retention.** The deferral of a native admission without a current report is `retainNativeTx`. That is one transaction writing the run outcome `deferred` (`metadata_unavailable`, or the existing quota, rate or auth reason) plus exactly one pending video-scoped item (`discovery_processing_pending`, or the matching existing reason). This is the same pairing as above.
  - **Live status.** A recorded `live`, `upcoming` or `unconfirmed` → `liveSkip`. A `Live` requirement is never satisfied by a held Store A row, even when "Current locally usable IDs need no fetch" applies to the route inputs. The only clearance is the run's own `run_video_outcome.live_status = 'none'`.
  - **Prefetch.** The discovery prefetch keeps the returned IDs and records `live_status` with `RecordLiveStatusTx` for the discovering run, in the transaction of `DiscoverVideoTx`. Videos from the legacy `GetVideos` branch, and from refused or failed batches, stay `NULL`.

## 4. Dependencies and compatibility

**What `main` has at `8d7e009`:**
- the `031` receipt functions: `ClaimDeferredGroupReceiptTx`, `ValidateRetryReceiptTx`, `RequeueRetryReceiptTx`, `AcknowledgeRetryDeferralReceiptTx`, `CloseRetryReceiptFinalTx`;
- the run-aware candidate fence (`ListDeferredRetryCandidatesForRun`);
- `GetVideosWithReturnedIDs` with field presence;
- `metadata.Manager.FetchForEvaluation`.

**What it does not have:**
- the ARS-005 §6.2 claimed-batch precondition (`deferRetryForInputsTx`, `requireInputsKey`), so `processRetryVideo` still goes straight to exclusions, duplicate detection and routing;
- any stored dispatch stop.

Source page commits must **not be enabled** before PR-R's guarded receiving readiness path and the §1.2 dispatch stop exist. The Plan gives this order: PR-D depends on `rq-ship`.

- **True BR-037 overlap:** implement or reuse ARS-005 §6's bound fetch, complete returned-ID classification, input guard and receipt dispositions for the discovery work. Coordinate with the PR-R implementation; do not create a discovery-only second fetch or claim loop. Its §5 bounded claiming (variant K) is independently required by amended BR-035 AC14. This spec needs its no-repeat admission invariant, not the whole BR-037 backlog-draining feature. The existing one-pass runner can prove this feature with the same receipt and fence. The historical stranded requeue scanner, preview/confirm, the `requeued_by_user` producer and its UI, and `commitUnavailableTx` are **not** Run 52 prerequisites. This document does not amend them.
- **Already applied, not repeated:** `031`'s one-time Store A default clearing (ARS-005 §6.1.1) and its writer boundary already give this spec's "description and duration are `NULL` until reliably provided". The discovery migration does not clear or rewrite Store A.

### 4.1 Discovery migration content (Plan node `run52-schema`)

**Baseline and number.** The baseline is `main`'s latest migration when PR-D rebases for merge. Today that is `031`; the Plan's phase `migrations` expects `032` (PR-E) and possibly `033` (the PR-R dispatch stop) to come first. The number is `max + 1` at that rebase, under ARS-002 §6's rules: contiguous, no reservation or placeholder, no edit to `001`–`031`, never renumbered once a deployed or shared database has it. It is one forward file with no down file, like `029`–`031`.

**Statements, in this order:**

1. **The dispatch stop, only if PR-R did not ship it:** §1.2's `ALTER TABLE` and trigger, unchanged.
2. **Sealed scope:**

   ```sql
   ALTER TABLE run ADD COLUMN discovery_scope_cutoff TEXT;

   CREATE TABLE discovery_source_scope (
       run_id             INTEGER NOT NULL REFERENCES run(id) ON DELETE CASCADE,
       channel_id         TEXT    NOT NULL CHECK (length(channel_id) = 24 AND substr(channel_id, 1, 2) = 'UC'),
       input_floor        TEXT,
       input_floor_kind   TEXT    CHECK (input_floor_kind IS NULL OR input_floor_kind IN ('modern', 'legacy')),
       settlement         TEXT    NOT NULL DEFAULT 'unsettled'
                                  CHECK (settlement IN ('unsettled', 'complete', 'local_failure', 'no_longer_eligible')),
       has_committed_page INTEGER NOT NULL DEFAULT 0 CHECK (has_committed_page IN (0, 1)),
       completed_cutoff   TEXT,
       settled_at         TEXT,
       PRIMARY KEY (run_id, channel_id),
       CHECK ((input_floor IS NULL) = (input_floor_kind IS NULL)),
       CHECK ((settlement = 'complete') = (completed_cutoff IS NOT NULL)),
       CHECK ((settlement = 'unsettled') = (settled_at IS NULL)),
       CHECK (settlement <> 'complete' OR has_committed_page = 1)
   );
   CREATE INDEX discovery_source_scope_coverage
       ON discovery_source_scope(channel_id, completed_cutoff) WHERE settlement = 'complete';

   CREATE TRIGGER discovery_source_scope_transitions
   BEFORE UPDATE ON discovery_source_scope
   WHEN NEW.run_id <> OLD.run_id OR NEW.channel_id <> OLD.channel_id
     OR NEW.input_floor IS NOT OLD.input_floor OR NEW.input_floor_kind IS NOT OLD.input_floor_kind
     OR NEW.has_committed_page < OLD.has_committed_page
     OR (OLD.settlement <> 'unsettled' AND (NEW.settlement <> OLD.settlement
         OR NEW.completed_cutoff IS NOT OLD.completed_cutoff OR NEW.settled_at IS NOT OLD.settled_at))
   BEGIN
       SELECT RAISE(ABORT, 'invalid discovery_source_scope transition');
   END;
   ```

   ```sql
   CREATE TRIGGER discovery_source_scope_completed_cutoff
   BEFORE UPDATE OF settlement, completed_cutoff ON discovery_source_scope
   WHEN NEW.settlement = 'complete'
    AND NEW.completed_cutoff IS NOT (SELECT discovery_scope_cutoff FROM run WHERE id = NEW.run_id)
   BEGIN
       SELECT RAISE(ABORT, 'completed_cutoff must equal run.discovery_scope_cutoff');
   END;
   ```

   The seal transaction inserts only `unsettled` or `local_failure` rows; no writer inserts a `complete` row. A completed source always committed at least one page (the empty or all-known final page counts). `completed_cutoff` is stored, not joined, so the coverage index can find a channel's latest coverage. Timestamps use the existing UTC run-time format (`formatRunTime`), like `030`. Rows leave only through `run` deletion. No `DELETE` trigger, because that would block the cascade.
3. **Page marker:**

   ```sql
   ALTER TABLE video ADD COLUMN discovery_page_observation INTEGER NOT NULL DEFAULT 0
       CHECK (discovery_page_observation IN (0, 1));
   CREATE TRIGGER video_discovery_page_observation_immutable
   BEFORE UPDATE OF discovery_page_observation ON video
   WHEN NEW.discovery_page_observation <> OLD.discovery_page_observation
   BEGIN
       SELECT RAISE(ABORT, 'video discovery_page_observation is immutable');
   END;
   ```

   Every existing video reads `0`. The `028` final-outcome triggers on `video` are unaffected.
4. **Frozen legacy floor:**

   ```sql
   ALTER TABLE channel ADD COLUMN legacy_discovery_floor TEXT;
   UPDATE channel SET legacy_discovery_floor = last_discovered_at;
   ```

   This runs once; the migration version is the marker. No later writer touches the column. It carries the interim (`030`/`031`) completed-channel watermarks forward without claiming they prove complete coverage.
5. **Saved-work reason.** Rebuild `deferred_item` to add `'discovery_processing_pending'` to the `deferral_reason` CHECK. Follow `026`'s rebuild pattern. Copy every column from the DDL in `sqlite_master` at the baseline, not from historical `026` alone. At `031` that means:
   - the six reasons of `026`;
   - `027`'s `closed_reason` column and its CHECK;
   - the indexes `idx_deferred_item_status`, `deferred_item_retry_owner` and `deferred_item_video_status`;
   - the foreign keys;
   - the `AUTOINCREMENT` high-water mark (the `sqlite_sequence` copy).

   If `032` or `033` changed `deferred_item`, carry those changes too. Preserve every row, ID, timestamp, owner, `retry_run_id` and terminal field.

No statement alters `discovery_source_failure` (ARS-002 §6 rule 5). Transient incidents need no DDL. There is no historical backfill: runs from before this migration have no scope rows and no cutoff, and read as "none" (§1.3).

**Later migrations.** Live status (`b42-migration`) and capacity (`cap-migration`) come after this one in the Plan. If either rebuilds `deferred_item`, it carries `discovery_processing_pending` and everything else in the CHECK at its own baseline.

**Code that ships with it.** Register the reason in queue validation, the safe reader, and the Deferred projection, filter and label ("Saved discovery awaiting processing"). Do not rewrite it as an old reason. Stop older writers before migrating. Once this migration is applied, an older image refuses the database. Rollback means restoring a pre-migration backup with writers stopped ([Upgrade notes](../../../user-docs/upgrade-notes.md)). A binary-only downgrade is not supported.

**Required migration evidence.**
- A clean install.
- An upgrade from a populated database at the then-production schema (`030` or `031`; record which) through every new migration to this one, then a reopen.
- Every old row and ID, owner, age and retry timestamp, `closed_reason`, foreign key, index and trigger is unchanged. That includes the `028` triggers, the Search index, `031` provenance and `030` incidents.
- The `AUTOINCREMENT` high-water mark of `deferred_item` is preserved.
- `legacy_discovery_floor` equals the pre-upgrade `last_discovered_at`.
- Every new CHECK and trigger rejects its forbidden value or transition.

## 5. Authorized inspection contract

Use the existing authorization boundary for Run Detail and Errors. No read here dispatches a provider request. Keep the existing video Error list, command IDs, retry/dismiss handlers and pagination unchanged.

- **`GET /api/runs/{id}` adds nullable `discovery_acquisition`**, the §1.3 derived state. It is read in one SQLite read view with `status`, `discovery_scan` (ARS-002 §4.5, unchanged) and the scope rows.
  - `null` means the state is none: unavailable evidence, not zero coverage. That covers legacy runs, runs from `030`/`031` builds, `queue_retry` runs, and runs that never sealed a scope other than a discovery run stopped before sealing (§1.3).
  - A present object has:
    - `state`: `scope_ready`, `mixed`, `complete`, `no_attempts`, `all_local_failed`, `blocked` or `eligibility_changed_no_coverage`;
    - integer counts `completed_sources` and `locally_failed_sources` (rows `complete` and `local_failure`), and `unexamined_sources` (rows `unsettled` or `no_longer_eligible` with `has_committed_page = 0`), all three `null` when no scope is sealed;
    - nullable `blocked_reason`, which is the `dispatch_stop` token when the state is `blocked` and otherwise `null`.
  - The counts are settled scan results, never video totals. Expose them even when the pass is blocked or interrupted.
  - Clients never infer `all_local_failed` from zero counts.
  - Clients that understand `discovery_acquisition` prefer it to `discovery_scan`. `discovery_scan` keeps its ARS-002 meaning for every run.
  - An unrecognized stored value in any input makes the field `null`, with a server warning.
- `status=completed` with `state=eligibility_changed_no_coverage` means explicit **incomplete coverage after eligibility changed**: no completed or failed scans and no run-wide stop, in the approved only-source case. Never project it as complete coverage, no attempts or no new uploads. There were saved observations, not routed results for them. It is visible only after the handoff and completion commit.
- **Source errors.** The endpoints, item fields, vetting, paging, refusals and routing are [ARS-002](ars-002-durable-source-failure-record.md) §4.1–§4.3, unchanged, and are not restated here. This spec only adds rows to the same table:
  - permanent rows from the `local_failure` settlement transaction (§1.3), using ARS-002 §2's statement;
  - `category='transient'` rows, which only the run-scoped endpoint lists.

  `reason` stays the vetted provider token or `null`. Mandatory producer and read tests show that a known `playlistNotFound` does not become generic or disappear after restart. Neither a later healthy poll nor inspection rewrites these records.

## 6. Required assembled proof

Run through the real manual/scheduled runner and startup recovery with a controlled YouTube provider and real migrated SQLite (close/reopen; kill or inject at the indicated durable boundary). Observe actual provider calls, ledger, run inspection, queue/receipts, video and run-local outcomes, decision and action/guard rows.

| Case | Required observations |
|---|---|
| Page transaction and replay | Kill before commit, after commit before another request, and after final-page commit before phase advance. Include duplicates within/across pages and a known/final ID. Verify all-or-none page data, exact origin, one fingerprint/credit/update for new video, no stale source floor on rollback, no repeated completed source on resume. Test eligible older unseen page and cutoff tie on fresh retry. |
| Mixed coverage and readiness | B returns two pages then 404; A completes with zero new uploads. Reopen before enrichment and later between metadata write and first decision. Real eligible B inputs route under the original saved snapshot, once; B remains incomplete. Give another B video invalid detail or expire/evict its row: it is retained with no fabricated predicate result and a later receiver can recreate metadata and route under its own snapshot. Repeat with no repetition of that ID in uploads. |
| All-local and crash | B commits an intermediate page; B and all other attempted channels fail locally. Kill after B fails while C is still unsettled: resume only C, with no terminal classification before settlement. Then kill after the last source settles, inject fatal transaction failure, and kill after commit before event. Assert no premature foreign claim; exactly one due row per retained observation after commit, no origin decisions/sends for them, durable coverage message, no failed-origin resume, unchanged settled retries. Race Stop/Discard on the supported ownership paths. Prove separate scheduled and Run Now receivers claim the page-produced ID when uploads never return it, and independently observe the next eligible fresh poll of its still-eligible source. |
| Only source becomes ineligible after a committed page | The only source B returns a real eligible video and a next-page token. Kill after page commit, before the next call. Reopen through supported startup recovery; valid current enumeration now excludes B. Verify `has_committed_page=1`, no more B uploads calls, unchanged floor, no origin enrichment/routing/send for the saved video and no source/video failure. Inject rollback before the terminal write and kill after commit before notification: before commit no completion or foreign claim; after reopen exactly one due row, preserved origin `not_yet_evaluated`, and registered Run Detail returns `completed` with `eligibility_changed_no_coverage`, not empty success or failed. If Stop/Discard wins first, verify its existing outcome and handoff instead. A partial enumeration or auth stop must not prove ineligibility. In a fresh eligible scheduled run and separately Run Now, use real receipt processing with B still absent and its video never repeated by uploads; verify the saved ID is claimable and, with usable inputs, routes under the receiver's snapshot without rediscovery or duplicate credit. |
| AC11 case 1: another source completed | Use two sources: A, and S. S commits a page with a real eligible video and a next-page token. Kill before S's next call. On reopen, valid current enumeration excludes S, and A completes on the resume. Verify:<br>• no S uploads call, an unchanged S floor, no S incident, and no failure from the eligibility change;<br>• S's row is `no_longer_eligible` with `has_committed_page = 1`;<br>• A's ready observations route normally, and none of S's observations gets a route decision, action or send in the origin;<br>• the terminal transaction writes exactly one due `discovery_processing_pending` row per S observation, with an origin outcome of `not_yet_evaluated`, before `completed`.<br><br>Inject a rollback of that transaction: no terminal status and no S row. Kill after commit: on reopen, Run Detail shows `completed` with `mixed`. A later scheduled run and Run Now claim S's work without polling S. |
| AC11 case 2: others failed locally | S as in case 1. Every other source fails locally, one of them after saving a page. Verify:<br>• the all-local recheck accepts S as neither completed nor failed;<br>• in one transaction: S's `discovery_processing_pending` rows; the pending rows for the other sources' saved observations; status `failed`; and `no_source_scanned`;<br>• no observation is routed;<br>• Run Detail shows `failed` with `all_local_failed`.<br><br>Inject a rollback: no `failed` status and no new pending row. Kill after commit, then restart: S's rows survive, with one due row per observation, and no failed-origin resume. This is the derived "before any final outcome, including Failed" proof. |
| AC11 case 3: two ineligible sources | S1 and S2 each commit a page, then both are ineligible on recovery. Verify no uploads calls. One transaction writes due rows for both sources' observations, `completed`, and the scan outcome. Run Detail shows `completed` with `eligibility_changed_no_coverage`. Contrast: one of the two never committed a page. It is unexamined, and the state is still `eligibility_changed_no_coverage` because the other has a page. Contrast: a partial enumeration does not prove ineligibility. Combine with a rate-limit suspension of a third source: S's observations get `discovery_processing_pending`, and the others get the `api_rate_limited` pairing, before the terminal status. |
| Receiving claim and release | Crash after claim before metadata/first decision, mid-prefix followed by a contradictory metadata reload, and after durable final result before ack. Same receiving run recovers its exact receipt and snapshot; do not splice the contradictory prefix, and ack does not re-fetch/re-send terminal work. Inject transient, unusable, actual complete omission, proactive/provider quota and auth refusal: check exact receipt disposition, FIFO/age and no repeat claim in the same run, no release after ownership loss and no false unavailable/final result. Contrast `requeued_by_user` and its unchanged absence outcome on the integrated BR-037 path. Race authorized Stop/Discard and retention/claim; prove one eligible owner or explicit removal, never an orphan or duplicate. |
| Guard, dispatch and simulation controls | Seed known final, partly done and guarded pending controls through real paths; no re-admission, downgrade, new-configuration continuation, or uncertain resend. Prove admitted provider quota with positive estimate prevents subsequent uploads, metadata and send calls after reopen; pre-admission quota emits no row. Contrast failed renewal, recovered same-identity read, and compound accounting+quota/auth failures with separately retained facts. Assert authorized local-only variant K and no ordinary fallback with released receipts. Run dry-run, Replay and Backtest over ready/unready observations: no calls through this seam, no production writes or claims, no fabricated results, unchanged date selection; Backtest still makes zero external calls. |
| Upgrade and global contrasts | Upgrade a populated database at the then-production schema (`030` or `031`) through every new migration (§4.1 evidence). Include a run left `running` by a `030`/`031` build with a permanent incident row: after upgrade its resume seals a scope with that source pre-settled `local_failure` and makes no uploads request for it. Compare all old queue columns/IDs/owners/sequence and final histories; new reason round-trips through authenticated Deferred readers. With no complete source contrast all-local failure against successful empty sources, no attempted sources, interruption, provider quota and auth; those alternatives must not acquire the all-local label or produce spurious source Errors or route outcomes. |
| Dispatch stop | For each value, show that after the write and after a reopen no run-owned request is made: no uploads, `videos.list`, send or renewal for a new protected request. `authorization` still allows local-only claims and `authorization_pending` settlement. `quota` and `accounting_or_integrity` stop claims. Raise `authorization` to `quota`. Show the trigger refuses lowering or clearing, including through Run Now resume and Stop. Kill after an admitted provider quota attempt and before the stop write: recovery writes `quota` from ledger evidence before any request. Inject a stop-write failure: the process makes no further request and fails closed. A stop during the scan leaves `discovery_scan_outcome='cut_short'` and unsettled rows, and Run Detail reads `blocked` with that `blocked_reason`. A `run_now` or `schedule` run stopped before sealing (no cutoff, `NULL` scan outcome) reads `blocked`. A `queue_retry` run with a stop reads `null`, and so does a run from a `030`/`031` build with a stored scan outcome that gets a stop after the upgrade. A claim under `quota` and a `ClaimPlaylistSend` under any stop are refused inside their writer transactions, even when the execution's copy is stale. A preflight store error writes `accounting_or_integrity`, not `quota`. After a same-run resume following a quota reset, the run still makes no request. A stop after the scan settled leaves the derived state unchanged. Simulations read `NULL`. A fresh run starts `NULL`. |
| Scan outcome and derived state | For every §1.3 row, compare the stored `discovery_scan_outcome`, the derived `discovery_acquisition` and ARS-002's `discovery_scan`. Cover: seal with an empty scope (`completed`, `no_attempts`); budget break (stored `quota`, `cut_short`, `blocked`); rate-limit suspension on one source (row `unsettled`, no incident, `cut_short`, `scope_ready`); `completed_cutoff` equals the seal cutoff, and a `complete` row with `has_committed_page = 0` is refused; last-row settlement (`completed`); AC8 (`no_source_scanned` in the same `UPDATE` as `failed`); AC10; Stop winning over AC8 (`stopped`, `completed`, `scope_ready`); a 5xx and a live-context timeout as transient local failures (incident `transient`, source skipped for the run, polled again by the next fresh run), contrasted with run cancellation, which is not a source failure. Show no second stored assessment exists. |
| Rate limit then terminal | A completes after committing pages; B commits one page, then returns 429; C is still `unsettled`. Verify: no further uploads request for B or C in that execution; no in-place wait and retry; no incident, settlement, coverage advance or dispatch stop; other phases still run under their own guards; no marked observation from A or B is routed or sent. Let the run reach its terminal result. That transaction gives every unfinished marked observation an origin `deferred`/`api_rate_limited` outcome and exactly one pending `api_rate_limited` `deferred_item`, then the status. The scan outcome is `cut_short`, the derived state is `scope_ready`, and there is no `discovery_processing_pending` row. Inject a rollback: no terminal status and no foreign eligibility. Kill after commit: on reopen, exactly one due row per observation. A separate fresh scheduled run and Run Now each claim that work through receipts without the IDs reappearing in uploads, and poll B and C again. Contrast: a crash before the terminal transaction gives an interrupted run whose resume rescans B and C from page one and routes normally after settlement. Stop committing first gives its own handoff. A quota stop gives `blocked`, not this row. |
| Envelope, expiry and inspection | Treat `{}`, `null`, `items:null`, duplicate/unrequested ID and truncated/paginated candidates as incomplete/invalid; only valid `items:[]` proves absence for all requested IDs. Verify malformed success cannot create a video error by omission or a BR-037 final result. Force readiness loss between preflight and evaluation; another ready origin video still routes. After restart, compare registered authorized Errors and Run Detail projections (including `404` and `playlistNotFound` for real page-derived work), no source commands and no raw disclosure, including invalid reason fixtures. The designer's shipped-browser check of the same journey is separate assembled experience proof. |

## 7. Open decisions

1. **Resolved 2026-11-22: eligibility changes in a multi-source scope.** The user accepted the recommendation (Loom `b2f00946`, answer `2b70486b`). It is recorded as BR-002 AC11 in `44f3ba4` and realized in §1.3 and §2 (*Source ineligible after a saved page*, *Terminal handoff rule*), with §6's three AC11 rows. The rule that the handoff is durable before any final outcome, including Failed, is derived from AC3 and AC10–11; it is not a separate product choice.
2. **Dispatch-stop migration owner (Plan).** This depends on whether `rq-stop-arch` confirms PR-R ships §1.2's DDL. If it does not, `run52-schema` carries it (§4.1 statement 1). The shape is fixed either way.

No test above has been executed in this architecture assignment. Independent review `ses_e0fe622cbffeON13GhZqYatir6` findings 1–4 and API-side 5 motivated these corrections. Implementation and assembled proof are prerequisites for enabling the writer. A new simulation history/list contract and a crash-complete quota ledger are not required by these findings; revisit only with accepted authority or evidence the existing contracts cannot meet the scoped guarantees.
