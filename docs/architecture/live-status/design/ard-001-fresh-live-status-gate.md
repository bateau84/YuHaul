---
type: design
title: ARD-001 — Fresh Live-Status Gate Before Exclusions, Routing and Sends
description: Every production video gets a live-status check from the run's own videos.list answer before exclusions, duplicate detection, routing, capacity checks and playlist adds. The run records the result on its own run-video row, so resumes and sends never depend on expiring metadata. Anything not confirmed as neither live nor upcoming is deferred on one active queue item and checked again on the next run.
tags: [architecture, design, live, upcoming, deferral, deferred-queue, metadata, routing, send-guard]
timestamp: 2026-11-19T00:00:00Z
---

**Status:** proposed technical realization of accepted BR-042 and the amended OC-002 Guarantee 5. Revised 2026-11-19 after critic `ses_e0ca5c4cdffeU13eZuktRfYwvH` (CHANGES REQUIRED; see *Review disposition*). AC7 (time limit) is designed for its default (no limit) and leaves room for a later cap. Accepted by the critic after the focused recheck, with the ARS-001 precision fixes for §4, §5.1 and §6.2 applied. Not implemented. The ARS-001 §10 amendments were applied on 2026-11-19 (Plan r29 node `b42-amend`). Those to ARS-005, ARS-006, authorization ARS-001 and safe-diagnostic ARS-001 are committed. Those to capacity ARD-001 and Discovery ARS-001 sit in their still-untracked documents, for those documents' owners to commit.

## Summary

**What the system will do.** Before a production run evaluates, routes or adds a video, the run gets that video's live status from a by-ID `videos.list` answer it made itself. That call already returns `snippet.liveBroadcastContent`. The run also requests `liveStreamingDetails`, which costs no extra quota. The run writes the result on its own row for that video (`run_video_outcome.live_status`) once, and never changes it during the run.
- If the result is *live*, *upcoming* or *unconfirmed*, the video is deferred with the reason *live or upcoming on YouTube*. It keeps one active queue item, and the next run checks it again.
- If the result is `none`, which covers ordinary uploads and the recording of a stream that has ended, the video is processed like any other.

Nothing else can permit evaluation or an add. That includes a status from another run, Store A metadata, or a duration.

**Why this design.** The rule sits in one gate. Its result is stored where the run already keeps per-video facts, so it does not expire, cannot be evicted, and needs no change to any Store A writer. The gate also runs whenever a run resumes, whatever phase it resumes at. A check at the evaluation entry points and in the playlist send-guard writer catches any path that skips the gate. Its refusal always ends in a non-final live deferral, never in `failed` or an authority failure.

**Normal example.** Run 60 discovers L (live) and P (a premiere on Friday). Discovery's by-ID prefetch classifies L as `live` and P as `upcoming`. `DiscoverVideoTx` writes those values on run 60's rows in the same transaction. The main-loop gate sees that the rows are already filled, so it makes no second call. It commits `deferred / live_now` and `deferred / live_upcoming`, and creates one pending queue item each (`deferral_reason = live_or_upcoming`, with `live_hold` set).

Run 61 claims both items. Its rows for L and P are empty, so the gate makes one live-only read. L now reports `none` with `liveStreamingDetails.actualEndTime`. Run 61's row for L gets `live_status = none`, and L proceeds to exclusions, duplicates, routes and the add. P is still `upcoming`, so it is deferred again on the same item.

If run 61 is interrupted after routing and resumed five weeks later, its rows still say `none` for L. The resume needs no read, and the send fence passes.

**Important limits.**
- YouTube does not document what premieres report, what ended streams report, or the duration while a video is live. The classification fails closed, so an undocumented shape causes a skip, never a route. If YouTube ever omits `actualEndTime` for a finished broadcast, that video is skipped on every run until the user dismisses it. Under AC7's default, nothing ends that automatically.
- Simulations make no new provider call. They use the most recent production live status.
- The claimed-work paths require [run-initiation ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) §6.1–6.2, which is **not built** (see *Prerequisites*).
- Earlier adds are never undone (AC12).

