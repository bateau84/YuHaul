import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { useState } from 'react'
import { StopRunControl, StopRunMessage, type StopOutcome } from './StopRunControl'
import { ApiError } from '../api/core'
import type { Run } from '../types'

const mockStopRun = vi.fn()
vi.mock('../api/runs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/runs')>()),
  stopRun: (...a: unknown[]) => mockStopRun(...a),
}))

type Target = Parameters<typeof StopRunControl>[0]['run']
const prod: Target = { id: 41, run_type: 'scheduled', status: 'running', initiated_by: 'schedule' }

function Harness({ run, onOutcomeSpy }: { run: Target; onOutcomeSpy?: (o: StopOutcome) => void }) {
  const [outcome, setOutcome] = useState<StopOutcome | null>(null)
  return (
    <MemoryRouter>
      <StopRunControl run={run} onOutcome={(o) => { onOutcomeSpy?.(o); setOutcome(o) }} />
      {outcome && <StopRunMessage outcome={outcome} onDismiss={() => setOutcome(null)} />}
    </MemoryRouter>
  )
}

function open() {
  fireEvent.click(screen.getByRole('button', { name: 'Stop run 41' }))
}
function confirm() {
  fireEvent.click(within_dialog('Stop run 41'))
}
function within_dialog(name: string) {
  return screen.getAllByRole('button', { name }).find((b) => b.closest('[role="dialog"]'))!
}

// findByText then closest(): asserts the message is an alert without a role query.
async function findAlert(): Promise<HTMLElement> {
  const el = (await screen.findByText(/Stop did nothing|Could not confirm/)).closest('[role="alert"]')
  if (!el) throw new Error('message is not an alert')
  return el as HTMLElement
}

