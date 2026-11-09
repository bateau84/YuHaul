---
type: decision
title: ADR-001 — Capture the Causal Exclusion as an Immutable Snapshot Reference
description: Record which configured exclusion actually caused a future route skip without retaining compared provider input, inventing legacy causes or claiming complete historical reproduction.
tags: [architecture, decision, exclusions, evidence, snapshots, privacy]
timestamp: 2026-11-07T00:00:00Z
---

**Status:** accepted — bounded technical realization for future decision capture; not implemented or runtime-verified by this artifact. Full evaluated-input retention and historical reproduction remain blocked separately.

## Decision

**Yes: record the exact configured exclusion occurrence that actually caused a new route skip.** Store a small reference into that run's immutable configuration snapshot, atomically with the skipped decision. Do not copy the compared YouTube title, channel name, description, duration, substring, hash or other evaluated-input representation into this new evidence.

For example, inspection can say: “Recorded causal exclusion: New Listens, snapshot 42, exclusion #3 — title contains the configured phrase ‘live’. Compared input unavailable; this record does not retain the title evaluated.” The phrase is shown only when its configured value is permitted for inspection. This answers **which rule**. It does not independently demonstrate **why the original input matched**, reproduce that comparison, or prove that the run completed.

This is a separate useful slice of the accepted explainability obligation, not a replacement for it. Outside BR-015 AC13's user-approved generic-only legacy boundary, BR-015 AC7 and the complete compared-input portion of OC-005 remain binding and unresolved. The durable-title conflict is also unchanged. Neither is declared satisfied by this reference.

## Derived from

- [BR-007 — Exclusion Behaviors](../../../requirements/yuhaul/br-007-exclusion-behaviors.md), especially AC7–8: exclusions are route-scoped and the causal rule is inspectable.
- [BR-015 — Explainable Route Decisions](../../../requirements/yuhaul/br-015-explainable-route-decisions.md), AC1–3/5, mandatory all-mode/list-detail AC10–13 and unchanged AC7; [OC-005](../../../requirements/yuhaul/oc-005-decision-evidence-attribution-and-availability.md), Guarantees 4–5/7–12 and verification cases 6–10.
- [OC-003 — Configuration Snapshot](../../../requirements/yuhaul/oc-003-configuration-snapshot-for-historical-interpretation.md), immutable captured configuration, not current-rule reconstruction.
- [Parent decision-evidence ARD](../design/ard-001-run-attributed-evidence-and-metadata-lifecycles.md), snapshot reference and atomic capture direction; raw-input retention remains unresolved there.
- User's 2026-11-07 focused request: identify the exact causal exclusion for **future** decisions, without interpreting that request as permission to retain historical provider input indefinitely. The supplied Fireship/New Listens screenshot description was not independently observed here; no live run was queried or changed.
- Subsequent user approval, recorded in BR-015 AC13 and OC-005 Guarantee 9: old generic-only exclusion rows may display “rule unavailable” and remain explicitly incomplete. The relayed answer “Yes, that is ok” accepts only this legacy boundary; it does not waive new capture or authorize provider retention. Original human conversation not independently inspected; exact provenance is in BR-015.

## Satisfies

| Obligation | Disposition |
|---|---|
| BR-007 AC8; BR-015 AC3; OC-005 Guarantees 4–5, rule-identification portion | Selected realization records one actual causal predicate occurrence and resolves its unchanged configuration, scope and comparison meaning. No enumeration of other potential matches. |
| BR-015 AC1/5; OC-003 | Reuse the existing run/route decision identity and immutable snapshot. Persistence survives restart; current edits do not redefine the pointer. Runtime proof remains required. |
| OC-005 Guarantees 7–10 | Preserve route versus video scope, distinguish legacy absence and value withholding, release only relevant permitted configured evidence. Current/local recognition stays separate. |
| OC-005 Guarantees 11–12 | No provider repair or production action is introduced; simulation capture remains hypothetical and run-scoped. Existing mode gates still apply. |
| BR-015 AC10–13; OC-005 cases 6–10 | ARS-002 specifies mandatory future capture and list/detail explanation. Generic-only new skips fail, including a new evaluation of a video with old history. User-approved old generic-only rows display “rule unavailable” in both surfaces, preserve supported outcomes and remain unchanged on reuse. Implementation and composed proof remain outstanding. |
| BR-015 AC7 outside AC13's accepted legacy boundary; OC-005 compared-input/reproduction portion; BR-009 title preservation | **Still unresolved/blocked**, not satisfied by a reference or the recorded fact that a branch matched. Accepted legacy incompleteness is not recovered/reproducible input. This decision grants no raw-input retention basis. |

## Decision question and constraints

