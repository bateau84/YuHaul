import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ChannelScanErrors } from './ChannelScanErrors'
import * as sourceApi from '../api/source-failures'
import * as queueApi from '../api/queue'
import { ErrorsView } from '../views/ErrorsView'
import { ToastProvider } from './ToastProvider'
import type { SourceFailure, SourceFailurePage } from '../types'

const CH_B = `UC${'B'.repeat(22)}`
const CH_C = `UC${'C'.repeat(22)}`

function failure(over: Partial<SourceFailure> = {}): SourceFailure {
  return {
    id: 5,
    run_id: 9,
    channel_id: CH_B,
    channel_name: 'Channel B',
    occurred_at: '2026-11-20T12:00:00Z',
    category: 'permanent',
    operation: 'playlist_items_list',
    http_status: 404,
    reason: 'playlistNotFound',
    ...over,
  }
}

const page = (items: SourceFailure[], next: number | null = null): SourceFailurePage => ({ items, next_before_id: next })

function renderSection() {
  return render(
    <MemoryRouter>
      <ChannelScanErrors />
    </MemoryRouter>,
  )
}

afterEach(() => vi.restoreAllMocks())

describe('Errors: Channel scan errors', () => {
  it('shows one read-only entry with the accepted copy and only a View run link', async () => {
    vi.spyOn(sourceApi, 'getSourceFailures').mockResolvedValue(page([failure()]))
    renderSection()

    const heading = await screen.findByRole('heading', { name: 'Channel scan errors' })
    const section = heading.closest('section')!
    expect(within(section).getByText(/A history of channel scans that failed, newest first\./)).toBeInTheDocument()
    expect(within(section).getByText('Channel scan error')).toBeInTheDocument()
    expect(within(section).getByText('Channel B')).toBeInTheDocument()
    expect(within(section).getByText(`Channel ID: ${CH_B}`)).toBeInTheDocument()
    expect(within(section).getByText(/Failed in run 9 on /)).toBeInTheDocument()
    expect(within(section).getByText(/Uploads could not be scanned\. YouTube reported that the uploads playlist was not found\./)).toBeInTheDocument()
    expect(within(section).getByText(/This entry records what happened in run 9\. It does not show whether this channel scans successfully now\./)).toBeInTheDocument()
    expect(within(section).getByRole('link', { name: 'View run 9' })).toHaveAttribute('href', '/runs/9')
    // No command of any kind, and no status number or raw text.
    expect(within(section).queryByRole('button')).not.toBeInTheDocument()
    expect(section.textContent).not.toMatch(/Retry|Dismiss|Remove|suppress|404|UU/)
  })

  it('names the channel by its ID when it has no name, and says so when there is neither', async () => {
    vi.spyOn(sourceApi, 'getSourceFailures').mockResolvedValue(
      page([failure({ id: 3, channel_name: null }), failure({ id: 2, channel_name: null, channel_id: null, run_id: 8 })]),
    )
    renderSection()
    const section = (await screen.findByRole('heading', { name: 'Channel scan errors' })).closest('section')!
    const items = within(section).getAllByRole('listitem')
    expect(items[0].textContent).toContain(CH_B)
    expect(items[0].textContent).not.toContain('Channel ID:')
    expect(items[1].textContent).toContain('Channel details unavailable.')
  })

  it.each([
    ['an unknown reason', 'somethingElse'],
    ['no reason', null],
  ])('reads "The specific reason is unavailable." for %s', async (_n, reason) => {
    vi.spyOn(sourceApi, 'getSourceFailures').mockResolvedValue(page([failure({ reason })]))
    renderSection()
    expect(await screen.findByText(/The specific reason is unavailable\./)).toBeInTheDocument()
    expect(screen.queryByText(/uploads playlist was not found/)).not.toBeInTheDocument()
  })

  it('shows the loading text, then the empty copy that never says channels are healthy', async () => {
    let resolve!: (p: SourceFailurePage) => void
    vi.spyOn(sourceApi, 'getSourceFailures').mockReturnValue(new Promise((r) => { resolve = r }))
    renderSection()
    expect(screen.getByText('Loading channel scan errors…')).toBeInTheDocument()
    resolve(page([]))
    expect(await screen.findByText('No channel scan errors are recorded. Runs from before YuHaul kept these records show none.')).toBeInTheDocument()
    expect(screen.queryByText(/healthy|all channels/i)).not.toBeInTheDocument()
  })

  it('a failed read is not an empty state, and Reload re-reads', async () => {
    const spy = vi.spyOn(sourceApi, 'getSourceFailures').mockRejectedValueOnce(new Error('boom')).mockResolvedValue(page([failure()]))
    renderSection()
    expect(await screen.findByText('Could not load channel scan errors. Nothing was changed.')).toBeInTheDocument()
    expect(screen.queryByText(/No channel scan errors are recorded/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Reload channel scan errors' }))
    expect(await screen.findByText('Channel B')).toBeInTheDocument()
    expect(spy).toHaveBeenCalledTimes(2)
  })

  it('loads older entries by cursor, announces them politely and moves focus to the first new one', async () => {
    const spy = vi
      .spyOn(sourceApi, 'getSourceFailures')
      .mockResolvedValueOnce(page([failure({ id: 9, run_id: 12 })], 9))
      .mockResolvedValueOnce(page([failure({ id: 7, run_id: 10, channel_id: CH_C, channel_name: 'Channel C' }), failure({ id: 6, run_id: 9 })]))
    renderSection()
    fireEvent.click(await screen.findByRole('button', { name: 'Show older entries' }))

    await screen.findByText('Channel C')
    expect(spy).toHaveBeenLastCalledWith({ beforeId: 9 })
    expect(screen.queryByRole('button', { name: 'Show older entries' })).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('2 older entries loaded.')
    await waitFor(() => expect(document.activeElement).toBe(screen.getByText('Channel C').closest('li')))
    expect(screen.queryByText(/of \d+|total|page \d/i)).not.toBeInTheDocument()
  })

  it('keeps the loaded list and labels it as possibly out of date when a later read fails', async () => {
    vi.spyOn(sourceApi, 'getSourceFailures')
      .mockResolvedValueOnce(page([failure({ id: 9 })], 9))
      .mockRejectedValueOnce(new Error('boom'))
    renderSection()
    fireEvent.click(await screen.findByRole('button', { name: 'Show older entries' }))
    expect(await screen.findByText('The list below may be out of date.')).toBeInTheDocument()
    expect(screen.getByText('Channel B')).toBeInTheDocument()
  })
})

describe('Errors view with channel scan errors', () => {
  it('shows the section on the Errors tab without changing the video error count or adding commands', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(sourceApi, 'getSourceFailures').mockResolvedValue(page([failure(), failure({ id: 4, run_id: 8 })]))
    render(
      <MemoryRouter>
        <ToastProvider>
          <ErrorsView />
        </ToastProvider>
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { name: 'Channel scan errors' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Errors (0)' })).toBeInTheDocument()
    const section = screen.getByRole('heading', { name: 'Channel scan errors' }).closest('section')!
    expect(await within(section).findAllByText('Channel scan error')).toHaveLength(2)
    expect(within(section).queryByRole('button')).not.toBeInTheDocument()
  })
})
