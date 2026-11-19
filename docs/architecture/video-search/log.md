---
type: Log
---

# Video Search architecture log

## 2026-11-18

- Revised [ARS-001](/docs/architecture/video-search/specifications/ars-001-video-search-api-and-queries.md) and synchronized [ADR-001](/docs/architecture/video-search/decisions/adr-001-held-title-search-by-request-time-scan.md) in response to Loom QA `f3522231-9b8a-435e-91e6-1a0c2dfc760c`: purge the destination on an ID move, close or discard canceled write transactions safely, and measure actual 5,000-entry expiry against a working no-index control.
- S1, migration 029 ordering and rollback, and release thresholds are unchanged. This is a design correction, not independent recheck, implementation, or release evidence.
