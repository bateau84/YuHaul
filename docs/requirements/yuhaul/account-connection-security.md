---
type: requirement
title: Account Connection Security
description: The system must protect account credentials and restrict API access to the authorized account.
tags: [requirement, account, security, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL protect stored account credentials from unauthorized access and SHALL NOT permit API calls on behalf of an account that has not completed the authorization flow.

## Acceptance criteria
1. Stored credentials are not exposed in plaintext through any system interface
2. The system rejects API requests that lack valid credentials
3. Revoked or expired credentials are detected and the system ceases API calls on behalf of that account
4. The user can disconnect an account and have credentials removed

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 1)

## Verified by

- Security review: credential storage is encrypted or otherwise protected
- Test: system returns an authorization error when credentials are absent or expired
- Test: disconnection removes stored credentials

## Depends on

- account-connection.md
