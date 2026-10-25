---
type: reconciliation-report
title: YuHaul — Reconciliation: Clean Obligation Baseline vs Designer Output
description: Comparison of the 32 ACs + obligation baseline against the Designer output to identify gaps, conflicts, additions, and alignment
tags: [reconciliation, yuhaul, design, requirements]
status: proposed
---

**Date:** 2026-09-15
**Baseline:** `docs/requirements/yuhaul/` (32 BRs + 3 OCs)
**Designer output:** `docs/design/yuhaul-admin-ui-design.md`
**Anchor:** `docs/anchors/youtube-discovery-routing.md` (32 ACs)

---

## Summary

All 32 acceptance criteria have corresponding BRs in the clean baseline and corresponding User Stories/Scenarios/Workflows in the Designer output. No AC is missing coverage from either artifact. The Designer output is detailed, well-structured, and largely faithful to the Anchor semantics.

**Material findings:** 8 items requiring attention (3 conflicts, 2 downstream-only additions, 1 coverage gap, 1 architecture leak, 1 semantic divergence).

---

## AC-by-AC Alignment

| AC | BR | Design Coverage | Status |
|----|-----|-----------------|--------|
| 1 | BR-001 | US-001, Workflow 1 | ALIGNED |
| 2 | BR-002 | US-009, Workflow 2 | ALIGNED |
| 3 | BR-003 | US-003/006, Workflow 3 | ALIGNED |
| 4 | BR-004 | US-003, Workflow 3 | ALIGNED |
| 5 | BR-005 | Workflow 2 (multi-route) | ALIGNED |
| 6 | BR-006 | US-005, Workflow 3 | ALIGNED |
| 7 | BR-007 | US-004, Workflow 4 | ALIGNED |
| 8 | BR-008 | US-007, Workflow 5 | ALIGNED |
| 9 | BR-009 | US-002, SC-004, Workflow 5 | ALIGNED |
| 10 | BR-010 | US-008 | ALIGNED |
| 11 | BR-011 | Workflow 2 | ALIGNED |
| 12 | BR-012 | Workflow 2 | ALIGNED |
| 13 | BR-013 | US-020, SC-006, Workflow 7/13 | ALIGNED |
| 14 | BR-014 | US-019, SC-007, Workflow 13 | ALIGNED |
| 15 | BR-015 | US-013, SC-002, Workflow 8 | ALIGNED |
| 16 | BR-016 | Workflow 6 | ALIGNED |
| 17 | BR-017 | US-009, Workflow 6 | ALIGNED |
| 18 | BR-018 | US-018, Workflow 7 | ALIGNED |
| 19 | BR-019 | US-018, Workflow 7 | ALIGNED |
| 20 | BR-020 | Workflow 9 | ALIGNED |
| 21 | BR-021 | US-016, Workflow 9 | ALIGNED |
| 22 | BR-022 | US-016, Workflow 9 | ALIGNED |
| 23 | BR-023 | US-015, Workflow 10 | ALIGNED |
| 24 | BR-024 | US-015, Workflow 10 | ALIGNED |
| 25 | BR-025 | US-021, Workflow 10/12 | ALIGNED |
| 26 | BR-026 | US-010, SC-003, Workflow 11 | ALIGNED |
| 27 | BR-027 | US-011/012, SC-005, Workflow 11 | ALIGNED |
| 28 | BR-028 | US-013, Workflow 8 | ALIGNED |
| 29 | BR-029 | US-022, Workflow 14 | ALIGNED |
| 30 | BR-030 | US-020, SC-006, Workflow 13 | ALIGNED |
| 31 | BR-031 | US-017, Workflow 15 | ALIGNED |
| 32 | BR-032 | US-014, Workflow 16 | ALIGNED |

---

## Findings

### CONFLICT

#### C-1: Design three-layer outcome model diverges from BR-032 four-category model

