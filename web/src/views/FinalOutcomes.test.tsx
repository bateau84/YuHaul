import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ErrorsView } from './ErrorsView'
import { ToastProvider } from '../components/ToastProvider'
import { ApiError } from '../api/core'
import * as queueApi from '../api/queue'
import * as deferredPageApi from '../api/deferred-page'
import type { DeferredItem, DeferredPage, ErrorEntry } from '../types'

// BR-039 queue copy: the 409 video_already_final refusal followed by the closed
// row, closed rows from the API, the dismiss-only original_decision_unavailable
// entry, and the Deferred rejected page. Design: "Final Outcomes Are Never
// Reprocessed — Run Detail, Queue and Statistics Copy", items 3–5 and 7.

const REFUSAL_ACCEPTED =
  'Retry did nothing. This video already has a final outcome: Accepted. It was not processed again, and nothing was sent to YouTube. This item is now closed.'
const CLOSED_ACCEPTED = 'Closed. This video already has a final outcome: Accepted. It was not processed again.'

function errorEntry(over: Partial<ErrorEntry> = {}): ErrorEntry {
  return {
    id: 427,
    video_id: 12,
    video_title: 'Final Video',
    external_video_id: 'dQw4w9WgXcQ',
    run_id: 9,
    error_category: 'api_error',
    error_message: 'diag:v1 phase=routing operation=load_metadata code=provider_error',
    status: 'active',
    created_at: '2026-10-01T00:00:00Z',
    retry_count: 0,
    ...over,
  }
}

function deferredItem(id: number, over: Partial<DeferredItem> = {}): DeferredItem {
  return {
    id,
    video_id: 1000 + id,
    external_video_id: `vid${String(id).padStart(8, '0')}`,
    video_title: `Video ${id}`,
    run_id: 5,
    deferral_reason: 'quota_exhausted',
    status: 'pending',
    created_at: '2026-11-11T00:00:00Z',
    retry_count: 0,
    ...over,
  }
}

function pageOf(items: DeferredItem[], over: Partial<DeferredPage> = {}): DeferredPage {
  return {
    deferred: items,
    total: items.length,
    queue_total: items.length,
    by_reason: {},
    page: { limit: 50, number: 1, total_pages: 1, first_position: items.length ? 1 : null, last_position: items.length || null, next_after_id: null, prev_before_id: null },
    ...over,
  }
}

function renderErrors(search = '') {
  return render(
    <MemoryRouter initialEntries={[`/errors${search}`]}>
      <ToastProvider>
        <ErrorsView />
      </ToastProvider>
    </MemoryRouter>,
  )
}

const refused = (outcome?: string, entry?: unknown) =>
  new ApiError(409, 'this video already has a final outcome; nothing was retried', { code: 'video_already_final', finalOutcome: outcome, entry })

// jsdom has no modal dialog support; open and close are all these tests need.
const showModal = HTMLDialogElement.prototype.showModal
const close = HTMLDialogElement.prototype.close
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
  vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(pageOf([]))
})
afterEach(() => {
  HTMLDialogElement.prototype.showModal = showModal
  HTMLDialogElement.prototype.close = close
  vi.restoreAllMocks()
})

