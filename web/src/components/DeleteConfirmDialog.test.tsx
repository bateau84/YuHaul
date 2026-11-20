import { StrictMode, useState } from 'react'
import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { DeleteConfirmDialog } from './DeleteConfirmDialog'

function Page() {
  const [open, setOpen] = useState(false)
  return (
    <main tabIndex={-1} data-testid="main">
      <button onClick={() => setOpen(true)}>Delete route</button>
      {open && <DeleteConfirmDialog routeLabel="Mix (a1)" onCancel={() => setOpen(false)} onConfirm={() => setOpen(false)} />}
    </main>
  )
}

function openDialog() {
  const trigger = screen.getByRole('button', { name: 'Delete route' })
  trigger.focus()
  fireEvent.click(trigger)
  expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
  return trigger
}

describe.each([
  ['plain', (ui: React.ReactElement) => ui],
  ['StrictMode', (ui: React.ReactElement) => <StrictMode>{ui}</StrictMode>],
])('DeleteConfirmDialog focus return (%s)', (_name, wrap) => {
  it('returns focus to the trigger on Cancel', () => {
    render(wrap(<Page />))
    const trigger = openDialog()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(trigger).toHaveFocus()
  })

  it('returns focus to the trigger on Escape', () => {
    render(wrap(<Page />))
    const trigger = openDialog()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(trigger).toHaveFocus()
  })

  // A browser moves focus to the nearest focusable ancestor (<main tabindex="-1">)
  // when the backdrop is pressed, before the click that cancels.
  it('returns focus to the trigger when the backdrop is clicked', () => {
    render(wrap(<Page />))
    const trigger = openDialog()
    const backdrop = screen.getByRole('alertdialog').parentElement!
    fireEvent.mouseDown(backdrop)
    screen.getByTestId('main').focus()
    expect(screen.getByTestId('main')).toHaveFocus()
    fireEvent.click(backdrop)
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(trigger).toHaveFocus()
  })
})