Can the system durably answer “which exclusion made this route skip?” before retention authority for historical compared input is settled? It can, within the already accepted configuration/operational-decision model: the new data identifies which configured branch the application took. It adds no source-content archive or new source-derived metric. This is not a claim that every derived Boolean, audit payload or copied configured value is exempt from provider policy.

The earlier ARD coupled reference capture and compared-input storage too broadly as one blocked unit. The independent reference is now selected. The gate on new raw-input retention, historical reproduction and old audit lifecycle is unchanged. No user decision about a weaker historical promise is needed to add this truthful partial evidence.

## Current evidence and ordering

Source read on 2026-11-07:

- `internal/config/models.go::SnapshotExclusion` contains type/value, with no exclusion UUID or persisted child ID. `config/service.go` appends the exclusions to the snapshot in the supplied sequence.
- `internal/pipeline/dryrun.go::routesFromSnapshot` preserves that array sequence. `pipeline/authority.go::productionAuthority` sorts routes, conditions and actions, but does not reorder exclusions.
- `internal/router/engine.go::Excluded` walks `route.Exclusions` and returns on the first match. It currently loses the index and returns only `exclusion`; `evaluateRoute` then records a generic skipped decision.
- `internal/pipeline/exclusion.go` separately records generic all-route exclusion skips through `RecordRouteDecision`; both producer paths need the same causal-reference contract.
- `internal/state/machine.go::RecordRouteEvaluation` inserts decision/audits transactionally, and an existing run/video/route tuple is reused without rewriting history. `RecordRouteDecision` uses a separate plain insert path; it must not become a bypass of causal capture for new exclusion skips.
- `internal/router/store.go::listExclusions` has **no SQL ORDER BY**. Neither a type priority nor a live-row-ID ordering can be inferred from it. The reference must use the actual immutable snapshot's sequence, not a reconstructed current order.

Semantic lookup was attempted first and returned degraded gopls analysis; bounded source reads ground these claims. No source changes or runtime tests were performed.

## Options considered and comparison

| Option | Benefits | Costs and failure modes | Decision |
|---|---|---|---|
| Snapshot-scoped occurrence reference | Reuses accepted immutable configuration; preserves duplicate entries; no new user-facing identity lifecycle or predicate-value copies | Depends on unchanged snapshot and exact array mapping; capture/read must reject ambiguous or invalid references | **Chosen**, with validation below. |
| Add stable exclusion UUIDs throughout configuration/history | Can identify a rule across multiple configuration versions and editor moves | New child identity, migration, proposal/import/restore and API machinery; duplicate/new/deleted rule lifecycle must be defined. Does not prove which occurrence ran unless captured then | Viable but unnecessary: requirement is identity **within the run's snapshot**, not cross-version rule continuity. |
| Copy type/value into every decision | Self-contained predicate if a snapshot later disappears | Duplicates potentially sensitive/provider-origin configured strings, adds retention/release surfaces, and loses occurrence identity for identical entries unless more identity is added | Reject: existing immutable snapshot already does the job. |
| Infer from current rules, one configured candidate, audits or a later reevaluation | Little new capture | Does not prove the causal branch used then; edits, duplicates and metadata changes create silent wrong answers | Reject: violates OC-005 attribution and legacy semantics. |
| Wait for full historical-input permission before adding any rule evidence | No new pointer field | Leaves an independent accepted “which rule” need unmet although a source-content-free reference can answer it | Reject the blanket coupling; full raw-input capture remains gated. |

## Minimum persistence and producer contract

### Identity and interpretation

The semantic reference is `(run_id, video_id, route_id, config_version_id, exclusion_index, semantics_version)`:

- Run/video/route identity already belongs to the persisted `route_decision`. The immutable run's `config_version_id` supplies the snapshot; do not duplicate or substitute it from today's active configuration.
- `exclusion_index` is **zero-based across the complete exclusions array of that route in that exact snapshot**, not a per-type index, database child ID, sorted display position, or index after filtering invalid/unknown types. The public route identity remains its existing UUID mapping, not its internal integer key or display name.
- `semantics_version = 1` identifies the comparison meanings below. Its purpose is to prevent future matcher changes silently changing the explanation of an old reference. An unsupported version cannot be rendered using today's semantics.
- The minimal persisted extension is a nullable nonnegative index and nullable semantics version on the existing decision row. Both are null for uncaptured legacy/non-exclusion decisions; for a new captured exclusion both are present. No copied provider input or configured value is needed. Exact column/wire spelling and migration number belong to the follow-up specification; this ADR fixes the identity and invariants, not an accidental DTO.
- The referenced snapshot must resolve to exactly one matching route and an in-range entry of a supported exclusion type. A route name is not a key. A pointer validated against a different snapshot is invalid, even if that entry happens to have the same text.

