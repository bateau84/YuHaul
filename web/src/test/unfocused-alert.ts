import { expect } from 'vitest'

// A refusal or removal notice is a persistent live alert that never takes
// focus (design, "Final Outcomes Are Never Reprocessed", item 3 and 3a): Orca
// with Chrome read a focused plain container as its first visual line only, and
// an alert that also took focus twice. So the alert must not be focused or
// focusable, and must carry no accessible name that would repeat its text.
export function expectUnfocusedAlert(alert: HTMLElement) {
  expect(alert).toHaveAttribute('role', 'alert')
  expect(alert).not.toHaveFocus()
  expect(alert.contains(document.activeElement)).toBe(false)
  expect(alert).not.toHaveAttribute('tabindex')
  expect(alert).not.toHaveAttribute('aria-label')
  expect(alert).not.toHaveAttribute('aria-labelledby')
}
