---
type: requirement
title: BR-015 — Explainable Route Decisions
description: Every meaningful route decision explainable from durable evidence
tags: [requirement, explainability, decisions, evidence, priority:MUST]
timestamp: 2026-11-10T00:00:00Z
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
7. Decision evidence is sufficient to explain the recorded evaluation outcome without re-querying external APIs. The retained app-owned evidence identifies each route's disposition, the conditions and exclusions involved, their comparison meaning, their configured values and the recorded match or non-match. The system SHALL NOT retain historical snapshots of provider-derived inputs, such as titles, descriptions or durations as evaluated, in order to reproduce past comparisons. When inspection shows an input value for a past decision, it SHALL show it either as current/local information, labeled as such and never as the value evaluated then, or as “unavailable”. A difference between today's current/local value and the recorded result is not a contradiction and SHALL NOT change the recorded decision.
8. Inspection makes the video recognizable from permitted available title and source-channel context, distinguishes that context from the input actually evaluated, and identifies missing evidence explicitly. Evidence producers and inspection consumers SHALL conform to [OC-005 — Decision Evidence Attribution and Availability](oc-005-decision-evidence-attribution-and-availability.md).
9. A decision-detail read that ends in failure or not-found does not remain presented as loading or become a routing result. The user can retry a failed read and return to the originating run. Retrying inspection does not initiate another evaluation or external metadata refresh.
10. Every newly evaluated route decision in production, dry-run, replay or backtest that skips a route because of an exclusion SHALL retain and display the reason and the specific first causal exclusion occurrence from that run's immutable configuration. Generic “matched route-scoped exclusion rule” text alone does not satisfy this requirement. OC-005 defines the shared evidence meaning; this requirement does not change exclusion matching or order.
11. Whenever list or detail presents an exclusion skip, the user SHALL be able to identify the affected route, the causal rule and the comparison meaning, including the configured value or threshold of the matched rule, subject only to the accepted generic-only legacy boundary in AC13. The configured value is the user's own configuration, so it is eligible for release and SHALL be shown in list and detail. It SHALL NOT be withheld on provider-data or diagnostic-safety grounds, and it SHALL be displayed as text. It is “unavailable” only when the rule definition itself cannot be resolved, as for a legacy generic-only record or a missing snapshot. Configured values SHALL be distinguished from inputs, which follow AC7. Missing input SHALL be explicit and SHALL NOT erase a supported causal rule. This partial explanation does not satisfy AC7 by itself.
12. The whole-video outcome and each route's disposition SHALL remain separate. For example, a video's recorded overall rejected outcome may coexist with one route's condition non-match and another route's exclusion skip. Inspection explains each recorded route cause; it does not call the exclusion the cause of rejection by every route. If another route matches, the excluded route still does not establish whole-video exclusion.
13. An old generic-only skip, including any such Run15 record, SHALL remain explicitly incomplete. List and detail SHALL display “rule unavailable” while preserving the supported skip and overall outcome. It SHALL NOT acquire a historical rule through guesswork, current or snapshot configuration, or reevaluation, even if one rule seems plausible. The user accepts this bounded exception to complete historical exclusion explanation under AC3/AC7/AC11; it is not a claim that the missing cause or input was recovered. It does not exempt any newly evaluated exclusion under AC10, including a new evaluation of a video with old history. Reuse of an old decision preserves that decision's original incompleteness. AC7 governs how inputs are shown.

## Derived from

- Anchor: youtube-discovery-routing.md (AC15)
- User's Run15 decision-detail feedback, relayed in the 2026-11-06 specification assignment: video/channel IDs, a duration dash, and “matched route-scoped exclusion rule” do not let the user understand the video or why it was excluded. The image was not independently available in this specification session. AC8–9 clarify recognition and inspection recovery within this request; they do not authorize new data retention or external refresh.
- Specification choices: explicit unavailable versus failed-read states, read-only retry, and the evidence attribution semantics in OC-005. Specific exclusion/condition evidence follows existing AC1–3, not the proposed UI layout or an implementation's available fields.
- User's explicit 2026-11-07 request, relayed in this assignment: “always displaying reason and rule of exclusion needs to be a requirement”; screenshot question: “which rule in route made it skip?” The screenshot was not independently viewed. AC10–13 make this mandatory for newly evaluated production and simulation decisions. First causal occurrence and unchanged run configuration refine the already accepted OC-005 causal-match choice; they do not introduce a new rule priority.
- Subsequent user decision, relayed verbatim in the 2026-11-07 specification assignment: asked whether old exclusion records may say “rule unavailable”, the user answered “Yes, that is ok.” This authorizes only AC13's explicitly incomplete generic-only legacy boundary, not weaker capture for new decisions or a provider-retention exemption. The original human conversation was not independently inspected. Provenance is recorded in Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`, task `decision-evidence-contract`, Loom claim `3966066e-c6d4-4cf0-a197-695f698f3876`.

