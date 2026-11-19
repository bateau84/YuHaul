# Discovery source isolation

- [ARD-001 — Isolate Uploads-Source Failures Within a Production Run](design/ard-001-isolate-uploads-source-failures.md) — Proposed correction for Run 52's failure mode. Commits observations before later requests can erase them, fences the retry watermark, and projects permanent source errors read-only in Errors. An all-local-failure scan ends Failed with explicit no-coverage evidence.
- [ARS-001 — Durable Discovery Page Handoff](specifications/ars-001-durable-discovery-page-handoff.md) — Exact page ownership, metadata readiness, deferred release and receiving-run contract, with restart proof and BR-037 dependencies.
- [ADR-001 — Interim Channel-Granular Isolation of Unavailable Uploads Sources](decisions/adr-001-interim-channel-granular-uploads-isolation.md) — No-migration interim: skip a channel on exact `404 playlistNotFound`, keep completed channels and floors, ledger-derived incomplete note, explicit AC8 failure. User-owned gaps G1 (Errors entry), G2 (AC9) and G3 (channel ID in logs).
- [Change log](log.md)