- **Design:** DD-9 introduces a three-layer outcome model: (1) processing state (7 values: discovered, evaluating, evaluated, acted, completed, deferred, not-yet-evaluated), (2) per-route decision (accepted, rejected, skipped), (3) per-action execution (succeeded, failed, deferred). Summary badges derive from layers.
- **BR-032 / Anchor AC32:** "Each processed video is classified into exactly one outcome category: accepted, rejected, excluded, deferred, or failed." Four primary categories plus not-yet-evaluated.
- **Issue:** The design does not surface "excluded" as a distinct outcome category. Exclusion is folded into the "skipped" decision state at Layer 2. A video excluded by every applicable route has no Layer 2 "accepted" result, and the summary badge derivation (design line 569-574) would classify it as "Rejected" — but the anchor's AC32 says "excluded" is a distinct category. The design's badge rules say "evaluated and no route matched → Rejected" which conflates excluded videos with rejected videos.
- **Materiality:** Medium. The user sees "Rejected" for a video that was excluded, not evaluated and rejected. This misrepresents the reason for the negative outcome.
- **Resolution:** Route to Designer to reconcile the three-layer model with the "excluded" category. Either add "excluded" as a Layer 2 decision state distinct from "rejected", or add it as a Layer 1 processing state, or add a badge derivation rule that surfaces exclusion separately.

#### C-2: Design dry-run scope is more restrictive than the anchor

- **Design:** US-010 (line 156): "System processes pending videos against draft (unsaved) route configuration without mutating playlists, duplicate corpus, or statistics."
- **Anchor AC26:** "Dry-run evaluation without external playlist mutation."
- **Issue:** The anchor limits dry-run protection to external playlist mutation. The design extends this to also protect corpus and statistics — which are internal state, not external mutations. This is more conservative than the anchor requires. A dry-run that updates internal statistics but does not touch external playlists would satisfy AC26 but violate the design spec.
- **Materiality:** Low. The design is more conservative, which is generally safer. But if the implementation follows the anchor rather than the design, statistics from dry-runs could pollute analytics.
- **Resolution:** Route to Designer to clarify whether corpus/statistics protection is a design decision (in which case, make it explicit as a DD) or an over-extension beyond the anchor. If a design decision, note the added conservatism.

#### C-3: Design "Apply" action on backtest extends beyond the anchor

- **Design:** SC-005 line 383: "User clicks 'Apply' — the proposed route configuration is saved as a new route."
- **Anchor AC27:** "Historical backtesting compares routing changes against retained observations without altering external state."
- **Issue:** The anchor scopes backtesting to comparison only ("compares routing changes against retained observations"). The design adds an "Apply" action that persists the proposed configuration as a new route. This is a convenience feature beyond what the anchor requires — it bridges backtesting into route creation.
- **Materiality:** Low. The "Apply" action is a UX convenience that does not violate the anchor's "without altering external state" constraint (it alters internal state, not external). But it extends the scope of the backtest workflow.
- **Resolution:** Route to Designer. If the "Apply" action is desired, it is a design addition, not an anchor requirement. Mark it as DD-14 (or similar) to distinguish it from the AC27 obligation.

### DOWNSTREAM-ONLY

#### D-1: Advisory dismiss and ignore-for-30-days behaviors are not in the anchor

- **Design:** Advisory interaction (lines 707-713): "dismiss" marks advisory as acknowledged; "ignore" hides for 30 days and reappears if conditions persist.
- **Anchor AC31:** "System provides evidence-backed advice but does not autonomously unsubscribe channels or make material policy changes."
- **Issue:** The anchor requires the system to advise but not act. The design specifies two additional advisory lifecycle behaviors (dismiss = permanent acknowledge; ignore = temporary hide) that have no anchor authority. These are downstream UX decisions.
- **Materiality:** Low. These are reasonable UX behaviors that support AC31. But they are not Anchor-entailed and should not be treated as obligations.
- **Resolution:** Mark as design-level decisions. Do not promote to BRs. If they need obligation weight, route to product authority for acceptance.

#### D-2: Very small viewport warning (< 320px) is design-only

- **Design:** Line 807: "Warning banner: 'Screen is very small. Some features may be limited. Consider using landscape orientation.'"
- **Anchor AC29:** "Administration interface usable on desktop and mobile."
- **Issue:** The warning banner for viewports under 320px is a design decision not entailed by the anchor. The anchor requires usability, not a warning for extreme sizes.
- **Materiality:** Negligible. This is a defensive UX choice. Not an obligation.
- **Resolution:** No action needed. Design-level decision.

