---
type: Log
title: Quota Stop Change Log
description: Changes to the quota-stop architecture bundle.
tags: [changelog, architecture, quota, hotfix]
timestamp: 2026-11-22T00:00:00Z
---

# Quota Stop Change Log

## 2026-11-22

**Proposed:** [ARS-001](specifications/ars-001-provider-quota-stop-pacific-reset.md) covers run 58, using the diagnosis in debugger session `ses_dfd3cf500ffeOrmsSiJ7cIV7ui`. The spec is based on `origin/main` `8d7e009`.

- **Run quota stop.** The stop is a ledger predicate over the run's `quota` rows in the current bucket. It is checked in `Acquire` and `CanAfford`. It is the first realization of ARS-005 §6.1's quota dispatch stop.
- **Charging.** A refused call costs 1 unit.
- **Reset.** The quota day defaults to midnight `America/Los_Angeles`. `SetResetSchedule` still overrides it.
- **Pre-scan outcome.** A quota refusal before the scan is typed and ends discovery as `cut_short`.
- **Rejected:** a stop for the whole quota day across runs (ARS-005 §6.1, and YouTube's separate quota pools), and a relabeling migration.
- **Open:** Q-1 (terminal status) and Q-2 ("skipped" wording) are for the user.

Not implemented.
