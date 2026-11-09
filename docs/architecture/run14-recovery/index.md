# Run 14 recovery assessment

- [ADR-001 — Evidence-Gated Recovery of Run 14's Pending Videos](decisions/adr-001-run14-evidence-gated-recovery.md) — Read-only incident assessment. No existing public API directly processes arbitrary pending discoveries without discovery. Existing queue retry can process retained queue groups without discovery, but admitting run 14's unqueued leftovers requires a tested repair. No production intervention is authorized.