describe('Errors: Retry refused with video_already_final', () => {
  it('shows a persistent alert in the row place, then the closed row; sends nothing more and does not count it', async () => {
    const entry = errorEntry()
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry, errorEntry({ id: 428, video_title: 'Other Video', external_video_id: 'aaaaaaaaaaa' })])
    const retry = vi.spyOn(queueApi, 'retryError').mockRejectedValue(
      refused('accepted', { ...entry, closed_reason: 'final_outcome', video_final_outcome: 'accepted', status: 'resolved' }),
    )
    renderErrors()
    expect(await screen.findByRole('tab', { name: 'Errors (2)' })).toBeInTheDocument()
    const row = screen.getByText('Final Video').closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: /^Retry / }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(REFUSAL_ACCEPTED)
    // A persistent message, not a toast. Focus moves to it, not to <body>.
    expect(alert).toHaveFocus()
    expect(retry).toHaveBeenCalledTimes(1)

    const closedRow = screen.getByText('Final Video').closest('tr')!
    expect(within(closedRow).getByText('Closed')).toBeInTheDocument()
    expect(within(closedRow).getByText(CLOSED_ACCEPTED)).toBeInTheDocument()
    expect(within(closedRow).queryByRole('button')).toBeNull()
    expect(within(closedRow).getByRole('link', { name: 'View video' })).toHaveAttribute('href', '/videos/dQw4w9WgXcQ')
    // The closed row keeps its original reason and is not counted as active.
    expect(within(closedRow).getByText('diag:v1 phase=routing operation=load_metadata code=provider_error')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Errors (1)' })).toBeInTheDocument()
    // The other row is untouched.
    const other = screen.getByText('Other Video').closest('tr')!
    expect(within(other).getByRole('button', { name: /^Retry / })).toBeInTheDocument()
  })

  it('names no outcome it does not know, and closes the row without a usable entry', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry()])
    vi.spyOn(queueApi, 'retryError').mockRejectedValue(refused('not_yet_evaluated', { id: 999 }))
    renderErrors()
    fireEvent.click(await screen.findByRole('button', { name: /^Retry / }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Retry did nothing. This video already has a final outcome. It was not processed again, and nothing was sent to YouTube. This item is now closed.',
    )
    const row = screen.getByText('Final Video').closest('tr')!
    expect(within(row).getByText('Closed. This video already has a final outcome. It was not processed again.')).toBeInTheDocument()
    expect(within(row).queryByRole('button')).toBeNull()
    expect(screen.queryByText(/not_yet_evaluated/)).toBeNull()
  })

  it('names Unavailable on YouTube', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry()])
    vi.spyOn(queueApi, 'retryError').mockRejectedValue(refused('unavailable_on_youtube'))
    renderErrors()
    fireEvent.click(await screen.findByRole('button', { name: /^Retry / }))
    expect(await screen.findByRole('alert')).toHaveTextContent('final outcome: Unavailable on YouTube. It was not processed again')
  })

  it('still toasts other retry failures and shows no closed row', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry()])
    vi.spyOn(queueApi, 'retryError').mockRejectedValue(new ApiError(409, 'invalid state'))
    renderErrors()
    fireEvent.click(await screen.findByRole('button', { name: /^Retry / }))
    expect((await screen.findAllByText('invalid state')).length).toBeGreaterThan(0)
    expect(screen.queryByText('Closed')).toBeNull()
  })

  it('shows a closed entry the API returns as closed, with no Retry or Remove, and leaves it out of the count', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      errorEntry({ closed_reason: 'final_outcome', video_final_outcome: 'rejected', status: 'resolved' }),
      errorEntry({ id: 2, video_title: 'Live', external_video_id: 'bbbbbbbbbbb' }),
    ])
    renderErrors()
    expect(await screen.findByRole('tab', { name: 'Errors (1)' })).toBeInTheDocument()
    const row = screen.getByText('Final Video').closest('tr')!
    expect(within(row).getByText('Closed. This video already has a final outcome: Rejected. It was not processed again.')).toBeInTheDocument()
    expect(within(row).queryByRole('button')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

describe('Errors: original_decision_unavailable', () => {
  const unreadable = () =>
    errorEntry({ error_message: 'diag:v1 phase=action_execution operation=load_run code=original_decision_unavailable' })

  it('offers Dismiss only, with the type, the detail and the no-retry line, and never the raw diagnostic', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([unreadable()])
    renderErrors()
    const row = (await screen.findByText('Final Video')).closest('tr')!
    expect(within(row).getByText('Original decision unavailable')).toBeInTheDocument()
    expect(
      within(row).getByText(
        "This video already has a playlist addition that YuHaul sent in an earlier run. YuHaul cannot read that run's saved decision, so it cannot safely send the remaining additions. Nothing was sent now.",
      ),
    ).toBeInTheDocument()
    expect(within(row).queryByRole('button', { name: /^Retry / })).toBeNull()
    expect(within(row).getByText('Retry is not offered: it would fail the same way.')).toBeInTheDocument()
    expect(within(row).getByRole('link', { name: 'View video' })).toHaveAttribute('href', '/videos/dQw4w9WgXcQ')
    expect(within(row).getAllByRole('button')).toHaveLength(1)
    expect(screen.queryByText(/diag:v1|original_decision_unavailable/)).toBeNull()
  })

  it('confirms with the longer text before dismissing, and toasts once it is dismissed', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([unreadable()])
    const dismiss = vi.spyOn(queueApi, 'dismissError').mockResolvedValue(unreadable())
    renderErrors()
    fireEvent.click(await screen.findByRole('button', { name: 'Dismiss error: Final Video' }))
    const dialog = await screen.findByRole('dialog', { name: 'Dismiss this item from the error queue?' })
    expect(dialog).toHaveTextContent(
      "YuHaul cannot finish this video's remaining playlist additions, so dismissing it means they will not be sent. The historical record will be preserved.",
    )
    expect(dismiss).not.toHaveBeenCalled()

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(dismiss).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss error: Final Video' }))
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Dismiss this item from the error queue?' })).getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(dismiss).toHaveBeenCalledWith(427))
    expect((await screen.findAllByText('Item dismissed from error queue')).length).toBeGreaterThan(0)
    expect(screen.queryByText('Final Video')).toBeNull()
  })
})