## Derived from

- [BR-042](../../../requirements/yuhaul/br-042-skip-live-and-upcoming-videos-until-ended.md) and the 2026-11-19 live case in [OC-002](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md) Guarantee 5.
- Researcher findings, session `ses_e0cb38c9effeuoJ0R85AY1zKbq`, relayed in the assignment:
  - `snippet.liveBroadcastContent` is in the `snippet` part already requested;
  - `liveStreamingDetails` adds no quota cost;
  - premieres, ended streams and duration while live are undocumented;
  - no YuHaul read path parses either field.
- The [YouTube `videos` resource reference](https://developers.google.com/youtube/v3/docs/videos), read this session and quoted again by the critic:
  - `liveBroadcastContent` is `live`, `none` or `upcoming`;
  - `liveStreamingDetails` is present only for an upcoming, live or completed broadcast;
  - `actualEndTime` "will not be available until the broadcast is over";
  - `activeLiveChatId` "is filled only if the video is a currently live broadcast";
  - `concurrentViewers` is not tracked after the end.
- Code at the working tree, read this session:
  - `internal/metadata/manager.go::FetchForEvaluation`, `internal/youtube/client.go::GetVideosWithReturnedIDs` (`part=snippet,contentDetails`, no live parsing) and `cmd/server/main.go::FetchMetadataBound`;
  - `internal/state/machine.go::DiscoverVideoTx` writes this run's `not_yet_evaluated` row;
  - `internal/queue/retry.go` (≈ line 406) inserts the receiving run's row at claim;
  - `internal/state/store.go::RecordRunVideoOutcomeTx` updates only `final_outcome`, `deferred_reason`, `error_message`, `completed_at` and `updated_at` on conflict;
  - `internal/pipeline/pipeline.go` (≈ 405–533): resume starts at the stored phase, and `RunLifecycle`/`ExpireStale` runs first;
  - `internal/pipeline/executor.go` (≈ 511–561, 749–757): guarded post-evaluation deferral exists, and a `ClaimPlaylistSend` error becomes `ActionOutcomeFailed`.
- Critic `ses_e0ca5c4cdffeU13eZuktRfYwvH`, evidence cited below.

## Satisfies

| BR-042 / OC-002 | Satisfier | Confirmation (ARS-001 §9) |
|---|---|---|
| AC1: fresh status on every path; stored status never permits | The **live gate**, run on every pass and every resume. **Run-scoped clearance**: this run's `run_video_outcome.live_status = 'none'`. **Backstops** at the evaluation entries and in `ClaimPlaylistSend`. | L1, L4–L6, L14, L16 |
| AC1: duration is not evidence | The classifier never reads duration. | L3 |
| AC2: skip unless confirmed; three sub-kinds | The fail-closed classifier; `liveSkip`; run reasons `live_now`, `live_upcoming`, `live_unconfirmed`. | L2, L3 |
| AC3: no current report | Each non-returned read status keeps its existing disposition. For new discoveries before Discovery ARS-001, the gate's native retention commits an atomic deferral with a pending item. | L4, L17 |
| AC4: confirmed is normal | After clearance the existing path runs unchanged. | L1 |
| AC5: one item; next run; at most once per run | `liveSkip` reuses or creates exactly one active video-scoped item, due immediately. `live_status` is written once per run row and never re-read. | L1, L7, L11 |
| AC6: never final; not a failure | Only `deferred` is written, including on fence and backstop refusals. | L2, L14, L16 |
| AC7: no time limit (default) | No age check. A single cap seam in `liveSkip`. | L8 |
| AC8: not returned | `absent` keeps its existing disposition and is checked before the live check. | L9 |
| AC9: before exclusions and duplicates | The gate precedes the start phase of every pass; backstops in `videoContextForExclusion`, `detectVideos`, `routeVideos`. | L5 |
| AC10: routes of the processing run | Unchanged existing behavior. | L1 |
| AC11: final and partly done | BR-039 classification first; final videos are never read; a live-only gate before `continueOriginalDecision`. | L10 |
| AC12: nothing undone | The gate only withholds future work. | L10 |
| AC13: capacity | The gate runs before any capacity count GET; clearance is checked at admission; capacity rows are released unchanged. | L11 |
| AC14: handoff, re-queue | `live_hold` survives handoff. An item with a live hold is active, so the video is not stranded. | L12 |
| AC15: distinct reason; no raw provider data | Registered enums only. | L13 |
| AC16: simulations | Local status from the latest production run row. No call and no write. | L15 |

## Problem

Nothing in YuHaul knows whether a video is live:
- the discovery listing has no live status;
- the by-ID read ignores `liveBroadcastContent` and does not request `liveStreamingDetails`;
- a stored duration of `0` is ambiguous.

BR-042 needs a status that comes from the routing run itself. It must be obtained before exclusions on every production entry, including resumes that start after routing. It must fail closed. A skipped video must keep exactly one active deferred item, without changing the BR-037 and BR-039 meanings that existing items carry.

## Prerequisites and build order

- **P1. Claimed paths require ARS-005 §6.1–6.2 (hard prerequisite).** This covers deferred claims, Errors retries, Stop/Discard handoffs, re-queues, and partly-done and capacity retries.
  - `FetchForEvaluation` has no production caller today, only tests.
  - `processRetryVideo` goes straight from BR-039 classification to `runExclusionGate` (`retry.go` ≈ 219–231).
  - The ARS-005 §6.2 claimed-batch precondition (`deferRetryForInputsTx`, the receipt dispositions, the dispatch-stop handling) does not exist in code.

  The live gate is an **addition to** that precondition, not a reuse of something built. BR-042 must not be released on claimed paths before §6.2 ships. That also pulls in its migration prerequisites: the receipt-provenance migration and the §6.1.1 clearing. A claimed path without §6.2 has no AC3 disposition, and its backstops would release the same item on every run without ever processing it.
- **P2. New discoveries do not wait for Discovery ARS-001.** The main-loop gate defines its own native retention (ARS-001 §5.2). It uses exactly the reasons and pairing that [Discovery ARS-001](../../discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) defines, so the two converge on one writer when Discovery readiness lands. In this case the main-loop gate requests **`Live` only**. Ordinary routing inputs and their `NULL` behavior (ARS-005 §6.1.1) do not change before Discovery lands.
- **P3. Capacity.** If capacity (ARD-001) ships first or together with this, its count-GET and admission steps call the gate and the clearance check as described here. If it ships later, it must include them.

## Constraints and forces

- **No new read seam.** The by-ID `videos.list` costs one unit per 50 IDs, whichever parts are requested (researcher).
- **Freshness is per run.** A report from earlier in the same run counts. A report from another run never does (BR-042 *Specification choices*).
- **Fail closed. Unknown is never negative** (AC2, AC3, AC6).
- **Existing queue meaning.** ARS-005 §6.2 keeps an item's original `deferral_reason`. `requeued_by_user` changes the `absent` disposition (BR-037).
- **Retention.** Store A holds provider content for at most 30 days. Run rows hold permanent decision facts (deferred reasons, outcomes).
- **Migrations.** Search `029` is committed. The receipt-provenance and Discovery migrations follow it. Capacity has an unnumbered `deferred_item` rebuild. No number is assigned here.

## Invariants and guarantees

| # | Guarantee | Enforced by | Falsified by |
|---|---|---|---|
| I1 | No production exclusion, duplicate detection or routing of a video unless this run's `run_video_outcome.live_status` for it is `none`. | The gate; backstops in the three evaluation entries. | L5, L14 |
| I2 | No playlist add (ordinary, continuation or capacity) unless I1's clearance holds for the **sending** run. Checked in the guard writer and again at POST admission. | `ClaimPlaylistSend`; the admission fence. | L10, L11, L14, L16 |
| I3 | `live_status` on a run row is written only from a by-ID answer obtained by that production run. It is written at most once, and is never cleared or overwritten during the run. | The `WHERE live_status IS NULL` writer; no other writer lists the column. | L6, L7 |
| I4 | A live skip, or a refusal by a fence or backstop, writes only a non-final `deferred` with a live reason, plus queue state. It writes no decision, audit, duplicate evidence, success, `failed`, Errors entry or final outcome. | `liveSkip` and `liveDeferAfterEvaluation`. | L2, L5, L14, L16 |
| I5 | After a live skip, the video has exactly one active **video-scoped** deferred item, unless the user removed it while it was claimed. Capacity rows keep their identity and age. | The reuse-or-create writer, with a video-scoped uniqueness guard. | L7, L11 |
| I6 | Without a current report, the video keeps its existing AC3 disposition, or for a new discovery the native retention. In every case the result is a deferred outcome plus an active item, never "no outcome" and never `failed`. | Gate precedence (ARS-001 §5). | L4, L17 |
| I7 | Every pass of a production run, fresh or resumed, applies the gate to its unfinished member videos before the work of its start phase. | `liveGatePass` at pass entry (ARS-001 §5.1). | L16 |

## Options considered

**Where the per-run clearance is kept.**

- **A. A Store A stamp (`transient_metadata.live_*` with the run id).** This was the first revision.
  - *Fails on resume:* every start runs `ExpireStale`, which clears the stamp. A resume that starts at duplicate detection, routing or the action phase never reaches a gate placed before exclusion. The fence then refuses through a path that commits `failed` (critic MAJOR 1).
  - *Costs:* changes to six Store A writers and to `Expire`.
  - It can be patched (re-read on every resume), but each Store A writer remains a place where the rule can break.
- **B. The run's own `run_video_outcome` row (chosen; the critic's alternative).**
  - The row already exists before the read: `DiscoverVideoTx` writes it for new discoveries, and the claim inserts it for the receiving run.
  - It does not expire, it is scoped to exactly one run, and no Store A writer changes.
  - Its status survives longer than 30 days for simulations. That is a decision fact like `deferred_reason`, not provider content.
  - *Cost:* a crash between the read and the row update repeats the read once in that run. That is accepted (*Failure and recovery*).
  - *Failure modes:* a missing row is a local fault; the read result is then not used and the video is retained.
