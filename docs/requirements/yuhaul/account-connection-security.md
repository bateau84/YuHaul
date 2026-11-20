---
type: requirement
title: Account Connection Security
description: The system must protect account credentials and restrict API access to the authorized account.
tags: [requirement, account, security, MUST]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL protect stored account credentials from unauthorized access and SHALL NOT permit API calls on behalf of an account that has not completed the authorization flow.

## Acceptance criteria
1. Stored credentials are not exposed in plaintext through any system interface
2. The system rejects API requests that lack valid credentials
3. Revoked or expired access credentials are detected and not knowingly used for further YouTube data calls. Expired access may be renewed under [BR-034](br-034-automatic-youtube-token-refresh.md); valid renewed access permits calls without interactive reconnection. Missing/rejected refresh authorization requires reconnection before further data calls.
4. The user can disconnect an account and have credentials removed

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 1)
- User's 2026-11-07 refresh amendment recorded in [BR-034](br-034-automatic-youtube-token-refresh.md), superseding only expiry-implies-reauthorization meaning.

## Verified by

- Security review: credential storage is encrypted or otherwise protected
- Test: absent authorization is rejected; expired access renews when possible; missing/rejected renewal authorization reports reconnect-required without data calls using known-invalid access.
- Test: disconnection removes stored credentials

## Depends on

- account-connection.md
