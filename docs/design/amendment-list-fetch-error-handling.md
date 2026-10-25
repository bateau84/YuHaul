---
type: design-amendment
title: "List View Fetch Error Handling — Routes and Runs"
description: "Amends the accepted YuHaul admin UI design spec to specify how list views distinguish successful empty results from fetch failures, present the failure, and let the user recover. Applies the existing Route Editor error-recovery pattern to Route List and Run List."
tags: [design-amendment, web-ui, error-handling, fetch, recovery]
status: accepted
amends: yuhaul-admin-ui-design.md
---

## Amendment: List View Fetch Error Handling

### Problem

The accepted design spec (`yuhaul-admin-ui-design.md`) defines empty states for Route List ("No routes yet…") and Run List ("No runs yet…") but does not specify behavior when the fetch itself fails. The architecture contract requires APIs to return `[]` for successful empty lists and 500 JSON for failures. Without a spec for failure, the UI cannot distinguish "the system has no routes" from "the system is unreachable," leaving the user unable to recover.

### Current implementation

Both `RoutesView.tsx` and `RunsView.tsx` use `.then()` only, with no `.catch()`. On API failure, the view remains in the loading state indefinitely. The user sees "Loading routes…" or "Loading runs…" forever, with no indication of failure and no path to recovery.

This is not implementation drift — the spec is silent, so the implementation has no behavioral contract to follow.

### Design decision

Extend the existing Route Editor error-recovery pattern (spec lines 493-497) to all list views that fetch data. The pattern is:

1. **Loading state:** Show skeleton or "Loading…" text.
2. **Success state:** Show populated list or empty state as appropriate.
3. **Error state:** Show error message with retry action.

This pattern is already established in the Route Editor spec and is consistent with the product's experience goal: "Enable the user to trust that their YouTube discovery system is operating correctly… without second-guessing the system's behavior."

### Amended specifications

#### View: Route List (amended)

**Regions (added):**
- Error region (conditional): visible when the route list fetch fails. Contains error message and retry button.

**Components (added):**
- Fetch error state: inline error message with retry button.
  - Message: "Failed to load routes. Check your connection and try again."
  - Button: "Retry" — re-attempts the `GET /api/routes` request.
  - Visual treatment: error icon (alert triangle) + message in `color.status.failed` + secondary-styled Retry button.
  - Accessibility: `role="alert"` and `aria-live="assertive"` so screen readers announce the error.

**States (added):**
- fetch-error: replaces loading state when the fetch fails. Shows error region. Toolbar ("New Route" button) remains visible but non-functional (disabled) until retry succeeds. List region is hidden.
- retry-in-progress: when Retry is clicked, button shows spinner and label changes to "Retrying…", button disabled. On success, transition to success state. On failure, remain in fetch-error state, re-enable Retry button, show toast "Retry failed. Please try again."

**State transitions:**
- Initial load → loading → success (empty or populated) OR fetch-error
- fetch-error → retry-in-progress → success OR fetch-error
- User can click Retry multiple times; each attempt re-enters retry-in-progress

**Focus behavior (added):**
- On entering fetch-error state: focus moves to the Retry button.
- On successful retry: focus moves to the first route row (or "New Route" button if empty), per existing spec.

#### View: Run List (amended)

**Regions (added):**
- Error region (conditional): visible when the run list fetch fails.

**Components (added):**
- Fetch error state: inline error message with retry button.
  - Message: "Failed to load runs. Check your connection and try again."
  - Button: "Retry" — re-attempts the `GET /api/runs` request.
  - Visual treatment: same as Route List error state.
  - Accessibility: `role="alert"` and `aria-live="assertive"`.

**States (added):**
- fetch-error: replaces loading state when the fetch fails. Filter region remains visible but filter controls are disabled until retry succeeds. List region is hidden.
- retry-in-progress: same as Route List.

**State transitions:**
- Same as Route List.

