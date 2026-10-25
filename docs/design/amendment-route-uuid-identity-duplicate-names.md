---
type: design-amendment
title: "Route Identity — Reconciliation to Stable UUID Identifiers and Duplicate Display Names"
description: "Amends route list, editor, decision detail, and configuration history views to reflect the authoritative product decision: route IDs are stable UUIDs and display names may duplicate. Removes unique-name validation. Introduces short UUID suffix (first 8 hex chars) as a persistent disambiguator in all route references."
tags: [design-amendment, routes, uuid, identity, duplicate-names, reconciliation]
status: accepted
amends: yuhaul-admin-ui-design.md
---

## Amendment: Route Identity Reconciliation to Stable UUID and Duplicate Names

### Authority

The user has made an authoritative product decision: route IDs are stable UUIDs, and display names may be duplicated. This resolves the open question in ADR-011 and ARS-004 regarding requirements/design reconciliation. The specifier has amended BR-003 AC10 to permit duplicate names while retaining non-empty and maximum-length constraints.

### Problem

The accepted design required unique route names (Route Editor validation rules, line 528) and labeled routes by name only throughout the interface. Under the user's explicit direction and amended BR-003, two routes may share the same display name. The design could not be implemented as written because:

1. Name uniqueness validation conflicts with the user's permission to duplicate names.
2. Name-only labels cannot unambiguously identify routes when duplicates exist.
3. Historical decision evidence and configuration snapshots reference routes by name alone, which is insufficient when same-named routes are present.

### Accepted direction

Route identity is the stable UUID. Display names are labels. The UI shows a short UUID suffix (first 8 hex characters of the canonical UUIDv4) alongside every route name in contexts where route identity matters, so users can distinguish same-named routes and trace historical references unambiguously. The short UUID is always present, not conditional on collision state, because:

- Consistency prevents re-flow when a second route with the same name appears.
- Historical references (decisions, config snapshots) always need disambiguation since the design cannot know whether a same-named route exists at the time of inspection.
- Users learn to identify routes by the short UUID, which is stable across rename and deletion.

The full UUID is available in the Route Editor's metadata region for copy/reference.

### Design changes

#### Route Editor — Validation rules (amended)

**Validation rules (changed):**
- **Name:** Required. Must be non-empty after trimming whitespace. Maximum 100 characters. **Removed:** "Must be unique across all routes (checked on save)." Duplicate names are accepted for create and rename.
- **Conditions:** At least one condition required before saving. Each condition requires: field selected, operator selected, value non-empty (except `between` which requires both min and max).
- **Actions:** At least one action required before saving. Each action requires: type selected, all type-specific parameters filled (for `add_to_playlist`: playlist selected).
- **Validation timing:** Validate on save attempt. Show inline error messages below each invalid field. Scroll to first error if multiple errors exist.

**Validation error messages (changed):**
- Name empty: "Route name is required"
- Name too long: "Route name must be 100 characters or fewer"
- **Removed:** "Name not unique: 'A route with this name already exists'"
- No conditions: "Add at least one condition"
- Condition missing field: "Select a field"
- Condition missing operator: "Select an operator"
- Condition missing value: "Enter a value"
- No actions: "Add at least one action"
- Action missing playlist: "Select a playlist"

#### View: Route List (amended)

**Route row details (changed):**
- **Name:** route name followed by short UUID suffix in parentheses, monospace caption style, muted color. Format: `Route Name (a3f5b2c1)`. Clickable to open editor. The short UUID is the first 8 hex characters of the route's canonical UUIDv4, always present regardless of whether duplicate names exist.
- **Conditions summary:** compact text summarizing conditions (e.g., "title contains 'lo-fi' | duration ≥ 2min"). Truncate with ellipsis if longer than 60 characters. If no conditions, show "No conditions" in muted text.
- **Enabled toggle:** see Toggle component below
- **Final badge:** small badge "Final" if route is marked final; hidden if not final
- **Match count:** number of videos matched by this route (lifetime or period, depending on context)
- **Drag handle:** grip icon for drag reordering
- **Move Up/Down buttons:** arrow icons for keyboard-accessible reordering

**Rationale:** The short UUID suffix is always present so that when a second route with the same name is created, the list does not re-flow or require conditional logic. Users can immediately distinguish same-named routes by their short UUID. The suffix is muted to avoid visual noise while remaining legible.

#### Route Editor — Create vs Edit mode (amended)

