---
type: decision
title: "ADR-013 — Duplicate-Reference Eligibility Is Monotonic Across Fingerprint Upserts"
description: "Preserves imported matching-candidate eligibility when native discovery writes the same video identity before duplicate evaluation."
tags: [architecture, decision, yuhaul, duplicate-detection, fingerprints, import, provenance]
timestamp: 2026-10-24T00:00:00Z
---

**Status:** accepted

An imported fingerprint is already valid historical matching data. If native discovery later observes the same video ID before duplicate matching, it writes to the same unique fingerprint row. The discovery write may refresh the fingerprint, but it must not turn off that row's matching eligibility. For example, an imported `v1` row starts eligible; a later native write for `v1` that requests the normal ineligible initial state still leaves the row eligible. When `v1` is evaluated, however, it is not a duplicate of itself: exact and fuzzy detection exclude the row with that same video ID. The retained row can still match a different video whose normalized canonical title fingerprint is equal, or whose fingerprint tokens meet the fuzzy threshold. This preserves the accepted imported-history contract without turning import or matching into evidence of routing or discovery credit.

## Derived from

- [OC-004 — Imported History as Duplicate References](../requirements/yuhaul/oc-004-imported-history-as-duplicate-reference.md)
- [BR-008 — Exact and Fuzzy Duplicate Detection](../requirements/yuhaul/br-008-exact-and-fuzzy-duplicate-detection.md)
- [BR-009 — Historical Title Persistence and Corpus Import](../requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md)
- [BR-010 — Duplicate Provenance for Discovery Credit](../requirements/yuhaul/br-010-duplicate-provenance-for-discovery-credit.md)
- [ARS-002 — Web Corpus Import API Contract](ars-002-web-corpus-import-api.md)
- [YuHaul System Architecture](yuhaul-system-architecture.md) §INV-6 and §Duplicate Detection Engine
- Current persistence evidence: `internal/duplicate/detector.go`, `internal/discovery/engine.go`, and `internal/store/migrations/001_initial_schema.up.sql`

## Satisfies

- OC-004: an imported historical reference remains eligible when native discovery observes the same `video_id` before matching; the detector excludes it for that ID but can use it for different video IDs.
- BR-008 AC5–AC6 and BR-009 AC4/AC8: imported references remain eligible in the full historical candidate set while same-ID import retries remain append-only.
- BR-010 AC2–AC3/AC6: candidate eligibility and a match reference do not create or infer earliest-discovery credit for an imported channel.

## Decision question

How should the single `title_fingerprint` row for a video ID retain imported duplicate-reference eligibility when the native discovery path upserts that same ID with its normal initial value `is_duplicate_reference=false` before duplicate evaluation?

## Drivers and constraints

- `title_fingerprint.video_id` is unique, and `SaveFingerprint` uses an `ON CONFLICT(video_id)` upsert. Multiple fingerprint rows for the same canonical video ID are not the current data model.
- The collision is reachable in the current flow: the importer inserts only `title_fingerprint`, while discovery's prior-existence check looks in `video`; the later discovery path can therefore persist the same ID into the unique fingerprint row before duplicate evaluation.
- The exact and fuzzy detector both admit references using `is_duplicate_reference=true`.
- New imported rows are matching candidates at insertion. Native discovery's ordinary initial write requests `false`; successful routing can later enable native candidates.
- OC-004 requires candidate eligibility to remain independent of current-system routing and discovery-credit evidence. BR-009 AC8 separately requires a same-ID import skip to leave the existing row unchanged.
- Current source evidence shows a correctness gap: `SaveFingerprint` assigns the conflict row's flag from the incoming value, and `internal/discovery/engine.go` calls it with `false`. That can demote an imported candidate before the detector runs. This is an implementation defect, not an accepted exception to OC-004.
- The fix should reuse the existing row and field rather than add a second reference store, duplicate row identity, or migration.

## Options considered

