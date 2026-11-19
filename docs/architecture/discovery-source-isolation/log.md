---
type: Log
title: Discovery Source Isolation Change Log
description: Changes to the discovery source isolation architecture bundle.
tags: [changelog, architecture, discovery]
timestamp: 2026-11-18T00:00:00Z
---

# Discovery Source Isolation Change Log

## 2026-11-18

**Proposed:** [ARD-001](/docs/architecture/discovery-source-isolation/design/ard-001-isolate-uploads-source-failures.md) describes the Run 52 correction and downstream proof, including the accepted Errors and all-local-failure behavior. No production operation or implementation.

**Specified:** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) defines the durable page and receiving-queue handoff, the shared metadata precondition and the required restart evidence. No runtime proof was performed.

**Corrected after independent review:** ARD-001 and ARS-001 seal acquisition scope and make terminal retention atomic, align readiness and eligibility fences across receiver/recovery entry points, and specify authorized inspection with vetted reasons. The shared [receiving ARS-005](/docs/architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md) now distinguishes complete responses, typed dispatch stops and local-only claiming. Parent architecture navigation, shared log reconciliation and migration numbering are coordinated by the integrator.

**Targeted re-review:** The shared receiver now preserves authorization deferral for unsent, locally claimed and restored receipts. The user approved Completed with explicit incomplete coverage when the only source becomes ineligible on restart after saving observations. Discovery ARD-001 and ARS-001 specify atomic handoff, no origin routing, and the restart proof. No runtime proof was performed.

**Live-status amendment (BR-042, 2026-11-19).** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §3: readiness adopts `liveGatePass` and `retainNativeTx`, and the prefetch records `live_status` ([Live ARS-001 §10](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md)).