describe('StopRunControl', () => {
  beforeEach(() => { mockStopRun.mockReset() })

  it('renders nothing for a run that is not running', () => {
    render(<Harness run={{ ...prod, status: 'interrupted' }} />)
    expect(screen.queryByRole('button', { name: /Stop run/ })).toBeNull()
  })

  it('shows plain Stopping text instead of the button when a stop is recorded', () => {
    render(<Harness run={{ ...prod, stop_requested_at: '2026-11-10T10:00:00Z' } as Run} />)
    expect(screen.queryByRole('button', { name: /Stop run/ })).toBeNull()
    expect(screen.getByText('Stopping run 41…')).toBeTruthy()
  })

  it('opens a production dialog with Keep focused and sends nothing until confirmed', () => {
    render(<Harness run={prod} />)
    open()
    const dialog = screen.getByRole('dialog', { name: 'Stop run 41?' })
    expect(dialog.textContent).toContain('Run 41 is a scheduled run and is running now.')
    expect(dialog.textContent).toContain('deferred queue')
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Keep run 41 running' }))
    expect(mockStopRun).not.toHaveBeenCalled()
  })

  it('uses the simulation dialog for a dry run', () => {
    render(<Harness run={{ ...prod, run_type: 'dry_run', initiated_by: null }} />)
    open()
    const text = screen.getByRole('dialog').textContent
    expect(text).toContain('Run 41 is a dry run and is running now. It makes no changes to your playlists.')
    expect(text).toContain('Nothing is added to the deferred queue')
    expect(text).not.toContain('Stopping can take a moment')
  })

  it.each([
    ['Keep button', () => fireEvent.click(screen.getByRole('button', { name: 'Keep run 41 running' }))],
    ['Escape', () => fireEvent.keyDown(document, { key: 'Escape' })],
    ['backdrop', () => fireEvent.click(document.querySelector('.modal-backdrop')!)],
  ])('cancels via %s without calling the server and returns focus', async (_n, act) => {
    render(<Harness run={prod} />)
    const trigger = screen.getByRole('button', { name: 'Stop run 41' })
    trigger.focus()
    open()
    act()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(mockStopRun).not.toHaveBeenCalled()
    await waitFor(() => expect(document.activeElement).toBe(trigger))
  })

  it('is pending once: disables both buttons, marks busy and ignores a second press', async () => {
    let resolve!: (v: unknown) => void
    mockStopRun.mockReturnValue(new Promise((r) => { resolve = r }))
    render(<Harness run={prod} />)
    open()
    confirm()
    const confirmBtn = screen.getByRole('button', { name: 'Stopping run 41…' }) as HTMLButtonElement
    expect(confirmBtn.disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Keep run 41 running' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByRole('dialog').querySelector('[aria-busy="true"]')).not.toBeNull()
    fireEvent.click(confirmBtn)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(mockStopRun).toHaveBeenCalledTimes(1)
    resolve({ run_id: 41, status: 'running', stop: 'accepted' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('acknowledges an accepted stop with a status message and replaces the button', async () => {
    mockStopRun.mockResolvedValue({ run_id: 41, status: 'running', stop: 'accepted' })
    render(<Harness run={prod} />)
    open()
    confirm()
    const status = await screen.findByRole('status')
    expect(status.textContent).toContain('Stopping: run 41 is stopping and will never be resumed. It is not stopped yet.')
    expect(status.textContent).not.toContain('deferred queue')
    expect(screen.queryByRole('button', { name: 'Stop run 41' })).toBeNull()
    expect(screen.getByText('Stopping run 41…')).toBeTruthy()
    expect(mockStopRun).toHaveBeenCalledWith(41)
  })

  it('treats already_stopping as success, not a refusal', async () => {
    mockStopRun.mockResolvedValue({ run_id: 41, status: 'running', stop: 'already_stopping' })
    render(<Harness run={prod} />)
    open()
    confirm()
    const status = await screen.findByRole('status')
    expect(status.textContent).toContain('Stopping: run 41 is already stopping.')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it.each([
    ['shutting_down', 503, 'YuHaul is shutting down, so run 41 was not stopped'],
    [undefined, 503, "YuHaul can't accept a stop right now"],
  ])('keeps the dialog open with an alert for 503 (%s) and re-enables buttons', async (code, status, text) => {
    mockStopRun.mockImplementation(async () => { throw new ApiError(status, 'x', { code, runId: 41 }) })
    render(<Harness run={prod} />)
    open()
    confirm()
    const alert = await findAlert()
    expect(alert.textContent).toContain(`Stop did nothing. ${text}`)
    expect(screen.getByRole('dialog').contains(alert)).toBe(true)
    expect((within_dialog('Stop run 41') as HTMLButtonElement).disabled).toBe(false)
  })

  it.each([
    ['run_not_found', 404, 'Run 41 was not found. Nothing was changed.'],
    ['run_ended', 409, 'Run 41 has already ended, so there is nothing to stop. Nothing was changed.'],
    ['run_not_active', 409, 'Run 41 is not running, so there is nothing to stop. Nothing was changed.'],
  ])('closes the dialog and shows a persistent alert for %s', async (code, status, text) => {
    mockStopRun.mockImplementation(async () => { throw new ApiError(status, 'x', { code, runId: 41 }) })
    render(<Harness run={prod} />)
    open()
    confirm()
    const alert = await findAlert()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(alert.textContent).toContain(`Stop did nothing. ${text}`)
  })

  it('adds the discard hint for a not-active discardable run only', async () => {
    mockStopRun.mockImplementation(async () => { throw new ApiError(409, 'x', { code: 'run_not_active', runId: 41 }) })
    render(<Harness run={{ ...prod, discardable: true }} />)
    open()
    confirm()
    expect((await findAlert()).textContent).toContain('You can discard it from its run page.')
  })

  it('says it could not confirm on a network failure and does not retry', async () => {
    mockStopRun.mockImplementation(async () => { throw new TypeError('network') })
    render(<Harness run={prod} />)
    open()
    confirm()
    const alert = await findAlert()
    expect(alert.textContent).toContain('Could not confirm whether run 41 was stopped.')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(mockStopRun).toHaveBeenCalledTimes(1)
  })
})