### A. Merge candidate eligibility monotonically on the existing row (chosen)

For a new fingerprint, use the supplied initial value. For an existing `video_id`, resolve the flag as `existing OR incoming`, so a true value survives a later false write and a true incoming value can promote a false row.

- **Benefits:** Preserves imported eligibility and native fingerprint refresh on the existing unique identity; makes repeated same-ID writes idempotent for candidate membership; requires no schema or detector change.
- **Costs:** The upsert must distinguish mergeable eligibility from ordinary refreshable fingerprint fields. The eligibility flag is not a last-writer-wins field.
- **Failure modes:** A buggy reset outside the covered write path could still violate the invariant; integration verification must exercise the real native persistence path before matching.
- **Operational/compatibility impact:** No data migration, new table, API, or deploy coordination. Existing rows already marked true remain true; existing false rows retain current behavior until a legitimate eligibility event enables them.
- **Authority impact:** The flag records candidate eligibility only; it does not write route, action, or discovery-credit evidence.
- **Obligation impact:** Directly realizes OC-004's imported-reference eligibility across the discovery-before-matching ordering.

### B. Skip native fingerprint updates when the video ID already exists

On conflict, leave the whole imported fingerprint row unchanged.

- **Benefits:** Candidate state and import fields cannot be overwritten by discovery.
- **Costs:** Also suppresses the native title/fingerprint refresh on the same identity, even though other native discovery data is stored through the normal path. This couples eligibility preservation to stale-payload preservation and is broader than required.
- **Failure modes:** Later fuzzy matching may use an older title normalization than the native discovery path has observed.
- **Operational/compatibility impact:** No schema change, but changes all conflict behavior and may hide valid fingerprint refreshes.
- **Obligation impact:** Preserves eligibility, but is less complete than A because it unnecessarily suppresses independent field updates.

### C. Store imported references separately or allow multiple fingerprint rows per video ID

Keep the imported row immutable and add a separate row or relation for native observations.

- **Benefits:** Separates imported and native source records explicitly.
- **Costs:** Requires schema changes, detector identity/selection rules, migration and lifecycle rules for multiple rows per video ID, and arbitration for exact-match outputs.
- **Failure modes:** Two rows can disagree on candidate or title state; detection can return an unintended record unless another identity rule is introduced.
- **Operational/compatibility impact:** Adds durable model and migration surface without an accepted requirement for multi-row history.
- **Obligation impact:** Can preserve the guarantee, but adds machinery where a boolean merge on the existing canonical identity suffices.

## Comparison

Option A is the least-complex complete realization: it preserves the existing uniqueness invariant and native fingerprint refresh while preventing a false incoming value from erasing an accepted reference. Option B retains eligibility but also prevents unrelated native fingerprint refresh. Option C provides richer source history, but no accepted obligation requires that history and it introduces new identity and migration rules.

## Decision

For `title_fingerprint` writes keyed by `video_id`:

1. A newly inserted row uses the supplied initial `is_duplicate_reference` value. A valid new `user_import` row MUST be inserted with `true`; a new native `api_discovery` observation retains its existing initial `false` behavior.
2. On conflict with an existing row, the effective eligibility is `existing.is_duplicate_reference OR incoming.is_duplicate_reference`. The native discovery write with `false` MUST NOT demote an imported or otherwise eligible row. A true incoming value may promote an ineligible row.
3. Import retries that find an existing `video_id` continue to skip it without mutation, as BR-009 AC8 requires. Native discovery of that ID is a separate observation on the same canonical fingerprint identity; it MUST update no second fingerprint row and MUST preserve a true eligibility value before duplicate evaluation.
4. The merge rule applies only to candidate eligibility. Other fingerprint fields continue to follow the existing native discovery/import field ownership; this decision does not add historical versions of source, title, channel association, or import batch data.
5. Candidate eligibility is not route completion or discovery credit. The persistence merge itself MUST NOT create or alter route decisions, playlist actions, or earliest-discovery attribution. Any credit remains governed by BR-010 and requires independent evidence of the earliest-discovered instance; an imported `channel_id`, source label, import order/time, or matching-reference identity is not such evidence.
6. After a same-ID native write, the row remains eligible, but exact and fuzzy detection MUST exclude it when evaluating that same video ID. The exact pass compares the normalized canonical title fingerprint against eligible rows with a different `video_id`; it does not look up equality of video identifiers. For another observed ID, the same row remains available to the exact pass when canonical fingerprints are equal, and to the FTS/Jaccard path when the configured threshold is exceeded. This defines eligibility and matching, not a route or credit outcome. The persisted match-type value `exact_video_id` is a legacy schema label for this exact-title path, not its comparison key.