describe('Deferred: Retry refused with video_already_final', () => {
  it('shows the refusal and the closed row in place, sends nothing, and takes it out of the totals', async () => {
    const items = [deferredItem(1), deferredItem(2)]
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    const page = vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(pageOf(items, { total: 120, queue_total: 120, page: { limit: 50, number: 1, total_pages: 3, first_position: 1, last_position: 2, next_after_id: 2, prev_before_id: null } }))
    const retry = vi.spyOn(queueApi, 'retryDeferred').mockRejectedValue(
      refused('accepted', { ...items[0], closed_reason: 'final_outcome', video_final_outcome: 'accepted', status: 'discarded' }),
    )
    renderErrors('?tab=deferred')
    expect(await screen.findByRole('tab', { name: 'Deferred (120)' })).toBeInTheDocument()
    const row = screen.getByText('Video 1').closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: /^Retry now: / }))

    expect(await screen.findByRole('alert')).toHaveTextContent(REFUSAL_ACCEPTED)
    const closedRow = screen.getByText('Video 1').closest('tr')!
    expect(within(closedRow).getByText('Closed')).toBeInTheDocument()
    expect(within(closedRow).getByText(CLOSED_ACCEPTED)).toBeInTheDocument()
    expect(within(closedRow).queryByRole('button')).toBeNull()
    expect(within(closedRow).getByRole('link', { name: 'View video' })).toHaveAttribute('href', '/videos/vid00000001')
    // The closed item is not pending: the tab count and the count line fall by one.
    expect(await screen.findByRole('tab', { name: 'Deferred (119)' })).toBeInTheDocument()
    expect(screen.getByText('Showing 1–2 of 119 items.')).toBeInTheDocument()
    // No toast, no reload, and the other row keeps its controls.
    expect(page).toHaveBeenCalledTimes(1)
    expect(retry).toHaveBeenCalledTimes(1)
    expect(within(screen.getByText('Video 2').closest('tr')!).getByRole('button', { name: /^Retry now: / })).toBeInTheDocument()
  })

  it('shows a closed row the API returns, without counting it again', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(
      pageOf([deferredItem(1, { closed_reason: 'final_outcome', video_final_outcome: 'excluded' }), deferredItem(2)], { total: 7, queue_total: 7 }),
    )
    renderErrors('?tab=deferred')
    const row = (await screen.findByText('Video 1')).closest('tr')!
    expect(within(row).getByText('Closed. This video already has a final outcome: Excluded. It was not processed again.')).toBeInTheDocument()
    expect(within(row).queryByRole('button')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('tab', { name: 'Deferred (7)' })).toBeInTheDocument()
  })
})

describe('Deferred: rejected page or cursor', () => {
  it('shows the stale-page message and a button back to the first page', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    const get = vi.spyOn(deferredPageApi, 'getDeferredPage')
    get.mockRejectedValueOnce(new ApiError(400, 'invalid page', { code: 'invalid_page' }))
    get.mockResolvedValue(pageOf([deferredItem(1)]))
    renderErrors('?tab=deferred&after_id=99&page=2')
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("This page of the deferred queue can't be opened any more. Go to the first page.")
    expect(screen.queryByText('Could not load the deferred queue. Nothing was changed.')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Go to the first page' }))
    expect(await screen.findByText('Video 1')).toBeInTheDocument()
    expect(get.mock.calls.at(-1)![0]).toEqual({ limit: 50, reason: undefined })
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Deferred queue' })).toHaveFocus())
  })

  it('keeps the failed-read state for other errors', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockRejectedValue(new ApiError(500, 'boom'))
    renderErrors('?tab=deferred')
    expect(await screen.findByText('Could not load the deferred queue. Nothing was changed.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Go to the first page' })).toBeNull()
  })
})

