# Duplicate continuation

Detected duplicates continue through eligible routes and actions. These accepted contracts retire the misleading handling setting without changing immutable history. Actual production actions depend on the executing run's own admissible decisions and captured targets. Legacy in-flight evidence needs independent snapshot reevaluation, not just an ownership check; an internal producer marker distinguishes new runs without backfilling history. Deferred siblings form one owned video group, and retained integrity failures have durable fresh-run manual retry ownership. The three owner/producer-marker extensions serve recovery, not duplicate policy. Implementation evidence and remaining limits are recorded in the specifications; existing provider-crash idempotency is not certified.

- [ADR-017 — Fixed Duplicate Continuation with Inert Legacy Fields](decisions/adr-017-fixed-duplicate-continuation.md)
- [ARS-005 — Duplicate Continuation Compatibility and Results Contract](specifications/ars-005-duplicate-continuation-compatibility-results.md)
- [ARS-006 — Run-Attributed Production Outcomes](specifications/ars-006-run-attributed-production-outcomes.md)
- [ARD-001 — Run-Owned Production Action Authority](design/ard-001-run-owned-production-action-authority.md)

ARS-005 also specifies the separate nullable `original_production_outcome` inspection field, not yet implemented. ARS-006's run-local production records do not choose a simulation baseline; neither mutable current state nor hypothetical results can substitute. [BR-027 AC5](../../requirements/yuhaul/br-027-historical-backtesting.md) retains same-app production-versus-hypothetical comparison as accepted future scope, explicitly deferred from first-use launch. Baseline meaning and comparison capture remain unresolved; no comparison data availability or completion is claimed.

Migration 014's state/queue foundations and the subsequent production/receiving-run integration are recorded in [ARS-005's implementation status](specifications/ars-005-duplicate-continuation-compatibility-results.md#foundation-implementation-status), including corrected new-run producer version `1`. Those local evidence reports do not certify provider-crash recovery or deployed acceptance.

Changes are recorded in the parent [architecture log](../log.md).

ARS-006's migration-015 run-local production capture/projection is implemented: recorded outcomes/reasons and receiving-run counts are separate from discovery, and uncaptured legacy per-video outcomes remain null. Its implementation-status section preserves conformance gaps and unexecuted verification limits. It does not settle the original-versus-simulation baseline or reconstruct old history. First use focuses on subscription → playlist; own-server deployment remains postponed/unverified.
