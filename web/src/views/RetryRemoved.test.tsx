import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ErrorsView } from './ErrorsView'
import { ToastProvider } from '../components/ToastProvider'
import { ApiError } from '../api/core'
import * as queueApi from '../api/queue'
import * as deferredPageApi from '../api/deferred-page'
import type { DeferredItem, DeferredPage, ErrorEntry } from '../types'
import { expectUnfocusedAlert } from '../test/unfocused-alert'

// Admin UI design 3a: a manual Retry whose run ended because the person's own
// Dismiss won answers 409 retry_work_removed. The UI branches on the code; no
// toast of either kind; a persistent role="alert" notice that is never focused;
// the row becomes a removed row and focus moves to its View video link (to the
// list heading when there is no row or link), or the notice sits at the top with
// no focus move when the row is already gone.

const NOTICE =
  'This item was removed while its retry was running. The retry may already have sent playlist additions to YouTube. It will not be retried again.'
const REMOVED_ROW = 'Removed from a queue by you. It will not be retried.'
const PENDING_LINE =
  'A retry of this item is running now. Dismissing removes the item, but the retry may already have sent playlist additions to YouTube.'

function errorEntry(over: Partial<ErrorEntry> = {}): ErrorEntry {
  return {
    id: 427,
    video_id: 12,
    video_title: 'Removed Video',
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

const removed = (entry?: unknown) => new ApiError(409, NOTICE, { code: 'retry_work_removed', entry })

function renderErrors(search = '') {
  return render(
    <MemoryRouter initialEntries={[`/errors${search}`]}>
      <ToastProvider>
        <ErrorsView />
      </ToastProvider>
    </MemoryRouter>,
  )
}

// A promise the test settles by hand: the retry stays pending until then.
function deferredCall<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

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

describe('Errors: Retry ended with retry_work_removed', () => {
  it('a pending Retry reads "Retrying…", is aria-disabled and named, keeps focus, ignores a second press and leaves Remove usable', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry()])
    const call = deferredCall<ErrorEntry>()
    const retry = vi.spyOn(queueApi, 'retryError').mockReturnValue(call.promise)
    renderErrors()
    const row = (await screen.findByText('Removed Video')).closest('tr')!
    const button = within(row).getByRole('button', { name: 'Retry Removed Video' })
    button.focus()
    fireEvent.click(button)

    const pending = await within(row).findByRole('button', { name: 'Retrying Removed Video' })
    expect(pending).toHaveTextContent('Retrying…')
    expect(pending).toHaveAttribute('aria-disabled', 'true')
    expect(pending).not.toBeDisabled()
    expect(document.activeElement).toBe(pending)
    fireEvent.click(pending)
    expect(retry).toHaveBeenCalledTimes(1)
    expect(within(row).getByRole('button', { name: 'Remove error: Removed Video' })).not.toHaveAttribute('aria-disabled')
    call.resolve(errorEntry())
  })

  it('shows the unfocused notice in the row place, a removed row, no toast, focus on its View video link and a lower count', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry(), errorEntry({ id: 428, video_id: 13, video_title: 'Other', external_video_id: 'other' })])
    vi.spyOn(queueApi, 'retryError').mockRejectedValue(removed({ ...errorEntry(), status: 'dismissed', retry_run_id: 77 }))
    renderErrors()
    expect(await screen.findByRole('tab', { name: 'Errors (2)' })).toBeInTheDocument()
    const row = screen.getByText('Removed Video').closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'Retry Removed Video' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(NOTICE)
    expect(within(alert).getByRole('link', { name: 'View run 77' })).toHaveAttribute('href', '/runs/77')
    expectUnfocusedAlert(alert)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'View video: Removed Video' })).toHaveFocus()
    // No toast of either kind.
    expect(screen.queryByText('Error queued for retry')).toBeNull()
    expect(screen.queryByText(/Failed to retry/)).toBeNull()
    expect(screen.queryByText(/queued for retry/i)).toBeNull()
    // A removed row: badge, text, original reason, no actions, link to the video.
    const removedRow = screen.getByText('Removed Video').closest('tr')!
    expect(within(removedRow).getByText('Removed')).toBeInTheDocument()
    expect(within(removedRow).getByText(REMOVED_ROW)).toBeInTheDocument()
    expect(within(removedRow).queryByRole('button')).toBeNull()
    expect(within(removedRow).getByRole('link', { name: 'View video: Removed Video' })).toHaveAttribute('href', '/videos/dQw4w9WgXcQ')
    expect(within(removedRow).getByText('YouTube returned an error.')).toBeInTheDocument()
    // Not counted as active: the tab count falls by one.
    expect(screen.getByRole('tab', { name: 'Errors (1)' })).toBeInTheDocument()
  })

  it('with no retry run in the entry there is no run link and no sentence about checking a run', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry()])
    vi.spyOn(queueApi, 'retryError').mockRejectedValue(removed({ ...errorEntry(), status: 'dismissed' }))
    renderErrors()
    fireEvent.click(within((await screen.findByText('Removed Video')).closest('tr')!).getByRole('button', { name: 'Retry Removed Video' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(NOTICE)
    expect(within(alert).queryByRole('link')).toBeNull()
    expect(alert.textContent).not.toMatch(/check|nothing/i)
    expectUnfocusedAlert(alert)
  })

  it('without a usable entry takes the row out, keeps the unfocused notice, focuses the heading and counts it out', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry()])
    vi.spyOn(queueApi, 'retryError').mockRejectedValue(removed(undefined))
    renderErrors()
    fireEvent.click(within((await screen.findByText('Removed Video')).closest('tr')!).getByRole('button', { name: 'Retry Removed Video' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(`Removed Video: ${NOTICE}`)
    expectUnfocusedAlert(alert)
    expect(screen.getByRole('heading', { name: 'Errors & Deferred' })).toHaveFocus()
    expect(screen.queryByRole('link', { name: 'View video: Removed Video' })).toBeNull()
    expect(screen.getByRole('tab', { name: 'Errors (0)' })).toBeInTheDocument()
  })

  it('when the row is already gone it does not return, adds no second toast and does not move focus', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry(), errorEntry({ id: 428, video_id: 13, video_title: 'Other', external_video_id: 'other' })])
    const call = deferredCall<ErrorEntry>()
    vi.spyOn(queueApi, 'retryError').mockReturnValue(call.promise)
    vi.spyOn(queueApi, 'dismissError').mockResolvedValue(errorEntry({ status: 'dismissed' }))
    renderErrors()
    const row = (await screen.findByText('Removed Video')).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'Retry Removed Video' }))
    await within(row).findByRole('button', { name: 'Retrying Removed Video' })
    // Remove stays usable during the pending Retry.
    fireEvent.click(within(row).getByRole('button', { name: 'Remove error: Removed Video' }))
    expect((await screen.findAllByText('Error dismissed')).length).toBeGreaterThan(0)
    await waitFor(() => expect(screen.queryByText('Removed Video')).toBeNull())
    const focusBefore = document.activeElement

    call.reject(removed({ ...errorEntry(), status: 'dismissed', retry_run_id: 5 }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(`Removed Video: ${NOTICE}`)
    expect(within(alert).getByRole('link', { name: 'View run 5' })).toBeInTheDocument()
    expect(document.activeElement).toBe(focusBefore)
    expect(document.activeElement).not.toBe(alert)
    expect(screen.queryByText('Removed Video', { selector: 'td' })).toBeNull()
    // One dismissal toast only (it renders once visibly and once for screen readers).
    expect(screen.getAllByText('Error dismissed')).toHaveLength(2)
    expect(screen.queryByText(/queued for retry|Failed to retry/)).toBeNull()
  })

  it('keeps the other 409s and failures as they were', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([errorEntry()])
    vi.spyOn(queueApi, 'retryError').mockRejectedValue(new ApiError(500, 'failed to retry error entry'))
    renderErrors()
    fireEvent.click(within((await screen.findByText('Removed Video')).closest('tr')!).getByRole('button', { name: 'Retry Removed Video' }))
    expect((await screen.findAllByText('failed to retry error entry')).length).toBeGreaterThan(0)
    expect(screen.queryByText(NOTICE)).toBeNull()
  })
})