- User's explicit decisions of 2026-11-10, relayed in the specification assignment (Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`); the original human conversation was not independently inspected. (a) Historical inputs: “accept current/local information for old runs”; past inputs are shown as current/local or “unavailable”, with no retention of historical provider snapshots. This replaces AC7's former promise to reproduce the evaluation outcome. (b) Configured exclusion values: “the configured value of the matched rule must be shown”; it is the user's own configuration, so release is eligible. This replaces AC11's “permitted” qualifier.

## Verified by

- Unit test: video decision record contains route-by-route evaluation path.
- Unit test: rejection record identifies the failing condition(s).
- Persistence test: decision evidence survives restart.
- Explanation test (AC7): the recorded outcome and causal path are explained from persisted evidence alone with zero YouTube calls. Change the local title after evaluation: inspection shows the new title labeled current/local, never as the evaluated title, and the recorded decision is unchanged. Remove the local metadata: the input shows “unavailable”. No historical copy of provider input is created by evaluation for this purpose.
- Inspection test: available local title/channel context is recognizable; absent/expired context is explicit, IDs remain labeled identifiers, and a changed current title cannot masquerade as the evaluated title. Apply OC-005's composed verification cases.
- Recovery test: failed detail read ends loading with safe failure feedback and working retry/back navigation; not-found is distinct. Recovery performs only inspection reads, not a new run or YouTube call.
- Exclusion acceptance: execute OC-005 verification cases 6–10 through capture, durable history, restart and list/detail. A new successful generic-only exclusion decision, a wrong snapshot occurrence, a list that hides the available causal rule, or a resolved rule whose configured value is withheld fails acceptance even if detail is correct. Configured values containing markup, quotes or non-ASCII text are shown as the same literal text. Runtime implementation is not established by this amendment.

## Depends on

- BR-003–BR-006 (route evaluation provides decision logic)
- BR-032 (outcome classification vocabulary)

## Reconciliation limits

- **2026-11-10 user decisions supersede the earlier open retention and value-release questions.** AC7 now promises explanation, not reproduction of provider-input comparisons. Inputs for past decisions are current/local or “unavailable”. No historical provider-input snapshot is retained, and no retention extension or provider-policy exemption is sought. AC11's configured value is shown because it is the user's own configuration. These decisions do not change exclusion matching, causal capture for new decisions (AC10), the legacy boundary (AC13) or the separation of route and video outcomes (AC12).
- AC1–6 and AC8–10, their sources, priority and verification remain unchanged. AC13's user-approved generic-only legacy boundary is unchanged: those records remain incomplete, and current/local information does not complete them. Neither a fingerprint nor a freshly fetched title is a substitute for a recorded fact, and inspection never calls YouTube.
- **BR-009 is not changed by this decision.** Its durable title record serves duplicate detection; it is not a historical-input snapshot for decision inspection. Its durable-title promise still conflicts with the architecture's metadata-expiry model; that duplicate-detection question remains open and is not decided here.
- The designer's Backtest comprehension report is a proposed experience refinement, not a new authority for layout, mandatory thumbnails, full descriptions, separate subscription names, playlist display names, or other unavailable facts. Date selection is now specified in [BR-027 AC8–13](br-027-historical-backtesting.md). Run15 SQLite contention, production comparison and evaluation behavior are outside this amendment.
- **Aligned realization:** the accepted [causal-exclusion ADR](../../architecture/decision-evidence/decisions/adr-001-capture-causal-exclusion-snapshot-reference.md) and [ARS-002](../../architecture/decision-evidence/specifications/ars-002-causal-exclusion-capture-and-inspection.md) identify future causal occurrences without storing provider input, which now fits AC7. ARS-002 is updated for configured-value release. Schema, reference format and capture mechanics remain Architect-owned.
- **Superseded history:** earlier versions of this section held AC7 reproduction and BR-009 durable titles open against provider retention and required a user-approved bounded-history amendment. The 2026-11-10 decision is that amendment for AC7. The legacy scope decision of 2026-11-07 (“Yes, that is ok”) stays in force.