**Focus behavior (added):**
- On entering fetch-error state: focus moves to the Retry button.
- On successful retry: focus moves to the first run row, per existing spec.

### Applicability to other list views

This amendment applies the same pattern to any list view that fetches data from the API. The following views should adopt the same fetch-error behavior:

- **Errors view** (listErrors): error region with retry when fetch fails. Message: "Failed to load queue. Check your connection and try again." The tab controls stay visible but disabled until a retry succeeds; tab counts are hidden while the fetch has failed.
- **Deferred queue** (listDeferred): fetched with the Errors view, so it shares that view's single error region and retry.
- **Analytics channels list** (listChannels): error region with retry when fetch fails. Message: "Failed to load channels. Check your connection and try again."
- **Configuration history list** (listConfigVersions): error region with retry when fetch fails. Message: "Failed to load configuration history. Check your connection and try again."
- **Overview** (getOverview): error region with retry when fetch fails. Message: "Failed to load overview. Check your connection and try again." The Run Now / Dry Run toolbar stays visible but disabled until a retry succeeds.
- **Backtest route loading** (listRoutes): error region with retry when fetch fails. Message: "Failed to load routes. Check your connection and try again." While routes are loading or have failed, the scenario diff region stays hidden so it never claims the proposal matches an unknown current configuration. The configuration version selector (listConfigVersions) gets its own error region with the message "Failed to load configuration versions. Check your connection and try again."

For every one of these views the error region uses the same structure as Route List and Run List: `role="alert"`, `aria-live="assertive"`, focus moves to the Retry button on entering the fetch-error state, the button shows "Retrying…" while a retry is in flight, a failed retry shows the toast "Retry failed. Please try again." and re-enables Retry, and the empty state appears only when the fetch succeeded and returned an empty list.

The pattern is consistent: loading → success OR fetch-error, with retry as the recovery action.

### Implementation status

Route List, Run List, and every view listed under "Applicability to other list views" implement the state machine described above (loading → success or fetch-error → retry), verified by the view tests under `web/src/views/`.

The "Current implementation" section describes the gap as it stood when this amendment was written.

### Validation criteria

The implementation passes this amendment when:

1. Route List and Run List distinguish three states: loading, success (empty or populated), and fetch-error.
2. On fetch failure, the error region is visible with the specified message and a functional Retry button.
3. Retry re-attempts the fetch. On success, the view transitions to the success state. On failure, the error region remains visible and the Retry button is re-enabled.
4. The error region uses `role="alert"` and `aria-live="assertive"` for screen reader announcement.
5. Focus moves to the Retry button on entering the fetch-error state.
6. The empty state ("No routes yet…", "No runs yet…") appears ONLY when the fetch succeeds and returns an empty array, never when the fetch fails.

### Design decisions

**DD-amendment-1: Inline error region over modal dialog**
- Decision: Fetch errors are shown as an inline error region within the view, not as a modal dialog.
- Rationale: Modal dialogs interrupt the user's flow and require dismissal. A fetch error is a persistent state that the user must resolve by retrying or navigating away. An inline region is less disruptive and makes the error state visible alongside the view's toolbar and navigation.
- Alternatives considered: Modal dialog with "Retry" and "Cancel" — rejected because it adds a dismissal step and obscures the view's context. Toast notification only — rejected because toasts are transient and the error state persists until resolved.

**DD-amendment-2: Retry button in error region, not in toolbar**
- Decision: The Retry button appears in the error region, not in the toolbar.
- Rationale: Placing Retry in the toolbar would require disabling the toolbar on error, which creates an inconsistent state. Keeping Retry in the error region makes the recovery action adjacent to the error message, reducing cognitive load.
- Alternatives considered: Retry in toolbar as a persistent action — rejected because it adds toolbar clutter for a rare state. Auto-retry on navigation — rejected because it masks failures and can create confusing state transitions.

### Open questions

None. This amendment closes a spec gap using an established pattern.
