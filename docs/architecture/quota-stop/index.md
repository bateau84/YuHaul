# Quota stop

Proposed 2026-11-22 hotfix for run 58, which kept sending playlist inserts after YouTube refused them for quota. Not implemented.

- After a provider quota refusal in a run, the ledger refuses the rest of that run's calls in the same quota day. The stop is read from the run's own ledger rows.
- A refused call costs 1 unit, not 50.
- The quota day defaults to midnight Pacific.
- A quota refusal before the channel scan ends discovery as `cut_short` instead of failing the run. The terminal status is pending the user's decision.

Contents:

- [ARS-001 — Run Quota Stop, Refused-Call Charging, Pacific Reset and Pre-Scan Quota Outcome](specifications/ars-001-provider-quota-stop-pacific-reset.md) — the exact contract, log lines, write boundary and tests Q1–Q13.
- [Change log](log.md)
