---
type: requirement
title: BR-033 — Honest Manual Run Progress
description: After Run Now, identify the acknowledged run and show its saved status and phase without inventing completion or quantitative progress.
tags: [requirement, runs, progress, evidence, accessibility]
timestamp: 2026-11-05T00:00:00Z
---

**Status:** accepted — bounded user-requested progress feedback; implementation conformance not established here.
**Priority:** MUST

## Statement

After Overview → Run Now acknowledges a specific run, the system SHALL let the user follow that run's saved status and current phase, including discovery, without treating acknowledgement, counters, quota consumption, or loss of contact as completion.

## Acceptance criteria

1. A successful start acknowledgement identifies the particular run the user can inspect. Acknowledgement alone means started/accepted, not finished. The accepted design's navigation to that run's Run Detail remains applicable; this requirement does not choose another interaction.
2. While the saved run status is `running`, Run Detail shows that status and the phase recorded for that run. In particular, a run whose saved phase is discovery is described as discovering, even when selected inputs are unknown and all displayed counters are zero. A phase must not be inferred from quota use or counters. When no phase is available, show that it is unavailable rather than guessing discovery, evaluation, or action execution.
3. Status and phase are available from saved run evidence when the user opens or reloads that run's detail, without requiring the browser to have observed its start or earlier progress messages. While the detail remains open, subsequent successful run reads update the displayed saved status and phase; no manual reload is required to see a changed value once received. No new refresh interval or completion deadline is promised.
4. An unavailable selected-input count remains unknown, not zero and not a value substituted from discovered or decision-bearing counts. A recorded zero remains zero. Zero counters, equal selected/evaluated counts, elapsed time, and account-wide quota consumption must not be presented as a percentage of this run completed or as proof of completion. This minimal feature does not require a percentage, remaining-time estimate, or new provider/substep counters.
5. A successful read of this run's persisted non-running status supersedes active progress: show `completed`, `failed`, or `interrupted` according to the existing lifecycle, not a generic success. A completion message alone cannot establish completion. Once `completed` or `failed` has been confirmed, an older running read or delayed phase message cannot restore active progress. An interrupted run may show running again only when a subsequent authoritative read confirms a legitimate resumed state; this does not change existing recovery policy.
6. A failed/unavailable status read or lost progress connection is an observation failure, not a run result. When saved status cannot be refreshed, indicate that current progress cannot be confirmed, distinguish any retained information as last known, and permit retry of observation. Lost progress events alone do not make successful saved reads stale. Do not claim that work stopped, completed, failed, or made no external changes merely because contact was lost. Do not automatically submit another start request because start confirmation or progress is uncertain; retrying observation is not starting another run.

## Existing obligations retained

- [BR-017](br-017-manual-run-initiation.md) continues to govern initiation, history, and nonconcurrent execution; [BR-028](br-028-historical-run-inspection.md) governs persisted inspection, and [BR-032](br-032-processing-outcome-distinction.md) prevents absent processing becoming a negative video outcome. Run status and phase do not replace video outcomes.
- [BR-029](br-029-responsive-administration-interface.md) applies to this feedback on desktop and mobile. The accepted [Admin Interface Design](../../design/yuhaul-admin-ui-design.md), §Manual run trigger interaction, §Focus Behavior, and §Accessibility / Run progress indicator, already governs navigation, focus, textual progress, polite progress announcements, and announced connection errors. Those are existing obligations, not new design instructions or a new QS here.
- The accepted [diagnostic architecture](../../architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md), §Failure diagnostic boundary, remains applicable: unsafe diagnostic text is withheld and unavailable reasons are not guessed. No raw provider body, OAuth credential, or authorization data is authorized by this progress requirement.

## Derived from

