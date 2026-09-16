---
type: decision
title: ADR-004 — Canonical Condition Operator Vocabulary Is the Accepted Design Spec
description: Realize the accepted design spec's condition-operator vocabulary (contains, equals, starts_with, ends_with, matches_regex for text; equals, greater_than, less_than, between for numerics) as the engine's single canonical set, retiring the divergent gt/gte/lt/lte/not_* /in_list vocabulary that the implementation drifted onto.
tags: [architecture, decision, yuhaul, routes, conditions, operators, vocabulary]
---

**Status:** proposed

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md)
- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md)

## Satisfies

- [Admin Interface Design Specification](../design/yuhaul-admin-ui-design.md) §Route Editor — Condition composition UI — "Operator options per field" (text: `contains, equals, starts_with, ends_with, matches_regex`; numeric `duration_seconds`: `equals, greater_than, less_than, between`).
- [Anchor](../anchors/youtube-discovery-routing.md) AC4 — routes contain configurable conditions and playlist actions.

## Problem

The operator vocabulary has drifted in two directions at once:

- The engine (`internal/router/engine.go:181-204`) and its model (`internal/router/models.go:38-46`) implement `contains, not_contains, equals, not_equals, gt, gte, lt, lte, in_list`.
- The UI (`web/src/components/RouteEditor.tsx:13-23`, `web/src/types.ts:12-21`) offers the same drifted set.
- The **accepted** design spec (§Route Editor — Condition composition UI, "Operator options per field") requires text fields to offer `contains, equals, starts_with, ends_with, matches_regex` and `duration_seconds` to offer `equals, greater_than, less_than, between` — with `between` as two number inputs and `matches_regex` in monospace.

Four operators the spec requires (`starts_with`, `ends_with`, `matches_regex`, `between`) are **unimplemented in the engine**, and five operators the engine implements (`not_contains`, `not_equals`, `in_list`, `gte`, `lte`) and two it implements under different names (`gt`, `lt`) are **not in the accepted vocabulary**. This is a Design ↔ Architecture mismatch, not a UI-only fix (reviewer MAJOR-5).

## Decision drivers

- **Authority direction.** The design spec is the accepted human-facing vocabulary authority. The engine is its realization. The correction must flow spec → engine, not engine → spec; rewriting the accepted spec to match an implementation that drifted ahead of it would invert authority and regress expressiveness (dropping `matches_regex`, `between`, `starts_with`, `ends_with`).
- **One source of truth.** A superset that keeps the old operators alongside the new ones re-opens the exact divergence the finding flags. The engine and the UI must share one canonical enumeration.
- **Single-value schema.** `route_condition.value` and `condition_audit.expected_value` are single `TEXT` columns; `between` must encode two numbers without a schema change.
- **`matches_regex` must be safe.** Go's `regexp` (RE2) is linear-time and rejection-safe; no catastrophic-backtracking risk.

## Options considered

### Option A — Realize the spec vocabulary in the engine (chosen)

Redefine `ConditionOperator` to exactly the spec set; extend the engine's `evaluateCondition`; align the UI's operator list and `ConditionOperator` union; retire the drifted operators.

- **Benefits:** single canonical vocabulary; the four missing operators become available; the UI and engine agree by construction; honors the accepted spec's authority.
- **Costs:** the drifted operators are removed (see negative consequences below); engine tests that use `gt`/`in_list`/`not_equals` must be rewritten against the canonical set.
- **Failure modes:** none structural — this is a pure realization of already-accepted semantics.

### Option B — Rewrite the accepted design spec to match the engine

Edit the spec's operator tables to `gt, gte, lt, lte, not_contains, not_equals, in_list` and drop `starts_with, ends_with, matches_regex, between`.

- **Benefits:** smaller code change (one document).
- **Costs:** the spec is a Designer-owned accepted artifact — this is authority regression, not reconciliation; it discards `matches_regex` (powerful, already-designed) and `between` (already-designed two-input numeric range) in favor of a half-implemented engine vocabulary that the Designer did not choose. Rejected.

## Decision

In the context of a pre-merge MAJOR where the engine and UI implement a vocabulary the accepted design spec does not require while four spec-required operators are unimplemented, facing the choice between realizing the spec in the engine or rewriting the spec to the engine, we decided **for realizing the spec vocabulary as the engine's single canonical set** and **against** rewriting the accepted spec, to honor authority direction and deliver the designed expressiveness, accepting the removal of the drifted operators and a test rewrite.

### Canonical operator set

| Field kind | Operators |
|---|---|
| Text (`title`, `channel_name`, `description`) | `contains`, `equals`, `starts_with`, `ends_with`, `matches_regex` |
| Numeric (`duration_seconds`) | `equals`, `greater_than`, `less_than`, `between` |

### Semantics