- **C. Keep the answer in memory only.** Rejected. A resume loses it, and the fence has no durable fact to check.
- **D. A separate table `run_live_check`.** Rejected. It duplicates B's row identity and adds a table, with no extra benefit.

**How the queue item records the live reason.**

- **R1 (kept).** Keep the item's origin `deferral_reason`. Add a nullable `live_hold` with the sub-kind. A new item gets the origin reason `live_or_upcoming`. This preserves `requeued_by_user` for BR-037 (critic: sound).
- **R2 and R3** (overwrite the reason, or use three queue reasons) are rejected because they erase `requeued_by_user`.

**New discoveries without Discovery ARS-001 (critic MAJOR 2).**

- **N1. Make Discovery readiness a hard prerequisite.** This delays BR-042 behind a large proposed contract.
- **N2. Native retention defined here, using Discovery ARS-001's own reason pairing (chosen).** It is small: one transaction per video. It never leaves a video without an outcome, and it converges with Discovery's writer later.

For claimed paths no equivalent small option exists. Receipt validation, acknowledgement and dispatch-stop precedence are ARS-005 §6.2 itself, so P1 makes it a prerequisite instead.

## Decision

Option **B** with **R1** and **N2**, with prerequisite P1.

## Structure and flows

### Components

| Component | Change | Why |
|---|---|---|
| `youtube.Client.GetVideosWithReturnedIDs` | Adds `liveStreamingDetails` to `part`; returns `Live` for every returned ID. | The by-ID read used by discovery prefetch and `FetchMetadataBound`. |
| `metadata.FetchResult` / `FetchForEvaluation` | A `Live` requirement that no stored row satisfies, so it always reads. Returns `Live`. Store A is unchanged. | One read rule; no persistence in the metadata layer. |
| `state` | `run_video_outcome.live_status` and `live_checked_at`; a write-once recorder; the clearance helper. | Durable, non-expiring, run-scoped evidence. |
| `discovery.Engine.persist` / `DiscoverVideoTx` | Writes the prefetch classification on this run's row in the same transaction. | Avoids a second read for new videos. |
| `pipeline` gate (`liveGatePass`, `liveGateClaimed`) | Pass entry (fresh and resume, any phase) and the ARS-005 §6.2 precondition. | I1, I6, I7. |
| `pipeline` native retention | Atomic deferred outcome plus pending item for no-report cases of new discoveries. | AC3 without Discovery ARS-001 (P2). |
| `pipeline.liveSkip` / `liveDeferAfterEvaluation` | The only writers of live reasons. | AC2, AC5, AC6; the cap seam. |
| Backstops; `ClaimPlaylistSend`; the admission fence | Check the clearance. Return the typed `ErrLiveNotCleared`, which callers map to a live deferral. | I1, I2, I4. |
| `queue` | `live_hold`; the `live_or_upcoming` reason; a video-scoped uniqueness guard; the active-only filter. | AC5, AC14, AC15. |
| Inspection, statistics, SPA, diagnostic registry | New registered values. | AC6, AC15. |

