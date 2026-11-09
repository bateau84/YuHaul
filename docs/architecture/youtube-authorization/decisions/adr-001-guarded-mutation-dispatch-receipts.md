---
type: decision
title: ADR-001 — Guarded Mutation Dispatch Receipts
description: Close the executor and authorization-adapter seam with non-secret binding identity, fenced request admission and an explicit mutation result independent of accounting errors.
tags: [architecture, decision, authentication, idempotency, dispatch]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** decided technical correction; bounded critique approved/SOUND after missing-receipt clarification. Implementation and runtime conformance outstanding.

Keep the durable guard in the executor and make the request adapter return what it actually knows about sending and application. Expose the binding's non-secret connection ID for that guard; do not expose credentials. For example, after a guard commits, Disconnect can make the adapter refuse the POST. That live invocation can atomically record the no-attempt disposition and remove its own guard. A lost response after admission cannot do so, even if renewed access works.

## Derived from

- [Authorization ARD-001](../design/ard-001-shared-renewal-and-mutation-send-safety.md) and [ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md).
- [BR-034](../../../requirements/yuhaul/br-034-automatic-youtube-token-refresh.md), [OC-006](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md), [BR-012](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md) and [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md).
- [ARS-006](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md), run ownership and terminal immutability.

## Satisfies

| Authority | Disposition |
|---|---|
| BR-034 AC4/7; OC-006 G3–5 | This correction supplies an authoritative per-request admission receipt and separates mutation application from renewal/accounting outcomes. |
| BR-012; authorization ARS guard ownership | This correction binds claim, request and result to the same exact target and connection, with atomic owned release. |
| BR-034 AC8 | Only non-secret IDs, attempt facts and bounded diagnostics cross this seam; credentials remain inside auth/request adaptation. |
| Renewal algorithm/status, legacy admission, UI and retention | Remain with the linked ARD/ARS and accepted contracts; unchanged, not reimplemented here. |

## Decision question

What is the smallest interface that lets independently implemented auth-dispatch and send-guard components safely decide whether to release an exact-target guard?

## Drivers and constraints

Source reads on 2026-11-07 establish the integration gap, not a production incident cause:

- `internal/auth/service.go:275–308` supplies a binding with a private identity and resolves credentials, but resolution is not admission.
- `internal/youtube/client.go:194–205` resolves and then calls transport separately. Its `post:580–599` returns an accounting error in place of already known provider success.
- `internal/pipeline/executor.go:23–26,430–470` receives only an error from insertion, takes an attempt timestamp before calling it and cannot distinguish refusal from uncertainty. Its action evidence helper does not combine success with guard removal.
- `internal/auth/store.go:102–166` already supplies durable connection/revision identity. `internal/state/store.go:289–324,716–743` and the existing action schema supply ordinary transaction/evidence mechanisms, not a guard claim/resolve protocol.

Semantic navigation returned degraded gopls (`authorityTestTransport is not a function`); the bounded source reads above are the grounding. No runtime test or live operation was performed.

One application process and the existing production gate remain the trust model. Do not add a provider transaction, new service, distributed lease, general event log or recovery override. No SQLite action transaction or auth mutex spans a provider wait.

## Options considered

| Option | Benefit | Cost, failure mode and obligation impact |
|---|---|---|
| Keep error-only insertion; conservatively retain every errored claim | Smallest interface change; protects against duplicate replay | Cannot truthfully identify auth refusal as no-attempt, cannot safely release known refusals, and loses success behind recorder failure. Requires special error knowledge anyway. Incomplete for accepted ARS behavior. |
| Adapter owns claim, provider send, action persistence and queue disposition | A single component could ensure guard-before-send | Viable with the same receipts internally, but moves run/snapshot authority and SQLite/queue concerns into the provider layer or needs transaction callbacks. More coupling and migration risk than the existing executor boundary requires. |
| Executor owns durable claim/resolve; adapter returns explicit dispatch/application facts | Reuses run authority and state transactions; narrow cross-component contract | Requires changing the production insertion interface and auth admission boundary together. Conservative crash-before-send blocking remains. **Choose.** |
| Persistent prepared/admitted/completed workflow or outbox worker | Richer persisted sending history | Still cannot eliminate the provider/local-commit gap. Additional states, worker ownership and recovery behavior discharge no accepted requirement beyond the small guard. Reject. |

## Comparison

