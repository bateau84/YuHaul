---
type: Log
title: Discovery Source Isolation Change Log
description: Changes to the discovery source isolation architecture bundle.
tags: [changelog, architecture, discovery]
timestamp: 2026-11-22T18:00:00Z
---

# Discovery Source Isolation Change Log

## 2026-11-22

**Critique `ses_dfc1e6147ffeI1A5dhsvpYrek0` accepted `fd73783`; two MINOR wording fixes applied.**
- **R-1.** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §2 *Terminal cause* 2 now covers a quota stop observed by any path, including a `RunDispatchStop` reader and admission. §6 adds the same-bucket resume (PR #9 Q2) as a contrast.
- **R-2.** §1.2 *Scope* and the §6 dispatch-stop row use ARS-005 S8's simulation wording.
- The attempt date is read through the exported `Ledger.Budget(ctx).Date`.

**Critique `ses_dfc1e6147ffeI1A5dhsvpYrek0` on the dispatch-stop amendment fixed; branch rebased onto `main` (PRs #4–#9).** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) changes:
- **F-1.** The derived state's quota evidence now counts only `quota` ledger rows with `date >= run.discovery_attempt_quota_date`. The page-path engine writes that new column (in the discovery migration) at each discovery attempt's start, so legacy, `030`/`031` and PR #9-build runs, whose value is `NULL`, never read `blocked`/`quota`. The no-scope `blocked` row keys on that column instead of `initiated_by` and a `NULL` scan outcome, so PR #9's `preScanOutcome` (`cut_short`, completed) reads `blocked` on the page path.
- **F-2.** The new §2 *Terminal cause* picks the run-wide deferral from current-execution facts and the stored level, never from the display rule or a commit-time bucket check. The display is stable after the run ends. §6 adds a reset → resume → 429 case.
- **m-1.** §6 now reads "budget break → `scope_ready`".
- **Wording.** The AC8 row's pending rows are the handoff rule's rows. The rate-limit row says `discovery_processing_pending` is not used for the remaining observations.

**Dispatch stop pinned (`rq-stop-arch`, receiving [ARS-005 §6.1.3](/docs/architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md)).** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §1.2 now derives the `quota` level from the run's `quota` ledger rows, as PR #9 does on `main`, instead of storing it. `run.dispatch_stop` holds only `authorization` and `accounting_or_integrity`, is raise-only, and ships in PR-R's migration `032`. Writer 4 no longer writes `quota` for a local-budget refusal, writer 5 (ledger reconstruction on recovery) is removed, and readers use one `RunDispatchStop` read instead of a per-execution copy. A same-run resume after the quota reset can dispatch again when its only stop was `quota`; the old "accepted trade-off" is withdrawn. §1.3 and §5: `blocked` uses the *recorded stop* (stored value or any `quota` ledger row of the run); a local-budget break alone reads `scope_ready`. §4.1 no longer carries the DDL unless PR-R is dropped. §6's dispatch-stop row and §7 decision 2 follow.

**Critique on `723d418` (one MAJOR, two MINOR) fixed.**
- **M1.** The new immutable `video.discovery_source_channel_id`, written by the page transaction with the marker in the same migration, keys both the routing gate and the terminal handoff. The provider snippet's `video.channel_id` is no longer used for either. The terminal handoff now gives every unfinished marked observation a pending row at any terminal result other than Stop or Discard, with a reason order. §6 adds a mismatched or empty `channelId` case.
- **m2.** §2's mixed/complete row now excepts ineligible-after-page sources.
- **m3.** The AC11 case 1 proof row adds a locally failed source B with a saved page.

§5 has a designer note on Run Detail counts: an ineligible-after-page source is in no count.

**AC11 realized (user decision `b2f00946`, BR-002 AC11 in `44f3ba4`).** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) changes:
- §1.3: a source that saved a page and is ineligible on recovery settles `no_longer_eligible`. It counts as neither completed nor failed, and its observations are never routed in the origin. The routing gate admits only observations of `complete` or `local_failure` sources. The `eligibility_changed_no_coverage` derivation now covers every row being `no_longer_eligible`.
- §2: the AC10 row now covers AC11's three cases: mixed `completed`, AC8 `failed`, and all-ineligible `completed`. The all-local recheck accepts ineligible-after-page sources.
- §2 adds a *Terminal handoff rule*, derived from AC3 and AC10–11: every terminal transaction other than Stop or Discard writes the `discovery_processing_pending` rows first, including Failed and run-wide deferrals.
- §6 adds three AC11 proof rows.
- §7 decision 1 is resolved.

[ARD-001](/docs/architecture/discovery-source-isolation/design/ard-001-isolate-uploads-source-failures.md) maps AC6–11.

**Recheck R1 fixed (critique `ses_dff73847effengFMSzBkXiCHwF`).** In [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md), a rate limit on any uploads read now suspends uploads reads for the rest of that execution (§1.3). There is no in-place wait and retry, and no write. The scan stays `cut_short` with rows `unsettled`. If the run then reaches a terminal result other than Stop or Discard, §2's global row treats it as a run-wide deferral under the existing `api_rate_limited` reason. The terminal transaction gives every unfinished marked observation an origin `deferred` outcome and one pending `api_rate_limited` row before it publishes the status, so nothing is stranded. A crash before then remains an interruption, and resume rescans. A §6 "Rate limit then terminal" proof row is added.

**Revised after critique `ses_dff73847effengFMSzBkXiCHwF` (CHANGES REQUIRED; approach kept).** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md):
- **F1.** `blocked` without a sealed scope applies only to `run_now`/`schedule` runs whose `discovery_scan_outcome` is `NULL`. `queue_retry` runs and runs from `030`/`031` builds read `null`.
- **F2.** The marked-observation authority applies only to the marked routing gate. Unmarked native rows keep ARS-005 §5.1.1.
- **F3.** An attributable transient failure is exactly a 5xx or a per-request timeout while the context is live. Rate limits follow ARS-005 pacing: the row stays unsettled and `cut_short` stands. ARD-001's Satisfies row records this as an AC6/AC7 interpretation.
- **F4.** ARS-002 §1's same-run re-scan rationale is amended.
- **F5.** `completed_cutoff` equals `run.discovery_scope_cutoff`, enforced by a trigger.
- **F6.** A budget break is writer 4 and always stores `quota`, so the derived state is `blocked`. A preflight store error writes `accounting_or_integrity`.

