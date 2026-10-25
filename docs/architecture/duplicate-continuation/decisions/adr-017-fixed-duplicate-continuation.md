---
type: decision
title: ADR-017 — Fixed Duplicate Continuation with Inert Legacy Fields
description: Retain legacy configuration shapes as inert compatibility data, emit continue on active surfaces, and continue historical simulation after matches without rewriting history.
tags: [architecture, decision, duplicates, compatibility, replay, backtest]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted — target realization; not implemented or independently verified by this documentation change.

A duplicate flag explains a relationship between two videos; it does not stop the new video from reaching eligible playlists. The engine will always continue after a distinct match. Old clients may still send `duplicate_behavior: "skip"`, but the field is retired: active settings report `continue`, and a newly evaluated proposal records `continue`. An old snapshot keeps its recorded `skip` value and past outcomes unchanged; applying it today does not bring skip behavior back.

Retiring the setting uses the existing configuration and pipeline boundaries. It needs no behavior-setting migration, new policy selector, historical evidence rewrite, or simulation provenance store. A match still does not guarantee a route match, a completed playlist insertion, or discovery credit. The later [ARD-001 correction](../design/ard-001-run-owned-production-action-authority.md) closes demonstrated production authorization/snapshot defects, including independently admitting legacy evidence that may have used live conditions. Its nullable deferred/error receiving-run references and run producer marker serve durable recovery and admission, not duplicate policy; no old evidence is backfilled as trusted.

## Derived from

- [BR-008](../../../requirements/yuhaul/br-008-exact-and-fuzzy-duplicate-detection.md), AC9–AC11, including the user's explicit 2026-10-25 “Continue, yes”.
- [OC-002](../../../requirements/yuhaul/oc-002-route-evaluation-outcome-semantics.md), Guarantee 11.
- [OC-003](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md), immutable configuration history.
- [OC-004](../../../requirements/yuhaul/oc-004-imported-history-as-duplicate-reference.md), Guarantee 5.
- [System architecture](../../yuhaul-system-architecture.md), processing order and INV-8 isolation.
- [ADR-012](../../adr-012-proposal-route-identity-anchors.md), proposal isolation and effective saved-configuration selection.
- [ADR-014](../../adr-014-discovery-credit-requires-native-earliest-evidence.md), unchanged pairwise credit.

## Satisfies

| Obligation | Disposition |
|---|---|
| BR-008 AC9 | Realized here and in ARS-005: remove the historical match short circuit; preserve production/recovery and dry-run continuation, normal routing rules, and mode isolation. |
| BR-008 AC10–AC11 | Realized here and in ARS-005: compatibility strings are accepted but inert; active output is `continue`; historical output remains recorded data. |
| OC-002 Guarantee 11 | Realized here: duplicate information is not a primary result or exclusion; counters derive from actual or simulated route/action evidence. |
| OC-003 Guarantees 1–8 / BR-025 history | Preserved here: old rows and run references unchanged; existing snapshot selection/copy/diff remains; new effective saved/proposal capture is canonical. ARD-001/ARS-005 require actual production phases/actions to consume that run's snapshot; capture alone is insufficient. Broader restore behavior remains elsewhere in ADR-012 and ARS-004. |
| BR-026 / BR-027 isolation | ARD-001/ARS-005 enforce the downstream production boundary as well as simulation writes; simulation decisions cannot authorize a subsequent real action. No simulation production writes or external calls are added. |
| OC-004 Guarantee 5 | Realized here: an imported-reference match has the same continuation rule. Eligibility and no-self-match remain elsewhere in ADR-013 / ARS-002. |
| BR-010 pairwise credit / BR-008 AC7 exclusions | Satisfied elsewhere in ADR-014 and the system pipeline; unchanged, not deferred or reopened. |

No accepted continuation obligation is deferred. Runtime conformance and downstream confirmation remain pending. Native-reference eligibility beyond the current routed-only native realization and other legacy-document conflicts are separate product questions, not prerequisites to this change.

## Decision question

How can fixed continuation be made consistent across runtime and configuration surfaces without rejecting existing configuration/proposal traffic, falsifying history, or adding an unauthorized skip mode?

## Drivers and constraints

- Legacy fields exist in `settings`, ConfigVersion JSON, public history, settings/threshold responses, settings updates, and dry-run/backtest proposals.
- `config.Service.DuplicateBehavior` currently defaults to `skip` and echoes arbitrary stored strings; its setter writes and snapshots those strings. The run detector reads snapshot threshold only (`internal/config/service.go`, `internal/pipeline/duplicate_config.go`).
- Production records a match and retains the survivor list for routing; dry-run detects read-only and continues. `evaluateHistoricalVideo` instead returns immediately on a match (`internal/pipeline/pipeline.go:385–425`, `dryrun.go:148–158`, `replay.go:176–185`).
- Existing history reads and diffs preserve recorded duplicate strings. Restore currently appends a snapshot copy, not active route/settings installation (`internal/config/service.go:88–105`). This amendment must not silently change that larger restore contract or claim it was verified as a complete restore implementation.
- Simulation records decisions and `not_attempted` actions under its own run; it must not write production duplicate/provenance, fingerprints, video processing state, idempotency, or operational statistics.

## Options considered

### A. Inert legacy storage/input, canonical active output (chosen)

Keep the field shapes, ignore their handling values at runtime, accept previously accepted strings, and return `continue` from active settings. New effective saved captures and new proposals record `continue`. Historical read/diff/copy remains recorded data. Existing obsolete settings rows may remain untouched and inert. Historical evaluation keeps read-only detection but no longer returns on a match.