### Gate order for one video in a pass

```text
BR-039 classify ──final──▶ existing close (no read)
      │ open / partly_done
      ▼
this run's live_status already set? ──yes──▶ use it (no read)
      │ no
      ▼
run dispatch stop? ──yes──▶ existing stop disposition (AC3)
      ▼
FetchForEvaluation(Live, + route inputs only where §6.2 / Discovery readiness owns them)
      ▼
record live_status for every ID that has a Live entry (write once), then:
  absent ................................▶ existing absent rule (AC8)
  quota/auth/rate/failed, envelope unusable ▶ AC3: existing claimed rule, or native retention
  live ≠ none ..........................▶ liveSkip (before evaluation) or liveDeferAfterEvaluation
  live = none ..........................▶ continue the pass at its start phase
```

### Entry paths (AC1)

| Path | Gate |
|---|---|
| New discovery, fresh pass | `liveGatePass` before exclusion, Live only (P2). Usually satisfied by the prefetch. |
| **Any resume** (Run Now resume, startup recovery, re-entry after an interruption), at exclusion, duplicate detection, routing or the action phase | `liveGatePass` over the pass's unfinished member videos before the start phase runs. Rows written before the interruption are reused. Only rows still `NULL`, such as runs in flight when the migration ran, are read. |
| Deferred claim, Errors retry, Stop/Discard handoff, re-queue (and a resumed receipt) | `liveGateClaimed` inside the ARS-005 §6.2 precondition (P1). |
| Partly-done continuation | `liveGateClaimed`, live only, before `continueOriginalDecision`. |
| Capacity retry | `liveGateClaimed`, live only, before the first count GET. |
| Version-0 continuation | Unreachable in production; see below. |

