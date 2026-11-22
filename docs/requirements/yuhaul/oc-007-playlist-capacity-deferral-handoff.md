---
type: obligation-contract
title: OC-007 — Playlist Capacity Deferral Handoff
description: Shared evidence, ownership and guarded resolution for capacity checks, refusals, retained adds and eviction.
tags: [obligation-contract, playlist, capacity, retry, send-guard]
timestamp: 2026-11-22T00:00:00Z
---

**Status:** accepted  
**Priority:** MUST

## Participants

- The production caller and the provider-facing capacity and mutation authority.
- The owner of waiting work and the receiving run that claims or resolves it.
- Run/queue evidence and diagnostic consumers.

## Preconditions and semantic inputs

A production decision requires a missing add for a known video and an exact actual YouTube playlist. The participants must distinguish decision authority, the executing run, any current retry ownership, the actual target's capacity evidence, whether this invocation sent the add, and what is known about application. Provider behavior constrains the available evidence; neither this contract nor an interface field makes that evidence conclusive.

## Guarantees

1. **Known full before sending:** capacity evidence for the exact target prevents sending, not an attempted provider failure. An independently unresolved guard still vetoes dispatch; this check cannot resolve it.
2. **Definite provider refusal after sending:** an actually admitted add may be treated as deferred for capacity only when the accepted guarded-send contract recognizes evidence from that invocation that establishes both fullness and non-application of that exact add. This is one real attempt, under existing attempt/quota accounting. The current owned guard may be resolved only with a committed, truthful capacity disposition and valid retained-work handoff, under the existing guard-resolution ownership and run-lifecycle rules, except as stated next. No immediate replay is authorized.
   - **Refusal after eviction or dismissal** *(amended 2026-11-22 by user decision)*: if, when such a refusal is resolved, that add's waiting entry has already been evicted or dismissed under [BR-041](br-041-bound-playlist-full-waiting-work.md), no retained-work handoff is required. YuHaul SHALL then record the add as not applied ("not added": YouTube refused it because the playlist was full) and resolve the current owned guard with that committed result, under the same ownership and run-lifecycle rules. It SHALL NOT create or restore a waiting entry, admit a replacement, evict another entry or retry the add; the evicted or dismissed add stays suppressed. Run Detail SHALL show the add as attempted and not added, not as waiting or "application unknown". This exception does not lower the evidence required above: anything short of a definite refusal of that exact add falls under Guarantee 3. The video's outcome still follows [BR-041](br-041-bound-playlist-full-waiting-work.md) AC7; this exception adds no outcome. The positive pre-admission case likewise resolves only an owned guard, if one was prepared and the invocation can prove it did not send.
3. **Uncertain application:** a missing, malformed or inconclusive result, a capacity-like message alone, or even a later observation that the target is full supplies no permission to resolve a sent guard. Keep the independent durable guard and actual attempt evidence, show uncertainty under [OC-006](oc-006-refresh-authorization-and-call-safety.md), and retain unresolved work under the applicable existing rules. Never move uncertainty into an evictable waiting entry as a substitute for this protection.
4. **Retained work is not send permission.** A handoff identifies the same missing video-and-playlist add and its authorizing decision for the receiving run. Claimants must establish current ownership and send eligibility; stale, evicted, dismissed and otherwise not-owned sources cannot authorize a send or a replacement capacity admission. Existing success and final-outcome vetoes still apply. A transfer to capacity waiting does not rewrite the historical reason, origin or disposition of earlier queue or run records, or consume unrelated work or wait entries for another target.

## Failure semantics and invariants

- Acknowledge capacity retention only once current run/action evidence, the valid retained missing add, any necessary overflow eviction/history and any permitted owned guard resolution agree durably. On failure or unknown commit, no partial success or replay permission may be inferred. Retention failure must not remove the guard or acknowledge original work without an actual authorized replacement. Recovery preserves existing ownership/removal boundaries and cannot recreate an old live send authorization.
- Capacity findings, eviction and local accounting failures do not invalidate actual successes, admitted-call evidence, independently unresolved guards, or earlier runs' outcomes. Retention or accounting failure follows existing failure reporting, not fabricated capacity, success, or non-application.
- A slot becoming available only makes work potentially eligible; it is not authorization, proof of earlier non-application, or evidence that a stale claimant may proceed.