Advisories:
- The claim and `ClaimPlaylistSend` re-read the stop per call in their writer transactions; other readers read it per execution.
- The trade-off is recorded: a same-run resume cannot dispatch after a quota reset.
- New `CHECK (settlement <> 'complete' OR has_committed_page = 1)`.

§7 decision 1 still awaits user decision `b2f00946`.

**ARD-001 and ARS-001 reconciled with `main` at `8d7e009` (Plan r30 `run52-arch-reconcile`; both still proposed).** They now build on what PR #4 and PR #5 built and no longer restate it:
- [ARS-002](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md)'s `030` incident table, its source Errors projection and `run.discovery_scan_outcome`;
- `031`'s `retry_claim_provenance` and receipt functions.

[ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) changes:
- §1.1 names one owner per stored fact.
- §1.2 defines the run dispatch stop: `run.dispatch_stop` with the values `authorization`, `quota` and `accounting_or_integrity`, a rank that writes can only raise, its writers, readers and `NULL` meaning, and a trigger. The DDL goes to PR-R's `rq-stop-schema`, or to `run52-schema` if PR-R ships without it.
- §1.3 replaces the stored run assessment with a state derived from `discovery_scan_outcome`, the run status, the sealed scope (`run.discovery_scope_cutoff`, `discovery_source_scope`) and the dispatch stop. That removes the ARS-002 §4.5 overlap. ARS-002's writes 1 and 2 move into the seal and last-row transactions.
- An attributable transient source failure now settles that source for the run, with a `transient` incident.
- §4.1 gives the discovery migration content against the `031` baseline. The stale `028`/`029` sequencing text is removed.
- §5 points to ARS-002 §4 for the source Errors endpoints instead of restating them.
- §7 lists the open decisions.

[ARD-001](/docs/architecture/discovery-source-isolation/design/ard-001-isolate-uploads-source-failures.md) maps BR-002 AC6–10 and points to these sections. ARS-002 §4.5 *Reuse* and §6 rule 4 are amended prospectively; nothing built changes. The accepted user decisions behind BR-002 AC7–10 are unchanged. No implementation or runtime proof.

## 2026-11-20