describe('Retry accessible names (SC 2.5.3, 2.4.6)', () => {
  it('names the target in Errors rows, with the id when no title is held', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      errorEntry(),
      errorEntry({ id: 2, video_title: undefined, external_video_id: 'abcdefghijk' }),
      errorEntry({ id: 3, video_title: undefined, external_video_id: undefined, video_id: 77 }),
    ])
    renderErrors()
    expect(await screen.findByRole('button', { name: 'Retry Final Video' })).toHaveTextContent(/^Retry$/)
    expect(screen.getByRole('button', { name: 'Retry video abcdefghijk' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry video 77' })).toBeInTheDocument()
  })

  it('names the target in Deferred rows and keeps the visible label first', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(pageOf([deferredItem(1), deferredItem(2, { video_title: undefined })]))
    renderErrors('?tab=deferred')
    const named = await screen.findByRole('button', { name: 'Retry now: Video 1' })
    expect(named).toHaveTextContent('Retry now')
    expect(screen.getByRole('button', { name: 'Retry now: video vid00000002' })).toBeInTheDocument()
  })
})

describe('Refusal focus and Deferred range', () => {
  it('retains a refused last-page row while allowing navigation to the remaining items', async () => {
    const item = deferredItem(51)
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    const get = vi.spyOn(deferredPageApi, 'getDeferredPage')
    get.mockResolvedValueOnce(pageOf([item], { total: 51, queue_total: 51, page: { limit: 50, number: 2, total_pages: 2, first_position: 51, last_position: 51, next_after_id: null, prev_before_id: 51 } }))
    get.mockResolvedValueOnce(pageOf([deferredItem(1)], { total: 50, queue_total: 50, page: { limit: 50, number: 1, total_pages: 1, first_position: 1, last_position: 50, next_after_id: null, prev_before_id: null } }))
    vi.spyOn(queueApi, 'retryDeferred').mockRejectedValue(refused('accepted', { ...item, closed_reason: 'final_outcome', video_final_outcome: 'accepted' }))
    renderErrors('?tab=deferred&page=2')
    fireEvent.click(await screen.findByRole('button', { name: 'Retry now: Video 51' }))
    expect(await screen.findByRole('alert')).toHaveFocus()
    expect(screen.getByText('Showing 0 of 50 items.')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Deferred (50)' })).toBeInTheDocument()
    expect(within(screen.getByText('Video 51').closest('tr')!).getByText('Closed')).toBeInTheDocument()
    expect(screen.getByText('Page 2 (no active items on this page)')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Previous page' }))
    expect(await screen.findByText('Showing 1–50 of 50 items.')).toBeInTheDocument()
    expect(get.mock.calls.at(-1)![0]).toEqual({ limit: 50, reason: undefined, beforeId: 51 })
  })

  it('moves focus to the alert after a Deferred refusal', async () => {
    const items = [deferredItem(1)]
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(pageOf(items))
    vi.spyOn(queueApi, 'retryDeferred').mockRejectedValue(refused('accepted', { ...items[0], closed_reason: 'final_outcome', video_final_outcome: 'accepted' }))
    renderErrors('?tab=deferred')
    fireEvent.click(await screen.findByRole('button', { name: /^Retry now: / }))
    expect(await screen.findByRole('alert')).toHaveFocus()
  })

  it('does not say "Showing 1–1 of 0" when the only item closes', async () => {
    const items = [deferredItem(1)]
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(pageOf(items))
    vi.spyOn(queueApi, 'retryDeferred').mockRejectedValue(refused('accepted', { ...items[0], closed_reason: 'final_outcome', video_final_outcome: 'accepted' }))
    renderErrors('?tab=deferred')
    expect(await screen.findByText('Showing 1–1 of 1 items.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Retry now: / }))
    expect(await screen.findByText('Showing 0 of 0 items.')).toBeInTheDocument()
    expect(screen.queryByText(/1–1 of 0/)).toBeNull()
  })
})
