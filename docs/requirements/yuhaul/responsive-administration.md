---
type: requirement
title: Responsive Administration Interface
description: The administration interface must be usable on both desktop and mobile devices.
tags: [requirement, interface, MUST]
---

**Status:** accepted
**Priority:** MUST

## Statement
The system SHALL provide a web-based administration interface that is usable on both desktop and mobile devices.

## Acceptance criteria
1. The administration interface is accessible through a web browser
2. The interface layout adapts to different screen sizes including desktop and mobile
3. All core administration functions are accessible on both desktop and mobile
4. The interface is navigable using touch input on mobile devices

## Derived from

- Anchor: YuHaul — YouTube Subscription Discovery & Routing — relative path `../../anchors/youtube-discovery-routing.md` (AC 29)

## Verified by

- Test: the interface renders and is functional in a desktop web browser
- Test: the interface renders and is functional in a mobile web browser viewport
- Test: all administration functions (route management, run inspection, statistics) are accessible on mobile

## Depends on

- None