No accepted lifecycle operation revokes candidate eligibility. If an explicit removal/revocation policy is introduced later, it requires its own behavioral contract and must not be implemented by an incidental upsert.

## Consequences

- **Positive:** Imported rows remain exact/fuzzy candidates across native same-ID observation, but are never returned as their own duplicate; the existing unique video identity and shared detector remain intact.
- **Negative:** Candidate eligibility is no longer last-write-wins. A future explicit eligibility-revocation feature cannot reuse an ordinary false-valued upsert.
- **Neutral:** Native observation may refresh fingerprint payload and its current association under existing rules; those values do not define eligibility or discovery credit.
- **Neutral:** Exact detection uses normalized canonical-title fingerprint equality across distinct video IDs. Its persisted `exact_video_id` match-type value remains unchanged for schema compatibility, even though the detector does not compare video IDs for exact matching.
- **Compatibility:** Existing schema and stored data are unchanged. No backfill is needed because this decision governs future conflicting writes; already demoted rows cannot be identified as imports reliably from current source fields and are not reconstructed by this decision.

## Confirmation and reconsideration

The assembled persistence-to-detection proof MUST:

1. Insert a valid imported fingerprint for `v1` with eligibility true.
2. Persist a native discovery observation for `v1` through the real discovery write path with the normal incoming false value, before duplicate matching.
3. Verify that exactly one fingerprint row remains for `v1` and it remains eligible. Evaluating `v1` must return no duplicate from either exact or fuzzy detection. Evaluating a distinct `v2` whose title normalizes to the same canonical fingerprint must return `v1` as an exact match, independent of the fuzzy threshold; a distinct `v3` whose title shares fingerprint tokens must still be eligible for the existing FTS/Jaccard path.
4. Verify that the eligibility merge itself creates no route/action/discovery-credit evidence and that the imported channel association is not treated as earliest-discovery evidence.
5. Verify control cases: a new native row stays ineligible initially; a true incoming eligibility event promotes a false existing row; same-ID import retry leaves the existing row unchanged; invalid imports do not create candidates.

The current implementation evidence is `internal/duplicate/detector.go`: `exactMatch` queries `fingerprint = ?`, filters `video_id != ?`, requires `is_duplicate_reference = 1`, and selects the oldest eligible row by `id`; `fuzzyMatch` likewise filters out the evaluated `video_id` before scoring FTS candidates. `internal/duplicate/detector_test.go` covers canonical exact matches across distinct IDs, same-ID exclusion, and imported eligibility across native upsert; `internal/pipeline/duplicate_match_test.go` covers no self `duplicate_match` row while a distinct duplicate is recorded. The requirements bundle's current reconciliation log reports detector, pipeline, replay, and assembled import→native-persistence→detection regression coverage against SQLite. This records test coverage evidence only; it does not claim Product Acceptance is complete.

Reconsider this choice only if the accepted model changes to retain multiple fingerprint versions per video, eligibility revocation becomes a product requirement, or a measured data/consistency risk shows the existing unique-row model cannot preserve the required candidate lifecycle.

## Open questions

- None. Discovery-credit attribution remains governed by BR-010; this decision does not infer or resolve earliest-discovery evidence.
