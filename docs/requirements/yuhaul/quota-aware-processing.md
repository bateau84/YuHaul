---
type: requirement
title: Quota-Aware Processing
description: Processing must be quota-aware, avoiding unnecessary external API expenditure.
tags: [requirement, quota, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL avoid unnecessary external API expenditure by incorporating quota awareness into its processing decisions, avoiding redundant or avoidable API calls.

## Acceptance criteria
1. The system does not make API calls for videos already fully processed
2. The system does not make redundant API calls for information already obtained and cached within a run
3. The system defers processing when quota is insufficient rather than making partial calls that cannot complete
4. Processing decisions consider remaining quota and avoid expenditure that would leave insufficient quota for higher-priority work

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 19)

## Verified by

- Test: re-running on already-processed videos produces no new API calls
- Test: the system does not exceed configured quota limits
- Test: when quota is low, the system defers rather than makes incomplete API calls

## Depends on

- quota-accounting.md
- quota-deferral.md
