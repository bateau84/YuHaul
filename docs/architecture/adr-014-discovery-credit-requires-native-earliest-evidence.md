---
type: decision
title: "ADR-014 — Discovery Credit Requires Native Earliest-Discovery Evidence"
description: "Realizes accepted pairwise discovery credit from native chronology for each selected match, without imported-source credit or transitive fuzzy-group attribution."
tags: [architecture, decision, yuhaul, duplicate-detection, provenance, discovery-credit, import]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted

An imported fingerprint can help YuHaul recognize a video without proving which channel first discovered it. For each observed selected distinct match, retain both video identities and their available channel associations, and compare the two native `video` records: the earlier observation earns discovery credit and the later one earns relevance credit for that match. An imported fingerprint's optional channel association is not evidence. If either native record is missing or the native observation times cannot be ordered, neither side receives chronology-based credit; the selected match remains identifiable. Attribution is pairwise, not a globally original channel or a credit propagated through fuzzy-match chains, and does not require returning every eligible candidate. An eligible same-ID fingerprint remains in the corpus, but the duplicate detector excludes it from both exact and fuzzy results when that same video is evaluated. It therefore produces neither a returned duplicate nor a new `duplicate_match`; statistics also exclude legacy persisted self-match rows.

For example, an imported `v1` associated with `UC-import` can remain eligible after native discovery records `v1` from `UC-native`. Evaluating `v1` does not return that row as an exact or fuzzy duplicate; a distinct `v2` whose normalized title matches `v1` can use it as an exact reference. For a distinct matched pair `v1`/`v2`, whichever native `video.discovered_at` is earlier owns discovery credit; the later video is source relevance. If `v1` has no native video record or the times cannot be ordered, no chronology-based credit is awarded. The imported association never earns credit.

## Derived from

- [BR-010 — Duplicate Provenance for Discovery Credit](../requirements/yuhaul/br-010-duplicate-provenance-for-discovery-credit.md)
- User approval recorded in BR-010, 2026-10-25: “Ok. i will follow your recommendation” accepts attribution per observed selected pair, retains copies/channel associations, and excludes a global fuzzy-group origin. BR-010 AC7–AC8 resolve the former pairwise-versus-group-global question; this approval does not decide duplicate routing, excluded-video evidence, or native reference eligibility.
- [OC-004 — Imported History as Duplicate References](../requirements/yuhaul/oc-004-imported-history-as-duplicate-reference.md)
- [BR-008 — Exact and Fuzzy Duplicate Detection](../requirements/yuhaul/br-008-exact-and-fuzzy-duplicate-detection.md)
- [BR-009 — Historical Title Persistence and Corpus Import](../requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md)
- [ADR-013 — Duplicate-Reference Eligibility Is Monotonic Across Fingerprint Upserts](adr-013-monotonic-duplicate-reference-eligibility.md)
- [ARS-002 — Web Corpus Import API Contract](ars-002-web-corpus-import-api.md)
- [YuHaul System Architecture](yuhaul-system-architecture.md) §INV-6, §Duplicate Detection Engine, and §Statistics and Analytics
- Current persistence evidence: `internal/store/migrations/001_initial_schema.up.sql`, `internal/discovery/engine.go`, `internal/state/machine.go`, `internal/duplicate/detector.go`, `internal/pipeline/pipeline.go`, `internal/migration/migration.go`, and `internal/stats/store.go`

## Satisfies

| Obligation | Classification | Realization |
|---|---|---|
| BR-010 AC1–AC5 | Satisfied by this decision and the existing `duplicate_match` / statistics boundary | Distinct duplicate events retain the current video and matched fingerprint identity. Summary and per-video provenance assign discovery/relevance roles according to the two native observations' chronology, not reference direction. |
| BR-010 AC6 | Satisfied by this decision; importer boundary remains in ARS-002 and OC-004 | A supplied import channel, import order/time, fingerprint source, and match identity cannot grant discovery credit. |
| BR-010 AC7 | Satisfied by this decision | Missing native evidence for either member, or equal/unavailable/unorderable discovery times, leaves both chronology-based roles unknown without discarding selected-pair identities or available associations. |
| BR-010 AC8 / BR-008 AC6 | Satisfied by this decision | `duplicate_match` retains the evaluated video and selected distinct reference, not a global original. Each recorded pair is attributed independently; no transitive credit, all-candidate enumeration, or canonical fuzzy-group identity is required. |
| OC-004 Guarantees 1–3 | Satisfied together with ADR-013 and ARS-002 | Imported rows remain exact/fuzzy candidates across same-ID native upserts, but the detector excludes the evaluated ID from both result paths. Eligibility does not create a distinct duplicate event or discovery credit. |
| BR-008 / BR-009 | Satisfied elsewhere | Candidate eligibility and append-only same-ID import retry remain governed by BR-008, BR-009, OC-004, ADR-013, and ARS-002. |
| OC-002 | Satisfied elsewhere; not changed | Duplicate provenance does not change a video's primary outcome classification. See OC-002 and the system architecture outcome model. |

