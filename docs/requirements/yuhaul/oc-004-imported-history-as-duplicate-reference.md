---
type: obligation-contract
title: OC-004 — Imported History as Duplicate References
description: Shared import, native-observation, duplicate-evaluation, and provenance semantics for imported historical records
tags: [obligation-contract, duplicate-detection, corpus, import, provenance]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted

## Participants

- **Historical Corpus Importer:** accepts valid prior-system records into YuHaul's historical corpus.
- **Native Discovery Persistence:** records a native observation before duplicate evaluation.
- **Duplicate Evaluator:** compares newly discovered videos with historical records and identifies a matching reference or no match.
- **Discovery Provenance Attribution:** distinguishes the reference used for matching from the channel that has evidence of earliest discovery.

## Preconditions

- A prior-system source record supplies the title and video identifier required by BR-009.
- The record is successfully added to the corpus; source records skipped because the same video identifier is already present follow BR-009 AC8.
- Duplicate evaluation uses the exact and fuzzy matching behavior configured under BR-008.

## Guarantees

1. Every valid imported record newly added to the corpus is available to the Duplicate Evaluator as a historical reference for exact and fuzzy matching. Its eligibility does not depend on evidence that YuHaul previously routed it.
2. If native discovery persists an observation for the same video identifier before duplicate evaluation, that write does not remove an imported record's eligibility in the historical reference corpus. However, the evaluator does not return that same-ID reference as a duplicate of the observation: the same video is not its own duplicate. The reference remains available for comparisons with different video identifiers under the configured matching rules.
3. A matching reference or preserved eligibility does not establish route completion or discovery credit. An optional channel identifier from the source remains a channel association; credit is attributed only when independent evidence identifies the earliest-discovered instance, consistent with BR-010.
4. A duplicate match, when returned, identifies a different video from the video being evaluated; it never identifies the evaluated video itself by the same video identifier.
5. An imported-reference match carries the same continuation meaning as any other distinct-video match under [BR-008 AC9](br-008-exact-and-fuzzy-duplicate-detection.md). Consumers do not infer suppression, completed playlist actions, or discovery credit from the match; [OC-002](oc-002-route-evaluation-outcome-semantics.md) governs outcome reporting.

## Semantic inputs and outputs

- **Input:** An accepted historical title/video-identifier record, with an optional associated channel; native discovery may persist an observation for the same video identifier before a discovered video is evaluated under the configured matching rules.
- **Output:** Either no duplicate match or a match identifying a different historical reference video. A same-ID historical row is not returned as a duplicate of the evaluated video. A match does not by itself carry a discovery-credit claim for the imported record's channel.

## Failure semantics

- A source row that is invalid or cannot be imported is not a matching candidate; BR-009 requires it to be accounted for as not imported with an explicit reason.
- A source row whose video identifier is already present is accounted for as already present and does not alter or reclassify the existing entry. Its eligibility remains the eligibility of that existing entry.
- If discovery provenance is unavailable, the imported channel association is not substituted for a known earliest-discovered channel.

## Invariants

- No valid newly added imported record is excluded from matching solely because the prior source does not establish current-system routing.
- An imported reference that is eligible for matching remains eligible after a same-ID native discovery observation is persisted before duplicate evaluation; neither that write nor a same-ID import retry implicitly revokes eligibility.
- A video identifier cannot be returned as a duplicate reference for an observation with that same identifier; imported reference eligibility does not permit self-matching.
- Matching-reference eligibility is distinct from route completion and discovery credit.
- Import order, import time, and an optional source channel identifier do not alone establish earliest discovery.

## Derived from

- [Anchor — YouTube Subscription Discovery & Routing](../../anchors/youtube-discovery-routing.md) (AC8–AC10)
- [BR-008 — Exact and Fuzzy Duplicate Detection](br-008-exact-and-fuzzy-duplicate-detection.md) (AC5–AC6)
- [BR-009 — Historical Title Persistence and Corpus Import](br-009-historical-title-persistence-and-corpus-import.md) (AC4, AC7–AC8)
- [BR-010 — Duplicate Provenance for Discovery Credit](br-010-duplicate-provenance-for-discovery-credit.md) (AC1–AC3)

## Verification semantics

Use the assembled import → native same-ID persistence → duplicate-evaluation path with a valid newly imported record, with the native observation persisted before matching. Verify that the imported reference remains eligible but is not returned as a duplicate of the same-ID observation. Then evaluate a different video identifier that satisfies the configured matching rules and verify that the imported reference remains available for that comparison. Verify that the same-ID import retry leaves the existing entry's eligibility unchanged, and that neither the native write, preserved eligibility, nor a match establishes route completion or discovery credit. An optional imported channel identifier does not by itself receive credit; any credit requires independent evidence of the earliest-discovered instance, consistent with BR-010. Invalid source rows do not become candidates.

For that distinct matching video, include an eligible matching route and observe continuation to its permitted playlist action, with separate match/provenance and route/action evidence. Repeat with a real exclusion or condition non-match and verify those rules still govern the result. This adds acceptance coverage, not a claim that the continuation composition has been verified.
