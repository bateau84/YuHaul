---
type: requirement
title: BR-008 — Exact and Fuzzy Duplicate Detection
description: Historical duplicate detection via exact title matching and configurable fuzzy similarity
tags: [requirement, duplicate, detection, fuzzy, exact, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL detect historical duplicates of videos using exact title matching and configurable fuzzy similarity, allowing the user to adjust the similarity threshold and method without requiring code changes.

## Acceptance criteria

1. The system detects exact title matches against the historical corpus.
2. The system detects fuzzy title similarity using a configurable algorithm and threshold (default high-similarity, compatible with prior Levenshtein-style ~97% behavior).
3. The similarity threshold is user-configurable through the administration interface.
4. The similarity algorithm is not hardcoded; the system supports substituting or tuning the matching method.
5. Duplicate detection operates against the full historical corpus, not only the current run's discoveries.
6. A video identified as a duplicate is flagged with a reference to the original video it duplicates.
7. Duplicate detection is evaluated after exclusion filtering and before route evaluation.

## Derived from

- Anchor: youtube-discovery-routing.md (AC8, Context: previous implementation used Levenshtein-style similarity with ~97% threshold; rebuilt system must provide equivalent capability without binding to that exact algorithm)

## Verified by

- Unit test: exact title match identifies duplicate.
- Unit test: fuzzy match at configured threshold identifies duplicate; below threshold does not.
- Unit test: threshold adjustment changes detection sensitivity.
- Unit test: duplicate detection runs after exclusion, before route evaluation.

## Depends on

- BR-009 (historical title persistence provides the corpus)
- BR-007 (exclusion filtering runs before duplicate detection)