**Release check recorded (`6d65f7ef`, PR #4 at `2930a2d`):** the ADR-001 and ARS-002 status lines now cite the assembled evidence of `0a9116c4` and `6d65f7ef`, including browser checks at 320 px and 1280 px. Screen-reader and 200% zoom checks remain open. The operator [Upgrade notes](/docs/user-docs/upgrade-notes.md) now say `030` ships in PR #4, and give the release check's operator steps: stop; copy `yuhaul.db` with its `-wal` and `-shm` files; deploy; check for `title search index ready` and version 30; roll back by restoring that copy so `debad57` starts again. They also warn against deploying onto a database already at version 30 from an interim build.

**Implementation recorded (local candidate on `interim-uploads-isolation`):** ADR-001 D1–D5 and D7 in `5f849ea`, ARS-002 §1–§4.4 in `1a0bf19`, and §4.5 with the explicit `isolateSources` flag and the Errors page split in `f88ada9`. The status lines of ADR-001, ARS-002 and the admin UI design now cite these SHAs. Coder-run evidence on `f88ada9`: race tests on store, state, discovery, pipeline, server, stats and diag, `go test ./...` and the web suite pass; browser, screen-reader and 320 px checks were not run. Not yet pushed or reviewed as a whole.

**Review `833a06fb` MAJOR-1 realized in the specification:** [ARS-002 §4.5](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md#45-per-run-channel-scan-outcome-added-2026-11-20-review-833a06fb-major-1) extends unreleased `030` with the nullable `run.discovery_scan_outcome` (`completed`, `cut_short`, `no_source_scanned`, or `NULL` for unknown or legacy). Writes:
- `cut_short` when the uploads scan begins;
- `completed` when the loop ends normally;
- `no_source_scanned` in the same UPDATE as the AC8 terminal failure.

A resumed scan overwrites the value. The run read exposes `discovery_scan` (adds `in_progress`) with a variant table for the design's three coverage wordings. [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) D1 now requires an explicit `isolateSources` flag separate from `byID`. Status lines in ADR-001, ARS-002 and the admin design cite `b8934d6`/`04aee26` with the reviewer's evidence, and leave the SHA for the pending fix as a placeholder. The `030` rollback note is also in the operator [Upgrade notes](/docs/user-docs/upgrade-notes.md).

**ARS-002 accepted by critic; follow-ups applied:** [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md)'s body no longer claims "no migration", "schema unchanged", a ledger-derived note or "no source records". Each point is amended or struck and points to [ARS-002](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md). Budget breaks now read as exiting the channel's page loop, with later channels refused before any request. Release note added: after `030` is applied, a pre-`030` image fails at startup until a pre-`030` backup is restored.

**Source-failure record specified (G1 declined by the user):** [ARS-002](/docs/architecture/discovery-source-isolation/specifications/ars-002-durable-source-failure-record.md) adds migration `030`, an immutable `discovery_source_failure` table. The row is committed at the isolated `playlistNotFound` failure before the next request. A failed insert means the source is not isolated. Same-run resume skips a channel already recorded, and later successes never clear or rewrite rows. Read-only `GET /api/errors/sources` and run-scoped endpoints carry the validated channel ID, current local name and vetted reason. [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) records the user answers (G2 and G3 accepted, G1 declined) and withdraws D6. [ARD-001](/docs/architecture/discovery-source-isolation/design/ard-001-isolate-uploads-source-failures.md) and [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §4–5 now reuse the table. Central migration allocation: `030` goes ahead of receipt provenance; later numbers are assigned at integration, contiguously, with no reservation above `030`. [Run-initiation ARS-005](/docs/architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md) §5.1.1 and §6.1.1 are amended to match. No implementation or runtime proof.

**Interim decided:** [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) records a no-migration slice for the Run 52/53 failure on `debad57`. It isolates only exactly classified `404 playlistNotFound` uploads reads per channel, merges a channel's buffer only on completion, leaves a skipped channel's floor unchanged, derives Run Detail's incomplete-coverage note from the durable ledger, and adds closed code `no_source_scanned` for BR-002 AC8. User-owned gaps G1 (OC-001 Errors entry), G2 (AC9 partial pages) and G3 (channel ID in one log record) are open. ARD-001 and ARS-001 are unchanged and remain the full design. No implementation or runtime proof.

**Corrected after critique (CHANGES REQUIRED):** [ADR-001](/docs/architecture/discovery-source-isolation/decisions/adr-001-interim-channel-granular-uploads-isolation.md) D1 now isolates only a bare `*APIError` (never any `*ReadError`) with status 404, `Complete`, at least one reason and every reason `playlistNotFound`, with `ctx.Err() == nil`, and only on `RunBound`. D6 states that its ledger premise holds only on the guarded path and accepts the resume over-report. D2 records that budget breaks exit the page loop and later channels are refused before any request. D5 checks `ctx.Err()` before `ErrNoSourceScanned`.

## 2026-11-18

**Proposed:** [ARD-001](/docs/architecture/discovery-source-isolation/design/ard-001-isolate-uploads-source-failures.md) describes the Run 52 correction and downstream proof, including the accepted Errors and all-local-failure behavior. No production operation or implementation.

**Specified:** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) defines the durable page and receiving-queue handoff, the shared metadata precondition and the required restart evidence. No runtime proof was performed.

**Corrected after independent review:** ARD-001 and ARS-001 seal acquisition scope and make terminal retention atomic, align readiness and eligibility fences across receiver/recovery entry points, and specify authorized inspection with vetted reasons. The shared [receiving ARS-005](/docs/architecture/run-initiation/specifications/ars-005-requeue-stranded-videos.md) now distinguishes complete responses, typed dispatch stops and local-only claiming. Parent architecture navigation, shared log reconciliation and migration numbering are coordinated by the integrator.

**Targeted re-review:** The shared receiver now preserves authorization deferral for unsent, locally claimed and restored receipts. The user approved Completed with explicit incomplete coverage when the only source becomes ineligible on restart after saving observations. Discovery ARD-001 and ARS-001 specify atomic handoff, no origin routing, and the restart proof. No runtime proof was performed.

**Live-status amendment (BR-042, 2026-11-19).** [ARS-001](/docs/architecture/discovery-source-isolation/specifications/ars-001-durable-discovery-page-handoff.md) §3: readiness adopts `liveGatePass` and `retainNativeTx`, and the prefetch records `live_status` ([Live ARS-001 §10](/docs/architecture/live-status/specifications/ars-001-live-status-read-classification-and-deferral.md)).
