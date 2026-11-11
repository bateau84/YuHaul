import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { EndLegacyRunControl } from './EndLegacyRunControl'

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function mount(props: Partial<React.ComponentProps<typeof EndLegacyRunControl>> = {}) {
  return render(
    <MemoryRouter>
      <EndLegacyRunControl runId={14} savedStatus="running" {...props} />
    </MemoryRouter>,
  )
}

const endOk = { run_id: 14, status: 'failed', outcome: 'ended' }

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function confirmButton() {
  return within(screen.getByRole('dialog')).getByRole('button', { name: /^Mark(ing)? run 14 as ended/ })
}

function openDialog() {
  fireEvent.click(screen.getByRole('button', { name: 'Mark run 14 as ended' }))
}

describe('EndLegacyRunControl', () => {
  it('sends nothing until confirmed, and names the run, the failed status and the unprocessed videos', () => {
    const spy = vi.spyOn(globalThis, 'fetch')
    mount({ savedStatus: 'interrupted' })
    expect(screen.queryByRole('dialog')).toBeNull()
    openDialog()
    const dialog = screen.getByRole('dialog', { name: 'Mark run 14 as ended?' })
    expect(dialog).toHaveTextContent('Run 14 was created by an older version of YuHaul. Its saved status is interrupted, but nothing is executing it and it cannot be resumed.')
    expect(dialog).toHaveTextContent('Run 14 will be marked failed. It will never be resumed.')
    expect(dialog).toHaveTextContent('Its unfinished videos will not be processed.')
    expect(dialog).toHaveTextContent('This does not start or resume any run. The schedule is not affected.')
    expect(spy).not.toHaveBeenCalled()
  })

  it('starts focus on Keep and cancels by button, Escape and backdrop without a request', () => {
    const spy = vi.spyOn(globalThis, 'fetch')
    mount()
    openDialog()
    expect(screen.getByRole('button', { name: 'Keep run 14 as it is' })).toHaveFocus()
    fireEvent.click(screen.getByRole('button', { name: 'Keep run 14 as it is' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByRole('button', { name: 'Mark run 14 as ended' })).toHaveFocus()

    openDialog()
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(spy).not.toHaveBeenCalled()
  })

  it('is pending once, then acknowledges, takes focus and reports the result', async () => {
    let resolve!: (r: Response) => void
    const spy = vi.spyOn(globalThis, 'fetch').mockReturnValue(new Promise((r) => { resolve = r }))
    const onSettled = vi.fn()
    mount({ onSettled })
    openDialog()
    fireEvent.click(confirmButton())
    fireEvent.click(confirmButton())
    expect(spy).toHaveBeenCalledTimes(1)
    expect(confirmButton()).toHaveTextContent('Marking run 14 as ended…')
    expect(screen.getByRole('button', { name: 'Keep run 14 as it is' })).toBeDisabled()
    expect(within(screen.getByRole('dialog')).getByText(/Mark run 14 as ended\?/).closest('[aria-busy]')).toHaveAttribute('aria-busy', 'true')

    resolve(jsonResponse(endOk, 200))
    const ack = await screen.findByRole('status')
    expect(ack).toHaveTextContent('Marked as ended: run 14 is now marked failed. You ended it as a legacy run, and it will not be resumed. Its unfinished videos were not processed and were not added to any queue. Nothing was sent to YouTube.')
    expect(ack).toHaveFocus()
    expect(screen.queryByRole('button', { name: 'Mark run 14 as ended' })).toBeNull()
    expect(onSettled).toHaveBeenCalledWith(endOk)
    expect(screen.getByRole('link', { name: 'View runs' })).toHaveAttribute('href', '/runs')
  })

  // Dialog closes: the message is a persistent page alert and the run is re-read.
  it.each([
    [404, { code: 'run_not_found', run_id: 14 }, 'Mark as ended did nothing. Run 14 was not found. Nothing was changed.'],
    [409, { code: 'run_ended', run_id: 14, status: 'failed' }, 'Mark as ended did nothing. Run 14 has already ended. Nothing was changed.'],
    [409, { code: 'run_not_endable', run_id: 14, reason: 'not_legacy' }, 'Run 14 is not an older-version run, so it cannot be marked as ended. Nothing was changed.'],
    [409, { code: 'run_not_endable', run_id: 14, reason: 'not_production', run_type: 'dry_run' }, 'Run 14 is a dry run, so it cannot be marked as ended. Nothing was changed.'],
    [409, { code: 'run_not_endable', run_id: 14, reason: 'not_production', run_type: 'replay' }, 'Run 14 is a replay, so it cannot be marked as ended.'],
    [409, { code: 'run_not_endable', run_id: 14, reason: 'not_production', run_type: 'backtest' }, 'Run 14 is a backtest, so it cannot be marked as ended.'],
    [409, { code: 'run_not_endable', run_id: 14, reason: 'executing' }, 'Run 14 is running now, so it cannot be marked as ended. Nothing was changed. To end a running run, use “Stop run 14”.'],
    [500, { code: 'end_outcome_unknown', run_id: 14 }, 'Could not confirm whether run 14 was marked as ended. Open the run to check its status.'],
  ])('closes the dialog with a persistent alert for %i %j', async (status, body, text) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'raw error text', ...body }, status))
    const onSettled = vi.fn()
    mount({ onSettled })
    openDialog()
    fireEvent.click(confirmButton())
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(text)
    expect(alert).not.toHaveTextContent('raw error text')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(onSettled).toHaveBeenCalledWith(undefined)
  })

  // Dialog stays open with an alert inside it and the buttons re-enable.
  it.each([
    [409, { code: 'production_run_active', run_id: 14 }, 'A production run is running, so run 14 was not marked as ended. Nothing was changed. Wait for it to finish or stop it, then try again.'],
    [503, { code: 'startup_recovery_active', run_id: 14 }, 'YuHaul is starting up and recovering runs, so run 14 was not marked as ended. Nothing was changed. Try again in a minute.'],
    [503, { code: 'shutting_down', run_id: 14 }, 'YuHaul is shutting down, so run 14 was not marked as ended. Nothing was changed. Try again after it has started again.'],
    [503, {}, 'YuHaul can\'t mark a run as ended right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.'],
    [500, { code: 'end_failed', run_id: 14, status: 'interrupted' }, 'Run 14 could not be marked as ended, so it keeps its earlier status (interrupted). Nothing was changed. You can try again.'],
  ])('keeps the dialog open with an alert for %i %j', async (status, body, text) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'raw error text', ...body }, status))
    mount()
    openDialog()
    fireEvent.click(confirmButton())
    const alert = await within(await screen.findByRole('dialog')).findByRole('alert')
    expect(alert).toHaveTextContent(text)
    expect(alert).not.toHaveTextContent('raw error text')
    await waitFor(() => expect(confirmButton()).toBeEnabled())
    expect(screen.getByRole('button', { name: 'Keep run 14 as it is' })).toBeEnabled()
  })

  it('says it could not confirm on a network failure and never retries on its own', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('network'))
    mount()
    openDialog()
    fireEvent.click(confirmButton())
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not confirm whether run 14 was marked as ended.')
    expect(spy).toHaveBeenCalledTimes(1)
  })

  it('withdraws the button but keeps a message when the run stops being endable', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'x', code: 'run_ended', run_id: 14, status: 'failed' }, 409))
    const { rerender } = mount()
    openDialog()
    fireEvent.click(confirmButton())
    await screen.findByRole('alert')
    rerender(<MemoryRouter><EndLegacyRunControl runId={14} savedStatus="running" available={false} /></MemoryRouter>)
    expect(screen.queryByRole('button', { name: 'Mark run 14 as ended' })).toBeNull()
    expect(screen.getByRole('alert')).toHaveTextContent('has already ended')
  })
})
