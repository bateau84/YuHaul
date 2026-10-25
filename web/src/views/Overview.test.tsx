import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Overview } from './Overview'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'

function renderWithToast(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <ToastProvider>{ui}</ToastProvider>
    </MemoryRouter>
  )
}

describe('Overview', () => {
  it('renders empty state when last_run is null', async () => {
    vi.spyOn(mock, 'getOverview').mockResolvedValueOnce({
      last_run: null,
      runs_today: 0,
      quota: {
        daily_limit: 10000,
        consumed_today: 0,
        remaining: 10000,
        reset_at: '2026-09-09T00:00:00Z',
      },
      error_count: 0,
      deferred_count: 0,
    })

    renderWithToast(<Overview />)
    await waitFor(() => expect(screen.getByText('No runs yet.')).toBeInTheDocument())

    expect(screen.getByText('Discovered')).toBeInTheDocument()
    expect(screen.getAllByText('0')[0]).toBeInTheDocument()
  })

  it('shows an accessible fetch-error state when the overview fetch fails', async () => {
    vi.spyOn(mock, 'getOverview').mockRejectedValueOnce(new Error('Network error'))

    renderWithToast(<Overview />)

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent('Failed to load overview. Check your connection and try again.')
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    await waitFor(() => expect(retry).toHaveFocus())

    // The toolbar stays visible but disabled until a retry succeeds.
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Dry Run/i })).toBeDisabled()

    // A failed fetch never renders the dashboard empty state.
    expect(screen.queryByText(/No runs yet/)).not.toBeInTheDocument()
  })

  it('recovers to the dashboard after a successful retry', async () => {
    vi.spyOn(mock, 'getOverview').mockRejectedValueOnce(new Error('Network error'))

    renderWithToast(<Overview />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText('Quota today')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeEnabled()
  })

  it('stays in the fetch-error state and re-enables retry when retry also fails', async () => {
    vi.spyOn(mock, 'getOverview')
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce(new Error('still down'))

    renderWithToast(<Overview />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/Retry failed. Please try again./i))
    expect(screen.getByText('Failed to load overview. Check your connection and try again.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled())
    expect(screen.queryByText(/No runs yet/)).not.toBeInTheDocument()
  })
})
