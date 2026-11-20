---
type: Log
title: User and Operator Documentation Change Log
description: Changes to the user and operator documentation bundle.
tags: [changelog, user-docs, operations]
timestamp: 2026-11-20T00:00:00Z
---

# User and Operator Documentation Change Log

## 2026-11-20

**Created:** [Upgrade notes](/docs/user-docs/upgrade-notes.md) with the backup step and the database version 30 rollback rule: after `030` is applied, a pre-`030` image fails at startup until a pre-`030` backup is restored. Follows [Discovery ARS-002](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md) Release notes.
