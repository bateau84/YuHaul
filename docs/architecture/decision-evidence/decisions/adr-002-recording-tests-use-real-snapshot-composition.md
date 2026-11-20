---
type: decision
title: ADR-002 — Recording Tests Use Real Snapshot Composition
description: Keep recording fail-closed and move snapshot-dependent router tests outside the router package instead of adding a validation bypass or moving configuration ownership.
tags: [architecture, decision, testing, snapshots, evidence]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted technical realization within ARS-002's unchanged contract; implementation and runtime proof outstanding.

Recording tests will use the real pipeline-owned validator and a persisted run snapshot. Move the recording cases to `package router_test`; keep private predicate and store-only tests in `package router`. No runtime package extraction, second validator or test-only recording permission is needed.

For example, a test creates a route, captures configuration through the real configuration service, creates a running manual run linked to that version, and constructs the Engine through normal pipeline composition. Direct `Evaluate` then records its normal decision and audits. Changing an exclusion afterward makes live-route evaluation against that old run fail, rather than silently changing the run's meaning.

This decision changes neither ARS-002 nor product behavior. It addresses how to implement and prove its already accepted recording preflight. It does not authorize live operations, retention, legacy backfill or production enablement.

## Derived from

- [ARS-002 — Causal Exclusion Capture and Inspection](../specifications/ars-002-causal-exclusion-capture-and-inspection.md), especially Recording preflight and its focused proof.
- [OC-003 — Configuration Snapshot](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md).
- [OC-005 — Decision Evidence Attribution](../../../requirements/yuhaul/oc-005-decision-evidence-attribution-and-availability.md), Guarantees 4–5 and 9, verification cases 6–10.
- Parent's focused request: resolve recording-test import cycles without weakening fail-closed validation or undertaking unrelated migration.

## Satisfies

| Obligation | Allocation |
|---|---|
| ARS-002 recording preflight; OC-003 snapshot identity/immutability | Real persisted run/configuration and shared validator in recording fixtures; missing dependency remains an error. Runtime proof required. |
| OC-005 G4–5 and cases 6–7 | Ordinary recording tests retain their decisions/audits assertions and add binding coverage; no alternate matcher or permissive callback. |
| OC-005 G9 and case 10 | Existing rows and conflict-winner reuse remain unchanged; snapshot-correct fixtures do not authorize enrichment. Atomicity/recovery proof remains allocated to ARS-002. |
| HTTP/UI explanation, positive configured-value release, historical-input reproduction | Satisfied elsewhere or unresolved as classified by ARS-002; this test-boundary choice does not claim to deliver them. |

## Decision question

Where should snapshot-dependent recording tests live when configuration and pipeline already depend on router, but router must not import them?

## Drivers and source evidence

Bounded source reads on 2026-11-07 establish:

- `internal/router/engine.go:95–137` records explicit arrays without validation. `ExpectedEvaluations` and `evaluateRoute` already provide non-recording evaluation; recording also preserves persisted-winner behavior at lines 127–134.
- `internal/router/engine_test.go:38–53` creates a running manual run without a configuration link. Direct recording calls and `evaluateWithVideo` use it. Several direct cases create the run before configuring routes. The file also contains store-only tests, so changing its package blindly would strand `setupTestDB` used by `store_test.go` and `route_identity_test.go`.
- `internal/config/service.go:8–9,64–85` imports router and provides `ActiveVersionID`, which captures effective saved configuration. `internal/pipeline/pipeline.go:13–22,134–170` imports config/router and constructs Pipeline after applying options. Therefore importing either package from internal `package router` tests creates a cycle; external tests can import all three without changing production dependencies.
- `internal/pipeline/authority.go:19–56` owns persisted production authority and mode-specific preparation. `dryrun.go:115–126` loads snapshot-only simulation routes. `exclusion.go:35–47` and `authority.go:214–223` identify the separate gate and missing-legacy-insert validation seams.
- Literal Pipeline fixtures exist in `dryrun_test.go:30`, `replay_test.go:31` and other focused suites. Constructor-only wiring will not repair those fixtures automatically.

Semantic navigation was attempted but degraded because gopls reported `authorityTestTransport is not a function`; these claims use direct reads/searches, not a successful semantic graph. No tests were executed for this decision.

## Options considered

| Option | Benefit | Cost and failure modes | Disposition |
|---|---|---|---|
| External recording tests plus real snapshot fixture and normal constructor wiring | Preserves recording/audit/reuse coverage and current package ownership; no runtime extraction | Mechanical qualification/movement of recording cases; run creation must occur after route setup; keep internal shared DB helper available | **Choose.** Smallest complete change to the demonstrated dependency problem. |
| Neutral validation package | Could make the real validator importable by internal router tests | A package accepting `[]router.Route` still cycles when imported by internal router tests. Breaking that cycle requires moving route types or another representation/conversion. Moving authority also risks duplicating mode checks. No demonstrated production reuse earns this cost | Reject for this slice. Revisit only for a real second runtime consumer with incompatible dependency needs. |
| Separate pure evaluation and recording further | Pure predicate tests need no snapshot | Such seams already exist. Replacing recording tests with pure results loses persisted-winner, audit and final-route recovery proof; a new unguarded recording API would violate ARS-002 | Retain existing pure tests; no further runtime split. |
| Internal fixture serializes snapshot JSON or reimplements validation | Avoids moving test packages | Duplicates configuration conversion/validation and does not prove production binding; an always-success callback conceals missing composition | Reject. |

## Decision and architectural constraints

### Production boundary

