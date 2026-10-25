---
type: decision
title: ADR-008 — Route Drag Reordering Uses the Current dnd-kit React API
description: Realize the accepted Route List drag-to-reorder interaction with the current @dnd-kit/react sortable API while retaining explicit Move Up/Down buttons as the accessible alternative.
tags: [architecture, decision, yuhaul, routes, web-ui, drag-reordering, accessibility]
---

**Status:** accepted

On the Route List, a user can drag a route by its grip handle to a new position. The list shows the prospective position while dragging and saves only when the user drops. Move Up and Move Down buttons remain alongside the handle; they continue to work by keyboard and pointer and remain the required alternative to dragging. This decision selects the drag mechanism only: both interaction paths use the existing route reorder operation and API.

## Derived from

- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route List and §Route row interaction — drag handle / Move Up/Down buttons
- [Drag-to-Reorder Implementation Drift — Route List](../ephemeral-reports/designer/drag-reorder-validation-finding.md)
- [Current `@dnd-kit/react` package metadata](https://www.npmjs.com/package/@dnd-kit/react) and [React sensor documentation](https://dndkit.com/react/guides/sensors) (checked 2026-10-23)
- [dnd-kit React migration guide](https://dndkit.com/react/guides/migration) and [legacy `@dnd-kit/sortable` package metadata](https://www.npmjs.com/package/@dnd-kit/sortable) (checked 2026-10-23)

## Satisfies

- [BR-003 — Multiple Ordered Route Definition](../requirements/yuhaul/br-003-multiple-ordered-route-definition.md) AC5 — the user can reorder routes and the declared order determines evaluation sequence.
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route row interaction — provides drag-to-reorder as primary interaction, Escape cancellation, drop feedback, and Move Up/Down controls as the accessible alternative.

## Decision question

Which implementation approach realizes the accepted Route List drag behavior without removing or weakening its explicit Move Up/Down controls?

## Drivers and constraints

- The accepted design requires both primary drag-to-reorder and explicit Move Up/Down buttons. The buttons are not optional, and drag is not a replacement for them.
- The grip handle alone starts a drag. Dragging must not begin from route links, toggles, or other row controls.
- The design requires a visible drop indicator, Escape cancellation, and automatic persistence on drop.
- Drag completion and the existing buttons must share the same route-order update path. The reorder API and persistence semantics do not change.
- The web application is React 19.2.8 (`web/package.json`); no drag-and-drop library is currently declared.
- The design includes mobile use. The primary drag path should handle pointer input across mouse, touch, and pen; explicit buttons remain available for keyboard and single-pointer operation.

## Options considered

### Option A — Native browser drag events, no library

Implement the drag handle, hit testing, row movement preview, drop line, Escape restoration, pointer/touch handling, and persistence using browser APIs.

- **Benefits:** no new dependency and direct control over the interaction.
- **Costs:** YuHaul would own the sensor and sortable-list mechanics, including touch/scroll interaction, drop-target behavior, keyboard/focus feedback, and cancellation. The event code and browser-specific cases become local maintenance surface.
- **Failure modes and risks:** incomplete touch support or cancellation/target handling could make the required primary interaction unreliable even while the buttons still work. The alternative buttons would preserve reorder access but would not satisfy the accepted requirement for a functioning drag interaction.
- **Compatibility and authority:** uses existing browser and React behavior; no new package trust boundary. It does not remove the need for the existing controls.
- **Obligation impact:** possible, but not the least-complex complete option once the specified drag states and mobile pointer behavior are included.

### Option B — Legacy `@dnd-kit/core` plus `@dnd-kit/sortable`

Use the mature two-package dnd-kit API. At decision time, npm reports `@dnd-kit/core` 6.3.1 and `@dnd-kit/sortable` 10.0.0; their published peer constraints permit the application's React version. The current upstream documentation labels this package family Legacy and provides a migration guide to the new React API.

- **Benefits:** established sortable hooks and broad React compatibility; less custom sensor and reordering code than native events.
- **Costs:** adds the older multi-package API when the same project now has a current React adapter; its latest published package versions are from December 2024, so it is a less current fit than the new API.
- **Failure modes and risks:** legacy API knowledge and future maintenance diverge from the current upstream documentation. This is not evidence that the packages are unusable, but it makes them a weaker forward choice for new integration.
- **Compatibility and authority:** technically compatible with React 19 per published peer constraints; no change to the API or button contract.
- **Obligation impact:** viable, but loses to the current API on project fit and maintenance direction.

### Option C — Current `@dnd-kit/react` sortable API (chosen)

Use `useSortable` from `@dnd-kit/react/sortable` within the route list's `DragDropProvider`, with a handle bound to the grip affordance. At decision time the current stable package is `@dnd-kit/react` 0.5.1, published 2026-10-15; its peer constraints explicitly support React and React DOM 18 or 19. The package documentation describes pointer sensors for mouse, touch, and pen, plus a keyboard sensor by default. `@dnd-kit/helpers` is not needed because the route list already owns the move-and-persist operation.

- **Benefits:** sortable behavior and pointer sensors are provided by a React-specific API that matches the current application; it avoids hand-implementing drag lifecycle and touch input, while adding one direct dependency rather than the legacy package family.
- **Costs:** introduces a new runtime dependency. The package is pre-1.0, so its public API has more breaking-change risk than a mature 1.x library; the integration must stay small and the selected release must be recorded in the lockfile.
- **Failure modes and risks:** an incompatible release or incorrect drag-end mapping could save the wrong order. The existing reorder operation remains the sole persistence path, and integration tests must cover order, cancellation, and control isolation.
- **Compatibility and authority:** published peer requirements include React 19. Pointer support covers mouse, touch, and pen. The package is a frontend build/runtime dependency in the existing web trust boundary; lockfile review controls the selected artifact. It adds no server/API surface and does not alter route order semantics.
- **Obligation impact:** directly supports the required handle, pointer drag, drop/cancel lifecycle, while the existing buttons preserve the specified keyboard and single-pointer alternative.

## Comparison

Native events save a dependency but transfer a complete multi-input drag lifecycle to application code. The legacy dnd-kit packages are viable and React-compatible, but upstream now directs new React integrations to the current adapter, and the legacy packages have not had a published release since December 2024. The current `@dnd-kit/react` API is therefore the least-complex complete option for the accepted mouse/touch/pen drag interaction. Its pre-1.0 API risk is contained by keeping the integration at the Route List boundary, using the existing order operation, and locking the chosen package version.

## Decision

Use the current `@dnd-kit/react` API and its `useSortable` export for Route List drag-to-reorder. Bind drag initiation only to the grip handle. Use pointer-based drag and drop to preview a new position; show the design's drop indicator; Escape cancels without changing the saved or displayed order; dropping calls the same existing move-and-persist path as Move Up/Down. The existing Move Up/Down buttons and their accessible names remain visible and functional; keyboard drag support from the library, if enabled, is additive and never substitutes for these controls.

The decision is about the package family and boundary, not a forced dependency patch number. At implementation time, select a published `@dnd-kit/react` release whose peer requirements still include the repository's React/React DOM versions, and lock the exact resolved version. Do not add `@dnd-kit/helpers` merely for array movement; reuse the existing route move operation.

## Realization

**Implemented:** `web/src/views/RoutesView.tsx` uses `DragDropProvider` and `useSortable` from `@dnd-kit/react`, with the grip wired as the drag handle. Canceled drops return without saving; completed drops resolve the visible DOM order and call the existing `move` function, which is also used by Move Up/Down and persists through `api.reorderRoutes`. The locked dependency is `@dnd-kit/react` 0.5.1 in `web/package.json` / lockfile.

**Automated confirmation passed on 2026-10-24:** `RouteReorder.test.tsx` passed as part of the targeted 43-test Vitest run, and `npm run build` completed successfully. The installed/locked 0.5.1 package declares React/React DOM peer support for 18 and 19. The tests exercise keyboard drag, pointer drag from the handle, control isolation, Escape cancellation, persistence failure rollback, and Move Up/Down keyboard operation through the shared reorder API path.

**Confirmation remains partial:** these tests use the frontend mock API and a simulated DOM. They do not prove the server-backed reorder survives an actual reload, nor do they exercise touch/pen devices or ordinary mobile scrolling in a real browser. The dnd-kit adapter and selected package are present, but those device/browser-specific scenarios remain verification items below.

## Consequences

- **Positive:** no API, server, or persistence change; the same route order operation serves both drag and button interactions; drag lifecycle and pointer sensors are delegated to a purpose-built library.
- **Negative:** one new web runtime dependency and the maintenance cost of its public API; its pre-1.0 status requires version locking and compatibility review on upgrades.
- **Neutral:** the existing Move Up/Down controls remain required even if the library also enables keyboard drag. No route-name, route-order, or failed YouTube attempt semantics are changed.

## Confirmation and reconsideration

- The repository locks `@dnd-kit/react` 0.5.1, declares React/React DOM 19.2.8, and the package's peer range includes React/React DOM 18 and 19. The targeted production build completed successfully on 2026-10-24.
- Component/integration verification must show: grip-only drag initiation; visible drop indicator; pointer reorder of a route; saved order after reload; Escape cancellation with no persistence; failure rollback through the existing reorder path; and no drag activation from route links, toggles, or Move Up/Down buttons.
- Verify Move Up/Down remains operable by keyboard and pointer, retains the accessible names `Move [route name] up` / `Move [route name] down`, and continues to persist through the existing reorder path.
- Verify touch/pen pointer input does not make the route list's ordinary scrolling unusable.
- Reconsider if no published current adapter release supports the repository's React version, if compatibility tests fail, or if the library's API changes make the integration materially more complex than the legacy alternative or native implementation.

## Open questions

- None for the technical drag implementation or its relationship to the accepted Move Up/Down alternative. Route-name semantics and failed-attempt quota accounting are outside this decision and remain user-owned.