- **Benefits:** Smallest compatible change; no migration or cleanup operation; no history loss; arbitrary legacy strings cannot reactivate suppression.
- **Costs:** Same legacy field name has context-dependent meaning: effective fixed policy on active responses, recorded value in immutable history. API documentation and history presentation must make that distinction explicit.
- **Failure modes:** A shared serializer or low-level store normalizer could falsify history; a future consumer could mistakenly interpret old strings. Enforce canonicalization only at active/proposal capture and never use the string as an execution branch.
- **Operational/security impact:** No new endpoint, privilege, table, external call, or trust boundary. Old supported requests continue to succeed subject to their existing unrelated validation.
- **Coupling/obligation impact:** Reuses configuration service, proposal-ingest transaction, and router. Satisfies AC9–AC11 without another policy model.

### B. Forward data migration of active setting plus boundary normalization

Update the mutable settings row to `continue` in a new migration, accept/normalize legacy inputs, preserve historical snapshots, and remove the same historical short circuit.

- **Benefits:** Mutable raw setting agrees with active API at initial upgrade; supports direct settings-table inspection without an obsolete value.
- **Costs:** A migration plus the same effective-policy enforcement is still necessary: missing rows, arbitrary legacy input, historical snapshots, or a direct write must not enable skip. Migration alone cannot satisfy AC10.
- **Failure modes:** Future writes can recreate obsolete data; an overbroad migration could modify history. Requires extra migration verification/rollback semantics with no accepted requirement for raw settings-table cleanup.
- **Operational/security impact:** Adds upgrade work but no useful runtime safety beyond A's mandatory boundary rule; same trust boundary.
- **Coupling/obligation impact:** Also viable if correctly bounded, but not less complex and does not discharge an additional demonstrated risk.

### C. Remove/reject the field, or replay old `skip` semantics

Removal/rejection breaks otherwise valid legacy traffic (AC10). Honoring `skip` for historical snapshots or mode-specific replay breaks AC9. Rewriting old snapshots to `continue` breaks AC10 and OC-003. These are not viable alternatives.

## Comparison and decision

Choose A. Retire policy semantics, not serialized compatibility. A new database migration, versioned policy protocol, `effective_*` field family, or durable simulation-match store does not earn its cost here. Retaining the existing response field with the truthful fixed value is sufficient. The exact source, API, copy, and counting rules are in [ARS-005](../specifications/ars-005-duplicate-continuation-compatibility-results.md).

The compatibility setter is an acknowledged no-op, not a save of the supplied value and not a configuration change. It creates no behavior-only snapshot. Existing low-level history creation/read/diff/copy must remain non-normalizing. Normal saved/proposal producers canonicalize before persistence; old immutable readers do not.

## Consequences

- Old raw `settings.duplicate_behavior` values may remain; they are not an effective setting. No old migration is edited and no new migration is needed.
- An active run may need a new ConfigVersion because its canonical saved snapshot no longer exactly matches old `skip` JSON. Reuse the existing `ActiveVersionID` exact-match mechanism; do not use the greatest history ID or rewrite a run reference.
- Historical replay uses today's fixed continuation policy with the selected snapshot's routes/threshold, not an emulation of obsolete suppression. Its results can differ from earlier runs; old outcome/action evidence remains authoritative for what actually happened then.
- New proposals cannot preserve their submitted legacy handling string as an effective snapshot field: they capture `continue`. Existing persisted proposals remain immutable. No raw request audit is added.
- Restore retains its existing snapshot-copy behavior. A copied legacy field is visibly recorded data; neither the copy nor selecting it for evaluation activates skip. A complete active-restore redesign is outside this decision.
- Successful hypothetical duplicate results enter the existing distinct-video counters through route decisions, not through an extra duplicate counter. A demonstrated existing partial-exclusion/nonmatch overlap must not be repeated in summary/event projections; ARS-005 bounds the correction without inventing mixed-failure precedence.
- Simulation isolation must survive later production execution, not merely the simulation's own completion. Independent reviewer reproductions showed backtest matches leaking into real actions and live playlist edits overriding captured targets. [ARD-001](../design/ard-001-run-owned-production-action-authority.md) chooses current-run matched decisions plus snapshot-resolved actions over run-only filtering, stale-decision replay or a new outbox. Deferred work is reevaluated in the receiving run; interrupted work stays in its original run/snapshot. ARS-005 specifies validation, durable retry ownership, exact-target completion reuse and proof. Existing provider-success/local-record idempotency and broad historical accounting gaps are explicitly not certified by this correction.

## Confirmation and reconsideration

ARS-005 defines the representative composed compatibility, runtime, history, counting, and isolation checks. All are downstream requirements, not executed evidence. Obtain independent critique and assembled verification before relying on the implementation. This architecture-only session ran no runtime tests.

Revisit representation only if a supported consumer demonstrably needs machine-readable recorded-versus-effective metadata beyond the documented endpoint context, or a future accepted requirement introduces another handling policy. Do not activate skip or rewrite history to satisfy such a consumer without new product authority.

## Open questions

- **No user decision blocks this bounded design.** Continue is settled. Similarity sensitivity and pairwise attribution remain unchanged.
- **Implementation / requirements owners, nonblocking for continuation:** complete active configuration restore semantics and broader mixed-failure/zero-route historical accounting are not established by this amendment. Do not claim those paths are fixed by removing the match short circuit. Any new observable failure precedence needs its requirements owner, not an implementation guess.