## Decision question

When an imported fingerprint is eligible for matching but its `channel_id` is only an association, what durable evidence may the statistics path use to award discovery credit, and how must it treat an exact match where the current video and reference have the same canonical video ID?

## Drivers and constraints

- BR-010 AC2/AC7–AC8 requires the earlier native observation within each observed selected pair, not merely the reference row or a globally earliest fuzzy-group member. BR-008 AC6 identifies that selected distinct reference, not an original.
- OC-004 explicitly separates imported-reference eligibility from discovery credit and says import time/order and a supplied channel association do not establish earliest discovery.
- ADR-013 preserves one unique `title_fingerprint` row per `video_id`; native upserts may refresh its `source` and `channel_id`, while `is_duplicate_reference` is merged monotonically. Those fingerprint fields are therefore not stable discovery evidence.
- The existing `video` row is created by the native discovery path before duplicate evaluation, is unique by `video_id`, and stores that video's `channel_id` and `discovered_at`. The importer inserts only `title_fingerprint`; it does not create a `video` row or channel-discovery record.
- `channel.first_discovered_at` is a channel-level aggregate, not a timestamp for the matched video. It cannot decide which of two video instances was first.
- `duplicate_match` identifies the evaluated video and matched fingerprint. It does not itself prove that the fingerprint's optional channel is the channel that first surfaced the content.
- No accepted requirement calls for retaining multiple fingerprint versions or a duplicate-cluster identity. The solution should reuse available per-video native evidence and avoid a new persistence subsystem unless the evidence cannot satisfy BR-010.

## Options considered

### A. Credit from the matched fingerprint's current `channel_id` or `source` — rejected

Continue using `title_fingerprint.channel_id` as the discovery-credit owner, perhaps gated on `source='api_discovery'`.

- **Benefits:** Smallest query change; no new join or state.
- **Costs:** The channel may originate from an import and is expressly only an association under OC-004. Native `SaveFingerprint` updates can also replace the row's source and channel on same-ID conflict, so the current values do not describe the first native observation.
- **Failure mode:** Imported association receives false credit; a later native upsert can silently reclassify the same fingerprint row and change attribution. Gating on mutable `source` does not repair temporal provenance.
- **Compatibility and operations:** No migration, but previously incorrect statistics remain structurally plausible and can silently mislead.
- **Obligation impact:** Fails BR-010 AC2/AC6 and OC-004 Guarantee 3.

### B. Use the existing native `video` row as per-video discovery evidence — chosen

For an observed selected distinct match, join the matched fingerprint's `video_id` to its native `video` row and compare it with the current native `video` row. Award discovery credit to the earlier row's channel and relevance credit to the later row's channel only when the two decoded `discovered_at` values are strictly ordered. A missing native row for either member or ambiguous/equal time means neither side receives chronology-based credit; retain the selected match identities and available channel associations regardless. Attribute each pair independently, without traversing other matches to inherit an origin. The detector excludes a candidate whose `video_id` equals the evaluated video ID from both exact and fuzzy passes; thus the production pipeline receives no same-ID match to persist. Exclude any existing legacy self-match rows from both discovery and relevance projections.

- **Benefits:** Reuses the existing per-video native observations, which the importer does not create and whose channel/time are video-specific. It is independent of mutable fingerprint source/channel association and directly proves which member of the recorded pair came first, even when the match points to a later native observation.
- **Costs:** Attribution is conservative: a reference without a native row or with an unorderable/equal timestamp remains unknown. The statistics path must decode timestamps as times rather than compare unnormalized SQLite text.
- **Failure modes:** If stored timestamps cannot be decoded, the implementation must return no discovery credit rather than fall back to the fingerprint channel. A regression that allows same-ID rows into provenance queries could reintroduce self-credit, so legacy-row filtering is required even if new writes are guarded.
- **Compatibility and operations:** No schema or data migration. The projection is computed from existing `video` and `duplicate_match` rows. It also corrects old computed statistics when the query is deployed, without rewriting historical records.
- **Authority impact:** The source channel on the native `video` row is evidence of the system's observation for that exact video, while the fingerprint channel remains an association only.
- **Obligation impact:** Preserves imported-reference eligibility while assigning pairwise discovery/relevance roles only where independent per-video evidence establishes their ordering.

