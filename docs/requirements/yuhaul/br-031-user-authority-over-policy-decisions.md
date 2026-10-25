---
type: requirement
title: BR-031 — User Authority Over Policy Decisions
description: System provides evidence-backed advice but does not autonomously change policy or unsubscribe channels
tags: [requirement, authority, user-control, policy, autonomy, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL provide evidence-backed recommendations and statistics to the user but SHALL NOT autonomously unsubscribe channels, modify routing rules, adjust thresholds, or make other material policy changes without explicit user action.

## Acceptance criteria

1. The system presents channel usefulness data and trends that may inform subscription decisions, but does not automatically unsubscribe from channels.
2. The system presents routing effectiveness data that may inform rule adjustments, but does not automatically modify routing rules.
3. The system presents duplicate detection statistics that may inform threshold tuning, but does not automatically adjust thresholds.
4. All policy changes (route modifications, exclusion changes, threshold adjustments) require explicit user action through the administration interface.
5. The system may surface recommendations (e.g., "this channel has low acceptance rate") but labels them as recommendations, not actions.
6. The system does not autonomously create, enable, or disable routes.

## Derived from

- Anchor: youtube-discovery-routing.md (AC31, Context: user retains authority over all policy decisions; system automates repetitive work but does not autonomously redefine what user wants)

## Verified by

- Integration test: system does not unsubscribe any channel without explicit user action.
- Integration test: routing rules are not modified without explicit user action.
- UI test: recommendations are labeled as such and do not auto-execute.

## Depends on

- BR-021 (channel usefulness statistics provide recommendation basis)
- BR-022 (retention signals provide recommendation basis)