## Derived from

- The user direction relayed in the 2026-11-18 specification assignment as captured in [BR-040](br-040-respect-youtube-playlist-capacity.md) and [BR-041](br-041-bound-playlist-full-waiting-work.md).
- Guarantee 2, *refusal after eviction or dismissal*: the user's "yes" of 2026-11-22 to capacity ARS-001 §12 item 3 (R3), relayed in the specification assignment; the original human conversation was not independently inspected. The user's rule: on a definite `403 playlistContainsMaximumNumberOfVideos` refusal of an admitted add whose waiting entry was evicted or dismissed, record it as not added and release its send guard with no waiting entry; nothing is retried; Run Detail shows the truthful not-added result. *Derived:* the refusal still counts as one attempted add under existing accounting, and the video's outcome is unchanged from BR-041 AC7, because the user decided only the add's result and its guard. *Specification choice:* the exception applies when the entry is already gone at the moment the refusal is resolved; if the entry still exists, the ordinary handoff applies.
- [BR-012](br-012-idempotent-playlist-actions.md), [BR-039](br-039-final-outcomes-are-never-reprocessed.md), [OC-006](oc-006-refresh-authorization-and-call-safety.md) and [OC-002](oc-002-route-evaluation-outcome-semantics.md) constrain replay, work retention and honest outcomes. Without a durable handoff, a refusal could erase the guard without leaving authorized work or evict an older item without admitting its replacement.

## Reconciliation

- **Aligned:** [Authorization ARS-001](../../architecture/youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) requires positive non-application, exact owned resolution and durable retention. Its currently specified admitted-release path does not establish a capacity refusal as known non-application. The architect must establish provider-backed evidence and extend the guarded path before permitting this branch to release a guard. A status or reason code alone is not assumed sufficient; if the architect cannot substantiate positive non-application, uncertainty remains binding on that branch. This does not block the positively known pre-send path. No provider membership reconciliation or manual guard clearing is authorized.
- **Missing realization:** the [Safe Diagnostic ARS](../../architecture/run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md) lists closed action and queue reasons without capacity/eviction. The architect owns consistent registration and compatibility before use. Authorization ARS-001's “Exact retry ownership receipt” and [final-outcome continuation ARS-006](../../architecture/run-initiation/specifications/ars-006-final-outcomes-are-never-reprocessed.md) need reconciliation for target-scoped retention and eviction without losing unrelated ownership. Neither a video's identity nor a new queue reason alone can implement that behavior.
- **Missing coverage:** the [Admin Interface Design](../../design/yuhaul-admin-ui-design.md) defines run and deferred views and user dismissal, but not a capacity deferral or visible system eviction. The designer owns how to present these distinctly; no new control or placement is decided here.

## Verified by

Exercise the actual production guard, provider adapter, queue and run/queue inspection path with a controlled provider and durable reopen. Compare known-full without a POST, reliable room followed by a positively recognized full refusal, and an admitted add followed by a timeout, incomplete reply or ambiguous capacity-like failure. Inspect sent attempts and quota accounting, guard state, retained queue identity, present and earlier run evidence, and safe reasons. Then open room and start normal retry: only eligible retained work can send, never a still-guarded or evicted add. Evict, and separately dismiss, an admitted add's waiting entry while its send is in flight, then return a definite `403 playlistContainsMaximumNumberOfVideos`: the guard is released with a committed not-added result, one attempted add is counted, no waiting entry exists, Run Detail shows not added, and after reopen and later runs the add is never sent again. Repeat with a timeout instead: the guard stays and Run Detail shows unknown application. Inject failed saves and ownership loss during retention and overflow. Verify no guard is released without the required durable result, and neither stale nor explicitly removed work reappears. These are conformance cases, not executed proof or a substitute for authoritative provider evidence.
