import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { FailureReason } from './FailureReason'

// Design, "Added codes": where the reason is shown outside the run's own page,
// a known run id makes "run {id}", "Open the run to see what was recorded." and
// the link View run {id}; with no id the wording is "a retry run" and no link.

const reason = (code: string) => `diag:v1 phase=unknown operation=retry code=${code}`
const view = (code: string, run?: Parameters<typeof FailureReason>[0]['run']) =>
  render(
    <MemoryRouter>
      <FailureReason message={reason(code)} subject="the item" run={run} />
    </MemoryRouter>,
  )

describe('FailureReason retry-run context', () => {
  it.each(['retry_claim_unverifiable', 'retry_work_removed'])('%s with a run id names the run and links to it', (code) => {
    const { container } = view(code, { kind: 'id', id: 41 })
    const sentence = container.querySelector('span')!.textContent ?? ''
    expect(sentence).toMatch(/run 41|Run 41/)
    expect(sentence).toMatch(/Open the run to see what was recorded\./)
    expect(screen.getByRole('link', { name: 'View run 41' })).toHaveAttribute('href', '/runs/41')
  })

  it.each(['retry_claim_unverifiable', 'retry_work_removed'])('%s with no run id says "a retry", links nothing and has no "Open the run" sentence', (code) => {
    view(code)
    expect(screen.getByText(/a retry/i)).toBeInTheDocument()
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.queryByText(/Open the run/)).toBeNull()
    expect(screen.queryByText(/this run/)).toBeNull()
  })

  it('keeps the support code under Technical details', () => {
    view('retry_claim_unverifiable', { kind: 'id', id: 7 })
    expect(screen.getByText(reason('retry_claim_unverifiable'))).toBeInTheDocument()
  })

  it('never links a code that is not a retry-run code', () => {
    view('stop_settlement_blocked', { kind: 'id', id: 7 })
    expect(screen.queryByRole('link')).toBeNull()
  })
})
