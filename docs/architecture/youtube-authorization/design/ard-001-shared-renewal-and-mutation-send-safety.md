---
type: design
title: ARD-001 — Shared YouTube Renewal and Mutation Send Safety
description: One authorization owner renews and durably saves credentials before per-request dispatch; durable send guards prevent blind playlist replay.
tags: [architecture, authentication, youtube, token-refresh, concurrency, idempotency]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** decided technical design; bounded critique approved/SOUND. Not implemented or runtime-verified by this artifact.

Production work will renew expired YouTube access automatically, then use the saved replacement for each actual request. One shared authorization owner serves scheduled runs, manual runs and other production callers. It also prevents a late renewal from undoing Disconnect or replacing a newer account. Renewal restores access; it does not prove that an earlier playlist insert failed to apply.

For example, a scheduled run starts with expired access. The authorization owner renews and encrypts the replacement, commits it, then allows discovery. If access expires before a later playlist action, that action resolves authorization again. If a playlist POST might have applied but its result is lost, a durable guard blocks another POST even after renewal, reconnect or restart. The work stays inspectable rather than being declared successful or certainly absent.

The design supports one application process and one SQLite writer, not overlapping replicas or old binaries. It adds no refresh scheduler, simulation provider calls, historical-run restart, live-account repair or automatic mutation reconciler. Run19's exact cause remains unproven; the supplied Run20 401/expired-status observations do not establish refresh-grant rejection.

## Derived from

- [BR-034 — Automatic YouTube Token Refresh](../../../requirements/yuhaul/br-034-automatic-youtube-token-refresh.md).
- [OC-006 — Refresh Authorization and Call Safety](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md).
- [BR-001 — Secure YouTube Account Connection](../../../requirements/yuhaul/br-001-secure-youtube-account-connection.md) and [Account Connection Security](../../../requirements/yuhaul/account-connection-security.md).
- [BR-012 — Idempotent Playlist Actions](../../../requirements/yuhaul/br-012-idempotent-playlist-actions.md) and [OC-001 — API Quota and Failure Semantics](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md).
- [System Architecture](../../yuhaul-system-architecture.md), single-process deployment and run failure/recovery.
- [Run-Owned Production Action Authority](../../duplicate-continuation/design/ard-001-run-owned-production-action-authority.md) and [ARS-006 — Run-Attributed Production Outcomes](../../duplicate-continuation/specifications/ars-006-run-attributed-production-outcomes.md).
- [Safe Diagnostic Contract](../../run-outcome-diagnostics/specifications/ars-001-safe-diagnostic-contract.md).
- [Admin Interface Design](../../../design/yuhaul-admin-ui-design.md), SC-007 and Account Access — Automatic Renewal and Safe Recovery.

## Satisfies

| Authority | Disposition and responsible boundary |
|---|---|
| BR-034 AC1–2 | Satisfied by this design: production request adapter resolves current access before every send, including restart/between calls, and handles recognized access rejection through shared renewal. |
| BR-034 AC3–4; OC-006 G1–2 | Satisfied by this design: auth.Service owns renewal, one flight per current authorization, revision-fenced results, encrypted atomic credential/state commit before use. |
| BR-034 AC5–6; OC-006 G6 | Satisfied by this design and exact ARS: safe status distinguishes usable, needs renewal, renewing, unavailable and reconnect-required. Status reads themselves do not renew. Designer owns rendering; implementation remains outstanding. |
| BR-034 AC7; OC-006 G4–5 | Satisfied by this design: renewal is separate from operation retry; exact-target durable send guard vetoes uncertain repeats. Existing queues retain work; original terminal evidence stays unchanged. |
| OC-006 G3; BR-001 AC3 | Satisfied by this design: disconnect/replacement fences credentials, callback completion, dispatch and late results. Already issued external calls are not promised cancellable. |
| BR-034 AC8; BR-001 AC4/6; security requirement | Satisfied by this design: existing AES-GCM custody, no plaintext fallback, typed safe diagnostic facts on auth as well as run paths. Ingress/access control remains satisfied elsewhere by ADR-010/ARS-003, not certified here. |
| BR-012; OC-001 | Preserved: existing successful-action veto, run-owned targets, quota deferral, pending-work retention and accounting contracts remain binding. This artifact closes the newly relevant blind-replay path, not all legacy provider accounting or historical uncertainty gaps. |
| Simulation isolation, date meaning, failed-run recovery | Satisfied elsewhere by existing contracts; unchanged. No refresh or API call is added to local simulation/inspection. Date selection remains owner-blocked; accepted failed runs remain terminal. |

No upstream product decision blocks this bounded realization. The exact schema/status/call contract is [ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md).