### C. Add an explicit first-discovery provenance field/table or duplicate-group record — rejected for now

Add a durable provenance field to fingerprints or a separate append-only provenance/group relation, then make statistics depend on that record.

- **Benefits:** Could make provenance more explicit and can support stronger group-level ownership if a canonical duplicate-group model is later accepted.
- **Costs:** Requires a schema migration, backfill/unknown-state policy, write-path ownership, and rules for same-ID imports/upserts and fuzzy matches. A new field copied from the imported association would preserve the same defect under another name; a group model additionally needs membership, merge, and identity semantics not specified by the current requirements.
- **Failure modes:** A denormalized provenance value can drift from the native video record; a group can merge false-positive fuzzy matches and silently transfer credit; a backfill cannot reconstruct trustworthy earliest evidence for imported-only rows.
- **Compatibility and operations:** Adds durable schema and migration support without any current evidence that `video.discovered_at` / `video.channel_id` is unavailable for the pairwise attribution required here.
- **Obligation impact:** It could support a future stronger guarantee, but is not needed to meet the current imported-reference/earliest-pair evidence boundary.

## Comparison

Option A is simpler only superficially: its input fields have the wrong authority and can change on an upsert. Option C adds schema and lifecycle machinery without a requirement for multi-row fingerprints or duplicate groups. Option B reuses the native per-video records already written before evaluation, excludes the import-only and self-match cases, and avoids guessing when evidence is absent. It is the least complex complete realization of the current pairwise provenance obligation.

## Decision

1. `is_duplicate_reference` remains matching-candidate eligibility only. ADR-013's monotonic merge and OC-004's imported-reference eligibility are unchanged.
2. Pairwise discovery/relevance attribution MUST use `video.channel_id` and `video.discovered_at` for both the current video and the matched reference. It MUST NOT use `title_fingerprint.channel_id`, `title_fingerprint.source`, `title_fingerprint.first_seen_at`, import order/time, or `channel.first_discovered_at` as earliest-discovery proof.
3. Chronology-based provenance requires both the currently evaluated video and the selected matched reference to have native `video` rows with different `video_id` values and strictly orderable decoded discovery times. The earlier row's channel MUST receive discovery credit and the later row's channel MUST receive relevance credit for that match. If either native row is absent or the times are equal/unavailable/unorderable, neither imported association nor an assumed reference role may stand in for chronology; both chronology-based roles remain unknown. The recorded pair's video identities and available channel associations MUST remain identifiable even when credit is unknown.
4. The duplicate detector MUST exclude an eligible fingerprint whose `video_id` is the same as the evaluated video's ID in both exact and fuzzy matching. Exact matching compares normalized canonical title fingerprints across distinct video IDs; the same-ID row stays eligible for other videos. Since the production pipeline records only a non-nil detector result, no same-ID result reaches its duplicate-match persistence path. Statistics MUST also filter legacy same-ID rows so old self-matches earn neither discovery nor relevance credit.
5. For a distinct pair with orderable native records, discovery and relevance roles follow observed chronology, not which side the detector returned as the matched fingerprint. The earlier video's native `video.channel_id` receives discovery credit; the later video's native `video.channel_id` receives relevance credit. Neither role is inferred from the fingerprint association.
6. Timestamp ordering MUST operate on decoded time values (UTC-normalized as needed), not raw SQLite text ordering. Existing video timestamps may have driver-encoded and RFC3339 representations; an unparseable value is unknown, never a reason to use imported metadata as a fallback.
7. The change requires no schema migration or new field: the importer does not create `video` rows, native discovery creates the row before duplicate evaluation, and the row is unique per video ID. Existing duplicate-match data is read through corrected projections; historical self-match rows are ignored for credit.
8. Attribution MUST be scoped to each observed selected match. Matches A–B and B–C MUST NOT establish an A–C match or transfer A's discovery credit into B–C. Neither detector output nor provenance is required to enumerate or credit every eligible candidate; no canonical fuzzy-equivalence group or global group-origin record is introduced (BR-010 AC8).

This decision realizes the accepted pairwise scope, not an unresolved approximation to group-global attribution. For native discovery times A < B < C and recorded A–B and B–C matches, A earns discovery credit for A–B, while B independently earns discovery credit for B–C. B's relevance role in A–B does not prevent its discovery role in B–C; no A–C match or global-origin claim follows.

## Consequences

