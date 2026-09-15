---
type: requirement
title: BR-018 — API Consumption Accounting and Visibility
description: External API consumption tracked and visible to the user
tags: [requirement, api, quota, accounting, visibility, priority:MUST]
---

**Status:** proposed
**Priority:** MUST

## Statement

The system SHALL track and present external YouTube API consumption so the user can see how much quota has been used, by which operations, and when.

## Acceptance criteria

1. Every YouTube API call is accounted for with the operation type, quota cost, and timestamp.
2. Cumulative quota consumption is visible for the current quota window.
3. The user can view API consumption broken down by operation type (channel enumeration, video listing, playlist mutation, etc.).
4. The user can view API consumption history across multiple quota windows.
5. The system exposes remaining quota estimate when available from the YouTube API.
6. API consumption data is durable and survives restarts.

## Derived from

- Anchor: youtube-discovery-routing.md (AC18)

## Verified by

- Unit test: API call recorded with operation type, cost, and timestamp.
- History test: consumption visible across quota windows.
- Persistence test: consumption data survives restart.

## Depends on

- OC-001 (YouTube API quota semantics)
