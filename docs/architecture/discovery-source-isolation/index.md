# Discovery source isolation

- [ARD-001 — Isolate Uploads-Source Failures Within a Production Run](design/ard-001-isolate-uploads-source-failures.md) — Proposed correction for Run 52's failure mode. Commits observations before later requests can erase them, fences the retry watermark, and projects permanent source errors read-only in Errors. An all-local-failure scan ends Failed with explicit no-coverage evidence.
- [ARS-001 — Durable Discovery Page Handoff](specifications/ars-001-durable-discovery-page-handoff.md) — Exact page ownership, metadata readiness, deferred release and receiving-run contract, with restart proof and BR-037 dependencies.
- [Change log](log.md)