The focused [ADR-001 — Guarded Mutation Dispatch Receipts](../decisions/adr-001-guarded-mutation-dispatch-receipts.md) closes an implementation seam: the executor may read the binding's non-secret connection identity for guard storage, and the adapter returns explicit admission/application facts rather than an error alone. Token resolution is not sending admission. Guard ownership remains in executor/state; success cannot disappear behind a later quota-recording error. Its separate bounded critique approved the correction after clarifying that a missing admission receipt cannot fabricate an attempted failure; details and independence limits are in the ADR. No implementation proof is implied.

## Problem and grounded findings

Direct source reads on 2026-11-07 establish these gaps, not incident root causes:

These are the initial design's baseline observations, before concurrent auth-owner/binding changes. They are not a claim that every listed gap remains in the integrated tree. The focused ADR records the later guard/dispatch seam inspection; assembled conformance still needs proof.

- `internal/auth/service.go::ActiveToken` loads and returns stored credentials without validating expiry or renewing. `Status` derives connection solely from expiry. Callback `Save` and Disconnect `Delete` have no shared ordering with future renewal.
- `internal/auth/store.go` already encrypts access/refresh values independently with the versioned AES-256-GCM format in `crypto.go`; its single-row Save is atomic but unconditional. Reuse this store, not a second credential repository.
- `internal/youtube/client.go::NewClient` installs `oauth2.StaticTokenSource`. Discovery retains a client across pages; fixing only run admission cannot handle expiry between calls. Executor, `cmd/server` playlist listing and metadata fetching use the same token-snapshot pattern.
- Executor checks only durable `succeeded` rows and writes action evidence after POST. A crash, read failure or local save failure after provider application can therefore leave no completion veto. Renewing a token does not repair that evidence gap.
- `deferred_item` permits only quota/rate reasons. Accepted authorization deferral needs an explicit `authorization_pending` extension, not a fabricated quota reason.
- Installed `golang.org/x/oauth2@v0.37.0` supplies refresh exchange and token validity. Its `Config.TokenSource` caches within each source; `NewClient` also wraps a source in a reuse cache. Neither implements YuHaul durable save, user-authority fencing or operation-result safety. A generic source placed behind that cache can bypass per-request authority checks.

Semantic navigation/one repair returned degraded gopls (`Option is not a function` in corpusimport); bounded source reads supplied grounding. No database, credential or provider operation was performed.

## Constraints and forces

1. Keep auth ownership in existing `auth.Service` and existing encrypted SQLite store. All production token/callback/disconnect writes go through that owner; direct diagnostic/test seeders are not production interfaces.
2. Use current run-owned target/action authority and the shared production execution gate. Auth locking cannot replace pipeline ownership; pipeline serialization cannot protect concurrent Settings/metadata/provider reads.
3. Hold the authorization mutex only for state checks, dispatch admission and local commits. Never hold it while waiting on provider response. This lets Disconnect invalidate an in-flight renewal immediately.
4. Tokens remain private to the adapter/owner. An exported cached token is not an authorization capability for future sends. Clients bind an opaque authorization identity, not a refresh credential or mutable current account.
5. No distributed lock, broker, durable job scheduler, event ledger or two-phase commit with Google. SQLite cannot atomically commit a provider mutation; when evidence is absent, preserve uncertainty.
6. Supported rollout stops the old process before applying forward migrations and starting the new process. Downgrade writers and independent CLI writers against the same database are unsupported.

## Options considered

| Option | Mechanism and benefit | Costs, failure modes and decision |
|---|---|---|
| Renew at run start, retaining current client factories | Smallest local change; reuses token loading | Fails BR-034 between pages/actions and OC-006 Disconnect precedence; separate callers race rotated credentials. Not viable. |
| Per-client `oauth2.Config.TokenSource` with save wrapper | Reuses library exchange/cache; simple individual client | Each client owns separate renewal/cache. Requires additional shared coordination anyway; cached usable tokens bypass invalidation. Provider success can precede save. Reject as system authority. Use library exchange only inside the chosen owner. |
| Shared auth owner, per-request fenced adapter, existing store, minimal send guard | One place for renewal, commit, status and user authority; existing process/store/queues | Requires bounded service/interface changes and one small operational guard relation. Correct with one writer; conservative uncertainty may block a request that crashed before it was actually sent. **Choose.** |
| External credential service/distributed lease plus durable mutation worker/reconciler | Can support multiple application writers and richer resolution | Adds services, lease failure/expiry, operational burden and provider reconciliation policy not required by supported deployment. No accepted requirement justifies it. Revisit for approved multi-writer support or a specified resolution flow. |

