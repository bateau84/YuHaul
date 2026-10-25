---
type: requirement
title: BR-024 — Trend Visualization
description: Useful trends rendered graphically
tags: [requirement, statistics, visualization, trends, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL present useful historical statistical trends graphically in the administration interface.

## Acceptance criteria

1. The administration interface presents a graphical representation of at least one useful historical statistic that makes its change over time discernible.
2. A presentation containing only numerical or tabular statistics, or a graphic that does not depict change over time, does not satisfy this requirement.
3. This requirement does not prescribe which statistics are useful, a chart form or framework, interaction controls, or a refresh cadence.

## Derived from

- Anchor: youtube-discovery-routing.md (AC24; v1 Scope states that the acceptance criteria above define v1)

## Verified by

- With historical values over time for a useful statistic, observe that the administration interface presents a graphical representation from which change over time is discernible. A numerical/tabular-only presentation or a graphic without temporal change falsifies the requirement. No particular statistic, chart form/framework, interaction, or refresh cadence is required.

## Depends on

- BR-023 (historical statistics provide data for visualization)
