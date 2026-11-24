import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { RemovalAlert, RemovedNotice } from './RemovedNotice'
import { retryRemovedNotice } from '../lib/retry-removed'
import { useLinkFocusOnce } from '../lib/focus'
import { expectUnfocusedAlert } from '../test/unfocused-alert'

// Design 3a: the removal notice is a live alert that never takes focus.
describe('RemovalAlert', () => {
  it('is role="alert", names the target when given, and takes no focus', () => {
    render(
      <MemoryRouter>
        <RemovalAlert target="Some Video" runId={7} />
        <button>elsewhere</button>
      </MemoryRouter>,
    )
    const elsewhere = screen.getByRole('button', { name: 'elsewhere' })
    elsewhere.focus()
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(`Some Video: ${retryRemovedNotice}`)
    expect(screen.getByRole('link', { name: 'View run 7' })).toHaveAttribute('href', '/runs/7')
    expectUnfocusedAlert(alert)
    expect(elsewhere).toHaveFocus()
  })
})

function Row({ armed }: { armed: boolean }) {
  const linkFocus = useLinkFocusOnce()
  if (armed) linkFocus.arm(3)
  return (
    <MemoryRouter>
      <RemovedNotice videoId="aaaaaaaaaaa" target="Gone" linkRef={linkFocus.refFor(3)} />
    </MemoryRouter>
  )
}

describe('RemovedNotice', () => {
  it('focuses its View video link once when the row was just removed under a Retry', () => {
    render(<Row armed />)
    expect(screen.getByRole('link', { name: 'View video: Gone' })).toHaveFocus()
  })

  it('does not take focus for a row that was not just removed', () => {
    render(<Row armed={false} />)
    expect(document.body).toHaveFocus()
  })
})