- **Positive:** Imported channel associations can no longer earn discovery credit merely because an eligible fingerprint matched. The genuinely earlier native observation receives positive discovery credit whether it is the matched reference or the currently evaluated video.
- **Positive:** The same-ID imported/native path keeps candidate eligibility while the detector excludes the evaluated ID from exact and fuzzy results, avoiding a persisted self-duplicate event.
- **Positive:** Existing aggregate and per-video statistics can apply the corrected rule to historical rows without rewriting data.
- **Negative:** Some formerly attributed credits will disappear when there is no native reference observation or no provable temporal ordering. They remain unknown rather than guessed.
- **Neutral:** Fingerprint source/channel updates continue to refresh the matching row as ADR-013 allows; their mutable values no longer affect discovery-credit attribution.
- **Neutral:** A channel may receive relevance credit in one pair and discovery credit in another. The retained evidence describes selected observed matches, not every possible candidate or one transitive content origin.
- **Neutral:** No API, schema, import format, routing outcome, or duplicate eligibility contract changes.

## Confirmation and reconsideration

The implementation proof MUST include both statistics aggregation and the real import → native persistence → detector → provenance path:

1. Import `v1` with an optional `UC-import` association. Verify it is eligible and import creates no native `video`, channel-discovery, duplicate-match, route, or action evidence; discovery credit for `UC-import` is zero.
2. Persist native `v1` through the real discovery path with channel `UC-native`; verify its `video` observation is retained independently of the fingerprint's refreshed source/channel fields and its imported eligibility remains true.
3. Evaluate native `v2` from `UC-cover` against `v1` as a fuzzy reference. With `v1.discovered_at < v2.discovered_at`, verify discovery credit is assigned to `UC-native`, not `UC-import`, and relevance to `UC-cover`. In the converse case where `v2.discovered_at < v1.discovered_at`, verify `UC-cover` receives discovery credit and the later native reference's channel receives relevance; the detector's reference direction does not override chronology.
4. Cover no native `video` row for the imported reference and equal/unparseable timestamps; both must yield no chronology-based discovery or relevance credit and must not fall back to `title_fingerprint.channel_id`.
5. Run the native same-ID imported `v1` path. Verify detector evaluation of `v1` returns no exact or fuzzy match and the pipeline persists no `duplicate_match` self-event. Then evaluate a distinct video ID with an equal normalized title and verify the eligible imported `v1` row can be returned as an exact reference. Verify neither new nor legacy self-match rows appear in summary or per-video duplicate-credit projections.
6. Verify native `video` timestamp values in both supported stored encodings decode and compare chronologically as time values. Include malformed/unparseable values as unknown; do not use text ordering.
7. Verify imports with no channel association and same-ID import retries preserve the same no-credit boundary, and that ordinary native first-reference / later-distinct-duplicate controls still produce positive discovery and relevance credits respectively.
8. With native times A < B < C, record selected A–B and B–C matches but no A–C match. Verify both video identities and available channels remain inspectable for each selected pair; A receives discovery and B relevance for A–B, while B receives discovery and C relevance for B–C. No A–C event or inherited A credit may appear. Include another eligible candidate without a selected match to verify that provenance does not require enumerating or crediting all candidates. Repeat the selected-pair readback with missing native evidence and verify identities/associations remain available while both credit roles are unknown.

**Evidence limit for this reconciliation:** Existing implementation/composition evidence is recorded in the architecture log. This 2026-10-25 documentation-only reconciliation ran no runtime tests and does not establish the new nontransitive-chain confirmation above, independently reverify current pairwise projections, or claim Product Acceptance. Independent confirmation of the affected provenance/history/statistics paths is recommended before relying on implementation conformance.

Reconsider the no-migration choice only if the actual deployed timestamp formats cannot be decoded reliably from `video.discovered_at`, or if a new accepted requirement needs a canonical duplicate-group origin spanning more than the recorded matched pair. The latter requires explicit group/merge semantics and should not be inferred from fuzzy similarity alone.

## Open questions

None for attribution scope: BR-010 AC7–AC8 and the recorded user approval settle pairwise attribution and unknown evidence. The later “Continue, yes” decision now settles duplicate handling under BR-008 AC9–AC11; [ADR-017](duplicate-continuation/decisions/adr-017-fixed-duplicate-continuation.md) realizes it without changing this pairwise credit rule. The all-route-excluded evidence decision remains unchanged under AC7. Native reference eligibility and remaining legacy requirement conflicts stay with the product / requirements owner; they do not block this provenance-document reconciliation and are not decided here.
