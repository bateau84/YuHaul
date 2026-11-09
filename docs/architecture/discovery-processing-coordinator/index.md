# Discovery, queued processing, and coordination

- [ARD-001 — Separate Discovery and Processing with a Local Budget Coordinator](design/ard-001-discovery-processing-budget-coordinator.md) — Proposed structural direction, not accepted new product behavior or an implementation claim. Reuses SQLite pending videos, run evidence, retry ownership, scheduler, and quota ledger. Records safe handoff, discovery checkpoint and quota-admission requirements; automation, first-discovery scope, fairness and pause semantics require owner decisions.

Changes use the shared [Architecture Change Log](../log.md).
