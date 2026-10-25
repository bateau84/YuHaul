---
type: requirement
title: BR-029 — Responsive Administration Interface
description: Administration interface usable on desktop and mobile
tags: [requirement, interface, responsive, desktop, mobile, priority:MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL provide an administration interface that is functional and usable on both desktop and mobile screen sizes.

## Acceptance criteria

1. The administration interface renders and is interactive on screen widths from 320px (mobile) to 2560px+ (desktop).
2. Core operations (viewing runs, inspecting videos, managing routes, viewing statistics) are accessible on both desktop and mobile.
3. Navigation adapts to screen size (e.g., collapsible menu on mobile, full sidebar on desktop).
4. Tables and data displays are scrollable or adapt layout for narrow screens.
5. Touch interactions (tap, swipe) work correctly on mobile devices.
6. No core functionality is unavailable solely due to screen size.

## Derived from

- Anchor: youtube-discovery-routing.md (AC29)

## Verified by

- UI test: interface renders at 320px, 768px, 1024px, and 2560px widths.
- Functional test: core operations accessible at each breakpoint.
- Touch test: touch interactions work on mobile viewport.

## Depends on

- None