Router declares the synchronous validator dependency selected by ARS-002, without importing pipeline/config. Its guarded recording entry rejects an absent validator before predicates or writes. `Evaluate` delegates to that same entry. Do not add a permissive default, special test run ID, build-tag bypass, unchecked recording export or empty-route exception.

Pipeline retains the real validator implementation and exact effective-definition comparison. Reuse its production authority and snapshot-only simulation preparation, preserving each mode's existing ordering. Register that method on a non-nil Engine in `NewPipeline` **after options are applied**, so the callback sees the configured store. No new exported pipeline validation API is required solely for tests. A nil Engine remains permissible for fixtures that never route; it does not grant recording permission.

The gate and `completeLegacyEvaluation` call the same validator directly at their existing ARS-002 boundaries. Gate validation precedes the video loop, including empty input and simulation. Constructor injection alone is not gate or legacy-insert protection. Existing production admission remains in force; simulation is not routed through production authority checks.

### Test boundary

Place existing tests that invoke `Evaluate`/`EvaluateWithRoutes` in external `package router_test`. Preserve their behavioral assertions, especially decision/audit persistence, final-route behavior and soft-delete history. Move only affected cases/helpers; leave store-only/private tests and the shared internal DB helper available to current internal consumers. A small separate external DB fixture is preferable to converting unrelated suites or introducing a shared runtime test-support package.

The external recording fixture opens migrated temporary SQLite and constructs the real state/router/config stores, configuration service, Engine and `NewPipeline` with its real config store. Retain the Engine handle and invoke its public recording API directly; Pipeline construction supplies the real dependency. Unused dependencies may be nil because this fixture does not execute the pipeline lifecycle. No provider call is required.

**Fixture lifecycle:** configure routes, capture a version using the real configuration service, then create the run with that version already linked and discover the video. Move early `createRunAndVideo` calls below route setup; do not repair them by relinking an already executing run or recapturing inside the validator. The multi-video helper may capture/create a fresh run per helper invocation, as it already creates a fresh run. Recovery tests deliberately reuse their original run and snapshot. Subsequent mutable-route changes must not automatically refresh that link.

Pipeline tests that actually record must use `NewPipeline` composition. Upgrade the affected dry-run/replay setup helpers to provide persisted selected snapshots, not mutable fallback. Literal fixtures testing only unrelated lifecycle/queue behavior need not migrate. A literal fixture deliberately testing a failed preflight may remain unwired and assert rejection. Do not bind an Engine to one Pipeline and silently reuse it for another store/configuration owner.

Internal router tests may use a rejecting spy to prove call ordering and error propagation. Successful composition tests must use the real validator, not a blanket success callback. Pure matching and `ExpectedEvaluations` remain usable without validation; their results alone do not authorize insertion.

## Ownership and dependency constraints

These are integration constraints, not a delivery task graph. The task-manager owns sequencing and assignments.

- **Router implementation owner:** dependency declaration, fail-closed entry and existing recording/reuse semantics.
- **Pipeline implementation owner:** real validator, constructor binding, both gate modes and legacy-insert preflight; preserve failure/retention behavior.
- **Test implementation owner:** external recording fixture/case moves and only the pipeline fixtures that cross recording seams. Keep shared internal helpers available.
- **Reviewer:** independent assembled-path proof against ARS-002. Unit spies cannot certify real snapshot binding.

The validator must exist before real constructor wiring can be exercised. Fixture capture must precede run creation; real binding must precede any successful recording assertion. Guard, composition and affected fixture changes must be verified together before treating the change as complete. A temporarily red intermediate edit is not a reason to restore permissive production behavior. No unrelated fixture migration is a prerequisite.

## Consequences

Immediate benefit: one real validator, unchanged dependency direction and meaningful existing persistence tests. Cost: bounded movement/qualification of recording tests and correcting their run-creation order; affected pipeline setup helpers need real snapshots. There is no new runtime service, store, protocol, route model or validation authority. The change is reversible at the test-layout level without reopening the mandatory recording guard.

## Confirmation and reconsideration

Required evidence, not executed here:

- Existing recording tests pass with real snapshot composition and their original decision/audit/recovery assertions intact. No test silently substitutes `ExpectedEvaluations` for recording.
- Missing dependency, missing persisted snapshot and same-ID mismatched arrays fail with no new decisions/audits. Include empty route arrays and a first route that would match, so a late guard cannot pass.
- A valid snapshot succeeds; editing live exclusions afterward rejects `Evaluate` for the old run while explicit unchanged snapshot routes remain valid. Do not refresh the test run's link to make this pass.
- ARS-002's complete-definition mutation matrix, malformed-field checks, both gate modes, ordinary production/dry-run/replay/backtest recording, retry and missing-legacy insertion use the same real validator. No partial writes/video-excluded transition/action can arise from a rejected call.
- Existing generic-only rows and valid recorded references remain unchanged on reuse; stored-winner behavior remains authoritative.
- Run focused router and affected pipeline suites, then repository regression checks. Classify unrelated failures rather than weakening the guard or claiming unobserved success.

Reconsider extraction only if a demonstrated additional runtime consumer cannot use existing pipeline composition, or the bounded fixture movement requires private router access that cannot be separated without losing assertions. Neither is demonstrated by the recording cases inspected here. An independent bounded review is recommended before reliance.

## Open questions and limits

No unresolved product policy blocks this implementation/testing choice. Exact runtime conformance remains unproved and is implementation/reviewer-owned. Configured-value eligibility and historical-input retention remain unresolved under ARS-002; they do not block snapshot-binding capture and are not resolved by these fixtures. The parent's report of two reverted coder attempts was not independently reconstructed; the actual fixture and import-cycle causes were inspected directly.