For mutation safety, successful-row-only checks lose the provider/local-record crash window. Membership absence is not selected as safe-replay evidence: this design establishes no provider consistency guarantee that makes a negative read prove non-application. A minimal unresolved-send row is sufficient to block blind replay; a general outbox/reconciler is unnecessary. A known existing success remains the cheapest safe veto.

## Decision

Use **one shared authorization owner with commit-before-use renewal and per-request dispatch fencing; add a minimal persistent veto for potentially issued playlist mutations**. Keep operation policy in the YouTube adapter/executor, not inside a generic OAuth transport. The adapter may repeat a read once after accepted renewal; it never repeats an uncertain mutation. Exact fields and transitions live in the ARS.

## Structure and flows

```text
Scheduler / Manual / Retry / Resume ---- existing production authority gate
                                            |
Discovery / Executor / metadata / playlist listing
                        |                   |
                        +---- YouTube request adapter
                                      |
                          shared auth.Service
                         /                   \
             existing encrypted         OAuth token endpoint
               SQLite store             (bounded exchange)

Executor ---- exact-target success / unresolved-send checks
    |                     |
    |              SQLite operational guard + existing run evidence/queues
    +---- single admitted playlist POST through same request adapter

Settings / Overview ---- local status snapshot; never implicit renewal
```

### Normal renewal

1. A production client binds the current authorization identity. Before each concrete request, the owner validates that binding and checks current access expiry/rejection.
2. If renewal is needed, one caller starts a flight for the current identity/revision; others join its outcome. Token exchange runs outside the mutex, with a bounded request context. Cancellation is not proof of rejected consent.
3. The owner rechecks identity/revision after response. It discards obsolete results. A valid response preserves an omitted refresh credential, preserves connected-at and scope authority, encrypts replacements and commits the entire credential/state update atomically before dispatch is allowed.
4. At the immediate dispatch boundary the adapter checks identity/current revision again and admits the request. No reusable permit is returned to a caller. A later Disconnect cannot revoke an already admitted external operation, but it vetoes every later admission and old renewal/callback result.
5. On recognized access rejection, the adapter tags the observation with the authorization identity and credential revision actually sent. Only a still-current observation can change current status or initiate renewal. A late old failure cannot poison newer credentials.

### Mutation send safety

1. The executor validates the receiving run's captured action authority and checks existing exact-target production success. A prior success records the existing no-attempt/already-completed disposition, not a new success.
2. With the invocation's bound identity, an atomic local claim rechecks run-owned authority, success and unresolved-send absence, then commits the exact-target guard before the adapter can issue the POST. Auth/budget preflight may avoid unnecessary claims, but is not permission to send. Final auth/quota/cancellation checks follow claim commit, outside the transaction. Competing claimants see an existing success or guard and do not send.
3. A definite successful provider response commits current-run success and removes the guard in one transaction. A known pre-dispatch refusal may remove its own guard while retaining the corresponding no-attempt/deferral evidence. A documented definite non-application result may likewise release the guard; authorization errors are not presumed to supply this proof.
4. Any possibly issued operation without a committed conclusive result keeps its guard. That includes refresh-success-after-401, transport/read failure, caller cancellation after admission, and provider-success/local-save failure. All retry/resume/new-run paths check it. Reconnect and queue removal do not clear it.
5. The UI receives explicit uncertain-application evidence. No new automatic reconciliation, human override or false certainty is introduced. Existing removal may stop queue retry, not certify that the external effect was absent.

## Invariants and guarantees

| Guarantee | Preconditions / owner | Enforcement and falsification point |
|---|---|---|
| No known-expired/rejected access is admitted | Production adapter uses shared auth owner | Resolve before each request; delay between pages/actions through expiry and assert renewal precedes next send. |
| Replacement never used before durable save | Token exchange succeeds | Auth owner encrypts/commits then publishes; inject Save failure and assert zero protected calls with replacement. |
| Latest authority wins | One application writer | Identity/revision fencing; Disconnect invalidates before network revocation; race callback/renewal and observe no restoration/new admission under old identity. |
| Concurrent callers share coherent outcome | All use the same owner | One flight for identity/revision, joined result; old access rejection cannot overwrite newer success. |
| No blind mutation repeat | Guard was durably committed before possible issue | Atomic exact-target claim and success/guard veto on every path; kill after send before local success, reopen and attempt retry. |
| Work is not acknowledged away | Renewal unavailable or effect uncertain | Existing receiving-run/queue transactions retain outcome and work before acknowledgement; deletion of retry visibility cannot remove send safety. |
| Diagnostics contain facts, not credentials/prose | All auth/run sinks | Existing typed diagnostic policy plus ARS registrations; inject token/body/description/URL sentinels into callback, refresh, save and error paths. |

## Failure and recovery

