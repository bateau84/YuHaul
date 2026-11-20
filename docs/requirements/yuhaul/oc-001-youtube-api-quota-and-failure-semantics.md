---
type: obligation-contract
title: OC-001 — YouTube API Quota and Failure Semantics
description: Agreement between the system and the YouTube Data API v3 on how quota exhaustion and API failures are treated operationally
tags: [obligation-contract, youtube, api, quota, failure]
timestamp: 2026-11-18T00:00:00Z
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
5. When the YouTube API returns an authorization error (HTTP 401, 403 with `authError`), the system resolves available renewal under [BR-034](br-034-automatic-youtube-token-refresh.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md) before requiring interactive reauthorization. Safe recovery may continue work; missing/rejected refresh authorization defers remaining work and prompts reconnection. Temporary renewal failure preserves work with a renewal-unavailable reason. Renewal does not permit blind replay of an issued mutation with uncertain application.
6. The system records the YouTube API response category (success, quota, transient, auth, permanent) for every API call in the consumption log.
7. The system does not make API calls that it knows will exceed remaining quota; it short-circuits and defers proactively when quota state indicates exhaustion is imminent.

## Semantic inputs and outputs
- **Input**: YouTube API response status and error category for each call.
- **Output**: Operational disposition (continue, defer, retry, prompt-reauth) and consumption log entry.

## Failure semantics
- **Quota exhaustion**: all pending API-dependent work deferred; no immediate retry; next run or manual run resumes.
- **Transient error (5xx, timeout)**: individual work item retained for retry; other work in the run continues if independent.
- **Authorization error**: BR-034/OC-006 renewal and call safety take precedence. If recovery cannot safely continue, remaining work is preserved/deferred with the applicable unavailable or reconnect-required reason. Work remains eligible after recovery, but uncertain mutations are not blindly replayed.
- **Permanent error (404, invalid parameter)**: individual work item moved to error queue with permanent-failure classification; other work continues.
- **Permanent uploads-source error**: for a permanent response that prevents scanning an individual channel's uploads, the required error-queue disposition is a durable, read-only source entry in Errors. The user can identify the affected channel and inspect its permanent failure reason after a reload or application restart. This is not a failed video processing item; it has no manual Retry, Dismiss, or source-suppression action. It does not block the next eligible fresh discovery run from polling that channel under [BR-002 AC6–7](br-002-subscription-discovery-without-live-monitoring.md). A consumption-log entry or Run Detail diagnosis does not replace the Errors entry.

## Invariants
- No API call is made when the system knows remaining quota is insufficient.
- Quota exhaustion is never classified as an error; it is always an operational deferral.
- No work is silently dropped due to any YouTube API response category.
- The consumption log accurately reflects every API call attempted and its outcome category.

## Derived from
- Anchor: youtube-discovery-routing.md (AC13, AC18, AC19, AC30, Context: quota exhaustion as operational state)
- User's 2026-11-07 refresh amendment recorded in [BR-034](br-034-automatic-youtube-token-refresh.md), superseding only unconditional auth-error-to-interactive-reauthorization handling.
- Permanent uploads-source disposition: the user's “1. yes / 2. agreed.” decision on 2026-11-18, relayed in dispatcher answer `0fab403b-69b5-4702-9d29-1124d3e03426` to Loom question `f8bb5793-7026-4e1d-b3f7-4b7545b73234` (Action `b820f6ca-88c9-4f0f-a28a-a58a88060612`). It specifies the source-item disposition only; existing video retry and removal are unchanged.

## Verified by
- Integration test: quota exhaustion response triggers deferral, not error classification.
- Integration test: transient error retains work for retry.
- Integration test: access rejection attempts available renewal; safe read recovery continues; missing/rejected renewal authorization prompts reconnect and defers work; ambiguous mutations remain protected under OC-006.
- Consumption audit: every API call logged with response category.
- Proactive deferral test: system does not attempt calls when quota is exhausted.
- Controlled-provider run: a permanent uploads-source response is classified as permanent in consumption accounting and yields an identifiable, read-only Errors entry available after restart. Verify no source Retry, Dismiss, or suppression action; verify the next eligible fresh discovery run polls the source. Contrast with the existing transient, quota and authorization dispositions and verify that video Retry and removal still work as specified.

## Open questions
- None