The chosen option preserves existing component ownership and requires no new table beyond the already accepted guard. A read-only identity accessor is smaller than moving persistence into auth. Explicit results are necessary because an ordinary error cannot prove transport non-entry or erase independently observed success. Admission, not token lookup or inferred socket progress, remains the accepted authority boundary.

## Decision

1. Add a read-only internal accessor for `AuthorizationBinding`'s stable `authorization_id`. It is correlation data, never a permission, Google account identifier, public API field or credential. Keep all credential/revision validation in auth.
2. Replace the production error-only mutation seam with the guarded insertion request/result defined in [ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md), §Guard-facing adapter contract. All production executor entries use it; no error-only or static-token fallback is permitted for guarded production.
3. Auth owns synchronous `Dispatch`: resolve outside transactions, then under the Disconnect/replacement fence accept one concrete request for immediate transport and capture its actual identity/revision/time. Release the mutex before network I/O. No permit escapes for later use. The adapter returns `not_issued` only on positive knowledge of no admission; an admitted failure defaults to `uncertain`.
4. Keep the committed claim and owned transaction operations in executor/state. Claim precedes any possible POST. The same immutable target/identity descriptor must be used in claim, insertion and resolution. A live claim receipt is available only to the winner of that insertion, never recreated by reading an existing guard.
5. Separate application result from accounting/renewal/recording failures. Definitive provider success stays success evidence even if quota recording fails. If its local success-and-delete transaction cannot commit, retain the guard and report unresolved local confirmation; never resend. Only conclusive owned resolution commits release a guard.

The ARS defines fields, exact classifications, safe release and restart behavior. This is a realization correction, not permission to retry more operations or resolve historical uncertainty.

## Consequences

- Auth-dispatch and guard implementation have a shared interface dependency; neither an identity accessor alone nor a pre-POST insert alone completes the guarantee.
- A committed guard may remain forever after a crash before transport. This is the already accepted conservative result, not a new retry policy.
- Existing error-only test clients need typed guarded fixtures. Unknown/custom result values fail closed; retaining an old seam for read helpers does not authorize a production bypass.
- Missing admission evidence retains the unresolved guard but proves neither attempt nor definite non-issue. It cannot fabricate a failed-attempt row/count; the ARS states this explicitly for invalid/custom results.
- No new persisted claim UUID, admitted flag or expiry is required in this single-process model. Invocation-local receipt ownership plus comparison of the unchanged full guard row suffices. Revisit if dispatch/result handling outlives its invocation or another writer is supported.

## Confirmation and reconsideration

Required, not executed here: controlled replacement after resolution but before admission; auth refusal after claim; two claim contenders; POST application followed by lost response; success plus recorder failure; local resolution rollback; same-run repeated action after uncertainty; crash/reopen at claim and provider/local-save boundaries. Contrast an empty result after refusal with unknown application carrying a valid admission receipt: both retain the guard, but only the latter establishes an attempted failure/time/count. Preserve any prior independent attempt evidence. Assert request count, retained guard, action timestamps, current-run attribution and unchanged terminal history together. Prove no redirects or HTTP/client automatic POST replay. Use real auth, adapter, executor and SQLite with controlled external endpoints, not a fake error-only inserter.

Bounded critique by `ses_e49eaceb2ffevU1POSie10Yz5H` initially found one MAJOR ambiguity: invalid/missing admission facts could be mistaken for an attempted failure. Accepted and corrected by the explicit missing-receipt rule and contrasting conformance cases in ARS/ADR. Its branch-only follow-up, QA `d264f240-e1aa-4e4e-9fde-acdcd6f84b8f`, reports APPROVED/SOUND and resolves concern `cdf440d3-ccca-442d-a12e-bdbd18020564` as a specification defect. Automatic prior-opinion exposure and the author's note qualify independence. No implementation, runtime test or shipping approval is implied.

Reconsider only for a demonstrated failure of this contract, accepted multi-writer support, an asynchronous sending owner, or an explicitly specified reconciliation/recovery capability.

## Open questions

No new user policy ambiguity blocks this seam. Historical guard resolution and legacy uncertainty retain their existing limits. Owners: `auth-owner` supplies binding identity and fenced Dispatch; `auth-dispatch` supplies request/result adaptation and attempt accounting; `auth-send-guard` supplies migration, claim/resolve and executor integration. The dispatcher coordinates their shared interface and verification; this decision does not sequence tasks or claim their completion.
