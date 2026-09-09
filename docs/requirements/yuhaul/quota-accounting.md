---
type: requirement
title: Quota Accounting
description: External API resource consumption must be accounted for by operation type, per run, cumulatively, with remaining capacity visible.
tags: [requirement, quota, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL account for all external API resource consumption by operation type, tracking cost per operation, per run, and cumulatively, and SHALL present remaining quota capacity and quota bucket reset information to the user.

## Acceptance criteria

1. The system tracks resource cost by operation type (e.g., playlist insertions, search queries, subscription list reads) rather than only counting API calls
2. The system records resource consumption per run, broken down by operation type
3. The system maintains cumulative resource consumption across runs
4. The system reports remaining quota capacity and quota bucket reset timing where the API provides this information
5. Resource consumption data is retained across runs for historical review
6. The user can view resource consumption through the administration interface

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 18)

## Verified by

- Test: after a processing run, the system reports resource consumption by operation type, not only total API call count
- Test: cumulative resource consumption is available across multiple runs
- Test: remaining quota capacity and reset timing are reported when available from the API
- Test: resource consumption data persists across application restarts

## Depends on

- None
