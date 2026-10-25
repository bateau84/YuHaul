---
type: obligation-contract
title: OC-001 — YouTube API Quota and Failure Semantics
description: Agreement between the system and the YouTube Data API v3 on how quota exhaustion and API failures are treated operationally
tags: [obligation-contract, youtube, api, quota, failure]
---

**Status:** accepted

## Participants
- **System** (YuHaul application): the party initiating YouTube Data API v3 calls for discovery, playlist management, and channel enumeration.
- **YouTube Data API v3** (external): the external API whose quota and error semantics constrain system behavior.

## Preconditions
- The user has authorized the system to make API calls on their behalf (BR-001).
- The system tracks quota consumption per API call (BR-018).
- Quota exhaustion is treated as operational state, not an error (Anchor Context).

## Guarantees
1. When the YouTube API returns a quota exhaustion response (HTTP 403 with `quotaExceeded` or equivalent), the system treats this as an operational deferral condition, not an error.
2. Work that would require additional quota after exhaustion is deferred with a quota-exhaustion reason and is re-queued for the next run.
3. The system does not retry immediately upon quota exhaustion; it waits for the next configured run or a user-initiated run.
4. When the YouTube API returns a transient error (HTTP 5xx, network timeout, rate limit), the system retains the affected work item for retry rather than dropping it.
5. When the YouTube API returns an authorization error (HTTP 401, 403 with `authError`), the system defers remaining work and prompts the user to re-authorize.
6. The system records the YouTube API response category (success, quota, transient, auth, permanent) for every API call in the consumption log.
7. The system does not make API calls that it knows will exceed remaining quota; it short-circuits and defers proactively when quota state indicates exhaustion is imminent.

## Semantic inputs and outputs
- **Input**: YouTube API response status and error category for each call.
- **Output**: Operational disposition (continue, defer, retry, prompt-reauth) and consumption log entry.

## Failure semantics
- **Quota exhaustion**: all pending API-dependent work deferred; no immediate retry; next run or manual run resumes.
- **Transient error (5xx, timeout)**: individual work item retained for retry; other work in the run continues if independent.
- **Authorization error**: remaining run work deferred; user prompted to re-authorize; deferred work re-queued after re-authorization.
- **Permanent error (404, invalid parameter)**: individual work item moved to error queue with permanent-failure classification; other work continues.

## Invariants
- No API call is made when the system knows remaining quota is insufficient.
- Quota exhaustion is never classified as an error; it is always an operational deferral.
- No work is silently dropped due to any YouTube API response category.
- The consumption log accurately reflects every API call attempted and its outcome category.

## Derived from
- Anchor: youtube-discovery-routing.md (AC13, AC18, AC19, AC30, Context: quota exhaustion as operational state)

## Verified by
- Integration test: quota exhaustion response triggers deferral, not error classification.
- Integration test: transient error retains work for retry.
- Integration test: auth error triggers re-authorization prompt and work deferral.
- Consumption audit: every API call logged with response category.
- Proactive deferral test: system does not attempt calls when quota is exhausted.

## Open questions
- None
