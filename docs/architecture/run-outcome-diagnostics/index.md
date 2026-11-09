# Run outcome diagnostics

This design reuses YuHaul's durable run, route-decision, condition-audit, action, and queue evidence to explain what each run did. Captured selected count, decision-bearing count, status, action dispositions, real-client SPA build configuration and inspection pagination have bounded implementation evidence. Diagnostic safety remains incomplete: printable text and earlier raw logs can leak. ARS-001 defines the decided closed-fact contract; implementation and fresh composition verification are outstanding. Earlier local/browser evidence remains limited to its tested paths, not blanket safety approval.

Simulation evidence remains hypothetical and cannot authorize production. Counts are not selected-input membership proof. Provider-success/local-record uncertainty remains unresolved. Backtest/Replay date filtering is **blocked on the user's decision**, not implemented; same-app production comparison remains deferred. Full container/deployment, live-Google and accessibility certification are not claimed.

- [ARD-001 — Durable Run Diagnostics and Simulation Input Visibility](design/ard-001-durable-run-diagnostics-and-input-visibility.md)
- [ARS-001 — Safe Diagnostic Facts Across Producers, Storage, API and Logs](specifications/ars-001-safe-diagnostic-contract.md)

Changes are recorded in the parent [Architecture Change Log](../log.md).
