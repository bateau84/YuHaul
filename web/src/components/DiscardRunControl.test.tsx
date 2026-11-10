import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { DiscardRunControl } from './DiscardRunControl'

// user-event is not a dependency; this covers the click and key presses used here.
const userEvent = {
  setup: () => ({
    click: async (el: Element) => { fireEvent.click(el) },
    keyboard: async (keys: string) => {
      if (keys === '{Escape}') fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    },
  }),
}

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function mount(props: Partial<React.ComponentProps<typeof DiscardRunControl>> = {}) {
  return render(
    <MemoryRouter>
      <DiscardRunControl runId={30} cause="snapshot_unavailable" {...props} />
    </MemoryRouter>,
  )
}

const discardOk = { run_id: 30, status: 'failed', outcome: 'discarded', cause: 'snapshot_unavailable' }

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function confirmButton() {
  return within(screen.getByRole('dialog')).getByRole('button', { name: /^Discard/ })
}

async function openDialog() {
  const user = userEvent.setup()
  const trigger = screen.getByRole('button', { name: 'Discard run 30' })
  await user.click(trigger)
  return { user, trigger }
}

describe('DiscardRunControl', () => {
  it('shows only the labelled trigger and sends nothing until confirmed', async () => {
    const spy = vi.spyOn(globalThis, 'fetch')
    mount()
    expect(screen.queryByRole('dialog')).toBeNull()
    await openDialog()
    expect(screen.getByRole('dialog', { name: 'Discard run 30?' })).toBeInTheDocument()
    expect(spy).not.toHaveBeenCalled()
  })

  it('states the opening line and the four facts, and starts focus on Keep', async () => {
    mount({ cause: 'checkpoint_unrecognized' })
    await openDialog()
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByText(/Run 30 stopped at its time limit and cannot be resumed\. Its saved progress point is not recognized\./)).toBeInTheDocument()
    const facts = within(dialog).getAllByRole('listitem').map((li) => li.textContent)
    expect(facts).toEqual([
      'Run 30 will be marked failed. It will never be resumed.',
      'Its unfinished videos will be put in the deferred queue. The next new run, from Run Now or the schedule, will process them automatically.',
      'Nothing is sent to YouTube. Playlist additions already recorded stay recorded. An addition whose outcome could not be confirmed stays marked as unconfirmed and still blocks a repeat.',
      'This cannot be undone.',
    ])
    expect(within(dialog).getByText(/Discarding does not start a run\./)).toBeInTheDocument()
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Keep run 30' })).toHaveFocus()
    expect(dialog).toHaveAccessibleDescription(/stopped at its time limit/)
  })

  it.each([
    ['snapshot_unavailable', 'Its saved settings are missing.'],
    ['snapshot_unreadable', 'Its saved settings cannot be read.'],
    ['snapshot_inadmissible', 'Its saved settings cannot be used safely.'],
    ['checkpoint_unrecognized', 'Its saved progress point is not recognized.'],
    ['terminal_timestamp_contradiction', 'Its saved record is contradictory: it has a completion time but is marked interrupted.'],
    [undefined, 'Its saved data cannot be used to continue it safely.'],
    ['something_new', 'Its saved data cannot be used to continue it safely.'],
  ])('maps cause %s to its reason sentence', async (cause, sentence) => {
    mount({ cause })
    await openDialog()
    expect(screen.getByRole('dialog')).toHaveTextContent(`cannot be resumed. ${sentence}`)
    if (cause === 'something_new') expect(screen.getByRole('dialog')).not.toHaveTextContent('something_new')
  })

  it.each([
    ['Keep button', async (u: ReturnType<typeof userEvent.setup>) => u.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Keep run 30' }))],
    ['Escape', async (u: ReturnType<typeof userEvent.setup>) => u.keyboard('{Escape}')],
    ['backdrop click', async () => { fireEvent.click(document.querySelector('.modal-backdrop') as Element) }],
  ])('closes via %s, changes nothing and returns focus to the trigger', async (_name, act) => {
    const spy = vi.spyOn(globalThis, 'fetch')
    mount()
    const { user, trigger } = await openDialog()
    await act(user)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(spy).not.toHaveBeenCalled()
    expect(trigger).toHaveFocus()
  })

  it('shows the pending state, blocks a second submit, then acknowledges with focus', async () => {
    let resolve!: (r: Response) => void
    const spy = vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise<Response>((r) => { resolve = r }))
    const onDiscarded = vi.fn()
    mount({ onDiscarded })
    const { user } = await openDialog()
    const confirm = confirmButton()
    await user.click(confirm)

    const busy = await screen.findByRole('button', { name: 'Discarding run 30…' })
    expect(busy).toBeDisabled()
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Keep run 30' })).toBeDisabled()
    expect(document.querySelector('[aria-busy="true"]')).not.toBeNull()
    fireEvent.click(busy)
    await user.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy).toHaveBeenCalledWith('/api/runs/30/discard', expect.objectContaining({ method: 'POST' }))

    resolve(jsonResponse(discardOk, 200))
    const ack = await screen.findByRole('status')
    expect(ack).toHaveFocus()
    expect(ack).toHaveTextContent(
      'Discarded: run 30 is now marked failed and will not be resumed. Its unfinished videos are in the deferred queue, and the next new run will process them automatically. Nothing was sent to YouTube. Press Run Now to continue with the next stopped run, or to start a new run.',
    )
    expect(within(ack).getByRole('link', { name: 'View deferred queue' })).toHaveAttribute('href', '/errors')
    expect(within(ack).getByRole('link', { name: 'View run 30' })).toHaveAttribute('href', '/runs/30')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('button', { name: /Discard run/ })).toBeNull()
    expect(onDiscarded).toHaveBeenCalledWith(discardOk)
  })

  it('omits the View run link in the acknowledgement on the run page', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(discardOk, 200))
    mount({ onRunPage: true })
    const { user } = await openDialog()
    await user.click(confirmButton())
    const ack = await screen.findByRole('status')
    expect(within(ack).queryByRole('link', { name: 'View run 30' })).toBeNull()
    expect(within(ack).getByRole('link', { name: 'View deferred queue' })).toBeInTheDocument()
  })

  describe('refusals that close the dialog', () => {
    it.each([
      [404, { error: 'x', code: 'run_not_found', run_id: 30 }, 'Discard did nothing. Run 30 was not found. Nothing was changed.', ['View runs']],
      [409, { error: 'x', code: 'run_ended', run_id: 30 }, 'Discard did nothing. Run 30 has already ended, so it was not discarded again. Nothing was changed.', ['View run 30']],
      [
        409,
        { error: 'x', code: 'run_not_discardable', run_id: 30, reason: 'not_oldest' },
        'Discard did nothing. Run 30 cannot be discarded now. Only the run that is blocking Run Now can be discarded, and run 30 is not that run. Its state may have changed. Nothing was changed.',
        ['View run 30', 'Go to Overview'],
      ],
      [500, { error: 'x', code: 'discard_outcome_unknown', run_id: 30 }, 'Could not confirm whether run 30 was discarded. Open the run to check its status before trying again.', ['View run 30']],
      [500, { error: 'x' }, 'Could not confirm whether run 30 was discarded. Open the run to check its status before trying again.', ['View run 30']],
    ])('%i %j', async (status, body, text, links) => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(body, status))
      const onRefused = vi.fn()
      mount({ onRefused })
      const { user } = await openDialog()
      await user.click(confirmButton())

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent(text)
      expect(within(alert).getAllByRole('link').map((a) => a.textContent)).toEqual(links)
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(alert).not.toHaveFocus()
      expect(onRefused).toHaveBeenCalledTimes(1)
      expect(screen.queryByRole('status')).toBeNull()
    })

    it('treats a dropped connection as uncertain and does not retry', async () => {
      const spy = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('network'))
      mount()
      const { user } = await openDialog()
      await user.click(confirmButton())
      expect(await screen.findByRole('alert')).toHaveTextContent('Could not confirm whether run 30 was discarded.')
      expect(spy).toHaveBeenCalledTimes(1)
    })
  })

  describe('refusals that keep the dialog open', () => {
    it.each([
      [409, { error: 'x', code: 'production_run_active', run_id: 41 }, 'Discard did nothing. A production run is already running. Run 30 was not discarded and nothing was changed. Wait for it to finish, then try again.', ['View run 41']],
      [503, { error: 'x', code: 'startup_recovery_active' }, "Discard did nothing. YuHaul can't discard a run right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.", ['View runs']],
      [503, { error: 'x', code: 'shutting_down' }, "Discard did nothing. YuHaul can't discard a run right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.", ['View runs']],
      [500, { error: 'x', code: 'discard_failed', run_id: 30 }, 'Discard did nothing. Run 30 could not be saved as discarded, so it is still interrupted and nothing was changed. You can try again.', []],
    ])('%i %j', async (status, body, text, links) => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(body, status))
      mount()
      const { user } = await openDialog()
      await user.click(confirmButton())

      const dialog = screen.getByRole('dialog')
      const alert = await within(dialog).findByRole('alert')
      expect(alert).toHaveTextContent(text)
      expect(within(alert).queryAllByRole('link').map((a) => a.textContent)).toEqual(links)
      expect(confirmButton()).toBeEnabled()
      expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Keep run 30' })).toBeEnabled()
    })

    it('production_run_active without a run id links to the runs list', async () => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'x', code: 'production_run_active' }, 409))
      mount()
      const { user } = await openDialog()
      await user.click(confirmButton())
      const alert = await within(screen.getByRole('dialog')).findByRole('alert')
      expect(within(alert).getByRole('link', { name: 'View runs' })).toBeInTheDocument()
    })

    it('allows a retry that then succeeds', async () => {
      vi.spyOn(globalThis, 'fetch')
        .mockResolvedValueOnce(jsonResponse({ error: 'x', code: 'discard_failed', run_id: 30 }, 500))
        .mockResolvedValueOnce(jsonResponse(discardOk, 200))
      mount()
      const { user } = await openDialog()
      await user.click(confirmButton())
      await within(screen.getByRole('dialog')).findByRole('alert')
      await user.click(confirmButton())
      expect(await screen.findByRole('status')).toHaveTextContent('Discarded: run 30')
    })
  })

  it('never uses the bare word Discard as a control label', async () => {
    mount()
    await openDialog()
    for (const b of screen.getAllByRole('button')) expect(b.textContent).not.toMatch(/^Discard$/)
    expect(screen.getByRole('dialog').textContent).not.toMatch(/dismiss|Error Queue/i)
  })
})
