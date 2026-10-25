import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ErrorsView } from './ErrorsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { ErrorEntry } from '../types'

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <ToastProvider>{ui}</ToastProvider>
    </MemoryRouter>
  )
}

describe('ErrorsView', () => {
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
    expect(screen.getByText('quota_exhausted')).toBeInTheDocument()

    const deferredRow = screen.getByText('Pop Hit 2026').closest('tr')
    expect(deferredRow).not.toBeNull()
    expect(within(deferredRow!).getByRole('button', { name: /Retry now/i })).toBeInTheDocument()
    expect(within(deferredRow!).getByRole('button', { name: /Discard/i })).toBeInTheDocument()
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
    vi.spyOn(mock, 'listDeferred').mockResolvedValueOnce([])

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
    vi.spyOn(mock, 'listDeferred').mockResolvedValueOnce([])

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