- User request, relayed in the 2026-11-05 specification assignment: progress indicators after starting production run14, where rising quota and “in progress / Unknown / 0” did not explain the current phase. The bounded assignment explicitly requires findable run identity, saved status/current phase including discovery, honest unknown/zero values, persisted final-status precedence, and no fabricated completion from unavailable reads.
- [BR-017 — Manual Run Initiation](br-017-manual-run-initiation.md) and [BR-028 — Historical Run Inspection](br-028-historical-run-inspection.md): linking acknowledgement to the requested run is necessary to inspect the run just started rather than unrelated activity.
- Specification choices within that scope: unavailable phase stays unavailable; retained observations are labeled last known after a read failure; older running information cannot undo a confirmed nonresumable result; observation recovery must not silently initiate more production work. These define truthful feedback, not new retry or provider guarantees.

## Verified by

Verify through the assembled browser and application API against a controlled provider, not the live account. Observe real run identity and saved state; shaped UI responses alone do not prove phase production/readback.

1. Start from Overview and record the acknowledged run identity. Confirm navigation reaches that same run and no completion is announced merely from acknowledgement. Reopen its detail directly.
2. Hold a real production run in discovery with unknown selection and zero counters. Observe saved `running` and discovery through the run read and visible/accessible text, including after reload. Advance saved phase and observe the detail update after a successful read. Separately exercise unavailable phase, unknown selection, and recorded zero; no percentage or fabricated phase appears.
3. Finish separate runs as completed, failed, and interrupted. Observe the matching saved status replace active feedback. Deliver delayed running/phase information after completed/failed readback; it must not reverse that result. If exercising an existing interrupted-run resume, confirm running only from its legitimate subsequent saved state.
4. Lose progress contact and fail a run read before completion confirmation. Check observation-error and last-known wording, observation retry, and absence of inferred completion or another start submission. Recover contact and confirm the actual saved status. Also lose start confirmation and check that the system does not automatically resubmit it.
5. Apply existing design checks with keyboard and a screen reader: run navigation identifies the new detail, phase/status changes are announced without stealing focus, connection loss is announced, and feedback is textual rather than spinner/color only. Verify at 320px and desktop widths under BR-029; existing design contrast, resize, and reduced-motion expectations remain applicable. A rendered screenshot alone does not establish announcements.
6. If a failure reason is displayed, verify only safe existing diagnostic projections are visible; withheld/missing reasons remain unavailable.

## Reconciliation and limits

The baseline above was extracted from the relayed request and accepted BRs before reading downstream design/architecture in this pass. The assignment had already supplied the designer recommendation and a suspected event-shape mismatch; neither supplies new product authority or a confirmed root cause.

- **Aligned:** accepted design US-009 already names discovering/evaluating/acting feedback and navigation to the new run. Its accessibility/focus requirements and BR-029 already cover the human-facing quality obligations. No duplicate US, design, or QS is introduced.
- **Concurrent design reconciliation:** the designer's 2026-11-05 §Run Now — Recorded Progress and Closure amendment became visible during this pass and is aligned on saved-phase readback, unknown/zero distinction, stale observations, and terminal confirmation. Its Overview placement, five-second refresh, exact copy/phase labels, tab-return behavior, and controls are design-owned realization, not additional BR-033 promises derived from implementation. No conflict was found; this pass did not edit or validate that design artifact.
- **Aligned:** the accepted diagnostic architecture already distinguishes unknown selection from zero, acknowledgement from completion, saved status from best-effort events, and safe reasons from raw details. Its run recovery meaning and accepted ADR-002 remain unchanged.
- **Missing coverage / realization to verify:** the relayed observation reports no visible phase during run14. This requirement adds explicit saved-phase visibility on direct/reloaded detail and precedence/observation-failure acceptance conditions. Engineering must establish whether current producer, run reads, or browser consumption fails them; no source or live-environment diagnosis was performed here. Architect owns any necessary API/representation decision. No transport, event format, schema, polling algorithm, or new telemetry is specified.
- **Outside scope:** proposed discovery/processing coordinator, new scheduling or quota policy, date selection, provider-side progress/completion guarantees, and more detailed per-item telemetry. No genuinely user-owned choice blocks this bounded behavior.
