---
type: decision
title: ADR-004 — Atomic Quota Admission and Single-Attempt Transport
description: Shared local quota holds at fenced dispatch and HTTP/1-only transport prevent stale budget admission and hidden retries.
tags: [architecture, decision, youtube, quota, concurrency, transport]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** decided technical contract; bounded critique approved/SOUND; implementation and runtime proof outstanding.

The final sending boundary reserves capacity, rather than merely reading a budget. All production callers share the ledger's small in-memory admission guard. Renewal finishes before acquisition. Each admitted request retains capacity until its actual-call entry commits. Standard Go Data API transports use HTTP/1 only without connection reuse, preventing one admission from silently becoming two requests.

For example, metadata waits for renewal while another request spends the last unit. Metadata then fails quota admission with zero Data API calls and zero consumption entries. If instead metadata wins the last unit, a concurrent caller is refused even while metadata awaits its response.

## Derived from

- [Authorization ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md), immediate authority admission and no network transaction lock.
- [ADR-003](adr-003-read-adapter-owns-actual-call-accounting.md), sole read accounting owner and one entry per actual attempt.
- Reviewer assessment `b293924b-0735-4c4b-8a05-dda22137c71b`, Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`: controlled stale-budget and HTTP/2 REFUSED_STREAM reproductions. These reports were read, not independently rerun here.

## Satisfies

| Authority | Treatment |
|---|---|
| [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) G6–7/invariants | Shared arbitration prevents knowingly exceeding budget; holds are not fabricated consumption. |
| [BR-018](../../../requirements/yuhaul/br-018-api-consumption-accounting-and-visibility.md) AC1/6 | Existing ledger remains sole durable actual-call store; accounting failure limits remain ADR-003's. AC2–5 remain existing views/window logic. |
| [BR-034](../../../requirements/yuhaul/br-034-automatic-youtube-token-refresh.md) AC1–4/7; [OC-006](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md) G2–5 | Renewal precedes final admission; authority fence and mutation replay veto remain unchanged. Remaining renewal/status/security behavior stays ARD/ARS-001's responsibility. |

No accepted requirement is intentionally deferred here. Crash-complete accounting remains unresolved under ADR-003; local holds do not solve it.

## Drivers and grounded findings

Source inspected on 2026-11-07:

- `internal/youtube/client.go`: `get` runs `readPreflight` before `httpClient.Do`; `NewAuthorizedClient` clones a standard transport and sets only `DisableKeepAlives`.
- `internal/auth/service.go::Dispatch`: `Resolve` may exchange/save credentials before final authority fencing and `RoundTrip`.
- `internal/quota/quota.go`: `CanAfford` sums committed rows, without reserving capacity or seeing in-flight calls. `RecordAttempt` appends after the call.
- `cmd/server/main.go`: standalone read preflight uses `CanAfford`; recording uses `RecordAttempt`.
- `go.mod` selects Go 1.26.3. Installed `/usr/local/go/src/net/http/transport.go` documents explicit `Transport.Protocols` and HTTP/2 defaults. `shouldRetryRequest` refuses ordinary HTTP/1 retry on a fresh connection, but has a separate HTTP/2 cached-connection retry path. Disabling keep-alives alone is not an HTTP/2 retry switch.

Semantic navigation failed with degraded gopls (`authorityTestTransport is not a function`); bounded source reads supplied grounding. No application tests or live operations were performed.

## Options considered

| Option | Benefits | Costs / failure modes | Decision |
|---|---|---|---|
| Move `CanAfford` after renewal | Tiny isolated stale-check repair. | Two callers still pass before either records; failed recording restores fictitious capacity. | Incomplete. |
| Serialize complete provider requests | Simple arbitration. | Gate spans slow network work; still needs accounting-failure debit. Auth/SQLite locks across network are forbidden. | Unnecessary blocking. |
| Shared local capacity holds finalized into existing ledger | Short locks; existing accounting owners remain. | Request-local hold lifecycle, shared wiring, conservative failed-record debit. | Choose for accepted single-process deployment. |
| Persist reservations/pending attempts | Could support additional crash/recovery semantics. | Schema/recovery states; still cannot atomically prove network issue and SQLite commit. | Not justified for this bounded defect. |
| Retain HTTP/2 and account beneath retry loop | Keeps multiplexing. | Standard transport has no public per-retry admission/recording callback; needs replacement transport/internal coupling. | Reject without demonstrated need. |
| Standard HTTP/1-only transport without reuse | Supported configuration; fresh connections avoid ordinary HTTP/1 replay. | Extra connections/TLS latency. | Choose; no measured performance goal justifies more machinery. |

## Decision and exact contract

### Shared capacity owner

Extend the existing ledger instance with one process-local admission mutex and opaque request-local holds. Production composition shares that instance across discovery, metadata, playlist listing and mutations. Separate per-client ledgers/mutexes against the same database cannot arbitrate capacity. No new package, durable reservation table, worker or user-visible state is required. Names below describe operations, not mandated Go spelling.

1. **Acquire at final admission.** Dispatch resolves/renews without a quota hold and outside action/queue transactions. Under its short final authority fence, validate current identity/revision/access, invoke an injected local quota admission function for the concrete operation, then accept only if both checks succeed. Quota acquisition serializes reading the configured limit/current bucket/committed consumption with checking and inserting a hold. Use existing operation cost. Early manager/client preflight is advisory, never permission.
2. **Arbitrate outstanding cost.** For positive finite limit, admit only when `committed units + outstanding held units + this cost <= limit` in that bucket. Unlimited (`limit <= 0`) retains current meaning but still creates an attempt-associated hold for finalization. Outstanding includes admitted calls awaiting response/recording and known admitted calls whose recording failed. Capacity is project/bucket-wide, not per account/run. Of simultaneous last-unit contenders, at most one wins; the loser gets existing quota refusal, no admission receipt, no call/row.
3. **Bind at acceptance.** A hold carries immutable operation/cost/bucket and the UTC admission timestamp shared by the authorization receipt and ledger row. Capture/recheck time at acceptance, not before renewal or database waits; if the bucket changes while preparing acquisition, recompute before acceptance. Revalidate context/access usability after a delayed local check. If acceptance cannot finish, cancel the provisional hold before resolving again or returning. No caller-visible reusable permit escapes. Release locks before the accepted request goes directly to its one transport call.
4. **Cancel only positive non-admission.** Auth/context/quota/local validation refusal before acceptance releases any provisional hold and counts zero. A hold is not an attempt receipt. After acceptance, timeout, cancellation, rejection, malformed response or network failure cannot refund it. Mutation guard release still depends on ARS application facts, not quota capacity.
5. **Finalize once.** The existing actual-call owner supplies the hold and safe outcome/status. Under the same quota mutex, append one actual-call row with captured admission time/bucket; remove the hold only after confirmed commit. No contender can observe neither debit or count both during a budget decision. Reads retain ADR-003's client owner; mutations retain executor ownership, without a second client charge. Do not also append a legacy/planned-count row. Repeated local finalization of a completed hold cannot append another row or repeat provider work.
6. **Failed recording stays debited.** Failed/unknown append commit reports separate accounting failure and retains that admitted hold against its bucket in this process. Do not refund known attempted cost or blindly append again after unknown commit. A possibly committed row plus retained hold may conservatively underestimate capacity; public consumption must not fabricate a durable row. No automatic repair loop, accounting-success claim or permanent service-wide latch is added. Other buckets remain independent. Restart/crash limits remain ADR-003's unresolved boundary.
7. **All spenders participate.** Every production Data API admission uses this owner, including each allowed explicit read recovery and mutation. Legacy append APIs remain for historical/test recording, not another production sending path. Existing views report committed consumption, not reservations as completed calls. Capacity preflights should include held cost. Simulation/local inspection stays provider-free and quota-free.

**Lock order:** auth authority fence → quota admission mutex → bounded local database work. Quota never calls auth or enters action/queue ownership. Finalization takes quota/database locks only, releasing them before rejection observation or any auth call. No reverse quota → auth nesting or action/queue transaction while entering Dispatch. No auth/quota mutex or SQLite transaction spans OAuth/Data API waits or body reads. Local database operations remain request-context bounded. The injected admission callback is local only, never generic network-capable work. Auth does not import quota policy; composition maps concrete operation and receipt/hold facts.

### Supported single-attempt transport

Bound production Data API clients use a dedicated standard `*http.Transport`, configured **before first use** with explicit `Transport.Protocols` containing HTTP/1 only, neither HTTP/2 nor unencrypted HTTP/2, and `DisableKeepAlives=true`. Do not rely on `ForceAttemptHTTP2=false` alone, nil protocol defaults, request headers, clearing `GetBody` or synthetic idempotency keys. Preserve TLS verification and supported proxy/dial/timeouts. Do not mutate `http.DefaultTransport` or OAuth token-exchange transport.

Custom TLS dialers, alternate-protocol registrations and wrappers cannot bypass HTTP/1-only/no-replay semantics. Custom injected RoundTrippers are supported only when their construction owner establishes one request attempt per call, without redirects/replay; setting fields on some other underlying transport does not certify arbitrary wrappers. Production wiring must reject unsupported construction rather than silently accept an unproved bypass. Tests may inject controlled single-attempt transports. No library fork or Go-internal retry flag is introduced.

Keep redirect refusal. Connection/TLS establishment attempts before issuing an HTTP request are not additional Data API calls; an additional HTTP request/stream is. Explicit read recovery, if used, re-enters auth/quota admission and records separately. POST is never automatically replayed. Reconsider HTTP/2 only with a supported per-actual-request boundary and equal proof, not merely better reuse.

## Consequences

The repair necessarily spans competing spenders, not just metadata preflight. Shared wiring and owner-specific hold finalization need implementation/review/tests; no migration is needed for local arbitration. Quota denial stays existing operational deferral, not auth failure or consumption. Failed accounting can conservatively reduce capacity until the bucket changes or process exits; this avoids known overspend without claiming crash recovery. HTTP/1/no reuse costs connections/latency. Multiple writers and crash-complete evidence would reopen persistence design, not waive accepted accounting guarantees.

## Confirmation and reconsideration

Required controlled tests through real auth/adapter/shared ledger/SQLite, with provider counts independent of accounting:

- Pause renewal with one unit left; let another production caller admit/finish that unit. Release renewal: metadata GET is refused with no new request/row. OAuth is uncharged.
- Race different clients for the last unit while holding winner response/body open. Exactly one admits. Repeat metadata versus discovery and mutation versus read with actual costs; per-client locks must fail this proof.
- Fail authority/context after provisional acquisition: release capacity, no call/row. Admit then cancel/fail/timeout: one attempt and no refund until recording commits. Disconnect remains responsive during network waits.
- Fail ledger recording, then try another same-bucket call: known spent capacity stays unavailable, no replay. Success atomically transfers hold to one row; duplicate local finalization adds none. Delayed completion across reset retains admission bucket/time. No crash-complete claim from memory.
- Use actual TLS standard transport against HTTP/2-capable controlled peer: negotiate HTTP/1.1 only, one request, no REFUSED_STREAM retry. Exercise HTTP/1 closed/stale connection failures and count requests/connections. Redirect sends no second request. Test custom transport rejection/acceptance and mutation no-replay separately.
- Re-run ADR-003 manager/fetcher accounting matrix and ARS authority/guard cases: no double charge, unsafe markers, transaction/network lock, or simulation provider call.

Integrator owns implementation/runtime proof. Bounded docs-only critic `ses_e48659e7dffeLxZfbXYsfgYdpa` reports APPROVED/SOUND, with no material flaw or simpler complete alternative; it confirms contract adequacy only. Prior opinions were exposed, so this is not independent rediscovery. Reconsider for another hidden attempt/bypassing spender, measured connection performance that fails accepted goals, multiple writers or accepted crash-recovery work.

## Open questions

No user policy decision is needed: both repairs realize existing quota/per-attempt promises. Whole-system accounting completeness and interrupted persistence remain unresolved, not waived. No implementation readiness or live-operation authority is claimed.