- Missing or typed confirmed-rejected refresh grant requires reconnect. Temporary network/provider failure, malformed token response, OAuth client configuration failure and save failure are renewal unavailable, not invented revoked consent.
- A successful refresh whose save fails is not published or used. While the process lives, retain its private candidate and retry that local save before another refresh; never overwrite it with an older flight. Disconnect/replacement discards it. A crash can lose an uncommitted rotated credential; no plaintext rescue store is permitted. Actual later rejection may require reconnect.
- Status reads do not call Google. They report one coherent local snapshot, fail non-2xx on unavailable custody/state and do not infer usable access from configuration/refresh presence. A previously observed current access rejection persists with credential state before normal use resumes.
- Auth deferral retains pending action/video groups as `authorization_pending`, using the existing queue with an additive admitted reason. Uncertain issued mutations remain failed/unresolved work, not quota deferrals. Renewal success alone resolves neither.
- Failed runs remain terminal. Current phase-abort/retention/interruption rules still apply; this design does not automatically restart old runs or rewrite terminal evidence.
- Uncertain guards survive process restart, authority replacement, retries and error dismissal. There is no TTL or “try again after refresh” release. Resolving them needs actual safe evidence; a broader resolution flow is future scope, not a blocker to conservative retention.

## Security / trust boundaries

Credential plaintext is needed in process memory and on the TLS token/data requests only. Existing AES-GCM ciphertext is the sole durable credential format; database errors never justify plaintext fallback. Persist safe enum state separately, never a token fingerprint, raw request/response, grant body or raw exception. The server key remains deployment-owned and must survive restart.

Only fixed trusted OAuth/Data API destinations receive bearer/refresh credentials. The existing provider base-URL seam remains an explicit operator/test trust boundary, not user-controlled input. Authenticated data requests do not follow redirects to another origin or automatically replay POSTs. Tokens must not appear in debug transport logs. Library `RetrieveError` contains raw body/description/URI; classify its registered facts without rendering it. Scope/issuer/account authority cannot be widened by token response text.

Auth status/callback/initiate/disconnect and status consumers must use the same safe boundary as production callers. The current callback handler has been partially guarded in the shared working tree; that is not proof that every auth log/status path is safe. Existing ingress prerequisites remain required for non-local exposure.

## Verification and composition

Downstream conformance must exercise the real scheduled and manual entry paths, shared auth.Service, AES-GCM store, migrated SQLite, request adapter, executor, registered account/run HTTP APIs and bundled real-client UI. Substitute only the external token/Data API endpoints using controlled test wiring; do not replace auth with a fake token source.

Prove expired-start, restart and between-call renewal; rotation/omitted-refresh preservation; save/decrypt/read failures; temporary versus rejected grant; concurrent readers/writers; late rejection; Disconnect/replacement during delayed exchange and before dispatch; read retry budget; zero automatic mutation replay; crash/reopen at guard commit, POST application, local success commit and queue handoff. Show prior historical rows stay unchanged, no simulation calls/writes, accurate attempted-call evidence and no secret/raw-body markers in storage/logs/API/UI. The exact matrix is in the ARS. No such execution occurred in this documentation task.

## Consequences and open questions

- One owner and one small guard relation add less machinery than a worker/reconciler while satisfying accepted concurrency and uncertainty guarantees. The price is conservative blocking: a guard committed just before a crash can say “unknown” even if no bytes were sent.
- Operator deployment must retain the single-writer boundary, encryption key and forward-only migration/reader compatibility. Multi-process/old-writer safety is not accepted or claimed.
- Legacy unsuccessful/missing action records cannot reveal historical application. This release cannot backfill trustworthy certainty or claim to fix Run19/Run20. An explicit incident recovery requires fresh evidence/authority outside this artifact.
- Design/requirements owners should mark their stale downstream-reconciliation notes resolved only after checking these documents; the accepted UX amendment already covers successful, temporary, save-failed and reconnect branches. Exact unknown-guard resolution is deliberately not a new product capability.
- Critic `ses_e4a36273affeNBMqBTu13mOueT`, QA `f5a3cbbc-ab09-468f-9925-06ddee43bb7d`, reports APPROVED/SOUND for the bounded technical architecture after current authority/source inspection and hostile lifecycle walkthroughs. No CRITICAL/MAJOR defect or materially simpler complete alternative was found. Prior-opinion/author-direction exposure qualifies independence; no runtime tests or shipping approval are implied. Its advisory binding-ownership ambiguity (`ddc3b0e9-50ee-4d57-b67f-b37b91282fd2`) is clarified in the ARS: one enclosing invocation shares the binding across clients, without permanent run/restart account pinning. Implementation/assembled-product verification remains outstanding. No user-owned scope decision blocks implementing this conservative design.
