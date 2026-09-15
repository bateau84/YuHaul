---
type: requirement
title: BR-009 — Historical Title Persistence and Corpus Import
description: Historical titles persisted durably; existing corpus from prior system importable and preservable
tags: [requirement, persistence, titles, corpus, import, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL durably persist historical video titles and support importation of the existing title corpus from the prior application so that duplicate detection operates against the full historical record.

## Acceptance criteria

1. Every video title encountered during discovery is durably persisted with its associated video identifier and channel.
2. Historical titles survive application restarts and are not dependent on transient state.
3. The system supports bulk import of the existing historical corpus from the prior application.
4. Imported titles are treated identically to newly discovered titles for duplicate detection purposes.
5. The historical corpus is not silently truncated or discarded during migration or upgrade.
6. The user can inspect the size and recency of the historical corpus.

## Derived from

- Anchor: youtube-discovery-routing.md (AC9, Context: rebuild of existing application with historical data that should be preserved)

## Verified by

- Persistence test: titles survive restart and are queryable.
- Import test: bulk-imported titles are present in the corpus and matched by duplicate detection.
- Data integrity test: corpus size matches expected count after import.

## Depends on

- None
