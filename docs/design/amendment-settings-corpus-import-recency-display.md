---
type: design-amendment
title: Settings Corpus Import Recency Display — Null State Handling
description: Specifies the Settings UI display for corpus import recency, including the null state for legacy databases, with exact copy, placement, accessible semantics, and validation criteria.
tags: [design-amendment, settings, corpus-import, recency, null-state, accessibility]
authorities:
  - docs/requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md
  - docs/architecture/adr-015-durable-corpus-import-completion-timestamp.md
  - docs/architecture/ars-002-web-corpus-import-api.md
  - docs/design/yuhaul-admin-ui-design.md
---

## Experience context

BR-009 AC9 requires that corpus import recency be displayed in Settings and persist across restart. ADR-015 establishes that `last_successful_corpus_import_at` is `null` for legacy databases (created before the timestamp tracking feature was added) and explicitly states that null does not prove no import occurred. ARS-002 mandates that the Settings client read the metadata endpoint and present the value without substituting other dates, and notes that null means no reliable timestamp is recorded, not proof that no prior import occurred.

This amendment specifies the exact Settings UI display for both states (timestamp present and null) to ensure truthful, helpful communication without misleading the user about corpus state.

## States and interactions

### Display location

In the Settings view > Data region, adjacent to the corpus import controls.

### Label

"Last corpus import"

### When `last_successful_corpus_import_at` is present (non-null)

Display the formatted timestamp:
- Format: "<Month> <Day>, <Year> at <Time>" (e.g., "October 24, 2026 at 3:45 PM")
- The timestamp is in the user's local timezone
- Example: "Last corpus import: October 24, 2026 at 3:45 PM"

### When `last_successful_corpus_import_at` is null

Display the following:
- **Value**: "Not recorded"
- **Helper text**: "No reliable import date is available. This can occur when the database predates import tracking. Run a successful import to establish a timestamp."

The helper text:
- Truthfully states that no reliable date is available (does not claim the corpus is empty or never imported)
- Explains why this can happen (database predates import tracking)
- Guides the user to action (run a successful import)
- Does not claim that the corpus is empty or that no import ever occurred

## Focus / input / accessibility

### Semantic structure

- The label "Last corpus import" is a `<label>` element or has `role="text"` with appropriate association
- The value ("Not recorded" or the formatted date) is associated with the label using `aria-labelledby` or structural association
- The helper text is associated with the value using `aria-describedby` so screen readers announce it after the value

### Screen reader announcement

When the timestamp is null, screen readers should announce:
"Last corpus import: Not recorded. No reliable import date is available. This can occur when the database predates import tracking. Run a successful import to establish a timestamp."

### Visual design

- The value ("Not recorded" or the date) is displayed in normal text weight and color
- The helper text is displayed in a smaller font size and muted color (e.g., 80% opacity or secondary text color)
- The state does not rely on color alone to convey meaning (the text is clear without color)
- The display does not use error styling (red, warning icons) because null is not an error state—it is expected for legacy databases

### Keyboard navigation

- The display is static text and does not require keyboard interaction
- If the helper text includes an action link (e.g., "Run a successful import"), the link should be keyboard-focusable and have a clear focus indicator

## Validation criteria

1. **Truthfulness**: When `last_successful_corpus_import_at` is null, the display does not claim the corpus is empty or that no import ever occurred
2. **Clarity**: When null, the display truthfully states that no reliable import date is available
3. **Context**: When null, the display explains that this can occur when the database predates import tracking
4. **Actionability**: When null, the display guides the user to run a successful import to establish a timestamp
5. **Accuracy**: When non-null, the display shows the correct formatted timestamp in the user's local timezone
6. **Accessibility**: The label, value, and helper text are properly associated and readable by screen readers
7. **Persistence**: The display reads from the metadata endpoint and persists across restart
8. **Visual clarity**: The null state does not use error styling and is clearly distinguishable from an error state
9. **No substitution**: The display does not substitute `published_at`, source-row timestamps, or import-job `started` values

## Source authorities

- [BR-009 — Historical Title Persistence and Corpus Import](../requirements/yuhaul/br-009-historical-title-persistence-and-corpus-import.md) AC9
- [ADR-015 — Persist One Durable Corpus Import Completion Timestamp](../architecture/adr-015-durable-corpus-import-completion-timestamp.md)
- [ARS-002 — Web Corpus Import API Contract](../architecture/ars-002-web-corpus-import-api.md) §Durable corpus import metadata
- [YuHaul Admin Interface Design Specification](yuhaul-admin-ui-design.md) §View: Settings > Data region
