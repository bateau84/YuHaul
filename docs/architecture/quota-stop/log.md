---
type: Log
title: Quota Stop Change Log
description: Changes to the quota-stop architecture bundle.
tags: [changelog, architecture, quota, hotfix]
timestamp: 2026-11-22T00:00:00Z
---

# Quota Stop Change Log

## 2026-11-22

**Revised after critic `ses_dfd2e7bdeffeRof29fhoIoWn65`.**

- The per-run stop is checked before any daily-limit logic, including `daily_limit=0`. New test Q1b.
- The deployment-day overcount lasts until the next Pacific midnight after the deployment's UTC day, up to about 24 hours, not 8 hours.
- Added the optional operator relabel (§3.3.1, Q-4): a backup, a read-only preview with a UTC-text guard, a bounded `UPDATE`, and an undo path. The user decides whether to run it. The old Q-4 is now Q-5.

**Proposed:** [ARS-001](specifications/ars-001-provider-quota-stop-pacific-reset.md) covers run 58, using the diagnosis in debugger session `ses_dfd3cf500ffeOrmsSiJ7cIV7ui`. The spec is based on `origin/main` `8d7e009`.

- **Run quota stop.** The stop is a ledger predicate over the run's `quota` rows in the current bucket. It is checked in `Acquire` and `CanAfford`. It is the first realization of ARS-005 §6.1's quota dispatch stop.
- **Charging.** A refused call costs 1 unit.
- **Reset.** The quota day defaults to midnight `America/Los_Angeles`. `SetResetSchedule` still overrides it.
- **Pre-scan outcome.** A quota refusal before the scan is typed and ends discovery as `cut_short`.
- **Rejected:** a stop for the whole quota day across runs (ARS-005 §6.1, and YouTube's separate quota pools), and a relabeling migration.
- **Open:** Q-1 (terminal status) and Q-2 ("skipped" wording) are for the user.

Not implemented.