### Version-0 continuation is unreachable (recorded, not open)

Critic evidence (`ses_e0ca5c4cdffeU13eZuktRfYwvH`):
- The scheduler creates only version-1 runs (`pipeline.go:227`, `:312`).
- Run Now resume requires `action_authority_version = 1` (`run_now.go:226`).
- Manual retries create version-1 runs (`retry.go:44`).
- Startup recovery skips `mutation_safety_version = 0` rows and blocks on any other version-0 row instead of resuming it (`startup_recovery.go:105`, `:136–138`).
- `ClaimPlaylistSend` refuses every add for a version-0 run (`playlist_guard.go:149`).
- Only tests run version-0 continuation (`continuation_integration_test.go:305`, `legacy_causal_recovery_test.go:27`).

So BR-042 needs no version-0 disposition. The backstops still refuse evaluation for such a run, because it has no clearance. **Revisit trigger:** any change that lets a production entry continue a version-0 run must first define its AC3 retention.

### Data ownership

- **`run_video_outcome.live_status` and `live_checked_at`** belong to the run. They are written once, by the gate or by discovery persist, and are never cleared. They are the only clearance.
- **`deferred_item.live_hold`** belongs to the queue. A live skip sets it. The receiving run clears it on confirmation, in the transaction that moves the video to `evaluating` or starts continuation. No other transition changes it.
- **Store A** is unchanged.

