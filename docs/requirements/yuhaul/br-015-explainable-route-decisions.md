---
type: requirement
title: BR-015 — Explainable Route Decisions
description: Every meaningful route decision explainable from durable evidence
tags: [requirement, explainability, decisions, evidence, priority:MUST]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL retain durable evidence for every meaningful route decision so that the user can understand why a video was accepted, rejected, deferred, excluded, or failed, using only the system's persisted records.

## Acceptance criteria

1. For every video processed, the system records the evaluation path: which routes were evaluated, which conditions passed or failed, and the resulting action or disposition.
2. Decision evidence includes the specific condition(s) that caused rejection or acceptance.
3. Decision evidence includes any exclusion rules that prevented evaluation.
4. Decision evidence includes duplicate detection results and provenance.
5. Decision evidence is durable and survives across runs and restarts.
6. The user can query a video by identifier and retrieve the full decision history.
7. Decision evidence is sufficient to reproduce the evaluation outcome without re-querying external APIs.
8. Inspection makes the video recognizable from permitted available title and source-channel context, distinguishes that context from the input actually evaluated, and identifies missing evidence explicitly. Evidence producers and inspection consumers SHALL conform to [OC-005 — Decision Evidence Attribution and Availability](oc-005-decision-evidence-attribution-and-availability.md).
9. A decision-detail read that ends in failure or not-found does not remain presented as loading or become a routing result. The user can retry a failed read and return to the originating run. Retrying inspection does not initiate another evaluation or external metadata refresh.
10. Every newly evaluated route decision in production, dry-run, replay or backtest that skips a route because of an exclusion SHALL retain and display the reason and the specific first causal exclusion occurrence from that run's immutable configuration. Generic “matched route-scoped exclusion rule” text alone does not satisfy this requirement. OC-005 defines the shared evidence meaning; this requirement does not change exclusion matching or order.
11. Whenever list or detail presents an exclusion skip, the user SHALL be able to identify the affected route, the causal rule and the comparison meaning, including the permitted configured value or threshold, subject only to the accepted generic-only legacy boundary in AC13. Supported historical input SHALL be distinguished from configured values and current display context. Missing or withheld input SHALL be explicit and SHALL NOT erase a supported causal rule. This partial explanation does not satisfy AC7 by itself.
12. The whole-video outcome and each route's disposition SHALL remain separate. For example, a video's recorded overall rejected outcome may coexist with one route's condition non-match and another route's exclusion skip. Inspection explains each recorded route cause; it does not call the exclusion the cause of rejection by every route. If another route matches, the excluded route still does not establish whole-video exclusion.
13. An old generic-only skip, including any such Run15 record, SHALL remain explicitly incomplete. List and detail SHALL display “rule unavailable” while preserving the supported skip and overall outcome. It SHALL NOT acquire a historical rule through guesswork, current or snapshot configuration, or reevaluation, even if one rule seems plausible. The user accepts this bounded exception to complete historical exclusion explanation under AC3/AC7/AC11; it is not a claim that the missing cause or input was recovered. It does not exempt any newly evaluated exclusion under AC10, including a new evaluation of a video with old history. Reuse of an old decision preserves that decision's original incompleteness. Provider-derived historical-input retention remains separately unresolved.

## Derived from

- Anchor: youtube-discovery-routing.md (AC15)
- User's Run15 decision-detail feedback, relayed in the 2026-11-06 specification assignment: video/channel IDs, a duration dash, and “matched route-scoped exclusion rule” do not let the user understand the video or why it was excluded. The image was not independently available in this specification session. AC8–9 clarify recognition and inspection recovery within this request; they do not authorize new data retention or external refresh.
- Specification choices: explicit unavailable versus failed-read states, read-only retry, and the evidence attribution semantics in OC-005. Specific exclusion/condition evidence follows existing AC1–3, not the proposed UI layout or an implementation's available fields.
- User's explicit 2026-11-07 request, relayed in this assignment: “always displaying reason and rule of exclusion needs to be a requirement”; screenshot question: “which rule in route made it skip?” The screenshot was not independently viewed. AC10–13 make this mandatory for newly evaluated production and simulation decisions. First causal occurrence and unchanged run configuration refine the already accepted OC-005 causal-match choice; they do not introduce a new rule priority.
- Subsequent user decision, relayed verbatim in the 2026-11-07 specification assignment: asked whether old exclusion records may say “rule unavailable”, the user answered “Yes, that is ok.” This authorizes only AC13's explicitly incomplete generic-only legacy boundary, not weaker capture for new decisions or a provider-retention exemption. The original human conversation was not independently inspected. Provenance is recorded in Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`, task `decision-evidence-contract`, Loom claim `3966066e-c6d4-4cf0-a197-695f698f3876`.

