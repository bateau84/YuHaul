---
type: obligation-contract
title: OC-006 — Refresh Authorization and Call Safety
description: Shared authority and failure meaning across renewal, durable credentials, production calls and account status.
tags: [obligation-contract, authentication, concurrency, youtube]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Participants

- Authorization renewal and durable credential custody.
- Production YouTube API callers, including discovery and playlist actions.
- Account-status and diagnostic presentation.
- External authorization provider: its acceptance/availability are conditions, not guarantees made by YuHaul.

## Preconditions

The user authorized a YouTube account. Production work needs access authorization or the provider rejected the access used. [BR-034](br-034-automatic-youtube-token-refresh.md) owns automatic renewal's system-level outcome.

## Semantic inputs and outputs

- Inputs: intended account/operation, authorization validity, renewal response/failure, durable-save outcome, whether an issued operation is known applied/not applied or uncertain, and intervening user disconnect/replacement.
- Outputs: usable authorization for the intended account or safe unavailable/reconnect-required result; durable-renewal outcome; operation continuation or retained unresolved work; truthful status and operation evidence. These are meanings, not wire fields.

## Guarantees

1. A caller cannot receive known-expired access as usable merely because stored credentials exist. Renewal and durable-save outcome must be resolved before replacement is treated as durable usable authorization.
2. Overlapping renewal/calls share a coherent current outcome. Late results cannot overwrite newer accepted credentials, discard a rotated refresh credential or report an obsolete rejection as failure of newer successful renewal. No particular refresh-request count or coordination mechanism is prescribed.
3. User disconnect or account replacement takes precedence over in-flight renewal of old authorization. Late renewal cannot reconnect the disconnected account, overwrite its replacement or authorize new calls under obsolete authority. Cancellation of an already issued external call is not promised.
4. Renewal and operation retry are distinct. A read-only call may be retried with renewed access without claiming first-call success. An issued playlist mutation MUST NOT be blindly replayed after an authorization response or transport failure leaves application uncertain. Automatic replay requires evidence that it cannot duplicate the intended effect under existing idempotency obligations; renewed credentials alone are not that evidence.
5. Without safe-replay evidence, callers preserve unresolved work and report uncertainty without inventing success or certain non-application. Reauthorization does not erase uncertainty or independently authorize replay.
6. Missing/rejected refresh authorization yields reconnect-required. Temporary provider/network failure or failure to persist replacement yields renewal unavailable with a safe reason, not fabricated revocation. Expired/rejected access plus an unvalidated refresh credential is not proof of usable access. Later successful recovery supersedes earlier failure; raw provider prose/credentials never cross diagnostic boundaries.

## Failure semantics and invariants

- Cancellation/failure before durable renewal is not durable success. Later calls resolve authorization again; no unconditional in-request retry or completion deadline is promised.
- Provider success followed by persistence failure is renewal-persistence failure, not rejected consent. If rotation prevents credential reuse, reconnect may become necessary only on actual evidence.
- Mutation ambiguity survives refresh, reconnect, retry initiation and restart until evidence resolves it. Work preservation, truthful run outcomes and completed-action idempotency remain binding.
- Renewal cannot broaden authorized account/scopes. BR-034's encrypted persistence and diagnostic secrecy apply throughout.

## Derived from

- [BR-034](br-034-automatic-youtube-token-refresh.md), including recorded user direction and explicit concurrency/safe-mutation constraints.
- [BR-001](br-001-secure-youtube-account-connection.md): revocation/replacement authority; late renewal cannot undo it.
- [BR-012](br-012-idempotent-playlist-actions.md) and [OC-001](oc-001-youtube-api-quota-and-failure-semantics.md): preserved action safety, call evidence and work retention.
- Ordinary specification detail: late-result/authority precedence and persistence-failure classification make those guarantees compose without prescribing mechanisms.

## Verified by

Use the assembled production caller → renewal → durable credentials → account-status path with a controlled provider. Overlap callers with delayed/out-of-order results and rotated credentials; later calls/restart use the accepted replacement, and obsolete rejection cannot change status. Overlap renewal with disconnect/account replacement; late completion cannot restore old authority. Inject save failure: no durable-success claim or plaintext fallback.

For expiry before mutation, observe renewal then one intended mutation. For uncertain mutation application after authorization/transport failure, allow refresh success but no resolving evidence: no blind second mutation occurs, unresolved work stays inspectable and success/non-application is not fabricated. Separately provide safe-replay evidence and verify existing idempotency. Exercise read-only recovery, reconnect-required versus temporary-unavailable status and credential/raw-body marker checks across all diagnostic boundaries. These are required cases, not executed proof.

## Reconciliation

The decided [authorization ARD](../../architecture/youtube-authorization/design/ard-001-shared-renewal-and-mutation-send-safety.md) and [authorization ARS](../../architecture/youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) provide realization for Guarantees 1–6, including durable renewal, late-result/user-authority ordering and uncertainty-preserving mutation handling. The accepted [Admin Interface Design](../../design/yuhaul-admin-ui-design.md), US-001, SC-007 and Account Access section, covers the corresponding status and recovery meanings. No material documentation conflict remains in this bounded seam. Architectural fields, request budgets and mechanisms are not new obligations in this contract. Implementation and assembled-path conformance remain unverified; document alignment is not product acceptance.