### Capture, atomicity and recovery

1. The predicate returns the first matched occurrence **at the comparison that causes the skip**. Capture its snapshot position with the returned match, not by running the predicate again afterward or scanning for an equal value.
2. Preserve the matcher order and semantics. There is no new channel-before-video-before-title priority. Identical entries and matching entries of different types remain distinct occurrences; the first actual match in the snapshot sequence wins. A human label may use `index + 1` as “exclusion #N”, explicitly in the run's snapshot.
3. Both all-route filtering and ordinary routing bind their new excluded decisions to this result. Fully excluded videos record the appropriate cause for each enabled route that actually excludes them; partial exclusions stay attached to their route. Disabled/final-route skips receive no exclusion reference. A survival check that does not produce a route skip is not itself a captured causal decision.
4. Resolve/validate the run's snapshot identity before publishing a new reference-bearing skip. The decision, skip reason, index and semantics version commit as **one atomic decision write**, using the existing route-evaluation persistence seam or its equivalent atomic insert. Do not insert a generic skip and attach its cause later. No second evidence store, outbox or metadata transaction is needed for these scalar fields.
5. New corrected producers must supply the reference for a newly evaluated exclusion skip. Invalid/unresolvable new references are processing/persistence errors under existing failure handling, not successful precise evidence. A returned result is the durably persisted decision/reference, not merely the candidate held in memory.
6. Reuse of an existing tuple returns its existing recorded identity unchanged. An old generic skip stays generic; even a single snapshot exclusion or a later metadata match does not justify filling it in. The existing atomic reuse/conflict boundary must prevent mixing one contender's decision with another's index. All successful callers see the persisted winner; a failing insert reports its existing persistence error rather than invented evidence. There is no new retry policy.
7. A restart, metadata refresh, expiry, rule edit, route rename/deletion or retry cannot change the identity of a captured decision. A fresh receiving run uses its own accepted snapshot and records its own new decision; it does not repair the originating run.

### Comparison meanings for version 1

These meanings describe existing matcher behavior, not new operators or changes to filtering:

| Snapshot `exclusion_type` | Field / comparison meaning |
|---|---|
| `channel` | Video channel identifier equals the configured identifier; exact string comparison. Not channel display-name equality. |
| `video` | Video identifier equals configured identifier; exact string comparison. |
| `title` | Title equals configured title after trimming both sides' surrounding whitespace and Unicode case-insensitive equality. |
| `word` | Lowercased title contains the lowercased configured substring. Despite the type name, this is **not** a whole-word boundary test. |
| `duration_max` | A known duration is strictly greater than the configured integer maximum. Equality is not excluded by this rule. |
| `duration_min` | A known duration is strictly less than the configured integer minimum. Equality is not excluded by this rule. |

Current duration thresholds use the matcher's integer parsing; unparseable thresholds or absent duration do not produce those duration matches. Unknown exclusion types do not produce a match. This decision does not normalize configuration, change missing-input evaluation behavior, or reorder rules. Any later semantic change must use a distinguishable semantics version; do not reinterpret version 1.

## Inspection and safety contract

Inspection resolves the pointer through the selected run and unchanged snapshot. It exposes the captured occurrence, configured type/comparison, scope and the recorded fact that this was the causal match. A permitted configured value/threshold may be read from that snapshot; it must be labeled **configured**, never the compared provider input. Release only that relevant predicate, not a full configuration dump. No copied actual input, substring, fingerprint, source-derived match score or all-predicates result matrix belongs to this new capture.

Configured data is not automatically safe merely because it is configured. OAuth material, arbitrary diagnostics and restricted provider-origin text do not become releasable through the snapshot reference. If the configured value is not permitted for display, show the valid identity/type and explicit “configured value unavailable for display”; do not echo it into another field, log, SSE payload or diagnostic. This decision neither authorizes retaining a prohibited configured value nor resolves existing snapshot provenance defects. The pointer can remain useful without releasing that value. Independently authored permitted rule values need no new provider-data retention claim.

The existing accepted run/configuration/operational-decision model is the authority for this **branch identity**, not a blanket legal exemption for arbitrary derived data. Earlier unresolved policy questions about stored actual values and newly invented derived representations are not answered by calling them operational. If compliance establishes that this precise source-content-free branch-reference representation is itself prohibited, revisit this bounded decision with that evidence. No such blocker was established in this assessment, and no new legal permission is asserted here.

The UI explanation must separate:

