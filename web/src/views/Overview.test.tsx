import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { Overview } from './Overview'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'

function renderWithToast(ui: React.ReactElement) {
  return render(<ToastProvider>{ui}</ToastProvider>)
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

  it('shows error state when overview fetch fails', async () => {
    vi.spyOn(mock, 'getOverview').mockRejectedValueOnce(new Error('Network error'))

    renderWithToast(<Overview />)
    await waitFor(() => expect(screen.getByText(/Network error/i)).toBeInTheDocument())

    expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()
  })
})