## Input / operation coverage

| Provider answer for a returned ID | Classification |
|---|---|
| `liveBroadcastContent = "live"` / `"upcoming"` | `live` / `upcoming` |
| `"none"`, no `liveStreamingDetails` | `none` |
| `"none"`, `liveStreamingDetails` with a valid `actualEndTime`, no `activeLiveChatId`, no `concurrentViewers`, and `actualStartTime` absent or valid and not after the end | `none` (ended) |
| `"none"` with any other `liveStreamingDetails` content (only scheduled times, a start without an end, a chat ID, viewers, an invalid time, `{}`) | `unconfirmed` |
| Missing, null, non-string, duplicate or unknown `liveBroadcastContent`; unreadable or duplicate `snippet`; duplicate or non-object `liveStreamingDetails` | `unconfirmed` |
| Duration (any value) | Not consulted |
| Returned, but `publishedAt` is unusable | Classified from the item anyway |
| Not returned in a complete response | `absent` (AC8) |
| Invalid envelope, refusal, transport failure | No classification (AC3) |

## Failure and recovery

- **Crash after the read, before `live_status` is recorded.** The resumed pass finds the row `NULL` and reads once more. This is the only double read in a run. It is accepted, because no decision used the first answer.
- **Crash after `live_status` is recorded, before the skip or the evaluation.** The resumed pass uses the recorded value without a read: a skip for non-`none`, otherwise it continues.
- **Resume weeks later, any phase** (the critic's MAJOR 1 scenario). The run rows still hold `none`. The resume gate makes no read, and the fence passes. Store A expiry is irrelevant. Inputs needed by the start phase keep their existing rules.
- **A run that was in flight when the migration ran.** Its rows are `NULL`. The resume gate makes one live-only read per unfinished video and records it.
  - For a video still before evaluation, a non-`none` result goes through `liveSkip`.
  - For a video already evaluated in this run with an unsent add, a non-`none` result goes through `liveDeferAfterEvaluation`: the existing post-evaluation guarded deferral (`retainGuardedDeferralTx` and its handoff) with a live reason. No add is sent, and the recorded decision is kept. Later handling follows that path's existing rules.
  - If the read cannot be made (AC3), the existing post-evaluation quota, auth or rate deferral applies, or native retention for an unevaluated new video.
- **Fence or backstop refusal.** Either a path skipped the gate, or the row has no `none`. The result is `liveDeferAfterEvaluation` (at the fence) or `liveSkip` (at a backstop), with `live_unconfirmed` and the fixed diagnostic `routing / check_live_status / live_status_not_current`. It is never an authority failure, `ActionOutcomeFailed`, `FinalOutcomeFailed` or an Errors entry. It is a defect signal that still leaves the video deferred with an active item.
- **The `live_status` write fails.** This is a local persistence fault. The pass does not use the read. For claimed work: ARS-005 §6.2 local-fault handling; for native work: the native retention transaction is attempted. If that also fails, existing retain-before-fail recovery applies.
- **The skip or retention transaction fails.** It rolls back, and the receipt or membership remains recoverable. On resume the recorded `live_status` is reused.

## Security / trust boundaries

No new credential, endpoint or authority. Only an enum and a timestamp are stored. No raw `liveStreamingDetails`, times, chat IDs or viewer counts are stored or logged (AC15; safe-diagnostic ARS-001).

## Verification and composition

ARS-001 §9 (L1–L17). The decisive proofs use the real server path, migrated SQLite and a request-counting controlled provider:
- two runs with a restart in between;
- a stale status from another run;
- every ambiguity shape;
- a resume after Store A expiry at the routing and action phases, with zero reads and no `failed` result;
- a fence refusal that ends deferred;
- a new discovery with a failed read that ends deferred with a pending item;
- partly-done and capacity cases;
- Stop and re-queue;
- three simulation modes with zero requests.

## Consequences

- **Quota.**
  - Each **claimed** open or partly-done item now costs a by-ID read on every run that claims it: ⌈N/50⌉ units for N claimed videos, shared with any other by-ID read in that pass. This applies even when its route inputs are already current in Store A. Before this change, ARS-005 §6.1 step 1 made such an item free.
  - A live-held video therefore costs one read per run for as long as it is held.
  - New discoveries cost nothing extra when the discovery prefetch succeeded.
  - Runs in flight at migration cost one live-only read per unfinished video, once.
- **AC7 cap later.** Every live deferral goes through `liveSkip` or `liveDeferAfterEvaluation`. A cap would be one check there, computed from this video's existing production run rows (the earliest run of the current unbroken series of live reasons). No column is needed now. What a capped video becomes is a product decision. **Revisit trigger:** the user chooses a cap.
- **Discovery convergence.** When Discovery ARS-001 readiness ships, the native retention writer becomes its writer (same reasons). The gate then also requests route inputs there.
- **Coupling.** Capacity, BR-039 continuation and ARS-005 §6.2 each call the same gate. Their rules are otherwise unchanged.
- **Migration** (ARS-001 §8): `run_video_outcome` gains two columns. `deferred_item` is rebuilt once. Store A is untouched.

## Review disposition (critic `ses_e0ca5c4cdffeU13eZuktRfYwvH`)

| Finding | Disposition |
|---|---|
| MAJOR 1: a lost stamp on resume leads to `failed` | **Accepted.** Clearance moved to `run_video_outcome` (option B), so it cannot expire. The gate runs at every pass entry, whatever the phase (I7). Fence and backstop refusals end in a live deferral (I4). |
| MAJOR 2: unbuilt dependencies; new discoveries could be stranded | **Accepted.** ARS-005 §6.1–6.2 is a hard prerequisite for claimed paths (P1), and it is no longer called "reused". New discoveries get native retention: an atomic deferral plus a pending item (P2). |
| Capacity contradiction | **Accepted.** `liveSkip` always leaves one active video-scoped item, even when the receipt owns only capacity rows. The uniqueness guard is limited to video-scoped rows. |
| Quota cost understated | **Accepted.** Stated in *Consequences*. |
| Queue filter and label | **Accepted.** The filter covers active rows only. The label after the hold clears goes to the Designer. |
| Gate inputs for new discoveries | **Accepted.** Live only before Discovery lands (P2). |
| Version-0 continuation | **Accepted.** Recorded as unreachable, with the evidence above. |

## Open questions

1. **AC5 counting (requirements owner; not blocking).** The design guarantees exactly one active **video-scoped** item. A video with BR-041 capacity rows also keeps those target-scoped rows (AC13), so it can have more than one active row. This arises only for capacity rows created before BR-042. Please confirm that AC5 counts video-scoped items.
2. **Ended broadcast without `actualEndTime` (accepted provider risk).** The video would be skipped indefinitely as `live_unconfirmed` under AC7's default. It remains visible, and the user can dismiss it. Revisit with provider evidence or the AC7 choice.
3. **Queue label after confirmation (Designer).** Once a hold clears, an item created by a live skip still carries the origin reason `live_or_upcoming`, for example if a later quota deferral reuses it. How that is shown is a design decision. The `reason=live_or_upcoming` filter selects active held rows only.
4. **Cross-document amendments (integration).** These are applied on acceptance; ARS-001 §10 lists them.
