# Architecture

- [YuHaul System Architecture](yuhaul-system-architecture.md) - Compliance-aware system architecture for YuHaul — two-store data model, processing pipeline, quota management, duplicate detection, and deployment model. Updated 2026-09-15 with fresh obligation mapping against 32 BRs + 3 OCs, reconciled outcome model with distinct "excluded" category per OC-002, and a five-phase pipeline (Discovery → Duplicate Detection → Exclusion → Routing → Action Execution) with a separated exclusion gate.
- [YouTube API Compliance Analysis](yuhaul-youtube-compliance.md) - Analysis of YouTube Developer Policies data-retention requirements and their impact on YuHaul's storage model.
- [ADR-001 — YouTube Provider Seam via Base URL Override](adr-001-youtube-provider-seam.md) - Threads `youtube.WithBaseURL` through the shipped binary behind `YUHAUL_YOUTUBE_BASE_URL` so Product Acceptance can run against a fake YouTube without modifying the binary.
- [ADR-002 — Failed-Phase Runs Terminate as `failed`, Not `completed`](adr-002-run-status-failed-terminal-state.md) - Adds `failed` as a fourth terminal Run status so phase failures and panics are not reported `completed`.
