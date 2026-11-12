---
type: requirement
title: BR-034 — Automatic YouTube Token Refresh
description: Production work renews expired YouTube access automatically and reports renewal failure truthfully.
tags: [requirement, authentication, youtube, token-refresh]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL automatically renew YouTube access authorization using the stored refresh credential when available, so scheduled and manually initiated production API work continues across access-token expiry without requiring reconnection solely because access expired.

## Acceptance criteria

1. Before a production YouTube API call uses access known to be expired, the system attempts renewal using the stored refresh credential. Successful renewal permits the intended call for the same authorized account without interactive authorization. This applies after application restart and when access expires between calls during a run.
2. If the provider rejects access authorization during production work, the system attempts available renewal before concluding that interactive reconnection is necessary. Renewal alone does not authorize replay of an issued call; [OC-006](oc-006-refresh-authorization-and-call-safety.md) governs that boundary.
3. Successful renewal preserves usable authorization for later calls and across restarts. Replacement access credentials and any replacement refresh credential are persisted encrypted. An omitted replacement refresh credential does not erase the existing usable one. Failure to persist is not reported as durable renewal success.
4. Concurrent callers cannot lose accepted replacement credentials, overwrite them with older credentials, or mistake an obsolete refresh failure for rejection of newer authorization. OC-006 owns shared ordering and user-authority precedence.
5. Missing refresh authorization, confirmed invalid/revoked refresh authorization, temporary provider/network failure and failure to persist renewal produce distinguishable safe recovery information. Missing/rejected refresh authorization offers the existing reconnect flow. Temporary renewal failure is not proof that consent was revoked.
6. Account status does not claim usable access merely because configuration or a refresh credential exists. Expiry alone does not mean reconnection is required while renewal remains available and has not been rejected. Usable access, renewal pending/unavailable and reconnection required are distinct meanings, not prescribed API fields or UI copy. Confirmed authorization rejection supersedes earlier connected information until recovery succeeds.
7. Renewal failure neither fabricates successful API work nor discards pending work. Existing run-failure, preservation and idempotency obligations remain binding. Renewal does not authorize restarting failed runs or blindly replaying playlist mutations.
8. Tokens, refresh credentials and provider-controlled raw error bodies/chains are absent from logs, diagnostics, API/status responses and UI. No credential material is persisted in plaintext, including diagnostic records.

## Derived from

- User direction on 2026-11-07: “refreshing of tokens needs to be a requirement”, relayed in this specification assignment with explicit scheduled/manual production, encrypted persistence, concurrency, safe status/failure and mutation-retry constraints. Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`, follow-up to `auth-restart-research`. This supplies the new refresh promise; implementation does not.
- [BR-001](br-001-secure-youtube-account-connection.md): secure authorization, user revocation/replacement and restart persistence.
- [BR-012](br-012-idempotent-playlist-actions.md): renewal must preserve completed-action protection.

## Specification choices and limits

Ordinary specification detail: preserve an existing refresh credential when renewal omits replacement; distinguish temporary failure from reconnect-required; do not equate configured/refresh-capable with currently usable access. OC-006 settles late-result and user-authority precedence so these guarantees compose.

This requirement chooses no refresh schedule, numeric retry budget, algorithm, locking mechanism, storage schema/technology, API shape or proactive status-triggered network call. The authorization ARD/ARS below define the supported concurrency boundary and realization; no new multi-replica capability is promised. No simulation/provider calls, date policy, coordinator, live-account operation, bulk recovery or replay of historical failed runs is authorized.

## Verified by

- Through scheduled and manual production entry paths with a controlled provider, expired access plus valid refresh authorization renews before the protected call, preserves the account and completes work without reconnecting. Repeat after restart and with expiry between calls.
- Restart readback uses replacement authorization. Inspect credential storage and diagnostic surfaces for plaintext credential markers.
- Exercise missing refresh credential, rejected/revoked grant, temporary provider failure, refresh-response omission of replacement refresh credential and persistence failure; observe distinct recovery meaning, truthful status/run outcome and preserved work.
- Exercise OC-006's concurrent-caller, authority-change and ambiguous-mutation composition cases. Inspect logs, persisted diagnostics, API/status and UI for injected token and raw-body markers. Adjacent helper tests alone are insufficient.

## Reconciliation

- **Aligned design:** The accepted [Admin Interface Design](../../design/yuhaul-admin-ui-design.md), opening journey, US-001, SC-007 and Account Access section now distinguish automatic renewal, temporary/save failure and missing/rejected renewal authorization. They preserve work, truthful status, newer user authority and uncertain-mutation safety. The expiry-only reconnection discrepancy is closed; no story is authored here.
- **Aligned architecture:** [System Architecture](../../architecture/yuhaul-system-architecture.md), OAuth and Credential Storage and Failure Modes item 3, now applies renewal before reconnecting. The decided [authorization ARD](../../architecture/youtube-authorization/design/ard-001-shared-renewal-and-mutation-send-safety.md) and [authorization ARS](../../architecture/youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md) cover all eight acceptance criteria and OC-006's six guarantees: shared durable renewal, authority precedence, truthful status and separate mutation safety. Their single-writer boundary, schemas, dispatch checks and replay veto are architectural realization, not additional product meaning. This closes documentation reconciliation only, not implementation or assembled-path verification.
- **Aligned associated requirement:** [BR-030](br-030-temporary-failure-work-preservation.md) now applies BR-034/OC-006 to mid-run expiry without weakening work preservation.
- Supplied research reports expiry-only disconnected status and a static-token path: implementation gaps, not independently verified runtime behavior or product authority. Supplied account-status/Run20 evidence does not prove restart token loss, refresh-grant validity or Run19's unavailable cause.

## Open questions

No user-owned choice blocks this bounded change. Designer/Architect documentation reconciliation is closed for this amendment. Implementation and assembled-production-path verification remain outstanding; the required cases above are not executed evidence.
