import { beforeEach, describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ErrorsView } from './ErrorsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import * as queueApi from '../api/queue'
import type { ErrorEntry } from '../types'

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <ToastProvider>{ui}</ToastProvider>
    </MemoryRouter>
  )
}

describe('ErrorsView', () => {
  beforeEach(() => mock.resetMockDeferred())

  it('renders the server queue contract and sends the returned id to retry and remove', async () => {
    const entry = {
      id: 427,
      video_id: 12,
      external_video_id: 'yt-safe-id',
      run_id: 9,
      error_category: 'api_error' as const,
      error_message: 'quotaExceeded',
      status: 'active' as const,
      created_at: '2026-10-01T00:00:00Z',
      retry_count: 0,
    }
    vi.spyOn(queueApi, 'listErrors').mockResolvedValueOnce([entry])
    const retry = vi.spyOn(queueApi, 'retryError').mockResolvedValue(entry)
    const dismiss = vi.spyOn(queueApi, 'dismissError').mockResolvedValue(entry)

    renderWithProviders(<ErrorsView />)
    const row = await waitFor(() => screen.getByText('yt-safe-id').closest('tr'))
    expect(within(row!).getByText('api_error')).toBeInTheDocument()
    expect(within(row!).getByText('quotaExceeded')).toBeInTheDocument()
    fireEvent.click(within(row!).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(retry).toHaveBeenCalledWith(427))

    // Reload the same real-shaped row to exercise the independent dismiss action.
    vi.spyOn(queueApi, 'listErrors').mockResolvedValueOnce([entry])
    renderWithProviders(<ErrorsView />)
    const secondRow = await waitFor(() => screen.getByText('yt-safe-id').closest('tr'))
    fireEvent.click(within(secondRow!).getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(dismiss).toHaveBeenCalledWith(427))
  })

  it('renders errors and deferred queues with retry/dismiss actions', async () => {
    renderWithProviders(<ErrorsView />)

    await waitFor(() => expect(screen.getByRole('heading', { name: /Errors & Deferred/i })).toBeInTheDocument())

    // Default errors tab should show the mock error.
    await waitFor(() => expect(screen.getByText('Ambient Study Session')).toBeInTheDocument())
    expect(screen.getByText('OAuth token expired during playlist insertion.')).toBeInTheDocument()

    const errorRow = screen.getByText('Ambient Study Session').closest('tr')
    expect(errorRow).not.toBeNull()
    expect(within(errorRow!).getByRole('button', { name: /Retry/i })).toBeInTheDocument()
    expect(within(errorRow!).getByRole('button', { name: /Remove/i })).toBeInTheDocument()

    // Switch to deferred tab.
    fireEvent.click(screen.getByRole('tab', { name: /Deferred/i }))
    await waitFor(() => expect(screen.getByText('Pop Hit 2026')).toBeInTheDocument())
    const deferredRow = screen.getByText('Pop Hit 2026').closest('tr')
    expect(within(deferredRow!).getByText('Quota exhausted')).toBeInTheDocument()
    expect(deferredRow).not.toBeNull()
    expect(within(deferredRow!).getByRole('button', { name: /Retry now/i })).toBeInTheDocument()
    expect(within(deferredRow!).getByRole('button', { name: 'Dismiss deferred item: Pop Hit 2026' })).toBeInTheDocument()
    expect(within(deferredRow!).queryByRole('button', { name: /Discard/i })).not.toBeInTheDocument()
  })

  it('labels handed-over deferred items and falls back to the video id for the dismiss name', async () => {
    const base = { video_id: 5, status: 'pending' as const, created_at: '2026-10-01T00:00:00Z', retry_count: 0 }
    vi.spyOn(queueApi, 'listErrors').mockResolvedValueOnce([])
    mock.resetMockDeferred([
      { ...base, id: 1, video_title: 'Stopped One', run_id: 41, deferral_reason: 'stopped_by_user' },
      { ...base, id: 2, video_title: 'Discarded One', run_id: 42, deferral_reason: 'discarded_by_user' },
      { ...base, id: 3, run_id: 43, deferral_reason: 'stopped_by_user', deferred_reason_unavailable: true },
    ])
    renderWithProviders(<ErrorsView />)
    await waitFor(() => expect(screen.getByRole('tab', { name: /Deferred/i })).toBeEnabled())
    fireEvent.click(screen.getByRole('tab', { name: /Deferred/i }))

    const stopped = (await screen.findByText('Stopped One')).closest('tr')!
    expect(within(stopped).getByText('Waiting for the next run. Run 41 was stopped by user.')).toBeInTheDocument()
    expect(within(stopped).getByText('Eligible for the next run')).toBeInTheDocument()
    const discarded = screen.getByText('Discarded One').closest('tr')!
    expect(within(discarded).getByText('Waiting for the next run. Run 42 was discarded.')).toBeInTheDocument()
    expect(screen.getByText('Waiting for the next run.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Dismiss deferred item: 5' })).toBeInTheDocument()
  })

  it('opens the Deferred tab from ?tab=deferred', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValueOnce([])
    mock.resetMockDeferred([
      { id: 1, video_id: 5, video_title: 'Waiting One', status: 'pending', created_at: '2026-10-01T00:00:00Z', retry_count: 0, run_id: 41, deferral_reason: 'stopped_by_user' },
    ])
    render(
      <MemoryRouter initialEntries={['/errors?tab=deferred']}>
        <ToastProvider><ErrorsView /></ToastProvider>
      </MemoryRouter>,
    )
    expect(await screen.findByText('Waiting One')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /Deferred/ })).toHaveAttribute('aria-selected', 'true')
  })

  it('warns that a handed-over item will not be retried and dismisses only after confirming', async () => {
    const show = HTMLDialogElement.prototype.showModal
    const close = HTMLDialogElement.prototype.close
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
    try {
      vi.spyOn(queueApi, 'listErrors').mockResolvedValueOnce([])
      mock.resetMockDeferred([
        { id: 7, video_id: 5, video_title: 'Handed One', status: 'pending', created_at: '2026-10-01T00:00:00Z', retry_count: 0, run_id: 41, deferral_reason: 'discarded_by_user' },
      ])
      const discard = vi.spyOn(queueApi, 'discardDeferred').mockResolvedValue(undefined as never)
      render(
        <MemoryRouter initialEntries={['/errors?tab=deferred']}>
          <ToastProvider><ErrorsView /></ToastProvider>
        </MemoryRouter>,
      )
      fireEvent.click(await screen.findByRole('button', { name: 'Dismiss deferred item: Handed One' }))
      const dialog = screen.getByRole('dialog', { name: 'Dismiss this deferred item?' })
      expect(dialog).toHaveTextContent('It will not be retried, but the historical record will be preserved.')
      expect(discard).not.toHaveBeenCalled()
      fireEvent.click(within(dialog).getByRole('button', { name: 'Dismiss' }))
      await waitFor(() => expect(discard).toHaveBeenCalledWith(7))
    } finally {
      HTMLDialogElement.prototype.showModal = show
      HTMLDialogElement.prototype.close = close
    }
  })

  it('dismisses an error and removes it from the list', async () => {
    renderWithProviders(<ErrorsView />)

    await waitFor(() => expect(screen.getByText('Ambient Study Session')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Remove/i }))

    await waitFor(() => expect(screen.queryByText('Ambient Study Session')).not.toBeInTheDocument())
    expect(screen.getByText(/No errors/i)).toBeInTheDocument()
  })

  it('shows an accessible fetch-error state when the queue fetch fails', async () => {
    vi.spyOn(mock, 'listErrors').mockRejectedValueOnce(new Error('Queue unavailable'))

    renderWithProviders(<ErrorsView />)

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent('Failed to load queue. Check your connection and try again.')
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    await waitFor(() => expect(retry).toHaveFocus())

    // The tab controls stay visible but disabled until a retry succeeds.
    expect(screen.getByRole('tab', { name: /^Errors$/ })).toBeDisabled()
    expect(screen.getByRole('tab', { name: /^Deferred$/ })).toBeDisabled()

    // A failed fetch never renders either empty state.
    expect(screen.queryByText(/No errors\. All processing/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/No deferred work/i)).not.toBeInTheDocument()
  })

  it('renders the empty states only when the fetch succeeds with empty lists', async () => {
    vi.spyOn(mock, 'listErrors').mockResolvedValueOnce([])
    mock.resetMockDeferred([])

    renderWithProviders(<ErrorsView />)

    await waitFor(() =>
      expect(screen.getByText('No errors. All processing is running smoothly.')).toBeInTheDocument()
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByText('Errors (0)')).toBeInTheDocument()
  })

  it('recovers to the queue lists after a successful retry', async () => {
    // The mock queue is mutated by earlier tests (dismiss marks an entry
    // inactive), so the retry result is supplied explicitly.
    const recoveredError: ErrorEntry = {
      id: 99,
      video_id: 99,
      video_title: 'Recovered Video',
      run_id: 1,
      error_category: 'runtime',
      error_message: 'boom',
      status: 'active',
      created_at: '2026-09-08T06:00:00Z',
      retry_count: 0,
    }
    vi.spyOn(mock, 'listErrors')
      .mockRejectedValueOnce(new Error('Queue unavailable'))
      .mockResolvedValueOnce([recoveredError])
    mock.resetMockDeferred([])

    renderWithProviders(<ErrorsView />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText('Recovered Video')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /^Errors \(1\)$/ })).toBeEnabled()
  })

  it('stays in the fetch-error state and re-enables retry when retry also fails', async () => {
    vi.spyOn(mock, 'listErrors')
      .mockRejectedValueOnce(new Error('Queue unavailable'))
      .mockRejectedValueOnce(new Error('still down'))

    renderWithProviders(<ErrorsView />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/Retry failed. Please try again./i))
    // The error region persists (disambiguated from the error toast, which is also role=alert).
    expect(screen.getByText('Failed to load queue. Check your connection and try again.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled())
    expect(screen.queryByText(/No errors\. All processing/i)).not.toBeInTheDocument()
  })
})