**Edit mode (changed):**
- **URL pattern:** `/routes/:id` (UUID)
- **Page title:** "Edit Route · [name] ([short-uuid])". Example: "Edit Route · Music Discovery (a3f5b2c1)". The short UUID matches the suffix shown in the Route list.

**Route Editor — Header region (amended):**
- **Header region:** route name (editable), description (editable), metadata region (read-only full UUID, created timestamp, last modified timestamp). The metadata region shows the full canonical UUIDv4 in monospace with a copy-to-clipboard button for reference. This is not the primary identifier displayed to the user (the short UUID suffix is), but provides the full ID for debugging or external reference.

#### View: Decision Detail (amended)

**Route decisions region (changed):**
- **Route decision row:** route name with short UUID suffix, decision (accepted/rejected/skipped), icon, skip reason if skipped. Format: `Route Name (a3f5b2c1) — MATCHED` or `Route Name (a3f5b2c1) — NOT MATCHED` or `Route Name (a3f5b2c1) — SKIPPED (final route matched)`. The short UUID suffix ensures unambiguous identification when same-named routes are present in the evaluation sequence.

**Evidence trail region (changed):**
- **Condition evaluation row:** route name with short UUID suffix, condition, result, icon. Format: `Route Name (a3f5b2c1): condition 'title contains [genre keyword]' — NOT MATCHED`. The short UUID suffix disambiguates same-named routes in the evidence trail.

**Rationale:** Decision evidence must identify routes unambiguously (BR-003 AC13, BR-015). When two routes share a name, the short UUID suffix distinguishes them in the evaluation sequence and evidence trail.

#### View: Configuration History (amended)

**Change detail region (changed):**
- **Change detail panel:** full route configuration snapshot with each route entry showing route name and short UUID suffix. Format: `Route Name (a3f5b2c1)`. Linked runs list. Diff view comparing to previous version.
- **Diff view:** Side-by-side or inline comparison showing what changed between two configuration versions. Each route in the diff shows name and short UUID suffix. Added routes, removed routes, and modified routes are identified by short UUID, not name. Highlight additions in green, removals in red, modifications in amber.

**Rationale:** Configuration history must preserve route identity across rename and deletion (BR-025). The short UUID suffix ensures that a historical snapshot can be traced to the same route UUID even if the route's name has changed or if a same-named route exists.

#### Route Editor — Delete behavior (amended)

**Delete confirmation dialog (changed):**
- **Body:** "Delete route '[name] ([short-uuid])'? This cannot be undone. Videos that would have matched this route will no longer be routed by it." Example: "Delete route 'Music Discovery (a3f5b2c1)'? This cannot be undone. Videos that would have matched this route will no longer be routed by it."
- **Rationale:** When same-named routes exist, the delete confirmation must identify which route is being deleted. The short UUID suffix disambiguates.

#### View: Analytics Dashboard (amended)

**Trend charts region (changed):**
- **Route match rates over time:** Each route series in the chart legend shows route name and short UUID suffix. Format: `Route Name (a3f5b2c1)`. The short UUID ensures that same-named routes produce distinct chart series and are not merged by name.

**Rationale:** ARS-004 requires that "distinct rows, controls, and analytics series MUST NOT merge because their labels match." The short UUID suffix in chart legends ensures each route's analytics series is independently identified.

#### View: Channel Analysis (amended)

**Route match breakdown (within channel detail, changed):**
- **Route match breakdown:** Per-route match counts show route name and short UUID suffix. Format: `Route Name (a3f5b2c1): 23 matches`. This ensures that same-named routes are not merged in the per-channel route match statistics.

### Scenario amendments

#### SC-002: Investigating a Specific Rejection (amended)

**Step 6 — Evidence trail (changed):**
- Before: Evidence trail shows:
  - Route 1 "Music Discovery": condition "title contains [genre keyword]" — NOT MATCHED (red X). The video's title uses a different keyword.
  - Route 2 "Ambient": condition "duration > 10min" — NOT MATCHED (red X). Video is 4 minutes.
  - Result: rejected (no route matched)
- After: Evidence trail shows:
  - Route 1 "Music Discovery (a3f5b2c1)": condition "title contains [genre keyword]" — NOT MATCHED (red X). The video's title uses a different keyword.
  - Route 2 "Ambient (b7e9d4f2)": condition "duration > 10min" — NOT MATCHED (red X). Video is 4 minutes.
  - Result: rejected (no route matched)
