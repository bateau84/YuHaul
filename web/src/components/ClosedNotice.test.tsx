import { useLayoutEffect } from 'react'
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { RefusalAlert } from './ClosedNotice'

// Records what has focus in the layout phase of the same commit that attached
// the alert. A passive useEffect runs after this phase, so it would still see <body>.
function FocusProbe({ seen }: { seen: Element[] }) {
  useLayoutEffect(() => {
    if (document.activeElement) seen.push(document.activeElement)
  })
  return null
}

describe('RefusalAlert', () => {
  it('has focus in the commit that attaches it, not in a later passive effect', () => {
    const seen: Element[] = []
    render(
      <>
        <RefusalAlert text="Already decided." />
        <FocusProbe seen={seen} />
      </>
    )
    const alert = screen.getByRole('alert')
    expect(seen[0]).toBe(alert)
    expect(alert).toHaveFocus()
  })

  it('does not take focus back on a later re-render', () => {
    const { rerender } = render(
      <>
        <RefusalAlert text="Already decided." />
        <button>other</button>
      </>
    )
    const other = screen.getByRole('button', { name: 'other' })
    other.focus()
    rerender(
      <>
        <RefusalAlert text="Already decided." />
        <button>other</button>
      </>
    )
    expect(other).toHaveFocus()
  })
})
