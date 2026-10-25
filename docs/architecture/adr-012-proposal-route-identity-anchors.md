---
type: decision
title: "ADR-012 — Proposal-Only Routes Use Inactive Identity Anchors"
description: "Resolve proposal UUIDs to durable internal keys for dry-run/backtest evidence while preserving route foreign keys, immutable snapshots, and the active route set."
tags: [architecture, decision, yuhaul, routes, uuid, proposals, history]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted

Dry-run and backtest can evaluate a route that is not in the saved route list. Those evaluations still write route-decision history, and each decision's integer route key must satisfy the existing foreign key to `route.id`. The chosen realization keeps that key valid without making the proposed route active: it records an inactive route row as an identity anchor and stores the proposal's actual configuration in its immutable ConfigVersion snapshot. For example, a proposal carrying a valid UUID not yet in the registry gets one internal integer key and one persistent UUID mapping; the evaluator uses the snapshot, while the active route list and next production run remain unchanged.

## Derived from

- [ADR-011 — Stable UUID Route Identity Independent of Display Name](adr-011-stable-uuid-route-identifiers.md).
- [ARS-004 — Route UUID Schema, API, and Migration Contract](ars-004-route-uuid-schema-api-migration.md).
- [ARS-001 — Route, Playlist, and Run-Outcome API Contracts](ars-001-route-and-playlist-api-contracts.md).
- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md).
- [BR-026 — Dry-Run Evaluation](../requirements/yuhaul/br-026-dry-run-evaluation.md).
- [BR-027 — Historical Backtesting](../requirements/yuhaul/br-027-historical-backtesting.md).
- [OC-003 — Configuration Snapshot for Historical Interpretation](../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md).
- [YuHaul System Architecture](yuhaul-system-architecture.md), route, route-decision, and ConfigVersion storage model.

## Satisfies

- BR-026/BR-027: proposal route definitions can be evaluated and recorded without changing the active route configuration or mutating external YouTube state.
- BR-003 AC12/AC13 and ARS-004: a route UUID remains the stable identity in proposal and historical decision surfaces; no name or internal integer fallback is used.
- OC-003: each proposal's persisted ConfigVersion snapshot remains the immutable authority for the configuration that run evaluated.
- ADR-011: existing integer primary/foreign keys and the `route_decision.route_id → route.id` foreign key remain intact.

## Decision question

How can a dry-run/backtest proposal use a UUID that is not yet in `route_identity` while its resulting `route_decision.route_id` must remain an integer foreign key to `route.id`, without changing the active route set or rewriting proposal history?

## Drivers and constraints

- ARS-004 retains `route.id` and integer foreign keys, including `route_decision.route_id → route.id`; `route_identity` intentionally has no foreign key so it can retain snapshot-only historical identities.
- Dry-run and backtest evaluate explicit proposal snapshots and persist route decisions. The evaluator does not need the proposal's route configuration loaded from active route tables.
- BR-026/BR-027 isolation and ARS-004 require that a proposal not become active merely because it was evaluated.
- Proposal UUIDs and display names are separate. Duplicate names are valid, and a caller-supplied display name cannot select or merge identities.
- ConfigVersion snapshots are immutable. Public input/output route IDs are UUIDs; stored snapshot and decision route IDs remain internal integers.

## Options considered

### A. Persist an inactive route identity anchor for each proposal-only UUID (chosen)

For a valid proposal UUID already in `route_identity`, use its mapped integer key. If the map exists but an old hard delete removed the corresponding `route` row, recreate an inactive anchor at that same integer key. For a valid UUID not yet registered, allocate a new internal route key, insert the registry mapping using the supplied UUID, and create an inactive route row at that key. Persist the translated ConfigVersion snapshot and the run in the same SQLite transaction. Evaluation uses the proposal snapshot, not the anchor row or active route loader.

- **Benefits:** Preserves the existing `route_decision` foreign key, stable public UUID, immutable snapshot, and durable evidence without changing active route selection or evaluation.
- **Costs:** Proposal runs create durable, soft-deleted rows in `route` as FK anchors and require one transactional proposal-ingest path to translate UUIDs before saving the ConfigVersion.
- **Failure modes:** A missing/duplicate mapping, invalid UUID, duplicate UUID within one proposal, or failed transaction must not be converted to a guessed identity. A failed transaction leaves no partial anchor, mapping, snapshot, or run.
- **Operational/compatibility impact:** No new service, table family, or external protocol. Existing active-route queries already exclude `deleted_at` rows; anchors are never exposed by route CRUD.
- **Security/authority impact:** UUIDs remain identifiers, not authorization credentials. Proposal validation and persistence remain server-owned.
- **Obligation impact:** Route decisions keep their existing FK and each run's immutable snapshot remains the authority for the route definition and display name evaluated.

### B. Point route decisions at `route_identity` instead of `route`

Change the history foreign-key target or introduce a new decision-to-identity relation.

