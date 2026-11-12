---
type: decision
title: ADR-003 — Read Adapter Owns Actual-Call Accounting
description: One accounting owner for metadata reads, including admitted failures, independent of local metadata persistence.
tags: [architecture, decision, youtube, quota, metadata, authentication]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** decided; implementation and runtime confirmation outstanding

## Decision question

Who records consumption when a metadata request is refused before sending, fails after sending, or succeeds remotely but cannot be saved locally?

The YouTube read adapter owns accounting for each actual request. The metadata manager owns budget preflight and local metadata updates, not consumption writes. For example, an admitted `videos_list` returning 401 consumes one call even if renewal fails; a refused renewal before admission consumes none. A later local metadata-save failure cannot undo or add consumption.

## Derived from

- [Authorization ARS-001](../specifications/ars-001-authorization-state-dispatch-and-send-guard.md), request admission, read recovery and actual-call evidence.
- Focused auth-dispatch finding `fe6e092a-7da8-476a-8be6-0028a7d154fc`, Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`; source reads below independently confirm the accounting defect.

## Satisfies

| Authority | Treatment in this decision |
|---|---|
| [BR-018](../../../requirements/yuhaul/br-018-api-consumption-accounting-and-visibility.md) AC1, AC6 | Actual operation, cost and attempt timestamp go to the existing durable ledger once per request. Recording failure is reported, never silent success. |
| BR-018 AC2–5 | Existing quota views/window logic remain responsible; this decision adds no new UI or provider quota estimate. |
| [OC-001](../../../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) G6–7 and accounting invariant | Each admitted call has a safe outcome category; budget refusal has no call. Existing response dispositions and retention remain unchanged. |
| [BR-034](../../../requirements/yuhaul/br-034-automatic-youtube-token-refresh.md) AC2, AC7–8; [OC-006](../../../requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md) G4–6 | Original read failure, renewal failure and accounting failure stay distinct. Renewal is not consumption or first-call success. Existing authorization ARD/ARS owns the rest of renewal and mutation safety. |

No requirement is intentionally deferred by this decision. Missing category storage and failure-path wiring are implementation gaps, not exemptions.

## Drivers and constraints

Source inspected on 2026-11-07 in the shared working tree:

- `internal/metadata/manager.go`, `RefreshOnDemand` and `refreshCandidates`, writes quota only after fetch and Store A refresh succeed. This misses remote failures, empty on-demand responses and local-save failures.
- `internal/metadata/models.go`, `Fetcher`, returns metadata/error; its caller does not know actual transport admission or internal retries.
- `cmd/server/main.go`, `metadataFetcher`, binds authorization and builds a rejection-aware client without a recorder. `playlistProvider` already wires a client recorder.
- `internal/youtube/client.go`, `get`, records before body decoding but only after `httpClient.Do` returns without error. An admitted network error is therefore missed. `authorizationTransport` receives admission even when dispatch returns an error; its caller currently loses that fact.
- `internal/quota/quota.go`, `RecordCalls`, stores operation/count/cost and recording time, but no response category or actual admission timestamp. Planned counts cannot satisfy OC-001 G6.

Semantic navigation was attempted but degraded: gopls reported `authorityTestTransport is not a function` in an auth test. Bounded direct source reads were used. These are source findings, not executed-runtime evidence.

## Options considered

| Option | Benefits | Costs and failure modes | Obligation impact |
|---|---|---|---|
| Manager records a fetcher's detailed attempt report | Keeps all ledger calls in manager; could work with complete reports on every return path. | Requires changing every Fetcher implementation/fake, returning admission/category/time and partial-attempt evidence on errors, then remembering to record before every local failure. Other read callers still need equivalent machinery. | Viable only with full reports, not planned counts. More coupling than needed. |
| YouTube read adapter records actual attempts; manager never records consumption | Reuses the existing client-recorder seam and transport admission; one owner sees all HTTP, body/decode and network outcomes. Works for standalone metadata and playlist reads. | Requires carrying admission through `Do` errors and extending read recording to outcome/time. Manager tests must stop expecting planned fetch counts. | Chosen: smallest complete ownership boundary. |
| Add a fetcher recorder but retain manager records, or record only successful fetches | Smallest patch. | Double charges successes or misses failures; a batch estimate cannot distinguish refusal, retry or partial success. | Not viable. |

## Decision

Use the existing YouTube read adapter as the **sole actual-call accounting owner**. The quota ledger remains the persistence owner. The standalone fetcher supplies the ledger-backed recorder when constructing its authorized client, just as playlist listing supplies one; it does not record again after `GetVideos`. Remove both metadata-manager consumption writes together with this wiring. The manager retains `CanAfford`, candidate selection, Store A refresh, TTL and refreshed-record count.

Keep `metadata.Fetcher`'s metadata/error signature. Its contract becomes: the concrete provider adapter accounts for actual attempts on every return path; callers must not infer or record calls from requested IDs, returned rows, nil error or configured batch size. Local-only fakes consume no quota unless explicitly exercising the real adapter path. Production construction must require the ledger-backed recorder; a nil/no-op recorder is not an acceptable standalone-production configuration.

### Minimal attempt and error seam

1. Inside one explicit read attempt, retain request-local admission facts from the authorization transport: admitted/not-admitted, admission UTC time and the existing receipt. Preserve these facts even when `httpClient.Do` returns a network/cancellation error or discards a response. Do not infer admission from error class or from an earlier token resolution. No reusable permit, persistent read receipt table or manager-level attempt framework is needed.
2. Immediately before admission, honor existing quota/context/authorization checks for **each** actual attempt. A manager preflight grants no later permission. Explicitly refuse automatic redirects and hidden read retries on these bound clients; a second request is allowed only through the existing explicit read-recovery contract, with its own quota check and attempt evidence. Do not count OAuth exchange as a Data API request.
3. Finish each admitted attempt exactly once through the client-owned recorder, after deriving its bounded response category and before returning the attempt result. Recorder input is one operation, `call_count=1`, operation unit cost, actual admission timestamp, optional HTTP status, category and existing optional run attribution. Standalone lifecycle/on-demand calls use no run ID; never invent one from a current/latest run. No-admission has no consumption entry. Batch IDs are not call counts.
4. Category is one of OC-001's `success`, `quota`, `transient`, `auth`, `permanent`. Use typed status/structured-reason classification, not provider prose: 2xx is response success, 401/typed `authError` is auth, typed quota exhaustion is quota, admitted network failure/timeout/5xx/rate-limit is transient, and other complete nonretryable rejections are permanent. Unreadable rejection bodies use the bounded status classification without guessing `authError` or quota. Missing response after admission is transient. A 2xx malformed payload/body-read failure remains a successful HTTP response category but a failed read result; it cannot become successful metadata retrieval. Category records provider response meaning, not local save success or mutation application certainty.
5. Extend the existing ledger append path to accept category and actual attempt time. Persist these alongside operation/count/cost; derive the quota bucket from attempt time, not a later save time. Legacy rows remain explicitly unknown in any added nullable category field, never backfilled as success. Existing totals continue summing those rows once. This is one ledger, not an additional consumption store. Existing mutation accounting ownership is unchanged; read wiring must not capture/charge a guarded POST a second time.
6. Keep the read result and optional accounting/renewal failures distinct in a typed internal error/report. The read's safe primary failure remains inspectable when recorder or rejection-observer renewal also fails; `record quota` must not replace a 401/403/network fact. A successful provider read with failed recording returns a bounded accounting failure, not a nil error. Its consumption is not claimed durable. Preserve safe typed classification, not raw body/error chains. The existing `Fetcher` error can carry this report; the manager need not import HTTP or count attempts.
7. The manager applies Store A updates only after a successful fetch result. As today, a fetch error leaves existing metadata unchanged; this includes accounting failure. A successful fetch with zero rows has still consumed its actual call, even if on-demand refresh reports no data. A local `Store.Refresh` failure reports local failure without undoing the already recorded call. `refreshed` counts saved records, never calls, quota units or provider success. Local display, expiry and cleanup remain provider-free and quota-free.

The error/report is not retry permission. A caller must not rerun a provider call to repair a failed ledger write. If existing bounded read recovery issues a second request, the rejected first request and the second request each receive their own entry; renewed authorization never rewrites the first as success. A second-attempt refusal adds zero entries.

## Final-admission correction

[ADR-004](adr-004-atomic-quota-admission-and-single-attempt-transport.md) realizes the subsequently demonstrated concurrent-admission/HTTP2 retry gaps with shared process-local capacity holds and explicit HTTP/1-only transport. Its exact contract supersedes any interpretation that moving a non-atomic preflight alone is sufficient. Sole actual-call owners remain unchanged; capacity holds are not consumption entries or a new durable reservation system.

## Consequences

Immediate benefit: failures and local-save failures no longer disappear from consumption, and successful requests are not double charged. Fetcher callers do not acquire HTTP/admission knowledge. Concrete cost: coordinated manager/wiring changes, request-local admission capture, outcome-aware ledger append/migration and updated tests. No new service, queue, reservation system, background retry loop or distributed exactly-once protocol is justified.

Exactly-once here means one accounting invocation/entry for each observed admitted request on the completed in-process path, not atomicity between an external network effect and SQLite across process death. A crash or unavailable ledger can prevent durable evidence; report known accounting failure and do not claim complete accounting. This decision does not waive BR-018/OC-001 or certify crash-complete consumption recovery. If such recovery is required for acceptance, its unresolved persistence/network boundary must be separately realized, not hidden by a success-only recorder or guessed counts. No live operation is authorized.

## Confirmation and reconsideration

Required controlled-provider tests through the **real manager → standalone fetcher → authorized client → SQLite ledger** path, plus focused client boundary tests:

- Successful on-demand and lifecycle refresh: one actual request and one ledger entry, correct units/time/category, metadata saved; no manager duplicate.
- Empty 2xx, invalid payload, truncated 2xx body and local Store A save failure: one entry each; truthful fetch/save failure and unchanged local data where applicable. Exercise configured batching within the API's 50-ID maximum; local rejection of more than 50 IDs has zero calls, not an estimated count.
- 401, typed auth-403, quota-403, permanent-403, 5xx, rate limit and admitted network/timeout/cancellation: each admitted attempt recorded once with correct safe category, even if renewal/rejection observation fails. Pre-admission Bind/Resolve/save/authority/quota/cancellation refusal and empty ID list: zero transport calls and zero consumption.
- When existing read recovery makes a second attempt, first rejection remains auth, second success is separate; second-attempt quota refusal sends/records no second request. No hidden redirects or retries evade the per-attempt seam.
- Recorder failure combined with auth/network failure preserves both typed facts, does not save metadata or call the provider again, and never reports durable accounting success. Inject canceled context during recording and observe truthful persistence failure, not a fabricated row or unlimited background write.
- Two lifecycle batches with first success and later failure preserve earlier metadata/consumption and count the later admitted failure, not unattempted tail batches. Restart readback preserves rows/categories/times and legacy totals without inferred categories. Cross-window delayed completion buckets the call at admission.
- Inject token/raw-body markers; ledger, logs, errors/API/UI diagnostics remain safe. Local display/expiry/cleanup paths make no provider calls and add no ledger rows.

Reconsider if an adapter can make multiple underlying requests outside the explicit attempt boundary, if a second caller also charges these reads, or if crash-complete accounting becomes an acceptance dependency. Do not add speculative machinery before that premise is established. Bounded docs-only critic review `de23c4f8-3d9b-4a86-98d9-76d053081c10` reports APPROVED/SOUND, with no material flaw or simpler complete alternative. This is contract review, not runtime or whole-system accounting acceptance. No runtime tests were executed for this decision.

## Open questions

No user-owned policy blocks the ownership repair. Integrator owns outcome-aware ledger realization, the coordinated manager/fetcher/client changes and assembled-path proof. Whole-system consumption completeness, especially interrupted persistence, remains unverified; this bounded document does not close it.