describe('Deferred: Retry ended with retry_work_removed', () => {
  async function openWith(items: DeferredItem[], total = items.length) {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(pageOf(items, { total, queue_total: total }))
    renderErrors('?tab=deferred')
    await screen.findByRole('heading', { name: 'Deferred queue' })
  }

  it('shows the notice, a removed row, no toast, and takes the item out of the totals', async () => {
    const items = [deferredItem(1), deferredItem(2)]
    await openWith(items, 120)
    vi.spyOn(queueApi, 'retryDeferred').mockRejectedValue(removed({ ...items[0], status: 'discarded', retry_run_id: 31 }))
    expect(await screen.findByRole('tab', { name: 'Deferred (120)' })).toBeInTheDocument()
    fireEvent.click(within(screen.getByText('Video 1').closest('tr')!).getByRole('button', { name: /^Retry now: / }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(NOTICE)
    expect(within(alert).getByRole('link', { name: 'View run 31' })).toHaveAttribute('href', '/runs/31')
    expectUnfocusedAlert(alert)
    expect(screen.getByRole('link', { name: 'View video: Video 1' })).toHaveFocus()
    const row = screen.getByText('Video 1').closest('tr')!
    expect(within(row).getByText('Removed')).toBeInTheDocument()
    expect(within(row).getByText(REMOVED_ROW)).toBeInTheDocument()
    expect(within(row).queryByRole('button')).toBeNull()
    expect(within(row).getByRole('link', { name: 'View video: Video 1' })).toBeInTheDocument()
    expect(await screen.findByRole('tab', { name: 'Deferred (119)' })).toBeInTheDocument()
    expect(screen.getByText('Showing 1–1 of 119 items.')).toBeInTheDocument()
    expect(screen.queryByText(/queued for retry|Failed to retry/)).toBeNull()
    expect(within(screen.getByText('Video 2').closest('tr')!).getByRole('button', { name: /^Retry now: / })).toBeInTheDocument()
  })

  it('without a usable entry takes the row out, keeps the unfocused notice and focuses the list heading', async () => {
    const items = [deferredItem(1), deferredItem(2)]
    await openWith(items, 2)
    vi.spyOn(queueApi, 'retryDeferred').mockRejectedValue(removed(undefined))
    fireEvent.click(within(screen.getByText('Video 1').closest('tr')!).getByRole('button', { name: /^Retry now: / }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(`Video 1: ${NOTICE}`)
    expectUnfocusedAlert(alert)
    expect(screen.getByRole('heading', { name: 'Deferred queue' })).toHaveFocus()
    expect(screen.queryByText('Video 1', { selector: 'td' })).toBeNull()
  })

  it('pending Retry: "Retrying…", aria-disabled; Dismiss then asks with the pending-only first line, and the notice leads with the target once the row is gone', async () => {
    const items = [deferredItem(1), deferredItem(2)]
    await openWith(items, 2)
    const call = deferredCall<DeferredItem>()
    vi.spyOn(queueApi, 'retryDeferred').mockReturnValue(call.promise)
    const discard = vi.spyOn(queueApi, 'discardDeferred').mockResolvedValue({ ...items[0], status: 'discarded' })
    const row = screen.getByText('Video 1').closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: /^Retry now: Video 1/ }))
    const pending = await within(row).findByRole('button', { name: 'Retrying Video 1' })
    expect(pending).toHaveTextContent('Retrying…')
    expect(pending).toHaveAttribute('aria-disabled', 'true')

    // Another row's Dismiss has no pending line and no dialog (not handed over).
    fireEvent.click(within(row).getByRole('button', { name: 'Dismiss deferred item: Video 1' }))
    const dialog = await screen.findByRole('dialog', { name: 'Dismiss this deferred item?', hidden: true })
    expect(within(dialog).getByText(PENDING_LINE)).toBeInTheDocument()
    expect(within(dialog).getByText(/It will not be retried, but the historical record will be preserved\./)).toBeInTheDocument()
    // The pending line is the first line.
    expect(dialog.querySelector('p')?.textContent).toBe(PENDING_LINE)

    // Confirm: the row goes (reload), then the retry ends removed.
    vi.mocked(deferredPageApi.getDeferredPage).mockResolvedValue(pageOf([items[1]]))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(discard).toHaveBeenCalledWith(1))
    await waitFor(() => expect(screen.queryByText('Video 1')).toBeNull())
    const focusBefore = document.activeElement
    call.reject(removed({ ...items[0], status: 'discarded' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(`Video 1: ${NOTICE}`)
    expect(document.activeElement).toBe(focusBefore)
    expect(document.activeElement).not.toBe(alert)
  })

  it('shows no pending line when that row has no retry running', async () => {
    const items = [deferredItem(1, { deferral_reason: 'stopped_by_user' })]
    await openWith(items)
    fireEvent.click(within(screen.getByText('Video 1').closest('tr')!).getByRole('button', { name: /^Dismiss deferred item/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Dismiss this deferred item?', hidden: true })
    expect(within(dialog).queryByText(PENDING_LINE)).toBeNull()
    expect(within(dialog).getByText('It will not be retried, but the historical record will be preserved.')).toBeInTheDocument()
  })
})