- **Which rule:** “Recorded causal exclusion: {snapshot route}, exclusion #{N} — {configured type/comparison/value when permitted}.” The snapshot version and occurrence distinguish duplicate values and later edits. This is route evidence, not an overall video rejection.
- **What is missing:** “Compared input unavailable in this rule-reference record.” Do not render absent input as an empty string or zero, borrow another route's condition-audit actual, or relabel `current_local_metadata` as that input. The pointer proves the recorded branch identity, not independent reevaluation of the original data.
- **Legacy or unresolvable reference:** null legacy identity, missing/corrupt snapshot, out-of-range index, unsupported type/version or failed resolution never triggers current-rule/provider reconstruction. Preserve the supported skipped disposition and state that the specific rule or its definition is unavailable as appropriate. A database read failure is a read failure, not evidence that the rule never existed. Do not collapse a valid captured reference with a withheld configured value into “no rule captured”.
- **Other routes and mode:** Fireship's separately recorded condition non-match does not identify New Listens' exclusion. A route-specific skip is not proof that every route excluded the video. Simulations remain hypothetical and execute no actions; no action target or success is inferred from the reference.

No live Run15 record is changed or backfilled. The user accepts existing generic-only exclusion history displaying “rule unavailable” in list and detail, with supported skip and overall outcome preserved. Those records remain incomplete, not recovered or reproducible. Reusing an old decision preserves that absence; a new evaluation, even of the same video, must capture its actual causal rule.

## Consequences, documentation and confirmation

The improvement is independently implementable without deciding whether original evaluated titles may be kept. The cost is one forward additive decision-schema change, predicate return/persistence plumbing in both producer paths, snapshot resolution in inspection and bounded UI copy. It introduces no new service, stable-rule-ID subsystem or input archive. Captured identity depends on retaining its immutable configuration context; a missing definition remains an explicit limit, not a reason to retain redundant sensitive text.

[ARS-002 — Causal Exclusion Capture and Inspection](../specifications/ars-002-causal-exclusion-capture-and-inspection.md) now supplies exact nullable columns, migration/validation, both producer paths, legacy admission/reuse, shared list/detail projection, incomplete/withheld/read-failure states and compatibility. It keeps current/local metadata ARS-001 unchanged. Designer owns final presentation within these meanings, including the accepted legacy “rule unavailable” display. Specifier's AC10–13 remain binding, with AC7 unchanged outside the explicit AC13 exception. No product permission question blocks branch capture. Positive configured-value release needs eligibility evidence; raw input/history permission remains unresolved separately. The legacy-scope question is settled, not implementation-complete. This ADR's earlier reference to an outstanding exact specification is superseded, not an implementation-completion claim.

Required confirmation, not executed evidence:

- Real producer → SQLite → restart → registered HTTP/UI proves the actual exclusion occurrence for channel, video, title, word and both duration bounds; boundary equality and word-substring semantics match version 1.
- Two matching rules, duplicate values, duplicate types and changed order prove the first actual snapshot occurrence is captured, not a type-priority or equal-value guess. Reordered/edited/deleted current rules cannot alter old references.
- Fully and partially excluded videos exercise both producer paths; disabled/final skips have no pointer. Another matched route retains its own outcome/action evidence and simulation remains hypothetical.
- Fault injection proves decision/reference atomicity, no generic-first partial publication and persisted-winner reuse without legacy enrichment. Null old rows remain unchanged across migration/reopen/resume; list/detail show “rule unavailable” and preserve supported outcomes. A new decision for the same video captures its own actual cause. This passes the accepted legacy boundary, not reproduction verification.
- Missing/invalid snapshot, unsupported version/type, index errors, withheld configured values and database read failure remain distinct supported limits. No current metadata or adjacent condition audit is promoted to actual excluded input.
- Sentinel compared titles/names/descriptions/durations are absent from **new reference fields/payloads/logs**; test them separately from preexisting condition-audit storage. Backtest/inspection makes zero provider calls and changes no production idempotency/corpus/statistics. Do not mistake these tests for remediation of old raw audits.

Independent critique is recommended before relying on this implementation contract. No runtime, migration, browser, policy-exception or whole-product verification was performed by this decision task.

## Open questions and revisit triggers

No user-owned choice is required for the snapshot-occurrence representation. **Legacy scope is resolved:** BR-015 AC13 accepts explicitly incomplete old generic-only rows displaying “rule unavailable”, never inferred or backfilled. This does not excuse any new exclusion or make the old input reproducible. Retention question `75c4d87b-cd87-400e-93cc-4876f0709d8a` remains unresolved for full compared-input/history retention outside that boundary. It does not block independent future capture. Revisit representation if immutable snapshot/order cannot be preserved, cross-version continuity becomes accepted scope, or policy evidence changes permitted treatment of this precise branch reference. Configured-value eligibility is not established by being configured; ARS-002 records the positive-release evidence gap without widening retention.