### COVERAGE-GAP

#### G-1: BR-022 retention signal provenance has limited design coverage

- **BR-022 (AC22):** "User-retention/value signals measured where reliable provider evidence permits."
- **Design:** US-016 (line 209) mentions "retention signal provenance (user-driven vs YuHaul-routed)" in channel detail. DD-12 specifies retention provenance as progressive disclosure.
- **Issue:** BR-022 specifies that retention signals are measured "where reliable provider evidence permits" — a conditional obligation dependent on what YouTube's API actually provides. The design assumes retention provenance data exists and specifies how to display it. But BR-022's verification path ("provider evidence permits") is not validated in the design. If YouTube does not provide retention-quality signals, the design's retention provenance breakdown may have no data to display.
- **Materiality:** Medium. The design assumes data availability that BR-022 explicitly makes conditional.
- **Resolution:** The design should note that retention provenance display is contingent on data availability. This is a verification gap, not a conflict — but the design should not assume the data exists when the obligation explicitly conditions it.

### ARCHITECTURE-LEAK

#### A-1: Design specifies SSE transport detail

- **Design:** DD-13 (line 1004-1008): "Run progress is delivered via Server-Sent Events (SSE) through a REST endpoint (`/api/runs/:id/stream`)."
- **Issue:** SSE is a structural/interface choice (transport protocol, endpoint shape). This is architecture, not behavioral obligation. The behavioral requirement is "the user sees real-time progress during a run" — the transport mechanism belongs to the Architect.
- **Materiality:** Low. The design correctly identifies the need for real-time progress but specifies the implementation mechanism.
- **Resolution:** Flag for Architect. The endpoint shape (`/api/runs/:id/stream`) and SSE transport are structural decisions. The behavioral obligation is "run progress is visible in real time during processing."

### ALIGNED (no issues)

All other ACs show faithful representation in both the clean baseline and the Designer output. Specific highlights:

- **AC1/BR-001/US-001:** Account connection OAuth flow — aligned.
- **AC8/BR-008/US-007:** Fuzzy match threshold in Settings — aligned, with DD-11 correctly scoping it as global.
- **AC9/BR-009/US-002/SC-004:** Corpus import with format validation — aligned, design adds helpful UX (progress indicator, preview, export verification).
- **AC14/BR-014/US-019:** Error queue with retry/dismiss — aligned. Design correctly preserves historical record on dismiss.
- **AC15/BR-015/US-013/SC-002:** Evidence trail as vertical sequence — aligned, DD-5 correctly makes this design decision.
- **AC25/BR-025/US-021:** Configuration history with diff view — aligned.
- **AC29/BR-029/US-022:** Responsive design with breakpoints — aligned, design adds prescriptive detail (320/768/1024px) that is implementation guidance, not obligation.
- **AC30/BR-030/US-020/SC-006:** Quota exhaustion preserves deferred work — aligned.
- **AC31/BR-031/US-017:** Advisory system with evidence-backed suggestions — aligned on core obligation; D-1 notes lifecycle extensions.

---

## Recommendations

1. **C-1 (outcome model):** Designer should reconcile the three-layer model with the "excluded" category from AC32/BR-032. Either add "excluded" as a distinct visual/semantic state or document why "skipped" subsumes it.

2. **C-2 (dry-run scope):** Designer should decide whether corpus/statistics protection is a deliberate design decision (DD) or an over-extension. If deliberate, add a DD entry. If over-extension, align with the anchor.

3. **C-3 (backtest Apply):** Designer should mark the "Apply" action as a design convenience, not an AC27 obligation.

4. **G-1 (retention provenance):** Designer should note that retention provenance display is contingent on YouTube API data availability, consistent with BR-022's conditional wording.

5. **A-1 (SSE transport):** Architect should own the endpoint shape and transport protocol. The behavioral obligation is "real-time progress visibility."

6. **D-1/D-2:** No action required — these are design-level decisions that do not conflict with the anchor.

---

## Open Questions

- None blocking. All findings are classifiable and routable.
