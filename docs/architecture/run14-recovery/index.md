# Run 14 recovery assessment

**Declined by the user, 2026-11-10:** leave run 14 as it is; no recovery and no backfill; its saved unevaluated videos stay unprocessed. See the [requirements proposal](../../requirements/run14-recovery/index.md). The ADR below is kept as history and is not to be implemented.

- [ADR-001 — Evidence-Gated Recovery of Run 14's Pending Videos](decisions/adr-001-run14-evidence-gated-recovery.md) — Read-only incident assessment. No existing public API directly processes arbitrary pending discoveries without discovery. Existing queue retry can process retained queue groups without discovery, but admitting run 14's unqueued leftovers requires a tested repair. No production intervention is authorized.