- `contains` — case-insensitive substring (`strings.Contains(strings.ToLower(actual), strings.ToLower(value))`). Unchanged.
- `equals` — case-insensitive equality on text; numeric equality on `duration_seconds` (parse both sides as numbers).
- `starts_with` — case-insensitive prefix (`strings.HasPrefix`).
- `ends_with` — case-insensitive suffix (`strings.HasSuffix`).
- `matches_regex` — `regexp.MatchString(value, actual)` applied to the actual value; **case-sensitive by default**, with `(?i)` available for case-insensitive matching (full RE2 control in the operator's own syntax). Invalid pattern → condition does not match (record `actual_value`, `matched=false`), never a crash.
- `greater_than` — strict `a > b` (numeric). Replaces `gt`.
- `less_than` — strict `a < b` (numeric). Replaces `lt`.
- `between` — inclusive `min <= a <= max`; the single `value` field carries `"min,max"` (comma-separated), the same single-value encoding precedent the removed `in_list` already used. The UI renders two number inputs and serializes to `"min,max"`; the audit records the same `"min,max"` string as `expected_value`.

Removed operators: `not_contains`, `not_equals`, `in_list`, `gt`, `gte`, `lt`, `lte`.

## Realization

- `internal/router/models.go`
  - Replace the `ConditionOperator` const block with: `contains`, `equals`, `starts_with`, `ends_with`, `matches_regex`, `greater_than`, `less_than`, `between`.
- `internal/router/engine.go`
  - `evaluateCondition`: add `starts_with`, `ends_with`, `matches_regex` (text) and `between` (numeric) cases; rename the numeric comparison to `greater_than`/`less_than`; route numeric `equals` through `compareNumeric` when `field == duration_seconds`.
  - `between`: parse `value` as `"min,max"`, return `false` on parse failure (never panic).
  - Add a `default` case to the operator switch that logs a warning (route id, condition index, `operator`, `field`) and returns `false`, replacing the current silent `return false` at the end of the switch (`engine.go:206`). This is the stored-drifted-row path: a persisted operator outside the canonical set evaluates as non-matching but is now loud, not indistinguishable from a legitimate non-match. The `Engine` gains a `*slog.Logger` (set by `NewEngine` or a `WithLogger` option, defaulting to `slog.Default()`).
- `internal/server/server.go`
  - `decodeRouteRequest`: after the existing name/composition/count checks, validate each condition's operator against the canonical set for its field kind — `duration_seconds` admits `equals`, `greater_than`, `less_than`, `between`; the text fields (`title`, `channel_name`, `description`, `channel_id`) admit `contains`, `equals`, `starts_with`, `ends_with`, `matches_regex` (the same text/numeric split `conditionValue` and `evaluateCondition` already apply). Reject an operator outside the field's set with `400` and a message naming the offending condition (e.g. `condition %d: operator %q is not valid for field %q`). For `operator == "between"`, also require `value` to parse as `"min,max"` with two numbers, else `400`. This is the write boundary: no non-canonical operator can be persisted through the API.
- `internal/router/engine_test.go`
  - Rewrite operators in the existing cases (`gt` → `greater_than`, `in_list` → an equivalent OR/comma form or a new `between` case, `not_equals` → restructure) and add coverage for `starts_with`, `ends_with`, `matches_regex`, `greater_than`, `less_than`, and `between`; add a case asserting an unknown operator returns `false` and emits the warning.
- `web/src/types.ts`
  - Redefine the `ConditionOperator` union to the canonical set.
- `web/src/components/RouteEditor.tsx`
  - Replace the `operators` list with the per-field operator sets (text vs `duration_seconds`), add the two-input `between` rendering and the monospace `matches_regex` input per spec.
- No schema change: operators are stored as free `TEXT` with no `CHECK` constraint on `route_condition.operator` or `condition_audit.operator`.
- Stored drifted rows are handled lazily, with no data migration: rows already persisted with `gt`/`gte`/`lt`/`lte`/`not_contains`/`not_equals`/`in_list` (dev-era data) continue to evaluate as non-matches, now with a logged warning at evaluation time; the next edit of such a route is rejected (`400`) until its operators are switched to the canonical set.

## Consequences

- **Positive:** the four spec-required operators become available; the engine and UI share one vocabulary; the Design ↔ Architecture mismatch closes.
- **Negative:** negations (`not_contains`, `not_equals`) and `in_list` are removed as first-class condition operators. This expressiveness is partially recovered by the existing route-scoped exclusion gate (`exclusion_type: word`/`title`/`channel`/`video`/`duration_min`/`duration_max`) and by OR-composition, but the removal is a real narrowing the accepted spec authorizes. If the Designer intended negations to survive, that is a spec change, not an engine change.
- **Neutral:** `equals` keeps its case-insensitive text behavior; `between` is inclusive at both ends.
- **Neutral:** write-boundary validation turns a previously-silent class of error (a typo'd or retired operator, a malformed `between`) into a `400` at the API, and the engine's `default` case makes stored drifted operators visible in the log instead of silently non-matching.

## Confirmation

- Unit test: `starts_with`, `ends_with`, `matches_regex` (including a case-insensitive `(?i)` pattern and an invalid pattern that yields `matched=false` without error), `greater_than`, `less_than`, and `between` (inclusive bounds, malformed value → false) each produce the expected match/no-match and record the correct `ConditionAudit.expected_value`.
- UI test: the Route Editor offers the per-field operator sets and serializes `between` as `"min,max"`.
- API test: `POST`/`PUT /api/routes` with an operator outside the field's canonical set (e.g. `gte`, or `between` on a text field) returns `400`; a `between` with a malformed value returns `400`.
- Engine test: a stored drifted operator evaluates to `false` and emits a warning log (no silent non-match).

## Open questions

- None.
