---
type: Log
title: Quota Stop Change Log
description: Changes to the quota-stop architecture bundle.
tags: [changelog, architecture, quota, hotfix]
timestamp: 2026-11-22T00:00:00Z
---

# Quota Stop Change Log

## 2026-11-22

**Corrected after release assessment `6bcb6c7f`.** The optional relabel (§3.3.1) now handles the real stored `recorded_at` form (`2026-11-22 11:44:38.664736063 +0000 UTC`) as well as `…Z`, and its format check covers every row labeled L. It was tested on a scratch database holding rows written by the real ledger code and rows in the `T…Z` form, on both sides of 08:00Z. The reset field is `quota.reset_at` in `GET /api/overview`. The pre-scan stop logs `discovery complete`. The e2e test gives the limited run one unit of headroom, so the claim-then-requeue coverage is back.

**Implemented** on `hotfix/quota-stop` (commits `111d244`, `2aa0506`, `f02a69e`, `0eb154a`, `6827e05`). [ARS-001 §10](specifications/ars-001-provider-quota-stop-pacific-reset.md) records the evidence and the deviations: the Q1 pending count is 7, not 6; Q2, Q5 and Q12 are proved at the boundary the tests can reach; one e2e test outside the write boundary changed its expectation. Q-1 and Q-2 are still open.

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