## Verified by

- Unit test: video decision record contains route-by-route evaluation path.
- Unit test: rejection record identifies the failing condition(s).
- Persistence test: decision evidence survives restart.
- Reproduction test: decision can be reconstructed from persisted evidence alone.
- Inspection test: available local title/channel context is recognizable; absent/expired context is explicit, IDs remain labeled identifiers, and a changed current title cannot masquerade as the evaluated title. Apply OC-005's composed verification cases.
- Recovery test: failed detail read ends loading with safe failure feedback and working retry/back navigation; not-found is distinct. Recovery performs only inspection reads, not a new run or YouTube call.
- Exclusion acceptance: execute OC-005 verification cases 6–10 through capture, durable history, restart and list/detail. A new successful generic-only exclusion decision, a wrong snapshot occurrence, or a list that hides the available causal rule fails acceptance even if detail is correct. Runtime implementation is not established by this amendment.

## Depends on

- BR-003–BR-006 (route evaluation provides decision logic)
- BR-032 (outcome classification vocabulary)

## Reconciliation limits

- AC1–7, their sources, priority and verification remain unchanged except for the explicit, user-approved generic-only legacy boundary in AC13. Those records remain incomplete, not reproducible. Outside that boundary, honest unavailable history does not satisfy AC7 where the retained evidence cannot reproduce the outcome. Neither a fingerprint nor a freshly fetched title is a substitute for the historical evaluated value.
- Accepted system architecture INV-5 and §Two-Store Data Model expire raw API metadata; §Evidence display permits fingerprint fallback and external refresh. That realization does not establish AC7 conformance and conflicts with presenting a fingerprint as a title or repairing Backtest inspection through YouTube. OC-005 does not extend retention, grant a provider-policy exemption, or authorize external calls. Compliance/research must establish permitted evidence retention; Architect must realize it or surface infeasibility. Only the product owner can accept weaker historical explainability. BR-009's durable-title promise also remains binding and unresolved against that expiry model; this amendment does not silently replace it with permanent hashes.
- The designer's Backtest comprehension report is a proposed experience refinement, not a new authority for layout, mandatory thumbnails, full descriptions, separate subscription names, playlist display names, or other unavailable facts. Date selection, Run15 SQLite contention, production comparison and evaluation behavior are outside this amendment.
- **Aligned, partial realization only:** the accepted [causal-exclusion ADR](../../architecture/decision-evidence/decisions/adr-001-capture-causal-exclusion-snapshot-reference.md) can identify future causal occurrences independently of raw provider-input history. Its reference-only scope cannot weaken AC7 or satisfy full reproduction. Schema, reference format and capture mechanics remain Architect-owned. Earlier architecture/ledger excerpts were supplied before this amendment; no new obligation is derived from them.
- **Resolved legacy scope; independent retention boundary remains:** the user's “Yes, that is ok” accepts existing generic-only history displaying “rule unavailable” under AC13. No recoverable historical cause for Run15 is asserted and no reconstruction is authorized. Separately, if permitted metadata retention cannot meet AC7 outside that legacy boundary and BR-009 AC1–2, the user must accept an exact bounded-history amendment or require another compliant realization. BR-009 AC7 concerns imported title/identifier completeness, not a retention exemption; the reproduction conflict is BR-015 AC7. Compliance/research and Architect must establish the permitted realization before any necessary product amendment. Product preference cannot authorize prohibited retention. This remaining boundary does not block the independent future causal-rule improvement; it blocks claiming complete historical reproduction.
