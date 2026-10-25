# Duplicate continuation

Detected duplicates continue through eligible routes and actions. These accepted target contracts retire the misleading handling setting without changing immutable history. Actual production actions depend on the executing run's own admissible decisions and captured targets. Legacy in-flight evidence needs independent snapshot reevaluation, not just an ownership check; an internal producer marker distinguishes new runs without backfilling history. Deferred siblings form one owned video group, and retained integrity failures have durable fresh-run manual retry ownership. The three owner/producer-marker extensions serve recovery, not duplicate policy. Implementation and independent confirmation remain pending; existing provider-crash idempotency is not certified.

- [ADR-017 — Fixed Duplicate Continuation with Inert Legacy Fields](decisions/adr-017-fixed-duplicate-continuation.md)
- [ARS-005 — Duplicate Continuation Compatibility and Results Contract](specifications/ars-005-duplicate-continuation-compatibility-results.md)
- [ARD-001 — Run-Owned Production Action Authority](design/ard-001-run-owned-production-action-authority.md)

ARS-005 also specifies the separate nullable `original_production_outcome` inspection field. Current persistence cannot reliably attribute a primary outcome to the original production baseline, so the new field must emit null; it must not copy mutable current state or the hypothetical result. The field is not implemented. Positive original comparison still needs requirements/design to settle baseline meaning across retries, then an explicit capture/provenance contract; US-012's per-video comparison is not removed or declared complete.

Migration 014 and opt-in state/queue ownership APIs are implemented and locally tested; [ARS-005's foundation status](specifications/ars-005-duplicate-continuation-compatibility-results.md#foundation-implementation-status) records their caller obligations. Production integration and independent confirmation remain pending; no active path emits producer version `1` yet.

Changes are recorded in the parent [architecture log](../log.md).
