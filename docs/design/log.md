---
type: Log
title: Design documentation change log
description: Records maintenance of the accepted human-facing design documentation.
tags: [design, documentation, change-log]
timestamp: 2026-10-25T00:00:00Z
---

## 2026-10-25

- Amended [admin interface design](/docs/design/yuhaul-admin-ui-design.md), “Simulation Inspection — Requested-Run Results”, against BR-027 AC6, existing US-012 and ARS-005: exact run/detail simulation labels, requested-run outcome badge precedence, neutral production Actions heading, truthful not-attempted/empty copy, context-loading/retry behavior and representative browser/WCAG 2.2 AA checks. Updated the design index. Inspected reviewer browser-result evidence and current source; no production code or new browser/AT conformance proof. Flagged the inspected response's missing separately identified original-production-outcome comparison information for the API/architecture owner rather than inventing it from processing state.
- Amended [admin interface design](/docs/design/yuhaul-admin-ui-design.md), “Fixed Duplicate Continuation — Settings and Recorded History”, against BR-008 AC9–AC11 and ADR-017/ARS-005: exact inline Settings copy, read-only recorded-history row/diff labels, empty-value fallback, accessibility conditions, and downstream checks. Preserved threshold/import UX and immutable historical evidence; no handling selector or production code added. Updated the design index. Source inspection only; browser/runtime conformance and restore activation are not claimed.
- Reconciled [corpus import SQLite amendment](/docs/design/amendment-corpus-import-sqlite-reconciliation.md) with accepted BR-009 AC6/AC9, ADR-015, ARS-002, and the Settings recency amendment. Replaced stale proposed/unresolved claims with settled API, upload-cap, browser-disconnect, and last-successful-import recency decisions; preserved the deployment-verification prerequisite and no-source-date-preview meaning. Source inspection confirms the Settings metadata display path; no new browser, accessibility, cluster, or Product Acceptance proof is claimed.
- Added the [design index](/docs/design/index.md) covering all five existing design documents and this log. No UX or production source changes.