- **Benefits:** A proposal identity could exist without a row in the active route table.
- **Costs:** Contradicts ADR-011/ARS-004's explicit retained FK target and requires a history-schema migration or an additional relation plus new joins for every decision consumer.
- **Failure modes:** A partial conversion could strand existing decision, audit, action, provenance, or analytics joins; it increases the migration surface without adding a required user-visible capability.
- **Compatibility/obligation impact:** Does not preserve the accepted database constraint as written; rejected.

### C. Keep proposal identities in memory or generate a surrogate public UUID

Retain a temporary mapping during evaluation, or silently replace an unknown request UUID with a new server UUID.

- **Benefits:** Avoids storing a proposal-only route row.
- **Costs:** The route-decision FK still requires a `route` row; an in-memory mapping cannot survive history inspection or restart. Replacing the public UUID breaks request/result identity continuity and makes a valid caller route UUID appear to identify another route.
- **Failure modes:** FK failure, lost route identity after restart, or incompatible proposal and result identities.
- **Obligation impact:** Rejected because it cannot produce durable, correctly identified route evidence.

### D. Reject every proposal UUID absent from the registry

- **Benefits:** No proposal-specific identity persistence.
- **Costs:** Prevents dry-run/backtest from evaluating genuinely new unsaved routes, despite the accepted draft/proposed-configuration surfaces.
- **Obligation impact:** Rejected as incomplete for proposal configurations containing a new route.

## Comparison

Option A is the only option that preserves the explicit `route_decision → route` foreign key, durable UUID identity, snapshot immutability, and evaluation of unsaved proposals together. Option B changes an accepted storage guarantee. Option C cannot satisfy the FK or stable public identity. Option D blocks new proposal routes rather than realizing the accepted proposal surface. The additional route rows in Option A are inactive identity anchors, not active configuration, and reuse the existing route table and soft-delete filter rather than adding another storage model.

## Decision

For `POST /api/runs/dry-run` with `draft_configuration` and `POST /api/backtest` with `proposed_configuration`:

1. Each `routes[].id` MUST be a canonical UUIDv4 string as specified by ARS-004. The parser may accept uppercase hexadecimal in an otherwise canonical UUID; all stored/public output is lowercase. A missing, malformed, non-v4, or duplicate UUID within one proposal is invalid input (`400`); route names are not used to resolve identity.
2. If the UUID exists in `route_identity`, translate it to the mapped internal integer key and leave the mapping and any existing route row unchanged. This permits a proposal to reuse an active, soft-deleted, or snapshot-only route identity while evaluating the configuration present in this proposal's snapshot.
3. If the mapping exists but its `route` row is absent, create a soft-deleted identity-anchor row using the same internal integer key. If the UUID is not in the registry, allocate a new internal integer key and register the supplied normalized UUID; do not generate a replacement public ID.
4. The anchor row is committed with `deleted_at IS NOT NULL`, `is_enabled = false`, and `evaluation_order = -id`, matching ADR-003's inactive sentinel convention. Copy the proposal route's `name`, `description`, `is_final`, and `condition_composition` as required identity-row metadata; set creation/update/deletion timestamps in the same transaction. It is not inserted into the active configuration, receives no route child rows, and does not invoke the route-mutation ConfigVersion callback. The proposal's complete route definition, including the evaluated name, conditions, actions, exclusions, enabled state, and evaluation order, remains in the proposal ConfigVersion snapshot. Evaluation reads those snapshot values only.
5. One SQLite transaction covers any required anchor/mapping insertions, the translated immutable ConfigVersion snapshot, and its run row. The API returns `202` only after commit. On validation or persistence failure, the transaction leaves none of those new records committed. Repeated or simultaneous submissions carrying the same previously unknown UUID converge on the one `route_identity` mapping; a uniqueness race is resolved by reloading the winning mapping after rollback/retry, never by replacing it.
6. Each resulting `route_decision.route_id` stores the mapped integer key and satisfies its existing FK to `route.id`. The public route reference is the UUID. Run-specific decision display names are projected from that run's immutable ConfigVersion snapshot, not from the identity-anchor row or a later route rename; existing OC-003 incomplete-context handling remains authoritative if a legacy run has no usable snapshot.
7. An identity-anchor row remains soft-deleted and is never made active by evaluation. A later route-create request still follows ARS-004's server-generated UUID rule; no implicit proposal-to-active promotion or reuse operation is added by this decision.

## Consequences

### Completion and proposal-ingest contention correction (2026-10-25)

Production completion publishes `completed` only after its final outcome-counter write succeeds. Thus the first completed HTTP response includes final counters, as historical completion already does; a failed counter write cannot expose completed status. Failed/interrupted run handling and action idempotency are unchanged.

