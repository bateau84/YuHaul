import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ClosedNotice, RefusalAlert } from './ClosedNotice'
import { useLinkFocusOnce } from '../lib/focus'
import { expectUnfocusedAlert } from '../test/unfocused-alert'

describe('RefusalAlert', () => {
  it('is a live alert that takes no focus and has no accessible name', () => {
    render(
      <>
        <RefusalAlert text="Already decided." />
        <button>other</button>
      </>,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Already decided.')
    expectUnfocusedAlert(alert)
    expect(document.body).toHaveFocus()
  })
})

// Two closed rows; the first was just refused, so only its View video link takes
// focus (useLinkFocusOnce), once.
function Rows({ armed }: { armed: number | null }) {
  const linkFocus = useLinkFocusOnce()
  if (armed !== null) linkFocus.arm(armed)
  return (
    <MemoryRouter>
      <ClosedNotice outcome="accepted" videoId="aaaaaaaaaaa" target="First" linkRef={linkFocus.refFor(1)} />
      <ClosedNotice outcome="accepted" videoId="bbbbbbbbbbb" target="Second" linkRef={linkFocus.refFor(2)} />
    </MemoryRouter>
  )
}

describe('useLinkFocusOnce', () => {
  it('focuses only the armed row link, in the commit that attaches it, and not again on re-render', () => {
    const { rerender } = render(<Rows armed={1} />)
    expect(screen.getByRole('link', { name: 'View video: First' })).toHaveFocus()
    expect(screen.getByRole('link', { name: 'View video: Second' })).not.toHaveFocus()
    screen.getByRole('link', { name: 'View video: Second' }).focus()
    rerender(<Rows armed={null} />)
    expect(screen.getByRole('link', { name: 'View video: Second' })).toHaveFocus()
  })

  it('focuses nothing for rows that were never armed', () => {
    render(<Rows armed={null} />)
    expect(document.body).toHaveFocus()
  })
})
