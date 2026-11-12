# Discovery, queued processing, and coordination

- [ARD-001 — Separate Discovery and Processing with a Local Budget Coordinator](design/ard-001-discovery-processing-budget-coordinator.md) — Proposed structural direction, not accepted new product behavior or an implementation claim. Reuses SQLite pending videos, run evidence, retry ownership, scheduler, and quota ledger. Records safe handoff, discovery checkpoint and quota-admission requirements; automation, first-discovery scope, fairness and pause semantics require owner decisions.

**User direction, 2026-11-10 (still a proposal, not accepted):** both jobs run automatically on separate schedules; exhausting the quota is acceptable because work continues after the quota resets. Recorded in the [requirements index](../../requirements/yuhaul/index.md). This answers part of the ARD's automation and budget questions; it does not accept the ARD or amend BR-016/BR-017.

Changes use the shared [Architecture Change Log](../log.md).
