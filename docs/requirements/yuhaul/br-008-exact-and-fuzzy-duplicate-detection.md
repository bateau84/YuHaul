---
type: requirement
title: BR-008 — Exact and Fuzzy Duplicate Detection
description: Historical duplicate detection via exact title matching and configurable fuzzy similarity
tags: [requirement, duplicate, detection, fuzzy, exact, priority:MUST]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL detect historical duplicates of videos using exact title matching and configurable fuzzy similarity, allowing the user to adjust the similarity threshold and method without requiring code changes. A duplicate match SHALL NOT itself suppress the distinct video's otherwise eligible route evaluation or playlist actions.

## Acceptance criteria

1. The system detects exact title matches against the historical corpus.
2. The system detects fuzzy title similarity using a configurable algorithm and threshold (default high-similarity, compatible with prior Levenshtein-style ~97% behavior).
3. The similarity threshold is user-configurable through the administration interface.
4. The similarity algorithm is not hardcoded; the system supports substituting or tuning the matching method.
5. Duplicate detection operates against the full historical corpus, not only the current run's discoveries. Imported-record eligibility is governed by the shared semantics in [OC-004](oc-004-imported-history-as-duplicate-reference.md).
6. A video identified as a duplicate is flagged with the selected distinct historical reference video it matches. This identifies the observed match, not a globally original video; discovery-credit meaning is governed by [BR-010](br-010-duplicate-provenance-for-discovery-credit.md).
7. Duplicate detection is evaluated after exclusion filtering and before route evaluation.
8. A discovered video is not classified as a duplicate of a historical reference with the same video identifier. A reported duplicate must refer to a different video; matching a record with the same video identifier is a self-match, not a duplicate.
9. After a distinct-video duplicate match, processing continues through otherwise applicable routes and their playlist actions. Duplicate detection and [BR-010](br-010-duplicate-provenance-for-discovery-credit.md) provenance remain required. Exclusions, route conditions and order, final-route termination, quota/failure handling, and completed-action idempotency still apply; a match neither forces acceptance nor proves an action completed. Production/recovery, replay/backtest, and dry-run follow this same continuation meaning within each mode's existing evidence/isolation boundaries. Simulation still performs no external playlist mutation or production corpus, provenance, idempotency, or statistics writes; continuation does not authorize such writes.
10. Continue is the supported duplicate-handling policy, not a default selectable against skip. Legacy stored configuration, restored snapshots, and otherwise valid configuration/proposal requests carrying the former duplicate-behavior setting remain usable without enabling suppression on a match or failing solely because they contain that retired setting. Omitting it also means continue. Existing immutable snapshots and historical outcome evidence are not rewritten to imply that they recorded a different value or that a past action occurred.
11. Active configuration and administration surfaces must not present the retired setting as an effective skip choice. If a compatibility representation remains visible, its effective meaning must be continue; historical legacy values must be distinguishable as recorded values, not an effective skip policy. This does not retire configurable similarity threshold or matching method.

## Derived from

- Anchor: youtube-discovery-routing.md (AC8, Context: previous implementation used Levenshtein-style similarity with ~97% threshold; rebuilt system must provide equivalent capability without binding to that exact algorithm)
- BR-009 AC1 and AC7 establish that a historical record carries the identifier of its source video; BR-008 AC6 identifies the selected distinct matched reference, not a globally original video. The same video identity cannot be its own duplicate reference.
- Explicit user decision, 2026-10-25, recorded in [BR-010](br-010-duplicate-provenance-for-discovery-credit.md): the user accepted pairwise attribution per observed selected match rather than a global fuzzy-group origin. AC6's reference wording is reconciled with that approval; it does not select duplicate route/action handling, excluded-video evidence, or native reference eligibility.
- Explicit user decision, 2026-10-25: “Continue, yes”, answering whether a distinct newly observed video matching an earlier one continues through eligible routes and playlist actions or skips them. AC9 records that decision and its necessary interaction with existing routing, isolation, and provenance obligations. It supersedes only AC6's configurable-handling promise in [the legacy requirement](duplicate-detection.md); it does not decide native-reference eligibility or require duplicate evidence for fully excluded inputs.
- Specification choice within that fixed policy: AC10–AC11 retire the misleading choice while preserving existing configuration/proposal usability and immutable history. No new skip mode, removal schedule, API field/value, schema, or persistence migration is prescribed. Exact compatibility realization belongs to the architect.
- Prior approved clean baseline: the [Critic Obligation Gate](../../ephemeral-reports/critic/yuhaul-obligation-gate/review.md) approved BR-001–BR-032, including AC7's after-exclusion/before-routing sequence. Anchor AC8 and the accepted design do not specify that order. The accepted [system architecture](../../architecture/yuhaul-system-architecture.md), §Processing Pipeline, now conforms to AC7; the former architecture-order conflict is closed. The continuation amendment leaves AC7 and the separate excluded-video evidence question unchanged.

## Verified by

- Unit test: exact title match identifies duplicate.
- Unit test: fuzzy match at configured threshold identifies duplicate; below threshold does not.
- Unit test: threshold adjustment changes detection sensitivity.
- Unit test: duplicate detection runs after exclusion, before route evaluation.
- Identity-boundary test: when an imported historical record and a discovered observation have the same video identifier, duplicate evaluation does not report that record as a duplicate of itself.
- Import/detection test: the assembled importer and detector conform to [OC-004](oc-004-imported-history-as-duplicate-reference.md): same-ID observations do not self-match, while an eligible imported reference remains available for matching a different video when the configured rules are satisfied.
- Continuation proof: use distinct IDs with a confirmed exact match, then a fuzzy match, and an eligible matching route. Observe retained duplicate/provenance evidence plus the route decision and permitted playlist action, not suppression on the match. Repeat with a partial route exclusion, a condition non-match, a final route, an already completed action, and an operational deferral/failure: each keeps its existing meaning. Verify recovery/replay/backtest and dry-run continuation with their existing mutation/isolation boundaries; do not count simulated actions as executed.
- Compatibility proof: existing stored/restored configurations and valid requests/proposals containing legacy behavior strings (including `skip`, `flag`, and empty), and configurations omitting the setting, all retain continuation. Verify truthful active readback/presentation, continued threshold sensitivity, and unchanged historical snapshot contents/outcome evidence. These are acceptance observations to perform, not completed verification claims.

## Depends on

- BR-009 (historical title persistence provides the corpus)
- BR-007 (exclusion filtering runs before duplicate detection)