- Rationale: Each route reference includes the short UUID suffix for unambiguous identification, consistent with the amended Decision Detail view.

#### SC-003: Setting Up a New Genre Route (amended)

**Step 1 (changed):**
- Before: User navigates to Routes — sees existing routes in evaluation order
- After: User navigates to Routes — sees existing routes in evaluation order, each showing route name and short UUID suffix (e.g., "Music Discovery (a3f5b2c1)", "Ambient (b7e9d4f2)")
- Rationale: The Route list now always shows the short UUID suffix for every route.

**Step 9 (changed):**
- Before: User clicks "Save" — route is created
- After: User clicks "Save" — route is created; the new route appears in the Route list with its assigned short UUID suffix (e.g., "Lo-Fi Discoveries (c1d8e3a9)")
- Rationale: The new route receives a server-generated UUID, and the short UUID suffix is displayed immediately in the Route list.

### Interaction and accessibility amendments

**Route row accessible name (amended):**
- **Route enable/disable toggle:** Accessible name: "Enable route [route name] ([short-uuid])" or "Disable route [route name] ([short-uuid])" depending on current state. Example: "Enable route Music Discovery (a3f5b2c1)".
- **Drag-to-reorder buttons:** Accessible names: "Move [route name] ([short-uuid]) up" and "Move [route name] ([short-uuid]) down". Example: "Move Music Discovery (a3f5b2c1) up".
- **Route row click:** Navigates to Route Editor for this route (URL: `/routes/:id` where `:id` is the UUID). The route's full UUID is used in the URL, not the short UUID suffix.

**Screen reader announcement (amended):**
- When a screen reader encounters a route row, it announces: "Route [route name] ([short-uuid]), [conditions summary], [enabled/disabled], [final badge if present], [match count]". Example: "Route Music Discovery (a3f5b2c1), title contains 'lo-fi' | duration ≥ 2min, enabled, 23 matches".
- The short UUID suffix is announced as individual characters (e.g., "a three f five b two c one") to ensure clarity.

### Open questions resolved

This amendment resolves the following open questions from ADR-011 and ARS-004:

1. **ADR-011 Open Question (Requirements owner / Specifier and Designer):** Reconcile the accepted unique-name clauses in BR-003 AC10 and the Admin UI Design with the user's explicit direction that route display names may duplicate. **Resolved:** BR-003 AC10 has been amended to permit duplicates. This design amendment removes unique-name validation and introduces short UUID suffixes for disambiguation.

2. **ARS-004 Open Question (Designer):** Define the presentation treatment where identical display names appear together. UUID remains the stable key. **Resolved:** The short UUID suffix (first 8 hex characters) is always shown alongside route names in route list, decision detail, configuration history, delete confirmation, and analytics legends. The full UUID is available in the Route Editor metadata region.

### Verification

The amended design must be verified by:

1. **Route list with duplicate names:** Create two routes with the same display name. Confirm both appear in the Route list with distinct short UUID suffixes. Confirm each can be opened, edited, reordered, and deleted independently by UUID.
2. **Decision detail with duplicate names:** Execute a run where two same-named routes are in the evaluation sequence. Confirm the Decision Detail evidence trail shows each route with its short UUID suffix, and the evidence is unambiguous.
3. **Configuration history with renamed route:** Create a route, capture a config snapshot, rename the route, capture another snapshot. Confirm the configuration history diff shows the route identified by short UUID in both snapshots, and the rename is visible as a name change, not a route replacement.
4. **Delete confirmation with duplicate names:** Create two routes with the same name. Delete one. Confirm the delete confirmation dialog shows the short UUID suffix, and the correct route (identified by UUID) is deleted.
5. **Analytics with duplicate names:** Create two routes with the same name. Execute runs. Confirm the analytics route match chart shows two distinct series, each labeled with route name and short UUID suffix, and the series are not merged.

### Conformance

This amendment brings the design into conformance with:

- BR-003 AC10 (as amended): Display names may be repeated; non-empty and maximum-length constraints remain.
- BR-003 AC12/AC13: Stable identifiers distinguish routes; historical run data identifies routes unambiguously by stable identity.
- ADR-011: Route IDs are stable UUIDs independent of display name.
- ARS-004: Public route APIs use UUID strings; UI state keys routes by UUID; distinct analytics series are not merged by name.

The design no longer conflicts with the user's explicit direction or the amended BR-003.
