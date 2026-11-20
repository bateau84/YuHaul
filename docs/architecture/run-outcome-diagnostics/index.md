# Run outcome diagnostics

This design reuses YuHaul's durable run, route-decision, condition-audit, action, and queue evidence to explain what each run did. Captured selected count, decision-bearing count, status, action dispositions, real-client SPA build configuration and inspection pagination have bounded implementation evidence. Diagnostic safety remains incomplete: printable text and earlier raw logs can leak. ARS-001 defines the decided closed-fact contract; implementation and fresh composition verification are outstanding. Earlier local/browser evidence remains limited to its tested paths, not blanket safety approval.

Simulation evidence remains hypothetical and cannot authorize production. Counts are not selected-input membership proof. Provider-success/local-record uncertainty remains unresolved. Backtest/Replay date filtering is **specified** by the user's 2026-11-10 decision in [BR-027 AC8–13](../../requirements/yuhaul/br-027-historical-backtesting.md) (YouTube published date, UTC days, both ends included, no range means no filter) and is implemented, review pending; its realization is [run-initiation ADR-002/ARS-001](../run-initiation/index.md); ARD-001's open date question is answered; same-app production comparison remains deferred. Full container/deployment, live-Google and accessibility certification are not claimed.

- [ARD-001 — Durable Run Diagnostics and Simulation Input Visibility](design/ard-001-durable-run-diagnostics-and-input-visibility.md)
- [ARS-001 — Safe Diagnostic Facts Across Producers, Storage, API and Logs](specifications/ars-001-safe-diagnostic-contract.md)

Changes are recorded in the parent [Architecture Change Log](../log.md).