Proposal ingestion reserves SQLite's writer before reading identity mappings, using a zero-row `UPDATE run SET id = id WHERE 0` inside its existing `*sql.Tx`. This changes no rows but prevents a deferred read snapshot from becoming stale when another writer commits before proposal persistence (`SQLITE_BUSY_SNAPSHOT`). Ordinary writer acquisition uses the connection's bounded busy timeout; exhaustion or cancellation remains an error and rolls back the transaction, not a partial success. There is no sleep, HTTP-start retry, configuration activation, or change to the atomic anchor/mapping/snapshot/run boundary. Existing uniqueness-race rollback/reload handling is retained.

`internal/state/completion_test.go` pauses the actual SQLite counter write and reads run status through a separate WAL connection; it also injects counter-write failure. `internal/pipeline/proposal_contention_test.go` deterministically attempts a competing commit during the real identity lookup and exercises 16 concurrent ingests against one file-backed database. `e2e/completion_proposal_race_test.go` immediately posts backtest/dry-run proposals on the first completed production HTTP response, checks counters and unchanged saved configuration, and verifies the persisted snapshot/identity/anchor/run linkage. Optional post-completion advisory generation is not moved into run finalization; writer reservation also protects proposal reads from concurrent advisory writes.

### Active configuration selection correction (2026-10-25)

`config.Service.ActiveVersionID` builds the saved effective configuration from active route tables and duplicate settings at run initiation. It reuses the newest ConfigVersion whose serialized snapshot exactly matches that configuration, or captures a new immutable version if none matches. The greatest history ID is not an activation marker: proposals and historical copies are appended to the same table. Explicit replay/proposal selection still uses the requested immutable snapshot without saving it as active configuration.

This needs no schema migration or description-based legacy classification. Existing databases with a proposal at the end of history select a matching saved configuration, or capture the current saved values when no exact match exists. Existing snapshots, run foreign keys, and proposal identities are never rewritten. A byte-identical proposal can be reused because its complete evaluated configuration is identical to the saved configuration; version provenance is not an activation signal. Serialization differences can cause an additional equivalent snapshot, not selection of different values. Lookup scans history for a match and does not introduce a persisted active pointer. This correction does not make concurrent route/settings edits atomic with run initiation or change production's existing live-route evaluation path.

`internal/config/config_test.go` exercises contaminated history across service recreation, immutable historical copies, and missing saved snapshots. `e2e/active_config_selection_test.go` exercises save → backtest/draft → production without resaving, including historical replay selection: production keeps the saved threshold and route snapshot, the fuzzy candidate produces no match at the saved threshold, the saved playlist receives the action, and proposal snapshots remain unchanged.

- **Positive:** Unknown proposal UUIDs survive run-result inspection and restart, while FK-backed decisions and condition/action evidence remain structurally valid. Active route listing, route evaluation, active ConfigVersion, and route CRUD are not changed by proposal evaluation.
- **Negative:** `route` contains inactive anchor rows for proposal identities and historical-only UUIDs that need new FK-backed decisions. Database inspection must distinguish these soft-deleted rows from active routes.
- **Neutral:** Proposed route configuration is stored once in the run's ConfigVersion snapshot; anchor rows are not a second copy of proposal children. Reusing an existing route UUID in a proposal does not change its active row.
- **Compatibility:** The UUID API remains coordinated and breaking as ARS-004 defines. No integer alias is added.
- **Unspecified operation:** This decision does not add a workflow that promotes a proposal-only identity into an active route. Route creation continues to use the server-generated identity contract in ARS-004; a future promotion workflow would need an explicit API/product contract.

## Confirmation and reconsideration

The implementation must prove:

1. A proposal-only UUID produces one registry mapping and one soft-deleted route anchor, and its dry-run/backtest decision satisfies `route_decision.route_id → route.id`.
2. The run's stored ConfigVersion snapshot contains internal integer route IDs after translation, is immutable after creation, and public reads project the original UUIDs; snapshot route configuration and decision display names come from that run's snapshot.
3. A proposal does not appear in `GET /api/routes`, change route evaluation for production, create route children, trigger the active-route snapshot callback, or mutate the active ConfigVersion.
4. An already mapped active or soft-deleted UUID is reused without row mutation; a mapped historical-only UUID recreates an anchor at exactly its existing internal key.
5. Invalid or duplicate UUIDs return `400`; transaction failures leave no partial mapping, anchor, ConfigVersion, or run. Concurrent proposals with the same unknown UUID yield one durable mapping and interoperable results.
6. Repeat the proposal evaluation across restart and verify stable UUID mapping, run decision, audits/actions, and snapshot identity. Verify no public API exposes the internal integer.

Reconsider this realization only if the accepted FK target changes, route decisions stop requiring `route` rows, or a separately specified proposal-promotion workflow needs proposal identities to become active. Any such change requires revisiting ADR-011/ARS-004 compatibility and historical evidence guarantees.

## Open questions

- None for proposal evaluation identity or FK realization. Proposal-to-active promotion is explicitly outside this decision and must be specified before such a workflow is introduced.
