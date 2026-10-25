---
type: requirement
title: BR-010 — Duplicate Provenance for Discovery Credit
description: Duplicate provenance retained to distinguish discovery credit from other source relevance
tags: [requirement, duplicate, provenance, discovery-credit, priority:MUST]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL retain provenance for each observed duplicate match so that discovery credit for the earlier native observation in that matched pair is distinguishable from source relevance for the later observation. This is pairwise attribution, not a claim to the globally earliest origin of a fuzzy-duplicate group.

## Acceptance criteria

1. When a duplicate is detected, the system retains the identities of the evaluated video and the selected distinct reference video, with their available channel associations. The selected reference is not necessarily the earlier discovery.
2. When independent native discovery evidence for both members establishes a strict chronological order, the earlier observation's channel receives discovery credit for that match and the later observation's channel receives source-relevance credit, regardless of which member was selected as the reference.
3. Both video copies and their channel associations remain identifiable. The later member receives no discovery credit for that match; attribution in another observed match is assessed independently, not propagated through a chain of fuzzy matches.
4. Provenance information is available in the evaluation history and statistics.
5. Provenance data survives across runs and restarts.
6. Importing a corpus record does not establish which channel first discovered the content or confer discovery credit. A supplied channel identifier is an association only; any credit requires independent native discovery evidence under AC2. The shared import/reference boundary is specified in [OC-004](oc-004-imported-history-as-duplicate-reference.md).
7. If either member lacks native discovery evidence, or their discovery times are equal, unavailable, or cannot be ordered reliably, chronology-based discovery and relevance attribution for that match remain unknown. Reference direction and imported associations are not substitutes for that evidence.
8. Attribution covers the observed selected match. It does not require enumerating or crediting every eligible candidate, constructing a canonical fuzzy-equivalence group, or assigning one global group origin. Fuzzy similarity is not assumed transitive: matches A–B and B–C do not establish an A–C match or transfer A's credit to the B–C event.

## Derived from

- [Anchor](../../anchors/youtube-discovery-routing.md) (AC10).
- Explicit user decision, 2026-10-25: “Ok. i will follow your recommendation” accepted the recommendation to retain attribution per observed match, keep copies/channel associations, and not assert a global fuzzy-cluster origin. This resolves the previously open pairwise-versus-group-global meaning; it does not select duplicate skip/continue behavior or an excluded-video evidence policy. The preceding A/B/C exchange was a feasibility question, not those policy decisions.
- Subsequent explicit user decision, 2026-10-25: “Continue, yes” settles duplicate route/action continuation in [BR-008 AC9–AC11](br-008-exact-and-fuzzy-duplicate-detection.md). It leaves this pairwise provenance requirement in force; a matching copy may itself be routed without erasing either identity or changing evidence-based credit. It does not decide fully excluded-video evidence or native-reference eligibility.

## Verified by

- Pairwise test: for a selected distinct pair with strictly ordered native observations, verify earlier-channel discovery credit and later-channel relevance credit; reverse reference direction and verify roles still follow chronology.
- Evidence-boundary test: absent native evidence or equal/unorderable times yields unknown chronology-based attribution, not credit from an imported association.
- Nontransitive-chain test: with observed A–B and B–C matches but no A–C match, verify each selected pair's attribution independently, preserve copies/channels, and make no global-origin claim. A selected match need not include all matching candidates.
- Provenance test: importing a row with or without a channel identifier does not by itself assign that channel discovery credit; verify the reference identity and attribution boundary in [OC-004](oc-004-imported-history-as-duplicate-reference.md).
- History test: provenance data available in evaluation records.
- Persistence test: provenance survives restart.

## Depends on

- BR-008 (duplicate detection identifies originals and duplicates)
- BR-009 (historical corpus provides matching baseline)
